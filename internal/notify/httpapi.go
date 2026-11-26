// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package notify

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

func writeError(w http.ResponseWriter, status int, message string, logger *slog.Logger) {
	errID := idgen.New()
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		logger.Warn("notify http error", "errorId", errID, "httpStatus", status, "message", message)
	case status >= 400 && status < 500:
		logger.Info("notify http error", "errorId", errID, "httpStatus", status, "message", message)
	default:
		logger.Error("notify http error", "errorId", errID, "httpStatus", status, "message", message)
	}
	writeJSON(w, status, map[string]any{"message": message, "errorId": errID})
}

func writeServerError(w http.ResponseWriter, message string, cause error, logger *slog.Logger) {
	writeServerErrorStatus(w, http.StatusInternalServerError, message, cause, logger)
}

func writeServerErrorStatus(w http.ResponseWriter, status int, message string, cause error, logger *slog.Logger) {
	errID := idgen.New()
	logger.Error("notify http server error", "errorId", errID, "httpStatus", status, "message", message, "error", cause)
	writeJSON(w, status, map[string]any{"message": message, "errorId": errID})
}

func decodeJSON(r *http.Request, v any) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		if errors.Is(err, io.EOF) {
			return errors.New("request body is empty")
		}
		return err
	}
	return nil
}

func validatePathUUID(id string) error {
	if id == "" {
		return errors.New("path id is required")
	}
	if _, err := uuid.Parse(id); err != nil {
		return errors.New("path id must be a valid UUID")
	}
	return nil
}

func parsePagination(r *http.Request) (pageNumber, pageSize int) {
	pageNumber = atoiDefault(r.URL.Query().Get("pageNumber"), 1)
	pageSize = atoiDefault(r.URL.Query().Get("pageSize"), 20)
	if pageNumber < 1 {
		pageNumber = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}
	return pageNumber, pageSize
}

func resolveUserID(w http.ResponseWriter, r *http.Request, othersScope string) (string, bool) {
	return resolveUserIDWith(w, r, othersScope, slog.Default())
}

func resolveUserIDWith(w http.ResponseWriter, r *http.Request, othersScope string, logger *slog.Logger) (string, bool) {
	id, ok := auth.SubjectFromContext(r.Context())
	if !ok || id == nil || id.UserID() == "" {
		writeError(w, http.StatusUnauthorized, "authentication required", logger)
		return "", false
	}
	userID := callerKey(id)
	if q := canonicalUserKey(r.URL.Query().Get("userId")); q != "" && !isCaller(id, q) {
		if !id.HasScope(othersScope) {
			writeError(w, http.StatusForbidden, "not permitted to act on behalf of another user", logger)
			return "", false
		}
		userID = q
	}
	return userID, true
}

func (n *NotifyCore) targetUser(w http.ResponseWriter, r *http.Request, othersScope string) (string, bool) {
	key, ok := resolveUserIDWith(w, r, othersScope, n.logger)
	if !ok || n.db == nil {
		return key, ok
	}
	if id, _ := auth.SubjectFromContext(r.Context()); key == callerKey(id) {
		return key, true
	}
	platformUserID, err := n.db.PlatformUserIDFor(r.Context(), key)
	if err != nil {
		writeServerError(w, "failed to resolve the target user", err, n.logger)
		return "", false
	}
	return cmp.Or(platformUserID, key), true
}

func (n *NotifyCore) adoptLoginRows(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, ok := auth.SubjectFromContext(r.Context())
		if ok && id != nil && n.db != nil {
			if from, to := id.UserID(), callerKey(id); from != "" && from != to && !isUUID(from) {
				n.adopt(r.Context(), from, to)
			}
		}
		next.ServeHTTP(w, r)
	})
}

func isUUID(s string) bool {
	_, err := uuid.Parse(s)
	return err == nil
}

const (
	adoptionRecheck = 5 * time.Minute
	adoptionRetry   = time.Minute
)

func (n *NotifyCore) adopt(ctx context.Context, from, to string) {
	now := n.adoptions.now()
	if !n.adoptions.due(from, to, now) {
		return
	}
	taken, err := n.db.IsAliasTarget(ctx, from)
	if err == nil && taken {
		n.logger.Warn("notify: refused to adopt rows: the login id is a users.id another login maps to", "platformUserId", to)
		n.adoptions.remember(from, to, now, adoptionRecheck)
		return
	}
	if err == nil {
		err = n.db.RecordAlias(ctx, from, to)
	}
	var pending bool
	if err == nil {
		pending, err = n.db.HasRowsFor(ctx, from)
	}
	if err == nil && pending {
		err = n.db.AdoptUser(ctx, from, to)
		if err == nil {
			n.logger.Debug("notify: moved login-keyed rows under the platform user id", "platformUserId", to)
		}
	}
	switch {
	case err == nil:
		n.adoptions.remember(from, to, now, adoptionRecheck)
	case errors.Is(err, context.Canceled):
	default:
		n.adoptions.remember(from, to, now, adoptionRetry)
		n.logger.Warn("notify: moving login-keyed rows failed; retrying later", "platformUserId", to, "error", err)
	}
}

type adoptions struct {
	clock   func() time.Time
	mu      sync.Mutex
	entries map[string]adoption
}

type adoption struct {
	platformUserID string
	until          time.Time
}

const maxAdoptions = 4096

func (a *adoptions) now() time.Time {
	if a.clock == nil {
		return time.Now()
	}
	return a.clock()
}

func (a *adoptions) due(login, platformUserID string, now time.Time) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	e, ok := a.entries[login]
	return !ok || e.platformUserID != platformUserID || now.After(e.until)
}

func (a *adoptions) remember(login, platformUserID string, now time.Time, ttl time.Duration) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.entries == nil {
		a.entries = make(map[string]adoption)
	}
	if len(a.entries) >= maxAdoptions {
		maps.DeleteFunc(a.entries, func(_ string, e adoption) bool { return now.After(e.until) })
	}
	a.entries[login] = adoption{platformUserID: platformUserID, until: now.Add(ttl)}
}

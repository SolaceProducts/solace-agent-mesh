// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/SolaceProducts/solace-agent-mesh/internal/apicontract"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

type sessionFailure struct {
	status  int
	message string
	errorID string
}

func failSession(status int, message string) *sessionFailure {
	return &sessionFailure{status: status, message: message}
}

func logFailure(logger *slog.Logger, status int, message, logMsg string, cause error, attrs ...any) *sessionFailure {
	errID := idgen.New()
	logger.Error(logMsg, append(attrs, "errorId", errID, "error", cause)...)
	return &sessionFailure{status: status, message: message, errorID: errID}
}

func (h *sessionHandler) loggedFailure(status int, message, logMsg string, cause error, attrs ...any) *sessionFailure {
	return logFailure(h.logger, status, message, logMsg, cause, attrs...)
}

func (h *chatTaskHandler) loggedFailure(status int, message, logMsg string, cause error, attrs ...any) *sessionFailure {
	return logFailure(h.logger, status, message, logMsg, cause, attrs...)
}

func (f *sessionFailure) write(w http.ResponseWriter) {
	if f.errorID != "" {
		writeErrorWithID(w, f.status, f.message, f.errorID)
		return
	}
	writeError(w, f.status, f.message)
}

func (f *sessionFailure) asError() error {
	if f == nil {
		return nil
	}
	return &apicontract.CallerError{Status: f.status, Message: f.message, ErrorID: f.errorID}
}

func isUnnamedSession(sessionID string) bool {
	return sessionID == "" || sessionID == "null" || sessionID == "undefined"
}

func ownedSession(ctx context.Context, repo *store.SessionRepo, logger *slog.Logger, sessionID, userID string) *store.Session {
	if isUnnamedSession(sessionID) {
		return nil
	}
	session, err := repo.GetByID(ctx, sessionID)
	if err != nil {
		if logger != nil {
			logger.Warn("ownership preflight: get session", "sessionID", sessionID, "error", err)
		}
		return nil
	}
	if session == nil || session.UserID != userID {
		return nil
	}
	return session
}

func requireOwnedSession(w http.ResponseWriter, r *http.Request, repo *store.SessionRepo, logger *slog.Logger, sessionID, userID string) *store.Session {
	session := ownedSession(r.Context(), repo, logger, sessionID, userID)
	if session == nil {
		writeError(w, http.StatusNotFound, sessionNotFoundMsg)
	}
	return session
}

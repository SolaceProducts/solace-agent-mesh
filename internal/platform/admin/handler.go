// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/apicontract"
	"github.com/SolaceProducts/solace-agent-mesh/internal/budget"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	"github.com/google/uuid"
)

func writeJSON(w http.ResponseWriter, status int, v any) {
	apicontract.WriteJSON(w, status, v)
}

type errorEnvelope = apicontract.ErrorBody

func writeError(w http.ResponseWriter, status int, message string) {
	writeErrorWithDetails(w, status, message, nil)
}

func writeErrorWithDetails(w http.ResponseWriter, status int, message string, details map[string][]string) {
	writeErrorWithCause(w, status, message, details, nil)
}

const msgInsufficientPermissions = "Insufficient permissions"

var errRetryable = apicontract.ErrRetryable

func writeErrorWithCause(w http.ResponseWriter, status int, message string, details map[string][]string, cause error) {
	apicontract.WriteErrorWithCause(w, status, message, details, cause)
}

func writeErrorLoggedAs(w http.ResponseWriter, status int, message, logMessage string, details map[string][]string, cause error) {
	apicontract.WriteErrorLoggedAs(w, status, message, logMessage, details, cause)
}

func writeErrorWithLoggedID(w http.ResponseWriter, status int, message string, details map[string][]string, errorID string) {
	apicontract.WriteErrorWithLoggedID(w, status, message, details, errorID)
}

func logError(logger *slog.Logger, msg string, args ...any) {
	if logger != nil {
		logger.Error(msg, args...)
	}
}

func logWarn(logger *slog.Logger, msg string, args ...any) {
	if logger != nil {
		logger.Warn(msg, args...)
	}
}

func handleServiceError(w http.ResponseWriter, err error) {
	if resetsAt, ok := budget.ResetsAt(err); ok {
		budget.SetRetryAfter(w, resetsAt, time.Now())
		writeError(w, http.StatusTooManyRequests, entrypoint.BudgetRefusalText(err))
	} else if validErr, ok := errors.AsType[*ValidationError](err); ok {
		writeErrorWithCause(w, http.StatusUnprocessableEntity, v1ValidationMessage(err, validErr), validErr.Details, loggableCause(err))
	} else if notFoundErr, ok := errors.AsType[*NotFoundError](err); ok {
		writeError(w, http.StatusNotFound, notFoundErr.Error())
	} else if conflictErr, ok := errors.AsType[*ConflictError](err); ok {
		writeErrorLoggedAs(w, http.StatusConflict, conflictErr.Message, conflictErr.Error(), nil, nil)
	} else if forbiddenErr, ok := errors.AsType[*ForbiddenError](err); ok {
		writeError(w, http.StatusForbidden, forbiddenErr.Message)
	} else if extSvcErr, ok := errors.AsType[*ExternalServiceError](err); ok {
		writeErrorWithCause(w, http.StatusBadGateway, extSvcErr.Message, nil, loggableCause(extSvcErr.Cause))
	} else if unavailErr, ok := errors.AsType[*UnavailableError](err); ok {
		writeError(w, http.StatusServiceUnavailable, unavailErr.Message)
	} else {
		writeError(w, http.StatusInternalServerError, "internal error")
	}
}

func handleServiceErrorWithCause(w http.ResponseWriter, err error) {
	if isServiceError(err) {
		handleServiceError(w, err)
		return
	}
	writeErrorWithCause(w, http.StatusInternalServerError, "internal error", nil, err)
}

func isServiceError(err error) bool {
	if errors.Is(err, budget.ErrExhausted) {
		return true
	}
	if _, ok := errors.AsType[*ValidationError](err); ok {
		return true
	}
	if _, ok := errors.AsType[*NotFoundError](err); ok {
		return true
	}
	if _, ok := errors.AsType[*ConflictError](err); ok {
		return true
	}
	if _, ok := errors.AsType[*ForbiddenError](err); ok {
		return true
	}
	if _, ok := errors.AsType[*ExternalServiceError](err); ok {
		return true
	}
	if _, ok := errors.AsType[*UnavailableError](err); ok {
		return true
	}
	return false
}

func userIDFromRequest(r *http.Request) string {
	return userIDFromContext(r.Context())
}

func userIDFromContext(ctx context.Context) string {
	if identity, ok := auth.SubjectFromContext(ctx); ok {
		if identity.Email() != "" {
			return identity.Email()
		}
		if identity.UserID() != "" {
			return identity.UserID()
		}
	}
	if u := platpkg.UserFromContext(ctx); u != nil {
		return u.Email
	}
	return "unknown"
}

func ScopesFromContext(ctx context.Context) []string {
	if identity, ok := auth.SubjectFromContext(ctx); ok && identity != nil {
		return identity.Scopes()
	}
	return nil
}

const maxPageSize = 100

func parsePagination(r *http.Request) (int, int, error) {
	pageNumber := 1
	if raw := r.URL.Query().Get("pageNumber"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			return 0, 0, &ValidationError{Message: "pageNumber must be a positive integer"}
		}
		pageNumber = n
	}
	pageSize := 20
	if raw := r.URL.Query().Get("pageSize"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > maxPageSize {
			return 0, 0, &ValidationError{Message: "pageSize must be between 1 and 100"}
		}
		pageSize = n
	}
	return pageNumber, pageSize, nil
}

func sortAndPage[T any](
	data []T,
	pageNumber, pageSize int,
	sortField, sortDir, defaultField string,
	comparators map[string]func(a, b T) int,
) (PaginatedResponse[T], error) {
	field := sortField
	if field == "" {
		field = defaultField
	}
	compare, ok := comparators[field]
	if !ok {
		return PaginatedResponse[T]{}, &ValidationError{Message: fmt.Sprintf("Unknown sort field %q", sortField)}
	}
	if strings.EqualFold(sortDir, "desc") {
		asc := compare
		compare = func(a, b T) int { return -asc(a, b) }
	}
	slices.SortStableFunc(data, compare)

	total := len(data)
	start := total
	if pageSize > 0 {
		if lastPage := (total + pageSize - 1) / pageSize; pageNumber <= lastPage {
			start = (pageNumber - 1) * pageSize
		}
	}
	end := min(start+pageSize, total)
	page := make([]T, end-start)
	copy(page, data[start:end])
	return PaginatedResponse[T]{
		Data: page,
		Meta: NewPaginationMeta(pageNumber, pageSize, total),
	}, nil
}

func filterInPlace[T any](data []T, keep func(T) bool) []T {
	out := data[:0]
	for _, v := range data {
		if keep(v) {
			out = append(out, v)
		}
	}
	return out
}

func containsFold(s, substr string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(substr))
}

func compareFold(a, b string) int {
	return cmp.Compare(strings.ToLower(a), strings.ToLower(b))
}

func parseExpandPlatform(r *http.Request) map[string]bool {
	result := make(map[string]bool)
	for _, raw := range r.URL.Query()["expand"] {
		for field := range strings.SplitSeq(raw, ",") {
			field = strings.TrimSpace(field)
			if field != "" {
				result[field] = true
			}
		}
	}
	return result
}

func withLoggerName(_ *slog.Logger, name string) *slog.Logger {
	return slog.Default().With("logger_name", name)
}

func validatePathUUID(id string) error {
	if id == "" {
		return nil
	}
	if _, err := uuid.Parse(id); err != nil || len(id) != canonicalUUIDLen {
		return &ValidationError{Message: "invalid id: must be a UUID"}
	}
	return nil
}

const canonicalUUIDLen = 36

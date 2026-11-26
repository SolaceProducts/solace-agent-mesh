// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package auth

import (
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

type ErrorType string

const (
	ErrorAuthenticationRequired ErrorType = "authentication_required"

	ErrorInvalidToken ErrorType = "invalid_token"

	ErrorAuthorizationFailure ErrorType = "authorization_failure"

	ErrorSessionExpired ErrorType = "session_expired"

	ErrorTokenRefreshFailed ErrorType = "token_refresh_failed"

	ErrorServerConfiguration ErrorType = "server_configuration"

	ErrorServiceUnavailable ErrorType = "service_unavailable"
)

type errorBody struct {
	Message   string    `json:"message"`
	ErrorID   string    `json:"errorId"`
	ErrorType ErrorType `json:"errorType"`
}

func WriteError(w http.ResponseWriter, status int, errType ErrorType, detail string) {
	writeError(w, nil, status, errType, detail)
}

func WriteErrorReq(w http.ResponseWriter, r *http.Request, status int, errType ErrorType, detail string) {
	writeError(w, r, status, errType, detail)
}

var authErrorLogMessage = map[ErrorType]string{
	ErrorAuthenticationRequired: "gateway auth: authentication required",
	ErrorInvalidToken:           "gateway auth: invalid token",
	ErrorAuthorizationFailure:   "gateway auth: authorization failure",
	ErrorSessionExpired:         "gateway auth: session expired",
	ErrorTokenRefreshFailed:     "gateway auth: token refresh failed",
	ErrorServerConfiguration:    "gateway auth: server configuration error",
	ErrorServiceUnavailable:     "gateway auth: service unavailable",
}

func writeError(w http.ResponseWriter, r *http.Request, status int, errType ErrorType, detail string) {
	errID := idgen.New()
	logger := slog.Default().With("logger_name", "gateway-auth")
	logArgs := []any{"errorId", errID, "errorType", string(errType), "httpStatus", status, "detail", detail}
	logArgs = append(logArgs, requestAuthFields(r)...)
	msg := authErrorLogMessage[errType]
	if msg == "" {
		msg = "gateway auth: error"
	}
	if status >= http.StatusInternalServerError {
		logger.Error(msg, logArgs...)
	} else {
		logger.Warn(msg, logArgs...)
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(errorBody{Message: detail, ErrorID: errID, ErrorType: errType})
}

func requestAuthFields(r *http.Request) []any {
	if r == nil {
		return nil
	}
	fields := []any{
		"method", r.Method,
		"path", LoggablePath(r),
		"clientIP", capAuthLogField(clientIP(r)),
		"userAgent", capAuthLogField(r.UserAgent()),
	}
	if id, ok := SubjectFromContext(r.Context()); ok && id != nil && id.credentialID != "" {
		fields = append(fields, "credentialID", id.credentialID)
	}
	return fields
}

const (
	sharePathPrefix      = "/api/v1/share/"
	sharePathPlaceholder = sharePathPrefix + "..."
)

func LoggablePath(r *http.Request) string {
	if strings.HasPrefix(r.URL.Path, sharePathPrefix) {
		return sharePathPlaceholder
	}
	return r.URL.Path
}

const maxAuthLogFieldLen = 256

func capAuthLogField(s string) string {
	if len(s) > maxAuthLogFieldLen {
		return s[:maxAuthLogFieldLen]
	}
	return s
}

func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); strings.TrimSpace(xff) != "" {
		first := xff
		if i := strings.IndexByte(xff, ','); i >= 0 {
			first = xff[:i]
		}
		if ip := strings.TrimSpace(first); ip != "" {
			return ip
		}
	}
	if xr := strings.TrimSpace(r.Header.Get("X-Real-IP")); xr != "" {
		return xr
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

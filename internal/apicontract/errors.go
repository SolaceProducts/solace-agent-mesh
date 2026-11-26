// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apicontract

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

const ErrorLoggerName = "platform-admin"

const MsgHTTPErrorResponse = "http error response"

type ErrorBody = errorEnvelope

type errorEnvelope struct {
	Message           string              `json:"message" example:"Request validation failed"`
	ErrorID           string              `json:"errorId" example:"6f1b8c1e-2c7a-4d0e-9a53-1d7f4c2b9e10"`
	ValidationDetails map[string][]string `json:"validationDetails,omitempty" example:"{\"name\":[\"must not be empty\"]}"`
}

type WarnLogged interface {
	error
	LoggedAsWarning() bool
}

var ErrRetryable = errors.New("retryable server condition")

func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set(headerContentType, "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func WriteErrorWithCause(w http.ResponseWriter, status int, message string, details map[string][]string, cause error) {
	WriteErrorLoggedAs(w, status, message, message, details, cause)
}

func WriteErrorLoggedAs(w http.ResponseWriter, status int, message, logMessage string, details map[string][]string, cause error) {
	errID := idgen.New()
	logger := slog.Default().With("logger_name", ErrorLoggerName)
	attrs := []any{"errorId", errID, "httpStatus", status, "message", logMessage}
	if cause != nil {
		attrs = append(attrs, "error", cause)
	}
	switch {
	case errors.Is(cause, context.Canceled), errors.Is(cause, context.DeadlineExceeded):
		logger.Warn(MsgHTTPErrorResponse, attrs...)
	case errors.Is(cause, ErrRetryable), isWarnLogged(cause):
		logger.Warn(MsgHTTPErrorResponse, attrs...)
	case status >= 500:
		logger.Error(MsgHTTPErrorResponse, attrs...)
	default:
		logger.Warn(MsgHTTPErrorResponse, attrs...)
	}
	WriteJSON(w, status, errorEnvelope{Message: message, ErrorID: errID, ValidationDetails: details})
}

func isWarnLogged(err error) bool {
	cause, ok := errors.AsType[WarnLogged](err)
	return ok && cause.LoggedAsWarning()
}

func WriteErrorWithLoggedID(w http.ResponseWriter, status int, message string, details map[string][]string, errorID string) {
	WriteJSON(w, status, errorEnvelope{Message: message, ErrorID: errorID, ValidationDetails: details})
}

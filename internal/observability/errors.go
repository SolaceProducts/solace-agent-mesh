// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package observability

import (
	"errors"
	"net/http"
	"strings"
)

func classifyError(err error) string {
	if err == nil {
		return ""
	}

	errStr := err.Error()

	var httpErr interface{ StatusCode() int }
	if errors.As(err, &httpErr) {
		return classifyHTTPStatus(httpErr.StatusCode())
	}

	lower := strings.ToLower(errStr)

	switch {
	case strings.Contains(lower, "timeout") || errors.Is(err, http.ErrHandlerTimeout):
		return "timeout"
	case strings.Contains(lower, "rate_limit") || strings.Contains(lower, "rate limit"):
		return "rate_limit"
	case strings.Contains(lower, "context_length") || strings.Contains(lower, "context length"):
		return "context_length_exceeded"
	case strings.Contains(lower, "permission") || strings.Contains(lower, "unauthorized") ||
		strings.Contains(lower, "forbidden"):
		return "auth_error"
	default:
		return "error"
	}
}

func classifyHTTPStatus(code int) string {
	switch {
	case code == http.StatusUnauthorized || code == http.StatusForbidden:
		return "auth_error"
	case code >= 400 && code < 500:
		return "4xx_error"
	case code >= 500:
		return "5xx_error"
	default:
		return "error"
	}
}

func ClassifyHTTPStatus(code int) string {
	return classifyHTTPStatus(code)
}

func classifyRemoteError(err error) string {
	if err == nil {
		return ""
	}
	if isConnectionError(err) {
		return "connection_error"
	}
	return classifyError(err)
}

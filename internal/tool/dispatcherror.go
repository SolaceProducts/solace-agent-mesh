// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tool

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/taxonomy"
)

const AuditToolErrorCode = "tool_error"

const DispatchErrorCodePrefix = taxonomy.DispatchErrorCodePrefix

const ErrorCodeDispatchError = DispatchErrorCodePrefix + "ERROR"

func DispatchErrorCode(class string) string {
	return DispatchErrorCodePrefix + strings.ToUpper(class)
}

func DispatchFailure(err error) *Result {
	return ErrorFromCaller(fmt.Sprintf("dispatch error: %v", err),
		WithErrorCode(DispatchErrorCode(ClassifyDispatchError(err))), WithCause(err))
}

func PreDispatchFailure(err error) *Result {
	return ErrorFromCaller(fmt.Sprintf("dispatch error: %v", err),
		WithErrorCode(ErrorCodeDispatchError), WithCause(err))
}

func ClassifyDispatchError(err error) string {
	if err == nil {
		return ""
	}
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return taxonomy.DispatchClassTimeout
	case errors.Is(err, context.Canceled):
		return taxonomy.DispatchClassCanceled
	}

	msg := err.Error()
	low := strings.ToLower(msg)
	switch {
	case strings.HasPrefix(low, "invalid arguments"):
		return taxonomy.DispatchClassValidation
	case strings.HasPrefix(low, "validation"):
		return taxonomy.DispatchClassValidation
	case strings.Contains(low, "auth_required"), strings.Contains(low, "auth required"):
		return taxonomy.DispatchClassAuthRequired
	case containsBackendStatus(low, 400, 499):
		return taxonomy.DispatchClassBackend4xx
	case containsBackendStatus(low, 500, 599):
		return taxonomy.DispatchClassBackend5xx
	}
	return taxonomy.DispatchClassInternal
}

func containsBackendStatus(low string, lo, hi int) bool {
	for code := lo; code <= hi; code++ {
		c := itoa3(code)
		if strings.Contains(low, "status "+c) ||
			strings.Contains(low, "http "+c) ||
			strings.Contains(low, "status: "+c) ||
			strings.Contains(low, "code: "+c) {
			return true
		}
	}
	return false
}

func itoa3(n int) string {
	if n < 100 || n > 999 {
		return ""
	}
	return string(rune('0'+n/100)) +
		string(rune('0'+(n/10)%10)) +
		string(rune('0'+n%10))
}

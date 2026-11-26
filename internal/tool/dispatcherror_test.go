// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tool

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/taxonomy"
)

func TestClassifyDispatchError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{"nil", nil, ""},
		{"deadline", context.DeadlineExceeded, "timeout"},
		{"deadline-wrapped", fmt.Errorf("call: %w", context.DeadlineExceeded), "timeout"},
		{"canceled", context.Canceled, "canceled"},
		{"invalid-arguments", errors.New("invalid arguments for foo: missing field x"), "validation"},
		{"auth-required-marker", errors.New("auth_required: please re-login"), "auth_required"},
		{"backend-4xx", errors.New("upstream MCP: status 401 Unauthorized"), "backend_4xx"},
		{"backend-5xx", errors.New("upstream MCP: HTTP 503 Service Unavailable"), "backend_5xx"},
		{"unknown", errors.New("could not parse 'sk-secret-token-leaked' as JSON"), "internal"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ClassifyDispatchError(tt.err)
			if got != tt.want {
				t.Errorf("ClassifyDispatchError(%v) = %q, want %q", tt.err, got, tt.want)
			}
		})
	}
}

func TestClassifyDispatchErrorRedactsSensitive(t *testing.T) {
	leak := "sk-supersecret-1234"
	err := fmt.Errorf("backend rejected request: could not parse %q", leak)
	got := ClassifyDispatchError(err)
	if got == "" {
		t.Fatalf("expected a classification, got empty string")
	}
	if got != "internal" {
		t.Errorf("expected classification 'internal' for unknown error, got %q", got)
	}
	if got == err.Error() {
		t.Errorf("classifier returned full error message — leaks sensitive data")
	}
}

var dispatchErrorsByBranch = []error{
	context.DeadlineExceeded,
	fmt.Errorf("call: %w", context.DeadlineExceeded),
	context.Canceled,
	fmt.Errorf("call: %w", context.Canceled),
	errors.New("invalid arguments for foo: missing field x"),
	errors.New("Validation failed: x must be positive"),
	errors.New("auth_required: please re-login"),
	errors.New("upstream said auth required"),
	errors.New("upstream MCP: status 401 Unauthorized"),
	errors.New("upstream MCP: http 404"),
	errors.New("upstream MCP: status: 429"),
	errors.New("upstream MCP: code: 499"),
	errors.New("upstream MCP: HTTP 503 Service Unavailable"),
	errors.New("upstream MCP: status 500"),
	errors.New("upstream MCP: code: 599"),
	errors.New("could not parse the response as JSON"),
}

func TestDispatchClassesStayInTheSharedCatalog(t *testing.T) {
	for _, err := range dispatchErrorsByBranch {
		got := ClassifyDispatchError(err)
		if !slices.Contains(taxonomy.DispatchErrorClasses, got) {
			t.Errorf("ClassifyDispatchError(%q) = %q, which taxonomy.DispatchErrorClasses %v does not list",
				err, got, taxonomy.DispatchErrorClasses)
		}
	}
}

func TestEveryDispatchClassIsReachable(t *testing.T) {
	reached := map[string]bool{}
	for _, err := range dispatchErrorsByBranch {
		reached[ClassifyDispatchError(err)] = true
	}
	for _, class := range taxonomy.DispatchErrorClasses {
		if class == taxonomy.DispatchClassError || slices.Contains(taxonomy.UserInputClasses, class) ||
			slices.Contains(taxonomy.ToolReportedClasses, class) {
			continue
		}
		if !reached[class] {
			t.Errorf("catalog class %q is never returned by ClassifyDispatchError", class)
		}
	}
}

func TestDispatchErrorCodesNormalizeIntoTheCatalog(t *testing.T) {
	codes := []string{ErrorCodeDispatchError}
	for _, err := range dispatchErrorsByBranch {
		codes = append(codes, DispatchErrorCode(ClassifyDispatchError(err)))
	}
	for _, code := range codes {
		class, ok := strings.CutPrefix(code, DispatchErrorCodePrefix)
		if !ok {
			t.Errorf("code %q lacks the %q prefix the dispatch taxonomy is keyed on", code, DispatchErrorCodePrefix)
			continue
		}
		if code != strings.ToUpper(code) {
			t.Errorf("code %q is not upper-case like the other closed tool error codes", code)
		}
		if !slices.Contains(taxonomy.DispatchErrorClasses, strings.ToLower(class)) {
			t.Errorf("code %q normalizes to %q, which the catalog does not list", code, strings.ToLower(class))
		}
	}
}

func TestDispatchFailureCarriesTheClassifiedCode(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want string
	}{
		{fmt.Errorf("call: %w", context.DeadlineExceeded), "DISPATCH_TIMEOUT"},
		{errors.New("upstream MCP: HTTP 503"), "DISPATCH_BACKEND_5XX"},
		{errors.New("could not parse the response"), "DISPATCH_INTERNAL"},
	} {
		got := DispatchFailure(tc.err)
		if got.Status != StatusError || got.ErrorCode != tc.want {
			t.Errorf("DispatchFailure(%q) = %s/%s, want error/%s", tc.err, got.Status, got.ErrorCode, tc.want)
		}
	}
	if got := PreDispatchFailure(errors.New("unknown tool")); got.ErrorCode != "DISPATCH_ERROR" {
		t.Errorf("PreDispatchFailure code = %s, want DISPATCH_ERROR", got.ErrorCode)
	}
}

// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package observability

import (
	"errors"
	"testing"
)

func TestClassifyError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{"nil", nil, ""},
		{"timeout", errors.New("context deadline exceeded: timeout"), "timeout"},
		{"rate limit", errors.New("rate_limit error from API"), "rate_limit"},
		{"rate limit spaces", errors.New("rate limit exceeded"), "rate_limit"},
		{"context length", errors.New("context_length_exceeded"), "context_length_exceeded"},
		{"context length alt", errors.New("maximum context length exceeded"), "context_length_exceeded"},
		{"permission", errors.New("permission denied"), "auth_error"},
		{"unauthorized", errors.New("unauthorized access"), "auth_error"},
		{"forbidden", errors.New("forbidden resource"), "auth_error"},
		{"generic", errors.New("something went wrong"), "error"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classifyError(tt.err)
			if got != tt.want {
				t.Errorf("classifyError(%v) = %q, want %q", tt.err, got, tt.want)
			}
		})
	}
}

func TestClassifyHTTPStatus(t *testing.T) {
	tests := []struct {
		code int
		want string
	}{
		{401, "auth_error"},
		{403, "auth_error"},
		{400, "4xx_error"},
		{404, "4xx_error"},
		{429, "4xx_error"},
		{500, "5xx_error"},
		{503, "5xx_error"},
		{200, "error"},
		{301, "error"},
	}

	for _, tt := range tests {
		got := classifyHTTPStatus(tt.code)
		if got != tt.want {
			t.Errorf("classifyHTTPStatus(%d) = %q, want %q", tt.code, got, tt.want)
		}
	}
}

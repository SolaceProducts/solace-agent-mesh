// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package doctor

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLLMCheck(t *testing.T) {
	tests := []struct {
		name               string
		env                Env
		handler            http.HandlerFunc
		wantStatus         Status
		wantReasonContains string
	}{
		{
			name:       "skip when endpoint empty",
			env:        Env{},
			wantStatus: StatusSkip,
		},
		{
			name:               "fail when endpoint set but no api key",
			env:                Env{LLMServiceEndpoint: "http://example.invalid"},
			wantStatus:         StatusFail,
			wantReasonContains: "no API key",
		},
		{
			name: "pass on 200",
			env:  Env{LLMServiceAPIKey: "k"},
			handler: func(w http.ResponseWriter, r *http.Request) {
				if !strings.HasSuffix(r.URL.Path, "/v1/models") {
					t.Errorf("unexpected path %s", r.URL.Path)
				}
				if r.Header.Get("Authorization") != "Bearer k" {
					t.Errorf("missing bearer header")
				}
				w.WriteHeader(http.StatusOK)
			},
			wantStatus: StatusPass,
		},
		{
			name: "warn on 401",
			env:  Env{LLMServiceAPIKey: "k"},
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusUnauthorized)
			},
			wantStatus:         StatusWarn,
			wantReasonContains: "invalid or unauthorized",
		},
		{
			name: "warn on 403",
			env:  Env{LLMServiceAPIKey: "k"},
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusForbidden)
			},
			wantStatus:         StatusWarn,
			wantReasonContains: "invalid or unauthorized",
		},
		{
			name: "fall through 404 to root then PASS on 200",
			env:  Env{LLMServiceAPIKey: "k"},
			handler: func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/v1/models") {
					w.WriteHeader(http.StatusNotFound)
					return
				}
				w.WriteHeader(http.StatusOK)
			},
			wantStatus: StatusPass,
		},
		{
			name: "fall through 405 to root, treat 500 as PASS (server reachable)",
			env:  Env{LLMServiceAPIKey: "k"},
			handler: func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/v1/models") {
					w.WriteHeader(http.StatusMethodNotAllowed)
					return
				}
				w.WriteHeader(http.StatusInternalServerError)
			},
			wantStatus: StatusPass,
		},
		{
			name: "5xx on /v1/models → PASS",
			env:  Env{LLMServiceAPIKey: "k"},
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusBadGateway)
			},
			wantStatus: StatusPass,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := tt.env
			if tt.handler != nil {
				srv := httptest.NewServer(tt.handler)
				defer srv.Close()
				env.LLMServiceEndpoint = srv.URL
			}
			c := NewLLMCheck()
			got := c.Run(context.Background(), env, ContextLocal)
			if got.Status != tt.wantStatus {
				t.Errorf("status: got %s, want %s; reason=%q", got.Status, tt.wantStatus, got.Reason)
			}
			if tt.wantReasonContains != "" && !strings.Contains(got.Reason, tt.wantReasonContains) {
				t.Errorf("reason: got %q, want contains %q", got.Reason, tt.wantReasonContains)
			}
		})
	}
}

func TestLLMCheck_NetworkError(t *testing.T) {
	env := Env{LLMServiceEndpoint: "http://127.0.0.1:1", LLMServiceAPIKey: "k"}
	c := NewLLMCheck()
	got := c.Run(context.Background(), env, ContextLocal)
	if got.Status != StatusWarn {
		t.Errorf("status: got %s, want WARN", got.Status)
	}
	if !strings.Contains(got.Reason, "unreachable") {
		t.Errorf("reason: %q", got.Reason)
	}
}

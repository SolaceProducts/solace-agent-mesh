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

func TestOIDCCheck(t *testing.T) {
	tests := []struct {
		name       string
		env        Env
		statusCode int
		dontStart  bool
		wantStatus Status
		wantReason string
	}{
		{
			name:       "skip when authentication disabled",
			env:        Env{AuthenticationEnabled: "false", AuthenticationServer: "x"},
			wantStatus: StatusSkip,
		},
		{
			name:       "skip when server empty",
			env:        Env{AuthenticationEnabled: "true"},
			wantStatus: StatusSkip,
		},
		{
			name:       "pass on 200",
			env:        Env{AuthenticationEnabled: "true"},
			statusCode: 200,
			wantStatus: StatusPass,
		},
		{
			name:       "fail on 404",
			env:        Env{AuthenticationEnabled: "true"},
			statusCode: 404,
			wantStatus: StatusFail,
			wantReason: "HTTP 404",
		},
		{
			name:       "fail on 500",
			env:        Env{AuthenticationEnabled: "true"},
			statusCode: 500,
			wantStatus: StatusFail,
			wantReason: "HTTP 500",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := tt.env
			if tt.statusCode != 0 {
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if !strings.HasSuffix(r.URL.Path, "/.well-known/openid-configuration") {
						t.Errorf("unexpected path %s", r.URL.Path)
					}
					w.WriteHeader(tt.statusCode)
				}))
				defer srv.Close()
				env.AuthenticationServer = srv.URL + "/"
			}
			c := NewOIDCCheck()
			got := c.Run(context.Background(), env, ContextHelm)
			if got.Status != tt.wantStatus {
				t.Errorf("status: got %s, want %s; reason=%q", got.Status, tt.wantStatus, got.Reason)
			}
			if tt.wantReason != "" && !strings.Contains(got.Reason, tt.wantReason) {
				t.Errorf("reason: got %q, want contains %q", got.Reason, tt.wantReason)
			}
		})
	}
}

func TestOIDCCheck_NetworkError(t *testing.T) {
	env := Env{AuthenticationEnabled: "true", AuthenticationServer: "http://127.0.0.1:1"}
	c := NewOIDCCheck()
	got := c.Run(context.Background(), env, ContextHelm)
	if got.Status != StatusFail {
		t.Errorf("got %s, want FAIL", got.Status)
	}
	if !strings.Contains(got.Reason, "unreachable") {
		t.Errorf("reason: %q", got.Reason)
	}
}

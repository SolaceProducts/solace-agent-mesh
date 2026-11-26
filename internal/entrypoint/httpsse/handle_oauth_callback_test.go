// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

func TestOAuthCallback_ErrorShape(t *testing.T) {
	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(t.Context()) })

	core := entrypoint.NewGatewayCore(NewHTTPSSEAdapter(), map[string]any{
		"namespace":  "test",
		"gateway_id": "gw",
	})
	rt := runtime.NewRuntime(brk, nil, runtime.WithLogger(slog.Default()))
	core.SetServices(rt.Services())

	handler := &oauthCallbackHandler{core: core, logger: slog.Default()}

	cases := []struct {
		name         string
		query        string
		wantStatus   int
		wantErrType  auth.ErrorType
		wantContains string
	}{
		{
			name:         "missing both",
			query:        "",
			wantStatus:   http.StatusBadRequest,
			wantErrType:  auth.ErrorInvalidToken,
			wantContains: "Missing code or state",
		},
		{
			name:         "missing code",
			query:        "state=abc",
			wantStatus:   http.StatusBadRequest,
			wantErrType:  auth.ErrorInvalidToken,
			wantContains: "Missing code or state",
		},
		{
			name:         "unknown state",
			query:        "code=xyz&state=never-issued",
			wantStatus:   http.StatusBadRequest,
			wantErrType:  auth.ErrorInvalidToken,
			wantContains: "Invalid or expired state",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/api/v1/auth/tool/callback?"+tc.query, nil)
			w := httptest.NewRecorder()
			handler.handleCallback(w, req)

			if w.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d (body=%s)", w.Code, tc.wantStatus, w.Body.String())
			}

			ct := w.Header().Get("Content-Type")
			if !strings.HasPrefix(ct, "application/json") {
				t.Errorf("Content-Type = %q, want application/json (regression: handler must not fall back to text/plain)", ct)
			}

			var body map[string]any
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatalf("response not JSON: %v (body=%s)", err, w.Body.String())
			}
			message, _ := body["message"].(string)
			if !strings.Contains(message, tc.wantContains) {
				t.Errorf("message = %q, want substring %q", message, tc.wantContains)
			}
			if body["errorType"] != string(tc.wantErrType) {
				t.Errorf("errorType = %v, want %q", body["errorType"], tc.wantErrType)
			}
		})
	}
}

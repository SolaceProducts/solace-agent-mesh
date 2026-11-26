// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package notify

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

func TestHandlerRefusesAPIKeys(t *testing.T) {
	ctx := t.Context()
	brk := broker.NewDevBroker()
	if err := brk.Connect(ctx); err != nil {
		t.Fatalf("connect broker: %v", err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(ctx) })

	for _, tc := range []struct {
		authMethod string
		want       int
	}{
		{auth.APIKeyAuthMethod, http.StatusForbidden},
		{"sam_access_token", http.StatusServiceUnavailable},
	} {
		t.Run(tc.authMethod, func(t *testing.T) {
			identity := auth.NewTestSubject(auth.SubjectFields{UserID: "system:ci", Authenticated: true, AuthMethod: tc.authMethod, Scopes: []string{"*"}})
			core := NewNotifyCore(map[string]any{"namespace": "test-ns"}, NotifyExtensions{
				AuthMiddleware: func(next http.Handler) http.Handler {
					return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						next.ServeHTTP(w, r.WithContext(auth.ContextWithSubject(r.Context(), identity)))
					})
				},
			})
			core.SetServices(runtime.NewRuntime(brk, nil, runtime.WithLogger(slog.Default())).Services())
			if err := core.Init(ctx, nil); err != nil {
				t.Fatalf("Init: %v", err)
			}
			t.Cleanup(func() { _ = core.Stop(ctx) })

			rr := httptest.NewRecorder()
			core.handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v1/notify/inbox", nil))
			if rr.Code != tc.want {
				t.Errorf("status = %d, want %d; body=%s", rr.Code, tc.want, rr.Body.String())
			}
		})
	}
}

// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"net/http/httptest"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
)

func TestResolveUserID(t *testing.T) {
	core := entrypoint.NewGatewayCore(NewHTTPSSEAdapter(), map[string]any{
		"namespace":  "test",
		"gateway_id": "gw",
	})
	tests := []struct {
		name     string
		identity *auth.Subject
		want     string
	}{
		{name: "sub wins", identity: auth.NewTestSubject(auth.SubjectFields{UserID: "sub-x", Email: "e@example.com"}), want: "sub-x"},
		{name: "email is the middle rung when sub is empty", identity: auth.NewTestSubject(auth.SubjectFields{Email: "e@example.com"}), want: "e@example.com"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/api/v1/tasks", nil)
			r = r.WithContext(auth.ContextWithSubject(r.Context(), tc.identity))
			if got := resolveUserID(r, core); got != tc.want {
				t.Errorf("resolveUserID = %q, want %q", got, tc.want)
			}
		})
	}
}

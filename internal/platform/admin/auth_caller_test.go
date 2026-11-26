// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	samauth "github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/trust"
)

type stubAuthenticator struct{ id *samauth.Identity }

func (s stubAuthenticator) Authenticate(context.Context, string, string) (*samauth.Identity, error) {
	return s.id, nil
}

func TestAuthMiddleware_DerivesEmailAndName(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name                string
		id                  *samauth.Identity
		wantEmail, wantName string
	}{
		{
			name:      "from claims",
			id:        &samauth.Identity{UserID: "sub-1", Scopes: []string{"agent_builder:*:read"}, Claims: map[string]any{"email": "alice@example.com", "name": "Alice"}},
			wantEmail: "alice@example.com",
			wantName:  "Alice",
		},
		{
			name:      "falls back to user id when claims absent",
			id:        &samauth.Identity{UserID: "sub-2", Scopes: []string{"agent_builder:*:read"}},
			wantEmail: "sub-2",
			wantName:  "sub-2",
		},
		{
			name:      "mixed: email from claims, name falls back to user id",
			id:        &samauth.Identity{UserID: "sub-3", Scopes: []string{"agent_builder:*:read"}, Claims: map[string]any{"email": "carol@example.com"}},
			wantEmail: "carol@example.com",
			wantName:  "sub-3",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mw := NewAuthMiddleware(stubAuthenticator{id: tc.id}, nil, nil, slog.Default())
			var got *auth.Subject
			next := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				got, _ = auth.SubjectFromContext(r.Context())
			})
			req := httptest.NewRequest(http.MethodGet, "/api/v1/platform/test", nil)
			req.Header.Set("Authorization", "Bearer token")
			rr := httptest.NewRecorder()
			mw(next).ServeHTTP(rr, req)

			if rr.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", rr.Code)
			}
			if got == nil {
				t.Fatal("middleware injected no identity")
			}
			if got.Email() != tc.wantEmail || got.Name() != tc.wantName {
				t.Errorf("Email=%q Name=%q, want %q/%q", got.Email(), got.Name(), tc.wantEmail, tc.wantName)
			}
		})
	}
}

func TestAuthMiddleware_CarriesThePlatformUserIDClaim(t *testing.T) {
	t.Parallel()
	rowID := idgen.New()
	for name, tc := range map[string]struct {
		claims map[string]any
		want   string
	}{
		"claim present": {claims: map[string]any{trust.ClaimPlatformUserID: rowID}, want: rowID},
		"claim absent":  {claims: map[string]any{"email": "alice@example.com"}, want: ""},
	} {
		t.Run(name, func(t *testing.T) {
			id := &samauth.Identity{UserID: "sub-1", Scopes: []string{"agent_builder:*:read"}, Claims: tc.claims}
			var got *auth.Subject
			next := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				got, _ = auth.SubjectFromContext(r.Context())
			})
			req := httptest.NewRequest(http.MethodGet, "/api/v1/platform/test", nil)
			req.Header.Set("Authorization", "Bearer token")
			NewAuthMiddleware(stubAuthenticator{id: id}, nil, nil, slog.Default())(next).ServeHTTP(httptest.NewRecorder(), req)

			if got == nil || got.PlatformUserID() != tc.want {
				t.Errorf("PlatformUserID = %+v, want %q", got, tc.want)
			}
		})
	}
}

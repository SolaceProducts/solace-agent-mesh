// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	samauth "github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
)

func TestPlatformAuthMiddleware_KeepsThePlatformUserID(t *testing.T) {
	t.Parallel()
	const sessionUserRow = "0192f3aa-7c31-70b2-9e4d-9b1f2a5c8e11"
	keyOwnerRow := resolvedKeyAuthority().resolution.OwnerID
	for name, tc := range map[string]struct {
		session *samauth.Identity
		request *http.Request
		want    string
	}{
		"a session token naming a users row": {
			session: &samauth.Identity{UserID: "alice@example.com", Scopes: []string{"agent:*:invoke"}, PlatformUserID: sessionUserRow},
			request: sessionRequest(),
			want:    sessionUserRow,
		},
		"a session token minted before the claim": {
			session: &samauth.Identity{UserID: "alice@example.com", Scopes: []string{"agent:*:invoke"}},
			request: sessionRequest(),
			want:    "",
		},
		"an API key is its owner's row": {
			request: keyRequest("/api/v1/platform/agents"),
			want:    keyOwnerRow,
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			mw := NewAuthMiddleware(&countingAuthenticator{id: tc.session}, nil, resolvedKeyAuthority(), slog.Default())
			var got *auth.Subject
			mw(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				got, _ = auth.SubjectFromContext(r.Context())
			})).ServeHTTP(httptest.NewRecorder(), tc.request)

			if got == nil {
				t.Fatal("no identity reached the handler")
			}
			if got.PlatformUserID() != tc.want {
				t.Errorf("PlatformUserID = %q, want %q", got.PlatformUserID(), tc.want)
			}
		})
	}
}

func sessionRequest() *http.Request {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/platform/agents", nil)
	req.Header.Set("Authorization", "Bearer signed.session.token")
	return req
}

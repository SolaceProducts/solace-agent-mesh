// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/apikey"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
)

type gateKeyAuthority struct{ scopes []string }

func (g gateKeyAuthority) Validate(context.Context, string) (apikey.Resolution, error) {
	return apikey.Resolution{
		KeyID:    "key-1",
		OwnerID:  "0192f3aa-7c31-70b2-9e4d-9b1f2a5c8e77",
		OwnerSub: "system:ci-deployer",
		Scopes:   g.scopes,
	}, nil
}

func TestScoped_HonoursAPIKeySurvivingScopes(t *testing.T) {
	okHandler := func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }

	for name, tc := range map[string]struct {
		surviving  []string
		gate       string
		wantStatus int
		why        string
	}{
		"exact scope admits": {
			surviving: []string{"agent:finance:invoke"}, gate: "agent:finance:invoke",
			wantStatus: http.StatusOK,
			why:        "a key scoped for exactly this route must reach it",
		},
		"wildcard scope admits a concrete instance": {
			surviving: []string{"agent:*:invoke"}, gate: "agent:finance:invoke",
			wantStatus: http.StatusOK,
			why:        "the key selected agent:*:invoke; expansion happens in HasScope, which a slice comparison cannot test",
		},
		"a different instance is refused": {
			surviving: []string{"agent:finance:invoke"}, gate: "agent:hr:invoke",
			wantStatus: http.StatusForbidden,
			why:        "this is the narrowing doing its job — the key holds finance and must not reach hr",
		},
		"a different category is refused": {
			surviving: []string{"agent:*:invoke"}, gate: "workflow:abc:invoke",
			wantStatus: http.StatusForbidden,
			why:        "agent:*:invoke does not cover the workflow category",
		},
		"universal grant admits": {
			surviving: []string{"*:*:*"}, gate: "agent:finance:invoke",
			wantStatus: http.StatusOK,
			why:        "a key carrying the universal grant reaches scoped routes exactly as a person does",
		},
		"empty surviving set is refused, not admitted": {
			surviving: nil, gate: "agent:finance:invoke",
			wantStatus: http.StatusForbidden,
			why:        "the real authority refuses such a key with a 401, so an empty set can only come from another implementation, and must be denied at the gate, never passed",
		},
	} {
		t.Run(name, func(t *testing.T) {
			identity, rejection := auth.ResolveAPIKey(
				t.Context(), gateKeyAuthority{scopes: tc.surviving}, "samk_AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8")
			if rejection != nil {
				t.Fatalf("key did not resolve: %+v", rejection)
			}

			req := httptest.NewRequest(http.MethodPost, "/api/v1/message:send", nil)
			req = req.WithContext(auth.ContextWithSubject(req.Context(), identity))
			rec := httptest.NewRecorder()
			scoped(tc.gate, okHandler)(rec, req)

			if rec.Code != tc.wantStatus {
				t.Errorf("status = %d, want %d — %s", rec.Code, tc.wantStatus, tc.why)
			}
		})
	}
}

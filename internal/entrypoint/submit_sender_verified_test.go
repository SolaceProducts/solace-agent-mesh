// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"testing"

	gwauth "github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
)

type matchingScopeResolver struct{ platformUserID string }

func (matchingScopeResolver) ResolveScopes(string, []string) []string {
	return []string{"agent:*:invoke"}
}

func (r matchingScopeResolver) ResolveScopesAndPlatformUser(string, []string) ([]string, string) {
	return []string{"agent:*:invoke"}, r.platformUserID
}

func TestResolveLazyScopesStampsTheMatchedRowOnlyForAVerifiedSender(t *testing.T) {
	const matched = "0192f0c4-6a4e-7b1e-9d3c-5f2a8b7e1c46"
	for _, tc := range []struct {
		name     string
		verified bool
		want     string
	}{
		{"a chat sender whose address the adapter verified", true, matched},
		{"a user an event payload names, which any publisher could forge", false, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			core := &GatewayCore{scopeResolver: matchingScopeResolver{platformUserID: matched}}
			identity := gwauth.NewTestSubject(gwauth.SubjectFields{UserID: "victim@example.com", SenderVerified: tc.verified})

			core.resolveLazyScopes(identity)

			if identity.PlatformUserID() != tc.want {
				t.Errorf("PlatformUserID = %q, want %q", identity.PlatformUserID(), tc.want)
			}
			if len(identity.Scopes()) != 1 {
				t.Errorf("scopes = %v, want them resolved either way", identity.Scopes())
			}
		})
	}
}

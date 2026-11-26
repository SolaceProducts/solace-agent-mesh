// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package samembed

import (
	"context"
	"errors"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/oidcauth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/identity"
)

type resolutionClient struct {
	resolution auth.AuthorizationResolution
	err        error
}

func (c resolutionClient) Lookup(context.Context, string, map[string]any) (auth.AuthorizationResolution, error) {
	return c.resolution, c.err
}

func TestAuthzScopeResolverReportsOnlyAMatchedPlatformUser(t *testing.T) {
	const aliceID = "0192f0c4-6a4e-7b1e-9d3c-5f2a8b7e1c44"
	for _, tc := range []struct {
		name   string
		client resolutionClient
		want   string
	}{
		{"matched to one user", resolutionClient{resolution: auth.AuthorizationResolution{Scopes: []string{"s"}, Resolution: identity.LookupMatched, ResolvedUserID: aliceID}}, aliceID},
		{"ambiguous between users", resolutionClient{resolution: auth.AuthorizationResolution{Scopes: []string{"s"}, Resolution: identity.LookupAmbiguous, ResolvedUserID: aliceID}}, ""},
		{"no stored user", resolutionClient{resolution: auth.AuthorizationResolution{Scopes: []string{"s"}, Resolution: identity.LookupNoMatch}}, ""},
		{"lookup failed", resolutionClient{err: errors.New("platform unreachable")}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, err := oidcauth.NewAuthorizationService(oidcauth.AuthorizationServiceConfig{Type: "default_rbac"}, "gw", discardLogger())
			if err != nil {
				t.Fatal(err)
			}
			svc.SetAuthorizationClient(tc.client)
			if _, got := (&AuthzScopeResolver{Svc: svc}).ResolveScopesAndPlatformUser("alice@example.com", nil); got != tc.want {
				t.Errorf("platform user = %q, want %q", got, tc.want)
			}
		})
	}
}

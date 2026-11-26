// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package harness

import (
	"context"

	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
)

type testAuthenticator struct {
	identity *auth.Identity
}

func (a *testAuthenticator) Authenticate(_ context.Context, _ string, _ string) (*auth.Identity, error) {
	return a.identity, nil
}

func (a *testAuthenticator) AuthenticateWithoutTaskBinding(_ context.Context, _ string) (*auth.Identity, error) {
	return a.identity, nil
}

func (a *testAuthenticator) AuthenticateWithSoftTaskBinding(_ context.Context, _ string, _ string) (*auth.Identity, error) {
	return a.identity, nil
}

func (*testAuthenticator) AllowEmptyToken() {}

var _ auth.Authenticator = (*testAuthenticator)(nil)
var _ auth.PeerAuthenticator = (*testAuthenticator)(nil)
var _ auth.EmptyTokenAuthenticator = (*testAuthenticator)(nil)

func SynthesizeCatalog(auth *GatewayAuth, idps map[string]*MockOIDCProvider) map[string]any {
	out := make(map[string]any, len(auth.Catalog))
	for _, c := range auth.Catalog {
		spec, ok := findFakeIDP(auth, c.IDPRef)
		if !ok {
			continue
		}
		idp := idps[c.IDPRef]
		if idp == nil {
			continue
		}
		entry := map[string]any{
			"issuer":        idp.URL(),
			"client_id":     spec.ClientID,
			"client_secret": spec.ClientSecret,
			"dev_mode":      true,
		}
		if len(c.Scopes) > 0 {
			entry["scopes"] = c.Scopes
		}
		out[c.Name] = entry
	}
	return out
}

func findFakeIDP(auth *GatewayAuth, name string) (*FakeIDPSpec, bool) {
	for i := range auth.FakeIDPs {
		if auth.FakeIDPs[i].Name == name {
			return &auth.FakeIDPs[i], true
		}
	}
	return nil, false
}

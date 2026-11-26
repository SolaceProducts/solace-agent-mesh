// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package oidcauth

import (
	"context"
	"errors"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/identity"
)

type failingAuthzClient struct{ err error }

func (c failingAuthzClient) Lookup(context.Context, string, map[string]any) (auth.AuthorizationResolution, error) {
	return auth.AuthorizationResolution{}, c.err
}

type fixedAuthzClient struct{ res auth.AuthorizationResolution }

func (c fixedAuthzClient) Lookup(context.Context, string, map[string]any) (auth.AuthorizationResolution, error) {
	return c.res, nil
}

func resolverWithClient(client auth.AuthorizationClient) *IdentityResolver {
	svc := &AuthorizationService{serviceType: "default_rbac", logger: testLogger()}
	svc.SetAuthorizationClient(client)
	return NewIdentityResolver(svc, nil)
}

func TestIdentityResolver_LookupOutageIsNotReportedAsUnauthorized(t *testing.T) {
	outage := errors.New("authz service unreachable")

	_, err := resolverWithClient(failingAuthzClient{err: outage}).ResolveIdentity(context.Background(), "alice")

	if err == nil {
		t.Fatal("expected an error")
	}
	if errors.Is(err, entrypoint.ErrIdentityUnauthorized) {
		t.Fatalf("an authz outage was reported as the owner being unauthorized: %v", err)
	}
	if !errors.Is(err, outage) {
		t.Fatalf("err = %v, want it to wrap the underlying outage", err)
	}
}

func TestIdentityResolver_DegradedReplyWithoutScopesIsNotADenial(t *testing.T) {
	client := fixedAuthzClient{res: auth.AuthorizationResolution{Resolution: identity.LookupDegraded}}

	_, err := resolverWithClient(client).ResolveIdentity(context.Background(), "alice")

	if err == nil {
		t.Fatal("expected an error")
	}
	if errors.Is(err, entrypoint.ErrIdentityUnauthorized) {
		t.Fatalf("a degraded authz reply was reported as the owner being unauthorized: %v", err)
	}
}

func TestIdentityResolver_DegradedReplyIsRetryableEvenWhenItCarriesScopes(t *testing.T) {
	client := fixedAuthzClient{res: auth.AuthorizationResolution{
		Resolution: identity.LookupDegraded,
		Scopes:     []string{"agent:PeerAgent:invoke"},
	}}

	_, err := resolverWithClient(client).ResolveIdentity(context.Background(), "alice")

	if err == nil {
		t.Fatal("a degraded reply resolved the owner from scopes computed without the owner's grants")
	}
	if errors.Is(err, entrypoint.ErrIdentityUnauthorized) {
		t.Fatalf("a degraded authz reply was reported as the owner being unauthorized: %v", err)
	}
}

func TestIdentityResolver_MatchedReplyWithScopesResolves(t *testing.T) {
	client := fixedAuthzClient{res: auth.AuthorizationResolution{
		Resolution: identity.LookupMatched,
		Scopes:     []string{"agent:PeerAgent:invoke"},
	}}

	owner, err := resolverWithClient(client).ResolveIdentity(context.Background(), "alice")
	if err != nil {
		t.Fatalf("ResolveIdentity: %v", err)
	}
	if len(owner.Scopes()) != 1 {
		t.Fatalf("scopes = %v, want the granted scope", owner.Scopes())
	}
}

func TestIdentityResolver_EmptyScopesWithAMatchedUserIsStillADenial(t *testing.T) {
	client := fixedAuthzClient{res: auth.AuthorizationResolution{Resolution: identity.LookupMatched}}

	_, err := resolverWithClient(client).ResolveIdentity(context.Background(), "alice")

	if !errors.Is(err, entrypoint.ErrIdentityUnauthorized) {
		t.Fatalf("err = %v, want ErrIdentityUnauthorized", err)
	}
}

func TestIdentityResolver_DenyAllIsUnauthorized(t *testing.T) {
	svc := &AuthorizationService{serviceType: "deny_all", logger: testLogger()}

	_, err := NewIdentityResolver(svc, nil).ResolveIdentity(context.Background(), "alice")
	if !errors.Is(err, entrypoint.ErrIdentityUnauthorized) {
		t.Fatalf("err = %v, want ErrIdentityUnauthorized", err)
	}
}

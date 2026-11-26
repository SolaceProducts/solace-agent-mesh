// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package oidcauth

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/identity"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

func TestIdentityResolver_PopulatedIdentity(t *testing.T) {
	svc := &AuthorizationService{
		serviceType: "default_rbac",
		logger:      testLogger(),
		yamlUsers: rbac.UsersConfig{
			Users: map[string]rbac.UserAssignment{
				"alice": {Roles: []string{"delegator"}},
			},
		},
	}
	engine, err := rbac.NewRoleEngine(rbac.RolesConfig{
		Roles: map[string]rbac.RoleDefinition{
			"delegator": {Scopes: []string{"agent:PeerAgent:invoke"}},
		},
	}, rbac.UsersConfig{}, nil)
	if err != nil {
		t.Fatalf("NewRoleEngine: %v", err)
	}
	svc.roleEngine = engine

	r := NewIdentityResolver(svc, nil)
	id, err := r.ResolveIdentity(context.Background(), "alice")
	if err != nil {
		t.Fatalf("ResolveIdentity: %v", err)
	}
	if id.UserID() != "alice" {
		t.Errorf("UserID = %q, want alice", id.UserID())
	}
	if !id.Authenticated() {
		t.Errorf("Authenticated = false, want true")
	}
	if id.AuthMethod() != "scheduled_task" {
		t.Errorf("AuthMethod = %q, want scheduled_task", id.AuthMethod())
	}
	if len(id.Scopes()) != 1 || id.Scopes()[0] != "agent:PeerAgent:invoke" {
		t.Errorf("Scopes = %v, want [agent:PeerAgent:invoke]", id.Scopes())
	}
}

func TestIdentityResolver_ResolveRunAsNeedsAKnownRow(t *testing.T) {
	r := NewIdentityResolver(&AuthorizationService{serviceType: "none", logger: testLogger()}, nil)

	id, err := r.ResolveRunAs(context.Background(), rbac.DefaultSystemUserSub)
	if err != nil {
		t.Fatalf("ResolveRunAs(%s): %v", rbac.DefaultSystemUserSub, err)
	}
	if id.UserID() != rbac.DefaultSystemUserSub || id.PlatformUserID() != rbac.DefaultSystemUserID {
		t.Errorf("caller = (%q, %q), want the built-in and its seeded row", id.UserID(), id.PlatformUserID())
	}
	if _, err = r.ResolveRunAs(context.Background(), "system:nightly"); !errors.Is(err, entrypoint.ErrIdentityUnauthorized) || !errors.Is(err, auth.ErrUnknownSystemUser) {
		t.Errorf("custom service account with no confirmed row: err = %v, want ErrIdentityUnauthorized wrapping ErrUnknownSystemUser", err)
	}
	if _, err = r.ResolveRunAs(context.Background(), "alice@example.com"); !errors.Is(err, entrypoint.ErrIdentityUnauthorized) {
		t.Errorf("a person: err = %v, want ErrIdentityUnauthorized", err)
	}
}

func TestIdentityResolver_ResolveRunAsRefusesACustomSystemUserWhereNoPlatformKnowsRows(t *testing.T) {
	engine, err := rbac.NewRoleEngine(rbac.RolesConfig{}, rbac.UsersConfig{}, nil)
	if err != nil {
		t.Fatalf("NewRoleEngine: %v", err)
	}
	cases := []struct {
		name string
		svc  *AuthorizationService
	}{
		{"deny_all", &AuthorizationService{serviceType: "deny_all", logger: testLogger()}},
		{"local RBAC", &AuthorizationService{serviceType: "default_rbac", logger: testLogger(), roleEngine: engine}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := NewIdentityResolver(c.svc, nil).ResolveRunAs(t.Context(), "system:nightly")

			if !errors.Is(err, auth.ErrUnknownSystemUser) {
				t.Errorf("ResolveRunAs(system:nightly) error = %v, want ErrUnknownSystemUser, not a wait", err)
			}
			if !errors.Is(err, entrypoint.ErrIdentityUnauthorized) {
				t.Errorf("ResolveRunAs(system:nightly) error = %v, want ErrIdentityUnauthorized", err)
			}
		})
	}
}

func TestIdentityResolver_ResolveRunAsKeepsAnUnconfirmedRowApartFromAMissingOne(t *testing.T) {
	scopes := []string{"agent:reporter:invoke"}
	cases := []struct {
		name             string
		res              auth.AuthorizationResolution
		wantUnavailable  bool
		wantUnauthorized bool
		wantUnknown      bool
		wantNotReported  bool
	}{
		{"the platform reports the row", auth.AuthorizationResolution{Scopes: scopes, SystemUserID: "row-1", SystemUserRow: auth.SystemUserRowFound}, false, false, false, false},
		{"the platform reports no row", auth.AuthorizationResolution{Scopes: scopes, SystemUserRow: auth.SystemUserRowMissing}, false, true, true, false},
		{"the platform could not look the row up", auth.AuthorizationResolution{Scopes: scopes, SystemUserRow: auth.SystemUserRowUnavailable}, true, false, false, false},
		{"an older platform says nothing about the row", auth.AuthorizationResolution{Scopes: scopes}, false, false, false, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			svc := &AuthorizationService{serviceType: "default_rbac", logger: testLogger()}
			svc.SetAuthorizationClient(stubAuthorizationClient{res: c.res})

			_, err := NewIdentityResolver(svc, nil).ResolveRunAs(t.Context(), "system:nightly")

			if got := errors.Is(err, auth.ErrSystemUserLookupUnavailable); got != c.wantUnavailable {
				t.Errorf("ResolveRunAs unavailable = %v, want %v (err: %v)", got, c.wantUnavailable, err)
			}
			if got := errors.Is(err, entrypoint.ErrIdentityUnauthorized); got != c.wantUnauthorized {
				t.Errorf("ResolveRunAs unauthorized = %v, want %v (err: %v)", got, c.wantUnauthorized, err)
			}
			if got := errors.Is(err, auth.ErrUnknownSystemUser); got != c.wantUnknown {
				t.Errorf("ResolveRunAs unknown service account = %v, want %v (err: %v)", got, c.wantUnknown, err)
			}
			if got := errors.Is(err, auth.ErrSystemUserRowNotReported); got != c.wantNotReported {
				t.Errorf("ResolveRunAs row not reported = %v, want %v (err: %v)", got, c.wantNotReported, err)
			}
		})
	}
}

func TestIdentityResolver_EmptyUserID(t *testing.T) {
	r := NewIdentityResolver(&AuthorizationService{serviceType: "default_rbac", logger: testLogger()}, nil)
	_, err := r.ResolveIdentity(context.Background(), "")
	if !errors.Is(err, entrypoint.ErrIdentityUnauthorized) {
		t.Errorf("err = %v, want ErrIdentityUnauthorized", err)
	}
}

func TestIdentityResolver_WritesProfileToCache(t *testing.T) {
	svc := &AuthorizationService{
		serviceType: "default_rbac",
		logger:      testLogger(),
		yamlUsers: rbac.UsersConfig{
			Users: map[string]rbac.UserAssignment{
				"alice": {Roles: []string{"delegator"}},
			},
		},
	}
	engine, err := rbac.NewRoleEngine(rbac.RolesConfig{
		Roles: map[string]rbac.RoleDefinition{
			"delegator": {Scopes: []string{"agent:PeerAgent:invoke"}},
		},
	}, rbac.UsersConfig{}, nil)
	if err != nil {
		t.Fatalf("NewRoleEngine: %v", err)
	}
	svc.roleEngine = engine

	cache := auth.NewProfileCache(time.Hour)
	r := NewIdentityResolver(svc, cache)

	id, err := r.ResolveIdentity(context.Background(), "alice")
	if err != nil {
		t.Fatalf("ResolveIdentity: %v", err)
	}
	if id.UserID() != "alice" {
		t.Fatalf("UserID = %q", id.UserID())
	}

	entry, ok := cache.Get("alice")
	if !ok {
		t.Fatal("expected profile cache to be populated by IdentityResolver")
	}
	if entry.EnrichmentStatus != id.EnrichmentStatus() {
		t.Errorf("cache EnrichmentStatus = %q, want %q", entry.EnrichmentStatus, id.EnrichmentStatus())
	}
}

type stubAuthorizationClient struct {
	res auth.AuthorizationResolution
	err error
}

func (c stubAuthorizationClient) Lookup(context.Context, string, map[string]any) (auth.AuthorizationResolution, error) {
	return c.res, c.err
}

var errLookupTimedOut = errors.New("authz lookup timed out")

func TestIdentityResolver_ResolveIdentityOnlyCallsRefusalsUnauthorized(t *testing.T) {
	cases := []struct {
		name             string
		userID           string
		client           stubAuthorizationClient
		wantUnauthorized bool
		wantLookupErr    bool
		wantUnavailable  bool
	}{
		{"a lookup nothing classifies is not a refusal", "alice", stubAuthorizationClient{err: errLookupTimedOut}, false, true, false},
		{"a lookup the platform failed is a wait, not a refusal", "alice", stubAuthorizationClient{err: fmt.Errorf("authz client: lookup: %w", auth.ErrIdentityLookupUnavailable)}, false, false, true},
		{"a lookup that hit its deadline is a wait, not a refusal", "alice", stubAuthorizationClient{err: context.DeadlineExceeded}, false, false, true},
		{"a user with no effective scopes is refused", "alice", stubAuthorizationClient{res: auth.AuthorizationResolution{Roles: []string{"empty"}}}, true, false, false},
		{"an empty user id is refused", "", stubAuthorizationClient{err: errLookupTimedOut}, true, false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			svc := &AuthorizationService{serviceType: "default_rbac", logger: testLogger()}
			svc.SetAuthorizationClient(c.client)

			_, err := NewIdentityResolver(svc, nil).ResolveIdentity(t.Context(), c.userID)

			if err == nil {
				t.Fatalf("ResolveIdentity(%q) = nil error, want a failure", c.userID)
			}
			if got := errors.Is(err, entrypoint.ErrIdentityUnauthorized); got != c.wantUnauthorized {
				t.Errorf("ResolveIdentity(%q) unauthorized = %v, want %v (err: %v)", c.userID, got, c.wantUnauthorized, err)
			}
			if got := errors.Is(err, errLookupTimedOut); got != c.wantLookupErr {
				t.Errorf("ResolveIdentity(%q) wraps the lookup error = %v, want %v (err: %v)", c.userID, got, c.wantLookupErr, err)
			}
			if got := errors.Is(err, auth.ErrIdentityLookupUnavailable); got != c.wantUnavailable {
				t.Errorf("ResolveIdentity(%q) unavailable = %v, want %v (err: %v)", c.userID, got, c.wantUnavailable, err)
			}
		})
	}
}

func TestIdentityResolver_ResolveRunAsKeepsAFailedLookupApartFromARefusal(t *testing.T) {
	cases := []struct {
		name             string
		client           stubAuthorizationClient
		wantUnauthorized bool
		wantUnavailable  bool
	}{
		{"the platform's lookup failed", stubAuthorizationClient{err: fmt.Errorf("authz client: lookup: %w", auth.ErrIdentityLookupUnavailable)}, false, true},
		{"a lookup nothing classifies", stubAuthorizationClient{err: errLookupTimedOut}, false, false},
		{"no effective scopes", stubAuthorizationClient{res: auth.AuthorizationResolution{SystemUserID: "row-1", SystemUserRow: auth.SystemUserRowFound}}, true, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			svc := &AuthorizationService{serviceType: "default_rbac", logger: testLogger()}
			svc.SetAuthorizationClient(c.client)

			_, err := NewIdentityResolver(svc, nil).ResolveRunAs(t.Context(), "system:nightly")

			if err == nil {
				t.Fatal("ResolveRunAs(system:nightly) = nil error, want a failure")
			}
			if got := errors.Is(err, entrypoint.ErrIdentityUnauthorized); got != c.wantUnauthorized {
				t.Errorf("ResolveRunAs unauthorized = %v, want %v (err: %v)", got, c.wantUnauthorized, err)
			}
			if got := errors.Is(err, auth.ErrIdentityLookupUnavailable); got != c.wantUnavailable {
				t.Errorf("ResolveRunAs unavailable = %v, want %v (err: %v)", got, c.wantUnavailable, err)
			}
		})
	}
}

func TestIdentityResolver_RoleWithEmptyScopes(t *testing.T) {
	svc := &AuthorizationService{
		serviceType: "default_rbac",
		logger:      testLogger(),
		yamlUsers: rbac.UsersConfig{
			Users: map[string]rbac.UserAssignment{
				"alice": {Roles: []string{"empty"}},
			},
		},
	}
	engine, err := rbac.NewRoleEngine(rbac.RolesConfig{
		Roles: map[string]rbac.RoleDefinition{"empty": {Scopes: nil}},
	}, rbac.UsersConfig{}, nil)
	if err != nil {
		t.Fatalf("NewRoleEngine: %v", err)
	}
	svc.roleEngine = engine

	r := NewIdentityResolver(svc, nil)
	_, err = r.ResolveIdentity(context.Background(), "alice")
	if !errors.Is(err, entrypoint.ErrIdentityUnauthorized) {
		t.Errorf("err = %v, want ErrIdentityUnauthorized", err)
	}
}

var degradedReplyRows = []struct {
	name            string
	resolution      identity.LookupResolution
	wantUnavailable bool
}{
	{"the platform replied degraded with the default roles", identity.LookupDegraded, true},
	{"the platform matched the user", identity.LookupMatched, false},
}

func TestIdentityResolver_ResolveIdentityWaitsOnADegradedReplyInsteadOfTrustingItsScopes(t *testing.T) {
	for _, c := range degradedReplyRows {
		t.Run(c.name, func(t *testing.T) {
			svc := &AuthorizationService{serviceType: "default_rbac", logger: testLogger()}
			svc.SetAuthorizationClient(stubAuthorizationClient{res: auth.AuthorizationResolution{
				Scopes: []string{"agent:*:invoke"}, Resolution: c.resolution,
			}})

			id, err := NewIdentityResolver(svc, nil).ResolveIdentity(t.Context(), "alice@example.com")

			if got := errors.Is(err, auth.ErrIdentityLookupUnavailable); got != c.wantUnavailable {
				t.Errorf("ResolveIdentity unavailable = %v, want %v (err: %v)", got, c.wantUnavailable, err)
			}
			if errors.Is(err, entrypoint.ErrIdentityUnauthorized) {
				t.Errorf("ResolveIdentity error %v is an unauthorized owner, want the lookup reported as failed", err)
			}
			if c.wantUnavailable == (id != nil) {
				t.Errorf("ResolveIdentity caller = %v, want a caller only when the reply is not degraded", id)
			}
		})
	}
}

func TestIdentityResolver_ResolveRunAsWaitsOnADegradedReplyInsteadOfTrustingItsScopes(t *testing.T) {
	for _, c := range degradedReplyRows {
		t.Run(c.name, func(t *testing.T) {
			svc := &AuthorizationService{serviceType: "default_rbac", logger: testLogger()}
			svc.SetAuthorizationClient(stubAuthorizationClient{res: auth.AuthorizationResolution{
				Scopes: []string{"agent:*:invoke"}, SystemUserID: "row-1", SystemUserRow: auth.SystemUserRowFound, Resolution: c.resolution,
			}})

			id, err := NewIdentityResolver(svc, nil).ResolveRunAs(t.Context(), "system:nightly")

			if got := errors.Is(err, auth.ErrIdentityLookupUnavailable); got != c.wantUnavailable {
				t.Errorf("ResolveRunAs unavailable = %v, want %v (err: %v)", got, c.wantUnavailable, err)
			}
			if errors.Is(err, entrypoint.ErrIdentityUnauthorized) {
				t.Errorf("ResolveRunAs error %v is a refusal, want the lookup reported as failed", err)
			}
			if c.wantUnavailable == (id != nil) {
				t.Errorf("ResolveRunAs caller = %v, want a caller only when the reply is not degraded", id)
			}
		})
	}
}

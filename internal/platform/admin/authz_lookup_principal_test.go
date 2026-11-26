// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/identity"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

const (
	principalGatewayID   = "whatsapp"
	principalSenderPhone = "15551234567"
	principalPhoneClaims = `{"phone_number":"+15551234567","phone_number_verified":true}`
	principalDirectScope = "agent:direct:invoke"
	principalYAMLScope   = "agent:yaml:invoke"
	principalEmail       = "alice@example.com"
)

type principalFixture struct {
	db         *store.DB
	users      *store.UsersRepo
	rbac       *store.RBACRepo
	composer   *rbac.Composer
	directRole string
	authorizer *auth.BrokerSenderAuthorizer
	client     *auth.BrokerAuthorizationClient
}

func newPrincipalFixture(t *testing.T, ns string) *principalFixture {
	t.Helper()
	ctx := context.Background()
	db := testOpenDB(t)
	f := &principalFixture{db: db, users: store.NewUsersRepo(db), rbac: store.NewRBACRepo(db)}

	role := &store.RBACRole{Name: "direct-role", CreatedBy: "x", UpdatedBy: "x"}
	if err := f.rbac.CreateRole(ctx, role, []string{principalDirectScope}, nil); err != nil {
		t.Fatalf("create direct role: %v", err)
	}
	f.directRole = role.ID

	yaml := rbac.YAMLState{
		Roles: rbac.RolesConfig{Roles: map[string]rbac.RoleDefinition{
			"everyone":  {Scopes: []string{"agent:*:invoke"}},
			"yaml-role": {Scopes: []string{principalYAMLScope}},
		}},
		Users:        rbac.UsersConfig{Users: map[string]rbac.UserAssignment{principalEmail: {Roles: []string{"yaml-role"}}}},
		DefaultRoles: []string{"everyone"},
	}
	f.composer = rbac.NewComposer(yaml, newRBACDBSource(f.rbac), discardLogger())
	if err := f.composer.Rebuild(ctx); err != nil {
		t.Fatalf("composer rebuild: %v", err)
	}

	brk := newTestBroker(t)
	startAuthzHandlerWithResolver(t, brk, f.composer,
		newUsersRepoIdentityResolver(f.users, discardLogger()).withClaimProjection(store.NewUserIdentityClaimsRepo(db)), ns)
	f.client = auth.NewBrokerAuthorizationClient(brk, auth.BrokerAuthorizationClientConfig{
		Namespace: ns,
		GatewayID: principalGatewayID,
	}, discardLogger())
	f.authorizer = auth.NewBrokerSenderAuthorizer(f.client, principalGatewayID)
	return f
}

func (f *principalFixture) seedSender(t *testing.T, email *string) string {
	t.Helper()
	userID := upsertUserWithClaims(t, f.users, &store.User{Sub: "alice-sub", Email: email, Source: principalPhoneClaims})
	if err := f.rbac.AddUserRole(t.Context(), userID, f.directRole, "admin"); err != nil {
		t.Fatalf("grant direct role: %v", err)
	}
	if err := f.composer.Rebuild(t.Context()); err != nil {
		t.Fatalf("composer rebuild: %v", err)
	}
	return userID
}

func (f *principalFixture) authorizeSender(t *testing.T) auth.SenderAuthorization {
	t.Helper()
	got, err := f.authorizer.AuthorizeSender(t.Context(), auth.SenderAuthorizationRequest{
		UserID:     "whatsapp:" + principalSenderPhone,
		ClaimKey:   ClaimKeyPhoneNumber,
		ClaimValue: principalSenderPhone,
	})
	if err != nil {
		t.Fatalf("AuthorizeSender: %v", err)
	}
	if got.Resolution != identity.LookupMatched {
		t.Fatalf("resolution = %q, want matched", got.Resolution)
	}
	return got
}

func TestClaimMatchedSenderResolvesToTheirStoredEmail(t *testing.T) {
	f := newPrincipalFixture(t, "authz-principal-email")
	f.seedSender(t, new("alice@example.com"))

	if got := f.authorizeSender(t).ResolvedPrincipal; got != "alice@example.com" {
		t.Errorf("resolved principal = %q, want the stored email", got)
	}
}

func TestAMatchedSendersRolesAreComposedForTheirEmail(t *testing.T) {
	t.Run("matched to a unique email", func(t *testing.T) {
		f := newPrincipalFixture(t, "authz-principal-yaml")
		f.seedSender(t, new(principalEmail))

		if got := f.authorizeSender(t).Scopes; !slices.Contains(got, principalYAMLScope) {
			t.Errorf("scopes = %v, want the YAML role assigned to %s", got, principalEmail)
		}
	})
	t.Run("offered no principal", func(t *testing.T) {
		f := newPrincipalFixture(t, "authz-principal-yaml-shared")
		f.seedSender(t, new(principalEmail))
		upsertUserWithClaims(t, f.users, &store.User{Sub: "carol-sub", Email: new(principalEmail), Source: `{}`})

		got := f.authorizeSender(t)
		if got.ResolvedPrincipal != "" {
			t.Fatalf("resolved principal = %q, want none while two users hold the email", got.ResolvedPrincipal)
		}
		if slices.Contains(got.Scopes, principalYAMLScope) {
			t.Errorf("scopes = %v carry the YAML role of an email this sender was not given", got.Scopes)
		}
		if !slices.Contains(got.Scopes, principalDirectScope) {
			t.Errorf("scopes = %v, want the matched user's direct grant still", got.Scopes)
		}
	})
}

func TestResolvedPrincipalResolvesBackToTheUserAtFireTime(t *testing.T) {
	f := newPrincipalFixture(t, "authz-principal-roundtrip")
	f.seedSender(t, new("alice@example.com"))
	principal := f.authorizeSender(t).ResolvedPrincipal

	fired, err := f.client.Lookup(t.Context(), principal, nil)
	if err != nil {
		t.Fatalf("fire-time lookup: %v", err)
	}
	if !slices.Contains(fired.Scopes, principalDirectScope) {
		t.Errorf("fire-time scopes for %q = %v, want the user's direct grant %q", principal, fired.Scopes, principalDirectScope)
	}
}

func TestNoPrincipalIsOfferedWithoutAUniqueStoredEmail(t *testing.T) {
	t.Run("no email is stored", func(t *testing.T) {
		f := newPrincipalFixture(t, "authz-principal-no-email")
		f.seedSender(t, nil)

		if got := f.authorizeSender(t).ResolvedPrincipal; got != "" {
			t.Errorf("resolved principal = %q, want none: a bare subject does not resolve back to the user at fire time", got)
		}
	})

	t.Run("another user holds the same email", func(t *testing.T) {
		f := newPrincipalFixture(t, "authz-principal-shared-email")
		f.seedSender(t, new("alice@example.com"))
		upsertUserWithClaims(t, f.users, &store.User{Sub: "carol-sub", Email: new("alice@example.com"), Source: `{}`})

		if got := f.authorizeSender(t).ResolvedPrincipal; got != "" {
			t.Errorf("resolved principal = %q, want none while two users hold the email", got)
		}
	})

	t.Run("the stored email is a username", func(t *testing.T) {
		f := newPrincipalFixture(t, "authz-principal-username")
		f.seedSender(t, new("alice"))

		if got := f.authorizeSender(t).ResolvedPrincipal; got != "" {
			t.Errorf("resolved principal = %q, want none for a value with no @", got)
		}
	})

	t.Run("a legacy mixed-case email that canonicalizes to another user's", func(t *testing.T) {
		f := newPrincipalFixture(t, "authz-principal-legacy-case")
		aliceID := f.seedSender(t, new("alice@example.com"))
		if _, err := f.db.ExecContext(t.Context(), `UPDATE users SET email = ? WHERE id = ?`, "Alice@Example.com", aliceID); err != nil {
			t.Fatalf("write legacy email: %v", err)
		}
		upsertUserWithClaims(t, f.users, &store.User{Sub: "carol-sub", Email: new("alice@example.com"), Source: `{}`})

		if got := f.authorizeSender(t).ResolvedPrincipal; got != "" {
			t.Errorf("resolved principal = %q, want none: the canonical lookup names a different user", got)
		}
	})

	t.Run("the stored email carries the system prefix", func(t *testing.T) {
		f := newPrincipalFixture(t, "authz-principal-system")
		f.seedSender(t, new("system:ops@example.com"))

		if got := f.authorizeSender(t).ResolvedPrincipal; got != "" {
			t.Errorf("resolved principal = %q, want none for a system-prefixed value", got)
		}
	})

	t.Run("the match was on a username claim", func(t *testing.T) {
		const key = "extension_deadbeef_waUsername"
		f := newPrincipalFixture(t, "authz-principal-wa-username")
		upsertUserWithClaims(t, f.users, &store.User{Sub: "erin-sub", Email: new("erin@example.com"), Source: `{"` + key + `":"ErinSmith"}`})

		got, err := f.authorizer.AuthorizeSender(t.Context(), auth.SenderAuthorizationRequest{
			UserID: "whatsapp:US.1349", ClaimKey: key, ClaimValue: "erinsmith",
		})
		if err != nil {
			t.Fatalf("AuthorizeSender: %v", err)
		}
		if got.Resolution != identity.LookupMatched {
			t.Fatalf("resolution = %q, want matched", got.Resolution)
		}
		if got.ResolvedPrincipal != "" {
			t.Errorf("resolved principal = %q, want none for a username match", got.ResolvedPrincipal)
		}
	})
}

func TestAPrincipalReadFaultDegradesBeforeTheBindingMoves(t *testing.T) {
	usersRepo, _, bindings, resolver := bindingFixture(t)
	userID := upsertUserWithClaims(t, usersRepo, &store.User{Sub: "alice-sub", Email: new(principalEmail), Source: principalPhoneClaims})
	if _, err := resolver.Resolve(t.Context(), phoneLookup("wa-gw", principalSenderPhone, "US.old")); err != nil {
		t.Fatalf("seed binding: %v", err)
	}
	if err := bindings.Corroborate(t.Context(), nil, ClaimKeyPhoneNumber, principalSenderPhone, userID, 1); err != nil {
		t.Fatalf("corroborate: %v", err)
	}

	resolver.userIDsByEmail = failingEmailLookup
	res, err := resolver.Resolve(t.Context(), phoneLookup("wa-gw", principalSenderPhone, "US.new"))
	if err == nil {
		t.Error("the read fault was swallowed")
	}
	if res.Outcome != identity.LookupDegraded {
		t.Errorf("outcome = %q, want degraded", res.Outcome)
	}
	assertBindingToken(t, bindings, "US.old")
}

func TestARotationRequestIgnoresAPrincipalReadFault(t *testing.T) {
	usersRepo, _, bindings, resolver := bindingFixture(t)
	userID := upsertUserWithClaims(t, usersRepo, &store.User{Sub: "alice-sub", Email: new(principalEmail), Source: principalPhoneClaims})
	if _, err := resolver.Resolve(t.Context(), phoneLookup("wa-gw", principalSenderPhone, "US.old")); err != nil {
		t.Fatalf("seed binding: %v", err)
	}
	if err := bindings.Corroborate(t.Context(), nil, ClaimKeyPhoneNumber, principalSenderPhone, userID, 1); err != nil {
		t.Fatalf("corroborate: %v", err)
	}

	resolver.userIDsByEmail = failingEmailLookup
	req := phoneLookup("wa-gw", principalSenderPhone, "US.new")
	req.PredecessorToken = "US.old"
	res, err := resolver.Resolve(t.Context(), req)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if res.Outcome != identity.LookupMatched || !res.RotationApplied {
		t.Errorf("outcome = %q, rotationApplied = %v; want the rebind applied", res.Outcome, res.RotationApplied)
	}
	if res.Principal != "" {
		t.Errorf("a rotation request carries principal %q, want none", res.Principal)
	}
	assertBindingToken(t, bindings, "US.new")
}

func failingEmailLookup(context.Context, string) ([]string, error) {
	return nil, errors.New("connection reset")
}

func assertBindingToken(t *testing.T, bindings *store.EntrypointIdentityBindingsRepo, want string) {
	t.Helper()
	row, err := bindings.Get(t.Context(), "wa-gw", ClaimKeyPhoneNumber, principalSenderPhone)
	if err != nil || row == nil {
		t.Fatalf("binding = %v, err = %v; want the seeded row", row, err)
	}
	if row.ContinuityToken != want {
		t.Errorf("continuity token = %q, want %q", row.ContinuityToken, want)
	}
}

func TestRefusedRepliesCarryNoPrincipal(t *testing.T) {
	const ns = "authz-principal-refusals"
	db := testOpenDB(t)
	usersRepo := store.NewUsersRepo(db)
	bindings := store.NewEntrypointIdentityBindingsRepo(db)
	composer := rbac.NewComposer(rbac.YAMLState{}, newRBACDBSource(store.NewRBACRepo(db)), discardLogger())
	if err := composer.Rebuild(t.Context()); err != nil {
		t.Fatalf("composer rebuild: %v", err)
	}
	brk := newTestBroker(t)
	startAuthzHandlerWithResolver(t, brk, composer,
		newUsersRepoIdentityResolver(usersRepo, discardLogger()).
			withClaimProjection(store.NewUserIdentityClaimsRepo(db)).
			withContinuity(bindings), ns)

	aliceID := upsertUserWithClaims(t, usersRepo, &store.User{Sub: "alice-sub", Email: new("alice@example.com"), Source: principalPhoneClaims})
	for _, sub := range []string{"bob-sub", "dan-sub"} {
		upsertUserWithClaims(t, usersRepo, &store.User{Sub: sub, Email: new(sub + "@example.com"),
			Source: `{"phone_number":"+15559990000","phone_number_verified":true}`})
	}

	lookup := func(value, token string) map[string]any {
		t.Helper()
		resp := roundTrip(t, nil, brk, ns, "whatsapp:"+value, map[string]any{
			"gateway_id":                     principalGatewayID,
			gatewayContextIdentityClaimKey:   ClaimKeyPhoneNumber,
			gatewayContextIdentityClaimValue: value,
			gatewayContextContinuityToken:    token,
		})
		if resp.Error != nil {
			t.Fatalf("lookup %s: %+v", value, resp.Error)
		}
		res, _ := resp.Result.(map[string]any)
		return res
	}
	if got := lookup(principalSenderPhone, "US.alice")["resolved_principal"]; got != "alice@example.com" {
		t.Fatalf("control: matched reply principal = %v, want the email", got)
	}
	if err := bindings.Corroborate(t.Context(), nil, ClaimKeyPhoneNumber, principalSenderPhone, aliceID, 1); err != nil {
		t.Fatalf("corroborate: %v", err)
	}

	for _, tt := range []struct {
		name, value, token string
		want               identity.LookupResolution
	}{
		{name: "no match", value: "15550000000", token: "US.nobody", want: identity.LookupNoMatch},
		{name: "ambiguous", value: "15559990000", token: "US.shared", want: identity.LookupAmbiguous},
		{name: "withheld", value: principalSenderPhone, token: "US.stranger", want: identity.LookupRefused},
	} {
		t.Run(tt.name, func(t *testing.T) {
			res := lookup(tt.value, tt.token)
			if got := identity.LookupResolution(fmt.Sprint(res["resolution"])); got != tt.want {
				t.Fatalf("resolution = %q, want %q", got, tt.want)
			}
			if got, present := res["resolved_principal"]; present {
				t.Errorf("a %s reply carries resolved_principal %v", tt.want, got)
			}
		})
	}
}

func TestAWithheldMatchCarriesNoPrincipal(t *testing.T) {
	usersRepo, _, bindings, resolver := bindingFixture(t)
	userID := upsertUserWithClaims(t, usersRepo, &store.User{Sub: "alice-sub", Email: new("alice@example.com"), Source: principalPhoneClaims})

	if res, err := resolver.Resolve(t.Context(), phoneLookup("wa-gw", principalSenderPhone, "US.1111")); err != nil || res.Principal == "" {
		t.Fatalf("first lookup principal = %q, err = %v; want the email", res.Principal, err)
	}
	if err := bindings.Corroborate(t.Context(), nil, ClaimKeyPhoneNumber, principalSenderPhone, userID, 1); err != nil {
		t.Fatalf("corroborate: %v", err)
	}

	res, err := resolver.Resolve(t.Context(), phoneLookup("wa-gw", principalSenderPhone, "US.2222"))
	if err != nil {
		t.Fatalf("rotated lookup: %v", err)
	}
	if res.Outcome != identity.LookupRefused {
		t.Fatalf("outcome = %q, want refused", res.Outcome)
	}
	if res.Principal != "" {
		t.Errorf("a withheld match carries principal %q, want none", res.Principal)
	}
}

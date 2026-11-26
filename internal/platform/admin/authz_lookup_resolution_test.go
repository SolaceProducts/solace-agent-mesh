// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"io"
	"log/slog"
	"slices"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/control"
	"github.com/SolaceProducts/solace-agent-mesh/internal/identity"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

func resolutionFromResult(t *testing.T, resp *control.Response) identity.LookupResolution {
	t.Helper()
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
	res, ok := resp.Result.(map[string]any)
	if !ok {
		t.Fatalf("expected map result, got %T", resp.Result)
	}
	s, _ := res["resolution"].(string)
	return identity.LookupResolution(s)
}

func resolvedUserIDFromResult(t *testing.T, resp *control.Response) string {
	t.Helper()
	res, _ := resp.Result.(map[string]any)
	s, _ := res["resolved_user_id"].(string)
	return s
}

func claimsFixture(t *testing.T) (*rbac.Composer, *store.UsersRepo, *store.UserIdentityClaimsRepo) {
	t.Helper()
	ctx := context.Background()
	db := testOpenDB(t)
	rbacRepo := store.NewRBACRepo(db)
	usersRepo := store.NewUsersRepo(db)
	claimsRepo := store.NewUserIdentityClaimsRepo(db)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	yaml := rbac.YAMLState{
		Roles: rbac.RolesConfig{Roles: map[string]rbac.RoleDefinition{
			"everyone": {Scopes: []string{"agent:*:invoke"}},
			"insider":  {Scopes: []string{"agent:*:invoke", "workflow:*:invoke"}},
		}},
		DefaultRoles: []string{"everyone"},
	}
	composer := rbac.NewComposer(yaml, newRBACDBSource(rbacRepo), logger)
	if err := composer.Rebuild(ctx); err != nil {
		t.Fatalf("composer rebuild: %v", err)
	}
	return composer, usersRepo, claimsRepo
}

func seedClaimUser(t *testing.T, usersRepo *store.UsersRepo, sub, source string) string {
	t.Helper()
	return upsertWithClaims(t, usersRepo, sub, source)
}

func TestGatewayDefaultRolesStillApplyToAnUnresolvedSender(t *testing.T) {
	const ns = "authz-characterize-default"
	brk := newTestBroker(t)
	composer, usersRepo, claimsRepo := claimsFixture(t)
	startAuthzHandlerWithResolver(t, brk, composer,
		newUsersRepoIdentityResolver(usersRepo, discardLogger()).withClaimProjection(claimsRepo), ns)

	resp := roundTrip(t, nil, brk, ns, "whatsapp:15550000000", map[string]any{
		"gateway_id":                     "whatsapp",
		gatewayContextIdentityClaimKey:   ClaimKeyPhoneNumber,
		gatewayContextIdentityClaimValue: "15550000000",
	})

	if roles := rolesFromResult(t, resp); !slices.Contains(roles, "everyone") {
		t.Errorf("roles for an unresolved sender = %v, want the defaults still applied — this slice must not change behaviour", roles)
	}
	if got := resolutionFromResult(t, resp); got != identity.LookupNoMatch {
		t.Errorf("resolution = %q, want %q — the miss has to become visible even though the roles do not change", got, identity.LookupNoMatch)
	}
}

func TestResolutionIsAbsentWhenNoLookupRan(t *testing.T) {
	const ns = "authz-resolution-absent"
	brk := newTestBroker(t)
	composer, _, _ := claimsFixture(t)
	startAuthzHandler(t, brk, composer, ns)

	resp := roundTrip(t, nil, brk, ns, "whatsapp:15550000000", map[string]any{"gateway_id": "whatsapp"})
	if got := resolutionFromResult(t, resp); got != identity.LookupUnknown {
		t.Errorf("resolution with no resolver wired = %q, want it absent — a consumer must not read this as a denial", got)
	}
}

func TestFieldAgnosticLookup(t *testing.T) {
	t.Run("a verified claim resolves to its user", func(t *testing.T) {
		brk := newTestBroker(t)
		composer, usersRepo, claimsRepo := claimsFixture(t)
		startAuthzHandlerWithResolver(t, brk, composer,
			newUsersRepoIdentityResolver(usersRepo, discardLogger()).withClaimProjection(claimsRepo), "fa-match")
		userID := seedClaimUser(t, usersRepo, "alice",
			`{"phone_number":"+15551234567","phone_number_verified":true}`)

		resp := roundTrip(t, nil, brk, "fa-match", "whatsapp:15551234567", map[string]any{
			"gateway_id":                     "whatsapp",
			gatewayContextIdentityClaimKey:   ClaimKeyPhoneNumber,
			gatewayContextIdentityClaimValue: "15551234567",
		})
		if got := resolutionFromResult(t, resp); got != identity.LookupMatched {
			t.Fatalf("resolution = %q, want matched", got)
		}
		if got := resolvedUserIDFromResult(t, resp); got != userID {
			t.Errorf("resolved_user_id = %q, want %q", got, userID)
		}
	})

	t.Run("a wa_id matches an E.164 claim through the canonical form", func(t *testing.T) {
		brk := newTestBroker(t)
		composer, usersRepo, claimsRepo := claimsFixture(t)
		startAuthzHandlerWithResolver(t, brk, composer,
			newUsersRepoIdentityResolver(usersRepo, discardLogger()).withClaimProjection(claimsRepo), "fa-e164")
		seedClaimUser(t, usersRepo, "bob", `{"phone_number":"+1 (555) 987-6543","phone_number_verified":true}`)

		resp := roundTrip(t, nil, brk, "fa-e164", "whatsapp:15559876543", map[string]any{
			"gateway_id":                     "whatsapp",
			gatewayContextIdentityClaimKey:   ClaimKeyPhoneNumber,
			gatewayContextIdentityClaimValue: "15559876543",
		})
		if got := resolutionFromResult(t, resp); got != identity.LookupMatched {
			t.Errorf("resolution = %q, want matched — the wa_id and the claim spell one number differently", got)
		}
	})

	t.Run("a national-format value does not match an international claim", func(t *testing.T) {
		brk := newTestBroker(t)
		composer, usersRepo, claimsRepo := claimsFixture(t)
		startAuthzHandlerWithResolver(t, brk, composer,
			newUsersRepoIdentityResolver(usersRepo, discardLogger()).withClaimProjection(claimsRepo), "fa-national")
		seedClaimUser(t, usersRepo, "carol", `{"phone_number":"+15551112222","phone_number_verified":true}`)

		resp := roundTrip(t, nil, brk, "fa-national", "whatsapp:5551112222", map[string]any{
			"gateway_id":                     "whatsapp",
			gatewayContextIdentityClaimKey:   ClaimKeyPhoneNumber,
			gatewayContextIdentityClaimValue: "5551112222",
		})
		if got := resolutionFromResult(t, resp); got != identity.LookupNoMatch {
			t.Errorf("resolution = %q, want no_match — matching here means a candidate set crept back in", got)
		}
	})

	t.Run("two holders of one value refuse as ambiguous", func(t *testing.T) {
		brk := newTestBroker(t)
		composer, usersRepo, claimsRepo := claimsFixture(t)
		startAuthzHandlerWithResolver(t, brk, composer,
			newUsersRepoIdentityResolver(usersRepo, discardLogger()).withClaimProjection(claimsRepo), "fa-ambiguous")
		const source = `{"phone_number":"+15553334444","phone_number_verified":true}`
		seedClaimUser(t, usersRepo, "leaver", source)
		seedClaimUser(t, usersRepo, "joiner", source)

		resp := roundTrip(t, nil, brk, "fa-ambiguous", "whatsapp:15553334444", map[string]any{
			"gateway_id":                     "whatsapp",
			gatewayContextIdentityClaimKey:   ClaimKeyPhoneNumber,
			gatewayContextIdentityClaimValue: "15553334444",
		})
		if got := resolutionFromResult(t, resp); got != identity.LookupAmbiguous {
			t.Errorf("resolution = %q, want ambiguous — a recycled identifier must never resolve to one of its holders", got)
		}
	})

	t.Run("an unverified claim does not resolve", func(t *testing.T) {
		brk := newTestBroker(t)
		composer, usersRepo, claimsRepo := claimsFixture(t)
		startAuthzHandlerWithResolver(t, brk, composer,
			newUsersRepoIdentityResolver(usersRepo, discardLogger()).withClaimProjection(claimsRepo), "fa-unverified")
		seedClaimUser(t, usersRepo, "dave", `{"phone_number":"+15555556666","phone_number_verified":false}`)

		resp := roundTrip(t, nil, brk, "fa-unverified", "whatsapp:15555556666", map[string]any{
			"gateway_id":                     "whatsapp",
			gatewayContextIdentityClaimKey:   ClaimKeyPhoneNumber,
			gatewayContextIdentityClaimValue: "15555556666",
		})
		if got := resolutionFromResult(t, resp); got != identity.LookupNoMatch {
			t.Errorf("resolution = %q, want no_match — an unverified phone must never authorize", got)
		}
	})

	t.Run("a custom claim resolves with no deployment list", func(t *testing.T) {
		const key = "extension_deadbeef_waUsername"
		brk := newTestBroker(t)
		composer, usersRepo, claimsRepo := claimsFixture(t)
		startAuthzHandlerWithResolver(t, brk, composer,
			newUsersRepoIdentityResolver(usersRepo, discardLogger()).withClaimProjection(claimsRepo), "fa-custom")
		userID := upsertWithClaims(t, usersRepo, "erin", `{"`+key+`":"AliceSmith"}`)

		resp := roundTrip(t, nil, brk, "fa-custom", "whatsapp:US.1349", map[string]any{
			"gateway_id":                     "whatsapp",
			gatewayContextIdentityClaimKey:   key,
			gatewayContextIdentityClaimValue: "alicesmith",
		})
		if got := resolutionFromResult(t, resp); got != identity.LookupMatched {
			t.Fatalf("resolution = %q, want matched", got)
		}
		if got := resolvedUserIDFromResult(t, resp); got != userID {
			t.Errorf("resolved_user_id = %q, want %q", got, userID)
		}
	})

	t.Run("a custom claim matches case-insensitively", func(t *testing.T) {
		const key = "extension_deadbeef_waUsername"
		brk := newTestBroker(t)
		composer, usersRepo, claimsRepo := claimsFixture(t)
		startAuthzHandlerWithResolver(t, brk, composer,
			newUsersRepoIdentityResolver(usersRepo, discardLogger()).withClaimProjection(claimsRepo), "fa-case")
		upsertWithClaims(t, usersRepo, "frank", `{"`+key+`":"AliceSmith"}`)

		resp := roundTrip(t, nil, brk, "fa-case", "whatsapp:US.1349", map[string]any{
			"gateway_id":                     "whatsapp",
			gatewayContextIdentityClaimKey:   key,
			gatewayContextIdentityClaimValue: "ALICESMITH",
		})
		if got := resolutionFromResult(t, resp); got != identity.LookupMatched {
			t.Errorf("resolution = %q, want matched", got)
		}
	})
}

func TestACustomClaimKeyResolvesEndToEnd(t *testing.T) {
	const claimKey = "whatsapp_username"
	const source = `{"whatsapp_username":"pat.morgan.acme"}`

	t.Run("it projects and resolves", func(t *testing.T) {
		brk := newTestBroker(t)
		composer, usersRepo, claimsRepo := claimsFixture(t)
		startAuthzHandlerWithResolver(t, brk, composer,
			newUsersRepoIdentityResolver(usersRepo, discardLogger()).withClaimProjection(claimsRepo), "custom-listed")
		userID := upsertWithClaims(t, usersRepo, "pat", source)

		resp := roundTrip(t, nil, brk, "custom-listed", "whatsapp:pat.morgan.acme", map[string]any{
			"gateway_id":                     "whatsapp",
			gatewayContextIdentityClaimKey:   claimKey,
			gatewayContextIdentityClaimValue: "pat.morgan.acme",
		})
		if got := resolutionFromResult(t, resp); got != identity.LookupMatched {
			t.Fatalf("resolution = %q, want matched — a listed custom claim key must resolve", got)
		}
		if got := resolvedUserIDFromResult(t, resp); got != userID {
			t.Errorf("resolved_user_id = %q, want %q", got, userID)
		}
	})

	t.Run("listed with no verification companion claim", func(t *testing.T) {
		_, usersRepo, claimsRepo := claimsFixture(t)
		upsertWithClaims(t, usersRepo, "pat", source)

		ids, err := claimsRepo.UserIDsByClaim(t.Context(), claimKey, "pat.morgan.acme")
		if err != nil {
			t.Fatalf("lookup: %v", err)
		}
		if len(ids) != 1 {
			t.Errorf("holders = %v, want exactly one — the row must be stored verified", ids)
		}
	})

	t.Run("listed and matched case-insensitively", func(t *testing.T) {
		brk := newTestBroker(t)
		composer, usersRepo, claimsRepo := claimsFixture(t)
		startAuthzHandlerWithResolver(t, brk, composer,
			newUsersRepoIdentityResolver(usersRepo, discardLogger()).withClaimProjection(claimsRepo), "custom-folded")
		upsertWithClaims(t, usersRepo, "pat", `{"whatsapp_username":"Pat.Morgan.ACME"}`)

		resp := roundTrip(t, nil, brk, "custom-folded", "whatsapp:pat.morgan.acme", map[string]any{
			"gateway_id":                     "whatsapp",
			gatewayContextIdentityClaimKey:   claimKey,
			gatewayContextIdentityClaimValue: "pat.morgan.acme",
		})
		if got := resolutionFromResult(t, resp); got != identity.LookupMatched {
			t.Errorf("resolution = %q, want matched — the claim and the handle must fold to one form", got)
		}
	})

}

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
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/identity"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

const (
	claimUserEmail  = "claimant@example.com"
	claimGrantRole  = "direct-role"
	claimMappedRole = "claim-role"
	liveMappedRole  = "live-claim-role"
)

func dbComposerWithClaimMapping(t *testing.T, provider, storedSource string) (*rbac.Composer, *store.UsersRepo, *store.DB, string) {
	t.Helper()
	ctx := context.Background()
	db := testOpenDB(t)
	rbacRepo := store.NewRBACRepo(db)
	usersRepo := store.NewUsersRepo(db)

	directRole := &store.RBACRole{Name: claimGrantRole, CreatedBy: "x", UpdatedBy: "x"}
	if err := rbacRepo.CreateRole(ctx, directRole, []string{"agent:direct:invoke"}, nil); err != nil {
		t.Fatalf("create direct role: %v", err)
	}
	mappedRole := &store.RBACRole{Name: claimMappedRole, CreatedBy: "x", UpdatedBy: "x"}
	if err := rbacRepo.CreateRole(ctx, mappedRole, []string{"agent:mapped:invoke"}, nil); err != nil {
		t.Fatalf("create mapped role: %v", err)
	}
	if err := rbacRepo.CreateClaimMapping(ctx, &store.RBACIdpClaimMapping{
		OIDCProvider: provider,
		ClaimKey:     "groups",
		ClaimValue:   "finance-admins",
		Name:         "finance admins",
		CreatedBy:    "x",
		UpdatedBy:    "x",
	}, []string{mappedRole.ID}); err != nil {
		t.Fatalf("create claim mapping: %v", err)
	}

	liveRole := &store.RBACRole{Name: liveMappedRole, CreatedBy: "x", UpdatedBy: "x"}
	if err := rbacRepo.CreateRole(ctx, liveRole, []string{"agent:live:invoke"}, nil); err != nil {
		t.Fatalf("create live role: %v", err)
	}
	if err := rbacRepo.CreateClaimMapping(ctx, &store.RBACIdpClaimMapping{
		OIDCProvider: provider,
		ClaimKey:     "groups",
		ClaimValue:   "engineering",
		Name:         "engineering",
		CreatedBy:    "x",
		UpdatedBy:    "x",
	}, []string{liveRole.ID}); err != nil {
		t.Fatalf("create live claim mapping: %v", err)
	}

	email := claimUserEmail
	u := &store.User{
		Issuer:       "iss",
		Sub:          "claimant",
		OIDCProvider: "azure",
		Email:        &email,
		Source:       storedSource,
		CreatedBy:    "x",
		UpdatedBy:    "x",
	}
	if err := usersRepo.UpsertUser(ctx, u, nil); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if err := rbacRepo.AddUserRole(ctx, u.ID, directRole.ID, "admin"); err != nil {
		t.Fatalf("grant direct role: %v", err)
	}

	composer := rbac.NewComposer(rbac.YAMLState{}, newRBACDBSource(rbacRepo), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := composer.Rebuild(ctx); err != nil {
		t.Fatalf("composer rebuild: %v", err)
	}
	return composer, usersRepo, db, u.ID
}

const financeAdminClaims = `{"sub":"claimant","groups":["finance-admins"],"iat":1700000000}`

const rowOwnerClaimEmail = "row-owner@example.com"
const financeAdminClaimsWithEmail = `{"sub":"claimant","email":"row-owner@example.com","groups":["finance-admins"],"iat":1700000000}`

type recordingIdentityService struct {
	mu     sync.Mutex
	claims map[string]any
}

func (s *recordingIdentityService) Resolve(_ context.Context, claims map[string]any) (*identity.Resolution, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.claims = claims
	return &identity.Resolution{EnrichmentStatus: identity.EnrichmentOK}, nil
}

func (s *recordingIdentityService) seenClaims() map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.claims
}

func (*recordingIdentityService) SearchUsers(context.Context, string, int) ([]*identity.Profile, error) {
	return nil, nil
}
func (*recordingIdentityService) GetUserProfile(context.Context, string) (map[string]any, error) {
	return nil, nil
}
func (*recordingIdentityService) GetTimeOff(context.Context, string, string, string) ([]identity.TimeOffEntry, error) {
	return nil, nil
}
func (*recordingIdentityService) GetUserProfilePicture(context.Context, string) ([]byte, string, error) {
	return nil, "", nil
}

func TestAuthzLookupHandler_EnrichmentReadsResolvedClaims(t *testing.T) {
	const ns = "rbac-enrichment-resolved-claims"
	brk := newTestBroker(t)
	composer, usersRepo, _, _ := dbComposerWithClaimMapping(t, "azure", financeAdminClaimsWithEmail)

	idSvc := &recordingIdentityService{}
	h := NewAuthzLookupHandler(composer, idSvc, brk,
		AuthzLookupConfig{Namespace: ns, CacheTTL: time.Minute}, discardLogger())
	h.SetUserIdentityResolver(newUsersRepoIdentityResolver(usersRepo, discardLogger()))
	if err := h.Start(context.Background()); err != nil {
		t.Fatalf("start handler: %v", err)
	}
	t.Cleanup(func() { _ = h.Stop(context.Background()) })
	time.Sleep(20 * time.Millisecond)

	roundTrip(t, nil, brk, ns, claimUserEmail, map[string]any{"gateway_id": "slack"})

	seen := idSvc.seenClaims()
	if seen == nil {
		t.Fatal("profile enrichment was never called")
	}
	if got, _ := seen["email"].(string); got != rowOwnerClaimEmail {
		t.Errorf("enrichment saw email = %q, want %q (the stored row's, not the request's); claims = %v",
			got, rowOwnerClaimEmail, seen)
	}
}

func TestAuthzLookupHandler_NilContextResolvesClaimRoles(t *testing.T) {
	const ns = "rbac-claims-nil-context"
	brk := newTestBroker(t)
	composer, usersRepo, _, _ := dbComposerWithClaimMapping(t, "azure", financeAdminClaims)
	startAuthzHandlerWithResolver(t, brk, composer, newUsersRepoIdentityResolver(usersRepo, discardLogger()), ns)

	roles := rolesFromResult(t, roundTrip(t, nil, brk, ns, claimUserEmail, nil))
	if !slices.Contains(roles, claimMappedRole) {
		t.Errorf("a nil gateway context must still resolve claim-mapped roles from the stored claims; roles = %v", roles)
	}
	if !slices.Contains(roles, claimGrantRole) {
		t.Errorf("the direct grant must keep applying alongside the claim role; roles = %v", roles)
	}
}

func TestAuthzLookupHandler_ClaimProviderMustMatch(t *testing.T) {
	const ns = "rbac-claims-provider-mismatch"
	brk := newTestBroker(t)
	composer, usersRepo, _, _ := dbComposerWithClaimMapping(t, "okta", financeAdminClaims)
	startAuthzHandlerWithResolver(t, brk, composer, newUsersRepoIdentityResolver(usersRepo, discardLogger()), ns)

	roles := rolesFromResult(t, roundTrip(t, nil, brk, ns, claimUserEmail, nil))
	if slices.Contains(roles, claimMappedRole) {
		t.Errorf("a mapping under a different provider must not apply; roles = %v", roles)
	}
	if !slices.Contains(roles, claimGrantRole) {
		t.Errorf("the direct grant is unaffected by the provider mismatch; roles = %v", roles)
	}
}

func TestAuthzLookupHandler_LiveClaimsAreNotClobbered(t *testing.T) {
	const ns = "rbac-claims-live-wins"
	brk := newTestBroker(t)
	composer, usersRepo, _, _ := dbComposerWithClaimMapping(t, "azure", financeAdminClaims)
	startAuthzHandlerWithResolver(t, brk, composer, newUsersRepoIdentityResolver(usersRepo, discardLogger()), ns)

	live := map[string]any{
		"gateway_id":    "webui",
		"oidc_provider": "azure",
		"idp_claims":    map[string]any{"groups": []any{"engineering"}},
	}
	roles := rolesFromResult(t, roundTrip(t, nil, brk, ns, claimUserEmail, live))
	if !slices.Contains(roles, liveMappedRole) {
		t.Errorf("the request's live claims must resolve their own mappings; roles = %v", roles)
	}
	if slices.Contains(roles, claimMappedRole) {
		t.Errorf("stored claims must not override the request's live claims; roles = %v", roles)
	}
	if !slices.Contains(roles, claimGrantRole) {
		t.Errorf("resolution still applies the direct grant when live claims are present; roles = %v", roles)
	}
}

func TestAuthzLookupHandler_UnverifiedEmailIsNotEnriched(t *testing.T) {
	const ns = "rbac-unverified-no-enrich"
	brk := newTestBroker(t)
	composer, usersRepo, _, _ := dbComposerWithClaimMapping(t, "azure", financeAdminClaimsWithEmail)

	idSvc := &recordingIdentityService{}
	h := NewAuthzLookupHandler(composer, idSvc, brk,
		AuthzLookupConfig{Namespace: ns, CacheTTL: time.Minute}, discardLogger())
	h.SetUserIdentityResolver(newUsersRepoIdentityResolver(usersRepo, discardLogger()))
	if err := h.Start(context.Background()); err != nil {
		t.Fatalf("start handler: %v", err)
	}
	t.Cleanup(func() { _ = h.Stop(context.Background()) })
	time.Sleep(20 * time.Millisecond)

	unverified := map[string]any{
		"gateway_id": "webui",
		"idp_claims": map[string]any{"email": claimUserEmail, "email_verified": false},
	}
	resp := roundTrip(t, nil, brk, ns, claimUserEmail, unverified)
	if resp.Error != nil {
		t.Fatalf("the lookup must still reply: %+v", resp.Error)
	}
	if seen := idSvc.seenClaims(); seen != nil {
		t.Errorf("an unverified email must never reach the enricher; claims = %v", seen)
	}
	res := resp.Result.(map[string]any)
	if res["enrichment_status"] != string(identity.EnrichmentUnavailable) {
		t.Errorf("enrichment_status = %v, want unavailable", res["enrichment_status"])
	}
}

func TestAuthzLookupHandler_MappedClaimChangeDrivesTheCacheFlush(t *testing.T) {
	const ns = "rbac-claims-flush-gate"
	brk := newTestBroker(t)
	composer, usersRepo, _, _ := dbComposerWithClaimMapping(t, "azure", financeAdminClaims)
	resolver := &countingResolver{delegate: newUsersRepoIdentityResolver(usersRepo, discardLogger())}
	lookupHandler := startAuthzHandlerWithResolver(t, brk, composer, resolver, ns)
	startUpsertHandlerWithLookupRefresh(t, brk, usersRepo, composer, lookupHandler, ns)

	if roles := rolesFromResult(t, roundTrip(t, nil, brk, ns, claimUserEmail, nil)); !slices.Contains(roles, claimMappedRole) {
		t.Fatalf("precondition: the stored claim must resolve and populate the cache; roles = %v", roles)
	}

	relogin := func(t *testing.T, source string) {
		t.Helper()
		resp := upsertRoundTrip(t, brk, ns, "test-gw", userUpsertRequest{
			Issuer: "iss", Sub: "claimant", OIDCProvider: "azure",
			Email: claimUserEmail, Source: source,
		})
		if resp.Error != nil {
			t.Fatalf("upsert: %+v", resp.Error)
		}
	}

	relogin(t, `{"sub":"claimant","groups":["finance-admins"],"iat":1700009999}`)
	if roles := rolesFromResult(t, roundTrip(t, nil, brk, ns, claimUserEmail, nil)); !slices.Contains(roles, claimMappedRole) {
		t.Errorf("the claim-mapped role must survive an unchanged login; roles = %v", roles)
	}
	if got := resolver.calls.Load(); got != 1 {
		t.Errorf("a login that moves only iat must not flush: resolver called %d times, want 1", got)
	}

	relogin(t, `{"sub":"claimant","groups":["engineering"],"iat":1700009999}`)
	roles := rolesFromResult(t, roundTrip(t, nil, brk, ns, claimUserEmail, nil))
	if slices.Contains(roles, claimMappedRole) {
		t.Errorf("the old group's role must stop resolving once the mapped claim moved; roles = %v", roles)
	}
	if !slices.Contains(roles, liveMappedRole) {
		t.Errorf("the new group's role must resolve after the flush; roles = %v", roles)
	}
}

func TestAuthzLookupHandler_LiveClaimsMergeRules(t *testing.T) {
	const ns = "rbac-claims-merge-rules"
	brk := newTestBroker(t)
	composer, usersRepo, _, _ := dbComposerWithClaimMapping(t, "azure", financeAdminClaims)
	startAuthzHandlerWithResolver(t, brk, composer, newUsersRepoIdentityResolver(usersRepo, discardLogger()), ns)

	for _, tt := range []struct {
		name           string
		gatewayContext map[string]any
		want           []string
		notWant        []string
	}{
		{
			name:           "null claims fall back to the stored set",
			gatewayContext: map[string]any{"gateway_id": "mcp", "idp_claims": nil},
			want:           []string{claimMappedRole, claimGrantRole},
			notWant:        []string{liveMappedRole},
		},
		{
			name:           "empty claims fall back to the stored set",
			gatewayContext: map[string]any{"gateway_id": "mcp", "idp_claims": map[string]any{}},
			want:           []string{claimMappedRole, claimGrantRole},
			notWant:        []string{liveMappedRole},
		},
		{
			name: "a live provider is not replaced by the stored one",
			gatewayContext: map[string]any{
				"gateway_id":    "webui",
				"oidc_provider": "okta",
				"idp_claims":    map[string]any{"groups": []any{"engineering"}},
			},
			want:    []string{claimGrantRole},
			notWant: []string{liveMappedRole, claimMappedRole},
		},
		{
			name: "live claims without a provider resolve no mappings",
			gatewayContext: map[string]any{
				"gateway_id": "webui",
				"idp_claims": map[string]any{"groups": []any{"engineering"}},
			},
			want:    []string{claimGrantRole},
			notWant: []string{liveMappedRole, claimMappedRole},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			roles := rolesFromResult(t, roundTrip(t, nil, brk, ns, claimUserEmail, tt.gatewayContext))
			for _, want := range tt.want {
				if !slices.Contains(roles, want) {
					t.Errorf("missing role %q; roles = %v", want, roles)
				}
			}
			for _, notWant := range tt.notWant {
				if slices.Contains(roles, notWant) {
					t.Errorf("unexpected role %q; roles = %v", notWant, roles)
				}
			}
		})
	}
}

func TestAuthzLookupHandler_UnparseableStoredClaimsKeepsDirectGrant(t *testing.T) {
	const ns = "rbac-claims-corrupt"
	brk := newTestBroker(t)
	composer, usersRepo, _, _ := dbComposerWithClaimMapping(t, "azure", `{"groups":`)
	startAuthzHandlerWithResolver(t, brk, composer, newUsersRepoIdentityResolver(usersRepo, discardLogger()), ns)

	roles := rolesFromResult(t, roundTrip(t, nil, brk, ns, claimUserEmail, nil))
	if !slices.Contains(roles, claimGrantRole) {
		t.Errorf("corrupt claims must cost only the claim-derived roles, never the direct grant; roles = %v", roles)
	}
	if slices.Contains(roles, claimMappedRole) {
		t.Errorf("claims that cannot be parsed cannot map to a role; roles = %v", roles)
	}
}

func TestAuthzLookupHandler_RefusedRowResolvesNoClaims(t *testing.T) {
	const ns = "rbac-claims-deactivated"
	brk := newTestBroker(t)
	composer, usersRepo, db, userID := dbComposerWithClaimMapping(t, "azure", financeAdminClaims)

	if _, err := db.ExecContext(context.Background(), `UPDATE users SET active = ? WHERE id = ?`, 0, userID); err != nil {
		t.Fatalf("deactivate user: %v", err)
	}
	startAuthzHandlerWithResolver(t, brk, composer, newUsersRepoIdentityResolver(usersRepo, discardLogger()), ns)

	roles := rolesFromResult(t, roundTrip(t, nil, brk, ns, claimUserEmail, nil))
	if slices.Contains(roles, claimMappedRole) || slices.Contains(roles, claimGrantRole) {
		t.Errorf("a deactivated row must resolve to nothing, claims included; roles = %v", roles)
	}
}

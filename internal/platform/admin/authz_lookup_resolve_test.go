// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/control"
	"github.com/SolaceProducts/solace-agent-mesh/internal/identity"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

func dbComposerWithGrant(t *testing.T) (composer *rbac.Composer, usersRepo *store.UsersRepo, email, canonicalID string) {
	t.Helper()
	ctx := context.Background()
	db := testOpenDB(t)
	rbacRepo := store.NewRBACRepo(db)
	usersRepo = store.NewUsersRepo(db)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	role := &store.RBACRole{Name: "chat-role", CreatedBy: "x", UpdatedBy: "x"}
	if err := rbacRepo.CreateRole(ctx, role, []string{"agent:*:invoke"}, nil); err != nil {
		t.Fatalf("create role: %v", err)
	}
	email = "grantee@example.com"
	u := &store.User{Issuer: "iss", Sub: "grantee", Email: &email, CreatedBy: "x", UpdatedBy: "x"}
	if err := usersRepo.UpsertUser(ctx, u, nil); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if err := rbacRepo.AddUserRole(ctx, u.ID, role.ID, "admin"); err != nil {
		t.Fatalf("grant role: %v", err)
	}
	composer = rbac.NewComposer(rbac.YAMLState{}, newRBACDBSource(rbacRepo), logger)
	if err := composer.Rebuild(ctx); err != nil {
		t.Fatalf("composer rebuild: %v", err)
	}
	return composer, usersRepo, email, u.ID
}

func startAuthzHandlerWithResolver(t *testing.T, brk broker.Broker, composer *rbac.Composer, resolver userIdentityResolver, namespace string) *AuthzLookupHandler {
	t.Helper()
	return startAuthzHandlerWithConfig(t, brk, composer, resolver,
		AuthzLookupConfig{Namespace: namespace, CacheTTL: time.Minute})
}

func startAuthzHandlerWithConfig(t *testing.T, brk broker.Broker, composer *rbac.Composer, resolver userIdentityResolver, cfg AuthzLookupConfig) *AuthzLookupHandler {
	t.Helper()
	h := NewAuthzLookupHandler(composer, nil, brk, cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	h.SetUserIdentityResolver(resolver)
	if err := h.Start(context.Background()); err != nil {
		t.Fatalf("start handler: %v", err)
	}
	t.Cleanup(func() { _ = h.Stop(context.Background()) })
	time.Sleep(20 * time.Millisecond)
	return h
}

func rolesFromResult(t *testing.T, resp *control.Response) []string {
	t.Helper()
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
	res, ok := resp.Result.(map[string]any)
	if !ok {
		t.Fatalf("expected map result, got %T", resp.Result)
	}
	raw, _ := res["roles"].([]any)
	out := make([]string, 0, len(raw))
	for _, r := range raw {
		if s, ok := r.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func TestAuthzLookupHandler_ChatEmailResolvesToDBGrant(t *testing.T) {
	const ns = "rbac-chat-resolve"
	brk := newTestBroker(t)
	composer, usersRepo, email, _ := dbComposerWithGrant(t)
	startAuthzHandlerWithResolver(t, brk, composer, newUsersRepoIdentityResolver(usersRepo, discardLogger()), ns)

	resp := roundTrip(t, nil, brk, ns, email, map[string]any{"gateway_id": "slack"})
	if roles := rolesFromResult(t, resp); !slices.Contains(roles, "chat-role") {
		t.Errorf("chat email should resolve to the id-keyed grant; roles = %v, want to contain chat-role", roles)
	}
}

func TestAuthzLookupHandler_ChatEmailWithoutResolverMissesGrant(t *testing.T) {
	const ns = "rbac-chat-noresolver"
	brk := newTestBroker(t)
	composer, _, email, _ := dbComposerWithGrant(t)
	startAuthzHandler(t, brk, composer, ns)

	resp := roundTrip(t, nil, brk, ns, email, map[string]any{"gateway_id": "slack"})
	if roles := rolesFromResult(t, resp); slices.Contains(roles, "chat-role") {
		t.Errorf("without a resolver the DB grant must not apply; roles = %v", roles)
	}
}

func TestAuthzLookupHandler_UnverifiedEmailDoesNotResolve(t *testing.T) {
	const ns = "rbac-unverified-email"
	brk := newTestBroker(t)
	composer, usersRepo, email, _ := dbComposerWithGrant(t)
	startAuthzHandlerWithResolver(t, brk, composer, newUsersRepoIdentityResolver(usersRepo, discardLogger()), ns)

	for _, tt := range []struct {
		name         string
		emailClaims  map[string]any
		wantResolved bool
	}{
		{
			name:         "explicit false refuses the attribute",
			emailClaims:  map[string]any{"email": email, "email_verified": false},
			wantResolved: false,
		},
		{
			name:         "non-canonical truthy string is not verified",
			emailClaims:  map[string]any{"email": email, "email_verified": "False"},
			wantResolved: false,
		},
		{
			name:         "a present non-bool non-string value is not verified",
			emailClaims:  map[string]any{"email": email, "email_verified": float64(0)},
			wantResolved: false,
		},
		{
			name:         "explicit true resolves",
			emailClaims:  map[string]any{"email": email, "email_verified": true},
			wantResolved: true,
		},
		{
			name:         "absent claim resolves (Entra never sends it)",
			emailClaims:  map[string]any{"email": email},
			wantResolved: true,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			gatewayContext := map[string]any{"gateway_id": "webui", "idp_claims": tt.emailClaims}
			roles := rolesFromResult(t, roundTrip(t, nil, brk, ns, email, gatewayContext))
			resolved := slices.Contains(roles, "chat-role")
			if resolved != tt.wantResolved {
				t.Errorf("grant applied = %v, want %v; roles = %v", resolved, tt.wantResolved, roles)
			}
		})
	}
}

func TestAuthzLookupHandler_SamUserIDPresentSkipsResolution(t *testing.T) {
	const ns = "rbac-webui-skip"
	brk := newTestBroker(t)
	composer, _, email, canonicalID := dbComposerWithGrant(t)
	startAuthzHandlerWithResolver(t, brk, composer, misdirectingResolver{}, ns)

	resp := roundTrip(t, nil, brk, ns, email, map[string]any{"gateway_id": "webui", "sam_user_id": canonicalID})
	if roles := rolesFromResult(t, resp); !slices.Contains(roles, "chat-role") {
		t.Errorf("a request carrying sam_user_id must skip resolution and keep the grant; roles = %v", roles)
	}
}

func TestAuthzLookupHandler_BlankSamUserIDStillResolves(t *testing.T) {
	const ns = "rbac-blank-samuserid"
	brk := newTestBroker(t)
	composer, usersRepo, email, _ := dbComposerWithGrant(t)
	startAuthzHandlerWithResolver(t, brk, composer, newUsersRepoIdentityResolver(usersRepo, discardLogger()), ns)

	resp := roundTrip(t, nil, brk, ns, email, map[string]any{"gateway_id": "slack", "sam_user_id": ""})
	if roles := rolesFromResult(t, resp); !slices.Contains(roles, "chat-role") {
		t.Errorf("a blank sam_user_id must not suppress resolution; roles = %v, want to contain chat-role", roles)
	}
}

func TestAuthzLookupHandler_UnknownChatEmailFallsThrough(t *testing.T) {
	const ns = "rbac-chat-unknown"
	brk := newTestBroker(t)
	composer, usersRepo, _, _ := dbComposerWithGrant(t)
	startAuthzHandlerWithResolver(t, brk, composer, newUsersRepoIdentityResolver(usersRepo, discardLogger()), ns)

	resp := roundTrip(t, nil, brk, ns, "stranger@example.com", map[string]any{"gateway_id": "slack"})
	if roles := rolesFromResult(t, resp); slices.Contains(roles, "chat-role") {
		t.Errorf("an unknown email must not receive the grant; roles = %v", roles)
	}
}

func TestAuthzLookupHandler_ResolverErrorFallsThrough(t *testing.T) {
	const ns = "rbac-resolver-error"
	brk := newTestBroker(t)
	composer, _, email, _ := dbComposerWithGrant(t)
	startAuthzHandlerWithResolver(t, brk, composer, erroringResolver{}, ns)

	resp := roundTrip(t, nil, brk, ns, email, map[string]any{"gateway_id": "slack"})
	if roles := rolesFromResult(t, resp); slices.Contains(roles, "chat-role") {
		t.Errorf("a resolver error must fall through to defaults, not apply the grant; roles = %v", roles)
	}
}

func TestAuthzLookupHandler_DegradedResolutionExpiresQuickly(t *testing.T) {
	const ns = "rbac-degraded-expires"
	brk := newTestBroker(t)
	composer, usersRepo, email, _ := dbComposerWithGrant(t)
	resolver := &flakyResolver{delegate: newUsersRepoIdentityResolver(usersRepo, discardLogger())}
	resolver.remainingFailures.Store(1)
	const degradedTTL = 10 * time.Millisecond
	startAuthzHandlerWithConfig(t, brk, composer, resolver,
		AuthzLookupConfig{Namespace: ns, CacheTTL: time.Minute, DegradedResolveTTL: degradedTTL})

	gatewayContext := map[string]any{"gateway_id": "slack"}
	if roles := rolesFromResult(t, roundTrip(t, nil, brk, ns, email, gatewayContext)); slices.Contains(roles, "chat-role") {
		t.Fatalf("precondition: the failing resolve must not apply the grant; roles = %v", roles)
	}

	time.Sleep(3 * degradedTTL)

	if roles := rolesFromResult(t, roundTrip(t, nil, brk, ns, email, gatewayContext)); !slices.Contains(roles, "chat-role") {
		t.Errorf("a degraded resolution must expire quickly: after %v the retry should resolve and apply the grant; roles = %v", degradedTTL, roles)
	}
}

func TestAuthzLookupHandler_DegradedResolutionShedsLoad(t *testing.T) {
	const ns = "rbac-degraded-sheds-load"
	brk := newTestBroker(t)
	composer, _, email, _ := dbComposerWithGrant(t)
	resolver := &countingResolver{delegate: erroringResolver{}}
	startAuthzHandlerWithConfig(t, brk, composer, resolver,
		AuthzLookupConfig{Namespace: ns, CacheTTL: time.Minute, DegradedResolveTTL: time.Minute})

	gatewayContext := map[string]any{"gateway_id": "slack"}
	for range 3 {
		if roles := rolesFromResult(t, roundTrip(t, nil, brk, ns, email, gatewayContext)); slices.Contains(roles, "chat-role") {
			t.Fatalf("a failing resolve must not apply the grant; roles = %v", roles)
		}
	}
	if got := resolver.calls.Load(); got != 1 {
		t.Errorf("a degraded resolution must be cached briefly to shed load: expected 1 resolve across 3 identical lookups, got %d", got)
	}
}

func TestAuthzLookupHandler_UserWriteInvalidatesCache(t *testing.T) {
	const ns = "rbac-user-write-invalidates"
	brk := newTestBroker(t)
	composer, usersRepo, email, _ := dbComposerWithGrant(t)
	lookupHandler := startAuthzHandlerWithResolver(t, brk, composer, newUsersRepoIdentityResolver(usersRepo, discardLogger()), ns)
	startUpsertHandlerWithLookupRefresh(t, brk, usersRepo, composer, lookupHandler, ns)

	gatewayContext := map[string]any{"gateway_id": "slack"}
	if roles := rolesFromResult(t, roundTrip(t, nil, brk, ns, email, gatewayContext)); !slices.Contains(roles, "chat-role") {
		t.Fatalf("precondition: the grant must apply and populate the cache; roles = %v", roles)
	}

	resp := upsertRoundTrip(t, brk, ns, "test-gw", userUpsertRequest{
		Issuer: "iss", Sub: "grantee", Email: "renamed@example.com",
	})
	if resp.Error != nil {
		t.Fatalf("upsert the renamed email: %+v", resp.Error)
	}

	if roles := rolesFromResult(t, roundTrip(t, nil, brk, ns, email, gatewayContext)); slices.Contains(roles, "chat-role") {
		t.Errorf("the old email must stop resolving to the grant once the users row changed; roles = %v", roles)
	}
}

func TestAuthzLookupHandler_UnchangedLoginKeepsCache(t *testing.T) {
	const ns = "rbac-unchanged-login-keeps-cache"
	brk := newTestBroker(t)
	composer, usersRepo, email, _ := dbComposerWithGrant(t)
	resolver := &countingResolver{delegate: newUsersRepoIdentityResolver(usersRepo, discardLogger())}
	lookupHandler := startAuthzHandlerWithResolver(t, brk, composer, resolver, ns)
	startUpsertHandlerWithLookupRefresh(t, brk, usersRepo, composer, lookupHandler, ns)

	gatewayContext := map[string]any{"gateway_id": "slack"}
	if roles := rolesFromResult(t, roundTrip(t, nil, brk, ns, email, gatewayContext)); !slices.Contains(roles, "chat-role") {
		t.Fatalf("precondition: the grant must apply and populate the cache; roles = %v", roles)
	}
	if got := resolver.calls.Load(); got != 1 {
		t.Fatalf("precondition: expected exactly one resolve, got %d", got)
	}

	resp := upsertRoundTrip(t, brk, ns, "test-gw", userUpsertRequest{
		Issuer: "iss", Sub: "grantee", Email: email,
	})
	if resp.Error != nil {
		t.Fatalf("re-login upsert: %+v", resp.Error)
	}

	if roles := rolesFromResult(t, roundTrip(t, nil, brk, ns, email, gatewayContext)); !slices.Contains(roles, "chat-role") {
		t.Errorf("the grant must still apply after an unchanged login; roles = %v", roles)
	}
	if got := resolver.calls.Load(); got != 1 {
		t.Errorf("an unchanged login must not flush the cache: expected the second lookup to be a cache hit, but the resolver was called %d times", got)
	}
}

func startUpsertHandlerWithLookupRefresh(t *testing.T, brk broker.Broker, repo *store.UsersRepo, composer *rbac.Composer, lookup *AuthzLookupHandler, namespace string) {
	t.Helper()
	h := NewUserUpsertHandler(repo, brk, UserUpsertConfig{Namespace: namespace}, discardLogger())
	h.SetGrantRefresh(composer, lookup, nil)
	if err := h.Start(context.Background()); err != nil {
		t.Fatalf("start upsert handler: %v", err)
	}
	t.Cleanup(func() { _ = h.Stop(context.Background()) })
}

type countingResolver struct {
	calls    atomic.Int32
	delegate userIdentityResolver
}

func (r *countingResolver) Resolve(ctx context.Context, req identityLookupRequest) (identityResolution, error) {
	r.calls.Add(1)
	return r.delegate.Resolve(ctx, req)
}

type misdirectingResolver struct{}

func (misdirectingResolver) Resolve(context.Context, identityLookupRequest) (identityResolution, error) {
	return identityResolution{
		Context: map[string]any{"sam_user_id": "some-other-id"},
		Outcome: identity.LookupMatched,
		UserID:  "some-other-id",
	}, nil
}

type erroringResolver struct{}

func (erroringResolver) Resolve(context.Context, identityLookupRequest) (identityResolution, error) {
	return identityResolution{Outcome: identity.LookupDegraded}, errors.New("db unavailable")
}

type flakyResolver struct {
	remainingFailures atomic.Int32
	delegate          userIdentityResolver
}

func (r *flakyResolver) Resolve(ctx context.Context, req identityLookupRequest) (identityResolution, error) {
	if r.remainingFailures.Add(-1) >= 0 {
		return identityResolution{Outcome: identity.LookupDegraded}, errors.New("db unavailable")
	}
	return r.delegate.Resolve(ctx, req)
}

type rotatingResolver struct {
	calls    atomic.Int32
	delegate userIdentityResolver
}

func (r *rotatingResolver) Resolve(ctx context.Context, req identityLookupRequest) (identityResolution, error) {
	r.calls.Add(1)
	if req.DiscardBinding {
		return identityResolution{Outcome: identity.LookupRefused, RefusalReason: "rotated", RotationApplied: true}, nil
	}
	return r.delegate.Resolve(ctx, req)
}

func TestAuthzLookupHandler_RotationEvictsTheCachedHolder(t *testing.T) {
	const ns = "rbac-rotation-evicts"
	brk := newTestBroker(t)
	composer, usersRepo, email, _ := dbComposerWithGrant(t)
	resolver := &rotatingResolver{delegate: newUsersRepoIdentityResolver(usersRepo, discardLogger())}
	startAuthzHandlerWithResolver(t, brk, composer, resolver, ns)

	ordinary := map[string]any{"gateway_id": "whatsapp"}
	for range 2 {
		if roles := rolesFromResult(t, roundTrip(t, nil, brk, ns, email, ordinary)); !slices.Contains(roles, "chat-role") {
			t.Fatalf("precondition: the holder must match; roles = %v", roles)
		}
	}
	if got := resolver.calls.Load(); got != 1 {
		t.Fatalf("precondition: the second identical lookup must be a cache hit; resolves = %d", got)
	}

	rotation := roundTrip(t, nil, brk, ns, email, map[string]any{"gateway_id": "whatsapp", "continuity_discard": true})
	if rotation.Error != nil {
		t.Fatalf("rotation lookup error: %+v", rotation.Error)
	}

	roundTrip(t, nil, brk, ns, email, ordinary)
	if got := resolver.calls.Load(); got != 3 {
		t.Errorf("resolves = %d, want 3 — the lookup after the rotation was served the previous holder's cached match instead of re-resolving against the tombstone", got)
	}
}

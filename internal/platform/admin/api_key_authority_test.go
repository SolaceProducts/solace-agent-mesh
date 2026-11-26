// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/apikey"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

type stubOwnerScopes struct {
	scopes []string
	err    error
	calls  int
	gotSub string
	gotID  string
}

func (s *stubOwnerScopes) ResolveOwnerScopes(sub, userID string) (rbac.Resolution, error) {
	s.calls++
	s.gotSub, s.gotID = sub, userID
	if s.err != nil {
		return rbac.Resolution{}, s.err
	}
	return rbac.Resolution{Scopes: s.scopes}, nil
}

type authorityFixture struct {
	authority *APIKeyAuthority
	db        *store.DB
	keys      *store.APIKeysRepo
	users     *store.UsersRepo
	owners    *stubOwnerScopes
}

func newAuthorityFixture(t *testing.T, ownerScopes []string) *authorityFixture {
	t.Helper()
	db := store.TestDB(t)
	owners := &stubOwnerScopes{scopes: ownerScopes}
	keys := store.NewAPIKeysRepo(db)
	users := store.NewUsersRepo(db)
	return &authorityFixture{
		authority: NewAPIKeyAuthority(keys, users, owners, authorizationModeDefaultRBAC, slog.Default()),
		db:        db,
		keys:      keys,
		users:     users,
		owners:    owners,
	}
}

func (f *authorityFixture) seedOwner(ctx context.Context, t *testing.T, sub, issuer, provisionedVia string) *store.User {
	t.Helper()
	owner := &store.User{
		Issuer: issuer, Sub: sub,
		ProvisionedVia: provisionedVia,
		CreatedBy:      "test", UpdatedBy: "test",
	}
	if err := f.users.UpsertUser(ctx, owner, nil); err != nil {
		t.Fatalf("seed owner %q: %v", sub, err)
	}
	return owner
}

func (f *authorityFixture) seedSystemOwner(ctx context.Context, t *testing.T, sub string) *store.User {
	t.Helper()
	return f.seedOwner(ctx, t, sub, rbac.SystemUserIssuer, rbac.ProvisionedViaSystem)
}

func (f *authorityFixture) seedKey(ctx context.Context, t *testing.T, ownerID, hash string, scopes []string, expires *int64) *store.APIKey {
	t.Helper()
	key, err := f.keys.Create(ctx, store.APIKey{
		UserID: ownerID, Label: "test-key", KeyHash: hash, Prefix: "abcd1234",
		ExpiresTime: expires, CreatedBy: "admin", UpdatedBy: "admin",
	}, scopes, 10, time.Now().UnixMilli())
	if err != nil {
		t.Fatalf("seed key: %v", err)
	}
	return key
}

func TestAPIKeyAuthority_ValidatesAnEligibleKey(t *testing.T) {
	f := newAuthorityFixture(t, []string{"agent_builder:_:create", "agent:*:invoke"})
	ctx := t.Context()
	owner := f.seedSystemOwner(ctx, t, "system:ci-deployer")
	key := f.seedKey(ctx, t, owner.ID, "good-hash", []string{"agent:*:invoke"}, nil)

	got, err := f.authority.Validate(ctx, "good-hash")
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if got.KeyID != key.ID {
		t.Errorf("KeyID = %q, want %q", got.KeyID, key.ID)
	}
	if got.OwnerID != owner.ID {
		t.Errorf("OwnerID = %q, want %q", got.OwnerID, owner.ID)
	}
	if got.OwnerSub != "system:ci-deployer" {
		t.Errorf("OwnerSub = %q, want %q", got.OwnerSub, "system:ci-deployer")
	}
	if !slices.Equal(got.Scopes, []string{"agent:*:invoke"}) {
		t.Errorf("Scopes = %v, want [agent:*:invoke]", got.Scopes)
	}
}

func TestAPIKeyAuthority_PassesBothOwnerIdentifiersToTheResolver(t *testing.T) {
	f := newAuthorityFixture(t, []string{"agent:*:invoke"})
	ctx := t.Context()
	owner := f.seedSystemOwner(ctx, t, "system:both-ids")
	f.seedKey(ctx, t, owner.ID, "both-ids-hash", []string{"agent:*:invoke"}, nil)

	if _, err := f.authority.Validate(ctx, "both-ids-hash"); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if f.owners.gotSub != "system:both-ids" {
		t.Errorf("resolver received sub %q, want %q", f.owners.gotSub, "system:both-ids")
	}
	if f.owners.gotID != owner.ID {
		t.Errorf("resolver received userID %q, want the canonical id %q", f.owners.gotID, owner.ID)
	}
}

func TestAPIKeyAuthority_RefusalsAreIndistinguishable(t *testing.T) {
	ctx := t.Context()

	tests := []struct {
		name  string
		setup func(t *testing.T, f *authorityFixture) string
	}{
		{"unknown credential", func(_ *testing.T, _ *authorityFixture) string {
			return "no-such-hash"
		}},
		{"empty credential", func(_ *testing.T, _ *authorityFixture) string {
			return ""
		}},
		{"expired key", func(t *testing.T, f *authorityFixture) string {
			owner := f.seedSystemOwner(ctx, t, "system:expired")
			lapsed := time.Now().Add(-time.Hour).UnixMilli()
			f.seedKey(ctx, t, owner.ID, "expired-hash", []string{"agent:*:invoke"}, &lapsed)
			return "expired-hash"
		}},
		{"human owner", func(t *testing.T, f *authorityFixture) string {
			owner := f.seedOwner(ctx, t, "alice@example.com", "https://idp.example.com", "jit")
			f.seedKey(ctx, t, owner.ID, "human-hash", []string{"agent:*:invoke"}, nil)
			return "human-hash"
		}},
		{"owner issuer is not the system issuer", func(t *testing.T, f *authorityFixture) string {
			owner := f.seedOwner(ctx, t, "system:wrong-issuer", "https://idp.example.com", rbac.ProvisionedViaSystem)
			f.seedKey(ctx, t, owner.ID, "issuer-hash", []string{"agent:*:invoke"}, nil)
			return "issuer-hash"
		}},
		{"owner provisioned by the IdP, otherwise a system user", func(t *testing.T, f *authorityFixture) string {
			owner := f.seedOwner(ctx, t, "system:via-jit", rbac.SystemUserIssuer, "jit")
			f.seedKey(ctx, t, owner.ID, "via-jit-hash", []string{"agent:*:invoke"}, nil)
			return "via-jit-hash"
		}},
		{"owner subject lacks the system prefix", func(t *testing.T, f *authorityFixture) string {
			owner := f.seedOwner(ctx, t, "not-prefixed", rbac.SystemUserIssuer, rbac.ProvisionedViaSystem)
			f.seedKey(ctx, t, owner.ID, "prefix-hash", []string{"agent:*:invoke"}, nil)
			return "prefix-hash"
		}},
		{"reserved built-in owner", func(t *testing.T, f *authorityFixture) string {
			owner := f.seedSystemOwner(ctx, t, rbac.DefaultSystemUserSub)
			f.seedKey(ctx, t, owner.ID, "reserved-hash", []string{"agent:*:invoke"}, nil)
			return "reserved-hash"
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newAuthorityFixture(t, []string{"agent:*:invoke"})
			hash := tc.setup(t, f)
			got, err := f.authority.Validate(t.Context(), hash)
			if err != apikey.ErrInvalidKey {
				t.Fatalf("Validate error = %v, want exactly ErrInvalidKey", err)
			}
			if got.KeyID != "" || got.OwnerID != "" || len(got.Scopes) > 0 {
				t.Errorf("a refusal returned a populated resolution: %+v", got)
			}
		})
	}
}

func TestAPIKeyAuthority_RefusesEveryReservedBuiltIn(t *testing.T) {
	for _, sub := range rbac.ReservedSystemUserSubs() {
		t.Run(sub, func(t *testing.T) {
			f := newAuthorityFixture(t, []string{"agent:*:invoke"})
			ctx := t.Context()
			owner := f.seedSystemOwner(ctx, t, sub)
			f.seedKey(ctx, t, owner.ID, "reserved-"+sub, []string{"agent:*:invoke"}, nil)

			if _, err := f.authority.Validate(ctx, "reserved-"+sub); !errors.Is(err, apikey.ErrInvalidKey) {
				t.Errorf("Validate error = %v, want ErrInvalidKey", err)
			}
		})
	}
}

func TestAPIKeyAuthority_RefusesOutsideDefaultRBAC(t *testing.T) {
	for _, mode := range []string{"none", "deny_all", "", "DEFAULT_RBAC"} {
		t.Run("mode="+mode, func(t *testing.T) {
			db := store.TestDB(t)
			authority := NewAPIKeyAuthority(
				store.NewAPIKeysRepo(db), store.NewUsersRepo(db),
				&stubOwnerScopes{}, mode, slog.Default(),
			)
			_, err := authority.Validate(t.Context(), "any-hash")
			if !errors.Is(err, apikey.ErrAuthorizationModeDisabled) {
				t.Errorf("mode %q: error = %v, want ErrAuthorizationModeDisabled", mode, err)
			}
		})
	}
}

func TestAPIKeyAuthority_ResolverFailureIsUnavailableNotInvalid(t *testing.T) {
	f := newAuthorityFixture(t, nil)
	f.owners.err = errors.New("composer snapshot unavailable")
	ctx := t.Context()
	owner := f.seedSystemOwner(ctx, t, "system:degraded")
	f.seedKey(ctx, t, owner.ID, "degraded-hash", []string{"agent:*:invoke"}, nil)

	_, err := f.authority.Validate(ctx, "degraded-hash")
	if !errors.Is(err, apikey.ErrAuthorityUnavailable) {
		t.Fatalf("Validate error = %v, want ErrAuthorityUnavailable", err)
	}
	if errors.Is(err, apikey.ErrInvalidKey) {
		t.Error("an outage was reported as an invalid credential")
	}
}

func TestAPIKeyAuthority_ScopesSurviveOnlyWhileTheOwnerCoversThem(t *testing.T) {
	tests := []struct {
		name        string
		ownerScopes []string
		selected    []string
		want        []string
	}{
		{
			name:        "fully covered scopes survive intact",
			ownerScopes: []string{"agent_builder:_:create", "agent_builder:*:deploy", "agent:*:invoke"},
			selected:    []string{"agent_builder:_:create", "agent_builder:*:deploy"},
			want:        []string{"agent_builder:_:create", "agent_builder:*:deploy"},
		},
		{
			name:        "a scope the owner lost is dropped and the rest survive",
			ownerScopes: []string{"agent_builder:_:create"},
			selected:    []string{"agent_builder:_:create", "agent_builder:*:deploy"},
			want:        []string{"agent_builder:_:create"},
		},
		{
			name:        "a narrowed owner grant removes the broad scope entirely",
			ownerScopes: []string{"agent:finance:invoke", "agent_builder:_:create"},
			selected:    []string{"agent:*:invoke", "agent_builder:_:create"},
			want:        []string{"agent_builder:_:create"},
		},
		{
			name:        "an owner wildcard covers a specific selection",
			ownerScopes: []string{"agent:*:invoke"},
			selected:    []string{"agent:finance:invoke"},
			want:        []string{"agent:finance:invoke"},
		},
		{
			name:        "the universal grant covers everything selected",
			ownerScopes: []string{"*:*:*"},
			selected:    []string{"rbac:_:update", "agent:*:invoke"},
			want:        []string{"agent:*:invoke", "rbac:_:update"},
		},
		{
			name:        "owner scopes beyond the selection never widen the key",
			ownerScopes: []string{"agent:*:invoke", "rbac:_:update", "agent_builder:*:delete"},
			selected:    []string{"agent:*:invoke"},
			want:        []string{"agent:*:invoke"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newAuthorityFixture(t, tc.ownerScopes)
			ctx := t.Context()
			owner := f.seedSystemOwner(ctx, t, "system:coverage")
			f.seedKey(ctx, t, owner.ID, "coverage-hash", tc.selected, nil)

			got, err := f.authority.Validate(ctx, "coverage-hash")
			if err != nil {
				t.Fatalf("Validate: %v", err)
			}
			gotSorted := slices.Clone(got.Scopes)
			wantSorted := slices.Clone(tc.want)
			slices.Sort(gotSorted)
			slices.Sort(wantSorted)
			if !slices.Equal(gotSorted, wantSorted) {
				t.Errorf("surviving scopes = %v, want %v", gotSorted, wantSorted)
			}
		})
	}
}

func TestAPIKeyAuthority_TotalCoverageLossRefusesTheKey(t *testing.T) {
	tests := []struct {
		name        string
		ownerScopes []string
		selected    []string
	}{
		{
			name:        "the owner lost every role that covered the key",
			ownerScopes: []string{"model_config:_:read"},
			selected:    []string{"agent:*:invoke"},
		},
		{
			name:        "the owner's only covering grant narrowed",
			ownerScopes: []string{"agent:finance:invoke"},
			selected:    []string{"agent:*:invoke"},
		},
		{
			name:        "the owner holds no scopes at all",
			ownerScopes: nil,
			selected:    []string{"agent:*:invoke"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newAuthorityFixture(t, tc.ownerScopes)
			ctx := t.Context()
			owner := f.seedSystemOwner(ctx, t, "system:deroled")
			f.seedKey(ctx, t, owner.ID, "deroled-hash", tc.selected, nil)

			got, err := f.authority.Validate(ctx, "deroled-hash")
			if !errors.Is(err, apikey.ErrInvalidKey) {
				t.Errorf("Validate = %v, want ErrInvalidKey", err)
			}
			if got.KeyID != "" || got.OwnerID != "" || len(got.Scopes) != 0 {
				t.Errorf("a refusal returned a populated resolution: %+v", got)
			}
		})
	}
}

func TestAPIKeyAuthority_RefusesAnInactiveOwner(t *testing.T) {
	f := newAuthorityFixture(t, []string{"agent:*:invoke"})
	ctx := t.Context()
	owner := f.seedSystemOwner(ctx, t, "system:deactivated")
	f.seedKey(ctx, t, owner.ID, "deactivated-hash", []string{"agent:*:invoke"}, nil)

	if _, err := f.authority.Validate(ctx, "deactivated-hash"); err != nil {
		t.Fatalf("the key must be valid while its owner is active: %v", err)
	}
	if _, err := f.db.ExecContext(ctx, `UPDATE users SET active = ? WHERE id = ?`, 0, owner.ID); err != nil {
		t.Fatalf("deactivate owner: %v", err)
	}

	got, err := f.authority.Validate(ctx, "deactivated-hash")
	if !errors.Is(err, apikey.ErrInvalidKey) {
		t.Errorf("Validate for an inactive owner = %v, want ErrInvalidKey", err)
	}
	if got.KeyID != "" || len(got.Scopes) != 0 {
		t.Errorf("a refusal returned a populated resolution: %+v", got)
	}
}

func TestAPIKeyAuthority_ExpiryIsEvaluatedPerRequest(t *testing.T) {
	f := newAuthorityFixture(t, []string{"agent:*:invoke"})
	ctx := t.Context()
	owner := f.seedSystemOwner(ctx, t, "system:lapsing")
	expires := time.Now().Add(time.Hour).UnixMilli()
	f.seedKey(ctx, t, owner.ID, "lapsing-hash", []string{"agent:*:invoke"}, &expires)

	if _, err := f.authority.Validate(ctx, "lapsing-hash"); err != nil {
		t.Fatalf("key should be valid before its expiry: %v", err)
	}

	f.authority.now = func() time.Time { return time.UnixMilli(expires) }
	if _, err := f.authority.Validate(ctx, "lapsing-hash"); !errors.Is(err, apikey.ErrInvalidKey) {
		t.Errorf("at exactly its expiry the key returned %v, want ErrInvalidKey", err)
	}

	f.authority.now = func() time.Time { return time.UnixMilli(expires).Add(time.Millisecond) }
	if _, err := f.authority.Validate(ctx, "lapsing-hash"); !errors.Is(err, apikey.ErrInvalidKey) {
		t.Errorf("past its expiry the key returned %v, want ErrInvalidKey", err)
	}
}

func TestAPIKeyAuthority_DeletionTakesEffectImmediately(t *testing.T) {
	f := newAuthorityFixture(t, []string{"agent:*:invoke"})
	ctx := t.Context()
	owner := f.seedSystemOwner(ctx, t, "system:revoked")
	key := f.seedKey(ctx, t, owner.ID, "revoked-hash", []string{"agent:*:invoke"}, nil)

	if _, err := f.authority.Validate(ctx, "revoked-hash"); err != nil {
		t.Fatalf("Validate before deletion: %v", err)
	}
	if err := f.keys.Delete(ctx, key.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := f.authority.Validate(ctx, "revoked-hash"); !errors.Is(err, apikey.ErrInvalidKey) {
		t.Errorf("a deleted key still validated: %v", err)
	}
}

func newOwnerScopesHandler(t *testing.T, db rbac.DBSource) *AuthzLookupHandler {
	t.Helper()
	yaml := rbac.YAMLState{
		Roles: rbac.RolesConfig{Roles: map[string]rbac.RoleDefinition{
			"pipeline":   {Scopes: []string{"agent:*:invoke"}},
			"human_only": {Scopes: []string{"agent_builder:*:read"}},
		}},
		Users: rbac.UsersConfig{Users: map[string]rbac.UserAssignment{
			"system:ci-deployer": {Roles: []string{"pipeline"}},
		}},
		DefaultRoles: []string{"human_only"},
	}
	composer := rbac.NewComposer(yaml, db, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := composer.Rebuild(t.Context()); err != nil {
		t.Fatalf("composer rebuild: %v", err)
	}
	return NewAuthzLookupHandler(composer, nil, newTestBroker(t),
		AuthzLookupConfig{Namespace: "test-ns", CacheTTL: time.Minute},
		slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func TestResolveOwnerScopes_ResolvesASystemSubjectWithoutHumanDefaults(t *testing.T) {
	h := newOwnerScopesHandler(t, nil)

	got, err := h.ResolveOwnerScopes("system:ci-deployer", "01a0ee68-0000-7000-8000-00000000abcd")
	if err != nil {
		t.Fatalf("ResolveOwnerScopes: %v", err)
	}
	if !slices.Contains(got.Scopes, "agent:*:invoke") {
		t.Errorf("scopes = %v, want the owner's granted agent:*:invoke", got.Scopes)
	}
	unassigned, err := h.ResolveOwnerScopes("system:unassigned", "01a0ee68-0000-7000-8000-00000000dcba")
	if err == nil && len(unassigned.Scopes) > 0 {
		t.Errorf("scopes = %v: an unassigned system principal inherited the default roles", unassigned.Scopes)
	}
}

func TestResolveOwnerScopes_FailsClosedOnMissingInputs(t *testing.T) {
	h := newOwnerScopesHandler(t, nil)
	tests := []struct{ name, sub, userID string }{
		{"no subject", "", "01a0ee68-0000-7000-8000-00000000abcd"},
		{"no user id", "system:ci-deployer", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := h.ResolveOwnerScopes(tc.sub, tc.userID); err == nil {
				t.Error("ResolveOwnerScopes accepted an incomplete owner")
			}
		})
	}
	var unwired *AuthzLookupHandler
	if _, err := unwired.ResolveOwnerScopes("system:ci-deployer", "01a0ee68-0000-7000-8000-00000000abcd"); err == nil {
		t.Error("a nil handler resolved owner scopes")
	}
}

func TestResolveOwnerScopes_ReResolvesAWrongTypedCacheEntry(t *testing.T) {
	h := newOwnerScopesHandler(t, nil)
	const sub, userID = "system:ci-deployer", "01a0ee68-0000-7000-8000-00000000abcd"

	if _, err := h.ResolveOwnerScopes(sub, userID); err != nil {
		t.Fatalf("priming call: %v", err)
	}
	cacheKey := ownerScopesCacheKeyPrefix + cacheKeyFor(sub, "", map[string]any{"sam_user_id": userID})
	h.cache.Set(cacheKey, "not a resolution", runtime.WithTTL(time.Minute))

	got, err := h.ResolveOwnerScopes(sub, userID)
	if err != nil {
		t.Fatalf("ResolveOwnerScopes over a wrong-typed cache entry: %v", err)
	}
	if !slices.Contains(got.Scopes, "agent:*:invoke") {
		t.Errorf("scopes = %v, want a fresh resolution", got.Scopes)
	}
}

func TestAPIKeyAuthority_StoreFailuresAreUnavailableNotInvalid(t *testing.T) {
	tests := []struct {
		name    string
		breakAt func(t *testing.T, f *authorityFixture) context.Context
	}{
		{"the key lookup", func(t *testing.T, _ *authorityFixture) context.Context {
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			return ctx
		}},
		{"the owner load", func(t *testing.T, f *authorityFixture) context.Context {
			if _, err := f.db.ExecContext(t.Context(), `DROP TABLE user_groups`); err != nil {
				t.Fatalf("drop user_groups: %v", err)
			}
			return t.Context()
		}},
		{"the scope load", func(t *testing.T, f *authorityFixture) context.Context {
			if _, err := f.db.ExecContext(t.Context(), `DROP TABLE api_key_scopes`); err != nil {
				t.Fatalf("drop api_key_scopes: %v", err)
			}
			return t.Context()
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newAuthorityFixture(t, []string{"agent:*:invoke"})
			owner := f.seedSystemOwner(t.Context(), t, "system:store-outage")
			f.seedKey(t.Context(), t, owner.ID, "outage-hash", []string{"agent:*:invoke"}, nil)

			_, err := f.authority.Validate(tc.breakAt(t, f), "outage-hash")
			if !errors.Is(err, apikey.ErrAuthorityUnavailable) {
				t.Fatalf("Validate error = %v, want ErrAuthorityUnavailable", err)
			}
			if errors.Is(err, apikey.ErrInvalidKey) {
				t.Error("an outage was reported as an invalid credential")
			}
		})
	}
}

func TestAPIKeyAuthority_RefusalLogsAtWarnWithoutTheHash(t *testing.T) {
	f := newAuthorityFixture(t, []string{"agent:*:invoke"})
	var logs bytes.Buffer
	f.authority.logger = slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	ctx := t.Context()
	owner := f.seedSystemOwner(ctx, t, "system:logged")
	const hash = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	lapsed := time.Now().Add(-time.Hour).UnixMilli()
	key := f.seedKey(ctx, t, owner.ID, hash, []string{"agent:*:invoke"}, &lapsed)

	if _, err := f.authority.Validate(ctx, hash); !errors.Is(err, apikey.ErrInvalidKey) {
		t.Fatalf("Validate error = %v, want ErrInvalidKey", err)
	}
	line := logs.String()
	if !strings.Contains(line, `"level":"WARN"`) {
		t.Errorf("refusal was not logged at WARN: %s", line)
	}
	if !strings.Contains(line, `"credentialID":"`+key.ID+`"`) {
		t.Errorf("refusal does not name the key by credentialID: %s", line)
	}
	if strings.Contains(line, hash) {
		t.Errorf("refusal log carries the presented hash: %s", line)
	}
}

func TestResolveOwnerScopes_ResolvesAnOwnerWithOnlyADatabaseGrant(t *testing.T) {
	const ownerID, roleID = "01a0ee68-0000-7000-8000-0000000000db", "11111111-1111-4111-8111-1111111111db"
	h := newOwnerScopesHandler(t, &stubDBSource{state: &rbac.DBState{
		Roles:     []rbac.DBRole{{ID: roleID, Name: "db_pipeline", Scopes: []string{"agent_builder:*:deploy"}}},
		UserRoles: []rbac.DBUserRole{{UserID: ownerID, RoleID: roleID}},
	}})

	got, err := h.ResolveOwnerScopes("system:db-only", ownerID)
	if err != nil {
		t.Fatalf("ResolveOwnerScopes: %v", err)
	}
	if !slices.Contains(got.Scopes, "agent_builder:*:deploy") {
		t.Errorf("scopes = %v, want the database grant's agent_builder:*:deploy", got.Scopes)
	}
}

func TestResolveOwnerScopes_CachesUntilInvalidated(t *testing.T) {
	const ownerID, roleID = "01a0ee68-0000-7000-8000-0000000000ca", "11111111-1111-4111-8111-1111111111ca"
	granted := &rbac.DBState{
		Roles:     []rbac.DBRole{{ID: roleID, Name: "cached", Scopes: []string{"agent_builder:*:deploy"}}},
		UserRoles: []rbac.DBUserRole{{UserID: ownerID, RoleID: roleID}},
	}
	source := &stubDBSource{state: granted}
	h := newOwnerScopesHandler(t, source)
	const sub = "system:cached"
	resolve := func() []string {
		t.Helper()
		got, err := h.ResolveOwnerScopes(sub, ownerID)
		if err != nil {
			t.Fatalf("ResolveOwnerScopes: %v", err)
		}
		return got.Scopes
	}

	if !slices.Contains(resolve(), "agent_builder:*:deploy") {
		t.Fatal("priming resolution lacks the granted scope")
	}
	source.set(&rbac.DBState{Roles: granted.Roles})
	if err := h.composer.Rebuild(t.Context()); err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	if !slices.Contains(resolve(), "agent_builder:*:deploy") {
		t.Error("a revoked grant disappeared before eviction; the resolution is not being cached")
	}
	h.InvalidateAll()
	if slices.Contains(resolve(), "agent_builder:*:deploy") {
		t.Error("InvalidateAll did not evict the owner's cached resolution")
	}
}

func TestResolveOwnerScopes_DoesNotReadTheBrokerLookupsEntry(t *testing.T) {
	h := newOwnerScopesHandler(t, nil)
	const sub, userID = "system:ci-deployer", "01a0ee68-0000-7000-8000-00000000abcd"
	unprefixed := cacheKeyFor(sub, "", map[string]any{"sam_user_id": userID})
	h.cache.Set(unprefixed, rbac.Resolution{Scopes: []string{"*:*:*"}}, runtime.WithTTL(time.Minute))

	got, err := h.ResolveOwnerScopes(sub, userID)
	if err != nil {
		t.Fatalf("ResolveOwnerScopes: %v", err)
	}
	if slices.Contains(got.Scopes, "*:*:*") {
		t.Errorf("scopes = %v: the owner lookup read an entry outside its namespace", got.Scopes)
	}
}

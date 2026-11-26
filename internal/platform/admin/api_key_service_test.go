// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/apikey"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

var testActorScopes = []string{"*"}

type serviceFixture struct {
	svc    *APIKeyService
	db     *store.DB
	keys   *store.APIKeysRepo
	users  *store.UsersRepo
	owners *stubOwnerScopes
	logs   *syncedBuffer
}

func newServiceFixture(t *testing.T, ownerScopes []string) *serviceFixture {
	t.Helper()
	db := store.TestDB(t)
	owners := &stubOwnerScopes{scopes: ownerScopes}
	keys := store.NewAPIKeysRepo(db)
	users := store.NewUsersRepo(db)
	logs := &syncedBuffer{}
	svc := NewAPIKeyService(keys, users, owners, authorizationModeDefaultRBAC, slog.Default())
	svc.logger = slog.New(slog.NewJSONHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	return &serviceFixture{svc: svc, db: db, keys: keys, users: users, owners: owners, logs: logs}
}

func (f *serviceFixture) seedSystemOwner(ctx context.Context, t *testing.T, sub string) *store.User {
	t.Helper()
	owner := &store.User{
		Issuer: rbac.SystemUserIssuer, Sub: sub,
		ProvisionedVia: rbac.ProvisionedViaSystem,
		CreatedBy:      "test", UpdatedBy: "test",
	}
	if err := f.users.UpsertUser(ctx, owner, nil); err != nil {
		t.Fatalf("seed owner %q: %v", sub, err)
	}
	return owner
}

func wireInstant(ms int64) string {
	return time.UnixMilli(ms).UTC().Format("2006-01-02T15:04:05.000Z")
}

func futureExpiry(d time.Duration) *RESTTimestamp {
	return new(RESTTimestamp(time.Now().Add(d).UnixMilli()))
}

func TestAPIKeyService_CreateIssuesAKeyAndStoresOnlyItsHash(t *testing.T) {
	f := newServiceFixture(t, []string{"agent_builder:_:create"})
	ctx := t.Context()
	owner := f.seedSystemOwner(ctx, t, "system:ci-deployer")

	req := CreateAPIKeyRequest{
		UserID:      owner.ID,
		Label:       "deploy-pipeline",
		Scopes:      []string{"agent_builder:_:create"},
		ExpiresTime: futureExpiry(24 * time.Hour),
	}
	resp, err := f.svc.Create(ctx, req, "admin@example.com", testActorScopes)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if !apikey.HasMarker(resp.Secret) {
		t.Errorf("secret %q does not carry the samk_ marker", resp.Secret)
	}
	if resp.Prefix != apikey.DisplayPrefix(resp.Secret) {
		t.Errorf("prefix %q does not match the issued secret", resp.Prefix)
	}
	if resp.Status != store.APIKeyStatusActive {
		t.Errorf("status = %q, want active", resp.Status)
	}
	if resp.Type != apiKeyResourceType {
		t.Errorf("type = %q, want %q", resp.Type, apiKeyResourceType)
	}

	stored, err := f.keys.GetByKeyHash(ctx, apikey.Hash(resp.Secret))
	if err != nil {
		t.Fatalf("look up the issued key by its hash: %v", err)
	}
	if stored.ID != resp.ID {
		t.Errorf("hash lookup found key %q, want %q", stored.ID, resp.ID)
	}
	if stored.KeyHash == resp.Secret {
		t.Error("the plaintext was stored verbatim")
	}

	wantExpiry := int64(*req.ExpiresTime)
	if resp.ExpiresTime == nil || int64(*resp.ExpiresTime) != wantExpiry {
		t.Errorf("response expiresTime = %v, want %d", resp.ExpiresTime, wantExpiry)
	}
	if stored.ExpiresTime == nil || *stored.ExpiresTime != wantExpiry {
		t.Errorf("stored expires_time = %v, want %d", stored.ExpiresTime, wantExpiry)
	}
	if stored.CreatedBy != "admin@example.com" || stored.UpdatedBy != "admin@example.com" {
		t.Errorf("stored createdBy/updatedBy = %q/%q, want the actor", stored.CreatedBy, stored.UpdatedBy)
	}
	if resp.CreatedBy != "admin@example.com" || resp.UpdatedBy != "admin@example.com" {
		t.Errorf("response createdBy/updatedBy = %q/%q, want the actor", resp.CreatedBy, resp.UpdatedBy)
	}
}

func TestAPIKeyService_CreateLogsNeitherPlaintextNorHash(t *testing.T) {
	f := newServiceFixture(t, []string{"agent:*:invoke"})
	ctx := t.Context()
	owner := f.seedSystemOwner(ctx, t, "system:ci-deployer")

	resp, err := f.svc.Create(ctx, CreateAPIKeyRequest{
		UserID: owner.ID, Label: "pipeline", Scopes: []string{"agent:*:invoke"}, NeverExpires: true,
	}, "admin@example.com", testActorScopes)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	logged := f.logs.String()
	if logged == "" {
		t.Fatal("no audit line was emitted, so this assertion proves nothing")
	}
	if strings.Contains(logged, resp.Secret) {
		t.Error("the audit stream carries the key plaintext")
	}
	if strings.Contains(logged, apikey.Hash(resp.Secret)) {
		t.Error("the audit stream carries the stored key hash")
	}
	if !strings.Contains(logged, resp.ID) {
		t.Error("the audit stream does not name the key id, so a key event cannot be attributed")
	}
	if !strings.Contains(logged, "rbac_mutation") {
		t.Error("key events do not reach the rbac_mutation audit stream")
	}
}

func TestAPIKeyService_CreateRefusesScopesTheOwnerDoesNotHold(t *testing.T) {
	f := newServiceFixture(t, []string{"agent_builder:_:read"})
	ctx := t.Context()
	owner := f.seedSystemOwner(ctx, t, "system:ci-deployer")

	_, err := f.svc.Create(ctx, CreateAPIKeyRequest{
		UserID: owner.ID, Label: "too-broad",
		Scopes:      []string{"agent_builder:_:read", "rbac:_:create"},
		ExpiresTime: futureExpiry(time.Hour),
	}, "admin@example.com", testActorScopes)

	validationErr := requireValidationError(t, err)
	joined := strings.Join(validationErr.Details["scopes"], " ")
	if !strings.Contains(joined, "rbac:_:create") {
		t.Errorf("refusal does not name the uncovered scope: %v", validationErr.Details)
	}
	if strings.Contains(joined, "agent_builder:_:read") {
		t.Errorf("refusal names a covered scope: %v", validationErr.Details)
	}
}

func TestAPIKeyService_CreateWillNotNarrowAWildcardToFit(t *testing.T) {
	f := newServiceFixture(t, []string{"agent:finance:invoke"})
	ctx := t.Context()
	owner := f.seedSystemOwner(ctx, t, "system:ci-deployer")

	_, err := f.svc.Create(ctx, CreateAPIKeyRequest{
		UserID: owner.ID, Label: "wildcard", Scopes: []string{"agent:*:invoke"}, NeverExpires: true,
	}, "admin@example.com", testActorScopes)

	if validationErr := requireValidationError(t, err); !strings.Contains(
		strings.Join(validationErr.Details["scopes"], " "), "agent:*:invoke") {
		t.Errorf("expected the wildcard scope to be refused, got %v", validationErr.Details)
	}
}

func TestAPIKeyService_CreateRefusesIneligibleOwners(t *testing.T) {
	ctx := t.Context()
	cases := []struct {
		name           string
		sub            string
		issuer         string
		provisionedVia string
		wantReason     string
	}{
		{"a human user", "person@example.com", "https://idp.example.com", "oidc", "not a service account"},
		{"a reserved built-in", rbac.DefaultSystemUserSub, rbac.SystemUserIssuer, rbac.ProvisionedViaSystem, "reserved"},
		{"a system subject from another issuer", "system:imposter", "https://idp.example.com", rbac.ProvisionedViaSystem, "issuer"},
		{"a system-provisioned user without the system prefix", "not-prefixed", rbac.SystemUserIssuer, rbac.ProvisionedViaSystem, "prefix"},
	}
	t.Run("the default system user, which has no row", func(t *testing.T) {
		f := newServiceFixture(t, []string{"agent:*:invoke"})
		_, err := f.svc.Create(ctx, CreateAPIKeyRequest{
			UserID: rbac.DefaultSystemUserID, Label: "k", Scopes: []string{"agent:*:invoke"}, NeverExpires: true,
		}, "admin@example.com", testActorScopes)
		validationErr := requireValidationError(t, err)
		if detail := strings.Join(validationErr.Details["userId"], " "); !strings.Contains(detail, "reserved") {
			t.Errorf("refusal %q does not say the owner is reserved", detail)
		}
	})
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newServiceFixture(t, []string{"agent:*:invoke"})
			owner := &store.User{
				Issuer: tc.issuer, Sub: tc.sub, ProvisionedVia: tc.provisionedVia,
				CreatedBy: "test", UpdatedBy: "test",
			}
			if err := f.users.UpsertUser(ctx, owner, nil); err != nil {
				t.Fatalf("seed owner: %v", err)
			}

			_, err := f.svc.Create(ctx, CreateAPIKeyRequest{
				UserID: owner.ID, Label: "k", Scopes: []string{"agent:*:invoke"}, NeverExpires: true,
			}, "admin@example.com", testActorScopes)

			validationErr := requireValidationError(t, err)
			detail := strings.Join(validationErr.Details["userId"], " ")
			if !strings.Contains(detail, tc.wantReason) {
				t.Errorf("refusal %q does not mention %q", detail, tc.wantReason)
			}
		})
	}
}

func TestAPIKeyService_CreateRefusesAnUnknownOwner(t *testing.T) {
	f := newServiceFixture(t, []string{"agent:*:invoke"})
	_, err := f.svc.Create(t.Context(), CreateAPIKeyRequest{
		UserID: "3f2504e0-4f89-11d3-9a0c-0305e82c3301",
		Label:  "k", Scopes: []string{"agent:*:invoke"}, NeverExpires: true,
	}, "admin@example.com", testActorScopes)

	if validationErr := requireValidationError(t, err); len(validationErr.Details["userId"]) == 0 {
		t.Errorf("expected a userId detail, got %v", validationErr.Details)
	}
}

func TestAPIKeyService_CreateRequiresAnExplicitExpiryChoice(t *testing.T) {
	ctx := t.Context()
	past := new(RESTTimestamp(time.Now().Add(-time.Hour).UnixMilli()))
	cases := []struct {
		name string
		req  CreateAPIKeyRequest
		want string
	}{
		{"neither", CreateAPIKeyRequest{}, "expiresTime is required"},
		{"both", CreateAPIKeyRequest{NeverExpires: true, ExpiresTime: futureExpiry(time.Hour)}, "not both"},
		{"already past", CreateAPIKeyRequest{ExpiresTime: past}, "must be in the future"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newServiceFixture(t, []string{"agent:*:invoke"})
			owner := f.seedSystemOwner(ctx, t, "system:ci-deployer")
			req := tc.req
			req.UserID, req.Label, req.Scopes = owner.ID, "k", []string{"agent:*:invoke"}

			_, err := f.svc.Create(ctx, req, "admin@example.com", testActorScopes)

			validationErr := requireValidationError(t, err)
			if detail := strings.Join(validationErr.Details["expiresTime"], " "); !strings.Contains(detail, tc.want) {
				t.Errorf("detail %q does not mention %q", detail, tc.want)
			}
		})
	}
}

func TestAPIKeyService_CreateReportsEveryFieldErrorTogether(t *testing.T) {
	f := newServiceFixture(t, nil)
	_, err := f.svc.Create(t.Context(), CreateAPIKeyRequest{
		UserID: "not-a-uuid", Label: "   ", Scopes: []string{"nope"},
	}, "admin@example.com", testActorScopes)

	validationErr := requireValidationError(t, err)
	for _, field := range []string{"label", "userId", "scopes", "expiresTime"} {
		if len(validationErr.Details[field]) == 0 {
			t.Errorf("no detail reported for %q; got %v", field, validationErr.Details)
		}
	}
}

func TestAPIKeyService_CreateRejectsScopesThatAreNotThreeSegments(t *testing.T) {
	ctx := t.Context()
	for _, scope := range []string{"admin", "agent:invoke", "agent:*:invoke:extra", "Agent:*:invoke"} {
		t.Run(scope, func(t *testing.T) {
			f := newServiceFixture(t, []string{"*"})
			owner := f.seedSystemOwner(ctx, t, "system:ci-deployer")

			_, err := f.svc.Create(ctx, CreateAPIKeyRequest{
				UserID: owner.ID, Label: "k", Scopes: []string{scope}, NeverExpires: true,
			}, "admin@example.com", testActorScopes)

			if validationErr := requireValidationError(t, err); len(validationErr.Details["scopes"]) == 0 {
				t.Errorf("accepted malformed scope %q without a scopes detail: %v", scope, validationErr.Details)
			}
		})
	}
}

func TestAPIKeyService_CreateAcceptsTheUniversalGrantWhenTheOwnerHoldsIt(t *testing.T) {
	f := newServiceFixture(t, []string{"*:*:*"})
	ctx := t.Context()
	owner := f.seedSystemOwner(ctx, t, "system:ci-deployer")

	resp, err := f.svc.Create(ctx, CreateAPIKeyRequest{
		UserID: owner.ID, Label: "broad", Scopes: []string{"*"}, NeverExpires: true,
	}, "admin@example.com", testActorScopes)
	if err != nil {
		t.Fatalf("create with the universal grant: %v", err)
	}
	if !slices.Equal(resp.Scopes, []string{"*:*:*"}) {
		t.Errorf("scopes = %v, want the canonical universal grant", resp.Scopes)
	}
	read, err := f.svc.Get(ctx, resp.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !slices.Equal(read.Scopes, []string{"*:*:*"}) {
		t.Errorf("stored scopes = %v, want the canonical universal grant", read.Scopes)
	}
}

func TestAPIKeyService_CreateSurfacesTheCapAsAConflict(t *testing.T) {
	f := newServiceFixture(t, []string{"agent:*:invoke"})
	ctx := t.Context()
	owner := f.seedSystemOwner(ctx, t, "system:ci-deployer")
	f.svc.maxActiveKeys = 2

	for i := range f.svc.maxActiveKeys {
		if _, err := f.svc.Create(ctx, CreateAPIKeyRequest{
			UserID: owner.ID, Label: "key", Scopes: []string{"agent:*:invoke"}, NeverExpires: true,
		}, "admin@example.com", testActorScopes); err != nil {
			t.Fatalf("create %d: %v", i, err)
		}
	}

	_, err := f.svc.Create(ctx, CreateAPIKeyRequest{
		UserID: owner.ID, Label: "one too many", Scopes: []string{"agent:*:invoke"}, NeverExpires: true,
	}, "admin@example.com", testActorScopes)

	conflictErr, ok := err.(*ConflictError)
	if !ok {
		t.Fatalf("error = %T (%v), want *ConflictError", err, err)
	}
	if !strings.Contains(conflictErr.Message, "maximum") {
		t.Errorf("conflict message does not explain the cap: %q", conflictErr.Message)
	}
}

func TestAPIKeyService_ExpiredKeysDoNotConsumeTheCap(t *testing.T) {
	f := newServiceFixture(t, []string{"agent:*:invoke"})
	ctx := t.Context()
	owner := f.seedSystemOwner(ctx, t, "system:ci-deployer")
	f.svc.maxActiveKeys = 1

	expired := time.Now().Add(-time.Hour).UnixMilli()
	if _, err := f.keys.Create(ctx, store.APIKey{
		UserID: owner.ID, Label: "stale", KeyHash: "stalehash", Prefix: "stale123",
		ExpiresTime: &expired, CreatedBy: "admin", UpdatedBy: "admin",
	}, []string{"agent:*:invoke"}, 10, time.Now().UnixMilli()); err != nil {
		t.Fatalf("seed expired key: %v", err)
	}

	if _, err := f.svc.Create(ctx, CreateAPIKeyRequest{
		UserID: owner.ID, Label: "replacement", Scopes: []string{"agent:*:invoke"}, NeverExpires: true,
	}, "admin@example.com", testActorScopes); err != nil {
		t.Fatalf("an expired key blocked its own replacement: %v", err)
	}
}

func TestAPIKeyService_CreateFailsClosedWithoutAnOwnerScopeResolver(t *testing.T) {
	db := store.TestDB(t)
	users := store.NewUsersRepo(db)
	var unwired *AuthzLookupHandler
	svc := NewAPIKeyService(store.NewAPIKeysRepo(db), users, unwired, authorizationModeDefaultRBAC, slog.Default())

	ctx := t.Context()
	owner := &store.User{
		Issuer: rbac.SystemUserIssuer, Sub: "system:ci-deployer",
		ProvisionedVia: rbac.ProvisionedViaSystem, CreatedBy: "test", UpdatedBy: "test",
	}
	if err := users.UpsertUser(ctx, owner, nil); err != nil {
		t.Fatalf("seed owner: %v", err)
	}

	_, err := svc.Create(ctx, CreateAPIKeyRequest{
		UserID: owner.ID, Label: "k", Scopes: []string{"agent:*:invoke"}, NeverExpires: true,
	}, "admin@example.com", testActorScopes)

	if _, ok := err.(*UnavailableError); !ok {
		t.Fatalf("error = %T (%v), want *UnavailableError so the handler answers 503", err, err)
	}
}

func TestAPIKeyService_CreateNormalizesTheRequestedScopes(t *testing.T) {
	f := newServiceFixture(t, []string{"agent:*:invoke"})
	ctx := t.Context()
	owner := f.seedSystemOwner(ctx, t, "system:ci-deployer")

	resp, err := f.svc.Create(ctx, CreateAPIKeyRequest{
		UserID: owner.ID, Label: "dup",
		Scopes:       []string{" agent:*:invoke ", "agent:*:invoke", ""},
		NeverExpires: true,
	}, "admin@example.com", testActorScopes)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if len(resp.Scopes) != 1 || resp.Scopes[0] != "agent:*:invoke" {
		t.Errorf("scopes = %v, want exactly one normalized scope", resp.Scopes)
	}
}

func TestAPIKeyService_ListFiltersByOwnerAndDerivesStatus(t *testing.T) {
	f := newServiceFixture(t, []string{"agent:*:invoke"})
	ctx := t.Context()
	owner := f.seedSystemOwner(ctx, t, "system:ci-deployer")
	other := f.seedSystemOwner(ctx, t, "system:other")

	expired := time.Now().Add(-time.Hour).UnixMilli()
	for _, seed := range []struct {
		label   string
		hash    string
		user    string
		expires *int64
	}{
		{"live", "hash-live", owner.ID, nil},
		{"stale", "hash-stale", owner.ID, &expired},
		{"elsewhere", "hash-other", other.ID, nil},
	} {
		if _, err := f.keys.Create(ctx, store.APIKey{
			UserID: seed.user, Label: seed.label, KeyHash: seed.hash, Prefix: "pfx12345",
			ExpiresTime: seed.expires, CreatedBy: "admin", UpdatedBy: "admin",
		}, []string{"agent:*:invoke"}, 10, time.Now().UnixMilli()); err != nil {
			t.Fatalf("seed %s: %v", seed.label, err)
		}
	}

	page, err := f.svc.List(ctx, APIKeyListQuery{PageNumber: 1, PageSize: 20, UserID: owner.ID})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if page.Meta.Pagination.Count != 2 {
		t.Errorf("count = %d, want 2 (the other owner's key must not appear)", page.Meta.Pagination.Count)
	}
	statuses := map[string]string{}
	for _, key := range page.Data {
		statuses[key.Label] = key.Status
	}
	if statuses["live"] != store.APIKeyStatusActive || statuses["stale"] != store.APIKeyStatusExpired {
		t.Errorf("derived statuses = %v", statuses)
	}

	activeOnly, err := f.svc.List(ctx, APIKeyListQuery{
		PageNumber: 1, PageSize: 20, UserID: owner.ID, Status: store.APIKeyStatusActive,
	})
	if err != nil {
		t.Fatalf("list active: %v", err)
	}
	if got := activeOnly.Meta.Pagination.Count; got != 1 {
		t.Errorf("active count = %d, want 1", got)
	}
}

func TestAPIKeyService_ListRejectsUnsupportedSortAndStatus(t *testing.T) {
	f := newServiceFixture(t, nil)
	ctx := t.Context()

	for _, query := range []APIKeyListQuery{
		{PageNumber: 1, PageSize: 20, SortField: "keyHash"},
		{PageNumber: 1, PageSize: 20, SortField: "key_hash"},
		{PageNumber: 1, PageSize: 20, Status: "revoked"},
	} {
		if _, err := f.svc.List(ctx, query); err == nil {
			t.Errorf("accepted %+v", query)
		} else if _, ok := err.(*ValidationError); !ok {
			t.Errorf("error for %+v = %T, want *ValidationError so the handler answers 4xx", query, err)
		}
	}
}

func TestAPIKeyService_UpdateRenamesWithoutTouchingScopes(t *testing.T) {
	f := newServiceFixture(t, []string{"agent:*:invoke"})
	ctx := t.Context()
	owner := f.seedSystemOwner(ctx, t, "system:ci-deployer")
	created, err := f.svc.Create(ctx, CreateAPIKeyRequest{
		UserID: owner.ID, Label: "before", Scopes: []string{"agent:*:invoke"}, NeverExpires: true,
	}, "admin@example.com", testActorScopes)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	updated, err := f.svc.UpdateLabel(ctx, created.ID, UpdateAPIKeyRequest{Label: "after"}, "other@example.com")
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.Label != "after" {
		t.Errorf("label = %q, want %q", updated.Label, "after")
	}
	if updated.UpdatedBy != "other@example.com" {
		t.Errorf("updatedBy = %q", updated.UpdatedBy)
	}
	if len(updated.Scopes) != 1 || updated.Scopes[0] != "agent:*:invoke" {
		t.Errorf("scopes changed on rename: %v", updated.Scopes)
	}
	if updated.Prefix != created.Prefix {
		t.Errorf("prefix changed on rename: %q -> %q", created.Prefix, updated.Prefix)
	}
}

func TestAPIKeyService_UpdateRejectsABlankLabel(t *testing.T) {
	f := newServiceFixture(t, nil)
	_, err := f.svc.UpdateLabel(t.Context(), "3f2504e0-4f89-11d3-9a0c-0305e82c3301",
		UpdateAPIKeyRequest{Label: "  "}, "admin@example.com")
	if validationErr := requireValidationError(t, err); len(validationErr.Details["label"]) == 0 {
		t.Errorf("expected a label detail, got %v", validationErr.Details)
	}
}

func TestAPIKeyService_LabelFollowsTheSharedNameRules(t *testing.T) {
	longestAllowed := strings.Repeat("é", maxAPIKeyLabelLen)
	refused := map[string]string{
		"one character too long": strings.Repeat("é", maxAPIKeyLabelLen+1),
		"a newline":              "deploy\npipeline",
		"a tab":                  "deploy\tpipeline",
		"a NUL":                  "deploy\x00pipeline",
	}

	f := newServiceFixture(t, []string{"agent:*:invoke"})
	ctx := t.Context()
	owner := f.seedSystemOwner(ctx, t, "system:ci-deployer")
	create := func(label string) (*APIKeyResponse, error) {
		return f.svc.Create(ctx, CreateAPIKeyRequest{
			UserID: owner.ID, Label: label, Scopes: []string{"agent:*:invoke"}, NeverExpires: true,
		}, "admin@example.com", testActorScopes)
	}

	created, err := create(longestAllowed)
	if err != nil {
		t.Fatalf("create with a %d-character multi-byte label: %v", maxAPIKeyLabelLen, err)
	}
	if created.Label != longestAllowed {
		t.Errorf("label was altered on create")
	}
	if _, err := f.svc.UpdateLabel(ctx, created.ID, UpdateAPIKeyRequest{Label: longestAllowed}, "admin@example.com"); err != nil {
		t.Errorf("update with a %d-character multi-byte label: %v", maxAPIKeyLabelLen, err)
	}

	for name, label := range refused {
		t.Run(name, func(t *testing.T) {
			_, createErr := create(label)
			if validationErr := requireValidationError(t, createErr); len(validationErr.Details["label"]) == 0 {
				t.Errorf("create: expected a label detail, got %v", validationErr.Details)
			}
			_, updateErr := f.svc.UpdateLabel(ctx, created.ID, UpdateAPIKeyRequest{Label: label}, "admin@example.com")
			if validationErr := requireValidationError(t, updateErr); len(validationErr.Details["label"]) == 0 {
				t.Errorf("update: expected a label detail, got %v", validationErr.Details)
			}
		})
	}
}

func TestAPIKeyService_DeleteRevokesImmediatelyAndIsNotIdempotent(t *testing.T) {
	f := newServiceFixture(t, []string{"agent:*:invoke"})
	ctx := t.Context()
	owner := f.seedSystemOwner(ctx, t, "system:ci-deployer")
	created, err := f.svc.Create(ctx, CreateAPIKeyRequest{
		UserID: owner.ID, Label: "doomed", Scopes: []string{"agent:*:invoke"}, NeverExpires: true,
	}, "admin@example.com", testActorScopes)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if err := f.svc.Delete(ctx, created.ID, "admin@example.com"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := f.keys.GetByKeyHash(ctx, apikey.Hash(created.Secret)); err == nil {
		t.Error("the deleted key still resolves by hash, so it would still authenticate")
	}

	if err := f.svc.Delete(ctx, created.ID, "admin@example.com"); err == nil {
		t.Error("deleting a deleted key succeeded")
	} else if _, ok := err.(*NotFoundError); !ok {
		t.Errorf("second delete error = %T, want *NotFoundError", err)
	}
}

func TestAPIKeyService_GetReportsAnUnknownIDAsNotFound(t *testing.T) {
	f := newServiceFixture(t, nil)
	_, err := f.svc.Get(t.Context(), "3f2504e0-4f89-11d3-9a0c-0305e82c3301")
	if _, ok := err.(*NotFoundError); !ok {
		t.Fatalf("error = %T (%v), want *NotFoundError", err, err)
	}
}

func requireValidationError(t *testing.T, err error) *ValidationError {
	t.Helper()
	validationErr, ok := err.(*ValidationError)
	if !ok {
		t.Fatalf("error = %T (%v), want *ValidationError", err, err)
	}
	return validationErr
}

func TestAPIKeyService_ReadPathsNeverMarshalTheSecret(t *testing.T) {
	f := newServiceFixture(t, []string{"agent:*:invoke"})
	ctx := t.Context()
	owner := f.seedSystemOwner(ctx, t, "system:reader")

	created, err := f.svc.Create(ctx, CreateAPIKeyRequest{
		UserID: owner.ID, Label: "k", Scopes: []string{"agent:*:invoke"}, NeverExpires: true,
	}, "admin@example.com", testActorScopes)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.Secret == "" {
		t.Fatal("create must return the plaintext once")
	}

	assertNoSecret := func(t *testing.T, what string, v any) {
		t.Helper()
		encoded, marshalErr := json.Marshal(v)
		if marshalErr != nil {
			t.Fatalf("marshal %s: %v", what, marshalErr)
		}
		if bytes.Contains(encoded, []byte(`"secret"`)) {
			t.Errorf("%s response carries a secret field: %s", what, encoded)
		}
		if bytes.Contains(encoded, []byte(created.Secret)) {
			t.Errorf("%s response carries the plaintext credential", what)
		}
	}

	got, err := f.svc.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	assertNoSecret(t, "get", got)

	page, err := f.svc.List(ctx, APIKeyListQuery{PageNumber: 1, PageSize: 10, UserID: owner.ID})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	assertNoSecret(t, "list", page)

	renamed, err := f.svc.UpdateLabel(ctx, created.ID, UpdateAPIKeyRequest{Label: "renamed"}, "admin@example.com")
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	assertNoSecret(t, "update", renamed)
}

func TestAPIKeyService_CreateRequiresDefaultRBAC(t *testing.T) {
	for _, mode := range []string{"none", "deny_all", ""} {
		t.Run("mode="+mode, func(t *testing.T) {
			f := newServiceFixture(t, []string{"agent:*:invoke"})
			ctx := t.Context()
			owner := f.seedSystemOwner(ctx, t, "system:wrong-mode")
			f.svc.authorizationMode = mode

			_, err := f.svc.Create(ctx, CreateAPIKeyRequest{
				UserID: owner.ID, Label: "k", Scopes: []string{"agent:*:invoke"}, NeverExpires: true,
			}, "admin@example.com", testActorScopes)

			if _, ok := err.(*ForbiddenError); !ok {
				t.Fatalf("error = %T (%v), want *ForbiddenError so the handler answers 403", err, err)
			}
		})
	}
}

func TestAPIKeyService_CreateRefusesScopesTheActorDoesNotHold(t *testing.T) {
	f := newServiceFixture(t, []string{"agent:*:invoke", "rbac:_:update"})
	ctx := t.Context()
	owner := f.seedSystemOwner(ctx, t, "system:regrant")

	_, err := f.svc.Create(ctx, CreateAPIKeyRequest{
		UserID: owner.ID, Label: "k",
		Scopes: []string{"agent:*:invoke", "rbac:_:update"}, NeverExpires: true,
	}, "narrow@example.com", []string{"agent:*:invoke"})

	forbidden, ok := err.(*ForbiddenError)
	if !ok {
		t.Fatalf("error = %T (%v), want *ForbiddenError", err, err)
	}
	if !strings.Contains(forbidden.Message, "rbac:_:update") {
		t.Errorf("refusal %q does not name the scope the actor lacks", forbidden.Message)
	}
	if strings.Contains(forbidden.Message, "agent:*:invoke") {
		t.Errorf("refusal %q names a scope the actor does hold", forbidden.Message)
	}
}

func TestAPIKeyService_CreateRefusesAnEmptyScopeSelection(t *testing.T) {
	for _, name := range []string{"nil", "empty", "blank-only"} {
		t.Run(name, func(t *testing.T) {
			f := newServiceFixture(t, []string{"agent:*:invoke"})
			ctx := t.Context()
			owner := f.seedSystemOwner(ctx, t, "system:scopeless")

			var scopes []string
			switch name {
			case "empty":
				scopes = []string{}
			case "blank-only":
				scopes = []string{"", "   "}
			}

			_, err := f.svc.Create(ctx, CreateAPIKeyRequest{
				UserID: owner.ID, Label: "k", Scopes: scopes, NeverExpires: true,
			}, "admin@example.com", testActorScopes)

			validationErr := requireValidationError(t, err)
			detail := strings.Join(validationErr.Details["scopes"], " ")
			if !strings.Contains(detail, "at least one scope") {
				t.Errorf("refusal %q does not ask for at least one scope", detail)
			}
		})
	}
}

func TestAPIKeyService_ListDefaultsToNewestFirst(t *testing.T) {
	f := newServiceFixture(t, []string{"agent:*:invoke"})
	ctx := t.Context()
	owner := f.seedSystemOwner(ctx, t, "system:ci-deployer")

	start := time.Now()
	for i, label := range []string{"older", "newer"} {
		f.svc.now = func() time.Time { return start.Add(time.Duration(i) * time.Second) }
		if _, err := f.svc.Create(ctx, CreateAPIKeyRequest{
			UserID: owner.ID, Label: label, Scopes: []string{"agent:*:invoke"}, NeverExpires: true,
		}, "admin@example.com", testActorScopes); err != nil {
			t.Fatalf("create %s: %v", label, err)
		}
	}

	page, err := f.svc.List(ctx, APIKeyListQuery{PageNumber: 1, PageSize: 10, SortOrder: "asc"})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(page.Data) != 2 || page.Data[0].Label != "newer" || page.Data[1].Label != "older" {
		t.Errorf("default order = %v, want newer then older", []string{page.Data[0].Label, page.Data[1].Label})
	}
}

func TestAPIKeyService_CreateListsScopesInTheOrderReadsDo(t *testing.T) {
	f := newServiceFixture(t, []string{"agent:*:invoke", "agent_builder:*:deploy"})
	ctx := t.Context()
	owner := f.seedSystemOwner(ctx, t, "system:ci-deployer")

	created, err := f.svc.Create(ctx, CreateAPIKeyRequest{
		UserID: owner.ID, Label: "ordered",
		Scopes: []string{"agent_builder:*:deploy", "agent:*:invoke"}, NeverExpires: true,
	}, "admin@example.com", testActorScopes)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	read, err := f.svc.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	want := []string{"agent:*:invoke", "agent_builder:*:deploy"}
	if !slices.Equal(created.Scopes, want) || !slices.Equal(read.Scopes, want) {
		t.Errorf("create scopes = %v, get scopes = %v, want both %v", created.Scopes, read.Scopes, want)
	}
}

func TestAPIKeyService_CapIsEvaluatedAtTheServiceInstant(t *testing.T) {
	f := newServiceFixture(t, []string{"agent:*:invoke"})
	ctx := t.Context()
	owner := f.seedSystemOwner(ctx, t, "system:ci-deployer")
	f.svc.maxActiveKeys = 1

	expiresAt := time.Now().Add(24 * time.Hour)
	if _, err := f.keys.Create(ctx, store.APIKey{
		UserID: owner.ID, Label: "lapses-tomorrow", KeyHash: "tomorrowhash", Prefix: "tmrw1234",
		ExpiresTime: new(expiresAt.UnixMilli()), CreatedBy: "admin", UpdatedBy: "admin",
	}, []string{"agent:*:invoke"}, 10, time.Now().UnixMilli()); err != nil {
		t.Fatalf("seed: %v", err)
	}

	f.svc.now = func() time.Time { return expiresAt }
	if _, err := f.svc.Create(ctx, CreateAPIKeyRequest{
		UserID: owner.ID, Label: "replacement", Scopes: []string{"agent:*:invoke"}, NeverExpires: true,
	}, "admin@example.com", testActorScopes); err != nil {
		t.Errorf("create at the old key's expiry instant was refused: %v", err)
	}
}

func TestAPIKeyService_AuditLinesRecordEachChange(t *testing.T) {
	f := newServiceFixture(t, []string{"agent:*:invoke"})
	ctx := t.Context()
	owner := f.seedSystemOwner(ctx, t, "system:audited")

	created, err := f.svc.Create(ctx, CreateAPIKeyRequest{
		UserID: owner.ID, Label: "audited", Scopes: []string{"agent:*:invoke"}, ExpiresTime: futureExpiry(time.Hour),
	}, "creator@example.com", testActorScopes)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := f.svc.UpdateLabel(ctx, created.ID, UpdateAPIKeyRequest{Label: "renamed"}, "renamer@example.com"); err != nil {
		t.Fatalf("rename: %v", err)
	}
	if err := f.svc.Delete(ctx, created.ID, "deleter@example.com"); err != nil {
		t.Fatalf("delete: %v", err)
	}

	logged := f.logs.String()
	if strings.Contains(logged, created.Secret) || strings.Contains(logged, apikey.Hash(created.Secret)) {
		t.Fatal("the audit stream carries the credential or its hash")
	}
	if strings.Contains(logged, apikey.Marker) {
		t.Errorf("the audit stream carries a %s string: %s", apikey.Marker, logged)
	}

	audits := map[string]map[string]any{}
	for line := range strings.SplitSeq(strings.TrimSpace(logged), "\n") {
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("log line is not JSON: %s", line)
		}
		if record["event"] == "rbac_mutation" && record["resourceId"] == created.ID {
			audits[record["action"].(string)] = record
		}
	}
	for action, actor := range map[string]string{
		auditActionCreated: "creator@example.com",
		auditActionUpdated: "renamer@example.com",
		auditActionDeleted: "deleter@example.com",
	} {
		record, ok := audits[action]
		if !ok {
			t.Errorf("no %q audit line for the key", action)
			continue
		}
		if record["actor"] != actor || record["ownerId"] != owner.ID || record["prefix"] != created.Prefix {
			t.Errorf("%s audit = actor %v owner %v prefix %v, want %s %s %s",
				action, record["actor"], record["ownerId"], record["prefix"], actor, owner.ID, created.Prefix)
		}
	}
	if createdLine := audits[auditActionCreated]; createdLine != nil {
		if scopes, _ := createdLine["scopes"].([]any); len(scopes) != 1 || scopes[0] != "agent:*:invoke" {
			t.Errorf("create audit scopes = %v, want [agent:*:invoke]", createdLine["scopes"])
		}
		if want := wireInstant(int64(*created.ExpiresTime)); createdLine["expiresTime"] != want {
			t.Errorf("create audit expiresTime = %v, want %s", createdLine["expiresTime"], want)
		}
	}
}

func TestAPIKeyService_CreateAuditIsReadableInTextFormat(t *testing.T) {
	f := newServiceFixture(t, []string{"agent:*:invoke"})
	ctx := t.Context()
	owner := f.seedSystemOwner(ctx, t, "system:text-audited")
	text := &syncedBuffer{}
	f.svc.logger = slog.New(slog.NewTextHandler(text, nil))

	expiring, err := f.svc.Create(ctx, CreateAPIKeyRequest{
		UserID: owner.ID, Label: "expiring", Scopes: []string{"agent:*:invoke"}, ExpiresTime: futureExpiry(time.Hour),
	}, "creator@example.com", testActorScopes)
	if err != nil {
		t.Fatalf("create expiring: %v", err)
	}
	if _, err := f.svc.Create(ctx, CreateAPIKeyRequest{
		UserID: owner.ID, Label: "forever", Scopes: []string{"agent:*:invoke"}, NeverExpires: true,
	}, "creator@example.com", testActorScopes); err != nil {
		t.Fatalf("create non-expiring: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(text.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("want one audit line per key, got %d:\n%s", len(lines), text.String())
	}
	if want := "expiresTime=" + wireInstant(int64(*expiring.ExpiresTime)); !strings.Contains(lines[0], want) {
		t.Errorf("expiring key's audit line lacks %q: %s", want, lines[0])
	}
	if !strings.Contains(lines[1], "neverExpires=true") || strings.Contains(lines[1], "expiresTime=") {
		t.Errorf("non-expiring key's audit line should say neverExpires=true and carry no expiry: %s", lines[1])
	}
}

func TestAPIKeyService_RenameIsAuditedEvenWhenTheResponseCannotBeBuilt(t *testing.T) {
	f := newServiceFixture(t, []string{"agent:*:invoke"})
	ctx := t.Context()
	owner := f.seedSystemOwner(ctx, t, "system:rename-audit")
	created, err := f.svc.Create(ctx, CreateAPIKeyRequest{
		UserID: owner.ID, Label: "before", Scopes: []string{"agent:*:invoke"}, NeverExpires: true,
	}, "admin@example.com", testActorScopes)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := f.db.ExecContext(ctx, `DROP TABLE api_key_scopes`); err != nil {
		t.Fatalf("drop api_key_scopes: %v", err)
	}

	if _, err := f.svc.UpdateLabel(ctx, created.ID, UpdateAPIKeyRequest{Label: "after"}, "admin@example.com"); err == nil {
		t.Fatal("rename succeeded without its scopes table; the test did not break the read")
	}
	if !strings.Contains(f.logs.String(), `"action":"`+auditActionUpdated+`"`) {
		t.Error("the committed rename was not audited")
	}
}

func TestAPIKeyService_RenameOfAnUnknownKeyIsNotFound(t *testing.T) {
	f := newServiceFixture(t, nil)
	_, err := f.svc.UpdateLabel(t.Context(), "3f2504e0-4f89-11d3-9a0c-0305e82c3301",
		UpdateAPIKeyRequest{Label: "valid"}, "admin@example.com")
	if _, ok := err.(*NotFoundError); !ok {
		t.Errorf("error = %T (%v), want *NotFoundError", err, err)
	}
}

func TestAPIKeyService_CreateChecksTheActorBeforeTheOwner(t *testing.T) {
	f := newServiceFixture(t, []string{"agent:*:invoke"})
	ctx := t.Context()
	owner := f.seedSystemOwner(ctx, t, "system:probed")

	_, err := f.svc.Create(ctx, CreateAPIKeyRequest{
		UserID: owner.ID, Label: "k", Scopes: []string{"rbac:*:update"}, NeverExpires: true,
	}, "narrow@example.com", []string{"agent:*:invoke"})
	if _, ok := err.(*ForbiddenError); !ok {
		t.Errorf("error = %T (%v), want *ForbiddenError from the actor check", err, err)
	}
}

func TestAPIKeyService_ExpiryBoundaries(t *testing.T) {
	f := newServiceFixture(t, []string{"agent:*:invoke"})
	ctx := t.Context()
	owner := f.seedSystemOwner(ctx, t, "system:boundary")
	now := time.UnixMilli(1_800_000_000_000)
	f.svc.now = func() time.Time { return now }
	create := func(expires int64) (*APIKeyResponse, error) {
		return f.svc.Create(ctx, CreateAPIKeyRequest{
			UserID: owner.ID, Label: "k", Scopes: []string{"agent:*:invoke"}, ExpiresTime: new(RESTTimestamp(expires)),
		}, "admin@example.com", testActorScopes)
	}

	if _, err := create(now.UnixMilli()); err == nil {
		t.Error("an expiry equal to now was accepted")
	}
	created, err := create(now.UnixMilli() + 1)
	if err != nil {
		t.Fatalf("an expiry one millisecond after now was refused: %v", err)
	}
	if created.Status != store.APIKeyStatusActive {
		t.Errorf("status one millisecond before expiry = %q, want active", created.Status)
	}
	f.svc.now = func() time.Time { return now.Add(time.Millisecond) }
	read, err := f.svc.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if read.Status != store.APIKeyStatusExpired {
		t.Errorf("status at the expiry instant = %q, want expired", read.Status)
	}
}

func TestAPIKeyService_EmptyListMarshalsAnEmptyArray(t *testing.T) {
	f := newServiceFixture(t, nil)
	page, err := f.svc.List(t.Context(), APIKeyListQuery{PageNumber: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	encoded, err := json.Marshal(page)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.HasPrefix(string(encoded), `{"data":[]`) {
		t.Errorf("empty list = %s, want data: []", encoded)
	}
}

func TestAPIKeyService_ListSortsByEveryWireFieldAndPages(t *testing.T) {
	f := newServiceFixture(t, []string{"agent:*:invoke"})
	ctx := t.Context()
	owner := f.seedSystemOwner(ctx, t, "system:wire-sort")
	base := time.Now()
	hour := time.Hour.Milliseconds()
	for i, s := range []struct {
		label, prefix string
		expires       *int64
		updated       int64
		lastUsed      int64
	}{
		{"d", "pfx-2", new(base.UnixMilli() + hour), 300, 20},
		{"B", "pfx-1", new(base.UnixMilli() + 2*hour), 200, 0},
		{"a", "pfx-4", nil, 400, 10},
		{"c", "pfx-3", new(base.UnixMilli() + 48*hour), 100, 30},
	} {
		created, err := f.keys.Create(ctx, store.APIKey{
			UserID: owner.ID, Label: s.label, KeyHash: "wire-" + s.label, Prefix: s.prefix,
			ExpiresTime: s.expires, CreatedBy: "admin", UpdatedBy: "admin",
		}, []string{"agent:*:invoke"}, 10, base.UnixMilli()+int64(i))
		if err != nil {
			t.Fatalf("seed %s: %v", s.label, err)
		}
		if _, err := f.db.ExecContext(ctx, `UPDATE api_keys SET updated_time = ? WHERE id = ?`, s.updated, created.ID); err != nil {
			t.Fatalf("set updated_time: %v", err)
		}
		if s.lastUsed > 0 {
			if err := f.keys.TouchLastUsed(ctx, created.ID, s.lastUsed); err != nil {
				t.Fatalf("touch: %v", err)
			}
		}
	}
	ascending := map[string][]string{
		"label":        {"a", "B", "c", "d"},
		"prefix":       {"B", "d", "c", "a"},
		"createdTime":  {"d", "B", "a", "c"},
		"updatedTime":  {"c", "B", "d", "a"},
		"expiresTime":  {"d", "B", "c", "a"},
		"lastUsedTime": {"B", "a", "d", "c"},
	}
	if len(ascending) != len(apiKeySortColumns) {
		t.Fatalf("test covers %d sort fields, the service exposes %d", len(ascending), len(apiKeySortColumns))
	}
	labels := func(t *testing.T, q APIKeyListQuery) []string {
		t.Helper()
		page, err := f.svc.List(ctx, q)
		if err != nil {
			t.Fatalf("list %+v: %v", q, err)
		}
		out := make([]string, 0, len(page.Data))
		for _, k := range page.Data {
			out = append(out, k.Label)
		}
		return out
	}
	for field, want := range ascending {
		t.Run(field, func(t *testing.T) {
			if got := labels(t, APIKeyListQuery{PageNumber: 1, PageSize: 10, SortField: field, SortOrder: "asc"}); !slices.Equal(got, want) {
				t.Errorf("asc = %v, want %v", got, want)
			}
			descending := slices.Clone(want)
			slices.Reverse(descending)
			if got := labels(t, APIKeyListQuery{PageNumber: 1, PageSize: 10, SortField: field, SortOrder: "desc"}); !slices.Equal(got, descending) {
				t.Errorf("desc = %v, want %v", got, descending)
			}
		})
	}
	if got := labels(t, APIKeyListQuery{PageNumber: 2, PageSize: 2, SortField: "label", SortOrder: "asc"}); !slices.Equal(got, []string{"c", "d"}) {
		t.Errorf("page 2 = %v, want [c d]", got)
	}
	second, err := f.svc.List(ctx, APIKeyListQuery{PageNumber: 2, PageSize: 2, SortField: "label", SortOrder: "asc"})
	if err != nil {
		t.Fatalf("list page 2: %v", err)
	}
	if meta := second.Meta.Pagination; meta.PageNumber != 2 || meta.Count != 4 || meta.TotalPages != 2 {
		t.Errorf("page 2 meta = %+v, want page 2 of 2 with count 4", meta)
	}
}

// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"
)

func seedKeyOwner(ctx context.Context, t *testing.T, db *DB, sub string) string {
	t.Helper()
	owner := &User{
		Issuer: "sam:system", Sub: sub,
		DisplayName:    new("Key Owner"),
		ProvisionedVia: provisionedViaSystem,
		CreatedBy:      "test", UpdatedBy: "test",
	}
	if err := NewUsersRepo(db).UpsertUser(ctx, owner, nil); err != nil {
		t.Fatalf("seed key owner %q: %v", sub, err)
	}
	return owner.ID
}

func nowMS() int64 { return time.Now().UnixMilli() }

func keyFor(ownerID, label, hash string, expires *int64) APIKey {
	return APIKey{
		UserID:      ownerID,
		Label:       label,
		KeyHash:     hash,
		Prefix:      "abcd1234",
		ExpiresTime: expires,
		CreatedBy:   "admin", UpdatedBy: "admin",
	}
}

func TestAPIKeysRepo_CreateAndReadBack(t *testing.T) {
	db := TestDB(t)
	ctx := t.Context()
	repo := NewAPIKeysRepo(db)
	ownerID := seedKeyOwner(ctx, t, db, "system:ci-deployer")

	expires := time.Now().Add(24 * time.Hour).UnixMilli()
	want := []string{"agent_builder:_:create", "agent_builder:*:deploy"}
	created, err := repo.Create(ctx, keyFor(ownerID, "deploy-pipeline", "hash-a", &expires), want, 10, nowMS())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.ID == "" {
		t.Error("Create did not assign an id")
	}
	if created.CreatedTime == 0 || created.UpdatedTime == 0 {
		t.Error("Create did not stamp created/updated time")
	}

	byID, err := repo.GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if byID.Label != "deploy-pipeline" || byID.UserID != ownerID {
		t.Errorf("GetByID returned %+v", byID)
	}
	if byID.ExpiresTime == nil || *byID.ExpiresTime != expires {
		t.Errorf("ExpiresTime = %v, want %d", byID.ExpiresTime, expires)
	}
	if byID.LastUsedTime != nil {
		t.Errorf("a new key reports LastUsedTime %v; want nil", byID.LastUsedTime)
	}

	byHash, err := repo.GetByKeyHash(ctx, "hash-a")
	if err != nil {
		t.Fatalf("GetByKeyHash: %v", err)
	}
	if byHash.ID != created.ID {
		t.Errorf("GetByKeyHash returned id %q, want %q", byHash.ID, created.ID)
	}

	scopes, err := repo.ScopesForKeys(ctx, []string{created.ID})
	if err != nil {
		t.Fatalf("ScopesForKeys: %v", err)
	}
	wantOrdered := []string{"agent_builder:*:deploy", "agent_builder:_:create"}
	if got := scopes[created.ID]; !slices.Equal(got, wantOrdered) {
		t.Errorf("scopes = %v, want %v", got, wantOrdered)
	}
}

func TestAPIKeysRepo_CreateRejectsEmptyScopes(t *testing.T) {
	db := TestDB(t)
	ctx := t.Context()
	repo := NewAPIKeysRepo(db)
	ownerID := seedKeyOwner(ctx, t, db, "system:no-scopes")

	if _, err := repo.Create(ctx, keyFor(ownerID, "empty", "hash-empty", nil), nil, 10, nowMS()); err == nil {
		t.Fatal("Create accepted an empty scope set")
	}
	_, total, err := repo.ListPaginated(ctx, APIKeyListOptions{PageNumber: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("ListPaginated: %v", err)
	}
	if total != 0 {
		t.Errorf("refused create left %d rows behind", total)
	}
}

func TestAPIKeysRepo_GetByKeyHashMissIsNotFound(t *testing.T) {
	db := TestDB(t)
	ctx := t.Context()
	repo := NewAPIKeysRepo(db)

	for _, hash := range []string{"", "no-such-hash"} {
		if _, err := repo.GetByKeyHash(ctx, hash); !errors.Is(err, ErrAPIKeyNotFound) {
			t.Errorf("GetByKeyHash(%q) error = %v, want ErrAPIKeyNotFound", hash, err)
		}
	}
}

func TestAPIKeysRepo_CapRefusesTheKeyPastTheLimit(t *testing.T) {
	db := TestDB(t)
	ctx := t.Context()
	repo := NewAPIKeysRepo(db)
	ownerID := seedKeyOwner(ctx, t, db, "system:at-the-cap")
	otherID := seedKeyOwner(ctx, t, db, "system:has-room")

	const cap = 3
	for i := range cap {
		if _, err := repo.Create(ctx,
			keyFor(ownerID, fmt.Sprintf("key-%d", i), fmt.Sprintf("hash-%d", i), nil),
			[]string{"agent:*:invoke"}, cap, nowMS()); err != nil {
			t.Fatalf("Create %d of %d: %v", i+1, cap, err)
		}
	}
	_, err := repo.Create(ctx, keyFor(ownerID, "one-too-many", "hash-over", nil),
		[]string{"agent:*:invoke"}, cap, nowMS())
	if !errors.Is(err, ErrAPIKeyCapReached) {
		t.Fatalf("Create past the cap error = %v, want ErrAPIKeyCapReached", err)
	}

	_, total, err := repo.ListPaginated(ctx, APIKeyListOptions{PageNumber: 1, PageSize: 50, UserID: ownerID})
	if err != nil {
		t.Fatalf("ListPaginated: %v", err)
	}
	if total != cap {
		t.Errorf("owner holds %d keys, want %d", total, cap)
	}

	if _, err := repo.Create(ctx, keyFor(otherID, "unaffected", "hash-other", nil),
		[]string{"agent:*:invoke"}, cap, nowMS()); err != nil {
		t.Errorf("a second owner was refused because the first is at the cap: %v", err)
	}
}

func TestAPIKeysRepo_CapCountsAKeyExpiringNowAsExpired(t *testing.T) {
	db := TestDB(t)
	ctx := t.Context()
	repo := NewAPIKeysRepo(db)
	ownerID := seedKeyOwner(ctx, t, db, "system:cap-boundary")

	const expiresAt int64 = 1_000_000
	if _, err := repo.Create(ctx, keyFor(ownerID, "expiring", "hash-expiring", new(expiresAt)),
		[]string{"agent:*:invoke"}, 1, expiresAt-10); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := repo.Create(ctx, keyFor(ownerID, "early", "hash-early", nil),
		[]string{"agent:*:invoke"}, 1, expiresAt-1); !errors.Is(err, ErrAPIKeyCapReached) {
		t.Errorf("create one millisecond before the expiry = %v, want ErrAPIKeyCapReached", err)
	}
	if _, err := repo.Create(ctx, keyFor(ownerID, "on-time", "hash-on-time", nil),
		[]string{"agent:*:invoke"}, 1, expiresAt); err != nil {
		t.Errorf("create at the expiry instant was refused: %v", err)
	}
}

func TestAPIKeysRepo_ExpiredKeysDoNotCountTowardTheCap(t *testing.T) {
	db := TestDB(t)
	ctx := t.Context()
	repo := NewAPIKeysRepo(db)
	ownerID := seedKeyOwner(ctx, t, db, "system:all-lapsed")

	lapsed := time.Now().Add(-time.Hour).UnixMilli()
	const cap = 2
	for i := range cap {
		if _, err := repo.Create(ctx,
			keyFor(ownerID, fmt.Sprintf("old-%d", i), fmt.Sprintf("old-hash-%d", i), &lapsed),
			[]string{"agent:*:invoke"}, cap, nowMS()); err != nil {
			t.Fatalf("seed expired key %d: %v", i, err)
		}
	}
	if _, err := repo.Create(ctx, keyFor(ownerID, "replacement", "hash-new", nil),
		[]string{"agent:*:invoke"}, cap, nowMS()); err != nil {
		t.Fatalf("Create with only expired keys present: %v", err)
	}
}

func TestAPIKeysRepo_CapHoldsUnderConcurrentCreates(t *testing.T) {
	db := TestDB(t)
	ctx := t.Context()
	repo := NewAPIKeysRepo(db)
	ownerID := seedKeyOwner(ctx, t, db, "system:concurrent")

	const (
		cap      = 5
		attempts = 20
	)
	var (
		mu        sync.Mutex
		succeeded int
		capErrors int
		other     []error
		wg        sync.WaitGroup
	)
	for i := range attempts {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := repo.Create(ctx,
				keyFor(ownerID, fmt.Sprintf("race-%d", i), fmt.Sprintf("race-hash-%d", i), nil),
				[]string{"agent:*:invoke"}, cap, nowMS())
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				succeeded++
			case errors.Is(err, ErrAPIKeyCapReached):
				capErrors++
			default:
				other = append(other, err)
			}
		}(i)
	}
	wg.Wait()

	if len(other) > 0 {
		t.Fatalf("%d creates failed for reasons other than the cap; first: %v", len(other), other[0])
	}
	if succeeded != cap {
		t.Errorf("%d creates succeeded, want exactly %d", succeeded, cap)
	}
	if capErrors != attempts-cap {
		t.Errorf("%d creates hit the cap, want %d", capErrors, attempts-cap)
	}

	_, total, err := repo.ListPaginated(ctx, APIKeyListOptions{PageNumber: 1, PageSize: 100, UserID: ownerID})
	if err != nil {
		t.Fatalf("ListPaginated: %v", err)
	}
	if total != cap {
		t.Errorf("owner ended with %d keys, want %d — the cap leaked under concurrency", total, cap)
	}
}

func TestAPIKeysRepo_DeletingTheOwnerCascadesItsKeys(t *testing.T) {
	db := TestDB(t)
	ctx := t.Context()
	repo := NewAPIKeysRepo(db)
	ownerID := seedKeyOwner(ctx, t, db, "system:doomed")
	survivorID := seedKeyOwner(ctx, t, db, "system:survivor")

	doomed, err := repo.Create(ctx, keyFor(ownerID, "doomed-key", "hash-doomed", nil),
		[]string{"agent:*:invoke"}, 10, nowMS())
	if err != nil {
		t.Fatalf("Create doomed key: %v", err)
	}
	if _, createErr := repo.Create(ctx, keyFor(survivorID, "survivor-key", "hash-survivor", nil),
		[]string{"agent:*:invoke"}, 10, nowMS()); createErr != nil {
		t.Fatalf("Create survivor key: %v", createErr)
	}

	if deleteErr := NewUsersRepo(db).DeleteSystemUser(ctx, ownerID); deleteErr != nil {
		t.Fatalf("delete owner: %v", deleteErr)
	}

	if _, getErr := repo.GetByID(ctx, doomed.ID); !errors.Is(getErr, ErrAPIKeyNotFound) {
		t.Errorf("key survived its owner: err = %v", getErr)
	}
	if _, hashErr := repo.GetByKeyHash(ctx, "hash-doomed"); !errors.Is(hashErr, ErrAPIKeyNotFound) {
		t.Error("deleted owner's key still resolves by hash — it would still authenticate")
	}
	scopes, err := repo.ScopesForKeys(ctx, []string{doomed.ID})
	if err != nil {
		t.Fatalf("ScopesForKeys: %v", err)
	}
	if len(scopes[doomed.ID]) != 0 {
		t.Errorf("orphaned scope rows survived the cascade: %v", scopes[doomed.ID])
	}
	if _, err := repo.GetByKeyHash(ctx, "hash-survivor"); err != nil {
		t.Errorf("unrelated owner's key was collateral damage: %v", err)
	}
}

func TestAPIKeysRepo_DeletingAKeyCascadesItsScopes(t *testing.T) {
	db := TestDB(t)
	ctx := t.Context()
	repo := NewAPIKeysRepo(db)
	ownerID := seedKeyOwner(ctx, t, db, "system:delete-one")

	key, err := repo.Create(ctx, keyFor(ownerID, "temp", "hash-temp", nil),
		[]string{"agent:*:invoke", "agent_builder:_:create"}, 10, nowMS())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := repo.Delete(ctx, key.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	var orphans int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM api_key_scopes WHERE api_key_id = ?`, key.ID).Scan(&orphans); err != nil {
		t.Fatalf("count orphan scopes: %v", err)
	}
	if orphans != 0 {
		t.Errorf("%d scope rows outlived their key", orphans)
	}
	if _, err := repo.GetByKeyHash(ctx, "hash-temp"); !errors.Is(err, ErrAPIKeyNotFound) {
		t.Errorf("GetByKeyHash after Delete = %v, want ErrAPIKeyNotFound", err)
	}
}

func TestAPIKeysRepo_UpdateLabelRenamesAndNothingElse(t *testing.T) {
	db := TestDB(t)
	ctx := t.Context()
	repo := NewAPIKeysRepo(db)
	ownerID := seedKeyOwner(ctx, t, db, "system:renamer")

	key, err := repo.Create(ctx, keyFor(ownerID, "before", "hash-rename", nil),
		[]string{"agent:*:invoke"}, 10, nowMS())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	const backdated = 1
	if _, backdateErr := db.ExecContext(ctx, `UPDATE api_keys SET updated_time = ? WHERE id = ?`, backdated, key.ID); backdateErr != nil {
		t.Fatalf("backdate: %v", backdateErr)
	}
	renamed, err := repo.UpdateLabel(ctx, key.ID, "after", "second-admin")
	if err != nil {
		t.Fatalf("UpdateLabel: %v", err)
	}
	if renamed.Label != "after" {
		t.Errorf("Label = %q, want %q", renamed.Label, "after")
	}
	if renamed.UpdatedBy != "second-admin" {
		t.Errorf("UpdatedBy = %q, want %q", renamed.UpdatedBy, "second-admin")
	}
	if renamed.KeyHash != key.KeyHash || renamed.Prefix != key.Prefix || renamed.CreatedBy != key.CreatedBy {
		t.Error("UpdateLabel altered a field other than the label")
	}
	if renamed.UpdatedTime <= backdated {
		t.Errorf("UpdateLabel left updated_time at %d", renamed.UpdatedTime)
	}

	if _, err := repo.UpdateLabel(ctx, "00000000-0000-0000-0000-000000000000", "x", "admin"); !errors.Is(err, ErrAPIKeyNotFound) {
		t.Errorf("UpdateLabel on an unknown id error = %v, want ErrAPIKeyNotFound", err)
	}
}

func TestAPIKeysRepo_DeleteUnknownIDIsNotFound(t *testing.T) {
	db := TestDB(t)
	ctx := t.Context()
	repo := NewAPIKeysRepo(db)

	if err := repo.Delete(ctx, "00000000-0000-0000-0000-000000000000"); !errors.Is(err, ErrAPIKeyNotFound) {
		t.Errorf("Delete on an unknown id error = %v, want ErrAPIKeyNotFound", err)
	}
}

func TestAPIKeysRepo_ListFiltersByOwner(t *testing.T) {
	db := TestDB(t)
	ctx := t.Context()
	repo := NewAPIKeysRepo(db)
	first := seedKeyOwner(ctx, t, db, "system:owner-one")
	second := seedKeyOwner(ctx, t, db, "system:owner-two")

	for i := range 2 {
		if _, err := repo.Create(ctx, keyFor(first, fmt.Sprintf("a-%d", i), fmt.Sprintf("ha-%d", i), nil),
			[]string{"agent:*:invoke"}, 10, nowMS()); err != nil {
			t.Fatalf("seed first owner key: %v", err)
		}
	}
	if _, err := repo.Create(ctx, keyFor(second, "b-0", "hb-0", nil),
		[]string{"agent:*:invoke"}, 10, nowMS()); err != nil {
		t.Fatalf("seed second owner key: %v", err)
	}

	keys, total, err := repo.ListPaginated(ctx, APIKeyListOptions{PageNumber: 1, PageSize: 50, UserID: first})
	if err != nil {
		t.Fatalf("ListPaginated: %v", err)
	}
	if total != 2 || len(keys) != 2 {
		t.Fatalf("owner filter returned total=%d len=%d, want 2/2", total, len(keys))
	}
	for _, k := range keys {
		if k.UserID != first {
			t.Errorf("owner filter leaked a key owned by %q", k.UserID)
		}
	}
}

func TestAPIKeysRepo_ListDerivesStatusFromExpiry(t *testing.T) {
	db := TestDB(t)
	ctx := t.Context()
	repo := NewAPIKeysRepo(db)
	ownerID := seedKeyOwner(ctx, t, db, "system:status")

	base := time.Now().UnixMilli()
	lapsed := base - 1000
	future := base + 1_000_000
	seed := []struct {
		label   string
		hash    string
		expires *int64
	}{
		{"lapsed", "h-lapsed", &lapsed},
		{"future", "h-future", &future},
		{"never", "h-never", nil},
	}
	for _, s := range seed {
		if _, err := repo.Create(ctx, keyFor(ownerID, s.label, s.hash, s.expires),
			[]string{"agent:*:invoke"}, 10, nowMS()); err != nil {
			t.Fatalf("seed %s: %v", s.label, err)
		}
	}

	tests := []struct {
		status string
		now    int64
		want   int
	}{
		{APIKeyStatusActive, base, 2},
		{APIKeyStatusExpired, base, 1},
		{APIKeyStatusActive, future, 1},
		{APIKeyStatusExpired, future, 2},
		{APIKeyStatusActive, future + 1, 1},
		{APIKeyStatusExpired, future + 1, 2},
		{"", base, 3},
	}
	for _, tc := range tests {
		t.Run(fmt.Sprintf("%s_at_%d", tc.status, tc.now), func(t *testing.T) {
			_, total, err := repo.ListPaginated(ctx, APIKeyListOptions{
				PageNumber: 1, PageSize: 50, UserID: ownerID, Status: tc.status, Now: tc.now,
			})
			if err != nil {
				t.Fatalf("ListPaginated: %v", err)
			}
			if total != tc.want {
				t.Errorf("status=%q now=%d returned %d keys, want %d", tc.status, tc.now, total, tc.want)
			}
		})
	}
}

func TestAPIKeysRepo_ListRejectsUnsafeSortStatusAndOrder(t *testing.T) {
	db := TestDB(t)
	ctx := t.Context()
	repo := NewAPIKeysRepo(db)

	bad := []APIKeyListOptions{
		{PageNumber: 1, PageSize: 10, SortColumn: "key_hash"},
		{PageNumber: 1, PageSize: 10, SortColumn: "created_time; DROP TABLE api_keys"},
		{PageNumber: 1, PageSize: 10, SortOrder: "ASC; DROP TABLE api_keys"},
		{PageNumber: 1, PageSize: 10, Status: "revoked", Now: 1},
	}
	for i, opts := range bad {
		if _, _, err := repo.ListPaginated(ctx, opts); err == nil {
			t.Errorf("case %d: ListPaginated accepted %+v", i, opts)
		}
	}
	if _, ok := apiKeysSortExpressions["key_hash"]; ok {
		t.Error("key_hash is sortable; the digest must not be orderable through the API")
	}
}

func TestAPIKeysRepo_ListRefusesAStatusFilterWithNoInstant(t *testing.T) {
	db := TestDB(t)
	ctx := t.Context()
	repo := NewAPIKeysRepo(db)

	for _, status := range []string{APIKeyStatusActive, APIKeyStatusExpired} {
		if _, _, err := repo.ListPaginated(ctx, APIKeyListOptions{
			PageNumber: 1, PageSize: 10, Status: status,
		}); err == nil {
			t.Errorf("status=%q with Now unset was accepted", status)
		}
	}
	if _, _, err := repo.ListPaginated(ctx, APIKeyListOptions{PageNumber: 1, PageSize: 10}); err != nil {
		t.Errorf("unfiltered list required an instant: %v", err)
	}
}

func TestAPIKeysRepo_ListPastTheEndIsAnEmptyPage(t *testing.T) {
	db := TestDB(t)
	ctx := t.Context()
	repo := NewAPIKeysRepo(db)
	ownerID := seedKeyOwner(ctx, t, db, "system:paging")

	if _, err := repo.Create(ctx, keyFor(ownerID, "only", "h-only", nil),
		[]string{"agent:*:invoke"}, 10, nowMS()); err != nil {
		t.Fatalf("Create: %v", err)
	}
	keys, total, err := repo.ListPaginated(ctx, APIKeyListOptions{PageNumber: 99, PageSize: 10})
	if err != nil {
		t.Fatalf("ListPaginated: %v", err)
	}
	if total != 1 {
		t.Errorf("total = %d, want 1", total)
	}
	if keys == nil {
		t.Error("past-the-end page returned nil rather than an empty slice")
	}
	if len(keys) != 0 {
		t.Errorf("past-the-end page returned %d rows", len(keys))
	}
}

func TestAPIKeysRepo_ListSortsNeverExpiringLast(t *testing.T) {
	db := TestDB(t)
	ctx := t.Context()
	repo := NewAPIKeysRepo(db)
	ownerID := seedKeyOwner(ctx, t, db, "system:expiry-sort")

	soon := time.Now().Add(time.Hour).UnixMilli()
	later := time.Now().Add(48 * time.Hour).UnixMilli()
	for _, s := range []struct {
		label   string
		hash    string
		expires *int64
	}{
		{"never", "hs-never", nil},
		{"soon", "hs-soon", &soon},
		{"later", "hs-later", &later},
	} {
		if _, err := repo.Create(ctx, keyFor(ownerID, s.label, s.hash, s.expires),
			[]string{"agent:*:invoke"}, 10, nowMS()); err != nil {
			t.Fatalf("seed %s: %v", s.label, err)
		}
	}

	keys, _, err := repo.ListPaginated(ctx, APIKeyListOptions{
		PageNumber: 1, PageSize: 10, UserID: ownerID,
		SortColumn: "expires_time", SortOrder: "ASC",
	})
	if err != nil {
		t.Fatalf("ListPaginated: %v", err)
	}
	var order []string
	for _, k := range keys {
		order = append(order, k.Label)
	}
	want := []string{"soon", "later", "never"}
	if len(order) != len(want) {
		t.Fatalf("got %v, want %v", order, want)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("ascending expiry order = %v, want %v", order, want)
		}
	}
}

func TestAPIKeysRepo_TouchLastUsed(t *testing.T) {
	db := TestDB(t)
	ctx := t.Context()
	repo := NewAPIKeysRepo(db)
	ownerID := seedKeyOwner(ctx, t, db, "system:touch")

	key, err := repo.Create(ctx, keyFor(ownerID, "touched", "h-touch", nil),
		[]string{"agent:*:invoke"}, 10, nowMS())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	at := time.Now().UnixMilli()
	if touchErr := repo.TouchLastUsed(ctx, key.ID, at); touchErr != nil {
		t.Fatalf("TouchLastUsed: %v", touchErr)
	}
	stamped, err := repo.GetByID(ctx, key.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if stamped.LastUsedTime == nil || *stamped.LastUsedTime != at {
		t.Errorf("LastUsedTime = %v, want %d", stamped.LastUsedTime, at)
	}

	if touchErr := repo.TouchLastUsed(ctx, key.ID, at-1000); touchErr != nil {
		t.Fatalf("TouchLastUsed with an earlier instant: %v", touchErr)
	}
	if restamped, getErr := repo.GetByID(ctx, key.ID); getErr != nil || restamped.LastUsedTime == nil || *restamped.LastUsedTime != at {
		t.Errorf("an earlier stamp moved LastUsedTime to %v, want %d (err %v)", restamped.LastUsedTime, at, getErr)
	}

	if err := repo.TouchLastUsed(ctx, "00000000-0000-0000-0000-000000000000", at); err != nil {
		t.Errorf("TouchLastUsed on an unknown id returned %v; a miss must not be an error", err)
	}
}

func TestAPIKeysRepo_ListCombinesOwnerAndStatus(t *testing.T) {
	db := TestDB(t)
	ctx := t.Context()
	repo := NewAPIKeysRepo(db)
	first := seedKeyOwner(ctx, t, db, "system:status-one")
	second := seedKeyOwner(ctx, t, db, "system:status-two")

	now := nowMS()
	lapsed := now - 1000
	for _, s := range []struct {
		owner, label, hash string
		expires            *int64
	}{
		{first, "first-active", "hc-1a", nil},
		{first, "first-expired", "hc-1e", &lapsed},
		{second, "second-active", "hc-2a", nil},
		{second, "second-active-too", "hc-2b", nil},
		{second, "second-expired", "hc-2e", &lapsed},
	} {
		if _, err := repo.Create(ctx, keyFor(s.owner, s.label, s.hash, s.expires),
			[]string{"agent:*:invoke"}, 10, now); err != nil {
			t.Fatalf("seed %s: %v", s.label, err)
		}
	}

	for _, tc := range []struct {
		owner, status string
		want          []string
	}{
		{first, APIKeyStatusActive, []string{"first-active"}},
		{first, APIKeyStatusExpired, []string{"first-expired"}},
		{second, APIKeyStatusActive, []string{"second-active", "second-active-too"}},
		{second, APIKeyStatusExpired, []string{"second-expired"}},
	} {
		keys, total, err := repo.ListPaginated(ctx, APIKeyListOptions{
			PageNumber: 1, PageSize: 50, UserID: tc.owner, Status: tc.status, Now: now,
			SortColumn: "label", SortOrder: "ASC",
		})
		if err != nil {
			t.Fatalf("ListPaginated: %v", err)
		}
		if got := apiKeyLabels(keys); total != len(tc.want) || !slices.Equal(got, tc.want) {
			t.Errorf("owner %s status %s = %v (total %d), want %v", tc.owner, tc.status, got, total, tc.want)
		}
	}
}

func apiKeyLabels(keys []*APIKey) []string {
	labels := make([]string, 0, len(keys))
	for _, k := range keys {
		labels = append(labels, k.Label)
	}
	return labels
}

func TestAPIKeysRepo_ListSortsByEveryColumnBothWays(t *testing.T) {
	db := TestDB(t)
	ctx := t.Context()
	repo := NewAPIKeysRepo(db)
	ownerID := seedKeyOwner(ctx, t, db, "system:sorting")

	base := nowMS()
	hour := time.Hour.Milliseconds()
	nearest, near, far := base+hour, base+2*hour, base+48*hour
	for i, s := range []struct {
		label, prefix string
		expires       *int64
		updated       int64
		lastUsed      int64
	}{
		{"d", "pfx-2", &nearest, 300, 20},
		{"B", "pfx-1", &near, 200, 0},
		{"a", "pfx-4", nil, 400, 10},
		{"c", "pfx-3", &far, 100, 30},
	} {
		key := keyFor(ownerID, s.label, "hsort-"+s.label, s.expires)
		key.Prefix = s.prefix
		created, err := repo.Create(ctx, key, []string{"agent:*:invoke"}, 10, base+int64(i))
		if err != nil {
			t.Fatalf("seed %s: %v", s.label, err)
		}
		if _, err := db.ExecContext(ctx, `UPDATE api_keys SET updated_time = ? WHERE id = ?`, s.updated, created.ID); err != nil {
			t.Fatalf("set updated_time: %v", err)
		}
		if s.lastUsed > 0 {
			if err := repo.TouchLastUsed(ctx, created.ID, s.lastUsed); err != nil {
				t.Fatalf("touch: %v", err)
			}
		}
	}

	ascending := map[string][]string{
		"label":          {"a", "B", "c", "d"},
		"prefix":         {"B", "d", "c", "a"},
		"created_time":   {"d", "B", "a", "c"},
		"updated_time":   {"c", "B", "d", "a"},
		"expires_time":   {"d", "B", "c", "a"},
		"last_used_time": {"B", "a", "d", "c"},
	}
	if len(ascending) != len(apiKeysSortExpressions) {
		t.Fatalf("test covers %d sort columns, repository exposes %d", len(ascending), len(apiKeysSortExpressions))
	}
	list := func(t *testing.T, column, order string, page, size int) []string {
		t.Helper()
		keys, _, err := repo.ListPaginated(ctx, APIKeyListOptions{
			PageNumber: page, PageSize: size, UserID: ownerID, SortColumn: column, SortOrder: order,
		})
		if err != nil {
			t.Fatalf("ListPaginated(%s %s): %v", column, order, err)
		}
		return apiKeyLabels(keys)
	}
	for column, want := range ascending {
		t.Run(column, func(t *testing.T) {
			if got := list(t, column, "ASC", 1, 10); !slices.Equal(got, want) {
				t.Errorf("ASC = %v, want %v", got, want)
			}
			descending := slices.Clone(want)
			slices.Reverse(descending)
			if got := list(t, column, "DESC", 1, 10); !slices.Equal(got, descending) {
				t.Errorf("DESC = %v, want %v", got, descending)
			}
		})
	}

	t.Run("default is newest first, across pages", func(t *testing.T) {
		first, second := list(t, "", "", 1, 2), list(t, "", "", 2, 2)
		if got, want := slices.Concat(first, second), []string{"c", "a", "B", "d"}; !slices.Equal(got, want) {
			t.Errorf("default order over two pages = %v, want %v", got, want)
		}
	})
}

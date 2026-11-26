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
	"strings"
	"testing"
	"time"
)

func TestUsersRepo_UpsertNormalizesEmail(t *testing.T) {
	db := TestDB(t)
	repo := NewUsersRepo(db)
	ctx := t.Context()

	u := &User{Issuer: "iss", Sub: "s1", Email: new("  Alice@Example.COM "), CreatedBy: "x", UpdatedBy: "x"}
	if err := repo.UpsertUser(ctx, u, nil); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	got, _, err := repo.GetByID(ctx, u.ID)
	if err != nil || got == nil {
		t.Fatalf("get by id: %v", err)
	}
	if got.Email == nil || *got.Email != "alice@example.com" {
		t.Errorf("stored email = %v, want alice@example.com", got.Email)
	}
	if byEmail, _, err := repo.GetByEmail(ctx, "alice@example.com"); err != nil || byEmail == nil {
		t.Errorf("GetByEmail(normalized) failed: byEmail=%v err=%v", byEmail, err)
	}
}

func TestUsersRepo_ListRejectsUnsafeSort(t *testing.T) {
	db := TestDB(t)
	repo := NewUsersRepo(db)
	ctx := t.Context()

	if _, _, err := repo.ListUsers(ctx, UserListOptions{PageNumber: 1, PageSize: 10, SortColumn: "id); DROP TABLE users;--"}); err == nil {
		t.Error("ListUsers accepted an unsafe sort column; want error")
	}
	if _, _, err := repo.ListUsers(ctx, UserListOptions{PageNumber: 1, PageSize: 10, SortColumn: "email", SortOrder: "sideways"}); err == nil {
		t.Error("ListUsers accepted an unsafe sort order; want error")
	}
	if _, _, err := repo.ListUsers(ctx, UserListOptions{PageNumber: 1, PageSize: 10, SortColumn: "email", SortOrder: "ASC"}); err != nil {
		t.Errorf("ListUsers rejected a valid sort: %v", err)
	}
}

func TestUsersRepo_UpsertCreatesUser(t *testing.T) {
	db := TestDB(t)
	repo := NewUsersRepo(db)
	ctx := t.Context()

	u := &User{
		Issuer:       "https://idp.example.com",
		Sub:          "sub-alice",
		OIDCProvider: "azure",
		Email:        new("alice@example.com"),
		Source:       `{"sub":"sub-alice","email":"alice@example.com"}`,
		CreatedBy:    "alice@example.com",
		UpdatedBy:    "alice@example.com",
	}
	if err := repo.UpsertUser(ctx, u, []string{"sam-admins", "sam-users"}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if u.ID == "" || u.CreatedTime == 0 || u.UpdatedTime == 0 {
		t.Fatalf("expected id and timestamps populated: %+v", u)
	}

	got, groups, err := repo.GetByIssuerSub(ctx, "https://idp.example.com", "sub-alice")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got == nil {
		t.Fatal("expected a user, got nil")
	}
	if got.ID != u.ID || got.Email == nil || *got.Email != "alice@example.com" {
		t.Fatalf("unexpected user: %+v", got)
	}
	if got.OIDCProvider != "azure" {
		t.Errorf("oidc_provider = %q, want azure", got.OIDCProvider)
	}
	if got.ProvisionedVia != "jit" {
		t.Errorf("provisioned_via = %q, want jit (default)", got.ProvisionedVia)
	}
	if !got.Active {
		t.Error("active = false, want true (default)")
	}
	slices.Sort(groups)
	if !slices.Equal(groups, []string{"sam-admins", "sam-users"}) {
		t.Errorf("groups = %v, want [sam-admins sam-users]", groups)
	}
}

func TestUsersRepo_UpsertIsIdempotentAndRefreshes(t *testing.T) {
	db := TestDB(t)
	repo := NewUsersRepo(db)
	ctx := t.Context()

	first := &User{
		Issuer: "iss", Sub: "sub-bob",
		OIDCProvider: "azure",
		Email:        new("bob.old@example.com"),
		Source:       `{"v":1}`,
		CreatedBy:    "bob", UpdatedBy: "bob",
	}
	if err := repo.UpsertUser(ctx, first, []string{"group-a", "group-b"}); err != nil {
		t.Fatalf("first upsert: %v", err)
	}
	originalID := first.ID

	second := &User{
		Issuer: "iss", Sub: "sub-bob",
		OIDCProvider: "okta",
		Email:        new("bob.new@example.com"),
		Source:       `{"v":2}`,
		CreatedBy:    "bob", UpdatedBy: "bob",
	}
	if err := repo.UpsertUser(ctx, second, []string{"group-a", "group-c"}); err != nil {
		t.Fatalf("second upsert: %v", err)
	}
	if second.ID != originalID {
		t.Fatalf("canonical id changed on update: %q -> %q", originalID, second.ID)
	}

	got, groups, err := repo.GetByIssuerSub(ctx, "iss", "sub-bob")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Email == nil || *got.Email != "bob.new@example.com" {
		t.Errorf("email not refreshed: %+v", got.Email)
	}
	if got.OIDCProvider != "okta" {
		t.Errorf("oidc_provider not refreshed: %q, want okta", got.OIDCProvider)
	}
	if got.Source != `{"v":2}` {
		t.Errorf("source not refreshed: %q", got.Source)
	}
	slices.Sort(groups)
	if !slices.Equal(groups, []string{"group-a", "group-c"}) {
		t.Errorf("groups not replaced: %v, want [group-a group-c]", groups)
	}

	count, err := queryUserCount(ctx, db, "iss", "sub-bob")
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 row for identity, got %d", count)
	}
}

func TestUsersRepo_ResolveByEmailAndPhone(t *testing.T) {
	db := TestDB(t)
	repo := NewUsersRepo(db)
	ctx := t.Context()

	u := &User{
		Issuer: "iss", Sub: "sub-carol",
		Email:     new("carol@example.com"),
		Phone:     new("+15551234567"),
		CreatedBy: "carol", UpdatedBy: "carol",
	}
	if err := repo.UpsertUser(ctx, u, []string{"g1"}); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	byEmail, _, err := repo.GetByEmail(ctx, "carol@example.com")
	if err != nil || byEmail == nil {
		t.Fatalf("by email: user=%v err=%v", byEmail, err)
	}
	if byEmail.ID != u.ID {
		t.Errorf("email resolved to %q, want canonical %q", byEmail.ID, u.ID)
	}

	var phoneOwner string
	if err := db.QueryRowContext(ctx, `SELECT id FROM users WHERE phone = ?`, "+15551234567").Scan(&phoneOwner); err != nil {
		t.Fatalf("by phone: %v", err)
	}
	if phoneOwner != u.ID {
		t.Errorf("phone resolved to %q, want canonical %q", phoneOwner, u.ID)
	}
}

func TestUsersRepo_UpsertPreservesScimOwnership(t *testing.T) {
	db := TestDB(t)
	repo := NewUsersRepo(db)
	ctx := t.Context()

	scim := &User{
		Issuer: "iss", Sub: "sub-scim",
		OIDCProvider:   "azure",
		ProvisionedVia: "scim",
		ExternalID:     new("scim-ext-1"),
		DisplayName:    new("SCIM Name"),
		Email:          new("scim.managed@example.com"),
		CreatedBy:      "scim", UpdatedBy: "scim",
	}
	if err := repo.UpsertUser(ctx, scim, []string{"scim-group"}); err != nil {
		t.Fatalf("scim upsert: %v", err)
	}
	scimSeen := *scim.LastLoginAt
	if _, err := db.ExecContext(ctx, "UPDATE users SET active = 0 WHERE issuer = ? AND sub = ?", "iss", "sub-scim"); err != nil {
		t.Fatalf("deactivate: %v", err)
	}

	time.Sleep(2 * time.Millisecond)
	relogin := &User{
		Issuer: "iss", Sub: "sub-scim",
		OIDCProvider:   "okta",
		ProvisionedVia: "jit",
		DisplayName:    new("JIT Name"),
		Email:          new("jit.login@example.com"),
		CreatedBy:      "jit.login@example.com", UpdatedBy: "jit.login@example.com",
	}
	if err := repo.UpsertUser(ctx, relogin, []string{"jit-group"}); err != nil {
		t.Fatalf("jit re-login upsert: %v", err)
	}

	got, groups, err := repo.GetByIssuerSub(ctx, "iss", "sub-scim")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.ProvisionedVia != "scim" {
		t.Errorf("provisioned_via reverted to %q; SCIM ownership must be preserved", got.ProvisionedVia)
	}
	if got.Active {
		t.Error("active flipped back to true; a SCIM deactivation must survive a JIT re-login")
	}
	if got.DisplayName == nil || *got.DisplayName != "SCIM Name" {
		t.Errorf("display_name = %v; a JIT login must not overwrite a SCIM-owned attribute", got.DisplayName)
	}
	if got.Email == nil || *got.Email != "scim.managed@example.com" {
		t.Errorf("email = %v; a JIT login must not overwrite a SCIM-owned attribute", got.Email)
	}
	if got.ExternalID == nil || *got.ExternalID != "scim-ext-1" {
		t.Errorf("external_id = %v; must be preserved", got.ExternalID)
	}
	if got.OIDCProvider != "azure" {
		t.Errorf("oidc_provider = %q; a JIT login must not overwrite the SCIM value", got.OIDCProvider)
	}
	if len(groups) != 1 || groups[0] != "scim-group" {
		t.Errorf("groups = %v; a JIT login must not replace SCIM-owned membership", groups)
	}
	if got.LastLoginAt == nil || *got.LastLoginAt <= scimSeen {
		t.Errorf("last_login_at = %v, want advanced past %d (the login must still be recorded)", got.LastLoginAt, scimSeen)
	}
}

func TestUsersRepo_GetByEmailMostRecentWins(t *testing.T) {
	db := TestDB(t)
	repo := NewUsersRepo(db)
	ctx := t.Context()

	older := &User{Issuer: "iss", Sub: "sub-old", Email: new("shared@example.com"), CreatedBy: "x", UpdatedBy: "x"}
	if err := repo.UpsertUser(ctx, older, nil); err != nil {
		t.Fatalf("older upsert: %v", err)
	}
	time.Sleep(2 * time.Millisecond)
	newer := &User{Issuer: "iss", Sub: "sub-new", Email: new("shared@example.com"), CreatedBy: "x", UpdatedBy: "x"}
	if err := repo.UpsertUser(ctx, newer, nil); err != nil {
		t.Fatalf("newer upsert: %v", err)
	}

	got, _, err := repo.GetByEmail(ctx, "shared@example.com")
	if err != nil || got == nil {
		t.Fatalf("by email: user=%v err=%v", got, err)
	}
	if got.Sub != "sub-new" {
		t.Errorf("GetByEmail returned %q, want the most-recently-updated sub-new", got.Sub)
	}
}

func TestUsersRepo_UpsertDedupesGroups(t *testing.T) {
	db := TestDB(t)
	repo := NewUsersRepo(db)
	ctx := t.Context()

	u := &User{Issuer: "iss", Sub: "sub-dup", CreatedBy: "x", UpdatedBy: "x"}
	if err := repo.UpsertUser(ctx, u, []string{"g1", "g1", "", "g2"}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	_, groups, err := repo.GetByIssuerSub(ctx, "iss", "sub-dup")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	slices.Sort(groups)
	if !slices.Equal(groups, []string{"g1", "g2"}) {
		t.Errorf("groups = %v, want deduped [g1 g2] (empty skipped)", groups)
	}
}

func TestUsersRepo_MissingResolvesToNil(t *testing.T) {
	db := TestDB(t)
	repo := NewUsersRepo(db)
	ctx := t.Context()

	got, groups, err := repo.GetByEmail(ctx, "nobody@example.com")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got != nil || groups != nil {
		t.Errorf("expected nil for missing user, got user=%v groups=%v", got, groups)
	}
}

func TestUsersRepo_PhoneUniqueConflict(t *testing.T) {
	db := TestDB(t)
	repo := NewUsersRepo(db)
	ctx := t.Context()

	first := &User{
		Issuer: "iss", Sub: "sub-1",
		Phone:     new("+15550000001"),
		CreatedBy: "x", UpdatedBy: "x",
	}
	if err := repo.UpsertUser(ctx, first, nil); err != nil {
		t.Fatalf("first upsert: %v", err)
	}

	second := &User{
		Issuer: "iss", Sub: "sub-2",
		Phone:     new("+15550000001"),
		CreatedBy: "y", UpdatedBy: "y",
	}
	err := repo.UpsertUser(ctx, second, nil)
	if !errors.Is(err, ErrUserPhoneConflict) {
		t.Fatalf("expected ErrUserPhoneConflict, got %v", err)
	}
}

func TestUsersRepo_NilPhoneAllowsMultiple(t *testing.T) {
	db := TestDB(t)
	repo := NewUsersRepo(db)
	ctx := t.Context()

	for _, sub := range []string{"sub-a", "sub-b"} {
		u := &User{Issuer: "iss", Sub: sub, CreatedBy: "x", UpdatedBy: "x"}
		if err := repo.UpsertUser(ctx, u, nil); err != nil {
			t.Fatalf("upsert %s with nil phone: %v", sub, err)
		}
	}
}

func TestUsersRepo_UpsertPersistsDisplayNameUserNameAndLastLogin(t *testing.T) {
	db := TestDB(t)
	repo := NewUsersRepo(db)
	ctx := t.Context()

	u := &User{
		Issuer: "iss", Sub: "sub-disp", OIDCProvider: "azure",
		DisplayName: new("Alice Example"),
		UserName:    new("alice"),
		Email:       new("alice@example.com"),
		CreatedBy:   "x", UpdatedBy: "x",
	}
	if err := repo.UpsertUser(ctx, u, nil); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if u.LastLoginAt == nil || *u.LastLoginAt != u.UpdatedTime {
		t.Fatalf("last_login_at should be stamped to updated_time on upsert: last=%v updated=%d", u.LastLoginAt, u.UpdatedTime)
	}
	firstLogin := *u.LastLoginAt

	got, _, err := repo.GetByIssuerSub(ctx, "iss", "sub-disp")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.DisplayName == nil || *got.DisplayName != "Alice Example" {
		t.Errorf("display_name = %v, want Alice Example", got.DisplayName)
	}
	if got.UserName == nil || *got.UserName != "alice" {
		t.Errorf("user_name = %v, want alice", got.UserName)
	}
	if got.LastLoginAt == nil || *got.LastLoginAt != firstLogin {
		t.Errorf("last_login_at = %v, want %d", got.LastLoginAt, firstLogin)
	}

	time.Sleep(2 * time.Millisecond)
	u2 := &User{
		Issuer: "iss", Sub: "sub-disp", OIDCProvider: "azure",
		DisplayName: new("Alice Renamed"),
		UserName:    new("alice2"),
		CreatedBy:   "x", UpdatedBy: "x",
	}
	if err = repo.UpsertUser(ctx, u2, nil); err != nil {
		t.Fatalf("second upsert: %v", err)
	}
	got2, _, err := repo.GetByIssuerSub(ctx, "iss", "sub-disp")
	if err != nil {
		t.Fatalf("get2: %v", err)
	}
	if got2.DisplayName == nil || *got2.DisplayName != "Alice Renamed" {
		t.Errorf("display_name not refreshed: %v", got2.DisplayName)
	}
	if got2.UserName == nil || *got2.UserName != "alice2" {
		t.Errorf("user_name not refreshed: %v", got2.UserName)
	}
	if got2.LastLoginAt == nil || *got2.LastLoginAt <= firstLogin {
		t.Errorf("last_login_at not advanced on re-login: first=%d got=%v", firstLogin, got2.LastLoginAt)
	}
}

func TestUsersRepo_ListUsersFiltersAndPaginates(t *testing.T) {
	db := TestDB(t)
	repo := NewUsersRepo(db)
	ctx := t.Context()

	seed := []struct {
		sub, name, email, provider string
		groups                     []string
	}{
		{"s1", "Alice Anderson", "alice@example.com", "azure", []string{"eng"}},
		{"s2", "Bob Brown", "bob@example.com", "azure", []string{"sales"}},
		{"s3", "Carol Clark", "carol@other.com", "google", []string{"eng", "sales"}},
	}
	for _, s := range seed {
		u := &User{Issuer: "iss", Sub: s.sub, OIDCProvider: s.provider, DisplayName: new(s.name), Email: new(s.email), CreatedBy: "x", UpdatedBy: "x"}
		if err := repo.UpsertUser(ctx, u, s.groups); err != nil {
			t.Fatalf("seed %s: %v", s.sub, err)
		}
	}

	users, total, err := repo.ListUsers(ctx, UserListOptions{PageNumber: 1, PageSize: 20, Search: "alice"})
	if err != nil {
		t.Fatalf("list search name: %v", err)
	}
	if total != 1 || len(users) != 1 || users[0].Sub != "s1" {
		t.Fatalf("search 'alice' = %d users (total %d), want 1 (s1)", len(users), total)
	}

	_, total, err = repo.ListUsers(ctx, UserListOptions{PageNumber: 1, PageSize: 20, Search: "example.com"})
	if err != nil {
		t.Fatalf("list search email: %v", err)
	}
	if total != 2 {
		t.Errorf("search 'example.com' total = %d, want 2", total)
	}

	for _, needle := range []string{"%", "_"} {
		_, total, err = repo.ListUsers(ctx, UserListOptions{PageNumber: 1, PageSize: 20, Search: needle})
		if err != nil {
			t.Errorf("search %q: %v", needle, err)
			continue
		}
		if total != 0 {
			t.Errorf("search %q total = %d, want 0 — metacharacter acted as a wildcard", needle, total)
		}
	}

	_, total, err = repo.ListUsers(ctx, UserListOptions{PageNumber: 1, PageSize: 20, Group: "eng"})
	if err != nil {
		t.Fatalf("list group: %v", err)
	}
	if total != 2 {
		t.Errorf("group 'eng' total = %d, want 2", total)
	}

	_, total, err = repo.ListUsers(ctx, UserListOptions{PageNumber: 1, PageSize: 20, OIDCProvider: "google"})
	if err != nil {
		t.Fatalf("list provider: %v", err)
	}
	if total != 1 {
		t.Errorf("provider 'google' total = %d, want 1", total)
	}

	wantTotal := len(seed) + seededSystemUserCount
	page1, total, err := repo.ListUsers(ctx, UserListOptions{PageNumber: 1, PageSize: 4})
	if err != nil {
		t.Fatalf("list page1: %v", err)
	}
	if total != wantTotal || len(page1) != 4 {
		t.Errorf("page1 = %d users (total %d), want 4 (total %d)", len(page1), total, wantTotal)
	}
	page2, _, err := repo.ListUsers(ctx, UserListOptions{PageNumber: 2, PageSize: 4})
	if err != nil {
		t.Fatalf("list page2: %v", err)
	}
	if len(page2) != wantTotal-4 {
		t.Errorf("page2 = %d users, want %d", len(page2), wantTotal-4)
	}
}

const seededSystemUserCount = 4

const seededSystemUserIssuer = "sam:system"

func withoutSeededSystemUsers(users []User) []string {
	subs := make([]string, 0, len(users))
	for _, u := range users {
		if u.Issuer != seededSystemUserIssuer {
			subs = append(subs, u.Sub)
		}
	}
	return subs
}

func TestUsersRepo_ListUsersExcludeSubs(t *testing.T) {
	db := TestDB(t)
	repo := NewUsersRepo(db)
	ctx := t.Context()
	u := &User{Issuer: "iss", Sub: "s1", OIDCProvider: "azure", DisplayName: new("Alice"), Email: new("alice@example.com"), CreatedBy: "x", UpdatedBy: "x"}
	if err := repo.UpsertUser(ctx, u, nil); err != nil {
		t.Fatalf("seed: %v", err)
	}

	for _, exclude := range [][]string{{"system:channel"}, {"system:channel", "system:eval"}} {
		users, total, err := repo.ListUsers(ctx, UserListOptions{PageNumber: 1, PageSize: 2, SortColumn: "email", SortOrder: "ASC", ExcludeSubs: exclude})
		if err != nil {
			t.Fatalf("ListUsers(exclude %v): %v", exclude, err)
		}
		if want := 1 + seededSystemUserCount - len(exclude); total != want {
			t.Errorf("exclude %v: total = %d, want %d", exclude, total, want)
		}
		for _, got := range users {
			if slices.Contains(exclude, got.Sub) {
				t.Errorf("exclude %v: page lists %s", exclude, got.Sub)
			}
		}
	}
}

func TestUsersRepo_ListUsersSortsSeededSystemUsersLikeRealRows(t *testing.T) {
	db := TestDB(t)
	repo := NewUsersRepo(db)
	ctx := t.Context()

	seed := []struct{ sub, name, email string }{
		{"s1", "Alice Anderson", "alice@example.com"},
		{"s2", "Bob Brown", "bob@example.com"},
		{"s3", "Carol Clark", "carol@example.com"},
	}
	for _, s := range seed {
		u := &User{Issuer: "iss", Sub: s.sub, OIDCProvider: "azure", DisplayName: new(s.name), Email: new(s.email), CreatedBy: "x", UpdatedBy: "x"}
		if err := repo.UpsertUser(ctx, u, nil); err != nil {
			t.Fatalf("seed %s: %v", s.sub, err)
		}
	}

	list := func(page, size int, sortColumn, order string) ([]string, int) {
		t.Helper()
		users, total, err := repo.ListUsers(ctx, UserListOptions{
			PageNumber: page, PageSize: size, SortColumn: sortColumn, SortOrder: order,
		})
		if err != nil {
			t.Fatalf("list %s %s: %v", sortColumn, order, err)
		}
		subs := make([]string, len(users))
		for i, u := range users {
			subs[i] = u.Sub
		}
		return subs, total
	}

	wantTotal := len(seed) + seededSystemUserCount
	wantASC := []string{"s1", "s2", "s3", "system:channel", "system:default", "system:eval", "system:evidence"}
	if got, total := list(1, 20, "display_name", "ASC"); !slices.Equal(got, wantASC) || total != wantTotal {
		t.Errorf("display_name ASC = %v (total %d), want %v (total %d) — Channel/Default/Eval/Evidence sort after Carol", got, total, wantASC, wantTotal)
	}
	wantDESC := slices.Clone(wantASC)
	slices.Reverse(wantDESC)
	if got, _ := list(1, 20, "display_name", "DESC"); !slices.Equal(got, wantDESC) {
		t.Errorf("display_name DESC = %v, want %v", got, wantDESC)
	}

	got, _ := list(1, 20, "email", "ASC")
	for i := range seededSystemUserCount {
		if i >= len(got) || !strings.HasPrefix(got[i], "system:") {
			t.Fatalf("email ASC = %v, want the %d seeded (no email) rows first", got, seededSystemUserCount)
		}
	}

	page1, total := list(1, 4, "display_name", "ASC")
	page2, _ := list(2, 4, "display_name", "ASC")
	if total != wantTotal {
		t.Errorf("total = %d, want %d", total, wantTotal)
	}
	if got := slices.Concat(page1, page2); !slices.Equal(got, wantASC) {
		t.Errorf("page1+page2 = %v, want %v split cleanly with no drop/duplicate", got, wantASC)
	}
}

func TestUsersRepo_ListUsersSortsNeverLoggedInUsersConsistently(t *testing.T) {
	db := TestDB(t)
	repo := NewUsersRepo(db)
	ctx := t.Context()

	u := &User{Issuer: "iss", Sub: "s1", DisplayName: new("Alice"), Email: new("alice@example.com"), CreatedBy: "x", UpdatedBy: "x"}
	if err := repo.UpsertUser(ctx, u, nil); err != nil {
		t.Fatalf("seed s1: %v", err)
	}

	usersInOrder := func(order string) []User {
		t.Helper()
		users, _, err := repo.ListUsers(ctx, UserListOptions{
			PageNumber: 1, PageSize: 20, SortColumn: "last_login_at", SortOrder: order,
		})
		if err != nil {
			t.Fatalf("list last_login_at %s: %v", order, err)
		}
		return users
	}

	asc := usersInOrder("ASC")
	if len(asc) != 1+seededSystemUserCount || asc[0].Sub != "s1" {
		t.Errorf("last_login_at ASC = %v, want s1 first, then the seeded rows", withoutSeededSystemUsers(asc))
	}
	desc := usersInOrder("DESC")
	if len(desc) != 1+seededSystemUserCount || desc[len(desc)-1].Sub != "s1" {
		t.Errorf("last_login_at DESC led by the seeded rows? got last %v, want s1", desc[len(desc)-1].Sub)
	}

	for _, u := range asc {
		if u.Issuer == seededSystemUserIssuer && u.LastLoginAt != nil {
			t.Errorf("%s LastLoginAt = %v, want nil (sentinel must not leak into the returned value)", u.Sub, *u.LastLoginAt)
		}
	}
}

func TestUsersRepo_ListUsersSortsNullNamesTheSameOnBothDialects(t *testing.T) {
	db := TestDB(t)
	repo := NewUsersRepo(db)
	ctx := t.Context()

	seed := []struct {
		sub   string
		name  *string
		email *string
	}{
		{"has-name", new("alpha"), new("alpha@example.com")},
		{"no-name", nil, new("beta@example.com")},
		{"has-name-late", new("gamma"), new("gamma@example.com")},
		{"no-name-no-email", nil, nil},
	}
	for _, s := range seed {
		u := &User{Issuer: "iss", Sub: s.sub, DisplayName: s.name, Email: s.email, CreatedBy: "x", UpdatedBy: "x"}
		if err := repo.UpsertUser(ctx, u, nil); err != nil {
			t.Fatalf("seed %s: %v", s.sub, err)
		}
	}

	subsInOrder := func(order string) []string {
		t.Helper()
		users, _, err := repo.ListUsers(ctx, UserListOptions{PageNumber: 1, PageSize: 20, SortColumn: "display_name", SortOrder: order})
		if err != nil {
			t.Fatalf("list display_name %s: %v", order, err)
		}
		return withoutSeededSystemUsers(users)
	}

	wantASC := []string{"no-name-no-email", "has-name", "no-name", "has-name-late"}
	if got := subsInOrder("ASC"); !slices.Equal(got, wantASC) {
		t.Errorf("display_name ASC = %v, want %v", got, wantASC)
	}
	wantDESC := []string{"has-name-late", "no-name", "has-name", "no-name-no-email"}
	if got := subsInOrder("DESC"); !slices.Equal(got, wantDESC) {
		t.Errorf("display_name DESC = %v, want %v", got, wantDESC)
	}

	users, _, err := repo.ListUsers(ctx, UserListOptions{PageNumber: 1, PageSize: 20, SortColumn: "email", SortOrder: "ASC"})
	if err != nil {
		t.Fatalf("list email ASC: %v", err)
	}
	if got := withoutSeededSystemUsers(users); len(got) == 0 || got[0] != "no-name-no-email" {
		t.Errorf("email ASC led with %v, want the user with no email first", got)
	}
}

func TestUsersRepo_ListUsersSortsNamesCaseInsensitively(t *testing.T) {
	db := TestDB(t)
	repo := NewUsersRepo(db)
	ctx := t.Context()

	for i, name := range []string{"Banana", "apple", "Cherry"} {
		u := &User{Issuer: "iss", Sub: fmt.Sprintf("s%d", i), DisplayName: new(name), CreatedBy: "x", UpdatedBy: "x"}
		if err := repo.UpsertUser(ctx, u, nil); err != nil {
			t.Fatalf("seed %s: %v", name, err)
		}
	}

	users, _, err := repo.ListUsers(ctx, UserListOptions{PageNumber: 1, PageSize: 20, SortColumn: "display_name", SortOrder: "ASC"})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var got []string
	for _, u := range users {
		if u.Issuer != seededSystemUserIssuer {
			got = append(got, *u.DisplayName)
		}
	}
	if want := []string{"apple", "Banana", "Cherry"}; !slices.Equal(got, want) {
		t.Errorf("display_name ASC = %v, want %v", got, want)
	}
}

func TestUsersRepo_GetByIDAndLookupHelpers(t *testing.T) {
	db := TestDB(t)
	repo := NewUsersRepo(db)
	ctx := t.Context()

	u := &User{Issuer: "iss", Sub: "s-look", OIDCProvider: "azure", Email: new("look@example.com"), CreatedBy: "x", UpdatedBy: "x"}
	if err := repo.UpsertUser(ctx, u, []string{"eng", "ops"}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	got, groups, err := repo.GetByID(ctx, u.ID)
	if err != nil || got == nil {
		t.Fatalf("GetByID: %v (got=%v)", err, got)
	}
	if got.Sub != "s-look" {
		t.Errorf("GetByID sub = %q, want s-look", got.Sub)
	}
	slices.Sort(groups)
	if !slices.Equal(groups, []string{"eng", "ops"}) {
		t.Errorf("groups = %v, want [eng ops]", groups)
	}

	missing, _, err := repo.GetByID(ctx, "00000000-0000-0000-0000-000000000000")
	if err != nil {
		t.Fatalf("GetByID missing: %v", err)
	}
	if missing != nil {
		t.Errorf("GetByID missing = %+v, want nil", missing)
	}

	ids, err := repo.UserIDsByIdentities(ctx, []string{"look@example.com"})
	if err != nil || len(ids) != 1 || ids[0] != u.ID {
		t.Fatalf("UserIDsByIdentities(email) = %v (err %v), want [%s]", ids, err, u.ID)
	}
	ids, err = repo.UserIDsByIdentities(ctx, []string{"s-look"})
	if err != nil || len(ids) != 1 || ids[0] != u.ID {
		t.Fatalf("UserIDsByIdentities(sub) = %v (err %v), want [%s]", ids, err, u.ID)
	}

	ids, err = repo.UserIDsByGroups(ctx, []string{"ops"})
	if err != nil || len(ids) != 1 || ids[0] != u.ID {
		t.Fatalf("UserIDsByGroups = %v (err %v), want [%s]", ids, err, u.ID)
	}

	groupsByID, err := repo.GroupsForUsers(ctx, []string{u.ID})
	if err != nil {
		t.Fatalf("GroupsForUsers: %v", err)
	}
	got2 := groupsByID[u.ID]
	slices.Sort(got2)
	if !slices.Equal(got2, []string{"eng", "ops"}) {
		t.Errorf("GroupsForUsers = %v, want [eng ops]", got2)
	}
}

func queryUserCount(ctx context.Context, db *DB, issuer, sub string) (int, error) {
	row := db.QueryRowContext(ctx,
		db.Rebind(`SELECT COUNT(*) FROM users WHERE issuer = ? AND sub = ?`), issuer, sub)
	var n int
	if err := row.Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

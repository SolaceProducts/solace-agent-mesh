// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"errors"
	"testing"
)

func TestUsersRepo_SystemUserWritesCannotTouchAHuman(t *testing.T) {
	db := TestDB(t)
	repo := NewUsersRepo(db)
	ctx := t.Context()

	human := &User{
		Issuer: "https://idp.example.com", Sub: "sub-human", OIDCProvider: "azure",
		DisplayName: new("Ada Lovelace"), Email: new("ada@example.com"),
		ProvisionedVia: "jit", CreatedBy: "x", UpdatedBy: "x",
	}
	if err := repo.UpsertUser(ctx, human, []string{"eng"}); err != nil {
		t.Fatalf("seed human: %v", err)
	}

	if err := repo.UpdateSystemUser(ctx, human.ID, new("Renamed By Config"), "actor"); !errors.Is(err, ErrSystemUserNotFound) {
		t.Errorf("UpdateSystemUser(human id) = %v, want ErrSystemUserNotFound", err)
	}
	if err := repo.DeleteSystemUser(ctx, human.ID); !errors.Is(err, ErrSystemUserNotFound) {
		t.Errorf("DeleteSystemUser(human id) = %v, want ErrSystemUserNotFound", err)
	}

	got, groups, err := repo.GetByID(ctx, human.ID)
	if err != nil || got == nil {
		t.Fatalf("human row after refused writes: got=%v err=%v", got, err)
	}
	if got.DisplayName == nil || *got.DisplayName != "Ada Lovelace" {
		t.Errorf("display name = %v, want unchanged Ada Lovelace", got.DisplayName)
	}
	if len(groups) != 1 || groups[0] != "eng" {
		t.Errorf("groups = %v, want [eng] — a refused write must not disturb membership", groups)
	}
}

func epochMs(v *int64) int64 {
	if v == nil {
		return 0
	}
	return *v
}

func TestUsersRepo_SystemUserWritesApplyToASystemUser(t *testing.T) {
	db := TestDB(t)
	repo := NewUsersRepo(db)
	ctx := t.Context()

	su := &User{
		Issuer: "sam", Sub: "system:orders-bot",
		DisplayName: new("Orders Bot"), ProvisionedVia: provisionedViaSystem,
		CreatedBy: "x", UpdatedBy: "x",
	}
	if err := repo.UpsertUser(ctx, su, nil); err != nil {
		t.Fatalf("seed system user: %v", err)
	}
	seeded, _, err := repo.GetByID(ctx, su.ID)
	if err != nil || seeded == nil {
		t.Fatalf("read back seeded system user: got=%v err=%v", seeded, err)
	}
	lastLoginAtSeed := seeded.LastLoginAt

	if err = repo.UpdateSystemUser(ctx, su.ID, new("Orders Bot v2"), "actor"); err != nil {
		t.Fatalf("UpdateSystemUser: %v", err)
	}

	updated, _, err := repo.GetByID(ctx, su.ID)
	if err != nil || updated == nil {
		t.Fatalf("read back updated: got=%v err=%v", updated, err)
	}
	if updated.DisplayName == nil || *updated.DisplayName != "Orders Bot v2" {
		t.Errorf("display name = %v, want Orders Bot v2", updated.DisplayName)
	}
	if epochMs(updated.LastLoginAt) != epochMs(lastLoginAtSeed) {
		t.Errorf("last login moved from %d to %d; UpdateSystemUser must not touch it", epochMs(lastLoginAtSeed), epochMs(updated.LastLoginAt))
	}
	if updated.UpdatedBy != "actor" {
		t.Errorf("updated_by = %q, want actor", updated.UpdatedBy)
	}

	if err = repo.DeleteSystemUser(ctx, su.ID); err != nil {
		t.Fatalf("DeleteSystemUser: %v", err)
	}
	gone, _, err := repo.GetByID(ctx, su.ID)
	if err != nil {
		t.Fatalf("read back deleted: %v", err)
	}
	if gone != nil {
		t.Errorf("system user still present after delete: %+v", gone)
	}
	if err := repo.DeleteSystemUser(ctx, su.ID); !errors.Is(err, ErrSystemUserNotFound) {
		t.Errorf("second DeleteSystemUser = %v, want ErrSystemUserNotFound", err)
	}
}

func TestUsersRepo_SystemUserIDMatchesOnlyASystemUser(t *testing.T) {
	db := TestDB(t)
	repo := NewUsersRepo(db)
	ctx := t.Context()

	system := &User{Issuer: "sam", Sub: "system:orders-bot", ProvisionedVia: provisionedViaSystem, CreatedBy: "x", UpdatedBy: "x"}
	shadow := &User{Issuer: "sam", Sub: "system:shadow-bot", ProvisionedVia: "jit", CreatedBy: "x", UpdatedBy: "x"}
	for _, u := range []*User{system, shadow} {
		if err := repo.UpsertUser(ctx, u, nil); err != nil {
			t.Fatalf("seed %s: %v", u.Sub, err)
		}
	}

	for _, tc := range []struct {
		sub, want string
	}{
		{"system:orders-bot", system.ID},
		{"system:shadow-bot", ""},
		{"system:nobody", ""},
	} {
		got, err := repo.SystemUserID(ctx, "sam", tc.sub)
		if err != nil {
			t.Fatalf("SystemUserID(%s): %v", tc.sub, err)
		}
		if got != tc.want {
			t.Errorf("SystemUserID(%s) = %q, want %q", tc.sub, got, tc.want)
		}
	}
}

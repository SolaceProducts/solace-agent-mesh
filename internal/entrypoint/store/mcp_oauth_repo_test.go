// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"testing"
	"time"
)

func TestMCPOAuthRepo_ErrorsOnAClosedDatabase(t *testing.T) {
	db := TestDB(t)
	repo := NewMCPOAuthRepo(db)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	now := time.Now().UnixMilli()
	if err := repo.PutState(ctx, "gw", "s", "v", "d", now+1000, now); err == nil {
		t.Error("PutState")
	}
	if _, _, err := repo.TakeState(ctx, "gw", "s", now); err == nil {
		t.Error("TakeState")
	}
	if _, _, err := repo.TakeStateByVerifier(ctx, "gw", "v", now); err == nil {
		t.Error("TakeStateByVerifier")
	}
	if _, err := repo.CountLiveStates(ctx, "gw", now); err == nil {
		t.Error("CountLiveStates")
	}
	if err := repo.PutCode(ctx, "gw", "c", "d", now+1000, now); err == nil {
		t.Error("PutCode")
	}
	if _, _, err := repo.TakeCode(ctx, "gw", "c", now); err == nil {
		t.Error("TakeCode")
	}
	if err := repo.PutRefresh(ctx, "gw", "r", "d", now+1000, now); err == nil {
		t.Error("PutRefresh")
	}
	if _, _, err := repo.GetRefresh(ctx, "gw", "r", now); err == nil {
		t.Error("GetRefresh")
	}
	if _, err := repo.RotateRefresh(ctx, "gw", "r", "r2", "d", now+1000, now); err == nil {
		t.Error("RotateRefresh")
	}
	if err := repo.DeleteExpired(ctx, now); err == nil {
		t.Error("DeleteExpired")
	}
}

func TestMCPOAuthRepo_RotateInsertFailureKeepsTheOldMapping(t *testing.T) {
	repo := NewMCPOAuthRepo(TestDB(t))
	ctx := t.Context()
	now := time.Now().UnixMilli()
	for _, h := range []string{"old", "taken"} {
		if err := repo.PutRefresh(ctx, "gw", h, "d-"+h, now+60_000, now); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := repo.RotateRefresh(ctx, "gw", "old", "taken", "new", now+60_000, now); err == nil {
		t.Fatal("rotating onto an existing hash succeeded")
	}
	if data, ok, _ := repo.GetRefresh(ctx, "gw", "old", now); !ok || data != "d-old" {
		t.Errorf("old mapping after a failed rotation = %q, %v", data, ok)
	}
}

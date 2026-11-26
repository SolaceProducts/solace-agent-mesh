// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"testing"
	"time"
)

func TestMCPConnectorOAuthStateRepo(t *testing.T) {
	db := TestDB(t)
	repo := NewMCPConnectorOAuthStateRepo(db)
	ctx := t.Context()
	now := time.Now().UnixMilli()

	if err := repo.Insert(ctx, MCPConnectorOAuthState{StateHash: "h1", SessionID: "sess1", OwnerUserID: "alice", Data: "sealed", AccessToken: "", ExpiresAt: now + 60_000, CreatedTime: now}); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if err := repo.Insert(ctx, MCPConnectorOAuthState{StateHash: "old", SessionID: "sess-old", OwnerUserID: "alice", Data: "sealed", AccessToken: "", ExpiresAt: now - 1, CreatedTime: now - 60_000}); err != nil {
		t.Fatalf("Insert expired: %v", err)
	}

	got, err := repo.Get(ctx, "h1")
	if err != nil || got == nil || got.SessionID != "sess1" || got.Data != "sealed" || got.AccessToken != "" {
		t.Fatalf("Get = %+v, %v", got, err)
	}
	if old, getErr := repo.Get(ctx, "old"); getErr != nil || old == nil || old.ExpiresAt != now-1 {
		t.Fatalf("Get expired = %+v, %v", old, getErr)
	}
	if missing, getErr := repo.Get(ctx, "missing"); getErr != nil || missing != nil {
		t.Fatalf("Get missing = %+v, %v", missing, getErr)
	}

	set, err := repo.SetAccessToken(ctx, "h1", "tok", now)
	if err != nil || !set {
		t.Fatalf("first SetAccessToken = %v, %v", set, err)
	}
	if set, err := repo.SetAccessToken(ctx, "h1", "tok2", now); err != nil || set {
		t.Fatalf("second SetAccessToken = %v, %v", set, err)
	}
	if set, err := repo.SetAccessToken(ctx, "old", "tok", now); err != nil || set {
		t.Fatalf("SetAccessToken on expired = %v, %v", set, err)
	}

	if got, err := repo.FindBySession(ctx, "sess1", "bob", now); err != nil || got != nil {
		t.Fatalf("FindBySession wrong owner = %+v, %v", got, err)
	}
	if got, err := repo.FindBySession(ctx, "sess1", "alice", now); err != nil || got == nil || got.AccessToken != "tok" {
		t.Fatalf("FindBySession = %+v, %v", got, err)
	}

	if got, err := repo.TakeBySession(ctx, "sess1", now); err != nil || got == nil || got.OwnerUserID != "alice" {
		t.Fatalf("TakeBySession = %+v, %v", got, err)
	}
	if got, err := repo.TakeBySession(ctx, "sess1", now); err != nil || got != nil {
		t.Fatalf("second TakeBySession = %+v, %v", got, err)
	}
	if got, err := repo.TakeBySession(ctx, "sess-old", now); err != nil || got != nil {
		t.Fatalf("TakeBySession expired = %+v, %v", got, err)
	}

	if err := repo.Insert(ctx, MCPConnectorOAuthState{StateHash: "h2", SessionID: "sess2", OwnerUserID: "alice", Data: "sealed", AccessToken: "tok", ExpiresAt: now + 60_000, CreatedTime: now}); err != nil {
		t.Fatalf("Insert with token: %v", err)
	}
	if err := repo.Delete(ctx, "h2"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if got, err := repo.Get(ctx, "h2"); err != nil || got != nil {
		t.Fatalf("Get after Delete = %+v, %v", got, err)
	}
	if err := repo.DeleteExpired(ctx, now); err != nil {
		t.Fatalf("DeleteExpired: %v", err)
	}
	var n int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM mcp_connector_oauth_states`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("rows left = %d, %v", n, err)
	}
}

func TestMCPConnectorOAuthStateRepoErrors(t *testing.T) {
	db := TestDB(t)
	repo := NewMCPConnectorOAuthStateRepo(db)
	ctx := t.Context()
	_ = db.Close()
	if err := repo.Insert(ctx, MCPConnectorOAuthState{StateHash: "h", SessionID: "s", OwnerUserID: "o", Data: "d", AccessToken: "", ExpiresAt: 1, CreatedTime: 0}); err == nil {
		t.Error("Insert on closed db")
	}
	if _, err := repo.Get(ctx, "h"); err == nil {
		t.Error("Get on closed db")
	}
	if err := repo.Delete(ctx, "h"); err == nil {
		t.Error("Delete on closed db")
	}
	if _, err := repo.SetAccessToken(ctx, "h", "t", 0); err == nil {
		t.Error("SetAccessToken on closed db")
	}
	if _, err := repo.FindBySession(ctx, "s", "o", 0); err == nil {
		t.Error("FindBySession on closed db")
	}
	if _, err := repo.TakeBySession(ctx, "s", 0); err == nil {
		t.Error("TakeBySession on closed db")
	}
	if err := repo.DeleteExpired(ctx, 0); err == nil {
		t.Error("DeleteExpired on closed db")
	}
}

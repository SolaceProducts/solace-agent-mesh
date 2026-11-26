// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"context"
	"testing"
	"time"
)

func seedEphemeralAgent(t *testing.T, repo *AgentRepo, sessionID, owner string, expiresAt int64) *Agent {
	t.Helper()
	desc := "test ephemeral"
	prompt := "p"
	skills := "[]"
	src := "Source"
	a := &Agent{
		Name:                "test_" + sessionID[:8] + "__" + owner,
		Type:                "standard",
		Description:         &desc,
		SystemPrompt:        &prompt,
		Skills:              &skills,
		CreatedBy:           owner,
		UpdatedBy:           owner,
		IsEphemeral:         true,
		ExpiresAt:           &expiresAt,
		SourceSessionID:     &sessionID,
		SourceComponentName: &src,
	}
	if err := repo.Create(context.Background(), a, nil, nil, nil, nil, nil); err != nil {
		t.Fatalf("seed: %v", err)
	}
	return a
}

func TestAgentRepo_GetEphemeralForSession_MatchesByOwner(t *testing.T) {
	db := TestDB(t)
	repo := NewAgentRepo(db)
	ctx := context.Background()

	sessionID := "session-aaaaaaaa-1111"
	owner := "user-1"
	expires := time.Now().Add(time.Hour).UnixMilli()
	seedEphemeralAgent(t, repo, sessionID, owner, expires)

	got, err := repo.GetEphemeralForSession(ctx, sessionID, owner)
	if err != nil {
		t.Fatalf("GetEphemeralForSession: %v", err)
	}
	if got == nil {
		t.Fatal("got nil, want the seeded ephemeral")
	}
	if !got.IsEphemeral {
		t.Error("returned row should have IsEphemeral=true")
	}
	if got.SourceSessionID == nil || *got.SourceSessionID != sessionID {
		t.Errorf("SourceSessionID = %v, want %q", got.SourceSessionID, sessionID)
	}
}

func TestAgentRepo_GetEphemeralForSession_NoMatchReturnsNil(t *testing.T) {
	db := TestDB(t)
	repo := NewAgentRepo(db)

	got, err := repo.GetEphemeralForSession(context.Background(), "session-zzzzzzzz-9999", "u-x")
	if err != nil {
		t.Fatalf("GetEphemeralForSession: %v", err)
	}
	if got != nil {
		t.Errorf("got %v, want nil for missing pair", got)
	}
}

func TestAgentRepo_GetEphemeralForSession_DifferentOwnerReturnsNil(t *testing.T) {
	db := TestDB(t)
	repo := NewAgentRepo(db)
	ctx := context.Background()

	sessionID := "session-aaaaaaaa-2222"
	owner := "owner-real"
	seedEphemeralAgent(t, repo, sessionID, owner, time.Now().Add(time.Hour).UnixMilli())

	got, err := repo.GetEphemeralForSession(ctx, sessionID, "intruder")
	if err != nil {
		t.Fatalf("GetEphemeralForSession: %v", err)
	}
	if got != nil {
		t.Errorf("got %v, want nil — cross-owner lookup must not match", got)
	}
}

func TestAgentRepo_ListExpiredEphemeralIDs(t *testing.T) {
	db := TestDB(t)
	repo := NewAgentRepo(db)
	ctx := context.Background()

	now := time.Now().UnixMilli()
	expired := seedEphemeralAgent(t, repo, "session-expired-aaaa", "u-1", now-5_000)
	_ = seedEphemeralAgent(t, repo, "session-live-bbbb", "u-2", now+10*60*1000)

	ids, err := repo.ListExpiredEphemeralIDs(ctx, now)
	if err != nil {
		t.Fatalf("ListExpiredEphemeralIDs: %v", err)
	}
	if len(ids) != 1 || ids[0] != expired.ID {
		t.Errorf("got %v, want [%s] (live row must not be reaped)", ids, expired.ID)
	}
}

func TestAgentRepo_ListExpiredEphemeralIDs_EmptyWhenNoneExpired(t *testing.T) {
	db := TestDB(t)
	repo := NewAgentRepo(db)

	now := time.Now().UnixMilli()
	_ = seedEphemeralAgent(t, repo, "session-future-cccc", "u-3", now+10*60*1000)

	ids, err := repo.ListExpiredEphemeralIDs(context.Background(), now)
	if err != nil {
		t.Fatalf("ListExpiredEphemeralIDs: %v", err)
	}
	if len(ids) != 0 {
		t.Errorf("got %v, want empty (no expired rows)", ids)
	}
}

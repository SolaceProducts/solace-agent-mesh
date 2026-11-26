// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"testing"
)

const (
	upsertOwnerSession = "00000000-0000-0000-0000-0000000000a1"
	upsertOtherSession = "00000000-0000-0000-0000-0000000000b1"
	upsertOwnerTask    = "20000000-0000-0000-0000-0000000000a1"
)

func seedUpsertFixture(t *testing.T) (*ChatTaskRepo, *SessionRepo) {
	t.Helper()
	db := newTestDB(t)
	sessions := NewSessionRepo(db)
	repo := NewChatTaskRepo(db)
	ctx := t.Context()
	for _, s := range []*Session{
		{ID: upsertOwnerSession, UserID: "alice"},
		{ID: upsertOtherSession, UserID: "bob"},
	} {
		if err := sessions.Create(ctx, s); err != nil {
			t.Fatal(err)
		}
	}
	if err := repo.Create(ctx, &ChatTask{
		ID: upsertOwnerTask, SessionID: upsertOwnerSession, UserID: "alice",
		UserMessage: strPtr("alice question"), MessageBubbles: `[{"text":"alice"}]`, TaskMetadata: strPtr(`{"owner":"alice"}`),
	}); err != nil {
		t.Fatal(err)
	}
	return repo, sessions
}

func TestChatTaskRepo_Upsert_ByOwnerAndSession(t *testing.T) {
	cases := []struct {
		name        string
		sessionID   string
		userID      string
		wantWritten bool
	}{
		{"same user, same session", upsertOwnerSession, "alice", true},
		{"another user, their own session", upsertOtherSession, "bob", false},
		{"another user, the owner's session", upsertOwnerSession, "bob", false},
		{"same user, another of their sessions", upsertOtherSession, "alice", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo, _ := seedUpsertFixture(t)
			ctx := t.Context()

			written, err := repo.Upsert(ctx, &ChatTask{
				ID: upsertOwnerTask, SessionID: tc.sessionID, UserID: tc.userID,
				UserMessage: strPtr("overwritten"), MessageBubbles: `[{"text":"overwritten"}]`, TaskMetadata: strPtr(`{"owner":"other"}`),
			})
			if err != nil {
				t.Fatal(err)
			}
			if written != tc.wantWritten {
				t.Errorf("written = %v, want %v", written, tc.wantWritten)
			}

			got, err := repo.GetByID(ctx, upsertOwnerTask)
			if err != nil || got == nil {
				t.Fatalf("GetByID: %v, %v", got, err)
			}
			if got.UserID != "alice" || got.SessionID != upsertOwnerSession {
				t.Errorf("row moved to user=%s session=%s", got.UserID, got.SessionID)
			}
			if tc.wantWritten {
				if got.MessageBubbles != `[{"text":"overwritten"}]` || got.UserMessage == nil || *got.UserMessage != "overwritten" || got.UpdatedTime == nil {
					t.Errorf("owner's re-save did not update the row: %+v", got)
				}
				return
			}
			if got.MessageBubbles != `[{"text":"alice"}]` || got.UserMessage == nil || *got.UserMessage != "alice question" ||
				got.TaskMetadata == nil || *got.TaskMetadata != `{"owner":"alice"}` || got.UpdatedTime != nil {
				t.Errorf("refused upsert changed the row: %+v", got)
			}
		})
	}
}

func TestChatTaskRepo_Upsert_RefusedWriteDoesNotMarkTheSessionActive(t *testing.T) {
	repo, sessions := seedUpsertFixture(t)
	ctx := t.Context()

	written, err := repo.Upsert(ctx, &ChatTask{
		ID: upsertOwnerTask, SessionID: upsertOtherSession, UserID: "bob", MessageBubbles: "[]",
	})
	if err != nil || written {
		t.Fatalf("Upsert = %v, %v; want refused without error", written, err)
	}
	bobSession, err := sessions.GetByID(ctx, upsertOtherSession)
	if err != nil || bobSession == nil {
		t.Fatalf("GetByID: %v, %v", bobSession, err)
	}
	if bobSession.HasActivity {
		t.Error("a refused upsert marked the caller's session active")
	}
}

func TestChatTaskRepo_Upsert_NewIDIsCreatedForTheCaller(t *testing.T) {
	repo, sessions := seedUpsertFixture(t)
	ctx := t.Context()

	const newID = "20000000-0000-0000-0000-0000000000b2"
	written, err := repo.Upsert(ctx, &ChatTask{ID: newID, SessionID: upsertOtherSession, UserID: "bob", MessageBubbles: "[]"})
	if err != nil || !written {
		t.Fatalf("Upsert = %v, %v; want written", written, err)
	}
	got, err := repo.GetByID(ctx, newID)
	if err != nil || got == nil || got.UserID != "bob" || got.SessionID != upsertOtherSession {
		t.Fatalf("created row = %+v, %v", got, err)
	}
	bobSession, err := sessions.GetByID(ctx, upsertOtherSession)
	if err != nil || bobSession == nil || !bobSession.HasActivity {
		t.Errorf("session after the write = %+v, %v; want marked active", bobSession, err)
	}
}

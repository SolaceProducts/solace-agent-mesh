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

const sessionsHasActivityVersion = 30

func TestBackfill_SQLite_SessionsHasActivity(t *testing.T) {
	t.Parallel()
	db := openFreshSQLite(t)
	ctx := context.Background()

	if err := db.MigrateUpTo(ctx, sessionsHasActivityVersion-1); err != nil {
		t.Fatalf("migrate up to %d: %v", sessionsHasActivityVersion-1, err)
	}

	const (
		withChat  = "10000000-0000-0000-0000-000000000001"
		withTask  = "10000000-0000-0000-0000-000000000002"
		scheduler = "10000000-0000-0000-0000-000000000003"
		empty     = "10000000-0000-0000-0000-000000000004"
		slack     = "10000000-0000-0000-0000-000000000005"
	)
	for _, id := range []string{withChat, withTask, empty, slack} {
		if _, err := db.ExecContext(ctx,
			`INSERT INTO sessions (id, user_id, created_time, updated_time) VALUES (?, ?, 1, 1)`,
			id, "u1",
		); err != nil {
			t.Fatalf("seed session %s: %v", id, err)
		}
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO sessions (id, user_id, created_time, updated_time, source) VALUES (?, ?, 1, 1, 'scheduler')`,
		scheduler, "u1",
	); err != nil {
		t.Fatalf("seed scheduler session: %v", err)
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO chat_tasks (id, session_id, user_id, message_bubbles, created_time) VALUES (?, ?, ?, '[]', 1)`,
		"20000000-0000-0000-0000-000000000001", withChat, "u1",
	); err != nil {
		t.Fatalf("seed chat task: %v", err)
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO tasks (id, user_id, start_time, session_id) VALUES (?, ?, 1, ?)`,
		"30000000-0000-0000-0000-000000000001", "u1", withTask,
	); err != nil {
		t.Fatalf("seed task: %v", err)
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO chat_tasks (id, session_id, user_id, message_bubbles, created_time) VALUES (?, ?, ?, '[]', 1)`,
		"20000000-0000-0000-0000-000000000002", slack, "u1",
	); err != nil {
		t.Fatalf("seed slack chat task: %v", err)
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO gateway_session_keys (gateway_type, external_key, session_id, created_time) VALUES ('slack', 'C1:1.1', ?, 1)`,
		slack,
	); err != nil {
		t.Fatalf("seed slack mapping: %v", err)
	}

	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("complete migrations: %v", err)
	}

	repo := NewSessionRepo(db)
	got, total, err := repo.ListByUser(ctx, "u1", false, true, false, "", nil, nil, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 4 || len(got) != 4 {
		t.Fatalf("nonEmptyOnly after backfill: got %d (total %d), want 4", len(got), total)
	}
	active := make(map[string]bool, len(got))
	for _, s := range got {
		active[s.ID] = s.HasActivity
	}
	for _, want := range []string{withChat, withTask, scheduler, slack} {
		if !active[want] {
			t.Errorf("session %s should be back-filled active", want)
		}
	}
	if _, ok := active[empty]; ok {
		t.Error("never-started session should stay excluded after backfill")
	}

	webui, total, err := repo.ListByUser(ctx, "u1", false, true, true, "", nil, nil, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 3 || len(webui) != 3 {
		t.Fatalf("excludeExternalGateways after backfill: got %d (total %d), want 3", len(webui), total)
	}
	for _, s := range webui {
		if s.ID == slack {
			t.Error("slack-mapped session should be back-filled external and excluded")
		}
	}
}

func TestChatTaskRepo_UpsertMarksSessionActive(t *testing.T) {
	db := newTestDB(t)
	repo := NewSessionRepo(db)
	chatRepo := NewChatTaskRepo(db)
	ctx := context.Background()

	const sessionID = "10000000-0000-0000-0000-000000000001"
	if err := repo.Create(ctx, &Session{ID: sessionID, UserID: "u1"}); err != nil {
		t.Fatal(err)
	}
	if _, total, err := repo.ListByUser(ctx, "u1", false, true, false, "", nil, nil, 10, 0); err != nil || total != 0 {
		t.Fatalf("fresh session should be hidden from nonEmptyOnly list: total %d err %v", total, err)
	}

	um := "hi"
	if _, err := chatRepo.Upsert(ctx, &ChatTask{
		ID: "20000000-0000-0000-0000-000000000001", SessionID: sessionID, UserID: "u1",
		UserMessage: &um, MessageBubbles: "[]", CreatedTime: time.Now().UnixMilli(),
	}); err != nil {
		t.Fatal(err)
	}

	sess, err := repo.GetByID(ctx, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if sess == nil || !sess.HasActivity {
		t.Fatal("first chat-task upsert should mark the session active")
	}
	if _, total, err := repo.ListByUser(ctx, "u1", false, true, false, "", nil, nil, 10, 0); err != nil || total != 1 {
		t.Fatalf("session should appear in nonEmptyOnly list after upsert: total %d err %v", total, err)
	}
}

func TestTaskRepo_Create_WithoutSession(t *testing.T) {
	db := newTestDB(t)
	taskRepo := NewTaskRepo(db)
	ctx := context.Background()

	if err := taskRepo.Create(ctx, &Task{
		ID: "30000000-0000-0000-0000-000000000001", UserID: "u1",
		StartTime: time.Now().UnixMilli(), ExecutionMode: "foreground",
	}); err != nil {
		t.Fatalf("task without a session must insert cleanly: %v", err)
	}
}

func TestSessionRepo_Create_SchedulerSeedsActivity(t *testing.T) {
	db := newTestDB(t)
	repo := NewSessionRepo(db)
	ctx := context.Background()

	src := "scheduler"
	const sessionID = "10000000-0000-0000-0000-000000000001"
	if err := repo.Create(ctx, &Session{ID: sessionID, UserID: "u1", Source: &src}); err != nil {
		t.Fatal(err)
	}
	sess, err := repo.GetByID(ctx, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if sess == nil || !sess.HasActivity {
		t.Fatal("scheduler session should be created with has_activity set")
	}
}

func TestHealDenormalizedFlags(t *testing.T) {
	db := newTestDB(t)
	repo := NewSessionRepo(db)
	ctx := context.Background()

	const (
		hiddenChat = "10000000-0000-0000-0000-000000000001"
		leakySlack = "10000000-0000-0000-0000-000000000002"
		empty      = "10000000-0000-0000-0000-000000000003"
	)
	for _, id := range []string{hiddenChat, leakySlack, empty} {
		if _, err := db.ExecContext(ctx,
			`INSERT INTO sessions (id, user_id, created_time, updated_time) VALUES (?, ?, 1, 1)`,
			id, "u1",
		); err != nil {
			t.Fatalf("seed session %s: %v", id, err)
		}
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO chat_tasks (id, session_id, user_id, message_bubbles, created_time) VALUES (?, ?, ?, '[]', 1)`,
		"20000000-0000-0000-0000-000000000001", hiddenChat, "u1",
	); err != nil {
		t.Fatalf("seed chat task: %v", err)
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO gateway_session_keys (gateway_type, external_key, session_id, created_time) VALUES ('slack', 'C9:9.9', ?, 1)`,
		leakySlack,
	); err != nil {
		t.Fatalf("seed slack mapping: %v", err)
	}

	if err := repo.HealDenormalizedFlags(ctx); err != nil {
		t.Fatal(err)
	}

	assertFlags := func(id string, wantActivity, wantExternal bool) {
		t.Helper()
		sess, err := repo.GetByID(ctx, id)
		if err != nil || sess == nil {
			t.Fatalf("GetByID(%s): sess=%v err=%v", id, sess, err)
		}
		if sess.HasActivity != wantActivity || sess.ExternalGateway != wantExternal {
			t.Errorf("session %s: has_activity=%v external_gateway=%v, want %v/%v",
				id, sess.HasActivity, sess.ExternalGateway, wantActivity, wantExternal)
		}
	}
	assertFlags(hiddenChat, true, false)
	assertFlags(leakySlack, false, true)
	assertFlags(empty, false, false)
}

func TestGetOrCreateByExternalKey_RestampsExternalGateway(t *testing.T) {
	db := newTestDB(t)
	keyRepo := NewGatewaySessionKeyRepo(db)
	sessRepo := NewSessionRepo(db)
	ctx := context.Background()

	args := GetOrCreateByExternalKeyArgs{GatewayType: "slack", ExternalKey: "C1:42.0", UserID: "u1"}
	id, err := keyRepo.GetOrCreateByExternalKey(ctx, args)
	if err != nil {
		t.Fatal(err)
	}
	if _, execErr := db.ExecContext(ctx,
		`UPDATE sessions SET external_gateway = 0 WHERE id = ?`, id,
	); execErr != nil {
		t.Fatal(execErr)
	}

	again, err := keyRepo.GetOrCreateByExternalKey(ctx, args)
	if err != nil {
		t.Fatal(err)
	}
	if again != id {
		t.Fatalf("expected same session on reuse: got %s want %s", again, id)
	}
	sess, err := sessRepo.GetByID(ctx, id)
	if err != nil || sess == nil {
		t.Fatalf("GetByID: sess=%v err=%v", sess, err)
	}
	if !sess.ExternalGateway {
		t.Fatal("reuse should re-stamp external_gateway on a drifted session")
	}
}

// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"
)

func TestSessionRepo_CreateAndGetByID(t *testing.T) {
	db := newTestDB(t)
	repo := NewSessionRepo(db)
	ctx := context.Background()

	s := &Session{ID: "00000000-0000-0000-0000-000000000001", UserID: "u1"}
	if err := repo.Create(ctx, s); err != nil {
		t.Fatal(err)
	}

	got, err := repo.GetByID(ctx, "00000000-0000-0000-0000-000000000001")
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("expected session")
	}
	if got.UserID != "u1" {
		t.Errorf("user_id = %s, want u1", got.UserID)
	}
	if got.CreatedTime == 0 {
		t.Error("expected created_time to be set")
	}
}

func TestSessionRepo_ListAndSearch_ExcludeExternalGateways(t *testing.T) {
	db := newTestDB(t)
	repo := NewSessionRepo(db)
	keys := NewGatewaySessionKeyRepo(db)
	ctx := context.Background()

	web := "New Chat name"
	_ = repo.Create(ctx, &Session{ID: "00000000-0000-0000-0000-000000000001", UserID: "u1", Name: &web})
	_ = repo.Create(ctx, &Session{ID: "00000000-0000-0000-0000-000000000002", UserID: "u1", Name: &web})

	extID, err := keys.GetOrCreateByExternalKey(ctx, GetOrCreateByExternalKeyArgs{
		GatewayType: GatewayTypeSlack, ExternalKey: "C123:169.5", UserID: "u1", Name: &web,
	})
	if err != nil {
		t.Fatal(err)
	}

	schedID, err := keys.GetOrCreateByExternalKey(ctx, GetOrCreateByExternalKeyArgs{
		GatewayType: GatewayTypeScheduled, ExternalKey: "exec-1", UserID: "u1", Name: &web,
	})
	if err != nil {
		t.Fatal(err)
	}

	all, total, err := repo.ListByUser(ctx, "u1", false, false, false, "", nil, nil, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 4 || len(all) != 4 {
		t.Fatalf("include external: got len=%d total=%d, want 4/4", len(all), total)
	}

	scoped, total, err := repo.ListByUser(ctx, "u1", false, false, true, "", nil, nil, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 3 || len(scoped) != 3 {
		t.Fatalf("exclude external: got len=%d total=%d, want 3/3", len(scoped), total)
	}
	seenSched := false
	for _, s := range scoped {
		if s.ID == extID {
			t.Errorf("external Slack session %s should be excluded from the recent-chats list", extID)
		}
		if s.ID == schedID {
			seenSched = true
		}
	}
	if !seenSched {
		t.Errorf("scheduler session %s should remain in the recent-chats list", schedID)
	}

	found, total, err := repo.Search(ctx, "u1", "New Chat", SessionSearchFilter{}, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 3 || len(found) != 3 {
		t.Fatalf("search: got len=%d total=%d, want 3/3", len(found), total)
	}
	for _, s := range found {
		if s.ID == extID {
			t.Errorf("external Slack session %s should be excluded from search", extID)
		}
	}
}

func TestSessionRepo_GetByID_NotFound(t *testing.T) {
	db := newTestDB(t)
	repo := NewSessionRepo(db)

	got, err := repo.GetByID(context.Background(), "ffffffff-ffff-ffff-ffff-ffffffffffff")
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Error("expected nil")
	}
}

func TestSessionRepo_ListByUser(t *testing.T) {
	db := newTestDB(t)
	repo := NewSessionRepo(db)
	ctx := context.Background()

	for i := range 5 {
		s := &Session{ID: fmt.Sprintf("00000000-0000-0000-0000-00000000000%d", i+1), UserID: "u1"}
		_ = repo.Create(ctx, s)
	}

	sessions, total, err := repo.ListByUser(ctx, "u1", false, false, false, "", nil, nil, 3, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 5 {
		t.Errorf("total = %d, want 5", total)
	}
	if len(sessions) != 3 {
		t.Errorf("len = %d, want 3", len(sessions))
	}
}

func TestSessionRepo_ListByUser_TotalOnShortPage(t *testing.T) {
	db := newTestDB(t)
	repo := NewSessionRepo(db)
	ctx := context.Background()

	for i := range 5 {
		s := &Session{ID: fmt.Sprintf("00000000-0000-0000-0000-00000000000%d", i+1), UserID: "u1"}
		_ = repo.Create(ctx, s)
	}

	sessions, total, err := repo.ListByUser(ctx, "u1", false, false, false, "", nil, nil, 3, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 2 {
		t.Errorf("len = %d, want 2", len(sessions))
	}
	if total != 5 {
		t.Errorf("total = %d, want 5", total)
	}

	sessions, total, err = repo.ListByUser(ctx, "u1", false, false, false, "", nil, nil, 5, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 5 || total != 5 {
		t.Errorf("len = %d total = %d, want 5 and 5", len(sessions), total)
	}

	sessions, total, err = repo.ListByUser(ctx, "u1", false, false, false, "", nil, nil, 3, 6)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 0 || total != 5 {
		t.Errorf("len = %d total = %d, want 0 and 5", len(sessions), total)
	}
}

func TestSessionRepo_ListByUser_ExcludesDeleted(t *testing.T) {
	db := newTestDB(t)
	repo := NewSessionRepo(db)
	ctx := context.Background()

	_ = repo.Create(ctx, &Session{ID: "00000000-0000-0000-0000-000000000001", UserID: "u1"})
	_ = repo.Create(ctx, &Session{ID: "00000000-0000-0000-0000-000000000002", UserID: "u1"})
	_, _ = repo.SoftDelete(ctx, "00000000-0000-0000-0000-000000000001", "u1")

	sessions, total, err := repo.ListByUser(ctx, "u1", false, false, false, "", nil, nil, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 {
		t.Errorf("total = %d, want 1", total)
	}
	if len(sessions) != 1 {
		t.Errorf("len = %d, want 1", len(sessions))
	}
	if sessions[0].ID != "00000000-0000-0000-0000-000000000002" {
		t.Errorf("expected 00000000-0000-0000-0000-000000000002, got %s", sessions[0].ID)
	}
}

func TestSessionRepo_ListByUser_OrdersByLastActivity(t *testing.T) {
	db := newTestDB(t)
	repo := NewSessionRepo(db)
	ctx := context.Background()

	older := "00000000-0000-0000-0000-000000000001"
	newer := "00000000-0000-0000-0000-000000000002"

	_ = repo.Create(ctx, &Session{ID: older, UserID: "u1"})
	time.Sleep(2 * time.Millisecond)
	_ = repo.Create(ctx, &Session{ID: newer, UserID: "u1"})

	time.Sleep(2 * time.Millisecond)
	if err := repo.Touch(ctx, older); err != nil {
		t.Fatalf("Touch: %v", err)
	}

	sessions, _, err := repo.ListByUser(ctx, "u1", false, false, false, "", nil, nil, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 2 {
		t.Fatalf("len = %d, want 2", len(sessions))
	}
	if sessions[0].ID != older {
		t.Errorf("first session = %s, want the Touched (most recently active) one %s", sessions[0].ID, older)
	}
}

func TestSessionRepo_Rename(t *testing.T) {
	db := newTestDB(t)
	repo := NewSessionRepo(db)
	ctx := context.Background()

	_ = repo.Create(ctx, &Session{ID: "00000000-0000-0000-0000-000000000001", UserID: "u1"})

	if err := repo.Rename(ctx, "00000000-0000-0000-0000-000000000001", "New Name"); err != nil {
		t.Fatal(err)
	}

	got, _ := repo.GetByID(ctx, "00000000-0000-0000-0000-000000000001")
	if got.Name == nil || *got.Name != "New Name" {
		t.Errorf("name = %v, want 'New Name'", got.Name)
	}
}

func TestSessionRepo_Touch(t *testing.T) {
	db := newTestDB(t)
	repo := NewSessionRepo(db)
	ctx := context.Background()

	id := "00000000-0000-0000-0000-000000000010"
	_ = repo.Create(ctx, &Session{ID: id, UserID: "u1"})

	initial, _ := repo.GetByID(ctx, id)
	if initial == nil {
		t.Fatal("seed session missing")
	}
	time.Sleep(2 * time.Millisecond)

	if err := repo.Touch(ctx, id); err != nil {
		t.Fatalf("Touch: %v", err)
	}
	after, _ := repo.GetByID(ctx, id)
	if after.UpdatedTime <= initial.UpdatedTime {
		t.Errorf("UpdatedTime = %d, want > %d", after.UpdatedTime, initial.UpdatedTime)
	}
}

func TestSessionRepo_Touch_NotFound(t *testing.T) {
	db := newTestDB(t)
	repo := NewSessionRepo(db)

	err := repo.Touch(context.Background(), "ffffffff-ffff-ffff-ffff-ffffffffffff")
	if err != sql.ErrNoRows {
		t.Errorf("expected sql.ErrNoRows, got %v", err)
	}
}

func TestSessionRepo_SetAgentID(t *testing.T) {
	db := newTestDB(t)
	repo := NewSessionRepo(db)
	ctx := context.Background()

	id := "00000000-0000-0000-0000-000000000020"
	_ = repo.Create(ctx, &Session{ID: id, UserID: "u1"})

	if err := repo.SetAgentID(ctx, id, "agent-42"); err != nil {
		t.Fatalf("SetAgentID: %v", err)
	}
	got, _ := repo.GetByID(ctx, id)
	if got.AgentID == nil || *got.AgentID != "agent-42" {
		t.Errorf("AgentID = %v, want 'agent-42'", got.AgentID)
	}
}

func TestSessionRepo_SetAgentID_NotFound(t *testing.T) {
	db := newTestDB(t)
	repo := NewSessionRepo(db)

	err := repo.SetAgentID(context.Background(), "ffffffff-ffff-ffff-ffff-ffffffffffff", "agent-1")
	if err != sql.ErrNoRows {
		t.Errorf("expected sql.ErrNoRows, got %v", err)
	}
}

func TestSessionRepo_BackfillAgentID_WhenNull(t *testing.T) {
	db := newTestDB(t)
	repo := NewSessionRepo(db)
	ctx := context.Background()

	id := "00000000-0000-0000-0000-000000000030"
	_ = repo.Create(ctx, &Session{ID: id, UserID: "u1"})
	initial, _ := repo.GetByID(ctx, id)
	time.Sleep(2 * time.Millisecond)

	ok, err := repo.BackfillAgentID(ctx, id, "u1", "agent-42")
	if err != nil {
		t.Fatalf("BackfillAgentID: %v", err)
	}
	if !ok {
		t.Fatal("expected backfill to update a row")
	}
	got, _ := repo.GetByID(ctx, id)
	if got.AgentID == nil || *got.AgentID != "agent-42" {
		t.Errorf("AgentID = %v, want 'agent-42'", got.AgentID)
	}
	if got.UpdatedTime != initial.UpdatedTime {
		t.Errorf("UpdatedTime = %d, want unchanged %d", got.UpdatedTime, initial.UpdatedTime)
	}
}

func TestSessionRepo_BackfillAgentID_NoOverwrite(t *testing.T) {
	db := newTestDB(t)
	repo := NewSessionRepo(db)
	ctx := context.Background()

	id := "00000000-0000-0000-0000-000000000031"
	existing := "agent-original"
	_ = repo.Create(ctx, &Session{ID: id, UserID: "u1", AgentID: &existing})

	ok, err := repo.BackfillAgentID(ctx, id, "u1", "agent-new")
	if err != nil {
		t.Fatalf("BackfillAgentID: %v", err)
	}
	if ok {
		t.Error("expected no update when agent_id already set")
	}
	got, _ := repo.GetByID(ctx, id)
	if got.AgentID == nil || *got.AgentID != "agent-original" {
		t.Errorf("AgentID = %v, want unchanged 'agent-original'", got.AgentID)
	}
}

func TestSessionRepo_BackfillAgentID_WrongUser(t *testing.T) {
	db := newTestDB(t)
	repo := NewSessionRepo(db)
	ctx := context.Background()

	id := "00000000-0000-0000-0000-000000000032"
	_ = repo.Create(ctx, &Session{ID: id, UserID: "alice"})

	ok, err := repo.BackfillAgentID(ctx, id, "bob", "agent-42")
	if err != nil {
		t.Fatalf("BackfillAgentID: %v", err)
	}
	if ok {
		t.Error("expected no update for a session owned by another user")
	}
	got, _ := repo.GetByID(ctx, id)
	if got.AgentID != nil {
		t.Errorf("AgentID = %v, want nil", got.AgentID)
	}
}

func TestSessionRepo_BackfillAgentID_NotFound(t *testing.T) {
	db := newTestDB(t)
	repo := NewSessionRepo(db)

	ok, err := repo.BackfillAgentID(context.Background(), "ffffffff-ffff-ffff-ffff-ffffffffffff", "u1", "agent-1")
	if err != nil {
		t.Fatalf("BackfillAgentID: %v", err)
	}
	if ok {
		t.Error("expected no update for an unknown session")
	}
}

func TestSessionRepo_Rename_NotFound(t *testing.T) {
	db := newTestDB(t)
	repo := NewSessionRepo(db)

	err := repo.Rename(context.Background(), "ffffffff-ffff-ffff-ffff-ffffffffffff", "name")
	if err != sql.ErrNoRows {
		t.Errorf("expected sql.ErrNoRows, got %v", err)
	}
}

func TestSessionRepo_SoftDelete(t *testing.T) {
	db := newTestDB(t)
	repo := NewSessionRepo(db)
	ctx := context.Background()

	_ = repo.Create(ctx, &Session{ID: "00000000-0000-0000-0000-000000000001", UserID: "u1"})
	if _, err := repo.SoftDelete(ctx, "00000000-0000-0000-0000-000000000001", "u1"); err != nil {
		t.Fatal(err)
	}

	var deletedAt sql.NullInt64
	var deletedBy sql.NullString
	row := db.QueryRowContext(ctx,
		`SELECT deleted_at, deleted_by FROM sessions WHERE id = ?`,
		"00000000-0000-0000-0000-000000000001")
	if err := row.Scan(&deletedAt, &deletedBy); err != nil {
		t.Fatal(err)
	}
	if !deletedAt.Valid {
		t.Error("expected deleted_at to be set")
	}
	if !deletedBy.Valid || deletedBy.String != "u1" {
		t.Errorf("deleted_by = %v, want 'u1'", deletedBy)
	}

	if got, _ := repo.GetByID(ctx, "00000000-0000-0000-0000-000000000001"); got != nil {
		t.Errorf("GetByID should hide soft-deleted row, got %+v", got)
	}
}

func TestSessionRepo_SoftDelete_RejectsCrossUser(t *testing.T) {
	db := newTestDB(t)
	repo := NewSessionRepo(db)
	ctx := context.Background()

	_ = repo.Create(ctx, &Session{ID: "00000000-0000-0000-0000-000000000001", UserID: "alice"})
	if _, err := repo.SoftDelete(ctx, "00000000-0000-0000-0000-000000000001", "bob"); err != sql.ErrNoRows {
		t.Errorf("cross-user SoftDelete: got %v, want sql.ErrNoRows", err)
	}

	if got, _ := repo.GetByID(ctx, "00000000-0000-0000-0000-000000000001"); got == nil {
		t.Fatal("session should still be readable by owner after rejected cross-user delete")
	} else if got.DeletedAt != nil {
		t.Errorf("deleted_at should remain nil after rejected cross-user delete, got %v", *got.DeletedAt)
	}
}

func TestSessionRepo_GetByID_HidesSoftDeleted(t *testing.T) {
	db := newTestDB(t)
	repo := NewSessionRepo(db)
	ctx := context.Background()

	_ = repo.Create(ctx, &Session{ID: "00000000-0000-0000-0000-000000000001", UserID: "u1"})
	_, _ = repo.SoftDelete(ctx, "00000000-0000-0000-0000-000000000001", "u1")

	got, err := repo.GetByID(ctx, "00000000-0000-0000-0000-000000000001")
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Errorf("GetByID returned soft-deleted row %+v; want nil", got)
	}
}

func TestSessionRepo_GetByIDIncludingDeleted(t *testing.T) {
	db := newTestDB(t)
	repo := NewSessionRepo(db)
	ctx := context.Background()
	const live = "00000000-0000-0000-0000-000000000011"
	const deleted = "00000000-0000-0000-0000-000000000012"
	_ = repo.Create(ctx, &Session{ID: live, UserID: "u1"})
	_ = repo.Create(ctx, &Session{ID: deleted, UserID: "u2"})
	_, _ = repo.SoftDelete(ctx, deleted, "u2")

	got, err := repo.GetByIDIncludingDeleted(ctx, live)
	if err != nil || got == nil || got.UserID != "u1" || got.DeletedAt != nil {
		t.Errorf("live row: %+v, %v; want u1's row with no DeletedAt", got, err)
	}
	got, err = repo.GetByIDIncludingDeleted(ctx, deleted)
	if err != nil || got == nil || got.UserID != "u2" || got.DeletedAt == nil {
		t.Errorf("tombstone: %+v, %v; want u2's row with DeletedAt set", got, err)
	}
	got, err = repo.GetByIDIncludingDeleted(ctx, "00000000-0000-0000-0000-000000000013")
	if err != nil || got != nil {
		t.Errorf("unknown id: %+v, %v; want nil, nil", got, err)
	}
}

func TestSessionRepo_AssignProject(t *testing.T) {
	db := newTestDB(t)
	repo := NewSessionRepo(db)
	ctx := context.Background()

	_ = repo.Create(ctx, &Session{ID: "00000000-0000-0000-0000-000000000001", UserID: "u1"})

	if err := repo.AssignProject(ctx, "00000000-0000-0000-0000-000000000001", "30000000-0000-0000-0000-000000000001"); err != nil {
		t.Fatal(err)
	}

	got, _ := repo.GetByID(ctx, "00000000-0000-0000-0000-000000000001")
	if got.ProjectID == nil || *got.ProjectID != "30000000-0000-0000-0000-000000000001" {
		t.Errorf("project_id = %v, want '30000000-0000-0000-0000-000000000001'", got.ProjectID)
	}
}

func TestSessionRepo_ListByUser_WithProjectFilter(t *testing.T) {
	db := newTestDB(t)
	repo := NewSessionRepo(db)
	ctx := context.Background()

	_ = repo.Create(ctx, &Session{ID: "00000000-0000-0000-0000-000000000001", UserID: "u1"})
	_ = repo.Create(ctx, &Session{ID: "00000000-0000-0000-0000-000000000002", UserID: "u1"})
	_ = repo.AssignProject(ctx, "00000000-0000-0000-0000-000000000001", "30000000-0000-0000-0000-000000000001")

	sessions, total, err := repo.ListByUser(ctx, "u1", false, false, false, "30000000-0000-0000-0000-000000000001", nil, nil, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 {
		t.Errorf("total = %d, want 1", total)
	}
	if len(sessions) != 1 {
		t.Errorf("len = %d, want 1", len(sessions))
	}
	if sessions[0].ID != "00000000-0000-0000-0000-000000000001" {
		t.Errorf("expected 00000000-0000-0000-0000-000000000001, got %s", sessions[0].ID)
	}
}

func TestSessionRepo_Search(t *testing.T) {
	db := newTestDB(t)
	repo := NewSessionRepo(db)
	ctx := context.Background()

	name1 := "Weather Chat"
	name2 := "Code Review"
	name3 := "Weather Report"
	_ = repo.Create(ctx, &Session{ID: "00000000-0000-0000-0000-000000000001", UserID: "u1", Name: &name1})
	_ = repo.Create(ctx, &Session{ID: "00000000-0000-0000-0000-000000000002", UserID: "u1", Name: &name2})
	_ = repo.Create(ctx, &Session{ID: "00000000-0000-0000-0000-000000000003", UserID: "u1", Name: &name3})

	sessions, total, err := repo.Search(ctx, "u1", "Weather", SessionSearchFilter{}, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 {
		t.Errorf("total = %d, want 2", total)
	}
	if len(sessions) != 2 {
		t.Errorf("len = %d, want 2", len(sessions))
	}

	sessions, total, err = repo.Search(ctx, "u1", "Weather", SessionSearchFilter{}, 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || total != 2 {
		t.Errorf("len = %d total = %d, want 1 and 2", len(sessions), total)
	}
}

func TestSessionRepo_NonEmptyOnly_ExcludesNeverStarted(t *testing.T) {
	db := newTestDB(t)
	repo := NewSessionRepo(db)
	chatRepo := NewChatTaskRepo(db)
	taskRepo := NewTaskRepo(db)
	ctx := context.Background()

	const (
		withChat  = "10000000-0000-0000-0000-000000000001"
		withTask  = "10000000-0000-0000-0000-000000000002"
		scheduler = "10000000-0000-0000-0000-000000000003"
		empty     = "10000000-0000-0000-0000-000000000004"
	)
	src := "scheduler"
	_ = repo.Create(ctx, &Session{ID: withChat, UserID: "u1"})
	_ = repo.Create(ctx, &Session{ID: withTask, UserID: "u1"})
	_ = repo.Create(ctx, &Session{ID: scheduler, UserID: "u1", Source: &src})
	_ = repo.Create(ctx, &Session{ID: empty, UserID: "u1"})

	um := "hi"
	if err := chatRepo.Create(ctx, &ChatTask{
		ID: "20000000-0000-0000-0000-000000000001", SessionID: withChat, UserID: "u1",
		UserMessage: &um, MessageBubbles: "[]", CreatedTime: time.Now().UnixMilli(),
	}); err != nil {
		t.Fatal(err)
	}
	st := "running"
	wt := withTask
	if err := taskRepo.Create(ctx, &Task{
		ID: "30000000-0000-0000-0000-000000000001", UserID: "u1", Status: &st,
		StartTime: time.Now().UnixMilli(), ExecutionMode: "background", SessionID: &wt,
	}); err != nil {
		t.Fatal(err)
	}

	all, total, err := repo.ListByUser(ctx, "u1", false, false, false, "", nil, nil, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 4 || len(all) != 4 {
		t.Fatalf("nonEmptyOnly=false: got %d (total %d), want 4", len(all), total)
	}

	got, total, err := repo.ListByUser(ctx, "u1", false, true, false, "", nil, nil, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 3 || len(got) != 3 {
		t.Fatalf("nonEmptyOnly=true: got %d (total %d), want 3", len(got), total)
	}
	ids := make(map[string]bool, len(got))
	for _, s := range got {
		ids[s.ID] = true
	}
	if ids[empty] {
		t.Error("never-started session should be excluded when nonEmptyOnly=true")
	}
	for _, want := range []string{withChat, withTask, scheduler} {
		if !ids[want] {
			t.Errorf("session %s should be included when nonEmptyOnly=true", want)
		}
	}
}

func TestSessionRepo_Search_NonEmptyOnly_ExcludesNeverStarted(t *testing.T) {
	db := newTestDB(t)
	repo := NewSessionRepo(db)
	chatRepo := NewChatTaskRepo(db)
	ctx := context.Background()

	const (
		started = "10000000-0000-0000-0000-000000000001"
		empty   = "10000000-0000-0000-0000-000000000002"
	)
	n1, n2 := "Weather started", "Weather empty"
	_ = repo.Create(ctx, &Session{ID: started, UserID: "u1", Name: &n1})
	_ = repo.Create(ctx, &Session{ID: empty, UserID: "u1", Name: &n2})

	um := "hi"
	if err := chatRepo.Create(ctx, &ChatTask{
		ID: "20000000-0000-0000-0000-000000000001", SessionID: started, UserID: "u1",
		UserMessage: &um, MessageBubbles: "[]", CreatedTime: time.Now().UnixMilli(),
	}); err != nil {
		t.Fatal(err)
	}

	got, total, err := repo.Search(ctx, "u1", "Weather", SessionSearchFilter{NonEmptyOnly: true}, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(got) != 1 {
		t.Fatalf("nonEmptyOnly=true search: got %d (total %d), want 1", len(got), total)
	}
	if got[0].ID != started {
		t.Errorf("expected only the started session, got %s", got[0].ID)
	}
}

func TestSessionRepo_ExcludesEvalSessions(t *testing.T) {
	db := newTestDB(t)
	repo := NewSessionRepo(db)
	ctx := context.Background()

	const (
		normal    = "10000000-0000-0000-0000-000000000001"
		scheduler = "10000000-0000-0000-0000-000000000002"
		eval      = "10000000-0000-0000-0000-000000000003"
	)
	n1, n2, n3 := "Weather normal", "Weather scheduler", "Weather eval"
	schedulerSrc, evalSrc := "scheduler", "eval"
	_ = repo.Create(ctx, &Session{ID: normal, UserID: "u1", Name: &n1})
	_ = repo.Create(ctx, &Session{ID: scheduler, UserID: "u1", Name: &n2, Source: &schedulerSrc})
	_ = repo.Create(ctx, &Session{ID: eval, UserID: "u1", Name: &n3, Source: &evalSrc})

	assertNoEval := func(t *testing.T, got []*Session, total int) {
		t.Helper()
		if total != 2 || len(got) != 2 {
			t.Fatalf("got %d (total %d), want 2 (eval excluded)", len(got), total)
		}
		for _, s := range got {
			if s.ID == eval {
				t.Error("eval session should be excluded")
			}
		}
	}

	list, total, err := repo.ListByUser(ctx, "u1", false, false, false, "", nil, nil, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	assertNoEval(t, list, total)

	found, total, err := repo.Search(ctx, "u1", "Weather", SessionSearchFilter{}, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	assertNoEval(t, found, total)
}

func TestSessionRepo_MarkViewed_SetsTimestamp(t *testing.T) {
	db := newTestDB(t)
	repo := NewSessionRepo(db)
	ctx := context.Background()

	_ = repo.Create(ctx, &Session{ID: "00000000-0000-0000-0000-000000000001", UserID: "u1"})
	created, _ := repo.GetByID(ctx, "00000000-0000-0000-0000-000000000001")

	viewedAt := created.UpdatedTime + 10_000
	ok, err := repo.MarkViewed(ctx, "00000000-0000-0000-0000-000000000001", "u1", viewedAt)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected MarkViewed to return true (row found)")
	}

	got, _ := repo.GetByID(ctx, "00000000-0000-0000-0000-000000000001")
	if got.LastViewedAt == nil {
		t.Fatal("expected last_viewed_at to be set")
	}
	if *got.LastViewedAt != viewedAt {
		t.Errorf("last_viewed_at = %d, want %d", *got.LastViewedAt, viewedAt)
	}
	if got.UpdatedTime != created.UpdatedTime {
		t.Errorf("updated_time changed: before=%d after=%d — MarkViewed must leave it alone",
			created.UpdatedTime, got.UpdatedTime)
	}
}

func TestSessionRepo_MarkViewed_NotFound(t *testing.T) {
	db := newTestDB(t)
	repo := NewSessionRepo(db)

	ok, err := repo.MarkViewed(context.Background(), "ffffffff-ffff-ffff-ffff-ffffffffffff", "u1", 1700000000000)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Error("expected MarkViewed to return false (row missing)")
	}
}

func TestSessionRepo_MarkViewed_WrongUser(t *testing.T) {
	db := newTestDB(t)
	repo := NewSessionRepo(db)
	ctx := context.Background()

	_ = repo.Create(ctx, &Session{ID: "00000000-0000-0000-0000-000000000001", UserID: "owner"})

	ok, err := repo.MarkViewed(ctx, "00000000-0000-0000-0000-000000000001", "different-user", 1700000000000)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Error("expected MarkViewed to reject non-owner")
	}
	got, _ := repo.GetByID(ctx, "00000000-0000-0000-0000-000000000001")
	if got.LastViewedAt != nil {
		t.Errorf("last_viewed_at should remain nil for rejected owner, got %d", *got.LastViewedAt)
	}
}

func TestSessionRepo_MarkViewed_DoesNotRegressBehindUpdatedTime(t *testing.T) {
	db := newTestDB(t)
	repo := NewSessionRepo(db)
	ctx := context.Background()

	_ = repo.Create(ctx, &Session{ID: "00000000-0000-0000-0000-000000000001", UserID: "u1"})

	_ = repo.Rename(ctx, "00000000-0000-0000-0000-000000000001", "after-bump")
	got, _ := repo.GetByID(ctx, "00000000-0000-0000-0000-000000000001")
	bumped := got.UpdatedTime

	stale := bumped - 500
	ok, err := repo.MarkViewed(ctx, "00000000-0000-0000-0000-000000000001", "u1", stale)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected MarkViewed to return true")
	}

	got, _ = repo.GetByID(ctx, "00000000-0000-0000-0000-000000000001")
	if got.LastViewedAt == nil {
		t.Fatal("expected last_viewed_at to be set")
	}
	if *got.LastViewedAt < bumped {
		t.Errorf("last_viewed_at=%d regressed below updated_time=%d", *got.LastViewedAt, bumped)
	}
	if *got.LastViewedAt != bumped {
		t.Errorf("last_viewed_at=%d, expected exactly updated_time=%d (the MAX branch)", *got.LastViewedAt, bumped)
	}
}

func TestSessionRepo_MarkViewed_RespectsSoftDelete(t *testing.T) {
	db := newTestDB(t)
	repo := NewSessionRepo(db)
	ctx := context.Background()

	_ = repo.Create(ctx, &Session{ID: "00000000-0000-0000-0000-000000000001", UserID: "u1"})
	_, _ = repo.SoftDelete(ctx, "00000000-0000-0000-0000-000000000001", "u1")

	ok, err := repo.MarkViewed(ctx, "00000000-0000-0000-0000-000000000001", "u1", 1700000000000)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Error("expected MarkViewed to skip soft-deleted sessions")
	}
}

func TestSessionRepo_Search_NoResults(t *testing.T) {
	db := newTestDB(t)
	repo := NewSessionRepo(db)
	ctx := context.Background()

	name := "Test Chat"
	_ = repo.Create(ctx, &Session{ID: "00000000-0000-0000-0000-000000000001", UserID: "u1", Name: &name})

	sessions, total, err := repo.Search(ctx, "u1", "nonexistent", SessionSearchFilter{}, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 0 {
		t.Errorf("total = %d, want 0", total)
	}
	if len(sessions) != 0 {
		t.Errorf("len = %d, want 0", len(sessions))
	}
}

func TestSessionRepo_Search_CaseInsensitive(t *testing.T) {
	db := newTestDB(t)
	repo := NewSessionRepo(db)
	ctx := context.Background()

	name := "Weather Report"
	_ = repo.Create(ctx, &Session{ID: "00000000-0000-0000-0000-000000000001", UserID: "u1", Name: &name})

	for _, query := range []string{"weather", "WEATHER", "wEaThEr", "Weather", "report"} {
		sessions, total, err := repo.Search(ctx, "u1", query, SessionSearchFilter{}, 10, 0)
		if err != nil {
			t.Fatalf("Search(%q): %v", query, err)
		}
		if total != 1 || len(sessions) != 1 {
			t.Errorf("Search(%q): total = %d len = %d, want 1 and 1", query, total, len(sessions))
		}
	}
}

func TestSessionRepo_Search_EscapesLikeWildcards(t *testing.T) {
	db := newTestDB(t)
	repo := NewSessionRepo(db)
	ctx := context.Background()

	plain := "Q3 Report"
	percent := "50% off deal"
	underscore := "snake_case naming"
	backslash := `back\slash chat`
	_ = repo.Create(ctx, &Session{ID: "00000000-0000-0000-0000-000000000001", UserID: "u1", Name: &plain})
	_ = repo.Create(ctx, &Session{ID: "00000000-0000-0000-0000-000000000002", UserID: "u1", Name: &percent})
	_ = repo.Create(ctx, &Session{ID: "00000000-0000-0000-0000-000000000003", UserID: "u1", Name: &underscore})
	_ = repo.Create(ctx, &Session{ID: "00000000-0000-0000-0000-000000000004", UserID: "u1", Name: &backslash})

	tests := []struct {
		query string
		want  string
	}{
		{"%", percent},
		{"_", underscore},
		{`\`, backslash},
	}
	for _, tt := range tests {
		sessions, total, err := repo.Search(ctx, "u1", tt.query, SessionSearchFilter{}, 10, 0)
		if err != nil {
			t.Errorf("Search(%q): %v", tt.query, err)
			continue
		}
		if total != 1 || len(sessions) != 1 {
			t.Errorf("Search(%q): total = %d len = %d, want 1 and 1", tt.query, total, len(sessions))
			continue
		}
		if sessions[0].Name == nil || *sessions[0].Name != tt.want {
			t.Errorf("Search(%q): name = %v, want %q", tt.query, sessions[0].Name, tt.want)
		}
	}
}

func TestSessionRepo_UnassignedProjectFilter(t *testing.T) {
	db := newTestDB(t)
	repo := NewSessionRepo(db)
	ctx := context.Background()

	const (
		unassigned = "00000000-0000-0000-0000-000000000001"
		assigned   = "00000000-0000-0000-0000-000000000002"
		projectID  = "30000000-0000-0000-0000-000000000001"
	)
	name1 := "Weather Unassigned"
	name2 := "Weather Assigned"
	_ = repo.Create(ctx, &Session{ID: unassigned, UserID: "u1", Name: &name1})
	_ = repo.Create(ctx, &Session{ID: assigned, UserID: "u1", Name: &name2})
	if err := repo.AssignProject(ctx, assigned, projectID); err != nil {
		t.Fatal(err)
	}

	sessions, total, err := repo.Search(ctx, "u1", "weather", SessionSearchFilter{ProjectID: UnassignedProjectFilter}, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(sessions) != 1 {
		t.Fatalf("search unassigned: total = %d len = %d, want 1 and 1", total, len(sessions))
	}
	if sessions[0].ID != unassigned {
		t.Errorf("search unassigned returned %s, want %s", sessions[0].ID, unassigned)
	}

	listed, _, err := repo.ListByUser(ctx, "u1", false, false, false, UnassignedProjectFilter, nil, nil, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 || listed[0].ID != unassigned {
		t.Errorf("list unassigned returned %d rows, want only %s", len(listed), unassigned)
	}

	third := "00000000-0000-0000-0000-000000000003"
	name3 := "Weather Also Unassigned"
	_ = repo.Create(ctx, &Session{ID: third, UserID: "u1", Name: &name3})

	page, total, err := repo.Search(ctx, "u1", "weather", SessionSearchFilter{ProjectID: UnassignedProjectFilter}, 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 1 {
		t.Errorf("full page returned %d rows, want 1", len(page))
	}
	if total != 2 {
		t.Errorf("COUNT path total = %d, want 2 — page and count must share the same predicate", total)
	}
}

func TestSessionRepo_Search_AgentIDFilters(t *testing.T) {
	db := newTestDB(t)
	repo := NewSessionRepo(db)
	ctx := context.Background()

	const (
		alphaID = "00000000-0000-0000-0000-000000000001"
		betaID  = "00000000-0000-0000-0000-000000000002"
	)
	alphaName := "Weather Alpha"
	betaName := "Weather Beta"
	alphaAgent, betaAgent := "AlphaAgent", "BetaAgent"
	_ = repo.Create(ctx, &Session{ID: alphaID, UserID: "u1", Name: &alphaName, AgentID: &alphaAgent})
	_ = repo.Create(ctx, &Session{ID: betaID, UserID: "u1", Name: &betaName, AgentID: &betaAgent})

	t.Run("exclude drops only the named agent", func(t *testing.T) {
		got, total, err := repo.Search(ctx, "u1", "weather", SessionSearchFilter{ExcludeAgentIDs: []string{alphaAgent}}, 10, 0)
		if err != nil {
			t.Fatal(err)
		}
		if total != 1 || len(got) != 1 || got[0].ID != betaID {
			t.Fatalf("exclude %s returned %d rows (total %d), want only %s", alphaAgent, len(got), total, betaID)
		}
	})

	t.Run("include keeps only the named agent", func(t *testing.T) {
		got, total, err := repo.Search(ctx, "u1", "weather", SessionSearchFilter{IncludeAgentIDs: []string{alphaAgent}}, 10, 0)
		if err != nil {
			t.Fatal(err)
		}
		if total != 1 || len(got) != 1 || got[0].ID != alphaID {
			t.Fatalf("include %s returned %d rows (total %d), want only %s", alphaAgent, len(got), total, alphaID)
		}
	})

	t.Run("include overrides exclude", func(t *testing.T) {
		got, _, err := repo.Search(ctx, "u1", "weather", SessionSearchFilter{
			IncludeAgentIDs: []string{alphaAgent},
			ExcludeAgentIDs: []string{alphaAgent},
		}, 10, 0)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 1 || got[0].ID != alphaID {
			t.Fatalf("include+exclude of the same agent returned %d rows, want only %s", len(got), alphaID)
		}
	})
}

func TestSessionRepo_Search_FiltersByProject(t *testing.T) {
	db := newTestDB(t)
	repo := NewSessionRepo(db)
	ctx := context.Background()

	const (
		inProject = "00000000-0000-0000-0000-000000000001"
		noProject = "00000000-0000-0000-0000-000000000002"
		projectID = "30000000-0000-0000-0000-000000000001"
	)
	name1 := "Weather Chat"
	name2 := "Weather Report"
	_ = repo.Create(ctx, &Session{ID: inProject, UserID: "u1", Name: &name1})
	_ = repo.Create(ctx, &Session{ID: noProject, UserID: "u1", Name: &name2})
	if err := repo.AssignProject(ctx, inProject, projectID); err != nil {
		t.Fatal(err)
	}

	sessions, total, err := repo.Search(ctx, "u1", "weather", SessionSearchFilter{ProjectID: projectID}, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(sessions) != 1 {
		t.Fatalf("scoped search: total = %d len = %d, want 1 and 1", total, len(sessions))
	}
	if sessions[0].ID != inProject {
		t.Errorf("scoped search returned %s, want %s", sessions[0].ID, inProject)
	}

	_, total, err = repo.Search(ctx, "u1", "weather", SessionSearchFilter{}, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 {
		t.Errorf("unscoped search: total = %d, want 2", total)
	}
}

func TestSessionRepo_NamesByIDs(t *testing.T) {
	db := newTestDB(t)
	repo := NewSessionRepo(db)
	ctx := context.Background()

	named, unnamed, deleted := "00000000-0000-0000-0000-00000000a001", "00000000-0000-0000-0000-00000000a002", "00000000-0000-0000-0000-00000000a003"
	namedTitle, deletedTitle := "Weather in Ottawa", "Gone"
	for _, s := range []*Session{
		{ID: named, UserID: "u1", Name: &namedTitle},
		{ID: unnamed, UserID: "u1"},
		{ID: deleted, UserID: "u1", Name: &deletedTitle},
	} {
		if err := repo.Create(ctx, s); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := repo.SoftDelete(ctx, deleted, "u1"); err != nil {
		t.Fatal(err)
	}

	names, err := repo.NamesByIDs(ctx, []string{named, unnamed, deleted, "00000000-0000-0000-0000-00000000a004"})
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 1 || names[named] != namedTitle {
		t.Errorf("NamesByIDs = %v, want only %s=%q", names, named, namedTitle)
	}

	empty, err := repo.NamesByIDs(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(empty) != 0 {
		t.Errorf("NamesByIDs(nil) = %v, want empty", empty)
	}
}

func TestSessionRepo_SetTitleIfUnset(t *testing.T) {
	db := newTestDB(t)
	repo := NewSessionRepo(db)
	ctx := t.Context()
	named := func(name string) *string { return &name }
	sessions := []struct {
		id        string
		name      *string
		wantSaved bool
		wantName  string
	}{
		{"00000000-0000-0000-0000-000000000001", nil, true, "Generated"},
		{"00000000-0000-0000-0000-000000000002", named("  "), true, "Generated"},
		{"00000000-0000-0000-0000-000000000003", named("New Chat"), true, "Generated"},
		{"00000000-0000-0000-0000-000000000004", named("Chosen by hand"), false, "Chosen by hand"},
	}
	for _, s := range sessions {
		if err := repo.Create(ctx, &Session{ID: s.id, UserID: "u1", Name: s.name}); err != nil {
			t.Fatal(err)
		}
	}

	for _, s := range sessions {
		saved, err := repo.SetTitleIfUnset(ctx, s.id, "Generated", "New Chat")
		if err != nil {
			t.Fatalf("%s: %v", s.id, err)
		}
		if saved != s.wantSaved {
			t.Errorf("%s: saved = %v, want %v", s.id, saved, s.wantSaved)
		}
		got, _ := repo.GetByID(ctx, s.id)
		if got.Name == nil || *got.Name != s.wantName {
			t.Errorf("%s: name = %v, want %q", s.id, got.Name, s.wantName)
		}
	}

	if saved, err := repo.SetTitleIfUnset(ctx, "ffffffff-ffff-ffff-ffff-ffffffffffff", "Generated", "New Chat"); err != nil || saved {
		t.Errorf("missing session: saved = %v, err = %v, want false, nil", saved, err)
	}
}

func TestSessionRepo_HideEmptySession(t *testing.T) {
	tests := map[string]struct {
		withChat   bool
		caller     string
		hideTwice  bool
		wantHidden bool
		wantListed bool
		wantKey    bool
		wantLink   bool
	}{
		"an empty session is hidden and loses its mapping and file links": {false, "alice", false, true, false, false, false},
		"a session holding a chat is left alone":                          {true, "alice", false, false, true, true, true},
		"another user's session is left alone":                            {false, "bob", false, false, true, true, true},
		"hiding twice reports nothing hidden the second time":             {false, "alice", true, false, false, false, false},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			db := newTestDB(t)
			ctx := t.Context()
			keys := NewGatewaySessionKeyRepo(db)
			repo := NewSessionRepo(db)
			source := sessionSourceScheduler
			id, err := keys.GetOrCreateByExternalKey(ctx, GetOrCreateByExternalKeyArgs{
				GatewayType: GatewayTypeScheduled, ExternalKey: "run-1", UserID: "alice", Source: &source,
			})
			if err != nil {
				t.Fatalf("GetOrCreateByExternalKey: %v", err)
			}
			if _, seedErr := db.ExecContext(ctx,
				`INSERT INTO mcp_artifact_links (mcp_session_id, uri, agent_name, user_id, session_id, filename, created_at) VALUES (?, ?, ?, ?, ?, ?, 1)`,
				"mcp-1", "artifact://report.txt", "agent-1", "alice", id, "report.txt",
			); seedErr != nil {
				t.Fatalf("seed MCP artifact link: %v", seedErr)
			}
			if tc.withChat {
				if _, seedErr := db.ExecContext(ctx,
					`INSERT INTO chat_tasks (id, session_id, user_id, message_bubbles, created_time) VALUES (?, ?, ?, '[]', 1)`,
					"20000000-0000-0000-0000-000000000001", id, "alice",
				); seedErr != nil {
					t.Fatalf("seed chat task: %v", seedErr)
				}
			}

			hidden, err := repo.HideEmptySession(ctx, id, tc.caller)
			if err != nil {
				t.Fatalf("HideEmptySession(%s, %s): %v", id, tc.caller, err)
			}
			if tc.hideTwice {
				if hidden, err = repo.HideEmptySession(ctx, id, tc.caller); err != nil {
					t.Fatalf("second HideEmptySession(%s, %s): %v", id, tc.caller, err)
				}
			}

			if hidden != tc.wantHidden {
				t.Errorf("hidden = %v, want %v", hidden, tc.wantHidden)
			}
			listed, _, err := repo.ListByUserPage(ctx, "alice", SessionListFilter{NonEmptyOnly: true, ExcludeExternalGateways: true}, 10, 0)
			if err != nil {
				t.Fatalf("ListByUserPage(alice): %v", err)
			}
			if got := len(listed) == 1; got != tc.wantListed {
				t.Errorf("listed for the owner = %v, want %v", got, tc.wantListed)
			}
			mapped, err := keys.GetByGatewayAndExternalKeys(ctx, GatewayTypeScheduled, []string{"run-1"})
			if err != nil {
				t.Fatalf("GetByGatewayAndExternalKeys(run-1): %v", err)
			}
			if got := mapped["run-1"] == id; got != tc.wantKey {
				t.Errorf("mapping kept = %v, want %v", got, tc.wantKey)
			}
			var links int
			if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM mcp_artifact_links WHERE session_id = ?`, id).Scan(&links); err != nil {
				t.Fatalf("count MCP artifact links: %v", err)
			}
			if got := links == 1; got != tc.wantLink {
				t.Errorf("MCP artifact links kept = %v, want %v", got, tc.wantLink)
			}
		})
	}
}

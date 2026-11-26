// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
)

func tid(slug string) string {
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte("scheduled-test:"+slug)).String() //nolint:forbidigo // deterministic test ID
}

func newTestScheduledTask(id, namespace, name string) *ScheduledTask {
	return &ScheduledTask{
		ID:                 id,
		Name:               name,
		Namespace:          namespace,
		CreatedBy:          "user1",
		ScheduleType:       "cron",
		ScheduleExpression: "0 9 * * *",
		Timezone:           "UTC",
		TargetAgentName:    "test-agent",
		TargetType:         "agent",
		TaskMessage:        `[{"type":"text","text":"hello"}]`,
		Enabled:            true,
		MaxRetries:         3,
		RetryDelaySeconds:  60,
		TimeoutSeconds:     3600,
		Source:             "ui",
	}
}

func TestScheduledTaskRepo_CreateAndGetByID(t *testing.T) {
	db := newTestDB(t)
	repo := NewScheduledTaskRepo(db)
	ctx := t.Context()

	task := newTestScheduledTask(tid("st-001"), "ns1", "daily-report")
	desc := "A daily report task"
	task.Description = &desc

	if err := repo.Create(ctx, task); err != nil {
		t.Fatal(err)
	}

	got, err := repo.GetByID(ctx, tid("st-001"))
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("expected task")
	}
	if got.Name != "daily-report" {
		t.Errorf("name = %s, want daily-report", got.Name)
	}
	if got.Description == nil || *got.Description != "A daily report task" {
		t.Errorf("description = %v, want 'A daily report task'", got.Description)
	}
	if got.ScheduleType != "cron" {
		t.Errorf("schedule_type = %s, want cron", got.ScheduleType)
	}
	if !got.Enabled {
		t.Error("expected enabled = true")
	}
	if got.CreatedAt == 0 {
		t.Error("expected created_at to be set")
	}
	if got.UpdatedAt == 0 {
		t.Error("expected updated_at to be set")
	}
}

func TestScheduledTaskRepo_GetByID_NotFound(t *testing.T) {
	db := newTestDB(t)
	repo := NewScheduledTaskRepo(db)

	got, err := repo.GetByID(t.Context(), tid("nonexistent"))
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Error("expected nil")
	}
}

func TestScheduledTaskRepo_Update(t *testing.T) {
	db := newTestDB(t)
	repo := NewScheduledTaskRepo(db)
	ctx := t.Context()

	task := newTestScheduledTask(tid("st-002"), "ns1", "hourly-check")
	_ = repo.Create(ctx, task)

	err := repo.Update(ctx, tid("st-002"), map[string]any{
		"name":                "renamed-check",
		"schedule_expression": "*/30 * * * *",
	})
	if err != nil {
		t.Fatal(err)
	}

	got, _ := repo.GetByID(ctx, tid("st-002"))
	if got.Name != "renamed-check" {
		t.Errorf("name = %s, want renamed-check", got.Name)
	}
	if got.ScheduleExpression != "*/30 * * * *" {
		t.Errorf("expression = %s, want */30 * * * *", got.ScheduleExpression)
	}
	if got.UpdatedAt <= got.CreatedAt {
		t.Error("expected updated_at > created_at after update")
	}
}

func TestScheduledTaskRepo_Update_NotFound(t *testing.T) {
	db := newTestDB(t)
	repo := NewScheduledTaskRepo(db)

	err := repo.Update(t.Context(), tid("nonexistent"), map[string]any{"name": "x"})
	if err != sql.ErrNoRows {
		t.Errorf("expected sql.ErrNoRows, got %v", err)
	}
}

func TestScheduledTaskRepo_Update_EmptyFields(t *testing.T) {
	db := newTestDB(t)
	repo := NewScheduledTaskRepo(db)

	err := repo.Update(t.Context(), tid("any-id"), map[string]any{})
	if err != nil {
		t.Errorf("expected nil for empty fields, got %v", err)
	}
}

func TestScheduledTaskRepo_SoftDelete(t *testing.T) {
	db := newTestDB(t)
	repo := NewScheduledTaskRepo(db)
	ctx := t.Context()

	task := newTestScheduledTask(tid("st-003"), "ns1", "to-delete")
	_ = repo.Create(ctx, task)

	if err := repo.SoftDelete(ctx, tid("st-003"), "admin"); err != nil {
		t.Fatal(err)
	}

	got, _ := repo.GetByID(ctx, tid("st-003"))
	if got.DeletedAt == nil {
		t.Error("expected deleted_at to be set")
	}
	if got.DeletedBy == nil || *got.DeletedBy != "admin" {
		t.Errorf("deleted_by = %v, want 'admin'", got.DeletedBy)
	}
}

func TestScheduledTaskRepo_SoftDelete_AlreadyDeleted(t *testing.T) {
	db := newTestDB(t)
	repo := NewScheduledTaskRepo(db)
	ctx := t.Context()

	task := newTestScheduledTask(tid("st-004"), "ns1", "double-delete")
	_ = repo.Create(ctx, task)
	_ = repo.SoftDelete(ctx, tid("st-004"), "admin")

	err := repo.SoftDelete(ctx, tid("st-004"), "admin")
	if err != sql.ErrNoRows {
		t.Errorf("expected sql.ErrNoRows for already-deleted task, got %v", err)
	}
}

func TestScheduledTaskRepo_SetEnabled(t *testing.T) {
	db := newTestDB(t)
	repo := NewScheduledTaskRepo(db)
	ctx := t.Context()

	task := newTestScheduledTask(tid("st-005"), "ns1", "toggle-me")
	_ = repo.Create(ctx, task)

	if err := repo.SetEnabled(ctx, tid("st-005"), false); err != nil {
		t.Fatal(err)
	}
	got, _ := repo.GetByID(ctx, tid("st-005"))
	if got.Enabled {
		t.Error("expected enabled = false")
	}

	if err := repo.SetEnabled(ctx, tid("st-005"), true); err != nil {
		t.Fatal(err)
	}
	got, _ = repo.GetByID(ctx, tid("st-005"))
	if !got.Enabled {
		t.Error("expected enabled = true")
	}
}

func TestScheduledTaskRepo_FindByNamespace(t *testing.T) {
	db := newTestDB(t)
	repo := NewScheduledTaskRepo(db)
	ctx := t.Context()

	for i := range 5 {
		task := newTestScheduledTask(tid(fmt.Sprintf("st-ns-%d", i)), "ns1", fmt.Sprintf("task-%d", i))
		if i == 4 {
			task.Enabled = false
		}
		_ = repo.Create(ctx, task)
	}
	other := newTestScheduledTask(tid("st-other"), "ns2", "other-task")
	_ = repo.Create(ctx, other)

	tasks, err := repo.FindByNamespace(ctx, "ns1", nil, false, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 5 {
		t.Errorf("len = %d, want 5", len(tasks))
	}

	tasks, err = repo.FindByNamespace(ctx, "ns1", nil, true, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 4 {
		t.Errorf("enabled only: len = %d, want 4", len(tasks))
	}

	tasks, err = repo.FindByNamespace(ctx, "ns1", nil, false, 2, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 2 {
		t.Errorf("paginated: len = %d, want 2", len(tasks))
	}
}

func TestScheduledTaskRepo_FindByNamespace_ExcludesDeleted(t *testing.T) {
	db := newTestDB(t)
	repo := NewScheduledTaskRepo(db)
	ctx := t.Context()

	_ = repo.Create(ctx, newTestScheduledTask(tid("st-d1"), "ns1", "alive"))
	_ = repo.Create(ctx, newTestScheduledTask(tid("st-d2"), "ns1", "dead"))
	_ = repo.SoftDelete(ctx, tid("st-d2"), "admin")

	tasks, err := repo.FindByNamespace(ctx, "ns1", nil, false, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 {
		t.Errorf("len = %d, want 1", len(tasks))
	}
	if tasks[0].ID != tid("st-d1") {
		t.Errorf("expected st-d1, got %s", tasks[0].ID)
	}
}

func TestScheduledTaskRepo_FindByNamespace_UserFilter(t *testing.T) {
	db := newTestDB(t)
	repo := NewScheduledTaskRepo(db)
	ctx := t.Context()

	u1 := "user-a"
	u2 := "user-b"
	t1 := newTestScheduledTask(tid("st-u1"), "ns1", "user-a-task")
	t1.UserID = &u1
	t2 := newTestScheduledTask(tid("st-u2"), "ns1", "user-b-task")
	t2.UserID = &u2
	_ = repo.Create(ctx, t1)
	_ = repo.Create(ctx, t2)

	tasks, err := repo.FindByNamespace(ctx, "ns1", &u1, false, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 {
		t.Errorf("len = %d, want 1", len(tasks))
	}
	if tasks[0].ID != tid("st-u1") {
		t.Errorf("expected st-u1, got %s", tasks[0].ID)
	}
}

func TestScheduledTaskRepo_CountByNamespace(t *testing.T) {
	db := newTestDB(t)
	repo := NewScheduledTaskRepo(db)
	ctx := t.Context()

	for i := range 3 {
		task := newTestScheduledTask(tid(fmt.Sprintf("st-cnt-%d", i)), "ns1", fmt.Sprintf("cnt-task-%d", i))
		_ = repo.Create(ctx, task)
	}

	count, err := repo.CountByNamespace(ctx, "ns1", nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if count != 3 {
		t.Errorf("count = %d, want 3", count)
	}
}

func TestScheduledTaskRepo_FindEnabledByNamespace(t *testing.T) {
	db := newTestDB(t)
	repo := NewScheduledTaskRepo(db)
	ctx := t.Context()

	enabled := newTestScheduledTask(tid("st-en1"), "ns1", "enabled-task")
	disabled := newTestScheduledTask(tid("st-en2"), "ns1", "disabled-task")
	disabled.Enabled = false
	deleted := newTestScheduledTask(tid("st-en3"), "ns1", "deleted-task")

	_ = repo.Create(ctx, enabled)
	_ = repo.Create(ctx, disabled)
	_ = repo.Create(ctx, deleted)
	_ = repo.SoftDelete(ctx, tid("st-en3"), "admin")

	tasks, err := repo.FindEnabledByNamespace(ctx, "ns1")
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 {
		t.Errorf("len = %d, want 1", len(tasks))
	}
	if tasks[0].ID != tid("st-en1") {
		t.Errorf("expected st-en1, got %s", tasks[0].ID)
	}
}

func TestScheduledTaskRepo_UniqueNameConstraint(t *testing.T) {
	db := newTestDB(t)
	repo := NewScheduledTaskRepo(db)
	ctx := t.Context()

	t1 := newTestScheduledTask(tid("st-uc1"), "ns1", "unique-name")
	t2 := newTestScheduledTask(tid("st-uc2"), "ns1", "unique-name")

	if err := repo.Create(ctx, t1); err != nil {
		t.Fatal(err)
	}
	err := repo.Create(ctx, t2)
	if err == nil {
		t.Fatal("expected unique constraint violation")
	}

	_ = repo.SoftDelete(ctx, tid("st-uc1"), "admin")
	t3 := newTestScheduledTask(tid("st-uc3"), "ns1", "unique-name")
	if err := repo.Create(ctx, t3); err != nil {
		t.Fatalf("expected create after soft-delete to succeed: %v", err)
	}
}

func TestScheduledTaskRepo_UniqueNameIsPerOwner(t *testing.T) {
	db := newTestDB(t)
	repo := NewScheduledTaskRepo(db)
	ctx := t.Context()

	user1, user2 := "user1", "user2"

	first := newTestScheduledTask(tid("st-own1"), "ns1", "ABC")
	first.UserID = &user1
	if err := repo.Create(ctx, first); err != nil {
		t.Fatalf("first owner create: %v", err)
	}

	second := newTestScheduledTask(tid("st-own2"), "ns1", "ABC")
	second.UserID = &user2
	if err := repo.Create(ctx, second); err != nil {
		t.Fatalf("a second owner must be able to use the same name: %v", err)
	}

	sameOwnerAgain := newTestScheduledTask(tid("st-own3"), "ns1", "ABC")
	sameOwnerAgain.UserID = &user1
	if err := repo.Create(ctx, sameOwnerAgain); err == nil {
		t.Fatal("an owner reusing their own name must still violate the index")
	}
}

func TestScheduledTaskRepo_NamespaceLevelNamesStayUnique(t *testing.T) {
	db := newTestDB(t)
	repo := NewScheduledTaskRepo(db)
	ctx := t.Context()

	shared := newTestScheduledTask(tid("st-nsl1"), "ns1", "shared-report")
	if err := repo.Create(ctx, shared); err != nil {
		t.Fatalf("namespace-level create: %v", err)
	}

	owner := "user1"
	owned := newTestScheduledTask(tid("st-nsl2"), "ns1", "shared-report")
	owned.UserID = &owner
	if err := repo.Create(ctx, owned); err != nil {
		t.Fatalf("a user may shadow a namespace-level name: %v", err)
	}

	secondShared := newTestScheduledTask(tid("st-nsl3"), "ns1", "shared-report")
	if err := repo.Create(ctx, secondShared); err == nil {
		t.Fatal("two namespace-level tasks must not share a name")
	}
}

func TestScheduledTaskRepo_CreateAndGetExecution(t *testing.T) {
	db := newTestDB(t)
	repo := NewScheduledTaskRepo(db)
	ctx := t.Context()

	task := newTestScheduledTask(tid("st-exec-1"), "ns1", tid("exec-parent"))
	_ = repo.Create(ctx, task)

	a2aID := tid("a2a-task-123")
	exec := &ScheduledTaskExecution{
		ID:              tid("exec-001"),
		ScheduledTaskID: tid("st-exec-1"),
		Status:          "pending",
		A2ATaskID:       &a2aID,
		ScheduledFor:    time.Now().UnixMilli(),
		RetryCount:      0,
	}
	if err := repo.CreateExecution(ctx, exec); err != nil {
		t.Fatal(err)
	}

	got, err := repo.GetExecutionByA2ATaskID(ctx, tid("a2a-task-123"))
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("expected execution")
	}
	if got.ID != tid("exec-001") {
		t.Errorf("id = %s, want exec-001", got.ID)
	}
	if got.Status != "pending" {
		t.Errorf("status = %s, want pending", got.Status)
	}
}

func TestScheduledTaskRepo_GetExecutionByA2ATaskID_NotFound(t *testing.T) {
	db := newTestDB(t)
	repo := NewScheduledTaskRepo(db)

	got, err := repo.GetExecutionByA2ATaskID(t.Context(), tid("nonexistent"))
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Error("expected nil")
	}
}

func TestScheduledTaskRepo_UpdateExecution(t *testing.T) {
	db := newTestDB(t)
	repo := NewScheduledTaskRepo(db)
	ctx := t.Context()

	task := newTestScheduledTask(tid("st-uexec"), "ns1", "update-exec-parent")
	_ = repo.Create(ctx, task)

	now := time.Now().UnixMilli()
	exec := &ScheduledTaskExecution{
		ID:              tid("exec-upd-1"),
		ScheduledTaskID: tid("st-uexec"),
		Status:          "running",
		ScheduledFor:    now,
	}
	_ = repo.CreateExecution(ctx, exec)

	completedAt := time.Now().UnixMilli()
	summary := `{"agent_response":"done"}`
	err := repo.UpdateExecution(ctx, tid("exec-upd-1"), map[string]any{
		"status":         "completed",
		"completed_at":   completedAt,
		"result_summary": summary,
	})
	if err != nil {
		t.Fatal(err)
	}

	a2aID := tid("find-by-a2a")
	_ = repo.UpdateExecution(ctx, tid("exec-upd-1"), map[string]any{"a2a_task_id": a2aID})
	got, _ := repo.GetExecutionByA2ATaskID(ctx, tid("find-by-a2a"))
	if got == nil {
		t.Fatal("expected execution after update")
	}
	if got.Status != "completed" {
		t.Errorf("status = %s, want completed", got.Status)
	}
	if got.ResultSummary == nil || *got.ResultSummary != summary {
		t.Errorf("result_summary = %v, want %s", got.ResultSummary, summary)
	}
}

func TestScheduledTaskRepo_FindExecutionsByTask(t *testing.T) {
	db := newTestDB(t)
	repo := NewScheduledTaskRepo(db)
	ctx := t.Context()

	task := newTestScheduledTask(tid("st-fexec"), "ns1", "find-exec-parent")
	_ = repo.Create(ctx, task)

	baseTime := time.Now().UnixMilli()
	for i := range 5 {
		exec := &ScheduledTaskExecution{
			ID:              tid(fmt.Sprintf("exec-f-%d", i)),
			ScheduledTaskID: tid("st-fexec"),
			Status:          "completed",
			ScheduledFor:    baseTime + int64(i*1000),
		}
		_ = repo.CreateExecution(ctx, exec)
	}

	execs, err := repo.FindExecutionsByTask(ctx, tid("st-fexec"), 10, 0, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(execs) != 5 {
		t.Errorf("len = %d, want 5", len(execs))
	}
	if execs[0].ID != tid("exec-f-4") {
		t.Errorf("first = %s, want exec-f-4 (most recent)", execs[0].ID)
	}

	execs, err = repo.FindExecutionsByTask(ctx, tid("st-fexec"), 2, 0, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(execs) != 2 {
		t.Errorf("paginated: len = %d, want 2", len(execs))
	}
}

func TestScheduledTaskRepo_CountExecutionsByTask(t *testing.T) {
	db := newTestDB(t)
	repo := NewScheduledTaskRepo(db)
	ctx := t.Context()

	task := newTestScheduledTask(tid("st-cexec"), "ns1", "count-exec-parent")
	_ = repo.Create(ctx, task)

	for i := range 3 {
		exec := &ScheduledTaskExecution{
			ID:              tid(fmt.Sprintf("exec-c-%d", i)),
			ScheduledTaskID: tid("st-cexec"),
			Status:          "completed",
			ScheduledFor:    time.Now().UnixMilli(),
		}
		_ = repo.CreateExecution(ctx, exec)
	}

	count, err := repo.CountExecutionsByTask(ctx, tid("st-cexec"), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if count != 3 {
		t.Errorf("count = %d, want 3", count)
	}
}

func TestScheduledTaskRepo_FindRecentExecutions(t *testing.T) {
	db := newTestDB(t)
	repo := NewScheduledTaskRepo(db)
	ctx := t.Context()

	task1 := newTestScheduledTask(tid("st-recent-1"), "ns1", "recent-task-1")
	task2 := newTestScheduledTask(tid("st-recent-2"), "ns1", "recent-task-2")
	_ = repo.Create(ctx, task1)
	_ = repo.Create(ctx, task2)

	baseTime := time.Now().UnixMilli()
	_ = repo.CreateExecution(ctx, &ScheduledTaskExecution{
		ID: tid("exec-r-1"), ScheduledTaskID: tid("st-recent-1"), Status: "completed", ScheduledFor: baseTime,
	})
	_ = repo.CreateExecution(ctx, &ScheduledTaskExecution{
		ID: tid("exec-r-2"), ScheduledTaskID: tid("st-recent-2"), Status: "completed", ScheduledFor: baseTime + 1000,
	})

	notif := `[{"channel":"email"}]`
	user := "u-manual"
	_ = repo.UpdateExecution(ctx, tid("exec-r-2"), map[string]any{
		"notifications_sent": notif,
		"trigger_type":       TriggerTypeManual,
		"triggered_by":       user,
	})

	execs, err := repo.FindRecentExecutions(ctx, "ns1", nil, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(execs) != 2 {
		t.Errorf("len = %d, want 2", len(execs))
	}
	if execs[0].ID != tid("exec-r-2") {
		t.Errorf("first = %s, want exec-r-2", execs[0].ID)
	}
	if execs[0].NotificationsSent == nil || *execs[0].NotificationsSent != notif {
		t.Errorf("notifications_sent not round-tripped: got %v", execs[0].NotificationsSent)
	}
	if execs[0].TriggerType != TriggerTypeManual {
		t.Errorf("trigger_type = %q, want %q", execs[0].TriggerType, TriggerTypeManual)
	}
	if execs[0].TriggeredBy == nil || *execs[0].TriggeredBy != user {
		t.Errorf("triggered_by not round-tripped: got %v", execs[0].TriggeredBy)
	}
}

func TestScheduledTaskRepo_DeleteOldestExecutions(t *testing.T) {
	db := newTestDB(t)
	repo := NewScheduledTaskRepo(db)
	ctx := t.Context()

	task := newTestScheduledTask(tid("st-del-exec"), "ns1", "del-exec-parent")
	_ = repo.Create(ctx, task)

	baseTime := time.Now().UnixMilli()
	for i := range 5 {
		exec := &ScheduledTaskExecution{
			ID:              tid(fmt.Sprintf("exec-d-%d", i)),
			ScheduledTaskID: tid("st-del-exec"),
			Status:          "completed",
			ScheduledFor:    baseTime + int64(i*1000),
		}
		_ = repo.CreateExecution(ctx, exec)
	}

	deleted, err := repo.DeleteOldestExecutions(ctx, tid("st-del-exec"), 2)
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 3 {
		t.Errorf("deleted = %d, want 3", deleted)
	}

	remaining, _ := repo.FindExecutionsByTask(ctx, tid("st-del-exec"), 10, 0, nil, nil)
	if len(remaining) != 2 {
		t.Errorf("remaining = %d, want 2", len(remaining))
	}
	if remaining[0].ID != tid("exec-d-4") {
		t.Errorf("newest = %s, want exec-d-4", remaining[0].ID)
	}
	if remaining[1].ID != tid("exec-d-3") {
		t.Errorf("second = %s, want exec-d-3", remaining[1].ID)
	}
}

func TestScheduledTaskRepo_CreateExecution_DefaultsTriggerTypeScheduled(t *testing.T) {
	db := newTestDB(t)
	repo := NewScheduledTaskRepo(db)
	ctx := t.Context()

	task := newTestScheduledTask(tid("st-trig-def"), "ns1", "trigger-default")
	_ = repo.Create(ctx, task)

	_ = repo.CreateExecution(ctx, &ScheduledTaskExecution{
		ID:              tid("exec-trig-def"),
		ScheduledTaskID: tid("st-trig-def"),
		Status:          "pending",
		ScheduledFor:    time.Now().UnixMilli(),
	})

	got, err := repo.GetExecutionByID(ctx, tid("exec-trig-def"))
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("expected execution")
	}
	if got.TriggerType != TriggerTypeScheduled {
		t.Errorf("trigger_type = %q, want %q", got.TriggerType, TriggerTypeScheduled)
	}
	if got.TriggeredBy != nil {
		t.Errorf("triggered_by = %v, want nil for scheduled fire", got.TriggeredBy)
	}
}

func TestScheduledTaskRepo_CreateExecution_ManualTriggerRoundTrips(t *testing.T) {
	db := newTestDB(t)
	repo := NewScheduledTaskRepo(db)
	ctx := t.Context()

	task := newTestScheduledTask(tid("st-trig-man"), "ns1", "trigger-manual")
	_ = repo.Create(ctx, task)

	user := "u1"
	_ = repo.CreateExecution(ctx, &ScheduledTaskExecution{
		ID:              tid("exec-trig-man"),
		ScheduledTaskID: tid("st-trig-man"),
		Status:          "pending",
		ScheduledFor:    time.Now().UnixMilli(),
		TriggerType:     TriggerTypeManual,
		TriggeredBy:     &user,
	})

	got, _ := repo.GetExecutionByID(ctx, tid("exec-trig-man"))
	if got.TriggerType != TriggerTypeManual {
		t.Errorf("trigger_type = %q, want %q", got.TriggerType, TriggerTypeManual)
	}
	if got.TriggeredBy == nil || *got.TriggeredBy != "u1" {
		t.Errorf("triggered_by = %v, want 'u1'", got.TriggeredBy)
	}
}

func TestScheduledTaskRepo_FindTaskInfoForExecutions(t *testing.T) {
	db := newTestDB(t)
	repo := NewScheduledTaskRepo(db)
	ctx := t.Context()

	task1 := newTestScheduledTask(tid("st-info-1"), "ns1", "info-task-1")
	task2 := newTestScheduledTask(tid("st-info-2"), "ns1", "info-task-2")
	_ = repo.Create(ctx, task1)
	_ = repo.Create(ctx, task2)

	baseTime := time.Now().UnixMilli()
	_ = repo.CreateExecution(ctx, &ScheduledTaskExecution{
		ID: tid("exec-info-1"), ScheduledTaskID: tid("st-info-1"), Status: "completed", ScheduledFor: baseTime,
	})
	_ = repo.CreateExecution(ctx, &ScheduledTaskExecution{
		ID: tid("exec-info-2"), ScheduledTaskID: tid("st-info-2"), Status: "completed", ScheduledFor: baseTime,
	})

	rows, err := repo.FindTaskInfoForExecutions(ctx, []string{tid("exec-info-1"), tid("exec-info-2"), tid("exec-missing")})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("len = %d, want 2 (missing id is silently skipped)", len(rows))
	}
	byExec := make(map[string]SchedulerSessionTaskInfo, len(rows))
	for _, r := range rows {
		byExec[r.ExecutionID] = r
	}
	if byExec[tid("exec-info-1")].ScheduledTaskID != tid("st-info-1") || byExec[tid("exec-info-1")].TaskName != "info-task-1" {
		t.Errorf("exec-info-1 → %+v", byExec[tid("exec-info-1")])
	}
	if byExec[tid("exec-info-2")].ScheduledTaskID != tid("st-info-2") || byExec[tid("exec-info-2")].TaskName != "info-task-2" {
		t.Errorf("exec-info-2 → %+v", byExec[tid("exec-info-2")])
	}
}

func TestScheduledTaskRepo_FindTaskInfoForExecutions_Empty(t *testing.T) {
	db := newTestDB(t)
	repo := NewScheduledTaskRepo(db)

	rows, err := repo.FindTaskInfoForExecutions(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Errorf("len = %d, want 0", len(rows))
	}
}

func TestScheduledTaskRepo_FindLastExecutionsByTasks(t *testing.T) {
	db := newTestDB(t)
	repo := NewScheduledTaskRepo(db)
	ctx := t.Context()

	t1 := newTestScheduledTask(tid("t-last-1"), "ns1", "last-parent-1")
	t2 := newTestScheduledTask(tid("t-last-2"), "ns1", "last-parent-2")
	t3 := newTestScheduledTask(tid("t-last-3"), "ns1", "last-parent-3")
	_ = repo.Create(ctx, t1)
	_ = repo.Create(ctx, t2)
	_ = repo.Create(ctx, t3)

	_ = repo.CreateExecution(ctx, &ScheduledTaskExecution{ID: tid("e-t1-1"), ScheduledTaskID: tid("t-last-1"), Status: "completed", ScheduledFor: 100})
	_ = repo.CreateExecution(ctx, &ScheduledTaskExecution{ID: tid("e-t1-2"), ScheduledTaskID: tid("t-last-1"), Status: "failed", ScheduledFor: 200})
	_ = repo.CreateExecution(ctx, &ScheduledTaskExecution{ID: tid("e-t1-3"), ScheduledTaskID: tid("t-last-1"), Status: "completed", ScheduledFor: 300})
	_ = repo.CreateExecution(ctx, &ScheduledTaskExecution{ID: tid("e-t2-1"), ScheduledTaskID: tid("t-last-2"), Status: "completed", ScheduledFor: 150})

	result, err := repo.FindLastExecutionsByTasks(ctx, []string{tid("t-last-1"), tid("t-last-2"), tid("t-last-3")})
	if err != nil {
		t.Fatal(err)
	}
	if got := result[tid("t-last-1")]; got == nil || got.ID != tid("e-t1-3") {
		t.Errorf("t-last-1 latest = %v, want e-t1-3", got)
	}
	if got := result[tid("t-last-2")]; got == nil || got.ID != tid("e-t2-1") {
		t.Errorf("t-last-2 latest = %v, want e-t2-1", got)
	}
	if _, present := result[tid("t-last-3")]; present {
		t.Errorf("t-last-3 should be absent (no executions)")
	}
}

func TestScheduledTaskRepo_FindLastExecutionsByTasks_EmptyInput(t *testing.T) {
	db := newTestDB(t)
	repo := NewScheduledTaskRepo(db)

	result, err := repo.FindLastExecutionsByTasks(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(result) != 0 {
		t.Errorf("len = %d, want 0 for empty input", len(result))
	}
}

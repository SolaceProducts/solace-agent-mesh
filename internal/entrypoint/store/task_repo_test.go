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
	"sort"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

func TestTaskRepo_Search_TotalDerivation(t *testing.T) {
	db := newTestDB(t)
	repo := NewTaskRepo(db)
	ctx := context.Background()

	for i := range 5 {
		_ = repo.Create(ctx, &Task{
			ID:            fmt.Sprintf("20000000-0000-0000-0000-00000000000%d", i+1),
			UserID:        "u1",
			StartTime:     int64(1000 + i),
			ExecutionMode: "foreground",
		})
	}
	opts := TaskSearchOpts{}

	tasks, total, err := repo.Search(ctx, TaskVisibility{OwnerUserID: "u1"}, opts, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 5 || total != 5 {
		t.Errorf("short page: len=%d total=%d, want 5 and 5", len(tasks), total)
	}

	tasks, total, err = repo.Search(ctx, TaskVisibility{OwnerUserID: "u1"}, opts, 3, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 3 || total != 5 {
		t.Errorf("full page: len=%d total=%d, want 3 and 5", len(tasks), total)
	}

	tasks, total, err = repo.Search(ctx, TaskVisibility{OwnerUserID: "u1"}, opts, 3, 6)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 0 || total != 5 {
		t.Errorf("over-paged: len=%d total=%d, want 0 and 5", len(tasks), total)
	}
}

func TestTaskRepo_Search_UserAndSearchFilters(t *testing.T) {
	db := newTestDB(t)
	repo := NewTaskRepo(db)
	ctx := context.Background()

	sessA, sessB := "30000000-0000-0000-0000-00000000000a", "30000000-0000-0000-0000-00000000000b"
	rows := []*Task{
		{ID: "30000000-0000-0000-0000-000000000001", UserID: "alice@example.com", SessionID: &sessA, StartTime: 1000},
		{ID: "30000000-0000-0000-0000-000000000002", UserID: "alice@example.com", SessionID: &sessB, StartTime: 2000},
		{ID: "30000000-0000-0000-0000-000000000003", UserID: "bob@example.com", SessionID: &sessA, StartTime: 3000},
	}
	for _, r := range rows {
		r.ExecutionMode = "foreground"
		if err := repo.Create(ctx, r); err != nil {
			t.Fatal(err)
		}
	}

	ids := func(opts TaskSearchOpts) []string {
		t.Helper()
		tasks, _, err := repo.Search(ctx, TaskVisibility{Everything: true}, opts, 10, 0)
		if err != nil {
			t.Fatal(err)
		}
		out := make([]string, 0, len(tasks))
		for _, task := range tasks {
			out = append(out, task.ID[len(task.ID)-1:])
		}
		return out
	}

	if got := ids(TaskSearchOpts{UserID: "alice@example.com"}); fmt.Sprint(got) != "[2 1]" {
		t.Errorf("UserID filter = %v, want [2 1]", got)
	}
	if got := ids(TaskSearchOpts{Search: "30000000-0000-0000-0000-000000000003"}); fmt.Sprint(got) != "[3]" {
		t.Errorf("Search by task id = %v, want [3]", got)
	}
	if got := ids(TaskSearchOpts{SessionIDs: []string{sessA, sessB}}); fmt.Sprint(got) != "[3 2 1]" {
		t.Errorf("SessionIDs filter = %v, want [3 2 1]", got)
	}
	if got := ids(TaskSearchOpts{SessionIDs: []string{sessB}, UserID: "bob@example.com"}); len(got) != 0 {
		t.Errorf("SessionIDs and UserID combine = %v, want none", got)
	}
	if got := ids(TaskSearchOpts{Search: sessA}); fmt.Sprint(got) != "[3 1]" {
		t.Errorf("Search by session id = %v, want [3 1]", got)
	}
	if got := ids(TaskSearchOpts{Search: "Bob@Example.com"}); fmt.Sprint(got) != "[3]" {
		t.Errorf("Search by owner, case-insensitive = %v, want [3]", got)
	}
	if got := ids(TaskSearchOpts{Search: "alice"}); len(got) != 0 {
		t.Errorf("Search is an equality match, got %v for a prefix", got)
	}
	if got := ids(TaskSearchOpts{Search: "alice@example.com"}); fmt.Sprint(got) != "[2 1]" {
		t.Errorf("Search by owner = %v, want [2 1]", got)
	}
	if got := ids(TaskSearchOpts{UserID: "alice@example.com", Search: sessA}); fmt.Sprint(got) != "[1]" {
		t.Errorf("UserID and Search combine = %v, want [1]", got)
	}

	tasks, _, err := repo.Search(ctx, TaskVisibility{OwnerUserID: "bob@example.com"}, TaskSearchOpts{Search: sessA}, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 || tasks[0].UserID != "bob@example.com" {
		t.Errorf("visibility-bounded search returned %d rows (first owner %q), want bob's single row", len(tasks), firstOwner(tasks))
	}
}

func firstOwner(tasks []*Task) string {
	if len(tasks) == 0 {
		return ""
	}
	return tasks[0].UserID
}

func TestTaskRepo_CreateAndGetByID(t *testing.T) {
	db := newTestDB(t)
	repo := NewTaskRepo(db)
	ctx := context.Background()

	task := &Task{
		ID:            "10000000-0000-0000-0000-000000000001",
		UserID:        "u1",
		StartTime:     time.Now().UnixMilli(),
		ExecutionMode: "foreground",
	}
	if err := repo.Create(ctx, task); err != nil {
		t.Fatal(err)
	}

	got, err := repo.GetByID(ctx, "10000000-0000-0000-0000-000000000001")
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("expected task")
	}
	if got.UserID != "u1" {
		t.Errorf("user_id = %s, want u1", got.UserID)
	}
	if got.ExecutionMode != "foreground" {
		t.Errorf("execution_mode = %s, want foreground", got.ExecutionMode)
	}
	if got.EventsBuffered {
		t.Error("expected events_buffered = false")
	}
}

func TestTaskRepo_GetByID_NotFound(t *testing.T) {
	db := newTestDB(t)
	repo := NewTaskRepo(db)

	got, err := repo.GetByID(context.Background(), "ffffffff-ffff-ffff-ffff-ffffffffffff")
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Error("expected nil")
	}
}

func TestTaskRepo_UpdateStatus(t *testing.T) {
	db := newTestDB(t)
	repo := NewTaskRepo(db)
	ctx := context.Background()

	_ = repo.Create(ctx, &Task{ID: "10000000-0000-0000-0000-000000000001", UserID: "u1", StartTime: time.Now().UnixMilli(), ExecutionMode: "foreground"})

	if err := repo.UpdateStatus(ctx, "10000000-0000-0000-0000-000000000001", "completed"); err != nil {
		t.Fatal(err)
	}

	got, _ := repo.GetByID(ctx, "10000000-0000-0000-0000-000000000001")
	if got.Status == nil || *got.Status != "completed" {
		t.Errorf("status = %v, want 'completed'", got.Status)
	}
}

func TestTaskRepo_UpdateEndTime(t *testing.T) {
	db := newTestDB(t)
	repo := NewTaskRepo(db)
	ctx := context.Background()

	_ = repo.Create(ctx, &Task{ID: "10000000-0000-0000-0000-000000000001", UserID: "u1", StartTime: time.Now().UnixMilli(), ExecutionMode: "foreground"})

	endTime := time.Now().UnixMilli()
	if err := repo.UpdateEndTime(ctx, "10000000-0000-0000-0000-000000000001", endTime); err != nil {
		t.Fatal(err)
	}

	got, _ := repo.GetByID(ctx, "10000000-0000-0000-0000-000000000001")
	if got.EndTime == nil || *got.EndTime != endTime {
		t.Errorf("end_time = %v, want %d", got.EndTime, endTime)
	}
}

func TestTaskRepo_UpdateTokenUsage(t *testing.T) {
	db := newTestDB(t)
	repo := NewTaskRepo(db)
	ctx := context.Background()

	_ = repo.Create(ctx, &Task{ID: "10000000-0000-0000-0000-000000000001", UserID: "u1", StartTime: time.Now().UnixMilli(), ExecutionMode: "foreground"})

	if err := repo.UpdateTokenUsage(ctx, "10000000-0000-0000-0000-000000000001", 100, 200, 50); err != nil {
		t.Fatal(err)
	}

	got, _ := repo.GetByID(ctx, "10000000-0000-0000-0000-000000000001")
	if got.TotalInputTokens == nil || *got.TotalInputTokens != 100 {
		t.Errorf("input_tokens = %v, want 100", got.TotalInputTokens)
	}
	if got.TotalOutputTokens == nil || *got.TotalOutputTokens != 200 {
		t.Errorf("output_tokens = %v, want 200", got.TotalOutputTokens)
	}
}

func TestTaskRepo_FillTokenUsage_FillsOnlyARowWithoutUsage(t *testing.T) {
	db := newTestDB(t)
	repo := NewTaskRepo(db)
	ctx := t.Context()
	const id = "10000000-0000-0000-0000-000000000001"
	const endTime int64 = 1_700_000_000_000

	if err := repo.Create(ctx, &Task{ID: id, UserID: "u1", StartTime: time.Now().UnixMilli(), ExecutionMode: "foreground"}); err != nil {
		t.Fatal(err)
	}
	if err := repo.UpdateFinalStatus(ctx, id, FinalStatusUpdate{Status: "failed", EndTime: endTime}); err != nil {
		t.Fatal(err)
	}

	first := `{"total_input_tokens":300}`
	wrote, err := repo.FillTokenUsage(ctx, id, TokenUsageFill{InputTokens: 300, OutputTokens: 40, CachedInputTokens: 250, TokenUsageDetails: &first})
	if err != nil || !wrote {
		t.Fatalf("first FillTokenUsage = (%v, %v), want (true, nil)", wrote, err)
	}
	second := `{"total_input_tokens":999}`
	wrote, err = repo.FillTokenUsage(ctx, id, TokenUsageFill{InputTokens: 999, OutputTokens: 999, CachedInputTokens: 999, TokenUsageDetails: &second})
	if err != nil || wrote {
		t.Fatalf("second FillTokenUsage = (%v, %v), want (false, nil)", wrote, err)
	}

	got, err := repo.GetByID(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status == nil || *got.Status != "failed" {
		t.Errorf("status = %v, want failed", valueOf(got.Status))
	}
	if got.EndTime == nil || *got.EndTime != endTime {
		t.Errorf("end_time = %v, want %d", valueOf(got.EndTime), endTime)
	}
	if got.TotalInputTokens == nil || *got.TotalInputTokens != 300 {
		t.Errorf("input_tokens = %v, want 300 from the first fill", valueOf(got.TotalInputTokens))
	}
	if got.TotalOutputTokens == nil || *got.TotalOutputTokens != 40 {
		t.Errorf("output_tokens = %v, want 40", valueOf(got.TotalOutputTokens))
	}
	if got.TotalCachedInputTokens == nil || *got.TotalCachedInputTokens != 250 {
		t.Errorf("cached_input_tokens = %v, want 250", valueOf(got.TotalCachedInputTokens))
	}
	if got.TokenUsageDetails == nil || *got.TokenUsageDetails != first {
		t.Errorf("token_usage_details = %v, want %s", valueOf(got.TokenUsageDetails), first)
	}
}

func valueOf[T any](p *T) any {
	if p == nil {
		return nil
	}
	return *p
}

func TestTaskRepo_UpdateFinalStatus_PreservesTokensOnNullFollowUp(t *testing.T) {
	db := newTestDB(t)
	repo := NewTaskRepo(db)
	ctx := context.Background()
	const id = "10000000-0000-0000-0000-000000000099"

	_ = repo.Create(ctx, &Task{ID: id, UserID: "u1", StartTime: time.Now().UnixMilli(), ExecutionMode: "foreground"})

	in1, out1, cached1 := 5000, 250, 0
	details := `{"total_input_tokens":5000,"total_output_tokens":250}`
	if err := repo.UpdateFinalStatus(ctx, id, FinalStatusUpdate{
		Status:            "completed",
		EndTime:           time.Now().UnixMilli(),
		InputTokens:       &in1,
		OutputTokens:      &out1,
		CachedInputTokens: &cached1,
		TokenUsageDetails: &details,
	}); err != nil {
		t.Fatalf("first finalize: %v", err)
	}

	if err := repo.UpdateFinalStatus(ctx, id, FinalStatusUpdate{
		Status:  "completed",
		EndTime: time.Now().UnixMilli() + 1,
	}); err != nil {
		t.Fatalf("second finalize: %v", err)
	}

	got, _ := repo.GetByID(ctx, id)
	if got.TotalInputTokens == nil || *got.TotalInputTokens != 5000 {
		t.Errorf("total_input_tokens = %v, want 5000 (preserved across NULL follow-up)", got.TotalInputTokens)
	}
	if got.TotalOutputTokens == nil || *got.TotalOutputTokens != 250 {
		t.Errorf("total_output_tokens = %v, want 250 (preserved across NULL follow-up)", got.TotalOutputTokens)
	}
	if got.TokenUsageDetails == nil || *got.TokenUsageDetails != details {
		t.Errorf("token_usage_details = %v, want preserved JSON", got.TokenUsageDetails)
	}
}

func TestTaskRepo_ListByUser(t *testing.T) {
	db := newTestDB(t)
	repo := NewTaskRepo(db)
	ctx := context.Background()

	now := time.Now().UnixMilli()
	_ = repo.Create(ctx, &Task{ID: "10000000-0000-0000-0000-000000000001", UserID: "u1", StartTime: now, ExecutionMode: "foreground"})
	_ = repo.Create(ctx, &Task{ID: "10000000-0000-0000-0000-000000000002", UserID: "u1", StartTime: now + 1, ExecutionMode: "background"})
	_ = repo.Create(ctx, &Task{ID: "10000000-0000-0000-0000-000000000003", UserID: "u1", StartTime: now + 2, ExecutionMode: "foreground"})

	tasks, err := repo.ListByUser(ctx, "u1", "", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 3 {
		t.Errorf("expected 3 tasks, got %d", len(tasks))
	}

	tasks, err = repo.ListByUser(ctx, "u1", "foreground", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 2 {
		t.Errorf("expected 2 foreground tasks, got %d", len(tasks))
	}
}

func TestTaskRepo_MarkEventsBufferedAndConsumed(t *testing.T) {
	db := newTestDB(t)
	repo := NewTaskRepo(db)
	ctx := context.Background()

	_ = repo.Create(ctx, &Task{ID: "10000000-0000-0000-0000-000000000001", UserID: "u1", StartTime: time.Now().UnixMilli(), ExecutionMode: "foreground"})

	if err := repo.MarkEventsBuffered(ctx, "10000000-0000-0000-0000-000000000001"); err != nil {
		t.Fatal(err)
	}

	got, _ := repo.GetByID(ctx, "10000000-0000-0000-0000-000000000001")
	if !got.EventsBuffered {
		t.Error("expected events_buffered = true")
	}

	if err := repo.MarkEventsConsumed(ctx, "10000000-0000-0000-0000-000000000001"); err != nil {
		t.Fatal(err)
	}

	got, _ = repo.GetByID(ctx, "10000000-0000-0000-0000-000000000001")
	if !got.EventsConsumed {
		t.Error("expected events_consumed = true")
	}
}

func TestTaskRepo_MarkEventsBufferedBatch(t *testing.T) {
	db := newTestDB(t)
	repo := NewTaskRepo(db)
	ctx := context.Background()
	ids := []string{
		"10000000-0000-0000-0000-000000000001",
		"10000000-0000-0000-0000-000000000002",
		"10000000-0000-0000-0000-000000000003",
	}
	for _, id := range ids {
		if err := repo.Create(ctx, &Task{ID: id, UserID: "u1", StartTime: time.Now().UnixMilli(), ExecutionMode: "foreground"}); err != nil {
			t.Fatal(err)
		}
	}

	if err := repo.MarkEventsBufferedBatch(ctx, ids[:2]); err != nil {
		t.Fatal(err)
	}

	for i, id := range ids {
		got, err := repo.GetByID(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if want := i < 2; got.EventsBuffered != want {
			t.Errorf("task %s events_buffered = %v, want %v", id, got.EventsBuffered, want)
		}
	}
	if err := repo.MarkEventsBufferedBatch(ctx, nil); err != nil {
		t.Fatalf("MarkEventsBufferedBatch(nil): %v", err)
	}
}

func TestTaskRepo_MarkEventsBufferedBatch_MoreTasksThanOneStatementHolds(t *testing.T) {
	db := newTestDB(t)
	repo := NewTaskRepo(db)
	ctx := context.Background()
	ids := make([]string, dbutil.InClauseCap+2)
	for i := range ids {
		ids[i] = fmt.Sprintf("20000000-0000-0000-0000-%012d", i)
		if err := repo.Create(ctx, &Task{ID: ids[i], UserID: "u1", StartTime: time.Now().UnixMilli(), ExecutionMode: "foreground"}); err != nil {
			t.Fatal(err)
		}
	}

	if err := repo.MarkEventsBufferedBatch(ctx, ids); err != nil {
		t.Fatal(err)
	}

	got, err := dbutil.QueryVal[int64](ctx, db, `SELECT COUNT(*) FROM tasks WHERE events_buffered = 1`)
	if err != nil {
		t.Fatal(err)
	}
	if got != int64(len(ids)) {
		t.Fatalf("%d tasks marked events_buffered, want all %d", got, len(ids))
	}
}

func TestTaskRepo_HandlersForTasks(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	repo := NewTaskRepo(db)

	str := func(s string) *string { return &s }
	workflowTask, agentTask, agentDup, unclassified, unknown := idgen.New(), idgen.New(), idgen.New(), idgen.New(), idgen.New()
	create := func(id string, handler, kind *string) {
		if err := repo.Create(ctx, &Task{
			ID: id, UserID: "u1", StartTime: 1, ExecutionMode: "foreground", Kind: TaskKindNormal,
			HandlerName: handler, HandlerType: kind,
		}); err != nil {
			t.Fatalf("create %s: %v", id, err)
		}
	}
	create(workflowTask, str("workflow_01a0c9b5_94e6_743d_9896_a779500a1a6c"), str(TaskHandlerTypeWorkflow))
	create(agentTask, str("hr-bot"), str(TaskHandlerTypeAgent))
	create(agentDup, str("hr-bot"), str(TaskHandlerTypeAgent))
	create(unclassified, nil, nil)

	got, err := repo.HandlersForTasks(ctx, []string{workflowTask, agentTask, agentDup, unclassified, unknown})
	if err != nil {
		t.Fatalf("HandlersForTasks: %v", err)
	}
	want := map[TaskHandler]bool{
		{HandlerName: "workflow_01a0c9b5_94e6_743d_9896_a779500a1a6c", HandlerType: TaskHandlerTypeWorkflow}: true,
		{HandlerName: "hr-bot", HandlerType: TaskHandlerTypeAgent}:                                           true,
		{HandlerName: "", HandlerType: ""}:                                                                   true,
	}
	if len(got) != len(want) {
		t.Fatalf("got %d handlers %v, want %d", len(got), got, len(want))
	}
	for _, h := range got {
		if !want[h] {
			t.Errorf("unexpected handler %+v", h)
		}
	}
}

func TestTaskRepo_HandlersForTasks_EmptyInput(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	repo := NewTaskRepo(db)

	got, err := repo.HandlersForTasks(ctx, nil)
	if err != nil {
		t.Fatalf("HandlersForTasks(nil): %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %d handlers, want 0: %v", len(got), got)
	}
}

func TestTaskRepo_Search_Visibility(t *testing.T) {
	db := newTestDB(t)
	repo := NewTaskRepo(db)
	ctx := context.Background()
	str := func(s string) *string { return &s }

	create := func(id, owner string, parent *string, handler, kind string) {
		t.Helper()
		task := &Task{ID: id, UserID: owner, StartTime: 1000, ExecutionMode: "foreground", Kind: TaskKindNormal, ParentTaskID: parent}
		if handler != "" {
			task.HandlerName, task.HandlerType = str(handler), str(kind)
		}
		if err := repo.Create(ctx, task); err != nil {
			t.Fatal(err)
		}
	}
	const (
		own       = "30000000-0000-0000-0000-000000000001"
		rootA     = "30000000-0000-0000-0000-000000000002"
		rootB     = "30000000-0000-0000-0000-000000000003"
		childB    = "30000000-0000-0000-0000-000000000004"
		rootWf    = "30000000-0000-0000-0000-000000000005"
		rootNoKin = "30000000-0000-0000-0000-000000000006"
		unowned   = "30000000-0000-0000-0000-000000000009"
		unnamed   = "30000000-0000-0000-0000-00000000000a"
	)
	create(own, "me", nil, "agent-b", TaskHandlerTypeAgent)
	create(rootA, "them", nil, "agent-a", TaskHandlerTypeAgent)
	create(rootB, "system:default", nil, "agent-b", TaskHandlerTypeAgent)
	parent := rootA
	create(childB, "them", &parent, "agent-b", TaskHandlerTypeAgent)
	create(rootWf, "them", nil, "workflow_x", TaskHandlerTypeWorkflow)
	create(rootNoKin, "them", nil, "", "")
	create(unowned, "", nil, "workflow_y", TaskHandlerTypeWorkflow)
	if err := repo.Create(ctx, &Task{ID: unnamed, UserID: "them", StartTime: 1000, ExecutionMode: "foreground", Kind: TaskKindNormal, HandlerName: str(""), HandlerType: str(TaskHandlerTypeAgent)}); err != nil {
		t.Fatal(err)
	}
	const (
		compaction = "30000000-0000-0000-0000-000000000007"
		late       = "30000000-0000-0000-0000-000000000008"
	)
	if err := repo.Create(ctx, &Task{ID: compaction, UserID: "them", StartTime: 1000, ExecutionMode: "foreground", Kind: TaskKindCompactionCost, HandlerName: str("agent-a"), HandlerType: str(TaskHandlerTypeAgent)}); err != nil {
		t.Fatal(err)
	}
	if err := repo.Create(ctx, &Task{ID: late, UserID: "them", StartTime: 5000, ExecutionMode: "foreground", Kind: TaskKindNormal, HandlerName: str("agent-a"), HandlerType: str(TaskHandlerTypeAgent)}); err != nil {
		t.Fatal(err)
	}

	idsForOpts := func(vis TaskVisibility, opts TaskSearchOpts) map[string]bool {
		t.Helper()
		rows, total, err := repo.Search(ctx, vis, opts, 50, 0)
		if err != nil {
			t.Fatal(err)
		}
		if total != len(rows) {
			t.Errorf("total %d != rows %d", total, len(rows))
		}
		got := map[string]bool{}
		for _, r := range rows {
			got[r.ID] = true
		}
		return got
	}
	idsFor := func(vis TaskVisibility) map[string]bool { return idsForOpts(vis, TaskSearchOpts{}) }
	expectOpts := func(desc string, vis TaskVisibility, opts TaskSearchOpts, want ...string) {
		t.Helper()
		got := idsForOpts(vis, opts)
		if len(got) != len(want) {
			t.Errorf("%s: got %d rows, want %d: %v", desc, len(got), len(want), got)
		}
		for _, id := range want {
			if !got[id] {
				t.Errorf("%s: missing %s", desc, id)
			}
		}
	}
	expect := func(desc string, vis TaskVisibility, want ...string) {
		t.Helper()
		got := idsFor(vis)
		if len(got) != len(want) {
			t.Errorf("%s: got %d rows, want %d: %v", desc, len(got), len(want), got)
		}
		for _, id := range want {
			if !got[id] {
				t.Errorf("%s: missing %s", desc, id)
			}
		}
	}

	agentA := TaskVisibility{OwnerUserID: "me", AgentRootHandlers: []string{"agent-a"}}
	allAgents := TaskVisibility{OwnerUserID: "me", AllAgentRoots: true}
	normal := TaskSearchOpts{Kind: TaskKindNormal}
	window := TaskSearchOpts{StartDate: 900, EndDate: 1100}

	expect("zero value lists nothing", TaskVisibility{})
	expect("no owner but an agent arm lists the agent roots alone", TaskVisibility{AgentRootHandlers: []string{"agent-a"}}, rootA, compaction, late)
	expect("owner only", TaskVisibility{OwnerUserID: "me"}, own)
	expect("owner plus agent-a roots", agentA, own, rootA, compaction, late)
	expect("agent-b roots exclude the agent-b child", TaskVisibility{OwnerUserID: "me", AgentRootHandlers: []string{"agent-b"}}, own, rootB)
	expect("several handlers", TaskVisibility{OwnerUserID: "me", AgentRootHandlers: []string{"agent-a", "agent-b"}}, own, rootA, rootB, compaction, late)
	expect("all agent roots skip workflow, unclassified and unnamed roots", allAgents, own, rootA, rootB, compaction, late)
	expect("everything", TaskVisibility{Everything: true}, own, rootA, rootB, childB, rootWf, rootNoKin, unowned, unnamed, compaction, late)

	expectOpts("agent-a roots with the kind filter drop the compaction row", agentA, normal, own, rootA, late)
	expectOpts("all agent roots with the kind filter drop the compaction row", allAgents, normal, own, rootA, rootB, late)
	expectOpts("agent-a roots inside a date window drop the late row", agentA, window, own, rootA, compaction)
	expectOpts("everything with the kind filter drops the compaction row", TaskVisibility{Everything: true}, normal, own, rootA, rootB, childB, rootWf, rootNoKin, unowned, unnamed, late)
	expectOpts("everything with a non-normal kind keeps only the compaction row", TaskVisibility{Everything: true}, TaskSearchOpts{Kind: TaskKindCompactionCost}, compaction)

	rows, total, err := repo.Search(ctx, allAgents, normal, 2, 0)
	if err != nil || len(rows) != 2 || total != 4 {
		t.Errorf("full page under all agent roots: rows=%d total=%d err=%v, want 2 and 4", len(rows), total, err)
	}
}

func TestTaskRepo_Search_TooManyHandlersIsAnError(t *testing.T) {
	db := newTestDB(t)
	repo := NewTaskRepo(db)
	handlers := make([]string, dbutil.InClauseCap+1)
	for i := range handlers {
		handlers[i] = fmt.Sprintf("agent-%d", i)
	}
	_, _, err := repo.Search(context.Background(), TaskVisibility{OwnerUserID: "me", AgentRootHandlers: handlers}, TaskSearchOpts{}, 10, 0)
	if !errors.Is(err, dbutil.ErrTooManyValues) {
		t.Fatalf("err = %v, want %v", err, dbutil.ErrTooManyValues)
	}
}

func TestTaskRepo_GetAllDescendantIDs_EndsOnACycle(t *testing.T) {
	db := newTestDB(t)
	repo := NewTaskRepo(db)
	ctx := context.Background()
	const (
		a = "31000000-0000-0000-0000-000000000001"
		b = "31000000-0000-0000-0000-000000000002"
	)
	parentA, parentB := a, b
	for _, task := range []*Task{
		{ID: a, UserID: "me", StartTime: 1, ExecutionMode: "foreground", Kind: TaskKindNormal, ParentTaskID: &parentB},
		{ID: b, UserID: "me", StartTime: 2, ExecutionMode: "foreground", Kind: TaskKindNormal, ParentTaskID: &parentA},
	} {
		if err := repo.Create(ctx, task); err != nil {
			t.Fatal(err)
		}
	}
	ids, err := repo.GetAllDescendantIDs(ctx, a)
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(ids)
	if want := []string{a, b}; !slices.Equal(ids, want) {
		t.Errorf("descendants of a cycle = %v, want %v", ids, want)
	}
}

func TestTaskRepo_DescendantIDs_ReportsTheDepthBound(t *testing.T) {
	db := newTestDB(t)
	repo := NewTaskRepo(db)
	ctx := context.Background()
	chain := func(prefix string, levels int) string {
		t.Helper()
		id := func(i int) string { return fmt.Sprintf("%s-0000-0000-0000-%012d", prefix, i) }
		for i := 0; i <= levels; i++ {
			task := &Task{ID: id(i), UserID: "me", StartTime: int64(i + 1), ExecutionMode: "foreground", Kind: TaskKindNormal}
			if i > 0 {
				parent := id(i - 1)
				task.ParentTaskID = &parent
			}
			if err := repo.Create(ctx, task); err != nil {
				t.Fatal(err)
			}
		}
		return id(0)
	}

	within := chain("32000001", TaskTreeMaxDepth-1)
	ids, atBound, err := repo.DescendantIDs(ctx, within)
	if err != nil {
		t.Fatal(err)
	}
	if atBound || len(ids) != TaskTreeMaxDepth-1 {
		t.Errorf("chain within the bound: %d ids, atBound=%v; want %d, false", len(ids), atBound, TaskTreeMaxDepth-1)
	}

	beyond := chain("32000002", TaskTreeMaxDepth+1)
	ids, atBound, err = repo.DescendantIDs(ctx, beyond)
	if err != nil {
		t.Fatal(err)
	}
	if !atBound || len(ids) != TaskTreeMaxDepth {
		t.Errorf("chain past the bound: %d ids, atBound=%v; want %d, true", len(ids), atBound, TaskTreeMaxDepth)
	}
}

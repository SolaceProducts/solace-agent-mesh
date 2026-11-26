// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"context"
	"testing"
)

func i64Ptr(v int64) *int64 { return &v }
func intPtr(v int) *int     { return &v }

func makeTask(t *testing.T, repo *TaskRepo, id, user, status string, start, end int64, inTok, outTok int) {
	t.Helper()
	task := &Task{
		ID:            id,
		UserID:        user,
		StartTime:     start,
		Status:        strPtr(status),
		ExecutionMode: "foreground",
		Kind:          TaskKindNormal,
	}
	if end > 0 {
		task.EndTime = i64Ptr(end)
	}
	task.TotalInputTokens = intPtr(inTok)
	task.TotalOutputTokens = intPtr(outTok)
	task.TotalCachedInputTokens = intPtr(0)
	if err := repo.Create(context.Background(), task); err != nil {
		t.Fatalf("create task %s: %v", id, err)
	}
}

func makeTaskWithActivity(t *testing.T, repo *TaskRepo, id, user string, status *string, start int64, end, lastActivity *int64) {
	t.Helper()
	task := &Task{
		ID:               id,
		UserID:           user,
		StartTime:        start,
		Status:           status,
		EndTime:          end,
		LastActivityTime: lastActivity,
		ExecutionMode:    "foreground",
		Kind:             TaskKindNormal,
	}
	if err := repo.Create(context.Background(), task); err != nil {
		t.Fatalf("create task %s: %v", id, err)
	}
}

func TestAnalyticsRepo_AggregateTaskHealth(t *testing.T) {
	db := newTestDB(t)
	taskRepo := NewTaskRepo(db)
	repo := NewAnalyticsRepo(db)
	ctx := context.Background()

	const base = 1_700_000_000_000
	makeTask(t, taskRepo, "10000000-0000-0000-0000-000000000001", "u1", "completed", base, base+1000, 10, 20)
	makeTask(t, taskRepo, "10000000-0000-0000-0000-000000000002", "u1", "completed", base+10, base+3000, 5, 5)
	makeTask(t, taskRepo, "10000000-0000-0000-0000-000000000003", "u2", "error", base+20, base+500, 1, 0)
	makeTask(t, taskRepo, "10000000-0000-0000-0000-000000000004", "u2", "running", base+30, 0, 0, 0)

	agg, err := repo.AggregateTaskHealth(ctx, TaskAnalyticsOpts{Kind: TaskKindNormal})
	if err != nil {
		t.Fatal(err)
	}
	if agg.ByStatus["completed"] != 2 {
		t.Errorf("completed = %d, want 2", agg.ByStatus["completed"])
	}
	if agg.ByStatus["error"] != 1 {
		t.Errorf("error = %d, want 1", agg.ByStatus["error"])
	}
	if agg.ByStatus["running"] != 1 {
		t.Errorf("running = %d, want 1", agg.ByStatus["running"])
	}
	if len(agg.DurationsMs) != 3 {
		t.Fatalf("durations len = %d, want 3", len(agg.DurationsMs))
	}
	if agg.DurationsMs[0] != 480 || agg.DurationsMs[2] != 2990 {
		t.Errorf("durations = %v, want first 480 last 2990", agg.DurationsMs)
	}
}

func TestAnalyticsRepo_AggregateTaskHealth_StaleSplit(t *testing.T) {
	db := newTestDB(t)
	taskRepo := NewTaskRepo(db)
	repo := NewAnalyticsRepo(db)
	ctx := context.Background()

	const base = 1_700_000_000_000
	fresh := int64(base + 60_000)
	stale := int64(base + 10)
	cutoff := int64(base + 30_000)

	makeTaskWithActivity(t, taskRepo, "10000000-0000-0000-0000-000000000001", "u1", nil, base, nil, i64Ptr(fresh))
	makeTaskWithActivity(t, taskRepo, "10000000-0000-0000-0000-000000000002", "u1", nil, base, nil, i64Ptr(stale))
	makeTaskWithActivity(t, taskRepo, "10000000-0000-0000-0000-000000000003", "u1", strPtr("running"), base, nil, i64Ptr(stale))
	makeTaskWithActivity(t, taskRepo, "10000000-0000-0000-0000-000000000004", "u1", strPtr("completed"), base, i64Ptr(base+1000), nil)

	agg, err := repo.AggregateTaskHealth(ctx, TaskAnalyticsOpts{
		Kind:                  TaskKindNormal,
		StaleActivityBeforeMs: cutoff,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := agg.ByStatus[TaskStatusInProgress]; got != 1 {
		t.Errorf("in_progress = %d, want 1 (fresh last_activity)", got)
	}
	if got := agg.ByStatus[TaskStatusStale]; got != 2 {
		t.Errorf("stale = %d, want 2 (empty-status old + running old both fall under stale — status is overridden when end_time is null)", got)
	}
	if got := agg.ByStatus["completed"]; got != 1 {
		t.Errorf("completed = %d, want 1", got)
	}
}

func TestAnalyticsRepo_AggregateTaskHealth_ActiveBackground(t *testing.T) {
	db := newTestDB(t)
	taskRepo := NewTaskRepo(db)
	repo := NewAnalyticsRepo(db)
	ctx := context.Background()

	const base = 1_700_000_000_000
	fresh := int64(base + 60_000)
	stale := int64(base + 10)
	cutoff := int64(base + 30_000)

	mk := func(id, execMode string, status *string, end, lastActivity *int64) {
		t.Helper()
		task := &Task{
			ID:               id,
			UserID:           "u1",
			StartTime:        base,
			Status:           status,
			EndTime:          end,
			LastActivityTime: lastActivity,
			ExecutionMode:    execMode,
			Kind:             TaskKindNormal,
		}
		if err := taskRepo.Create(ctx, task); err != nil {
			t.Fatalf("create %s: %v", id, err)
		}
	}

	mk("10000000-0000-0000-0000-000000000001", "background", nil, nil, i64Ptr(fresh))
	mk("10000000-0000-0000-0000-000000000002", "background", nil, nil, i64Ptr(stale))
	mk("10000000-0000-0000-0000-000000000003", "foreground", nil, nil, i64Ptr(fresh))
	mk("10000000-0000-0000-0000-000000000004", "background", strPtr("completed"), i64Ptr(base+1000), nil)
	mk("10000000-0000-0000-0000-000000000005", "background", nil, nil, i64Ptr(cutoff))

	agg, err := repo.AggregateTaskHealth(ctx, TaskAnalyticsOpts{
		Kind:                  TaskKindNormal,
		StaleActivityBeforeMs: cutoff,
	})
	if err != nil {
		t.Fatal(err)
	}

	if agg.ActiveBackgroundCount != 1 {
		t.Errorf("ActiveBackgroundCount = %d, want 1 (only the fresh background task)", agg.ActiveBackgroundCount)
	}
	if got := agg.ByStatus[TaskStatusInProgress]; got != 2 {
		t.Errorf("in_progress = %d, want 2 (fresh background + fresh foreground)", got)
	}
	if got := agg.ByStatus[TaskStatusStale]; got != 2 {
		t.Errorf("stale = %d, want 2 (the stale background task + the at-cutoff background task)", got)
	}
	if agg.ActiveBackgroundCount > agg.ByStatus[TaskStatusInProgress] {
		t.Errorf("active background (%d) must be <= in_progress (%d)",
			agg.ActiveBackgroundCount, agg.ByStatus[TaskStatusInProgress])
	}
}

func TestAnalyticsRepo_ListFailedAndSlow(t *testing.T) {
	db := newTestDB(t)
	taskRepo := NewTaskRepo(db)
	repo := NewAnalyticsRepo(db)
	ctx := context.Background()

	const base = 1_700_000_000_000
	makeTask(t, taskRepo, "10000000-0000-0000-0000-000000000001", "u1", "completed", base, base+1000, 0, 0)
	makeTask(t, taskRepo, "10000000-0000-0000-0000-000000000002", "u1", "error", base+10, base+500, 0, 0)
	makeTask(t, taskRepo, "10000000-0000-0000-0000-000000000003", "u2", "timeout", base+20, base+9000, 0, 0)
	makeTask(t, taskRepo, "10000000-0000-0000-0000-000000000004", "u1", "completed", base+30, base+5030, 0, 0)

	failed, total, err := repo.ListFailedTasks(ctx, TaskAnalyticsOpts{Kind: TaskKindNormal}, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 || len(failed) != 2 {
		t.Errorf("failed total=%d len=%d, want 2/2", total, len(failed))
	}

	slow, total, err := repo.ListSlowTasks(ctx, TaskAnalyticsOpts{Kind: TaskKindNormal}, 800, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 || len(slow) != 2 {
		t.Fatalf("slow total=%d len=%d, want 2/2", total, len(slow))
	}
	if slow[0].ID != "10000000-0000-0000-0000-000000000004" {
		t.Errorf("slowest = %s, want the 5000ms completed task (not the timeout artifact)", slow[0].ID)
	}
	for _, s := range slow {
		if s.ID == "10000000-0000-0000-0000-000000000003" {
			t.Errorf("janitor-timeout task leaked into the slow list")
		}
	}
}

func TestAnalyticsRepo_AggregateTokens(t *testing.T) {
	db := newTestDB(t)
	taskRepo := NewTaskRepo(db)
	repo := NewAnalyticsRepo(db)
	ctx := context.Background()

	const day = int64(86400000)
	d0 := int64(1_700_000_000_000) / day * day
	makeTask(t, taskRepo, "10000000-0000-0000-0000-000000000001", "u1", "completed", d0+100, d0+200, 10, 20)
	makeTask(t, taskRepo, "10000000-0000-0000-0000-000000000002", "u1", "completed", d0+300, d0+400, 30, 40)
	makeTask(t, taskRepo, "10000000-0000-0000-0000-000000000003", "u2", "completed", d0+day+100, d0+day+200, 5, 5)

	byDay, err := repo.AggregateTokens(ctx, TaskAnalyticsOpts{Kind: TaskKindNormal}, TokenGroupByDay)
	if err != nil {
		t.Fatal(err)
	}
	if len(byDay) != 2 {
		t.Fatalf("byDay len = %d, want 2", len(byDay))
	}
	if byDay[0].PeriodStartMs != d0 || byDay[0].InputTokens != 40 || byDay[0].OutputTokens != 60 {
		t.Errorf("day0 = %+v, want day=%d in=40 out=60", byDay[0], d0)
	}
	if byDay[0].TaskCount != 2 {
		t.Errorf("day0 taskCount = %d, want 2", byDay[0].TaskCount)
	}

	byUser, err := repo.AggregateTokens(ctx, TaskAnalyticsOpts{Kind: TaskKindNormal}, TokenGroupByUser)
	if err != nil {
		t.Fatal(err)
	}
	if len(byUser) != 2 {
		t.Fatalf("byUser len = %d, want 2", len(byUser))
	}
	if byUser[0].GroupKey != "u1" || byUser[0].InputTokens != 40 {
		t.Errorf("byUser[0] = %+v, want u1 in=40", byUser[0])
	}
}

func TestAnalyticsRepo_AggregateAgentPerformance(t *testing.T) {
	db := newTestDB(t)
	taskRepo := NewTaskRepo(db)
	eventRepo := NewTaskEventRepo(db)
	repo := NewAnalyticsRepo(db)
	ctx := context.Background()

	const base = 1_700_000_000_000
	makeTask(t, taskRepo, "10000000-0000-0000-0000-000000000001", "u1", "completed", base, base+1000, 0, 0)
	makeTask(t, taskRepo, "10000000-0000-0000-0000-000000000002", "u1", "error", base+10, base+500, 0, 0)

	mkEvent := func(id, taskID, agent string) {
		if err := eventRepo.Create(ctx, &TaskEvent{
			ID:          id,
			TaskID:      taskID,
			CreatedTime: base,
			Topic:       "ns/a2a/v1/agent/request/" + agent,
			Direction:   "request",
			Payload:     "{}",
		}); err != nil {
			t.Fatalf("create event: %v", err)
		}
	}
	mkEvent("20000000-0000-0000-0000-000000000001", "10000000-0000-0000-0000-000000000001", "search-agent")
	mkEvent("20000000-0000-0000-0000-000000000002", "10000000-0000-0000-0000-000000000002", "search-agent")
	mkEvent("20000000-0000-0000-0000-000000000003", "10000000-0000-0000-0000-000000000001", "search-agent")

	rows, err := repo.AggregateAgentPerformance(ctx, "ns", TaskAnalyticsOpts{Kind: TaskKindNormal})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("agent rows = %d, want 1", len(rows))
	}
	row := rows[0]
	if row.AgentName != "search-agent" || row.InvocationCnt != 2 || row.ErrorCnt != 1 {
		t.Errorf("row = %+v, want search-agent inv=2 err=1 (retry on task 1 deduped)", row)
	}
	if len(row.DurationsMs) != 2 {
		t.Errorf("durations = %v, want 2", row.DurationsMs)
	}

	rowsNoNS, err := repo.AggregateAgentPerformance(ctx, "", TaskAnalyticsOpts{Kind: TaskKindNormal})
	if err != nil {
		t.Fatal(err)
	}
	if len(rowsNoNS) != 1 || rowsNoNS[0].InvocationCnt != 2 {
		t.Errorf("unanchored path = %+v, want 1 row inv=2", rowsNoNS)
	}
}

func TestAnalyticsRepo_AgentPerformance_PerChildTask(t *testing.T) {
	db := newTestDB(t)
	taskRepo := NewTaskRepo(db)
	eventRepo := NewTaskEventRepo(db)
	repo := NewAnalyticsRepo(db)
	ctx := context.Background()

	const base = int64(1_700_000_000_000)
	parent := "10000000-0000-0000-0000-0000000000a0"
	childA := "10000000-0000-0000-0000-0000000000a1"
	childB := "10000000-0000-0000-0000-0000000000a2"

	mustCreate := func(id, status string, parentID *string, end int64) {
		task := &Task{ID: id, UserID: "u1", StartTime: base, Status: strPtr(status),
			ExecutionMode: "foreground", Kind: TaskKindNormal, ParentTaskID: parentID}
		if end > 0 {
			task.EndTime = i64Ptr(end)
		}
		if err := taskRepo.Create(ctx, task); err != nil {
			t.Fatalf("create %s: %v", id, err)
		}
	}
	mustCreate(parent, "error", nil, base+5000)
	mustCreate(childA, "completed", &parent, base+1000)
	mustCreate(childB, "error", &parent, base+800)

	mkEvent := func(id, taskID, agent string) {
		if err := eventRepo.Create(ctx, &TaskEvent{ID: id, TaskID: taskID, CreatedTime: base,
			Topic: "ns/a2a/v1/agent/request/" + agent, Direction: "request", Payload: "{}"}); err != nil {
			t.Fatalf("event: %v", err)
		}
	}
	mkEvent("20000000-0000-0000-0000-0000000000b0", parent, "orchestrator")
	mkEvent("20000000-0000-0000-0000-0000000000b1", childA, "peer-a")
	mkEvent("20000000-0000-0000-0000-0000000000b2", childB, "peer-b")

	rows, err := repo.AggregateAgentPerformance(ctx, "ns", TaskAnalyticsOpts{Kind: TaskKindNormal})
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]AgentPerfRow{}
	for _, r := range rows {
		byName[r.AgentName] = r
	}
	if len(byName) != 3 {
		t.Fatalf("agents = %v, want orchestrator/peer-a/peer-b", byName)
	}
	if got := byName["peer-a"]; got.InvocationCnt != 1 || got.ErrorCnt != 0 {
		t.Errorf("peer-a = %+v, want inv=1 err=0 (parent failure must not smear)", got)
	}
	if got := byName["peer-b"]; got.InvocationCnt != 1 || got.ErrorCnt != 1 {
		t.Errorf("peer-b = %+v, want inv=1 err=1 (its own child failed)", got)
	}
	if got := byName["orchestrator"]; got.InvocationCnt != 1 || got.ErrorCnt != 1 {
		t.Errorf("orchestrator = %+v, want inv=1 err=1 (its own task)", got)
	}
}

func TestAnalyticsRepo_AgentPerformance_SameAgentDistinctTasks(t *testing.T) {
	db := newTestDB(t)
	taskRepo := NewTaskRepo(db)
	eventRepo := NewTaskEventRepo(db)
	repo := NewAnalyticsRepo(db)
	ctx := context.Background()

	const base = int64(1_700_000_000_000)
	makeTask(t, taskRepo, "10000000-0000-0000-0000-0000000000c1", "u1", "completed", base, base+1000, 0, 0)
	makeTask(t, taskRepo, "10000000-0000-0000-0000-0000000000c2", "u1", "error", base+10, base+500, 0, 0)

	mkEvent := func(id, taskID string) {
		if err := eventRepo.Create(ctx, &TaskEvent{ID: id, TaskID: taskID, CreatedTime: base,
			Topic: "ns/a2a/v1/agent/request/peer-a", Direction: "request", Payload: "{}"}); err != nil {
			t.Fatalf("event: %v", err)
		}
	}
	mkEvent("20000000-0000-0000-0000-0000000000d1", "10000000-0000-0000-0000-0000000000c1")
	mkEvent("20000000-0000-0000-0000-0000000000d2", "10000000-0000-0000-0000-0000000000c2")
	mkEvent("20000000-0000-0000-0000-0000000000d3", "10000000-0000-0000-0000-0000000000c1")

	rows, err := repo.AggregateAgentPerformance(ctx, "ns", TaskAnalyticsOpts{Kind: TaskKindNormal})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %v, want 1 (peer-a)", rows)
	}
	if got := rows[0]; got.AgentName != "peer-a" || got.InvocationCnt != 2 || got.ErrorCnt != 1 {
		t.Errorf("peer-a = %+v, want inv=2 (two distinct tasks, retry deduped) err=1 (one failed)", got)
	}
}

func TestAnalyticsRepo_AggregateFeedback(t *testing.T) {
	db := newTestDB(t)
	fbRepo := NewFeedbackRepo(db)
	repo := NewAnalyticsRepo(db)
	ctx := context.Background()

	mustSaveFeedback(t, fbRepo, &Feedback{ID: "40000000-0000-0000-0000-000000000001", SessionID: "00000000-0000-0000-0000-000000000001", TaskID: "10000000-0000-0000-0000-000000000001", UserID: "u1", Rating: "up"})
	mustSaveFeedback(t, fbRepo, &Feedback{ID: "40000000-0000-0000-0000-000000000002", SessionID: "00000000-0000-0000-0000-000000000001", TaskID: "10000000-0000-0000-0000-000000000002", UserID: "u1", Rating: "down", Comment: strPtr("it was wrong")})

	agg, err := repo.AggregateFeedback(ctx, 0, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if agg.Total != 2 {
		t.Errorf("total = %d, want 2", agg.Total)
	}
	if agg.ByRating["up"] != 1 || agg.ByRating["down"] != 1 {
		t.Errorf("byRating = %v", agg.ByRating)
	}
	if len(agg.RecentComments) != 1 || agg.RecentComments[0].Comment != "it was wrong" {
		t.Errorf("recentComments = %+v, want 1 with the down comment", agg.RecentComments)
	}
}

func TestAnalyticsRepo_AggregateFeedback_DateRange(t *testing.T) {
	db := newTestDB(t)
	fbRepo := NewFeedbackRepo(db)
	repo := NewAnalyticsRepo(db)
	ctx := context.Background()

	mustSaveFeedback(t, fbRepo, &Feedback{ID: "40000000-0000-0000-0000-000000000001", SessionID: "00000000-0000-0000-0000-000000000001", TaskID: "10000000-0000-0000-0000-000000000001", UserID: "u1", Rating: "up", CreatedAt: 1000, UpdatedAt: 1000})
	mustSaveFeedback(t, fbRepo, &Feedback{ID: "40000000-0000-0000-0000-000000000002", SessionID: "00000000-0000-0000-0000-000000000001", TaskID: "10000000-0000-0000-0000-000000000002", UserID: "u1", Rating: "down", Comment: strPtr("in window"), CreatedAt: 2000, UpdatedAt: 2000})
	mustSaveFeedback(t, fbRepo, &Feedback{ID: "40000000-0000-0000-0000-000000000003", SessionID: "00000000-0000-0000-0000-000000000001", TaskID: "10000000-0000-0000-0000-000000000003", UserID: "u1", Rating: "up", CreatedAt: 3000, UpdatedAt: 3000})

	agg, err := repo.AggregateFeedback(ctx, 1500, 2500, 10)
	if err != nil {
		t.Fatal(err)
	}
	if agg.Total != 1 {
		t.Errorf("total = %d, want 1 (only the row inside [1500, 2500])", agg.Total)
	}
	if agg.ByRating["down"] != 1 || agg.ByRating["up"] != 0 {
		t.Errorf("byRating = %v, want only the in-window down", agg.ByRating)
	}
	if len(agg.RecentComments) != 1 || agg.RecentComments[0].Comment != "in window" {
		t.Errorf("recentComments = %+v, want the in-window comment only", agg.RecentComments)
	}
}

func TestAnalyticsRepo_AggregateScheduledHealth(t *testing.T) {
	db := newTestDB(t)
	schedRepo := NewScheduledTaskRepo(db)
	repo := NewAnalyticsRepo(db)
	ctx := context.Background()

	mk := func(id, name string, enabled bool, fails int) {
		st := &ScheduledTask{
			ID:                      id,
			Name:                    name,
			Namespace:               "default",
			CreatedBy:               "admin",
			ScheduleType:            "cron",
			ScheduleExpression:      "0 * * * *",
			Timezone:                "UTC",
			TargetAgentName:         "agent-x",
			TargetType:              "agent",
			TaskMessage:             "hi",
			Source:                  "test",
			Enabled:                 enabled,
			ConsecutiveFailureCount: fails,
		}
		if err := schedRepo.Create(ctx, st); err != nil {
			t.Fatalf("create scheduled task: %v", err)
		}
	}
	mk("50000000-0000-0000-0000-000000000001", "ok-task", true, 0)
	mk("50000000-0000-0000-0000-000000000002", "broken-task", true, 3)
	mk("50000000-0000-0000-0000-000000000003", "disabled-task", false, 0)
	mk("50000000-0000-0000-0000-000000000004", "paused-broken-task", false, 5)

	agg, err := repo.AggregateScheduledHealth(ctx, "default", 0, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if agg.TotalEnabled != 2 {
		t.Errorf("enabled = %d, want 2", agg.TotalEnabled)
	}
	if agg.TotalDisabled != 2 {
		t.Errorf("disabled = %d, want 2", agg.TotalDisabled)
	}
	if len(agg.RecentFailures) != 1 || agg.RecentFailures[0].Name != "broken-task" {
		t.Errorf("recentFailures = %+v, want 1 broken-task (paused-broken-task excluded)", agg.RecentFailures)
	}
}

func TestAnalyticsRepo_QueryTaskFacts_Direct(t *testing.T) {
	db := newTestDB(t)
	taskRepo := NewTaskRepo(db)
	repo := NewAnalyticsRepo(db)
	ctx := context.Background()

	const base = int64(1_700_000_000_000)
	makeTask(t, taskRepo, "30000000-0000-0000-0000-000000000001", "u1", "completed", base, base+1000, 10, 20)
	makeTask(t, taskRepo, "30000000-0000-0000-0000-000000000002", "u1", "error", base+10, base+500, 1, 2)
	makeTask(t, taskRepo, "30000000-0000-0000-0000-000000000003", "u2", "completed", base+20, base+2000, 3, 4)

	all, _, err := repo.QueryTaskFacts(ctx, "ns", TaskFactsOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Fatalf("facts = %d, want 3", len(all))
	}
	for _, f := range all {
		if f.AgentName != "" {
			t.Errorf("direct-path fact should have empty AgentName, got %q", f.AgentName)
		}
	}

	errored, _, err := repo.QueryTaskFacts(ctx, "ns", TaskFactsOpts{Status: "error"})
	if err != nil {
		t.Fatal(err)
	}
	if len(errored) != 1 || errored[0].Status != "error" {
		t.Errorf("status=error facts = %+v, want one error task", errored)
	}

	u2, _, err := repo.QueryTaskFacts(ctx, "ns", TaskFactsOpts{UserID: "u2"})
	if err != nil {
		t.Fatal(err)
	}
	if len(u2) != 1 {
		t.Fatalf("user=u2 facts = %d, want 1", len(u2))
	}
	if u2[0].InputTokens != 3 || u2[0].OutputTokens != 4 {
		t.Errorf("u2 tokens = %d/%d, want 3/4", u2[0].InputTokens, u2[0].OutputTokens)
	}
	if u2[0].EndTimeMs == nil || *u2[0].EndTimeMs != base+2000 {
		t.Errorf("u2 endTime = %v, want %d", u2[0].EndTimeMs, base+2000)
	}

	win, _, err := repo.QueryTaskFacts(ctx, "ns", TaskFactsOpts{EndDate: base + 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(win) != 1 {
		t.Errorf("window facts = %d, want 1 (only the earliest)", len(win))
	}
}

func TestAnalyticsRepo_QueryTaskFacts_AgentJoin(t *testing.T) {
	db := newTestDB(t)
	taskRepo := NewTaskRepo(db)
	eventRepo := NewTaskEventRepo(db)
	repo := NewAnalyticsRepo(db)
	ctx := context.Background()

	const base = int64(1_700_000_000_000)
	mk := func(id, status string) {
		makeTask(t, taskRepo, id, "u1", status, base, base+1000, 0, 0)
	}
	gw := "40000000-0000-0000-0000-0000000000a0"
	childA := "40000000-0000-0000-0000-0000000000a1"
	childB := "40000000-0000-0000-0000-0000000000a2"
	mk(gw, "completed")
	mk(childA, "completed")
	mk(childB, "error")
	mkEvent := func(id, taskID, agent string) {
		if err := eventRepo.Create(ctx, &TaskEvent{ID: id, TaskID: taskID, CreatedTime: base,
			Topic: "ns/a2a/v1/agent/request/" + agent, Direction: "request", Payload: "{}"}); err != nil {
			t.Fatalf("event: %v", err)
		}
	}
	mkEvent("50000000-0000-0000-0000-0000000000b1", childA, "agent_aaa")
	mkEvent("50000000-0000-0000-0000-0000000000b2", childB, "agent_bbb")
	mkEvent("50000000-0000-0000-0000-0000000000b3", childA, "agent_aaa")

	facts, _, err := repo.QueryTaskFacts(ctx, "ns", TaskFactsOpts{RequireAgent: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(facts) != 2 {
		t.Fatalf("agent facts = %+v, want 2 (peer-a, peer-b)", facts)
	}
	byAgent := map[string]TaskFact{}
	for _, f := range facts {
		byAgent[f.AgentName] = f
	}
	if _, ok := byAgent["agent_aaa"]; !ok {
		t.Errorf("missing agent_aaa fact (dedup must keep one, not drop it): %+v", facts)
	}
	if byAgent["agent_bbb"].Status != "error" {
		t.Errorf("agent_bbb status = %q, want error", byAgent["agent_bbb"].Status)
	}
}

func TestAnalyticsRepo_QueryTaskFacts_ExecutionModeFilter(t *testing.T) {
	db := newTestDB(t)
	taskRepo := NewTaskRepo(db)
	repo := NewAnalyticsRepo(db)
	ctx := context.Background()

	const base = int64(1_700_000_000_000)
	makeTask(t, taskRepo, "60000000-0000-0000-0000-000000000001", "u1", "completed", base, base+1000, 0, 0)
	bg := &Task{ID: "60000000-0000-0000-0000-000000000002", UserID: "u1", StartTime: base + 10,
		Status: strPtr("completed"), EndTime: i64Ptr(base + 2000), ExecutionMode: "background", Kind: TaskKindNormal,
		TotalInputTokens: intPtr(0), TotalOutputTokens: intPtr(0), TotalCachedInputTokens: intPtr(0)}
	if err := taskRepo.Create(ctx, bg); err != nil {
		t.Fatalf("create bg: %v", err)
	}

	fg, _, err := repo.QueryTaskFacts(ctx, "ns", TaskFactsOpts{ExecutionMode: "foreground"})
	if err != nil {
		t.Fatal(err)
	}
	if len(fg) != 1 || fg[0].ExecutionMode != "foreground" {
		t.Errorf("execution_mode=foreground facts = %+v, want one foreground task", fg)
	}
	back, _, err := repo.QueryTaskFacts(ctx, "ns", TaskFactsOpts{ExecutionMode: "background"})
	if err != nil {
		t.Fatal(err)
	}
	if len(back) != 1 || back[0].ExecutionMode != "background" {
		t.Errorf("execution_mode=background facts = %+v, want one background task", back)
	}
}

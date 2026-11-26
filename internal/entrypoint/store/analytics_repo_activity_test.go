// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestAnalyticsRepo_ActivityWorkflowBreakdown_RecursiveSubtree(t *testing.T) {
	db := newTestDB(t)
	taskRepo := NewTaskRepo(db)
	repo := NewAnalyticsRepo(db)
	ctx := context.Background()

	const base = 1_700_000_000_000
	mk := func(id, name, htype string, parent *string, status string, start, end int64, in, out int) {
		task := &Task{
			ID:                     id,
			UserID:                 "u1",
			StartTime:              start,
			Status:                 strPtr(status),
			ExecutionMode:          "foreground",
			Kind:                   TaskKindNormal,
			HandlerName:            strPtr(name),
			HandlerType:            strPtr(htype),
			ParentTaskID:           parent,
			TotalInputTokens:       intPtr(in),
			TotalOutputTokens:      intPtr(out),
			TotalCachedInputTokens: intPtr(0),
		}
		if end > 0 {
			task.EndTime = i64Ptr(end)
		}
		if err := taskRepo.Create(ctx, task); err != nil {
			t.Fatalf("create %s: %v", id, err)
		}
	}

	rootID := "10000000-0000-0000-0000-000000000001"
	scraperID := "10000000-0000-0000-0000-000000000002"
	fetchID := "10000000-0000-0000-0000-000000000003"
	aggID := "10000000-0000-0000-0000-000000000004"

	mk(rootID, "news_scraper", TaskHandlerTypeWorkflow, nil, "completed", base, base+9000, 5, 10)
	mk(scraperID, "NewsScraper", TaskHandlerTypeAgent, strPtr(rootID), "completed", base+100, base+4000, 100, 200)
	mk(fetchID, "web_fetch", TaskHandlerTypeAgent, strPtr(scraperID), "completed", base+200, base+1500, 40, 60)
	mk(aggID, "NewsAggregator", TaskHandlerTypeAgent, strPtr(rootID), "error", base+4500, base+8500, 30, 15)

	rows, err := repo.AggregateActivityWorkflowBreakdown(ctx, TaskAnalyticsOpts{Kind: TaskKindNormal})
	if err != nil {
		t.Fatalf("AggregateActivityWorkflowBreakdown: %v", err)
	}

	by := indexBreakdown(rows)

	scraper := by["news_scraper|NewsScraper"]
	if scraper == nil {
		t.Fatalf("expected news_scraper→NewsScraper row; got %+v", rows)
	}
	if scraper.InvocationCnt != 1 || scraper.InputTokens != 100 || scraper.OutputTokens != 200 {
		t.Errorf("scraper: inv=%d in=%d out=%d, want 1/100/200",
			scraper.InvocationCnt, scraper.InputTokens, scraper.OutputTokens)
	}

	fetch := by["news_scraper|web_fetch"]
	if fetch == nil {
		t.Fatalf("recursive CTE lost the grandchild web_fetch row: %+v", rows)
	}
	if fetch.InputTokens != 40 || fetch.OutputTokens != 60 {
		t.Errorf("web_fetch tokens = %d/%d, want 40/60", fetch.InputTokens, fetch.OutputTokens)
	}

	agg := by["news_scraper|NewsAggregator"]
	if agg == nil {
		t.Fatalf("expected news_scraper→NewsAggregator row; got %+v", rows)
	}
	if agg.ErrorCnt != 1 {
		t.Errorf("NewsAggregator errorCnt = %d, want 1", agg.ErrorCnt)
	}

	if _, ok := by["news_scraper|news_scraper"]; ok {
		t.Error("workflow root must not appear as its own descendant")
	}
}

func TestAnalyticsRepo_ActivityWorkflowBreakdown_WorkflowInvokedByAgent(t *testing.T) {
	db := newTestDB(t)
	taskRepo := NewTaskRepo(db)
	repo := NewAnalyticsRepo(db)
	ctx := context.Background()

	const base = 1_700_000_000_000
	mk := func(id, name, htype string, parent *string, in, out int) {
		task := &Task{
			ID:                     id,
			UserID:                 "u1",
			StartTime:              base,
			Status:                 strPtr("completed"),
			ExecutionMode:          "foreground",
			Kind:                   TaskKindNormal,
			HandlerName:            strPtr(name),
			HandlerType:            strPtr(htype),
			ParentTaskID:           parent,
			TotalInputTokens:       intPtr(in),
			TotalOutputTokens:      intPtr(out),
			TotalCachedInputTokens: intPtr(0),
			EndTime:                i64Ptr(base + 1000),
		}
		if err := taskRepo.Create(ctx, task); err != nil {
			t.Fatalf("create %s: %v", id, err)
		}
	}

	orchID := "30000000-0000-0000-0000-000000000001"
	wfID := "30000000-0000-0000-0000-000000000002"
	scraperID := "30000000-0000-0000-0000-000000000003"
	aggID := "30000000-0000-0000-0000-000000000004"

	mk(orchID, "Orchestrator", TaskHandlerTypeAgent, nil, 50, 30)
	mk(wfID, "NewsScraperWorkflow", TaskHandlerTypeWorkflow, strPtr(orchID), 0, 0)
	mk(scraperID, "NewsScraper", TaskHandlerTypeAgent, strPtr(wfID), 100, 200)
	mk(aggID, "NewsAggregator", TaskHandlerTypeAgent, strPtr(wfID), 80, 40)

	rows, err := repo.AggregateActivityWorkflowBreakdown(ctx, TaskAnalyticsOpts{Kind: TaskKindNormal})
	if err != nil {
		t.Fatalf("AggregateActivityWorkflowBreakdown: %v", err)
	}
	by := indexBreakdown(rows)

	if by["NewsScraperWorkflow|NewsScraper"] == nil {
		t.Fatalf("Orchestrator-invoked workflow lost from breakdown: %+v", rows)
	}
	if by["NewsScraperWorkflow|NewsAggregator"] == nil {
		t.Fatalf("Orchestrator-invoked workflow's second child lost: %+v", rows)
	}
}

func TestAnalyticsRepo_ActivityWorkflows_GrandchildrenTokensCounted(t *testing.T) {
	db := newTestDB(t)
	taskRepo := NewTaskRepo(db)
	repo := NewAnalyticsRepo(db)
	ctx := context.Background()

	const base = 1_700_000_000_000
	mk := func(id, name, htype string, parent *string, in, out int) {
		task := &Task{
			ID: id, UserID: "u1", StartTime: base,
			Status: strPtr("completed"), ExecutionMode: "foreground",
			Kind:                   TaskKindNormal,
			HandlerName:            strPtr(name),
			HandlerType:            strPtr(htype),
			ParentTaskID:           parent,
			TotalInputTokens:       intPtr(in),
			TotalOutputTokens:      intPtr(out),
			TotalCachedInputTokens: intPtr(0),
			EndTime:                i64Ptr(base + 1000),
		}
		if err := taskRepo.Create(ctx, task); err != nil {
			t.Fatalf("create %s: %v", id, err)
		}
	}

	wfID := "60000000-0000-0000-0000-000000000001"
	scraperID := "60000000-0000-0000-0000-000000000002"
	helperID := "60000000-0000-0000-0000-000000000003"

	mk(wfID, "NewsScraperWorkflow", TaskHandlerTypeWorkflow, nil, 0, 0)
	mk(scraperID, "NewsScraper", TaskHandlerTypeAgent, strPtr(wfID), 100, 200)
	mk(helperID, "helper_agent", TaskHandlerTypeAgent, strPtr(scraperID), 40, 60)

	rows, err := repo.AggregateActivityWorkflows(ctx, TaskAnalyticsOpts{
		Kind:        TaskKindNormal,
		HandlerType: TaskHandlerTypeWorkflow,
	})
	if err != nil {
		t.Fatalf("AggregateActivityWorkflows: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 workflow row, got %d: %+v", len(rows), rows)
	}
	r := rows[0]
	if r.InputTokens != 140 {
		t.Errorf("subtree input tokens = %d, want 140 (100 child + 40 grandchild)", r.InputTokens)
	}
	if r.OutputTokens != 260 {
		t.Errorf("subtree output tokens = %d, want 260 (200 child + 60 grandchild)", r.OutputTokens)
	}
}

func TestAnalyticsRepo_ActivityAgents_ExcludeWorkflowNested(t *testing.T) {
	db := newTestDB(t)
	taskRepo := NewTaskRepo(db)
	repo := NewAnalyticsRepo(db)
	ctx := context.Background()

	const base = 1_700_000_000_000
	mk := func(id, name, htype string, parent *string) {
		task := &Task{
			ID: id, UserID: "u1", StartTime: base,
			Status: strPtr("completed"), ExecutionMode: "foreground",
			Kind:                   TaskKindNormal,
			HandlerName:            strPtr(name),
			HandlerType:            strPtr(htype),
			ParentTaskID:           parent,
			TotalInputTokens:       intPtr(0),
			TotalOutputTokens:      intPtr(0),
			TotalCachedInputTokens: intPtr(0),
			EndTime:                i64Ptr(base + 1000),
		}
		if err := taskRepo.Create(ctx, task); err != nil {
			t.Fatalf("create %s: %v", id, err)
		}
	}

	orchID := "50000000-0000-0000-0000-000000000001"
	wfID := "50000000-0000-0000-0000-000000000002"
	scraperID := "50000000-0000-0000-0000-000000000003"
	aggID := "50000000-0000-0000-0000-000000000004"
	directAgentID := "50000000-0000-0000-0000-000000000005"

	mk(orchID, "Orchestrator", TaskHandlerTypeAgent, nil)
	mk(wfID, "NewsScraperWorkflow", TaskHandlerTypeWorkflow, strPtr(orchID))
	mk(scraperID, "NewsScraper", TaskHandlerTypeAgent, strPtr(wfID))
	mk(aggID, "NewsAggregator", TaskHandlerTypeAgent, strPtr(wfID))
	mk(directAgentID, "DirectAgent", TaskHandlerTypeAgent, strPtr(orchID))

	rows, err := repo.AggregateActivityAgents(ctx, TaskAnalyticsOpts{
		Kind:                  TaskKindNormal,
		HandlerType:           TaskHandlerTypeAgent,
		ExcludeWorkflowNested: true,
	})
	if err != nil {
		t.Fatalf("AggregateActivityAgents: %v", err)
	}

	got := map[string]bool{}
	for _, r := range rows {
		got[r.HandlerName] = true
	}
	if !got["Orchestrator"] {
		t.Errorf("Orchestrator (top-level agent) missing from direct-agents rollup: %+v", rows)
	}
	if !got["DirectAgent"] {
		t.Errorf("DirectAgent (agent invoked by another agent) missing from direct-agents rollup: %+v", rows)
	}
	if got["NewsScraper"] {
		t.Errorf("workflow-nested NewsScraper leaked into direct-agents rollup: %+v", rows)
	}
	if got["NewsAggregator"] {
		t.Errorf("workflow-nested NewsAggregator leaked into direct-agents rollup: %+v", rows)
	}
}

func TestAnalyticsRepo_ActivityWorkflows_WorkflowInvokedByAgent(t *testing.T) {
	db := newTestDB(t)
	taskRepo := NewTaskRepo(db)
	repo := NewAnalyticsRepo(db)
	ctx := context.Background()

	const base = 1_700_000_000_000
	mk := func(id, name, htype string, parent *string) {
		task := &Task{
			ID: id, UserID: "u1", StartTime: base,
			Status: strPtr("completed"), ExecutionMode: "foreground",
			Kind:                   TaskKindNormal,
			HandlerName:            strPtr(name),
			HandlerType:            strPtr(htype),
			ParentTaskID:           parent,
			TotalInputTokens:       intPtr(0),
			TotalOutputTokens:      intPtr(0),
			TotalCachedInputTokens: intPtr(0),
			EndTime:                i64Ptr(base + 1000),
		}
		if err := taskRepo.Create(ctx, task); err != nil {
			t.Fatalf("create %s: %v", id, err)
		}
	}

	orchID := "40000000-0000-0000-0000-000000000001"
	wfID := "40000000-0000-0000-0000-000000000002"
	mk(orchID, "Orchestrator", TaskHandlerTypeAgent, nil)
	mk(wfID, "NewsScraperWorkflow", TaskHandlerTypeWorkflow, strPtr(orchID))

	rows, err := repo.AggregateActivityWorkflows(ctx, TaskAnalyticsOpts{
		Kind:        TaskKindNormal,
		HandlerType: TaskHandlerTypeWorkflow,
	})
	if err != nil {
		t.Fatalf("AggregateActivityWorkflows: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 workflow row (Orchestrator-invoked), got %d: %+v", len(rows), rows)
	}
	if rows[0].HandlerName != "NewsScraperWorkflow" {
		t.Errorf("workflow name = %q, want NewsScraperWorkflow", rows[0].HandlerName)
	}
}

func TestAnalyticsRepo_ActivityWorkflowBreakdown_OwnerUserIDScopedToRoot(t *testing.T) {
	db := newTestDB(t)
	taskRepo := NewTaskRepo(db)
	repo := NewAnalyticsRepo(db)
	ctx := context.Background()

	const base = 1_700_000_000_000
	mk := func(id, user, name, htype string, parent *string) {
		task := &Task{
			ID: id, UserID: user, StartTime: base,
			Status: strPtr("completed"), ExecutionMode: "foreground",
			Kind:        TaskKindNormal,
			HandlerName: strPtr(name), HandlerType: strPtr(htype),
			ParentTaskID:      parent,
			TotalInputTokens:  intPtr(0),
			TotalOutputTokens: intPtr(0), TotalCachedInputTokens: intPtr(0),
			EndTime: i64Ptr(base + 100),
		}
		if err := taskRepo.Create(ctx, task); err != nil {
			t.Fatalf("create %s: %v", id, err)
		}
	}

	aliceRoot := "20000000-0000-0000-0000-000000000001"
	aliceChild := "20000000-0000-0000-0000-000000000002"
	bobRoot := "20000000-0000-0000-0000-000000000003"
	bobChild := "20000000-0000-0000-0000-000000000004"

	mk(aliceRoot, "alice", "wf_alice", TaskHandlerTypeWorkflow, nil)
	mk(aliceChild, "alice", "child_alice", TaskHandlerTypeAgent, strPtr(aliceRoot))
	mk(bobRoot, "bob", "wf_bob", TaskHandlerTypeWorkflow, nil)
	mk(bobChild, "bob", "child_bob", TaskHandlerTypeAgent, strPtr(bobRoot))

	rows, err := repo.AggregateActivityWorkflowBreakdown(ctx, TaskAnalyticsOpts{
		Kind:        TaskKindNormal,
		OwnerUserID: "alice",
	})
	if err != nil {
		t.Fatalf("AggregateActivityWorkflowBreakdown: %v", err)
	}

	by := indexBreakdown(rows)
	if by["wf_alice|child_alice"] == nil {
		t.Errorf("alice's own descendant missing: %+v", rows)
	}
	for k := range by {
		if k == "wf_bob|child_bob" || k == "wf_alice|child_bob" {
			t.Errorf("owner scope leaked bob's data into alice's report: key=%q rows=%+v", k, rows)
		}
	}
}

func TestAnalyticsRepo_ActivityWorkflowBreakdown_CountsWholeSubtreeAcrossWindow(t *testing.T) {
	db := newTestDB(t)
	taskRepo := NewTaskRepo(db)
	repo := NewAnalyticsRepo(db)
	ctx := context.Background()

	const base = 1_700_000_000_000
	mk := func(id, name, htype string, parent *string, start int64) {
		task := &Task{
			ID: id, UserID: "u1", StartTime: start, EndTime: i64Ptr(start + 100),
			Status: strPtr("completed"), ExecutionMode: "foreground", Kind: TaskKindNormal,
			HandlerName: strPtr(name), HandlerType: strPtr(htype), ParentTaskID: parent,
			TotalInputTokens: intPtr(1), TotalOutputTokens: intPtr(1), TotalCachedInputTokens: intPtr(0),
		}
		if err := taskRepo.Create(ctx, task); err != nil {
			t.Fatalf("create %s: %v", id, err)
		}
	}

	wfID := "40000000-0000-0000-0000-000000000001"
	mk(wfID, "wf", TaskHandlerTypeWorkflow, nil, base)
	mk("40000000-0000-0000-0000-000000000002", "in_agent", TaskHandlerTypeAgent, strPtr(wfID), base+500)
	mk("40000000-0000-0000-0000-000000000003", "late_agent", TaskHandlerTypeAgent, strPtr(wfID), base+100_000)

	rows, err := repo.AggregateActivityWorkflowBreakdown(ctx, TaskAnalyticsOpts{
		Kind:      TaskKindNormal,
		StartDate: base - 1000,
		EndDate:   base + 1000,
	})
	if err != nil {
		t.Fatalf("AggregateActivityWorkflowBreakdown: %v", err)
	}
	by := indexBreakdown(rows)
	if by["wf|in_agent"] == nil {
		t.Errorf("in-window descendant missing: %+v", rows)
	}
	if by["wf|late_agent"] == nil {
		t.Errorf("out-of-window descendant dropped — descendants must not be window-filtered (whole-subtree cost): %+v", rows)
	}
}

func TestAnalyticsRepo_ActivityAgents_ExcludeWorkflowNested_CycleSafety(t *testing.T) {
	db := newTestDB(t)
	taskRepo := NewTaskRepo(db)
	repo := NewAnalyticsRepo(db)
	ctx := context.Background()

	const base = 1_700_000_000_000
	mk := func(id, name, htype string, parent *string) {
		task := &Task{
			ID: id, UserID: "u1", StartTime: base,
			Status: strPtr("completed"), ExecutionMode: "foreground",
			Kind:                   TaskKindNormal,
			HandlerName:            strPtr(name),
			HandlerType:            strPtr(htype),
			ParentTaskID:           parent,
			TotalInputTokens:       intPtr(0),
			TotalOutputTokens:      intPtr(0),
			TotalCachedInputTokens: intPtr(0),
			EndTime:                i64Ptr(base + 1000),
		}
		if err := taskRepo.Create(ctx, task); err != nil {
			t.Fatalf("create %s: %v", id, err)
		}
	}

	aID := "60000000-0000-0000-0000-000000000001"
	bID := "60000000-0000-0000-0000-000000000002"
	mk(aID, "AgentA", TaskHandlerTypeAgent, strPtr(bID))
	mk(bID, "AgentB", TaskHandlerTypeAgent, strPtr(aID))

	done := make(chan struct{})
	var rows []*ActivityHandlerPerfRow
	var err error
	go func() {
		defer close(done)
		var raw []ActivityHandlerPerfRow
		raw, err = repo.AggregateActivityAgents(ctx, TaskAnalyticsOpts{
			Kind:                  TaskKindNormal,
			HandlerType:           TaskHandlerTypeAgent,
			ExcludeWorkflowNested: true,
		})
		for i := range raw {
			rows = append(rows, &raw[i])
		}
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("AggregateActivityAgents hung on cyclic parent_task_id — depth guard did not fire")
	}
	if err != nil {
		t.Fatalf("AggregateActivityAgents: %v", err)
	}
	got := map[string]bool{}
	for _, r := range rows {
		got[r.HandlerName] = true
	}
	if !got["AgentA"] || !got["AgentB"] {
		t.Errorf("cyclic tasks should still appear in direct-agents rollup (no workflow ancestor): rows=%+v", rows)
	}
}

func TestTaskAnalyticsOpts_WorkflowNestedAnchorScopedToWindow(t *testing.T) {
	opts := TaskAnalyticsOpts{StartDate: 1000, EndDate: 2000, ExcludeWorkflowNested: true}
	prefix, args := opts.workflowNestedCTEPrefix()

	if !strings.Contains(prefix, "WITH RECURSIVE ancestor_chain") {
		t.Fatalf("prefix is not the recursive workflow-nested CTE:\n%s", prefix)
	}
	if !strings.Contains(prefix, "t0.start_time") {
		t.Errorf("recursion anchor is not window-bound (t0.start_time absent) — the CTE will full-scan tasks:\n%s", prefix)
	}
	if !strings.Contains(prefix, "parent_task_id IS NOT NULL") {
		t.Errorf("recursion anchor dropped the parent_task_id constraint:\n%s", prefix)
	}
	if len(args) < 2 {
		t.Errorf("prefix must carry the window bounds as args; got %d", len(args))
	}

	if p, a := (TaskAnalyticsOpts{ExcludeWorkflowNested: false}).workflowNestedCTEPrefix(); p != "" || len(a) != 0 {
		t.Errorf("prefix must be empty when ExcludeWorkflowNested is false; got %q / %d args", p, len(a))
	}
}

func TestAnalyticsRepo_ActivityAgents_ExcludeWorkflowNested_CorrectnessParity(t *testing.T) {
	db := newTestDB(t)
	taskRepo := NewTaskRepo(db)
	repo := NewAnalyticsRepo(db)
	ctx := context.Background()

	const inWindow = 1_700_000_000_000
	const outOfWindow = 1_500_000_000_000
	mk := func(id, name, htype string, parent *string, start int64) {
		task := &Task{
			ID: id, UserID: "u1", StartTime: start,
			Status: strPtr("completed"), ExecutionMode: "foreground",
			Kind:                   TaskKindNormal,
			HandlerName:            strPtr(name),
			HandlerType:            strPtr(htype),
			ParentTaskID:           parent,
			TotalInputTokens:       intPtr(0),
			TotalOutputTokens:      intPtr(0),
			TotalCachedInputTokens: intPtr(0),
			EndTime:                i64Ptr(start + 1000),
		}
		if err := taskRepo.Create(ctx, task); err != nil {
			t.Fatalf("create %s: %v", id, err)
		}
	}

	outWfID := "70000000-0000-0000-0000-000000000001"
	outAgentID := "70000000-0000-0000-0000-000000000002"
	inAgentID := "70000000-0000-0000-0000-000000000003"

	mk(outWfID, "OldWorkflow", TaskHandlerTypeWorkflow, nil, outOfWindow)
	mk(outAgentID, "OldNested", TaskHandlerTypeAgent, strPtr(outWfID), outOfWindow)
	mk(inAgentID, "InWindowAgent", TaskHandlerTypeAgent, nil, inWindow)

	rows, err := repo.AggregateActivityAgents(ctx, TaskAnalyticsOpts{
		Kind:                  TaskKindNormal,
		HandlerType:           TaskHandlerTypeAgent,
		ExcludeWorkflowNested: true,
		StartDate:             inWindow - 1000,
		EndDate:               inWindow + 10_000,
	})
	if err != nil {
		t.Fatalf("AggregateActivityAgents: %v", err)
	}
	got := map[string]bool{}
	for _, r := range rows {
		got[r.HandlerName] = true
	}
	if !got["InWindowAgent"] {
		t.Errorf("in-window agent missing from rollup: %+v", rows)
	}
	if got["OldNested"] {
		t.Errorf("out-of-window workflow-nested agent leaked into rollup: %+v", rows)
	}
}

func TestAnalyticsRepo_ActivityAgents_ExactCountsSeparateFromDurationSample(t *testing.T) {
	db := newTestDB(t)
	taskRepo := NewTaskRepo(db)
	repo := NewAnalyticsRepo(db)
	ctx := context.Background()

	const base = 1_700_000_000_000
	done := i64Ptr(base + 1000)
	mk := func(id, name, status string, end *int64, in, out, cached int) {
		task := &Task{
			ID: id, UserID: "u1", StartTime: base,
			Status: strPtr(status), ExecutionMode: "foreground",
			Kind:                   TaskKindNormal,
			HandlerName:            strPtr(name),
			HandlerType:            strPtr(TaskHandlerTypeAgent),
			TotalInputTokens:       intPtr(in),
			TotalOutputTokens:      intPtr(out),
			TotalCachedInputTokens: intPtr(cached),
			EndTime:                end,
		}
		if err := taskRepo.Create(ctx, task); err != nil {
			t.Fatalf("create %s: %v", id, err)
		}
	}
	mk("c0000000-0000-0000-0000-000000000001", "AgentX", "completed", done, 10, 20, 1)
	mk("c0000000-0000-0000-0000-000000000002", "AgentX", "completed", done, 10, 20, 1)
	mk("c0000000-0000-0000-0000-000000000003", "AgentX", "error", done, 5, 0, 0)
	mk("c0000000-0000-0000-0000-000000000004", "AgentX", "in_progress", nil, 3, 0, 0)
	mk("c0000000-0000-0000-0000-000000000005", "AgentX", "in_progress", nil, 3, 0, 0)
	mk("c0000000-0000-0000-0000-000000000006", "AgentY", "completed", done, 7, 7, 0)

	rows, err := repo.AggregateActivityAgents(ctx, TaskAnalyticsOpts{Kind: TaskKindNormal, HandlerType: TaskHandlerTypeAgent})
	if err != nil {
		t.Fatalf("AggregateActivityAgents: %v", err)
	}
	byName := map[string]ActivityHandlerPerfRow{}
	for _, r := range rows {
		byName[r.HandlerName] = r
	}
	x, ok := byName["AgentX"]
	if !ok {
		t.Fatalf("AgentX missing: %+v", rows)
	}
	if x.InvocationCnt != 5 {
		t.Errorf("invocations = %d, want 5 (2 completed + 1 error + 2 in_progress)", x.InvocationCnt)
	}
	if x.ErrorCnt != 1 {
		t.Errorf("errors = %d, want 1", x.ErrorCnt)
	}
	if x.InputTokens != 31 {
		t.Errorf("input tokens = %d, want 31 (summed over ALL tasks, incl in_progress)", x.InputTokens)
	}
	if x.OutputTokens != 40 {
		t.Errorf("output tokens = %d, want 40", x.OutputTokens)
	}
	if x.CachedInputTokens != 2 {
		t.Errorf("cached tokens = %d, want 2", x.CachedInputTokens)
	}
	if len(x.DurationsMs) != 3 {
		t.Errorf("duration sample = %d, want 3 (2 completed + 1 error; in_progress excluded)", len(x.DurationsMs))
	}
	if y := byName["AgentY"]; y.InvocationCnt != 1 || len(y.DurationsMs) != 1 {
		t.Errorf("AgentY = %+v, want 1 invocation + 1 duration", y)
	}
}

func TestAnalyticsRepo_ActivityWorkflows_ExactCountsAndDurationSplit(t *testing.T) {
	db := newTestDB(t)
	taskRepo := NewTaskRepo(db)
	repo := NewAnalyticsRepo(db)
	ctx := context.Background()

	const base = 1_700_000_000_000
	done := i64Ptr(base + 1000)
	mk := func(id, name, htype, status string, end *int64, parent *string, in, out int) {
		task := &Task{
			ID: id, UserID: "u1", StartTime: base,
			Status: strPtr(status), ExecutionMode: "foreground",
			Kind:                   TaskKindNormal,
			HandlerName:            strPtr(name),
			HandlerType:            strPtr(htype),
			ParentTaskID:           parent,
			TotalInputTokens:       intPtr(in),
			TotalOutputTokens:      intPtr(out),
			TotalCachedInputTokens: intPtr(0),
			EndTime:                end,
		}
		if err := taskRepo.Create(ctx, task); err != nil {
			t.Fatalf("create %s: %v", id, err)
		}
	}
	wf1 := "d0000000-0000-0000-0000-000000000001"
	wf2 := "d0000000-0000-0000-0000-000000000002"
	child := "d0000000-0000-0000-0000-000000000003"
	mk(wf1, "Flow", TaskHandlerTypeWorkflow, "completed", done, nil, 0, 0)
	mk(wf2, "Flow", TaskHandlerTypeWorkflow, "in_progress", nil, nil, 0, 0)
	mk(child, "InnerAgent", TaskHandlerTypeAgent, "completed", done, strPtr(wf1), 100, 200)

	rows, err := repo.AggregateActivityWorkflows(ctx, TaskAnalyticsOpts{Kind: TaskKindNormal, HandlerType: TaskHandlerTypeWorkflow})
	if err != nil {
		t.Fatalf("AggregateActivityWorkflows: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 workflow row, got %d: %+v", len(rows), rows)
	}
	r := rows[0]
	if r.HandlerName != "Flow" {
		t.Fatalf("name = %q, want Flow", r.HandlerName)
	}
	if r.InvocationCnt != 2 {
		t.Errorf("invocations = %d, want 2 (both runs counted, incl in_progress)", r.InvocationCnt)
	}
	if r.InputTokens != 100 || r.OutputTokens != 200 {
		t.Errorf("subtree tokens = (%d,%d), want (100,200)", r.InputTokens, r.OutputTokens)
	}
	if len(r.DurationsMs) != 1 {
		t.Errorf("duration sample = %d, want 1 (measurable root only; in_progress excluded)", len(r.DurationsMs))
	}
}

func TestAnalyticsRepo_ActivityWorkflows_CycleSafety(t *testing.T) {
	db := newTestDB(t)
	taskRepo := NewTaskRepo(db)
	repo := NewAnalyticsRepo(db)
	ctx := context.Background()

	const base = 1_700_000_000_000
	done := i64Ptr(base + 1000)
	mk := func(id, name, htype, status string, parent *string) {
		task := &Task{
			ID: id, UserID: "u1", StartTime: base,
			Status: strPtr(status), ExecutionMode: "foreground",
			Kind:                   TaskKindNormal,
			HandlerName:            strPtr(name),
			HandlerType:            strPtr(htype),
			ParentTaskID:           parent,
			TotalInputTokens:       intPtr(0),
			TotalOutputTokens:      intPtr(0),
			TotalCachedInputTokens: intPtr(0),
			EndTime:                done,
		}
		if err := taskRepo.Create(ctx, task); err != nil {
			t.Fatalf("create %s: %v", id, err)
		}
	}
	w := "e0000000-0000-0000-0000-000000000001"
	z := "e0000000-0000-0000-0000-000000000002"
	mk(z, "Inner", TaskHandlerTypeAgent, "completed", strPtr(w))
	mk(w, "Flow", TaskHandlerTypeWorkflow, "completed", strPtr(z))

	opts := TaskAnalyticsOpts{Kind: TaskKindNormal, HandlerType: TaskHandlerTypeWorkflow}
	done2 := make(chan error, 1)
	go func() {
		if _, err := repo.AggregateActivityWorkflows(ctx, opts); err != nil {
			done2 <- err
			return
		}
		_, err := repo.AggregateActivityWorkflowBreakdown(ctx, opts)
		done2 <- err
	}()
	select {
	case err := <-done2:
		if err != nil {
			t.Fatalf("aggregate over cyclic parent_task_id errored: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("workflow aggregate hung on cyclic parent_task_id — depth guard did not fire")
	}
}

func indexBreakdown(rows []ActivityWorkflowBreakdownRow) map[string]*ActivityWorkflowBreakdownRow {
	m := make(map[string]*ActivityWorkflowBreakdownRow, len(rows))
	for i := range rows {
		k := rows[i].WorkflowName + "|" + rows[i].HandlerName
		m[k] = &rows[i]
	}
	return m
}

func TestAnalyticsRepo_ActivityAgents_DurationSampleCappedPerHandler(t *testing.T) {
	db := newTestDB(t)
	taskRepo := NewTaskRepo(db)
	repo := NewAnalyticsRepo(db)
	repo.durationSampleCap = 3
	ctx := context.Background()

	const base = 1_700_000_000_000
	mk := func(id, name string, start, end int64) {
		task := &Task{
			ID: id, UserID: "u1", StartTime: start, EndTime: i64Ptr(end),
			Status: strPtr("completed"), ExecutionMode: "foreground", Kind: TaskKindNormal,
			HandlerName: strPtr(name), HandlerType: strPtr(TaskHandlerTypeAgent),
			TotalInputTokens: intPtr(1), TotalOutputTokens: intPtr(1), TotalCachedInputTokens: intPtr(0),
		}
		if err := taskRepo.Create(ctx, task); err != nil {
			t.Fatalf("create %s: %v", id, err)
		}
	}

	mk("20000000-0000-0000-0000-000000000001", "early", base+1, base+101)
	mk("20000000-0000-0000-0000-000000000002", "early", base+2, base+152)
	mk("20000000-0000-0000-0000-000000000003", "recent", base+1000, base+1200)
	mk("20000000-0000-0000-0000-000000000004", "recent", base+1001, base+1201)
	mk("20000000-0000-0000-0000-000000000005", "recent", base+1002, base+1202)
	mk("20000000-0000-0000-0000-000000000006", "recent", base+1003, base+1203)

	rows, err := repo.AggregateActivityAgents(ctx, TaskAnalyticsOpts{Kind: TaskKindNormal, HandlerType: TaskHandlerTypeAgent})
	if err != nil {
		t.Fatalf("AggregateActivityAgents: %v", err)
	}
	byName := make(map[string]ActivityHandlerPerfRow, len(rows))
	for _, r := range rows {
		byName[r.HandlerName] = r
	}

	early, ok := byName["early"]
	if !ok {
		t.Fatal("early handler missing from rollup")
	}
	if early.InvocationCnt != 2 {
		t.Errorf("early InvocationCnt = %d, want 2 (exact, uncapped)", early.InvocationCnt)
	}
	if len(early.DurationsMs) != 2 {
		t.Errorf("early DurationsMs len = %d, want 2 — a global recency cap would starve it (p95=0 beside 2 invocations)", len(early.DurationsMs))
	}
	if recent := byName["recent"]; len(recent.DurationsMs) != 3 {
		t.Errorf("recent DurationsMs len = %d, want 3 (per-handler cap still bounds each handler)", len(recent.DurationsMs))
	}
}

func TestAnalyticsRepo_ActivityWorkflowBreakdown_DurationSampleCappedPerHandler(t *testing.T) {
	db := newTestDB(t)
	taskRepo := NewTaskRepo(db)
	repo := NewAnalyticsRepo(db)
	repo.durationSampleCap = 3
	ctx := context.Background()

	const base = 1_700_000_000_000
	mk := func(id, name, htype string, parent *string, start, end int64) {
		task := &Task{
			ID: id, UserID: "u1", StartTime: start, EndTime: i64Ptr(end),
			Status: strPtr("completed"), ExecutionMode: "foreground", Kind: TaskKindNormal,
			HandlerName: strPtr(name), HandlerType: strPtr(htype), ParentTaskID: parent,
			TotalInputTokens: intPtr(1), TotalOutputTokens: intPtr(1), TotalCachedInputTokens: intPtr(0),
		}
		if err := taskRepo.Create(ctx, task); err != nil {
			t.Fatalf("create %s: %v", id, err)
		}
	}

	wfID := "30000000-0000-0000-0000-000000000001"
	mk(wfID, "wf", TaskHandlerTypeWorkflow, nil, base, base+9000)
	mk("30000000-0000-0000-0000-000000000002", "early", TaskHandlerTypeAgent, strPtr(wfID), base+1, base+101)
	mk("30000000-0000-0000-0000-000000000003", "early", TaskHandlerTypeAgent, strPtr(wfID), base+2, base+152)
	mk("30000000-0000-0000-0000-000000000004", "recent", TaskHandlerTypeAgent, strPtr(wfID), base+1000, base+1200)
	mk("30000000-0000-0000-0000-000000000005", "recent", TaskHandlerTypeAgent, strPtr(wfID), base+1001, base+1201)
	mk("30000000-0000-0000-0000-000000000006", "recent", TaskHandlerTypeAgent, strPtr(wfID), base+1002, base+1202)
	mk("30000000-0000-0000-0000-000000000007", "recent", TaskHandlerTypeAgent, strPtr(wfID), base+1003, base+1203)

	rows, err := repo.AggregateActivityWorkflowBreakdown(ctx, TaskAnalyticsOpts{Kind: TaskKindNormal})
	if err != nil {
		t.Fatalf("AggregateActivityWorkflowBreakdown: %v", err)
	}
	byKey := indexBreakdown(rows)

	early := byKey["wf|early"]
	if early == nil {
		t.Fatal("wf|early missing from breakdown")
	}
	if early.InvocationCnt != 2 {
		t.Errorf("early InvocationCnt = %d, want 2", early.InvocationCnt)
	}
	if len(early.DurationsMs) != 2 {
		t.Errorf("early DurationsMs len = %d, want 2 — global recency cap would starve it", len(early.DurationsMs))
	}
	if recent := byKey["wf|recent"]; recent == nil || len(recent.DurationsMs) != 3 {
		t.Errorf("recent breakdown row = %v, want 3 duration samples (per-(workflow,handler) cap)", recent)
	}
}

func TestAnalyticsRepo_ActivityWorkflows_CyclicSubtreeCountedOnce(t *testing.T) {
	db := newTestDB(t)
	taskRepo := NewTaskRepo(db)
	repo := NewAnalyticsRepo(db)
	ctx := context.Background()

	const base = 1_700_000_000_000
	mk := func(id, name, htype string, parent *string, in, out int) {
		task := &Task{
			ID: id, UserID: "u1", StartTime: base, EndTime: i64Ptr(base + 1000),
			Status: strPtr("completed"), ExecutionMode: "foreground", Kind: TaskKindNormal,
			HandlerName: strPtr(name), HandlerType: strPtr(htype), ParentTaskID: parent,
			TotalInputTokens: intPtr(in), TotalOutputTokens: intPtr(out), TotalCachedInputTokens: intPtr(0),
		}
		if err := taskRepo.Create(ctx, task); err != nil {
			t.Fatalf("create %s: %v", id, err)
		}
	}

	rID := "70000000-0000-0000-0000-000000000001"
	aID := "70000000-0000-0000-0000-000000000002"
	mk(rID, "CyclicWorkflow", TaskHandlerTypeWorkflow, strPtr(aID), 0, 0)
	mk(aID, "ChildAgent", TaskHandlerTypeAgent, strPtr(rID), 10, 20)

	rows, err := repo.AggregateActivityWorkflows(ctx, TaskAnalyticsOpts{Kind: TaskKindNormal, HandlerType: TaskHandlerTypeWorkflow})
	if err != nil {
		t.Fatalf("AggregateActivityWorkflows: %v", err)
	}
	var wf *ActivityHandlerPerfRow
	for i := range rows {
		if rows[i].HandlerName == "CyclicWorkflow" {
			wf = &rows[i]
		}
	}
	if wf == nil {
		t.Fatal("CyclicWorkflow row missing")
	}
	if wf.InputTokens != 10 || wf.OutputTokens != 20 {
		t.Errorf("CyclicWorkflow subtree tokens = %d in / %d out, want 10/20 (cycle members counted once, not ~16x)", wf.InputTokens, wf.OutputTokens)
	}
}

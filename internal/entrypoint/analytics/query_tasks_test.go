// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package analytics

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
)

func i64p(v int64) *int64 { return &v }
func ip(v int) *int       { return &v }

func queryTasksFixture(t *testing.T) (*Service, context.Context) {
	t.Helper()
	db := store.TestDB(t)
	taskRepo := store.NewTaskRepo(db)
	eventRepo := store.NewTaskEventRepo(db)
	ctx := context.Background()

	jan := time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC).UnixMilli()
	feb := time.Date(2026, 2, 20, 12, 0, 0, 0, time.UTC).UnixMilli()

	mk := func(id, user, status string, start int64, inTok, outTok int) {
		task := &store.Task{
			ID: id, UserID: user, StartTime: start, Status: &status,
			EndTime: i64p(start + 1000), ExecutionMode: "foreground", Kind: store.TaskKindNormal,
			TotalInputTokens: ip(inTok), TotalOutputTokens: ip(outTok), TotalCachedInputTokens: ip(0),
		}
		if err := taskRepo.Create(ctx, task); err != nil {
			t.Fatalf("create task %s: %v", id, err)
		}
	}
	mkEvent := func(id, taskID, agentSlug string) {
		if err := eventRepo.Create(ctx, &store.TaskEvent{ID: id, TaskID: taskID, CreatedTime: 1,
			Topic: "ns/a2a/v1/agent/request/" + agentSlug, Direction: "request", Payload: "{}"}); err != nil {
			t.Fatalf("event %s: %v", id, err)
		}
	}

	mk("a0000000-0000-0000-0000-000000000001", "u1", "completed", jan, 10, 20)
	mk("a0000000-0000-0000-0000-000000000002", "u2", "error", jan+5, 1, 2)
	mk("a0000000-0000-0000-0000-000000000003", "u1", "completed", feb, 5, 5)
	mk("b0000000-0000-0000-0000-000000000001", "u1", "completed", jan+10, 3, 3)

	mkEvent("e0000000-0000-0000-0000-000000000001", "a0000000-0000-0000-0000-000000000001", "agent_aaa")
	mkEvent("e0000000-0000-0000-0000-000000000002", "a0000000-0000-0000-0000-000000000002", "agent_aaa")
	mkEvent("e0000000-0000-0000-0000-000000000003", "a0000000-0000-0000-0000-000000000003", "agent_aaa")
	mkEvent("e0000000-0000-0000-0000-000000000004", "b0000000-0000-0000-0000-000000000001", "agent_bbb")

	resolver := func(slug string) string {
		switch slug {
		case "agent_aaa":
			return "Agent AAA"
		case "agent_bbb":
			return "Agent BBB"
		}
		return ""
	}
	return NewService(db).WithAgentNameResolver(resolver), ctx
}

func paged(pn, ps int) Query { return Query{PageNumber: pn, PageSize: ps, IncludeContent: true} }

func TestService_QueryTasks_Granular(t *testing.T) {
	svc, ctx := queryTasksFixture(t)

	q := paged(1, 100)
	q.IncludeContent = false
	res, err := svc.QueryTasks(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	page, ok := res.(*Paginated[TaskQueryRow])
	if !ok {
		t.Fatalf("granular result type = %T, want *Paginated[TaskQueryRow]", res)
	}
	if len(page.Data) != 4 {
		t.Fatalf("granular rows = %d, want 4", len(page.Data))
	}
	for _, r := range page.Data {
		if r.UserID != "" {
			t.Errorf("userId must be redacted below content tier, got %q", r.UserID)
		}
	}

	res, err = svc.QueryTasks(ctx, paged(1, 100))
	if err != nil {
		t.Fatal(err)
	}
	page = res.(*Paginated[TaskQueryRow])
	anyUser := false
	for _, r := range page.Data {
		if r.UserID != "" {
			anyUser = true
		}
	}
	if !anyUser {
		t.Error("userId should be present on the content tier")
	}
}

func TestService_QueryTasks_GroupByStatus(t *testing.T) {
	svc, ctx := queryTasksFixture(t)
	q := paged(1, 100)
	q.GroupDims = []string{"status"}
	res, err := svc.QueryTasks(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	page, ok := res.(*Paginated[TaskMetricRow])
	if !ok {
		t.Fatalf("grouped result type = %T, want *Paginated[TaskMetricRow]", res)
	}
	got := map[string]int{}
	for _, r := range page.Data {
		if r.Group.Status == nil {
			t.Fatalf("status group missing status dim: %+v", r.Group)
		}
		got[*r.Group.Status] = r.TaskCount
	}
	if got["completed"] != 3 || got["error"] != 1 {
		t.Errorf("by-status counts = %v, want completed=3 error=1", got)
	}
}

func TestService_QueryTasks_GroupByAgentMonth(t *testing.T) {
	svc, ctx := queryTasksFixture(t)
	q := paged(1, 100)
	q.Namespace = "ns"
	q.GroupDims = []string{"agent", "month"}
	res, err := svc.QueryTasks(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	page := res.(*Paginated[TaskMetricRow])

	type key struct {
		agent string
		month string
	}
	got := map[key]TaskMetricRow{}
	for _, r := range page.Data {
		if r.Group.Agent == nil || r.Group.Period != "month" || r.Group.PeriodStart == nil {
			t.Fatalf("agent/month group malformed: %+v", r.Group)
		}
		got[key{r.Group.Agent.Name, (*r.Group.PeriodStart)[:7]}] = r
	}
	janAAA := got[key{"Agent AAA", "2026-01"}]
	if janAAA.TaskCount != 2 || janAAA.ErrorCount != 1 {
		t.Errorf("Agent AAA Jan = count %d err %d, want 2/1", janAAA.TaskCount, janAAA.ErrorCount)
	}
	if janAAA.SuccessRate < 0.49 || janAAA.SuccessRate > 0.51 {
		t.Errorf("Agent AAA Jan successRate = %v, want ~0.5", janAAA.SuccessRate)
	}
	if febAAA := got[key{"Agent AAA", "2026-02"}]; febAAA.TaskCount != 1 {
		t.Errorf("Agent AAA Feb count = %d, want 1", febAAA.TaskCount)
	}
	if janBBB := got[key{"Agent BBB", "2026-01"}]; janBBB.TaskCount != 1 {
		t.Errorf("Agent BBB Jan count = %d, want 1", janBBB.TaskCount)
	}
}

func TestService_QueryTasks_AgentFilterByDisplayName(t *testing.T) {
	svc, ctx := queryTasksFixture(t)
	q := paged(1, 100)
	q.Namespace = "ns"
	q.AgentName = "Agent AAA"
	q.GroupDims = []string{"month"}
	res, err := svc.QueryTasks(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	page := res.(*Paginated[TaskMetricRow])
	total := 0
	for _, r := range page.Data {
		total += r.TaskCount
	}
	if total != 3 {
		t.Errorf("Agent AAA total tasks = %d, want 3 (filter by display name)", total)
	}
}

func TestService_QueryTasks_ContentGate(t *testing.T) {
	svc, ctx := queryTasksFixture(t)
	q := paged(1, 100)
	q.IncludeContent = false
	q.GroupDims = []string{"user"}
	_, err := svc.QueryTasks(ctx, q)
	if !errors.Is(err, ErrForbidden) {
		t.Errorf("group-by-user without content tier: err = %v, want ErrForbidden", err)
	}

	q2 := paged(1, 100)
	q2.IncludeContent = false
	q2.UserID = "u1"
	if _, err := svc.QueryTasks(ctx, q2); !errors.Is(err, ErrForbidden) {
		t.Errorf("filter-by-user without content tier: err = %v, want ErrForbidden", err)
	}
}

func TestService_QueryTasks_InvalidDim(t *testing.T) {
	svc, ctx := queryTasksFixture(t)
	q := paged(1, 100)
	q.GroupDims = []string{"bogus"}
	if _, err := svc.QueryTasks(ctx, q); !errors.Is(err, ErrInvalidQuery) {
		t.Errorf("bad dim: err = %v, want ErrInvalidQuery", err)
	}
	q2 := paged(1, 100)
	q2.GroupDims = []string{"day", "month"}
	if _, err := svc.QueryTasks(ctx, q2); !errors.Is(err, ErrInvalidQuery) {
		t.Errorf("two time dims: err = %v, want ErrInvalidQuery", err)
	}
}

func TestTaskBucketStartMs(t *testing.T) {
	ts := time.Date(2026, 3, 18, 15, 30, 0, 0, time.UTC).UnixMilli()
	day := time.UnixMilli(taskBucketStartMs(ts, "day")).UTC()
	if !day.Equal(time.Date(2026, 3, 18, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("day bucket = %v, want 2026-03-18 00:00", day)
	}
	week := time.UnixMilli(taskBucketStartMs(ts, "week")).UTC()
	if !week.Equal(time.Date(2026, 3, 16, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("week bucket = %v, want 2026-03-16 (Monday)", week)
	}
	month := time.UnixMilli(taskBucketStartMs(ts, "month")).UTC()
	if !month.Equal(time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("month bucket = %v, want 2026-03-01", month)
	}
}

func TestService_QueryTasks_GranularWithAgent(t *testing.T) {
	svc, ctx := queryTasksFixture(t)
	q := paged(1, 100)
	q.Namespace = "ns"
	q.AgentName = "Agent AAA"
	res, err := svc.QueryTasks(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	page, ok := res.(*Paginated[TaskQueryRow])
	if !ok {
		t.Fatalf("type = %T, want *Paginated[TaskQueryRow]", res)
	}
	if len(page.Data) != 3 {
		t.Fatalf("rows = %d, want 3 (agent_aaa's tasks)", len(page.Data))
	}
	for _, r := range page.Data {
		if r.Agent == nil {
			t.Errorf("granular agent-filtered row missing Agent ref: %+v", r)
			continue
		}
		if r.Agent.Name != "Agent AAA" {
			t.Errorf("row Agent.Name = %q, want Agent AAA", r.Agent.Name)
		}
		if r.Agent.ID == "" {
			t.Errorf("row Agent.ID empty, want resolved id")
		}
	}
}

func TestService_QueryTasks_GroupByAgentStatus(t *testing.T) {
	svc, ctx := queryTasksFixture(t)
	q := paged(1, 100)
	q.Namespace = "ns"
	q.GroupDims = []string{"agent", "status"}
	res, err := svc.QueryTasks(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	page := res.(*Paginated[TaskMetricRow])
	type key struct{ agent, status string }
	got := map[key]int{}
	for _, r := range page.Data {
		if r.Group.Agent == nil || r.Group.Status == nil {
			t.Fatalf("agent+status group malformed: %+v", r.Group)
		}
		got[key{r.Group.Agent.Name, *r.Group.Status}] = r.TaskCount
	}
	if got[key{"Agent AAA", "completed"}] != 2 || got[key{"Agent AAA", "error"}] != 1 {
		t.Errorf("Agent AAA buckets = %v, want completed=2 error=1", got)
	}
	if got[key{"Agent BBB", "completed"}] != 1 {
		t.Errorf("Agent BBB completed = %d, want 1", got[key{"Agent BBB", "completed"}])
	}
}

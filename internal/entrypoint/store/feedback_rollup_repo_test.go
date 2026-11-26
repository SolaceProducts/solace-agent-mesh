// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

const (
	rollupDayMs int64 = 86_400_000
	rollupDay0  int64 = 19675 * rollupDayMs
)

func seedHandlerTask(t *testing.T, repo *TaskRepo, handler, htype string, start int64, parent *string) string {
	t.Helper()
	id := idgen.New()
	task := &Task{
		ID: id, UserID: "u1", StartTime: start,
		Status: strPtr("completed"), ExecutionMode: "foreground", Kind: TaskKindNormal,
		HandlerName: strPtr(handler), HandlerType: strPtr(htype), ParentTaskID: parent,
		TotalInputTokens: intPtr(0), TotalOutputTokens: intPtr(0), TotalCachedInputTokens: intPtr(0),
		EndTime: i64Ptr(start + 100),
	}
	if err := repo.Create(t.Context(), task); err != nil {
		t.Fatalf("create task %s: %v", handler, err)
	}
	return id
}

func handlerCounts(rows []HandlerTaskCount) map[string]int {
	out := make(map[string]int, len(rows))
	for _, r := range rows {
		out[fmt.Sprintf("%s@%d", r.HandlerName, r.BucketStartMs)] = r.Count
	}
	return out
}

func bucketWidthFor(byDay bool) int64 {
	if byDay {
		return DayBucketMs
	}
	return 0
}

func TestAnalyticsRepo_CountHandlerTasks(t *testing.T) {
	db := newTestDB(t)
	tasks := NewTaskRepo(db)
	repo := NewAnalyticsRepo(db)
	ctx := t.Context()

	day1 := rollupDay0 + rollupDayMs
	seedHandlerTask(t, tasks, "agent-a", TaskHandlerTypeAgent, rollupDay0+1_000, nil)
	seedHandlerTask(t, tasks, "agent-a", TaskHandlerTypeAgent, day1+1_000, nil)
	seedHandlerTask(t, tasks, "agent-b", TaskHandlerTypeAgent, rollupDay0+2_000, nil)
	wf := seedHandlerTask(t, tasks, "wf-1", TaskHandlerTypeWorkflow, rollupDay0+3_000, nil)
	seedHandlerTask(t, tasks, "agent-a", TaskHandlerTypeAgent, rollupDay0+4_000, &wf)

	agents := TaskAnalyticsOpts{
		StartDate: rollupDay0, EndDate: rollupDay0 + 2*rollupDayMs, Kind: TaskKindNormal,
		HandlerType: TaskHandlerTypeAgent, ExcludeWorkflowNested: true,
	}
	count := func(t *testing.T, opts TaskAnalyticsOpts, scope SubjectScope, byDay bool) map[string]int {
		t.Helper()
		rows, err := repo.CountHandlerTasks(ctx, opts, scope, bucketWidthFor(byDay))
		if err != nil {
			t.Fatalf("CountHandlerTasks: %v", err)
		}
		return handlerCounts(rows)
	}

	t.Run("totals per handler exclude workflow-nested runs", func(t *testing.T) {
		got := count(t, agents, SubjectScope{All: true}, false)
		want := map[string]int{"agent-a@0": 2, "agent-b@0": 1}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})

	t.Run("byDay buckets at UTC midnight", func(t *testing.T) {
		got := count(t, agents, SubjectScope{All: true}, true)
		want := map[string]int{
			fmt.Sprintf("agent-a@%d", rollupDay0): 1,
			fmt.Sprintf("agent-a@%d", day1):       1,
			fmt.Sprintf("agent-b@%d", rollupDay0): 1,
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})

	t.Run("handler scope narrows", func(t *testing.T) {
		got := count(t, agents, SubjectScope{IDs: []string{"agent-b"}}, false)
		if want := map[string]int{"agent-b@0": 1}; !reflect.DeepEqual(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})

	t.Run("empty scope returns nothing", func(t *testing.T) {
		rows, err := repo.CountHandlerTasks(ctx, agents, SubjectScope{}, 0)
		if err != nil {
			t.Fatalf("CountHandlerTasks: %v", err)
		}
		if rows == nil || len(rows) != 0 {
			t.Errorf("rows = %#v, want empty non-nil slice", rows)
		}
	})

	t.Run("workflow roots count under their own type", func(t *testing.T) {
		wfOpts := TaskAnalyticsOpts{StartDate: agents.StartDate, EndDate: agents.EndDate, Kind: TaskKindNormal, HandlerType: TaskHandlerTypeWorkflow}
		if got, want := count(t, wfOpts, SubjectScope{All: true}, false), map[string]int{"wf-1@0": 1}; !reflect.DeepEqual(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})

	t.Run("window excludes tasks outside it", func(t *testing.T) {
		narrow := agents
		narrow.StartDate, narrow.EndDate = rollupDay0+1_500, day1
		if got, want := count(t, narrow, SubjectScope{All: true}, false), map[string]int{"agent-b@0": 1}; !reflect.DeepEqual(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})
}

func TestCountHandlerTasksBucketsByRequestedWidth(t *testing.T) {
	db := newTestDB(t)
	tasks := NewTaskRepo(db)
	repo := NewAnalyticsRepo(db)
	ctx := t.Context()

	hourMs := int64(time.Hour / time.Millisecond)
	seedHandlerTask(t, tasks, "agent-a", TaskHandlerTypeAgent, rollupDay0+2*hourMs, nil)
	seedHandlerTask(t, tasks, "agent-a", TaskHandlerTypeAgent, rollupDay0+5*hourMs, nil)

	opts := TaskAnalyticsOpts{
		StartDate: rollupDay0, EndDate: rollupDay0 + DayBucketMs, Kind: TaskKindNormal,
		HandlerType: TaskHandlerTypeAgent,
	}

	byDay, err := repo.CountHandlerTasks(ctx, opts, SubjectScope{All: true}, DayBucketMs)
	if err != nil {
		t.Fatalf("day: %v", err)
	}
	if len(byDay) != 1 || byDay[0].Count != 2 || byDay[0].BucketStartMs != rollupDay0 {
		t.Fatalf("day width: want one bucket of 2 at %d, got %+v", rollupDay0, byDay)
	}

	byHour, err := repo.CountHandlerTasks(ctx, opts, SubjectScope{All: true}, HourBucketMs)
	if err != nil {
		t.Fatalf("hour: %v", err)
	}
	if len(byHour) != 2 {
		t.Fatalf("hour width: want two buckets, got %+v", byHour)
	}
	for _, b := range byHour {
		if b.Count != 1 {
			t.Fatalf("hour width: want 1 per bucket, got %+v", byHour)
		}
		if b.BucketStartMs%HourBucketMs != 0 {
			t.Fatalf("hour width: bucket %d is not on an hour boundary", b.BucketStartMs)
		}
	}
}

func seedRollupFeedback(t *testing.T, db *DB, tasks *TaskRepo, subjectType, subjectID, rating string, taskStart, createdAt int64) {
	t.Helper()
	taskID := seedHandlerTask(t, tasks, subjectID, subjectType, taskStart, nil)
	SeedFeedback(t, db, &Feedback{
		TaskID: taskID, UserID: "rater", SessionID: idgen.New(), Rating: rating,
		SubjectID: subjectID, SubjectType: subjectType, CreatedAt: createdAt, UpdatedAt: createdAt,
	})
}

func seedOrphanRollupFeedback(t *testing.T, db *DB, subjectType, subjectID, rating string, createdAt int64) {
	t.Helper()
	SeedFeedback(t, db, &Feedback{
		TaskID: idgen.New(), UserID: "rater", SessionID: idgen.New(), Rating: rating,
		SubjectID: subjectID, SubjectType: subjectType, CreatedAt: createdAt, UpdatedAt: createdAt,
	})
}

func feedbackCounts(rows []SubjectFeedbackCount) map[string][2]int {
	out := make(map[string][2]int, len(rows))
	for _, r := range rows {
		out[fmt.Sprintf("%s:%s@%d", r.SubjectType, r.SubjectID, r.BucketStartMs)] = [2]int{r.Positive, r.Negative}
	}
	return out
}

func TestAnalyticsRepo_AggregateFeedbackBySubject(t *testing.T) {
	db := newTestDB(t)
	tasks := NewTaskRepo(db)
	repo := NewAnalyticsRepo(db)
	ctx := t.Context()

	day1 := rollupDay0 + rollupDayMs
	seedRollupFeedback(t, db, tasks, SubjectTypeAgent, "agent-1", "up", rollupDay0+1_000, rollupDay0+1_000)
	seedRollupFeedback(t, db, tasks, SubjectTypeAgent, "agent-1", "down", rollupDay0+2_000, rollupDay0+2_000)
	seedRollupFeedback(t, db, tasks, SubjectTypeAgent, "agent-2", "up", rollupDay0+3_000, rollupDay0+3_000)
	seedRollupFeedback(t, db, tasks, SubjectTypeWorkflow, "wf-1", "down", rollupDay0+4_000, rollupDay0+4_000)
	seedOrphanRollupFeedback(t, db, SubjectTypeUnknown, "legacy", "down", rollupDay0+5_000)
	seedOrphanRollupFeedback(t, db, SubjectTypeAgent, "deleted-task-agent", "down", rollupDay0+6_000)

	window := CrossUserFeedbackFilter{StartDate: rollupDay0, EndDate: rollupDay0 + 2*rollupDayMs}
	everything := FeedbackVisibility{Agents: SubjectScope{All: true}, Workflows: SubjectScope{All: true}}
	agentOne := FeedbackVisibility{Agents: SubjectScope{IDs: []string{"agent-1"}}}
	aggregate := func(t *testing.T, vis FeedbackVisibility, f CrossUserFeedbackFilter, byDay bool) map[string][2]int {
		t.Helper()
		rows, err := repo.AggregateFeedbackBySubject(ctx, vis, f, bucketWidthFor(byDay))
		if err != nil {
			t.Fatalf("AggregateFeedbackBySubject: %v", err)
		}
		return feedbackCounts(rows)
	}
	allTotals := map[string][2]int{"agent:agent-1@0": {1, 1}, "agent:agent-2@0": {1, 0}, "workflow:wf-1@0": {0, 1}}

	t.Run("counts both ratings per subject and drops unknown rows", func(t *testing.T) {
		if got := aggregate(t, everything, window, false); !reflect.DeepEqual(got, allTotals) {
			t.Errorf("got %v, want %v", got, allTotals)
		}
	})

	t.Run("reach narrows exactly like the cross-user list", func(t *testing.T) {
		if got, want := aggregate(t, agentOne, window, false), map[string][2]int{"agent:agent-1@0": {1, 1}}; !reflect.DeepEqual(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})

	t.Run("a rating filter on the input is ignored", func(t *testing.T) {
		rated := window
		rated.Rating = "down"
		if got := aggregate(t, everything, rated, false); !reflect.DeepEqual(got, allTotals) {
			t.Errorf("got %v, want %v", got, allTotals)
		}
	})

	t.Run("no reach returns nothing", func(t *testing.T) {
		rows, err := repo.AggregateFeedbackBySubject(ctx, FeedbackVisibility{}, window, 0)
		if err != nil {
			t.Fatalf("AggregateFeedbackBySubject: %v", err)
		}
		if rows == nil || len(rows) != 0 {
			t.Errorf("rows = %#v, want empty non-nil slice", rows)
		}
	})

	t.Run("orphan feedback with no surviving task is excluded, even when its subject_type is a real kind the caller can read", func(t *testing.T) {
		got := aggregate(t, everything, window, false)
		if _, ok := got["agent:deleted-task-agent@0"]; ok {
			t.Errorf("got %v, want no agent:deleted-task-agent row: its task_id matches no task", got)
		}
		if _, ok := got["unknown:legacy@0"]; ok {
			t.Errorf("got %v, want no unknown:legacy row: its task_id matches no task", got)
		}
	})

	t.Run("a task outside the window excludes its feedback even when the feedback's own createdAt is inside it", func(t *testing.T) {
		seedRollupFeedback(t, db, tasks, SubjectTypeAgent, "agent-late-rating", "down", rollupDay0-rollupDayMs, rollupDay0+1_000)
		got := aggregate(t, everything, window, false)
		if _, ok := got["agent:agent-late-rating@0"]; ok {
			t.Errorf("got %v, want agent-late-rating excluded: its task's start_time is outside the window", got)
		}
	})

	t.Run("bucket follows the task's start_time, not the feedback row's own createdAt", func(t *testing.T) {
		seedRollupFeedback(t, db, tasks, SubjectTypeAgent, "agent-late-feedback", "down", rollupDay0+6_000, day1+6_000)
		got := aggregate(t, everything, window, true)
		if c, ok := got[fmt.Sprintf("agent:agent-late-feedback@%d", rollupDay0)]; !ok || c != [2]int{0, 1} {
			t.Errorf("got %v at day0, want {0,1}: the row's own task started on day0", got)
		}
		if _, ok := got[fmt.Sprintf("agent:agent-late-feedback@%d", day1)]; ok {
			t.Errorf("got %v, want no day1 bucket for agent-late-feedback: that is its createdAt, not its task's start_time", got)
		}
	})
}

func seedRaterOn(t *testing.T, db *DB, taskID, subjectType, subjectID, rating, rater string, createdAt int64) {
	t.Helper()
	SeedFeedback(t, db, &Feedback{
		TaskID: taskID, UserID: rater, SessionID: idgen.New(), Rating: rating,
		SubjectID: subjectID, SubjectType: subjectType, CreatedAt: createdAt, UpdatedAt: createdAt,
	})
}

func TestAnalyticsRepo_AggregateFeedbackBySubject_CountsTasksNotRatingRows(t *testing.T) {
	db := newTestDB(t)
	tasks := NewTaskRepo(db)
	repo := NewAnalyticsRepo(db)
	ctx := t.Context()

	taskID := seedHandlerTask(t, tasks, "agent-crowded", SubjectTypeAgent, rollupDay0+1_000, nil)
	for _, rater := range []string{"rater-a", "rater-b", "rater-c"} {
		seedRaterOn(t, db, taskID, SubjectTypeAgent, "agent-crowded", "down", rater, rollupDay0+1_000)
	}
	splitTask := seedHandlerTask(t, tasks, "agent-split", SubjectTypeAgent, rollupDay0+2_000, nil)
	seedRaterOn(t, db, splitTask, SubjectTypeAgent, "agent-split", "up", "rater-a", rollupDay0+2_000)
	seedRaterOn(t, db, splitTask, SubjectTypeAgent, "agent-split", "down", "rater-b", rollupDay0+2_000)

	window := CrossUserFeedbackFilter{StartDate: rollupDay0, EndDate: rollupDay0 + 2*rollupDayMs}
	everything := FeedbackVisibility{Agents: SubjectScope{All: true}, Workflows: SubjectScope{All: true}}
	rows, err := repo.AggregateFeedbackBySubject(ctx, everything, window, 0)
	if err != nil {
		t.Fatalf("AggregateFeedbackBySubject: %v", err)
	}
	got := feedbackCounts(rows)

	if c, want := got["agent:agent-crowded@0"], ([2]int{0, 1}); c != want {
		t.Errorf("three raters on one task: got %v, want %v — one rated task, not three ratings", c, want)
	}
	if c, want := got["agent:agent-split@0"], ([2]int{1, 1}); c != want {
		t.Errorf("one task rated up and down: got %v, want %v", c, want)
	}
}

func TestAnalyticsRepo_AggregateFeedbackBySubject_CountsOnlyTasksTheDenominatorCounts(t *testing.T) {
	db := newTestDB(t)
	tasks := NewTaskRepo(db)
	repo := NewAnalyticsRepo(db)
	ctx := t.Context()

	parent := seedHandlerTask(t, tasks, "wf-parent", SubjectTypeWorkflow, rollupDay0+1_000, nil)
	nested := seedHandlerTask(t, tasks, "agent-nested", SubjectTypeAgent, rollupDay0+2_000, &parent)
	seedRaterOn(t, db, nested, SubjectTypeAgent, "agent-nested", "down", "rater-a", rollupDay0+2_000)

	offKind := seedHandlerTask(t, tasks, "agent-offkind", SubjectTypeAgent, rollupDay0+3_000, nil)
	if _, err := db.ExecContext(ctx, `UPDATE tasks SET kind = ? WHERE id = ?`, TaskKindCompactionCost, offKind); err != nil {
		t.Fatalf("retag task kind: %v", err)
	}
	seedRaterOn(t, db, offKind, SubjectTypeAgent, "agent-offkind", "down", "rater-a", rollupDay0+3_000)

	window := CrossUserFeedbackFilter{StartDate: rollupDay0, EndDate: rollupDay0 + 2*rollupDayMs}
	everything := FeedbackVisibility{Agents: SubjectScope{All: true}, Workflows: SubjectScope{All: true}}
	rows, err := repo.AggregateFeedbackBySubject(ctx, everything, window, 0)
	if err != nil {
		t.Fatalf("AggregateFeedbackBySubject: %v", err)
	}
	got := feedbackCounts(rows)

	if _, ok := got["agent:agent-nested@0"]; ok {
		t.Errorf("got %v, want no agent-nested row: the agent denominator excludes workflow-nested tasks", got)
	}
	if _, ok := got["agent:agent-offkind@0"]; ok {
		t.Errorf("got %v, want no agent-offkind row: the denominator counts kind=%q only", got, TaskKindNormal)
	}
}

func TestAnalyticsRepo_AggregateFeedbackBySubject_SubjectMustMatchItsTasksType(t *testing.T) {
	db := newTestDB(t)
	tasks := NewTaskRepo(db)
	repo := NewAnalyticsRepo(db)
	ctx := t.Context()

	crossType := seedHandlerTask(t, tasks, "wf-crosstype", SubjectTypeWorkflow, rollupDay0+1_000, nil)
	seedRaterOn(t, db, crossType, SubjectTypeAgent, "agent-crosstype", "down", "rater-a", rollupDay0+1_000)

	parent := seedHandlerTask(t, tasks, "wf-parent", SubjectTypeWorkflow, rollupDay0+2_000, nil)
	sub := seedHandlerTask(t, tasks, "wf-sub", SubjectTypeWorkflow, rollupDay0+3_000, &parent)
	seedRaterOn(t, db, sub, SubjectTypeWorkflow, "wf-sub", "down", "rater-b", rollupDay0+3_000)

	window := CrossUserFeedbackFilter{StartDate: rollupDay0, EndDate: rollupDay0 + 2*rollupDayMs}
	everything := FeedbackVisibility{Agents: SubjectScope{All: true}, Workflows: SubjectScope{All: true}}
	rows, err := repo.AggregateFeedbackBySubject(ctx, everything, window, 0)
	if err != nil {
		t.Fatalf("AggregateFeedbackBySubject: %v", err)
	}
	got := feedbackCounts(rows)

	if _, ok := got["agent:agent-crosstype@0"]; ok {
		t.Errorf("got %v, want no agent-crosstype row: an agent subject on a workflow-type task has no denominator", got)
	}
	if c, want := got["workflow:wf-sub@0"], ([2]int{0, 1}); c != want {
		t.Errorf("nested sub-workflow: got %v, want %v — the nested exclusion is agent-side only", c, want)
	}
}

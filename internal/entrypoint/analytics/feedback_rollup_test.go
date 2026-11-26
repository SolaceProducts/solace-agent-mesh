// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package analytics

import (
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

const deployedAgentID = "3f2b8c1e-0000-4000-8000-000000000001"

func seedRollupTask(t *testing.T, db *store.DB, handler, htype string, start int64, parent *string) string {
	t.Helper()
	return seedRollupTaskKind(t, db, handler, htype, store.TaskKindNormal, start, parent)
}

func seedRollupTaskKind(t *testing.T, db *store.DB, handler, htype, kind string, start int64, parent *string) string {
	t.Helper()
	return seedRollupTaskOwnedBy(t, db, "u1", handler, htype, kind, start, parent)
}

func seedRollupTaskOwnedBy(t *testing.T, db *store.DB, owner, handler, htype, kind string, start int64, parent *string) string {
	t.Helper()
	id := idgen.New()
	status, name, htypeVal := "completed", handler, htype
	zero := 0
	end := start + 100
	task := &store.Task{
		ID: id, UserID: owner, StartTime: start, EndTime: &end,
		Status: &status, ExecutionMode: "foreground", Kind: kind,
		HandlerName: &name, HandlerType: &htypeVal, ParentTaskID: parent,
		TotalInputTokens: &zero, TotalOutputTokens: &zero, TotalCachedInputTokens: &zero,
	}
	if err := store.NewTaskRepo(db).Create(t.Context(), task); err != nil {
		t.Fatalf("create task %s: %v", handler, err)
	}
	return id
}

func seedRollupTasks(t *testing.T, db *store.DB, handler, htype string, n int) []string {
	t.Helper()
	ids := make([]string, 0, n)
	for i := range n {
		ids = append(ids, seedRollupTask(t, db, handler, htype, trendDay0+int64(i)*1_000, nil))
	}
	return ids
}

func seedRollupRating(t *testing.T, db *store.DB, kind, subjectID, rating, taskID string, at int64) {
	t.Helper()
	store.SeedFeedback(t, db, &store.Feedback{
		TaskID: taskID, UserID: "rater-" + idgen.New(), SessionID: idgen.New(), Rating: rating,
		SubjectID: subjectID, SubjectType: kind, CreatedAt: at, UpdatedAt: at,
	})
}

func seedOrphanRollupRating(t *testing.T, db *store.DB, kind, subjectID, rating string, at int64) {
	t.Helper()
	store.SeedFeedback(t, db, &store.Feedback{
		TaskID: idgen.New(), UserID: "rater", SessionID: idgen.New(), Rating: rating,
		SubjectID: subjectID, SubjectType: kind, CreatedAt: at, UpdatedAt: at,
	})
}

func everyone() store.FeedbackVisibility {
	return store.FeedbackVisibility{Agents: store.SubjectScope{All: true}, Workflows: store.SubjectScope{All: true}}
}

func rollupQuery(vis store.FeedbackVisibility) FeedbackRollupQuery {
	return FeedbackRollupQuery{
		Visibility: vis, StartDate: trendDay0, EndDate: trendDay0 + 7*trendDayMs - 1,
		GroupBy: RollupGroupBySubject, SortBy: RollupSortNegativeRatio, SortDesc: true,
		PageNumber: 1, PageSize: 20, Trend: TrendNone,
	}
}

func runRollup(t *testing.T, svc *Service, q FeedbackRollupQuery) []FeedbackRollupRow {
	t.Helper()
	page, err := svc.FeedbackRollup(t.Context(), q)
	if err != nil {
		t.Fatalf("FeedbackRollup: %v", err)
	}
	return page.Data
}

func rollupRowFor(t *testing.T, rows []FeedbackRollupRow, id string) FeedbackRollupRow {
	t.Helper()
	for _, r := range rows {
		if r.Subject != nil && r.Subject.ID == id {
			return r
		}
	}
	t.Fatalf("no row for subject %q in %+v", id, rows)
	return FeedbackRollupRow{}
}

func rollupSubjectIDs(rows []FeedbackRollupRow) []string {
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.Subject.ID)
	}
	slices.Sort(out)
	return out
}

func TestFeedbackRollup_JoinsActivityAndFeedbackBySubject(t *testing.T) {
	db := store.TestDB(t)
	slug := a2a.InstanceName(a2a.KindAgent, deployedAgentID)
	svc := NewService(db).WithAgentNameResolver(func(s string) string {
		if s == slug {
			return "Procurement Intake Router"
		}
		return ""
	})
	deployedTasks := seedRollupTasks(t, db, slug, store.TaskHandlerTypeAgent, 4)
	seedRollupRating(t, db, store.SubjectTypeAgent, deployedAgentID, "up", deployedTasks[0], trendDay0+10)
	seedRollupRating(t, db, store.SubjectTypeAgent, deployedAgentID, "down", deployedTasks[1], trendDay0+20)
	helperTasks := seedRollupTasks(t, db, "HelperAgent", store.TaskHandlerTypeAgent, 2)
	seedRollupRating(t, db, store.SubjectTypeAgent, "HelperAgent", "down", helperTasks[0], trendDay0+30)

	rows := runRollup(t, svc, rollupQuery(everyone()))

	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2 (slug and UUID must join into one row): %+v", len(rows), rows)
	}
	deployed := rollupRowFor(t, rows, deployedAgentID)
	if deployed.activities() != 4 || deployed.PositiveFeedback != 1 || deployed.NegativeFeedback != 1 || deployed.NegativeRatio != 0.25 {
		t.Errorf("deployed row = %+v, want 4 activities, 1 up, 1 down, ratio 0.25", deployed)
	}
	if deployed.Subject.Name != "Procurement Intake Router" || deployed.Subject.Type != store.SubjectTypeAgent {
		t.Errorf("deployed subject = %+v", deployed.Subject)
	}
	helper := rollupRowFor(t, rows, "HelperAgent")
	if helper.activities() != 2 || helper.NegativeFeedback != 1 || helper.NegativeRatio != 0.5 {
		t.Errorf("plain-named row = %+v, want 2 activities, 1 down, ratio 0.5", helper)
	}
}

func TestFeedbackRollup_ActivityWithoutFeedbackStillAppears(t *testing.T) {
	db := store.TestDB(t)
	svc := NewService(db)
	seedRollupTasks(t, db, "QuietAgent", store.TaskHandlerTypeAgent, 3)

	rows := runRollup(t, svc, rollupQuery(everyone()))

	quiet := rollupRowFor(t, rows, "QuietAgent")
	if quiet.activities() != 3 || quiet.NegativeFeedback != 0 || quiet.NegativeRatio != 0 {
		t.Errorf("activity-only row = %+v, want 3 activities, 0 negative and ratio 0", quiet)
	}
	if quiet.Subject.Name != "QuietAgent" {
		t.Errorf("unregistered name = %q, want the subject id", quiet.Subject.Name)
	}
}

func TestFeedbackRollup_RatioNeverExceedsOne(t *testing.T) {
	db := store.TestDB(t)
	svc := NewService(db)
	tasks := seedRollupTasks(t, db, "agent-1", store.TaskHandlerTypeAgent, 2)
	for i := range 3 {
		seedRollupRating(t, db, store.SubjectTypeAgent, "agent-1", "down", tasks[0], trendDay0+int64(i))
	}

	row := rollupRowFor(t, runRollup(t, svc, rollupQuery(everyone())), "agent-1")

	if row.activities() != 2 || row.NegativeFeedback != 1 || row.NegativeRatio != 0.5 {
		t.Errorf("row = %+v, want 2 activities, 1 negative (one rated task, not three ratings), ratio 0.5", row)
	}
	if row.NegativeRatio > 1 {
		t.Errorf("negativeRatio = %v, want at most 1", row.NegativeRatio)
	}
}

func TestFeedbackRollup_OrphanedRatingNeverAppears(t *testing.T) {
	db := store.TestDB(t)
	svc := NewService(db)
	seedRollupTasks(t, db, "agent-1", store.TaskHandlerTypeAgent, 3)
	seedOrphanRollupRating(t, db, store.SubjectTypeAgent, "agent-1", "down", trendDay0+10)
	seedOrphanRollupRating(t, db, store.SubjectTypeAgent, "DeletedTaskAgent", "down", trendDay0+20)

	rows := runRollup(t, svc, rollupQuery(everyone()))

	agentOne := rollupRowFor(t, rows, "agent-1")
	if agentOne.activities() != 3 || agentOne.NegativeFeedback != 0 || agentOne.NegativeRatio != 0 {
		t.Errorf("agent-1 = %+v, want 3 activities, 0 negative: its rating's task does not exist", agentOne)
	}
	for _, r := range rows {
		if r.Subject.ID == "DeletedTaskAgent" {
			t.Errorf("rows = %+v, want no row for DeletedTaskAgent: it has only an orphaned rating, no activity and no surviving task", rows)
		}
	}
}

func TestFeedbackRollup_TenantTotalsStayInsideReach(t *testing.T) {
	db := store.TestDB(t)
	svc := NewService(db)
	oneTasks := seedRollupTasks(t, db, "agent-1", store.TaskHandlerTypeAgent, 10)
	seedRollupRating(t, db, store.SubjectTypeAgent, "agent-1", "down", oneTasks[0], trendDay0+10)
	twoTasks := seedRollupTasks(t, db, "agent-2", store.TaskHandlerTypeAgent, 5)
	seedRollupRating(t, db, store.SubjectTypeAgent, "agent-2", "down", twoTasks[0], trendDay0+20)
	seedRollupRating(t, db, store.SubjectTypeAgent, "agent-2", "down", twoTasks[1], trendDay0+30)

	q := rollupQuery(store.FeedbackVisibility{Agents: store.SubjectScope{IDs: []string{"agent-1"}}})
	q.GroupBy = RollupGroupByTenant
	page, err := svc.FeedbackRollup(t.Context(), q)
	if err != nil {
		t.Fatalf("FeedbackRollup: %v", err)
	}
	rows := page.Data

	if len(rows) != 1 || rows[0].Subject != nil {
		t.Fatalf("tenant rows = %+v, want exactly one row with no subject", rows)
	}
	if got := rows[0]; got.activities() != 10 || got.NegativeFeedback != 1 || got.NegativeRatio != 0.1 {
		t.Errorf("tenant row = %+v, want only agent-1: 10 activities, 1 down, ratio 0.1", got)
	}
	if page.Meta.Pagination.PageSize != q.PageSize {
		t.Errorf("pageSize = %d, want the caller's %d echoed back", page.Meta.Pagination.PageSize, q.PageSize)
	}
}

func TestFeedbackRollup_KindReachesAreIndependent(t *testing.T) {
	db := store.TestDB(t)
	svc := NewService(db)
	seedRollupTasks(t, db, "agent-1", store.TaskHandlerTypeAgent, 1)
	seedRollupTasks(t, db, "wf-1", store.TaskHandlerTypeWorkflow, 1)
	seedRollupTasks(t, db, "wf-2", store.TaskHandlerTypeWorkflow, 1)

	vis := store.FeedbackVisibility{Agents: store.SubjectScope{All: true}, Workflows: store.SubjectScope{IDs: []string{"wf-1"}}}
	got := rollupSubjectIDs(runRollup(t, svc, rollupQuery(vis)))

	if want := []string{"agent-1", "wf-1"}; !reflect.DeepEqual(got, want) {
		t.Errorf("subjects = %v, want %v", got, want)
	}
}

func TestFeedbackRollup_RequestedIDsNarrowNeverWiden(t *testing.T) {
	db := store.TestDB(t)
	svc := NewService(db)
	seedRollupTasks(t, db, "agent-1", store.TaskHandlerTypeAgent, 1)
	seedRollupTasks(t, db, "agent-2", store.TaskHandlerTypeAgent, 1)
	seedRollupTasks(t, db, "wf-1", store.TaskHandlerTypeWorkflow, 1)

	t.Run("an id outside the reach is dropped", func(t *testing.T) {
		q := rollupQuery(store.FeedbackVisibility{Agents: store.SubjectScope{IDs: []string{"agent-1"}}})
		q.AgentIDs = []string{"agent-2"}
		if rows := runRollup(t, svc, q); len(rows) != 0 {
			t.Errorf("rows = %+v, want none", rows)
		}
	})

	t.Run("naming only agents drops the workflows the caller also holds", func(t *testing.T) {
		q := rollupQuery(everyone())
		q.AgentIDs = []string{"agent-1"}
		if got, want := rollupSubjectIDs(runRollup(t, svc, q)), []string{"agent-1"}; !reflect.DeepEqual(got, want) {
			t.Errorf("subjects = %v, want %v", got, want)
		}
	})

	t.Run("subjectType narrows to one kind", func(t *testing.T) {
		q := rollupQuery(everyone())
		q.SubjectType = store.SubjectTypeWorkflow
		if got, want := rollupSubjectIDs(runRollup(t, svc, q)), []string{"wf-1"}; !reflect.DeepEqual(got, want) {
			t.Errorf("subjects = %v, want %v", got, want)
		}
	})
}

func TestFeedbackRollup_WorkflowNestedAgentRunsCountForTheWorkflow(t *testing.T) {
	db := store.TestDB(t)
	svc := NewService(db)
	root := seedRollupTask(t, db, "wf-1", store.TaskHandlerTypeWorkflow, trendDay0+1_000, nil)
	seedRollupTask(t, db, "agent-1", store.TaskHandlerTypeAgent, trendDay0+2_000, &root)

	rows := runRollup(t, svc, rollupQuery(everyone()))

	if got, want := rollupSubjectIDs(rows), []string{"wf-1"}; !reflect.DeepEqual(got, want) {
		t.Errorf("subjects = %v, want %v (the node run belongs to the workflow)", got, want)
	}
}

func TestFeedbackRollup_SortsWithATotalOrderAndPaginates(t *testing.T) {
	db := store.TestDB(t)
	svc := NewService(db)
	tasksByAgent := map[string][]string{}
	for _, id := range []string{"a-agent", "b-agent", "c-agent"} {
		tasksByAgent[id] = seedRollupTasks(t, db, id, store.TaskHandlerTypeAgent, 10)
	}
	seedRollupRating(t, db, store.SubjectTypeAgent, "c-agent", "down", tasksByAgent["c-agent"][0], trendDay0+10)
	seedRollupRating(t, db, store.SubjectTypeAgent, "c-agent", "down", tasksByAgent["c-agent"][1], trendDay0+20)
	seedRollupRating(t, db, store.SubjectTypeAgent, "a-agent", "down", tasksByAgent["a-agent"][0], trendDay0+30)

	q := rollupQuery(everyone())
	q.PageSize = 2
	first := runRollup(t, svc, q)
	q.PageNumber = 2
	second := runRollup(t, svc, q)

	got := []string{first[0].Subject.ID, first[1].Subject.ID, second[0].Subject.ID}
	if want := []string{"c-agent", "a-agent", "b-agent"}; !reflect.DeepEqual(got, want) {
		t.Errorf("order = %v, want %v (ratio desc, then name)", got, want)
	}

	byName := rollupQuery(everyone())
	byName.SortBy, byName.SortDesc = RollupSortName, false
	if got := runRollup(t, svc, byName)[0].Subject.ID; got != "a-agent" {
		t.Errorf("name asc first = %q, want a-agent", got)
	}
}

type recordingRollupStore struct {
	inner          rollupStore
	dayHandlers    []store.SubjectScope
	dayFeedbackVis []store.FeedbackVisibility
}

func (r *recordingRollupStore) CountHandlerTasks(ctx context.Context, opts store.TaskAnalyticsOpts, handlers store.SubjectScope, bucketMs int64) ([]store.HandlerTaskCount, error) {
	if bucketMs > 0 {
		r.dayHandlers = append(r.dayHandlers, handlers)
	}
	return r.inner.CountHandlerTasks(ctx, opts, handlers, bucketMs)
}

func (r *recordingRollupStore) AggregateFeedbackBySubject(ctx context.Context, vis store.FeedbackVisibility, f store.CrossUserFeedbackFilter, bucketMs int64) ([]store.SubjectFeedbackCount, error) {
	if bucketMs > 0 {
		r.dayFeedbackVis = append(r.dayFeedbackVis, vis)
	}
	return r.inner.AggregateFeedbackBySubject(ctx, vis, f, bucketMs)
}

func TestFeedbackRollup_TrendOnlyForReturnedPage(t *testing.T) {
	db := store.TestDB(t)
	svc := NewService(db)
	rec := &recordingRollupStore{inner: svc.rollupRepo}
	svc.rollupRepo = rec
	tasksByAgent := map[string][]string{}
	for _, id := range []string{"a-agent", "b-agent", "c-agent"} {
		tasksByAgent[id] = seedRollupTasks(t, db, id, store.TaskHandlerTypeAgent, 10)
	}
	seedRollupRating(t, db, store.SubjectTypeAgent, "b-agent", "down", tasksByAgent["b-agent"][0], trendDay0+10)

	q := rollupQuery(everyone())
	q.PageSize, q.Trend = 1, TrendDay
	rows := runRollup(t, svc, q)

	if len(rows) != 1 || rows[0].Subject.ID != "b-agent" {
		t.Fatalf("page = %+v, want only b-agent", rows)
	}
	wantVis := []store.FeedbackVisibility{{Agents: store.SubjectScope{IDs: []string{"b-agent"}}}}
	if !reflect.DeepEqual(rec.dayFeedbackVis, wantVis) {
		t.Errorf("bucketed feedback asked for %+v, want %+v", rec.dayFeedbackVis, wantVis)
	}
	for _, scope := range rec.dayHandlers {
		if scope.All || len(scope.IDs) > 1 {
			t.Errorf("bucketed activity asked for %+v, want at most the one page subject", scope)
		}
	}
}

func TestFeedbackRollup_TrendNoneSkipsBucketedQueries(t *testing.T) {
	db := store.TestDB(t)
	svc := NewService(db)
	rec := &recordingRollupStore{inner: svc.rollupRepo}
	svc.rollupRepo = rec
	seedRollupTasks(t, db, "agent-1", store.TaskHandlerTypeAgent, 1)

	for _, groupBy := range []RollupGroupBy{RollupGroupBySubject, RollupGroupByTenant} {
		q := rollupQuery(everyone())
		q.GroupBy = groupBy
		for _, row := range runRollup(t, svc, q) {
			if row.Trend == nil || len(row.Trend) != 0 {
				t.Errorf("%s trend = %#v, want empty non-nil", groupBy, row.Trend)
			}
		}
	}
	if len(rec.dayHandlers) != 0 || len(rec.dayFeedbackVis) != 0 {
		t.Errorf("bucketed calls made with TrendNone: %d activity, %d feedback", len(rec.dayHandlers), len(rec.dayFeedbackVis))
	}
}

func TestFeedbackRollup_TrendSumsMatchRowTotals(t *testing.T) {
	db := store.TestDB(t)
	svc := NewService(db)
	var day0Second string
	for _, offset := range []int64{0, 2, 6} {
		at := trendDay0 + offset*trendDayMs
		first := seedRollupTask(t, db, "agent-1", store.TaskHandlerTypeAgent, at+1_000, nil)
		second := seedRollupTask(t, db, "agent-1", store.TaskHandlerTypeAgent, at+2_000, nil)
		seedRollupRating(t, db, store.SubjectTypeAgent, "agent-1", "down", first, at+3_000)
		if offset == 0 {
			day0Second = second
		}
	}
	seedRollupRating(t, db, store.SubjectTypeAgent, "agent-1", "up", day0Second, trendDay0+4_000)

	for _, tc := range []struct {
		g      TrendGranularity
		points int
	}{{TrendDay, 7}, {TrendWeek, 2}} {
		t.Run(string(tc.g), func(t *testing.T) {
			q := rollupQuery(everyone())
			q.Trend = tc.g
			row := rollupRowFor(t, runRollup(t, svc, q), "agent-1")
			if len(row.Trend) != tc.points {
				t.Fatalf("points = %d, want %d (zero-filled)", len(row.Trend), tc.points)
			}
			var sum bucketCounts
			for _, p := range row.Trend {
				sum = sum.add(bucketCounts{*p.TotalActivities, p.PositiveFeedback, p.NegativeFeedback})
			}
			if want := (bucketCounts{row.activities(), row.PositiveFeedback, row.NegativeFeedback}); sum != want {
				t.Errorf("trend sums %+v, row totals %+v", sum, want)
			}
		})
	}
}

func TestFeedbackRollup_TrendHourBucketsByHour(t *testing.T) {
	db := store.TestDB(t)
	svc := NewService(db)
	seedRollupTask(t, db, "agent-1", store.TaskHandlerTypeAgent, trendDay0+2*store.HourBucketMs+1_000, nil)
	ratedAtHour5 := seedRollupTask(t, db, "agent-1", store.TaskHandlerTypeAgent, trendDay0+5*store.HourBucketMs+1_000, nil)
	seedRollupRating(t, db, store.SubjectTypeAgent, "agent-1", "down", ratedAtHour5, trendDay0+5*store.HourBucketMs+1_000)

	q := rollupQuery(everyone())
	q.StartDate, q.EndDate = trendDay0, trendDay0+6*store.HourBucketMs-1
	q.Trend = TrendHour
	row := rollupRowFor(t, runRollup(t, svc, q), "agent-1")

	if len(row.Trend) != 6 {
		t.Fatalf("points = %d, want 6 hourly buckets", len(row.Trend))
	}
	for i, p := range row.Trend {
		wantActivities, wantNegative := 0, 0
		switch i {
		case 2:
			wantActivities = 1
		case 5:
			wantActivities, wantNegative = 1, 1
		}
		if *p.TotalActivities != wantActivities || p.NegativeFeedback != wantNegative {
			t.Errorf("hour %d = %+v, want activities=%d negative=%d", i, p, wantActivities, wantNegative)
		}
	}
}

func TestFeedbackRollup_UserFilterNarrowsCountsAndActivities(t *testing.T) {
	db := store.TestDB(t)
	svc := NewService(db)
	rate := func(user, rating, taskID string, at int64) {
		store.SeedFeedback(t, db, &store.Feedback{
			TaskID: taskID, UserID: user, SessionID: idgen.New(), Rating: rating,
			SubjectID: "agent-x", SubjectType: store.SubjectTypeAgent, CreatedAt: at, UpdatedAt: at,
		})
	}
	var u1, u2 []string
	for i := range 4 {
		u1 = append(u1, seedRollupTaskOwnedBy(t, db, "user1", "agent-x", store.TaskHandlerTypeAgent, store.TaskKindNormal, trendDay0+int64(i)*trendDayMs+1_000, nil))
	}
	for i := range 2 {
		u2 = append(u2, seedRollupTaskOwnedBy(t, db, "user2", "agent-x", store.TaskHandlerTypeAgent, store.TaskKindNormal, trendDay0+int64(i)*trendDayMs+2_000, nil))
	}
	rate("user1", "down", u1[0], trendDay0+10)
	rate("user1", "down", u1[1], trendDay0+11)
	rate("user1", "up", u1[2], trendDay0+12)
	rate("user2", "down", u2[0], trendDay0+13)
	rate("user2", "down", u2[1], trendDay0+14)
	rate("user2", "down", u1[3], trendDay0+15)

	q := rollupQuery(everyone())
	q.UserID = "user1"
	q.Trend = TrendDay
	row := rollupRowFor(t, runRollup(t, svc, q), "agent-x")

	if row.NegativeFeedback != 2 || row.PositiveFeedback != 1 || row.activities() != 4 {
		t.Fatalf("row = %+v, want 2 down, 1 up, 4 activities", row)
	}
	var sum bucketCounts
	for _, p := range row.Trend {
		sum = sum.add(bucketCounts{*p.TotalActivities, p.PositiveFeedback, p.NegativeFeedback})
	}
	if want := (bucketCounts{4, 1, 2}); sum != want {
		t.Errorf("trend sums %+v, want %+v", sum, want)
	}
}

func TestFeedbackRollup_RatingsOnlyCarriesNoActivity(t *testing.T) {
	db := store.TestDB(t)
	svc := NewService(db)
	rate := func(rating, taskID string, at int64) {
		store.SeedFeedback(t, db, &store.Feedback{
			TaskID: taskID, UserID: "user1", SessionID: idgen.New(), Rating: rating,
			SubjectID: "agent-x", SubjectType: store.SubjectTypeAgent, CreatedAt: at, UpdatedAt: at,
		})
	}
	var tasks []string
	for i := range 4 {
		tasks = append(tasks, seedRollupTaskOwnedBy(t, db, "user1", "agent-x", store.TaskHandlerTypeAgent, store.TaskKindNormal, trendDay0+int64(i)*1_000, nil))
	}
	seedRollupTaskOwnedBy(t, db, "user1", "agent-quiet", store.TaskHandlerTypeAgent, store.TaskKindNormal, trendDay0+5_000, nil)
	rate("down", tasks[0], trendDay0+10)
	rate("up", tasks[1], trendDay0+11)

	q := rollupQuery(everyone())
	q.UserID = "user1"
	q.RatingsOnly = true
	q.Trend = TrendDay
	rows := runRollup(t, svc, q)

	if len(rows) != 1 || rows[0].Subject.ID != "agent-x" {
		t.Fatalf("rows = %+v, want only the rated agent-x", rows)
	}
	row := rows[0]
	if row.TotalActivities != nil || row.NegativeRatio != 0.5 || row.NegativeFeedback != 1 || row.PositiveFeedback != 1 {
		t.Errorf("row = %+v, want no activity, 1 up, 1 down, ratio 0.5", row)
	}
	for _, p := range row.Trend {
		if p.TotalActivities != nil {
			t.Errorf("trend point %+v carries an activity count", p)
		}
	}
	raw, err := json.Marshal(row)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(raw), "totalActivities") {
		t.Errorf("payload carries an activity count: %s", raw)
	}
}

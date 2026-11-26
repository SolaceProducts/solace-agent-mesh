// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package analytics

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
)

var (
	ErrInvalidQuery = errors.New("analytics: invalid query")
	ErrForbidden    = errors.New("analytics: forbidden")
)

const defaultRecentLimit = 20

const staleActivityGrace = 5 * time.Minute

type Service struct {
	repo                       *store.AnalyticsRepo
	rollupRepo                 rollupStore
	feedbackRepo               *store.FeedbackRepo
	taskRepo                   *store.TaskRepo
	taskEventRepo              *store.TaskEventRepo
	logger                     *slog.Logger
	agentName                  func(slug string) string
	taskRetentionDays          int
	activityFeedbackFetchLimit int
}

func NewService(db *store.DB) *Service {
	repo := store.NewAnalyticsRepo(db)
	return &Service{
		repo:          repo,
		rollupRepo:    repo,
		feedbackRepo:  store.NewFeedbackRepo(db),
		taskRepo:      store.NewTaskRepo(db),
		taskEventRepo: store.NewTaskEventRepo(db),
	}
}

func (s *Service) consentedTaskSet(ctx context.Context, includeContent bool, taskIDs []string) map[string]bool {
	if includeContent || s.feedbackRepo == nil || len(taskIDs) == 0 {
		return nil
	}
	consented, err := s.feedbackRepo.ConsentedTaskIDs(ctx, taskIDs)
	if err != nil {
		if s.logger != nil && !errors.Is(err, context.Canceled) {
			s.logger.Warn("analytics consent lookup failed; failing closed (content stays redacted)",
				"error", err, "taskCount", len(taskIDs))
		}
		return nil
	}
	return consented
}

func taskIDsOf(tasks []*store.Task) []string {
	ids := make([]string, 0, len(tasks))
	for _, t := range tasks {
		ids = append(ids, t.ID)
	}
	return ids
}

func (s *Service) WithAgentNameResolver(fn func(slug string) string) *Service {
	s.agentName = fn
	return s
}

func (s *Service) WithLogger(l *slog.Logger) *Service {
	s.logger = l
	return s
}

func (s *Service) WithTaskRetentionDays(days int) *Service {
	s.taskRetentionDays = days
	return s
}

func (s *Service) agentRef(slug string) AgentRef {
	name := slug
	if s.agentName != nil {
		if dn := s.agentName(slug); dn != "" {
			name = dn
		}
	}
	return AgentRef{ID: resourceIDFromSlug(slug), Name: name}
}

func (s *Service) agentMatches(slug, want string) bool {
	if slug == want || resourceIDFromSlug(slug) == want {
		return true
	}
	if s.agentName != nil {
		return s.agentName(slug) == want
	}
	return false
}

func resourceIDFromSlug(slug string) string {
	if id, _, ok := a2a.IDFromInstanceName(slug); ok {
		return id
	}
	return slug
}

type Query struct {
	StartDate      int64
	EndDate        int64
	ExecutionMode  string
	PageNumber     int
	PageSize       int
	ThresholdMs    int64
	GroupBy        string
	Namespace      string
	AgentName      string
	UserID         string
	Status         string
	GroupDims      []string
	IncludeContent bool
	HandlerType    string
	OwnerUserID    string
}

func (q Query) taskOpts() store.TaskAnalyticsOpts {
	return store.TaskAnalyticsOpts{
		StartDate:     q.StartDate,
		EndDate:       q.EndDate,
		Kind:          store.TaskKindNormal,
		ExecutionMode: q.ExecutionMode,
		HandlerType:   q.HandlerType,
		OwnerUserID:   q.OwnerUserID,
	}
}

func (q Query) window() Window {
	return Window{StartDate: optISO(q.StartDate), EndDate: optISO(q.EndDate)}
}

func (q Query) offset() int {
	if q.PageNumber < 1 {
		return 0
	}
	return (q.PageNumber - 1) * q.PageSize
}

func (s *Service) TaskHealth(ctx context.Context, q Query) (*TaskHealthSummary, error) {
	opts := q.taskOpts()
	opts.StaleActivityBeforeMs = time.Now().Add(-staleActivityGrace).UnixMilli()
	agg, err := s.repo.AggregateTaskHealth(ctx, opts)
	if err != nil {
		return nil, err
	}
	total := 0
	for _, c := range agg.ByStatus {
		total += c
	}
	inProgress := agg.ByStatus[store.TaskStatusInProgress]
	rateDenominator := total - inProgress - agg.ByStatus[store.TaskStatusStale]
	success := agg.ByStatus[store.SuccessTaskStatus]
	errored := 0
	for _, st := range store.FailedTaskStatuses {
		errored += agg.ByStatus[st]
	}
	avg, p50, p95 := durationStats(agg.DurationsMs)
	return &TaskHealthSummary{
		Window:                q.window(),
		TotalTasks:            total,
		ByStatus:              agg.ByStatus,
		SuccessRate:           ratio(success, rateDenominator),
		ErrorRate:             ratio(errored, rateDenominator),
		DurationMeasuredCount: len(agg.DurationsMs),
		AvgDurationMs:         avg,
		P50DurationMs:         p50,
		P95DurationMs:         p95,
		ActiveBackgroundCount: agg.ActiveBackgroundCount,
		TaskRetentionDays:     s.taskRetentionDays,
		DurationBucketCounts:  bucketDurations(agg.DurationsMs),
	}, nil
}

func bucketDurations(sortedMs []int64) []int {
	if len(sortedMs) == 0 {
		return nil
	}
	edges := DurationBucketEdgesMs
	counts := make([]int, len(edges)+1)
	for _, d := range sortedMs {
		i := 0
		for i < len(edges) && d >= edges[i] {
			i++
		}
		counts[i]++
	}
	return counts
}

func (s *Service) FailedTasks(ctx context.Context, q Query) (*Paginated[TaskHealthRow], error) {
	opts := q.taskOpts()
	tasks, total, err := s.repo.ListFailedTasks(ctx, opts, q.PageSize, q.offset())
	if err != nil {
		return nil, err
	}
	consented := s.consentedTaskSet(ctx, q.IncludeContent, taskIDsOf(tasks))
	resp := paginate(s.taskRows(tasks, q.IncludeContent, consented), total, q.PageNumber, q.PageSize)
	activity, err := s.repo.CountTasksInWindow(ctx, opts)
	if err != nil {
		return nil, err
	}
	resp.Meta.WindowActivityCount = &activity
	return resp, nil
}

func (s *Service) SlowTasks(ctx context.Context, q Query) (*Paginated[TaskHealthRow], error) {
	tasks, total, err := s.repo.ListSlowTasks(ctx, q.taskOpts(), q.ThresholdMs, q.PageSize, q.offset())
	if err != nil {
		return nil, err
	}
	consented := s.consentedTaskSet(ctx, q.IncludeContent, taskIDsOf(tasks))
	return paginate(s.taskRows(tasks, q.IncludeContent, consented), total, q.PageNumber, q.PageSize), nil
}

func (s *Service) AgentPerformance(ctx context.Context, q Query) (*Paginated[AgentPerformanceRow], error) {
	aggRows, err := s.repo.AggregateAgentPerformance(ctx, q.Namespace, q.taskOpts())
	if err != nil {
		return nil, err
	}
	rows := make([]AgentPerformanceRow, 0, len(aggRows))
	for _, r := range aggRows {
		avg, _, p95 := durationStats(sortedCopy(r.DurationsMs))
		rows = append(rows, AgentPerformanceRow{
			Agent:                 s.agentRef(r.AgentName),
			InvocationCount:       r.InvocationCnt,
			ErrorCount:            r.ErrorCnt,
			SuccessRate:           ratio(r.InvocationCnt-r.ErrorCnt, r.InvocationCnt),
			AvgDurationMs:         avg,
			P95DurationMs:         p95,
			DurationMeasuredCount: len(r.DurationsMs),
		})
	}
	slices.SortFunc(rows, func(a, b AgentPerformanceRow) int { return b.InvocationCount - a.InvocationCount })
	return paginateSlice(rows, q.PageNumber, q.PageSize), nil
}

func (s *Service) ActivityAgentPerformance(ctx context.Context, q Query) (*Paginated[AgentPerformanceRow], error) {
	opts := q.taskOpts()
	opts.HandlerType = store.TaskHandlerTypeAgent
	opts.ExcludeWorkflowNested = true
	aggRows, err := s.repo.AggregateActivityAgents(ctx, opts)
	if err != nil {
		return nil, err
	}
	rows := make([]AgentPerformanceRow, 0, len(aggRows))
	for _, r := range aggRows {
		avg, _, p95 := durationStats(sortedCopy(r.DurationsMs))
		rows = append(rows, AgentPerformanceRow{
			Agent:                  s.handlerRef(r.HandlerName, r.HandlerType),
			InvocationCount:        r.InvocationCnt,
			ErrorCount:             r.ErrorCnt,
			SuccessRate:            ratio(r.InvocationCnt-r.ErrorCnt, r.InvocationCnt),
			AvgDurationMs:          avg,
			P95DurationMs:          p95,
			DurationMeasuredCount:  len(r.DurationsMs),
			TotalInputTokens:       r.InputTokens,
			TotalOutputTokens:      r.OutputTokens,
			TotalCachedInputTokens: r.CachedInputTokens,
		})
	}
	slices.SortFunc(rows, func(a, b AgentPerformanceRow) int {
		return cmp.Or(
			cmp.Compare(b.InvocationCount, a.InvocationCount),
			cmp.Compare(a.Agent.Name, b.Agent.Name),
		)
	})
	return paginateSlice(rows, q.PageNumber, q.PageSize), nil
}

func (s *Service) ActivityWorkflowPerformance(ctx context.Context, q Query) (*Paginated[AgentPerformanceRow], error) {
	opts := q.taskOpts()
	opts.HandlerType = store.TaskHandlerTypeWorkflow
	aggRows, err := s.repo.AggregateActivityWorkflows(ctx, opts)
	if err != nil {
		return nil, err
	}
	rows := make([]AgentPerformanceRow, 0, len(aggRows))
	for _, r := range aggRows {
		avg, _, p95 := durationStats(sortedCopy(r.DurationsMs))
		rows = append(rows, AgentPerformanceRow{
			Agent:                  s.handlerRef(r.HandlerName, r.HandlerType),
			InvocationCount:        r.InvocationCnt,
			ErrorCount:             r.ErrorCnt,
			SuccessRate:            ratio(r.InvocationCnt-r.ErrorCnt, r.InvocationCnt),
			AvgDurationMs:          avg,
			P95DurationMs:          p95,
			DurationMeasuredCount:  len(r.DurationsMs),
			TotalInputTokens:       r.InputTokens,
			TotalOutputTokens:      r.OutputTokens,
			TotalCachedInputTokens: r.CachedInputTokens,
		})
	}
	slices.SortFunc(rows, func(a, b AgentPerformanceRow) int {
		return cmp.Or(
			cmp.Compare(b.InvocationCount, a.InvocationCount),
			cmp.Compare(a.Agent.Name, b.Agent.Name),
		)
	})
	return paginateSlice(rows, q.PageNumber, q.PageSize), nil
}

func (s *Service) ActivityWorkflowBreakdown(ctx context.Context, q Query) (*Paginated[WorkflowBreakdownRow], error) {
	aggRows, err := s.repo.AggregateActivityWorkflowBreakdown(ctx, q.taskOpts())
	if err != nil {
		return nil, err
	}
	rows := make([]WorkflowBreakdownRow, 0, len(aggRows))
	for _, r := range aggRows {
		avg, _, p95 := durationStats(sortedCopy(r.DurationsMs))
		rows = append(rows, WorkflowBreakdownRow{
			Workflow:               s.handlerRef(r.WorkflowName, store.TaskHandlerTypeWorkflow),
			Agent:                  s.handlerRef(r.HandlerName, r.HandlerType),
			InvocationCount:        r.InvocationCnt,
			ErrorCount:             r.ErrorCnt,
			SuccessRate:            ratio(r.InvocationCnt-r.ErrorCnt, r.InvocationCnt),
			AvgDurationMs:          avg,
			P95DurationMs:          p95,
			TotalInputTokens:       r.InputTokens,
			TotalOutputTokens:      r.OutputTokens,
			TotalCachedInputTokens: r.CachedInputTokens,
		})
	}
	slices.SortFunc(rows, func(a, b WorkflowBreakdownRow) int {
		return cmp.Or(
			cmp.Compare(a.Workflow.Name, b.Workflow.Name),
			cmp.Compare(b.InvocationCount, a.InvocationCount),
			cmp.Compare(a.Agent.Name, b.Agent.Name),
		)
	})
	return paginateSlice(rows, q.PageNumber, q.PageSize), nil
}

func (s *Service) handlerRef(name, handlerType string) AgentRef {
	ref := s.agentRef(name)
	ref.Type = handlerType
	return ref
}

func (s *Service) subjectRef(subjectID, subjectType string) AgentRef {
	name := subjectID
	if s.agentName != nil {
		kind := a2a.KindAgent
		if subjectType == store.SubjectTypeWorkflow {
			kind = a2a.KindWorkflow
		}
		if dn := s.agentName(a2a.InstanceName(kind, subjectID)); dn != "" {
			name = dn
		} else if dn := s.agentName(subjectID); dn != "" {
			name = dn
		}
	}
	return AgentRef{ID: subjectID, Name: name, Type: subjectType}
}

func (s *Service) TokenUsage(ctx context.Context, q Query) (*Paginated[TokenUsageRow], error) {
	groupBy := store.TokenUsageGroupBy(q.GroupBy)
	if q.GroupBy == "" {
		groupBy = store.TokenGroupByDay
	}
	if groupBy == store.TokenGroupByUser && !q.IncludeContent {
		return nil, fmt.Errorf("%w: groupBy=user requires content-tier scope", ErrForbidden)
	}
	aggs, err := s.repo.AggregateTokens(ctx, q.taskOpts(), groupBy)
	if err != nil {
		return nil, err
	}
	rows := make([]TokenUsageRow, 0, len(aggs))
	for _, a := range aggs {
		var period *string
		key := a.GroupKey
		if a.PeriodStartMs > 0 {
			period = optISO(a.PeriodStartMs)
			if key == "" {
				key = *period
			}
		}
		rows = append(rows, TokenUsageRow{
			GroupKey:               key,
			PeriodStart:            period,
			TotalInputTokens:       a.InputTokens,
			TotalOutputTokens:      a.OutputTokens,
			TotalCachedInputTokens: a.CachedTokens,
			TaskCount:              a.TaskCount,
		})
	}
	return paginateSlice(rows, q.PageNumber, q.PageSize), nil
}

func (s *Service) ScheduledHealth(ctx context.Context, q Query) (*ScheduledTaskHealth, error) {
	agg, err := s.repo.AggregateScheduledHealth(ctx, q.Namespace, q.StartDate, q.EndDate, defaultRecentLimit)
	if err != nil {
		return nil, err
	}
	failures := make([]ScheduledFailureDTO, 0, len(agg.RecentFailures))
	for _, f := range agg.RecentFailures {
		dto := ScheduledFailureDTO{
			ScheduledTaskID:         f.ScheduledTaskID,
			Name:                    f.Name,
			ConsecutiveFailureCount: f.ConsecutiveFailureCount,
		}
		if q.IncludeContent {
			dto.LastError = f.LastError
		}
		failures = append(failures, dto)
	}
	return &ScheduledTaskHealth{
		TotalEnabled:   agg.TotalEnabled,
		TotalDisabled:  agg.TotalDisabled,
		Executions:     agg.ExecByStatus,
		RecentFailures: failures,
	}, nil
}

func (s *Service) FeedbackSummary(ctx context.Context, q Query) (*FeedbackSummary, error) {
	agg, err := s.repo.AggregateFeedback(ctx, q.StartDate, q.EndDate, defaultRecentLimit)
	if err != nil {
		return nil, err
	}
	comments := make([]FeedbackCommentDTO, 0, len(agg.RecentComments))
	for _, c := range agg.RecentComments {
		dto := FeedbackCommentDTO{
			TaskID:    c.TaskID,
			Rating:    c.Rating,
			UpdatedAt: epochMsToISO8601(c.UpdatedAtMs),
		}
		if q.IncludeContent || c.ShareContent {
			dto.Comment = c.Comment
		}
		comments = append(comments, dto)
	}
	return &FeedbackSummary{Total: agg.Total, ByRating: agg.ByRating, RecentComments: comments}, nil
}

const defaultActivityFeedbackFetchLimit = 5000

func (s *Service) activityFeedbackLimit() int {
	if s.activityFeedbackFetchLimit > 0 {
		return s.activityFeedbackFetchLimit
	}
	return defaultActivityFeedbackFetchLimit
}

func (s *Service) ActivityFeedback(ctx context.Context, q Query) (*Paginated[ActivityFeedbackRow], error) {
	limit := s.activityFeedbackLimit()
	rows, err := s.feedbackRepo.Search(ctx, q.OwnerUserID, q.StartDate, q.EndDate, "", "", "", limit, 0)
	if err != nil {
		return nil, err
	}
	out := s.activityFeedbackRows(rows)
	total := len(out)
	return &Paginated[ActivityFeedbackRow]{
		Data: out,
		Meta: Meta{
			Pagination: Pagination{PageNumber: 1, PageSize: total, TotalPages: 1, Count: total},
			Truncated:  total >= limit,
		},
	}, nil
}

func normalizeTaskDims(dims []string) (entity []string, timeDim string, err error) {
	seen := map[string]bool{}
	for _, d := range dims {
		d = strings.TrimSpace(d)
		if d == "" || seen[d] {
			continue
		}
		seen[d] = true
		switch d {
		case "agent", "user", "status":
			entity = append(entity, d)
		case "day", "week", "month":
			if timeDim != "" {
				return nil, "", fmt.Errorf("analytics: at most one time grouping (day/week/month) allowed, got %q and %q", timeDim, d)
			}
			timeDim = d
		default:
			return nil, "", fmt.Errorf("analytics: invalid group dimension %q (allowed: agent, user, status, day, week, month)", d)
		}
	}
	return entity, timeDim, nil
}

func (s *Service) QueryTasks(ctx context.Context, q Query) (any, error) {
	entityDims, timeDim, err := normalizeTaskDims(q.GroupDims)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidQuery, err)
	}
	if (q.UserID != "" || slices.Contains(entityDims, "user")) && !q.IncludeContent {
		return nil, fmt.Errorf("%w: filtering or grouping by user requires the content tier (analytics:_:read_content)", ErrForbidden)
	}
	requireAgent := q.AgentName != "" || slices.Contains(entityDims, "agent")

	facts, truncated, err := s.repo.QueryTaskFacts(ctx, q.Namespace, store.TaskFactsOpts{
		StartDate:     q.StartDate,
		EndDate:       q.EndDate,
		ExecutionMode: q.ExecutionMode,
		Status:        q.Status,
		UserID:        q.UserID,
		RequireAgent:  requireAgent,
	})
	if err != nil {
		return nil, err
	}

	if q.AgentName != "" {
		kept := facts[:0]
		for _, f := range facts {
			if s.agentMatches(f.AgentName, q.AgentName) {
				kept = append(kept, f)
			}
		}
		facts = kept
	}

	if len(entityDims) == 0 && timeDim == "" {
		page := s.granularTasks(ctx, facts, q)
		page.Meta.Truncated = truncated
		return page, nil
	}
	page := s.groupedTasks(facts, entityDims, timeDim, q)
	page.Meta.Truncated = truncated
	return page, nil
}

func (s *Service) granularTasks(ctx context.Context, facts []store.TaskFact, q Query) *Paginated[TaskQueryRow] {
	rows := make([]TaskQueryRow, 0, len(facts))
	for _, f := range facts {
		row := TaskQueryRow{
			ID:                f.TaskID,
			UserID:            f.UserID,
			Status:            f.Status,
			StartDate:         epochMsToISO8601(f.StartTimeMs),
			TotalInputTokens:  f.InputTokens,
			TotalOutputTokens: f.OutputTokens,
			TotalCachedTokens: f.CachedTokens,
			ExecutionMode:     f.ExecutionMode,
			Kind:              f.Kind,
			ParentTaskID:      f.ParentTaskID,
		}
		if f.AgentName != "" {
			ref := s.agentRef(f.AgentName)
			row.Agent = &ref
		}
		if f.EndTimeMs != nil {
			row.EndDate = optISO(*f.EndTimeMs)
			d := *f.EndTimeMs - f.StartTimeMs
			row.DurationMs = &d
		}
		rows = append(rows, row)
	}
	slices.SortFunc(rows, func(a, b TaskQueryRow) int {
		if a.StartDate != b.StartDate {
			return strings.Compare(b.StartDate, a.StartDate)
		}
		return strings.Compare(b.ID, a.ID)
	})
	page := paginateSlice(rows, q.PageNumber, q.PageSize)
	if !q.IncludeContent {
		ids := make([]string, 0, len(page.Data))
		for _, r := range page.Data {
			ids = append(ids, r.ID)
		}
		consented := s.consentedTaskSet(ctx, false, ids)
		for i := range page.Data {
			if !consented[page.Data[i].ID] {
				page.Data[i].UserID = ""
			}
		}
	}
	return page
}

type taskBucket struct {
	agent, user, status          string
	periodMs                     int64
	count, errorCount, completed int
	durations                    []int64
	inTok, outTok, cachedTok     int64
}

func (s *Service) groupedTasks(facts []store.TaskFact, entityDims []string, timeDim string, q Query) *Paginated[TaskMetricRow] {
	failed := make(map[string]bool, len(store.FailedTaskStatuses))
	for _, st := range store.FailedTaskStatuses {
		failed[st] = true
	}
	gAgent := slices.Contains(entityDims, "agent")
	gUser := slices.Contains(entityDims, "user")
	gStatus := slices.Contains(entityDims, "status")

	buckets := map[string]*taskBucket{}
	for _, f := range facts {
		status := f.Status
		if status == "" {
			status = "unknown"
		}
		var key strings.Builder
		if gAgent {
			key.WriteString(f.AgentName)
			key.WriteByte(0)
		}
		if gUser {
			key.WriteString(f.UserID)
			key.WriteByte(0)
		}
		if gStatus {
			key.WriteString(status)
			key.WriteByte(0)
		}
		var periodMs int64
		if timeDim != "" {
			periodMs = taskBucketStartMs(f.StartTimeMs, timeDim)
			key.WriteString(strconv.FormatInt(periodMs, 10))
		}
		b, ok := buckets[key.String()]
		if !ok {
			b = &taskBucket{agent: f.AgentName, user: f.UserID, status: status, periodMs: periodMs}
			buckets[key.String()] = b
		}
		b.count++
		if failed[f.Status] {
			b.errorCount++
		} else if f.Status == store.SuccessTaskStatus {
			b.completed++
		}
		if f.EndTimeMs != nil {
			b.durations = append(b.durations, *f.EndTimeMs-f.StartTimeMs)
		}
		b.inTok += f.InputTokens
		b.outTok += f.OutputTokens
		b.cachedTok += f.CachedTokens
	}

	rows := make([]TaskMetricRow, 0, len(buckets))
	for _, b := range buckets {
		avg, _, p95 := durationStats(sortedCopy(b.durations))
		row := TaskMetricRow{
			TaskCount:              b.count,
			ErrorCount:             b.errorCount,
			SuccessRate:            ratio(b.completed, b.count),
			AvgDurationMs:          avg,
			P95DurationMs:          p95,
			TotalInputTokens:       b.inTok,
			TotalOutputTokens:      b.outTok,
			TotalCachedInputTokens: b.cachedTok,
		}
		if gAgent {
			ref := s.agentRef(b.agent)
			row.Group.Agent = &ref
		}
		if gUser {
			u := b.user
			row.Group.UserID = &u
		}
		if gStatus {
			st := b.status
			row.Group.Status = &st
		}
		if timeDim != "" {
			row.Group.Period = timeDim
			row.Group.PeriodStart = optISO(b.periodMs)
		}
		rows = append(rows, row)
	}
	slices.SortFunc(rows, func(a, b TaskMetricRow) int {
		if timeDim != "" {
			pa, pb := deref(a.Group.PeriodStart), deref(b.Group.PeriodStart)
			if pa != pb {
				return strings.Compare(pa, pb)
			}
		}
		if a.TaskCount != b.TaskCount {
			return b.TaskCount - a.TaskCount
		}
		return strings.Compare(groupSortKey(a.Group), groupSortKey(b.Group))
	})
	return paginateSlice(rows, q.PageNumber, q.PageSize)
}

func taskBucketStartMs(ms int64, unit string) int64 {
	t := time.UnixMilli(ms).UTC()
	switch unit {
	case "week":
		offset := (int(t.Weekday()) + 6) % 7
		d := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
		return d.AddDate(0, 0, -offset).UnixMilli()
	case "month":
		return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC).UnixMilli()
	default:
		return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC).UnixMilli()
	}
}

func groupSortKey(g TaskMetricGroup) string {
	name := ""
	if g.Agent != nil {
		name = g.Agent.Name
	}
	return name + "\x00" + deref(g.UserID) + "\x00" + deref(g.Status)
}

func (s *Service) taskRows(tasks []*store.Task, includeContent bool, consented map[string]bool) []TaskHealthRow {
	rows := make([]TaskHealthRow, 0, len(tasks))
	for _, t := range tasks {
		slug := deref(t.HandlerName)
		name := slug
		if slug != "" {
			name = s.agentRef(slug).Name
		}
		status := deref(t.Status)
		row := TaskHealthRow{
			ID:            t.ID,
			Name:          name,
			Type:          deref(t.HandlerType),
			Status:        status,
			StatusEmoji:   statusEmoji(status),
			StartDate:     epochMsToISO8601(t.StartTime),
			ExecutionMode: t.ExecutionMode,
			Kind:          t.Kind,
		}
		if includeContent || consented[t.ID] {
			row.UserID = t.UserID
			row.InitialRequestText = deref(t.InitialRequestText)
		}
		if t.EndTime != nil {
			row.EndDate = optISO(*t.EndTime)
			d := *t.EndTime - t.StartTime
			row.DurationMs = &d
		}
		rows = append(rows, row)
	}
	return rows
}

func paginate[T any](data []T, total, pageNumber, pageSize int) *Paginated[T] {
	return &Paginated[T]{Data: data, Meta: Meta{Pagination: newPagination(pageNumber, pageSize, total)}}
}

func paginateSlice[T any](all []T, pageNumber, pageSize int) *Paginated[T] {
	total := len(all)
	start := (pageNumber - 1) * pageSize
	if start < 0 || start > total {
		start = total
	}
	end := start + pageSize
	if end > total {
		end = total
	}
	page := all[start:end]
	if page == nil {
		page = []T{}
	}
	return &Paginated[T]{Data: page, Meta: Meta{Pagination: newPagination(pageNumber, pageSize, total)}}
}

func newPagination(pageNumber, pageSize, total int) Pagination {
	totalPages := 0
	if pageSize > 0 {
		totalPages = (total + pageSize - 1) / pageSize
	}
	var nextPage *int
	if pageNumber < totalPages {
		n := pageNumber + 1
		nextPage = &n
	}
	return Pagination{PageNumber: pageNumber, PageSize: pageSize, TotalPages: totalPages, Count: total, NextPage: nextPage}
}

func durationStats(sorted []int64) (avg, p50, p95 int64) {
	n := len(sorted)
	if n == 0 {
		return 0, 0, 0
	}
	var sum int64
	for _, d := range sorted {
		sum += d
	}
	return sum / int64(n), percentile(sorted, 50), percentile(sorted, 95)
}

func percentile(sorted []int64, p int) int64 {
	n := len(sorted)
	if n == 0 {
		return 0
	}
	idx := int(math.Ceil(float64(p)/100*float64(n))) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= n {
		idx = n - 1
	}
	return sorted[idx]
}

func sortedCopy(in []int64) []int64 {
	out := slices.Clone(in)
	slices.Sort(out)
	return out
}

func ratio(num, den int) float64 {
	if den <= 0 {
		return 0
	}
	return float64(num) / float64(den)
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func optISO(ms int64) *string {
	if ms <= 0 {
		return nil
	}
	s := epochMsToISO8601(ms)
	return &s
}

func epochMsToISO8601(ms int64) string {
	return time.UnixMilli(ms).UTC().Format("2006-01-02T15:04:05.000000+00:00")
}

var ErrTaskNotFound = errors.New("analytics: task not found")

const taskUserUnknown = store.TaskUserUnknown

func (s *Service) TaskTree(ctx context.Context, taskID, callerUserID string, isAdmin bool) (*TaskTreeResponse, error) {
	if isAdmin {
		return s.serveTaskTree(ctx, taskID, treeGate{
			lineage:     func([]store.Task) error { return nil },
			descendants: func(children []store.Task) ([]store.Task, error) { return children, nil },
		})
	}
	return s.serveTaskTree(ctx, taskID, ownerGate(callerUserID, func(reason string) {
		s.logTraceDenied(taskID, callerUserID, reason)
	}))
}

func (s *Service) TaskTreeOwnedBy(ctx context.Context, taskID, ownerUserID string) (*TaskTreeResponse, error) {
	return s.serveTaskTree(ctx, taskID, ownerGate(ownerUserID, nil))
}

func ownerGate(ownerUserID string, onDeny func(reason string)) treeGate {
	deny := func(reason string) error {
		if onDeny != nil {
			onDeny(reason)
		}
		return ErrTaskNotFound
	}
	return treeGate{
		lineage: func(lineage []store.Task) error {
			if lineage[0].UserID != ownerUserID {
				return deny("queried task owned by another user")
			}
			for i := range lineage {
				if lineage[i].UserID != ownerUserID && lineage[i].UserID != taskUserUnknown {
					return deny("ancestor owned by another user")
				}
			}
			return nil
		},
		descendants: func(children []store.Task) ([]store.Task, error) {
			for i := range children {
				if children[i].UserID != ownerUserID && children[i].UserID != taskUserUnknown {
					return nil, deny("descendant owned by another user")
				}
			}
			return children, nil
		},
	}
}

func (s *Service) TaskTreeWithin(ctx context.Context, taskID string, scopedTo []string) (*TaskTreeResponse, error) {
	if len(scopedTo) == 0 {
		return nil, ErrTaskNotFound
	}
	allowed := make(map[string]struct{}, len(scopedTo))
	for _, id := range scopedTo {
		allowed[id] = struct{}{}
	}
	inScope := func(id string) bool { _, ok := allowed[id]; return ok }

	return s.serveTaskTree(ctx, taskID, treeGate{
		lineage: func(lineage []store.Task) error {
			for i := range lineage {
				if !inScope(lineage[i].ID) {
					return ErrTaskNotFound
				}
			}
			return nil
		},
		descendants: func(children []store.Task) ([]store.Task, error) {
			kept := map[string]struct{}{taskID: {}}
			out := make([]store.Task, 0, len(children))
			for i := range children {
				c := children[i]
				if !inScope(c.ID) || c.ParentTaskID == nil {
					continue
				}
				if _, ok := kept[*c.ParentTaskID]; !ok {
					continue
				}
				kept[c.ID] = struct{}{}
				out = append(out, c)
			}
			return out, nil
		},
	})
}

type treeGate struct {
	lineage     func([]store.Task) error
	descendants func([]store.Task) ([]store.Task, error)
}

func (s *Service) serveTaskTree(ctx context.Context, taskID string, gate treeGate) (*TaskTreeResponse, error) {
	lineage, err := s.repo.WalkAncestors(ctx, taskID, MaxTreeWalkUpHops)
	if err != nil {
		return nil, fmt.Errorf("analytics.TaskTree walkup(%s): %w", taskID, err)
	}
	if len(lineage) == 0 {
		return nil, ErrTaskNotFound
	}
	if denied := gate.lineage(lineage); denied != nil {
		return nil, denied
	}

	children, err := s.repo.GetTaskDescendants(ctx, taskID, MaxTreeWalkDownDepth+1, MaxTreeTasks)
	if err != nil {
		return nil, fmt.Errorf("analytics.TaskTree children(%s): %w", taskID, err)
	}
	fetchCapped := len(children) >= MaxTreeTasks
	children, err = gate.descendants(children)
	if err != nil {
		return nil, err
	}

	allTaskIDs := make([]string, 0, 1+len(children))
	allTaskIDs = append(allTaskIDs, taskID)
	for i := range children {
		allTaskIDs = append(allTaskIDs, children[i].ID)
	}

	allEvents, eventCounts, err := s.taskEventRepo.GetTreeEventsByTaskIDs(ctx, allTaskIDs, DefaultTreeEventsPerTask)
	if err != nil {
		return nil, fmt.Errorf("analytics.TaskTree events: %w", err)
	}
	eventsByTask, noiseDropped := groupTreeEvents(allEvents)
	return s.assembleTaskTree(taskID, lineage, children, eventsByTask, noiseDropped, eventCounts, fetchCapped), nil
}

func groupTreeEvents(allEvents []*store.TaskEvent) (eventsByTask map[string][]*store.TaskEvent, noiseDropped map[string]int) {
	eventsByTask = make(map[string][]*store.TaskEvent)
	noiseDropped = make(map[string]int)
	for _, e := range allEvents {
		if kind, _ := summarizeEvent(e.Direction, e.Payload); kind == eventKindLLMInvocation {
			noiseDropped[e.TaskID]++
			continue
		}
		eventsByTask[e.TaskID] = append(eventsByTask[e.TaskID], e)
	}
	return eventsByTask, noiseDropped
}

func descendantDepthsWhenParentsPrecedeChildren(taskID string, children []store.Task) map[string]int {
	childDepth := make(map[string]int, len(children))
	for i := range children {
		c := &children[i]
		if c.ParentTaskID != nil && *c.ParentTaskID == taskID {
			childDepth[c.ID] = 1
			continue
		}
		if c.ParentTaskID != nil {
			if pd, ok := childDepth[*c.ParentTaskID]; ok {
				childDepth[c.ID] = pd + 1
				continue
			}
		}
		childDepth[c.ID] = 1
	}
	return childDepth
}

func (s *Service) assembleTaskTree(taskID string, lineage, children []store.Task, eventsByTask map[string][]*store.TaskEvent, noiseDropped, eventCounts map[string]int, fetchCapped bool) *TaskTreeResponse {
	root := &lineage[len(lineage)-1]
	aggregate := 0
	treeEventsCapped := false
	applyCap := func(id string) (events []*store.TaskEvent, total int, capped bool) {
		src := eventsByTask[id]
		total = eventCounts[id] - noiseDropped[id]
		fetchSaturated := eventCounts[id] > 2*DefaultTreeEventsPerTask
		if aggregate >= MaxTreeTotalEvents {
			treeEventsCapped = true
			return nil, total, total > 0
		}
		remaining := MaxTreeTotalEvents - aggregate
		perTaskCap := DefaultTreeEventsPerTask
		if perTaskCap > remaining {
			perTaskCap = remaining
		}
		if total <= perTaskCap {
			aggregate += total
			return src, total, fetchSaturated
		}
		aggregate += perTaskCap
		return headTailSample(src, perTaskCap), total, true
	}

	treeEventsTotal := 0
	lineageNode := func(t *store.Task, depth int) TreeNodeDTO {
		if t.ID != taskID {
			n := s.treeNodeDTO(t, depth, nil, 0, false, false)
			n.EventsOmitted = true
			return n
		}
		ev, tot, capped := applyCap(t.ID)
		treeEventsTotal += tot
		return s.treeNodeDTO(t, depth, ev, tot, capped, true)
	}

	rootNode := lineageNode(root, 0)

	queriedDepth := len(lineage) - 1
	tree := make([]TreeNodeDTO, 0, (len(lineage)-1)+len(children))

	for i := len(lineage) - 2; i >= 0; i-- {
		tree = append(tree, lineageNode(&lineage[i], len(lineage)-1-i))
	}

	childDepth := descendantDepthsWhenParentsPrecedeChildren(taskID, children)
	lineageIDs := make(map[string]bool, len(lineage))
	for i := range lineage {
		lineageIDs[lineage[i].ID] = true
	}
	subtreeTruncated := fetchCapped
	for i := range children {
		c := &children[i]
		if lineageIDs[c.ID] {
			continue
		}
		if childDepth[c.ID] > MaxTreeWalkDownDepth {
			subtreeTruncated = true
			continue
		}
		ev, tot, capped := applyCap(c.ID)
		treeEventsTotal += tot
		tree = append(tree, s.treeNodeDTO(c, queriedDepth+childDepth[c.ID], ev, tot, capped, false))
	}

	lineageTruncated := root.ParentTaskID != nil
	treeCapped := subtreeTruncated || lineageTruncated

	resp := &TaskTreeResponse{
		QueriedTaskID:    taskID,
		Root:             rootNode,
		Tree:             tree,
		TreeEventsTotal:  treeEventsTotal,
		TreeEventsCapped: treeEventsCapped,
		TreeCapped:       treeCapped,
		Owner:            lineage[0].UserID,
	}
	fitTreeToBudget(resp)
	return resp
}

func (s *Service) logTraceDenied(taskID, callerUserID, reason string) {
	if s.logger == nil {
		return
	}
	s.logger.Warn("activity trace access denied",
		"reason", reason,
		"taskID", taskID,
		"callerUser", callerUserID)
}

func (s *Service) treeNodeDTO(t *store.Task, depth int, events []*store.TaskEvent, eventsTotal int, eventsCapped, isQueried bool) TreeNodeDTO {
	n := TreeNodeDTO{
		ID:           t.ID,
		StartDate:    epochMsToISO8601(t.StartTime),
		Depth:        depth,
		IsQueried:    isQueried,
		Events:       traceEventsDTO(events),
		EventsTotal:  eventsTotal,
		EventsCapped: eventsCapped,
	}
	if t.HandlerName != nil {
		n.Name = s.agentRef(*t.HandlerName).Name
	}
	if t.HandlerType != nil && *t.HandlerType != "" {
		n.Type = *t.HandlerType
	} else {
		n.Type = "unknown"
	}
	if t.Status != nil {
		n.Status = *t.Status
		n.StatusEmoji = statusEmoji(n.Status)
	}
	if t.ErrorMessage != nil && *t.ErrorMessage != "" {
		n.ErrorMessage = truncateRunes(*t.ErrorMessage, maxErrorMessageChars)
	}
	if t.ParentTaskID != nil {
		n.ParentTaskID = *t.ParentTaskID
	}
	if t.EndTime != nil {
		n.EndDate = epochMsToISO8601(*t.EndTime)
		dur := *t.EndTime - t.StartTime
		n.DurationMs = &dur
	}
	if n.Status == "timeout" {
		n.HideDuration = true
	}
	if t.TotalInputTokens != nil {
		n.TotalInputTokens = int64(*t.TotalInputTokens)
	}
	if t.TotalOutputTokens != nil {
		n.TotalOutputTokens = int64(*t.TotalOutputTokens)
	}
	if t.TotalCachedInputTokens != nil {
		n.TotalCachedInputTokens = int64(*t.TotalCachedInputTokens)
	}
	return n
}

func traceEventsDTO(events []*store.TaskEvent) []TraceEventDTO {
	out := make([]TraceEventDTO, 0, len(events))
	for _, e := range events {
		kind, summary := summarizeEvent(e.Direction, e.Payload)
		out = append(out, TraceEventDTO{
			CreatedAt: epochMsToISO8601(e.CreatedTime),
			Direction: e.Direction,
			Kind:      kind,
			Summary:   summary,
		})
	}
	return out
}

const eventKindLLMInvocation = "llm_invocation"

type wireEvent struct {
	Method string `json:"method"`
	Params struct {
		Message struct {
			Parts []wirePart `json:"parts"`
		} `json:"message"`
	} `json:"params"`
	Result struct {
		Kind   string `json:"kind"`
		Status struct {
			State   string `json:"state"`
			Message struct {
				Parts []wirePart `json:"parts"`
			} `json:"message"`
		} `json:"status"`
	} `json:"result"`
	Error *a2a.Error `json:"error"`
}

type wirePart struct {
	Kind string          `json:"kind"`
	Text string          `json:"text"`
	Data json.RawMessage `json:"data"`
}

func summarizeEvent(direction, payload string) (kind, summary string) {
	var w wireEvent
	if json.Unmarshal([]byte(payload), &w) != nil {
		return direction, ""
	}
	switch {
	case w.Error != nil:
		return "error", condenseSummary(fmt.Sprintf("%d: %s", w.Error.Code, w.Error.Message))
	case w.Method != "":
		return "request", condenseSummary(firstText(w.Params.Message.Parts))
	case w.Result.Kind == "task":
		s := w.Result.Status.State
		if t := firstText(w.Result.Status.Message.Parts); t != "" {
			s += ": " + t
		}
		return "response", condenseSummary(s)
	}
	for _, p := range w.Result.Status.Message.Parts {
		if len(p.Data) == 0 {
			continue
		}
		var probe struct {
			Type    string          `json:"type"`
			Request json.RawMessage `json:"request"`
		}
		_ = json.Unmarshal(p.Data, &probe)
		switch a2a.SignalType(probe.Type) {
		case a2a.SignalToolInvocationStart:
			var d a2a.ToolInvocationStartData
			_ = json.Unmarshal(p.Data, &d)
			return "tool_start", condenseSummary(d.ToolName)
		case a2a.SignalToolResult:
			var d a2a.ToolResultData
			_ = json.Unmarshal(p.Data, &d)
			return "tool_result", condenseSummary(d.ToolName + resultHint(d.ResultData))
		case a2a.SignalAgentProgressUpdate:
			var d a2a.AgentProgressUpdateData
			_ = json.Unmarshal(p.Data, &d)
			return "progress", condenseSummary(d.StatusText)
		case a2a.SignalLLMResponse:
			return "llm_response", condenseSummary(llmResponseSummary(p.Data))
		case a2a.SignalLLMInvocation:
			return eventKindLLMInvocation, ""
		case "":
			if len(probe.Request) > 0 {
				return "request_echo", ""
			}
		default:
			return probe.Type, condenseSummary(genericDataSummary(p.Data))
		}
	}
	return direction, ""
}

func headTailSample[T any](src []T, perTaskCap int) []T {
	total := len(src)
	if perTaskCap <= 0 {
		return nil
	}
	if total <= perTaskCap {
		return src
	}
	half := perTaskCap / 2
	headSize := perTaskCap - half
	if perTaskCap == 1 {
		half, headSize = 1, 0
	}
	merged := make([]T, 0, perTaskCap)
	merged = append(merged, src[:headSize]...)
	merged = append(merged, src[total-half:]...)
	return merged
}

var treeShrinkLadder = []int{20, 10, 8, 6, 4, 2, 0}

func fitTreeToBudget(resp *TaskTreeResponse) {
	if treeSizeBytes(resp) <= maxTreeBytes {
		return
	}
	for _, evCap := range treeShrinkLadder {
		if resampleTreeEvents(resp, evCap) {
			resp.TreeEventsCapped = true
		}
		if treeSizeBytes(resp) <= maxTreeBytes {
			return
		}
	}
	hardClipErrors(resp, minErrorMessageChars)
	for treeSizeBytes(resp) > maxTreeBytes && len(resp.Tree) > 1 {
		resp.Tree = resp.Tree[:len(resp.Tree)/2]
		resp.TreeCapped = true
	}
}

func treeSizeBytes(resp *TaskTreeResponse) int {
	b, _ := json.Marshal(resp)
	return len(b)
}

func resampleTreeEvents(resp *TaskTreeResponse, evCap int) bool {
	trimmed := false
	trim := func(n *TreeNodeDTO) {
		if len(n.Events) == 0 {
			return
		}
		sampled := headTailSample(n.Events, evCap)
		if len(sampled) < len(n.Events) {
			n.EventsCapped = true
			trimmed = true
		}
		n.Events = sampled
	}
	trim(&resp.Root)
	for i := range resp.Tree {
		trim(&resp.Tree[i])
	}
	return trimmed
}

func hardClipErrors(resp *TaskTreeResponse, maxRunes int) {
	clip := func(n *TreeNodeDTO) {
		if n.ErrorMessage != "" {
			n.ErrorMessage = truncateRunes(n.ErrorMessage, maxRunes)
		}
	}
	clip(&resp.Root)
	for i := range resp.Tree {
		clip(&resp.Tree[i])
	}
}

func firstText(parts []wirePart) string {
	for _, p := range parts {
		if p.Text != "" {
			return p.Text
		}
	}
	return ""
}

func resultHint(v any) string {
	switch t := v.(type) {
	case string:
		if t != "" {
			return " → " + t
		}
	case map[string]any:
		if msg, ok := t["message"].(string); ok && msg != "" {
			return " → " + msg
		}
	}
	return ""
}

func llmResponseSummary(raw json.RawMessage) string {
	var d struct {
		Data map[string]any `json:"data"`
	}
	if json.Unmarshal(raw, &d) != nil {
		return ""
	}
	content, _ := d.Data["content"].(map[string]any)
	parts, _ := content["parts"].([]any)
	var calls, texts []string
	for _, pa := range parts {
		pm, _ := pa.(map[string]any)
		if fc, ok := pm["function_call"].(map[string]any); ok {
			if name, ok := fc["name"].(string); ok && name != "" {
				calls = append(calls, name)
			}
		}
		if t, ok := pm["text"].(string); ok && t != "" {
			texts = append(texts, t)
		}
	}
	if len(calls) > 0 {
		return "→ " + strings.Join(calls, ", ")
	}
	return strings.Join(texts, " ")
}

func genericDataSummary(raw json.RawMessage) string {
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		return ""
	}
	for _, k := range []string{"status_text", "message", "text", "tool_name", "filename", "name", "detail", "reason"} {
		if v, ok := m[k].(string); ok && v != "" {
			return v
		}
	}
	return ""
}

func condenseSummary(s string) string {
	return truncateRunes(strings.Join(strings.Fields(s), " "), maxEventSummaryChars)
}

func truncateRunes(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	cut := maxBytes
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "…"
}

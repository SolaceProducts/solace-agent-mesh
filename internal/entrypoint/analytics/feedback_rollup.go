// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package analytics

import (
	"cmp"
	"context"
	"fmt"
	"maps"
	"slices"

	"github.com/google/uuid"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
)

type RollupGroupBy string

const (
	RollupGroupBySubject RollupGroupBy = "subject"
	RollupGroupByTenant  RollupGroupBy = "tenant"
)

const (
	RollupSortName             = "name"
	RollupSortType             = "type"
	RollupSortTotalActivities  = "totalActivities"
	RollupSortPositiveFeedback = "positiveFeedback"
	RollupSortNegativeFeedback = "negativeFeedback"
	RollupSortNegativeRatio    = "negativeRatio"
)

var rollupComparators = map[string]func(a, b FeedbackRollupRow) int{
	RollupSortName:             func(a, b FeedbackRollupRow) int { return cmp.Compare(a.Subject.Name, b.Subject.Name) },
	RollupSortType:             func(a, b FeedbackRollupRow) int { return cmp.Compare(a.Subject.Type, b.Subject.Type) },
	RollupSortTotalActivities:  func(a, b FeedbackRollupRow) int { return cmp.Compare(a.activities(), b.activities()) },
	RollupSortPositiveFeedback: func(a, b FeedbackRollupRow) int { return cmp.Compare(a.PositiveFeedback, b.PositiveFeedback) },
	RollupSortNegativeFeedback: func(a, b FeedbackRollupRow) int { return cmp.Compare(a.NegativeFeedback, b.NegativeFeedback) },
	RollupSortNegativeRatio:    func(a, b FeedbackRollupRow) int { return cmp.Compare(a.NegativeRatio, b.NegativeRatio) },
}

func IsRollupSortKey(key string) bool {
	_, ok := rollupComparators[key]
	return ok
}

func RollupSortKeys() []string {
	keys := slices.Collect(maps.Keys(rollupComparators))
	slices.Sort(keys)
	return keys
}

type FeedbackRollupQuery struct {
	Visibility  store.FeedbackVisibility
	AgentIDs    []string
	WorkflowIDs []string
	SubjectType string
	StartDate   int64
	EndDate     int64
	GroupBy     RollupGroupBy
	SortBy      string
	SortDesc    bool
	PageNumber  int
	PageSize    int
	Trend       TrendGranularity
	UserID      string
	RatingsOnly bool
}

type FeedbackRollupRow struct {
	Subject          *AgentRef             `json:"subject,omitempty"`
	TotalActivities  *int                  `json:"totalActivities,omitempty"`
	PositiveFeedback int                   `json:"positiveFeedback"`
	NegativeFeedback int                   `json:"negativeFeedback"`
	NegativeRatio    float64               `json:"negativeRatio"`
	Trend            []FeedbackRollupPoint `json:"trend"`
}

func (r FeedbackRollupRow) activities() int {
	if r.TotalActivities == nil {
		return 0
	}
	return *r.TotalActivities
}

type rollupStore interface {
	CountHandlerTasks(ctx context.Context, opts store.TaskAnalyticsOpts, handlers store.SubjectScope, bucketMs int64) ([]store.HandlerTaskCount, error)
	AggregateFeedbackBySubject(ctx context.Context, vis store.FeedbackVisibility, f store.CrossUserFeedbackFilter, bucketMs int64) ([]store.SubjectFeedbackCount, error)
}

type subjectKey struct{ kind, id string }

type subjectBucket struct {
	subject subjectKey
	bucket  int64
}

func (s *Service) FeedbackRollup(ctx context.Context, q FeedbackRollupQuery) (*Paginated[FeedbackRollupRow], error) {
	vis := effectiveVisibility(q)
	totals, err := s.rollupCounts(ctx, vis, q, 0)
	if err != nil {
		return nil, err
	}
	if q.GroupBy == RollupGroupByTenant {
		return s.tenantRollup(ctx, vis, totals, q)
	}
	rows := s.subjectRows(totals, q.RatingsOnly)
	sortRollupRows(rows, q.SortBy, q.SortDesc)
	page := paginateSlice(rows, q.PageNumber, q.PageSize)
	withTrends, err := s.withTrends(ctx, page.Data, q)
	if err != nil {
		return nil, err
	}
	page.Data = withTrends
	return page, nil
}

func effectiveVisibility(q FeedbackRollupQuery) store.FeedbackVisibility {
	agents, workflows := q.Visibility.Agents, q.Visibility.Workflows
	if len(q.AgentIDs) > 0 || len(q.WorkflowIDs) > 0 {
		agents, workflows = narrowScope(agents, q.AgentIDs), narrowScope(workflows, q.WorkflowIDs)
	}
	switch q.SubjectType {
	case store.SubjectTypeAgent:
		workflows = store.SubjectScope{}
	case store.SubjectTypeWorkflow:
		agents = store.SubjectScope{}
	}
	return store.FeedbackVisibility{Agents: agents, Workflows: workflows}
}

func narrowScope(reach store.SubjectScope, requested []string) store.SubjectScope {
	if len(requested) == 0 {
		return store.SubjectScope{}
	}
	if reach.All {
		return store.SubjectScope{IDs: slices.Clone(requested)}
	}
	kept := make([]string, 0, len(requested))
	for _, id := range requested {
		if slices.Contains(reach.IDs, id) {
			kept = append(kept, id)
		}
	}
	return store.SubjectScope{IDs: kept}
}

func handlerScope(scope store.SubjectScope, kind a2a.ComponentKind) store.SubjectScope {
	if scope.All {
		return scope
	}
	names := make([]string, 0, len(scope.IDs))
	for _, id := range scope.IDs {
		if _, err := uuid.Parse(id); err == nil {
			id = a2a.InstanceName(kind, id)
		}
		names = append(names, id)
	}
	return store.SubjectScope{IDs: names}
}

func (s *Service) rollupCounts(ctx context.Context, vis store.FeedbackVisibility, q FeedbackRollupQuery, bucketMs int64) (map[subjectBucket]bucketCounts, error) {
	var activity []store.HandlerTaskCount
	if !q.RatingsOnly {
		var err error
		activity, err = s.activityCounts(ctx, vis, q.UserID, q.StartDate, q.EndDate, bucketMs)
		if err != nil {
			return nil, err
		}
	}
	feedback, err := s.rollupRepo.AggregateFeedbackBySubject(ctx, vis, store.CrossUserFeedbackFilter{StartDate: q.StartDate, EndDate: q.EndDate, UserIDs: store.OptionalUserIDs(q.UserID)}, bucketMs)
	if err != nil {
		return nil, fmt.Errorf("feedback rollup ratings: %w", err)
	}
	out := make(map[subjectBucket]bucketCounts, len(activity)+len(feedback))
	for _, a := range activity {
		k := subjectBucket{subjectKey{a.HandlerType, resourceIDFromSlug(a.HandlerName)}, a.BucketStartMs}
		out[k] = out[k].add(bucketCounts{activities: a.Count})
	}
	for _, f := range feedback {
		k := subjectBucket{subjectKey{f.SubjectType, f.SubjectID}, f.BucketStartMs}
		out[k] = out[k].add(bucketCounts{positive: f.Positive, negative: f.Negative})
	}
	return out, nil
}

func (s *Service) activityCounts(ctx context.Context, vis store.FeedbackVisibility, userID string, start, end, bucketMs int64) ([]store.HandlerTaskCount, error) {
	base := store.TaskAnalyticsOpts{StartDate: start, EndDate: end, Kind: store.TaskKindNormal, OwnerUserID: userID}
	agentOpts := base
	agentOpts.HandlerType = store.TaskHandlerTypeAgent
	agentOpts.ExcludeWorkflowNested = true
	agents, err := s.rollupRepo.CountHandlerTasks(ctx, agentOpts, handlerScope(vis.Agents, a2a.KindAgent), bucketMs)
	if err != nil {
		return nil, fmt.Errorf("feedback rollup agent activity: %w", err)
	}
	workflowOpts := base
	workflowOpts.HandlerType = store.TaskHandlerTypeWorkflow
	workflows, err := s.rollupRepo.CountHandlerTasks(ctx, workflowOpts, handlerScope(vis.Workflows, a2a.KindWorkflow), bucketMs)
	if err != nil {
		return nil, fmt.Errorf("feedback rollup workflow activity: %w", err)
	}
	return append(agents, workflows...), nil
}

func (s *Service) subjectRows(totals map[subjectBucket]bucketCounts, ratingsOnly bool) []FeedbackRollupRow {
	rows := make([]FeedbackRollupRow, 0, len(totals))
	for k, c := range totals {
		if ratingsOnly && c.positive+c.negative == 0 {
			continue
		}
		ref := s.subjectRef(k.subject.id, k.subject.kind)
		rows = append(rows, rollupRow(&ref, c, ratingsOnly))
	}
	return rows
}

func rollupRow(subject *AgentRef, c bucketCounts, ratingsOnly bool) FeedbackRollupRow {
	return FeedbackRollupRow{
		Subject:          subject,
		TotalActivities:  c.activityCount(ratingsOnly),
		PositiveFeedback: c.positive,
		NegativeFeedback: c.negative,
		NegativeRatio:    c.negativeRatio(ratingsOnly),
		Trend:            []FeedbackRollupPoint{},
	}
}

func sortRollupRows(rows []FeedbackRollupRow, sortBy string, desc bool) {
	primary, ok := rollupComparators[sortBy]
	if !ok {
		primary = rollupComparators[RollupSortNegativeRatio]
	}
	slices.SortFunc(rows, func(a, b FeedbackRollupRow) int {
		c := primary(a, b)
		if desc {
			c = -c
		}
		return cmp.Or(c, cmp.Compare(a.Subject.Name, b.Subject.Name), cmp.Compare(a.Subject.ID, b.Subject.ID))
	})
}

func (s *Service) withTrends(ctx context.Context, rows []FeedbackRollupRow, q FeedbackRollupQuery) ([]FeedbackRollupRow, error) {
	if q.Trend == TrendNone || len(rows) == 0 {
		return rows, nil
	}
	buckets, err := s.rollupCounts(ctx, pageVisibility(rows), q, sourceBucketMs(q.Trend))
	if err != nil {
		return nil, err
	}
	bySubject := make(map[subjectKey]map[int64]bucketCounts, len(rows))
	for k, c := range buckets {
		if bySubject[k.subject] == nil {
			bySubject[k.subject] = map[int64]bucketCounts{}
		}
		bySubject[k.subject][k.bucket] = c
	}
	out := make([]FeedbackRollupRow, len(rows))
	for i, r := range rows {
		r.Trend = buildTrend(bySubject[subjectKey{r.Subject.Type, r.Subject.ID}], q.StartDate, q.EndDate, q.Trend, q.RatingsOnly)
		out[i] = r
	}
	return out, nil
}

func pageVisibility(rows []FeedbackRollupRow) store.FeedbackVisibility {
	var vis store.FeedbackVisibility
	for _, r := range rows {
		switch r.Subject.Type {
		case store.SubjectTypeAgent:
			vis.Agents.IDs = append(vis.Agents.IDs, r.Subject.ID)
		case store.SubjectTypeWorkflow:
			vis.Workflows.IDs = append(vis.Workflows.IDs, r.Subject.ID)
		}
	}
	return vis
}

func (s *Service) tenantRollup(ctx context.Context, vis store.FeedbackVisibility, totals map[subjectBucket]bucketCounts, q FeedbackRollupQuery) (*Paginated[FeedbackRollupRow], error) {
	var sum bucketCounts
	for _, c := range totals {
		sum = sum.add(c)
	}
	row := rollupRow(nil, sum, q.RatingsOnly)
	if q.Trend != TrendNone {
		buckets, err := s.rollupCounts(ctx, vis, q, sourceBucketMs(q.Trend))
		if err != nil {
			return nil, err
		}
		byBucket := map[int64]bucketCounts{}
		for k, c := range buckets {
			byBucket[k.bucket] = byBucket[k.bucket].add(c)
		}
		row.Trend = buildTrend(byBucket, q.StartDate, q.EndDate, q.Trend, q.RatingsOnly)
	}
	return paginateSlice([]FeedbackRollupRow{row}, q.PageNumber, q.PageSize), nil
}

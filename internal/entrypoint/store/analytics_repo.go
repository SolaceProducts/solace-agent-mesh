// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
)

type AnalyticsRepo struct {
	db                *DB
	durationSampleCap int
}

func NewAnalyticsRepo(db *DB) *AnalyticsRepo {
	return &AnalyticsRepo{db: db, durationSampleCap: defaultDurationSampleCap}
}

const taskColumns = `id, user_id, parent_task_id, start_time, end_time, status,
	initial_request_text, total_input_tokens, total_output_tokens, total_cached_input_tokens,
	token_usage_details, execution_mode, last_activity_time, background_execution_enabled,
	max_execution_time_ms, session_id, events_buffered, events_consumed, kind, post_compaction_remaining_tokens,
	handler_name, handler_type`

var FailedTaskStatuses = []string{"failed", "error", "timeout"}

const (
	TaskStatusInProgress = "in_progress"
	TaskStatusStale      = "stale"
)

const SuccessTaskStatus = "completed"

const TaskStatusJanitorTimeout = "timeout"

func hasMeasurableDuration(status *string, endTime *int64) bool {
	if endTime == nil {
		return false
	}
	if status != nil && *status == TaskStatusJanitorTimeout {
		return false
	}
	return true
}

const analyticsRowCap = 100000

const defaultDurationSampleCap = 2000

type TaskAnalyticsOpts struct {
	StartDate             int64
	EndDate               int64
	Kind                  string
	ExecutionMode         string
	HandlerType           string
	OwnerUserID           string
	ExcludeWorkflowNested bool
	StaleActivityBeforeMs int64
}

func (o TaskAnalyticsOpts) taskWhere(alias string) (string, []any) {
	prefix := ""
	if alias != "" {
		prefix = alias + "."
	}
	var clauses []string
	var args []any
	if o.Kind != "" {
		clauses = append(clauses, prefix+"kind = ?")
		args = append(args, o.Kind)
	}
	if o.ExecutionMode != "" {
		clauses = append(clauses, prefix+"execution_mode = ?")
		args = append(args, o.ExecutionMode)
	}
	if o.HandlerType != "" {
		clauses = append(clauses, prefix+"handler_type = ?")
		args = append(args, o.HandlerType)
	}
	if o.OwnerUserID != "" {
		clauses = append(clauses, prefix+"user_id = ?")
		args = append(args, o.OwnerUserID)
	}
	if o.StartDate > 0 {
		clauses = append(clauses, prefix+"start_time >= ?")
		args = append(args, o.StartDate)
	}
	if o.EndDate > 0 {
		clauses = append(clauses, prefix+"start_time <= ?")
		args = append(args, o.EndDate)
	}
	if len(clauses) == 0 {
		return "", nil
	}
	return strings.Join(clauses, " AND "), args
}

const sqlWhere = " WHERE "

func whereClause(joined string) string {
	if joined == "" {
		return ""
	}
	return sqlWhere + joined
}

type TaskStatusCount struct {
	Status string `db:"status"`
	Count  int    `db:"count"`
}

type TaskHealthAgg struct {
	ByStatus              map[string]int
	DurationsMs           []int64
	ActiveBackgroundCount int
}

func (r *AnalyticsRepo) AggregateTaskHealth(ctx context.Context, opts TaskAnalyticsOpts) (*TaskHealthAgg, error) {
	joined, args := opts.taskWhere("")

	var (
		statusExpr string
		statusArgs []any
	)
	if opts.StaleActivityBeforeMs > 0 {
		statusExpr = `CASE
				WHEN end_time IS NOT NULL THEN COALESCE(NULLIF(status, ''), 'unknown')
				WHEN last_activity_time IS NOT NULL AND last_activity_time > ? THEN '` + TaskStatusInProgress + `'
				ELSE '` + TaskStatusStale + `'
			END`
		statusArgs = []any{opts.StaleActivityBeforeMs}
	} else {
		statusExpr = `COALESCE(NULLIF(status, ''), 'unknown')`
	}
	queryArgs := append([]any{}, statusArgs...)
	queryArgs = append(queryArgs, args...)
	statusRows, err := dbutil.QueryRowPtrs[TaskStatusCount](ctx, r.db,
		`SELECT `+statusExpr+` AS status, COUNT(*) AS count
		 FROM tasks`+whereClause(joined)+`
		 GROUP BY 1`, queryArgs...)
	if err != nil {
		return nil, fmt.Errorf("AnalyticsRepo.AggregateTaskHealth status: %w", err)
	}
	byStatus := make(map[string]int, len(statusRows))
	for _, s := range statusRows {
		byStatus[s.Status] = s.Count
	}

	durWhere := "end_time IS NOT NULL AND (status IS NULL OR status != '" + TaskStatusJanitorTimeout + "')"
	if joined != "" {
		durWhere = joined + " AND " + durWhere
	}
	durations, err := dbutil.QueryVals[int64](ctx, r.db,
		`SELECT (end_time - start_time) AS duration_ms
		 FROM tasks WHERE `+durWhere+`
		 ORDER BY start_time DESC
		 LIMIT ?`, append(args, analyticsRowCap)...)
	if err != nil {
		return nil, fmt.Errorf("AnalyticsRepo.AggregateTaskHealth durations: %w", err)
	}
	slices.Sort(durations)

	bgJoined, bgArgs := opts.taskWhere("")
	bgClauses := []string{"execution_mode = 'background'", "end_time IS NULL"}
	if opts.StaleActivityBeforeMs > 0 {
		bgClauses = append(bgClauses, "last_activity_time IS NOT NULL AND last_activity_time > ?")
		bgArgs = append([]any{opts.StaleActivityBeforeMs}, bgArgs...)
	}
	if bgJoined != "" {
		bgClauses = append(bgClauses, bgJoined)
	}
	activeBg, err := dbutil.QueryVal[int](ctx, r.db,
		`SELECT COUNT(*) FROM tasks WHERE `+strings.Join(bgClauses, " AND "), bgArgs...)
	if err != nil {
		return nil, fmt.Errorf("AnalyticsRepo.AggregateTaskHealth activeBackground: %w", err)
	}

	return &TaskHealthAgg{
		ByStatus:              byStatus,
		DurationsMs:           durations,
		ActiveBackgroundCount: activeBg,
	}, nil
}

func (r *AnalyticsRepo) CountTasksInWindow(ctx context.Context, opts TaskAnalyticsOpts) (int, error) {
	joined, args := opts.taskWhere("")
	n, err := dbutil.QueryVal[int](ctx, r.db, `SELECT COUNT(*) FROM tasks`+whereClause(joined), args...)
	if err != nil {
		return 0, fmt.Errorf("AnalyticsRepo.CountTasksInWindow: %w", err)
	}
	return n, nil
}

func (r *AnalyticsRepo) ListFailedTasks(ctx context.Context, opts TaskAnalyticsOpts, limit, offset int) ([]*Task, int, error) {
	joined, args := opts.taskWhere("")
	ph, statusArgs, err := dbutil.InClause(FailedTaskStatuses)
	if err != nil {
		return nil, 0, fmt.Errorf("AnalyticsRepo.ListFailedTasks: %w", err)
	}
	statusPred := "status IN (" + ph + ")"
	where := statusPred
	if joined != "" {
		where = joined + " AND " + statusPred
	}
	allArgs := slices.Concat(args, statusArgs)

	total, err := dbutil.QueryVal[int](ctx, r.db, `SELECT COUNT(*) FROM tasks WHERE `+where, allArgs...)
	if err != nil {
		return nil, 0, fmt.Errorf("AnalyticsRepo.ListFailedTasks count: %w", err)
	}
	tasks, err := dbutil.QueryRowPtrs[Task](ctx, r.db,
		`SELECT `+taskColumns+`
		 FROM tasks WHERE `+where+`
		 ORDER BY start_time DESC, id DESC LIMIT ? OFFSET ?`,
		append(allArgs, limit, offset)...)
	if err != nil {
		return nil, 0, fmt.Errorf("AnalyticsRepo.ListFailedTasks query: %w", err)
	}
	return tasks, total, nil
}

func (r *AnalyticsRepo) ListSlowTasks(ctx context.Context, opts TaskAnalyticsOpts, minDurationMs int64, limit, offset int) ([]*Task, int, error) {
	joined, args := opts.taskWhere("")
	clauses := []string{"end_time IS NOT NULL", "(status IS NULL OR status != '" + TaskStatusJanitorTimeout + "')"}
	if joined != "" {
		clauses = append([]string{joined}, clauses...)
	}
	allArgs := append([]any{}, args...)
	if minDurationMs > 0 {
		clauses = append(clauses, "(end_time - start_time) >= ?")
		allArgs = append(allArgs, minDurationMs)
	}
	where := strings.Join(clauses, " AND ")

	total, err := dbutil.QueryVal[int](ctx, r.db, `SELECT COUNT(*) FROM tasks WHERE `+where, allArgs...)
	if err != nil {
		return nil, 0, fmt.Errorf("AnalyticsRepo.ListSlowTasks count: %w", err)
	}
	tasks, err := dbutil.QueryRowPtrs[Task](ctx, r.db,
		`SELECT `+taskColumns+`
		 FROM tasks WHERE `+where+`
		 ORDER BY (end_time - start_time) DESC, id DESC LIMIT ? OFFSET ?`,
		append(allArgs, limit, offset)...)
	if err != nil {
		return nil, 0, fmt.Errorf("AnalyticsRepo.ListSlowTasks query: %w", err)
	}
	return tasks, total, nil
}

type TokenUsageGroupBy string

const (
	TokenGroupByDay  TokenUsageGroupBy = "day"
	TokenGroupByUser TokenUsageGroupBy = "user"
)

type TokenUsageAgg struct {
	GroupKey      string
	PeriodStartMs int64
	InputTokens   int64
	OutputTokens  int64
	CachedTokens  int64
	TaskCount     int
}

func (r *AnalyticsRepo) AggregateTokens(ctx context.Context, opts TaskAnalyticsOpts, groupBy TokenUsageGroupBy) ([]TokenUsageAgg, error) {
	joined, args := opts.taskWhere("")

	switch groupBy {
	case TokenGroupByUser:
		type row struct {
			GroupKey string `db:"group_key"`
			In       int64  `db:"input_tokens"`
			Out      int64  `db:"output_tokens"`
			Cached   int64  `db:"cached_tokens"`
			Count    int    `db:"task_count"`
		}
		rows, err := dbutil.QueryRowPtrs[row](ctx, r.db,
			`SELECT user_id AS group_key,
				COALESCE(SUM(total_input_tokens), 0) AS input_tokens,
				COALESCE(SUM(total_output_tokens), 0) AS output_tokens,
				COALESCE(SUM(total_cached_input_tokens), 0) AS cached_tokens,
				COUNT(*) AS task_count
			 FROM tasks`+whereClause(joined)+`
			 GROUP BY user_id
			 ORDER BY COALESCE(SUM(total_input_tokens), 0) + COALESCE(SUM(total_output_tokens), 0) DESC`, args...)
		if err != nil {
			return nil, fmt.Errorf("AnalyticsRepo.AggregateTokens user: %w", err)
		}
		out := make([]TokenUsageAgg, 0, len(rows))
		for _, x := range rows {
			out = append(out, TokenUsageAgg{GroupKey: x.GroupKey, InputTokens: x.In, OutputTokens: x.Out, CachedTokens: x.Cached, TaskCount: x.Count})
		}
		return out, nil

	case TokenGroupByDay, "":
		const msPerDay = 86400000
		dayExpr := fmt.Sprintf("(start_time / %d) * %d", msPerDay, msPerDay)
		type row struct {
			DayMs  int64 `db:"day_ms"`
			In     int64 `db:"input_tokens"`
			Out    int64 `db:"output_tokens"`
			Cached int64 `db:"cached_tokens"`
			Count  int   `db:"task_count"`
		}
		rows, err := dbutil.QueryRowPtrs[row](ctx, r.db,
			`SELECT `+dayExpr+` AS day_ms,
				COALESCE(SUM(total_input_tokens), 0) AS input_tokens,
				COALESCE(SUM(total_output_tokens), 0) AS output_tokens,
				COALESCE(SUM(total_cached_input_tokens), 0) AS cached_tokens,
				COUNT(*) AS task_count
			 FROM tasks`+whereClause(joined)+`
			 GROUP BY `+dayExpr+`
			 ORDER BY day_ms ASC`, args...)
		if err != nil {
			return nil, fmt.Errorf("AnalyticsRepo.AggregateTokens day: %w", err)
		}
		out := make([]TokenUsageAgg, 0, len(rows))
		for _, x := range rows {
			out = append(out, TokenUsageAgg{PeriodStartMs: x.DayMs, InputTokens: x.In, OutputTokens: x.Out, CachedTokens: x.Cached, TaskCount: x.Count})
		}
		return out, nil

	default:
		return nil, fmt.Errorf("AnalyticsRepo.AggregateTokens: unsupported groupBy %q", groupBy)
	}
}

type AgentPerfRow struct {
	AgentName     string
	InvocationCnt int
	ErrorCnt      int
	DurationsMs   []int64
}

func (r *AnalyticsRepo) AggregateAgentPerformance(ctx context.Context, namespace string, opts TaskAnalyticsOpts) ([]AgentPerfRow, error) {
	joined, args := opts.taskWhere("t")

	type joinRow struct {
		TaskID    string  `db:"task_id"`
		Topic     string  `db:"topic"`
		Status    *string `db:"status"`
		StartTime int64   `db:"start_time"`
		EndTime   *int64  `db:"end_time"`
	}
	var topicPred string
	if namespace != "" {
		topicPred = "te.topic LIKE ?"
		args = append(args, namespace+"/a2a/v1/agent/request/%")
	} else {
		topicPred = "te.topic LIKE '%/agent/request/%'"
	}
	where := topicPred
	if joined != "" {
		where = joined + " AND " + topicPred
	}
	rows, err := dbutil.QueryRowPtrs[joinRow](ctx, r.db,
		`SELECT te.task_id AS task_id, te.topic AS topic, t.status AS status, t.start_time AS start_time, t.end_time AS end_time
		 FROM task_events te
		 JOIN tasks t ON t.id = te.task_id
		 WHERE `+where+`
		 ORDER BY t.start_time DESC, te.task_id DESC
		 LIMIT ?`, append(args, analyticsRowCap)...)
	if err != nil {
		return nil, fmt.Errorf("AnalyticsRepo.AggregateAgentPerformance: %w", err)
	}

	failed := make(map[string]bool, len(FailedTaskStatuses))
	for _, s := range FailedTaskStatuses {
		failed[s] = true
	}
	byAgent := make(map[string]*AgentPerfRow)
	seen := make(map[string]bool)
	for _, x := range rows {
		name := agentNameFromTopic(x.Topic)
		if name == "" {
			continue
		}
		dedupeKey := name + "\x00" + x.TaskID
		if seen[dedupeKey] {
			continue
		}
		seen[dedupeKey] = true

		row, ok := byAgent[name]
		if !ok {
			row = &AgentPerfRow{AgentName: name}
			byAgent[name] = row
		}
		row.InvocationCnt++
		if x.Status != nil && failed[*x.Status] {
			row.ErrorCnt++
		}
		if hasMeasurableDuration(x.Status, x.EndTime) {
			row.DurationsMs = append(row.DurationsMs, *x.EndTime-x.StartTime)
		}
	}
	out := make([]AgentPerfRow, 0, len(byAgent))
	for _, row := range byAgent {
		out = append(out, *row)
	}
	return out, nil
}

type TaskFactsOpts struct {
	StartDate     int64
	EndDate       int64
	ExecutionMode string
	Status        string
	UserID        string
	RequireAgent  bool
}

type TaskFact struct {
	TaskID        string
	UserID        string
	AgentName     string
	Status        string
	StartTimeMs   int64
	EndTimeMs     *int64
	InputTokens   int64
	OutputTokens  int64
	CachedTokens  int64
	ExecutionMode string
	Kind          string
	ParentTaskID  string
}

func (r *AnalyticsRepo) QueryTaskFacts(ctx context.Context, namespace string, opts TaskFactsOpts) ([]TaskFact, bool, error) {
	base := TaskAnalyticsOpts{
		StartDate:     opts.StartDate,
		EndDate:       opts.EndDate,
		Kind:          TaskKindNormal,
		ExecutionMode: opts.ExecutionMode,
	}
	if !opts.RequireAgent {
		joined, args := base.taskWhere("")
		clauses := []string{}
		if joined != "" {
			clauses = append(clauses, joined)
		}
		if opts.Status != "" {
			clauses = append(clauses, "status = ?")
			args = append(args, opts.Status)
		}
		if opts.UserID != "" {
			clauses = append(clauses, "user_id = ?")
			args = append(args, opts.UserID)
		}
		where := strings.Join(clauses, " AND ")
		facts, err := dbutil.QueryRowPtrs[TaskFact](ctx, r.db,
			`SELECT id AS task_id, user_id, '' AS agent_name,
				COALESCE(status, '') AS status, start_time AS start_time_ms, end_time AS end_time_ms,
				COALESCE(total_input_tokens, 0) AS input_tokens,
				COALESCE(total_output_tokens, 0) AS output_tokens,
				COALESCE(total_cached_input_tokens, 0) AS cached_tokens,
				execution_mode, kind, COALESCE(CAST(parent_task_id AS TEXT), '') AS parent_task_id
			 FROM tasks`+whereClause(where)+`
			 ORDER BY start_time DESC, id DESC
			 LIMIT ?`, append(args, analyticsRowCap)...)
		if err != nil {
			return nil, false, fmt.Errorf("AnalyticsRepo.QueryTaskFacts direct: %w", err)
		}
		out := make([]TaskFact, 0, len(facts))
		for _, f := range facts {
			out = append(out, *f)
		}
		return out, len(facts) >= analyticsRowCap, nil
	}

	joined, args := base.taskWhere("t")
	clauses := []string{}
	if joined != "" {
		clauses = append(clauses, joined)
	}
	if opts.Status != "" {
		clauses = append(clauses, "t.status = ?")
		args = append(args, opts.Status)
	}
	if opts.UserID != "" {
		clauses = append(clauses, "t.user_id = ?")
		args = append(args, opts.UserID)
	}
	if namespace != "" {
		clauses = append(clauses, "te.topic LIKE ?")
		args = append(args, namespace+"/a2a/v1/agent/request/%")
	} else {
		clauses = append(clauses, "te.topic LIKE '%/agent/request/%'")
	}
	where := strings.Join(clauses, " AND ")

	type joinRow struct {
		TaskID        string `db:"task_id"`
		UserID        string `db:"user_id"`
		Topic         string `db:"topic"`
		Status        string `db:"status"`
		StartTimeMs   int64  `db:"start_time_ms"`
		EndTimeMs     *int64 `db:"end_time_ms"`
		InputTokens   int64  `db:"input_tokens"`
		OutputTokens  int64  `db:"output_tokens"`
		CachedTokens  int64  `db:"cached_tokens"`
		ExecutionMode string `db:"execution_mode"`
		Kind          string `db:"kind"`
		ParentTaskID  string `db:"parent_task_id"`
	}
	rows, err := dbutil.QueryRowPtrs[joinRow](ctx, r.db,
		`SELECT te.task_id AS task_id, t.user_id AS user_id, te.topic AS topic,
			COALESCE(t.status, '') AS status, t.start_time AS start_time_ms, t.end_time AS end_time_ms,
			COALESCE(t.total_input_tokens, 0) AS input_tokens,
			COALESCE(t.total_output_tokens, 0) AS output_tokens,
			COALESCE(t.total_cached_input_tokens, 0) AS cached_tokens,
			t.execution_mode AS execution_mode, t.kind AS kind, COALESCE(CAST(t.parent_task_id AS TEXT), '') AS parent_task_id
		 FROM task_events te
		 JOIN tasks t ON t.id = te.task_id
		 WHERE `+where+`
		 LIMIT ?`, append(args, analyticsRowCap)...)
	if err != nil {
		return nil, false, fmt.Errorf("AnalyticsRepo.QueryTaskFacts agent: %w", err)
	}
	out := make([]TaskFact, 0, len(rows))
	seen := make(map[string]bool, len(rows))
	for _, x := range rows {
		name := agentNameFromTopic(x.Topic)
		if name == "" {
			continue
		}
		key := name + "\x00" + x.TaskID
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, TaskFact{
			TaskID:        x.TaskID,
			UserID:        x.UserID,
			AgentName:     name,
			Status:        x.Status,
			StartTimeMs:   x.StartTimeMs,
			EndTimeMs:     x.EndTimeMs,
			InputTokens:   x.InputTokens,
			OutputTokens:  x.OutputTokens,
			CachedTokens:  x.CachedTokens,
			ExecutionMode: x.ExecutionMode,
			Kind:          x.Kind,
			ParentTaskID:  x.ParentTaskID,
		})
	}
	return out, len(rows) >= analyticsRowCap, nil
}

func agentNameFromTopic(topic string) string {
	const marker = "/agent/request/"
	idx := strings.Index(topic, marker)
	if idx < 0 {
		return ""
	}
	rest := topic[idx+len(marker):]
	if slash := strings.IndexByte(rest, '/'); slash >= 0 {
		rest = rest[:slash]
	}
	return rest
}

type FeedbackAgg struct {
	Total          int
	ByRating       map[string]int
	RecentComments []FeedbackComment
}

type FeedbackComment struct {
	TaskID       string
	Rating       string
	Comment      string
	UpdatedAtMs  int64
	ShareContent bool
}

func (r *AnalyticsRepo) AggregateFeedback(ctx context.Context, startDate, endDate int64, recentLimit int) (*FeedbackAgg, error) {
	var clauses []string
	var args []any
	if startDate > 0 {
		clauses = append(clauses, "updated_at >= ?")
		args = append(args, startDate)
	}
	if endDate > 0 {
		clauses = append(clauses, "updated_at <= ?")
		args = append(args, endDate)
	}
	where := ""
	if len(clauses) > 0 {
		where = sqlWhere + strings.Join(clauses, " AND ")
	}

	type ratingRow struct {
		Rating string `db:"rating"`
		Count  int    `db:"count"`
	}
	ratingRows, err := dbutil.QueryRowPtrs[ratingRow](ctx, r.db,
		`SELECT rating, COUNT(*) AS count FROM feedback`+where+` GROUP BY rating`, args...)
	if err != nil {
		return nil, fmt.Errorf("AnalyticsRepo.AggregateFeedback ratings: %w", err)
	}
	byRating := make(map[string]int, len(ratingRows))
	total := 0
	for _, x := range ratingRows {
		byRating[x.Rating] = x.Count
		total += x.Count
	}

	commentWhere := where
	if commentWhere == "" {
		commentWhere = " WHERE comment IS NOT NULL AND comment <> ''"
	} else {
		commentWhere += " AND comment IS NOT NULL AND comment <> ''"
	}
	type commentRow struct {
		TaskID       string `db:"task_id"`
		Rating       string `db:"rating"`
		Comment      string `db:"comment"`
		ShareContent bool   `db:"share_content"`
		UpdatedAt    int64  `db:"updated_at"`
	}
	commentRows, err := dbutil.QueryRowPtrs[commentRow](ctx, r.db,
		`SELECT task_id, rating, COALESCE(comment, '') AS comment, share_content, updated_at
		 FROM feedback`+commentWhere+`
		 ORDER BY updated_at DESC LIMIT ?`, append(args, recentLimit)...)
	if err != nil {
		return nil, fmt.Errorf("AnalyticsRepo.AggregateFeedback comments: %w", err)
	}
	recent := make([]FeedbackComment, 0, len(commentRows))
	for _, x := range commentRows {
		recent = append(recent, FeedbackComment{TaskID: x.TaskID, Rating: x.Rating, Comment: x.Comment, UpdatedAtMs: x.UpdatedAt, ShareContent: x.ShareContent})
	}

	return &FeedbackAgg{Total: total, ByRating: byRating, RecentComments: recent}, nil
}

type ScheduledHealthAgg struct {
	TotalEnabled   int
	TotalDisabled  int
	ExecByStatus   map[string]int
	RecentFailures []ScheduledFailure
}

type ScheduledFailure struct {
	ScheduledTaskID         string
	Name                    string
	LastError               string
	ConsecutiveFailureCount int
}

func (r *AnalyticsRepo) AggregateScheduledHealth(ctx context.Context, namespace string, startDate, endDate int64, recentLimit int) (*ScheduledHealthAgg, error) {
	nsClauses := []string{"deleted_at IS NULL"}
	var nsArgs []any
	if namespace != "" {
		nsClauses = append(nsClauses, "namespace = ?")
		nsArgs = append(nsArgs, namespace)
	}
	nsWhere := sqlWhere + strings.Join(nsClauses, " AND ")

	enabled, err := dbutil.QueryVal[int](ctx, r.db,
		`SELECT COUNT(*) FROM scheduled_tasks`+nsWhere+` AND enabled = 1`, nsArgs...)
	if err != nil {
		return nil, fmt.Errorf("AnalyticsRepo.AggregateScheduledHealth enabled: %w", err)
	}
	disabled, err := dbutil.QueryVal[int](ctx, r.db,
		`SELECT COUNT(*) FROM scheduled_tasks`+nsWhere+` AND enabled = 0`, nsArgs...)
	if err != nil {
		return nil, fmt.Errorf("AnalyticsRepo.AggregateScheduledHealth disabled: %w", err)
	}

	var execClauses []string
	var execArgs []any
	if startDate > 0 {
		execClauses = append(execClauses, "scheduled_for >= ?")
		execArgs = append(execArgs, startDate)
	}
	if endDate > 0 {
		execClauses = append(execClauses, "scheduled_for <= ?")
		execArgs = append(execArgs, endDate)
	}
	execWhere := ""
	if len(execClauses) > 0 {
		execWhere = sqlWhere + strings.Join(execClauses, " AND ")
	}
	type statusRow struct {
		Status string `db:"status"`
		Count  int    `db:"count"`
	}
	statusRows, err := dbutil.QueryRowPtrs[statusRow](ctx, r.db,
		`SELECT status, COUNT(*) AS count FROM scheduled_task_executions`+execWhere+` GROUP BY status`, execArgs...)
	if err != nil {
		return nil, fmt.Errorf("AnalyticsRepo.AggregateScheduledHealth execStatus: %w", err)
	}
	execByStatus := make(map[string]int, len(statusRows))
	for _, x := range statusRows {
		execByStatus[x.Status] = x.Count
	}

	type failRow struct {
		ID                      string  `db:"id"`
		Name                    string  `db:"name"`
		ConsecutiveFailureCount int     `db:"consecutive_failure_count"`
		LastError               *string `db:"last_error"`
	}
	failRows, err := dbutil.QueryRowPtrs[failRow](ctx, r.db,
		`SELECT st.id AS id, st.name AS name, st.consecutive_failure_count AS consecutive_failure_count,
			(SELECT e.error_message FROM scheduled_task_executions e
			 WHERE e.scheduled_task_id = st.id AND e.error_message IS NOT NULL
			 ORDER BY e.scheduled_for DESC LIMIT 1) AS last_error
		 FROM scheduled_tasks st`+nsWhere+` AND st.enabled = 1 AND st.consecutive_failure_count > 0
		 ORDER BY st.consecutive_failure_count DESC, st.updated_at DESC
		 LIMIT ?`, append(nsArgs, recentLimit)...)
	if err != nil {
		return nil, fmt.Errorf("AnalyticsRepo.AggregateScheduledHealth failures: %w", err)
	}
	recent := make([]ScheduledFailure, 0, len(failRows))
	for _, x := range failRows {
		lastErr := ""
		if x.LastError != nil {
			lastErr = *x.LastError
		}
		recent = append(recent, ScheduledFailure{
			ScheduledTaskID:         x.ID,
			Name:                    x.Name,
			LastError:               lastErr,
			ConsecutiveFailureCount: x.ConsecutiveFailureCount,
		})
	}

	return &ScheduledHealthAgg{
		TotalEnabled:   enabled,
		TotalDisabled:  disabled,
		ExecByStatus:   execByStatus,
		RecentFailures: recent,
	}, nil
}

type ActivityHandlerPerfRow struct {
	HandlerName       string
	HandlerType       string
	InvocationCnt     int
	ErrorCnt          int
	DurationsMs       []int64
	InputTokens       int64
	OutputTokens      int64
	CachedInputTokens int64
}

func (r *AnalyticsRepo) AggregateActivityAgents(ctx context.Context, opts TaskAnalyticsOpts) ([]ActivityHandlerPerfRow, error) {
	return r.aggregateActivityHandlers(ctx, opts)
}

func (r *AnalyticsRepo) AggregateActivityWorkflows(ctx context.Context, opts TaskAnalyticsOpts) ([]ActivityHandlerPerfRow, error) {
	joined, args := opts.taskWhere("w")
	where := "w.handler_name IS NOT NULL"
	if joined != "" {
		where = joined + " AND " + where
	}

	failPH, failArgs, err := dbutil.InClause(FailedTaskStatuses)
	if err != nil {
		return nil, fmt.Errorf("AnalyticsRepo.AggregateActivityWorkflows failed-status clause: %w", err)
	}
	type aggRow struct {
		HandlerName string `db:"handler_name"`
		Invocations int    `db:"invocations"`
		Errors      int    `db:"errors"`
		InputTok    int64  `db:"in_tok"`
		OutputTok   int64  `db:"out_tok"`
		CachedTok   int64  `db:"cached_tok"`
	}
	aggArgs := make([]any, 0, len(args)+len(failArgs))
	aggArgs = append(aggArgs, args...)
	aggArgs = append(aggArgs, failArgs...)
	aggRows, err := dbutil.QueryRowPtrs[aggRow](ctx, r.db,
		`WITH RECURSIVE windowed_workflows AS (
			SELECT w.id, w.handler_name, w.status, w.start_time, w.end_time
			FROM tasks w
			WHERE `+where+`
		),
		descendants AS (
			-- depth guard: parent_task_id is unvalidated A2A metadata, so a
			-- self-cycle would otherwise loop forever (Postgres materializes the
			-- CTE before the outer LIMIT). 32 is ~10x real nesting.
			SELECT tasks.id AS descendant_id,
			       tasks.parent_task_id AS root_id,
			       tasks.total_input_tokens AS in_tok,
			       tasks.total_output_tokens AS out_tok,
			       tasks.total_cached_input_tokens AS cached_tok,
			       1 AS depth
			FROM tasks
			JOIN windowed_workflows ww ON tasks.parent_task_id = ww.id

			UNION ALL

			SELECT t.id AS descendant_id,
			       d.root_id AS root_id,
			       t.total_input_tokens AS in_tok,
			       t.total_output_tokens AS out_tok,
			       t.total_cached_input_tokens AS cached_tok,
			       d.depth + 1
			FROM tasks t
			JOIN descendants d ON t.parent_task_id = d.descendant_id
			WHERE d.depth < 32
		),
		subtree_totals AS (
			-- DISTINCT collapses a descendant re-emitted once per depth by a cyclic
			-- parent_task_id (unvalidated A2A metadata) so its tokens are summed
			-- once, not once per level. No-op for acyclic trees (one parent chain).
			SELECT root_id,
			       SUM(COALESCE(in_tok, 0))     AS subtree_in,
			       SUM(COALESCE(out_tok, 0))    AS subtree_out,
			       SUM(COALESCE(cached_tok, 0)) AS subtree_cached
			FROM (SELECT DISTINCT descendant_id, root_id, in_tok, out_tok, cached_tok FROM descendants) deduped
			GROUP BY root_id
		)
		SELECT ww.handler_name AS handler_name,
			COUNT(*) AS invocations,
			SUM(CASE WHEN ww.status IN (`+failPH+`) THEN 1 ELSE 0 END) AS errors,
			SUM(COALESCE(s.subtree_in, 0))     AS in_tok,
			SUM(COALESCE(s.subtree_out, 0))    AS out_tok,
			SUM(COALESCE(s.subtree_cached, 0)) AS cached_tok
		 FROM windowed_workflows ww
		 LEFT JOIN subtree_totals s ON s.root_id = ww.id
		 GROUP BY ww.handler_name`, aggArgs...)
	if err != nil {
		return nil, fmt.Errorf("AnalyticsRepo.AggregateActivityWorkflows: %w", err)
	}

	byName := make(map[string]*ActivityHandlerPerfRow, len(aggRows))
	for _, a := range aggRows {
		byName[a.HandlerName] = &ActivityHandlerPerfRow{
			HandlerName:       a.HandlerName,
			HandlerType:       TaskHandlerTypeWorkflow,
			InvocationCnt:     a.Invocations,
			ErrorCnt:          a.Errors,
			InputTokens:       a.InputTok,
			OutputTokens:      a.OutputTok,
			CachedInputTokens: a.CachedTok,
		}
	}
	if err := r.foldHandlerDurations(ctx, "", where, nil, args, "w", byName); err != nil {
		return nil, fmt.Errorf("AnalyticsRepo.AggregateActivityWorkflows durations: %w", err)
	}

	out := make([]ActivityHandlerPerfRow, 0, len(byName))
	for _, row := range byName {
		out = append(out, *row)
	}
	return out, nil
}

func (o TaskAnalyticsOpts) workflowNestedAnchorWhere() (string, []any) {
	joined, args := o.taskWhere("t0")
	where := "t0.parent_task_id IS NOT NULL"
	if joined != "" {
		where = joined + " AND " + where
	}
	return where, args
}

func (o TaskAnalyticsOpts) workflowNestedCTEPrefix() (string, []any) {
	if !o.ExcludeWorkflowNested {
		return "", nil
	}
	anchorWhere, args := o.workflowNestedAnchorWhere()
	prefix := `WITH RECURSIVE ancestor_chain AS (
			SELECT t0.id AS task_id, t0.parent_task_id AS ancestor_id, 1 AS depth
			FROM tasks t0
			WHERE ` + anchorWhere + `
			UNION ALL
			SELECT c.task_id, p.parent_task_id, c.depth + 1
			FROM ancestor_chain c
			JOIN tasks p ON p.id = c.ancestor_id
			WHERE p.parent_task_id IS NOT NULL AND c.depth < 32
		),
		workflow_nested AS (
			SELECT DISTINCT c.task_id
			FROM ancestor_chain c
			JOIN tasks a ON a.id = c.ancestor_id
			WHERE a.handler_type = 'workflow'
		)
		`
	return prefix, args
}

func (r *AnalyticsRepo) aggregateActivityHandlers(ctx context.Context, opts TaskAnalyticsOpts) ([]ActivityHandlerPerfRow, error) {
	joined, args := opts.taskWhere("t")
	where := "t.handler_name IS NOT NULL"
	if joined != "" {
		where = joined + " AND " + where
	}

	prefix, prefixArgs := opts.workflowNestedCTEPrefix()
	if opts.ExcludeWorkflowNested {
		where += ` AND NOT EXISTS (SELECT 1 FROM workflow_nested wn WHERE wn.task_id = t.id)`
	}

	failPH, failArgs, err := dbutil.InClause(FailedTaskStatuses)
	if err != nil {
		return nil, fmt.Errorf("AnalyticsRepo.aggregateActivityHandlers failed-status clause: %w", err)
	}
	type aggRow struct {
		HandlerName string  `db:"handler_name"`
		HandlerType *string `db:"handler_type"`
		Invocations int     `db:"invocations"`
		Errors      int     `db:"errors"`
		InputTok    int64   `db:"in_tok"`
		OutputTok   int64   `db:"out_tok"`
		CachedTok   int64   `db:"cached_tok"`
	}
	aggArgs := make([]any, 0, len(prefixArgs)+len(failArgs)+len(args))
	aggArgs = append(aggArgs, prefixArgs...)
	aggArgs = append(aggArgs, failArgs...)
	aggArgs = append(aggArgs, args...)
	aggRows, err := dbutil.QueryRowPtrs[aggRow](ctx, r.db,
		prefix+`SELECT t.handler_name AS handler_name,
			MAX(t.handler_type) AS handler_type,
			COUNT(*) AS invocations,
			SUM(CASE WHEN t.status IN (`+failPH+`) THEN 1 ELSE 0 END) AS errors,
			SUM(COALESCE(t.total_input_tokens, 0)) AS in_tok,
			SUM(COALESCE(t.total_output_tokens, 0)) AS out_tok,
			SUM(COALESCE(t.total_cached_input_tokens, 0)) AS cached_tok
		 FROM tasks t
		 WHERE `+where+`
		 GROUP BY t.handler_name`, aggArgs...)
	if err != nil {
		return nil, fmt.Errorf("AnalyticsRepo.aggregateActivityHandlers agg: %w", err)
	}

	byName := make(map[string]*ActivityHandlerPerfRow, len(aggRows))
	for _, a := range aggRows {
		ht := ""
		if a.HandlerType != nil {
			ht = *a.HandlerType
		}
		byName[a.HandlerName] = &ActivityHandlerPerfRow{
			HandlerName:       a.HandlerName,
			HandlerType:       ht,
			InvocationCnt:     a.Invocations,
			ErrorCnt:          a.Errors,
			InputTokens:       a.InputTok,
			OutputTokens:      a.OutputTok,
			CachedInputTokens: a.CachedTok,
		}
	}
	if err := r.foldHandlerDurations(ctx, prefix, where, prefixArgs, args, "t", byName); err != nil {
		return nil, fmt.Errorf("AnalyticsRepo.aggregateActivityHandlers durations: %w", err)
	}

	out := make([]ActivityHandlerPerfRow, 0, len(byName))
	for _, perf := range byName {
		out = append(out, *perf)
	}
	return out, nil
}

func (r *AnalyticsRepo) foldHandlerDurations(ctx context.Context, prefix, where string, prefixArgs, whereArgs []any, alias string, byName map[string]*ActivityHandlerPerfRow) error {
	type durRow struct {
		HandlerName string `db:"handler_name"`
		DurationMs  int64  `db:"duration_ms"`
	}
	durArgs := make([]any, 0, len(prefixArgs)+len(whereArgs)+2)
	durArgs = append(durArgs, prefixArgs...)
	durArgs = append(durArgs, whereArgs...)
	durArgs = append(durArgs, r.durationSampleCap, analyticsRowCap)
	rows, err := dbutil.QueryRowPtrs[durRow](ctx, r.db,
		prefix+`SELECT handler_name, duration_ms FROM (
			SELECT `+alias+`.handler_name AS handler_name,
				(`+alias+`.end_time - `+alias+`.start_time) AS duration_ms,
				ROW_NUMBER() OVER (PARTITION BY `+alias+`.handler_name ORDER BY `+alias+`.start_time DESC, `+alias+`.id DESC) AS rn
			 FROM tasks `+alias+`
			 WHERE `+where+` AND `+alias+`.end_time IS NOT NULL AND (`+alias+`.status IS NULL OR `+alias+`.status != '`+TaskStatusJanitorTimeout+`')
		 ) dur_sample WHERE rn <= ? LIMIT ?`, durArgs...)
	if err != nil {
		return err
	}
	for _, d := range rows {
		if perf, ok := byName[d.HandlerName]; ok {
			perf.DurationsMs = append(perf.DurationsMs, d.DurationMs)
		}
	}
	return nil
}

type ActivityWorkflowBreakdownRow struct {
	WorkflowName      string
	HandlerName       string
	HandlerType       string
	InvocationCnt     int
	ErrorCnt          int
	DurationsMs       []int64
	InputTokens       int64
	OutputTokens      int64
	CachedInputTokens int64
}

func (r *AnalyticsRepo) AggregateActivityWorkflowBreakdown(ctx context.Context, opts TaskAnalyticsOpts) ([]ActivityWorkflowBreakdownRow, error) {
	rootOpts := opts
	rootOpts.HandlerType = TaskHandlerTypeWorkflow
	joined, args := rootOpts.taskWhere("t")
	where := "t.handler_name IS NOT NULL"
	if joined != "" {
		where = joined + " AND " + where
	}

	failPH, failArgs, err := dbutil.InClause(FailedTaskStatuses)
	if err != nil {
		return nil, fmt.Errorf("AnalyticsRepo.AggregateActivityWorkflowBreakdown failed-status clause: %w", err)
	}

	cte := `WITH RECURSIVE roots AS (
		SELECT t.id AS root_id, t.handler_name AS workflow_name
		FROM tasks t
		WHERE ` + where + `
	), subtree AS (
		-- depth guard: bounds a cyclic parent_task_id (unvalidated A2A metadata)
		-- so the CTE can't loop forever. 32 is ~10x real workflow nesting.
		SELECT roots.root_id, roots.workflow_name, tasks.id AS descendant_id, 1 AS depth
		FROM tasks JOIN roots ON tasks.parent_task_id = roots.root_id
		UNION ALL
		SELECT subtree.root_id, subtree.workflow_name, tasks.id AS descendant_id, subtree.depth + 1
		FROM tasks JOIN subtree ON tasks.parent_task_id = subtree.descendant_id
		WHERE subtree.depth < 32
	)
	`

	type aggRow struct {
		WorkflowName string  `db:"workflow_name"`
		HandlerName  string  `db:"handler_name"`
		HandlerType  *string `db:"handler_type"`
		Invocations  int     `db:"invocations"`
		Errors       int     `db:"errors"`
		InputTok     int64   `db:"in_tok"`
		OutputTok    int64   `db:"out_tok"`
		CachedTok    int64   `db:"cached_tok"`
	}
	aggArgs := make([]any, 0, len(args)+len(failArgs))
	aggArgs = append(aggArgs, args...)
	aggArgs = append(aggArgs, failArgs...)
	aggRows, err := dbutil.QueryRowPtrs[aggRow](ctx, r.db,
		cte+`SELECT s.workflow_name AS workflow_name,
			d.handler_name AS handler_name,
			MAX(d.handler_type) AS handler_type,
			COUNT(*) AS invocations,
			SUM(CASE WHEN d.status IN (`+failPH+`) THEN 1 ELSE 0 END) AS errors,
			SUM(COALESCE(d.total_input_tokens, 0)) AS in_tok,
			SUM(COALESCE(d.total_output_tokens, 0)) AS out_tok,
			SUM(COALESCE(d.total_cached_input_tokens, 0)) AS cached_tok
		 FROM (SELECT DISTINCT root_id, workflow_name, descendant_id FROM subtree) s
		 JOIN tasks d ON d.id = s.descendant_id
		 WHERE d.handler_name IS NOT NULL
		 GROUP BY s.workflow_name, d.handler_name`, aggArgs...)
	if err != nil {
		return nil, fmt.Errorf("AnalyticsRepo.AggregateActivityWorkflowBreakdown: %w", err)
	}

	type key struct{ workflow, handler string }
	byKey := make(map[key]*ActivityWorkflowBreakdownRow, len(aggRows))
	for _, a := range aggRows {
		ht := ""
		if a.HandlerType != nil {
			ht = *a.HandlerType
		}
		byKey[key{a.WorkflowName, a.HandlerName}] = &ActivityWorkflowBreakdownRow{
			WorkflowName:      a.WorkflowName,
			HandlerName:       a.HandlerName,
			HandlerType:       ht,
			InvocationCnt:     a.Invocations,
			ErrorCnt:          a.Errors,
			InputTokens:       a.InputTok,
			OutputTokens:      a.OutputTok,
			CachedInputTokens: a.CachedTok,
		}
	}

	type durRow struct {
		WorkflowName string `db:"workflow_name"`
		HandlerName  string `db:"handler_name"`
		DurationMs   int64  `db:"duration_ms"`
	}
	durArgs := make([]any, 0, len(args)+2)
	durArgs = append(durArgs, args...)
	durArgs = append(durArgs, r.durationSampleCap, analyticsRowCap)
	durRows, err := dbutil.QueryRowPtrs[durRow](ctx, r.db,
		cte+`SELECT workflow_name, handler_name, duration_ms FROM (
			SELECT s.workflow_name AS workflow_name, d.handler_name AS handler_name,
				(d.end_time - d.start_time) AS duration_ms,
				ROW_NUMBER() OVER (PARTITION BY s.workflow_name, d.handler_name ORDER BY d.start_time DESC, d.id DESC) AS rn
			 FROM subtree s
			 JOIN tasks d ON d.id = s.descendant_id
			 WHERE d.handler_name IS NOT NULL AND d.end_time IS NOT NULL AND (d.status IS NULL OR d.status != '`+TaskStatusJanitorTimeout+`')
		 ) dur_sample WHERE rn <= ? LIMIT ?`, durArgs...)
	if err != nil {
		return nil, fmt.Errorf("AnalyticsRepo.AggregateActivityWorkflowBreakdown durations: %w", err)
	}
	for _, d := range durRows {
		if entry, ok := byKey[key{d.WorkflowName, d.HandlerName}]; ok {
			entry.DurationsMs = append(entry.DurationsMs, d.DurationMs)
		}
	}

	out := make([]ActivityWorkflowBreakdownRow, 0, len(byKey))
	for _, entry := range byKey {
		out = append(out, *entry)
	}
	return out, nil
}

func (r *AnalyticsRepo) GetTaskDescendants(ctx context.Context, rootID string, maxDepth, maxRows int) ([]Task, error) {
	if maxDepth < 1 {
		maxDepth = 3
	}
	if maxRows < 1 {
		maxRows = 50
	}
	query := `WITH RECURSIVE subtree AS (
		SELECT t.*, 1 AS depth, ',' || t.id || ',' AS path
		FROM tasks t
		WHERE t.parent_task_id = ? AND t.id <> ?
		UNION ALL
		SELECT t.*, s.depth + 1 AS depth, s.path || t.id || ',' AS path
		FROM tasks t JOIN subtree s ON t.parent_task_id = s.id
		WHERE s.depth < ?
		  AND s.path NOT LIKE ('%,' || t.id || ',%')
	)
	SELECT id, user_id, parent_task_id, start_time, end_time, status,
		initial_request_text, total_input_tokens, total_output_tokens,
		total_cached_input_tokens, token_usage_details, execution_mode,
		last_activity_time, background_execution_enabled, max_execution_time_ms,
		session_id, events_buffered, events_consumed, kind,
		post_compaction_remaining_tokens, handler_name, handler_type, error_message
	FROM subtree
	ORDER BY depth ASC, start_time ASC, id ASC
	LIMIT ?`
	rows, err := dbutil.QueryRowPtrs[Task](ctx, r.db, query, rootID, rootID, maxDepth, maxRows)
	if err != nil {
		return nil, fmt.Errorf("AnalyticsRepo.GetTaskDescendants(%s): %w", rootID, err)
	}
	out := make([]Task, 0, len(rows))
	for _, r := range rows {
		out = append(out, *r)
	}
	return out, nil
}

func (r *AnalyticsRepo) WalkAncestors(ctx context.Context, taskID string, maxHops int) ([]Task, error) {
	if maxHops < 1 {
		maxHops = 10
	}
	query := `WITH RECURSIVE ancestors AS (
		SELECT
			id, user_id, parent_task_id, start_time, end_time, status,
			initial_request_text, total_input_tokens, total_output_tokens,
			total_cached_input_tokens, token_usage_details, execution_mode,
			last_activity_time, background_execution_enabled, max_execution_time_ms,
			session_id, events_buffered, events_consumed, kind,
			post_compaction_remaining_tokens, handler_name, handler_type, error_message,
			0 AS hops, ',' || id || ',' AS path
		FROM tasks WHERE id = ?
		UNION ALL
		SELECT
			t.id, t.user_id, t.parent_task_id, t.start_time, t.end_time, t.status,
			t.initial_request_text, t.total_input_tokens, t.total_output_tokens,
			t.total_cached_input_tokens, t.token_usage_details, t.execution_mode,
			t.last_activity_time, t.background_execution_enabled, t.max_execution_time_ms,
			t.session_id, t.events_buffered, t.events_consumed, t.kind,
			t.post_compaction_remaining_tokens, t.handler_name, t.handler_type, t.error_message,
			a.hops + 1, a.path || t.id || ','
		FROM tasks t
		JOIN ancestors a ON t.id = a.parent_task_id
		WHERE a.hops < ?
		  AND a.path NOT LIKE ('%,' || t.id || ',%')
	)
	SELECT id, user_id, parent_task_id, start_time, end_time, status,
		initial_request_text, total_input_tokens, total_output_tokens,
		total_cached_input_tokens, token_usage_details, execution_mode,
		last_activity_time, background_execution_enabled, max_execution_time_ms,
		session_id, events_buffered, events_consumed, kind,
		post_compaction_remaining_tokens, handler_name, handler_type, error_message
	FROM ancestors ORDER BY hops ASC`
	rows, err := dbutil.QueryRowPtrs[Task](ctx, r.db, query, taskID, maxHops)
	if err != nil {
		return nil, fmt.Errorf("AnalyticsRepo.WalkAncestors(%s): %w", taskID, err)
	}
	out := make([]Task, 0, len(rows))
	for _, r := range rows {
		out = append(out, *r)
	}
	return out, nil
}

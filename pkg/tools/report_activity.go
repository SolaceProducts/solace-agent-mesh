// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

type ReportActivityTool struct {
	tool.SideEffectingTool
	artifactToolBase
}

func notifyArtifactSavedInProcessBecauseFrameworkSkipsIt(ctx context.Context, toolCtx *tool.Context, htmlName string, htmlVersion int, htmlBytes []byte) {
	if toolCtx.NotifyArtifactSaved != nil {
		toolCtx.NotifyArtifactSaved(ctx, tool.ArtifactSavedInfo{
			Filename:    htmlName,
			Version:     htmlVersion,
			MIMEType:    "text/html",
			SizeBytes:   int64(len(htmlBytes)),
			Description: activityReportHTMLDesc,
			ToolCallID:  toolCtx.ToolCallID,
		})
	}
}

func NewReportActivityTool() *ReportActivityTool {
	return &ReportActivityTool{}
}

func (t *ReportActivityTool) Name() string { return "report_activity" }

func (t *ReportActivityTool) Description() string {
	return "Produces the Activity Monitor's HTML report for a time window (see `window` param; default 24h). " +
		"Renders an inline dark-theme HTML document with Chart.js visualizations plus a short chat digest. " +
		"The HTML artifact renders in the chat preview pane. Emit the digest message verbatim; the report " +
		"artifact is auto-attached. byAgent/byWorkflow show at most the top 10 subjects by invocation count, " +
		"and tokenTrend covers at most 30 days starting from the window's beginning — a low-traffic agent or " +
		"a day near the end of a long window may be missing; say so rather than treating an absence as zero. " +
		"The full underlying data (every user, every feedback row — each section capped at its own " +
		"internal safety ceiling; topUsers.truncated / feedbackTruncated signal if hit) is also saved as a " +
		"JSON artifact named '<report>_data.json' (see artifacts_created). It does NOT include the " +
		"per-workflow sub-agent breakdown shown in the HTML table — if asked about a specific sub-agent " +
		"within a workflow, point the user to the HTML report rather than guessing. Query it with " +
		"transform_data_with_jmespath for anything not already in the digest — but check topUsers.truncated " +
		"/ feedbackTruncated first: if either is true, a length() count only reflects what was returned, " +
		"not the true total, and you must say so rather than reporting it as exact. Never paste or repeat " +
		"this artifact's raw content, and don't volunteer its existence unless asked — your reply is still " +
		"the digest message verbatim, plus the attached HTML report."
}

func (t *ReportActivityTool) RequiredScopes() []string { return nil }

func (t *ReportActivityTool) Parameters() *tool.JSONSchema {
	return &tool.JSONSchema{
		Type: "object",
		Properties: withActivityWindow(map[string]any{
			"userId": map[string]any{
				"type":        "string",
				"description": "Optional drill-down: narrow every section to a single user. Requires activity:*:read unless the value matches the caller's own user ID. Omitted under admin means tenant-wide.",
			},
		}),
	}
}

func (t *ReportActivityTool) ResultSchema() *tool.JSONSchema {
	return emptyResultSchema()
}

const activityReportHTMLDesc = "Activity Monitor report (interactive HTML; opens in the preview pane)"

const activityReportLLMDataDesc = "Activity Monitor report data (full JSON dataset for LLM querying via transform_data_with_jmespath; not for the end user)"

func (t *ReportActivityTool) Execute(ctx context.Context, args map[string]any, toolCtx *tool.Context) (*tool.Result, error) {
	if toolCtx == nil || toolCtx.ControlQuerier == nil {
		return tool.Error("Analytics service is not available. Ensure the gateway is running with the analytics service configured."), nil
	}

	windowKeyArg, _ := args["window"].(string)
	startDateArg, _ := args["startDate"].(string)
	endDateArg, _ := args["endDate"].(string)
	window, err := parseWindowArgsWithKey(windowKeyArg, startDateArg, endDateArg, time.Now())
	if err != nil {
		return tool.Error(err.Error()), nil
	}
	scope := resolveActivityScope(toolCtx)
	filters := activityFilters{
		TargetUserID: stringArg(args, "userId"),
		CallerUserID: callerUserID(toolCtx),
	}
	logger := slog.Default().With(
		"logger_name", "activity-report",
		"window", window.Label,
		"scope", scope,
		"filterUser", filters.TargetUserID,
		"traceID", toolTraceID(toolCtx),
	)
	if scope == ActivityScopeReadOwn && filters.TargetUserID != "" && filters.TargetUserID != filters.CallerUserID {
		logger.Warn("activity report cross-user access denied",
			"callerUserID", filters.CallerUserID, "targetUserID", filters.TargetUserID)
		return tool.Error("userId requires the activity:*:read scope"), nil
	}

	taskHealth, err := fetchTaskHealth(ctx, toolCtx, window, filters)
	if err != nil {
		logger.Warn("fetch task health failed", "error", err)
		return tool.Error(fmt.Sprintf("Failed to fetch task health: %s", err)), nil
	}

	if filters.TargetUserID != "" && taskHealth.TotalTasks == 0 {
		return tool.Error(fmt.Sprintf(
			"No activity found for user %q in the requested window (%s). Verify the user ID is correct or widen the window.",
			filters.TargetUserID, window.Label)), nil
	}

	priorWin, err := priorWindow(window)
	var priorHealth *taskHealthResp
	if err != nil {
		logger.Warn("compute prior window failed; skipping deltas", "error", err)
	} else if ph, perr := fetchTaskHealth(ctx, toolCtx, priorWin, filters); perr != nil {
		logger.Warn("fetch prior task health failed; skipping deltas", "error", perr)
	} else {
		priorHealth = ph
	}

	var agentPerf, workflowPerf []agentPerfResp
	var workflowBreakdown []workflowBreakdownResp
	if agentPerf, err = fetchAgentPerformance(ctx, toolCtx, window, filters, store.TaskHandlerTypeAgent); err != nil {
		logger.Warn("fetch agent performance failed", "error", err)
		return tool.Error(fmt.Sprintf("Failed to fetch agent performance: %s", err)), nil
	}
	if workflowPerf, err = fetchAgentPerformance(ctx, toolCtx, window, filters, store.TaskHandlerTypeWorkflow); err != nil {
		logger.Warn("fetch workflow performance failed", "error", err)
		return tool.Error(fmt.Sprintf("Failed to fetch workflow performance: %s", err)), nil
	}
	if wb, wberr := fetchWorkflowBreakdown(ctx, toolCtx, window, filters); wberr != nil {
		logger.Warn("fetch workflow breakdown failed; sub-rows will be empty", "error", wberr)
	} else {
		workflowBreakdown = wb
	}

	failures, failuresTotal, err := fetchRecentFailures(ctx, toolCtx, window, filters)
	if err != nil {
		logger.Warn("fetch recent failures failed; emitting empty list", "error", err)
		failures = nil
		failuresTotal = 0
	}

	feedback, feedbackTruncated, ferr := fetchFeedback(ctx, toolCtx, window, filters)
	if ferr != nil {
		logger.Warn("fetch feedback failed; emitting empty list", "error", ferr)
		feedback = nil
		feedbackTruncated = false
	}

	multiDay := windowSpansMultipleDays(window)
	var tokenTrend []tokenUsageRow
	if multiDay {
		if tt, terr := fetchTokenUsage(ctx, toolCtx, window, filters, "day"); terr != nil {
			logger.Warn("fetch token trend failed; skipping", "error", terr)
		} else {
			tokenTrend = tt
		}
	}

	var topUsers []tokenUsageRow
	var topUsersTruncated bool
	if scope == ActivityScopeReadAll && filters.TargetUserID == "" {
		if tu, trunc, uerr := fetchTopUsers(ctx, toolCtx, window, filters); uerr != nil {
			logger.Warn("fetch top users failed; skipping", "error", uerr)
		} else {
			topUsers, topUsersTruncated = tu, trunc
		}
	}

	report := composeActivityReport(scope, window, taskHealth, priorHealth, agentPerf, workflowPerf, workflowBreakdown, failures, failuresTotal, tokenTrend, topUsers, topUsersTruncated, multiDay, feedback, feedbackTruncated)

	generatedAt := time.Now().UTC()
	htmlName := reportFilenameStem("activity_report", window, scope, filters) + ".html"
	htmlBytes, htmlVersion, err := renderActivityReport(ctx, toolCtx, report, filters, generatedAt, htmlName)
	if err != nil {
		logger.Warn("render activity_report HTML failed", "error", err)
		return tool.Error(fmt.Sprintf("Failed to render activity_report HTML: %s", err)), nil
	}

	notifyArtifactSavedInProcessBecauseFrameworkSkipsIt(ctx, toolCtx, htmlName, htmlVersion, htmlBytes)

	digest := composeChatDigest(report, filters)

	llmData := buildActivityReportLLMData(report)
	llmDataJSON, err := json.Marshal(llmData)
	if err != nil {
		logger.Error("marshal activity report LLM data failed; omitting", "error", err)
		llmDataJSON = nil
	}

	dataObjects := []tool.DataObject{
		{
			Name:        htmlName,
			ArtifactRef: &tool.ArtifactRef{Filename: htmlName, Version: htmlVersion, SizeBytes: int64(len(htmlBytes))},
			MIMEType:    "text/html",
			Disposition: tool.DispositionArtifact,
			Description: activityReportHTMLDesc,
		},
	}
	if llmDataJSON != nil {
		dataObjects = append(dataObjects, tool.DataObject{
			Name:        reportFilenameStem("activity_report", window, scope, filters) + "_data.json",
			Content:     llmDataJSON,
			MIMEType:    "application/json",
			Disposition: tool.DispositionArtifact,
			Description: activityReportLLMDataDesc,
			Tags:        []string{a2a.ArtifactTagWorking},
		})
	}
	return tool.OK(digest, tool.WithDataObjects(dataObjects...)), nil
}

type activityFilters struct {
	TargetUserID string
	CallerUserID string
}

func resolveActivityScope(toolCtx *tool.Context) string {
	if toolCtx == nil || toolCtx.Identity == nil {
		return ActivityScopeReadOwn
	}
	if rbac.HasStrictScope(toolCtx.Identity.Scopes, rbac.ScopeActivityRead) {
		return ActivityScopeReadAll
	}
	return ActivityScopeReadOwn
}

func callerUserID(toolCtx *tool.Context) string {
	if toolCtx == nil || toolCtx.Identity == nil {
		return ""
	}
	return toolCtx.Identity.UserID
}

func stringArg(args map[string]any, key string) string {
	v, _ := args[key].(string)
	return v
}

func applyWindow(body map[string]any, window Window) error {
	if window.WindowKey != "" {
		body["window"] = window.WindowKey
		return nil
	}
	start, err := time.Parse(time.RFC3339, window.StartTimeIso)
	if err != nil {
		return fmt.Errorf("parse window start: %w", err)
	}
	end, err := time.Parse(time.RFC3339, window.EndTimeIso)
	if err != nil {
		return fmt.Errorf("parse window end: %w", err)
	}
	body["startDate"] = start.UnixMilli()
	body["endDate"] = end.UnixMilli()
	return nil
}

func windowControlBody(window Window, filters activityFilters) (json.RawMessage, error) {
	body := map[string]any{}
	if err := applyWindow(body, window); err != nil {
		return nil, err
	}
	if filters.TargetUserID != "" {
		body["userId"] = filters.TargetUserID
	}
	return json.Marshal(body)
}

type taskHealthResp struct {
	TotalTasks            int            `json:"totalTasks"`
	ByStatus              map[string]int `json:"byStatus"`
	SuccessRate           float64        `json:"successRate"`
	ErrorRate             float64        `json:"errorRate"`
	AvgDurationMs         int64          `json:"avgDurationMs"`
	P50DurationMs         int64          `json:"p50DurationMs"`
	P95DurationMs         int64          `json:"p95DurationMs"`
	DurationMeasuredCount int            `json:"durationMeasuredCount"`
	ActiveBackgroundCount int            `json:"activeBackgroundCount"`
	TaskRetentionDays     int            `json:"taskRetentionDays"`
	DurationBucketCounts  []int          `json:"durationBucketCounts"`
}

type tokenUsageRow struct {
	GroupKey               string  `json:"groupKey"`
	PeriodStart            *string `json:"periodStart"`
	TotalInputTokens       int64   `json:"totalInputTokens"`
	TotalOutputTokens      int64   `json:"totalOutputTokens"`
	TotalCachedInputTokens int64   `json:"totalCachedInputTokens"`
	TaskCount              int     `json:"taskCount"`
}

type pageResp[T any] struct {
	Data []T `json:"data"`
	Meta struct {
		Pagination struct {
			Count int `json:"count"`
		} `json:"pagination"`
		Truncated bool `json:"truncated,omitempty"`
	} `json:"meta"`
}

type agentPerfResp struct {
	Agent struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"agent"`
	InvocationCount        int     `json:"invocationCount"`
	ErrorCount             int     `json:"errorCount"`
	SuccessRate            float64 `json:"successRate"`
	AvgDurationMs          int64   `json:"avgDurationMs"`
	P95DurationMs          int64   `json:"p95DurationMs"`
	DurationMeasuredCount  int     `json:"durationMeasuredCount"`
	TotalInputTokens       int64   `json:"totalInputTokens"`
	TotalOutputTokens      int64   `json:"totalOutputTokens"`
	TotalCachedInputTokens int64   `json:"totalCachedInputTokens"`
}

type workflowBreakdownResp struct {
	Workflow               agentRefResp `json:"workflow"`
	Agent                  agentRefResp `json:"agent"`
	InvocationCount        int          `json:"invocationCount"`
	ErrorCount             int          `json:"errorCount"`
	SuccessRate            float64      `json:"successRate"`
	TotalInputTokens       int64        `json:"totalInputTokens"`
	TotalOutputTokens      int64        `json:"totalOutputTokens"`
	TotalCachedInputTokens int64        `json:"totalCachedInputTokens"`
}

type agentRefResp struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Type string `json:"type,omitempty"`
}

type failedTaskResp struct {
	ID                 string  `json:"id"`
	Name               string  `json:"name"`
	Type               string  `json:"type"`
	UserID             string  `json:"userId"`
	Status             string  `json:"status"`
	StartDate          string  `json:"startDate"`
	DurationMs         *int64  `json:"durationMs"`
	ExecutionMode      string  `json:"executionMode"`
	InitialRequestText string  `json:"initialRequestText"`
	EndDate            *string `json:"endDate"`
}

type feedbackResp struct {
	TaskID         string       `json:"taskId"`
	UserID         string       `json:"userId"`
	Rating         string       `json:"rating"`
	Comment        *string      `json:"comment"`
	Subject        agentRefResp `json:"subject"`
	EntrypointType string       `json:"entrypointType"`
	UpdatedAt      string       `json:"updatedAt"`
}

func fetchPaginatedControl[T any](
	ctx context.Context,
	toolCtx *tool.Context,
	endpoint string,
	window Window,
	filters activityFilters,
	pageSize int,
	extra map[string]any,
) ([]T, error) {
	body, err := windowControlBody(window, filters)
	if err != nil {
		return nil, err
	}
	var asMap map[string]any
	if unmErr := json.Unmarshal(body, &asMap); unmErr != nil {
		return nil, fmt.Errorf("repack control body: %w", unmErr)
	}
	asMap["pageNumber"] = 1
	asMap["pageSize"] = pageSize
	for k, v := range extra {
		asMap[k] = v
	}
	body, err = json.Marshal(asMap)
	if err != nil {
		return nil, fmt.Errorf("re-marshal control body: %w", err)
	}
	raw, err := toolCtx.ControlQuerier.Custom(ctx, gatewayControlApp, "get", endpoint, body)
	if err != nil {
		return nil, err
	}
	var page pageResp[T]
	if pErr := json.Unmarshal(raw, &page); pErr != nil {
		return nil, fmt.Errorf("parse %s response: %w", endpoint, pErr)
	}
	return page.Data, nil
}

func fetchPaginatedControlWithTotal[T any](
	ctx context.Context,
	toolCtx *tool.Context,
	endpoint string,
	window Window,
	filters activityFilters,
	pageSize int,
	extra map[string]any,
) ([]T, int, error) {
	body, err := windowControlBody(window, filters)
	if err != nil {
		return nil, 0, err
	}
	var asMap map[string]any
	if unmErr := json.Unmarshal(body, &asMap); unmErr != nil {
		return nil, 0, fmt.Errorf("repack control body: %w", unmErr)
	}
	asMap["pageNumber"] = 1
	asMap["pageSize"] = pageSize
	for k, v := range extra {
		asMap[k] = v
	}
	body, err = json.Marshal(asMap)
	if err != nil {
		return nil, 0, fmt.Errorf("re-marshal control body: %w", err)
	}
	raw, err := toolCtx.ControlQuerier.Custom(ctx, gatewayControlApp, "get", endpoint, body)
	if err != nil {
		return nil, 0, err
	}
	var page pageResp[T]
	if pErr := json.Unmarshal(raw, &page); pErr != nil {
		return nil, 0, fmt.Errorf("parse %s response: %w", endpoint, pErr)
	}
	return page.Data, page.Meta.Pagination.Count, nil
}

func fetchTaskHealth(ctx context.Context, toolCtx *tool.Context, window Window, filters activityFilters) (*taskHealthResp, error) {
	body, err := windowControlBody(window, filters)
	if err != nil {
		return nil, err
	}
	raw, err := toolCtx.ControlQuerier.Custom(ctx, gatewayControlApp, "get", "activity/tasks/summary", body)
	if err != nil {
		return nil, err
	}
	var out taskHealthResp
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("parse task health response: %w", err)
	}
	return &out, nil
}

func fetchAgentPerformance(ctx context.Context, toolCtx *tool.Context, window Window, filters activityFilters, subjectType string) ([]agentPerfResp, error) {
	var endpoint string
	switch subjectType {
	case store.TaskHandlerTypeAgent:
		endpoint = "activity/agents/performance"
	case store.TaskHandlerTypeWorkflow:
		endpoint = "activity/workflows/performance"
	default:
		return nil, fmt.Errorf("fetchAgentPerformance: unsupported subjectType %q", subjectType)
	}
	return fetchPaginatedControl[agentPerfResp](ctx, toolCtx, endpoint, window, filters, 10, nil)
}

func fetchWorkflowBreakdown(ctx context.Context, toolCtx *tool.Context, window Window, filters activityFilters) ([]workflowBreakdownResp, error) {
	return fetchPaginatedControl[workflowBreakdownResp](
		ctx, toolCtx, "activity/workflows/breakdown", window, filters, 100, nil,
	)
}

func fetchRecentFailures(ctx context.Context, toolCtx *tool.Context, window Window, filters activityFilters) ([]failedTaskResp, int, error) {
	return fetchPaginatedControlWithTotal[failedTaskResp](
		ctx, toolCtx, "activity/tasks/failed", window, filters, 10, nil,
	)
}

func fetchFeedback(ctx context.Context, toolCtx *tool.Context, window Window, filters activityFilters) ([]feedbackResp, bool, error) {
	body, err := windowControlBody(window, filters)
	if err != nil {
		return nil, false, err
	}
	raw, err := toolCtx.ControlQuerier.Custom(ctx, gatewayControlApp, "get", "activity/feedback", body)
	if err != nil {
		return nil, false, err
	}
	var page pageResp[feedbackResp]
	if err := json.Unmarshal(raw, &page); err != nil {
		return nil, false, fmt.Errorf("parse activity/feedback response: %w", err)
	}
	return page.Data, page.Meta.Truncated, nil
}

func fetchTokenUsage(ctx context.Context, toolCtx *tool.Context, window Window, filters activityFilters, groupBy string) ([]tokenUsageRow, error) {
	return fetchPaginatedControl[tokenUsageRow](
		ctx, toolCtx, "activity/tokens/summary", window, filters, 30,
		map[string]any{"groupBy": groupBy},
	)
}

const maxTopUsersFetchSize = 5000

func fetchTopUsers(ctx context.Context, toolCtx *tool.Context, window Window, filters activityFilters) ([]tokenUsageRow, bool, error) {
	rows, total, err := fetchPaginatedControlWithTotal[tokenUsageRow](
		ctx, toolCtx, "activity/tokens/summary", window, filters, maxTopUsersFetchSize,
		map[string]any{"groupBy": "user"},
	)
	if err != nil {
		return nil, false, err
	}
	return rows, total > len(rows), nil
}

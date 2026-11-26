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
	"strconv"
	"strings"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

const gatewayControlApp = "gateway"

const defaultAnalyticsWindow = "24h"

type SentinelAnalyticsTool struct {
	tool.ReadOnlyTool
	artifactToolBase
	name        string
	description string
	path        string
	paginated   bool
	threshold   bool
	groupBy     bool
}

func newSentinelAnalyticsTool(name, description, path string, paginated, threshold, groupBy bool) *SentinelAnalyticsTool {
	return &SentinelAnalyticsTool{
		name:        name,
		description: description,
		path:        path,
		paginated:   paginated,
		threshold:   threshold,
		groupBy:     groupBy,
	}
}

func NewQueryTaskHealthTool() *SentinelAnalyticsTool {
	return newSentinelAnalyticsTool("query_task_health",
		"Returns a health summary of tasks over a time window: total count, breakdown by status, "+
			"success/error rates, average and p50/p95 durations, and active background task count. "+
			"Use this first for an overall picture of mesh health.",
		"analytics/tasks/summary", false, false, false)
}

func NewQueryFailedTasksTool() *SentinelAnalyticsTool {
	return newSentinelAnalyticsTool("query_failed_tasks",
		"Lists tasks that failed (status failed/error/timeout) in a time window, most recent first, with "+
			"their IDs, status, duration, and initial request text. Use the task IDs to drill in or correlate "+
			"with logs.",
		"analytics/tasks/failed", true, false, false)
}

func NewQuerySlowTasksTool() *SentinelAnalyticsTool {
	return newSentinelAnalyticsTool("query_slow_tasks",
		"Lists the slowest completed tasks in a time window, longest first. Optionally filter by a minimum "+
			"duration (threshold_ms). Use to find latency outliers.",
		"analytics/tasks/slow", true, true, false)
}

func NewQueryAgentPerformanceTool() *SentinelAnalyticsTool {
	return newSentinelAnalyticsTool("query_agent_performance",
		"Returns per-agent performance over a time window: invocation count, error count, success rate, and "+
			"average/p95 duration. Each row identifies the agent as {id, name} with its human-readable name "+
			"(falling back to the internal id when the agent is not currently registered). Attribution is "+
			"best-effort (derived from task routing events); tool-level metrics are not included here.",
		"analytics/agents/performance", true, false, false)
}

func NewQueryTokenUsageTool() *SentinelAnalyticsTool {
	return newSentinelAnalyticsTool("query_token_usage",
		"Returns token usage (input/output/cached token sums and task counts) grouped by day or by user over "+
			"a time window. Note: this reports tokens only, not cost — cost lives in the metrics/Datadog plane.",
		"analytics/tokens/summary", true, false, true)
}

func NewQueryScheduledTaskHealthTool() *SentinelAnalyticsTool {
	return newSentinelAnalyticsTool("query_scheduled_task_health",
		"Returns scheduled-task health: counts of enabled/disabled tasks, execution outcomes by status, and "+
			"a list of currently-failing scheduled tasks with their last error.",
		"analytics/scheduledTasks/health", false, false, false)
}

func NewQueryFeedbackSummaryTool() *SentinelAnalyticsTool {
	return newSentinelAnalyticsTool("query_feedback_summary",
		"Returns a summary of user feedback over a time window: total count, breakdown by rating, and recent "+
			"comments. Use to gauge user satisfaction and surface complaints.",
		"analytics/feedback/summary", false, false, false)
}

type QueryTasksTool struct {
	tool.ReadOnlyTool
	artifactToolBase
}

func NewQueryTasksTool() *QueryTasksTool { return &QueryTasksTool{} }

func (t *QueryTasksTool) Name() string { return "query_tasks" }

func (t *QueryTasksTool) Description() string {
	return "General task analytics over a time window — the flexible companion to the fixed reports. " +
		"Filter by agent_name (display name, slug, or id), user_id, status, and/or execution_mode, and " +
		"optionally group_by a combination of: agent, user, status, day, week, month (at most one time " +
		"grouping). With NO group_by it returns granular per-task rows (id, agent, user, status, duration, " +
		"tokens, timestamps) — capped and paginated, for drill-down. WITH group_by it returns one aggregated " +
		"row per bucket (taskCount, errorCount, successRate, avg/p95 durationMs, token sums). Examples: " +
		"group_by=['agent','month'] for a per-agent monthly trend; group_by=['agent'] with user_id set for " +
		"one user's tasks broken down by agent; group_by=['status'] for an outcome distribution. The result " +
		"is a JSON artifact you can chart or query with the data_analysis tools. Grouping or filtering by " +
		"user requires the content access tier. Tokens only — no cost (cost lives in the metrics plane). " +
		"Very large windows are capped at a recent sample; when that happens the result's meta.truncated is " +
		"true and the metrics cover only that sample — narrow the window and say so in your report."
}

func (t *QueryTasksTool) Parameters() *tool.JSONSchema {
	return &tool.JSONSchema{Type: "object", Properties: map[string]any{
		"window": map[string]any{
			"type":        "string",
			"description": "Relative lookback window ending now, e.g. '24h', '7d', '90d'. Defaults to '24h'. Ignored when start_time is given.",
		},
		"start_time": map[string]any{
			"type":        "string",
			"description": "Optional ISO 8601 start of the window (UTC). Overrides 'window'.",
		},
		"end_time": map[string]any{
			"type":        "string",
			"description": "Optional ISO 8601 end of the window (UTC). Defaults to now.",
		},
		"execution_mode": map[string]any{
			"type":        "string",
			"description": "Optional filter: 'foreground' or 'background'.",
			"enum":        []string{"foreground", "background"},
		},
		"agent_name": map[string]any{
			"type":        "string",
			"description": "Optional: restrict to one agent, named by its display name, runtime slug, or platform id.",
		},
		"user_id": map[string]any{
			"type":        "string",
			"description": "Optional: restrict to one user's tasks. Requires the content access tier.",
		},
		"status": map[string]any{
			"type":        "string",
			"description": "Optional: restrict to one task status (e.g. 'completed', 'failed', 'error', 'timeout', 'cancelled', 'running').",
		},
		"group_by": map[string]any{
			"type":        "array",
			"items":       map[string]any{"type": "string", "enum": []string{"agent", "user", "status", "day", "week", "month"}},
			"description": "Optional grouping. Combine entity dims (agent, user, status) with at most one time bucket (day, week, month). Omit for granular per-task rows. Grouping by user requires the content access tier.",
		},
		"page_number": map[string]any{"type": "integer", "description": "1-based page number. Default 1."},
		"page_size":   map[string]any{"type": "integer", "description": "Page size (1-100). Default 20."},
	}}
}

func (t *QueryTasksTool) ResultSchema() *tool.JSONSchema {
	return emptyResultSchema()
}

func (t *QueryTasksTool) Execute(ctx context.Context, args map[string]any, toolCtx *tool.Context) (*tool.Result, error) {
	if toolCtx.ControlQuerier == nil {
		return tool.Error("Analytics service is not available. Ensure the gateway is running with the analytics service configured."), nil
	}
	body, err := buildAnalyticsWindowBody(args)
	if err != nil {
		return tool.Error(err.Error()), nil
	}
	if v := sentinelStrArg(args, "agent_name"); v != "" {
		body["agentName"] = v
	}
	if v := sentinelStrArg(args, "user_id"); v != "" {
		body["userId"] = v
	}
	if v := sentinelStrArg(args, "status"); v != "" {
		body["status"] = v
	}
	if dims := sentinelStrSliceArg(args, "group_by"); len(dims) > 0 {
		body["groupDims"] = dims
	}
	if n, ok := sentinelIntArg(args, "page_number"); ok {
		body["pageNumber"] = n
	}
	if n, ok := sentinelIntArg(args, "page_size"); ok {
		body["pageSize"] = n
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return tool.Error(fmt.Sprintf("Failed to encode request: %s", err)), nil
	}

	start := time.Now()
	raw, err := toolCtx.ControlQuerier.Custom(ctx, gatewayControlApp, "get", "analytics/tasks/query", encoded)
	if err != nil {
		slog.Default().With("logger_name", "sentinel-analytics").Warn("query_tasks failed", "traceID", toolTraceID(toolCtx), "error", err, "elapsed_ms", time.Since(start).Milliseconds())
		return tool.Error(fmt.Sprintf("Failed to query tasks: %s", err)), nil
	}
	var parsed map[string]any
	if err := json.Unmarshal(raw, &parsed); err != nil {
		slog.Default().With("logger_name", "sentinel-analytics").Warn("query_tasks response not JSON", "traceID", toolTraceID(toolCtx), "error", err)
		return tool.Error(fmt.Sprintf("Failed to parse query_tasks response: %s", err)), nil
	}
	return tool.OK("Retrieved query_tasks",
		tool.WithDataObjects(tool.DataObject{
			Content:     raw,
			MIMEType:    "application/json",
			Disposition: tool.DispositionAuto,
			Description: "query_tasks report",
		}),
	), nil
}

func (t *SentinelAnalyticsTool) Name() string { return t.name }

func (t *SentinelAnalyticsTool) Description() string { return t.description }

func (t *SentinelAnalyticsTool) Parameters() *tool.JSONSchema {
	props := map[string]any{
		"window": map[string]any{
			"type": "string",
			"description": "Relative lookback window ending now, e.g. '1h', '6h', '24h', '7d', '30d'. " +
				"Defaults to '24h'. Ignored when start_time is given.",
		},
		"start_time": map[string]any{
			"type":        "string",
			"description": "Optional ISO 8601 start of the window (UTC). Overrides 'window'.",
		},
		"end_time": map[string]any{
			"type":        "string",
			"description": "Optional ISO 8601 end of the window (UTC). Defaults to now.",
		},
		"execution_mode": map[string]any{
			"type":        "string",
			"description": "Optional filter: 'foreground' or 'background'.",
			"enum":        []string{"foreground", "background"},
		},
	}
	if t.paginated {
		props["page_number"] = map[string]any{"type": "integer", "description": "1-based page number. Default 1."}
		props["page_size"] = map[string]any{"type": "integer", "description": "Page size (1-100). Default 20."}
	}
	if t.threshold {
		props["threshold_ms"] = map[string]any{"type": "integer", "description": "Only include tasks at least this many milliseconds long."}
	}
	if t.groupBy {
		props["group_by"] = map[string]any{
			"type":        "string",
			"description": "Grouping dimension: 'day' (default) or 'user'.",
			"enum":        []string{"day", "user"},
		}
	}
	return &tool.JSONSchema{Type: "object", Properties: props}
}

func (t *SentinelAnalyticsTool) ResultSchema() *tool.JSONSchema {
	return emptyResultSchema()
}

func (t *SentinelAnalyticsTool) Execute(ctx context.Context, args map[string]any, toolCtx *tool.Context) (*tool.Result, error) {
	if toolCtx.ControlQuerier == nil {
		return tool.Error("Analytics service is not available. Ensure the gateway is running with the analytics service configured."), nil
	}

	body, err := t.buildBody(args)
	if err != nil {
		return tool.Error(err.Error()), nil
	}

	start := time.Now()
	raw, err := toolCtx.ControlQuerier.Custom(ctx, gatewayControlApp, "get", t.path, body)
	if err != nil {
		slog.Default().With("logger_name", "sentinel-analytics").Warn("sentinel analytics query failed", "tool", t.name, "path", t.path, "traceID", toolTraceID(toolCtx), "error", err, "elapsed_ms", time.Since(start).Milliseconds())
		return tool.Error(fmt.Sprintf("Failed to retrieve %s: %s", t.name, err)), nil
	}

	var parsed map[string]any
	if err := json.Unmarshal(raw, &parsed); err != nil {
		slog.Default().With("logger_name", "sentinel-analytics").Warn("sentinel analytics response not JSON", "tool", t.name, "path", t.path, "traceID", toolTraceID(toolCtx), "error", err)
		return tool.Error(fmt.Sprintf("Failed to parse %s response: %s", t.name, err)), nil
	}

	return tool.OK(fmt.Sprintf("Retrieved %s", t.name),
		tool.WithDataObjects(tool.DataObject{
			Content:     raw,
			MIMEType:    "application/json",
			Disposition: tool.DispositionAuto,
			Description: fmt.Sprintf("%s report", t.name),
		}),
	), nil
}

func (t *SentinelAnalyticsTool) buildBody(args map[string]any) (json.RawMessage, error) {
	body, err := buildAnalyticsWindowBody(args)
	if err != nil {
		return nil, err
	}
	if t.paginated {
		if n, ok := sentinelIntArg(args, "page_number"); ok {
			body["pageNumber"] = n
		}
		if n, ok := sentinelIntArg(args, "page_size"); ok {
			body["pageSize"] = n
		}
	}
	if t.threshold {
		if n, ok := sentinelIntArg(args, "threshold_ms"); ok {
			body["thresholdMs"] = n
		}
	}
	if t.groupBy {
		if g := sentinelStrArg(args, "group_by"); g != "" {
			if g != "day" && g != "user" {
				return nil, fmt.Errorf("group_by must be 'day' or 'user'")
			}
			body["groupBy"] = g
		}
	}

	return json.Marshal(body)
}

func parseWindow(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty window")
	}
	unit := s[len(s)-1]
	numStr := s[:len(s)-1]
	n, err := strconv.Atoi(numStr)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("invalid window %q (use forms like 24h, 7d)", s)
	}
	switch unit {
	case 'h', 'H':
		return time.Duration(n) * time.Hour, nil
	case 'd', 'D':
		return time.Duration(n) * 24 * time.Hour, nil
	default:
		return 0, fmt.Errorf("invalid window unit in %q (use h or d)", s)
	}
}

func buildAnalyticsWindowBody(args map[string]any) (map[string]any, error) {
	now := time.Now()
	body := map[string]any{}

	end := now.UnixMilli()
	if s := sentinelStrArg(args, "end_time"); s != "" {
		parsed, err := time.Parse(time.RFC3339, s)
		if err != nil {
			return nil, fmt.Errorf("invalid end_time (expected ISO 8601): %v", err)
		}
		end = parsed.UnixMilli()
	}
	var start int64
	if s := sentinelStrArg(args, "start_time"); s != "" {
		parsed, err := time.Parse(time.RFC3339, s)
		if err != nil {
			return nil, fmt.Errorf("invalid start_time (expected ISO 8601): %v", err)
		}
		start = parsed.UnixMilli()
	} else {
		win := sentinelStrArg(args, "window")
		if win == "" {
			win = defaultAnalyticsWindow
		}
		d, err := parseWindow(win)
		if err != nil {
			return nil, err
		}
		start = now.Add(-d).UnixMilli()
	}
	body["startDate"] = start
	body["endDate"] = end

	if em := sentinelStrArg(args, "execution_mode"); em != "" {
		if em != "foreground" && em != "background" {
			return nil, fmt.Errorf("execution_mode must be 'foreground' or 'background'")
		}
		body["executionMode"] = em
	}
	return body, nil
}

func sentinelStrSliceArg(args map[string]any, key string) []string {
	add := func(out []string, raw string) []string {
		if s := strings.TrimSpace(raw); s != "" {
			return append(out, s)
		}
		return out
	}
	switch v := args[key].(type) {
	case []any:
		out := make([]string, 0, len(v))
		for _, e := range v {
			if s, ok := e.(string); ok {
				out = add(out, s)
			}
		}
		return out
	case []string:
		out := make([]string, 0, len(v))
		for _, s := range v {
			out = add(out, s)
		}
		return out
	case string:
		var out []string
		for s := range strings.SplitSeq(v, ",") {
			out = add(out, s)
		}
		return out
	}
	return nil
}

func sentinelStrArg(args map[string]any, key string) string {
	if v, ok := args[key].(string); ok {
		return strings.TrimSpace(v)
	}
	return ""
}

func sentinelIntArg(args map[string]any, key string) (int, bool) {
	switch v := args[key].(type) {
	case float64:
		return int(v), true
	case int:
		return v, true
	case int64:
		return int(v), true
	case string:
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			return n, true
		}
	}
	return 0, false
}

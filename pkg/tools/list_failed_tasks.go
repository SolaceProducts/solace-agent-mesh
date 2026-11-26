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
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/analytics"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

type ListFailedTasksTool struct {
	tool.SideEffectingTool
	artifactToolBase
}

func NewListFailedTasksTool() *ListFailedTasksTool {
	return &ListFailedTasksTool{}
}

func (t *ListFailedTasksTool) Name() string { return "list_failed_tasks" }

func (t *ListFailedTasksTool) Description() string {
	return `Lists failed tasks (status failed, error or timeout) for a time window. The
tool message is an «artifact_content» embed that expands into the rendered
table. Your reply is that embed verbatim and nothing else: no table, summary or
«artifact_return».

For follow-ups the table doesn't answer, query the page's internal JSON
artifact 'failed_tasks_…_data.json' with transform_data_with_jmespath (rows
under data, paging under meta); never paste it or mention it unprompted.
Filter rows on status; they carry no error message. A timeout row's endDate is
when cleanup marked it, so never report its duration.

Use when asked about failures, errors or timeouts, or for more than
report_activity's 10 Recent Failures. The result carries {count (failures),
pageNumber, totalPages}: request one page per turn, and the next only when
pageNumber < totalPages.`
}

func (t *ListFailedTasksTool) RequiredScopes() []string { return nil }

func (t *ListFailedTasksTool) Parameters() *tool.JSONSchema {
	return &tool.JSONSchema{
		Type: "object",
		Properties: withActivityWindow(map[string]any{
			"pageNumber": map[string]any{
				"type":        "integer",
				"description": "1-based page number. Default 1.",
				"minimum":     1,
			},
			"pageSize": map[string]any{
				"type":        "integer",
				"description": "Rows per page. Default 20, capped at 100.",
				"minimum":     1,
				"maximum":     100,
			},
			"userId": map[string]any{
				"type":        "string",
				"description": "Optional drill-down: narrow the failed-task list to a single user. Requires activity:*:read unless the value matches the caller's own user ID. Omitted under admin means tenant-wide (all users).",
			},
		}),
	}
}

func (t *ListFailedTasksTool) ResultSchema() *tool.JSONSchema {
	return resultSchema(
		resultField{name: "count", typ: "integer",
			description: "Failed tasks in the window, across all pages."},
		resultField{name: "pageNumber", typ: "integer",
			description: "1-based page this result holds."},
		resultField{name: "totalPages", typ: "integer",
			description: "Pages the failures span at this page size."},
	)
}

func (t *ListFailedTasksTool) Execute(ctx context.Context, args map[string]any, toolCtx *tool.Context) (*tool.Result, error) {
	if toolCtx == nil || toolCtx.ControlQuerier == nil {
		return tool.Error("Activity service is not available. Ensure the gateway is running."), nil
	}

	body, window, err := listFailedTasksBody(args)
	if err != nil {
		return tool.Error(err.Error()), nil
	}

	logger := slog.Default().With("logger_name", "list-failed-tasks", "traceID", toolTraceID(toolCtx))
	start := time.Now()
	raw, err := toolCtx.ControlQuerier.Custom(ctx, gatewayControlApp, "get", "activity/tasks/failed", body)
	if err != nil {
		logger.Warn("list_failed_tasks control call failed",
			"error", err,
			"elapsed_ms", time.Since(start).Milliseconds())
		return tool.Error(fmt.Sprintf("Failed to list failed tasks: %s", err)), nil
	}

	var page analytics.Paginated[analytics.TaskHealthRow]
	if err = json.Unmarshal(raw, &page); err != nil {
		logger.Warn("list_failed_tasks response parse failed", "error", err, "responseBytes", len(raw))
		return tool.Error(fmt.Sprintf("Failed to parse failed tasks response: %s", err)), nil
	}

	rendered, err := renderActivityMarkdown(listFailedTasksTemplate, failedTasksData(&page, window))
	if err != nil {
		logger.Error("list_failed_tasks render failed", "error", err)
		return tool.Error(fmt.Sprintf("Failed to render failed tasks: %s", err)), nil
	}

	filters := activityFilters{TargetUserID: stringArg(args, "userId"), CallerUserID: callerUserID(toolCtx)}
	filename := reportFilenameStem("failed_tasks", window, resolveActivityScope(toolCtx), filters)
	if p := page.Meta.Pagination.PageNumber; p > 1 {
		filename += fmt.Sprintf("_p%d", p)
	}
	report, err := saveActivityMarkdown(ctx, toolCtx, filename+".md", rendered, "Failed tasks")
	if err != nil {
		logger.Warn("list_failed_tasks save failed", "error", err)
		return tool.Error(fmt.Sprintf("Failed to save failed tasks: %s", err)), nil
	}

	return tool.OK(report,
		tool.WithData(map[string]any{
			"count":      page.Meta.Pagination.Count,
			"pageNumber": page.Meta.Pagination.PageNumber,
			"totalPages": page.Meta.Pagination.TotalPages,
		}),
		tool.WithDataObjects(tool.DataObject{
			Name:        filename + "_data.json",
			Content:     stripRequestText(raw),
			MIMEType:    "application/json",
			Disposition: tool.DispositionArtifact,
			Description: failedTasksLLMDataDesc,
			Tags:        []string{a2a.ArtifactTagWorking},
		}),
	), nil
}

const failedTasksLLMDataDesc = "Failed tasks page (JSON for LLM querying via transform_data_with_jmespath; not for the end user)"

const failedTasksPageSizeCap = 100

func stripRequestText(raw json.RawMessage) json.RawMessage {
	var page map[string]any
	if err := json.Unmarshal(raw, &page); err != nil {
		return raw
	}
	rows, ok := page["data"].([]any)
	if !ok {
		return raw
	}
	for _, r := range rows {
		if row, ok := r.(map[string]any); ok {
			delete(row, "initialRequestText")
		}
	}
	slim, err := json.Marshal(page)
	if err != nil {
		return raw
	}
	return slim
}

func listFailedTasksBody(args map[string]any) (json.RawMessage, Window, error) {
	pageNumber := 1
	if v, ok := sentinelIntArg(args, "pageNumber"); ok && v > 0 {
		pageNumber = v
	}
	pageSize := 20
	if v, ok := sentinelIntArg(args, "pageSize"); ok && v > 0 {
		pageSize = min(v, failedTasksPageSizeCap)
	}

	body := map[string]any{
		"pageNumber": pageNumber,
		"pageSize":   pageSize,
	}
	if u, ok := args["userId"].(string); ok && u != "" {
		body["userId"] = u
	}
	window, err := applyWindowArgs(args, body)
	if err != nil {
		return nil, Window{}, err
	}
	raw, marshalErr := json.Marshal(body)
	return raw, window, marshalErr
}

func failedTasksData(page *analytics.Paginated[analytics.TaskHealthRow], window Window) map[string]any {
	p := page.Meta.Pagination
	rows := make([]map[string]any, 0, len(page.Data))
	for _, r := range page.Data {
		rows = append(rows, map[string]any{
			"when":        humanizeIsoTime(r.StartDate),
			"agent":       orDash(mdSafe(r.Name)),
			"statusEmoji": r.StatusEmoji,
			"status":      titleStatus(r.Status),
			"duration":    failedTaskDuration(r),
			"id":          r.ID,
		})
	}
	return map[string]any{
		"windowLabel":         window.Label,
		"windowActivityCount": windowActivityCount(page),
		"count":               p.Count,
		"pageNumber":          p.PageNumber,
		"totalPages":          p.TotalPages,
		"first":               (p.PageNumber-1)*p.PageSize + 1,
		"last":                min(p.PageNumber*p.PageSize, p.Count),
		"hasMore":             p.PageNumber < p.TotalPages,
		"rows":                rows,
	}
}

func failedTaskDuration(r analytics.TaskHealthRow) string {
	if r.DurationMs == nil || r.Status == store.TaskStatusJanitorTimeout {
		return "—"
	}
	return formatDurationShort(*r.DurationMs)
}

func windowActivityCount(page *analytics.Paginated[analytics.TaskHealthRow]) int {
	if page.Meta.WindowActivityCount == nil {
		return 0
	}
	return *page.Meta.WindowActivityCount
}

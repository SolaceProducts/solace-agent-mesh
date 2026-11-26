// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/analytics"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

type ExplainTaskTool struct {
	tool.SideEffectingTool
	artifactToolBase
}

func NewExplainTaskTool() *ExplainTaskTool { return &ExplainTaskTool{} }

func (t *ExplainTaskTool) Name() string { return "explain_task" }

func (t *ExplainTaskTool) Description() string {
	return `Explains one task. The tool message is an «artifact_content» embed that
expands into a rendered report (execution tree, timeline, root cause, related
failures). Start your reply with that embed verbatim; never re-render the tree
or timeline, paste the trace, or add an «artifact_return».

You also receive the task's trace as JSON. If the queried task ended without
completing, follow the embed with "### Recommendations": 2-3 concrete actions
grounded in the trace (its errorMessage, failed children, last events). A
failed child is a likely cause. Ancestors carry eventsOmitted: their events
were never fetched, so never say they did nothing. Ignore durationMs where
hideDuration is set.

Use when asked what happened, why a task failed, or how long it took.

If the id turns out to be a feedback id you can read, the result is
explain_feedback's instead: a feedback session report and its session JSON.
Handle it as explain_feedback's description says.`
}

func (t *ExplainTaskTool) RequiredScopes() []string { return nil }

func (t *ExplainTaskTool) Parameters() *tool.JSONSchema {
	return &tool.JSONSchema{
		Type: "object",
		Properties: map[string]any{
			"taskId": map[string]any{
				"type":        "string",
				"description": "The task's UUID. Format: 019f624e-9634-784d-9de0-901f494b4b2a.",
			},
		},
		Required: []string{"taskId"},
	}
}

func (t *ExplainTaskTool) ResultSchema() *tool.JSONSchema {
	return emptyResultSchema()
}

func (t *ExplainTaskTool) Execute(ctx context.Context, args map[string]any, toolCtx *tool.Context) (*tool.Result, error) {
	if toolCtx == nil || toolCtx.ControlQuerier == nil {
		return tool.Error("Activity service is not available. Ensure the gateway is running."), nil
	}

	taskID, ok := args["taskId"].(string)
	if !ok || taskID == "" {
		return tool.Error("Missing required parameter: taskId (UUID)."), nil
	}

	logger := slog.Default().With("logger_name", "explain-task", "traceID", toolTraceID(toolCtx), "taskID", taskID)
	start := time.Now()
	path := fmt.Sprintf("activity/tasks/%s/trace", taskID)
	raw, err := toolCtx.ControlQuerier.Custom(ctx, gatewayControlApp, "get", path, nil)
	if err != nil {
		logger.Warn("explain_task control call failed",
			"error", err,
			"elapsed_ms", time.Since(start).Milliseconds())
		return tool.Error(fmt.Sprintf("Failed to explain task: %s", err)), nil
	}

	var trace analytics.TaskTreeResponse
	if err = json.Unmarshal(raw, &trace); err != nil {
		logger.Warn("explain_task response parse failed", "error", err, "responseBytes", len(raw))
		return tool.Error(fmt.Sprintf("Failed to parse task trace response: %s", err)), nil
	}
	if trace.FeedbackID != "" {
		logger.Debug("explain_task id is a feedback id; handing off to explain_feedback")
		return NewExplainFeedbackTool().Execute(ctx, map[string]any{"feedbackId": trace.FeedbackID}, toolCtx)
	}
	nodes := traceNodes(&trace)
	queried := queriedNode(&trace, nodes)

	rendered, err := renderActivityMarkdown(explainTaskTemplate, explainTaskData(&trace, nodes, queried))
	if err != nil {
		logger.Error("explain_task render failed", "error", err)
		return tool.Error(fmt.Sprintf("Failed to render task explanation: %s", err)), nil
	}

	stem := "task_tree_" + taskID
	report, err := saveActivityMarkdown(ctx, toolCtx, stem+".md", rendered, "Task trace")
	if err != nil {
		logger.Warn("explain_task save failed", "error", err)
		return tool.Error(fmt.Sprintf("Failed to save task explanation: %s", err)), nil
	}

	return tool.OK(report,
		tool.WithDataObjects(traceJSON(stem+".json", "Task trace (LLM reasoning only)", raw)),
	), nil
}

func traceNodes(trace *analytics.TaskTreeResponse) []*analytics.TreeNodeDTO {
	nodes := make([]*analytics.TreeNodeDTO, 0, 1+len(trace.Tree))
	nodes = append(nodes, &trace.Root)
	for i := range trace.Tree {
		nodes = append(nodes, &trace.Tree[i])
	}
	return nodes
}

func queriedNode(trace *analytics.TaskTreeResponse, nodes []*analytics.TreeNodeDTO) *analytics.TreeNodeDTO {
	for _, n := range nodes {
		if n.IsQueried || n.ID == trace.QueriedTaskID {
			return n
		}
	}
	return &trace.Root
}

func explainTaskData(trace *analytics.TaskTreeResponse, nodes []*analytics.TreeNodeDTO, queried *analytics.TreeNodeDTO) map[string]any {
	eventsSampled := trace.TreeEventsCapped
	for _, n := range nodes {
		eventsSampled = eventsSampled || n.EventsCapped
	}
	return map[string]any{
		"queried": map[string]any{
			"statusEmoji": queried.StatusEmoji,
			"name":        orDash(codeSafe(queried.Name)),
			"status":      titleStatus(queried.Status),
			"at":          clockTime(cmp.Or(queried.EndDate, queried.StartDate)),
		},
		"treeLines":     treeLines(nodes, queried),
		"timeline":      timeline(queried.Events, humanizeIsoTime),
		"rootCause":     rootCause(queried),
		"related":       relatedTasks(nodes, queried),
		"treeCapped":    trace.TreeCapped,
		"eventsSampled": eventsSampled,
	}
}

func treeLines(nodes []*analytics.TreeNodeDTO, queried *analytics.TreeNodeDTO) []string {
	present := make(map[string]bool, len(nodes))
	for _, n := range nodes {
		present[n.ID] = true
	}
	children := make(map[string][]*analytics.TreeNodeDTO, len(nodes))
	for _, n := range nodes[1:] {
		parent := n.ParentTaskID
		if !present[parent] || parent == n.ID {
			parent = queried.ID
		}
		children[parent] = append(children[parent], n)
	}

	lines := make([]string, 0, len(nodes))
	visited := make(map[string]bool, len(nodes))
	var walk func(n *analytics.TreeNodeDTO, connector string)
	walk = func(n *analytics.TreeNodeDTO, connector string) {
		if visited[n.ID] {
			return
		}
		visited[n.ID] = true
		line := strings.Repeat(" ", 3*n.Depth) + connector + treeNodeLabel(n)
		if n == queried {
			line += "   ⬅ QUERIED"
		}
		lines = append(lines, line)
		kids := children[n.ID]
		for i, c := range kids {
			if i == len(kids)-1 {
				walk(c, "└─ ")
			} else {
				walk(c, "├─ ")
			}
		}
	}
	walk(nodes[0], "")
	return lines
}

func treeNodeLabel(n *analytics.TreeNodeDTO) string {
	status := strings.TrimSpace(n.StatusEmoji + " " + titleStatus(n.Status))
	label := fmt.Sprintf("[%s] %s [%s] — %s", n.ID, orDash(codeSafe(n.Name)), orDash(n.Type), status)
	if d := nodeDuration(n); d != "—" {
		label += " (" + d + ")"
	}
	return label
}

func nodeDuration(n *analytics.TreeNodeDTO) string {
	if n.DurationMs == nil || n.HideDuration {
		return "—"
	}
	return formatDurationShort(*n.DurationMs)
}

func timeline(events []analytics.TraceEventDTO, formatTime func(string) string) []map[string]any {
	out := make([]map[string]any, 0, len(events))
	for i := 0; i < len(events); {
		j := i + 1
		for j < len(events) && events[j].Kind == events[i].Kind {
			j++
		}
		kind, summary := events[i].Kind, codeSafe(events[i].Summary)
		if n := j - i; n > 1 {
			kind, summary = fmt.Sprintf("%d × %s", n, events[i].Kind), codeSafe(events[j-1].Summary)
		}
		out = append(out, map[string]any{
			"when":    formatTime(events[i].CreatedAt),
			"kind":    codeSafe(kind),
			"summary": summary,
		})
		i = j
	}
	return out
}

var unsuccessfulEndStatuses = map[string]bool{
	"failed": true, "error": true, "timeout": true, "stale": true,
	"canceled": true, "cancelled": true, "rejected": true,
}

func endedUnsuccessfully(status string) bool {
	return unsuccessfulEndStatuses[status]
}

func rootCause(queried *analytics.TreeNodeDTO) string {
	if !endedUnsuccessfully(queried.Status) {
		return ""
	}
	if queried.ErrorMessage != "" {
		return codeSafe(queried.ErrorMessage)
	}
	if n := len(queried.Events); n > 0 {
		return codeSafe(queried.Events[n-1].Summary)
	}
	return ""
}

func relatedTasks(nodes []*analytics.TreeNodeDTO, queried *analytics.TreeNodeDTO) []map[string]any {
	parentOf := make(map[string]string, len(nodes))
	for _, n := range nodes {
		parentOf[n.ID] = n.ParentTaskID
	}
	ancestors := map[string]bool{}
	for id := parentOf[queried.ID]; id != "" && !ancestors[id]; id = parentOf[id] {
		ancestors[id] = true
	}

	out := []map[string]any{}
	for _, n := range nodes {
		if n == queried || !endedUnsuccessfully(n.Status) {
			continue
		}
		out = append(out, map[string]any{
			"name":        orDash(mdSafe(n.Name)),
			"id":          n.ID,
			"type":        orDash(n.Type),
			"statusEmoji": n.StatusEmoji,
			"status":      titleStatus(n.Status),
			"duration":    nodeDuration(n),
			"relation":    relationTo(n, queried, ancestors),
		})
	}
	return out
}

func relationTo(n, queried *analytics.TreeNodeDTO, ancestors map[string]bool) string {
	switch {
	case n.ParentTaskID == queried.ID:
		return "child of the queried task"
	case ancestors[n.ID]:
		return "ancestor"
	default:
		return "descendant"
	}
}

func clockTime(iso string) string {
	t, err := time.Parse(time.RFC3339Nano, iso)
	if err != nil {
		return orDash(iso)
	}
	return t.UTC().Format("15:04 UTC")
}

// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package slack

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	goslack "github.com/slack-go/slack"

	"github.com/SolaceProducts/solace-agent-mesh/internal/stim"
)

const (
	maxTimelineCards = 50
	cardDetailLimit  = 600
	planTitleLimit   = 60
)

func planTitle(s string) string {
	return truncateRunes(strings.Join(strings.Fields(s), " "), planTitleLimit)
}

type activityTimeline struct {
	logger     *slog.Logger
	cardTitles map[string]string
	thinkingID string
	thinkingN  int
	thinking   strings.Builder
	cappedLog  bool
}

func newActivityTimeline(logger *slog.Logger) *activityTimeline {
	return &activityTimeline{logger: logger, cardTitles: map[string]string{}}
}

func (t *activityTimeline) applySignal(dataType string, data map[string]any) []goslack.StreamChunk {
	chunks := t.closeThinkingUnless(dataType)

	switch dataType {
	case "agent_progress_update":
		if title := planTitle(strField(data, "status_text")); title != "" {
			chunks = append(chunks, goslack.NewPlanUpdateChunk(title))
		}

	case "thinking_content":
		chunks = append(chunks, t.thinkingChunk(data)...)

	case "tool_invocation_start":
		chunks = append(chunks, t.toolStartChunk(data)...)

	case "tool_result":
		chunks = append(chunks, t.toolResultChunk(data)...)

	case "workflow_node_execution_start":
		if id := strField(data, "node_id"); id != "" {
			chunks = append(chunks, t.card(id, nodeTitle(data), goslack.TaskCardStatusInProgress, strField(data, "status_text"), "")...)
		}

	case "workflow_node_execution_result":
		if id := strField(data, "node_id"); id != "" {
			chunks = append(chunks, t.card(id, nodeTitle(data), goslack.TaskCardStatusComplete, "", "")...)
		}

	case "builder_component_progress":
		chunks = append(chunks, t.builderChunk(data)...)

	case "artifact_creation_progress":
		chunks = append(chunks, t.artifactChunk(data)...)

	case "artifact_saved":
		chunks = append(chunks, t.savedArtifactChunk(data)...)
	}
	return chunks
}

func (t *activityTimeline) finalize() []goslack.StreamChunk {
	return t.closeThinkingUnless("")
}

func (t *activityTimeline) closeThinkingUnless(dataType string) []goslack.StreamChunk {
	if t.thinkingID == "" || dataType == "thinking_content" {
		return nil
	}
	id := t.thinkingID
	t.thinkingID = ""
	return t.card(id, "Thinking", goslack.TaskCardStatusComplete, t.thinking.String(), "")
}

func (t *activityTimeline) thinkingChunk(data map[string]any) []goslack.StreamChunk {
	if t.thinkingID == "" {
		t.thinkingN++
		t.thinkingID = fmt.Sprintf("thinking:%d", t.thinkingN)
		t.thinking.Reset()
	}
	t.thinking.WriteString(strField(data, "content"))
	status := goslack.TaskCardStatusInProgress
	id := t.thinkingID
	if complete, _ := data["is_complete"].(bool); complete {
		status = goslack.TaskCardStatusComplete
		t.thinkingID = ""
	}
	return t.card(id, "Thinking", status, t.thinking.String(), "")
}

func (t *activityTimeline) toolStartChunk(data map[string]any) []goslack.StreamChunk {
	id := strField(data, "function_call_id")
	if id == "" {
		return nil
	}
	title := humanizeTool(strField(data, "tool_name"))
	if agent := strField(data, "delegating_agent_name"); agent != "" {
		title = "Delegating to " + agent
	}
	return t.card(id, title, goslack.TaskCardStatusInProgress, "", "")
}

func (t *activityTimeline) toolResultChunk(data map[string]any) []goslack.StreamChunk {
	id := strField(data, "function_call_id")
	if id == "" {
		return nil
	}
	title := t.cardTitles[id]
	if title == "" {
		title = humanizeTool(strField(data, "tool_name"))
	}
	outcome := stim.ToolResultPayload(data)
	status := goslack.TaskCardStatusComplete
	output := ""
	if isErrorResult(outcome) {
		status = goslack.TaskCardStatusError
		output = summarizeResult(outcome)
	}
	return t.card(id, title, status, "", output)
}

func (t *activityTimeline) builderChunk(data map[string]any) []goslack.StreamChunk {
	id := strField(data, "component_id")
	if id == "" {
		return nil
	}
	title := strField(data, "component_name")
	if title == "" {
		title = humanizeTool(id)
	}
	status := goslack.TaskCardStatusInProgress
	switch strField(data, "state") {
	case "completed", "skipped":
		status = goslack.TaskCardStatusComplete
	case "failed":
		status = goslack.TaskCardStatusError
	}
	return t.card(id, title, status, strField(data, "status_text"), "")
}

func (t *activityTimeline) artifactChunk(data map[string]any) []goslack.StreamChunk {
	filename := strField(data, "filename")
	if filename == "" {
		return nil
	}
	status := goslack.TaskCardStatusInProgress
	switch strField(data, "status") {
	case "completed":
		status = goslack.TaskCardStatusComplete
	case "failed", "cancelled":
		status = goslack.TaskCardStatusError
	}
	return t.card("artifact:"+filename, "Created "+filename, status, "", "")
}

func (t *activityTimeline) savedArtifactChunk(data map[string]any) []goslack.StreamChunk {
	filename := strField(data, "filename")
	if filename == "" {
		return nil
	}
	return t.card("artifact:"+filename, "Created "+filename, goslack.TaskCardStatusComplete, "", "")
}

func (t *activityTimeline) card(id, title string, status goslack.TaskCardStatus, details, output string) []goslack.StreamChunk {
	if _, known := t.cardTitles[id]; !known {
		if len(t.cardTitles) >= maxTimelineCards {
			if !t.cappedLog {
				t.cappedLog = true
				t.logger.Warn("activity timeline card cap reached; dropping further cards",
					"cap", maxTimelineCards)
			}
			return nil
		}
	}
	t.cardTitles[id] = title

	chunk := goslack.NewTaskUpdateChunk(id, title)
	chunk.Status = status
	if details != "" {
		chunk.Details = truncateRunes(details, cardDetailLimit)
	}
	if output != "" {
		chunk.Output = truncateRunes(output, cardDetailLimit)
	}
	return []goslack.StreamChunk{chunk}
}

func nodeTitle(data map[string]any) string {
	if name := strField(data, "node_name"); name != "" {
		return name
	}
	return strField(data, "node_id")
}

func humanizeTool(name string) string {
	if name == "" {
		return "Tool"
	}
	return strings.TrimSpace(strings.NewReplacer("_", " ", "-", " ").Replace(name))
}

var sensitiveArgSubstrings = []string{"token", "password", "secret", "key", "authorization", "credential", "passwd", "apikey"}

func isSensitiveArgKey(key string) bool {
	lower := strings.ToLower(key)
	for _, s := range sensitiveArgSubstrings {
		if strings.Contains(lower, s) {
			return true
		}
	}
	return false
}

const perValuePreviewLimit = 80

const maxPreviewFields = 5

func compactKV(m map[string]any) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		if len(parts) == maxPreviewFields {
			parts = append(parts, "…")
			break
		}
		if isSensitiveArgKey(k) {
			parts = append(parts, k+"=<redacted>")
			continue
		}
		parts = append(parts, k+"="+truncateRunes(scalarPreview(m[k]), perValuePreviewLimit))
	}
	return strings.Join(parts, ", ")
}

func summarizeResult(result any) string {
	if s, ok := result.(string); ok {
		if m := stim.AsMapOrParseJSON(s); m != nil {
			return summarizeResult(m)
		}
	}
	switch v := result.(type) {
	case nil:
		return ""
	case string:
		return v
	case map[string]any:
		if s := stim.TaskStatusMessage(v); s != "" {
			return s
		}
		for _, k := range []string{"message", "text", "summary", "result", "content"} {
			if s := strField(v, k); s != "" {
				return s
			}
		}
		return compactKV(v)
	default:
		return scalarPreview(v)
	}
}

func isErrorResult(result any) bool {
	m := stim.AsMapOrParseJSON(result)
	if m == nil {
		return false
	}
	if b, ok := m["isError"].(bool); ok && b {
		return true
	}
	return stim.ToolResultFailed(m)
}

func scalarPreview(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case nil:
		return ""
	default:
		b, err := json.Marshal(x)
		if err != nil {
			return fmt.Sprintf("%v", x)
		}
		return string(b)
	}
}

func strField(m map[string]any, key string) string {
	s, _ := m[key].(string)
	return s
}

func truncateRunes(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	return s[:runeSafeCut(s, limit-len("…"))] + "…"
}

// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func peerReply(state string, opts ...tool.ResultOption) *tool.Result {
	text := strings.Repeat("reply text ", 30_000)
	task := map[string]any{
		"id": "task-1", "kind": "task", "contextId": "ctx-1",
		"status": map[string]any{"state": state, "message": map[string]any{
			"role": "agent", "parts": []any{map[string]any{"kind": "text", "text": text}}}},
	}
	opts = append(opts, tool.WithDelegatedTask(task))
	if state == "failed" {
		return tool.Error("Peer task did not succeed: "+text,
			append(opts, tool.WithErrorCode(tool.ErrorCodeDelegatedTaskFailed))...)
	}
	return tool.OK(text, opts...)
}

func TestFormatToolResult_OversizedPeerReplyKeepsItsStatusAndTaskState(t *testing.T) {
	for _, tc := range []struct {
		name        string
		state       string
		withService bool
		wantStatus  string
		wantCode    any
	}{
		{"completed and saved", "completed", true, tool.StatusSuccess, nil},
		{"completed, nowhere to save", "completed", false, tool.StatusPartial, nil},
		{"failed and saved", "failed", true, tool.StatusError, tool.ErrorCodeDelegatedTaskFailed},
		{"failed, nowhere to save", "failed", false, tool.StatusError, tool.ErrorCodeDelegatedTaskFailed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			toolCtx, _ := ceilingTestContext()
			if !tc.withService {
				toolCtx = nil
			}

			_, parsed := formatWithCeiling(t, defaultToolOutputLLMReturnMaxBytes, peerReply(tc.state), toolCtx)

			if parsed["status"] != tc.wantStatus {
				t.Errorf("status = %v, want %v", parsed["status"], tc.wantStatus)
			}
			if parsed["task_state"] != tc.state {
				t.Errorf("task_state = %v, want %v", parsed["task_state"], tc.state)
			}
			if parsed["error_code"] != tc.wantCode {
				t.Errorf("error_code = %v, want %v", parsed["error_code"], tc.wantCode)
			}
		})
	}
}

func TestFormatToolResult_WindowEndsOnAWholeLine(t *testing.T) {
	toolCtx, _ := ceilingTestContext()
	var rows strings.Builder
	for i := range 200 {
		fmt.Fprintf(&rows, "row-%04d,alpha,%d\n", i, i*7)
	}

	_, parsed := formatWithCeiling(t, 2048, loadArtifactResult(rows.String(), 1, 1), toolCtx)

	kept, _ := parsed["content"].(string)
	if kept == "" || !strings.HasSuffix(kept, "\n") {
		t.Fatalf("content does not end on a whole line: %q", kept[max(0, len(kept)-30):])
	}
	if got, _ := parsed["content_returned_bytes"].(float64); int(got) != len(kept) {
		t.Errorf("content_returned_bytes = %v, want %d", parsed["content_returned_bytes"], len(kept))
	}
	if next, _ := parsed["next_offset"].(float64); int(next) != 1+strings.Count(kept, "\n") {
		t.Errorf("next_offset = %v after %d whole lines", parsed["next_offset"], strings.Count(kept, "\n"))
	}
}

func TestFormatToolResult_SmallCeilingCutsTheMessageInsteadOfDroppingIt(t *testing.T) {
	toolCtx, _ := ceilingTestContext()
	r := failedTool(map[string]any{"rows": strings.Repeat("r", 50_000)})
	r.Message = strings.Repeat("M", 10_000)

	_, parsed := formatWithCeiling(t, 1500, r, toolCtx)

	msg, _ := parsed["message"].(string)
	if !strings.HasPrefix(msg, "MMM") || !strings.HasSuffix(msg, "full text in the artifact]") {
		t.Errorf("message = %q, want its beginning with the truncation marker", msg)
	}
	if parsed["status"] != tool.StatusFailed || parsed["code"] != "TOOL_EXIT_NONZERO" || parsed["retryable"] != false {
		t.Errorf("status, code, retryable = %v, %v, %v", parsed["status"], parsed["code"], parsed["retryable"])
	}
}

func TestFormatToolResult_UnderBudgetDiagnosticsAndSmallerFieldsComeFirst(t *testing.T) {
	toolCtx, _ := ceilingTestContext()
	data := map[string]any{"rows": strings.Repeat("r", 50_000), "wide": strings.Repeat("w", 400)}
	for i := range 5_000 {
		data[fmt.Sprintf("f%04d", i)] = i
	}
	r := failedTool(data)
	r.FailedDetail.StackTrace = strings.Repeat("frame\n", 4_000)

	_, parsed := formatWithCeiling(t, 1500, r, toolCtx)

	if parsed["status"] != tool.StatusFailed || parsed["code"] != "TOOL_EXIT_NONZERO" ||
		parsed["guidance"] != "do not retry" {
		t.Errorf("status, code, guidance = %v, %v, %v; diagnostics go in before other fields",
			parsed["status"], parsed["code"], parsed["guidance"])
	}
	if _, ok := parsed["f0000"]; !ok {
		t.Error("f0000 missing; small fields go in smallest first")
	}
	if _, ok := parsed["wide"]; ok {
		t.Error("a 400-byte field went in while smaller ones were left out")
	}
	if _, ok := parsed["stack_trace"]; ok {
		t.Error("a stack trace larger than the room left went in")
	}
}

func TestFormatToolResult_WindowAtATinyCeilingStillReferencesTheSavedResult(t *testing.T) {
	toolCtx, svc := ceilingTestContext()

	_, parsed := formatWithCeiling(t, 300, loadArtifactResult(strings.Repeat("row\n", 5_000), 2, 1), toolCtx)

	name, _ := parsed["artifact"].(string)
	if !strings.HasPrefix(name, "tool_result_") {
		t.Fatalf("artifact = %v, want the saved result's name", parsed["artifact"])
	}
	loadSaved(t, svc, name)
	if parsed["status"] != tool.StatusPartial || parsed["view_truncated"] != true {
		t.Errorf("status = %v, view_truncated = %v; a cut success says so", parsed["status"], parsed["view_truncated"])
	}
}

func TestFormatToolResult_LastResortKeepsTheFailureDiagnostics(t *testing.T) {
	toolCtx, _ := ceilingTestContext()
	r := failedTool(map[string]any{"rows": strings.Repeat("r", 50_000)})

	_, parsed := formatWithCeiling(t, 300, r, toolCtx)

	if parsed["view_truncated"] != true || parsed["code"] != "TOOL_EXIT_NONZERO" || parsed["retryable"] != false {
		t.Errorf("view = %v; a cut failure still says what failed and whether to retry", parsed)
	}
	if name, _ := parsed["artifact"].(string); !strings.HasPrefix(name, "tool_result_") {
		t.Errorf("artifact = %v, want the saved result's name", parsed["artifact"])
	}
}

func TestFormatToolResult_InvalidWindowIsSpilledNotTruncated(t *testing.T) {
	for name, window := range map[string]tool.ResultOption{
		"no filename":      tool.WithContentWindow("", 0, 1),
		"line 0":           tool.WithContentWindow("big.csv", 0, 0),
		"negative version": tool.WithContentWindow("big.csv", -1, 1),
	} {
		t.Run(name, func(t *testing.T) {
			toolCtx, _ := ceilingTestContext()
			r := tool.OK("Loaded.", tool.WithData(map[string]any{"content": strings.Repeat("row\n", 60_000)}), window)

			_, parsed := formatWithCeiling(t, defaultToolOutputLLMReturnMaxBytes, r, toolCtx)

			if _, ok := parsed["content_truncated"]; ok {
				t.Error("an invalid window was truncated with a resume note it cannot honour")
			}
			savedArtifactName(t, parsed)
		})
	}
}

func TestFormatToolResult_PreviewIsKeptWhenTheViewFitsExactly(t *testing.T) {
	result := func() *tool.Result {
		return tool.OK("ok", tool.WithData(map[string]any{"rows": strings.Repeat("r", 200_000)}))
	}
	toolCtx, _ := ceilingTestContext()
	full, parsed := formatWithCeiling(t, defaultToolOutputLLMReturnMaxBytes, result(), toolCtx)
	if _, ok := parsed["artifacts_created"].([]any)[0].(map[string]any)["preview"]; !ok {
		t.Fatal("no preview at the default ceiling")
	}
	if _, ok := parsed["result_head"]; ok {
		t.Error("result_head shown although the result was saved")
	}

	toolCtx, _ = ceilingTestContext()
	_, tight := formatWithCeiling(t, len(full), result(), toolCtx)

	if _, ok := tight["artifacts_created"].([]any)[0].(map[string]any)["preview"]; !ok {
		t.Error("preview dropped although the view fits the ceiling exactly")
	}
}

func TestFormatToolResult_UnderBudgetTheMessageComesBeforeLargeDiagnostics(t *testing.T) {
	toolCtx, _ := ceilingTestContext()
	r := failedTool(map[string]any{"rows": strings.Repeat("r", 50_000)})
	r.FailedDetail.StackTrace = strings.Repeat("s", 3_000)
	r.Message = strings.Repeat("M", 10_000)

	_, parsed := formatWithCeiling(t, 4000, r, toolCtx)

	if msg, _ := parsed["message"].(string); len(msg) < 2000 {
		t.Errorf("message is %d bytes; it goes in before large diagnostics", len(msg))
	}
	if parsed["guidance"] != "do not retry" || parsed["stderr_tail"] != "boom" {
		t.Error("small diagnostics were dropped")
	}
	if _, ok := parsed["stack_trace"]; ok {
		t.Error("a 3 KB stack trace went in ahead of the message")
	}
}

func TestFormatToolResult_SpilledMessageIsCappedAt2048Bytes(t *testing.T) {
	const marker = " [... truncated; full text in the artifact]"
	for _, tc := range []struct {
		length int
		want   string
	}{
		{0, ""},
		{100, strings.Repeat("M", 100)},
		{2048, strings.Repeat("M", 2048)},
		{2049, strings.Repeat("M", 2048) + marker},
		{5000, strings.Repeat("M", 2048) + marker},
	} {
		t.Run(fmt.Sprint(tc.length), func(t *testing.T) {
			toolCtx, _ := ceilingTestContext()
			r := tool.OK(strings.Repeat("M", tc.length), tool.WithData(map[string]any{"rows": strings.Repeat("r", 200_000)}))

			_, parsed := formatWithCeiling(t, defaultToolOutputLLMReturnMaxBytes, r, toolCtx)

			if msg, _ := parsed["message"].(string); msg != tc.want {
				t.Errorf("message is %d bytes, want %d", len(msg), len(tc.want))
			}
		})
	}
}

func TestFormatToolResult_UnderBudgetTheMessageComesBeforeSmallFields(t *testing.T) {
	toolCtx, _ := ceilingTestContext()
	data := map[string]any{"rows": strings.Repeat("r", 50_000)}
	for i := range 300 {
		data[fmt.Sprintf("f%03d", i)] = i
	}

	_, parsed := formatWithCeiling(t, 1500, tool.OK(strings.Repeat("M", 10_000), tool.WithData(data)), toolCtx)

	if msg, _ := parsed["message"].(string); len(msg) < 300 {
		t.Errorf("message is %d bytes; small fields crowded it out", len(msg))
	}
}

func TestFormatToolResult_EqualSizeFieldsGoInByName(t *testing.T) {
	toolCtx, _ := ceilingTestContext()
	data := map[string]any{"a_wide": strings.Repeat("w", 400), "z": "q"}
	for i := range 3000 {
		data[fmt.Sprintf("k%04d", i)] = "vvvv"
	}

	_, parsed := formatWithCeiling(t, 1500, tool.OK("ok", tool.WithData(data)), toolCtx)

	if _, ok := parsed["a_wide"]; ok {
		t.Error("a 400-byte field went in ahead of smaller ones")
	}
	if _, ok := parsed["z"]; !ok {
		t.Error("the smallest field is missing")
	}
	kept := 0
	for k := range parsed {
		if strings.HasPrefix(k, "k") {
			kept++
		}
	}
	for i := range 3000 {
		if _, ok := parsed[fmt.Sprintf("k%04d", i)]; ok != (i < kept) {
			t.Fatalf("k%04d present = %v with %d kept; equal-size fields go in by name", i, ok, kept)
		}
	}
}

func TestFormatToolResult_InlineLimitsForSmallFieldsAndDiagnostics(t *testing.T) {
	for _, tc := range []struct {
		name    string
		data    map[string]any
		in, out string
	}{
		{"small field under 512 bytes", map[string]any{"in": strings.Repeat("a", 490), "out": strings.Repeat("b", 600)},
			"in", "out"},
		{"diagnostic under 8 KB", map[string]any{"artifact_schema": strings.Repeat("s", 7_000)}, "artifact_schema", ""},
		{"diagnostic over 8 KB", map[string]any{"artifact_schema": strings.Repeat("s", 9_000)}, "", "artifact_schema"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			toolCtx, _ := ceilingTestContext()
			tc.data["rows"] = strings.Repeat("r", 200_000)

			_, parsed := formatWithCeiling(t, defaultToolOutputLLMReturnMaxBytes, failedTool(tc.data), toolCtx)

			if _, ok := parsed[tc.in]; tc.in != "" && !ok {
				t.Errorf("%s missing", tc.in)
			}
			if _, ok := parsed[tc.out]; tc.out != "" && ok {
				t.Errorf("%s present", tc.out)
			}
		})
	}
}

func TestFormatToolResult_UnsavedResultLogsAWarning(t *testing.T) {
	agent, _, _ := newTestAgent()
	var logs bytes.Buffer
	agent.logger = slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	agent.cfg.ToolOutputLLMReturnMaxBytes = 2000
	result := func() *tool.Result {
		return tool.OK("ok", tool.WithData(map[string]any{"rows": strings.Repeat("r", 200_000)}))
	}

	agent.formatToolResult(context.Background(), result(), nil, nil)
	if !strings.Contains(logs.String(), "level=WARN") {
		t.Error("no warning when an over-ceiling result could not be saved")
	}

	logs.Reset()
	toolCtx, _ := ceilingTestContext()
	agent.formatToolResult(context.Background(), result(), toolCtx, nil)
	if strings.Contains(logs.String(), "level=WARN") {
		t.Error("warning logged although the result was saved")
	}
}

func TestFormatToolResult_UnsavedResultWithNoServiceHasNoFailedSave(t *testing.T) {
	_, parsed := formatWithCeiling(t, defaultToolOutputLLMReturnMaxBytes,
		tool.OK("ok", tool.WithData(map[string]any{"m": strings.Repeat("x", 200_000)})), nil)

	if _, ok := parsed["failed_artifacts"]; ok {
		t.Error("failed_artifacts reported when there is no artifact service to fail")
	}
}

func TestFormatToolResult_PayloadExactlyAtTheCeilingIsUnchanged(t *testing.T) {
	agent, _, _ := newTestAgent()
	r := tool.OK("ok", tool.WithData(map[string]any{"k": strings.Repeat("x", 100)}))
	full, _ := agent.formatToolResult(context.Background(), r, nil, nil)
	agent.cfg.ToolOutputLLMReturnMaxBytes = len(full)

	if out, _ := agent.formatToolResult(context.Background(), r, nil, nil); out != full {
		t.Errorf("payload exactly at the ceiling was altered: %s", out)
	}
}

func TestFormatToolResult_OverlongFirstLineNoteGivesTheResumePoint(t *testing.T) {
	toolCtx, _ := ceilingTestContext()
	content := strings.Repeat("x", 150_000) + "\nsecond line\n"

	_, parsed := formatWithCeiling(t, defaultToolOutputLLMReturnMaxBytes, loadArtifactResult(content, 4, 7), toolCtx)

	note, _ := parsed["truncation_note"].(string)
	if !strings.HasPrefix(note, "Line 7 ") || !strings.Contains(note, "version=4 offset=8") {
		t.Errorf("truncation_note = %q", note)
	}
}

func TestFormatToolResult_LastResortKeepsTheStatusBelowEverythingElse(t *testing.T) {
	toolCtx, _ := ceilingTestContext()

	_, parsed := formatWithCeiling(t, 30, peerReply("failed"), toolCtx)

	if parsed["status"] != tool.StatusError {
		t.Errorf("status = %v, want error", parsed["status"])
	}
}

// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	artmem "github.com/SolaceProducts/solace-agent-mesh/internal/artifact/memory"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func ceilingTestContext() (*tool.Context, *artifact.ScopedService) {
	scopedSvc := &artifact.ScopedService{Inner: artmem.New()}
	return &tool.Context{
		ArtifactService: scopedSvc,
		AgentName:       "test-agent",
		UserID:          "user-1",
		SessionID:       "sess-1",
	}, scopedSvc
}

func loadArtifactResult(content string, version, firstLine int) *tool.Result {
	return tool.OK("Loaded artifact 'big.csv'.", tool.WithData(map[string]any{
		"status":         "success",
		"filename":       "big.csv",
		"version":        version,
		"mime_type":      "text/csv",
		"content":        content,
		"size_bytes":     len(content),
		"message_to_llm": "",
	}), tool.WithContentWindow("big.csv", version, firstLine))
}

func decodeToolPayload(t *testing.T, out string) map[string]any {
	t.Helper()
	var parsed map[string]any
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("payload is not valid JSON: %v", err)
	}
	return parsed
}

func formatWithCeiling(t *testing.T, ceiling int, r *tool.Result, toolCtx *tool.Context) (string, map[string]any) {
	t.Helper()
	agent, _, _ := newTestAgent()
	agent.cfg.ToolOutputLLMReturnMaxBytes = ceiling
	out, _ := agent.formatToolResult(context.Background(), r, toolCtx, nil)
	if len(out) > ceiling {
		t.Fatalf("payload is %d bytes, over the %d-byte ceiling", len(out), ceiling)
	}
	return out, decodeToolPayload(t, out)
}

func savedArtifactName(t *testing.T, parsed map[string]any) string {
	t.Helper()
	arts, _ := parsed["artifacts_created"].([]any)
	if len(arts) != 1 {
		t.Fatalf("artifacts_created = %v, want exactly the spilled result", parsed["artifacts_created"])
	}
	return arts[0].(map[string]any)["filename"].(string)
}

func loadSaved(t *testing.T, svc *artifact.ScopedService, filename string) string {
	t.Helper()
	body, _, err := svc.Inner.Load(context.Background(), svc.Resolve("test-agent", "user-1", "sess-1", filename), nil)
	if err != nil {
		t.Fatalf("spilled artifact %q not saved: %v", filename, err)
	}
	return string(body)
}

func failedTool(data map[string]any) *tool.Result {
	r := tool.Failed("process crashed", &tool.ToolFailedDetail{
		Code: "TOOL_EXIT_NONZERO", StderrTail: "boom", StackTrace: strings.Repeat("frame\n", 500),
		Guidance: "do not retry",
	})
	r.Data = data
	return r
}

func TestFormatToolResult_WindowedContentIsTruncatedWithAResumePoint(t *testing.T) {
	toolCtx, _ := ceilingTestContext()
	content := strings.Repeat("order_id,customer,amount\n", 20_000)

	out, parsed := formatWithCeiling(t, defaultToolOutputLLMReturnMaxBytes, loadArtifactResult(content, 3, 1), toolCtx)

	if len(out) < defaultToolOutputLLMReturnMaxBytes-64 {
		t.Errorf("payload is %d bytes; ASCII content should fill the ceiling to within 64 bytes", len(out))
	}
	kept, _ := parsed["content"].(string)
	if kept == "" || !strings.HasPrefix(content, kept) {
		t.Fatalf("content is not a non-empty prefix of the artifact (len %d)", len(kept))
	}
	if parsed["content_truncated"] != true || parsed["status"] != "success" {
		t.Errorf("content_truncated = %v, status = %v", parsed["content_truncated"], parsed["status"])
	}
	lines := strings.Count(kept, "\n")
	if next, _ := parsed["next_offset"].(float64); int(next) != 1+lines {
		t.Errorf("next_offset = %v, want %d", parsed["next_offset"], 1+lines)
	}
	note, _ := parsed["truncation_note"].(string)
	if !strings.Contains(note, fmt.Sprintf("version=3 offset=%d", 1+lines)) {
		t.Errorf("truncation_note does not give the version and resume offset: %q", note)
	}
}

func TestFormatToolResult_ResumeOffsetCountsFromTheWindowStart(t *testing.T) {
	toolCtx, _ := ceilingTestContext()
	content := strings.Repeat("row\n", 2_000)

	_, parsed := formatWithCeiling(t, 2_000, loadArtifactResult(content, 0, 501), toolCtx)

	lines := strings.Count(parsed["content"].(string), "\n")
	if next, _ := parsed["next_offset"].(float64); int(next) != 501+lines {
		t.Errorf("next_offset = %v, want %d", parsed["next_offset"], 501+lines)
	}
}

func TestFormatToolResult_OverlongFirstLineResumesAtTheNextLine(t *testing.T) {
	toolCtx, _ := ceilingTestContext()
	content := strings.Repeat("x", 150_000) + "\nsecond line\n"

	_, parsed := formatWithCeiling(t, defaultToolOutputLLMReturnMaxBytes, loadArtifactResult(content, 0, 7), toolCtx)

	if next, _ := parsed["next_offset"].(float64); int(next) != 8 {
		t.Errorf("next_offset = %v, want 8, the line after the overlong one", parsed["next_offset"])
	}
}

func TestFormatToolResult_SingleLineWindowSaysPagingCannotReachTheRest(t *testing.T) {
	toolCtx, _ := ceilingTestContext()
	content := `{"rows":[` + strings.Repeat(`{"a":1},`, 30_000) + `{"a":1}]}`

	_, parsed := formatWithCeiling(t, defaultToolOutputLLMReturnMaxBytes, loadArtifactResult(content, 0, 1), toolCtx)

	if _, ok := parsed["next_offset"]; ok {
		t.Error("next_offset set for a single-line content that paging by line cannot advance")
	}
	if note, _ := parsed["truncation_note"].(string); !strings.Contains(note, "one line") {
		t.Errorf("truncation_note does not say the content is one line: %q", note)
	}
}

func TestFormatToolResult_EscapeHeavyWindowsStayWindowed(t *testing.T) {
	cases := map[string]string{
		"newlines":    strings.Repeat("\n", 60_000) + strings.Repeat("a", 60_000),
		"html-escape": strings.Repeat("<>&\n", 15_000) + strings.Repeat("a", 120_000),
		"control":     strings.Repeat("\x01\x02\n", 40_000),
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			toolCtx, _ := ceilingTestContext()
			out, parsed := formatWithCeiling(t, defaultToolOutputLLMReturnMaxBytes, loadArtifactResult(content, 0, 1), toolCtx)
			kept, _ := parsed["content"].(string)
			if parsed["content_truncated"] != true || kept == "" || !strings.HasPrefix(content, kept) {
				t.Fatalf("content_truncated = %v, kept %d bytes: want a truncated window", parsed["content_truncated"], len(kept))
			}
			if _, ok := parsed["next_offset"]; !ok {
				t.Error("next_offset missing")
			}
			if len(out) < defaultToolOutputLLMReturnMaxBytes-64 {
				t.Errorf("payload is %d bytes; the fit should be within 64 bytes of the ceiling", len(out))
			}
		})
	}
}

func TestFormatToolResult_TruncationNeverSplitsAMultiByteCharacter(t *testing.T) {
	content := strings.Repeat("é😀", 10_000)
	for ceiling := 3_000; ceiling < 3_012; ceiling++ {
		toolCtx, _ := ceilingTestContext()
		_, parsed := formatWithCeiling(t, ceiling, loadArtifactResult(content, 0, 1), toolCtx)
		kept, _ := parsed["content"].(string)
		if strings.ContainsRune(kept, '�') || !strings.HasPrefix(content, kept) {
			t.Fatalf("ceiling %d: kept content is not a whole-character prefix", ceiling)
		}
	}
}

func TestFormatToolResult_ContentWithoutAWindowIsSpilledNotTruncated(t *testing.T) {
	toolCtx, _ := ceilingTestContext()
	r := tool.OK("done", tool.WithData(map[string]any{"content": strings.Repeat("x", 200_000)}))

	_, parsed := formatWithCeiling(t, defaultToolOutputLLMReturnMaxBytes, r, toolCtx)

	if _, ok := parsed["content_truncated"]; ok {
		t.Error("content without a ContentWindow was truncated; it is not an artifact the model can page")
	}
	savedArtifactName(t, parsed)
}

func TestFormatToolResult_SpillKeepsStatusDiagnosticsAndSmallFieldsInline(t *testing.T) {
	toolCtx, svc := ceilingTestContext()
	r := failedTool(map[string]any{"output_filename": "out.csv", "rows": strings.Repeat("r", 200_000),
		"medium": strings.Repeat("m", 1_500)})
	r.LLMData = map[string]any{"hint": "model-only", "bulk_hint": strings.Repeat("h", 2_000)}

	_, parsed := formatWithCeiling(t, defaultToolOutputLLMReturnMaxBytes, r, toolCtx)

	for k, want := range map[string]any{"status": tool.StatusFailed, "code": "TOOL_EXIT_NONZERO", "stderr_tail": "boom",
		"guidance": "do not retry", "output_filename": "out.csv", "hint": "model-only", "retryable": false} {
		if parsed[k] != want {
			t.Errorf("%s = %v, want %v kept inline", k, parsed[k], want)
		}
	}
	if trace, _ := parsed["stack_trace"].(string); len(trace) < 2_000 {
		t.Errorf("stack_trace is %d bytes inline; a diagnostic up to 8 KB stays inline", len(trace))
	}
	for _, k := range []string{"rows", "medium", "bulk_hint"} {
		if _, ok := parsed[k]; ok {
			t.Errorf("%s stayed inline; only fields up to 512 bytes do", k)
		}
	}
	saved := loadSaved(t, svc, savedArtifactName(t, parsed))
	if strings.Contains(saved, "model-only") || strings.Contains(saved, "bulk_hint") {
		t.Error("model-only LLMData was written into the artifact, whose save is announced on the status stream")
	}
	if !strings.Contains(saved, "\n") {
		t.Error("the spilled artifact is one line, so load_artifact offset and limit cannot page it")
	}
}

func TestFormatToolResult_SpillKeepsTheToolsOwnVersion(t *testing.T) {
	toolCtx, _ := ceilingTestContext()
	r := tool.OK("ok", tool.WithData(map[string]any{"filename": "mine.txt", "version": 7, "rows": strings.Repeat("r", 200_000)}))

	_, parsed := formatWithCeiling(t, defaultToolOutputLLMReturnMaxBytes, r, toolCtx)

	if v, _ := parsed["version"].(float64); v != 7 || parsed["filename"] != "mine.txt" {
		t.Errorf("filename/version = %v/%v, want the tool's own mine.txt/7", parsed["filename"], parsed["version"])
	}
}

func TestFormatToolResult_SpilledArtifactKeepsDataOverLLMDataForASharedKey(t *testing.T) {
	toolCtx, svc := ceilingTestContext()
	r := tool.OK("ok", tool.WithData(map[string]any{"shared": "from-data", "rows": strings.Repeat("r", 200_000)}))
	r.LLMData = map[string]any{"shared": "from-llm-data"}

	_, parsed := formatWithCeiling(t, defaultToolOutputLLMReturnMaxBytes, r, toolCtx)

	saved := loadSaved(t, svc, savedArtifactName(t, parsed))
	if !strings.Contains(saved, "from-data") || strings.Contains(saved, "from-llm-data") {
		t.Error("the artifact must hold Data's value for a key LLMData also sets, never LLMData's")
	}
}

func TestFormatToolResult_ManySmallFieldsStillReferenceTheSavedArtifact(t *testing.T) {
	toolCtx, svc := ceilingTestContext()
	data := map[string]any{}
	for i := range 5_000 {
		data[fmt.Sprintf("id%05d", i)] = strings.Repeat("v", 40)
	}
	r := tool.OK("ok", tool.WithData(data))

	out, parsed := formatWithCeiling(t, defaultToolOutputLLMReturnMaxBytes, r, toolCtx)

	if strings.Contains(out, "could not be saved") {
		t.Error("a saved result is reported as unsaved")
	}
	name := savedArtifactName(t, parsed)
	loadSaved(t, svc, name)
	if note, _ := parsed["result_saved_note"].(string); !strings.Contains(note, name) || parsed["status"] != "success" {
		t.Errorf("result_saved_note = %q, status = %v", note, parsed["status"])
	}
}

func TestFormatToolResult_OversizedMessageIsCappedAndPointsAtTheArtifact(t *testing.T) {
	toolCtx, _ := ceilingTestContext()
	r := tool.OK(strings.Repeat("m", 300_000), tool.WithData(map[string]any{"a": 1}))

	_, parsed := formatWithCeiling(t, defaultToolOutputLLMReturnMaxBytes, r, toolCtx)

	savedArtifactName(t, parsed)
	msg, _ := parsed["message"].(string)
	if len(msg) > oversizedMessageMaxBytes+64 || !strings.HasSuffix(msg, "full text in the artifact]") {
		t.Errorf("message is %d bytes ending %q; want it capped and pointing at the artifact", len(msg), msg[max(0, len(msg)-40):])
	}
}

func TestFormatToolResult_FailedSaveIsPartialAndKeepsDiagnostics(t *testing.T) {
	toolCtx := &tool.Context{ArtifactService: newFailingArtifactSvc(errors.New("store down: " + strings.Repeat("x", 5_000))),
		AgentName: "test-agent", UserID: "user-1", SessionID: "sess-1"}
	r := failedTool(map[string]any{"matches": strings.Repeat("x", 200_000)})
	r.Message = strings.Repeat("m", 10_000)

	out, parsed := formatWithCeiling(t, defaultToolOutputLLMReturnMaxBytes, r, toolCtx)

	if strings.Contains(out, "saved as artifact") {
		t.Error("the response claims a save that failed")
	}
	if parsed["status"] != tool.StatusFailed || parsed["retryable"] != false || parsed["code"] != "TOOL_EXIT_NONZERO" {
		t.Errorf("status/retryable/code = %v/%v/%v; a failed tool must keep them", parsed["status"], parsed["retryable"], parsed["code"])
	}
	failed, _ := parsed["failed_artifacts"].([]any)
	if len(failed) != 1 {
		t.Fatalf("failed_artifacts = %v, want the failed save", parsed["failed_artifacts"])
	}
	if reason := failed[0].(map[string]any)["error"].(string); len(reason) > 200+len("...") {
		t.Errorf("save failure reason is %d bytes, want it bounded", len(reason))
	}
	if head, _ := parsed["result_head"].(string); len(head) < defaultToolOutputLLMReturnMaxBytes/2 {
		t.Errorf("result_head is %d bytes; the beginning should fill most of the ceiling", len(head))
	}
}

func TestFormatToolResult_UnsavedSuccessIsDowngradedToPartial(t *testing.T) {
	for name, toolCtx := range map[string]*tool.Context{"nil service": {}, "nil context": nil} {
		t.Run(name, func(t *testing.T) {
			r := tool.OK("ok", tool.WithData(map[string]any{"matches": strings.Repeat("x", 200_000)}))
			_, parsed := formatWithCeiling(t, defaultToolOutputLLMReturnMaxBytes, r, toolCtx)
			if parsed["status"] != tool.StatusPartial {
				t.Errorf("status = %v, want partial: the model sees only part of the result", parsed["status"])
			}
			if note, _ := parsed["note"].(string); !strings.Contains(note, noArtifactService) {
				t.Errorf("note = %q, want it to say no artifact service is configured", note)
			}
		})
	}
}

func TestFormatToolResult_ResultHeadNeverSplitsAMultiByteCharacter(t *testing.T) {
	r := tool.OK("ok", tool.WithData(map[string]any{"text": strings.Repeat("é😀", 40_000)}))
	for ceiling := 4_000; ceiling < 4_012; ceiling++ {
		_, parsed := formatWithCeiling(t, ceiling, r, nil)
		if head, _ := parsed["result_head"].(string); head == "" || strings.ContainsRune(head, '�') {
			t.Fatalf("ceiling %d: result_head %q is empty or splits a character", ceiling, head)
		}
	}
}

func TestFormatToolResult_TinyCeilingIsStillHonoured(t *testing.T) {
	toolCtx, _ := ceilingTestContext()
	r := tool.OK(strings.Repeat("m", 5_000), tool.WithData(map[string]any{"matches": strings.Repeat("x", 50_000)}))

	_, parsed := formatWithCeiling(t, 300, r, toolCtx)

	if parsed["status"] == nil {
		t.Error("status dropped from the last-resort view")
	}
}

func TestFormatToolResult_WindowSurvivesACheckpointRoundTrip(t *testing.T) {
	original := loadArtifactResult(strings.Repeat("row\n", 60_000), 2, 1)
	blob, err := json.Marshal(newPersistedToolResult(original))
	if err != nil {
		t.Fatal(err)
	}
	restored := resultFromPayload(blob)
	if restored == nil || restored.ContentWindow == nil || *restored.ContentWindow != *original.ContentWindow {
		t.Fatalf("ContentWindow after round trip = %+v, want %+v", restored.ContentWindow, original.ContentWindow)
	}

	toolCtx, _ := ceilingTestContext()
	_, parsed := formatWithCeiling(t, defaultToolOutputLLMReturnMaxBytes, restored, toolCtx)

	if parsed["content_truncated"] != true {
		t.Error("the restored page was not truncated as a window")
	}
}

func TestFormatToolResult_ResultUnderTheCeilingIsUnchanged(t *testing.T) {
	toolCtx, _ := ceilingTestContext()
	content := strings.Repeat("a,b\n", 1_000)

	_, parsed := formatWithCeiling(t, defaultToolOutputLLMReturnMaxBytes, loadArtifactResult(content, 0, 1), toolCtx)

	if parsed["content"] != content {
		t.Error("content under the ceiling was altered")
	}
	if _, ok := parsed["content_truncated"]; ok {
		t.Error("content_truncated set on a result under the ceiling")
	}
}

func TestFormatToolResult_CeilingIsNotAppliedWhenProcessingIsDisabled(t *testing.T) {
	agent, _, _ := newTestAgent()
	disabled := false
	agent.cfg.ToolResultProcessingEnabled = &disabled
	toolCtx, _ := ceilingTestContext()
	content := strings.Repeat("z", 2*defaultToolOutputLLMReturnMaxBytes)

	out, _ := agent.formatToolResult(context.Background(), loadArtifactResult(content, 0, 1), toolCtx, nil)

	if parsed := decodeToolPayload(t, out); parsed["content"] != content {
		t.Error("content was truncated although tool_result_processing_enabled is false")
	}
}

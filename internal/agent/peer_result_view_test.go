// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

const peerAnswer = "SKU-0947 sold the most units: 85."

func peerTaskReply(t *testing.T, state a2a.TaskState, leadingParts ...any) *broker.Message {
	t.Helper()
	parts := slices.Concat(leadingParts, []any{
		map[string]any{"kind": "data", "data": map[string]any{"filename": "top.csv", "version": 0, "status": "success",
			"message": "Artifact has been created and provided to the requester"},
			"metadata": map[string]any{"tool_name": "_notify_artifact_save"}},
		map[string]any{"kind": "text", "text": peerAnswer}})
	resp := map[string]any{
		"jsonrpc": "2.0",
		"id":      "req-1",
		"result": map[string]any{
			"kind": "task", "id": "task-1", "contextId": "ctx-1",
			"status": map[string]any{
				"state": string(state), "timestamp": "2026-10-31T03:10:21.612076+00:00",
				"message": map[string]any{"kind": "message", "messageId": "msg-1", "role": "agent", "parts": parts},
			},
			"metadata": map[string]any{
				"agent_name": "SalesLead", "peer_delegated": true,
				"produced_artifacts": []any{map[string]any{"filename": "top.csv", "version": 0}},
				"token_usage":        map[string]any{"total_input_tokens": 39911, "total_output_tokens": 1139},
			},
		},
	}
	payload, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}
	return &broker.Message{Payload: payload}
}

func renderForCaller(t *testing.T, r *tool.Result) (string, map[string]any) {
	t.Helper()
	agent, _, _ := newTestAgent()
	out, _ := agent.formatToolResult(context.Background(), r, nil, nil)
	return out, decodeToolPayload(t, out)
}

func replyPartsMention(parsed map[string]any, want string) bool {
	encoded, _ := json.Marshal(parsed["reply_parts"])
	return strings.Contains(string(encoded), want)
}

func TestFormatToolResult_PeerAnswerReachesTheCallerOnceWithoutTaskBookkeeping(t *testing.T) {
	res, err := parsePeerResponse("SalesLead", peerTaskReply(t, a2a.TaskStateCompleted))
	if err != nil {
		t.Fatalf("parsePeerResponse: %v", err)
	}

	out, parsed := renderForCaller(t, res)

	if n := strings.Count(out, peerAnswer); n != 1 {
		t.Errorf("the answer appears %d times in the caller's tool result, want once: %s", n, out)
	}
	for _, bookkeeping := range []string{"token_usage", "contextId", "timestamp", "messageId", "peer_delegated"} {
		if strings.Contains(out, bookkeeping) {
			t.Errorf("caller's tool result carries %q: %s", bookkeeping, out)
		}
	}
	if parsed["status"] != tool.StatusSuccess || parsed["task_state"] != "completed" {
		t.Errorf("status = %v, task_state = %v, want %q and completed", parsed["status"], parsed["task_state"], tool.StatusSuccess)
	}
	if !replyPartsMention(parsed, "top.csv") {
		t.Errorf("the peer's saved artifact is missing from the caller's tool result: %s", out)
	}
}

func TestFormatToolResult_FailedPeerKeepsItsErrorPartAndState(t *testing.T) {
	errorPart := map[string]any{"kind": "data", "data": map[string]any{
		"a2a_signal_type": "tool_execution_error", "code": "TOOL_CRASHED", "message": "the query tool crashed"}}
	res, err := parsePeerResponse("SalesLead", peerTaskReply(t, a2a.TaskStateFailed, errorPart))
	if err != nil {
		t.Fatalf("parsePeerResponse: %v", err)
	}

	out, parsed := renderForCaller(t, res)

	if parsed["status"] != tool.StatusError || parsed["task_state"] != "failed" {
		t.Errorf("status = %v, task_state = %v, want %q and failed", parsed["status"], parsed["task_state"], tool.StatusError)
	}
	if !replyPartsMention(parsed, "TOOL_CRASHED") {
		t.Errorf("the peer's tool failure is missing from the caller's tool result: %s", out)
	}
	if msg, _ := parsed["message"].(string); !strings.Contains(msg, "did not succeed") || !strings.Contains(msg, peerAnswer) {
		t.Errorf("message = %q, want the failure and the peer's text", msg)
	}
}

func TestFormatToolResult_PeerStructuredResultReachesTheCaller(t *testing.T) {
	res, err := parsePeerResponse("invoice-flow",
		delegatedTaskResponseWithStructuredResult(t, a2a.TaskStateCompleted, map[string]any{"output": map[string]any{"total": 42}}))
	if err != nil {
		t.Fatalf("parseResponse: %v", err)
	}

	out, parsed := renderForCaller(t, res)

	if !replyPartsMention(parsed, "structured_invocation_result") || !replyPartsMention(parsed, `"total":42`) {
		t.Errorf("the workflow's structured result is missing from the caller's tool result: %s", out)
	}
}

func TestPeerResult_PublishedDataKeepsTheWholeTask(t *testing.T) {
	res, err := parsePeerResponse("SalesLead", peerTaskReply(t, a2a.TaskStateCompleted))
	if err != nil {
		t.Fatalf("parsePeerResponse: %v", err)
	}
	agent, _, _ := newTestAgent()
	agent.formatToolResult(context.Background(), res, nil, nil)

	published, _ := tool.PublishedResultData(res).(map[string]any)
	metadata, _ := published["metadata"].(map[string]any)
	if published["kind"] != "task" || metadata["token_usage"] == nil {
		t.Errorf("published result_data lost the task or its token usage, which STIM and evidence read: %+v", published)
	}
}

func replyFor(t *testing.T, task map[string]any) *broker.Message {
	t.Helper()
	payload, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": "req-1", "result": task})
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}
	return &broker.Message{Payload: payload}
}

func textTask(state a2a.TaskState, text string, metadata map[string]any, extraParts ...any) map[string]any {
	parts := slices.Clone(extraParts)
	if text != "" {
		parts = append(parts, map[string]any{"kind": "text", "text": text})
	}
	task := map[string]any{"kind": "task", "id": "task-1", "contextId": "ctx-1",
		"status": map[string]any{"state": string(state), "message": map[string]any{"role": "agent", "parts": parts}}}
	if metadata != nil {
		task["metadata"] = metadata
	}
	return task
}

const inlineFileBytes = "QUJD"

func filePart(file map[string]any) map[string]any {
	return map[string]any{"kind": "file", "file": file}
}

func proxiedTask(saved bool) map[string]any {
	file := map[string]any{"name": "report.pdf", "mimeType": "application/pdf"}
	metadata := map[string]any{"agent_name": "ExternalAgent", "token_usage": map[string]any{"total_input_tokens": 100}}
	if saved {
		file["uri"] = "artifact://BenchAgent/user-1/sess-1/report.pdf?version=0"
		metadata["produced_artifacts"] = []any{map[string]any{"filename": "report.pdf", "version": 0}}
	} else {
		file["bytes"] = strings.Repeat(inlineFileBytes, 5_000)
	}
	task := textTask(a2a.TaskStateCompleted, "Done", metadata)
	task["artifacts"] = []any{map[string]any{"artifactId": "a-1", "name": "report", "parts": []any{filePart(file)}}}
	return task
}

func parsedPeerResult(t *testing.T, task map[string]any) *tool.Result {
	t.Helper()
	res, err := parsePeerResponse("ExternalAgent", replyFor(t, task))
	if err != nil {
		t.Fatalf("parsePeerResponse: %v", err)
	}
	return res
}

func TestFormatToolResult_SavedExternalArtifactReachesTheCallerByItsURI(t *testing.T) {
	out, parsed := renderForCaller(t, parsedPeerResult(t, proxiedTask(true)))

	if !strings.Contains(out, "artifact://BenchAgent/user-1/sess-1/report.pdf?version=0") {
		t.Errorf("the external agent's saved file is missing from the caller's tool result: %s", out)
	}
	if _, ok := parsed["produced_artifacts"]; !ok {
		t.Errorf("the saved-artifact manifest is missing from the caller's tool result: %s", out)
	}
	if _, ok := parsed["metadata"]; ok || strings.Contains(out, "token_usage") {
		t.Errorf("a completed external task's metadata reached the caller: %s", out)
	}
}

func TestFormatToolResult_UnsavedExternalFileSaysItsContentWasLeftOut(t *testing.T) {
	out, _ := renderForCaller(t, parsedPeerResult(t, proxiedTask(false)))

	if strings.Contains(out, inlineFileBytes) {
		t.Errorf("the file's inline bytes reached the caller's tool result")
	}
	if !strings.Contains(out, "report.pdf") || !strings.Contains(out, "bytes_omitted") {
		t.Errorf("the unsaved file must still be named, with a note that its content is not shown: %s", out)
	}
}

func TestFormatToolResult_FileInTheReplyMessageLosesOnlyItsInlineContent(t *testing.T) {
	inline := filePart(map[string]any{"name": "rows.csv", "bytes": strings.Repeat(inlineFileBytes, 2_000)})
	dataURI := filePart(map[string]any{"name": "chart.png", "uri": "data:image/png;base64," + strings.Repeat(inlineFileBytes, 2_000)})
	res := parsedPeerResult(t, textTask(a2a.TaskStateCompleted, peerAnswer, nil, inline, dataURI))

	for name, out := range map[string]string{
		"model view":   func() string { out, _ := renderForCaller(t, res); return out }(),
		"cancel flush": formatSyncResultForCancelFlush(res),
	} {
		if strings.Contains(out, inlineFileBytes) {
			t.Errorf("%s: inline file content reached the caller: %s", name, out)
		}
		if !strings.Contains(out, "rows.csv") || !strings.Contains(out, "chart.png") || strings.Count(out, "bytes_omitted") != 2 {
			t.Errorf("%s: both files must stay named with a note that their content is left out: %s", name, out)
		}
	}
}

func TestFormatToolResult_MetadataReachesTheCallerOnlyAsTheReasonForASilentFailure(t *testing.T) {
	reason := map[string]any{"error": "quota exceeded", "token_usage": map[string]any{"total_input_tokens": 5}}
	errorPart := map[string]any{"kind": "data", "data": map[string]any{"a2a_signal_type": "tool_execution_error", "code": "X"}}
	for name, tc := range map[string]struct {
		task       map[string]any
		wantReason bool
	}{
		"failed, nothing else":      {textTask(a2a.TaskStateFailed, "", reason), true},
		"cancelled, nothing else":   {textTask(a2a.TaskStateCancelled, "", reason), true},
		"rejected, nothing else":    {textTask(a2a.TaskStateRejected, "", reason), true},
		"failed with text":          {textTask(a2a.TaskStateFailed, "it broke", reason), false},
		"failed with an error part": {textTask(a2a.TaskStateFailed, "", reason, errorPart), false},
		"completed with no text":    {textTask(a2a.TaskStateCompleted, "", reason), false},
	} {
		t.Run(name, func(t *testing.T) {
			out, parsed := renderForCaller(t, parsedPeerResult(t, tc.task))

			if _, got := parsed["metadata"]; got != tc.wantReason {
				t.Errorf("metadata shown = %v, want %v: %s", got, tc.wantReason, out)
			}
			if strings.Contains(out, "token_usage") {
				t.Errorf("caller's tool result carries the peer's token usage: %s", out)
			}
		})
	}
}

func TestFormatToolResult_ModelViewLeavesThePublishedTaskUntouched(t *testing.T) {
	reason := map[string]any{"error": "quota exceeded", "token_usage": map[string]any{"total_input_tokens": 5}}
	inline := filePart(map[string]any{"name": "rows.csv", "bytes": inlineFileBytes})
	for name, task := range map[string]map[string]any{
		"unsaved external file": proxiedTask(false),
		"silent failure":        textTask(a2a.TaskStateFailed, "", reason),
		"file in the reply":     textTask(a2a.TaskStateCompleted, peerAnswer, nil, inline),
	} {
		t.Run(name, func(t *testing.T) {
			res := parsedPeerResult(t, task)
			before, err := json.Marshal(res.Data)
			if err != nil {
				t.Fatal(err)
			}

			renderForCaller(t, res)
			formatSyncResultForCancelFlush(res)

			after, err := json.Marshal(res.Data)
			if err != nil {
				t.Fatal(err)
			}
			if string(before) != string(after) {
				t.Errorf("rendering for the model changed the published task:\nbefore %s\nafter  %s", before, after)
			}
		})
	}
}

func TestSubTaskResult_ReachesTheCallerAsADelegatedTask(t *testing.T) {
	for _, state := range []a2a.TaskState{a2a.TaskStateCompleted, a2a.TaskStateFailed} {
		t.Run(string(state), func(t *testing.T) {
			res, err := (&subTaskTool{}).parseResponse(replyFor(t, textTask(state, peerAnswer,
				map[string]any{"token_usage": map[string]any{"total_input_tokens": 5}})))
			if err != nil {
				t.Fatalf("parseResponse: %v", err)
			}

			out, parsed := renderForCaller(t, res)

			if parsed["task_state"] != string(state) || strings.Contains(out, "token_usage") || strings.Contains(out, "ctx-1") {
				t.Errorf("sub-task result is not the caller's view of a delegated task: %s", out)
			}
		})
	}
}

func TestFormatSyncResultForCancelFlush_ShowsAPeerAnswerOnce(t *testing.T) {
	if got := formatSyncResultForCancelFlush(nil); got != cancelledToolResultContent {
		t.Errorf("nil result = %q, want %q", got, cancelledToolResultContent)
	}
	res, err := parsePeerResponse("SalesLead", peerTaskReply(t, a2a.TaskStateCompleted))
	if err != nil {
		t.Fatalf("parsePeerResponse: %v", err)
	}

	out := formatSyncResultForCancelFlush(res)

	if n := strings.Count(out, peerAnswer); n != 1 {
		t.Errorf("the answer appears %d times in the flushed tool result, want once: %s", n, out)
	}
	parsed := decodeToolPayload(t, out)
	if parsed["task_state"] != "completed" || !replyPartsMention(parsed, "top.csv") || strings.Contains(out, "token_usage") {
		t.Errorf("flushed tool result is not the caller's view of the peer task: %s", out)
	}
}

func TestFormatToolResult_DataThatOnlyLooksLikeATaskReachesTheModelWhole(t *testing.T) {
	for name, data := range map[string]map[string]any{
		"tasks API response": {"kind": "task", "id": "42", "title": "Fix login",
			"status": map[string]any{"state": "open", "assignee": "bob"}},
		"workflow structured result": {"status": "success", "output": map[string]any{"total": 42},
			"output_artifact": "out.json", "partial_failure_message": "branch b failed"},
		"other kind with a status object": {"kind": "ticket", "status": map[string]any{"state": "open"}},
	} {
		t.Run(name, func(t *testing.T) {
			_, parsed := renderForCaller(t, tool.OK("GET returned HTTP 200", tool.WithData(data)))

			for k, want := range data {
				got, _ := json.Marshal(parsed[k])
				wantJSON, _ := json.Marshal(want)
				if string(got) != string(wantJSON) {
					t.Errorf("%s = %s, want %s", k, got, wantJSON)
				}
			}
		})
	}
}

func TestFormatToolResult_OversizedExternalReplyKeepsItsFileReferences(t *testing.T) {
	var artifacts, manifest []any
	for i := range 4 {
		name := fmt.Sprintf("report-%d.pdf", i)
		file := map[string]any{"name": name, "mimeType": "application/pdf"}
		if i == 0 {
			file["bytes"] = strings.Repeat(inlineFileBytes, 5_000)
		} else {
			file["uri"] = "artifact://BenchAgent/user-1/sess-1/" + name + "?version=0"
			manifest = append(manifest, map[string]any{"filename": name, "version": 0})
		}
		artifacts = append(artifacts, map[string]any{"artifactId": name, "name": name, "parts": []any{filePart(file)}})
	}
	task := textTask(a2a.TaskStateCompleted, strings.Repeat("reply text ", 30_000),
		map[string]any{"produced_artifacts": manifest, "token_usage": map[string]any{"total_input_tokens": 5}})
	task["artifacts"] = artifacts
	toolCtx, svc := ceilingTestContext()

	out, parsed := formatWithCeiling(t, defaultToolOutputLLMReturnMaxBytes, parsedPeerResult(t, task), toolCtx)

	shown, _ := json.Marshal(parsed["artifacts"])
	for i := range 4 {
		if name := fmt.Sprintf("report-%d.pdf", i); !strings.Contains(string(shown), name) {
			t.Errorf("the cut view's artifacts lost %s: %s", name, out)
		}
	}
	if _, ok := parsed["produced_artifacts"]; !ok {
		t.Errorf("the cut view lost the saved-artifact manifest: %s", out)
	}
	if strings.Contains(out, inlineFileBytes) {
		t.Error("inline file content reached the cut view")
	}
	if saved := loadSaved(t, svc, savedArtifactName(t, parsed)); strings.Contains(saved, inlineFileBytes) {
		t.Error("inline file content was written into the saved copy of the answer")
	}
}

// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eval

import (
	"strings"
	"testing"
)

func statusEvent(parts ...map[string]any) map[string]any {
	anyParts := make([]any, len(parts))
	for i, p := range parts {
		anyParts[i] = p
	}
	return map[string]any{
		"payload": map[string]any{
			"result": map[string]any{
				"status": map[string]any{
					"state":   "working",
					"message": map[string]any{"parts": anyParts},
				},
			},
		},
	}
}

func parentStatusEvent(agentName string, parts ...map[string]any) map[string]any {
	ev := statusEvent(parts...)
	result := ev["payload"].(map[string]any)["result"].(map[string]any)
	result["metadata"] = map[string]any{"agent_name": agentName}
	return ev
}

func peerForwardedEvent(parentName, peerName string, parts ...map[string]any) map[string]any {
	ev := statusEvent(parts...)
	result := ev["payload"].(map[string]any)["result"].(map[string]any)
	message := result["status"].(map[string]any)["message"].(map[string]any)
	message["metadata"] = map[string]any{
		"agent_name":          parentName,
		"forwarded_from_peer": peerName,
	}
	return ev
}

func finalEvent(text string) map[string]any {
	return map[string]any{
		"payload": map[string]any{
			"result": map[string]any{
				"final": true,
				"status": map[string]any{
					"state":   "completed",
					"message": map[string]any{"parts": []any{textPart(text)}},
				},
			},
		},
	}
}

func textPart(text string) map[string]any {
	return map[string]any{"kind": "text", "text": text}
}

func dataPart(data map[string]any) map[string]any {
	return map[string]any{"kind": "data", "data": data}
}

func toolCallPart(name string, args map[string]any) map[string]any {
	return dataPart(map[string]any{
		"type":             "tool_invocation_start",
		"tool_name":        name,
		"tool_args":        args,
		"function_call_id": "fc-1",
	})
}

func toolResultPart(name string, result map[string]any) map[string]any {
	return dataPart(map[string]any{
		"type":             "tool_result",
		"tool_name":        name,
		"result_data":      result,
		"function_call_id": "fc-1",
	})
}

func llmInvocationPart(definedTools ...string) map[string]any {
	tools := make([]any, len(definedTools))
	for i, name := range definedTools {
		tools[i] = map[string]any{"name": name, "description": "a tool the agent may call"}
	}
	return dataPart(map[string]any{
		"type": "llm_invocation",
		"request": map[string]any{
			"config": map[string]any{"tools": tools},
		},
	})
}

func TestBuildJudgeTranscriptInterleaves(t *testing.T) {
	t.Parallel()
	messages := []map[string]any{
		statusEvent(textPart("Let me check the date. ")),
		statusEvent(llmInvocationPart("date_math", "secret_unused_tool")),
		statusEvent(toolCallPart("date_math", map[string]any{
			"operation": "add", "base_date": "2024-01-01", "days": 30,
		})),
		statusEvent(toolResultPart("date_math", map[string]any{
			"status": "success", "result": "2024-01-31",
		})),
		statusEvent(textPart("The date is 2024-01-31.")),
		finalEvent("The date is 2024-01-31."),
	}

	got := buildJudgeTranscript(messages, "Orchestrator")

	if !strings.Contains(got, "[assistant] Let me check the date.") {
		t.Errorf("missing first assistant segment:\n%s", got)
	}
	if !strings.Contains(got, "[tool call] date_math(") {
		t.Errorf("missing tool call line:\n%s", got)
	}
	for _, want := range []string{`"operation":"add"`, `"base_date":"2024-01-01"`, `"days":30`} {
		if !strings.Contains(got, want) {
			t.Errorf("tool call missing param %q:\n%s", want, got)
		}
	}
	if !strings.Contains(got, "[tool result] date_math → ") {
		t.Errorf("missing tool result line:\n%s", got)
	}
	if !strings.Contains(got, `"result":"2024-01-31"`) {
		t.Errorf("tool result missing payload:\n%s", got)
	}

	if strings.Contains(got, "secret_unused_tool") {
		t.Errorf("transcript leaked a tool definition:\n%s", got)
	}

	if n := strings.Count(got, "[assistant] The date is 2024-01-31."); n != 1 {
		t.Errorf("final assistant text appeared %d times, want 1:\n%s", n, got)
	}

	idxText1 := strings.Index(got, "[assistant] Let me check")
	idxCall := strings.Index(got, "[tool call] date_math")
	idxResult := strings.Index(got, "[tool result] date_math")
	idxText2 := strings.Index(got, "[assistant] The date is")
	if idxText1 >= idxCall || idxCall >= idxResult || idxResult >= idxText2 {
		t.Errorf("entries out of execution order: text1=%d call=%d result=%d text2=%d\n%s",
			idxText1, idxCall, idxResult, idxText2, got)
	}
}

func TestBuildJudgeTranscriptCapsToolResult(t *testing.T) {
	t.Parallel()
	huge := strings.Repeat("Z", maxToolPayloadChars+5000)
	messages := []map[string]any{
		statusEvent(toolCallPart("fetch", map[string]any{"url": "http://x"})),
		statusEvent(toolResultPart("fetch", map[string]any{"body": huge})),
	}

	got := buildJudgeTranscript(messages, "Orchestrator")

	if strings.Contains(got, huge) {
		t.Errorf("oversized tool result was inlined whole instead of capped")
	}
	if !strings.Contains(got, "truncated:") {
		t.Errorf("expected a truncation notice on the capped result:\n%s", got[:min(300, len(got))])
	}
	if len(got) > maxToolPayloadChars+2000 {
		t.Errorf("capped transcript still too large: %d bytes", len(got))
	}
}

func TestBuildJudgeTranscriptTotalBudget(t *testing.T) {
	t.Parallel()
	big := strings.Repeat("A", 40_000)
	var messages []map[string]any
	for _, tag := range []string{"first", "second", "third"} {
		messages = append(messages,
			statusEvent(toolCallPart("tool_"+tag, map[string]any{"marker": tag})),
			statusEvent(toolResultPart("tool_"+tag, map[string]any{"blob": big})),
		)
	}

	got := buildJudgeTranscript(messages, "Orchestrator")

	if !strings.Contains(got, "earlier event(s) omitted") {
		t.Errorf("expected an omitted-events notice:\n%s", got[:min(300, len(got))])
	}
	if !strings.Contains(got, "tool_third") {
		t.Errorf("most recent tool call should be retained")
	}
	if strings.Contains(got, "tool_first") {
		t.Errorf("oldest tool call should have been dropped under the budget")
	}
}

func TestBuildJudgeTranscriptEmpty(t *testing.T) {
	t.Parallel()
	messages := []map[string]any{
		statusEvent(llmInvocationPart("date_math")),
		finalEvent("All done."),
	}
	if got := buildJudgeTranscript(messages, "Orchestrator"); got != "" {
		t.Errorf("expected empty transcript, got:\n%s", got)
	}
	if got := buildJudgeTranscript(nil, "Orchestrator"); got != "" {
		t.Errorf("nil messages should yield empty transcript, got %q", got)
	}
}

func TestBuildJudgeTranscriptAttributesDelegation(t *testing.T) {
	t.Parallel()
	messages := []map[string]any{
		parentStatusEvent("agent_orch", textPart("I'll ask the Support agent. ")),
		parentStatusEvent("agent_orch", toolCallPart("peer_Support", map[string]any{
			"task_description": "Look up Salesforce support case number 00123456 and summarize it.",
		})),
		peerForwardedEvent("agent_orch", "Support", toolCallPart("salesforce_rest_request__salesforce_rest_request", map[string]any{
			"method": "GET", "path": "/services/data/v60.0/sobjects/Case/00123456",
		})),
		peerForwardedEvent("agent_orch", "Support", toolResultPart("salesforce_rest_request__salesforce_rest_request", map[string]any{
			"status": "success", "subject": "Login issue",
		})),
		parentStatusEvent("agent_orch", toolResultPart("peer_Support", map[string]any{
			"status": "success", "summary": "Case 00123456: login issue, resolved.",
		})),
		finalEvent("Case 00123456 was a login issue, now resolved."),
	}

	got := buildJudgeTranscript(messages, "Orchestrator")

	if !strings.Contains(got, `[Orchestrator → delegate] peer "Support" — task: `) {
		t.Errorf("missing delegation boundary line:\n%s", got)
	}
	if !strings.Contains(got, "00123456") {
		t.Errorf("delegation line missing task description:\n%s", got)
	}
	if !strings.Contains(got, `[Support → tool call] salesforce_rest_request__salesforce_rest_request(`) {
		t.Errorf("peer's salesforce call not attributed to the peer:\n%s", got)
	}
	if !strings.Contains(got, `[Support → tool result] salesforce_rest_request__salesforce_rest_request → `) {
		t.Errorf("peer's salesforce result not attributed to the peer:\n%s", got)
	}
	if strings.Contains(got, "[Orchestrator → tool call] salesforce_rest_request") {
		t.Errorf("peer's salesforce call wrongly attributed to the Orchestrator:\n%s", got)
	}
	if !strings.Contains(got, `[Orchestrator → delegate result] peer "Support" → `) {
		t.Errorf("missing delegation-result line:\n%s", got)
	}
	if !strings.Contains(got, "[Orchestrator] I'll ask the Support agent.") {
		t.Errorf("parent assistant text not attributed:\n%s", got)
	}

	idxDelegate := strings.Index(got, "→ delegate]")
	idxPeerCall := strings.Index(got, "[Support → tool call]")
	idxPeerResult := strings.Index(got, "[Support → tool result]")
	idxDelegateResult := strings.Index(got, "→ delegate result]")
	if idxDelegate >= idxPeerCall || idxPeerCall >= idxPeerResult || idxPeerResult >= idxDelegateResult {
		t.Errorf("delegation entries out of order: delegate=%d peerCall=%d peerResult=%d delegateResult=%d\n%s",
			idxDelegate, idxPeerCall, idxPeerResult, idxDelegateResult, got)
	}
}

func TestBuildJudgeTranscriptNoDelegationStaysUnattributed(t *testing.T) {
	t.Parallel()
	messages := []map[string]any{
		parentStatusEvent("agent_orch", textPart("Computing. ")),
		parentStatusEvent("agent_orch", toolCallPart("date_math", map[string]any{"operation": "add"})),
		parentStatusEvent("agent_orch", toolResultPart("date_math", map[string]any{"result": "2024-01-31"})),
		finalEvent("Done."),
	}

	got := buildJudgeTranscript(messages, "Orchestrator")

	if !strings.Contains(got, "[assistant] Computing.") {
		t.Errorf("expected legacy [assistant] line:\n%s", got)
	}
	if !strings.Contains(got, "[tool call] date_math(") {
		t.Errorf("expected legacy [tool call] line:\n%s", got)
	}
	if strings.Contains(got, "→ tool call]") || strings.Contains(got, "[Orchestrator]") {
		t.Errorf("single-agent transcript should not be attributed:\n%s", got)
	}
}

func TestCapToolResultJSONElidesPreviews(t *testing.T) {
	t.Parallel()
	rows := strings.Repeat("x", 16_000)

	got := capToolResultJSON(map[string]any{
		"message": "Query returned 28 rows",
		"content": rows,
	})

	if strings.Contains(got, rows) {
		t.Errorf("preview passed through verbatim (%d chars)", len(got))
	}
	if !strings.Contains(got, "Query returned 28 rows") {
		t.Errorf("summary lost: %q", got)
	}
	if !strings.Contains(got, "chars elided") {
		t.Errorf("elision not recorded: %q", got)
	}
	if len(got) > maxToolPreviewChars*2 {
		t.Errorf("line is %d chars; a single tool result should not dominate the prompt", len(got))
	}
	if !strings.Contains(got, strings.Repeat("x", 100)) {
		t.Errorf("no sample of the payload retained: %q", got[:min(len(got), 120)])
	}
}

func TestCapToolResultJSONKeepsSmallPreviews(t *testing.T) {
	t.Parallel()
	got := capToolResultJSON(map[string]any{
		"message": "Query returned 1 row",
		"content": `[{"region":"north"}]`,
	})

	if !strings.Contains(got, `[{\"region\":\"north\"}]`) {
		t.Errorf("small preview not passed through: %q", got)
	}
	if strings.Contains(got, "elided") {
		t.Errorf("small preview should not be elided: %q", got)
	}
}

func TestCapToolResultJSONLeavesOtherPayloadsAlone(t *testing.T) {
	t.Parallel()
	payload := map[string]any{"message": "done", "rows": float64(3)}
	before := capJSON(payload)

	if got := capToolResultJSON(payload); got != before {
		t.Errorf("payload without previews changed: %q -> %q", before, got)
	}
	if _, mutated := payload["content"]; mutated {
		t.Error("source payload mutated")
	}
}

func TestRenderTranscriptBlockFenced(t *testing.T) {
	t.Parallel()
	if got := renderTranscriptBlock(""); got != "" {
		t.Errorf("empty body should render empty block, got %q", got)
	}
	block := renderTranscriptBlock("[assistant] hi")
	if !strings.HasPrefix(block, "--- Agent transcript[") {
		t.Errorf("block missing open fence: %q", block)
	}
	if !strings.Contains(block, "--- End transcript[") {
		t.Errorf("block missing close fence: %q", block)
	}
	if !strings.Contains(block, "[assistant] hi") {
		t.Errorf("block missing body: %q", block)
	}
}

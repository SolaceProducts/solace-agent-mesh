// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
)

func sysMsg(s string) llm.ChatMessage  { return llm.ChatMessage{Role: "system", Content: s} }
func userMsg(s string) llm.ChatMessage { return llm.ChatMessage{Role: "user", Content: s} }
func toolResult(id, name, body string) llm.ChatMessage {
	return llm.ChatMessage{Role: "tool", Content: body, ToolCallID: id, ToolName: name}
}

func contentsOf(t *testing.T, sig map[string]any) []any {
	t.Helper()
	req, ok := sig["request"].(map[string]any)
	if !ok {
		t.Fatalf("request missing: %+v", sig)
	}
	c, _ := req["contents"].([]any)
	return c
}

func hasConfig(t *testing.T, sig map[string]any) bool {
	t.Helper()
	req, ok := sig["request"].(map[string]any)
	if !ok {
		t.Fatalf("request missing: %+v", sig)
	}
	_, ok = req["config"]
	return ok
}

func TestLLMInvocation_EmitsOnlyDelta(t *testing.T) {
	h := newInternalLLMHarness(t, newMockLLM(&llm.ChatResponse{Content: "x"}), "task-delta")

	tools := []llm.ToolDefinition{{Name: "search", Description: "search things"}}

	h.sp.PublishLLMInvocationDelta(t.Context(), &llm.ChatRequest{
		Model:    "m",
		Messages: []llm.ChatMessage{sysMsg("be helpful"), userMsg("hello")},
		Tools:    tools,
	}, "m", false)

	h.sp.PublishLLMInvocationDelta(t.Context(), &llm.ChatRequest{
		Model: "m",
		Messages: []llm.ChatMessage{
			sysMsg("be helpful"),
			userMsg("hello"),
			{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "c1", Name: "search", Arguments: "{}"}}},
			toolResult("c1", "search", `{"ok":true}`),
		},
		Tools: tools,
	}, "m", false)

	sigs := drainStatusSignals(t, h.broker, h.queue, 2)

	first := contentsOf(t, sigs[0])
	if len(first) != 1 {
		t.Fatalf("first event contents = %d, want 1 (the user turn)", len(first))
	}
	if !hasConfig(t, sigs[0]) {
		t.Error("first event must include the config block (system prompt + tools)")
	}

	second := contentsOf(t, sigs[1])
	if len(second) != 2 {
		t.Fatalf("second event contents = %d, want 2 (new assistant + tool result)", len(second))
	}
	if hasConfig(t, sigs[1]) {
		t.Error("second event must omit config — system prompt and tool set unchanged")
	}
	last, _ := second[len(second)-1].(map[string]any)
	parts, _ := last["parts"].([]any)
	if len(parts) == 0 {
		t.Fatalf("tool-result turn has no parts: %+v", last)
	}
	p0, _ := parts[0].(map[string]any)
	if _, ok := p0["function_response"]; !ok {
		t.Errorf("expected function_response part in delta, got %+v", p0)
	}
}

func TestLLMInvocation_ReemitsConfigWhenToolsChange(t *testing.T) {
	h := newInternalLLMHarness(t, newMockLLM(&llm.ChatResponse{Content: "x"}), "task-skill")

	base := []llm.ChatMessage{sysMsg("sys"), userMsg("hi")}
	toolsV1 := []llm.ToolDefinition{{Name: "a"}}
	toolsV2 := []llm.ToolDefinition{{Name: "a"}, {Name: "b"}}

	h.sp.PublishLLMInvocationDelta(t.Context(), &llm.ChatRequest{Model: "m", Messages: base, Tools: toolsV1}, "m", false)
	h.sp.PublishLLMInvocationDelta(t.Context(), &llm.ChatRequest{
		Model:    "m",
		Messages: append(append([]llm.ChatMessage{}, base...), userMsg("again")),
		Tools:    toolsV1,
	}, "m", false)
	h.sp.PublishLLMInvocationDelta(t.Context(), &llm.ChatRequest{
		Model:    "m",
		Messages: append(append([]llm.ChatMessage{}, base...), userMsg("again"), userMsg("more")),
		Tools:    toolsV2,
	}, "m", false)

	sigs := drainStatusSignals(t, h.broker, h.queue, 3)

	if !hasConfig(t, sigs[0]) {
		t.Error("event 0 (first) must include config")
	}
	if hasConfig(t, sigs[1]) {
		t.Error("event 1 must omit config — tools unchanged")
	}
	if !hasConfig(t, sigs[2]) {
		t.Error("event 2 must re-include config — tool set changed")
	}
}

func TestLLMInvocation_InternalCallDoesNotCorruptDelta(t *testing.T) {
	h := newInternalLLMHarness(t, newMockLLM(&llm.ChatResponse{Content: "x"}), "task-interleave")
	tools := []llm.ToolDefinition{{Name: "search"}}

	main1 := []llm.ChatMessage{sysMsg("sys"), userMsg("u1")}
	h.sp.PublishLLMInvocationDelta(t.Context(), &llm.ChatRequest{Model: "m", Messages: main1, Tools: tools}, "m", false)

	h.sp.PublishLLMInvocation(t.Context(), &llm.ChatRequest{
		Model:    "m",
		Messages: []llm.ChatMessage{sysMsg("internal sys"), userMsg("describe this image")},
	}, "m")

	main2 := append(append([]llm.ChatMessage{}, main1...),
		llm.ChatMessage{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "c1", Name: "search", Arguments: "{}"}}},
		toolResult("c1", "search", `{"ok":true}`),
	)
	h.sp.PublishLLMInvocationDelta(t.Context(), &llm.ChatRequest{Model: "m", Messages: main2, Tools: tools}, "m", false)

	sigs := drainStatusSignals(t, h.broker, h.queue, 3)

	if got := len(contentsOf(t, sigs[0])); got != 1 {
		t.Fatalf("main call 1 contents = %d, want 1", got)
	}
	if got := len(contentsOf(t, sigs[1])); got != 1 {
		t.Fatalf("internal call contents = %d, want 1 (full standalone dump)", got)
	}
	if got := len(contentsOf(t, sigs[2])); got != 2 {
		t.Fatalf("main call 2 contents = %d, want 2 (delta uncorrupted by internal call)", got)
	}
}

func TestLLMInvocation_TruncatesOversizedText(t *testing.T) {
	h := newInternalLLMHarness(t, newMockLLM(&llm.ChatResponse{Content: "x"}), "task-trunc")

	huge := strings.Repeat("Z", maxStatusEventTextBytes*3)
	h.sp.PublishLLMInvocationDelta(t.Context(), &llm.ChatRequest{
		Model:    "m",
		Messages: []llm.ChatMessage{sysMsg("sys"), userMsg(huge)},
	}, "m", false)

	sigs := drainStatusSignals(t, h.broker, h.queue, 1)
	contents := contentsOf(t, sigs[0])
	turn, _ := contents[0].(map[string]any)
	parts, _ := turn["parts"].([]any)
	p0, _ := parts[0].(map[string]any)
	text, _ := p0["text"].(string)
	if len(text) == 0 {
		t.Fatalf("missing text part: %+v", turn)
	}
	if len(text) >= len(huge) {
		t.Errorf("text not truncated: %d bytes (input %d)", len(text), len(huge))
	}
	if !strings.Contains(text, "truncated") {
		t.Errorf("expected truncation marker, got tail %q", text[max(0, len(text)-40):])
	}
}

func ephemeralTail(s string) llm.ChatMessage {
	return llm.ChatMessage{Role: "user", Content: s, NoCacheBreakpoint: true}
}

func turnHasPart(turn any, key string) bool {
	m, _ := turn.(map[string]any)
	parts, _ := m["parts"].([]any)
	for _, p := range parts {
		pm, _ := p.(map[string]any)
		if _, ok := pm[key]; ok {
			return true
		}
	}
	return false
}

func deltaMentions(contents []any, needle string) bool {
	for _, c := range contents {
		m, _ := c.(map[string]any)
		parts, _ := m["parts"].([]any)
		for _, p := range parts {
			pm, _ := p.(map[string]any)
			if text, _ := pm["text"].(string); strings.Contains(text, needle) {
				return true
			}
		}
	}
	return false
}

func TestLLMInvocation_SkipsEphemeralRequestTail(t *testing.T) {
	const ctxMarker = "SLACK_TRANSCRIPT_MARKER"

	h := newInternalLLMHarness(t, newMockLLM(&llm.ChatResponse{Content: "x"}), "task-ephemeral")
	tools := []llm.ToolDefinition{{Name: "search", Description: "search things"}}

	h.sp.PublishLLMInvocationDelta(t.Context(), &llm.ChatRequest{
		Model:    "m",
		Messages: []llm.ChatMessage{sysMsg("be helpful"), userMsg("hello"), ephemeralTail(ctxMarker)},
		Tools:    tools,
	}, "m", false)

	h.sp.PublishLLMInvocationDelta(t.Context(), &llm.ChatRequest{
		Model: "m",
		Messages: []llm.ChatMessage{
			sysMsg("be helpful"),
			userMsg("hello"),
			{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "c1", Name: "search", Arguments: "{}"}}},
			toolResult("c1", "search", `{"ok":true}`),
			ephemeralTail(ctxMarker),
		},
		Tools: tools,
	}, "m", false)

	sigs := drainStatusSignals(t, h.broker, h.queue, 2)
	first, second := contentsOf(t, sigs[0]), contentsOf(t, sigs[1])

	for i, contents := range [][]any{first, second} {
		if deltaMentions(contents, ctxMarker) {
			t.Errorf("event %d re-publishes the ephemeral request tail; it is not part of the "+
				"conversation the delta reports and would be re-sent on every call", i)
		}
	}

	if len(first) != 1 {
		t.Errorf("first event contents = %d, want 1 (the user turn alone)", len(first))
	}
	if len(second) == 0 {
		t.Fatal("second event carried no turns at all")
	}

	if !turnHasPart(second[0], "function_call") {
		t.Errorf("the assistant tool-call turn was dropped from the delta — first turn has "+
			"function_call=%v, function_response=%v; a task loses one assistant turn per "+
			"iteration and the visualization renders orphaned tool results",
			turnHasPart(second[0], "function_call"), turnHasPart(second[0], "function_response"))
	}
	if len(second) != 2 {
		t.Errorf("second event contents = %d, want 2 (the assistant tool call + its result)", len(second))
	}
}

func TestLLMInvocation_TrailingOmitsPerRequestContext(t *testing.T) {
	const transcript = "SLACK_TRANSCRIPT_MARKER"
	agent, _, _ := newTestAgent()
	ctxMsgs := agent.perRequestContextMessages(gatewayContext{ConversationContext: transcript})

	h := newInternalLLMHarness(t, newMockLLM(&llm.ChatResponse{Content: "x"}), "task-trailing-omit")
	h.sp.PublishLLMInvocationDelta(t.Context(), &llm.ChatRequest{
		Model:    "m",
		Messages: append([]llm.ChatMessage{sysMsg("be helpful"), userMsg("hello"), ephemeralTail("LAST_CALL_MARKER")}, ctxMsgs...),
	}, "m", false)

	sig := drainStatusSignals(t, h.broker, h.queue, 1)[0]
	req, _ := sig["request"].(map[string]any)
	trailing, _ := req[a2a.LLMRequestTrailing].([]any)
	if len(trailing) != 2 {
		t.Fatalf("trailing turns = %d, want 2", len(trailing))
	}
	if deltaMentions(trailing, transcript) {
		t.Error("the per-request context reached the trace under trailing")
	}
	if !deltaMentions(trailing, omittedTrailingText) {
		t.Error("expected a placeholder for the per-request context")
	}
	if !deltaMentions(trailing, "LAST_CALL_MARKER") {
		t.Error("the last-call warning should stay in the trace")
	}
}

func TestLLMInvocation_EphemeralTailDoesNotStrandLaterTurns(t *testing.T) {
	h := newInternalLLMHarness(t, newMockLLM(&llm.ChatResponse{Content: "x"}), "task-ephemeral-3")

	const calls = 3
	for turn := range calls {
		msgs := append(buildTaskMessages(turn), ephemeralTail("per-request context"))
		h.sp.PublishLLMInvocationDelta(t.Context(), &llm.ChatRequest{Model: "m", Messages: msgs}, "m", false)
	}

	sigs := drainStatusSignals(t, h.broker, h.queue, calls)
	total := 0
	for _, sig := range sigs {
		total += len(contentsOf(t, sig))
	}

	want := 0
	for _, m := range buildTaskMessages(calls - 1) {
		if m.Role != "system" {
			want++
		}
	}
	if total != want {
		t.Errorf("events carried %d turns across %d calls, want %d — the ephemeral tail shifted "+
			"the high-water mark, so turns were dropped or re-sent", total, calls, want)
	}
}

func buildTaskMessages(turn int) []llm.ChatMessage {
	msgs := []llm.ChatMessage{sysMsg(strings.Repeat("system prompt. ", 50)), userMsg("kick off the task")}
	body := `{"result":"` + strings.Repeat("x", 800) + `"}`
	for i := range turn {
		id := fmt.Sprintf("c%d", i)
		msgs = append(msgs,
			llm.ChatMessage{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: id, Name: "search", Arguments: `{"q":"foo"}`}}},
			toolResult(id, "search", body),
		)
	}
	return msgs
}

func benchTools() []llm.ToolDefinition {
	tools := make([]llm.ToolDefinition, 0, 20)
	for i := range 20 {
		tools = append(tools, llm.ToolDefinition{
			Name:        fmt.Sprintf("tool_%d", i),
			Description: strings.Repeat("describe the tool. ", 20),
			Parameters:  map[string]any{"type": "object", "properties": map[string]any{"q": map[string]any{"type": "string"}}},
		})
	}
	return tools
}

const benchTaskTurns = 40

func BenchmarkLLMInvocation_FullEachCall(b *testing.B) {
	tools := benchTools()
	for b.Loop() {
		for turn := 0; turn <= benchTaskTurns; turn++ {
			req := &llm.ChatRequest{Model: "m", Messages: buildTaskMessages(turn), Tools: tools}
			m := formatLLMRequest(req, "m")
			sanitizeBinaryInPlace(m)
			truncateStatusText(m, maxStatusEventTextBytes)
		}
	}
}

func BenchmarkLLMInvocation_Delta(b *testing.B) {
	tools := benchTools()
	for b.Loop() {
		sp := &StatusPublisher{logger: slog.Default()}
		for turn := 0; turn <= benchTaskTurns; turn++ {
			req := &llm.ChatRequest{Model: "m", Messages: buildTaskMessages(turn), Tools: tools}
			m := sp.buildLLMInvocationDelta(req, "m")
			sanitizeBinaryInPlace(m)
			truncateStatusText(m, maxStatusEventTextBytes)
		}
	}
}

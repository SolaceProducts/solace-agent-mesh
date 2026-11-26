// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

const lastCallWarningMarker = "Last allowed response"

func conversationFixture() []llm.ChatMessage {
	return []llm.ChatMessage{
		{Role: "system", Content: "You are a helpful assistant."},
		{Role: "user", Content: "how many albums did they release?"},
		{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "c1", Name: "search", Arguments: "{}"}}},
		{Role: "tool", ToolCallID: "c1", ToolName: "search", Content: `{"result":"seven"}`},
	}
}

func toolsFixture() []llm.ToolDefinition {
	return []llm.ToolDefinition{{Name: "artifact_list"}, {Name: "search"}}
}

func breakpointIndices(req *llm.ChatRequest) (msgs []int, tools int) {
	for i := range req.Messages {
		if req.Messages[i].CacheControl != nil {
			msgs = append(msgs, i)
		}
	}
	for i := range req.Tools {
		if req.Tools[i].CacheControl != nil {
			tools++
		}
	}
	return msgs, tools
}

func TestInjectLastCallWarningKeepsSystemMessageByteIdentical(t *testing.T) {
	in := conversationFixture()
	want := in[0].Content

	out := (&Agent{}).injectLastCallWarning(in)

	if out[0].Role != "system" {
		t.Fatalf("out[0].Role = %q, want system", out[0].Role)
	}
	if out[0].Content != want {
		t.Errorf("system message content changed.\n got: %q\nwant: %q", out[0].Content, want)
	}
}

func TestInjectLastCallWarningAppendsTrailingUserTurn(t *testing.T) {
	in := conversationFixture()

	out := (&Agent{}).injectLastCallWarning(in)

	if len(out) != len(in)+1 {
		t.Fatalf("len(out) = %d, want %d", len(out), len(in)+1)
	}
	last := out[len(out)-1]
	if last.Role != "user" {
		t.Errorf("appended message Role = %q, want user", last.Role)
	}
	if !strings.Contains(last.Content, lastCallWarningMarker) {
		t.Errorf("appended message missing warning text: %q", last.Content)
	}
	if !last.NoCacheBreakpoint {
		t.Error("appended message must set NoCacheBreakpoint so it cannot take a rolling breakpoint")
	}
	if strings.TrimSpace(last.Content) == "" {
		t.Error("appended message content is empty/whitespace")
	}
	for i := range in {
		if out[i].Role != in[i].Role || out[i].Content != in[i].Content {
			t.Errorf("out[%d] differs from in[%d]", i, i)
		}
	}
}

func TestInjectLastCallWarningDoesNotMutateInput(t *testing.T) {
	in := conversationFixture()
	in = append(make([]llm.ChatMessage, 0, len(in)+4), in...)

	type snap struct {
		role, content, toolCallID string
		toolCalls                 int
		cc                        *llm.CacheControl
		noCache                   bool
	}
	before := make([]snap, len(in))
	for i := range in {
		before[i] = snap{in[i].Role, in[i].Content, in[i].ToolCallID, len(in[i].ToolCalls), in[i].CacheControl, in[i].NoCacheBreakpoint}
	}
	backing := in[:cap(in)]

	out := (&Agent{}).injectLastCallWarning(in)

	if len(in) != len(before) {
		t.Errorf("input length changed: %d -> %d", len(before), len(in))
	}
	for i := range in {
		got := snap{in[i].Role, in[i].Content, in[i].ToolCallID, len(in[i].ToolCalls), in[i].CacheControl, in[i].NoCacheBreakpoint}
		if got != before[i] {
			t.Errorf("input message %d mutated: %+v -> %+v", i, before[i], got)
		}
	}
	if backing[len(before)].Role != "" {
		t.Errorf("appended into the caller's backing array at index %d (role %q)",
			len(before), backing[len(before)].Role)
	}
	if len(out) > 0 && &out[0] == &in[0] {
		t.Error("returned slice aliases the input backing array")
	}
}

func TestInjectLastCallWarningEmptyMessages(t *testing.T) {
	out := (&Agent{}).injectLastCallWarning(nil)
	if len(out) != 0 {
		t.Errorf("len(out) = %d, want 0", len(out))
	}
}

func TestInjectLastCallWarningIsCacheNeutral(t *testing.T) {
	plain := &llm.ChatRequest{Messages: conversationFixture(), Tools: toolsFixture()}
	applyCacheControl(plain, "5m")
	plainMsgs, plainTools := breakpointIndices(plain)

	warned := &llm.ChatRequest{
		Messages: (&Agent{}).injectLastCallWarning(conversationFixture()),
		Tools:    toolsFixture(),
	}
	applyCacheControl(warned, "5m")
	warnedMsgs, warnedTools := breakpointIndices(warned)

	if !slices.Equal(plainMsgs, warnedMsgs) {
		t.Errorf("breakpoint message indices changed: %v (plain) -> %v (with warning)", plainMsgs, warnedMsgs)
	}
	if plainTools != warnedTools {
		t.Errorf("tool breakpoint count changed: %d -> %d", plainTools, warnedTools)
	}
	rolling := 0
	for _, i := range plainMsgs {
		if i > 0 {
			rolling++
		}
	}
	if rolling < 2 {
		t.Fatalf("fixture saturates the rolling window with %d marks, need >= 2 (all marks: %v)", rolling, plainMsgs)
	}

	warningIdx := len(warned.Messages) - 1
	if warned.Messages[warningIdx].CacheControl != nil {
		t.Error("the warning message itself received a cache_control breakpoint")
	}
	for _, i := range warnedMsgs {
		if i >= warningIdx {
			t.Errorf("breakpoint at message %d is at or after the warning at %d — the warning is inside a cached prefix", i, warningIdx)
		}
	}
	for _, i := range plainMsgs {
		if plain.Messages[i].Content != warned.Messages[i].Content {
			t.Errorf("content at breakpoint %d differs, so the cached prefix is invalidated", i)
		}
	}
}

func TestApplyCacheControlSkipsNoCacheBreakpoint(t *testing.T) {
	req := &llm.ChatRequest{
		Messages: []llm.ChatMessage{
			{Role: "system", Content: "sys"},
			{Role: "user", Content: "q1"},
			{Role: "assistant", Content: "a1"},
			{Role: "user", Content: "ephemeral", NoCacheBreakpoint: true},
		},
		Tools: []llm.ToolDefinition{{Name: "t1"}},
	}

	applyCacheControl(req, "5m")

	if req.Messages[3].CacheControl != nil {
		t.Error("flagged message received a breakpoint")
	}
	if req.Messages[1].CacheControl == nil || req.Messages[2].CacheControl == nil {
		t.Error("rolling window did not fall through to the two real messages")
	}
	if req.Messages[0].CacheControl == nil {
		t.Error("system message lost its breakpoint")
	}
}

func TestInjectLastCallWarningAfterToolResults(t *testing.T) {
	in := conversationFixture()
	if in[len(in)-1].Role != "tool" {
		t.Fatalf("fixture should end in a tool result, got %q", in[len(in)-1].Role)
	}

	out := (&Agent{}).injectLastCallWarning(in)

	last := out[len(out)-1]
	if last.Role != "user" {
		t.Fatalf("appended Role = %q, want user", last.Role)
	}
	if last.ToolCallID != "" || len(last.ToolCalls) != 0 {
		t.Error("the warning turn must be a plain text turn, not a tool-call or tool-result turn")
	}
	if strings.TrimSpace(last.Content) == "" {
		t.Error("appended user turn has no content — providers reject empty user turns")
	}
}

func requestContainsWarning(req llm.ChatRequest) (bool, int) {
	for i := range req.Messages {
		if strings.Contains(req.Messages[i].Content, lastCallWarningMarker) {
			return true, i
		}
	}
	return false, -1
}

func registerNoopTool(t *testing.T, agent *Agent) {
	t.Helper()
	err := agent.Tools().Register(&testBuiltinTool{
		name:   "noop",
		params: &tool.JSONSchema{Type: "object", Properties: map[string]any{}},
	})
	if err != nil {
		t.Fatal(err)
	}
}

func toolCallResponse() *llm.ChatResponse {
	return &llm.ChatResponse{
		FinishReason: "tool_calls",
		ToolCalls:    []llm.ToolCall{{ID: "c1", Name: "noop", Arguments: "{}"}},
	}
}

func TestHandleTaskLastCallWarningTriggerBoundary(t *testing.T) {
	agent, mock, _ := newTestAgent(
		toolCallResponse(),
		toolCallResponse(),
		&llm.ChatResponse{Content: "done", FinishReason: "stop"},
	)
	agent.cfg.MaxLLMCalls = 3
	registerNoopTool(t, agent)

	_, handleErr := agent.HandleTask(
		context.Background(),
		"task-001",
		newUserMessage("test"),
		a2a.UserProperties{ClientID: "user-1", UserID: "user-1"},
	)
	if handleErr != nil {
		t.Fatal(handleErr)
	}

	calls := mock.getCalls()
	if len(calls) != 3 {
		t.Fatalf("expected 3 LLM calls, got %d", len(calls))
	}

	for i, want := range []bool{false, false, true} {
		got, _ := requestContainsWarning(calls[i])
		if got != want {
			t.Errorf("call %d: warning present = %v, want %v (MaxCalls=3, fires only at index 2)", i, got, want)
		}
	}
}

func TestHandleTaskLastCallWarningNeverPersisted(t *testing.T) {
	agent, mock, store := newTestAgent(
		toolCallResponse(),
		toolCallResponse(),
		&llm.ChatResponse{Content: "done", FinishReason: "stop"},
	)
	agent.cfg.MaxLLMCalls = 3
	registerNoopTool(t, agent)

	if _, err := agent.HandleTask(
		context.Background(),
		"task-001",
		newUserMessage("test"),
		a2a.UserProperties{ClientID: "user-1", UserID: "user-1"},
	); err != nil {
		t.Fatal(err)
	}

	calls := mock.getCalls()
	if len(calls) != 3 {
		t.Fatalf("expected 3 LLM calls, got %d", len(calls))
	}
	if fired, _ := requestContainsWarning(calls[2]); !fired {
		t.Fatal("warning never fired, so persistence cannot be under test")
	}

	sess, err := store.Get(context.Background(), "test-agent", "user-1", "ctx-1")
	if err != nil {
		t.Fatalf("session not found: %v", err)
	}
	if sess == nil {
		t.Fatal("session is nil")
	}
	for i, m := range sess.Messages {
		if strings.Contains(m.Content, lastCallWarningMarker) {
			t.Errorf("last-call warning leaked into persisted history at message %d (role %q)", i, m.Role)
		}
	}
}

func TestHandleTaskMarksOnlyTheLastAllowedCallsInvocation(t *testing.T) {
	const taskID = "task-last-allowed"
	agent, _, _ := newTestAgent(
		toolCallResponse(),
		toolCallResponse(),
		&llm.ChatResponse{Content: "done", FinishReason: "stop"},
	)
	agent.cfg.MaxLLMCalls = 3
	registerNoopTool(t, agent)
	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatalf("connect broker: %v", err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })
	agent.svc.(*mockServices).brk = brk
	statusTopic := "test-ns/a2a/v1/gateway/status/test-gw/" + taskID
	q, err := brk.CreateQueue("q-"+taskID, broker.QueueOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err = brk.Subscribe(q, statusTopic); err != nil {
		t.Fatal(err)
	}

	if _, err = agent.HandleTask(t.Context(), taskID, newUserMessage("test"),
		a2a.UserProperties{ClientID: "c", UserID: "u", A2AStatusTopic: statusTopic}); err != nil {
		t.Fatal(err)
	}

	var marks []bool
	for _, msg := range receiveAll(t, brk, q, 64, 2*time.Second) {
		var envelope struct {
			Result struct {
				Status struct {
					Message *struct {
						Parts []struct {
							Data map[string]any `json:"data"`
						} `json:"parts"`
					} `json:"message"`
				} `json:"status"`
			} `json:"result"`
		}
		if json.Unmarshal(msg.Payload, &envelope) != nil || envelope.Result.Status.Message == nil {
			continue
		}
		for _, part := range envelope.Result.Status.Message.Parts {
			if part.Data["type"] != string(a2a.SignalLLMInvocation) {
				continue
			}
			request, _ := part.Data["request"].(map[string]any)
			marked, _ := request[a2a.LLMRequestLastAllowedCall].(bool)
			marks = append(marks, marked)
		}
	}
	if !slices.Equal(marks, []bool{false, false, true}) {
		t.Errorf("last-allowed marks on the task's invocations = %v, want only the third", marks)
	}
}

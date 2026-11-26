// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/structured"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

const (
	firstOutputFilename  = "wf_classify_output_ed08558e.json"
	secondOutputFilename = "wf_classify_output_45b095a5.json"
)

func newStructuredAgent(t *testing.T, responses ...*llm.ChatResponse) (*Agent, *mockLLM) {
	t.Helper()
	agent, mock, _ := newTestAgent(responses...)
	handler, err := structured.NewHandler(structured.HandlerConfig{MaxRetries: 1, Logger: slog.Default()})
	if err != nil {
		t.Fatal(err)
	}
	agent.structuredHandler = handler
	return agent, mock
}

func structuredRunContext(outputFilename string) *taskRunContext {
	return &taskRunContext{structuredCtx: &structured.Context{
		Request: &a2a.StructuredInvocationRequest{
			NodeID:                  "classify",
			WorkflowName:            "invoice-exceptions",
			SuggestedOutputFilename: outputFilename,
		},
		MaxRetries: 1,
	}}
}

func TestStructuredInvocationLeavesSystemPromptStable(t *testing.T) {
	agent, _ := newStructuredAgent(t)

	var prompts []string
	for _, filename := range []string{firstOutputFilename, secondOutputFilename} {
		state := &TaskExecutionState{Messages: []llm.ChatMessage{{Role: "system"}, {Role: "user", Content: "route this"}}}
		var cache promptToolCache
		agent.refreshToolsAndPrompt(t.Context(), state, structuredRunContext(filename), &cache)

		if strings.Contains(state.Messages[0].Content, filename) {
			t.Errorf("the system prompt carries the invocation's output filename %q", filename)
		}
		prompts = append(prompts, state.Messages[0].Content)
	}

	if prompts[0] != prompts[1] {
		t.Error("the system prompt differs between two invocations of the same node, so the cached prefix is invalidated")
	}
}

func TestStructuredInstructionsRideAfterEveryCacheBreakpoint(t *testing.T) {
	agent, _ := newStructuredAgent(t)

	req := &llm.ChatRequest{
		Messages: agent.appendStructuredInstructions(conversationFixture(), structuredRunContext(firstOutputFilename)),
		Tools:    toolsFixture(),
	}
	applyCacheControl(req, "5m")

	idx := indexOfContent(req.Messages, firstOutputFilename)
	if idx != len(req.Messages)-1 {
		t.Fatalf("instructions at message %d of %d, want the trailing turn", idx, len(req.Messages))
	}
	if req.Messages[idx].CacheControl != nil || !req.Messages[idx].NoCacheBreakpoint {
		t.Error("the instructions turn is a cache breakpoint, so it would be cached under a prefix that never repeats")
	}
	breakpoints, _ := breakpointIndices(req)
	for _, i := range breakpoints {
		if i >= idx {
			t.Errorf("breakpoint at message %d is at or after the instructions at %d", i, idx)
		}
	}

	notStructured := agent.appendStructuredInstructions(conversationFixture(), &taskRunContext{})
	if len(notStructured) != len(conversationFixture()) {
		t.Errorf("a task that is not a structured invocation gained %d message(s)", len(notStructured)-len(conversationFixture()))
	}
}

func TestStructuredRetryCarriesInstructions(t *testing.T) {
	agent, mock := newStructuredAgent(t, &llm.ChatResponse{Content: "still no embed", FinishReason: "stop"})

	state := &TaskExecutionState{
		TaskID:    "task-006",
		Messages:  []llm.ChatMessage{{Role: "system", Content: "sys"}, {Role: "user", Content: "classify the invoice"}},
		UserProps: a2a.UserProperties{ClientID: "user-1", UserID: "user-1"},
	}
	rc := structuredRunContext(firstOutputFilename)
	rc.sp = NewStatusPublisher(nil, StatusPublisherConfig{TaskID: "task-006", ContextID: "ctx-1", AgentName: "test-agent", ClientID: "client-1", UserID: "user-1"}, slog.Default())
	rc.msg = newUserMessage("classify the invoice")
	rc.toolCtx = &tool.Context{}
	rc.originalSessionID = "sess-1"
	rc.logger = agent.logger

	if _, err := agent.finalizeStructuredInvocation(context.Background(), state, rc, "no embed here"); err != nil {
		t.Fatal(err)
	}

	calls := mock.getCalls()
	if len(calls) != 1 {
		t.Fatalf("expected exactly the one retry LLM call, got %d", len(calls))
	}
	if strings.Contains(calls[0].Messages[0].Content, firstOutputFilename) {
		t.Error("the retry put the output filename in the system message")
	}
	msgs := calls[0].Messages
	if idx := indexOfContent(msgs, firstOutputFilename); idx != len(msgs)-1 || !msgs[idx].NoCacheBreakpoint {
		t.Errorf("the structured retry's instructions are at message %d of %d, want the trailing turn flagged NoCacheBreakpoint", idx, len(msgs))
	}
}

const workflowInstructionsMarker = "You are node "

func TestStructuredInstructionsReachEveryLLMCallOfAnInvocation(t *testing.T) {
	llmMock := &scriptedStreamLLM{turns: [][]llm.StreamChunk{outputWithoutResultEmbed, artifactSaveStreamTurn, outputWithResultEmbed}}

	runStructuredInvocationWithMetadata(t.Context(), t, "task-si-instructions-every-call", llmMock, 1, nil, func(a *Agent) {
		a.cfg.CacheStrategy = "5m"
	})

	if got := llmMock.streamCalls(); got != 3 {
		t.Fatalf("LLM stream calls = %d, want 3 (main, retry, artifact follow-up)", got)
	}
	for call := range 3 {
		msgs := llmMock.request(t, call)
		if strings.Contains(msgs[0].Content, workflowInstructionsMarker) {
			t.Errorf("call %d: the system message carries the workflow instructions", call)
		}
		last := msgs[len(msgs)-1]
		if !strings.Contains(last.Content, workflowInstructionsMarker) {
			t.Errorf("call %d: the last message is not the workflow instructions: %q", call, last.Content)
			continue
		}
		if last.CacheControl != nil || !last.NoCacheBreakpoint {
			t.Errorf("call %d: the instructions turn is a cache breakpoint", call)
		}
	}
}

func TestStructuredInstructionsSitBetweenGatewayContextAndLastCallWarning(t *testing.T) {
	toolCallTurn := []llm.StreamChunk{
		{ToolCallDeltas: []llm.ToolCallDelta{{Index: 0, ID: "call-1", Name: "noop", ArgumentsDelta: "{}"}}},
		{FinishReason: "tool_calls"},
	}
	llmMock := &scriptedStreamLLM{turns: [][]llm.StreamChunk{toolCallTurn, outputWithResultEmbed}}
	metadata := map[string]any{"conversation_context": slackCtxFixture}

	runStructuredInvocationWithMetadata(t.Context(), t, "task-si-tail-order", llmMock, 2, metadata, func(a *Agent) {
		registerNoopTool(t, a)
	})

	if got := llmMock.streamCalls(); got != 2 {
		t.Fatalf("LLM stream calls = %d, want 2", got)
	}
	last := llmMock.request(t, 1)
	ctxIdx := indexOfContent(last, slackCtxFixture)
	instructionsIdx := indexOfContent(last, workflowInstructionsMarker)
	warningIdx := indexOfContent(last, lastCallWarningMarker)
	if ctxIdx < 0 || instructionsIdx < 0 || warningIdx < 0 {
		t.Fatalf("the last call lacks a trailing turn: context=%d instructions=%d warning=%d", ctxIdx, instructionsIdx, warningIdx)
	}
	if ctxIdx >= instructionsIdx || instructionsIdx >= warningIdx || warningIdx != len(last)-1 {
		t.Errorf("trailing turns out of order of %d messages: context=%d instructions=%d warning=%d, want context, instructions, warning last", len(last), ctxIdx, instructionsIdx, warningIdx)
	}
}

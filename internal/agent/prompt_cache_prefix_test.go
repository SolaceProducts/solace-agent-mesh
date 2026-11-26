// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"log/slog"
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/structured"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

const slackCtxFixture = "--- BEGIN SLACK CONTEXT ---\n> <@U1>: hi\n--- END SLACK CONTEXT ---"

func indexOfContent(msgs []llm.ChatMessage, needle string) int {
	found := -1
	for i := range msgs {
		if strings.Contains(msgs[i].Content, needle) {
			found = i
		}
	}
	return found
}

func TestSystemPromptIsStableAcrossRequests(t *testing.T) {
	cases := []struct {
		name string
		a, b gatewayContext
		why  string
	}{
		{
			name: "slack conversation context",
			a:    gatewayContext{ConversationContext: "thread:\nalice: hi\n"},
			b:    gatewayContext{ConversationContext: "thread:\nalice: hi\nbob: hello\n"},
			why: "the Slack adapter re-fetches up to 12KB of surrounding transcript per user " +
				"message, so every task in a thread would present a different prefix",
		},
		{
			name: "user profile",
			a:    gatewayContext{UserProfile: "Alice, engineering"},
			b:    gatewayContext{UserProfile: "Bob, support"},
			why:  "a per-user prefix is never shared between users",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			agent, _, _ := newTestAgent()
			agent.cfg.InjectUserProfile = true

			first := agent.buildSystemPrompt(t.Context(), nil, c.a, promptSkillState{}, false)
			second := agent.buildSystemPrompt(t.Context(), nil, c.b, promptSkillState{}, false)

			if first != second {
				t.Errorf("system prompt differs between requests, so the cached prefix is "+
					"invalidated: %s", c.why)
			}
		})
	}
}

func TestAppendPerRequestContextIsCacheNeutral(t *testing.T) {
	agent, _, _ := newTestAgent()
	agent.cfg.InjectUserProfile = true
	gc := gatewayContext{UserProfile: "Alice, engineering", ConversationContext: slackCtxFixture}

	plain := &llm.ChatRequest{Messages: conversationFixture(), Tools: toolsFixture()}
	applyCacheControl(plain, "5m")
	plainMsgs, plainTools := breakpointIndices(plain)

	withCtx := &llm.ChatRequest{
		Messages: agent.appendPerRequestContext(conversationFixture(), gc),
		Tools:    toolsFixture(),
	}
	applyCacheControl(withCtx, "5m")
	ctxMsgs, ctxTools := breakpointIndices(withCtx)

	if !slices.Equal(plainMsgs, ctxMsgs) {
		t.Errorf("breakpoint message indices changed: %v (plain) -> %v (with context)", plainMsgs, ctxMsgs)
	}
	if plainTools != ctxTools {
		t.Errorf("tool breakpoint count changed: %d -> %d", plainTools, ctxTools)
	}

	rolling := 0
	for _, i := range plainMsgs {
		if i > 0 {
			rolling++
		}
	}
	if rolling < 2 {
		t.Fatalf("fixture saturates the rolling window with %d marks, need >= 2", rolling)
	}

	ctxIdx := indexOfContent(withCtx.Messages, slackCtxFixture)
	if ctxIdx < 0 {
		t.Fatal("the context never reached the request")
	}
	if withCtx.Messages[ctxIdx].CacheControl != nil {
		t.Error("the context message itself received a cache_control breakpoint")
	}
	for _, i := range ctxMsgs {
		if i >= ctxIdx {
			t.Errorf("breakpoint at message %d is at or after the context block at %d — "+
				"the volatile context is inside a cached prefix", i, ctxIdx)
		}
	}
	for _, i := range plainMsgs {
		if plain.Messages[i].Content != withCtx.Messages[i].Content {
			t.Errorf("content at breakpoint %d differs, so the cached prefix is invalidated", i)
		}
	}
}

func TestPerRequestContextDoesNotTripHistoryRepairs(t *testing.T) {
	agent, mock, _ := newTestAgent(&llm.ChatResponse{Content: "answer"})

	msg := newUserMessage("first question")
	msg.Metadata = map[string]any{"conversation_context": slackCtxFixture}
	if _, err := agent.HandleTask(context.Background(), "task-001", msg,
		a2a.UserProperties{ClientID: "user-1", UserID: "user-1"}); err != nil {
		t.Fatal(err)
	}

	calls := mock.getCalls()
	if len(calls) == 0 {
		t.Fatal("no LLM call recorded")
	}
	for _, m := range calls[0].Messages {
		if strings.Contains(m.Content, cancellationMarkerContent) {
			t.Errorf("a clean first turn was told a previous request was cancelled; "+
				"the context block was mistaken for an orphaned user turn (role=%s)", m.Role)
		}
	}
}

func TestPerRequestContextOnEveryCallOfATask(t *testing.T) {
	agent, mock, _ := newTestAgent(
		&llm.ChatResponse{ToolCalls: []llm.ToolCall{{ID: "c1", Name: "test_tool", Arguments: "{}"}}},
		&llm.ChatResponse{Content: "done"},
	)
	agent.cfg.MaxLLMCalls = 2

	msg := newUserMessage("question")
	msg.Metadata = map[string]any{"conversation_context": slackCtxFixture}
	if _, err := agent.HandleTask(context.Background(), "task-002", msg,
		a2a.UserProperties{ClientID: "user-1", UserID: "user-1"}); err != nil {
		t.Fatal(err)
	}

	calls := mock.getCalls()
	if len(calls) < 2 {
		t.Fatalf("expected a tool loop of >=2 calls, got %d", len(calls))
	}
	for n, call := range calls {
		seen := 0
		for _, m := range call.Messages {
			if strings.Contains(m.Content, slackCtxFixture) {
				seen++
			}
		}
		if seen != 1 {
			t.Errorf("call %d carried the context %d times, want exactly 1 "+
				"(0 = dropped on a later turn, >1 = compounding)", n+1, seen)
		}
	}
}

func TestPerRequestContextAndLastCallWarningStayOutsideTheCachedPrefix(t *testing.T) {
	agent, mock, _ := newTestAgent(
		toolCallResponse(),
		toolCallResponse(),
		&llm.ChatResponse{Content: "done", FinishReason: "stop"},
	)
	agent.cfg.MaxLLMCalls = 3
	agent.cfg.CacheStrategy = "5m"
	registerNoopTool(t, agent)

	msg := newUserMessage("question")
	msg.Metadata = map[string]any{"conversation_context": slackCtxFixture}
	if _, err := agent.HandleTask(context.Background(), "task-004", msg,
		a2a.UserProperties{ClientID: "user-1", UserID: "user-1"}); err != nil {
		t.Fatal(err)
	}

	calls := mock.getCalls()
	if len(calls) != 3 {
		t.Fatalf("expected 3 LLM calls, got %d", len(calls))
	}

	last := calls[len(calls)-1].Messages
	ctxIdx := indexOfContent(last, slackCtxFixture)
	if ctxIdx < 0 {
		t.Fatal("the context was dropped on the last call, where the warning also lands")
	}
	warnIdx := indexOfContent(last, lastCallWarningMarker)
	if warnIdx < 0 {
		t.Fatalf("the last-call warning is absent from call %d, so this test is not "+
			"exercising the combination it exists for", len(calls))
	}

	for _, i := range []int{ctxIdx, warnIdx} {
		if last[i].CacheControl != nil {
			t.Errorf("trailing message %d took a cache_control breakpoint; both must stay "+
				"neutral so the rolling window keeps landing on real turns", i)
		}
	}

	firstVolatile := min(ctxIdx, warnIdx)
	for i := range last {
		if i >= firstVolatile && last[i].CacheControl != nil {
			t.Errorf("breakpoint at message %d is at or after the first volatile trailing "+
				"message at %d (context=%d, warning=%d) — a breakpoint caches the prefix up to "+
				"and including its own block, so the transcript is inside a cached segment and "+
				"the next task cannot reuse the conversation tail",
				i, firstVolatile, ctxIdx, warnIdx)
		}
	}
}

func TestPerRequestContextReachesTheModel(t *testing.T) {
	agent, mock, _ := newTestAgent(&llm.ChatResponse{Content: "answer"})
	agent.cfg.InjectUserProfile = true
	agent.cfg.CacheStrategy = "5m"

	msg := newUserMessage("question")
	msg.Metadata = map[string]any{
		"conversation_context": slackCtxFixture,
		"user_profile":         "Alice, engineering",
	}
	if _, err := agent.HandleTask(context.Background(), "task-003", msg,
		a2a.UserProperties{ClientID: "user-1", UserID: "user-1"}); err != nil {
		t.Fatal(err)
	}

	calls := mock.getCalls()
	if len(calls) == 0 {
		t.Fatal("no LLM call recorded")
	}
	msgs := calls[0].Messages

	if strings.Contains(msgs[0].Content, slackCtxFixture) {
		t.Error("context must not be in the system prompt: it carries the cache breakpoint")
	}

	carrierIdx := indexOfContent(msgs, slackCtxFixture)
	if carrierIdx < 0 {
		t.Fatal("conversation context never reached the model")
	}
	carrier := &msgs[carrierIdx]
	if carrier.Role == "system" {
		t.Error("per-request context must not use the system role: the Bifrost Anthropic " +
			"translator hoists system messages into the top-level system parameter, last one wins")
	}
	if !carrier.NoCacheBreakpoint {
		t.Error("per-request context must not become a cache breakpoint itself")
	}

	for i := range msgs {
		if i >= carrierIdx && msgs[i].CacheControl != nil {
			t.Errorf("breakpoint at message %d is at or after the context at %d — the "+
				"transcript is inside a cached prefix, so the conversation tail cannot be reused",
				i, carrierIdx)
		}
	}
}

func TestPerRequestContextRespectsInjectUserProfile(t *testing.T) {
	agent, _, _ := newTestAgent()
	gc := gatewayContext{UserProfile: "Alice, engineering", ConversationContext: slackCtxFixture}

	agent.cfg.InjectUserProfile = false
	off := agent.perRequestContextMessages(gc)
	if len(off) != 1 {
		t.Fatalf("expected the conversation context to still be delivered, got %d messages", len(off))
	}
	if strings.Contains(off[0].Content, "Alice, engineering") {
		t.Error("profile delivered despite inject_user_profile being false")
	}

	agent.cfg.InjectUserProfile = true
	if on := agent.perRequestContextMessages(gc); !strings.Contains(on[0].Content, "Alice, engineering") {
		t.Error("profile withheld despite inject_user_profile being true")
	}
}

func TestPerRequestContextEmptyOrWhitespaceProducesNothing(t *testing.T) {
	agent, _, _ := newTestAgent()
	agent.cfg.InjectUserProfile = true

	for name, gc := range map[string]gatewayContext{
		"nothing set": {},
		"whitespace":  {UserProfile: "  ", ConversationContext: "\n\t "},
	} {
		if got := agent.perRequestContextMessages(gc); got != nil {
			t.Errorf("%s: expected no message, got %d", name, len(got))
		}
	}
}

func TestStructuredRetryCarriesPerRequestContext(t *testing.T) {
	const profile = "Alice, engineering"

	agent, mock, _ := newTestAgent(&llm.ChatResponse{Content: "still no embed", FinishReason: "stop"})
	agent.cfg.InjectUserProfile = true

	handler, err := structured.NewHandler(structured.HandlerConfig{MaxRetries: 1, Logger: slog.Default()})
	if err != nil {
		t.Fatal(err)
	}
	agent.structuredHandler = handler

	msg := newUserMessage("produce the report")
	state := &TaskExecutionState{
		TaskID:   "task-005",
		Messages: []llm.ChatMessage{{Role: "system", Content: "sys"}, {Role: "user", Content: "produce the report"}},
		UserProps: a2a.UserProperties{
			ClientID: "user-1",
			UserID:   "user-1",
		},
	}
	rc := &taskRunContext{
		sp:  NewStatusPublisher(nil, StatusPublisherConfig{TaskID: "task-005", ContextID: "ctx-1", AgentName: "test-agent", ClientID: "client-1", UserID: "user-1"}, slog.Default()),
		msg: msg,
		gc:  gatewayContext{UserProfile: profile},
		structuredCtx: &structured.Context{
			Request:    &a2a.StructuredInvocationRequest{NodeID: "node-1"},
			MaxRetries: 1,
		},
		toolCtx:           &tool.Context{},
		originalSessionID: "sess-1",
		logger:            agent.logger,
	}

	if _, err := agent.finalizeStructuredInvocation(context.Background(), state, rc, "no embed here"); err != nil {
		t.Fatal(err)
	}

	calls := mock.getCalls()
	if len(calls) != 1 {
		t.Fatalf("expected exactly the one retry LLM call, got %d", len(calls))
	}
	if indexOfContent(calls[0].Messages, profile) < 0 {
		t.Error("the structured retry lost the user profile: it is not assembled by the main " +
			"loop, so it needs the per-request context appended at its own request site")
	}
}

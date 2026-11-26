// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
)

type countingTruncatingLLM struct {
	truncatingStreamLLM
	streams int
	prompts [][]llm.ChatMessage
}

func (c *countingTruncatingLLM) ChatCompletionStream(ctx context.Context, req *llm.ChatRequest) (<-chan llm.StreamChunk, error) {
	c.streams++
	c.prompts = append(c.prompts, slices.Clone(req.Messages))
	return c.truncatingStreamLLM.ChatCompletionStream(ctx, req)
}

func newFailingAgent(t *testing.T) (*Agent, *countingTruncatingLLM, *mockServices) {
	t.Helper()
	truncation := &llm.Error{Type: "stream_truncated", Provider: "anthropic", Message: "stream ended without a finish reason"}
	requireStreamTruncation(t, truncation)
	failing := &countingTruncatingLLM{truncatingStreamLLM: truncatingStreamLLM{truncation: truncation}}

	agent := New(AgentConfig{
		Name:        "orphan-agent",
		Model:       "test-model",
		MaxTokens:   1000,
		MaxLLMCalls: 5,
		Namespace:   "test-ns",
	})
	svc := newMockServices(failing, newMemSessionStore())
	svc.featureRegistry = durableResumeRegistry(true)
	if err := agent.Init(t.Context(), nil, svc); err != nil {
		t.Fatalf("Init: %v", err)
	}
	agent.checkpoints = newTaskCheckpointer(svc, agent.cfg.Name, newTestAgentBackstops(), agent.logger)
	if agent.checkpoints == nil {
		t.Fatal("expected durable resume to be enabled")
	}
	return agent, failing, svc
}

func storedFailedTaskRows(t *testing.T, agent *Agent, svc *mockServices, userID string) []session.Message {
	t.Helper()
	sess, err := svc.Sessions().Get(t.Context(), agent.cfg.Name, sessionUserID(userID), "ctx-orphan")
	if err != nil || sess == nil {
		t.Fatalf("load session: %v (%v)", err, sess)
	}
	return sess.Messages
}

func TestFailedTaskStoresATerminalTurnForItsQuestion(t *testing.T) {
	agent, _, svc := newFailingAgent(t)

	user := a2a.UserProperties{ClientID: "c1", UserID: "u1"}
	resp, err := agent.HandleTask(t.Context(), "task-fails", concUserMessage("save a markdown file", "ctx-orphan"), user)
	if err != nil {
		t.Fatalf("HandleTask: %v", err)
	}
	if resp == nil || resp.Error == nil {
		t.Fatalf("task did not fail: %+v", resp)
	}

	rows := storedFailedTaskRows(t, agent, svc, user.UserID)
	if len(rows) != 2 {
		t.Fatalf("stored rows = %+v, want the question followed by one terminal assistant turn", rows)
	}
	if last := rows[1]; last.Role != "assistant" || last.Content == "" || last.TaskID != "task-fails" {
		t.Errorf("last stored row = %+v, want a terminal assistant turn for task-fails", last)
	}
}

func TestRedeliveredFailedTaskRunsAgainWithoutItsFailedAttemptInThePrompt(t *testing.T) {
	agent, failing, _ := newFailingAgent(t)
	user := a2a.UserProperties{ClientID: "c1", UserID: "u1"}
	msg := concUserMessage("save a markdown file", "ctx-orphan")

	if _, err := agent.handleTask(t.Context(), "task-fails", msg, user, taskDelivery{}); err != nil {
		t.Fatalf("first run: %v", err)
	}
	if failing.streams != 1 {
		t.Fatalf("model calls after the first run = %d, want 1", failing.streams)
	}

	if _, err := agent.handleTask(t.Context(), "task-fails", msg, user, taskDelivery{redelivered: true}); err != nil {
		t.Fatalf("redelivered run: %v", err)
	}
	if failing.streams != 2 {
		t.Fatalf("model calls after the redelivery = %d, want 2: a failed task must run again, not be dropped", failing.streams)
	}

	var questions int
	for _, m := range failing.prompts[1] {
		if m.Role == "assistant" {
			t.Errorf("rerun prompt holds an assistant turn from the failed attempt, telling the model not to re-execute the request it is rerunning: %+v", m)
		}
		if m.Role == "user" && strings.Contains(m.Content, "save a markdown file") {
			questions++
		}
	}
	if questions != 1 {
		t.Errorf("rerun prompt holds the question %d times, want once: %+v", questions, failing.prompts[1])
	}
}

func TestCancelledFailingTaskStoresTheCancellationMarker(t *testing.T) {
	agent, _, svc := newFailingAgent(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	user := a2a.UserProperties{ClientID: "c1", UserID: "u1"}
	if _, err := agent.HandleTask(ctx, "task-cancelled", concUserMessage("save a markdown file", "ctx-orphan"), user); err != nil {
		t.Fatalf("HandleTask: %v", err)
	}

	rows := storedFailedTaskRows(t, agent, svc, user.UserID)
	if len(rows) != 2 || rows[1].Content != cancellationMarkerContent {
		t.Fatalf("stored rows = %+v, want the question followed by the cancellation marker", rows)
	}
}

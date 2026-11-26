// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
)

func schedulerHistoryMeta(pairs ...[2]string) map[string]any {
	entries := make([]any, 0, len(pairs))
	for _, p := range pairs {
		entries = append(entries, map[string]any{"role": p[0], "content": p[1]})
	}
	return map[string]any{"schedulerConversationHistory": entries}
}

func TestHandleTask_InjectsSchedulerHistory(t *testing.T) {
	agent, mock, _ := newTestAgent(&llm.ChatResponse{Content: "ok"})
	agent.cfg.InjectCurrentTime = new(bool)

	msg := newUserMessage("what did you just do?")
	msg.Metadata = schedulerHistoryMeta(
		[2]string{"user", "run the daily report"},
		[2]string{"assistant", "the daily report is complete"},
	)

	if _, err := agent.HandleTask(context.Background(), "task-001", msg,
		a2a.UserProperties{ClientID: "user-1", UserID: "user-1"}); err != nil {
		t.Fatal(err)
	}

	calls := mock.getCalls()
	if len(calls) != 1 {
		t.Fatalf("expected 1 LLM call, got %d", len(calls))
	}
	msgs := calls[0].Messages
	var sawHistUser, sawHistAssistant bool
	for _, m := range msgs {
		if m.Role == "user" && m.Content == "run the daily report" {
			sawHistUser = true
		}
		if m.Role == "assistant" && m.Content == "the daily report is complete" {
			sawHistAssistant = true
		}
	}
	if !sawHistUser || !sawHistAssistant {
		t.Errorf("scheduler history not injected into prompt; messages=%+v", msgs)
	}
	last := msgs[len(msgs)-1]
	if last.Role != "user" || last.Content != "what did you just do?" {
		t.Errorf("last message = %+v, want current user turn", last)
	}
}

func TestHandleTask_SchedulerHistoryReseedsEachFreshSession(t *testing.T) {
	agent, mock, store := newTestAgent(
		&llm.ChatResponse{Content: "first reply"},
		&llm.ChatResponse{Content: "second reply"},
	)
	agent.cfg.InjectCurrentTime = new(bool)
	props := a2a.UserProperties{ClientID: "user-1", UserID: "user-1"}

	msg1 := a2a.Message{Kind: "message", MessageID: "m1", Role: a2a.RoleUser,
		Parts: []a2a.Part{a2a.NewTextPart("q1")}, ContextID: "ctx-A"}
	msg1.Metadata = schedulerHistoryMeta([2]string{"user", "prior one"})
	if _, err := agent.HandleTask(context.Background(), "task-A", msg1, props); err != nil {
		t.Fatal(err)
	}

	msg2 := a2a.Message{Kind: "message", MessageID: "m2", Role: a2a.RoleUser,
		Parts: []a2a.Part{a2a.NewTextPart("q2")}, ContextID: "ctx-B"}
	msg2.Metadata = schedulerHistoryMeta(
		[2]string{"user", "prior one"},
		[2]string{"assistant", "prior reply"},
		[2]string{"user", "prior two"},
	)
	if _, err := agent.HandleTask(context.Background(), "task-B", msg2, props); err != nil {
		t.Fatal(err)
	}
	_ = store

	calls := mock.getCalls()
	if len(calls) != 2 {
		t.Fatalf("expected 2 LLM calls, got %d", len(calls))
	}
	countContent := func(msgs []llm.ChatMessage, content string) int {
		n := 0
		for _, m := range msgs {
			if m.Content == content {
				n++
			}
		}
		return n
	}
	if c := countContent(calls[1].Messages, "prior two"); c != 1 {
		t.Errorf("ctx-B prompt has %d copies of 'prior two', want exactly 1", c)
	}
	if c := countContent(calls[1].Messages, "prior reply"); c != 1 {
		t.Errorf("ctx-B prompt has %d copies of 'prior reply', want exactly 1", c)
	}
	last := calls[1].Messages[len(calls[1].Messages)-1]
	if last.Role != "user" || last.Content != "q2" {
		t.Errorf("ctx-B last message = %+v, want current user turn q2", last)
	}
}

func TestHandleTask_SchedulerHistoryIgnoredWhenSessionHasMessages(t *testing.T) {
	agent, mock, _ := newTestAgent(
		&llm.ChatResponse{Content: "first reply"},
		&llm.ChatResponse{Content: "second reply"},
	)

	props := a2a.UserProperties{ClientID: "user-1", UserID: "user-1"}

	if _, err := agent.HandleTask(context.Background(), "task-001",
		newUserMessage("hello"), props); err != nil {
		t.Fatal(err)
	}

	msg2 := newUserMessage("follow up")
	msg2.MessageID = "msg-user-2"
	msg2.Metadata = schedulerHistoryMeta([2]string{"user", "should not appear"})
	if _, err := agent.HandleTask(context.Background(), "task-002", msg2, props); err != nil {
		t.Fatal(err)
	}

	calls := mock.getCalls()
	if len(calls) != 2 {
		t.Fatalf("expected 2 LLM calls, got %d", len(calls))
	}
	for _, m := range calls[1].Messages {
		if m.Content == "should not appear" {
			t.Errorf("scheduler history leaked into non-empty session; messages=%+v", calls[1].Messages)
		}
	}
}

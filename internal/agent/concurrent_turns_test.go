// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"log/slog"
	"maps"
	"slices"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
)

func userRow(task, text string) session.Message {
	return session.Message{Role: "user", Content: text, TaskID: task}
}

func answerRow(task, text string) session.Message {
	return session.Message{Role: "assistant", Content: text, TaskID: task}
}

func callRow(task, callID string) session.Message {
	return session.Message{Role: "assistant", TaskID: task, ToolCalls: []session.ToolCall{{ID: callID, Name: "peer_worker", Arguments: "{}"}}}
}

func resultRow(task, callID, text string) session.Message {
	return session.Message{Role: "tool", Content: text, TaskID: task, ToolCallResult: session.ToolCallResult{ToolCallID: callID}}
}

func repairedHistory(rows []session.Message, turns historyTurns) []llm.ChatMessage {
	agent, _, _ := newTestAgent()
	sess := &session.Session{SessionID: "s", Messages: rows}
	msgs, offset := agent.buildMessagesForTask(context.Background(), nil, sess, turns, "now", gatewayContext{})
	msgs, offset = repairDanglingToolCalls(msgs, offset, slog.Default(), msgs[offset:])
	msgs, offset = repairOrphanUserMessages(msgs, offset, slog.Default())
	return msgs[1:offset]
}

func contents(msgs []llm.ChatMessage) []string {
	out := make([]string, 0, len(msgs))
	for _, m := range msgs {
		switch {
		case len(m.ToolCalls) > 0:
			out = append(out, "call:"+m.ToolCalls[0].ID)
		case m.Role == "tool":
			out = append(out, "result:"+m.ToolCallID+"="+m.Content)
		default:
			out = append(out, m.Role+":"+m.Content)
		}
	}
	return out
}

func TestArrangeSessionTurns_FinishedSiblingKeepsItsRealResult(t *testing.T) {
	rows := []session.Message{
		userRow("a", "delegate W01"),
		callRow("a", "call-1"),
		userRow("b", "what is 2+2"),
		answerRow("b", "4"),
		resultRow("a", "call-1", "essay"),
		answerRow("a", "W01 done"),
	}
	got := contents(repairedHistory(rows, historyTurns{currentTaskID: "c"}))
	want := []string{
		"user:delegate W01", "call:call-1", "result:call-1=" + stubbedToolResult, "assistant:W01 done",
		"user:what is 2+2", "assistant:4",
	}
	if !slices.Equal(got, want) {
		t.Errorf("history =\n  %v\nwant\n  %v", got, want)
	}
}

func TestArrangeSessionTurns_LiveSiblingShowsAsInProgress(t *testing.T) {
	rows := []session.Message{
		userRow("a", "delegate W01"),
		callRow("a", "call-1"),
	}
	got := contents(repairedHistory(rows, historyTurns{currentTaskID: "b", liveSiblings: map[string]bool{"a": true}}))
	want := []string{"user:delegate W01", "assistant:" + siblingInProgressContent}
	if !slices.Equal(got, want) {
		t.Errorf("history =\n  %v\nwant\n  %v", got, want)
	}
}

func TestArrangeSessionTurns_DeadSiblingIsStillRepaired(t *testing.T) {
	rows := []session.Message{
		userRow("a", "delegate W01"),
		callRow("a", "call-1"),
		userRow("z", "crashed before answering"),
	}
	got := contents(repairedHistory(rows, historyTurns{currentTaskID: "b"}))
	want := []string{
		"user:delegate W01", "call:call-1", "result:call-1=" + danglingToolResultContent,
		"user:crashed before answering", "assistant:" + cancellationMarkerContent,
	}
	if !slices.Equal(got, want) {
		t.Errorf("history =\n  %v\nwant\n  %v", got, want)
	}
}

func TestArrangeSessionTurns_OwnEarlierAttemptIsNotCollapsed(t *testing.T) {
	rows := []session.Message{userRow("a", "first attempt")}
	got := contents(repairedHistory(rows, historyTurns{currentTaskID: "a", liveSiblings: map[string]bool{"a": true}}))
	want := []string{"user:first attempt", "assistant:" + cancellationMarkerContent}
	if !slices.Equal(got, want) {
		t.Errorf("history =\n  %v\nwant\n  %v", got, want)
	}
}

func TestArrangeSessionTurns_UnattributedRowsKeepTheirPlace(t *testing.T) {
	rows := []session.Message{
		{Role: "user", Content: "old q"},
		{Role: "assistant", Content: "old a"},
		userRow("a", "new q"),
		answerRow("a", "new a"),
	}
	got := arrangeSessionTurns(rows, historyTurns{currentTaskID: "b"})
	if !slices.EqualFunc(got, rows, func(x, y session.Message) bool { return x.Content == y.Content }) {
		t.Errorf("order changed: %+v", got)
	}
}

func TestOpenSiblingTasks(t *testing.T) {
	rows := []session.Message{
		userRow("asking", "q"),
		userRow("calling", "q"), callRow("calling", "c1"),
		userRow("resulted", "q"), callRow("resulted", "c2"), resultRow("resulted", "c2", "r"),
		userRow("done", "q"), answerRow("done", "a"),
		userRow("me", "q"),
		{Role: "user", Content: "unattributed"},
	}
	got := slices.Sorted(maps.Keys(openSiblingTasks(rows, "me")))
	want := []string{"asking", "calling", "resulted"}
	if !slices.Equal(got, want) {
		t.Errorf("open siblings = %v, want %v", got, want)
	}
}

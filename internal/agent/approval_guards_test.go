// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/taxonomy"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool/hilsurface"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool/remote"
)

func shortAskTool(timeout time.Duration) stubExecFn {
	return func(ctx context.Context, _ map[string]any, toolCtx *tool.Context) (*tool.Result, error) {
		toolCtx.PublishUserInput(ctx, askRequestID, "ask_user_question", json.RawMessage(`{}`), timeout)
		return tool.Pending("Waiting for the user's answer", tool.WithData(map[string]any{
			"pending_kind":   hilsurface.PendingKindAsk,
			"hil_request_id": askRequestID,
			"hil_timeout_ns": float64(timeout),
			"questions":      []any{map[string]any{"question": "Which region?"}},
		}), tool.WithTrustedPending(tool.TrustedPendingHILAsk)), nil
	}
}

func toolMessageFor(calls []llm.ChatRequest, toolCallID string) (llm.ChatMessage, bool) {
	for _, req := range calls {
		for _, msg := range req.Messages {
			if msg.Role == "tool" && msg.ToolCallID == toolCallID {
				return msg, true
			}
		}
	}
	return llm.ChatMessage{}, false
}

func TestQuestionExpiresWhilePaused(t *testing.T) {
	agent, mock, brk := replicaOf(t, session.NewMemoryStore(), "owner")
	if err := agent.Tools().Register(&testBuiltinTool{name: "ask_like", description: "stub", execFn: shortAskTool(300 * time.Millisecond)}); err != nil {
		t.Fatalf("register ask_like: %v", err)
	}
	mock.responses = []*llm.ChatResponse{singleToolCallResponse("call-ask", "ask_like"), {Content: "No answer came."}}
	user := concurrentUser
	user.A2AStatusTopic = "test-ns/a2a/v1/gateway/status/gw/task-ask-expiry"
	user.ReplyTo = resumedReplyTopic

	if resp, err := agent.HandleTask(t.Context(), "task-ask-expiry", concUserMessage("make a report", idgen.New()), user); err != nil || resp != nil {
		t.Fatalf("the task should pause on the question: resp=%v err=%v", resp, err)
	}

	if !waitUntil(func() bool { return replyOnTopic(brk, resumedReplyTopic) != nil }) {
		t.Fatal("the unanswered question never timed out and resumed its task")
	}
	msg, found := toolMessageFor(mock.getCalls(), "call-ask")
	if !found {
		t.Fatal("the resumed turn carried no tool message for the question")
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(msg.Content), &got); err != nil {
		t.Fatalf("decode tool message %q: %v", msg.Content, err)
	}
	want := map[string]any{
		"status":     "timed_out",
		"message":    "User did not respond within 0 minutes.",
		"error_code": tool.DispatchErrorCode(taxonomy.DispatchClassUserInputUnanswered),
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("question result (-want +got):\n%s", diff)
	}
}

func TestRemoteToolWaitWindowIsTheWindowTheCallDeclared(t *testing.T) {
	cases := map[string]struct {
		data map[string]any
		want time.Duration
	}{
		"declared by the executor in memory": {data: map[string]any{"timeout_ms": int64(7000)}, want: 7 * time.Second},
		"declared after a JSON round trip":   {data: map[string]any{"timeout_ms": float64(90000)}, want: 90 * time.Second},
		"not declared":                       {data: map[string]any{}, want: remote.DefaultWaitWindow},
		"declared as zero":                   {data: map[string]any{"timeout_ms": int64(0)}, want: remote.DefaultWaitWindow},
		"declared as negative":               {data: map[string]any{"timeout_ms": int64(-5)}, want: remote.DefaultWaitWindow},
		"declared as a string":               {data: map[string]any{"timeout_ms": "7000"}, want: remote.DefaultWaitWindow},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := remoteToolWaitWindow(tool.Pending("waiting", tool.WithData(tc.data))); got != tc.want {
				t.Errorf("window = %v, want %v", got, tc.want)
			}
		})
	}
}

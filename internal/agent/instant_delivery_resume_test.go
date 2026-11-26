// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

const (
	peerAnswersAtOnce = "instant"
	peerAnswersLater  = "later"
	unreachablePeer   = "unreachable_peer"
)

type peerCall struct {
	id   string
	when string
}

func peerCalls(calls ...peerCall) *llm.ChatResponse {
	toolCalls := make([]llm.ToolCall, 0, len(calls))
	for _, c := range calls {
		toolCalls = append(toolCalls, llm.ToolCall{ID: c.id, Name: unreachablePeer, Arguments: `{"when":"` + c.when + `"}`})
	}
	return &llm.ChatResponse{FinishReason: "tool_calls", ToolCalls: toolCalls}
}

var peerIsDown = &llm.ChatResponse{Content: "The peer is down.", FinishReason: "stop"}

type instantPeerAgent struct {
	agent  *Agent
	llm    *mockLLM
	broker *mockBroker
	later  chan func(*tool.Result)
}

func newInstantPeerAgent(t *testing.T, name string, store session.Store, responses ...*llm.ChatResponse) *instantPeerAgent {
	t.Helper()
	const ns = "ns"
	h := &instantPeerAgent{
		agent:  New(AgentConfig{Name: name, Namespace: ns, MaxLLMCalls: 5}),
		llm:    newMockLLM(responses...),
		broker: newMockBroker(),
		later:  make(chan func(*tool.Result), 4),
	}
	svc := newMockServices(h.llm, store)
	svc.brk = h.broker
	svc.arts = &artifact.ScopedService{Inner: memoryArtifactStore(), Scope: "namespace", Namespace: ns}
	if err := h.agent.Init(t.Context(), nil, svc); err != nil {
		t.Fatalf("Init: %v", err)
	}
	h.agent.logger = discardLogger()
	h.agent.runCtx = t.Context()

	if err := h.agent.Tools().Register(&testBuiltinTool{
		name:        unreachablePeer,
		description: "peer whose connection is refused",
		execFn: func(_ context.Context, args map[string]any, _ *tool.Context) (*tool.Result, error) {
			r := tool.Pending("waiting for peer", tool.WithData(map[string]any{"sub_task_id": "sub-peer"}))
			r.OnDelivery = func(deliver func(*tool.Result)) {
				if args["when"] == peerAnswersLater {
					h.later <- deliver
					return
				}
				deliver(tool.Error("proxy error: connection refused"))
			}
			return r, nil
		},
	}); err != nil {
		t.Fatalf("register tool: %v", err)
	}
	return h
}

func repliesOn(brk *mockBroker, topic string) int {
	n := 0
	for _, m := range brk.getPublished() {
		if m.topic == topic {
			n++
		}
	}
	return n
}

func waitFor[T any](t *testing.T, ch <-chan T, what string) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
		var zero T
		return zero
	}
}

func TestAPeerThatFailsBeforeThePauseIsPublishedStillResumesTheTask(t *testing.T) {
	for _, tc := range []struct {
		name         string
		responses    []*llm.ChatResponse
		laterReplies int
	}{
		{
			name:      "pause in the first turn",
			responses: []*llm.ChatResponse{peerCalls(peerCall{"call-1", peerAnswersAtOnce}), peerIsDown},
		},
		{
			name: "pause in a resumed turn",
			responses: []*llm.ChatResponse{
				peerCalls(peerCall{"call-1", peerAnswersAtOnce}),
				peerCalls(peerCall{"call-2", peerAnswersAtOnce}),
				peerIsDown,
			},
		},
		{
			name: "the first of two calls fails at once, the second later",
			responses: []*llm.ChatResponse{
				peerCalls(peerCall{"call-1", peerAnswersAtOnce}, peerCall{"call-2", peerAnswersLater}),
				peerIsDown,
			},
			laterReplies: 1,
		},
		{
			name: "the second of two calls fails at once, the first later",
			responses: []*llm.ChatResponse{
				peerCalls(peerCall{"call-1", peerAnswersLater}, peerCall{"call-2", peerAnswersAtOnce}),
				peerIsDown,
			},
			laterReplies: 1,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			taskID := "task-" + tc.name
			h := newInstantPeerAgent(t, "instant-peer-agent", newMemSessionStore(), tc.responses...)

			submitTaskMessage(t, h.agent, "ns", "instant-peer-agent", taskID, "ctx-"+taskID)
			for range tc.laterReplies {
				deliver := waitFor(t, h.later, "the later peer call to be armed")
				waitForPausedTask(t, h.agent, taskID)
				deliver(tool.Error("proxy error: connection refused"))
			}

			if got := waitForReplyTaskState(t, h.broker, "reply/"+taskID); got != a2a.TaskStateCompleted {
				t.Fatalf("final task state = %q, want %q", got, a2a.TaskStateCompleted)
			}
			if got := repliesOn(h.broker, "reply/"+taskID); got != 1 {
				t.Errorf("replies published = %d, want 1", got)
			}
			if got := len(h.llm.getCalls()); got != len(tc.responses) {
				t.Errorf("LLM calls = %d, want %d", got, len(tc.responses))
			}
		})
	}
}

func TestAResumeRaisedInsideTheFirstTurnWaitsForThatTurnToEnd(t *testing.T) {
	h := newInstantPeerAgent(t, "claim-agent", newMemSessionStore(), peerCalls(peerCall{"call-1", peerAnswersAtOnce}))
	heldWhenRaised := make(chan bool, 4)
	useResumeHook(t, func(_ *Agent, state *TaskExecutionState) { heldWhenRaised <- state.resuming.Load() })

	submitTaskMessage(t, h.agent, "ns", "claim-agent", "task-claim", "ctx-claim")

	if !waitFor(t, heldWhenRaised, "the resume") {
		t.Fatal("the resume was raised while the first turn held no claim, so it could start before that turn's final session flush")
	}
}

type cancelOnSaveStore struct {
	*memSessionStore
	onSave func()
}

func (s *cancelOnSaveStore) Save(ctx context.Context, sess *session.Session) error {
	s.onSave()
	return s.memSessionStore.Save(ctx, sess)
}

func (s *cancelOnSaveStore) AppendMessages(ctx context.Context, appName, userID, sessionID string, msgs []session.Message) error {
	s.onSave()
	return s.memSessionStore.AppendMessages(ctx, appName, userID, sessionID, msgs)
}

func TestACancelThatLandsAfterTheFirstTurnPausesStillTearsTheTaskDown(t *testing.T) {
	const taskID = "task-cancel-tail"
	store := &cancelOnSaveStore{memSessionStore: newMemSessionStore(), onSave: func() {}}
	h := newInstantPeerAgent(t, "cancel-tail-agent", store, peerCalls(peerCall{"call-1", peerAnswersLater}))
	var cancelSent atomic.Bool
	store.onSave = func() {
		if h.agent.pausedTasks.Load(taskID) == nil || !cancelSent.CompareAndSwap(false, true) {
			return
		}
		h.agent.handleCancelRequest(&a2a.Request{ID: "cancel-1", Method: "tasks/cancel", Params: map[string]any{"id": taskID}})
	}

	submitTaskMessage(t, h.agent, "ns", "cancel-tail-agent", taskID, "ctx-"+taskID)

	if got := waitForReplyTaskState(t, h.broker, "reply/"+taskID); got != a2a.TaskStateCancelled {
		t.Fatalf("final task state = %q, want %q", got, a2a.TaskStateCancelled)
	}
	if !cancelSent.Load() {
		t.Fatal("the cancel never landed after the pause, so this test proves nothing")
	}
	if h.agent.pausedTasks.Load(taskID) != nil {
		t.Error("the cancelled task is still held as paused")
	}
}

func TestEndingATurnActsOnWhatArrivedWhileItHeldTheTask(t *testing.T) {
	for _, tc := range []struct {
		name            string
		resumeRequested bool
		pausedAgain     bool
		replacedUnderID bool
		cancelled       bool
		handoffStarted  bool
		wantResumed     bool
		wantPaused      bool
		wantClaimHeld   bool
	}{
		{name: "a resume arrived and every result is in", resumeRequested: true, wantResumed: true, wantPaused: true},
		{name: "no resume arrived", wantPaused: true},
		{name: "a resume arrived but the turn paused again on new calls", resumeRequested: true, pausedAgain: true, wantPaused: true},
		{name: "a resume arrived but another state now holds the task id", resumeRequested: true, replacedUnderID: true},
		{name: "a cancel arrived", resumeRequested: true, cancelled: true},
		{name: "the shutdown handoff began", resumeRequested: true, handoffStarted: true, wantClaimHeld: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			agent, _, _ := newTestAgent()
			agent.logger = discardLogger()
			if tc.handoffStarted {
				enableDurableResume(t, agent)
				agent.handingOffPausedTasks.Store(true)
			}
			var resumed atomic.Bool
			useResumeHook(t, func(_ *Agent, _ *TaskExecutionState) { resumed.Store(true) })
			state := newTestTaskState(agent)
			agent.pausedTasks.Store(state)
			release, claimed := markResuming(state)
			if !claimed {
				t.Fatal("could not claim a fresh task")
			}
			state.resumeRequested.Store(tc.resumeRequested)
			state.Cancelled.Store(tc.cancelled)
			if tc.pausedAgain {
				state.TotalPending = 1
			}
			if tc.replacedUnderID {
				agent.pausedTasks.Store(newTestTaskState(agent))
			}

			agent.endResumeClaim(state, release)

			if got := resumed.Load(); got != tc.wantResumed {
				t.Errorf("resumed = %v, want %v", got, tc.wantResumed)
			}
			if got := agent.pausedTasks.Load(state.TaskID) == state; got != tc.wantPaused {
				t.Errorf("still paused = %v, want %v", got, tc.wantPaused)
			}
			if got := state.resuming.Load(); got != tc.wantClaimHeld {
				t.Errorf("claim held = %v, want %v", got, tc.wantClaimHeld)
			}
		})
	}
}

func TestHandleTaskToCompletionAnswersAfterAnInstantPeerFailure(t *testing.T) {
	h := newInstantPeerAgent(t, "to-completion-agent", newMemSessionStore(),
		peerCalls(peerCall{"call-1", peerAnswersAtOnce}), peerIsDown)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	resp, err := h.agent.HandleTaskToCompletion(ctx, "task-to-completion", newUserMessage("go"),
		a2a.UserProperties{ClientID: "user-1", UserID: "user-1"})

	if err != nil {
		t.Fatalf("HandleTaskToCompletion: %v", err)
	}
	if resp == nil || resp.Error != nil {
		t.Fatalf("response = %+v, want a successful answer", resp)
	}
	var result struct {
		Status a2a.TaskStatus `json:"status"`
	}
	raw, err := json.Marshal(resp.Result)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if result.Status.State != a2a.TaskStateCompleted {
		t.Errorf("task state = %q, want %q", result.Status.State, a2a.TaskStateCompleted)
	}
}

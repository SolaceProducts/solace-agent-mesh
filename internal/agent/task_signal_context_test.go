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
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

const (
	statusTopic  = "test-ns/a2a/v1/gateway/status/gw/task-status"
	promptUserID = "user-1"
)

type cancelOrder int

const (
	notCancelled cancelOrder = iota
	cancelledDuringTurn
	cancelledAfterTurnEnded
)

func cancelTask(t *testing.T, replica *Agent, state *TaskExecutionState, endTurn context.CancelFunc, stopTask stopTaskFunc, order cancelOrder) {
	t.Helper()
	switch order {
	case notCancelled:
		endTurn()
	case cancelledDuringTurn:
		stopTask()
	case cancelledAfterTurnEnded:
		endTurn()
		replica.cancelPausedTask(state, cancelFromElsewhere)
	}
}

type contextCheckingBroker struct {
	*mockBroker
	acceptsCancelledContext  bool
	publishedWithoutDeadline atomic.Int32
}

func (b *contextCheckingBroker) Publish(ctx context.Context, topic string, msg *broker.Message) error {
	if err := ctx.Err(); err != nil && !b.acceptsCancelledContext {
		return err
	}
	if _, bounded := ctx.Deadline(); !bounded {
		b.publishedWithoutDeadline.Add(1)
	}
	return b.mockBroker.Publish(ctx, topic, msg)
}

func (b *contextCheckingBroker) PublishGuaranteed(ctx context.Context, topic string, msg *broker.Message) error {
	return b.Publish(ctx, topic, msg)
}

type publishedEvent struct {
	State a2a.TaskState
	Data  map[string]any
}

func dataOf(t *testing.T, v any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal %T: %v", v, err)
	}
	var data map[string]any
	if err := json.Unmarshal(raw, &data); err != nil {
		t.Fatalf("unmarshal %T: %v", v, err)
	}
	return data
}

func statusEventOf(t *testing.T, msg publishedMsg) publishedEvent {
	t.Helper()
	var resp struct {
		Result a2a.TaskStatusUpdateEvent `json:"result"`
	}
	if err := json.Unmarshal(msg.payload, &resp); err != nil {
		t.Fatalf("unmarshal status update: %v", err)
	}
	message := resp.Result.Status.Message
	if message == nil || len(message.Parts) != 1 {
		t.Fatalf("status update parts = %+v, want one data part", message)
	}
	return publishedEvent{State: resp.Result.Status.State, Data: dataOf(t, message.Parts[0].Data)}
}

func publishedEvents(t *testing.T, brk *mockBroker, topic string) []publishedEvent {
	t.Helper()
	var events []publishedEvent
	for _, m := range brk.getPublished() {
		if m.topic == topic {
			events = append(events, statusEventOf(t, m))
		}
	}
	return events
}

func taskPublisher(replica *Agent, brk broker.Broker, state *TaskExecutionState) *StatusPublisher {
	return NewStatusPublisher(brk, StatusPublisherConfig{
		StatusTopic: statusTopic,
		TaskID:      state.TaskID,
		AgentName:   "test-agent",
		SeqGen:      replica.seqGen,
	}, replica.logger)
}

type toolHook struct {
	name     string
	send     func(*tool.Context) error
	event    publishedEvent
	dropKind string
}

func toolHooks(t *testing.T) []toolHook {
	rag := a2a.RAGInfoUpdateData{Type: "rag_info_update", Query: "q"}
	progress := a2a.AgentProgressUpdateData{Type: string(a2a.SignalAgentProgressUpdate), StatusText: "still working"}
	signIn := a2a.AuthenticationRequiredData{Type: "authentication_required", AuthURI: "https://idp.example.com/auth", ToolDisplayName: "Drive"}
	return []toolHook{
		{
			name:     "a result signal",
			send:     func(toolCtx *tool.Context) error { return toolCtx.SendSignal(rag) },
			event:    publishedEvent{State: a2a.TaskStateWorking, Data: dataOf(t, rag)},
			dropKind: "signal",
		},
		{
			name:     "a status update",
			send:     func(toolCtx *tool.Context) error { return toolCtx.SendStatus(progress.StatusText) },
			event:    publishedEvent{State: a2a.TaskStateWorking, Data: dataOf(t, progress)},
			dropKind: "status",
		},
		{
			name:     "a sign-in prompt",
			send:     func(toolCtx *tool.Context) error { return toolCtx.SendSignal(&signIn) },
			event:    publishedEvent{State: a2a.TaskStateInputNeeded, Data: dataOf(t, signIn)},
			dropKind: "signal",
		},
	}
}

func TestToolPublishesAfterTheTurnEndedOnlyWhenTheTaskIsNotCancelled(t *testing.T) {
	tests := []struct {
		name                    string
		pauseRemoteTools        bool
		cancel                  cancelOrder
		acceptsCancelledContext bool
		wantPublished           int
		wantDrops               int
		wantNextSequence        uint64
	}{
		{name: "after the turn ended it reaches the client when calls can pause", pauseRemoteTools: true, wantPublished: 1, wantNextSequence: 2},
		{name: "after the user cancelled it is dropped when calls can pause", pauseRemoteTools: true, cancel: cancelledDuringTurn, wantDrops: 1, wantNextSequence: 1},
		{name: "after the user cancelled it is dropped even by a broker that accepts a cancelled context", pauseRemoteTools: true, cancel: cancelledDuringTurn, acceptsCancelledContext: true, wantDrops: 1, wantNextSequence: 1},
		{name: "after the user cancelled a paused task it is dropped when calls can pause", pauseRemoteTools: true, cancel: cancelledAfterTurnEnded, wantDrops: 1, wantNextSequence: 1},
		{name: "after the user cancelled a paused task it is dropped even by a broker that accepts a cancelled context", pauseRemoteTools: true, cancel: cancelledAfterTurnEnded, acceptsCancelledContext: true, wantDrops: 1, wantNextSequence: 1},
		{name: "after the turn ended it is refused by the broker when calls cannot pause", wantNextSequence: 2},
		{name: "after the user cancelled it is dropped when calls cannot pause", cancel: cancelledDuringTurn, wantNextSequence: 1},
	}
	for _, hook := range toolHooks(t) {
		for _, tt := range tests {
			t.Run(hook.name+" "+tt.name, func(t *testing.T) {
				var logs *syncLogBuffer
				replica, _, _ := replicaOf(t, session.NewMemoryStore(), "replica", func(a *Agent) { logs = captureAgentLogs(a) })
				brk := &contextCheckingBroker{mockBroker: newMockBroker(), acceptsCancelledContext: tt.acceptsCancelledContext}
				state := newTestTaskState(replica)
				turn, endTurn, stopTask := cancellableTaskContext(t.Context())
				defer endTurn()
				toolCtx := replica.buildToolCtxFromState(turn, state, taskPublisher(replica, brk, state))
				toolCtx.PauseRemoteTools = tt.pauseRemoteTools
				cancelTask(t, replica, state, endTurn, stopTask, tt.cancel)

				if err := hook.send(toolCtx); err != nil {
					t.Fatalf("%s: %v, want nil", hook.name, err)
				}

				want := slices.Repeat([]publishedEvent{hook.event}, tt.wantPublished)
				if diff := cmp.Diff(want, publishedEvents(t, brk.mockBroker, statusTopic), cmpopts.EquateEmpty()); diff != "" {
					t.Errorf("events published after the turn ended (-want +got):\n%s", diff)
				}
				if got := brk.publishedWithoutDeadline.Load(); got != 0 {
					t.Errorf("messages published without a deadline = %d, want 0", got)
				}
				if got := replica.seqGen.Next(state.TaskID); got != tt.wantNextSequence {
					t.Errorf("next event sequence of the task = %d, want %d", got, tt.wantNextSequence)
				}
				wantDrops := slices.Repeat([]map[string]any{{"level": "DEBUG", "taskID": state.TaskID, "kind": hook.dropKind}}, tt.wantDrops)
				var gotDrops []map[string]any
				for _, rec := range logs.records("tool publish dropped: the user cancelled the task") {
					gotDrops = append(gotDrops, map[string]any{"level": rec["level"], "taskID": rec["taskID"], "kind": rec["kind"]})
				}
				if diff := cmp.Diff(wantDrops, gotDrops, cmpopts.EquateEmpty()); diff != "" {
					t.Errorf("tool publish drop log lines (-want +got):\n%s", diff)
				}
			})
		}
	}
}

const (
	askQuestion      = "a question"
	approvalQuestion = "an approval request"
)

func promptReplica(t *testing.T) (*Agent, *contextCheckingBroker, *syncLogBuffer) {
	t.Helper()
	var logs *syncLogBuffer
	brk := &contextCheckingBroker{mockBroker: newMockBroker(), acceptsCancelledContext: true}
	registry := durableResumeRegistry(true)
	registry.Register(features.Definition{Key: "notify_service", Default: true})
	replica, _, _ := replicaOf(t, session.NewMemoryStore(), "replica", func(a *Agent) {
		svc := a.svc.(*mockServices)
		svc.brk = brk
		svc.featureRegistry = registry
		logs = captureAgentLogs(a)
	})
	gateApprovalTool(t, replica, &approvalRuns{}, time.Hour)
	brk.publishedWithoutDeadline.Store(0)
	return replica, brk, logs
}

func askForPerson(turn context.Context, t *testing.T, replica *Agent, brk *contextCheckingBroker, state *TaskExecutionState, kind string) {
	t.Helper()
	sp := taskPublisher(replica, brk, state)
	switch kind {
	case askQuestion:
		toolCtx := replica.buildToolCtxFromState(turn, state, sp)
		toolCtx.PublishUserInput(turn, askRequestID, "ask_user_question", json.RawMessage(`{}`), time.Hour)
	case approvalQuestion:
		replica.pauseForToolApproval(turn, sp, approvalTool, map[string]any{"to": "Acme"},
			HILToolConfig{RequireApproval: true, ShowArgs: true, Timeout: time.Hour}, &tool.Context{PauseResume: true, UserID: promptUserID})
	default:
		t.Fatalf("unknown prompt %q", kind)
	}
}

type promptSeen struct {
	Statuses []publishedEvent
	Notifies []a2a.NotifyRequest
}

func promptSeenBy(t *testing.T, brk *mockBroker, notifyTopic string) promptSeen {
	t.Helper()
	var seen promptSeen
	seen.Statuses = publishedEvents(t, brk, statusTopic)
	for _, m := range brk.getPublished() {
		if m.topic != notifyTopic {
			continue
		}
		var req a2a.NotifyRequest
		if err := json.Unmarshal(m.payload, &req); err != nil {
			t.Fatalf("unmarshal notify request: %v", err)
		}
		seen.Notifies = append(seen.Notifies, req)
	}
	return seen
}

func wantedPrompt(source string) promptSeen {
	return promptSeen{
		Statuses: []publishedEvent{{State: a2a.TaskStateInputNeeded, Data: map[string]any{"type": "user_input_request", "source": source}}},
		Notifies: []a2a.NotifyRequest{{
			RecipientUserID: promptUserID,
			Mode:            a2a.NotifyModeInput,
			SenderUserID:    promptUserID,
			SenderAgent:     "test-agent",
			Category:        a2a.CategoryHIL,
			Title:           "Your input is requested",
		}},
	}
}

func TestAPromptIsPublishedAfterTheTurnEndedOnlyWhenTheTaskIsNotCancelled(t *testing.T) {
	notifyTopic := a2a.NotifyRequestTopic("test-ns", promptUserID)
	tests := []struct {
		name              string
		kind              string
		cancel            cancelOrder
		want              promptSeen
		wantRequestPrefix string
		wantDrops         int
		wantDroppedIDSize int
		wantNextSequence  uint64
	}{
		{name: "a question after the turn ended reaches the person", kind: askQuestion, want: wantedPrompt("ask_user_question"), wantRequestPrefix: askRequestID, wantNextSequence: 2},
		{name: "an approval request after the turn ended reaches the person", kind: approvalQuestion, want: wantedPrompt("tool_approval"), wantRequestPrefix: "hil-approval-", wantNextSequence: 2},
		{name: "a question after the user cancelled is not published", kind: askQuestion, cancel: cancelledDuringTurn, wantDrops: 1, wantDroppedIDSize: len(askRequestID), wantNextSequence: 1},
		{name: "an approval request after the user cancelled is not published", kind: approvalQuestion, cancel: cancelledDuringTurn, wantDrops: 1, wantDroppedIDSize: len("hil-approval-" + idgen.New()), wantNextSequence: 1},
		{name: "a question after the user cancelled a paused task is not published", kind: askQuestion, cancel: cancelledAfterTurnEnded, wantDrops: 1, wantDroppedIDSize: len(askRequestID), wantNextSequence: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			replica, brk, logs := promptReplica(t)
			state := newTestTaskState(replica)
			state.UserProps.UserID = promptUserID
			turn, endTurn, stopTask := cancellableTaskContext(t.Context())
			defer endTurn()
			cancelTask(t, replica, state, endTurn, stopTask, tt.cancel)

			askForPerson(turn, t, replica, brk, state, tt.kind)

			got := promptSeenBy(t, brk.mockBroker, notifyTopic)
			ignored := cmp.Options{
				cmpopts.EquateEmpty(),
				cmpopts.IgnoreFields(a2a.NotifyRequest{}, "RequestID", "RequesterAddr", "Surface", "ExpiresAt", "Metadata"),
				cmpopts.IgnoreMapEntries(func(key string, _ any) bool {
					return key == "request_id" || key == "expires_at" || key == "surface"
				}),
			}
			if diff := cmp.Diff(tt.want, got, ignored); diff != "" {
				t.Errorf("prompt published after the turn ended (-want +got):\n%s", diff)
			}
			for _, req := range got.Notifies {
				if !strings.HasPrefix(req.RequestID, tt.wantRequestPrefix) {
					t.Errorf("request id = %q, want prefix %q", req.RequestID, tt.wantRequestPrefix)
				}
				if want := a2a.NotifyResponseTopic("test-ns", "test-agent", req.RequestID); req.RequesterAddr != want {
					t.Errorf("requester address = %q, want %q", req.RequesterAddr, want)
				}
				if len(got.Statuses) == 0 {
					t.Error("a notify request was published without a status event")
				} else if statusID := got.Statuses[0].Data["request_id"]; statusID != req.RequestID {
					t.Errorf("status request id = %v, want %q", statusID, req.RequestID)
				}
			}
			if got := brk.publishedWithoutDeadline.Load(); got != 0 {
				t.Errorf("messages published without a deadline = %d, want 0", got)
			}
			if got := replica.seqGen.Next(state.TaskID); got != tt.wantNextSequence {
				t.Errorf("next event sequence of the task = %d, want %d", got, tt.wantNextSequence)
			}
			drops := logs.records("prompt dropped: the user cancelled the task")
			if len(drops) != tt.wantDrops {
				t.Fatalf("prompt drop log lines = %d, want %d: %v", len(drops), tt.wantDrops, drops)
			}
			for _, rec := range drops {
				if rec["level"] != "DEBUG" || rec["taskID"] != state.TaskID {
					t.Errorf("prompt drop log line = %v, want a Debug line for task %q", rec, state.TaskID)
				}
				if rec["requestIDBytes"] != float64(tt.wantDroppedIDSize) {
					t.Errorf("prompt drop requestIDBytes = %v, want %d", rec["requestIDBytes"], tt.wantDroppedIDSize)
				}
			}
		})
	}
}

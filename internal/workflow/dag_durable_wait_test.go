// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
	wfstore "github.com/SolaceProducts/solace-agent-mesh/internal/workflow/store"
)

var timerDueAt = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

func newDurableWaitWorkflow(t *testing.T, outputMapping map[string]any, nodes ...*NodeConfig) (*WorkflowInstance, broker.Broker) {
	t.Helper()
	w, _ := newHILWorkflow(t, nodes...)
	w.cfg.OutputMapping = outputMapping
	w.cfg.FailFast = true
	w.durableWaits = true
	return w, w.svc.Broker()
}

func timerNode() *NodeConfig {
	return &NodeConfig{ID: "cool", Type: NodeTypeTimer, WaitDuration: time.Hour}
}

func waitNode(timeout time.Duration) *NodeConfig {
	return &NodeConfig{ID: "pay", Type: NodeTypeWait, Signal: "paid", Timeout: timeout}
}

func startParked(t *testing.T, w *WorkflowInstance, taskID string) string {
	t.Helper()
	var acks ackCounter
	req, msg := deliveredRequest(taskID, &acks, false)
	w.handleTaskRequest(t.Context(), *req, msg)
	w.wg.Wait()
	run, err := w.runStore().LiveRunForTask(t.Context(), w.cfg.Name, taskID)
	if err != nil || run == nil {
		t.Fatalf("live run of %s = %v, %v", taskID, run, err)
	}
	if run.Status != wfstore.RunParked || acks.acks() != 1 || executionFor(w, taskID) != nil {
		t.Fatalf("run = %s with %d request acks, resident %v; want parked, acknowledged once and out of memory",
			run.Status, acks.acks(), executionFor(w, taskID) != nil)
	}
	return run.ExecutionID
}

func makeWakesDue(t *testing.T, w *WorkflowInstance, executionID string) {
	t.Helper()
	makeWakesDueAt(t, w, executionID, timerDueAt)
}

func makeWakesDueAt(t *testing.T, w *WorkflowInstance, executionID string, at time.Time) {
	t.Helper()
	rewriteParkedState(t, w, executionID, func(s *WorkflowExecutionState) {
		for i := range s.Timers {
			if s.Timers[i].Kind == TimerKindWake {
				s.Timers[i].FiresAt = at
			}
		}
	})
	if _, err := w.pausedDB.ExecContext(t.Context(),
		`UPDATE workflow_checkpoint SET wake_at = ? WHERE execution_id = ?`, at.UnixMilli(), executionID); err != nil {
		t.Fatalf("make wake due: %v", err)
	}
}

func parkedStateOf(t *testing.T, w *WorkflowInstance, executionID string) *WorkflowExecutionState {
	t.Helper()
	state := &WorkflowExecutionState{}
	if err := json.Unmarshal([]byte(runRowOf(t, w, executionID).StateSnapshot), state); err != nil {
		t.Fatalf("decode parked state: %v", err)
	}
	return state
}

func deliverWake(t *testing.T, w *WorkflowInstance, executionID, messageID string) *ackCounter {
	t.Helper()
	return deliverWakeMessage(t, w, executionID, messageID, false)
}

func deliverWakeMessage(t *testing.T, w *WorkflowInstance, executionID, messageID string, redelivered bool) *ackCounter {
	t.Helper()
	var acks ackCounter
	msg := broker.NewDeliveredMessage("wake-"+messageID, a2a.WorkflowWakeTopic(w.cfg.Namespace, w.cfg.Name, executionID),
		[]byte(messageID), func() { acks.n.Add(1) }, func() {})
	msg.Redelivered = redelivered
	w.handleWakeMessage(t.Context(), msg)
	w.wg.Wait()
	return &acks
}

type durableFinal struct {
	State  a2a.TaskState
	Output map[string]any
}

func durableFinalTask(t *testing.T, brk broker.Broker, q broker.Queue) durableFinal {
	t.Helper()
	msg := receiveOn(t, brk, q)
	if msg == nil {
		t.Fatal("no final response")
	}
	var resp struct {
		Result struct {
			Status struct {
				State   a2a.TaskState `json:"state"`
				Message struct {
					Parts []struct {
						Kind string         `json:"kind"`
						Data map[string]any `json:"data"`
					} `json:"parts"`
				} `json:"message"`
			} `json:"status"`
		} `json:"result"`
	}
	if err := json.Unmarshal(msg.Payload, &resp); err != nil {
		t.Fatalf("not a final task response: %s", msg.Payload)
	}
	got := durableFinal{State: resp.Result.Status.State}
	for _, p := range resp.Result.Status.Message.Parts {
		if p.Kind == "data" {
			got.Output = p.Data
		}
	}
	return got
}

func TestTimerNode_ParksTheRunWithItsDueTime(t *testing.T) {
	w, _ := newDurableWaitWorkflow(t, map[string]any{"done": "yes"}, timerNode())
	before := time.Now()
	executionID := startParked(t, w, "task-timer-park")
	after := time.Now()

	run := runRowOf(t, w, executionID)
	if run.WakeAt == nil {
		t.Fatal("parked timer run has no wake_at")
	}
	wake := time.UnixMilli(*run.WakeAt)
	if wake.Before(before.Add(time.Hour).Truncate(time.Millisecond)) || wake.After(after.Add(time.Hour)) {
		t.Errorf("wake_at = %s, want one hour after the run started (%s .. %s)", wake, before.Add(time.Hour), after.Add(time.Hour))
	}
}

func TestTimerNode_AWakeCompletesTheTimerOnlyWhenItIsDue(t *testing.T) {
	for _, tc := range []struct {
		name       string
		due        bool
		wantStatus string
		wantFinal  durableFinal
	}{
		{
			name:       "a due timer completes and the run answers with its scheduled time",
			due:        true,
			wantStatus: wfstore.RunEnded,
			wantFinal: durableFinal{State: a2a.TaskStateCompleted, Output: map[string]any{
				"scheduled_for": "2026-01-02T03:04:05Z",
			}},
		},
		{
			name:       "an early wake is dropped and the run stays parked",
			due:        false,
			wantStatus: wfstore.RunParked,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w, brk := newDurableWaitWorkflow(t, map[string]any{"scheduled_for": "{{cool.output.scheduled_for}}"}, timerNode())
			replies := subscribed(t, brk, requestAckReplyTo)
			executionID := startParked(t, w, "task-timer-wake")
			if tc.due {
				makeWakesDue(t, w, executionID)
			}

			acks := deliverWake(t, w, executionID, "m1")

			if got := runRowOf(t, w, executionID).Status; got != tc.wantStatus {
				t.Errorf("run after the wake = %s, want %s", got, tc.wantStatus)
			}
			if acks.acks() != 1 {
				t.Errorf("wake acknowledged %d times, want 1", acks.acks())
			}
			if !tc.due {
				if msg := receiveOn(t, brk, replies); msg != nil {
					t.Errorf("an early wake answered the caller: %s", msg.Payload)
				}
				return
			}
			if diff := cmp.Diff(tc.wantFinal, durableFinalTask(t, brk, replies)); diff != "" {
				t.Errorf("final response mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestTimerNode_AWakeOnTheWorkflowQueueReachesTheWakeHandler(t *testing.T) {
	w, brk := newDurableWaitWorkflow(t, map[string]any{"done": "yes"}, timerNode())
	replies := subscribed(t, brk, requestAckReplyTo)
	executionID := startParked(t, w, "task-timer-routed")
	makeWakesDue(t, w, executionID)

	w.routeMessage(t.Context(), broker.NewDeliveredMessage("wake-routed",
		a2a.WorkflowWakeTopic(w.cfg.Namespace, w.cfg.Name, executionID), []byte("m1"), func() {}, func() {}))

	if got := durableFinalTask(t, brk, replies).State; got != a2a.TaskStateCompleted {
		t.Errorf("final state after a routed wake = %s, want completed", got)
	}
}

func TestTimerNode_AWakeRedeliveredAfterItsAWEDiedDispatchesTheNodeAfterTheTimerAgain(t *testing.T) {
	downstream := &NodeConfig{ID: "after", Type: "agent", AgentName: "afterAgent", DependsOn: []string{"cool"}}
	a, _ := newDurableWaitWorkflow(t, map[string]any{"done": "yes"}, timerNode(), downstream)
	a.checkpointRepo = wfstore.NewRunRepo(a.pausedDB)
	executionID := startParked(t, a, "task-timer-redelivered")
	makeWakesDue(t, a, executionID)

	b, _, brkB := newPeer(t, a)
	b.checkpointRepo = a.checkpointRepo
	b.durableWaits = true
	afterB := subscribed(t, brkB, a2a.AgentRequestTopic(b.cfg.Namespace, "afterAgent"))
	deliverWakeMessage(t, b, executionID, "m1", false)
	if receiveOn(t, brkB, afterB) == nil {
		t.Fatal("the AWE that took the wake did not dispatch the node after the timer")
	}

	c, _, brkC := newPeer(t, a)
	c.checkpointRepo = a.checkpointRepo
	c.durableWaits = true
	afterC := subscribed(t, brkC, a2a.AgentRequestTopic(c.cfg.Namespace, "afterAgent"))
	acks := deliverWakeMessage(t, c, executionID, "m1", true)

	if receiveOn(t, brkC, afterC) == nil {
		t.Error("the node dispatched after the timer was not dispatched again when the wake was redelivered after its AWE died")
	}
	if acks.acks() != 0 || c.residentByID(executionID) == nil {
		t.Errorf("redelivered wake acks=%d, run resident=%v; want the wake holding the run here", acks.acks(), c.residentByID(executionID) != nil)
	}
}

func TestSweepDueWakes_PublishesAWakeForEachDueParkedRunOnly(t *testing.T) {
	w, brk := newDurableWaitWorkflow(t, map[string]any{"done": "yes"}, timerNode())
	wakes := subscribed(t, brk, a2a.WorkflowWakeSubscription(w.cfg.Namespace, w.cfg.Name))
	due := startParked(t, w, "task-due")
	startParked(t, w, "task-not-due")
	makeWakesDue(t, w, due)

	w.sweepDueWakes(t.Context())

	msg := receiveOn(t, brk, wakes)
	if msg == nil || msg.Topic != a2a.WorkflowWakeTopic(w.cfg.Namespace, w.cfg.Name, due) {
		t.Fatalf("first wake = %v, want one for execution %s", msg, due)
	}
	if extra := receiveOn(t, brk, wakes); extra != nil {
		t.Errorf("sweepDueWakes also woke %s, whose timer is not due", extra.Topic)
	}
}

func TestTimerNode_ItsInProcessTimerPublishesTheWakeOfTheRunItParked(t *testing.T) {
	node := timerNode()
	node.WaitDuration = 300 * time.Millisecond
	w, brk := newDurableWaitWorkflow(t, map[string]any{"done": "yes"}, node)
	wakes := subscribed(t, brk, a2a.WorkflowWakeSubscription(w.cfg.Namespace, w.cfg.Name))

	executionID := startParked(t, w, "task-timer-in-process")

	msg := receiveOn(t, brk, wakes)
	if msg == nil || msg.Topic != a2a.WorkflowWakeTopic(w.cfg.Namespace, w.cfg.Name, executionID) {
		t.Fatalf("wake after the in-process timer fired = %v, want one for execution %s", msg, executionID)
	}
}

func TestWaitNode_ParksOnItsSignalAndASignalCompletesIt(t *testing.T) {
	const taskID = "task-wait-signal"
	w, brk := newDurableWaitWorkflow(t, map[string]any{"payer": "{{pay.output.payer}}", "status": "{{pay.output.status}}"}, waitNode(0))
	replies := subscribed(t, brk, requestAckReplyTo)
	startParked(t, w, taskID)

	pause := onlyPausedRow(t, w, taskID)
	if want := "signal:" + taskID + ":paid"; pause.RequestID != want || pause.Status != wfstore.StatusPaused {
		t.Fatalf("pause = %s (%s), want %s (paused)", pause.RequestID, pause.Status, want)
	}

	w.answer(t.Context(), pause.RequestID, "", "received", map[string]any{"payer": "bob", "status": "forged"})
	w.wg.Wait()

	want := durableFinal{State: a2a.TaskStateCompleted, Output: map[string]any{"payer": "bob", "status": "received"}}
	if diff := cmp.Diff(want, durableFinalTask(t, brk, replies)); diff != "" {
		t.Errorf("final response mismatch (-want +got):\n%s", diff)
	}
}

func TestWaitNode_ATimeoutWakePublishesATimedOutAnswerThatFailsTheNode(t *testing.T) {
	const taskID = "task-wait-timeout"
	w, brk := newDurableWaitWorkflow(t, map[string]any{"done": "yes"}, waitNode(time.Hour))
	replies := subscribed(t, brk, requestAckReplyTo)
	executionID := startParked(t, w, taskID)
	requestID := "signal:" + taskID + ":paid"
	answers := subscribed(t, brk, a2a.NotifyResponseTopic(w.cfg.Namespace, w.cfg.Name, requestID))
	makeWakesDue(t, w, executionID)

	deliverWake(t, w, executionID, "m1")
	timeout := receiveOn(t, brk, answers)
	if timeout == nil {
		t.Fatal("the timeout wake published no answer")
	}
	var answer a2a.UserInputResponseData
	if err := json.Unmarshal(timeout.Payload, &answer); err != nil || answer.RequestID != requestID || answer.Status != "timed_out" {
		t.Fatalf("timeout answer = %+v (err %v), want timed_out for %s", answer, err, requestID)
	}
	if got := runRowOf(t, w, executionID).Status; got != wfstore.RunParked {
		t.Fatalf("run after publishing its timeout = %s, want parked until the answer lands", got)
	}

	w.answer(t.Context(), requestID, "", "timed_out", nil)
	w.wg.Wait()

	if got := durableFinalTask(t, brk, replies).State; got != a2a.TaskStateFailed {
		t.Errorf("final state = %s, want failed", got)
	}
}

func TestWaitNode_ATimeoutIsPublishedUnderTheSignalTheRunParkedWithAfterARedeployRenamedIt(t *testing.T) {
	const taskID = "task-wait-renamed"
	const parkedRequestID = "signal:task-wait-renamed:paid"
	w, brk := newDurableWaitWorkflow(t, map[string]any{"done": "yes"}, waitNode(time.Hour))
	replies := subscribed(t, brk, requestAckReplyTo)
	executionID := startParked(t, w, taskID)
	answers := subscribed(t, brk, a2a.NotifyResponseTopic(w.cfg.Namespace, w.cfg.Name, parkedRequestID))
	w.dag.Nodes["pay"] = &NodeConfig{ID: "pay", Type: NodeTypeWait, Signal: "settled", Timeout: time.Hour}
	makeWakesDue(t, w, executionID)

	deliverWake(t, w, executionID, "m1")

	timeout := receiveOn(t, brk, answers)
	if timeout == nil {
		t.Fatalf("the timeout was not published under %s, the request id of the pause the run parked with", parkedRequestID)
	}
	var answer a2a.UserInputResponseData
	if err := json.Unmarshal(timeout.Payload, &answer); err != nil || answer.RequestID != parkedRequestID || answer.Status != "timed_out" {
		t.Fatalf("timeout answer = %+v (err %v), want timed_out for %s", answer, err, parkedRequestID)
	}

	w.answer(t.Context(), parkedRequestID, "", "timed_out", nil)
	w.wg.Wait()

	if got := durableFinalTask(t, brk, replies).State; got != a2a.TaskStateFailed {
		t.Errorf("final state = %s, want failed", got)
	}
	if diff := cmp.Diff(storedPause{Status: "timed_out"}, storedPauseOf(t, w, parkedRequestID)); diff != "" {
		t.Errorf("pause row mismatch (-want +got):\n%s", diff)
	}
	if got := runRowOf(t, w, executionID).Status; got != wfstore.RunEnded {
		t.Errorf("run after the timeout = %s, want %s", got, wfstore.RunEnded)
	}
}

func makeOnlyPayDue(t *testing.T, w *WorkflowInstance, executionID string) {
	t.Helper()
	rewriteParkedState(t, w, executionID, func(s *WorkflowExecutionState) {
		for j := range s.Timers {
			if s.Timers[j].NodeID == "pay" {
				s.Timers[j].FiresAt = timerDueAt
			}
		}
	})
	if _, err := w.pausedDB.ExecContext(t.Context(),
		`UPDATE workflow_checkpoint SET wake_at = ? WHERE execution_id = ?`, timerDueAt.UnixMilli(), executionID); err != nil {
		t.Fatalf("make the wake of pay due: %v", err)
	}
}

func publishedUnder(published *recordedPublishes, prefix string) []string {
	published.mu.Lock()
	defer published.mu.Unlock()
	var got []string
	for _, topic := range published.topics {
		if strings.HasPrefix(topic, prefix) {
			got = append(got, topic)
		}
	}
	return got
}

func TestWaitNode_AWakeForANodeThatWasATimerWhenTheRunParkedFailsTheRunBeforeTimingOutAnyOtherDueWait(t *testing.T) {
	const taskID = "task-timer-became-wait"
	ship := &NodeConfig{ID: "ship", Type: NodeTypeWait, Signal: "shipped", Timeout: 2 * time.Hour}
	w, inner := newDurableWaitWorkflow(t, map[string]any{"done": "yes"}, &NodeConfig{ID: "pay", Type: NodeTypeTimer, WaitDuration: time.Hour}, ship)
	published := &recordedPublishes{Broker: inner}
	w.svc = servicesOn{Services: w.svc, brk: published}
	replies := subscribed(t, inner, requestAckReplyTo)
	executionID := startParked(t, w, taskID)
	w.dag.Nodes["pay"] = &NodeConfig{ID: "pay", Type: NodeTypeWait, Signal: "paid", Timeout: time.Hour}
	makeWakesDue(t, w, executionID)
	rewriteParkedState(t, w, executionID, func(s *WorkflowExecutionState) {
		slices.SortFunc(s.Timers, func(a, b Timer) int { return strings.Compare(a.NodeID, b.NodeID) })
	})

	deliverWake(t, w, executionID, "m1")

	reply := receiveOn(t, inner, replies)
	if reply == nil {
		t.Fatal("the run did not end although its wait node has no signal pause to time out")
	}
	var resp struct {
		Result a2a.Task `json:"result"`
	}
	if err := json.Unmarshal(reply.Payload, &resp); err != nil {
		t.Fatalf("not a final task response: %s", reply.Payload)
	}
	var failure []string
	if msg := resp.Result.Status.Message; msg != nil {
		for _, part := range msg.Parts {
			failure = append(failure, part.Text)
		}
	}
	const wantFailure = `wait node "pay" was due with no open pause for signal "paid"`
	if resp.Result.Status.State != a2a.TaskStateFailed || !strings.Contains(strings.Join(failure, " "), wantFailure) {
		t.Errorf("final response = %s with message %q, want failed with %q", resp.Result.Status.State, failure, wantFailure)
	}
	if got := runRowOf(t, w, executionID).Status; got != wfstore.RunEnded {
		t.Errorf("run after the wake = %s, want %s", got, wfstore.RunEnded)
	}
	if got := publishedUnder(published, a2a.NotifyResponseTopicPrefix(w.cfg.Namespace, w.cfg.Name)); len(got) != 0 {
		t.Errorf("signal answers published for a wait node that never opened a signal pause: %v", got)
	}
}

func TestDurableWait_ARunThatFailsIntoItsExitHandlersDropsItsWakeTimers(t *testing.T) {
	const taskID = "task-fail-into-exit"
	charge := &NodeConfig{ID: "charge", Type: "agent", AgentName: "chargeAgent"}
	cleanup := &NodeConfig{ID: "cleanup", Type: "agent", AgentName: "cleanupAgent"}
	w, brk := newDurableWaitWorkflow(t, map[string]any{"done": "yes"}, waitNode(time.Hour), charge, cleanup)
	w.cfg.OnExit = &ExitHandler{Always: "cleanup"}
	w.dag.InnerNodes["cleanup"] = true
	cleanupRequests := subscribed(t, brk, a2a.AgentRequestTopic(w.cfg.Namespace, "cleanupAgent"))
	var acks ackCounter
	req, msg := deliveredRequest(taskID, &acks, false)
	w.handleTaskRequest(t.Context(), *req, msg)
	w.wg.Wait()
	exec := executionFor(w, taskID)
	if exec == nil {
		t.Fatal("the run waiting on its agent node is not resident")
	}

	deliverSubTaskResult(t, w, subTaskOf(t, exec, "charge"), a2a.TaskStateFailed)
	w.wg.Wait()

	if receiveOn(t, brk, cleanupRequests) == nil {
		t.Fatal("the exit handler was not dispatched after the agent node failed the run")
	}
	exec.Executor.mu.Lock()
	inExitHandler := exec.State.InExitHandler
	var wakeTimers []string
	for _, timer := range exec.State.Timers {
		if timer.Kind == TimerKindWake {
			wakeTimers = append(wakeTimers, timer.NodeID)
		}
	}
	exec.Executor.mu.Unlock()
	if !inExitHandler || len(wakeTimers) != 0 {
		t.Errorf("run in exit handler %v with wake timers %v; want its exit handler running and no wake timer left", inExitHandler, wakeTimers)
	}
}

func TestDurableWait_ATimerOrWaitReadyBesideANodeThatFailsTheRunIsNotStartedWhileExitHandlersRun(t *testing.T) {
	for _, tc := range []struct {
		name      string
		straggler *NodeConfig
	}{
		{name: "a timer", straggler: &NodeConfig{ID: "later", Type: NodeTypeTimer, WaitDuration: time.Hour}},
		{name: "a wait", straggler: &NodeConfig{ID: "later", Type: NodeTypeWait, Signal: "shipped", Timeout: time.Hour}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const taskID = "task-straggler-after-outcome"
			cleanup := &NodeConfig{ID: "cleanup", Type: "agent", AgentName: "cleanupAgent"}
			w, brk := newDurableWaitWorkflow(t, map[string]any{"done": "yes"}, waitNode(0), tc.straggler, cleanup)
			w.cfg.OnExit = &ExitHandler{Always: "cleanup"}
			w.dag.InnerNodes["cleanup"] = true
			cleanupRequests := subscribed(t, brk, a2a.AgentRequestTopic(w.cfg.Namespace, "cleanupAgent"))
			req, bmsg := authzTaskMessage(taskID, a2a.UserProperties{UserID: "system:eval", Trigger: a2a.TriggerEval, ReplyTo: requestAckReplyTo})
			msg := broker.NewDeliveredMessage("m-"+taskID, "", bmsg.Payload, func() {}, func() {})
			msg.UserProperties = bmsg.UserProperties

			w.handleTaskRequest(t.Context(), *req, msg)
			w.wg.Wait()

			if receiveOn(t, brk, cleanupRequests) == nil {
				t.Fatal("the exit handler was not dispatched after the wait node failed the evaluation run")
			}
			exec := executionFor(w, taskID)
			if exec == nil {
				t.Fatal("the run running its exit handler is not resident")
			}
			exec.Executor.mu.Lock()
			inExitHandler := exec.State.InExitHandler
			laterPending := exec.State.IsNodePending("later")
			var wakeTimers []string
			for _, timer := range exec.State.Timers {
				if timer.Kind == TimerKindWake {
					wakeTimers = append(wakeTimers, timer.NodeID)
				}
			}
			exec.Executor.mu.Unlock()
			if !inExitHandler || laterPending || len(wakeTimers) != 0 {
				t.Errorf("run in exit handler %v, later pending %v, wake timers %v; want its exit handler running, later not started and no wake timer",
					inExitHandler, laterPending, wakeTimers)
			}
		})
	}
}

func TestWaitNode_AWaitDueWithNoOpenPauseFailsOnlyItsNodeWhenFailFastIsOff(t *testing.T) {
	const taskID = "task-no-pause-no-fail-fast"
	ship := &NodeConfig{ID: "ship", Type: NodeTypeWait, Signal: "shipped", Timeout: 2 * time.Hour}
	w, _ := newDurableWaitWorkflow(t, map[string]any{"done": "yes"}, &NodeConfig{ID: "pay", Type: NodeTypeTimer, WaitDuration: time.Hour}, ship)
	w.cfg.FailFast = false
	executionID := startParked(t, w, taskID)
	w.dag.Nodes["pay"] = &NodeConfig{ID: "pay", Type: NodeTypeWait, Signal: "paid", Timeout: time.Hour}
	makeOnlyPayDue(t, w, executionID)

	deliverWake(t, w, executionID, "m1")

	if got := runRowOf(t, w, executionID).Status; got != wfstore.RunParked {
		t.Fatalf("run after the wake = %s, want parked on ship", got)
	}
	state := parkedStateOf(t, w, executionID)
	var failed []string
	for _, f := range state.FailedNodes {
		failed = append(failed, f.FailedNodeID)
	}
	if diff := cmp.Diff([]string{"pay"}, failed); diff != "" || !state.IsNodePending("ship") {
		t.Errorf("failed nodes mismatch (-want +got):\n%s\nship pending %v, want true", diff, state.IsNodePending("ship"))
	}
}

func TestWaitNode_ATimeoutIsPublishedOnlyForTheWaitNodeWhoseTimerIsDue(t *testing.T) {
	const taskID = "task-two-waits"
	ship := &NodeConfig{ID: "ship", Type: NodeTypeWait, Signal: "shipped", Timeout: 2 * time.Hour}
	w, inner := newDurableWaitWorkflow(t, map[string]any{"done": "yes"}, waitNode(time.Minute), ship)
	published := &recordedPublishes{Broker: inner}
	w.svc = servicesOn{Services: w.svc, brk: published}
	executionID := startParked(t, w, taskID)
	makeOnlyPayDue(t, w, executionID)

	deliverWake(t, w, executionID, "m1")

	want := []string{a2a.NotifyResponseTopic(w.cfg.Namespace, w.cfg.Name, "signal:"+taskID+":paid")}
	if diff := cmp.Diff(want, publishedUnder(published, a2a.NotifyResponseTopicPrefix(w.cfg.Namespace, w.cfg.Name))); diff != "" {
		t.Errorf("timeout answers published mismatch (-want +got):\n%s", diff)
	}
}

func TestWaitNode_ARunThatParksAgainAfterASignalCompletedItsWaitParksWithoutTheAnsweredPause(t *testing.T) {
	const taskID = "task-signal-then-timer"
	cool := timerNode()
	cool.DependsOn = []string{"pay"}
	w, _ := newDurableWaitWorkflow(t, map[string]any{"done": "yes"}, waitNode(0), cool)
	executionID := startParked(t, w, taskID)

	w.answer(t.Context(), "signal:"+taskID+":paid", "", "received", nil)
	w.wg.Wait()

	run := runRowOf(t, w, executionID)
	if run.Status != wfstore.RunParked || run.OpenPauses != "[]" || executionFor(w, taskID) != nil {
		t.Errorf("run after the signal = %s with open pauses %s, resident %v; want parked on the timer with no open pause and out of memory",
			run.Status, run.OpenPauses, executionFor(w, taskID) != nil)
	}
}

func TestWaitNode_AWakeThatRunsWhileAnAcceptedSignalIsBeingAppliedLeavesTheSignalToCompleteTheNode(t *testing.T) {
	const taskID = "task-wake-during-signal"
	requestID := "signal:" + taskID + ":paid"
	w, brk := newDurableWaitWorkflow(t, map[string]any{"payer": "{{pay.output.payer}}", "status": "{{pay.output.status}}"}, waitNode(time.Hour))
	replies := subscribed(t, brk, requestAckReplyTo)
	answers := subscribed(t, brk, a2a.NotifyResponseTopic(w.cfg.Namespace, w.cfg.Name, requestID))
	executionID := startParked(t, w, taskID)
	makeWakesDue(t, w, executionID)
	w.signalApplyHook = func(exec *WorkflowExecution) {
		if err := exec.Executor.Advance(t.Context(), &EventWake{}); err != nil {
			t.Errorf("wake between the claim of the signal and its apply: %v", err)
		}
	}

	w.answer(t.Context(), requestID, "", "received", map[string]any{"payer": "bob"})
	w.wg.Wait()

	timeout := receiveOn(t, brk, answers)
	if timeout == nil {
		t.Fatal("the wake that ran before the signal was applied published no timeout for the claimed pause")
	}
	want := durableFinal{State: a2a.TaskStateCompleted, Output: map[string]any{"payer": "bob", "status": "received"}}
	if diff := cmp.Diff(want, durableFinalTask(t, brk, replies)); diff != "" {
		t.Errorf("final response mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(storedPause{Status: "resumed"}, storedPauseOf(t, w, requestID)); diff != "" {
		t.Errorf("pause row mismatch (-want +got):\n%s", diff)
	}
	if got := runRowOf(t, w, executionID).Status; got != wfstore.RunEnded {
		t.Errorf("run after the signal = %s, want %s", got, wfstore.RunEnded)
	}
}

func TestWaitNode_ATimeoutStaysArmedUntilItsAnswerIsAppliedSoALostAnswerIsPublishedAgain(t *testing.T) {
	const taskID = "task-wait-timeout-rearmed"
	requestID := "signal:" + taskID + ":paid"
	w, brk := newDurableWaitWorkflow(t, map[string]any{"done": "yes"}, waitNode(time.Hour))
	replies := subscribed(t, brk, requestAckReplyTo)
	executionID := startParked(t, w, taskID)
	answers := subscribed(t, brk, a2a.NotifyResponseTopic(w.cfg.Namespace, w.cfg.Name, requestID))
	makeWakesDue(t, w, executionID)

	before := time.Now()
	deliverWake(t, w, executionID, "m1")
	after := time.Now()

	if receiveOn(t, brk, answers) == nil {
		t.Fatal("the timeout wake published no answer")
	}
	run := runRowOf(t, w, executionID)
	if run.Status != wfstore.RunParked || run.WakeAt == nil {
		t.Fatalf("run after publishing its timeout = %s with wake_at %v, want parked with the wake armed again", run.Status, run.WakeAt)
	}
	if rearmedAt := time.UnixMilli(*run.WakeAt); rearmedAt.Before(before.Add(30*time.Second).Truncate(time.Millisecond)) || rearmedAt.After(after.Add(30*time.Second)) {
		t.Errorf("wake armed again at %s, want 30s after the timeout was published (%s .. %s)", rearmedAt, before.Add(30*time.Second), after.Add(30*time.Second))
	}
	if state := parkedStateOf(t, w, executionID); !state.IsNodePending("pay") {
		t.Error("the wait node is no longer pending after its timeout was published but not yet applied")
	}

	makeWakesDue(t, w, executionID)
	deliverWake(t, w, executionID, "m2")

	if receiveOn(t, brk, answers) == nil {
		t.Fatal("the timeout was not published again when its first answer was never applied")
	}

	w.answer(t.Context(), requestID, "", "timed_out", nil)
	w.wg.Wait()

	if got := durableFinalTask(t, brk, replies).State; got != a2a.TaskStateFailed {
		t.Errorf("final state = %s, want failed", got)
	}
	if got := runRowOf(t, w, executionID).Status; got != wfstore.RunEnded {
		t.Errorf("run after the timeout was applied = %s, want %s", got, wfstore.RunEnded)
	}
	due, _, err := w.runStore().ParkedDueToWake(t.Context(), w.cfg.Name, time.Now().Add(time.Hour).UnixMilli(), wfstore.WakeCursor{}, 10)
	if err != nil || len(due) != 0 {
		t.Errorf("runs due to wake after the timeout was applied = %v (err %v), want none", due, err)
	}
}

type takenOverWake struct {
	Status      string
	HolderEpoch int64
	WakeAt      *int64
}

func TestDurableWait_AWakeForATimerDueWithinTheClockMarginTakesTheRunOverAndRearmsIt(t *testing.T) {
	w, brk := newDurableWaitWorkflow(t, map[string]any{"done": "yes"}, timerNode())
	replies := subscribed(t, brk, requestAckReplyTo)
	executionID := startParked(t, w, "task-wake-within-margin")
	makeWakesDueAt(t, w, executionID, time.Now().Add(2*time.Second))
	before := *runRowOf(t, w, executionID)

	deliverWake(t, w, executionID, "m1")

	run := runRowOf(t, w, executionID)
	want := takenOverWake{Status: wfstore.RunParked, HolderEpoch: before.HolderEpoch + 1, WakeAt: before.WakeAt}
	if diff := cmp.Diff(want, takenOverWake{Status: run.Status, HolderEpoch: run.HolderEpoch, WakeAt: run.WakeAt}); diff != "" {
		t.Fatalf("run after a wake 2s before its timer mismatch (-want +got):\n%s", diff)
	}
	if state := parkedStateOf(t, w, executionID); !state.hasWakeTimers() {
		t.Error("the run re-parked without its timer armed")
	}

	makeWakesDue(t, w, executionID)
	deliverWake(t, w, executionID, "m2")

	wantFinal := durableFinal{State: a2a.TaskStateCompleted, Output: map[string]any{"done": "yes"}}
	if diff := cmp.Diff(wantFinal, durableFinalTask(t, brk, replies)); diff != "" {
		t.Errorf("final response mismatch (-want +got):\n%s", diff)
	}
}

type partlyDueRun struct {
	Status         string
	EarlyCompleted bool
	LateCompleted  bool
	LatePending    bool
	WakeAt         *int64
}

func TestDurableWait_AWakeCompletesOnlyTheTimersThatAreDueAndRearmsTheRunForTheLaterOne(t *testing.T) {
	early := &NodeConfig{ID: "early", Type: NodeTypeTimer, WaitDuration: time.Hour}
	late := &NodeConfig{ID: "late", Type: NodeTypeTimer, WaitDuration: 2 * time.Hour}
	w, _ := newDurableWaitWorkflow(t, map[string]any{"done": "yes"}, early, late)
	executionID := startParked(t, w, "task-two-timers")
	var lateFiresAt time.Time
	rewriteParkedState(t, w, executionID, func(s *WorkflowExecutionState) {
		for i := range s.Timers {
			switch s.Timers[i].NodeID {
			case "early":
				s.Timers[i].FiresAt = timerDueAt
			case "late":
				lateFiresAt = s.Timers[i].FiresAt
			}
		}
	})
	if _, err := w.pausedDB.ExecContext(t.Context(),
		`UPDATE workflow_checkpoint SET wake_at = ? WHERE execution_id = ?`, timerDueAt.UnixMilli(), executionID); err != nil {
		t.Fatalf("make the early timer due: %v", err)
	}

	deliverWake(t, w, executionID, "m1")

	run := runRowOf(t, w, executionID)
	state := parkedStateOf(t, w, executionID)
	_, earlyCompleted := state.CompletedNodes["early"]
	_, lateCompleted := state.CompletedNodes["late"]
	lateWakeAt := lateFiresAt.UnixMilli()
	want := partlyDueRun{Status: wfstore.RunParked, EarlyCompleted: true, LatePending: true, WakeAt: &lateWakeAt}
	got := partlyDueRun{Status: run.Status, EarlyCompleted: earlyCompleted, LateCompleted: lateCompleted, LatePending: state.IsNodePending("late"), WakeAt: run.WakeAt}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("run after a wake with only the early timer due mismatch (-want +got):\n%s", diff)
	}
}

type evalFinal struct {
	State             a2a.TaskState
	TerminationReason string
	FailedNodeID      string
}

func TestWaitNode_OnAnEvaluationRunFailsAsPausedForInputWithoutParking(t *testing.T) {
	const taskID = "task-wait-eval"
	w, brk := newDurableWaitWorkflow(t, map[string]any{"done": "yes"}, waitNode(0))
	replies := subscribed(t, brk, requestAckReplyTo)
	req, bmsg := authzTaskMessage(taskID, a2a.UserProperties{UserID: "system:eval", Trigger: a2a.TriggerEval, ReplyTo: requestAckReplyTo})
	var acks ackCounter
	msg := broker.NewDeliveredMessage("m-"+taskID, "", bmsg.Payload, func() { acks.n.Add(1) }, func() {})
	msg.UserProperties = bmsg.UserProperties

	w.handleTaskRequest(t.Context(), *req, msg)
	w.wg.Wait()

	reply := receiveOn(t, brk, replies)
	if reply == nil {
		t.Fatal("the evaluation run published no final response")
	}
	var resp struct {
		Result struct {
			Status   struct{ State a2a.TaskState } `json:"status"`
			Metadata map[string]string             `json:"metadata"`
		} `json:"result"`
	}
	if err := json.Unmarshal(reply.Payload, &resp); err != nil {
		t.Fatalf("not a final task response: %s", reply.Payload)
	}
	got := evalFinal{
		State:             resp.Result.Status.State,
		TerminationReason: resp.Result.Metadata["termination_reason"],
		FailedNodeID:      resp.Result.Metadata["failed_node_id"],
	}
	want := evalFinal{State: a2a.TaskStateFailed, TerminationReason: "paused_for_input", FailedNodeID: "pay"}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("final response mismatch (-want +got):\n%s", diff)
	}
	if pauses, err := w.pausedRepo.PausesForTask(t.Context(), taskID); err != nil || len(pauses) != 0 {
		t.Errorf("paused rows of the evaluation run = %v (err %v), want none", pauses, err)
	}
	if run, err := w.runStore().LiveRunForTask(t.Context(), w.cfg.Name, taskID); err != nil || run != nil {
		t.Errorf("live run of the evaluation task = %+v (err %v), want none: nobody can send its signal", run, err)
	}
}

func TestDurableWait_ARunWaitingOnANodeARedeployRemovedFailsWhenItsWakeOrSignalArrives(t *testing.T) {
	reusedByAnAgent := func(id string) *NodeConfig {
		return &NodeConfig{ID: id, Type: "agent", AgentName: "reusedAgent"}
	}
	for _, tc := range []struct {
		name        string
		node        *NodeConfig
		replacement *NodeConfig
		signal      bool
	}{
		{name: "a due wake for a removed timer node", node: timerNode()},
		{name: "a signal for a removed wait node", node: waitNode(0), signal: true},
		{name: "a due wake for a timer node whose id an agent node now uses", node: timerNode(), replacement: reusedByAnAgent("cool")},
		{name: "a signal for a wait node whose id an agent node now uses", node: waitNode(0), replacement: reusedByAnAgent("pay"), signal: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const taskID = "task-removed-node"
			w, brk := newDurableWaitWorkflow(t, map[string]any{"done": "yes"}, tc.node)
			replies := subscribed(t, brk, requestAckReplyTo)
			executionID := startParked(t, w, taskID)
			if tc.replacement != nil {
				w.dag.Nodes[tc.node.ID] = tc.replacement
			} else {
				delete(w.dag.Nodes, tc.node.ID)
			}

			if tc.signal {
				w.answer(t.Context(), "signal:"+taskID+":paid", "", "received", nil)
				w.wg.Wait()
			} else {
				makeWakesDue(t, w, executionID)
				deliverWake(t, w, executionID, "m1")
			}

			if got := durableFinalTask(t, brk, replies).State; got != a2a.TaskStateFailed {
				t.Errorf("final state = %s, want failed", got)
			}
			waitFor(t, "the run to end", func() bool { return runRowOf(t, w, executionID).Status == wfstore.RunEnded })
		})
	}
}

func durableWaitServices(flags ...string) *stubServices {
	return &stubServices{brk: &capturingBroker{}, features: flagsOn(append([]string{features.KeyWorkflowCheckpointResume}, flags...)...)}
}

func timerDefinition() *WorkflowConfig {
	return &WorkflowConfig{
		Name: "durable", Namespace: "test-ns", OutputMapping: map[string]any{"done": "yes"},
		Nodes: []*NodeConfig{timerNode()},
	}
}

func agentOnlyDefinition() *WorkflowConfig {
	return &WorkflowConfig{
		Name: "durable", Namespace: "test-ns", OutputMapping: map[string]any{"done": "yes"},
		Nodes: []*NodeConfig{{ID: "step", Type: "agent", AgentName: "stepAgent"}},
	}
}

func parkedDueTimerRun(t *testing.T, dbURL, taskID string) string {
	t.Helper()
	a := startCheckpointAWEWith(t, timerDefinition(), dbURL, durableWaitServices(features.KeyWorkflowDurableWaits, hilPauseResumeFlag))
	var acks ackCounter
	a.deliver(t, taskID, &acks, false)
	executionID := awaitParkedRun(t, a.w, taskID)
	makeWakesDue(t, a.w, executionID)
	return executionID
}

func awaitParkedRun(t *testing.T, w *WorkflowInstance, taskID string) string {
	t.Helper()
	var run *wfstore.WorkflowRun
	waitFor(t, "the run to park", func() bool {
		var err error
		run, err = w.runStore().LiveRunForTask(t.Context(), w.cfg.Name, taskID)
		return err == nil && run != nil && run.Status == wfstore.RunParked
	})
	return run.ExecutionID
}

func deliverWakeThroughTheQueue(w *WorkflowInstance, executionID string, acks *ackCounter) {
	w.routeMessage(context.Background(), broker.NewDeliveredMessage("wake-routed",
		a2a.WorkflowWakeTopic(w.cfg.Namespace, w.cfg.Name, executionID), []byte("m1"), func() { acks.n.Add(1) }, func() {}))
}

func TestDurableWait_AWakeForARunOfATimerDefinitionFailsItOnAnInstanceWhoseDefinitionDroppedTheTimer(t *testing.T) {
	dbURL := sharedStoreURL(t)
	executionID := parkedDueTimerRun(t, dbURL, "task-timer-dropped-definition")
	b := startCheckpointAWEWith(t, agentOnlyDefinition(), dbURL, durableWaitServices(features.KeyWorkflowDurableWaits, hilPauseResumeFlag))

	var acks ackCounter
	deliverWakeThroughTheQueue(b.w, executionID, &acks)

	waitFor(t, "the run to end", func() bool { return runRowOf(t, b.w, executionID).Status == wfstore.RunEnded })
	final := finalTaskOn(t, b.brk.snapshot(), checkpointReplyTo)
	if final == nil || final.Status.State != a2a.TaskStateFailed {
		t.Errorf("final response = %+v, want a failed task", final)
	}
}

func TestDurableWait_AWakeIsDroppedAndTheRunStaysParkedWhileDurableWaitsAreOff(t *testing.T) {
	dbURL := sharedStoreURL(t)
	executionID := parkedDueTimerRun(t, dbURL, "task-timer-wake-flag-off")
	b := startCheckpointAWEWith(t, agentOnlyDefinition(), dbURL, durableWaitServices(hilPauseResumeFlag))

	var acks ackCounter
	deliverWakeThroughTheQueue(b.w, executionID, &acks)
	waitFor(t, "the wake handler to return", func() bool { return b.w.requestsInProcess.Load() == 0 })

	if acks.acks() != 1 {
		t.Errorf("wake acknowledged %d times, want 1", acks.acks())
	}
	if run := runRowOf(t, b.w, executionID); run.Status != wfstore.RunParked || run.HeldBy != "" {
		t.Errorf("run after the dropped wake = %s held by %q, want parked and held by nobody", run.Status, run.HeldBy)
	}
}

func TestSweepParkedRuns_PublishesTheWakeOfADueRunOnlyWhileDurableWaitsAreOn(t *testing.T) {
	for _, tc := range []struct {
		name      string
		flags     []string
		wantWakes int
	}{
		{name: "durable waits on publishes the wake", flags: []string{features.KeyWorkflowDurableWaits, hilPauseResumeFlag}, wantWakes: 1},
		{name: "durable waits off publishes none", flags: []string{hilPauseResumeFlag}, wantWakes: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dbURL := sharedStoreURL(t)
			executionID := parkedDueTimerRun(t, dbURL, "task-sweep-parked-runs")
			b := startCheckpointAWEWith(t, agentOnlyDefinition(), dbURL, durableWaitServices(tc.flags...))

			b.w.sweepParkedRuns(t.Context())

			wakeTopic := a2a.WorkflowWakeTopic("test-ns", "durable", executionID)
			got := 0
			for _, m := range b.brk.snapshot() {
				if m.Topic == wakeTopic {
					got++
				}
			}
			if got != tc.wantWakes {
				t.Errorf("wakes published for the due run = %d, want %d", got, tc.wantWakes)
			}
		})
	}
}

func TestStart_ADurableWaitInstanceReceivesAWakePublishedOnTheBroker(t *testing.T) {
	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatalf("connect broker: %v", err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })
	cfg := timerDefinition()
	cfg.HILDatabaseURL = sharedStoreURL(t)
	w := NewWorkflowInstance(cfg)
	svc := &stubServices{brk: brk, features: flagsOn(features.KeyWorkflowCheckpointResume, features.KeyWorkflowDurableWaits, hilPauseResumeFlag)}
	if err := w.Init(t.Context(), nil, svc); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := w.Start(t.Context()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = w.Stop(context.WithoutCancel(t.Context())) })
	replies := subscribed(t, brk, requestAckReplyTo)
	var acks ackCounter
	req, msg := deliveredRequest("task-wake-through-broker", &acks, false)
	w.handleTaskRequest(t.Context(), *req, msg)
	executionID := awaitParkedRun(t, w, "task-wake-through-broker")
	makeWakesDue(t, w, executionID)

	if err := w.publishWake(t.Context(), executionID); err != nil {
		t.Fatalf("publishWake: %v", err)
	}

	if got := durableFinalTask(t, brk, replies).State; got != a2a.TaskStateCompleted {
		t.Errorf("final state after a wake published on the broker = %s, want completed", got)
	}
}

type subscriptionRecorder struct {
	broker.Broker
	mu     sync.Mutex
	topics []string
}

func (r *subscriptionRecorder) Subscribe(q broker.Queue, topic string) error {
	r.mu.Lock()
	r.topics = append(r.topics, topic)
	r.mu.Unlock()
	return r.Broker.Subscribe(q, topic)
}

func (r *subscriptionRecorder) subscribedTo(topic string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Contains(r.topics, topic)
}

func TestStart_AnInstanceWithDurableWaitsOffDoesNotSubscribeToWakes(t *testing.T) {
	brk := &subscriptionRecorder{Broker: &capturingBroker{}}
	cfg := agentOnlyDefinition()
	cfg.HILDatabaseURL = sharedStoreURL(t)
	w := NewWorkflowInstance(cfg)
	if err := w.Init(t.Context(), nil, &stubServices{brk: brk, features: flagsOn(hilPauseResumeFlag)}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := w.Start(t.Context()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = w.Stop(context.WithoutCancel(t.Context())) })

	if !brk.subscribedTo(a2a.NotifyResponseSubscription("test-ns", "durable")) {
		t.Fatal("the instance did not subscribe to its answers; the recorder saw no subscriptions")
	}
	if brk.subscribedTo(a2a.WorkflowWakeSubscription("test-ns", "durable")) {
		t.Error("an instance with durable waits off subscribed to wakes")
	}
}

type failFirstGuaranteedPublish struct {
	broker.Broker
	topic  string
	failed atomic.Bool
}

func (b *failFirstGuaranteedPublish) PublishGuaranteed(ctx context.Context, topic string, msg *broker.Message) error {
	if topic == b.topic && b.failed.CompareAndSwap(false, true) {
		return errors.New("broker unavailable")
	}
	return b.Broker.PublishGuaranteed(ctx, topic, msg)
}

type servicesOn struct {
	runtime.Services
	brk broker.Broker
}

func (s servicesOn) Broker() broker.Broker { return s.brk }

func TestWaitNode_ATimeoutThatCannotBePublishedIsRetriedOnTheNextWake(t *testing.T) {
	const taskID = "task-wait-timeout-retry"
	w, inner := newDurableWaitWorkflow(t, map[string]any{"done": "yes"}, waitNode(time.Hour))
	requestID := "signal:" + taskID + ":paid"
	answerTopic := a2a.NotifyResponseTopic(w.cfg.Namespace, w.cfg.Name, requestID)
	w.svc = servicesOn{Services: w.svc, brk: &failFirstGuaranteedPublish{Broker: inner, topic: answerTopic}}
	answers := subscribed(t, inner, answerTopic)
	executionID := startParked(t, w, taskID)
	makeWakesDue(t, w, executionID)

	before := time.Now()
	deliverWake(t, w, executionID, "m1")
	after := time.Now()

	if msg := receiveOn(t, inner, answers); msg != nil {
		t.Fatalf("a timeout whose publish failed still reached the answer topic: %s", msg.Payload)
	}
	run := runRowOf(t, w, executionID)
	if run.Status != wfstore.RunParked || run.WakeAt == nil {
		t.Fatalf("run after the failed timeout publish = %s with wake_at %v, want parked with a retry wake", run.Status, run.WakeAt)
	}
	if retryAt := time.UnixMilli(*run.WakeAt); retryAt.Before(before.Add(30*time.Second).Truncate(time.Millisecond)) || retryAt.After(after.Add(30*time.Second)) {
		t.Errorf("retry wake at %s, want 30s after the failure (%s .. %s)", retryAt, before.Add(30*time.Second), after.Add(30*time.Second))
	}

	makeWakesDue(t, w, executionID)
	deliverWake(t, w, executionID, "m2")

	timeout := receiveOn(t, inner, answers)
	if timeout == nil {
		t.Fatal("the retried wake published no timeout answer")
	}
	var answer a2a.UserInputResponseData
	if err := json.Unmarshal(timeout.Payload, &answer); err != nil || answer.RequestID != requestID || answer.Status != "timed_out" {
		t.Errorf("timeout answer = %+v (err %v), want timed_out for %s", answer, err, requestID)
	}
}

var oneHourMs int64 = 3_600_000

func TestNextWakeAt_IsTheEarliestWakeTimerOrNone(t *testing.T) {
	for _, tc := range []struct {
		name   string
		timers []Timer
		want   *int64
	}{
		{
			name: "the earliest of several wake timers wins over a sooner retry timer",
			timers: []Timer{
				{Kind: TimerKindWake, NodeID: "late", FiresAt: time.UnixMilli(7_200_000)},
				{Kind: TimerKindWake, NodeID: "early", FiresAt: time.UnixMilli(3_600_000)},
				{Kind: TimerKindRetry, NodeID: "retrying", FiresAt: time.UnixMilli(60_000)},
			},
			want: &oneHourMs,
		},
		{
			name:   "a state with only a retry timer has no wake",
			timers: []Timer{{Kind: TimerKindRetry, NodeID: "retrying", FiresAt: time.UnixMilli(60_000)}},
		},
		{name: "a state without timers has no wake"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state := &WorkflowExecutionState{Timers: tc.timers}

			if diff := cmp.Diff(tc.want, state.nextWakeAt()); diff != "" {
				t.Errorf("nextWakeAt() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func signalAnswer(w *WorkflowInstance, requestID, status string, acks *ackCounter) *broker.Message {
	payload, err := json.Marshal(a2a.UserInputResponseData{
		Type: string(a2a.SignalUserInputResponse), RequestID: requestID, Status: status,
	})
	if err != nil {
		panic(err)
	}
	return broker.NewDeliveredMessage("answer-"+requestID, a2a.NotifyResponseTopic(w.cfg.Namespace, w.cfg.Name, requestID),
		payload, func() { acks.n.Add(1) }, func() {})
}

type pausePair struct {
	RequestID string
	Status    string
}

func TestDurableWait_ASignalAnswerForAnotherWorkflowsRunIsDroppedAndLeavesTheRunUntouched(t *testing.T) {
	const taskID = "task-signal-other-workflow"
	requestID := "signal:" + taskID + ":paid"
	dbURL := sharedStoreURL(t)
	waiting := &WorkflowConfig{
		Name: "wf-a", Namespace: "test-ns", OutputMapping: map[string]any{"done": "yes"},
		Nodes: []*NodeConfig{waitNode(0)},
	}
	other := agentOnlyDefinition()
	other.Name = "wf-b"
	a := startCheckpointAWEWith(t, waiting, dbURL, durableWaitServices(features.KeyWorkflowDurableWaits, hilPauseResumeFlag))
	b := startCheckpointAWEWith(t, other, dbURL, durableWaitServices(hilPauseResumeFlag))
	var requestAcks ackCounter
	a.deliver(t, taskID, &requestAcks, false)
	executionID := awaitParkedRun(t, a.w, taskID)

	var otherAcks ackCounter
	b.w.handleWorkflowHILResponse(t.Context(), signalAnswer(b.w, requestID, "received", &otherAcks))

	if otherAcks.acks() != 1 {
		t.Errorf("answer received by the other workflow acknowledged %d times, want 1", otherAcks.acks())
	}
	pauses, err := a.w.pausedRepo.PausesForTask(t.Context(), taskID)
	if err != nil {
		t.Fatalf("PausesForTask: %v", err)
	}
	var gotPauses []pausePair
	for _, p := range pauses {
		gotPauses = append(gotPauses, pausePair{RequestID: p.RequestID, Status: p.Status})
	}
	if diff := cmp.Diff([]pausePair{{RequestID: requestID, Status: wfstore.StatusPaused}}, gotPauses); diff != "" {
		t.Errorf("pauses after the other workflow's answer mismatch (-want +got):\n%s", diff)
	}
	if run := runRowOf(t, a.w, executionID); run.Status != wfstore.RunParked || run.HeldBy != "" {
		t.Errorf("run after the other workflow's answer = %s held by %q, want parked and held by nobody", run.Status, run.HeldBy)
	}
	if finalTaskOn(t, a.brk.snapshot(), checkpointReplyTo) != nil || finalTaskOn(t, b.brk.snapshot(), checkpointReplyTo) != nil {
		t.Error("the other workflow's answer produced a final response")
	}

	var ownAcks ackCounter
	a.w.handleWorkflowHILResponse(t.Context(), signalAnswer(a.w, requestID, "received", &ownAcks))

	waitFor(t, "the run to answer its caller", func() bool { return finalTaskOn(t, a.brk.snapshot(), checkpointReplyTo) != nil })
	if got := finalTaskOn(t, a.brk.snapshot(), checkpointReplyTo).Status.State; got != a2a.TaskStateCompleted {
		t.Errorf("final state after the owning workflow's answer = %s, want completed", got)
	}
}

type recordedPublishes struct {
	broker.Broker
	mu     sync.Mutex
	topics []string
}

func (r *recordedPublishes) PublishGuaranteed(ctx context.Context, topic string, msg *broker.Message) error {
	r.mu.Lock()
	r.topics = append(r.topics, topic)
	r.mu.Unlock()
	return r.Broker.PublishGuaranteed(ctx, topic, msg)
}

func parkDueRun(t *testing.T, w *WorkflowInstance, executionID string) {
	t.Helper()
	dueAt := timerDueAt.UnixMilli()
	if _, err := w.runStore().Park(t.Context(), executionID, wfstore.Holder{Key: wfstore.HeldByRequest}, wfstore.RunState{
		TaskID: "task-" + executionID, WorkflowName: w.cfg.Name, StateSnapshot: "{}", ExecContext: "{}", WakeAt: &dueAt,
	}); err != nil {
		t.Fatalf("Park(%s): %v", executionID, err)
	}
}

type refusedPublishes struct {
	broker.Broker
	attempts atomic.Int32
}

func (b *refusedPublishes) PublishGuaranteed(context.Context, string, *broker.Message) error {
	b.attempts.Add(1)
	return errors.New("broker unavailable")
}

func TestSweepDueWakes_StopsAtTheFirstWakeThatCannotBePublished(t *testing.T) {
	w, inner := newDurableWaitWorkflow(t, map[string]any{"done": "yes"}, timerNode())
	refused := &refusedPublishes{Broker: inner}
	w.svc = servicesOn{Services: w.svc, brk: refused}
	var logged strings.Builder
	w.logger = slog.New(slog.NewTextHandler(&lockedWriter{w: &logged, mu: &sync.Mutex{}}, &slog.HandlerOptions{Level: slog.LevelWarn}))
	for _, executionID := range []string{"run-a", "run-b", "run-c"} {
		parkDueRun(t, w, executionID)
	}

	w.sweepDueWakes(t.Context())

	if got := refused.attempts.Load(); got != 1 {
		t.Errorf("wake publishes attempted by one sweep of 3 due runs on a broker refusing them = %d, want 1", got)
	}
	for _, want := range []string{"executionID=run-a", "publishedThisSweep=0"} {
		if !strings.Contains(logged.String(), want) {
			t.Errorf("sweep warning = %q, want it to carry %q", logged.String(), want)
		}
	}
}

func TestSweepDueWakes_PublishesTheWakeOfEveryDueRunWhenMoreThanOnePageIsDue(t *testing.T) {
	const dueRuns = 129
	w, inner := newDurableWaitWorkflow(t, map[string]any{"done": "yes"}, timerNode())
	published := &recordedPublishes{Broker: inner}
	w.svc = servicesOn{Services: w.svc, brk: published}
	wantTopics := map[string]bool{}
	for i := range dueRuns {
		executionID := "run-" + string(rune('A'+i/26)) + string(rune('a'+i%26))
		parkDueRun(t, w, executionID)
		wantTopics[a2a.WorkflowWakeTopic(w.cfg.Namespace, w.cfg.Name, executionID)] = true
	}

	w.sweepDueWakes(t.Context())

	published.mu.Lock()
	defer published.mu.Unlock()
	gotTopics := map[string]bool{}
	for _, topic := range published.topics {
		gotTopics[topic] = true
	}
	if len(published.topics) != dueRuns || len(gotTopics) != dueRuns {
		t.Fatalf("sweep published %d wakes for %d distinct runs, want %d for %d", len(published.topics), len(gotTopics), dueRuns, dueRuns)
	}
	if diff := cmp.Diff(wantTopics, gotTopics); diff != "" {
		t.Errorf("woken runs mismatch (-want +got):\n%s", diff)
	}
}

type storedPause struct {
	Status string
	Answer a2a.UserInputResponseData
}

func storedPauseOf(t *testing.T, w *WorkflowInstance, requestID string) storedPause {
	t.Helper()
	row, err := w.pausedRepo.Get(t.Context(), requestID)
	if err != nil || row == nil {
		t.Fatalf("pause row %s = %v, %v", requestID, row, err)
	}
	got := storedPause{Status: row.Status}
	if row.Answer == nil {
		return got
	}
	if err := json.Unmarshal([]byte(*row.Answer), &got.Answer); err != nil {
		t.Fatalf("pause row %s holds an answer that is not a user_input_response: %s", requestID, *row.Answer)
	}
	return got
}

func TestWaitNode_ASignalAcceptedBeforeItsAWEDiedIsAppliedWhenTheWakeIsReplayedOnAnotherAWE(t *testing.T) {
	const taskID = "task-signal-before-restore"
	requestID := "signal:" + taskID + ":paid"
	pay := waitNode(0)
	pay.DependsOn = []string{"cool"}
	notify := &NodeConfig{ID: "notify", Type: "agent", AgentName: "notifyAgent", DependsOn: []string{"cool"}}
	a, _ := newDurableWaitWorkflow(t, map[string]any{"payer": "{{pay.output.payer}}", "status": "{{pay.output.status}}"}, timerNode(), pay, notify)
	executionID := startParked(t, a, taskID)
	makeWakesDue(t, a, executionID)

	b, _, brkB := newPeer(t, a)
	b.durableWaits = true
	notifyOnB := subscribed(t, brkB, a2a.AgentRequestTopic(b.cfg.Namespace, "notifyAgent"))
	deliverWakeMessage(t, b, executionID, "m1", false)
	if receiveOn(t, brkB, notifyOnB) == nil {
		t.Fatal("the AWE that took the wake did not dispatch the notify node")
	}
	b.answer(t.Context(), requestID, "", "received", map[string]any{"payer": "bob"})
	b.wg.Wait()
	wantPause := storedPause{Status: "resumed", Answer: a2a.UserInputResponseData{
		Type: "user_input_response", RequestID: requestID, Status: "received", Data: map[string]any{"payer": "bob"},
	}}
	if diff := cmp.Diff(wantPause, storedPauseOf(t, a, requestID)); diff != "" {
		t.Fatalf("pause after the signal reached the first AWE mismatch (-want +got):\n%s", diff)
	}

	c, _, brkC := newPeer(t, a)
	c.durableWaits = true
	replies := subscribed(t, brkC, requestAckReplyTo)
	notifyOnC := subscribed(t, brkC, a2a.AgentRequestTopic(c.cfg.Namespace, "notifyAgent"))
	deliverWakeMessage(t, c, executionID, "m1", true)
	if receiveOn(t, brkC, notifyOnC) == nil {
		t.Fatal("the AWE that replayed the wake did not dispatch the notify node")
	}

	if diff := cmp.Diff(wantPause, storedPauseOf(t, c, requestID)); diff != "" {
		t.Errorf("pause after the wake was replayed mismatch (-want +got):\n%s", diff)
	}
	exec := executionFor(c, taskID)
	if exec == nil {
		t.Fatal("the replayed run is not executing on the second AWE")
	}
	exec.Executor.mu.Lock()
	gotPay := exec.State.NodeOutputs["pay"]
	exec.Executor.mu.Unlock()
	wantPay := map[string]any{"output": map[string]any{"payer": "bob", "status": "received"}}
	if diff := cmp.Diff(wantPay, gotPay); diff != "" {
		t.Errorf("output of pay after the replay mismatch (-want +got):\n%s", diff)
	}

	deliverSubTaskResult(t, c, subTaskOf(t, exec, "notify"), a2a.TaskStateCompleted)
	c.wg.Wait()

	want := durableFinal{State: a2a.TaskStateCompleted, Output: map[string]any{"payer": "bob", "status": "received"}}
	if diff := cmp.Diff(want, durableFinalTask(t, brkC, replies)); diff != "" {
		t.Errorf("final response mismatch (-want +got):\n%s", diff)
	}
}

func TestDurableWait_AWakeForAParkedRunWhoseWaitIsNotDueIsAckedOnceAndLeavesTheRunUntouched(t *testing.T) {
	for _, tc := range []struct {
		name string
		node *NodeConfig
	}{
		{name: "a timer due in an hour", node: timerNode()},
		{name: "a wait node without a timeout has no due time", node: waitNode(0)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w, _ := newDurableWaitWorkflow(t, map[string]any{"done": "yes"}, tc.node)
			executionID := startParked(t, w, "task-wake-not-due")
			before := *runRowOf(t, w, executionID)

			acks := deliverWake(t, w, executionID, "m1")

			if acks.acks() != 1 {
				t.Errorf("wake acknowledged %d times, want 1", acks.acks())
			}
			if diff := cmp.Diff(before, *runRowOf(t, w, executionID)); diff != "" {
				t.Errorf("run row after a wake that is not due mismatch (-before +after):\n%s", diff)
			}
		})
	}
}

type signalAnswerStep struct {
	Status string
	Data   map[string]any
}

func TestWaitNode_ATimeoutAndASignalRacingForOnePauseLetTheFirstWinAndDropTheSecond(t *testing.T) {
	signal := signalAnswerStep{Status: "received", Data: map[string]any{"payer": "bob"}}
	timeout := signalAnswerStep{Status: "timed_out"}
	for _, tc := range []struct {
		name       string
		first      signalAnswerStep
		second     signalAnswerStep
		wantFinal  durableFinal
		wantStatus string
	}{
		{
			name:  "a signal then a timeout completes the node with the signal",
			first: signal, second: timeout,
			wantFinal:  durableFinal{State: a2a.TaskStateCompleted, Output: map[string]any{"payer": "bob", "status": "received"}},
			wantStatus: "resumed",
		},
		{
			name:  "a timeout then a signal fails the node as timed out",
			first: timeout, second: signal,
			wantFinal:  durableFinal{State: a2a.TaskStateFailed},
			wantStatus: "timed_out",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const taskID = "task-wait-race"
			requestID := "signal:" + taskID + ":paid"
			w, brk := newDurableWaitWorkflow(t, map[string]any{"payer": "{{pay.output.payer}}", "status": "{{pay.output.status}}"}, waitNode(time.Hour))
			replies := subscribed(t, brk, requestAckReplyTo)
			executionID := startParked(t, w, taskID)

			for _, step := range []signalAnswerStep{tc.first, tc.second} {
				w.answer(t.Context(), requestID, "", step.Status, step.Data)
				w.wg.Wait()
			}

			if diff := cmp.Diff(tc.wantFinal, durableFinalTask(t, brk, replies)); diff != "" {
				t.Errorf("final response mismatch (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(storedPause{Status: tc.wantStatus}, storedPauseOf(t, w, requestID)); diff != "" {
				t.Errorf("pause row after both answers mismatch (-want +got):\n%s", diff)
			}
			if got := runRowOf(t, w, executionID).Status; got != wfstore.RunEnded {
				t.Errorf("run after both answers = %s, want %s", got, wfstore.RunEnded)
			}
		})
	}
}

func TestWaitNode_ASignalWithoutDataCompletesItWithOnlyItsStatus(t *testing.T) {
	const taskID = "task-wait-signal-no-data"
	w, brk := newDurableWaitWorkflow(t, map[string]any{"all": "{{pay.output}}"}, waitNode(0))
	replies := subscribed(t, brk, requestAckReplyTo)
	startParked(t, w, taskID)

	w.answer(t.Context(), "signal:"+taskID+":paid", "", "received", nil)
	w.wg.Wait()

	want := durableFinal{State: a2a.TaskStateCompleted, Output: map[string]any{"all": map[string]any{"status": "received"}}}
	if diff := cmp.Diff(want, durableFinalTask(t, brk, replies)); diff != "" {
		t.Errorf("final response mismatch (-want +got):\n%s", diff)
	}
}

func TestWaitNode_ATimeoutFailsTheRunWithoutRetryingUnderAWorkflowRetryStrategy(t *testing.T) {
	const taskID = "task-wait-timeout-no-retry"
	w, brk := newDurableWaitWorkflow(t, map[string]any{"done": "yes"}, waitNode(time.Hour))
	w.cfg.RetryStrategy = &RetryStrategy{Limit: 2, RetryPolicy: "Always"}
	replies := subscribed(t, brk, requestAckReplyTo)
	executionID := startParked(t, w, taskID)

	w.answer(t.Context(), "signal:"+taskID+":paid", "", "timed_out", nil)
	w.wg.Wait()

	if got := durableFinalTask(t, brk, replies).State; got != a2a.TaskStateFailed {
		t.Errorf("final state = %s, want failed", got)
	}
	waitFor(t, "the run to end", func() bool { return runRowOf(t, w, executionID).Status == wfstore.RunEnded })
}

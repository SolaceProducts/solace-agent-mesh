// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agentlink

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
)

func openWork(t *testing.T, r *rig, c *client) (<-chan *broker.Message, string) {
	t.Helper()
	requests := r.listen(a2a.AgentRequestTopic(testNS, peerName))
	status := r.listen(statusTopic("T1"))
	c.send(event("T1", map[string]any{"kind": eventProgress, "text": "working"}))
	recv(t, status)
	peerID := r.callPeer(c, requests, "S1", "Is policy P-9 active?")
	c.send(map[string]any{"type": typePrompt, "taskId": "T1", "requestId": "q-1", "questions": []any{map[string]any{"question": "Which policy?"}}})
	recv(t, status)
	return requests, peerID
}

func cancelSent(t *testing.T, requests <-chan *broker.Message, peerTaskID string) {
	t.Helper()
	if m := recv(t, requests); !strings.Contains(string(m.Payload), methodCancel) || !strings.Contains(string(m.Payload), `"`+peerTaskID+`"`) {
		t.Fatalf("not a cancel of %s: %s", peerTaskID, m.Payload)
	}
}

func promptAndPeerCallEnd(t *testing.T, c *client) {
	t.Helper()
	got := map[string]received{}
	for len(got) < 2 {
		f := c.nextOf(typePromptAnswer, typePeerResult)
		got[f.Type] = f
	}
	if a := got[typePromptAnswer]; a.RequestID != "q-1" || a.Status != "canceled" {
		t.Fatalf("prompt answer %s", a.Raw)
	}
	if p := got[typePeerResult]; p.SubtaskID != "S1" || p.State != "canceled" {
		t.Fatalf("peer result %s", p.Raw)
	}
}

func TestATaskThatExpiresLetsGoOfItsPeerCallsPromptsAndSequence(t *testing.T) {
	r, c := connectedRig(t, nil)
	r.taskOn(c, "T1", nil)
	requests, peerID := openWork(t, r, c)

	s := r.adapter.agent(agentName)
	s.sweep(time.Now().Add(r.adapter.cfg.TaskTTL + time.Minute))
	cancelSent(t, requests, peerID)
	promptAndPeerCallEnd(t, c)
	r.locked(func(s *agentState) {
		if b := s.tasks["T1"]; b == nil || !b.final || len(s.asks) != 0 || len(s.subtasks) != 0 {
			t.Fatalf("task %+v prompts %d peer calls %d", b, len(s.asks), len(s.subtasks))
		}
	})
	if next := s.seq.Next("T1"); next != 1 {
		t.Fatalf("the expired task's event sequence was kept: next is %d", next)
	}
}

func TestACancelledTaskCancelsItsPeerCallsAndPromptsAtOnceAndTheAgentMayStillAnswer(t *testing.T) {
	r, c := connectedRig(t, nil)
	r.taskOn(c, "T1", nil)
	requests, peerID := openWork(t, r, c)
	answers := r.listen(responseTopic("T1"))

	r.cancel("T1")
	c.next(typeCancel)
	cancelSent(t, requests, peerID)
	promptAndPeerCallEnd(t, c)

	c.send(peerCall("S2", peerName, "x"))
	c.refused(errCodeForbidden, "S2")
	c.send(map[string]any{"type": typePrompt, "taskId": "T1", "requestId": "q-2", "questions": []any{map[string]any{"question": "Which?"}}})
	c.refused(errCodeForbidden, "T1")
	none(t, requests)

	c.send(map[string]any{"type": typeResult, "taskId": "T1", "state": stateCanceled})
	m := recv(t, answers)
	if task := finalTask(t, m); task.Status.State != a2a.TaskStateCancelled || m.UserProperties["eventSeq"] != "3" {
		t.Fatalf("answer %s %v", m.Payload, m.UserProperties)
	}
}

func TestAStalledStatusPublishDoesNotHoldTheConnection(t *testing.T) {
	r, c := connectedRig(t, nil)
	r.adapter.publishWait = 200 * time.Millisecond
	r.taskOn(c, "T1", nil)
	r.brk.stallTopic.Store(statusTopic("T1"))
	c.send(event("T1", map[string]any{"kind": eventProgress, "text": "working"}))
	c.send(map[string]any{"type": typeAck, "delivery": 99})
	c.refused(errCodeUnknownDeliver, "99")
}

func TestACancelWhileTheStoppedAgentHasNoConnectionReleasesTheTaskAndReachesItsNextConnection(t *testing.T) {
	r, c := connectedRig(t, nil)
	r.taskOn(c, "T1", nil)
	requests, peerID := openWork(t, r, c)
	_ = c.ws.Close()
	waitFor(t, func() bool { return len(r.adapter.agent(agentName).connections()) == 0 })
	r.stop()

	r.cancel("T1")
	cancelSent(t, requests, peerID)
	waitFor(t, func() bool {
		var released bool
		r.locked(func(s *agentState) { released = s.tasks["T1"].cancelled && len(s.asks) == 0 && len(s.held) == 3 })
		return released
	})

	c = r.connect()
	c.next(typeControl)
	if f := c.next(typeCancel); f.TaskID != "T1" {
		t.Fatalf("%s", f.Raw)
	}
	promptAndPeerCallEnd(t, c)
}

func TestACancelForAnAgentWithNoConsumingConnectionIsAppliedAtOnce(t *testing.T) {
	for name, newRig := range map[string]func(*testing.T) *rig{
		"queue kept bound":     func(t *testing.T) *rig { return newRig(t, nil, nil) },
		"queue let go of idle": newRigReleasing,
	} {
		t.Run(name, func(t *testing.T) {
			r := newRig(t)
			c := r.connect()
			r.consume(c)
			r.taskOn(c, "T1", nil)
			requests, peerID := openWork(t, r, c)
			_ = c.ws.Close()
			waitFor(t, func() bool { return len(r.adapter.agent(agentName).connections()) == 0 })

			r.cancel("T9")
			r.cancel("T1")
			cancelSent(t, requests, peerID)
			waitFor(t, func() bool {
				var released bool
				r.locked(func(s *agentState) { released = s.tasks["T1"].cancelled && len(s.asks) == 0 && len(s.held) == 3 })
				return released
			})

			c = r.connect()
			if f := c.next(typeCancel); f.TaskID != "T1" {
				t.Fatalf("%s", f.Raw)
			}
			promptAndPeerCallEnd(t, c)
		})
	}
}

func bothRigs(t *testing.T, test func(t *testing.T, r *rig)) {
	for name, newRig := range map[string]func(*testing.T) *rig{
		"queue kept bound":     func(t *testing.T) *rig { return newRig(t, nil, nil) },
		"queue let go of idle": newRigReleasing,
	} {
		t.Run(name, func(t *testing.T) { test(t, newRig(t)) })
	}
}

func TestATaskCancelledWhileNoConnectionHeldItIsAnsweredCancelledAndNeverDeliveredAgain(t *testing.T) {
	bothRigs(t, func(t *testing.T, r *rig) {
		c := r.connect()
		r.consume(c)
		r.submit("T1", "tok:alice:T1", nil)
		c.next(typeTask)
		_ = c.ws.Close()
		waitFor(t, func() bool { return len(r.adapter.agent(agentName).connections()) == 0 })
		r.cancel("T1")
		waitFor(t, func() bool {
			var cancelled bool
			r.locked(func(s *agentState) { cancelled = s.tasks["T1"].cancelled })
			return cancelled
		})

		answers := r.listen(responseTopic("T1"))
		c = r.connect()
		r.consume(c)
		r.submit("T1", "tok:alice:T1", nil)
		if task := finalTask(t, recv(t, answers)); task.Status.State != a2a.TaskStateCancelled {
			t.Fatalf("%+v", task)
		}
		deadline := time.After(500 * time.Millisecond)
		for {
			select {
			case f := <-c.in:
				if f.Type == typeTask {
					t.Fatalf("a cancelled task was delivered: %s", f.Raw)
				}
				continue
			case <-deadline:
			}
			break
		}
		r.locked(func(s *agentState) {
			if len(s.held) != 0 || !s.tasks["T1"].final {
				t.Fatalf("held %d, final %v", len(s.held), s.tasks["T1"].final)
			}
		})
	})
}

func TestATaskTakenOnANewConnectionBeforeItsCancelArrivesIsCancelledThere(t *testing.T) {
	bothRigs(t, func(t *testing.T, r *rig) {
		c := r.connect()
		r.consume(c)
		r.taskOn(c, "T1", nil)
		r.cancel("T1")
		if f := c.next(typeCancel); f.TaskID != "T1" {
			t.Fatalf("%s", f.Raw)
		}
		c.quiet()
	})
}

func TestTheQueuesCopyOfACancelAlreadyAppliedFromTheRequestTopicIsNotSentAgain(t *testing.T) {
	r, c := connectedRig(t, nil)
	for _, taskID := range []string{"T1", "T2"} {
		r.submit(taskID, "tok:alice:"+taskID, nil)
		c.next(typeTask)
	}
	_ = c.ws.Close()
	waitFor(t, func() bool { return len(r.adapter.agent(agentName).connections()) == 0 })
	r.cancel("T1")
	waitFor(t, func() bool {
		var applied bool
		r.locked(func(s *agentState) { applied = s.tasks["T1"].cancelCopyApplied })
		return applied
	})

	c = r.connect()
	if f := c.next(typeCancel); f.TaskID != "T1" {
		t.Fatalf("%s", f.Raw)
	}
	acked := r.brk.acks.Load()
	r.startConsuming(c, agentName)
	waitFor(t, func() bool { return r.brk.acks.Load() > acked })
	c.quiet()
}

func TestACancelNamesItsTaskInParamsIDAsANativeAgentReadsIt(t *testing.T) {
	r, c := connectedRig(t, nil)
	r.taskOn(c, "T1", nil)
	body, _ := json.Marshal(a2a.NewRequest("cancel-request-1", methodCancel, map[string]any{"id": "T1"}))
	if err := r.brk.PublishGuaranteed(t.Context(), a2a.AgentRequestTopic(testNS, agentName),
		&broker.Message{Payload: body, UserProperties: map[string]string{a2a.PropUserID: "alice@example.com"}}); err != nil {
		t.Fatal(err)
	}
	if f := c.next(typeCancel); f.TaskID != "T1" {
		t.Fatalf("%s", f.Raw)
	}
}

func TestAControlCommandWhoseCardPublishStallsDoesNotHoldBackAPeersAnswer(t *testing.T) {
	r, c := connectedRig(t, nil)
	r.taskOn(c, "T1", nil)
	requests := r.listen(a2a.AgentRequestTopic(testNS, peerName))
	peerID := r.callPeer(c, requests, "S1", "Is policy P-9 active?")
	r.adapter.publishWait = 3 * time.Second
	r.brk.stallTopic.Store(a2a.DiscoveryAgentCardsTopic(testNS))
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		r.stop()
	}()
	waitFor(t, func() bool { return r.adapter.agent(agentName).isStopped() })
	answered := time.Now()
	r.peerAnswers(peerID, "response", peerReply(peerID, a2a.TaskStateCompleted, a2a.NewTextPart("Active")))
	f := c.nextOf(typeControl, typePeerResult)
	if f.Type == typeControl {
		f = c.nextOf(typePeerResult)
	}
	if f.SubtaskID != "S1" || time.Since(answered) > time.Second {
		t.Fatalf("the peer's answer took %v: %s", time.Since(answered), f.Raw)
	}
	<-stopped
}

func waitingTask(t *testing.T, r *rig, c *client, taskID string) {
	t.Helper()
	waitFor(t, func() bool { return r.brk.receiving.Load() > 0 })
	c.send(map[string]any{"type": typeDescribe, "agent": "not an object"})
	c.refused(errCodeBadMessage, "")
	before := r.brk.taken.Load()
	r.submit(taskID, "tok:alice:"+taskID, nil)
	waitFor(t, func() bool { return r.brk.taken.Load() > before })
	c.quiet()
}

func TestATaskWaitingForAConnectionWhenSAMStopsTheAgentIsRefusedAsStopped(t *testing.T) {
	r, c := connectedRig(t, nil)
	answers := r.listen(responseTopic("T1"))
	waitingTask(t, r, c, "T1")
	r.adapter.publishWait = 2 * time.Second
	r.brk.stallTopic.Store(a2a.DiscoveryAgentCardsTopic(testNS))
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		r.stop()
	}()
	c.next(typeControl)
	c.send(map[string]any{"type": typeDescribe, "agent": map[string]any{}})
	if m := recv(t, answers); !strings.Contains(string(m.Payload), msgAgentStopped) {
		t.Fatalf("%s", m.Payload)
	}
	c.quiet()
	<-stopped
}

func TestATaskCancelledWhileItWaitsForAConnectionIsAnsweredCancelledAndNeverSent(t *testing.T) {
	r := newRig(t, nil, nil)
	c1 := r.connect()
	r.consume(c1)
	r.submit("T1", "tok:alice:T1", nil)
	c1.next(typeTask)
	c2 := r.connect()
	c2.send(map[string]any{"type": typeDescribe, "agent": "not an object"})
	c2.refused(errCodeBadMessage, "")
	c2.send(map[string]any{"type": typeConsume})
	c2.refused(errCodeBadMessage, "")
	waitFor(t, func() bool { return r.brk.receiving.Load() > 0 })
	_ = c1.ws.Close()
	waitFor(t, func() bool { return len(r.adapter.agent(agentName).connections()) == 1 })

	answers := r.listen(responseTopic("T1"))
	before := r.brk.taken.Load()
	r.submit("T1", "tok:alice:T1", nil)
	waitFor(t, func() bool { return r.brk.taken.Load() > before })
	r.cancel("T1")
	if f := c2.next(typeCancel); f.TaskID != "T1" {
		t.Fatalf("%s", f.Raw)
	}
	c2.send(map[string]any{"type": typeDescribe, "agent": map[string]any{}})
	if task := finalTask(t, recv(t, answers)); task.Status.State != a2a.TaskStateCancelled {
		t.Fatalf("%+v", task)
	}
	c2.quiet()
}

func TestATaskThatWaitedIsCheckedAgainstTheDescriptionCurrentWhenItIsDelivered(t *testing.T) {
	t.Run("required scopes", func(t *testing.T) {
		r := newRig(t, nil, scopeAuthorizer{})
		c := r.connect()
		r.consume(c)
		answers := r.listen(responseTopic("T1"))
		waitingTask(t, r, c, "T1")
		c.send(map[string]any{"type": typeDescribe, "agent": map[string]any{"requiredScopes": []any{"agent:claims:special"}}})
		if m := recv(t, answers); !strings.Contains(string(m.Payload), msgAccessDenied) {
			t.Fatalf("%s", m.Payload)
		}
		c.quiet()
	})
	t.Run("granted tool scopes", func(t *testing.T) {
		r, c := connectedRig(t, nil)
		waitingTask(t, r, c, "T1")
		c.send(map[string]any{"type": typeDescribe, "agent": map[string]any{"tools": []any{
			map[string]any{"name": "pay_claim", "requiredScopes": []any{"tool:claims:pay"}}}}})
		if f := c.next(typeTask); len(f.Task.GrantedToolScopes) != 1 || f.Task.GrantedToolScopes[0] != "tool:claims:pay" {
			t.Fatalf("%s", f.Raw)
		}
	})
}

func TestATaskDeliveredAgainAfterItsResultWasPublishedIsAcknowledgedAndNotSent(t *testing.T) {
	r, c := connectedRig(t, nil)
	answers := r.listen(responseTopic("T1"))
	r.taskOn(c, "T1", nil)
	c.send(map[string]any{"type": typeResult, "taskId": "T1", "state": stateCompleted, "text": "done"})
	recv(t, answers)
	acked := r.brk.acks.Load()
	r.submit("T1", "tok:alice:T1", nil)
	waitFor(t, func() bool { return r.brk.acks.Load() > acked })
	c.quiet()
}

func TestARequestDrivenAgentIsWokenAgainForATaskHeldWhileItsLastConnectionLeft(t *testing.T) {
	r, wakes := wakeRig(t)
	c := r.connect()
	r.consume(c)
	waitingTask(t, r, c, "T1")
	waitFor(t, func() bool { return r.brk.watched.Load() == 1 })
	_ = c.ws.Close()
	waitFor(t, func() bool { return wakes.Load() == 1 })
	time.Sleep(300 * time.Millisecond)
	if wakes.Load() != 1 {
		t.Fatalf("woken %d times for one task given back", wakes.Load())
	}
}

func TestACancelForATaskOnAConnectionIsAppliedWhileAnotherTaskWaits(t *testing.T) {
	r, c := connectedRig(t, nil)
	r.taskOn(c, "T1", nil)
	waitingTask(t, r, c, "T2")
	r.cancel("T1")
	if f := c.next(typeCancel); f.TaskID != "T1" {
		t.Fatalf("%s", f.Raw)
	}
}

func TestATaskBoundBeforeAStopReachesTheAgentBeforeTheStopsControl(t *testing.T) {
	r, c := connectedRig(t, nil)
	s := r.adapter.agent(agentName)
	var once sync.Once
	s.afterTaskBound = func() { once.Do(func() { s.setStopped(true) }) }
	r.submit("T1", "tok:alice:T1", nil)
	if f := c.nextOf(typeControl, typeTask); f.Type != typeTask {
		t.Fatalf("the stop's control frame came before the task bound before it: %s", f.Raw)
	}
	c.next(typeControl)
}

func TestACancelledTaskWaitingForAConnectionIsAnsweredCancelledAtOnce(t *testing.T) {
	r := newRig(t, nil, nil)
	c1 := r.connect()
	r.consume(c1)
	r.submit("T1", "tok:alice:T1", nil)
	c1.next(typeTask)
	c2 := r.connect()
	c2.send(map[string]any{"type": typeDescribe, "agent": "not an object"})
	c2.refused(errCodeBadMessage, "")
	c2.send(map[string]any{"type": typeConsume})
	c2.refused(errCodeBadMessage, "")
	waitFor(t, func() bool { return r.brk.receiving.Load() > 0 })
	_ = c1.ws.Close()
	waitFor(t, func() bool { return len(r.adapter.agent(agentName).connections()) == 1 })

	answers := r.listen(responseTopic("T1"))
	before := r.brk.taken.Load()
	r.submit("T1", "tok:alice:T1", nil)
	waitFor(t, func() bool { return r.brk.taken.Load() > before })
	cancelled := time.Now()
	r.cancel("T1")
	if task := finalTask(t, recv(t, answers)); task.Status.State != a2a.TaskStateCancelled {
		t.Fatalf("%+v", task)
	}
	if waited := time.Since(cancelled); waited > 500*time.Millisecond {
		t.Fatalf("the cancelled task was answered after %v", waited)
	}
}

func TestAStoppedAgentsTaskDeliveredAgainAfterItsResultIsAcknowledgedWithoutARefusal(t *testing.T) {
	r, c := connectedRig(t, nil)
	answers := r.listen(responseTopic("T1"))
	r.taskOn(c, "T1", nil)
	c.send(map[string]any{"type": typeResult, "taskId": "T1", "state": stateCompleted, "text": "done"})
	recv(t, answers)
	r.stop()
	acked := r.brk.acks.Load()
	r.submit("T1", "tok:alice:T1", nil)
	waitFor(t, func() bool { return r.brk.acks.Load() > acked })
	none(t, answers)
}

type countingScopes struct {
	scopeAuthorizer
	checks *atomic.Int32
	hook   func()
}

func (a countingScopes) AuthorizeAgentScopes(ctx context.Context, identity *auth.Identity, required []string) error {
	if a.checks.Add(1) == 1 {
		a.hook()
	}
	return a.scopeAuthorizer.AuthorizeAgentScopes(ctx, identity, required)
}

func TestAnIdenticalDescribeWhileATaskIsDeliveredDoesNotAuthorizeItAgain(t *testing.T) {
	var c *client
	checks := &atomic.Int32{}
	described := map[string]any{"type": typeDescribe, "agent": map[string]any{"requiredScopes": []any{"agent:claims:use"}}}
	r := newRig(t, nil, countingScopes{checks: checks, hook: func() {
		c.send(described)
		time.Sleep(200 * time.Millisecond)
	}})
	c = r.connect()
	c.send(described)
	r.startConsuming(c, agentName)
	r.submit("T1", "tok:alice:T1", nil)
	c.next(typeTask)
	if checks.Load() != 1 {
		t.Fatalf("the task's scopes were checked %d times", checks.Load())
	}
}

func TestAStoppedLinkRefusesToStartAgainInPlace(t *testing.T) {
	r := newRig(t, nil, nil)
	if err := r.core.Stop(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := r.core.Start(t.Context()); !errors.Is(err, errRestartInPlace) {
		t.Fatalf("Start after Stop = %v, want errRestartInPlace", err)
	}
}

func TestALinkStoppedBeforeItStartedRefusesToStart(t *testing.T) {
	adapter, err := New(map[string]any{"agents": []any{map[string]any{"name": agentName, "token_sha256": digest(token)}}})
	if err != nil {
		t.Fatal(err)
	}
	adapter.logger = slog.New(slog.DiscardHandler)
	if err := adapter.Stop(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := adapter.Start(t.Context()); !errors.Is(err, errRestartInPlace) {
		t.Fatalf("Start after Stop = %v, want errRestartInPlace", err)
	}
}

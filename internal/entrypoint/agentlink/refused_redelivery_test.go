// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agentlink

import (
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
)

func TestARedeliveryRefusedWhileTheLinkHoldsTheTaskOpenClosesTheTask(t *testing.T) {
	for _, tc := range []struct {
		name     string
		setUp    func(r *rig, c *client)
		userTok  string
		wantText string
	}{
		{name: "SAM stopped the agent", setUp: func(r *rig, c *client) {
			r.stop()
			c.next(typeControl)
		}, userTok: "tok:alice:T1", wantText: msgAgentStopped},
		{name: "the user's token no longer authenticates", setUp: func(*rig, *client) {}, userTok: "expired", wantText: msgAuthFailed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, c := connectedRig(t, nil)
			answers := r.listen(responseTopic("T1"))
			r.taskOn(c, "T1", nil)
			requests, peerID := openWork(t, r, c)
			tc.setUp(r, c)

			acked := r.brk.acks.Load()
			r.brk.redelivering.Store(true)
			r.submit("T1", tc.userTok, nil)
			refusal := recv(t, answers)
			if !strings.Contains(string(refusal.Payload), tc.wantText) {
				t.Fatalf("the refusal is %s, want %q", refusal.Payload, tc.wantText)
			}
			waitFor(t, func() bool { return r.brk.acks.Load() > acked })
			r.locked(func(s *agentState) {
				if b := s.tasks["T1"]; b == nil || !b.final {
					t.Fatal("the refused task's binding is not final: the sweep would answer it a second time")
				}
			})
			cancelSent(t, requests, peerID)
			promptAndPeerCallEnd(t, c)
			r.locked(func(s *agentState) {
				if len(s.asks) != 0 || len(s.subtasks) != 0 {
					t.Fatalf("the closed task still holds %d prompts and %d peer calls", len(s.asks), len(s.subtasks))
				}
			})

			r.adapter.agent(agentName).sweep(time.Now().Add(r.adapter.cfg.TaskTTL + time.Minute))
			none(t, answers)

			c.send(map[string]any{"type": typeResult, "taskId": "T1", "state": stateCompleted, "text": "done"})
			c.refused(errCodeUnknownTask, "T1")
			none(t, answers)
		})
	}
}

func stillAnswerable(t *testing.T, r *rig, c *client, answers <-chan *broker.Message) {
	t.Helper()
	r.locked(func(s *agentState) {
		if b := s.tasks["T1"]; b == nil || b.final {
			t.Fatalf("the task was closed: %+v", b)
		}
	})
	c.send(map[string]any{"type": typeResult, "taskId": "T1", "state": stateCompleted, "text": "done"})
	if m := recv(t, answers); !strings.Contains(string(m.Payload), "done") {
		t.Fatalf("the caller got %s", m.Payload)
	}
}

func TestARefusedRequestThatOnlyCarriesAnOpenTasksIDLeavesTheTaskOpen(t *testing.T) {
	t.Run("another publisher's request with the task's id and its own reply address", func(t *testing.T) {
		r, c := connectedRig(t, nil)
		answers := r.listen(responseTopic("T1"))
		elsewhere := r.listen(responseTopic("elsewhere"))
		r.taskOn(c, "T1", nil)

		r.brk.redelivering.Store(true)
		r.submit("T1", "expired", map[string]string{a2a.PropReplyTo: responseTopic("elsewhere")})
		if m := recv(t, elsewhere); !strings.Contains(string(m.Payload), msgAuthFailed) {
			t.Fatalf("the other publisher got %s", m.Payload)
		}
		none(t, answers)
		stillAnswerable(t, r, c, answers)
	})

	t.Run("a request that was not delivered again", func(t *testing.T) {
		r, c := connectedRig(t, nil)
		answers := r.listen(responseTopic("T1"))
		r.taskOn(c, "T1", nil)

		r.submit("T1", "expired", nil)
		if m := recv(t, answers); !strings.Contains(string(m.Payload), msgAuthFailed) {
			t.Fatalf("the refusal is %s", m.Payload)
		}
		stillAnswerable(t, r, c, answers)
	})

	t.Run("an answer to the task's prompt while SAM has the agent stopped", func(t *testing.T) {
		r, c := connectedRig(t, nil)
		answers := r.listen(responseTopic("T1"))
		r.taskOn(c, "T1", nil)
		c.send(map[string]any{"type": typePrompt, "taskId": "T1", "requestId": "q-1", "questions": []any{map[string]any{"question": "Which policy?"}}})
		waitFor(t, func() bool {
			open := false
			r.locked(func(s *agentState) { open = len(s.asks) == 1 })
			return open
		})
		r.stop()
		c.next(typeControl)

		acked := r.brk.acks.Load()
		r.brk.redelivering.Store(true)
		publishAnswer(t, r, "T1", "alice@example.com", a2a.UserInputResponseData{RequestID: "q-1", Status: "answered",
			Data: map[string]any{"answers": map[string]any{"q0": "Home"}}})
		waitFor(t, func() bool { return r.brk.acks.Load() > acked })
		stillAnswerable(t, r, c, answers)
	})
}

func TestARefusalOfARedeliveryThatCannotBePublishedLeavesTheTaskOpenAndTheRequestInTheQueue(t *testing.T) {
	r, c := connectedRig(t, nil)
	answers := r.listen(responseTopic("T1"))
	r.taskOn(c, "T1", nil)

	nacked := r.brk.nacks.Load()
	r.brk.redelivering.Store(true)
	r.brk.failTopic.Store(responseTopic("T1"))
	r.submit("T1", "expired", nil)
	waitFor(t, func() bool { return r.brk.nacks.Load() > nacked })
	r.brk.failTopic.Store("")
	none(t, answers)
	stillAnswerable(t, r, c, answers)
}

func TestAnAnsweredTasksRequestDeliveredAgainWithABadTokenIsAcknowledgedAndNotRefused(t *testing.T) {
	logs := captureLogs(t)
	r, c := connectedRig(t, nil)
	answers := r.listen(responseTopic("T1"))
	r.taskOn(c, "T1", nil)
	c.send(map[string]any{"type": typeResult, "taskId": "T1", "state": stateCompleted, "text": "done"})
	recv(t, answers)

	acked := r.brk.acks.Load()
	r.brk.redelivering.Store(true)
	r.submit("T1", "expired", nil)
	waitFor(t, func() bool { return r.brk.acks.Load() > acked })
	none(t, answers)
	if n := logs.count("agent link refused a task"); n != 0 {
		t.Fatalf("an answered task's redelivery was refused %d times", n)
	}
}

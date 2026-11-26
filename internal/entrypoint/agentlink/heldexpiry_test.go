// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agentlink

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
)

func TestAHeldFrameChosenForAClosedConnectionReachesAnotherOpenOneWithoutANewJoin(t *testing.T) {
	r, a := connectedRig(t, nil)
	r.taskOn(a, "T1", nil)
	b := r.connect()
	s := r.adapter.agent(agentName)
	var target *conn
	r.locked(func(s *agentState) { target = s.conns[s.tasks["T1"].connID] })

	target.close(websocket.CloseGoingAway, "test")
	waitFor(t, func() bool {
		gone := false
		r.locked(func(s *agentState) { gone = s.conns[target.id] == nil })
		return gone
	})
	s.sendOrHold(target, "T1", answerFrame("q-1", &openPrompt{taskID: "T1", questions: 1}, answerCanceled, nil))

	if f := b.next(typePromptAnswer); f.RequestID != "q-1" {
		t.Fatalf("answer %s", f.Raw)
	}
	waitFor(t, func() bool {
		s.mu.Lock()
		defer s.mu.Unlock()
		return len(s.held) == 0
	})
}

func TestAHeldFrameItsConnectionClaimedAndCouldNotWriteReachesAnotherOpenOne(t *testing.T) {
	r, a := connectedRig(t, nil)
	r.taskOn(a, "T1", nil)
	s := r.adapter.agent(agentName)
	var first *conn
	r.locked(func(s *agentState) { first = s.conns[s.tasks["T1"].connID] })
	claimed, proceed := make(chan struct{}), make(chan struct{})
	var once sync.Once
	s.afterHeldClaim = func(c *conn) {
		if c == first {
			once.Do(func() { close(claimed) })
			<-proceed
		}
	}

	s.sendOrHold(nil, "T1", answerFrame("q-1", &openPrompt{taskID: "T1", questions: 1}, answerCanceled, nil))
	select {
	case <-claimed:
	case <-time.After(arrivalWait):
		t.Fatal("the only open connection never claimed the held frame")
	}
	b := r.connect()
	first.close(websocket.CloseGoingAway, "test")
	close(proceed)

	if f := b.next(typePromptAnswer); f.RequestID != "q-1" {
		t.Fatalf("answer %s", f.Raw)
	}
}

const msgExpiredForCaller = "the agent did not answer before the task's time limit"

func errorAnswer(t *testing.T, m *broker.Message) a2a.Error {
	t.Helper()
	var env struct {
		Error *a2a.Error `json:"error"`
	}
	if err := json.Unmarshal(m.Payload, &env); err != nil || env.Error == nil {
		t.Fatalf("not an error answer %s: %v", m.Payload, err)
	}
	return *env.Error
}

func TestATaskTheAgentNeverAnswersIsAnsweredFailedToItsCallerOnceWhenItExpires(t *testing.T) {
	logs := captureLogs(t)
	r, c := connectedRig(t, nil)
	answers := r.listen(responseTopic("T1"))
	r.taskOn(c, "T1", nil)
	s := r.adapter.agent(agentName)
	past := time.Now().Add(r.adapter.cfg.TaskTTL + time.Minute)

	s.sweep(past)
	m := recv(t, answers)
	if e := errorAnswer(t, m); e.Message != msgExpiredForCaller {
		t.Fatalf("answer %s", m.Payload)
	}
	if m.UserProperties["eventSeq"] != "1" {
		t.Fatalf("the answer is not the first of the task's sequence: %v", m.UserProperties)
	}
	if logs.count("agent link task expired unanswered") != 1 {
		t.Fatalf("expiry logged %d times at WARN", logs.count("agent link task expired unanswered"))
	}

	s.sweep(past.Add(time.Minute))
	none(t, answers)
}

func TestAnExpiredTasksRequestDeliveredAgainIsNotRunAgain(t *testing.T) {
	r, c := connectedRig(t, nil)
	answers := r.listen(responseTopic("T1"))
	r.taskOn(c, "T1", nil)
	s := r.adapter.agent(agentName)

	s.sweep(time.Now().Add(r.adapter.cfg.TaskTTL + time.Minute))
	recv(t, answers)
	r.locked(func(s *agentState) {
		if b := s.tasks["T1"]; b == nil || !b.final {
			t.Fatalf("the expired task is not kept closed: %+v", b)
		}
	})

	r.submit("T1", "tok:alice:T1", nil)
	c.quiet()
	none(t, answers)
}

func TestAnExpiredTaskWhoseAnswerCouldNotBePublishedIsAnsweredByTheNextSweepWithTheSameSequenceNumber(t *testing.T) {
	r, c := connectedRig(t, nil)
	answers := r.listen(responseTopic("T1"))
	r.taskOn(c, "T1", nil)
	s := r.adapter.agent(agentName)
	past := time.Now().Add(r.adapter.cfg.TaskTTL + time.Minute)

	r.brk.failTopic.Store(responseTopic("T1"))
	s.sweep(past)
	none(t, answers)

	r.brk.failTopic.Store("")
	s.sweep(past.Add(time.Minute))
	m := recv(t, answers)
	if e := errorAnswer(t, m); e.Message != msgExpiredForCaller || m.UserProperties["eventSeq"] != "1" {
		t.Fatalf("answer %s %v", m.Payload, m.UserProperties)
	}
	s.sweep(past.Add(2 * time.Minute))
	none(t, answers)
}

func TestStopWarnsHowManyTasksWereStillOpenWhenTheDrainTimedOut(t *testing.T) {
	logs := captureLogs(t)
	r, c := connectedRig(t, nil)
	r.taskOn(c, "T1", nil)
	r.adapter.handlers.Add(1)
	t.Cleanup(r.adapter.handlers.Done)

	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	if err := r.adapter.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	if logs.count("agent link stopped before its work drained") != 1 {
		t.Fatalf("drain timeout warned %d times", logs.count("agent link stopped before its work drained"))
	}
}

type guaranteeRecordingBroker struct {
	*trackingBroker
	mu         sync.Mutex
	guaranteed map[string]int
	direct     map[string]int
}

func (b *guaranteeRecordingBroker) Publish(ctx context.Context, topic string, m *broker.Message) error {
	b.mu.Lock()
	b.direct[topic]++
	b.mu.Unlock()
	return b.trackingBroker.Publish(ctx, topic, m)
}

func (b *guaranteeRecordingBroker) PublishGuaranteed(ctx context.Context, topic string, m *broker.Message) error {
	b.mu.Lock()
	b.guaranteed[topic]++
	b.mu.Unlock()
	return b.trackingBroker.PublishGuaranteed(ctx, topic, m)
}

func (b *guaranteeRecordingBroker) publishes(topic string) (guaranteed, direct int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.guaranteed[topic], b.direct[topic]
}

func TestARefusalIsPublishedGuaranteedAndTheRequestAcknowledged(t *testing.T) {
	var recording *guaranteeRecordingBroker
	r := newRigOn(t, []any{claimsAgent(nil)}, fakeAuth{}, nil, func(b *trackingBroker) broker.Broker {
		recording = &guaranteeRecordingBroker{trackingBroker: b, guaranteed: map[string]int{}, direct: map[string]int{}}
		return recording
	})
	answers := r.listen(responseTopic("T1"))
	r.stop()
	r.submit("T1", "tok:alice:T1", nil)
	if m := recv(t, answers); !strings.Contains(string(m.Payload), msgAgentStopped) {
		t.Fatalf("refusal %s", m.Payload)
	}
	waitFor(t, func() bool { return r.brk.acks.Load() == 1 })
	if guaranteed, direct := recording.publishes(responseTopic("T1")); guaranteed != 1 || direct != 0 {
		t.Fatalf("refusal published guaranteed %d times and direct %d times", guaranteed, direct)
	}
	if r.brk.nacks.Load() != 0 {
		t.Fatalf("nacks %d", r.brk.nacks.Load())
	}
}

func TestARequestWhoseRefusalCouldNotBePublishedIsReturnedToTheQueueNotAcknowledged(t *testing.T) {
	cases := map[string]func(r *rig){
		"stopped": func(r *rig) {
			r.stop()
			r.submit("T1", "tok:alice:T1", nil)
		},
		"authentication failed": func(r *rig) { r.submit("T1", "tok:alice:OTHER-TASK", nil) },
		"unreadable message": func(r *rig) {
			props := map[string]string{a2a.PropReplyTo: responseTopic("T1"), a2a.PropAuthToken: "tok:alice:T1", a2a.PropUserID: "alice@example.com"}
			body := []byte(`{"jsonrpc":"2.0","id":"T1","method":"message/send","params":{}}`)
			if err := r.brk.PublishGuaranteed(r.t.Context(), a2a.AgentRequestTopic(testNS, agentName), &broker.Message{Payload: body, UserProperties: props}); err != nil {
				r.t.Fatal(err)
			}
		},
	}
	for name, submit := range cases {
		t.Run(name, func(t *testing.T) {
			r, _ := connectedRig(t, nil)
			r.brk.failTopic.Store(responseTopic("T1"))
			submit(r)
			waitFor(t, func() bool { return r.brk.nacks.Load() >= 1 })
			if r.brk.acks.Load() != 0 {
				t.Fatalf("acks %d nacks %d", r.brk.acks.Load(), r.brk.nacks.Load())
			}
		})
	}
}

func fullOutboxConn(t *testing.T, r *rig) *conn {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		ws, err := (&websocket.Upgrader{}).Upgrade(w, req, nil)
		if err != nil {
			return
		}
		defer func() { _ = ws.Close() }()
		for {
			if _, _, err := ws.ReadMessage(); err != nil {
				return
			}
		}
	}))
	t.Cleanup(srv.Close)
	ws, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	s := r.adapter.agent(agentName)
	c := newConn(r.adapter, s, ws)
	for range cap(c.out) {
		c.out <- outbound{data: []byte("{}")}
	}
	c.consuming = true
	s.mu.Lock()
	s.conns[c.id] = c
	s.mu.Unlock()
	return c
}

func settleCounts(m **broker.Message, acks, nacks *atomic.Int32) {
	*m = broker.NewDeliveredMessage("m1", "", cancelRequest("T1"), func() { acks.Add(1) }, func() { nacks.Add(1) })
}

func TestACancelThatCouldNotBeSentToAConnectionIsNotLostToAnAcknowledgement(t *testing.T) {
	t.Run("task this replica never delivered", func(t *testing.T) {
		r := newRig(t, nil, nil)
		fullOutboxConn(t, r)
		var m *broker.Message
		var acks, nacks atomic.Int32
		settleCounts(&m, &acks, &nacks)

		r.adapter.agent(agentName).deliverCancel(m, "T1")
		if acks.Load() != 0 || nacks.Load() != 1 {
			t.Fatalf("acks %d nacks %d", acks.Load(), nacks.Load())
		}
	})
	t.Run("task this replica already cancelled", func(t *testing.T) {
		r := newRig(t, nil, nil)
		c := fullOutboxConn(t, r)
		s := r.adapter.agent(agentName)
		s.mu.Lock()
		s.tasks["T1"] = &taskBinding{connID: c.id, cancelled: true, deadline: time.Now().Add(time.Hour)}
		s.mu.Unlock()
		var m *broker.Message
		var acks, nacks atomic.Int32
		settleCounts(&m, &acks, &nacks)

		s.deliverCancel(m, "T1")
		r.locked(func(s *agentState) {
			if len(s.held) != 1 {
				t.Fatalf("%d frames held for the task", len(s.held))
			}
		})
		if acks.Load() != 1 {
			t.Fatalf("acks %d nacks %d", acks.Load(), nacks.Load())
		}
	})
}

// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agentlink

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/observability"
)

const agentQueueMaxRedelivery = 3

func requeueingDevBrokerRig(t *testing.T, agentCfg map[string]any) *rig {
	t.Helper()
	return newRigOn(t, []any{claimsAgent(agentCfg)}, fakeAuth{}, nil,
		func(b *trackingBroker) broker.Broker {
			b.requeuesGiveBacks.Store(true)
			return observability.NewInstrumentedBroker(b)
		})
}

func requireRedelivered(t *testing.T, f received, taskID string) {
	t.Helper()
	if f.TaskID != taskID || !f.Redelivered {
		t.Fatalf("got task %s redelivered=%t, want task %s redelivered=true", f.TaskID, f.Redelivered, taskID)
	}
}

func requireNoNackOnTheDevBroker(t *testing.T, r *rig) {
	t.Helper()
	if n := r.brk.nacks.Load(); n != 0 {
		t.Fatalf("the original delivery was nacked %d times, want 0 because the requeue replaces redelivery", n)
	}
}

func TestATaskSentWhileARequestDrivenAgentSleepsReachesItOnTheEmbeddedDevBroker(t *testing.T) {
	url, wakes := countingRuntime(t)
	r := requeueingDevBrokerRig(t, map[string]any{"wake": map[string]any{"url": url, "on_start": false}})
	r.awaitWakeWatch()
	c := r.connect()
	r.consume(c)
	leaves(t, r, c)

	r.submit("T1", "tok:alice:T1", nil)
	wokenTimes(t, wakes, 1)
	woken := r.connect()
	r.consume(woken)
	requireRedelivered(t, woken.next(typeTask), "T1")
	requireNoNackOnTheDevBroker(t, r)
}

func TestATaskInFlightWhenItsConnectionClosesIsDeliveredToTheNextConnectionOnTheEmbeddedDevBroker(t *testing.T) {
	r := requeueingDevBrokerRig(t, nil)
	c := r.connect()
	r.consume(c)
	r.submit("T1", "tok:alice:T1", nil)
	c.next(typeTask)
	leaves(t, r, c)

	next := r.connect()
	r.consume(next)
	requireRedelivered(t, next.next(typeTask), "T1")
	requireNoNackOnTheDevBroker(t, r)
}

func TestATaskTheClientNacksIsRedeliveredToTheClientOnTheEmbeddedDevBroker(t *testing.T) {
	r := requeueingDevBrokerRig(t, nil)
	c := r.connect()
	r.consume(c)
	r.submit("T1", "tok:alice:T1", nil)
	first := c.next(typeTask)
	if first.Redelivered {
		t.Fatal("the first delivery was marked redelivered")
	}
	c.send(map[string]any{"type": typeNack, "delivery": first.Delivery})

	again := c.next(typeTask)
	requireRedelivered(t, again, "T1")
	c.send(map[string]any{"type": typeAck, "delivery": again.Delivery})
	waitFor(t, func() bool { return r.brk.acks.Load() == 2 })
	requireNoNackOnTheDevBroker(t, r)
}

func requireOneDropLogged(t *testing.T, logs *wakeLogText, msg string, extra ...string) {
	t.Helper()
	var dropped []string
	for _, line := range strings.Split(logs.all(), "\n") {
		if strings.Contains(line, msg) {
			dropped = append(dropped, line)
		}
	}
	if len(dropped) != 1 {
		t.Fatalf("logged %q %d times, want once:\n%s", msg, len(dropped), logs.all())
	}
	for _, field := range append([]string{"level=WARN", "taskID=T1", "method=message/send", "traceID=trace-1"}, extra...) {
		if !strings.Contains(dropped[0], field) {
			t.Fatalf("the drop log lacks %s: %s", field, dropped[0])
		}
	}
}

func TestATaskTheClientKeepsNackingIsDeliveredOnceAndOnlyRedeliveryLimitMoreTimesOnTheEmbeddedDevBroker(t *testing.T) {
	logs := captureAllLogs(t)
	r := requeueingDevBrokerRig(t, nil)
	c := r.connect()
	r.consume(c)
	r.submit("T1", "tok:alice:T1", nil)
	for delivery := range 1 + agentQueueMaxRedelivery {
		f := c.next(typeTask)
		if f.TaskID != "T1" || f.Redelivered != (delivery > 0) {
			t.Fatalf("delivery %d: got task %s redelivered=%t", delivery, f.TaskID, f.Redelivered)
		}
		c.send(map[string]any{"type": typeNack, "delivery": f.Delivery})
	}
	waitFor(t, func() bool { return r.brk.acks.Load() == 4 })
	c.quiet()
	requireNoNackOnTheDevBroker(t, r)
	requireOneDropLogged(t, logs, "agent link dropped a request after its redelivery limit")
}

func TestATaskTheDevBrokerCannotRequeueIsSettledOnceAndLoggedWithItsTrace(t *testing.T) {
	logs := captureAllLogs(t)
	r := requeueingDevBrokerRig(t, nil)
	queueGone := fmt.Errorf("queue gone: %w", broker.ErrQueueNotFound)
	r.brk.requeueErr.Store(&queueGone)
	c := r.connect()
	r.consume(c)
	r.submit("T1", "tok:alice:T1", nil)
	first := c.next(typeTask)
	c.send(map[string]any{"type": typeNack, "delivery": first.Delivery})

	waitFor(t, func() bool { return r.brk.acks.Load() == 1 })
	c.quiet()
	requireNoNackOnTheDevBroker(t, r)
	requireOneDropLogged(t, logs, "agent link could not return a request to its queue", `error="queue gone: `)
}

func TestADroppedCancelIsLoggedWithTheTaskItCancelsAndItsMethod(t *testing.T) {
	payload, err := json.Marshal(a2a.NewRequest("cancel-request-1", methodCancel, map[string]any{"id": "T7"}))
	if err != nil {
		t.Fatal(err)
	}
	cancel := &broker.Message{Payload: payload, UserProperties: map[string]string{a2a.PropTraceID: "trace-7"}}
	fields := map[string]any{}
	logged := givenBackRequestFields(cancel)
	for i := 0; i+1 < len(logged); i += 2 {
		fields[logged[i].(string)] = logged[i+1]
	}
	if fields["taskID"] != "T7" || fields["method"] != methodCancel || fields["traceID"] != "trace-7" {
		t.Fatalf("logged %v, want taskID=T7 method=%s traceID=trace-7", fields, methodCancel)
	}
}

func TestAnUnreadableGivenBackRequestIsLoggedWithoutATaskOrMethod(t *testing.T) {
	logged := givenBackRequestFields(&broker.Message{Payload: []byte("not json")})
	want := []any{"taskID", "", "method", "", "traceID", ""}
	if fmt.Sprint(logged) != fmt.Sprint(want) {
		t.Fatalf("logged %v, want %v", logged, want)
	}
}

func TestATaskTheClientCannotBeSentToIsDeliveredToTheNextConnectionOnTheEmbeddedDevBroker(t *testing.T) {
	r := requeueingDevBrokerRig(t, nil)
	stuffed, _ := observedOutboxConn(t, r)
	s := r.adapter.agent(agentName)
	s.ensureDispatch()
	select {
	case <-s.bound:
	case <-time.After(arrivalWait):
		t.Fatal("the request queue was never bound")
	}
	r.submit("T1", "tok:alice:T1", nil)
	waitFor(t, stuffed.isClosed)

	next := r.connect()
	r.consume(next)
	requireRedelivered(t, next.next(typeTask), "T1")
	requireNoNackOnTheDevBroker(t, r)
}

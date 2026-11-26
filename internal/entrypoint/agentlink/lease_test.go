// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agentlink

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
)

func newRigWithLease(t *testing.T, answer func(read int32) any) (*rig, *atomic.Int32) {
	t.Helper()
	var reads *atomic.Int32
	r := newRigOn(t, []any{claimsAgent(nil)}, fakeAuth{}, nil, func(b *trackingBroker) broker.Broker {
		reads = serveLease(t, b, answer)
		return b
	})
	select {
	case <-r.adapter.agent(agentName).stateReady:
	case <-time.After(arrivalWait):
		t.Fatal("the stop lease was never read")
	}
	return r, reads
}

func TestTheStopLeaseIsReadAtStartAndAppliedOnlyWhenSigned(t *testing.T) {
	for name, tc := range map[string]struct {
		lease   any
		stopped bool
	}{
		"signed stop":   {lease("stopped"), true},
		"forged stop":   {lease("stopped", "forged"), false},
		"unsigned stop": {lease("stopped", ""), false},
		"malformed":     {"stopped", false},
		"unknown state": {lease("paused"), false},
	} {
		t.Run(name, func(t *testing.T) {
			r, _ := newRigWithLease(t, func(int32) any { return tc.lease })
			if got := r.adapter.agent(agentName).isStopped(); got != tc.stopped {
				t.Fatalf("stopped %v, want %v", got, tc.stopped)
			}
		})
	}
}

func TestAStopTheBrokerLostIsAppliedByTheNextSweep(t *testing.T) {
	stoppedLease := make(chan struct{})
	r, _ := newRigWithLease(t, func(int32) any {
		select {
		case <-stoppedLease:
			return lease("stopped")
		default:
			return lease("running")
		}
	})
	s := r.adapter.agent(agentName)
	if s.isStopped() {
		t.Fatal("stopped by a running lease")
	}
	close(stoppedLease)
	r.adapter.sweepOnce(time.Now())
	waitFor(t, s.isStopped)
}

func TestASignedRunningLeaseReleasesAStoppedAgentAndAnUnsignedOneDoesNot(t *testing.T) {
	answers := make(chan any, 2)
	r, _ := newRigWithLease(t, func(read int32) any {
		if read == 1 {
			return nil
		}
		return <-answers
	})
	r.stop()
	s := r.adapter.agent(agentName)

	answers <- lease("running", "forged")
	r.adapter.sweepOnce(time.Now())
	waitFor(t, func() bool { return !s.leaseCheck.Load() && len(answers) == 0 })
	time.Sleep(100 * time.Millisecond)
	if !s.isStopped() {
		t.Fatal("a forged running lease released the agent")
	}
	answers <- lease("running")
	r.adapter.sweepOnce(time.Now())
	waitFor(t, func() bool { return !s.isStopped() })
}

func TestALeaseReadInFlightWhenAReleaseArrivesIsDiscardedAndReadAgain(t *testing.T) {
	inFlight, answer := make(chan struct{}), make(chan struct{})
	r, reads := newRigWithLease(t, func(read int32) any {
		switch read {
		case 1:
			return nil
		case 2:
			close(inFlight)
			<-answer
			return lease("stopped")
		}
		return lease("running")
	})
	r.adapter.controlWait = 5 * time.Second
	r.stop()
	s := r.adapter.agent(agentName)
	r.adapter.sweepOnce(time.Now())
	<-inFlight
	r.adapter.sweepOnce(time.Now())
	time.Sleep(100 * time.Millisecond)
	if reads.Load() != 2 {
		t.Fatalf("%d lease reads while one was in flight", reads.Load())
	}
	r.release()
	close(answer)
	waitFor(t, func() bool { return reads.Load() == 3 && !s.leaseCheck.Load() })
	if s.isStopped() {
		t.Fatal("a lease read before the release stopped the agent again")
	}
}

func TestEveryRefusedControlCommandIsLoggedAsAWarning(t *testing.T) {
	logs := captureLogs(t)
	r := newRig(t, nil, nil)
	topic := a2a.ControlAppCustomTopic(testNS, agentName, "patch", "instances/"+agentName)
	for _, m := range []*broker.Message{
		{Payload: []byte(`{"enabled":false}`)},
		{Payload: []byte(`{"enabled":false}`)},
		{Payload: []byte(`{"enabled":false}`), UserProperties: map[string]string{a2a.PropAuthToken: "forged"}},
		{Payload: []byte(`{"enabled":true}`), UserProperties: map[string]string{a2a.PropAuthToken: "forged"}},
		{Payload: []byte(`{}`)},
		{Payload: []byte(`not json`)},
	} {
		if err := r.brk.Publish(t.Context(), topic, m); err != nil {
			t.Fatal(err)
		}
	}
	waitFor(t, func() bool { return logs.count(msgControlNotSigned) == 4 && logs.count(msgControlMalformed) == 2 })
	if logs.countReason(msgControlNotSigned, statementMissing) != 2 || logs.countReason(msgControlNotSigned, statementRejected) != 2 {
		t.Fatal("a refusal's reason does not say whether the statement was missing or rejected")
	}
	if r.adapter.agent(agentName).isStopped() {
		t.Fatal("a refused control command stopped the agent")
	}
}

func TestARunningLeaseReadJustAfterAStopIsSetAsideUntilTheNextSweep(t *testing.T) {
	r, reads := newRigWithLease(t, func(read int32) any {
		if read == 1 {
			return nil
		}
		return lease("running")
	})
	r.adapter.leaseSettle = 300 * time.Millisecond
	r.stop()
	s := r.adapter.agent(agentName)
	r.adapter.sweepOnce(time.Now())
	waitFor(t, func() bool { return reads.Load() == 2 && !s.leaseCheck.Load() })
	if !s.isStopped() {
		t.Fatal("a running lease read just after a stop released the agent")
	}
	time.Sleep(300 * time.Millisecond)
	r.adapter.sweepOnce(time.Now())
	waitFor(t, func() bool { return !s.isStopped() })
}

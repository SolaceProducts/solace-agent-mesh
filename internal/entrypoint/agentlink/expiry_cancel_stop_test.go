// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agentlink

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
)

func TestADisabledAgentIsNotWokenWhenSAMStopsAndReleasesItAndARequestArrives(t *testing.T) {
	var wakes atomic.Int32
	runtimeSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		wakes.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(runtimeSrv.Close)
	r := newRig(t, map[string]any{"enabled": false, "wake": map[string]any{"url": runtimeSrv.URL}}, nil)

	r.stop()
	waitFor(t, func() bool { return r.brk.receiving.Load() > 0 })
	r.release()
	r.submit("T2", "tok:alice:T2", nil)

	waitFor(t, func() bool { return r.brk.taken.Load() > 0 && r.brk.nacks.Load() > 0 })
	time.Sleep(300 * time.Millisecond)
	if n := wakes.Load(); n != 0 {
		t.Fatalf("a disabled agent was woken %d times", n)
	}
}

func TestAnExpiryThatChoseATaskBeforeItsDeadlineMovedLeavesTheTaskOpenAndAnswerable(t *testing.T) {
	r, c := connectedRig(t, nil)
	answers := r.listen(responseTopic("T1"))
	r.taskOn(c, "T1", nil)
	s := r.adapter.agent(agentName)
	sweepsClock := time.Now().Add(r.adapter.cfg.TaskTTL + time.Minute)

	r.locked(func(s *agentState) { s.tasks["T1"].deadline = sweepsClock.Add(time.Hour) })
	s.expire(t.Context(), "T1", sweepsClock)

	r.locked(func(s *agentState) {
		if b := s.tasks["T1"]; b == nil || b.final {
			t.Fatalf("a task whose deadline had moved was closed (binding present: %v)", b != nil)
		}
	})
	none(t, answers)
	c.send(map[string]any{"type": typeResult, "taskId": "T1", "state": stateCompleted, "text": "done"})
	if task := finalTask(t, recv(t, answers)); task.Status.State != a2a.TaskStateCompleted {
		t.Fatalf("the agent's answer reached the caller as %+v", task)
	}
}

func TestACancelledTaskItsAgentLeavesUnansweredIsAnsweredCancelledAfterTheCancelGraceNotFailedAfterTheTTL(t *testing.T) {
	r, c := connectedRig(t, nil)
	answers := r.listen(responseTopic("T1"))
	r.taskOn(c, "T1", nil)
	s := r.adapter.agent(agentName)

	r.cancel("T1")
	c.next(typeCancel)
	r.locked(func(s *agentState) {
		left := time.Until(s.tasks["T1"].deadline)
		if left > cancelGrace || left < cancelGrace-30*time.Second {
			t.Fatalf("a cancelled task's deadline is %v away, want about the %v cancel grace, not the %v task TTL", left, cancelGrace, r.adapter.cfg.TaskTTL)
		}
	})

	s.sweep(time.Now().Add(cancelGrace + time.Second))
	m := recv(t, answers)
	if task := finalTask(t, m); string(task.Status.State) != "canceled" || !strings.Contains(string(m.Payload), `"state":"canceled"`) {
		t.Fatalf("answer %s", m.Payload)
	}
	s.sweep(time.Now().Add(cancelGrace + 2*time.Minute))
	none(t, answers)
}

func TestStopSendsTheAgentsOpenPeerCallsACancelAndWarnsOnlyWhenTasksWereOpen(t *testing.T) {
	logs := captureLogs(t)
	const warning = "agent link stopping with tasks its agents had not answered"

	t.Run("a task is open", func(t *testing.T) {
		r, c := connectedRig(t, nil)
		r.taskOn(c, "T1", nil)
		requests, peerID := openWork(t, r, c)

		if err := r.adapter.Stop(context.Background()); err != nil {
			t.Fatal(err)
		}
		cancelSent(t, requests, peerID)
		if n := logs.count(warning); n != 1 {
			t.Fatalf("Stop with an open task warned %d times", n)
		}
	})

	t.Run("no task is open", func(t *testing.T) {
		before := logs.count(warning)
		r, c := connectedRig(t, nil)
		r.taskOn(c, "T1", nil)
		c.send(map[string]any{"type": typeResult, "taskId": "T1", "state": stateCompleted, "text": "done"})
		waitFor(t, func() bool { return r.adapter.openTasks() == 0 })

		if err := r.adapter.Stop(context.Background()); err != nil {
			t.Fatal(err)
		}
		if n := logs.count(warning) - before; n != 0 {
			t.Fatalf("Stop with nothing open warned %d times", n)
		}
	})
}

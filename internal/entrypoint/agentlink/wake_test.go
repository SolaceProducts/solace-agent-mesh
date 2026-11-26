// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agentlink

import (
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
)

func leaves(t *testing.T, r *rig, c *client) {
	t.Helper()
	_ = c.ws.Close()
	waitFor(t, func() bool { return len(r.adapter.agent(agentName).connections()) == 0 })
}

func wokenTimes(t *testing.T, wakes *atomic.Int32, want int32) {
	t.Helper()
	waitFor(t, func() bool { return wakes.Load() >= want })
	time.Sleep(300 * time.Millisecond)
	if got := wakes.Load(); got != want {
		t.Fatalf("woken %d times, want %d", got, want)
	}
}

func queueBehind(t *testing.T, r *rig, c *client) {
	t.Helper()
	var deliveries []uint64
	for _, taskID := range []string{"T1", "T2"} {
		r.submit(taskID, "tok:alice:"+taskID, nil)
		deliveries = append(deliveries, c.next(typeTask).Delivery)
	}
	c.send(map[string]any{"type": typeDescribe, "agent": "not an object"})
	c.refused(errCodeBadMessage, "")
	for _, delivery := range deliveries {
		c.send(map[string]any{"type": typeAck, "delivery": delivery})
	}
	waitFor(t, func() bool { return r.brk.acks.Load() == 2 && r.brk.watched.Load() == 2 })
}

func TestARequestTakenBeforeTheWakeWatcherSeesItDoesNotWakeTheAgentAgain(t *testing.T) {
	for name, take := range map[string]func(t *testing.T, r *rig, c *client){
		"task": func(t *testing.T, r *rig, c *client) {
			r.taskOn(c, "T1", nil)
			c.send(map[string]any{"type": typeResult, "taskId": "T1", "state": stateCompleted, "text": "done"})
		},
		"unreadable request": func(t *testing.T, r *rig, _ *client) {
			if err := r.brk.PublishGuaranteed(t.Context(), a2a.AgentRequestTopic(testNS, agentName),
				&broker.Message{Payload: []byte("not a request")}); err != nil {
				t.Fatal(err)
			}
			waitFor(t, func() bool { return r.brk.acks.Load() == 1 })
		},
	} {
		t.Run(name, func(t *testing.T) {
			r, wakes := wakeRig(t)
			c := r.connect()
			r.consume(c)
			release := r.holdWatcherCopies()
			take(t, r, c)
			release()
			waitFor(t, func() bool { return r.brk.watched.Load() == 1 })
			leaves(t, r, c)
			wokenTimes(t, wakes, 0)
		})
	}
}

func TestARequestTheWakeWatcherSawBeforeItWasTakenDoesNotWakeTheAgentAgain(t *testing.T) {
	r, wakes := wakeRig(t)
	c := r.connect()
	r.consume(c)
	var deliveries []uint64
	for _, taskID := range []string{"T1", "T2"} {
		r.submit(taskID, "tok:alice:"+taskID, nil)
		deliveries = append(deliveries, c.next(typeTask).Delivery)
	}
	r.submit("T3", "tok:alice:T3", nil)
	waitFor(t, func() bool { return r.brk.watched.Load() == 3 })
	for _, delivery := range deliveries {
		c.send(map[string]any{"type": typeAck, "delivery": delivery})
	}
	f := c.next(typeTask)
	if f.TaskID != "T3" {
		t.Fatalf("%s", f.Raw)
	}
	c.send(map[string]any{"type": typeAck, "delivery": f.Delivery})
	waitFor(t, func() bool { return r.brk.acks.Load() == 3 })
	leaves(t, r, c)
	wokenTimes(t, wakes, 0)
}

func TestARequestLeftInTheQueueWhenTheAgentLeavesWakesItAgain(t *testing.T) {
	r, wakes := wakeRig(t)
	c := r.connect()
	r.consume(c)
	queueBehind(t, r, c)
	r.submit("T3", "tok:alice:T3", nil)
	waitFor(t, func() bool { return r.brk.watched.Load() == 3 })
	leaves(t, r, c)
	wokenTimes(t, wakes, 1)
}

func TestATaskGivenBackWhoseWakeWatcherCopyComesLateWakesTheAgentOnce(t *testing.T) {
	r, wakes := wakeRig(t)
	c := r.connect()
	r.consume(c)
	release := r.holdWatcherCopies()
	r.submit("T1", "tok:alice:T1", nil)
	c.next(typeTask)
	leaves(t, r, c)
	wokenTimes(t, wakes, 1)
	release()
	waitFor(t, func() bool { return r.brk.watched.Load() == 1 })
	c = r.connect()
	r.consume(c)
	leaves(t, r, c)
	wokenTimes(t, wakes, 1)
}

func TestAnAgentThatGivesATaskBackAndLeavesIsWokenAgain(t *testing.T) {
	r, wakes := wakeRig(t)
	c := r.connect()
	r.consume(c)
	r.submit("T1", "tok:alice:T1", nil)
	f := c.next(typeTask)
	c.send(map[string]any{"type": typeNack, "delivery": f.Delivery})
	waitFor(t, func() bool { return r.brk.nacks.Load() == 1 && r.brk.watched.Load() == 1 })
	leaves(t, r, c)
	wokenTimes(t, wakes, 1)
}

func TestAConnectionLeavingWhileAnotherConsumesWakesNothingAndItsWorkIsKeptForTheLast(t *testing.T) {
	r, wakes := wakeRig(t)
	c1, c2 := r.connect(), r.connect()
	r.consume(c1)
	r.consume(c2)
	r.submit("T1", "tok:alice:T1", nil)
	holder, _ := nextOf(t, typeTask, c1, c2)
	other := c1
	if holder == c1 {
		other = c2
	}
	waitFor(t, func() bool { return r.brk.watched.Load() == 1 })
	_ = holder.ws.Close()
	waitFor(t, func() bool { return len(r.adapter.agent(agentName).connections()) == 1 && r.brk.nacks.Load() == 1 })
	wokenTimes(t, wakes, 0)
	leaves(t, r, other)
	wokenTimes(t, wakes, 1)
}

func TestAWokenAgentThatFindsNothingIsNotWokenAgainForTheSameRequest(t *testing.T) {
	r, wakes := wakeRig(t)
	c := r.connect()
	r.consume(c)
	r.submit("T1", "tok:alice:T1", nil)
	c.next(typeTask)
	waitFor(t, func() bool { return r.brk.watched.Load() == 1 })
	leaves(t, r, c)
	wokenTimes(t, wakes, 1)
	c = r.connect()
	r.consume(c)
	leaves(t, r, c)
	wokenTimes(t, wakes, 1)
}

func TestAFailedWakeIsTriedAgain(t *testing.T) {
	refuseFirst := func(wake int32) int {
		if wake == 1 {
			return http.StatusServiceUnavailable
		}
		return http.StatusOK
	}
	wakeFailed := func(r *rig) func() bool {
		return func() bool {
			var failed bool
			r.locked(func(s *agentState) { failed = s.wakeFailed })
			return failed
		}
	}
	t.Run("at the next copy the watcher sees, even of a request already taken", func(t *testing.T) {
		r, wakes := wakeRigAnswering(t, refuseFirst)
		c := r.connect()
		r.consume(c)
		release := r.holdWatcherCopies()
		r.submit("T1", "tok:alice:T1", nil)
		c.next(typeTask)
		leaves(t, r, c)
		waitFor(t, wakeFailed(r))
		release()
		waitFor(t, func() bool { return r.brk.watched.Load() == 1 })
		wokenTimes(t, wakes, 2)
	})
	t.Run("at the agent's next departure", func(t *testing.T) {
		r, wakes := wakeRigAnswering(t, refuseFirst)
		idle, c := r.connect(), r.connect()
		r.consume(c)
		r.submit("T1", "tok:alice:T1", nil)
		c.next(typeTask)
		waitFor(t, func() bool { return r.brk.watched.Load() == 1 })
		_ = c.ws.Close()
		waitFor(t, func() bool { return len(r.adapter.agent(agentName).connections()) == 1 })
		waitFor(t, wakeFailed(r))
		leaves(t, r, idle)
		wokenTimes(t, wakes, 2)
		c = r.connect()
		r.consume(c)
		leaves(t, r, c)
		wokenTimes(t, wakes, 2)
	})
	t.Run("but not once the agent has connected", func(t *testing.T) {
		r, wakes := wakeRigAnswering(t, refuseFirst)
		c := r.connect()
		r.consume(c)
		r.submit("T1", "tok:alice:T1", nil)
		c.next(typeTask)
		waitFor(t, func() bool { return r.brk.watched.Load() == 1 })
		leaves(t, r, c)
		waitFor(t, wakeFailed(r))
		c = r.connect()
		r.consume(c)
		leaves(t, r, c)
		wokenTimes(t, wakes, 1)
	})
}

func TestAWakeWatcherCopyArrivingAfterTheAgentTookItsRequestAndLeftWakesNothing(t *testing.T) {
	r, wakes := wakeRig(t)
	c := r.connect()
	r.consume(c)
	release := r.holdWatcherCopies()
	r.taskOn(c, "T1", nil)
	c.send(map[string]any{"type": typeResult, "taskId": "T1", "state": stateCompleted, "text": "done"})
	leaves(t, r, c)
	release()
	waitFor(t, func() bool { return r.brk.watched.Load() == 1 })
	wokenTimes(t, wakes, 0)
}

func TestACancelTheLinkAlreadyAppliedDoesNotWakeTheAgentAgain(t *testing.T) {
	r, wakes := wakeRig(t)
	c := r.connect()
	r.consume(c)
	queueBehind(t, r, c)
	r.cancel("T1")
	if f := c.next(typeCancel); f.TaskID != "T1" {
		t.Fatalf("%s", f.Raw)
	}
	waitFor(t, func() bool { return r.brk.watched.Load() == 3 })
	leaves(t, r, c)
	wokenTimes(t, wakes, 0)
}

func TestACancelLeftInTheQueueThatTheLinkHasNotAppliedWakesTheAgentAgain(t *testing.T) {
	for name, cancel := range map[string]func(r *rig){
		"its direct copy was lost": func(r *rig) {
			r.brk.loseRequestCopies.Store(true)
			r.cancel("T1")
		},
		"for a task this replica does not hold": func(r *rig) { r.cancel("T9") },
	} {
		t.Run(name, func(t *testing.T) {
			r, wakes := wakeRig(t)
			c := r.connect()
			r.consume(c)
			queueBehind(t, r, c)
			cancel(r)
			waitFor(t, func() bool { return r.brk.watched.Load() == 3 })
			r.locked(func(s *agentState) {
				if s.tasks["T1"].cancelled {
					t.Fatal("the cancel was applied before the agent took it")
				}
			})
			leaves(t, r, c)
			wokenTimes(t, wakes, 1)
		})
	}
}

func leftWithTheQueueBound(t *testing.T, r *rig, whileConnected func(c *client) int32) {
	t.Helper()
	c := r.connect()
	r.consume(c)
	r.taskOn(c, "T1", nil)
	requests := 1 + whileConnected(c)
	waitFor(t, func() bool { return r.brk.acks.Load() == requests && r.brk.watched.Load() == requests })
	waitFor(t, func() bool { return r.brk.receiving.Load() > 0 })
	leaves(t, r, c)
	r.holdWatcherCopies()
}

func TestACancelForATaskThisReplicaDoesNotHoldTakenAfterTheAgentLeftWakesIt(t *testing.T) {
	r, wakes := wakeRig(t)
	leftWithTheQueueBound(t, r, func(*client) int32 { return 0 })
	r.cancel("T9")
	waitFor(t, func() bool { return r.brk.taken.Load() == 2 })
	wokenTimes(t, wakes, 1)
}

func TestACancelledTaskWhoseAnswerCouldNotBePublishedAfterTheAgentLeftWakesIt(t *testing.T) {
	r, wakes := wakeRig(t)
	leftWithTheQueueBound(t, r, func(c *client) int32 {
		r.cancel("T1")
		c.next(typeCancel)
		return 1
	})
	r.brk.failTopic.Store(responseTopic("T1"))
	r.submit("T1", "tok:alice:T1", nil)
	waitFor(t, func() bool { return r.brk.taken.Load() == 3 })
	wokenTimes(t, wakes, 1)
}

func heldFirstWake(t *testing.T) (status func(int32) int, answer func(), answered <-chan struct{}) {
	t.Helper()
	release, done := make(chan struct{}), make(chan struct{})
	var once sync.Once
	answer = func() { once.Do(func() { close(release) }) }
	status = func(wake int32) int {
		if wake != 1 {
			return http.StatusOK
		}
		select {
		case <-release:
		case <-t.Context().Done():
		}
		close(done)
		return http.StatusGatewayTimeout
	}
	return status, answer, done
}

func afterTheAnswer(answered <-chan struct{}) {
	<-answered
	time.Sleep(300 * time.Millisecond)
}

func servedT1(r *rig) *client {
	c := r.connect()
	r.consume(c)
	task := c.next(typeTask)
	c.send(map[string]any{"type": typeAck, "delivery": task.Delivery})
	c.send(map[string]any{"type": typeResult, "taskId": "T1", "state": stateCompleted, "text": "done"})
	return c
}

func TestAWakeCallThatFailsAfterTheAgentConnectedWakesNothing(t *testing.T) {
	t.Run("while it serves", func(t *testing.T) {
		status, answer, answered := heldFirstWake(t)
		r, wakes := wakeRigAnswering(t, status)
		r.submit("T1", "tok:alice:T1", nil)
		waitFor(t, func() bool { return wakes.Load() == 1 })
		c := servedT1(r)
		answer()
		afterTheAnswer(answered)
		leaves(t, r, c)
		wokenTimes(t, wakes, 1)
		c = r.connect()
		r.consume(c)
		leaves(t, r, c)
		wokenTimes(t, wakes, 1)
	})
	t.Run("after it left, while a connection that does not consume stays", func(t *testing.T) {
		status, answer, answered := heldFirstWake(t)
		r, wakes := wakeRigAnswering(t, status)
		r.submit("T1", "tok:alice:T1", nil)
		waitFor(t, func() bool { return wakes.Load() == 1 })
		idle := r.connect()
		c := servedT1(r)
		waitFor(t, func() bool { return r.brk.acks.Load() == 1 })
		_ = c.ws.Close()
		waitFor(t, func() bool { return len(r.adapter.agent(agentName).connections()) == 1 })
		answer()
		afterTheAnswer(answered)
		leaves(t, r, idle)
		wokenTimes(t, wakes, 1)
	})
}

func TestARequestBetweenTheWokenAgentsConnectAndConsumeWhoseWakeIsRefusedWakesNothingMore(t *testing.T) {
	r, wakes := wakeRigAnswering(t, func(wake int32) int {
		if wake == 2 {
			return http.StatusTooManyRequests
		}
		return http.StatusOK
	})
	r.submit("T1", "tok:alice:T1", nil)
	waitFor(t, func() bool { return wakes.Load() == 1 })
	c := r.connect()
	r.submit("T2", "tok:alice:T2", nil)
	waitFor(t, func() bool { return r.brk.watched.Load() == 2 })
	waitFor(t, func() bool {
		var failed bool
		r.locked(func(s *agentState) { failed = s.wakeFailed })
		return failed
	})
	r.consume(c)
	for range 2 {
		f := c.next(typeTask)
		c.send(map[string]any{"type": typeAck, "delivery": f.Delivery})
		c.send(map[string]any{"type": typeResult, "taskId": f.TaskID, "state": stateCompleted, "text": "done"})
	}
	waitFor(t, func() bool { return r.brk.acks.Load() == 2 })
	leaves(t, r, c)
	wokenTimes(t, wakes, 2)
}

func TestAnOlderWakeCallFailingLateLeavesANewerWakeAwaitingItsConnection(t *testing.T) {
	status, answer, answered := heldFirstWake(t)
	r, wakes := wakeRigAnswering(t, status)
	r.submit("T1", "tok:alice:T1", nil)
	waitFor(t, func() bool { return wakes.Load() == 1 })
	c := r.connect()
	r.consume(c)
	c.next(typeTask)
	waitFor(t, func() bool { return r.brk.watched.Load() == 1 })
	leaves(t, r, c)
	wokenTimes(t, wakes, 2)
	answer()
	afterTheAnswer(answered)
	r.locked(func(s *agentState) {
		if !s.awaitingConnect || s.wakeFailed {
			t.Fatalf("the older wake's failure changed the newer one: awaiting %v, failed %v", s.awaitingConnect, s.wakeFailed)
		}
	})
	r.submit("T2", "tok:alice:T2", nil)
	waitFor(t, func() bool { return r.brk.watched.Load() == 2 })
	wokenTimes(t, wakes, 2)
}

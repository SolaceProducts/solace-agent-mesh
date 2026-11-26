// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agentlink

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
)

func (r *rig) onlyServerConn() *conn {
	r.t.Helper()
	conns := r.adapter.agent(agentName).connections()
	if len(conns) != 1 {
		r.t.Fatalf("the agent has %d connections, want the one", len(conns))
	}
	return conns[0]
}

func linesLogged(logs *wakeLogText, message string) []string {
	var lines []string
	for _, line := range strings.Split(logs.all(), "\n") {
		if strings.Contains(line, `msg="`+message+`"`) {
			lines = append(lines, line)
		}
	}
	return lines
}

func theOneLineLogged(t *testing.T, logs *wakeLogText, message string, fields ...string) string {
	t.Helper()
	lines := linesLogged(logs, message)
	if len(lines) != 1 {
		t.Fatalf("logged %q %d times, want once:\n%s", message, len(lines), logs.all())
	}
	for _, field := range fields {
		if !strings.Contains(lines[0], field) {
			t.Fatalf("the log lacks %s: %s", field, lines[0])
		}
	}
	return lines[0]
}

func (r *replica) requestLost(taskID string) (lost bool) {
	r.locked(func(s *agentState) {
		b := s.tasks[taskID]
		lost = b != nil && b.requestLost
	})
	return lost
}

func nack(f received) map[string]any {
	return map[string]any{"type": typeNack, "delivery": f.Delivery}
}

func (r *replica) stopsDeliveringTo(c *client) {
	r.t.Helper()
	c.send(map[string]any{"type": typeDescribe, "agent": "not an object"})
	c.refused(errCodeBadMessage, "")
}

func tunedWaits(settleWait, publishWait time.Duration) func(*Adapter) {
	return func(adapter *Adapter) {
		adapter.settleWait = settleWait
		if publishWait > 0 {
			adapter.publishWait = publishWait
		}
	}
}

func resultStalledPastTheStandDown(t *testing.T, a *replica, taskID string) (term *ledTerm, answers <-chan *broker.Message) {
	t.Helper()
	term = a.lead(1)
	c := a.connect()
	a.consume(c)
	answers = a.listen(responseTopic(taskID))
	a.lateAckedTask(c, taskID)
	a.link.repliesStall.Store(true)
	c.send(completed(taskID))
	waitFor(t, func() bool {
		_, publishing := a.binding(taskID)
		return publishing
	})

	term.end()
	if _, stillPublishing := a.binding(taskID); !stillPublishing {
		t.Fatal("the result's publish ended before the stand-down did, so the stand-down never went on without it")
	}
	waitFor(t, func() bool {
		bound, final := a.binding(taskID)
		return bound && !final
	})
	none(t, answers)
	a.link.repliesStall.Store(false)
	return term, answers
}

func TestAResultWhosePublishOutlastsTheStandDownIsFailedOnceByTheNextSweep(t *testing.T) {
	onBothBrokerKinds(t, func(t *testing.T, releasesBinds bool) {
		a, b := newReplicas(t, replicasPlan{releasesBinds: releasesBinds, tune: tunedWaits(200*time.Millisecond, 3*time.Second)})
		_, answers := resultStalledPastTheStandDown(t, a, "T1")

		a.nextSweep()
		failedWith(t, recv(t, answers), msgLinkMoved)
		b.lead(2)
		b.reconnects().quiet()
		a.nextSweep()
		a.sweepPastTheTaskTimeLimit()
		none(t, answers)
	})
}

func TestATaskLeftForTheSweepIsFailedOnceByTheNextStandDownWhenTheReplicaLedAgainFirst(t *testing.T) {
	a, _ := newReplicas(t, replicasPlan{tune: tunedWaits(200*time.Millisecond, 3*time.Second)})
	_, answers := resultStalledPastTheStandDown(t, a, "T1")

	a.lead(2).end()
	failedWith(t, recv(t, answers), msgLinkMoved)
	a.nextSweep()
	a.sweepPastTheTaskTimeLimit()
	none(t, answers)
}

func TestAFailureNotPublishedAtStandDownIsPublishedOnceByTheSweepAfterTheReplicaLeadsAgain(t *testing.T) {
	a, _ := newReplicas(t, replicasPlan{})
	term := a.lead(1)
	c := a.connect()
	a.consume(c)
	answers := a.listen(responseTopic("T1"))
	a.acknowledgedTask(c, "T1")

	a.brk.failTopic.Store(responseTopic("T1"))
	term.end()
	none(t, answers)
	a.lead(2)
	again := a.reconnects()
	a.brk.failTopic.Store("")
	a.nextSweep()
	failedWith(t, recv(t, answers), msgLinkMoved)
	again.send(completed("T1"))
	again.refused(errCodeUnknownTask, "T1")
	a.nextSweep()
	a.sweepPastTheTaskTimeLimit()
	none(t, answers)
}

type steppedClock struct {
	start   time.Time
	stepped atomic.Int64
}

func newSteppedClock() *steppedClock { return &steppedClock{start: time.Now()} }

func (c *steppedClock) now() time.Time { return c.start.Add(time.Duration(c.stepped.Load())) }

func (c *steppedClock) advance(by time.Duration) { c.stepped.Add(int64(by)) }

func (c *steppedClock) runs(adapter *Adapter) { adapter.now = c.now }

func (r *replica) sweepsAfter(clock *steppedClock, later time.Duration) {
	clock.advance(later)
	r.adapter.agent(agentName).sweep(clock.now().Add(time.Second))
}

func acknowledgedTaskWhoseAnswerCannotBePublished(t *testing.T, a *replica) (term *ledTerm, answers <-chan *broker.Message) {
	t.Helper()
	term = a.lead(1)
	c := a.connect()
	a.consume(c)
	answers = a.listen(responseTopic("T1"))
	a.acknowledgedTask(c, "T1")
	a.brk.failTopic.Store(responseTopic("T1"))
	return term, answers
}

func requireStillLeftForTheSweep(t *testing.T, a *replica, taskID, when string) {
	t.Helper()
	if bound, final := a.binding(taskID); !bound || final {
		t.Fatalf("%s the task is bound=%t final=%t, want it kept open for the sweep", when, bound, final)
	}
}

func TestAnAnswerThatCannotBePublishedForThirtyMinutesIsGivenUpWithOneError(t *testing.T) {
	logs := captureAllLogs(t)
	clock := newSteppedClock()
	a, _ := newReplicas(t, replicasPlan{tune: clock.runs})
	term, answers := acknowledgedTaskWhoseAnswerCannotBePublished(t, a)

	term.end()
	a.sweepsAfter(clock, 10*time.Minute)
	a.sweepsAfter(clock, 19*time.Minute)
	requireStillLeftForTheSweep(t, a, "T1", "29 minutes into the outage")
	failedAttempts := linesLogged(logs, publishOfATaskFailureFailed)
	if len(failedAttempts) != 3 {
		t.Fatalf("%d failed attempts were logged, want the stand-down's and one for each of the 2 sweeps:\n%s", len(failedAttempts), logs.all())
	}
	for _, field := range []string{"level=WARN", "taskID=T1", "traceID=trace-1", `answerKind="link moved"`, "failing_for_ms=1740000", "gives_up_after_ms=1800000"} {
		if !strings.Contains(failedAttempts[2], field) {
			t.Fatalf("the failed attempt's log lacks %s: %s", field, failedAttempts[2])
		}
	}

	a.sweepsAfter(clock, time.Minute)
	if bound, _ := a.binding("T1"); bound {
		t.Fatal("the task is still bound 30 minutes into the outage, want it dropped")
	}
	loggedOnceWithItsIDs(t, logs, "ERROR", gaveUpAnsweringATask, "T1")
	theOneLineLogged(t, logs, gaveUpAnsweringATask, `answerKind="link moved"`, "failing_for_ms=1800000", "gives_up_after_ms=1800000")
	if warned := len(linesLogged(logs, publishOfATaskFailureFailed)); warned != 3 {
		t.Fatalf("%d failed attempts were logged at WARN, want the last one logged as the ERROR only", warned)
	}
	if said := len(linesLogged(logs, failedByStandDown.logged)); said != 0 {
		t.Fatalf("the task was logged as failed %d times though its answer was never published", said)
	}

	a.brk.failTopic.Store("")
	a.sweepsAfter(clock, time.Minute)
	none(t, answers)
}

func TestAnOutageOfManySweepsFollowedByARecoveryAnswersTheCallerOnce(t *testing.T) {
	logs := captureAllLogs(t)
	clock := newSteppedClock()
	a, _ := newReplicas(t, replicasPlan{tune: clock.runs})
	term, answers := acknowledgedTaskWhoseAnswerCannotBePublished(t, a)

	term.end()
	for range 12 {
		a.sweepsAfter(clock, sweepInterval)
	}
	requireStillLeftForTheSweep(t, a, "T1", "after twelve sweeps of an outage")
	none(t, answers)

	a.brk.failTopic.Store("")
	a.sweepsAfter(clock, sweepInterval)
	failedWith(t, recv(t, answers), msgLinkMoved)
	a.sweepsAfter(clock, sweepInterval)
	none(t, answers)
	if gaveUp := len(linesLogged(logs, gaveUpAnsweringATask)); gaveUp != 0 {
		t.Fatalf("the answer was given up on %d times during a 12 minute outage", gaveUp)
	}
}

func TestStandDownsAloneNeverGiveUpAnAnswerHoweverLongItHasFailed(t *testing.T) {
	logs := captureAllLogs(t)
	clock := newSteppedClock()
	a, _ := newReplicas(t, replicasPlan{tune: clock.runs})
	term, answers := acknowledgedTaskWhoseAnswerCannotBePublished(t, a)

	term.end()
	for number := uint64(2); number <= 5; number++ {
		clock.advance(answerGiveUpAfter)
		a.lead(number).end()
	}
	requireStillLeftForTheSweep(t, a, "T1", "after five stand-downs across two hours")
	if gaveUp := len(linesLogged(logs, gaveUpAnsweringATask)); gaveUp != 0 {
		t.Fatalf("a stand-down gave up on the answer %d times, want only a sweep to", gaveUp)
	}

	a.brk.failTopic.Store("")
	a.sweepsAfter(clock, sweepInterval)
	failedWith(t, recv(t, answers), msgLinkMoved)
	a.sweepsAfter(clock, sweepInterval)
	none(t, answers)
}

func TestAnUnpublishedAnswerIsTriedForThirtyMinutes(t *testing.T) {
	if answerGiveUpAfter != 30*time.Minute {
		t.Fatalf("an unpublished answer is given up on after %v, want 30 minutes", answerGiveUpAfter)
	}
}

func TestATaskBoundAgainStartsTheGiveUpClockOfItsAnswerAfresh(t *testing.T) {
	clock := newSteppedClock()
	a, _ := newReplicas(t, replicasPlan{tune: clock.runs})
	first, answers := acknowledgedTaskWhoseAnswerCannotBePublished(t, a)
	first.end()

	clock.advance(20 * time.Minute)
	second := a.lead(2)
	a.acknowledgedTask(a.reconnects(), "T1")
	second.end()
	a.sweepsAfter(clock, 20*time.Minute)
	requireStillLeftForTheSweep(t, a, "T1", "20 minutes after the answer of its second run first failed")

	a.brk.failTopic.Store("")
	a.sweepsAfter(clock, sweepInterval)
	failedWith(t, recv(t, answers), msgLinkMoved)
	a.sweepsAfter(clock, sweepInterval)
	none(t, answers)
}

func TestACancelledTasksAnswerThatCannotBePublishedIsGivenUpAtTheSameWindow(t *testing.T) {
	logs := captureAllLogs(t)
	clock := newSteppedClock()
	a, _ := newReplicas(t, replicasPlan{tune: clock.runs})
	term := a.lead(1)
	c := a.connect()
	a.consume(c)
	answers := a.listen(responseTopic("T1"))
	a.acknowledgedTask(c, "T1")
	a.cancel("T1")
	c.next(typeCancel)

	a.brk.failTopic.Store(responseTopic("T1"))
	term.end()
	a.sweepsAfter(clock, answerGiveUpAfter-time.Minute)
	requireStillLeftForTheSweep(t, a, "T1", "a minute before the window ends")
	a.sweepsAfter(clock, time.Minute)
	if bound, _ := a.binding("T1"); bound {
		t.Fatal("the cancelled task is still bound after its answer failed for the whole window, want it dropped")
	}
	loggedOnceWithItsIDs(t, logs, "ERROR", gaveUpAnsweringATask, "T1")
	theOneLineLogged(t, logs, gaveUpAnsweringATask, "answerKind=canceled", "failing_for_ms=1800000")

	a.brk.failTopic.Store("")
	a.sweepsAfter(clock, sweepInterval)
	none(t, answers)
}

func TestATaskLeftForTheSweepAndBoundAgainHasItsFullTimeLimitFromThatDelivery(t *testing.T) {
	clock := newSteppedClock()
	a, _ := newReplicas(t, replicasPlan{tune: clock.runs})
	first, answers := acknowledgedTaskWhoseAnswerCannotBePublished(t, a)
	first.end()
	a.brk.failTopic.Store("")

	clock.advance(time.Hour)
	a.lead(2)
	a.acknowledgedTask(a.reconnects(), "T1")
	a.sweepsAfter(clock, a.adapter.cfg.TaskTTL-time.Minute)
	none(t, answers)
	a.sweepsAfter(clock, 2*time.Minute)
	failedWith(t, recv(t, answers), msgTaskExpired)
}

func TestATaskBoundAgainWithNothingLeftForTheSweepKeepsItsFirstTimeLimit(t *testing.T) {
	clock := newSteppedClock()
	a, _ := newReplicas(t, replicasPlan{tune: clock.runs})
	a.lead(1)
	c := a.connect()
	a.consume(c)
	answers := a.listen(responseTopic("T1"))
	a.acknowledgedTask(c, "T1")

	clock.advance(time.Hour)
	a.acknowledgedTask(c, "T1")
	a.sweepsAfter(clock, a.adapter.cfg.TaskTTL-time.Hour+time.Minute)
	failedWith(t, recv(t, answers), msgTaskExpired)
}

func TestAFreshRunOfATaskAStandDownOnceLeftForTheSweepMaySendItsResultAgain(t *testing.T) {
	a, _ := newReplicas(t, replicasPlan{tune: tunedWaits(200*time.Millisecond, 3*time.Second)})
	_, answers := resultStalledPastTheStandDown(t, a, "T1")

	a.lead(2)
	again := a.reconnects()
	a.lateAckedTask(again, "T1")
	a.brk.failTopic.Store(responseTopic("T1"))
	again.send(completed("T1"))
	again.refused(errCodePublishFailed, "T1")
	a.nextSweep()
	none(t, answers)

	a.brk.failTopic.Store("")
	again.send(completed("T1"))
	completedOnce(t, answers)
	a.nextSweep()
	a.sweepPastTheTaskTimeLimit()
	none(t, answers)
}

func refusalStillBeingPublished(t *testing.T, a, b *replica) (term *ledTerm, answers <-chan *broker.Message, refusing *gate) {
	t.Helper()
	term = a.lead(1)
	c := a.connect()
	a.consume(c)
	answers = a.listen(responseTopic("T1"))
	a.lateAckedTask(c, "T1")
	refusing = a.link.holdPublishesTo(t, responseTopic("T1"))
	leaves(t, a.rig, c)
	a.stop()
	waitFor(t, func() bool { return b.adapter.agent(agentName).isStopped() })
	refusing.awaitArrival(t)
	return term, answers, refusing
}

var errQueueFull = errors.New("the queue is full")

func TestARefusalLeftByAStandDownWhoseRequestThenCannotGoBackIsFailedByTheNextSweep(t *testing.T) {
	a, b := newReplicas(t, replicasPlan{tune: tunedWaits(200*time.Millisecond, 0)})
	term, answers, refusing := refusalStillBeingPublished(t, a, b)

	term.end()
	waitFor(t, func() (passedOverAndSettled bool) {
		a.locked(func(s *agentState) {
			held := s.tasks["T1"]
			passedOverAndSettled = !s.settling && held != nil && held.passedOverAsAnswered
		})
		return passedOverAndSettled
	})
	a.brk.requeueErr.Store(&errQueueFull)
	refusing.openFailing(errors.New("the broker refused the publish"))
	waitFor(t, func() bool { return a.requestLost("T1") })
	a.nextSweep()
	failedWith(t, recv(t, answers), msgRequestNotBack)
	a.nextSweep()
	none(t, answers)
}

func TestARefusalWhoseRequestCannotGoBackWhileItsStandDownStillSettlesIsFailedAsLostByTheNextSweep(t *testing.T) {
	a, b := newReplicas(t, replicasPlan{tune: tunedWaits(200*time.Millisecond, 0)})
	term, answers, refusing := refusalStillBeingPublished(t, a, b)
	s := a.adapter.agent(agentName)
	s.cardMu.Lock()
	letTheStandDownEnd := sync.OnceFunc(s.cardMu.Unlock)
	t.Cleanup(letTheStandDownEnd)

	term.cancel()
	leftForTheSweep := func(passedOver bool) func() bool {
		return func() (so bool) {
			a.locked(func(s *agentState) {
				held := s.tasks["T1"]
				so = held != nil && ((passedOver && held.passedOverAsAnswered) || (!passedOver && held.unanswered != nil))
			})
			return so
		}
	}
	waitFor(t, leftForTheSweep(true))
	a.brk.requeueErr.Store(&errQueueFull)
	refusing.openFailing(errors.New("the broker refused the publish"))
	waitFor(t, leftForTheSweep(false))
	letTheStandDownEnd()
	term.endsWithin(standDownWithNothingToWaitFor)
	a.nextSweep()
	failedWith(t, recv(t, answers), msgRequestLost)
	a.nextSweep()
	none(t, answers)
}

func TestARefusalStillBeingPublishedWhenAStandDownGoesOnLeavesItsTaskToTheNextLeader(t *testing.T) {
	a, b := newReplicas(t, replicasPlan{tune: tunedWaits(200*time.Millisecond, 0)})
	term, answers, refusing := refusalStillBeingPublished(t, a, b)

	term.end()
	refusing.openFailing(errors.New("the broker refused the publish"))
	waitFor(t, func() bool {
		bound, _ := a.binding("T1")
		return !bound
	})
	a.nextSweep()
	none(t, answers)

	b.lead(2)
	if refusal := recv(t, answers); !strings.Contains(string(refusal.Payload), msgAgentStopped) {
		t.Fatalf("the next leader answered %s, want the stopped agent's refusal", refusal.Payload)
	}
	a.nextSweep()
	a.sweepPastTheTaskTimeLimit()
	none(t, answers)
}

func TestATaskIsLoggedAsFailedOnlyOnceItsAnswerIsPublished(t *testing.T) {
	logs := captureAllLogs(t)
	a, _ := newReplicas(t, replicasPlan{})
	term := a.lead(1)
	c := a.connect()
	a.consume(c)
	answers := a.listen(responseTopic("T1"))
	a.acknowledgedTask(c, "T1")

	a.brk.failTopic.Store(responseTopic("T1"))
	term.end()
	a.nextSweep()
	if said := len(linesLogged(logs, failedByStandDown.logged)); said != 0 {
		t.Fatalf("the task was logged as failed %d times before its answer was published", said)
	}
	a.brk.failTopic.Store("")
	a.nextSweep()
	failedWith(t, recv(t, answers), msgLinkMoved)
	loggedOnceWithItsIDs(t, logs, "INFO", failedByStandDown.logged, "T1")
}

func TestOnABrokerThatNeitherRequeuesNorRedeliversAStandDownFailsAnUnacknowledgedTaskOnce(t *testing.T) {
	a, b := newReplicas(t, replicasPlan{neverRedelivers: true})
	term := a.lead(1)
	c := a.connect()
	a.consume(c)
	answers := a.listen(responseTopic("T1"))
	a.lateAckedTask(c, "T1")

	term.end()
	failedWith(t, recv(t, answers), msgRequestLost)
	none(t, answers)
	if nacks := a.brk.nacks.Load(); nacks != 1 {
		t.Fatalf("the request was negatively acknowledged %d times, want once", nacks)
	}
	b.lead(2)
	b.reconnects().quiet()
	a.sweepPastTheTaskTimeLimit()
	none(t, answers)
}

func TestOnABrokerThatNeverRedeliversATaskItsConnectionLeftStaysOpenForTheAgentsNextConnection(t *testing.T) {
	r := newRig(t, nil, nil)
	c := r.connect()
	r.consume(c)
	answers := r.listen(responseTopic("T1"))
	r.submit("T1", "tok:alice:T1", nil)
	c.next(typeTask)
	leaves(t, r, c)
	waitFor(t, func() bool { return r.brk.nacks.Load() == 1 })

	r.adapter.agent(agentName).sweep(time.Now().Add(sweepInterval))
	none(t, answers)
	back := r.connect()
	back.send(completed("T1"))
	completedOnce(t, answers)
}

func TestATaskWhoseRequestCouldNotGoBackOutsideAStandDownIsFailedOnceByTheNextSweep(t *testing.T) {
	r := requeueingDevBrokerRig(t, nil)
	queueFull := errors.New("the queue is full")
	r.brk.requeueErr.Store(&queueFull)
	c := r.connect()
	r.consume(c)
	answers := r.listen(responseTopic("T1"))
	r.submit("T1", "tok:alice:T1", nil)
	c.send(nack(c.next(typeTask)))
	waitFor(t, func() bool { return r.brk.acks.Load() == 1 })

	s := r.adapter.agent(agentName)
	s.sweep(time.Now().Add(sweepInterval))
	failedWith(t, recv(t, answers), msgRequestNotBack)
	c.send(completed("T1"))
	c.refused(errCodeUnknownTask, "T1")
	s.sweep(time.Now().Add(2 * sweepInterval))
	s.sweep(time.Now().Add(r.adapter.cfg.TaskTTL + time.Minute))
	none(t, answers)
}

func TestAStandDownWithOpenPeerCallsAndAStalledBrokerEndsByItsDeadline(t *testing.T) {
	const settleWait = 300 * time.Millisecond
	a, _ := newReplicas(t, replicasPlan{tune: tunedWaits(settleWait, 0)})
	term := a.lead(1)
	c := a.connect()
	a.consume(c)
	peerRequests := a.listen(a2a.AgentRequestTopic(testNS, peerName))
	a.acknowledgedTask(c, "T1")
	a.lateAckedTask(c, "T2")
	a.lateAckedTask(c, "T3")
	for i, taskID := range []string{"T1", "T2", "T3"} {
		c.send(map[string]any{"type": typePeerCall, "parentTaskId": taskID, "subtaskId": fmt.Sprintf("S%d", i), "peer": peerName,
			"text": "Is policy P-9 active?", "callId": fmt.Sprintf("call-%d", i)})
		recv(t, peerRequests)
	}

	a.brk.stallTopic.Store(a2a.AgentRequestTopic(testNS, peerName))
	term.cancel()
	term.endsWithin(settleWait + 2*time.Second)
	if open := a.adapter.openTasks(); open != 0 {
		t.Fatalf("%d tasks are open after the stand-down, want none: one was failed and two forgotten", open)
	}
}

func TestAStandDownSettlesEightTasksAtATimeAndNoMore(t *testing.T) {
	const atATime = 8
	const tasks = atATime + 2
	a, _ := newReplicas(t, replicasPlan{})
	term := a.lead(1)
	c := a.connect()
	a.consume(c)
	publishing := shutGate(t)
	answers := make([]<-chan *broker.Message, tasks)
	for i := range tasks {
		taskID := fmt.Sprintf("T%d", i)
		answers[i] = a.listen(responseTopic(taskID))
		a.acknowledgedTask(c, taskID)
		a.link.heldPublishes.Store(responseTopic(taskID), publishing)
	}

	term.cancel()
	for range atATime {
		publishing.awaitArrival(t)
	}
	select {
	case <-publishing.reached:
		t.Fatalf("more than %d answers were being published at once", atATime)
	case <-time.After(heldBackFor):
	}
	publishing.open()
	term.endsWithin(standDownWithNothingToWaitFor)
	for _, answer := range answers {
		failedWith(t, recv(t, answer), msgLinkMoved)
	}
}

func TestAStandDownCountsWhatItDidWithItsTasksAndWarnsOnlyWhenSomethingWasLost(t *testing.T) {
	t.Run("a routine handover", func(t *testing.T) {
		logs := captureAllLogs(t)
		a, _ := newReplicas(t, replicasPlan{})
		term := a.lead(1)
		c := a.connect()
		a.consume(c)
		answers := a.listen(responseTopic("T1"))
		a.acknowledgedTask(c, "T1")
		a.lateAckedTask(c, "T2")

		term.end()
		failedWith(t, recv(t, answers), msgLinkMoved)
		theOneLineLogged(t, logs, stoodDown, "level=INFO", "term=1", "failed=1", "forgotten=1", "unanswered=0")
		theOneLineLogged(t, logs, forgotAHandedBackTask, "level=DEBUG", "agentName="+agentName, "taskID=T2", "traceID=trace-1")
		loggedOnceWithItsIDs(t, logs, "INFO", failedByStandDown.logged, "T1")
		if warned := len(linesLogged(logs, standDownLostWork)); warned != 0 {
			t.Fatalf("a handover that lost nothing warned %d times", warned)
		}
	})

	t.Run("an answer cannot be published", func(t *testing.T) {
		logs := captureAllLogs(t)
		a, _ := newReplicas(t, replicasPlan{})
		term := a.lead(1)
		c := a.connect()
		a.consume(c)
		a.acknowledgedTask(c, "T1")

		a.brk.failTopic.Store(responseTopic("T1"))
		term.end()
		theOneLineLogged(t, logs, stoodDown, "failed=0", "forgotten=0", "unanswered=1")
		theOneLineLogged(t, logs, standDownLostWork, "level=WARN", "term=1", "requestsLost=0", "unanswered=1")
	})

	t.Run("a request cannot go back", func(t *testing.T) {
		logs := captureAllLogs(t)
		a, _ := newReplicas(t, replicasPlan{})
		term := a.lead(1)
		c := a.connect()
		a.consume(c)
		answers := a.listen(responseTopic("T1"))
		a.lateAckedTask(c, "T1")
		queueFull := errors.New("the queue is full")
		a.brk.requeueErr.Store(&queueFull)

		term.end()
		failedWith(t, recv(t, answers), msgRequestLost)
		theOneLineLogged(t, logs, stoodDown, "failed=1", "forgotten=0", "unanswered=0")
		theOneLineLogged(t, logs, standDownLostWork, "level=WARN", "requestsLost=1", "unanswered=0")
		loggedOnceWithItsIDs(t, logs, "INFO", failedWithItsRequestLost.logged, "T1")
	})
}

func TestStoppingALeadingReplicaWarnsWithTheTasksItsStandDownSettled(t *testing.T) {
	logs := captureAllLogs(t)
	a, _ := newReplicas(t, replicasPlan{})
	a.lead(1)
	c := a.connect()
	a.consume(c)
	a.acknowledgedTask(c, "T1")
	a.lateAckedTask(c, "T2")

	ctx, cancel := context.WithTimeout(context.Background(), closeWait)
	defer cancel()
	if err := a.adapter.Stop(ctx); err != nil || ctx.Err() != nil {
		t.Fatalf("stop returned %v, drained in time: %t", err, ctx.Err() == nil)
	}
	theOneLineLogged(t, logs, stoppingWithOpenTasks, "level=WARN", "openTasks=2")
}

func TestALeaderTickWhileTheReplicaStopsStartsNoTerm(t *testing.T) {
	a, _ := newReplicas(t, replicasPlan{})
	term := a.lead(1)
	c := a.connect()
	a.consume(c)
	a.acknowledgedTask(c, "T1")
	publishing := a.link.holdPublishesTo(t, responseTopic("T1"))

	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		_ = a.adapter.Stop(context.Background())
	}()
	publishing.awaitArrival(t)
	a.adapter.RunIngress(context.Background(), 2)
	a.adapter.termMu.Lock()
	current := a.adapter.term
	a.adapter.termMu.Unlock()
	if current != term.started {
		t.Fatalf("a leader tick during the stop started term %d", current.number)
	}
	publishing.open()
	select {
	case <-stopped:
	case <-time.After(closeWait):
		t.Fatal("the stop never returned")
	}
}

func TestAnAgentThatAnswersAndClosesAtOnceIsAnsweredOnceAndNotRunAgain(t *testing.T) {
	r := requeueingDevBrokerRig(t, nil)
	c := r.connect()
	r.consume(c)
	answers := r.listen(responseTopic("T1"))
	r.submit("T1", "tok:alice:T1", nil)
	task := c.next(typeTask)

	c.send(completed("T1"))
	c.send(ack(task))
	leaves(t, r, c)
	completedOnce(t, answers)
	waitFor(t, func() bool { return r.brk.acks.Load() == 1 })
	next := r.connect()
	r.consume(next)
	next.quiet()
	if taken := r.brk.taken.Load(); taken != 1 {
		t.Fatalf("the request was taken %d times, want once: the task was answered and must not run again", taken)
	}
}

func TestAResultAndAckReadFromAConnectionJustClosedAsTooSlowAreHandled(t *testing.T) {
	r := requeueingDevBrokerRig(t, nil)
	c := r.connect()
	r.consume(c)
	answers := r.listen(responseTopic("T1"))
	r.submit("T1", "tok:alice:T1", nil)
	task := c.next(typeTask)
	closing := r.onlyServerConn()
	closing.closeOnce.Do(func() { close(closing.done) })
	t.Cleanup(func() { closing.closeSocket(websocket.CloseTryAgainLater, "too slow") })

	c.send(completed("T1"))
	c.send(ack(task))
	completedOnce(t, answers)
	waitFor(t, func() bool { return r.brk.acks.Load() == 1 })
	leaves(t, r, c)
	next := r.connect()
	r.consume(next)
	next.quiet()
	if taken := r.brk.taken.Load(); taken != 1 {
		t.Fatalf("the request was taken %d times, want once: the task was answered and must not run again", taken)
	}
}

func TestAFrameStillArrivingFromAConnectionTheStandDownClosedCannotAnswerATaskLeftForTheSweep(t *testing.T) {
	logs := captureAllLogs(t)
	frozen := time.Now()
	a, _ := newReplicas(t, replicasPlan{tune: func(adapter *Adapter) { adapter.now = func() time.Time { return frozen } }})
	term := a.lead(1)
	c := a.connect()
	a.consume(c)
	answers := a.listen(responseTopic("T1"))
	a.acknowledgedTask(c, "T1")
	closedByTheStandDown := a.onlyServerConn()

	a.brk.failTopic.Store(responseTopic("T1"))
	term.end()
	a.brk.failTopic.Store("")
	a.adapter.agent(agentName).handleFrame(closedByTheStandDown, inFrame{Type: typeResult, TaskID: "T1", State: stateCompleted, Text: "late"})
	none(t, answers)
	theOneLineLogged(t, logs, droppedAFrame, "level=DEBUG", "agentName="+agentName, "connectionID="+closedByTheStandDown.id,
		"frameType="+typeResult, `reason="`+droppedFromALostConnection+`"`)

	const typeOfTheClientsMaking = "bob@example.com-made-this-up"
	a.adapter.agent(agentName).handleFrame(closedByTheStandDown, inFrame{Type: typeOfTheClientsMaking, TaskID: "T1"})
	dropped := linesLogged(logs, droppedAFrame)
	if len(dropped) != 2 || !strings.Contains(dropped[1], "frameType="+unlistedFrameType) {
		t.Fatalf("a frame of a type the link does not know was logged as %v, want its type logged as %s", dropped, unlistedFrameType)
	}
	if strings.Contains(logs.all(), typeOfTheClientsMaking) {
		t.Fatalf("the log carries the frame type the client made up:\n%s", logs.all())
	}

	a.nextSweep()
	failedWith(t, recv(t, answers), msgLinkMoved)
	none(t, answers)
}

func TestAStandDownWaitsForARequestAnAgentIsGivingBackAndThenEndsAtOnce(t *testing.T) {
	a, b := newReplicas(t, replicasPlan{})
	term := a.lead(1)
	c := a.connect()
	a.consume(c)
	answers := a.listen(responseTopic("T1"))
	first := a.lateAckedTask(c, "T1")
	a.stopsDeliveringTo(c)
	requeueing := a.link.holdRequeues(t)
	c.send(nack(first))
	requeueing.awaitArrival(t)

	term.cancel()
	heldBack(t, term.started.ended, "the stand-down, with the agent's nack still being given back,")
	requeueing.open()
	term.endsWithin(standDownWithNothingToWaitFor)
	none(t, answers)
	b.lead(2)
	b.runsAgainAndCompletes("T1", answers)
}

func TestAStandDownWaitsForARequestItsSlowConnectionCouldNotTakeAndThenEndsAtOnce(t *testing.T) {
	a, b := newReplicas(t, replicasPlan{})
	term := a.lead(1)
	answers := a.listen(responseTopic("T1"))
	observedOutboxConn(t, a.rig)
	requeueing := a.link.holdRequeues(t)
	a.submit("T1", "tok:alice:T1", nil)
	requeueing.awaitArrival(t)

	term.cancel()
	heldBack(t, term.started.ended, "the stand-down, with an unsent task still being given back,")
	requeueing.open()
	term.endsWithin(standDownWithNothingToWaitFor)
	none(t, answers)
	b.lead(2)
	b.runsAgainAndCompletes("T1", answers)
}

func TestAStandDownWaitsForARequestDispatchReturnsForWantOfAConsumerAndThenEndsAtOnce(t *testing.T) {
	a, b := newReplicas(t, replicasPlan{})
	term := a.lead(1)
	c := a.connect()
	a.consume(c)
	answers := a.listen(responseTopic("T1"))
	received := a.link.holdReceivedRequests(t)
	a.submit("T1", "tok:alice:T1", nil)
	received.awaitArrival(t)
	leaves(t, a.rig, c)
	requeueing := a.link.holdRequeues(t)
	received.open()
	requeueing.awaitArrival(t)

	term.cancel()
	heldBack(t, term.started.ended, "the stand-down, with a returned request still being given back,")
	requeueing.open()
	term.endsWithin(standDownWithNothingToWaitFor)
	none(t, answers)
	b.lead(2)
	b.runsAgainAndCompletes("T1", answers)
}

func TestAStandDownWaitsForTheRequestDispatchHeldAndThenEndsAtOnce(t *testing.T) {
	a, b := newReplicas(t, replicasPlan{})
	term := a.lead(1)
	c := a.connect()
	a.consume(c)
	answers := a.listen(responseTopic("T1"))
	received := a.link.holdReceivedRequests(t)
	a.submit("T1", "tok:alice:T1", nil)
	received.awaitArrival(t)
	requeueing := a.link.holdRequeues(t)

	term.cancel()
	waitFor(t, func() bool {
		dispatching := true
		a.locked(func(s *agentState) { dispatching = s.dispatching })
		return !dispatching
	})
	received.open()
	requeueing.awaitArrival(t)
	heldBack(t, term.started.ended, "the stand-down, with dispatch's request still being given back,")
	requeueing.open()
	term.endsWithin(standDownWithNothingToWaitFor)
	none(t, answers)
	b.lead(2)
	b.runsAgainAndCompletes("T1", answers)
}

func TestATaskSentAgainAfterItsAckIsSettledByItsNewRequestAtStandDown(t *testing.T) {
	onBothBrokerKinds(t, func(t *testing.T, releasesBinds bool) {
		a, b := newReplicas(t, replicasPlan{releasesBinds: releasesBinds})
		term := a.lead(1)
		c := a.connect()
		a.consume(c)
		answers := a.listen(responseTopic("T1"))
		a.acknowledgedTask(c, "T1")
		a.lateAckedTask(c, "T1")

		term.end()
		none(t, answers)
		b.lead(2)
		b.runsAgainAndCompletes("T1", answers)
		a.sweepPastTheTaskTimeLimit()
		none(t, answers)
	})
}

func TestATaskSentAgainAfterItsRequestWasLostIsSettledByItsNewRequest(t *testing.T) {
	a, b := newReplicas(t, replicasPlan{})
	term := a.lead(1)
	c := a.connect()
	a.consume(c)
	answers := a.listen(responseTopic("T1"))
	first := a.lateAckedTask(c, "T1")
	queueFull := errors.New("the queue is full")
	a.brk.requeueErr.Store(&queueFull)
	c.send(nack(first))
	waitFor(t, func() bool { return a.requestLost("T1") })
	a.brk.requeueErr.Store(nil)
	a.lateAckedTask(c, "T1")

	a.nextSweep()
	none(t, answers)
	term.end()
	none(t, answers)
	b.lead(2)
	b.runsAgainAndCompletes("T1", answers)
	a.sweepPastTheTaskTimeLimit()
	none(t, answers)
}

func TestATaskLeftForTheSweepThatIsSentAgainDuringAStandDownIsLeftToTheNextLeader(t *testing.T) {
	a, b := newReplicas(t, replicasPlan{})
	first := a.lead(1)
	c := a.connect()
	a.consume(c)
	answers := a.listen(responseTopic("T1"))
	a.acknowledgedTask(c, "T1")
	a.brk.failTopic.Store(responseTopic("T1"))
	first.end()
	a.brk.failTopic.Store("")

	second := a.lead(2)
	again := a.reconnects()
	a.acknowledgedTask(again, "T2")
	publishing := a.link.holdPublishesTo(t, responseTopic("T2"))
	again.send(completed("T2"))
	publishing.awaitArrival(t)
	second.cancel()
	heldBack(t, second.started.ended, "the stand-down, with an answer still being published,")
	a.lateAckedTask(again, "T1")
	publishing.open()
	second.endsWithin(standDownWithNothingToWaitFor)
	none(t, answers)

	b.lead(3)
	b.runsAgainAndCompletes("T1", answers)
	a.sweepPastTheTaskTimeLimit()
	none(t, answers)
}

func TestTheAnswersACallerGetsForATaskTheLinkCouldNotFinishKeepTheirWording(t *testing.T) {
	for said, want := range map[string]string{
		msgLinkMoved:      "the agent link moved to another gateway replica before the agent answered; send the task again",
		msgRequestLost:    "the agent link moved to another gateway replica and could not return the task to its queue; send the task again",
		msgRequestNotBack: "the agent link could not return the task to its queue for another delivery; send the task again",
		msgTaskExpired:    "the agent did not answer before the task's time limit",
	} {
		if said != want {
			t.Errorf("a caller is told %q, want %q", said, want)
		}
	}
}

func (r *replica) losesAPromptAnswerItCanNeitherRefuseNorRequeue(taskID string) {
	r.t.Helper()
	refusal := responseTopic(taskID + "-answer")
	r.brk.failTopic.Store(refusal)
	r.brk.requeueErr.Store(&errQueueFull)
	settled := r.brk.acks.Load()
	answer := a2a.UserInputResponseData{Type: string(a2a.SignalUserInputResponse), RequestID: "q-1", Status: answerAnswered}
	body, _ := json.Marshal(a2a.NewRequest(taskID, methodSend, map[string]any{"message": a2a.Message{
		Kind: "message", Role: a2a.RoleUser, TaskID: taskID, Parts: []a2a.Part{a2a.NewDataPart(answer)}}}))
	props := map[string]string{a2a.PropReplyTo: refusal, a2a.PropUserID: "alice@example.com", a2a.PropAuthToken: "tok:alice:expired"}
	if err := r.brk.PublishGuaranteed(r.t.Context(), a2a.AgentRequestTopic(testNS, agentName), &broker.Message{Payload: body, UserProperties: props}); err != nil {
		r.t.Fatal(err)
	}
	waitFor(r.t, func() bool { return r.brk.acks.Load() == settled+1 })
	r.brk.failTopic.Store("")
}

func TestAPromptAnswerThatCanNeitherBeRefusedNorRequeuedLeavesItsLiveTaskToItsAgent(t *testing.T) {
	for name, acknowledged := range map[string]bool{"acknowledged": true, "still in its agent's hands": false} {
		t.Run(name, func(t *testing.T) {
			a, _ := newReplicas(t, replicasPlan{})
			a.lead(1)
			c := a.connect()
			a.consume(c)
			answers := a.listen(responseTopic("T1"))
			task := a.lateAckedTask(c, "T1")
			if acknowledged {
				c.send(ack(task))
				waitFor(t, func() bool { return a.brk.acks.Load() == 1 })
			}

			a.losesAPromptAnswerItCanNeitherRefuseNorRequeue("T1")
			a.nextSweep()
			none(t, answers)
			c.send(completed("T1"))
			completedOnce(t, answers)
		})
	}
}

func TestADuplicateRequestThatCannotGoBackLeavesItsLiveTaskToItsAgent(t *testing.T) {
	a, _ := newReplicas(t, replicasPlan{})
	a.lead(1)
	c := a.connect()
	a.consume(c)
	onItsOtherConnection := a.connect()
	answers := a.listen(responseTopic("T1"))
	a.acknowledgedTask(c, "T1")
	received := a.link.holdReceivedRequests(t)
	a.submit("T1", "tok:alice:T1", nil)
	received.awaitArrival(t)
	_ = c.ws.Close()
	waitFor(t, func() bool { return len(a.adapter.agent(agentName).connections()) == 1 })

	a.brk.requeueErr.Store(&errQueueFull)
	received.open()
	waitFor(t, func() bool { return a.brk.acks.Load() == 2 })
	a.nextSweep()
	none(t, answers)
	onItsOtherConnection.send(completed("T1"))
	completedOnce(t, answers)
}

func TestATaskBoundAgainWhileItsGivenUpAnswerIsBeingReleasedKeepsItsBinding(t *testing.T) {
	logs := captureAllLogs(t)
	clock := newSteppedClock()
	a, _ := newReplicas(t, replicasPlan{tune: clock.runs})
	a.lead(1)
	c := a.connect()
	a.consume(c)
	a.acknowledgedTask(c, "T1")
	a.callPeer(c, a.listen(a2a.AgentRequestTopic(testNS, peerName)), "S1", "Is policy P-9 active?")
	a.brk.failTopic.Store(responseTopic("T1"))
	a.sweepsAfter(clock, a.adapter.cfg.TaskTTL+time.Minute)

	a.brk.stallTopic.Store(a2a.AgentRequestTopic(testNS, peerName))
	released := make(chan struct{})
	go func() {
		defer close(released)
		a.sweepsAfter(clock, answerGiveUpAfter)
	}()
	waitFor(t, func() bool { return len(linesLogged(logs, gaveUpAnsweringATask)) == 1 })
	a.submit("T1", "tok:alice:T1", nil)
	for c.nextOf(typeTask, typePeerResult).Type != typeTask {
	}
	<-released
	if bound, _ := a.binding("T1"); !bound {
		t.Fatal("the task bound again while its given-up answer was released lost its fresh binding")
	}
}

// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agentlink

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

type gate struct {
	closed  atomic.Bool
	reached chan struct{}
	opened  chan struct{}
	outcome atomic.Pointer[error]
}

func newGate(t *testing.T) *gate {
	g := &gate{reached: make(chan struct{}, 16), opened: make(chan struct{})}
	t.Cleanup(g.open)
	return g
}

func shutGate(t *testing.T) *gate {
	g := newGate(t)
	g.shut()
	return g
}

func (g *gate) shut() { g.closed.Store(true) }

func (g *gate) pass() error {
	if !g.closed.Load() {
		return nil
	}
	g.reached <- struct{}{}
	<-g.opened
	if failure := g.outcome.Load(); failure != nil {
		return *failure
	}
	return nil
}

func (g *gate) awaitArrival(t *testing.T) {
	t.Helper()
	select {
	case <-g.reached:
	case <-time.After(arrivalWait):
		t.Fatal("nothing reached the gate")
	}
}

func (g *gate) open() {
	if g.closed.CompareAndSwap(true, false) {
		close(g.opened)
	}
}

func (g *gate) openFailing(failure error) {
	g.outcome.Store(&failure)
	g.open()
}

const heldBackFor = 200 * time.Millisecond

func heldBack(t *testing.T, finished <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-finished:
		t.Fatalf("%s did not wait", what)
	case <-time.After(heldBackFor):
	}
}

type gatedAuth struct {
	fakeAuth
	gate *gate
}

func (g gatedAuth) Authenticate(ctx context.Context, tok, taskID string) (*auth.Identity, error) {
	_ = g.gate.pass()
	return g.fakeAuth.Authenticate(ctx, tok, taskID)
}

func holdingHandshakesAt(g *gate) func(*Adapter) {
	return func(adapter *Adapter) {
		checkOrigin := adapter.upgrader.CheckOrigin
		adapter.upgrader.CheckOrigin = func(r *http.Request) bool {
			_ = g.pass()
			return checkOrigin(r)
		}
	}
}

func auditedBy(audit *authEvents) []runtime.Option {
	return []runtime.Option{runtime.WithAuditLogger(audit)}
}

func (a *authEvents) count() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.events)
}

func (r *replica) binding(taskID string) (bound, final bool) {
	r.locked(func(s *agentState) {
		if b := s.tasks[taskID]; b != nil {
			bound, final = true, b.final
		}
	})
	return bound, final
}

func (r *replica) sweepPastTheTaskTimeLimit() {
	r.adapter.agent(agentName).sweep(time.Now().Add(r.adapter.cfg.TaskTTL + time.Minute))
}

func (r *replica) nextSweep() {
	r.adapter.agent(agentName).sweep(time.Now().Add(sweepInterval))
}

func failedWith(t *testing.T, m *broker.Message, message string) {
	t.Helper()
	if !strings.Contains(string(m.Payload), message) || !strings.Contains(string(m.Payload), `"error"`) {
		t.Fatalf("answer %s, want the error %q", m.Payload, message)
	}
}

func (r *replica) acknowledgedTask(c *client, taskID string) {
	r.t.Helper()
	acked := r.brk.acks.Load()
	r.taskOn(c, taskID, nil)
	waitFor(r.t, func() bool { return r.brk.acks.Load() == acked+1 })
}

func loggedOnceWithItsIDs(t *testing.T, logs *wakeLogText, level, message, taskID string) {
	t.Helper()
	var lines []string
	for _, line := range strings.Split(logs.all(), "\n") {
		if strings.Contains(line, message) {
			lines = append(lines, line)
		}
	}
	if len(lines) != 1 {
		t.Fatalf("logged %q %d times, want once:\n%s", message, len(lines), logs.all())
	}
	for _, field := range []string{"level=" + level, "agentName=" + agentName, "taskID=" + taskID, "traceID=trace-1"} {
		if !strings.Contains(lines[0], field) {
			t.Fatalf("the log lacks %s: %s", field, lines[0])
		}
	}
	if strings.Contains(lines[0], "Is policy P-9 active?") || strings.Contains(lines[0], "tok:alice") {
		t.Fatalf("the log carries the task's content or token: %s", lines[0])
	}
}

func TestAStandDownFailsAnAcknowledgedTaskAtOnceAndCancelsItsPeerCalls(t *testing.T) {
	logs := captureAllLogs(t)
	a, b := newReplicas(t, replicasPlan{})
	term := a.lead(1)
	c := a.connect()
	a.consume(c)
	answers := a.listen(responseTopic("T1"))
	a.acknowledgedTask(c, "T1")
	peerRequests, peerTaskID := openWork(t, a.rig, c)

	term.end()
	closedForServiceRestart(t, c)
	failedWith(t, recv(t, answers), msgLinkMoved)
	cancelSent(t, peerRequests, peerTaskID)
	loggedOnceWithItsIDs(t, logs, "INFO", failedByStandDown.logged, "T1")
	if bound, final := a.binding("T1"); !bound || !final || a.adapter.openTasks() != 0 {
		t.Fatalf("after the failure the task is bound=%t final=%t with %d tasks open, want it closed", bound, final, a.adapter.openTasks())
	}

	b.lead(2)
	onB := b.connect()
	b.consume(onB)
	onB.send(map[string]any{"type": typeResult, "taskId": "T1", "state": stateCompleted, "text": "late"})
	onB.refused(errCodeUnknownTask, "T1")
	a.sweepPastTheTaskTimeLimit()
	none(t, answers)
}

func TestAFailureThatCannotBePublishedAtStandDownIsPublishedByTheNextSweepsUntilItIs(t *testing.T) {
	a, _ := newReplicas(t, replicasPlan{})
	term := a.lead(1)
	c := a.connect()
	a.consume(c)
	answers := a.listen(responseTopic("T1"))
	a.acknowledgedTask(c, "T1")
	peerRequests, peerTaskID := openWork(t, a.rig, c)

	a.brk.failTopic.Store(responseTopic("T1"))
	term.end()
	cancelSent(t, peerRequests, peerTaskID)
	none(t, answers)
	if bound, final := a.binding("T1"); !bound || final {
		t.Fatalf("a task whose failure was not published is bound=%t final=%t, want it kept for the sweep", bound, final)
	}
	a.nextSweep()
	none(t, answers)

	a.brk.failTopic.Store("")
	a.nextSweep()
	failedWith(t, recv(t, answers), msgLinkMoved)
	a.nextSweep()
	a.sweepPastTheTaskTimeLimit()
	none(t, answers)
}

func TestAStandDownAnswersNoTaskTwice(t *testing.T) {
	a, _ := newReplicas(t, replicasPlan{})
	term := a.lead(1)
	c := a.connect()
	a.consume(c)
	answered, cancelled := a.listen(responseTopic("T1")), a.listen(responseTopic("T2"))

	a.acknowledgedTask(c, "T1")
	c.send(map[string]any{"type": typeResult, "taskId": "T1", "state": stateCompleted, "text": "done"})
	if task := finalTask(t, recv(t, answered)); string(task.Status.State) != stateCompleted {
		t.Fatalf("T1 answered %s", task.Status.State)
	}
	a.acknowledgedTask(c, "T2")
	a.cancel("T2")
	c.next(typeCancel)

	term.end()
	if task := finalTask(t, recv(t, cancelled)); string(task.Status.State) != stateCanceled {
		t.Fatalf("the cancelled task was answered %s, want canceled", task.Status.State)
	}
	a.sweepPastTheTaskTimeLimit()
	none(t, answered)
	none(t, cancelled)
}

func TestALateAckingAgentsRunningTaskIsRunAgainByTheNewLeaderAndAnsweredOnce(t *testing.T) {
	onBothBrokerKinds(t, func(t *testing.T, releasesBinds bool) {
		a, b := newReplicas(t, replicasPlan{releasesBinds: releasesBinds})
		term := a.lead(1)
		c := a.connect()
		a.consume(c)
		answers := a.listen(responseTopic("T1"))
		a.lateAckedTask(c, "T1")
		peerRequests, peerTaskID := openWork(t, a.rig, c)

		term.end()
		cancelSent(t, peerRequests, peerTaskID)
		if bound, _ := a.binding("T1"); bound {
			t.Fatal("the old leader kept the binding of a task whose request went back to the queue")
		}
		none(t, answers)

		b.lead(2)
		next := b.reconnects()
		again := next.next(typeTask)
		requireRedelivered(t, again, "T1")
		next.send(completed("T1"))
		next.send(ack(again))
		completedOnce(t, answers)
		a.sweepPastTheTaskTimeLimit()
		none(t, answers)
	})
}

func TestStoppingALeadingReplicaStandsItDownFirst(t *testing.T) {
	onBothBrokerKinds(t, func(t *testing.T, releasesBinds bool) {
		url, _ := countingRuntime(t)
		a, _ := newReplicas(t, replicasPlan{agentCfg: requestDriven(url, true), releasesBinds: releasesBinds})
		term := a.lead(1)
		a.awaitWakeWatch()
		c := a.connect()
		a.consume(c)
		answers := a.listen(responseTopic("T1"))
		a.acknowledgedTask(c, "T1")

		ctx, cancel := context.WithTimeout(context.Background(), closeWait)
		defer cancel()
		if err := a.adapter.Stop(ctx); err != nil || ctx.Err() != nil {
			t.Fatalf("stop returned %v, drained in time: %t", err, ctx.Err() == nil)
		}
		if !term.started.hasEnded() {
			t.Fatal("stop returned before the term had stood down")
		}
		closedForServiceRestart(t, c)
		failedWith(t, recv(t, answers), msgLinkMoved)
		none(t, answers)
	})
}

func TestAHandshakeThatEndsAfterTheTermIsClosedForServiceRestartAndNeverJoins(t *testing.T) {
	audit := &authEvents{}
	handshakes := newGate(t)
	a, _ := newReplicas(t, replicasPlan{tune: holdingHandshakesAt(handshakes), opts: auditedBy(audit)})
	term := a.lead(1)

	handshakes.shut()
	type dialed struct {
		c   *client
		err error
	}
	dialing := make(chan dialed, 1)
	go func() {
		c, _, err := a.dial(agentName, token)
		dialing <- dialed{c, err}
	}()
	handshakes.awaitArrival(t)
	term.end()
	handshakes.open()

	upgraded := <-dialing
	if upgraded.err != nil {
		t.Fatalf("the upgrade failed: %v", upgraded.err)
	}
	code, frames := closeOf(t, upgraded.c)
	if code != websocket.CloseServiceRestart || frames != 0 {
		t.Fatalf("the connection got %d frames and closed with %d, want no welcome and %d", frames, code, websocket.CloseServiceRestart)
	}
	if open := len(a.adapter.agent(agentName).connections()); open != 0 {
		t.Fatalf("%d connections joined a replica that does not lead", open)
	}
	if events := audit.count(); events != 0 {
		t.Fatalf("the connection wrote %d audit events, want 0: it was neither refused for its credentials nor accepted", events)
	}
}

func heldWakeWatcher(t *testing.T, a *replica, c *client) (letGo func()) {
	t.Helper()
	letGo = a.holdWatcherCopies()
	a.submit("T1", "tok:alice:T1", nil)
	c.next(typeTask)
	waitFor(t, func() bool { return a.brk.copiesAtTheGate.Load() == 1 })
	return letGo
}

func TestANewerTermLeadsOnlyAfterTheOlderOneHasStoodDown(t *testing.T) {
	url, _ := countingRuntime(t)
	a, _ := newReplicas(t, replicasPlan{agentCfg: requestDriven(url, false)})
	first := a.lead(1)
	a.awaitWakeWatch()
	c := a.connect()
	a.consume(c)
	letTheOlderTermEnd := heldWakeWatcher(t, a, c)

	a.startTerm(2)
	closedForServiceRestart(t, c)
	heldBack(t, first.started.ended, "the older term, whose wake watcher still runs,")
	if leading := a.adapter.leading.Load(); leading != nil {
		t.Fatalf("term %d leads while the older term's wake watcher still runs", leading.number)
	}
	a.refusesAsFollower(token)

	letTheOlderTermEnd()
	waitFor(t, func() bool {
		leading := a.adapter.leading.Load()
		return leading != nil && leading.number == 2
	})
	if !first.started.hasEnded() {
		t.Fatal("the newer term leads before the older one ended")
	}
	again := a.connect()
	a.consume(again)
	requireRedelivered(t, again.next(typeTask), "T1")
}

func TestATermCancelledWhileItWaitsForTheOlderOneNeverLeads(t *testing.T) {
	logs := captureAllLogs(t)
	url, _ := countingRuntime(t)
	a, _ := newReplicas(t, replicasPlan{agentCfg: requestDriven(url, false)})
	a.lead(1)
	a.awaitWakeWatch()
	c := a.connect()
	a.consume(c)
	letTheOlderTermEnd := heldWakeWatcher(t, a, c)

	second := a.startTerm(2)
	closedForServiceRestart(t, c)
	second.cancel()
	letTheOlderTermEnd()
	second.ended()

	if leading := a.adapter.leading.Load(); leading != nil {
		t.Fatalf("term %d leads after it was cancelled", leading.number)
	}
	for _, line := range strings.Split(logs.all(), "\n") {
		if strings.Contains(line, "agent link leading") && strings.Contains(line, "term=2") {
			t.Fatalf("a term cancelled before the older one ended led all the same: %s", line)
		}
	}
	a.refusesAsFollower(token)
}

func TestATermNumberLeadsAgainOnceItsEarlierRunHasEnded(t *testing.T) {
	a, _ := newReplicas(t, replicasPlan{})
	a.lead(7).end()
	a.refusesAsFollower(token)

	a.lead(7)
	c := a.connect()
	a.consume(c)
	a.taskOn(c, "T1", nil)
}

func TestAStandDownKeepsItsConnectionsUntilDispatchHasLetGoOfTheQueue(t *testing.T) {
	a, _ := newReplicas(t, replicasPlan{releasesBinds: true})
	term := a.lead(1)
	c := a.connect()
	a.consume(c)
	letGo := a.link.holdRequestQueueReleases(t)

	term.cancel()
	waitFor(t, func() bool { return a.link.requestQueueReleases.Load() == 1 })
	heldBack(t, term.started.ended, "the stand-down, with dispatch still holding the queue,")
	if open := len(a.adapter.agent(agentName).connections()); open != 1 {
		t.Fatalf("%d connections while dispatch still held the queue, want the 1 still open", open)
	}

	letGo()
	term.ended()
	closedForServiceRestart(t, c)
}

func TestAFollowerThatSawAStopAndAReleaseListsTheAgentWhenItLeads(t *testing.T) {
	url, wakes := countingRuntime(t)
	var sinceStart atomic.Int64
	start := time.Now()
	a, b := newReplicas(t, replicasPlan{
		agentCfg: requestDriven(url, true),
		tune: func(adapter *Adapter) {
			adapter.now = func() time.Time { return start.Add(time.Duration(sinceStart.Load())) }
		},
	})
	term := a.lead(1)
	a.awaitWakeWatch()
	wokenTimes(t, wakes, 1)

	bothStopped := func(stopped bool) func() bool {
		return func() bool {
			return a.adapter.agent(agentName).isStopped() == stopped && b.adapter.agent(agentName).isStopped() == stopped
		}
	}
	a.stop()
	waitFor(t, bothStopped(true))
	sinceStart.Add(int64(2 * wakeInterval))
	a.release()
	waitFor(t, bothStopped(false))
	wokenTimes(t, wakes, 2)

	term.end()
	sinceStart.Add(int64(2 * wakeInterval))
	b.lead(2)
	b.awaitWakeWatch()
	wokenTimes(t, wakes, 3)
}

func TestAnExLeaderThatGivesBackARequestItWasDeliveringWakesNothing(t *testing.T) {
	url, wakes := countingRuntime(t)
	authenticating := newGate(t)
	a, _ := newReplicas(t, replicasPlan{agentCfg: requestDriven(url, false), authn: gatedAuth{gate: authenticating}})
	term := a.lead(1)
	a.awaitWakeWatch()
	c := a.connect()
	a.consume(c)

	authenticating.shut()
	a.submit("T1", "tok:alice:T1", nil)
	authenticating.awaitArrival(t)
	waitFor(t, func() bool { return a.brk.watched.Load() == 1 })
	leaves(t, a.rig, c)

	term.cancel()
	waitFor(t, func() bool { return a.adapter.leading.Load() == nil })
	authenticating.open()
	term.ended()
	wokenTimes(t, wakes, 0)
}

func (r *replica) lateAckedTask(c *client, taskID string) received {
	r.t.Helper()
	r.submit(taskID, "tok:alice:"+taskID, nil)
	f := c.next(typeTask)
	if f.TaskID != taskID {
		r.t.Fatalf("task %s, want %s", f.TaskID, taskID)
	}
	return f
}

func completed(taskID string) map[string]any {
	return map[string]any{"type": typeResult, "taskId": taskID, "state": stateCompleted, "text": "done"}
}

func ack(f received) map[string]any {
	return map[string]any{"type": typeAck, "delivery": f.Delivery}
}

func completedOnce(t *testing.T, answers <-chan *broker.Message) {
	t.Helper()
	if task := finalTask(t, recv(t, answers)); string(task.Status.State) != stateCompleted {
		t.Fatalf("the task was answered %s, want completed", task.Status.State)
	}
	none(t, answers)
}

func canceledOnce(t *testing.T, answers <-chan *broker.Message) {
	t.Helper()
	if task := finalTask(t, recv(t, answers)); string(task.Status.State) != stateCanceled {
		t.Fatalf("the task was answered %s, want canceled", task.Status.State)
	}
	none(t, answers)
}

func (r *replica) cancelTakenOffTheQueue(c *client, taskID string) {
	r.t.Helper()
	acknowledged := r.brk.acks.Load()
	r.cancel(taskID)
	c.next(typeCancel)
	waitFor(r.t, func() bool { return r.brk.acks.Load() > acknowledged })
}

func (r *replica) reconnects() *client {
	r.t.Helper()
	c := r.connect()
	r.consume(c)
	return c
}

func (r *replica) runsAgainAndCompletes(taskID string, answers <-chan *broker.Message) {
	r.t.Helper()
	c := r.reconnects()
	again := c.next(typeTask)
	requireRedelivered(r.t, again, taskID)
	c.send(completed(taskID))
	c.send(ack(again))
	completedOnce(r.t, answers)
}

func TestARunThatReportsAStepBeforeItsRedeliveryIsRefusedThenRunAgainAndAnsweredOnce(t *testing.T) {
	for name, sameReplica := range map[string]bool{"the term returns to the same replica": true, "another replica leads": false} {
		t.Run(name, func(t *testing.T) {
			onBothBrokerKinds(t, func(t *testing.T, releasesBinds bool) {
				a, b := newReplicas(t, replicasPlan{releasesBinds: releasesBinds})
				term := a.lead(1)
				c := a.connect()
				a.consume(c)
				answers := a.listen(responseTopic("T1"))
				a.lateAckedTask(c, "T1")

				term.end()
				closedForServiceRestart(t, c)
				next := b
				if sameReplica {
					next = a
				}
				next.lead(2)
				back := next.connect()
				back.send(event("T1", map[string]any{"kind": eventProgress, "text": "from the run before the stand-down"}))
				back.refused(errCodeUnknownTask, "T1")
				none(t, answers)
				next.consume(back)
				again := back.next(typeTask)
				requireRedelivered(t, again, "T1")
				back.send(completed("T1"))
				back.send(ack(again))
				completedOnce(t, answers)
				waitFor(t, func() bool { return next.adapter.openTasks() == 0 })
				a.sweepPastTheTaskTimeLimit()
				none(t, answers)
			})
		})
	}
}

func TestATaskIsRunAgainAfterEachOfTwoTermEndsAndAnsweredOnce(t *testing.T) {
	onBothBrokerKinds(t, func(t *testing.T, releasesBinds bool) {
		a, _ := newReplicas(t, replicasPlan{releasesBinds: releasesBinds})
		answers := a.listen(responseTopic("T1"))
		first := a.lead(1)
		c := a.connect()
		a.consume(c)
		a.lateAckedTask(c, "T1")
		first.end()

		second := a.lead(2)
		requireRedelivered(t, a.reconnects().next(typeTask), "T1")
		second.end()
		none(t, answers)

		a.lead(3)
		a.runsAgainAndCompletes("T1", answers)
		a.sweepPastTheTaskTimeLimit()
		none(t, answers)
	})
}

func TestATaskAnsweredButNotYetAcknowledgedAtStandDownIsNotRunAgain(t *testing.T) {
	onBothBrokerKinds(t, func(t *testing.T, releasesBinds bool) {
		a, b := newReplicas(t, replicasPlan{releasesBinds: releasesBinds})
		term := a.lead(1)
		c := a.connect()
		a.consume(c)
		answers := a.listen(responseTopic("T1"))
		a.lateAckedTask(c, "T1")
		c.send(completed("T1"))
		completedOnce(t, answers)

		term.end()
		if releasesBinds && a.flow.settledAfterRelease.Load() != 0 {
			t.Fatal("the answered task's request was acknowledged after its flow was released, which a broker ignores")
		}
		b.lead(2)
		b.reconnects().quiet()
		none(t, answers)
	})
}

func TestAResultBeingPublishedWhenTheTermEndsIsWaitedFor(t *testing.T) {
	for name, publishFails := range map[string]bool{"and published": false, "and not published": true} {
		t.Run(name, func(t *testing.T) {
			onBothBrokerKinds(t, func(t *testing.T, releasesBinds bool) {
				a, b := newReplicas(t, replicasPlan{releasesBinds: releasesBinds})
				term := a.lead(1)
				c := a.connect()
				a.consume(c)
				answers := a.listen(responseTopic("T1"))
				a.lateAckedTask(c, "T1")
				publishing := a.link.holdPublishesTo(t, responseTopic("T1"))
				c.send(completed("T1"))
				publishing.awaitArrival(t)

				term.cancel()
				heldBack(t, term.started.ended, "the stand-down, with an answer still being published,")
				if publishFails {
					publishing.openFailing(errors.New("the broker refused the publish"))
				} else {
					publishing.open()
				}
				term.ended()

				b.lead(2)
				if publishFails {
					none(t, answers)
					b.runsAgainAndCompletes("T1", answers)
				} else {
					completedOnce(t, answers)
					b.reconnects().quiet()
				}
				a.sweepPastTheTaskTimeLimit()
				none(t, answers)
			})
		})
	}
}

func TestAFrameReadAfterTheStandDownBeganIsDropped(t *testing.T) {
	onBothBrokerKinds(t, func(t *testing.T, releasesBinds bool) {
		logs := captureAllLogs(t)
		a, b := newReplicas(t, replicasPlan{releasesBinds: releasesBinds})
		term := a.lead(1)
		c := a.connect()
		a.consume(c)
		onItsOtherConnection := a.connect()
		first, second := a.listen(responseTopic("T1")), a.listen(responseTopic("T2"))
		a.lateAckedTask(c, "T1")
		a.lateAckedTask(c, "T2")
		publishing := a.link.holdPublishesTo(t, responseTopic("T1"))
		c.send(completed("T1"))
		publishing.awaitArrival(t)

		term.cancel()
		waitFor(t, func() bool {
			standingDown := false
			a.locked(func(s *agentState) { standingDown = s.settling })
			return standingDown
		})
		onItsOtherConnection.send(completed("T2"))
		waitFor(t, func() bool { return len(linesLogged(logs, droppedAFrame)) == 1 })
		if term.started.hasEnded() {
			t.Fatal("the stand-down, with a frame still being handled, did not wait")
		}
		publishing.open()
		term.ended()
		completedOnce(t, first)
		none(t, second)
		theOneLineLogged(t, logs, droppedAFrame, "level=DEBUG", "frameType="+typeResult, `reason="`+droppedWhileStandingDown+`"`)

		b.lead(2)
		b.runsAgainAndCompletes("T2", second)
		none(t, first)
	})
}

func TestAGiveBackStillInFlightWhenTheTermEndsIsWaitedForAndItsLostRequestFailsTheTask(t *testing.T) {
	a, b := newReplicas(t, replicasPlan{})
	term := a.lead(1)
	c := a.connect()
	a.consume(c)
	answers := a.listen(responseTopic("T1"))
	a.lateAckedTask(c, "T1")
	requeueing := a.link.holdRequeues(t)
	leaves(t, a.rig, c)
	requeueing.awaitArrival(t)

	term.cancel()
	heldBack(t, term.started.ended, "the stand-down, with a request still being given back,")
	requeueing.openFailing(fmt.Errorf("requeue: %w", broker.ErrRedeliveryExhausted))
	term.ended()
	failedWith(t, recv(t, answers), msgRequestLost)
	none(t, answers)

	b.lead(2)
	b.reconnects().quiet()
	a.sweepPastTheTaskTimeLimit()
	none(t, answers)
}

func TestATaskWhoseRequestCouldNotGoBackAtStandDownIsFailedAtOnce(t *testing.T) {
	for name, requeueFailure := range map[string]error{
		"its redelivery limit is spent": fmt.Errorf("requeue: %w", broker.ErrRedeliveryExhausted),
		"the requeue fails":             errors.New("the queue is full"),
	} {
		t.Run(name, func(t *testing.T) {
			a, b := newReplicas(t, replicasPlan{})
			term := a.lead(1)
			c := a.connect()
			a.consume(c)
			answers := a.listen(responseTopic("T1"))
			a.lateAckedTask(c, "T1")
			a.brk.requeueErr.Store(&requeueFailure)

			term.end()
			failedWith(t, recv(t, answers), msgRequestLost)
			none(t, answers)
			b.lead(2)
			b.reconnects().quiet()
			a.sweepPastTheTaskTimeLimit()
			none(t, answers)
		})
	}
}

func TestARequestDispatchHoldsWhenTheTermEndsGoesBackToTheQueue(t *testing.T) {
	onBothBrokerKinds(t, func(t *testing.T, releasesBinds bool) {
		a, b := newReplicas(t, replicasPlan{releasesBinds: releasesBinds})
		term := a.lead(1)
		c := a.connect()
		a.consume(c)
		answers := a.listen(responseTopic("T1"))
		received := a.link.holdReceivedRequests(t)
		a.submit("T1", "tok:alice:T1", nil)
		received.awaitArrival(t)

		term.cancel()
		waitFor(t, func() bool {
			dispatching := true
			a.locked(func(s *agentState) { dispatching = s.dispatching })
			return !dispatching
		})
		received.open()
		term.endsWithin(standDownWithNothingToWaitFor)
		a.locked(func(s *agentState) {
			if s.giveBacksInFlight != 0 {
				t.Errorf("%d give-backs are counted as in flight after the stand-down, want 0", s.giveBacksInFlight)
			}
		})

		b.lead(2)
		next := b.reconnects()
		held := next.next(typeTask)
		if held.TaskID != "T1" {
			t.Fatalf("the new leader delivered %s, want the request dispatch held", held.TaskID)
		}
		next.send(completed("T1"))
		next.send(ack(held))
		completedOnce(t, answers)
	})
}

func TestATaskTheAgentGaveBackBeforeAStandDownIsRunByTheNewLeaderAndNotFailedByTheOld(t *testing.T) {
	onBothBrokerKinds(t, func(t *testing.T, releasesBinds bool) {
		a, b := newReplicas(t, replicasPlan{releasesBinds: releasesBinds})
		term := a.lead(1)
		c := a.connect()
		a.consume(c)
		answers := a.listen(responseTopic("T1"))
		first := a.lateAckedTask(c, "T1")
		c.send(map[string]any{"type": typeDescribe, "agent": "not an object"})
		c.refused(errCodeBadMessage, "")
		c.send(map[string]any{"type": typeNack, "delivery": first.Delivery})
		waitFor(t, func() bool { return a.brk.acks.Load()+a.brk.nacks.Load() == 1 })

		term.end()
		none(t, answers)
		b.lead(2)
		b.runsAgainAndCompletes("T1", answers)
		a.sweepPastTheTaskTimeLimit()
		none(t, answers)
	})
}

func TestATaskCancelledBeforeItsAckIsAnsweredCanceledAtStandDownAndNeverRunAgain(t *testing.T) {
	for name, sameReplica := range map[string]bool{"the same replica leads again": true, "another replica leads": false} {
		t.Run(name, func(t *testing.T) {
			onBothBrokerKinds(t, func(t *testing.T, releasesBinds bool) {
				a, b := newReplicas(t, replicasPlan{releasesBinds: releasesBinds})
				term := a.lead(1)
				c := a.connect()
				a.consume(c)
				answers := a.listen(responseTopic("T1"))
				a.lateAckedTask(c, "T1")
				a.cancelTakenOffTheQueue(c, "T1")

				term.end()
				canceledOnce(t, answers)
				next := b
				if sameReplica {
					next = a
				}
				next.lead(2)
				next.reconnects().quiet()
				a.sweepPastTheTaskTimeLimit()
				none(t, answers)
			})
		})
	}
}

func TestACancelledTaskAlreadyBackInTheQueueIsAnsweredCanceledAndDroppedWhenItReturnsHere(t *testing.T) {
	a, _ := newReplicas(t, replicasPlan{})
	term := a.lead(1)
	c := a.connect()
	a.consume(c)
	answers := a.listen(responseTopic("T1"))
	first := a.lateAckedTask(c, "T1")
	c.send(map[string]any{"type": typeDescribe, "agent": "not an object"})
	c.refused(errCodeBadMessage, "")
	c.send(map[string]any{"type": typeNack, "delivery": first.Delivery})
	waitFor(t, func() bool { return a.brk.acks.Load() == 1 })
	a.cancel("T1")
	c.next(typeCancel)

	term.end()
	canceledOnce(t, answers)
	a.lead(2)
	a.reconnects().quiet()
	none(t, answers)
}

func TestACancelledTaskWhoseAnswerCannotBePublishedAtStandDownIsAnsweredOnceByALaterSweep(t *testing.T) {
	a, _ := newReplicas(t, replicasPlan{})
	term := a.lead(1)
	c := a.connect()
	a.consume(c)
	answers := a.listen(responseTopic("T1"))
	a.acknowledgedTask(c, "T1")
	a.cancel("T1")
	c.next(typeCancel)

	a.brk.failTopic.Store(responseTopic("T1"))
	term.end()
	none(t, answers)
	if bound, final := a.binding("T1"); !bound || final {
		t.Fatalf("the cancelled task is bound=%t final=%t, want it kept open until its answer is published", bound, final)
	}
	a.brk.failTopic.Store("")
	a.nextSweep()
	canceledOnce(t, answers)
	a.sweepPastTheTaskTimeLimit()
	none(t, answers)
}

func TestACancelForATaskTheOldLeaderForgotIsLeftToTheNewLeader(t *testing.T) {
	onBothBrokerKinds(t, func(t *testing.T, releasesBinds bool) {
		a, b := newReplicas(t, replicasPlan{releasesBinds: releasesBinds})
		term := a.lead(1)
		c := a.connect()
		a.consume(c)
		answers := a.listen(responseTopic("T1"))
		a.lateAckedTask(c, "T1")
		term.end()

		a.cancel("T1")
		none(t, answers)
		if bound, _ := a.binding("T1"); bound {
			t.Fatal("a cancel bound the task again on the replica that had forgotten it")
		}

		b.lead(2)
		next := b.reconnects()
		again := next.next(typeTask)
		requireRedelivered(t, again, "T1")
		next.next(typeCancel)
		next.send(map[string]any{"type": typeResult, "taskId": "T1", "state": stateCanceled, "text": ""})
		next.send(ack(again))
		canceledOnce(t, answers)
		a.sweepPastTheTaskTimeLimit()
		none(t, answers)
	})
}

func TestManyFailuresThatCannotBePublishedHoldAStandDownNoLongerThanItsOneDeadline(t *testing.T) {
	const tasks = 12
	const settleWait = 300 * time.Millisecond
	a, _ := newReplicas(t, replicasPlan{tune: func(adapter *Adapter) { adapter.settleWait = settleWait }})
	term := a.lead(1)
	c := a.connect()
	a.consume(c)
	answers := make([]<-chan *broker.Message, tasks)
	for i := range tasks {
		taskID := fmt.Sprintf("T%d", i)
		answers[i] = a.listen(responseTopic(taskID))
		a.acknowledgedTask(c, taskID)
	}

	a.link.repliesStall.Store(true)
	began := time.Now()
	term.end()
	if took := time.Since(began); took > settleWait+2*time.Second {
		t.Fatalf("the stand-down took %v with %d publishes stalled, want about its %v deadline", took, tasks, settleWait)
	}
	if open := a.adapter.openTasks(); open != tasks {
		t.Fatalf("%d tasks are kept for the sweep, want all %d whose failure was not published", open, tasks)
	}

	a.link.repliesStall.Store(false)
	a.nextSweep()
	for _, answer := range answers {
		failedWith(t, recv(t, answer), msgLinkMoved)
	}
	a.nextSweep()
	a.sweepPastTheTaskTimeLimit()
	for _, answer := range answers {
		none(t, answer)
	}
}

func TestStoppingWhileAStandDownIsUnderWayWaitsForIt(t *testing.T) {
	a, _ := newReplicas(t, replicasPlan{})
	term := a.lead(1)
	c := a.connect()
	a.consume(c)
	answers := a.listen(responseTopic("T1"))
	a.acknowledgedTask(c, "T1")
	publishing := a.link.holdPublishesTo(t, responseTopic("T1"))

	term.cancel()
	publishing.awaitArrival(t)
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		_ = a.adapter.Stop(context.Background())
	}()
	heldBack(t, stopped, "the stop, with a stand-down under way,")
	publishing.open()
	select {
	case <-stopped:
	case <-time.After(closeWait):
		t.Fatal("the stop never returned")
	}
	closedForServiceRestart(t, c)
	failedWith(t, recv(t, answers), msgLinkMoved)
	none(t, answers)
}

func TestAStopThatRunsOutOfTimeStillLetsTheStandDownAnswer(t *testing.T) {
	a, _ := newReplicas(t, replicasPlan{})
	term := a.lead(1)
	c := a.connect()
	a.consume(c)
	answers := a.listen(responseTopic("T1"))
	a.acknowledgedTask(c, "T1")

	outOfTime, cancel := context.WithCancel(context.Background())
	cancel()
	if err := a.adapter.Stop(outOfTime); err != nil {
		t.Fatal(err)
	}
	term.ended()
	failedWith(t, recv(t, answers), msgLinkMoved)
	none(t, answers)
}

func TestAStopThatRunsOutOfTimeReturnsWhileAStandDownStillHoldsTheAgents(t *testing.T) {
	logs := captureLogs(t)
	a, _ := newReplicas(t, replicasPlan{})
	term, _, letGo := standDownHeldWhileItGivesBack(t, a, "T1")
	defer letGo()

	outOfTime, cancel := context.WithCancel(context.Background())
	cancel()
	stopped := make(chan error, 1)
	go func() { stopped <- a.adapter.Stop(outOfTime) }()

	select {
	case err := <-stopped:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Stop with a context already done waited for the stand-down that holds the agents")
	}
	if got := logs.count(stoppedWhileAgentsChanged); got != 1 {
		t.Errorf("WARNs that the stop gave up on the change in progress = %d, want 1", got)
	}
	heldBack(t, term.started.ended, "the stand-down, still giving a request back,")
}

func standDownHeldWhileItGivesBack(t *testing.T, a *replica, taskID string) (term *ledTerm, answers <-chan *broker.Message, letGo func()) {
	t.Helper()
	term = a.lead(1)
	c := a.connect()
	a.consume(c)
	answers = a.listen(responseTopic(taskID))
	a.lateAckedTask(c, taskID)
	requeueing := a.link.holdRequeues(t)
	term.cancel()
	requeueing.awaitArrival(t)
	return term, answers, requeueing.open
}

func TestACancelThatArrivesDuringAStandDownIsLeftToTheNewLeader(t *testing.T) {
	a, b := newReplicas(t, replicasPlan{})
	term, answers, letGo := standDownHeldWhileItGivesBack(t, a, "T1")

	a.cancel("T1")
	heldBack(t, term.started.ended, "the stand-down, with a request still being given back,")
	letGo()
	term.ended()
	none(t, answers)

	b.lead(2)
	next := b.reconnects()
	var again received
	for cancelled := false; again.TaskID == "" || !cancelled; {
		switch f := next.nextOf(typeTask, typeCancel); f.Type {
		case typeTask:
			again = f
		case typeCancel:
			cancelled = true
		}
	}
	requireRedelivered(t, again, "T1")
	next.send(map[string]any{"type": typeResult, "taskId": "T1", "state": stateCanceled, "text": ""})
	next.send(ack(again))
	canceledOnce(t, answers)
}

func TestNoTaskExpiresWhileAStandDownSettlesIt(t *testing.T) {
	a, b := newReplicas(t, replicasPlan{})
	term, answers, letGo := standDownHeldWhileItGivesBack(t, a, "T1")

	a.sweepPastTheTaskTimeLimit()
	letGo()
	term.ended()
	none(t, answers)

	b.lead(2)
	b.runsAgainAndCompletes("T1", answers)
}

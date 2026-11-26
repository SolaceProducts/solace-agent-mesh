// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agentlink

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
)

func leadingLinkRig(t *testing.T, tune func(*Adapter), agents ...map[string]any) (*linkRig, *ledTerm) {
	t.Helper()
	l := newLinkRig(t, linkPlan{leaderOnly: true, agents: agents, tune: tune})
	term := l.lead(1)
	return l, term
}

func (l *linkRig) acknowledged(c *client, name, taskID string) <-chan *broker.Message {
	l.t.Helper()
	answers := l.listen(responseTopic(taskID))
	settled := l.brk.acks.Load()
	acknowledge(c, l.deliver(c, name, taskID))
	waitFor(l.t, func() bool { return l.brk.acks.Load() == settled+1 })
	return answers
}

func (l *linkRig) inHand(c *client, name, taskID string) <-chan *broker.Message {
	l.t.Helper()
	answers := l.listen(responseTopic(taskID))
	l.deliver(c, name, taskID)
	return answers
}

func (l *linkRig) cancelFor(name, taskID string) {
	l.t.Helper()
	if err := l.brk.PublishGuaranteed(l.t.Context(), a2a.AgentRequestTopic(testNS, name),
		&broker.Message{Payload: cancelRequest(taskID), UserProperties: map[string]string{a2a.PropUserID: "alice@example.com"}}); err != nil {
		l.t.Fatal(err)
	}
}

func (l *linkRig) openTasksOf(name string) int {
	if s := l.adapter.agent(name); s != nil {
		return s.openTasks()
	}
	return 0
}

func renamed(name string) map[string]any {
	return linkAgent(name, map[string]any{"display_name": "Deployed again"})
}

func TestAnAgentDeployedAgainWhileItsReplicaLeadsHasItsTasksSettledAsAtTheEndOfATerm(t *testing.T) {
	logs := captureAllLogs(t)
	l, _ := leadingLinkRig(t, nil, linkAgent(agentName), linkAgent(billingDesk))
	claims, billing := l.serving(agentName), l.serving(billingDesk)
	acknowledged := l.acknowledged(billing, billingDesk, "billing-acknowledged")
	untouched := l.acknowledged(claims, agentName, "claims-open")
	inHand := l.inHand(billing, billingDesk, "billing-in-hand")

	l.reconfigure(linkAgent(agentName), renamed(billingDesk))

	closedWith(t, billing, websocket.CloseServiceRestart)
	failedWith(t, recv(t, acknowledged), msgAgentRedeployed)
	none(t, acknowledged)
	none(t, inHand)
	theOneLineLogged(t, logs, settledADepartingAgent, "level=INFO", "agentName="+billingDesk, "failed=1", "forgotten=1", "unanswered=0")

	back := l.serving(billingDesk)
	again := back.next(typeTask)
	requireRedelivered(t, again, "billing-in-hand")
	complete(back, "billing-acknowledged")
	back.refused(errCodeUnknownTask, "billing-acknowledged")
	complete(back, "billing-in-hand")
	acknowledge(back, again)
	completedOnce(t, inHand)
	complete(claims, "claims-open")
	completedOnce(t, untouched)
	none(t, acknowledged)
}

func TestAnAgentRemovedWhileItsReplicaLeadsHasEveryOpenTaskFailedAndNoRequestHandedBack(t *testing.T) {
	logs := captureAllLogs(t)
	l, _ := leadingLinkRig(t, nil, linkAgent(agentName), linkAgent(billingDesk))
	billing := l.serving(billingDesk)
	acknowledged := l.acknowledged(billing, billingDesk, "billing-acknowledged")
	inHand := l.inHand(billing, billingDesk, "billing-in-hand")
	cancelled := l.inHand(billing, billingDesk, "billing-cancelled")
	l.cancelFor(billingDesk, "billing-cancelled")
	billing.next(typeCancel)
	settled := l.brk.acks.Load()

	l.reconfigure(linkAgent(agentName))

	closedWith(t, billing, closeCredentialsRevoked)
	failedWith(t, recv(t, acknowledged), msgAgentRemoved)
	failedWith(t, recv(t, inHand), msgAgentRemoved)
	canceledOnce(t, cancelled)
	none(t, acknowledged)
	none(t, inHand)
	theOneLineLogged(t, logs, settledADepartingAgent, "level=INFO", "agentName="+billingDesk, "failed=3", "forgotten=0", "unanswered=0")
	if handedBack := linesLogged(logs, forgotAHandedBackTask); len(handedBack) != 0 {
		t.Fatalf("a removed agent's task was left to a redelivery its deleted queue cannot make: %v", handedBack)
	}
	const requestsInHand = 2
	if acknowledgedToTheBroker := l.brk.acks.Load() - settled; acknowledgedToTheBroker != requestsInHand || l.brk.nacks.Load() != 0 {
		t.Fatalf("%d requests were acknowledged to the broker and %d negatively, want the %d in the agent's hands acknowledged",
			acknowledgedToTheBroker, l.brk.nacks.Load(), requestsInHand)
	}
	if requeued := l.flows.requeued.Load(); requeued != 0 {
		t.Fatalf("%d requests of the removed agent were put back in a queue that is about to be deleted", requeued)
	}
	if refused := l.refusal(billingDesk, tokenOf(billingDesk)); refused.status != http.StatusUnauthorized {
		t.Fatalf("the removed agent's reconnect was answered %d, want 401", refused.status)
	}
}

func TestAnAgentThatLeavesWhileItsReplicaLeadsHasItsPromptAndPeerCallEndedOnItsConnectionBeforeTheClose(t *testing.T) {
	withPeers := map[string]any{"peers": []any{peerName}}
	for name, departure := range map[string]struct {
		leaves    []map[string]any
		closeCode int
	}{
		"removed": {
			leaves:    []map[string]any{linkAgent(billingDesk)},
			closeCode: closeCredentialsRevoked,
		},
		"deployed again": {
			leaves:    []map[string]any{linkAgent(agentName, withPeers, map[string]any{"display_name": "Deployed again"}), linkAgent(billingDesk)},
			closeCode: websocket.CloseServiceRestart,
		},
		"token replaced": {
			leaves:    []map[string]any{linkAgent(agentName, withPeers, withToken(rotatedToken)), linkAgent(billingDesk)},
			closeCode: closeCredentialsRevoked,
		},
	} {
		t.Run(name, func(t *testing.T) {
			l, _ := leadingLinkRig(t, nil, linkAgent(agentName, withPeers), linkAgent(billingDesk))
			c := l.serving(agentName)
			l.acknowledged(c, agentName, "T1")
			peerRequests, peerTaskID := openWork(t, l.rig, c)

			l.reconfigure(departure.leaves...)

			cancelSent(t, peerRequests, peerTaskID)
			endedBeforeTheClose(t, framesUntilClosedWith(t, c, departure.closeCode))
		})
	}
}

func TestAnAnswerARemovedOrRedeployedAgentsTaskCannotBeGivenIsGivenUpWithOneError(t *testing.T) {
	for name, departure := range map[string]struct {
		leaves     func(l *linkRig) []map[string]any
		answerKind string
	}{
		"removed":        {func(l *linkRig) []map[string]any { return []map[string]any{linkAgent(agentName)} }, `answerKind="agent removed"`},
		"deployed again": {func(l *linkRig) []map[string]any { return []map[string]any{linkAgent(agentName), renamed(billingDesk)} }, `answerKind="agent redeployed"`},
	} {
		t.Run(name, func(t *testing.T) {
			logs := captureAllLogs(t)
			l, _ := leadingLinkRig(t, nil, linkAgent(agentName), linkAgent(billingDesk))
			answers := l.acknowledged(l.serving(billingDesk), billingDesk, "billing-acknowledged")

			l.brk.failTopic.Store(responseTopic("billing-acknowledged"))
			l.reconfigure(departure.leaves(l)...)
			l.brk.failTopic.Store("")

			theOneLineLogged(t, logs, gaveUpAnsweringATask, "level=ERROR", "agentName="+billingDesk, "taskID=billing-acknowledged", departure.answerKind)
			theOneLineLogged(t, logs, settledADepartingAgent, "failed=0", "forgotten=0", "unanswered=1")
			if open := l.openTasksOf(billingDesk); open != 0 {
				t.Fatalf("%d tasks of the earlier agent are open on the one that replaced it", open)
			}
			l.adapter.sweepOnce(time.Now().Add(sweepInterval))
			none(t, answers)
		})
	}
}

func TestAnAgentRemovedOnAFollowerPublishesTheAnswerItsLastTermLeftForTheSweepOrGivesItUp(t *testing.T) {
	for name, brokerRecovered := range map[string]bool{"the broker recovered": true, "the broker still refuses the answer": false} {
		t.Run(name, func(t *testing.T) {
			logs := captureAllLogs(t)
			l, term := leadingLinkRig(t, nil, linkAgent(agentName), linkAgent(billingDesk))
			answers := l.acknowledged(l.serving(billingDesk), billingDesk, "billing-acknowledged")
			l.brk.failTopic.Store(responseTopic("billing-acknowledged"))
			term.end()
			if brokerRecovered {
				l.brk.failTopic.Store("")
			}

			l.reconfigure(linkAgent(agentName))

			gaveUp := linesLogged(logs, gaveUpAnsweringATask)
			if brokerRecovered {
				failedWith(t, recv(t, answers), msgLinkMoved)
				if len(gaveUp) != 0 {
					t.Fatalf("the answer was published and also given up on: %v", gaveUp)
				}
			} else {
				theOneLineLogged(t, logs, gaveUpAnsweringATask, "level=ERROR", "agentName="+billingDesk, "taskID=billing-acknowledged", `answerKind="link moved"`)
				l.brk.failTopic.Store("")
			}
			l.adapter.sweepOnce(time.Now().Add(sweepInterval))
			none(t, answers)
		})
	}
}

func redeployingClaims() map[string]any {
	return linkSettings([]any{claimsAgent(map[string]any{"display_name": "Deployed again"})}, nil)
}

func reconfiguring(a *replica, settings map[string]any) <-chan error {
	done := make(chan error, 1)
	go func() { done <- a.adapter.Reconfigure(context.Background(), settings) }()
	return done
}

func reconfigured(t *testing.T, done <-chan error) {
	t.Helper()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("reconfigure: %v", err)
		}
	case <-time.After(closeWait):
		t.Fatal("the reconfigure never returned")
	}
}

func (r *replica) servesAgainInTerm(number uint64, taskID string) {
	r.t.Helper()
	r.lead(number)
	c := r.connect()
	r.consume(c)
	r.taskOn(c, taskID, nil)
	r.locked(func(s *agentState) {
		if s.settling {
			r.t.Fatal("the agent was left settling")
		}
	})
}

func TestAReconfigureThatArrivesWhileATermEndsWaitsForTheStandDownAndNoTaskIsAnsweredTwice(t *testing.T) {
	a, _ := newReplicas(t, replicasPlan{})
	term := a.lead(1)
	c := a.connect()
	a.consume(c)
	answers := a.listen(responseTopic("T1"))
	a.acknowledgedTask(c, "T1")
	publishing := a.link.holdPublishesTo(t, responseTopic("T1"))

	term.cancel()
	publishing.awaitArrival(t)
	done := reconfiguring(a, redeployingClaims())
	select {
	case err := <-done:
		t.Fatalf("the reconfigure returned %v while the term's stand-down was still settling", err)
	case <-time.After(heldBackFor):
	}
	publishing.open()
	term.endsWithin(standDownWithNothingToWaitFor)
	reconfigured(t, done)
	closedForServiceRestart(t, c)
	failedWith(t, recv(t, answers), msgLinkMoved)
	none(t, answers)

	a.refusesAsFollower(token)
	a.servesAgainInTerm(2, "T2")
	a.sweepPastTheTaskTimeLimit()
	none(t, answers)
}

func TestATermThatEndsWhileAnAgentIsDeployedAgainWaitsForItAndNoTaskIsAnsweredTwice(t *testing.T) {
	a, _ := newReplicas(t, replicasPlan{})
	term := a.lead(1)
	c := a.connect()
	a.consume(c)
	answers := a.listen(responseTopic("T1"))
	a.acknowledgedTask(c, "T1")
	publishing := a.link.holdPublishesTo(t, responseTopic("T1"))

	done := reconfiguring(a, redeployingClaims())
	publishing.awaitArrival(t)
	_, refused, err := a.dial(agentName, token)
	if err == nil || refused == nil || refused.StatusCode != http.StatusServiceUnavailable || refused.Header.Get("Retry-After") != followerRetryAfterSeconds {
		t.Fatalf("a connection opened while its agent settles was answered %v (err %v), want 503 with Retry-After %s", refused, err, followerRetryAfterSeconds)
	}
	if open := len(a.adapter.agent(agentName).connections()); open != 0 {
		t.Fatalf("%d connections joined an agent that is being deployed again", open)
	}
	term.cancel()
	heldBack(t, term.started.ended, "the term's end, with an agent still being deployed again,")
	publishing.open()
	reconfigured(t, done)
	term.endsWithin(standDownWithNothingToWaitFor)
	closedForServiceRestart(t, c)
	failedWith(t, recv(t, answers), msgAgentRedeployed)
	none(t, answers)

	a.refusesAsFollower(token)
	a.servesAgainInTerm(2, "T2")
	a.sweepPastTheTaskTimeLimit()
	none(t, answers)
}

func TestAnAgentAddedWhileItsReplicaLeadsIsSweptRefusedAndGivenUpLikeAConfiguredOne(t *testing.T) {
	for name, brokerRecovers := range map[string]bool{"the broker recovers": true, "the broker never recovers": false} {
		t.Run(name, func(t *testing.T) {
			logs := captureAllLogs(t)
			clock := newSteppedClock()
			l, term := leadingLinkRig(t, clock.runs, linkAgent(agentName))
			l.reconfigure(linkAgent(agentName), linkAgent(collectionsDesk))
			collections := l.serving(collectionsDesk)
			answers := l.acknowledged(collections, collectionsDesk, "collections-task")

			ofAnotherInstance := l.connectAs(collectionsDesk, tokenOf(collectionsDesk))
			complete(ofAnotherInstance, "collections-task")
			if refusal := ofAnotherInstance.refused(errCodeUnknownTask, "collections-task"); refusal.Message != msgTaskBoundElsewhere {
				t.Fatalf("the refusal says %q, want %q", refusal.Message, msgTaskBoundElsewhere)
			}
			theOneLineLogged(t, logs, refusedATaskFrame, "level=WARN", "agentName="+collectionsDesk, "taskID=collections-task", `reason="bound elsewhere"`)

			l.brk.failTopic.Store(responseTopic("collections-task"))
			term.end()
			none(t, answers)
			if brokerRecovers {
				l.brk.failTopic.Store("")
				clock.advance(sweepInterval)
				l.adapter.sweepOnce(clock.now().Add(time.Second))
				failedWith(t, recv(t, answers), msgLinkMoved)
				none(t, answers)
				return
			}
			clock.advance(answerGiveUpAfter)
			l.adapter.sweepOnce(clock.now().Add(time.Second))
			line := theOneLineLogged(t, logs, gaveUpAnsweringATask, "level=ERROR", "agentName="+collectionsDesk, "taskID=collections-task")
			if !strings.Contains(line, `answerKind="link moved"`) {
				t.Fatalf("the give-up does not name the answer: %s", line)
			}
			if open := l.openTasksOf(collectionsDesk); open != 0 {
				t.Fatalf("%d tasks are still open after the give-up", open)
			}
		})
	}
}

func TestTheRosterAFollowerWasGivenIsTheOneItLeadsWith(t *testing.T) {
	l := newLinkRig(t, linkPlan{leaderOnly: true, agents: []map[string]any{
		linkAgent(agentName), linkAgent(billingDesk), linkAgent(dispatchDesk),
	}})

	l.reconfigure(linkAgent(agentName), linkAgent(billingDesk, withToken(rotatedToken)), linkAgent(collectionsDesk))
	l.lead(1)

	if removed := l.refusal(dispatchDesk, tokenOf(dispatchDesk)); removed.status != http.StatusUnauthorized {
		t.Fatalf("the agent removed while this replica followed was answered %d once it led, want 401", removed.status)
	}
	if binds := l.flows.binds(a2a.LinkAgentRequestQueueName(testNS, dispatchDesk)); binds != 0 {
		t.Fatalf("the new leader bound the removed agent's queue %d times", binds)
	}
	if replaced := l.refusal(billingDesk, tokenOf(billingDesk)); replaced.status != http.StatusUnauthorized {
		t.Fatalf("the token replaced while this replica followed was answered %d once it led, want 401", replaced.status)
	}
	for agent, tok := range map[string]string{agentName: tokenOf(agentName), billingDesk: rotatedToken, collectionsDesk: tokenOf(collectionsDesk)} {
		c := l.serving(agent, tok)
		l.deliver(c, agent, agent+"-task")
	}
}

func TestAResultBeingPublishedWhenItsAgentIsDeployedAgainIsWaitedForAndPublished(t *testing.T) {
	a, _ := newReplicas(t, replicasPlan{})
	a.lead(1)
	c := a.connect()
	a.consume(c)
	answers := a.listen(responseTopic("T1"))
	a.lateAckedTask(c, "T1")
	publishing := a.link.holdPublishesTo(t, responseTopic("T1"))
	c.send(completed("T1"))
	publishing.awaitArrival(t)

	done := reconfiguring(a, redeployingClaims())
	select {
	case err := <-done:
		t.Fatalf("the reconfigure returned %v while the agent's result was still being published", err)
	case <-time.After(heldBackFor):
	}
	publishing.open()
	reconfigured(t, done)
	completedOnce(t, answers)
	back := a.reconnects()
	back.quiet()
}

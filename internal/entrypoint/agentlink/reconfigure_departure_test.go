// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agentlink

import (
	"bytes"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
)

const stalledTask = "stalled-answer"

func closedPromptlyWith(t *testing.T, c *client, code int) {
	t.Helper()
	_ = c.ws.SetReadDeadline(time.Now().Add(closeWait))
	closedWith(t, c, code)
}

func theDepartingAgentGaveUpItsStalledAnswer(t *testing.T, logs *wakeLogText, name string) {
	t.Helper()
	var mine []string
	for _, line := range linesLogged(logs, settledADepartingAgent) {
		if strings.Contains(line, "agentName="+name+" ") {
			mine = append(mine, line)
		}
	}
	if len(mine) != 1 || !strings.Contains(mine[0], "unanswered=1") {
		t.Fatalf("%s settled %d times (%v), want once with its stalled answer given up", name, len(mine), mine)
	}
}

func TestAgentsRemovedTogetherSettleTheirTasksAtTheSameTime(t *testing.T) {
	const settleWait = time.Second
	logs := captureAllLogs(t)
	l, _ := leadingLinkRig(t, tunedWaits(settleWait, 0), linkAgent(agentName), linkAgent(billingDesk), linkAgent(dispatchDesk))
	billing, dispatch := l.serving(billingDesk), l.serving(dispatchDesk)
	l.acknowledged(billing, billingDesk, stalledTask)
	l.acknowledged(dispatch, dispatchDesk, stalledTask)
	l.brk.stallTopic.Store(responseTopic(stalledTask))

	began := time.Now()
	l.reconfigure(linkAgent(agentName))
	took := time.Since(began)

	closedPromptlyWith(t, billing, closeCredentialsRevoked)
	closedPromptlyWith(t, dispatch, closeCredentialsRevoked)
	theDepartingAgentGaveUpItsStalledAnswer(t, logs, billingDesk)
	theDepartingAgentGaveUpItsStalledAnswer(t, logs, dispatchDesk)
	for _, name := range []string{billingDesk, dispatchDesk} {
		if open := l.openTasksOf(name); open != 0 {
			t.Fatalf("%d tasks of the removed agent %s are still open", open, name)
		}
	}
	if peak := l.brk.stalledPeak.Load(); peak != 2 {
		t.Fatalf("%d settles were blocked at once at most, want both removed agents' settles in flight together", peak)
	}
	if took < settleWait {
		t.Fatalf("the reconfigure took %v, less than the %v its stalled settles wait for: the stall did not hold", took, settleWait)
	}
	if limit := 2*settleWait - settleWait/4; took >= limit {
		t.Fatalf("the reconfigure took %v for two agents whose settles each wait %v, want them to run at the same time (under %v)", took, settleWait, limit)
	}
}

func TestAgentsRestartedTogetherSettleTheirTasksAtTheSameTime(t *testing.T) {
	for name, restart := range map[string]struct {
		next      []map[string]any
		closeCode int
	}{
		"deployed again": {
			next:      []map[string]any{linkAgent(agentName), renamed(billingDesk), renamed(dispatchDesk)},
			closeCode: websocket.CloseServiceRestart,
		},
		"token replaced": {
			next:      []map[string]any{linkAgent(agentName), linkAgent(billingDesk, withToken(rotatedToken)), linkAgent(dispatchDesk, withToken(rotatedToken+"-dispatch"))},
			closeCode: closeCredentialsRevoked,
		},
	} {
		t.Run(name, func(t *testing.T) {
			logs := captureAllLogs(t)
			l, _ := leadingLinkRig(t, tunedWaits(time.Second, 0), linkAgent(agentName), linkAgent(billingDesk), linkAgent(dispatchDesk))
			billing, dispatch := l.serving(billingDesk), l.serving(dispatchDesk)
			l.acknowledged(billing, billingDesk, stalledTask)
			l.acknowledged(dispatch, dispatchDesk, stalledTask)
			l.brk.stallTopic.Store(responseTopic(stalledTask))

			l.reconfigure(restart.next...)

			closedPromptlyWith(t, billing, restart.closeCode)
			closedPromptlyWith(t, dispatch, restart.closeCode)
			theDepartingAgentGaveUpItsStalledAnswer(t, logs, billingDesk)
			theDepartingAgentGaveUpItsStalledAnswer(t, logs, dispatchDesk)
			if peak := l.brk.stalledPeak.Load(); peak != 2 {
				t.Fatalf("%d settles were blocked at once at most, want both restarted agents' settles in flight together", peak)
			}
		})
	}
}

func TestAnAgentRemovedAfterItDescribedItselfIsDelistedByTheReplicaThatServesAndNotByAFollower(t *testing.T) {
	plan := linkPlan{dev: connectedDevBroker(t), leaderOnly: true, agents: []map[string]any{linkAgent(agentName), linkAgent(billingDesk)}}
	leader, follower := newLinkRig(t, plan), newLinkRig(t, plan)
	leader.lead(1)
	claims := leader.serving(agentName)
	claims.send(map[string]any{"type": typeDescribe, "agent": map[string]any{"description": "claims"}})
	waitFor(t, func() bool {
		published := false
		leader.locked(func(s *agentState) {
			published = s.described != nil && s.described.card.Description == "claims" && bytes.Equal(s.publishedBody, s.described.body)
		})
		return published
	})

	leader.reconfigure(linkAgent(billingDesk))
	follower.reconfigure(linkAgent(billingDesk))

	closedWith(t, claims, closeCredentialsRevoked)
	if removals := leader.flows.removalCards.Load(); removals != 1 {
		t.Fatalf("the serving replica published %d removal cards for the removed agent, want 1", removals)
	}
	if removals := follower.flows.removalCards.Load(); removals != 0 {
		t.Fatalf("the follower published %d removal cards for the removed agent, want 0", removals)
	}
}

func TestAConnectionThatJoinsAfterItsAgentWasRetiredIsClosedForRestartAndNeverRegistered(t *testing.T) {
	l := newLinkRig(t, linkPlan{agents: []map[string]any{linkAgent(agentName), linkAgent(billingDesk)}})
	s := l.adapter.agent(agentName)
	l.adapter.upgrader.CheckOrigin = func(*http.Request) bool {
		s.departing.Store(true)
		return true
	}

	late, _, err := l.dial(agentName, tokenOf(agentName))
	if err != nil {
		t.Fatalf("the handshake of a connection whose agent retired after the pre-check failed: %v", err)
	}

	_ = late.ws.SetReadDeadline(time.Now().Add(closeWait))
	if closed := late.closed(); closed.Code != websocket.CloseServiceRestart || closed.Text != closedForRedeploy {
		t.Fatalf("the late connection closed with %d (%s), want %d (%s)", closed.Code, closed.Text, websocket.CloseServiceRestart, closedForRedeploy)
	}
	if open := len(s.connections()); open != 0 {
		t.Fatalf("%d connections registered on a retired agent", open)
	}
	done := make(chan error, 1)
	go func() { done <- l.adapter.Reconfigure(t.Context(), l.settings(linkAgent(billingDesk))) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("reconfigure: %v", err)
		}
	case <-time.After(closeWait):
		t.Fatal("the reconfigure that removes the retired agent never returned")
	}
}

func TestAReconfigureWhoseDepartingAgentsSettleWhileTheLinkStopsAsksForARestartAndAddsNothing(t *testing.T) {
	const settleWait = time.Second
	l := newLinkRig(t, linkPlan{agents: []map[string]any{linkAgent(agentName), linkAgent(billingDesk)}, tune: tunedWaits(settleWait, 0)})
	billing := l.serving(billingDesk)
	l.acknowledged(billing, billingDesk, stalledTask)
	l.brk.stallTopic.Store(responseTopic(stalledTask))
	reconfigured := make(chan error, 1)
	go func() {
		reconfigured <- l.adapter.Reconfigure(t.Context(), l.settings(linkAgent(agentName), linkAgent(dispatchDesk)))
	}()
	waitFor(t, func() bool { return l.brk.stalledNow.Load() == 1 })
	stopped := make(chan error, 1)
	go func() { stopped <- l.adapter.Stop(t.Context()) }()
	waitFor(t, func() bool { return l.adapter.ctx.Err() != nil })

	select {
	case err := <-reconfigured:
		if !errors.Is(err, entrypoint.ErrRestartRequired) {
			t.Fatalf("Reconfigure that was overtaken by Stop = %v, want an error that asks for a restart", err)
		}
	case <-time.After(10 * settleWait):
		t.Fatal("the reconfigure never returned")
	}
	if l.adapter.agent(dispatchDesk) != nil {
		t.Fatal("a link that began stopping during the reconfigure took on an added agent")
	}
	if err := <-stopped; err != nil {
		t.Fatalf("Stop: %v", err)
	}
}

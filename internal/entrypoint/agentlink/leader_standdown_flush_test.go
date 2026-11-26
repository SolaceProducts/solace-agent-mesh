// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agentlink

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
)

const (
	bigFrameBytes     = 512 << 10
	framesToStall     = 96
	backlogOfAStalled = 4
	cancelledCalls    = settlePublishers + 2
	standDownSettle   = 3 * time.Second
)

type unreadConn struct {
	ws     *websocket.Conn
	server *conn
}

func (r *rig) connectWithoutReading() unreadConn {
	r.t.Helper()
	url := "ws" + strings.TrimPrefix(r.server.URL, "http") + "/v1/connect"
	dialer := websocket.Dialer{Subprotocols: []string{subprotocol}}
	ws, _, err := dialer.Dial(url, http.Header{"Authorization": {"Bearer " + token}, headerAgent: {agentName}})
	if err != nil {
		r.t.Fatal(err)
	}
	r.t.Cleanup(func() { _ = ws.Close() })
	var server *conn
	waitFor(r.t, func() bool {
		if conns := r.adapter.agent(agentName).connections(); len(conns) == 1 {
			server = conns[0]
		}
		return server != nil
	})
	return unreadConn{ws: ws, server: server}
}

func bigPeerResult(i int, text *string) outFrame {
	return outFrame{Type: typePeerResult, SubtaskID: fmt.Sprintf("S%03d", i), State: stateCompleted, Text: text}
}

func (u unreadConn) stalls(t *testing.T) (queued int) {
	t.Helper()
	text := strings.Repeat("x", bigFrameBytes)
	for sent := 0; sent < framesToStall; sent++ {
		u.server.send(bigPeerResult(sent, &text))
		if sent%8 != 7 {
			continue
		}
		time.Sleep(50 * time.Millisecond)
		if len(u.server.out) >= backlogOfAStalled {
			return sent + 1
		}
	}
	t.Fatal("the connection kept draining: its socket never stalled")
	return 0
}

func (u unreadConn) readUntilClosed(t *testing.T) (peerResults int, closeCode int) {
	t.Helper()
	_ = u.ws.SetReadDeadline(time.Now().Add(closeWait))
	for {
		_, data, err := u.ws.ReadMessage()
		var closed *websocket.CloseError
		if errors.As(err, &closed) {
			return peerResults, closed.Code
		}
		if err != nil {
			t.Fatalf("the connection ended with %v, want a close", err)
		}
		var f struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(data, &f) == nil && f.Type == typePeerResult {
			peerResults++
		}
	}
}

func (r *replica) settling() (settling bool) {
	r.locked(func(s *agentState) { settling = s.settling })
	return settling
}

func standDownHeldOpenByAnAgentThatCannotDrain(t *testing.T) (a *replica, term *ledTerm, c *client) {
	t.Helper()
	a, _ = newReplicas(t, replicasPlan{tune: tunedWaits(standDownSettle, 0)})
	term = a.lead(1)
	stuck := a.connectWithoutReading()
	c = a.connect()
	a.consume(c)
	a.acknowledgedTask(c, "T1")
	stuck.stalls(t)
	term.cancel()
	waitFor(t, a.settling)
	return a, term, c
}

func TestAStandDownWhoseAgentConnectionCannotDrainStillAnswersAnAcknowledgedTaskWellInsideItsDeadline(t *testing.T) {
	a, _ := newReplicas(t, replicasPlan{tune: tunedWaits(standDownSettle, 0)})
	term := a.lead(1)
	stuck := a.connectWithoutReading()
	c := a.connect()
	a.consume(c)
	answers := a.listen(responseTopic("T1"))
	a.acknowledgedTask(c, "T1")
	stuck.stalls(t)

	term.cancel()
	failedWith(t, recv(t, answers), msgLinkMoved)
	term.endsWithin(standDownSettle * 2 / 3)
	closedForServiceRestart(t, c)
}

func TestAStandDownDeliversEveryFrameQueuedOnAnAgentConnectionBeforeItClosesIt(t *testing.T) {
	a, _ := newReplicas(t, replicasPlan{})
	term := a.lead(1)
	slow := a.connectWithoutReading()
	queued := slow.stalls(t)

	term.cancel()
	got, code := slow.readUntilClosed(t)

	if got != queued || code != websocket.CloseServiceRestart {
		t.Fatalf("the agent got %d of the %d frames queued and then close %d, want all of them and close %d", got, queued, code, websocket.CloseServiceRestart)
	}
	term.endsWithin(standDownWithNothingToWaitFor)
}

func openPeerCalls(t *testing.T, a *replica, c *client, peerRequests <-chan *broker.Message, count int) {
	t.Helper()
	for i := range count {
		c.send(map[string]any{"type": typePeerCall, "parentTaskId": "T1", "subtaskId": fmt.Sprintf("S%d", i), "peer": peerName,
			"text": "Is policy P-9 active?", "callId": fmt.Sprintf("call-%d", i)})
		recv(t, peerRequests)
	}
}

func TestAStandDownGivesTheAgentEveryEndedPeerCallWhileTheCancelsToThePeersStillStall(t *testing.T) {
	a, _ := newReplicas(t, replicasPlan{tune: tunedWaits(standDownSettle, 0)})
	term := a.lead(1)
	c := a.connect()
	a.consume(c)
	peerRequests := a.listen(a2a.AgentRequestTopic(testNS, peerName))
	a.acknowledgedTask(c, "T1")
	openPeerCalls(t, a, c, peerRequests, cancelledCalls)

	a.brk.stallTopic.Store(a2a.AgentRequestTopic(testNS, peerName))
	began := time.Now()
	term.cancel()
	for range cancelledCalls {
		if f := c.nextOf(typePeerResult); f.State != stateCanceled {
			t.Fatalf("peer result %s, want a canceled one", f.Raw)
		}
	}
	if took := time.Since(began); took > standDownSettle/2 {
		t.Fatalf("the agent had its %d peer calls ended after %v: they waited on the cancels to their peers", cancelledCalls, took)
	}
}

func TestAStandDownSendsEightPeerCancelsAtATimeAndWaitsForAllOfThem(t *testing.T) {
	a, _ := newReplicas(t, replicasPlan{})
	term := a.lead(1)
	c := a.connect()
	a.consume(c)
	peerRequests := a.listen(a2a.AgentRequestTopic(testNS, peerName))
	a.acknowledgedTask(c, "T1")
	openPeerCalls(t, a, c, peerRequests, cancelledCalls)
	cancelling := shutGate(t)
	a.link.heldPublishes.Store(a2a.AgentRequestTopic(testNS, peerName), cancelling)

	term.cancel()
	for range settlePublishers {
		cancelling.awaitArrival(t)
	}
	select {
	case <-cancelling.reached:
		t.Fatalf("more than %d peer cancels were being published at once", settlePublishers)
	case <-term.started.ended:
		t.Fatal("the stand-down ended while its peer cancels were still being published")
	case <-time.After(heldBackFor):
	}

	cancelling.open()
	for range cancelledCalls {
		cancelSentToSomePeerTask(t, peerRequests)
	}
	term.endsWithin(standDownWithNothingToWaitFor)
}

func cancelSentToSomePeerTask(t *testing.T, requests <-chan *broker.Message) {
	t.Helper()
	if m := recv(t, requests); !strings.Contains(string(m.Payload), methodCancel) {
		t.Fatalf("not a cancel: %s", m.Payload)
	}
}

func TestAStandDownWaitsForAPeerResultBeingQueuedBeforeItFlushesAndClosesTheConnections(t *testing.T) {
	a, _ := newReplicas(t, replicasPlan{})
	term := a.lead(1)
	a.locked(func(s *agentState) { s.peerResultsInFlight++ })

	term.cancel()
	select {
	case <-term.started.ended:
		t.Fatal("the stand-down ended with a peer result still being queued")
	case <-time.After(heldBackFor):
	}

	a.locked(func(s *agentState) { s.peerResultsInFlight-- })
	a.adapter.agent(agentName).signalQuiet()
	term.endsWithin(standDownWithNothingToWaitFor)
}

func TestAPromptAndAPeerCallThatReachAStandDownAreAnsweredCanceledOnTheirConnectionBeforeItCloses(t *testing.T) {
	_, term, c := standDownHeldOpenByAnAgentThatCannotDrain(t)

	c.send(map[string]any{"type": typePrompt, "taskId": "T1", "requestId": "q-1", "questions": []any{map[string]any{"question": "Which policy?"}}})
	c.send(map[string]any{"type": typePeerCall, "parentTaskId": "T1", "subtaskId": "S1", "peer": peerName, "text": "Is policy P-9 active?", "callId": "call-1"})

	endedBeforeTheClose(t, framesUntilClosedForServiceRestart(t, c))
	term.endsWithin(standDownSettle)
}

func TestAnApprovalPromptThatReachesAStandDownIsAnsweredNotApproved(t *testing.T) {
	_, term, c := standDownHeldOpenByAnAgentThatCannotDrain(t)

	c.send(map[string]any{"type": typePrompt, "taskId": "T1", "requestId": "q-1", "approval": map[string]any{"tool": "delete_policy"}})

	frames := framesUntilClosedForServiceRestart(t, c)
	var answers int
	for _, f := range frames {
		if f.Type == typePromptAnswer {
			answers++
			if f.RequestID != "q-1" || f.Status != answerCanceled || f.Approved == nil || *f.Approved {
				t.Fatalf("answer %s, want q-1 canceled and not approved", f.Raw)
			}
		}
	}
	if answers != 1 {
		t.Fatalf("the agent got %d answers, want one: %v", answers, frames)
	}
	term.endsWithin(standDownSettle)
}

func TestAResultAndARetireThatReachAStandDownAreDroppedWithoutAnAnswer(t *testing.T) {
	_, term, c := standDownHeldOpenByAnAgentThatCannotDrain(t)

	c.send(completed("T1"))
	c.send(map[string]any{"type": typeRetire})

	for _, f := range framesUntilClosedForServiceRestart(t, c) {
		if f.Type != typePeers {
			t.Fatalf("the stand-down sent %s for a frame it drops, want nothing but the close", f.Raw)
		}
	}
	term.endsWithin(standDownSettle)
}

func TestAStandDownPublishesEveryPeerCancelEvenWhenItsDeadlinePassesWhileTheyWaitForAFreePublisher(t *testing.T) {
	const settle = 300 * time.Millisecond
	a, _ := newReplicas(t, replicasPlan{tune: tunedWaits(settle, 0)})
	term := a.lead(1)
	c := a.connect()
	a.consume(c)
	peerRequests := a.listen(a2a.AgentRequestTopic(testNS, peerName))
	a.acknowledgedTask(c, "T1")
	openPeerCalls(t, a, c, peerRequests, cancelledCalls)
	cancelling := shutGate(t)
	a.link.heldPublishes.Store(a2a.AgentRequestTopic(testNS, peerName), cancelling)

	term.cancel()
	for range settlePublishers {
		cancelling.awaitArrival(t)
	}
	time.Sleep(2 * settle)
	cancelling.open()

	for range cancelledCalls {
		cancelSentToSomePeerTask(t, peerRequests)
	}
	term.endsWithin(standDownWithNothingToWaitFor)
}

func TestAResultAndAUsageCarryingEventThatReachAStandDownAreWarnedWithTheirIDsAndCounted(t *testing.T) {
	logs := captureAllLogs(t)
	_, term, c := standDownHeldOpenByAnAgentThatCannotDrain(t)

	c.send(event("T1", map[string]any{"kind": eventModelFinished, "model": "gpt-4o", "text": "Policy P-9 is active",
		"usage": map[string]any{"input": 7, "output": 3, "cachedInput": 2, "reasoning": 1}}))
	c.send(event("T1", map[string]any{"kind": eventProgress, "text": "Checking policy P-9"}))
	answered := completed("T1")
	answered["text"] = "Policy P-9 is active"
	c.send(answered)

	framesUntilClosedForServiceRestart(t, c)
	term.endsWithin(standDownSettle)

	theOneLineLogged(t, logs, droppedModelUsage, "level=WARN", "agentName="+agentName, "taskID=T1", "traceID=trace-1",
		"inputTokens=7", "outputTokens=3", "cachedInputTokens=2", "reasoningTokens=1")
	theOneLineLogged(t, logs, droppedAResult, "level=WARN", "agentName="+agentName, "taskID=T1", "traceID=trace-1")
	theOneLineLogged(t, logs, stoodDown, "resultsDropped=1", "usageEventsDropped=1")
	theOneLineLogged(t, logs, standDownLostWork, "level=WARN", "usageEventsDropped=1")
	for _, canary := range []string{"Policy P-9 is active", "Checking policy P-9"} {
		for _, line := range append(linesLogged(logs, droppedModelUsage), linesLogged(logs, droppedAResult)...) {
			if strings.Contains(line, canary) {
				t.Fatalf("the drop log carries the frame's text %q: %s", canary, line)
			}
		}
	}
}

func TestADroppedResultIsCountedWithoutWarningThatWorkWasLostBecauseItsAgentSendsItAgain(t *testing.T) {
	logs := captureAllLogs(t)
	_, term, c := standDownHeldOpenByAnAgentThatCannotDrain(t)

	c.send(completed("T1"))

	framesUntilClosedForServiceRestart(t, c)
	term.endsWithin(standDownSettle)

	theOneLineLogged(t, logs, droppedAResult, "level=WARN", "taskID=T1")
	theOneLineLogged(t, logs, stoodDown, "level=INFO", "resultsDropped=1", "usageEventsDropped=0")
	if warned := len(linesLogged(logs, standDownLostWork)); warned != 0 {
		t.Fatalf("a stand-down that dropped only a result, which its agent sends again, warned of lost work %d times", warned)
	}
}

func TestWhatOneStandDownDroppedIsNotReportedAgainByTheNextTerm(t *testing.T) {
	logs := captureAllLogs(t)
	a, first, c := standDownHeldOpenByAnAgentThatCannotDrain(t)
	c.send(completed("T1"))
	framesUntilClosedForServiceRestart(t, c)
	first.endsWithin(standDownSettle)

	second := a.lead(2)
	second.cancel()
	second.endsWithin(standDownSettle)

	lines := linesLogged(logs, stoodDown)
	if len(lines) != 2 {
		t.Fatalf("logged %q %d times, want once for each term:\n%s", stoodDown, len(lines), logs.all())
	}
	if !strings.Contains(lines[0], "resultsDropped=1") {
		t.Fatalf("the first stand-down did not count the result it dropped: %s", lines[0])
	}
	if !strings.Contains(lines[1], "term=2") || !strings.Contains(lines[1], "resultsDropped=0") || !strings.Contains(lines[1], "usageEventsDropped=0") {
		t.Fatalf("the second stand-down reports what the first one dropped: %s", lines[1])
	}
	if warned := len(linesLogged(logs, standDownLostWork)); warned != 0 {
		t.Fatalf("lost work was warned of %d times, want none", warned)
	}
}

// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agentlink

import (
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
)

func progress(taskID string) map[string]any {
	return event(taskID, map[string]any{"kind": eventProgress, "text": "working"})
}

func (r *rig) serverConnOtherThan(known *conn) *conn {
	r.t.Helper()
	for _, c := range r.adapter.agent(agentName).connections() {
		if c != known {
			return c
		}
	}
	r.t.Fatal("the agent has no other connection")
	return nil
}

func TestAFrameForATaskFromAnotherConnectionThanItsLiveOneIsRefused(t *testing.T) {
	const boundElsewhere = "the task is bound to another connection of this agent that is still open"
	logs := captureAllLogs(t)
	r, c := connectedRig(t, nil)
	bound := r.onlyServerConn()
	ofAnotherInstance := r.connect()
	refusedOn := r.serverConnOtherThan(bound)
	answers, status := r.listen(responseTopic("T1")), r.listen(statusTopic("T1"))
	peerRequests := r.listen(a2a.AgentRequestTopic(testNS, peerName))
	r.taskOn(c, "T1", nil)

	const progressText, question, requestID, answerText, peerMessage = "canary-progress-text", "canary-prompt-question",
		"canary-request-id", "canary-answer-text", "canary-peer-message"
	for _, frame := range []map[string]any{
		event("T1", map[string]any{"kind": eventProgress, "text": progressText}),
		{"type": typePrompt, "taskId": "T1", "requestId": requestID, "questions": []any{map[string]any{"question": question}}},
		{"type": typeResult, "taskId": "T1", "state": stateCompleted, "text": answerText},
	} {
		ofAnotherInstance.send(frame)
		if refusal := ofAnotherInstance.refused(errCodeUnknownTask, "T1"); refusal.Message != boundElsewhere {
			t.Fatalf("the refusal says %q, want %q", refusal.Message, boundElsewhere)
		}
	}
	ofAnotherInstance.send(peerCall("S1", peerName, peerMessage))
	if refusal := ofAnotherInstance.refused(errCodeUnknownTask, "S1"); refusal.Message != boundElsewhere {
		t.Fatalf("the refusal says %q, want %q", refusal.Message, boundElsewhere)
	}
	none(t, status)
	none(t, answers)
	none(t, peerRequests)
	refusals := linesLogged(logs, refusedATaskFrame)
	if len(refusals) != 4 {
		t.Fatalf("%d refusals were logged, want one for each of the 4 frames:\n%s", len(refusals), logs.all())
	}
	for i, logged := range map[int][]string{
		0: {"level=DEBUG", "frameType=" + typeEvent},
		1: {"level=WARN", "frameType=" + typePrompt},
		2: {"level=WARN", "frameType=" + typeResult},
		3: {"level=WARN", "frameType=" + typePeerCall},
	} {
		for _, field := range append(logged, "agentName="+agentName, "taskID=T1", "connectionID="+refusedOn.id,
			"boundConnectionID="+bound.id, `reason="bound elsewhere"`) {
			if !strings.Contains(refusals[i], field) {
				t.Fatalf("the refusal's log lacks %s: %s", field, refusals[i])
			}
		}
	}
	for _, content := range []string{progressText, question, requestID, answerText, peerMessage} {
		if strings.Contains(logs.all(), content) {
			t.Fatalf("the log carries %q, which a refused frame sent:\n%s", content, logs.all())
		}
	}

	c.send(completed("T1"))
	completedOnce(t, answers)
}

func TestATaskWhoseConnectionIsGoneIsBoundToTheConnectionThatNextSendsForIt(t *testing.T) {
	logs := captureAllLogs(t)
	r, c := connectedRig(t, nil)
	answers := r.listen(responseTopic("T1"))
	r.taskOn(c, "T1", nil)
	_, peerID := openWork(t, r, c)
	gone := r.onlyServerConn()
	leaves(t, r, c)

	ofItsRun := r.connect()
	taking := r.onlyServerConn()
	ofAnotherInstance := r.connect()
	r.consume(ofAnotherInstance)
	bystanders := []*client{r.connect(), r.connect(), r.connect()}
	status := r.listen(statusTopic("T1"))
	ofItsRun.send(progress("T1"))
	recv(t, status)
	ofItsRun.send(progress("T1"))
	recv(t, status)
	theOneLineLogged(t, logs, boundATaskToItsNext, "level=INFO", "agentName="+agentName, "taskID=T1",
		"connectionID="+taking.id, "earlierConnectionID="+gone.id)

	publishAnswer(t, r, "T1", "alice@example.com", a2a.UserInputResponseData{RequestID: "q-1", Status: answerAnswered,
		Data: map[string]any{"answers": map[string]any{"q0": "Home"}}})
	if answer := ofItsRun.next(typePromptAnswer); answer.RequestID != "q-1" {
		t.Fatalf("prompt answer %s", answer.Raw)
	}
	r.peerAnswers(peerID, "response", peerReply(peerID, a2a.TaskStateCompleted, a2a.NewTextPart("Active")))
	if result := ofItsRun.next(typePeerResult); result.SubtaskID != "S1" {
		t.Fatalf("peer result %s", result.Raw)
	}
	r.cancel("T1")
	ofItsRun.next(typeCancel)
	for _, other := range append(bystanders, ofAnotherInstance) {
		other.quiet()
	}

	ofItsRun.send(map[string]any{"type": typeResult, "taskId": "T1", "state": stateCanceled, "text": ""})
	canceledOnce(t, answers)
}

func TestATaskDeliveredAgainToAnotherConnectionRefusesTheEarlierConnectionsLateFrames(t *testing.T) {
	r := requeueingDevBrokerRig(t, nil)
	earlier := r.connect()
	r.consume(earlier)
	answers, status := r.listen(responseTopic("T1")), r.listen(statusTopic("T1"))
	r.submit("T1", "tok:alice:T1", nil)
	first := earlier.next(typeTask)
	earlier.send(map[string]any{"type": typeDescribe, "agent": "not an object"})
	earlier.refused(errCodeBadMessage, "")
	later := r.connect()
	r.consume(later)
	earlier.send(nack(first))
	again := later.next(typeTask)
	requireRedelivered(t, again, "T1")

	earlier.send(progress("T1"))
	earlier.refused(errCodeUnknownTask, "T1")
	earlier.send(completed("T1"))
	earlier.refused(errCodeUnknownTask, "T1")
	none(t, status)
	none(t, answers)

	later.send(completed("T1"))
	later.send(ack(again))
	completedOnce(t, answers)
}

func TestBindingATaskToItsRunsNewConnectionLeavesWhatAStandDownMakesOfItsRequest(t *testing.T) {
	for name, settled := range map[string]struct {
		acknowledged bool
		failedWith   string
	}{
		"an acknowledged task is failed as moved":                         {true, msgLinkMoved},
		"a task whose request the broker will not deliver again, as lost": {false, msgRequestLost},
	} {
		t.Run(name, func(t *testing.T) {
			a, _ := newReplicas(t, replicasPlan{neverRedelivers: true})
			term := a.lead(1)
			c := a.connect()
			a.consume(c)
			answers, status := a.listen(responseTopic("T1")), a.listen(statusTopic("T1"))
			task := a.lateAckedTask(c, "T1")
			if settled.acknowledged {
				c.send(ack(task))
				waitFor(t, func() bool { return a.brk.acks.Load() == 1 })
			}
			leaves(t, a.rig, c)
			waitFor(t, func() bool { return settled.acknowledged || a.requestLost("T1") })

			a.connect().send(progress("T1"))
			recv(t, status)
			term.end()
			failedWith(t, recv(t, answers), settled.failedWith)
			none(t, answers)
		})
	}
}

func TestATaskWhoseConnectionIsClosedAndNotYetRemovedIsTakenFromAnother(t *testing.T) {
	r, c := connectedRig(t, nil)
	answers := r.listen(responseTopic("T1"))
	r.taskOn(c, "T1", nil)
	closing := r.onlyServerConn()
	closing.closeOnce.Do(func() { close(closing.done) })
	t.Cleanup(func() { closing.closeSocket(websocket.CloseTryAgainLater, "too slow") })

	r.connect().send(completed("T1"))
	completedOnce(t, answers)
}

func TestARoutineRefusalOfATaskFrameKeepsItsWordingAndIsLoggedAtDebug(t *testing.T) {
	const notOpen = "not an open task of this agent"
	logs := captureAllLogs(t)
	clock := newSteppedClock()
	a, _ := newReplicas(t, replicasPlan{tune: clock.runs})
	a.lead(1)
	c := a.connect()
	a.consume(c)
	a.acknowledgedTask(c, "T1")
	a.acknowledgedTask(c, "T2")
	c.send(completed("T1"))
	waitFor(t, func() bool { return a.adapter.openTasks() == 1 })
	clock.advance(a.adapter.cfg.TaskTTL + time.Minute)

	overlong := strings.Repeat("t", maxIDBytes+1)
	for _, refused := range []struct{ taskID, ref, logged, reason string }{
		{"T9", "T9", "T9", "absent"}, {"T1", "T1", "T1", "final"}, {"T2", "T2", "T2", "expired"}, {overlong, "", `""`, "absent"},
	} {
		c.send(completed(refused.taskID))
		if refusal := c.refused(errCodeUnknownTask, refused.ref); refusal.Message != notOpen {
			t.Fatalf("the refusal says %q, want %q", refusal.Message, notOpen)
		}
		lines := linesLogged(logs, refusedATaskFrame)
		for _, field := range []string{"level=DEBUG", "taskID=" + refused.logged + " ", "frameType=" + typeResult, "reason=" + refused.reason} {
			if !strings.Contains(lines[len(lines)-1], field) {
				t.Fatalf("the refusal's log lacks %s: %s", field, lines[len(lines)-1])
			}
		}
	}
	if strings.Contains(logs.all(), overlong) {
		t.Fatal("the log carries a task id longer than an id may be")
	}
}

func TestAnIdleCloseIsLoggedWithTheTasksStillBoundToItsConnection(t *testing.T) {
	logs := captureAllLogs(t)
	r, c := connectedRig(t, nil)
	r.taskOn(c, "T1", nil)
	r.taskOn(c, "T2", nil)
	c.send(completed("T2"))
	waitFor(t, func() bool { return r.adapter.openTasks() == 1 })
	idle := r.onlyServerConn()
	c.send(map[string]any{"type": typeDescribe, "agent": "not an object"})
	c.refused(errCodeBadMessage, "")
	busyElsewhere := r.connect()
	r.consume(busyElsewhere)
	r.taskOn(busyElsewhere, "T3", nil)

	if err := idle.ws.SetReadDeadline(time.Now().Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	if closed := closedWithin(t, c); closed.Code != closeIdle {
		t.Fatalf("closed with %d, want %d (idle)", closed.Code, closeIdle)
	}
	waitFor(t, func() bool { return len(linesLogged(logs, "agent disconnected")) == 1 })
	theOneLineLogged(t, logs, closedAnIdleConnection, "level=INFO", "agentName="+agentName, "connectionID="+idle.id, "boundTasks=1")
	theOneLineLogged(t, logs, "agent disconnected", "level=DEBUG", "connectionID="+idle.id, "closeReason=idle")
}

// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agentlink

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
)

const staleAnswerLog = "agent link dropped an answer to a prompt that is not open"

func framesUntilClosedForServiceRestart(t *testing.T, c *client) []received {
	t.Helper()
	return framesUntilClosedWith(t, c, websocket.CloseServiceRestart)
}

func framesUntilClosedWith(t *testing.T, c *client, code int) []received {
	t.Helper()
	var frames []received
	deadline := time.After(closeWait)
	for {
		select {
		case f, open := <-c.in:
			if open {
				frames = append(frames, f)
				continue
			}
			var closed *websocket.CloseError
			if !errors.As(c.readErr, &closed) || closed.Code != code {
				t.Fatalf("connection ended with %v, want a close with code %d", c.readErr, code)
			}
			return frames
		case <-deadline:
			t.Fatal("the connection was never closed")
		}
	}
}

func endedBeforeTheClose(t *testing.T, frames []received) {
	t.Helper()
	var prompts, peerResults int
	for _, f := range frames {
		switch {
		case f.Type == typePromptAnswer && f.RequestID == "q-1" && f.Status == answerCanceled && f.TaskID == "T1":
			prompts++
		case f.Type == typePeerResult && f.SubtaskID == "S1" && f.State == stateCanceled:
			peerResults++
		case f.Type == typePeers:
		default:
			t.Fatalf("the stand-down sent %s before closing the connection, want only the end of the prompt and of the peer call", f.Raw)
		}
	}
	if prompts != 1 || peerResults != 1 {
		t.Fatalf("before the close the agent got %d cancelled prompt answers and %d cancelled peer results, want one of each: %v", prompts, peerResults, frames)
	}
}

func staleAnswer(t *testing.T, r *rig, taskID, requestID string) {
	t.Helper()
	publishAnswer(t, r, taskID, "alice@example.com", a2a.UserInputResponseData{RequestID: requestID, Status: answerAnswered,
		Data: map[string]any{"answers": map[string]any{"q0": "Home"}}})
}

func TestAPromptAndAPeerCallOpenWhenTheLeaderStandsDownAreEndedOnTheOldConnectionBeforeItsCloseAndTheNewLeaderDropsALaterAnswer(t *testing.T) {
	onBothBrokerKinds(t, func(t *testing.T, releasesBinds bool) {
		logs := captureAllLogs(t)
		a, b := newReplicas(t, replicasPlan{releasesBinds: releasesBinds})
		term := a.lead(1)
		c := a.connect()
		a.consume(c)
		answers := a.listen(responseTopic("T1"))
		a.lateAckedTask(c, "T1")
		peerRequests, peerTaskID := openWork(t, a.rig, c)

		term.end()
		cancelSent(t, peerRequests, peerTaskID)
		endedBeforeTheClose(t, framesUntilClosedForServiceRestart(t, c))
		if bound, _ := a.binding("T1"); bound || a.adapter.openTasks() != 0 {
			t.Fatal("the old leader kept the task whose request went back to the queue")
		}
		none(t, answers)

		b.lead(2)
		next := b.reconnects()
		again := next.next(typeTask)
		requireRedelivered(t, again, "T1")
		staleAnswer(t, b.rig, "T1", "q-1")
		b.peerAnswers(peerTaskID, "response", peerReply(peerTaskID, a2a.TaskStateCompleted, a2a.NewTextPart("Active")))
		waitFor(t, func() bool { return len(linesLogged(logs, staleAnswerLog)) == 1 })
		theOneLineLogged(t, logs, staleAnswerLog, "level=DEBUG", "taskID=T1", "requestID=q-1", "reason="+`"`+answerDroppedTaskResumed+`"`)
		next.quiet()

		next.send(completed("T1"))
		next.send(ack(again))
		completedOnce(t, answers)
	})
}

func TestAnAnswerToAPromptWhoseTaskThisReplicaDoesNotHoldIsDroppedAtDebug(t *testing.T) {
	logs := captureAllLogs(t)
	r, c := connectedRig(t, nil)

	staleAnswer(t, r, "T-gone", "q-1")

	waitFor(t, func() bool { return len(linesLogged(logs, staleAnswerLog)) == 1 })
	theOneLineLogged(t, logs, staleAnswerLog, "level=DEBUG", "agentName="+agentName, "taskID=T-gone", "requestID=q-1", "reason="+`"`+answerDroppedTaskNotOpen+`"`)
	c.quiet()
}

func TestAnAnswerToAPromptOfAnAnsweredTaskIsDroppedAtDebug(t *testing.T) {
	logs := captureAllLogs(t)
	r, c := connectedRig(t, nil)
	answers := r.listen(responseTopic("T1"))
	r.taskOn(c, "T1", nil)
	c.send(completed("T1"))
	recv(t, answers)

	staleAnswer(t, r, "T1", "q-1")

	waitFor(t, func() bool { return len(linesLogged(logs, staleAnswerLog)) == 1 })
	theOneLineLogged(t, logs, staleAnswerLog, "level=DEBUG", "taskID=T1", "reason="+`"`+answerDroppedTaskNotOpen+`"`)
}

func TestAnAnswerToAPromptThatIsNotOpenOnATaskStillRunningHereIsDroppedWithAWarning(t *testing.T) {
	logs := captureAllLogs(t)
	r, c := connectedRig(t, nil)
	r.taskOn(c, "T1", nil)

	staleAnswer(t, r, "T1", "q-closed")

	waitFor(t, func() bool { return len(linesLogged(logs, staleAnswerLog)) == 1 })
	theOneLineLogged(t, logs, staleAnswerLog, "level=WARN", "taskID=T1", "requestID=q-closed", "reason="+`"`+answerDroppedNoSuchPrompt+`"`)
	c.quiet()
}

func TestAConnectionFlushedBeforeItIsClosedForTheHandoverDeliversEveryFrameQueuedOnIt(t *testing.T) {
	const queued = outboxSize / 2
	r, c := connectedRig(t, nil)
	server := r.onlyServerConn()
	for i := range queued {
		if !server.send(outFrame{Type: typePromptAnswer, TaskID: "T1", RequestID: fmt.Sprintf("q-%03d", i), Status: answerCanceled}) {
			t.Fatalf("frame %d was not queued", i)
		}
	}

	ctx, cancel := context.WithTimeout(t.Context(), closeWait)
	defer cancel()
	server.flushWithin(ctx)
	server.closeWithoutWaiting(websocket.CloseServiceRestart, closedForHandover)

	got := 0
	for _, f := range framesUntilClosedForServiceRestart(t, c) {
		if f.Type == typePromptAnswer {
			got++
		}
	}
	if got != queued {
		t.Fatalf("the agent got %d of the %d frames queued before the close", got, queued)
	}
}

// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package teams

import (
	"encoding/json"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
)

const shortQueueStop = 500 * time.Millisecond

const slowerThanAQueueStop = shortQueueStop + time.Second

func requireTaskKeptWhileHeld(t *testing.T, a *TeamsAdapter, taskID string, arrived <-chan struct{}, release func()) {
	t.Helper()
	select {
	case <-arrived:
	case <-time.After(10 * time.Second):
		t.Fatal("the final write never reached Bot Connector")
	}
	time.Sleep(slowerThanAQueueStop)
	if !slices.Contains(a.core.Tasks().AllByAgent(twoInstanceAgent), taskID) {
		t.Fatal("task's record deleted before Teams had its final write")
	}
	release()
	deadline := time.Now().Add(10 * time.Second)
	for slices.Contains(a.core.Tasks().AllByAgent(twoInstanceAgent), taskID) {
		if time.Now().After(deadline) {
			t.Fatal("task's record not deleted once Teams had its final write")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestFinalResponse_TaskKeptUntilTeamsHasTheFinalWrites(t *testing.T) {
	t.Parallel()
	inst := newTeamsInstances(t, 1)
	a := inst.adapters[0]
	a.queueStopTimeout = shortQueueStop
	taskID := submitDM(t, a)
	statusID := inst.bot.postedID(t, twoInstanceStatus)
	arrived, release := inst.bot.hold(t, http.MethodDelete)

	inst.agentStreams(t, taskID, answerStart)
	inst.agentCompletes(t, taskID)

	requireTaskKeptWhileHeld(t, a, taskID, arrived, release)
	inst.bot.waitChanged(t, activityChange{Method: http.MethodDelete, ID: statusID})
}

func TestError_TaskKeptUntilTeamsHasTheErrorText(t *testing.T) {
	t.Parallel()
	inst := newTeamsInstances(t, 1)
	a := inst.adapters[0]
	a.queueStopTimeout = shortQueueStop
	taskID := submitDM(t, a)
	statusID := inst.bot.postedID(t, twoInstanceStatus)
	arrived, release := inst.bot.hold(t, http.MethodPut)

	topic := a2a.GatewayResponseTopic("test-ns", "test-gw", taskID)
	payload, err := json.Marshal(a2a.NewErrorResponse(taskID, -32603, "agent crashed"))
	if err != nil {
		t.Fatal(err)
	}
	if err := inst.brk.PublishGuaranteed(t.Context(), topic, &broker.Message{Topic: topic, Payload: payload}); err != nil {
		t.Fatal(err)
	}

	requireTaskKeptWhileHeld(t, a, taskID, arrived, release)
	const errorText = "❌ An error occurred while the agent was processing your request."
	inst.bot.waitChanged(t, activityChange{Method: http.MethodPut, ID: statusID, Text: errorText})
}

func TestStop_KeepsTheFinalWritesOfATaskEndingHere(t *testing.T) {
	inst := newTeamsInstances(t, 1)
	a := inst.adapters[0]
	a.queueStopTimeout = shortQueueStop
	taskID := submitDM(t, a)
	statusID := inst.bot.postedID(t, twoInstanceStatus)
	arrived, release := inst.bot.hold(t, http.MethodPost)

	inst.agentCompletesWith(t, taskID, answerFinal)
	<-arrived
	a.queuesMu.Lock()
	q := a.queues[taskID]
	a.queuesMu.Unlock()
	waitUntil(t, "the final writes are queued behind the held post", func() bool { return len(q.ops) == 2 })

	if err := a.Stop(t.Context()); err != nil {
		t.Fatal(err)
	}
	release()

	inst.bot.waitChanged(t, activityChange{Method: http.MethodDelete, ID: statusID})
}

func TestStop_DiscardsBufferedAnswerTextWhenRecordsAreShared(t *testing.T) {
	t.Parallel()
	inst := newTeamsInstances(t, 1)
	a := inst.adapters[0]
	taskID := submitDM(t, a)
	inst.agentStreams(t, taskID, answerStart)
	answerID := ""
	waitUntil(t, "the answer is posted", func() bool {
		if !slices.Contains(inst.bot.posted(), answerStart) {
			return false
		}
		answerID = inst.bot.postedID(t, answerStart)
		return true
	})
	a.queuesMu.Lock()
	q := a.queues[taskID]
	a.queuesMu.Unlock()
	if q == nil {
		t.Fatal("the task has no queue")
	}
	q.Flush()
	q.appendText(" and a tail Teams never got")

	if err := a.Stop(t.Context()); err != nil {
		t.Fatal(err)
	}

	if n := inst.bot.edits(answerID); n != 0 {
		t.Errorf("the answer was edited %d times at stop, want 0: the tail is left to the instance that ends the task", n)
	}
}

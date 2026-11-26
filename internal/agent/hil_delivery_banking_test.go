// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
)

func swallowResumes(t *testing.T) {
	t.Helper()
	useResumeHook(t, func(*Agent, *TaskExecutionState) {})
}

func recordedAnswers(t *testing.T, store session.Store, taskID string) []session.ToolCallResult {
	t.Helper()
	cp, err := store.GetCheckpoint(context.Background(), taskID)
	if err != nil {
		t.Fatalf("GetCheckpoint(%q) failed: %v", taskID, err)
	}
	if cp == nil {
		return nil
	}
	return cp.ToolResults
}

func republishedAnswers(brk *mockBroker) int {
	topic := a2a.NotifyResponseTopic("test-ns", "test-agent", askRequestID)
	n := 0
	for _, m := range brk.getPublished() {
		if m.topic == topic {
			n++
		}
	}
	return n
}

func TestAnswerDeliveredToTheHolderSurvivesTheHolderDyingBeforeItResumes(t *testing.T) {
	store := session.NewMemoryStore()
	holder, _, _ := replicaOf(t, store, "holder")
	survivor, survivorLLM, survivorBrk := replicaOf(t, store, "survivor")
	state := pausedOnQuestion(t, store, holder, "task-ask")
	swallowResumes(t)

	if got := holder.routeUserInputResponse(t.Context(), regionAnswer(), hilSenderUser(state.UserProps.UserID)); got != hilDelivered {
		t.Fatalf("routeUserInputResponse() = %v, want delivered", got)
	}
	holder.pausedTasks.Delete("task-ask")
	ownerDies(t, store, "task-ask")
	useResumeHook(t, nil)

	survivor.resumeAbandonedCheckpoints(t.Context(), time.Now())

	assertResumedToCompletion(t, survivor, survivorLLM, survivorBrk, "task-ask", "EMEA")
}

func TestSecondAnswerAfterALocalDeliveryIsNotRecorded(t *testing.T) {
	store := session.NewMemoryStore()
	holder, _, _ := replicaOf(t, store, "holder")
	other, _, otherBrk := replicaOf(t, store, "other")
	state := pausedOnQuestion(t, store, holder, "task-ask")
	swallowResumes(t)
	user := hilSenderUser(state.UserProps.UserID)

	if got := holder.routeUserInputResponse(t.Context(), regionAnswer(), user); got != hilDelivered {
		t.Fatalf("first routeUserInputResponse() = %v, want delivered", got)
	}
	divergent := regionAnswer()
	divergent.Data = map[string]any{"answers": map[string]any{"q0": "APAC"}}
	if got := other.routeUserInputResponse(t.Context(), divergent, user); got != hilUnmatched {
		t.Errorf("second routeUserInputResponse() = %v, want unmatched", got)
	}

	recorded := recordedAnswers(t, store, "task-ask")
	if len(recorded) != 1 || !strings.Contains(recorded[0].Content, "EMEA") || strings.Contains(recorded[0].Content, "APAC") {
		t.Errorf("recorded results = %+v, want exactly the first answer, EMEA", recorded)
	}
	if relayed := republishedAnswers(otherBrk); relayed != 0 {
		t.Errorf("the second answer was republished %d times, want 0", relayed)
	}
}

func TestRelayLeavesAnAnswerToTheReplicaThatHoldsTheTask(t *testing.T) {
	store := session.NewMemoryStore()
	holder, _, holderBrk := replicaOf(t, store, "holder")
	state := pausedOnQuestion(t, store, holder, "task-ask")

	got := holder.relayUserInputResponse(t.Context(), regionAnswer(), hilSenderUser(state.UserProps.UserID), nil)

	if got != hilUnmatched {
		t.Errorf("relayUserInputResponse() = %v, want unmatched", got)
	}
	if recorded := recordedAnswers(t, store, "task-ask"); len(recorded) != 0 {
		t.Errorf("recorded results = %+v, want none", recorded)
	}
	if relayed := republishedAnswers(holderBrk); relayed != 0 {
		t.Errorf("the answer was republished %d times, want 0", relayed)
	}
}

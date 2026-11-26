// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"maps"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

func personWaitOf(agent *Agent, taskID string) *PendingToolCall {
	state := agent.pausedTasks.Load(taskID)
	if state == nil {
		return nil
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	for _, pc := range state.PendingCalls {
		if pc.Kind == PendingCallHILApproval && !pc.notYetDurable {
			snapshot := *pc
			return &snapshot
		}
	}
	return nil
}

func TestAnAdoptedTaskWhoseRemoteCallAskedAPersonWaitsOnThemAndGivesTheModelTheirAnswer(t *testing.T) {
	tests := []struct {
		name        string
		holdRepause bool
	}{
		{name: "the person answers once the task waits", holdRepause: false},
		{name: "the person answers while the adopting replica is still writing the wait", holdRepause: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			const taskID = "task-adopted-person"
			gate := newGatedStore(func(_ *gatedStore, _, corrID string) bool { return tt.holdRepause && corrID == "" })
			owner, _, _ := replicaOf(t, gate, "owner")
			survivor, survivorLLM, survivorBrk := replicaOf(t, gate, "survivor")
			registerRemoteTool(t, survivor, strBroker(t))
			corrID := idgen.New()
			state := pausedOnRemoteCallLostOwner(t, gate, owner, taskID, corrID)
			owner.pausedTasks.Delete(taskID)

			survivor.adoptOrphanReply(survivor.remoteToolReplyAdoption(corrID, askingForAPersonSTRReply(time.Hour)(corrID)))

			if tt.holdRepause {
				waitOrFail(t, gate.reached, "the adopting replica to reach the write of the person's wait")
			} else if !waitUntil(func() bool { return personWaitOf(survivor, taskID) != nil }) {
				t.Fatal("the adopting replica never made the task wait on the person")
			}
			if got := survivor.routeUserInputResponse(t.Context(), personsAnswer(), hilSenderUser(state.UserProps.UserID)); got != hilDelivered {
				t.Fatalf("delivery = %v, want delivered", got)
			}
			if tt.holdRepause {
				close(gate.proceed)
			}

			assertResumedToCompletion(t, survivor, survivorLLM, survivorBrk, taskID, "approved")
			if brieflyBecomes(func() bool { return len(survivorLLM.getCalls()) > 1 }) {
				t.Errorf("%d LLM calls, want the adopted task resumed once with the answer", len(survivorLLM.getCalls()))
			}
		})
	}
}

func adoptingAReplyOnASurvivor(t *testing.T, taskID string, reply func(corrID string) []byte) (*Agent, *mockLLM, *mockBroker, *TaskExecutionState) {
	t.Helper()
	store := session.NewMemoryStore()
	owner, _, _ := replicaOf(t, store, "owner")
	survivor, survivorLLM, survivorBrk := replicaOf(t, store, "survivor")
	registerRemoteTool(t, survivor, strBroker(t))
	corrID := idgen.New()
	state := pausedOnRemoteCallLostOwner(t, store, owner, taskID, corrID)
	owner.pausedTasks.Delete(taskID)

	adopted := make(chan struct{})
	go func() {
		defer close(adopted)
		survivor.adoptOrphanReply(survivor.remoteToolReplyAdoption(corrID, reply(corrID)))
	}()
	select {
	case <-adopted:
	case <-time.After(5 * time.Second):
		t.Fatal("adopting the reply did not return")
	}
	return survivor, survivorLLM, survivorBrk, state
}

func TestAnAdoptedReplyAskingForAPersonWithAnUnusableRequestIdIsRefusedToTheModelAsAnError(t *testing.T) {
	tests := []struct {
		name string
		data map[string]any
	}{
		{
			name: "a request id of 2 MiB",
			data: map[string]any{"hil_request_id": strings.Repeat("x", 2<<20)},
		},
		{
			name: "an empty request id beside padded details",
			data: map[string]any{"details": strings.Repeat("x", 2<<20)},
		},
		{
			name: "a request id of 300 bytes beside padded details",
			data: map[string]any{"hil_request_id": strings.Repeat("r", 300), "details": strings.Repeat("x", 2<<20)},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			const taskID = "task-adopted-unusable-id"
			data := map[string]any{"pending_kind": "hil_approval", "hil_timeout_ns": float64(time.Hour)}
			maps.Copy(data, tt.data)

			survivor, survivorLLM, survivorBrk, _ := adoptingAReplyOnASurvivor(t, taskID, strReplyWith("pending", data))

			assertResumedToCompletion(t, survivor, survivorLLM, survivorBrk, taskID, unusableIDResult)
			calls := survivorLLM.getCalls()
			if texts := toolTextsOf(calls[len(calls)-1]); !slices.Equal(texts, []string{unusableIDResult}) {
				t.Errorf("tool results shown to the model = %q, want only the error refusing the unusable request id", texts)
			}
		})
	}
}

func TestAnAdoptedReplyAskingForAPersonTooLargeToRecordInFullIsStillAnsweredByThePerson(t *testing.T) {
	const taskID = "task-adopted-oversized-details"
	reply := strReplyWith("pending", map[string]any{
		"pending_kind":   "hil_approval",
		"hil_request_id": personWaitRequestID,
		"hil_timeout_ns": float64(time.Hour),
		"details":        strings.Repeat("x", 2<<20),
	})
	survivor, survivorLLM, survivorBrk, state := adoptingAReplyOnASurvivor(t, taskID, reply)
	if !waitUntil(func() bool { return personWaitOf(survivor, taskID) != nil }) {
		t.Fatal("the adopting replica never made the task wait on the person")
	}

	if got := survivor.routeUserInputResponse(t.Context(), personsAnswer(), hilSenderUser(state.UserProps.UserID)); got != hilDelivered {
		t.Fatalf("delivery = %v, want delivered", got)
	}

	assertResumedToCompletion(t, survivor, survivorLLM, survivorBrk, taskID, "approved")
}

func TestAStaleResumeRequestRunsOnlyWhenTheClaimEndsOnTheSameTaskWithNothingLeftToWaitFor(t *testing.T) {
	tests := []struct {
		name            string
		resumeRequested bool
		totalPending    int32
		replaced        bool
		want            int32
	}{
		{name: "a remembered request and nothing left to wait for resumes the task", resumeRequested: true, want: 1},
		{name: "no remembered request does not resume", want: 0},
		{name: "a call still outstanding does not resume", resumeRequested: true, totalPending: 1, want: 0},
		{name: "another task state under the same id does not resume", resumeRequested: true, replaced: true, want: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			agent, _, _ := newTestAgent()
			var resumes atomic.Int32
			useResumeHook(t, func(*Agent, *TaskExecutionState) { resumes.Add(1) })
			state := pausedStateWithHILEntry(t, agent, "task-claimed", "call-claimed", "req-claimed")
			atomic.StoreInt32(&state.TotalPending, tt.totalPending)
			state.resumeRequested.Store(tt.resumeRequested)
			release, claimed := markResuming(state)
			if tt.replaced {
				replacement := newTestTaskState(agent)
				replacement.TaskID = state.TaskID
				agent.pausedTasks.Store(replacement)
			}
			if !claimed {
				t.Fatal("the state could not be claimed")
			}

			agent.endResumeClaim(state, release)

			if got := resumes.Load(); got != tt.want {
				t.Errorf("resumes = %d, want %d", got, tt.want)
			}
			if state.resuming.Load() || state.resumeRequested.Load() {
				t.Errorf("claim still held = %v and request still remembered = %v after the release", state.resuming.Load(), state.resumeRequested.Load())
			}
		})
	}
}

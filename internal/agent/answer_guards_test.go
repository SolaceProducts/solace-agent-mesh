// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func TestUserAnswerNeverResolvesACallThatWaitsOnSomethingElse(t *testing.T) {
	const subTaskID = "sub-answered-by-a-user"
	tests := []struct {
		name   string
		kind   PendingCallKind
		corrID string
	}{
		{name: "a user answer for a delegated peer call is refused", kind: PendingCallPeer, corrID: subTaskID},
		{name: "a user answer for a remote tool call is refused", kind: PendingCallRemoteTool, corrID: subTaskID},
		{name: "a user answer for a workflow call is refused", kind: PendingCallWorkflow, corrID: subTaskID},
		{name: "a user answer for a login wait is refused", kind: PendingCallAuth, corrID: "auth:task-waiting:tc-1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := session.NewMemoryStore()
			owner, _, _ := replicaOf(t, store, "owner")
			logs := &syncLogBuffer{}
			survivor, _, _ := replicaOf(t, store, "survivor", capturingLogs(logs))
			state := pausedOnPeerCall(t, store, "task-waiting", subTaskID)
			state.PendingCalls["tc-1"].Kind = tt.kind
			state.PendingCalls["tc-1"].Timeout = time.Minute
			if err := owner.checkpoints.save(context.Background(), state); err != nil {
				t.Fatalf("owner checkpoint: %v", err)
			}
			loseOwner(t, store, state, 1)
			answer := regionAnswer()
			answer.RequestID = tt.corrID

			survivor.handleNotifyHILResponse(notifyAnswer(t, answer))

			if !waitUntilWithin(5*time.Second, func() bool { return len(logs.records(refusedKindLog)) > 0 }) {
				t.Fatal("the user's answer was not refused")
			}
			if recorded := recordedAnswers(t, store, "task-waiting"); len(recorded) != 0 {
				t.Errorf("recorded results = %+v, want none: a user's answer must not resolve a call that waits on something else", recorded)
			}
		})
	}
}

func TestResumeWaitsForAnAnswerThatIsStillBeingBanked(t *testing.T) {
	inner := session.NewMemoryStore()
	blocked := newBlockedRecordStore(inner)
	releaseStore := sync.OnceFunc(func() { close(blocked.release) })
	t.Cleanup(releaseStore)
	holder, _, _ := replicaOf(t, inner, "holder", func(a *Agent) { a.checkpoints.store = blocked })
	state := pausedOnQuestion(t, inner, holder, "task-ask")

	banked := make(chan struct{})
	go func() {
		holder.bankAnswer(state, state.PendingCalls["tc-1"], tool.OK("EMEA"))
		close(banked)
	}()
	select {
	case <-blocked.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the answer's write never started")
	}
	resumed := make(chan struct{})
	go func() {
		holder.resumeTaskExecution(state)
		close(resumed)
	}()

	select {
	case <-resumed:
		t.Error("the resumed turn ran while the answer's durable write was still in flight")
	case <-time.After(300 * time.Millisecond):
	}
	releaseStore()
	for name, done := range map[string]chan struct{}{"the answer's write": banked, "the resumed turn": resumed} {
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Errorf("%s never finished after the store was released", name)
		}
	}
}

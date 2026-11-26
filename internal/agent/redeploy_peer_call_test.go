// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

func pausedOnRealPeerCall(t *testing.T, owner *Agent, store session.Store) (state *TaskExecutionState, subTaskID string) {
	t.Helper()
	if err := owner.Tools().Register(&peerAgentTool{peerName: "peer", agent: owner, namespace: owner.cfg.Namespace}); err != nil {
		t.Fatalf("register peer tool: %v", err)
	}
	state = pausedOnPeerCall(t, store, idgen.New(), idgen.New())
	pca := defaultPauseCallArgs(owner, state)
	pca.toolCtx.PauseResume = true
	pca.toolCtx.ParentTaskID = state.TaskID
	_, paused := owner.dispatchToolCallsMayPause(t.Context(), state,
		singleCall("peer_peer", "tc-1", `{"task_description":"look it up"}`),
		pca.toolCtx, pca.sp, pca.parallelGroupID, pca.sanitizeErrors, owner.logger)
	if !paused {
		t.Fatal("the task did not pause on the peer call")
	}
	return state, state.PendingCalls["tc-1"].SubTaskID
}

func TestRedeployDoesNotCancelAPeerCallAPausedTaskIsWaitingOn(t *testing.T) {
	store := session.NewMemoryStore()
	owner, _, ownerBrk := replicaOf(t, store, "owner")
	successor, successorLLM, successorBrk := replicaOf(t, store, "successor")

	state, subTaskID := pausedOnRealPeerCall(t, owner, store)

	if err := owner.Stop(t.Context()); err != nil {
		t.Fatalf("stop: %v", err)
	}

	if n := peerCancelsSent(ownerBrk); n != 0 {
		t.Errorf("the redeploy sent %d tasks/cancel to the peer; its work belongs to the successor", n)
	}

	successor.peerRouter.HandleResponse(subTaskID, peerReplyMessage(t, "the peer answered"))

	assertResumedToCompletion(t, successor, successorLLM, successorBrk, state.TaskID, "the peer answered")
}

func TestRedeployOutOfShutdownTimeLeavesThePeerCallAndTheRowAlone(t *testing.T) {
	store := session.NewMemoryStore()
	owner, _, ownerBrk := replicaOf(t, store, "owner")

	state, subTaskID := pausedOnRealPeerCall(t, owner, store)

	expired, cancel := context.WithCancel(t.Context())
	cancel()
	if err := owner.Stop(expired); err != nil {
		t.Fatalf("stop: %v", err)
	}

	if n := peerCancelsSent(ownerBrk); n != 0 {
		t.Errorf("the redeploy sent %d tasks/cancel to the peer; its work belongs to the successor", n)
	}
	time.Sleep(200 * time.Millisecond)
	cp, err := store.GetCheckpoint(t.Context(), state.TaskID)
	if err != nil || cp == nil {
		t.Fatalf("checkpoint = %v, %v", cp, err)
	}
	if cp.PendingCount != 1 || len(cp.ToolResults) != 0 {
		t.Errorf("the row records pending=%d results=%+v; the abandoned wait was written as the peer's answer", cp.PendingCount, cp.ToolResults)
	}

	owner.adoptOrphanReply(peerAdoption(subTaskID, peerReplyMessage(t, "the peer answered late")))

	cp, err = store.GetCheckpoint(t.Context(), state.TaskID)
	if err != nil || cp == nil {
		t.Fatalf("checkpoint after the late reply = %v, %v", cp, err)
	}
	if cp.PendingCount != 0 || len(cp.ToolResults) != 1 {
		t.Errorf("the late reply left pending=%d results=%+v; a handed-off task's reply must be recorded for the successor", cp.PendingCount, cp.ToolResults)
	}
}

func TestUndeployCancelsThePeerCallAPausedTaskIsWaitingOn(t *testing.T) {
	store := session.NewMemoryStore()
	owner, _, ownerBrk := replicaOf(t, store, "owner")

	state, _ := pausedOnRealPeerCall(t, owner, store)

	if err := owner.Remove(t.Context()); err != nil {
		t.Fatalf("remove: %v", err)
	}

	if n := peerCancelsSent(ownerBrk); n != 1 {
		t.Errorf("the undeploy sent %d tasks/cancel to the peer; want 1", n)
	}
	time.Sleep(200 * time.Millisecond)
	cp, err := store.GetCheckpoint(t.Context(), state.TaskID)
	if err != nil || cp == nil {
		t.Fatalf("checkpoint = %v, %v", cp, err)
	}
	if cp.PendingCount != 1 || len(cp.ToolResults) != 0 {
		t.Errorf("the row records pending=%d results=%+v; the cancelled wait was written as the peer's answer", cp.PendingCount, cp.ToolResults)
	}
}

func peerWaitHeld(r *PeerRouter, subTaskID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.pending[subTaskID]
	return ok
}

func TestProcessExitHandsAPeerCallToTheSuccessor(t *testing.T) {
	store := session.NewMemoryStore()
	owner, _, ownerBrk := replicaOf(t, store, "owner")
	successor, successorLLM, successorBrk := replicaOf(t, store, "successor")

	state, subTaskID := pausedOnRealPeerCall(t, owner, store)

	if err := owner.StopForProcessExit(t.Context()); err != nil {
		t.Fatalf("stop for process exit: %v", err)
	}

	if n := peerCancelsSent(ownerBrk); n != 0 {
		t.Errorf("the exit sent %d tasks/cancel to the peer; its work belongs to the successor", n)
	}
	if peerWaitHeld(owner.peerRouter, subTaskID) {
		t.Error("the exiting process still holds the peer wait, so the reply would reach it and be dropped")
	}

	successor.peerRouter.HandleResponse(subTaskID, peerReplyMessage(t, "the peer answered"))

	assertResumedToCompletion(t, successor, successorLLM, successorBrk, state.TaskID, "the peer answered")
}

func TestStopLeavesAMidTurnTaskAndItsPeerWaitToTheTurn(t *testing.T) {
	for name, expired := range map[string]bool{"in time": false, "out of time": true} {
		t.Run(name, func(t *testing.T) {
			store := session.NewMemoryStore()
			owner, _, ownerBrk := replicaOf(t, store, "owner")

			state, subTaskID := pausedOnRealPeerCall(t, owner, store)
			release, claimed := markResuming(state)
			if !claimed {
				t.Fatal("could not mark the task mid-turn")
			}
			defer release()

			stopCtx := t.Context()
			if expired {
				var cancel context.CancelFunc
				stopCtx, cancel = context.WithCancel(t.Context())
				cancel()
			}
			if !peerWaitHeld(owner.peerRouter, subTaskID) {
				t.Fatal("the peer wait was not registered")
			}
			if err := owner.Stop(stopCtx); err != nil {
				t.Fatalf("stop: %v", err)
			}

			if state.Fenced.Load() {
				t.Error("a task mid-turn was fenced; its own turn owns it")
			}
			if owner.pausedTasks.Load(state.TaskID) == nil {
				t.Error("a task mid-turn left the paused set")
			}
			if n := peerCancelsSent(ownerBrk); n != 1 {
				t.Errorf("stop sent %d tasks/cancel for a task mid-turn; want 1", n)
			}
		})
	}
}

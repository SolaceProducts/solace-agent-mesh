// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func TestAdoptionThatNamesNoKindsIsRefused(t *testing.T) {
	store := session.NewMemoryStore()
	owner, _, _ := replicaOf(t, store, "owner")
	logs := &syncLogBuffer{}
	survivor, _, _ := replicaOf(t, store, "survivor", capturingLogs(logs))
	pausedOnQuestionLostOwner(t, store, owner, "task-ask")

	survivor.adoptOrphanReply(orphanAdoption{
		corrID: askRequestID,
		source: "test reply",
		result: func(*session.PendingCall) (*tool.Result, error) { return tool.OK("forged"), nil },
	})

	if len(logs.records(refusedKindLog)) != 1 {
		t.Errorf("refusals logged = %d, want 1: an adoption that names no kind must not accept every kind", len(logs.records(refusedKindLog)))
	}
	if recorded := recordedAnswers(t, store, "task-ask"); len(recorded) != 0 {
		t.Errorf("recorded results = %+v, want none", recorded)
	}
}

func TestEarlyAnswerRefusedForItsSenderNamesTheTask(t *testing.T) {
	logs := &syncLogBuffer{}
	holder, _, _ := replicaOf(t, session.NewMemoryStore(), "holder", capturingLogs(logs))
	holder.expectEarlyAnswer("task-busy", askRequestID, questionOwnerID, time.Hour)

	if got := holder.deliverHILResponse(regionAnswer(), hilSenderUser(strangerID)); got != hilRefused {
		t.Fatalf("deliverHILResponse() = %v, want refused", got)
	}

	refusals := logs.records(refusedAnswerLog)
	if len(refusals) != 1 {
		t.Fatalf("refusals logged = %d, want 1", len(refusals))
	}
	want := map[string]any{"requestID": askRequestID, "taskID": "task-busy"}
	got := map[string]any{"requestID": refusals[0]["requestID"], "taskID": refusals[0]["taskID"]}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("refusal record mismatch (-want +got):\n%s", diff)
	}
}

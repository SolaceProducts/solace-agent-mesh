// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
)

const refusedAnswerLog = "control response refused: sender does not own the paused task"

func TestStrangersAnswerIsRefusedBeforeItIsQueuedForRecording(t *testing.T) {
	base := session.NewMemoryStore()
	holder, _, _ := replicaOf(t, base, "holder")
	logs := &syncLogBuffer{}
	other, _, _ := replicaOf(t, base, "other", capturingLogs(logs))
	pausedOnQuestion(t, base, holder, "task-ask")

	if !other.recordAnswerForTaskHeldElsewhere(regionAnswer(), hilSenderUser(strangerID)) {
		t.Fatal("recordAnswerForTaskHeldElsewhere() = false, want the answer queued")
	}

	if !waitUntilWithin(5*time.Second, func() bool { return len(logs.records(refusedAnswerLog)) > 0 }) {
		t.Fatal("the stranger's answer was not refused")
	}
	if recorded := recordedAnswers(t, base, "task-ask"); len(recorded) != 0 {
		t.Errorf("recorded results = %+v, want none", recorded)
	}
}

func TestOwnersAnswerIsRecordedFromTheAdoptionQueue(t *testing.T) {
	base := session.NewMemoryStore()
	holder, _, _ := replicaOf(t, base, "holder")
	other, _, _ := replicaOf(t, base, "other")
	pausedOnQuestion(t, base, holder, "task-ask")

	if !other.recordAnswerForTaskHeldElsewhere(regionAnswer(), hilSenderUser(questionOwnerID)) {
		t.Fatal("recordAnswerForTaskHeldElsewhere() = false, want the answer queued")
	}

	if !waitUntilWithin(5*time.Second, func() bool { return len(recordedAnswers(t, base, "task-ask")) == 1 }) {
		t.Errorf("recorded results = %+v, want the owner's answer recorded once", recordedAnswers(t, base, "task-ask"))
	}
}

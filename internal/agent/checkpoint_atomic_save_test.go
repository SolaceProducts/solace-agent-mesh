// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
)

type killedBeforeCleanup struct {
	unreadableHolders
}

func (killedBeforeCleanup) DeleteCheckpointIfOwned(context.Context, string, string) (bool, error) {
	return false, errors.New("the process was killed before it could clean up")
}

func TestAPauseWhoseCallsCannotBeRegisteredLeavesNoCheckpointEvenIfTheProcessDiesAfterward(t *testing.T) {
	base := session.NewMemoryStore()
	seedTaskHoldingARequestID(t, base, heldElsewhereID)
	replica, _, _ := replicaOf(t, killedBeforeCleanup{unreadableHolders{Store: base}}, "owner")
	state := newTestTaskState(replica)
	state.TaskID = personWaitTaskID
	state.PendingCalls["call-held"] = &PendingToolCall{
		ToolCallID: "call-held", ToolName: askPersonTool, Kind: PendingCallHILApproval, HILRequestID: heldElsewhereID, Timeout: time.Hour,
	}
	state.TotalPending = 1

	err := replica.checkpoints.save(t.Context(), state)

	var taken *session.CorrelationTakenError
	if !errors.As(err, &taken) {
		t.Fatalf("save error = %v, want the registration refused as a taken correlation", err)
	}
	if cp, getErr := base.GetCheckpoint(t.Context(), personWaitTaskID); getErr != nil || cp != nil {
		t.Errorf("checkpoint left after the refused save = %+v (err %v), want none: a checkpoint with pending calls and no rows can never be resolved", cp, getErr)
	}
}

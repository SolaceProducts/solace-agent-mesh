// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

type approvedCallOutcome struct {
	Refused bool
	Reason  tool.ApprovalRefusal
}

func TestApprovedCallIsRefusedOnlyForACheckpointedTaskNoReplicaHolds(t *testing.T) {
	tests := []struct {
		name         string
		checkpointed bool
		want         approvedCallOutcome
	}{
		{name: "a task whose pause-time save failed still runs its approved call", checkpointed: false, want: approvedCallOutcome{}},
		{name: "a checkpointed task whose checkpoint is gone refuses its approved call", checkpointed: true, want: approvedCallOutcome{Refused: true, Reason: tool.RefusalCancelled}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			holder, _, _ := replicaOf(t, session.NewMemoryStore(), "holder")
			state := &TaskExecutionState{TaskID: "task-approved"}
			state.checkpointed.Store(tt.checkpointed)

			refused, reason := holder.refusalToRunApprovedCall(t.Context(), state, holder.logger)

			got := approvedCallOutcome{Refused: refused != nil, Reason: reason}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("refusalToRunApprovedCall() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

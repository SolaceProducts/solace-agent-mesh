// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func TestRecordingAnAuthFollowersResultKeepsTheCountOfCallsStillOutstanding(t *testing.T) {
	c, store := newEnabledCheckpointer(t)
	state := fullTaskState()
	state.PendingCalls = map[string]*PendingToolCall{
		"tc-peer":     {ToolCallID: "tc-peer", ToolName: "peer_agent", Kind: PendingCallPeer, SubTaskID: "sub-peer", Timeout: time.Minute},
		"tc-leader":   {ToolCallID: "tc-leader", ToolName: "gcal", Kind: PendingCallAuth, CredentialKey: "cred", Timeout: time.Minute},
		"tc-follower": {ToolCallID: "tc-follower", ToolName: "gcal", Kind: PendingCallAuth, CredentialKey: "cred", IsAuthFollower: true, Timeout: time.Minute},
	}
	state.TotalPending = 3
	if err := c.save(t.Context(), state); err != nil {
		t.Fatalf("save: %v", err)
	}

	c.recordResult(t.Context(), state.TaskID, state.PendingCalls["tc-leader"], tool.OK("leader ran"))
	c.recordResult(t.Context(), state.TaskID, state.PendingCalls["tc-follower"], tool.OK("follower ran"))

	cp, err := store.GetCheckpoint(t.Context(), state.TaskID)
	if err != nil || cp == nil {
		t.Fatalf("GetCheckpoint() = %v, %v, want the checkpoint", cp, err)
	}
	type outcome struct {
		PendingCount int
		Recorded     []string
	}
	got := outcome{PendingCount: cp.PendingCount}
	for _, r := range cp.ToolResults {
		got.Recorded = append(got.Recorded, r.ToolCallID)
	}
	want := outcome{PendingCount: 1, Recorded: []string{"tc-leader", "tc-follower"}}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("checkpoint after the leader and its follower were resolved while the peer call is still running (-want +got):\n%s", diff)
	}
}

// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package session

import (
	"context"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

func TestRecordingAnUncountedResultStoresItAndLeavesThePendingCountAlone(t *testing.T) {
	eachStore(t, func(t *testing.T, store checkpointStore, seed func(string, string, string)) {
		ctx := context.Background()
		taskID, sessID := idgen.New(), idgen.New()
		seed("app", "user", sessID)
		if err := store.SaveCheckpoint(ctx, &TaskCheckpoint{
			TaskID: taskID, AppName: "app", UserID: "user", SessionID: sessID, PendingCount: 1,
		}); err != nil {
			t.Fatalf("save checkpoint: %v", err)
		}

		pending, err := store.RecordToolResult(ctx, taskID, "", &ToolCallResult{ToolCallID: "tc-follower", Content: "ran", Uncounted: true})
		if err != nil {
			t.Fatalf("RecordToolResult() = %v, want nil", err)
		}

		cp, getErr := store.GetCheckpoint(ctx, taskID)
		if getErr != nil || cp == nil {
			t.Fatalf("GetCheckpoint() = %v, %v, want the checkpoint", cp, getErr)
		}
		if pending != 1 || cp.PendingCount != 1 {
			t.Errorf("pending count = %d returned and %d stored, want 1: the call was never counted, so resolving it must not release the task early", pending, cp.PendingCount)
		}
		want := []ToolCallResult{{ToolCallID: "tc-follower", Content: "ran"}}
		if diff := cmp.Diff(want, cp.ToolResults); diff != "" {
			t.Errorf("recorded results (-want +got):\n%s", diff)
		}
	})
}

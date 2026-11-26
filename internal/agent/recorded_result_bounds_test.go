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

	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func TestBothRecordingSitesBoundTheMessage(t *testing.T) {
	huge := strings.Repeat("x", maxToolResultContentBytes*3)

	t.Run("the owner recording its own result", func(t *testing.T) {
		store := session.NewMemoryStore()
		agent, _, _ := replicaOf(t, store, "owner")
		ctx := context.Background()
		taskID, sessionID := idgen.New(), idgen.New()
		soon := time.Now().Add(time.Hour)
		if err := store.SaveCheckpoint(ctx, &session.TaskCheckpoint{
			TaskID: taskID, AppName: "test-agent", UserID: "u", SessionID: sessionID,
			AgentName: "test-agent", PendingCount: 1, State: []byte(`{"version":1}`),
			OwnerID: agent.checkpoints.ownerID, LeaseExpiresAt: &soon,
		}); err != nil {
			t.Fatalf("save checkpoint: %v", err)
		}
		if err := store.RegisterPendingCalls(ctx, taskID, heldOwner(t, store, taskID), []session.PendingCall{{
			CorrID: "sub-1", TaskID: taskID, AgentName: "test-agent",
			ToolCallID: "tc-1", ToolName: "peer_agent", Kind: session.PendingKindPeer,
		}}); err != nil {
			t.Fatalf("register: %v", err)
		}

		agent.checkpoints.recordResult(ctx, taskID, &PendingToolCall{
			ToolCallID: "tc-1", ToolName: "peer_agent", Kind: PendingCallPeer, SubTaskID: "sub-1",
		}, tool.OK(huge))

		assertBoundedRecord(ctx, t, store, taskID, "the owner's own recording site")
	})

	t.Run("adopting an orphaned result", func(t *testing.T) {
		store := session.NewMemoryStore()
		agent, _, _ := replicaOf(t, store, "adopter")
		ctx := context.Background()
		taskID, sessionID := idgen.New(), idgen.New()
		lapsed := time.Now().Add(-time.Hour)
		if err := store.SaveCheckpoint(ctx, &session.TaskCheckpoint{
			TaskID: taskID, AppName: "test-agent", UserID: "u", SessionID: sessionID,
			AgentName: "test-agent", PendingCount: 2, State: []byte(`{"version":1}`),
			OwnerID: "a-dead-replica", LeaseExpiresAt: &lapsed,
		}); err != nil {
			t.Fatalf("save checkpoint: %v", err)
		}
		pending := session.PendingCall{
			CorrID: "sub-2", TaskID: taskID, AgentName: "test-agent",
			ToolCallID: "tc-2", ToolName: "peer_agent", Kind: session.PendingKindPeer,
		}
		if err := store.RegisterPendingCalls(ctx, taskID, heldOwner(t, store, taskID), []session.PendingCall{pending}); err != nil {
			t.Fatalf("register: %v", err)
		}

		agent.adoptCheckpointedTask(ctx, &pending, "sub-2", tool.OK(huge), agent.logger)

		assertBoundedRecord(ctx, t, store, taskID, "the orphan-adoption recording site")
	})
}

func assertBoundedRecord(ctx context.Context, t *testing.T, store session.Store, taskID, site string) {
	t.Helper()
	cp, err := store.GetCheckpoint(ctx, taskID)
	if err != nil || cp == nil {
		t.Fatalf("read back: %v, %v", cp, err)
	}
	if len(cp.ToolResults) != 1 {
		t.Fatalf("%s recorded %d results, want 1", site, len(cp.ToolResults))
	}
	got := len(cp.ToolResults[0].Content)
	if got > maxToolResultContentBytes+64 {
		t.Errorf("%s recorded a %d-byte message against a %d-byte bound — the results array is read-modify-written, so this size is paid again on every later record for the task",
			site, got, maxToolResultContentBytes)
	}
	if !strings.Contains(cp.ToolResults[0].Content, "truncated") {
		t.Errorf("%s recorded a clipped message that does not say it was clipped, so a resumed turn reads a partial tool answer as complete", site)
	}
}

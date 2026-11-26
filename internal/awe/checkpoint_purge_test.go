// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package awe

import (
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

func TestPurgeRemovesDayOldCheckpointsInBatches(t *testing.T) {
	store := session.NewMemoryStore()
	exe := NewExecutor(runtime.NewRuntime(broker.NewDevBroker(), nil, runtime.WithSessions(store)).Services())

	ctx := t.Context()
	twoDays, aMinute := time.Now().Add(-48*time.Hour), time.Now().Add(-time.Minute)
	abandoned := make([]string, 0, checkpointPurgeBatch+3)
	for range checkpointPurgeBatch + 3 {
		id := idgen.New()
		abandoned = append(abandoned, id)
		if err := store.SaveCheckpoint(ctx, &session.TaskCheckpoint{TaskID: id, AgentName: "renamed", OwnerID: "gone", LeaseExpiresAt: &twoDays}); err != nil {
			t.Fatalf("save: %v", err)
		}
	}
	if err := store.SaveCheckpoint(ctx, &session.TaskCheckpoint{TaskID: "lapsed-a-minute", AgentName: "live", OwnerID: "gone", LeaseExpiresAt: &aMinute}); err != nil {
		t.Fatalf("save: %v", err)
	}

	exe.purgeAbandonedCheckpoints(ctx)

	for _, id := range abandoned {
		if cp, _ := store.GetCheckpoint(ctx, id); cp != nil {
			t.Fatalf("a checkpoint nobody took over for two days was kept past the first batch")
		}
	}
	if cp, _ := store.GetCheckpoint(ctx, "lapsed-a-minute"); cp == nil {
		t.Fatal("a checkpoint a surviving process is about to take over was purged")
	}
}

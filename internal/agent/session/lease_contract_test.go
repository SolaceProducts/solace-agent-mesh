// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package session

import (
	"context"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

func TestRenewLeasesCountsEveryChunk(t *testing.T) {
	eachStore(t, func(t *testing.T, store checkpointStore, seed func(string, string, string)) {
		ctx := context.Background()
		sessID := idgen.New()
		seed("app", "user", sessID)
		soon := time.Now().Add(time.Hour)

		const tasks = 1200
		ids := make([]string, 0, tasks)
		for range tasks {
			taskID := idgen.New()
			if err := store.SaveCheckpoint(ctx, &TaskCheckpoint{
				TaskID: taskID, AppName: "app", UserID: "user", SessionID: sessID,
				AgentName: "app", OwnerID: "mine", LeaseExpiresAt: &soon,
			}); err != nil {
				t.Fatalf("save: %v", err)
			}
			ids = append(ids, taskID)
		}

		renewed, err := store.RenewLeases(ctx, "mine", ids, time.Now().Add(2*time.Hour))
		if err != nil {
			t.Fatalf("renew: %v", err)
		}
		if renewed != tasks {
			t.Errorf("renewed = %d, want %d — the count is accumulated across IN-clause chunks, and reporting only the last one makes the caller believe it lost tasks it still owns, then fence them", renewed, tasks)
		}
	})
}

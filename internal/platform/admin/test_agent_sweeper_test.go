// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/leader"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func TestTheTestAgentPurgeRunsOnTheLeaderEveryHalfHour(t *testing.T) {
	t.Parallel()
	job := testAgentPurgeJob(nil)
	if job.Name != leader.JobPlatformTestAgentPurge || job.Every != 30*time.Minute {
		t.Errorf("job = %s every %s, want %s every 30m", job.Name, job.Every, leader.JobPlatformTestAgentPurge)
	}
}

func TestTheTestAgentPurgeDeletesExpiredAgentsAndKeepsLiveOnes(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	agentRepo := store.NewAgentRepo(db)
	svc := newTestAgentSvcForUnit(t, agentRepo, store.NewWorkflowRepo(db))
	now := time.Now().UnixMilli()
	expired := seedEphemeralWithExpiry(t, agentRepo, "s-expired", "u-expired", now-1000)
	live := seedEphemeralWithExpiry(t, agentRepo, "s-live", "u-live", now+time.Hour.Milliseconds())

	if err := testAgentPurgeJob(svc).Run(t.Context(), leader.Term{Number: 1}); err != nil {
		t.Fatalf("purge: %v", err)
	}

	if got, _, _, _, _, err := agentRepo.GetByID(t.Context(), expired.ID); err != nil || got != nil {
		t.Errorf("expired agent after the purge = %v (%v), want it deleted", got, err)
	}
	if got, _, _, _, _, err := agentRepo.GetByID(t.Context(), live.ID); err != nil || got == nil {
		t.Errorf("live agent after the purge = %v (%v), want it kept", got, err)
	}
}

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

func bindingAgedBy(t *testing.T, bindings *store.EntrypointIdentityBindingsRepo, gatewayID, value string, age time.Duration) {
	t.Helper()
	seenAt := time.Now().Add(-age).UnixMilli()
	if _, err := bindings.ObserveContinuity(t.Context(), gatewayID, ClaimKeyPhoneNumber, value, "US."+value, "", seenAt); err != nil {
		t.Fatalf("seed binding %s: %v", value, err)
	}
}

func bindingCount(t *testing.T, bindings *store.EntrypointIdentityBindingsRepo, gatewayID, value string) int {
	t.Helper()
	row, err := bindings.Get(t.Context(), gatewayID, ClaimKeyPhoneNumber, value)
	if err != nil {
		t.Fatalf("get binding %s: %v", value, err)
	}
	if row == nil {
		return 0
	}
	return 1
}

func TestDeleteStaleReapsOnlyPastTheCutoff(t *testing.T) {
	_, _, bindings, _ := bindingFixture(t)

	bindingAgedBy(t, bindings, "wa-gw", "15550000001", 200*24*time.Hour)
	bindingAgedBy(t, bindings, "wa-gw", "15550000002", time.Hour)

	cutoff := time.Now().Add(-bindingRetention).UnixMilli()
	removed, err := bindings.DeleteStale(t.Context(), cutoff)
	if err != nil {
		t.Fatalf("DeleteStale: %v", err)
	}
	if removed != 1 {
		t.Errorf("removed = %d, want 1", removed)
	}
	if got := bindingCount(t, bindings, "wa-gw", "15550000001"); got != 0 {
		t.Error("a binding older than the retention window survived the sweep")
	}
	if got := bindingCount(t, bindings, "wa-gw", "15550000002"); got != 1 {
		t.Error("a binding inside the retention window was reaped; a live withhold was thrown away")
	}

	again, err := bindings.DeleteStale(t.Context(), cutoff)
	if err != nil {
		t.Fatalf("DeleteStale (second pass): %v", err)
	}
	if again != 0 {
		t.Errorf("second pass removed = %d, want 0", again)
	}
}

func TestTheBindingSweepRunsOnTheLeaderAndReapsAgedRows(t *testing.T) {
	_, _, bindings, _ := bindingFixture(t)
	bindingAgedBy(t, bindings, "wa-gw", "15550000003", 200*24*time.Hour)

	job := entrypointBindingSweepJob(bindings, discardLogger())
	if job.Name != leader.JobPlatformEntrypointBindingSweep || job.Every != bindingSweepInterval {
		t.Errorf("job = %s every %s, want %s every %s", job.Name, job.Every, leader.JobPlatformEntrypointBindingSweep, bindingSweepInterval)
	}
	if err := job.Run(t.Context(), leader.Term{Number: 1}); err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if bindingCount(t, bindings, "wa-gw", "15550000003") != 0 {
		t.Error("the sweep left a binding older than the retention window")
	}
}

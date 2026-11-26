// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"testing"
	"time"

	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
)

func TestRunningStatus_DegradedWhenProblemsPresent(t *testing.T) {
	t.Run("no problems is running", func(t *testing.T) {
		got := RunningStatus(nil)
		if got.Status() != RuntimeStatusRunning {
			t.Errorf("Status() = %q, want %q", got.Status(), RuntimeStatusRunning)
		}
		if got.Problems() != nil {
			t.Errorf("Problems() = %v, want nil", got.Problems())
		}
	})

	t.Run("empty problems slice is running", func(t *testing.T) {
		got := RunningStatus([]platpkg.Problem{})
		if got.Status() != RuntimeStatusRunning {
			t.Errorf("Status() = %q, want %q", got.Status(), RuntimeStatusRunning)
		}
	})

	t.Run("any problems present is degraded, never running", func(t *testing.T) {
		got := RunningStatus([]platpkg.Problem{{ID: "queue:x", Severity: "degraded", Detail: "boom", Since: time.Now()}})
		if got.Status() != RuntimeStatusDegraded {
			t.Errorf("Status() = %q, want %q", got.Status(), RuntimeStatusDegraded)
		}
		if len(got.Problems()) != 1 {
			t.Errorf("Problems() = %v, want one entry", got.Problems())
		}
	})

	t.Run("a fatal problem also reports degraded, not a separate status", func(t *testing.T) {
		got := RunningStatus([]platpkg.Problem{{ID: "queue:x", Severity: "fatal", Detail: "gone", Since: time.Now()}})
		if got.Status() != RuntimeStatusDegraded {
			t.Errorf("Status() = %q, want %q (severity detail lives in Problems, not the top-level status)", got.Status(), RuntimeStatusDegraded)
		}
	})
}

func TestStartingStoppedDisconnected_NeverCarryProblems(t *testing.T) {
	for name, status := range map[string]RuntimeStatus{
		"starting":     StartingStatus(),
		"disconnected": DisconnectedStatus(),
		"stopped":      StoppedStatus(),
	} {
		if status.Problems() != nil {
			t.Errorf("%s: Problems() = %v, want nil", name, status.Problems())
		}
	}
}

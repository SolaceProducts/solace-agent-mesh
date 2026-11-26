// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/control"
)

func TestGWEStatusTracker_JudgesADeploymentOnEveryLiveGWE(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	tr := newGWEStatusTracker(nil, "ns")
	tr.now = func() time.Time { return now }
	report := func(process, deploymentID, failure string) {
		tr.observe(control.GatewayStatus{ProcessID: process, Entrypoints: []control.EntrypointStatus{{Name: "ep", DeploymentID: deploymentID, Error: failure}}})
	}
	check := func(step string, wantDone bool, wantFailure string) {
		t.Helper()
		if done, failure := tr.outcome("ep", "d1"); done != wantDone || failure != wantFailure {
			t.Fatalf("%s: outcome = %v, %q, want %v, %q", step, done, failure, wantDone, wantFailure)
		}
	}

	report("A", "d1", "")
	check("not listening yet", false, "")
	tr.listening = now
	report("A", "d1", "")
	check("before one report interval", false, "")

	now = now.Add(gweStatusSettle)
	report("A", "d1", "")
	check("one GWE runs it", true, "")
	report("B", "d0", "")
	check("a second GWE still runs the previous deployment", false, "")
	report("B", "d1", "port in use")
	check("the second GWE could not start it", false, "port in use")
	report("B", "d1", "")
	check("both GWEs run it", true, "")

	report("B", "d0", "")
	now = now.Add(control.GatewayStatusExpiry + time.Second)
	report("A", "d1", "")
	check("a GWE silent past the expiry", true, "")

	tr.observe(control.GatewayStatus{ProcessID: "A", Leaving: true})
	check("no live GWE", false, "")
}

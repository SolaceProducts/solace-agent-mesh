// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package a2a

import (
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestLinkAgentQueueNames_AreTheRequestQueueAndTheWakeQueue(t *testing.T) {
	got := LinkAgentQueueNames("acme/prod", "agent_0190f5ec_6f7a_7b8c_ad0e_8a9b0c1d2e3f")

	want := []string{
		"acme/prod/q/a2a/agent_0190f5ec_6f7a_7b8c_ad0e_8a9b0c1d2e3f",
		"acme/prod/q/agentlink/wake/agent_0190f5ec_6f7a_7b8c_ad0e_8a9b0c1d2e3f",
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("LinkAgentQueueNames(acme/prod, agent_…) mismatch (-want +got):\n%s", diff)
	}
}

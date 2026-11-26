// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
)

func TestResolveDelegationTimeout(t *testing.T) {
	if got := resolveDelegationTimeout(45, 600); got != 45*time.Second {
		t.Errorf("configured value: got %v, want 45s", got)
	}
	if got := resolveDelegationTimeout(0, 600); got != 600*time.Second {
		t.Errorf("unset value: got %v, want the 600s default", got)
	}
	if got := resolveDelegationTimeout(-1, 600); got != 600*time.Second {
		t.Errorf("negative value: got %v, want the 600s default", got)
	}
}

func TestDelegationPathsDoNotShareThePeerBudget(t *testing.T) {
	cfg := AgentConfig{PeerRequestTimeoutSeconds: 30}

	peer := resolveDelegationTimeout(
		cfg.PeerRequestTimeoutSeconds, config.DefaultPeerRequestTimeoutSecs)
	if peer != 30*time.Second {
		t.Errorf("peer delegation = %v, want the configured 30s", peer)
	}

	workflow := resolveDelegationTimeout(
		cfg.WorkflowRequestTimeoutSeconds, config.DefaultWorkflowRequestTimeoutSecs)
	if workflow != time.Duration(config.DefaultWorkflowRequestTimeoutSecs)*time.Second {
		t.Errorf("workflow delegation = %v, want the workflow default — a peer timeout must not reach it", workflow)
	}

	subTask := resolveDelegationTimeout(
		cfg.SubTaskTimeoutSeconds, config.DefaultSubTaskTimeoutSecs)
	if subTask != time.Duration(config.DefaultSubTaskTimeoutSecs)*time.Second {
		t.Errorf("sub-task = %v, want the sub-task default — a peer timeout must not reach it", subTask)
	}
}

// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
)

func resolveDelegationTimeout(configuredSecs, defaultSecs int) time.Duration {
	if configuredSecs > 0 {
		return time.Duration(configuredSecs) * time.Second
	}
	return time.Duration(defaultSecs) * time.Second
}

func (a *Agent) delegationBackstops() delegationBackstops {
	peer := resolveDelegationTimeout(a.cfg.PeerRequestTimeoutSeconds, config.DefaultPeerRequestTimeoutSecs)
	if subTask := resolveDelegationTimeout(a.cfg.SubTaskTimeoutSeconds, config.DefaultSubTaskTimeoutSecs); subTask > peer {
		peer = subTask
	}
	return delegationBackstops{
		peer:     peer,
		workflow: resolveDelegationTimeout(a.cfg.WorkflowRequestTimeoutSeconds, config.DefaultWorkflowRequestTimeoutSecs),
	}
}

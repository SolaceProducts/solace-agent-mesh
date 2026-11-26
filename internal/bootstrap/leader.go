// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package bootstrap

import (
	"fmt"
	"log/slog"

	"github.com/SolaceProducts/solace-agent-mesh/internal/leader"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

func NewLeader(svc runtime.Services, namespace string, role leader.Role, logger *slog.Logger) *leader.Elector {
	return leader.New(svc.Broker(), namespace, role, svc.Metrics(), logger)
}

func RegisterLeader(rt *runtime.Runtime, e *leader.Elector) error {
	for _, info := range rt.Components() {
		source, ok := info.Component.(leader.JobSource)
		if !ok {
			continue
		}
		if err := e.Register(source); err != nil {
			return fmt.Errorf("register %s leader jobs: %w", info.Name, err)
		}
	}
	rt.Register("leader", e)
	return nil
}

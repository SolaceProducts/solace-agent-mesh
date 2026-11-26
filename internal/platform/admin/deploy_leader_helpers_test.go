// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/leader"
	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
)

func registerRoutesLeading(t *testing.T, mux *http.ServeMux, core *platpkg.PlatformCore, deps *RouteDeps) {
	t.Helper()
	RegisterRoutesWithDeps(mux, core, deps)
	for _, job := range deps.LeaderJobs {
		if job.Name != leader.JobPlatformDeploys {
			continue
		}
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() {
			defer close(done)
			_ = job.Run(ctx, leader.Term{Number: 1})
		}()
		t.Cleanup(func() {
			cancel()
			<-done
		})
	}
}

func finishInFlight(ctx context.Context, l *deployLeader, agents *DeploymentService, workflows *WorkflowDeploymentService, entrypoints *GatewayDeploymentService) {
	finishWith(l, agents, workflows, entrypoints)
	l.finishLeftOperations(ctx)
}

var deleteKeepQueuesBody = json.RawMessage(`{"deprovision":false}`)

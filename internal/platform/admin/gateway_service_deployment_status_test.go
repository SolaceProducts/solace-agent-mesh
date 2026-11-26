// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/health"
	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func TestGetDeploymentStatus_NilRepo(t *testing.T) {
	t.Parallel()

	svc := &GatewayService{
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	if got := svc.getDeploymentStatus(context.Background(), "any-id"); got != DisplayStatusNotDeployed {
		t.Errorf("getDeploymentStatus with nil repo = %q, want %q", got, DisplayStatusNotDeployed)
	}
}

func TestGatewayService_BuildResponse_DisconnectedRuntime(t *testing.T) {
	t.Parallel()

	gw := &store.Gateway{
		Description: strPtr("test gateway for fixture purposes"),
		ID:          "gw-id-1",
		Name:        "test-gw",
		Type:        "webui",
		CreatedBy:   "u",
		UpdatedBy:   "u",
	}
	pastGrace := time.Now().Add(-10 * runtimeGracePeriod).UnixMilli()
	dep := func(ts int64) *store.GatewayDeployment {
		return &store.GatewayDeployment{
			GatewayID:   gw.ID,
			Status:      DeployStatusSuccess,
			Action:      DeployActionDeploy,
			CreatedBy:   "u",
			UpdatedTime: ts,
		}
	}

	t.Run("registry_branch_disconnected_past_grace", func(t *testing.T) {
		svc := &GatewayService{gatewayRegistry: platpkg.NewGatewayRegistry()}
		resp := svc.buildResponse(gw, DisplayStatusDeployed, dep(pastGrace))
		if resp.RuntimeStatus.Status() != RuntimeStatusDisconnected {
			t.Errorf("RuntimeStatus = %q, want %q", resp.RuntimeStatus.Status(), RuntimeStatusDisconnected)
		}
	})

	t.Run("registry_branch_stopped_when_not_deployed", func(t *testing.T) {
		svc := &GatewayService{gatewayRegistry: platpkg.NewGatewayRegistry()}
		resp := svc.buildResponse(gw, DisplayStatusNotDeployed, nil)
		if resp.RuntimeStatus.Status() != RuntimeStatusStopped {
			t.Errorf("RuntimeStatus = %q, want %q", resp.RuntimeStatus.Status(), RuntimeStatusStopped)
		}
	})
}

func TestGatewayService_BuildResponse_RegistryKeyIsGatewayID(t *testing.T) {
	t.Parallel()

	gw := &store.Gateway{
		Description: strPtr("test gateway for fixture purposes"),
		ID:          "gw-id-runtime-lookup",
		Name:        "test-gw",
		Type:        "webui",
		CreatedBy:   "u",
		UpdatedBy:   "u",
	}

	t.Run("registry_branch_keyed_by_gw_id", func(t *testing.T) {
		reg := platpkg.NewGatewayRegistry()
		reg.AddOrUpdate(a2a.AgentCard{Name: gw.ID})
		svc := &GatewayService{gatewayRegistry: reg}
		resp := svc.buildResponse(gw, DisplayStatusDeployed, nil)
		if resp.RuntimeStatus.Status() != RuntimeStatusRunning {
			t.Errorf("RuntimeStatus = %q, want %q (registry lookup must key off gw.ID)",
				resp.RuntimeStatus.Status(), RuntimeStatusRunning)
		}
	})
}

func TestGatewayService_BuildResponse_RegistryKeyIsSlugWhenSet(t *testing.T) {
	t.Parallel()

	gw := &store.Gateway{
		Description: strPtr("test gateway for fixture purposes"),
		ID:          "gw-id-slugged",
		Slug:        "my-mcp-gateway",
		Name:        "test-gw",
		Type:        "mcp",
		CreatedBy:   "u",
		UpdatedBy:   "u",
	}

	t.Run("registry_branch_keyed_by_slug_not_gw_id", func(t *testing.T) {
		reg := platpkg.NewGatewayRegistry()
		reg.AddOrUpdate(a2a.AgentCard{Name: gw.Slug})
		svc := &GatewayService{gatewayRegistry: reg}
		resp := svc.buildResponse(gw, DisplayStatusDeployed, nil)
		if resp.RuntimeStatus.Status() != RuntimeStatusRunning {
			t.Errorf("RuntimeStatus = %q, want %q (card lookup must key off the slug when set, not gw.ID)",
				resp.RuntimeStatus.Status(), RuntimeStatusRunning)
		}
	})
}

func TestGatewayService_BuildResponse_DegradedWithActiveProblems(t *testing.T) {
	t.Parallel()

	gw := &store.Gateway{
		Description: strPtr("test gateway for fixture purposes"),
		ID:          "gw-id-degraded",
		Name:        "test-gw",
		Type:        "webui",
		CreatedBy:   "u",
		UpdatedBy:   "u",
	}

	reg := platpkg.NewGatewayRegistry()
	reg.AddOrUpdate(a2a.AgentCard{
		Name: gw.ID,
		Capabilities: a2a.AgentCapabilities{Extensions: []a2a.AgentExtension{
			a2a.HealthProblemsExtension([]health.Problem{{ID: "queue:x", Severity: health.Degraded, Detail: "broker queue provisioning temporarily unavailable (capacity or connectivity issue); retrying automatically"}}),
		}},
	})
	svc := &GatewayService{gatewayRegistry: reg}
	resp := svc.buildResponse(gw, DisplayStatusDeployed, nil)

	if resp.RuntimeStatus.Status() != RuntimeStatusDegraded {
		t.Errorf("RuntimeStatus.Status() = %q, want %q", resp.RuntimeStatus.Status(), RuntimeStatusDegraded)
	}
	if len(resp.RuntimeStatus.Problems()) != 1 || resp.RuntimeStatus.Problems()[0].ID != "queue:x" {
		t.Errorf("RuntimeStatus.Problems() = %v, want one problem with id queue:x", resp.RuntimeStatus.Problems())
	}
}

func TestGatewayService_BuildResponse_DegradedWithActiveProblems_Slugged(t *testing.T) {
	t.Parallel()

	gw := &store.Gateway{
		Description: strPtr("test gateway for fixture purposes"),
		ID:          "gw-id-degraded-slugged",
		Slug:        "my-mcp-gateway",
		Name:        "test-gw-slugged",
		Type:        "mcp",
		CreatedBy:   "u",
		UpdatedBy:   "u",
	}

	reg := platpkg.NewGatewayRegistry()
	reg.AddOrUpdate(a2a.AgentCard{
		Name: gw.Slug,
		Capabilities: a2a.AgentCapabilities{Extensions: []a2a.AgentExtension{
			a2a.HealthProblemsExtension([]health.Problem{{ID: "queue:x", Severity: health.Degraded, Detail: "broker queue provisioning temporarily unavailable (capacity or connectivity issue); retrying automatically"}}),
		}},
	})
	svc := &GatewayService{gatewayRegistry: reg}
	resp := svc.buildResponse(gw, DisplayStatusDeployed, nil)

	if resp.RuntimeStatus.Status() != RuntimeStatusDegraded {
		t.Errorf("RuntimeStatus.Status() = %q, want %q", resp.RuntimeStatus.Status(), RuntimeStatusDegraded)
	}
	if len(resp.RuntimeStatus.Problems()) != 1 || resp.RuntimeStatus.Problems()[0].ID != "queue:x" {
		t.Errorf("RuntimeStatus.Problems() = %v, want one problem with id queue:x", resp.RuntimeStatus.Problems())
	}
}

func TestGatewayService_BuildResponse_UnionOfEveryGWEsProblems(t *testing.T) {
	t.Parallel()
	gw := &store.Gateway{ID: "gw-two", Name: "two", Type: "webui", CreatedBy: "u", UpdatedBy: "u"}
	reg := platpkg.NewGatewayRegistry()
	card := func(problems ...health.Problem) a2a.AgentCard {
		c := a2a.AgentCard{Name: gw.ID}
		if len(problems) > 0 {
			c.Capabilities.Extensions = []a2a.AgentExtension{a2a.HealthProblemsExtension(problems)}
		}
		return c
	}
	startFailed := health.Problem{ID: "start", Severity: health.Degraded, Detail: string(health.DetailStartFailed)}
	reg.AddOrUpdateInstance(card(startFailed), "gwe-a")
	reg.AddOrUpdateInstance(card(), "gwe-b")
	reg.AddOrUpdateInstance(card(startFailed), "gwe-c")
	resp := (&GatewayService{gatewayRegistry: reg}).buildResponse(gw, DisplayStatusDeployed, nil)
	if resp.RuntimeStatus.Status() != RuntimeStatusDegraded || len(resp.RuntimeStatus.Problems()) != 1 {
		t.Fatalf("runtime status = %q with %v, want degraded with the one problem", resp.RuntimeStatus.Status(), resp.RuntimeStatus.Problems())
	}
}

func TestGatewayService_BuildResponse_StartFailures(t *testing.T) {
	t.Parallel()
	gw := &store.Gateway{ID: "gw-start", Name: "start", Type: "webui", CreatedBy: "u", UpdatedBy: "u"}
	failed := a2a.AgentCard{Name: gw.ID, Capabilities: a2a.AgentCapabilities{Extensions: []a2a.AgentExtension{
		a2a.HealthProblemsExtension([]health.Problem{{ID: health.StartFailedProblemID, Severity: health.Degraded, Detail: string(health.DetailStartFailed)}}),
	}}}
	reg := platpkg.NewGatewayRegistry()
	reg.AddOrUpdateInstance(failed, "gwe-a/gw-start")
	svc := &GatewayService{gatewayRegistry: reg}
	if got := svc.buildResponse(gw, DisplayStatusDeployed, nil).RuntimeStatus.Status(); got != RuntimeStatusStopped {
		t.Fatalf("no GWE serves it: status %q, want stopped", got)
	}
	reg.AddOrUpdateInstance(a2a.AgentCard{Name: gw.ID}, "gwe-b-core")
	resp := svc.buildResponse(gw, DisplayStatusDeployed, nil)
	if resp.RuntimeStatus.Status() != RuntimeStatusDegraded || len(resp.RuntimeStatus.Problems()) != 1 {
		t.Fatalf("one GWE serves it: status %q with %v, want degraded with the failure", resp.RuntimeStatus.Status(), resp.RuntimeStatus.Problems())
	}
}

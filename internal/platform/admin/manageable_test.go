// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"fmt"
	"log/slog"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/health"
	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func testManageablePlatform() *ManageablePlatform {
	return &ManageablePlatform{
		deps:   &RouteDeps{},
		logger: slog.Default(),
	}
}

func TestManageablePlatform_UnknownResource(t *testing.T) {
	t.Parallel()
	mp := testManageablePlatform()
	_, err := mp.HandleManagement(context.Background(), "get", []string{"unknown"}, nil, nil)
	if err == nil {
		t.Fatal("expected error for unknown resource")
	}
}

func TestManageablePlatform_EmptyPath(t *testing.T) {
	t.Parallel()
	mp := testManageablePlatform()
	_, err := mp.HandleManagement(context.Background(), "get", nil, nil, nil)
	if err == nil {
		t.Fatal("expected error for empty path")
	}
}

func TestManageablePlatform_MethodNotAllowed(t *testing.T) {
	t.Parallel()
	mp := testManageablePlatform()
	_, err := mp.HandleManagement(context.Background(), "delete", []string{"agents"}, nil, nil)
	if err == nil {
		t.Fatal("expected error for non-GET method")
	}
}

func TestRequireIdentityScope_NilIdentity(t *testing.T) {
	t.Parallel()
	err := requireIdentityScope(nil, "agent_builder:*:read")
	if err != nil {
		t.Fatalf("nil identity should allow all: %v", err)
	}
}

func TestRequireIdentityScope_EmptyScopes(t *testing.T) {
	t.Parallel()
	id := &auth.Identity{Scopes: nil}
	err := requireIdentityScope(id, "agent_builder:*:read")
	if err != nil {
		t.Fatalf("empty scopes should allow all: %v", err)
	}
}

func TestRequireIdentityScope_ExactMatch(t *testing.T) {
	t.Parallel()
	id := &auth.Identity{Scopes: []string{"agent_builder:*:read"}}
	err := requireIdentityScope(id, "agent_builder:*:read")
	if err != nil {
		t.Fatalf("exact match should succeed: %v", err)
	}
}

func TestRequireIdentityScope_WildcardMatch(t *testing.T) {
	t.Parallel()
	id := &auth.Identity{Scopes: []string{"agent_builder:*"}}
	err := requireIdentityScope(id, "agent_builder:*:read")
	if err != nil {
		t.Fatalf("wildcard match should succeed: %v", err)
	}
}

func TestRequireIdentityScope_GlobalWildcard(t *testing.T) {
	t.Parallel()
	id := &auth.Identity{Scopes: []string{"*"}}
	err := requireIdentityScope(id, "agent_builder:*:read")
	if err != nil {
		t.Fatalf("global wildcard should succeed: %v", err)
	}
}

func TestRequireIdentityScope_NoMatch(t *testing.T) {
	t.Parallel()
	id := &auth.Identity{Scopes: []string{"connector:*:read"}}
	err := requireIdentityScope(id, "agent_builder:*:read")
	if err == nil {
		t.Fatal("non-matching scope should fail")
	}
}

func TestRequireIdentityScope_PlatformWildcards(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		granted  string
		required string
		wantErr  bool
	}{
		{"global wildcard grants anything", "*", "agent_builder:*:read", false},
		{"exact match", "agent_builder:*:read", "agent_builder:*:read", false},
		{"trailing wildcard at depth 2", "agent_builder:*", "agent_builder:*:read", false},
		{"trailing wildcard at depth 1", "*", "agent_builder:*:read", false},
		{"mid-segment wildcard *:*:read", "*:*:read", "agent_builder:*:read", false},
		{"first-segment wildcard agent_builder:*:read", "agent_builder:*:read", "agent_builder:*:read", false},
		{"verb mismatch", "agent_builder:*:read", "agent_builder:*:update", true},
		{"resource mismatch", "connector:*:read", "agent_builder:*:read", true},
		{"trailing wildcard wrong resource", "agent_builder:*", "connector:*:read", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id := &auth.Identity{Scopes: []string{tt.granted}}
			err := requireIdentityScope(id, tt.required)
			gotErr := err != nil
			if gotErr != tt.wantErr {
				t.Errorf("requireIdentityScope(granted=%q, required=%q): got err=%v, wantErr=%v",
					tt.granted, tt.required, err, tt.wantErr)
			}
		})
	}
}

func TestManagementEndpoints(t *testing.T) {
	t.Parallel()
	mp := testManageablePlatform()
	endpoints := mp.ManagementEndpoints()
	if len(endpoints) != 29 {
		t.Fatalf("expected 29 endpoints, got %d", len(endpoints))
	}

	wantPaths := map[string]bool{
		"model-configs/{aliasOrID}":              false,
		"webui-settings":                         false,
		"budget-state":                           false,
		"models":                                 false,
		"connectors/lookup/byName":               false,
		"workflows/validate/connectorToolInputs": false,
		"tools/describe":                         false,
		"connectors/openapi/parse":               false,
		"connectors/types":                       false,
		"model-grants/{id}/token":                false,
		"put model-grants/{id}":                  false,
		"delete model-grants/{id}":               false,
	}
	for _, ep := range endpoints {
		if _, tracked := wantPaths[ep.Method+" "+ep.Path]; tracked {
			wantPaths[ep.Method+" "+ep.Path] = true
		}
		if ep.Method == "post" && (ep.Path == "connectors/lookup/byName" || ep.Path == "workflows/validate/connectorToolInputs" || ep.Path == "tools/describe" || ep.Path == "connectors/openapi/parse" || ep.Path == "model-grants/{id}/token") {
			wantPaths[ep.Path] = true
		}
		if ep.Method == "get" {
			if _, tracked := wantPaths[ep.Path]; tracked {
				wantPaths[ep.Path] = true
			}
		}
	}
	for path, found := range wantPaths {
		if !found {
			t.Errorf("%s endpoint missing", path)
		}
	}
}

func TestIncludeRegistryCardInList(t *testing.T) {
	t.Parallel()

	knownDBAgents := map[string]bool{
		"agent_managed_running": true,
		"agent_managed_disconn": true,
	}

	ephemeralCard := a2a.AgentCard{
		Name: "agent_019e_test_uuid",
		Capabilities: a2a.AgentCapabilities{
			Extensions: []a2a.AgentExtension{
				{URI: a2a.EphemeralAgentExtensionURI, Params: map[string]any{"ephemeral": true}},
			},
		},
	}

	cases := []struct {
		name string
		card a2a.AgentCard
		want bool
	}{
		{
			name: "ordinary registry-only agent is included",
			card: a2a.AgentCard{Name: "agent_discovered_uuid"},
			want: true,
		},
		{
			name: "DB-known agent is skipped (dedup)",
			card: a2a.AgentCard{Name: "agent_managed_running"},
			want: false,
		},
		{
			name: "ephemeral test agent (extension marker) is skipped",
			card: ephemeralCard,
			want: false,
		},
		{
			name: "ephemeral card that also matches a DB entry is still skipped (dedup short-circuit takes priority)",
			card: a2a.AgentCard{
				Name: "agent_managed_running",
				Capabilities: a2a.AgentCapabilities{
					Extensions: []a2a.AgentExtension{
						{URI: a2a.EphemeralAgentExtensionURI, Params: map[string]any{"ephemeral": true}},
					},
				},
			},
			want: false,
		},
		{
			name: "card with extensions but no ephemeral marker is included",
			card: a2a.AgentCard{
				Name: "agent_discovered_uuid",
				Capabilities: a2a.AgentCapabilities{
					Extensions: []a2a.AgentExtension{
						{URI: "https://solace.com/a2a/extensions/agent-type"},
					},
				},
			},
			want: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := includeRegistryCardInList(tc.card, knownDBAgents)
			if got != tc.want {
				t.Errorf("includeRegistryCardInList(%q, …) = %v, want %v", tc.card.Name, got, tc.want)
			}
		})
	}
}

func TestListAgentsWithRuntime_RuntimeStatusPerBranch(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	agentRepo := store.NewAgentRepo(db)
	deployRepo := store.NewAgentDeploymentRepo(db)

	core := platpkg.NewPlatformCore(nil, platpkg.ExtensionPoints{})
	registry := core.AgentRegistry()
	svc := NewAgentService(agentRepo, deployRepo, nil, nil, registry, nil, nil)
	mp := &ManageablePlatform{
		PlatformCore: core,
		deps:         &RouteDeps{AgentSvc: svc},
		logger:       slog.Default(),
	}

	mkAgent := func(name string) *store.Agent {
		a := &store.Agent{Name: name, Type: "standard", CreatedBy: "u", UpdatedBy: "u"}
		if err := agentRepo.Create(context.Background(), a, nil, []string{"text"}, []string{"text"}, nil, nil); err != nil {
			t.Fatalf("create agent %q: %v", name, err)
		}
		return a
	}

	running := mkAgent("degraded-agent")
	disconnected := mkAgent("disconnected-agent")
	_ = mkAgent("stopped-agent")

	registry.AddOrUpdate(a2a.AgentCard{
		Name: AgentInstanceName(running),
		Capabilities: a2a.AgentCapabilities{Extensions: []a2a.AgentExtension{
			a2a.HealthProblemsExtension([]health.Problem{{ID: "queue:x", Severity: health.Degraded, Detail: "boom"}}),
		}},
	})

	hash := "h"
	snap := `{"name":"disconnected-agent"}`
	if err := deployRepo.Create(context.Background(), &store.AgentDeployment{
		AgentID:             disconnected.ID,
		Status:              DeployStatusSuccess,
		Action:              DeployActionDeploy,
		ConfigHash:          &hash,
		AgentConfigSnapshot: &snap,
		CreatedBy:           "u",
		UpdatedTime:         time.Now().Add(-10 * runtimeGracePeriod).UnixMilli(),
	}); err != nil {
		t.Fatalf("seed deployment: %v", err)
	}

	resp, err := mp.listAgentsWithRuntime(context.Background())
	if err != nil {
		t.Fatalf("listAgentsWithRuntime: %v", err)
	}

	byName := make(map[string]AgentResponse, len(resp.Data))
	for _, a := range resp.Data {
		byName[a.Name] = a
	}

	if got := byName["degraded-agent"].RuntimeStatus; got.Status() != RuntimeStatusDegraded || len(got.Problems()) != 1 {
		t.Errorf("degraded-agent RuntimeStatus = %+v, want Degraded with one problem", got)
	}
	if got := byName["disconnected-agent"].RuntimeStatus; got.Status() != RuntimeStatusDisconnected || len(got.Problems()) != 0 {
		t.Errorf("disconnected-agent RuntimeStatus = %+v, want Disconnected with no problems", got)
	}
	if got := byName["stopped-agent"].RuntimeStatus; got.Status() != RuntimeStatusStopped || len(got.Problems()) != 0 {
		t.Errorf("stopped-agent RuntimeStatus = %+v, want Stopped with no problems", got)
	}
}

func TestListAgentsWithRuntime_CountIsTheStoreTotalPlusDiscovered(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	agentRepo := store.NewAgentRepo(db)
	core := platpkg.NewPlatformCore(nil, platpkg.ExtensionPoints{})
	registry := core.AgentRegistry()
	mp := &ManageablePlatform{
		PlatformCore: core,
		deps:         &RouteDeps{AgentSvc: NewAgentService(agentRepo, store.NewAgentDeploymentRepo(db), nil, nil, registry, nil, nil)},
		logger:       slog.Default(),
	}

	const inStore = controlListMaxRows + 1
	for i := range inStore {
		a := &store.Agent{Name: fmt.Sprintf("agent-%04d", i), Type: "standard", CreatedBy: "u", UpdatedBy: "u"}
		if err := agentRepo.Create(t.Context(), a, nil, []string{"text"}, []string{"text"}, nil, nil); err != nil {
			t.Fatalf("create agent %q: %v", a.Name, err)
		}
	}
	registry.AddOrUpdate(a2a.AgentCard{Name: "agent_discovered_only"})

	resp, err := mp.listAgentsWithRuntime(t.Context())
	if err != nil {
		t.Fatalf("listAgentsWithRuntime: %v", err)
	}
	if len(resp.Data) != controlListMaxRows+1 {
		t.Errorf("got %d rows, want the %d-row ceiling plus the discovered agent", len(resp.Data), controlListMaxRows)
	}
	if resp.Meta.Pagination.Count != inStore+1 {
		t.Errorf("count = %d, want the store's %d plus the discovered agent", resp.Meta.Pagination.Count, inStore)
	}
}

func TestAgentResponseFromCard_RuntimeStatus(t *testing.T) {
	t.Parallel()

	card := a2a.AgentCard{
		Name: "agent_discovered",
		Capabilities: a2a.AgentCapabilities{Extensions: []a2a.AgentExtension{
			a2a.HealthProblemsExtension([]health.Problem{{ID: "queue:y", Severity: health.Fatal, Detail: "gone"}}),
		}},
	}

	resp := agentResponseFromCard(card)

	if resp.RuntimeStatus.Status() != RuntimeStatusDegraded {
		t.Errorf("RuntimeStatus.Status() = %q, want degraded", resp.RuntimeStatus.Status())
	}
	if len(resp.RuntimeStatus.Problems()) != 1 || resp.RuntimeStatus.Problems()[0].Severity != "fatal" {
		t.Errorf("RuntimeStatus.Problems() = %v, want one fatal problem", resp.RuntimeStatus.Problems())
	}
}

// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

func TestCollectResourceInstances_WalksAllPages(t *testing.T) {
	pages := map[int][]AgentResponse{
		1: {{Name: "A", InstanceName: "agent_1"}, {Name: "B", InstanceName: "agent_2"}},
		2: {{Name: "C", InstanceName: "agent_3"}},
	}
	var calls []int
	list := func(_ context.Context, page, _ int) (*PaginatedResponse[AgentResponse], error) {
		calls = append(calls, page)
		data, ok := pages[page]
		if !ok {
			t.Fatalf("requested page %d beyond the last page", page)
		}
		return &PaginatedResponse[AgentResponse]{Data: data, Meta: NewPaginationMeta(page, 2, 3)}, nil
	}

	got, err := collectResourceInstances(t.Context(), list, agentToInstance)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("collected %d instances, want 3: %+v", len(got), got)
	}
	if len(calls) != 2 {
		t.Errorf("lister called on pages %v, want exactly [1 2]", calls)
	}
	for i, want := range []string{"agent_1", "agent_2", "agent_3"} {
		if got[i].CanonicalName != want {
			t.Errorf("instance %d canonicalName = %q, want %q", i, got[i].CanonicalName, want)
		}
	}
}

func TestCollectResourceInstances_SinglePage(t *testing.T) {
	list := func(_ context.Context, page, _ int) (*PaginatedResponse[AgentResponse], error) {
		if page != 1 {
			t.Fatalf("single-page listing must not request page %d", page)
		}
		return &PaginatedResponse[AgentResponse]{
			Data: []AgentResponse{{Name: "Only", InstanceName: "agent_only"}},
			Meta: NewPaginationMeta(1, 20, 1),
		}, nil
	}
	got, err := collectResourceInstances(t.Context(), list, agentToInstance)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if len(got) != 1 || got[0].CanonicalName != "agent_only" {
		t.Fatalf("got %+v, want single agent_only", got)
	}
}

func TestCollectResourceInstances_PropagatesError(t *testing.T) {
	sentinel := fmt.Errorf("store unavailable")
	list := func(_ context.Context, _, _ int) (*PaginatedResponse[AgentResponse], error) {
		return nil, sentinel
	}
	_, err := collectResourceInstances(t.Context(), list, agentToInstance)
	if err == nil {
		t.Fatal("expected error to propagate, got nil")
	}
}

func TestAgentToInstance_CanonicalNamePrefersInstanceName(t *testing.T) {
	withInstance := agentToInstance(AgentResponse{Name: "HR Agent", InstanceName: "agent_019f"})
	if withInstance.CanonicalName != "agent_019f" {
		t.Errorf("canonicalName = %q, want the broker InstanceName agent_019f", withInstance.CanonicalName)
	}
	if withInstance.DisplayName != "HR Agent" {
		t.Errorf("displayName = %q, want HR Agent", withInstance.DisplayName)
	}

	builtin := agentToInstance(AgentResponse{Name: "OrchestratorBuiltin"})
	if builtin.CanonicalName != "OrchestratorBuiltin" {
		t.Errorf("canonicalName = %q, want fallback to Name for a registry built-in", builtin.CanonicalName)
	}
}

func TestWorkflowToInstance_CanonicalNameIsBrokerInstanceName(t *testing.T) {
	inst := workflowToInstance(WorkflowResponse{ID: "0199-abcd", Name: "Nightly Report"})
	want := a2a.InstanceName(a2a.KindWorkflow, "0199-abcd")
	if inst.CanonicalName != want {
		t.Errorf("canonicalName = %q, want %q", inst.CanonicalName, want)
	}
	if inst.DisplayName != "Nightly Report" {
		t.Errorf("displayName = %q, want Nightly Report", inst.DisplayName)
	}
}

func TestAgentInvokeScopeResolver_StandardAgent(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	agentRepo := store.NewAgentRepo(db)
	agentSvc := NewAgentService(agentRepo, nil, nil, nil, platpkg.NewAgentRegistry(), nil, slog.Default())
	agent := &store.Agent{Name: "Web Search", Type: "standard", Description: usPtr("resolver test agent"), CreatedBy: "u", UpdatedBy: "u"}
	if err := agentRepo.Create(t.Context(), agent, nil, []string{"text"}, []string{"text"}, nil, nil); err != nil {
		t.Fatalf("create agent: %v", err)
	}

	got, err := agentInvokeScopeResolver(agentSvc, nil)(t.Context(), agent.ID)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if want := rbac.AgentInvokeScope(agent.ID); got != want {
		t.Errorf("resolver scope = %q, want %q (agent:<uuid>:invoke)", got, want)
	}
}

func TestAgentInvokeScopeResolver_RemoteFallback(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	agentSvc := NewAgentService(store.NewAgentRepo(db), nil, nil, nil, platpkg.NewAgentRegistry(), nil, slog.Default())
	a2aSvc := NewA2AAgentService(store.NewA2AAgentRepo(db), nil, nil, platpkg.NewAgentRegistry(), nil, nil, "ns", slog.Default())

	remote := &store.A2AAgent{Name: "Remote Bot", AgentURL: "https://example.com", AgentCardLocation: "none", AgentAuthType: usPtr("none"), AgentAuthConfig: usPtr("{}"), RequestTimeoutSeconds: 120, CreatedBy: "u", UpdatedBy: "u"}
	if err := store.NewA2AAgentRepo(db).Create(t.Context(), remote); err != nil {
		t.Fatalf("create remote agent: %v", err)
	}

	got, err := agentInvokeScopeResolver(agentSvc, a2aSvc)(t.Context(), remote.ID)
	if err != nil {
		t.Fatalf("resolve (standard miss should fall back to a2a): %v", err)
	}
	if want := rbac.AgentInvokeScope(remote.ID); got != want {
		t.Errorf("remote-agent scope = %q, want %q (agent:<uuid>:invoke)", got, want)
	}
}

func TestWorkflowInvokeScopeResolver_RoundTrip(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	workflowRepo := store.NewWorkflowRepo(db)
	workflowSvc := NewWorkflowService(workflowRepo, nil, nil, nil, nil, "ns", slog.Default())

	wf := &store.Workflow{Name: "Nightly Report", AppConfig: `{"workflow":{"nodes":[]}}`, CreatedBy: "u", UpdatedBy: "u"}
	if err := workflowRepo.Create(t.Context(), wf, store.WorkflowBindings{}); err != nil {
		t.Fatalf("create workflow: %v", err)
	}

	got, err := workflowInvokeScopeResolver(workflowSvc)(t.Context(), wf.ID)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if want := rbac.WorkflowInvokeScopeForInstance(a2a.InstanceName(a2a.KindWorkflow, wf.ID)); got != want {
		t.Errorf("resolver scope = %q, want %q (workflow:<uuid>:invoke)", got, want)
	}
	if rbac.CheckScopes([]string{rbac.ScopeAgentInvokeAny}, []string{got}) {
		t.Errorf("workflow scope %q must not be satisfied by agent:*:invoke", got)
	}
}

func TestSplitScope(t *testing.T) {
	cat, res, verb := splitScope("agent:*:invoke")
	if cat != "agent" || res != "*" || verb != "invoke" {
		t.Errorf("splitScope(agent:*:invoke) = (%q,%q,%q)", cat, res, verb)
	}
	if c, r, v := splitScope("malformed:pair"); c != "" || r != "" || v != "" {
		t.Errorf("malformed scope must return empty segments, got (%q,%q,%q)", c, r, v)
	}
}

func TestVerbActionLabel(t *testing.T) {
	if got := verbActionLabel("invoke"); got != "Invoke" {
		t.Errorf("verbActionLabel(invoke) = %q, want Invoke", got)
	}
	if got := verbActionLabel(""); got != "" {
		t.Errorf("verbActionLabel(\"\") = %q, want empty", got)
	}
}

func TestPlatformMappers_KeyOnDashedUUIDNotCardName(t *testing.T) {
	agent := AgentResponse{
		ID:           "019e47f4-2be0-75d8-828e-77a0d1d9221f",
		Name:         "HR Assistant",
		InstanceName: "agent_019e47f4_2be0_75d8_828e_77a0d1d9221f",
	}
	if got := platformAgentToInstance(agent); got.CanonicalName != agent.ID || got.DisplayName != agent.Name {
		t.Errorf("platformAgentToInstance = %+v, want CanonicalName=dashed uuid, DisplayName=name", got)
	}
	if got := agentToInstance(agent); got.CanonicalName != agent.InstanceName {
		t.Errorf("agentToInstance = %+v, want CanonicalName=broker card name (invoke-side contract)", got)
	}

	wf := WorkflowResponse{ID: "019e47f4-1234-7abc-8def-0123456789ab", Name: "Payroll"}
	if got := platformWorkflowToInstance(wf); got.CanonicalName != wf.ID || got.DisplayName != wf.Name {
		t.Errorf("platformWorkflowToInstance = %+v, want CanonicalName=dashed uuid", got)
	}
	if got := workflowToInstance(wf); got.CanonicalName == wf.ID {
		t.Error("workflowToInstance must key on the underscored broker instance name, not the dashed uuid")
	}
}

func TestAgentSubjectResourceResolver_EnumeratesAgents(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	agentRepo := store.NewAgentRepo(db)
	agentSvc := NewAgentService(agentRepo, store.NewAgentDeploymentRepo(db), store.NewConnectorRepo(db), nil, platpkg.NewAgentRegistry(), nil, slog.Default())

	agent := &store.Agent{Name: "HR Assistant", Type: "standard", Description: usPtr("subject resolver agent"), CreatedBy: "u", UpdatedBy: "u"}
	if err := agentRepo.Create(t.Context(), agent, nil, []string{"text"}, []string{"text"}, nil, nil); err != nil {
		t.Fatalf("create agent: %v", err)
	}

	got, err := agentSubjectResourceResolver(agentSvc)(t.Context())
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("resolved %d instances, want the seeded agent: %+v", len(got), got)
	}
	if got[0].CanonicalName != agent.ID || got[0].DisplayName != "HR Assistant" {
		t.Errorf("agent instance = %+v, want CanonicalName=%q DisplayName=HR Assistant", got[0], agent.ID)
	}
}

func TestAgentSubjectResourceResolver_ErrorPropagates(t *testing.T) {
	t.Parallel()
	closedDB := testOpenDB(t)
	if err := closedDB.Close(); err != nil {
		t.Fatalf("close agent db: %v", err)
	}
	agentSvc := NewAgentService(store.NewAgentRepo(closedDB), store.NewAgentDeploymentRepo(closedDB), store.NewConnectorRepo(closedDB), nil, platpkg.NewAgentRegistry(), nil, slog.Default())

	if _, err := agentSubjectResourceResolver(agentSvc)(t.Context()); err == nil {
		t.Error("expected agent-store failure to propagate, got nil")
	}
}

func TestWorkflowSubjectResourceResolver_EnumeratesWorkflows(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	workflowRepo := store.NewWorkflowRepo(db)
	workflowSvc := NewWorkflowService(workflowRepo, store.NewWorkflowDeploymentRepo(db), store.NewConnectorRepo(db), nil, nil, "ns", slog.Default())

	wf := &store.Workflow{Name: "Payroll", AppConfig: `{"workflow":{"nodes":[]}}`, CreatedBy: "u", UpdatedBy: "u"}
	if err := workflowRepo.Create(t.Context(), wf, store.WorkflowBindings{}); err != nil {
		t.Fatalf("create workflow: %v", err)
	}

	got, err := workflowSubjectResourceResolver(workflowSvc)(t.Context())
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("resolved %d instances, want the seeded workflow: %+v", len(got), got)
	}
	if got[0].CanonicalName != wf.ID || got[0].DisplayName != "Payroll" {
		t.Errorf("workflow instance = %+v, want CanonicalName=%q DisplayName=Payroll", got[0], wf.ID)
	}
}

func TestWorkflowSubjectResourceResolver_ErrorPropagates(t *testing.T) {
	t.Parallel()
	closedDB := testOpenDB(t)
	if err := closedDB.Close(); err != nil {
		t.Fatalf("close workflow db: %v", err)
	}
	workflowSvc := NewWorkflowService(store.NewWorkflowRepo(closedDB), store.NewWorkflowDeploymentRepo(closedDB), store.NewConnectorRepo(closedDB), nil, nil, "ns", slog.Default())

	if _, err := workflowSubjectResourceResolver(workflowSvc)(t.Context()); err == nil {
		t.Error("expected workflow-store failure to propagate, got nil")
	}
}

func TestRBACResourceResolvers_KeysAreCatalogTemplates(t *testing.T) {
	catalog := map[string]bool{}
	for _, s := range rbac.Catalog() {
		catalog[s.Scope] = true
	}
	for scope := range rbacResourceResolvers(nil, nil) {
		if !catalog[scope] {
			t.Errorf("resolver key %q is not in rbac.Catalog()", scope)
		}
		if parts := strings.Split(scope, ":"); len(parts) != 3 || parts[1] != "*" {
			t.Errorf("resolver key %q is not a `*` per-instance template", scope)
		}
	}
}

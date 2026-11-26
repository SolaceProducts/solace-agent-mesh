// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"bytes"
	"context"
	"log/slog"
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func TestResolveModelProvider_Default(t *testing.T) {
	t.Parallel()
	agent := &store.Agent{ID: "a1", Name: "Test", Type: "STANDARD"}
	if got := resolveModelProvider(agent); got != "general" {
		t.Errorf("resolveModelProvider(nil provider) = %q, want %q", got, "general")
	}
}

func TestResolveModelProvider_Explicit(t *testing.T) {
	t.Parallel()
	mp := "planning"
	agent := &store.Agent{ID: "a1", Name: "Test", Type: "STANDARD", ModelProvider: &mp}
	if got := resolveModelProvider(agent); got != "planning" {
		t.Errorf("resolveModelProvider(planning) = %q, want %q", got, "planning")
	}
}

func TestResolveModelProvider_Empty(t *testing.T) {
	t.Parallel()
	mp := ""
	agent := &store.Agent{ID: "a1", Name: "Test", Type: "STANDARD", ModelProvider: &mp}
	if got := resolveModelProvider(agent); got != "general" {
		t.Errorf("resolveModelProvider(empty) = %q, want %q", got, "general")
	}
}

func TestNoOp_GetDependents(t *testing.T) {
	t.Parallel()
	h := &NoOpModelDependentsHandler{}
	deps, err := h.GetDependents(context.Background(), "general")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(deps) != 0 {
		t.Errorf("expected empty list, got %d", len(deps))
	}
}

func TestNoOp_UndeployDependents(t *testing.T) {
	t.Parallel()
	h := &NoOpModelDependentsHandler{}
	failed, err := h.UndeployDependents(t.Context(), "general")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(failed) > 0 {
		t.Errorf("failed dependents = %v, want none from the no-op handler", failed)
	}
}

func setupDependentsDB(t *testing.T) (*store.DB, *store.AgentRepo) {
	t.Helper()
	db := testOpenDB(t)
	return db, store.NewAgentRepo(db)
}

func createTestAgent(t *testing.T, repo *store.AgentRepo, name, agentType string, modelProvider *string) string {
	t.Helper()
	desc := "test agent"
	prompt := "test prompt"
	agent := &store.Agent{
		Name:          name,
		Type:          agentType,
		Description:   &desc,
		SystemPrompt:  &prompt,
		ModelProvider: modelProvider,
		CreatedBy:     "test",
		UpdatedBy:     "test",
	}
	if err := repo.Create(context.Background(), agent, nil, nil, nil, nil, nil); err != nil {
		t.Fatalf("create agent %s: %v", name, err)
	}
	return agent.ID
}

func TestEnterpriseDependents_GetDependents_DefaultGeneral(t *testing.T) {
	t.Parallel()
	_, repo := setupDependentsDB(t)

	createTestAgent(t, repo, "Agent1", "standard", nil)
	createTestAgent(t, repo, "Agent2", "standard", nil)

	h := NewModelDependentsHandler(repo, nil, nil, nil, nil, slog.Default())
	deps, err := h.GetDependents(context.Background(), "general")
	if err != nil {
		t.Fatalf("GetDependents: %v", err)
	}
	if len(deps) != 2 {
		t.Fatalf("expected 2 dependents, got %d", len(deps))
	}
	for _, d := range deps {
		if d.DeploymentStatus != "not_deployed" {
			t.Errorf("expected not_deployed, got %q", d.DeploymentStatus)
		}
	}
}

func TestEnterpriseDependents_GetDependents_ExplicitProvider(t *testing.T) {
	t.Parallel()
	_, repo := setupDependentsDB(t)

	planning := "planning"
	createTestAgent(t, repo, "PlanAgent", "standard", &planning)
	createTestAgent(t, repo, "DefaultAgent", "standard", nil)

	h := NewModelDependentsHandler(repo, nil, nil, nil, nil, slog.Default())

	deps, err := h.GetDependents(context.Background(), "planning")
	if err != nil {
		t.Fatalf("GetDependents(planning): %v", err)
	}
	if len(deps) != 1 {
		t.Fatalf("expected 1 dependent for planning, got %d", len(deps))
	}
	if deps[0].Name != "PlanAgent" {
		t.Errorf("expected PlanAgent, got %q", deps[0].Name)
	}

	deps2, err := h.GetDependents(context.Background(), "general")
	if err != nil {
		t.Fatalf("GetDependents(general): %v", err)
	}
	if len(deps2) != 1 {
		t.Fatalf("expected 1 dependent for general, got %d", len(deps2))
	}
	if deps2[0].Name != "DefaultAgent" {
		t.Errorf("expected DefaultAgent, got %q", deps2[0].Name)
	}
}

func TestEnterpriseDependents_ExcludesProxyAgents(t *testing.T) {
	t.Parallel()
	_, repo := setupDependentsDB(t)

	createTestAgent(t, repo, "StandardAgent", "standard", nil)
	createTestAgent(t, repo, "ProxyAgent", "a2a_proxy", nil)

	h := NewModelDependentsHandler(repo, nil, nil, nil, nil, slog.Default())
	deps, err := h.GetDependents(context.Background(), "general")
	if err != nil {
		t.Fatalf("GetDependents: %v", err)
	}
	if len(deps) != 1 {
		t.Fatalf("expected 1 dependent (standard only), got %d", len(deps))
	}
	if deps[0].Name != "StandardAgent" {
		t.Errorf("expected StandardAgent, got %q", deps[0].Name)
	}
}

func TestEnterpriseDependents_NoMatch(t *testing.T) {
	t.Parallel()
	_, repo := setupDependentsDB(t)

	createTestAgent(t, repo, "Agent1", "standard", nil)

	h := NewModelDependentsHandler(repo, nil, nil, nil, nil, slog.Default())
	deps, err := h.GetDependents(context.Background(), "custom-model")
	if err != nil {
		t.Fatalf("GetDependents: %v", err)
	}
	if len(deps) != 0 {
		t.Errorf("expected 0 dependents for custom-model, got %d", len(deps))
	}
}

func TestEnterpriseDependents_GetDependents_UUIDProvider(t *testing.T) {
	t.Parallel()
	db, repo := setupDependentsDB(t)
	modelRepo := store.NewModelConfigRepo(db)

	mc := &store.ModelConfiguration{
		Alias:         "general",
		Provider:      "openai",
		ModelName:     "gpt-4",
		ModelAuthType: "apikey",
		CreatedBy:     "test",
		UpdatedBy:     "test",
	}
	if err := modelRepo.Create(context.Background(), mc); err != nil {
		t.Fatalf("create model config: %v", err)
	}

	uuidProvider := mc.ID
	aliasProvider := "general"
	createTestAgent(t, repo, "UUIDAgent", "standard", &uuidProvider)
	createTestAgent(t, repo, "AliasAgent", "standard", &aliasProvider)

	h := NewModelDependentsHandler(repo, nil, nil, modelRepo, nil, slog.Default())
	deps, err := h.GetDependents(context.Background(), "general")
	if err != nil {
		t.Fatalf("GetDependents: %v", err)
	}
	if len(deps) != 2 {
		t.Fatalf("expected 2 dependents (UUID-saved + alias-saved), got %d", len(deps))
	}
	seen := make(map[string]bool)
	for _, d := range deps {
		seen[d.Name] = true
	}
	if !seen["UUIDAgent"] {
		t.Errorf("expected UUIDAgent in dependents; got %v", seen)
	}
	if !seen["AliasAgent"] {
		t.Errorf("expected AliasAgent in dependents; got %v", seen)
	}
}

func TestEnterpriseDependents_NilRepoFallback(t *testing.T) {
	t.Parallel()
	_, repo := setupDependentsDB(t)

	createTestAgent(t, repo, "AliasAgent", "standard", nil)

	h := NewModelDependentsHandler(repo, nil, nil, nil, nil, slog.Default())
	deps, err := h.GetDependents(context.Background(), "general")
	if err != nil {
		t.Fatalf("GetDependents: %v", err)
	}
	if len(deps) != 1 {
		t.Fatalf("expected 1 dependent, got %d", len(deps))
	}
	if deps[0].Name != "AliasAgent" {
		t.Errorf("expected AliasAgent, got %q", deps[0].Name)
	}
}

func TestEnterpriseDependents_StaleUUID(t *testing.T) {
	t.Parallel()
	db, repo := setupDependentsDB(t)
	modelRepo := store.NewModelConfigRepo(db)

	staleUUID := "11111111-1111-1111-1111-111111111111"
	createTestAgent(t, repo, "OrphanAgent", "standard", &staleUUID)

	h := NewModelDependentsHandler(repo, nil, nil, modelRepo, nil, slog.Default())
	deps, err := h.GetDependents(context.Background(), "general")
	if err != nil {
		t.Fatalf("GetDependents: %v", err)
	}
	if len(deps) != 0 {
		t.Fatalf("expected 0 dependents for stale UUID; got %d (%+v)", len(deps), deps)
	}
}

func TestEnterpriseDependents_GetDependents_RepoErrorFallsBack(t *testing.T) {
	t.Parallel()
	db, repo := setupDependentsDB(t)
	modelRepo := store.NewModelConfigRepo(db)

	aliasProvider := "general"
	uuidProvider := "22222222-2222-2222-2222-222222222222"
	createTestAgent(t, repo, "AliasAgent", "standard", &aliasProvider)
	createTestAgent(t, repo, "UUIDAgent", "standard", &uuidProvider)

	if _, err := db.ExecContext(context.Background(), `DROP TABLE model_configurations`); err != nil {
		t.Fatalf("drop model_configurations: %v", err)
	}

	h := NewModelDependentsHandler(repo, nil, nil, modelRepo, nil, slog.Default())
	deps, err := h.GetDependents(context.Background(), "general")
	if err != nil {
		t.Fatalf("GetDependents (lenient path): %v", err)
	}
	if len(deps) != 1 {
		t.Fatalf("expected 1 dependent (alias only; UUID untranslatable), got %d (%+v)", len(deps), deps)
	}
	if deps[0].Name != "AliasAgent" {
		t.Errorf("expected AliasAgent, got %q", deps[0].Name)
	}
}

func TestEnterpriseDependents_UndeployDependents_FailsClosedOnRepoError(t *testing.T) {
	t.Parallel()
	db, repo := setupDependentsDB(t)
	modelRepo := store.NewModelConfigRepo(db)

	aliasProvider := "general"
	createTestAgent(t, repo, "AliasAgent", "standard", &aliasProvider)

	if _, err := db.ExecContext(context.Background(), `DROP TABLE model_configurations`); err != nil {
		t.Fatalf("drop model_configurations: %v", err)
	}

	h := NewModelDependentsHandler(repo, nil, nil, modelRepo, nil, slog.Default())
	failed, err := h.UndeployDependents(t.Context(), "general")
	if err == nil {
		t.Fatal("UndeployDependents must return an error when the translation map can't be loaded; got nil")
	}
	if failed != nil {
		t.Errorf("failed dependents = %v, want none: a lookup failure undeploys nothing at all", failed)
	}
}

func TestModelDependentsServiceWiresTheWorkflowUndeployer(t *testing.T) {
	t.Parallel()
	workflowDeploySvc := &WorkflowDeploymentService{}
	got := newModelDependentsService(nil, nil, nil, nil, &DeploymentService{}, workflowDeploySvc, slog.Default())
	h, ok := got.(*ModelDependentsHandler)
	if !ok {
		t.Fatalf("service = %T, want *ModelDependentsHandler when the deploy service is available", got)
	}
	if h.workflowUndeploys != workflowDeploySvc {
		t.Errorf("workflow undeployer = %v, want the workflow deployment service", h.workflowUndeploys)
	}
	if _, ok := newModelDependentsService(nil, nil, nil, nil, nil, nil, slog.Default()).(*NoOpModelDependentsHandler); !ok {
		t.Error("service without a deploy service should be the no-op")
	}
}

func TestListDependentsPage_PagesAgentsBeforeWorkflowsByName(t *testing.T) {
	t.Parallel()
	db, repo := setupDependentsDB(t)
	for _, name := range []string{"Charlie", "Alpha", "Bravo"} {
		createTestAgent(t, repo, name, "standard", nil)
	}
	wf := &store.Workflow{Name: "Aardvark", AppConfig: `{"decision":{"model_alias":"general"}}`, CreatedBy: "tester", UpdatedBy: "tester"}
	if err := store.NewWorkflowRepo(db).Create(t.Context(), wf, store.WorkflowBindings{}); err != nil {
		t.Fatalf("create workflow: %v", err)
	}
	h := NewModelDependentsHandler(repo, store.NewWorkflowRepo(db), store.NewWorkflowDeploymentRepo(db), nil, nil, slog.Default())

	names := func(items []ModelDependentResponse) []string {
		out := make([]string, len(items))
		for i, d := range items {
			out[i] = d.Name
		}
		return out
	}

	byKind, total, err := h.ListDependentsPage(t.Context(), "general", 0, 10, "", false)
	if err != nil {
		t.Fatalf("ListDependentsPage: %v", err)
	}
	if total != 4 || !slices.Equal(names(byKind), []string{"Alpha", "Bravo", "Charlie", "Aardvark"}) {
		t.Fatalf("default order = %v (total %d), want the agents by name, then the workflow", names(byKind), total)
	}
	if byKind[3].Type != ComponentKindWorkflow || byKind[0].Type == ComponentKindWorkflow {
		t.Errorf("types = %q first, %q last; want the workflow last", byKind[0].Type, byKind[3].Type)
	}

	byName, _, err := h.ListDependentsPage(t.Context(), "general", 0, 10, "name", false)
	if err != nil {
		t.Fatalf("ListDependentsPage by name: %v", err)
	}
	if !slices.Equal(names(byName), []string{"Aardvark", "Alpha", "Bravo", "Charlie"}) {
		t.Errorf("by name = %v, want the kinds interleaved", names(byName))
	}

	items, _, err := h.ListDependentsPage(t.Context(), "general", 1, 1, "name", false)
	if err != nil {
		t.Fatalf("ListDependentsPage page: %v", err)
	}
	if len(items) != 1 || items[0].Name != "Alpha" {
		t.Fatalf("items = %+v, want the single dependent Alpha", items)
	}

	desc, _, err := h.ListDependentsPage(t.Context(), "general", 0, 10, "name", true)
	if err != nil {
		t.Fatalf("ListDependentsPage desc: %v", err)
	}
	if !slices.Equal(names(desc), []string{"Charlie", "Bravo", "Alpha", "Aardvark"}) {
		t.Errorf("desc = %v, want Charlie..Aardvark", names(desc))
	}
}

func TestListDependentsPage_OffsetPastTheEndIsEmptyNotNull(t *testing.T) {
	t.Parallel()
	_, repo := setupDependentsDB(t)
	createTestAgent(t, repo, "Only", "standard", nil)
	h := NewModelDependentsHandler(repo, nil, nil, nil, nil, slog.Default())

	items, total, err := h.ListDependentsPage(t.Context(), "general", 50, 10, "", false)
	if err != nil {
		t.Fatalf("ListDependentsPage: %v", err)
	}
	if total != 1 {
		t.Errorf("total = %d, want 1", total)
	}
	if items == nil || len(items) != 0 {
		t.Errorf("items = %#v, want a non-nil empty slice", items)
	}
}

func TestListDependentsPage_FindsTheDependentsOfAnAlias(t *testing.T) {
	t.Parallel()
	_, repo := setupDependentsDB(t)
	planning := "planning"
	createTestAgent(t, repo, "UsesPlanning", "standard", &planning)
	createTestAgent(t, repo, "UsesGeneral", "standard", nil)
	h := NewModelDependentsHandler(repo, nil, nil, nil, nil, slog.Default())

	items, total, err := h.ListDependentsPage(t.Context(), "planning", 0, 10, "", false)
	if err != nil {
		t.Fatalf("ListDependentsPage: %v", err)
	}
	if total != 1 || len(items) != 1 || items[0].Name != "UsesPlanning" {
		t.Fatalf("items = %+v total = %d, want only UsesPlanning", items, total)
	}
}

func TestUndeployWorkflows_WithoutAnUndeployerFailsClosed(t *testing.T) {
	t.Parallel()
	h := &ModelDependentsHandler{logger: slog.Default()}

	running := []ModelDependentResponse{{ID: "wf-1", Name: "one"}, {ID: "wf-2", Name: "two"}}
	if failed := h.undeployWorkflows(t.Context(), "general", running); len(failed) != len(running) {
		t.Errorf("failed = %v, want both running workflows reported", failed)
	}
	if failed := h.undeployWorkflows(t.Context(), "general", nil); len(failed) != 0 {
		t.Errorf("failed = %v, want none when no workflow is running", failed)
	}
}

type stubWorkflowUndeployer struct {
	resp *WorkflowDeploymentResponse
	err  error
}

func (s stubWorkflowUndeployer) Create(context.Context, CreateWorkflowDeploymentRequest, string) (*WorkflowDeploymentResponse, error) {
	return s.resp, s.err
}

func TestUndeployWorkflows_RefusedUndeployLogsTheDeploymentsOwnReason(t *testing.T) {
	t.Parallel()
	var logs bytes.Buffer
	h := &ModelDependentsHandler{
		logger: slog.New(slog.NewTextHandler(&logs, nil)),
		workflowUndeploys: stubWorkflowUndeployer{resp: &WorkflowDeploymentResponse{
			Status: DeployStatusFailed, ErrorID: "err-deploy-1", FailureReason: "control_plane_unreachable",
		}},
	}

	failed := h.undeployWorkflows(t.Context(), "general", []ModelDependentResponse{{ID: "wf-1", Name: "one"}})

	if len(failed) != 1 {
		t.Fatalf("failed = %v, want the refused workflow", failed)
	}
	out := logs.String()
	for _, want := range []string{"deploymentErrorID=err-deploy-1", "reason=control_plane_unreachable", "deployStatus=failed"} {
		if !strings.Contains(out, want) {
			t.Errorf("log lacks %q: %s", want, out)
		}
	}
	if strings.Contains(out, "error=<nil>") {
		t.Errorf("log carries an empty error field: %s", out)
	}
}

func TestUndeployWorkflows_NilResponseWithoutErrorIsReportedFailed(t *testing.T) {
	t.Parallel()
	h := &ModelDependentsHandler{logger: slog.New(slog.DiscardHandler), workflowUndeploys: stubWorkflowUndeployer{}}

	if failed := h.undeployWorkflows(t.Context(), "general", []ModelDependentResponse{{ID: "wf-1"}}); len(failed) != 1 {
		t.Errorf("failed = %v, want the workflow reported", failed)
	}
}

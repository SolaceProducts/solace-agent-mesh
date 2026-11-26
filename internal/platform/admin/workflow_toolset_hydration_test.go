// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"log/slog"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func resolveAndHydrateToolsets(t *testing.T, appConfig map[string]any, repo *store.ToolPackageRepo) ([]string, error) {
	t.Helper()
	names, err := resolveWorkflowToolsets(t.Context(), appConfig, repo)
	if err != nil {
		return nil, err
	}
	return names, hydrateWorkflowToolsets(t.Context(), appConfig, repo, slog.Default())
}

func emittedToolNames(t *testing.T, appConfig map[string]any) []string {
	t.Helper()
	rawTools, _ := appConfig["tools"].([]any)
	names := make([]string, 0, len(rawTools))
	for _, raw := range rawTools {
		entry, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if name, _ := entry["tool_name"].(string); name != "" {
			names = append(names, name)
		} else if group, _ := entry["group_name"].(string); group != "" {
			names = append(names, "group:"+group)
		}
	}
	return names
}

func TestWorkflowToolsets_HydratePackageTools(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	repo := store.NewToolPackageRepo(db)
	seedToolPackage(t, db, "echo-tools", "ready", "{}", "greet", "shout")

	appConfig := map[string]any{
		"toolsets": []any{"echo-tools"},
		"workflow": map[string]any{
			"output_mapping": map[string]any{"result": "{{nodes.greet.output}}"},
			"nodes": []any{
				map[string]any{"id": "greet", "type": "tool", "tool_name": "echo-tools__greet"},
			},
		},
	}

	names, err := resolveAndHydrateToolsets(t, appConfig, repo)
	if err != nil {
		t.Fatalf("resolve+hydrate: %v", err)
	}
	if len(names) != 1 || names[0] != "echo-tools" {
		t.Errorf("resolved names = %v, want [echo-tools]", names)
	}

	emitted := emittedToolNames(t, appConfig)
	if len(emitted) != 2 || emitted[0] != "echo-tools__greet" || emitted[1] != "echo-tools__shout" {
		t.Errorf("emitted tools = %v, want [echo-tools__greet echo-tools__shout]", emitted)
	}
	if _, present := appConfig["toolsets"]; present {
		t.Error("appConfig still carries symbolic 'toolsets' after hydration")
	}
	entry := appConfig["tools"].([]any)[0].(map[string]any)
	if entry["tool_type"] != "builtin" {
		t.Errorf("emitted tool_type = %v, want builtin", entry["tool_type"])
	}
}

func TestWorkflowToolsets_NestedInWorkflowBody(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	repo := store.NewToolPackageRepo(db)
	seedToolPackage(t, db, "nested-tools", "ready", "{}", "run")

	appConfig := map[string]any{
		"workflow": map[string]any{
			"toolsets":       []any{"nested-tools"},
			"output_mapping": map[string]any{"result": "x"},
			"nodes": []any{
				map[string]any{"id": "run", "type": "tool", "tool_name": "nested-tools__run"},
			},
		},
	}

	if _, err := resolveAndHydrateToolsets(t, appConfig, repo); err != nil {
		t.Fatalf("resolve+hydrate: %v", err)
	}
	emitted := emittedToolNames(t, appConfig)
	if len(emitted) != 1 || emitted[0] != "nested-tools__run" {
		t.Errorf("emitted tools = %v, want [nested-tools__run]", emitted)
	}
	if _, present := appConfig["workflow"].(map[string]any)["toolsets"]; present {
		t.Error("workflow body still carries symbolic 'toolsets' after hydration")
	}
}

func TestWorkflowToolsets_BuiltinGroup(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	repo := store.NewToolPackageRepo(db)

	appConfig := map[string]any{
		"toolsets": []any{"builtin_artifact_tools"},
		"workflow": map[string]any{"output_mapping": map[string]any{"r": "x"}, "nodes": []any{}},
	}

	if _, err := resolveAndHydrateToolsets(t, appConfig, repo); err != nil {
		t.Fatalf("resolve+hydrate: %v", err)
	}
	emitted := emittedToolNames(t, appConfig)
	if len(emitted) != 1 || emitted[0] != "group:builtin_artifact_tools" {
		t.Errorf("emitted tools = %v, want the builtin-group entry", emitted)
	}
}

func TestWorkflowToolsets_UnknownNameFailsSave(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	repo := store.NewToolPackageRepo(db)

	appConfig := map[string]any{"toolsets": []any{"no-such-toolset"}}
	_, err := resolveWorkflowToolsets(t.Context(), appConfig, repo)
	if err == nil || !strings.Contains(err.Error(), `unknown toolset "no-such-toolset"`) {
		t.Fatalf("err = %v, want unknown-toolset validation error", err)
	}
}

func TestWorkflowToolsets_NotReadyFailsDeploy(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	repo := store.NewToolPackageRepo(db)
	key := "res-key"
	pkg := &store.ToolPackage{Name: "pending-tools", DiscoveryStatus: "pending", ZipResourceKey: &key, CreatedBy: "u", UpdatedBy: "u"}
	if err := repo.Create(t.Context(), pkg); err != nil {
		t.Fatalf("seed package: %v", err)
	}

	appConfig := map[string]any{"toolsets": []any{"pending-tools"}}
	if _, err := resolveWorkflowToolsets(t.Context(), appConfig, repo); err != nil {
		t.Fatalf("save-time resolve should accept an uploaded-but-pending package, got %v", err)
	}
	err := hydrateWorkflowToolsets(t.Context(), appConfig, repo, slog.Default())
	if err == nil || !strings.Contains(err.Error(), "not ready") {
		t.Fatalf("err = %v, want not-ready deploy error", err)
	}
}

func TestWorkflowToolsets_ShapeAndDupErrors(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	repo := store.NewToolPackageRepo(db)
	seedToolPackage(t, db, "dup-tools", "ready", "{}", "run")

	cases := []struct {
		name      string
		appConfig map[string]any
		wantErr   string
	}{
		{"not a list", map[string]any{"toolsets": "dup-tools"}, "must be a list"},
		{"empty name", map[string]any{"toolsets": []any{""}}, "non-empty toolset name"},
		{"non-string", map[string]any{"toolsets": []any{42}}, "non-empty toolset name"},
		{"duplicate", map[string]any{"toolsets": []any{"dup-tools", "dup-tools"}}, "more than once"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := resolveWorkflowToolsets(t.Context(), tc.appConfig, repo)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want %q", err, tc.wantErr)
			}
		})
	}
}

func TestWorkflowToolsets_CollisionWithDeclaredTool(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	repo := store.NewToolPackageRepo(db)
	seedToolPackage(t, db, "clash-tools", "ready", "{}", "run")

	appConfig := map[string]any{
		"toolsets": []any{"clash-tools"},
		"tools": []any{
			map[string]any{"tool_type": "builtin", "tool_name": "clash-tools__run"},
		},
		"workflow": map[string]any{"output_mapping": map[string]any{"r": "x"}, "nodes": []any{}},
	}

	err := hydrateWorkflowToolsets(t.Context(), appConfig, repo, slog.Default())
	if err == nil || !strings.Contains(err.Error(), "collides") {
		t.Fatalf("err = %v, want collision error", err)
	}
}

func TestWorkflowToolsets_NoToolsetsIsNoop(t *testing.T) {
	t.Parallel()
	appConfig := map[string]any{
		"workflow": map[string]any{"output_mapping": map[string]any{"r": "x"}, "nodes": []any{}},
	}
	if err := hydrateWorkflowToolsets(t.Context(), appConfig, nil, slog.Default()); err != nil {
		t.Fatalf("hydrate with no toolsets should be a no-op even without a repo: %v", err)
	}
	if _, present := appConfig["tools"]; present {
		t.Error("no-op hydration should not create a tools list")
	}
}

func TestWorkflowToolsets_HydrateErrorBranches(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	repo := store.NewToolPackageRepo(db)
	seedToolPackage(t, db, "branch-tools", "ready", "{}", "run")

	t.Run("nil repo with toolsets present", func(t *testing.T) {
		appConfig := map[string]any{"toolsets": []any{"branch-tools"}}
		err := hydrateWorkflowToolsets(t.Context(), appConfig, nil, slog.Default())
		if err == nil || !strings.Contains(err.Error(), "not configured") {
			t.Fatalf("err = %v, want store-not-configured error", err)
		}
	})

	t.Run("unknown name at deploy time", func(t *testing.T) {
		appConfig := map[string]any{"toolsets": []any{"ghost-tools"}}
		err := hydrateWorkflowToolsets(t.Context(), appConfig, repo, slog.Default())
		if err == nil || !strings.Contains(err.Error(), `unknown toolset "ghost-tools"`) {
			t.Fatalf("err = %v, want unknown-toolset error", err)
		}
	})

	t.Run("malformed tools list", func(t *testing.T) {
		appConfig := map[string]any{
			"toolsets": []any{"branch-tools"},
			"tools":    "not-a-list",
		}
		err := hydrateWorkflowToolsets(t.Context(), appConfig, repo, slog.Default())
		if err == nil || !strings.Contains(err.Error(), "tools must be a list") {
			t.Fatalf("err = %v, want malformed-tools error", err)
		}
	})
}

func TestReconciler_RegenerateWorkflowYAML_ToolsetError(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	workflowRepo := store.NewWorkflowRepo(db)

	wf := &store.Workflow{Name: "wf-toolset-recon", AppConfig: `{"toolsets":["echo-tools"],"workflow":{"output_mapping":{"r":"x"},"nodes":[]}}`, CreatedBy: "u", UpdatedBy: "u"}
	if err := workflowRepo.Create(t.Context(), wf, store.WorkflowBindings{Toolsets: []string{"echo-tools"}}); err != nil {
		t.Fatalf("create workflow: %v", err)
	}

	r := &Reconciler{workflowRepo: workflowRepo, namespace: "ns", logger: slog.Default()}
	_, err := r.generateDeployableWorkflowYAML(t.Context(), wf)
	if err == nil || !strings.Contains(err.Error(), "resolve toolset refs") {
		t.Fatalf("err = %v, want toolset re-hydration error", err)
	}
}

func TestWorkflowToolsets_SecretConfigInlinedAtDeployOnly(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	repo := store.NewToolPackageRepo(db)

	pkgID := seedToolPackage(t, db, "secret-tools", "ready", "{}")
	schema := `[{"key":"api_key","type":"string","secret":true}]`
	desc := "run desc"
	if err := repo.UpsertToolFile(t.Context(), &store.ToolPackageFile{
		PackageID: pkgID, ToolName: "run", ToolDescription: &desc,
		TimeoutSeconds: 30, SandboxProfile: "default", ConfigSchema: &schema,
	}); err != nil {
		t.Fatalf("seed tool file: %v", err)
	}
	vals := `{"run":{"api_key":"sekret-value"}}`
	if err := repo.SetToolsetConfig(t.Context(), pkgID, &vals, "u"); err != nil {
		t.Fatalf("set toolset config: %v", err)
	}

	storedAppConfig := map[string]any{
		"toolsets":       []any{"secret-tools"},
		"output_mapping": map[string]any{"r": "x"},
		"nodes": []any{
			map[string]any{"id": "run", "type": "tool", "tool_name": "secret-tools__run"},
		},
	}

	svc := NewWorkflowService(store.NewWorkflowRepo(db), store.NewWorkflowDeploymentRepo(db), store.NewConnectorRepo(db), repo, nil, "ns", nil)
	created, err := svc.Create(t.Context(), CreateWorkflowRequest{
		Name: "secret-workflow", Description: "workflow with secret toolset config", AppConfig: storedAppConfig,
	}, "u")
	if err != nil {
		t.Fatalf("create workflow: %v", err)
	}

	cfg, err := svc.GetConfiguration(t.Context(), created.ID)
	if err != nil {
		t.Fatalf("get configuration: %v", err)
	}
	if strings.Contains(cfg.ConfigurationFile, "sekret-value") || strings.Contains(cfg.ConfigurationFile, "api_key") {
		t.Fatalf("FE-facing configuration leaked toolset config:\n%s", cfg.ConfigurationFile)
	}

	deployCopy := map[string]any{
		"toolsets":       []any{"secret-tools"},
		"output_mapping": map[string]any{"r": "x"},
	}
	if err := hydrateWorkflowToolsets(t.Context(), deployCopy, repo, slog.Default()); err != nil {
		t.Fatalf("hydrate: %v", err)
	}
	entry := deployCopy["tools"].([]any)[0].(map[string]any)
	toolConfig, _ := entry["tool_config"].(map[string]any)
	if toolConfig["api_key"] != "sekret-value" {
		t.Fatalf("deploy-time tool_config = %v, want the toolset-level api_key inlined", entry)
	}
}

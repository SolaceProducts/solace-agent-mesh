// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package pull

import (
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/manifest"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/reconcile"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/target"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/wire"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform"
)

const roundTripWorkflowName = "claim-routing"

func roundTripDefinition(extra map[string]any) map[string]any {
	definition := map[string]any{
		"output_mapping": map[string]any{"answer": "{{ask.output}}"},
		"nodes": []any{map[string]any{
			"id":         "ask",
			"type":       "agent",
			"agent_name": "Orchestrator",
			"input":      map[string]any{"message": "{{workflow.input.message}}"},
		}},
	}
	maps.Copy(definition, extra)
	return definition
}

func workflowPlatform(t *testing.T, definition map[string]any) string {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+platform.APIV2Prefix+"/workflows", func(w http.ResponseWriter, _ *http.Request) {
		writeV2Page(w, []wire.WorkflowResponse{{
			ID:               "0190f5ec-0000-7000-8000-00000000c001",
			Name:             roundTripWorkflowName,
			Description:      "Routes a filed claim to a queue.",
			AppConfig:        definition,
			DeploymentStatus: "deployed",
		}})
	})
	mux.HandleFunc("GET "+platform.APIV2Prefix+"/agents", func(w http.ResponseWriter, _ *http.Request) {
		writeV2Page(w, []wire.AgentResponse{{Name: "Orchestrator", InstanceName: "agent_0190f5ec_0000_7000_8000_00000000b001"}})
	})
	mux.HandleFunc("GET /api/v1/version", http.NotFound)
	return newV2Platform(t, mux).URL
}

func pullWorkflowInto(t *testing.T, url, repo string) map[string]any {
	t.Helper()
	entries, names, err := pullWorkflows(t.Context(), reconcile.NewClient(target.Resolved{URL: url}), Options{}, &Result{})
	if err != nil {
		t.Fatalf("pull workflows: %v", err)
	}
	if len(entries) != 1 || !slices.Equal(names, []string{roundTripWorkflowName}) {
		t.Fatalf("pulled %d files named %v, want the one workflow", len(entries), names)
	}
	path := filepath.Join(repo, entries[0].relPath)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, entries[0].content, 0o600); err != nil {
		t.Fatalf("write pulled file: %v", err)
	}
	var doc struct {
		Spec map[string]any `yaml:"spec"`
	}
	if err := yaml.Unmarshal(entries[0].content, &doc); err != nil {
		t.Fatalf("parse pulled file: %v", err)
	}
	return doc.Spec
}

func TestPulledWorkflow_PlansAsUnchanged(t *testing.T) {
	for name, extra := range map[string]map[string]any{
		"a definition with its own description": {"description": "A definition-level description."},
		"a definition with a name key":          {"name": "inner-name"},
		"a definition with an appConfig key":    {"appConfig": map[string]any{"nested": "value"}},
		"a definition with none of those":       nil,
	} {
		t.Run(name, func(t *testing.T) {
			url := workflowPlatform(t, roundTripDefinition(extra))
			repo := t.TempDir()
			pullWorkflowInto(t, url, repo)
			m := &manifest.Manifest{Resources: map[string][]manifest.ResourceEntry{
				"workflows": {{Name: roundTripWorkflowName, From: roundTripWorkflowName}},
			}}

			plan, err := reconcile.Plan(t.Context(), target.Target{URL: url}, repo, m)

			if err != nil {
				t.Fatalf("plan: %v", err)
			}
			if len(plan.Workflows.Unchanged) != 1 || len(plan.Workflows.Updates)+len(plan.Workflows.Creates) != 0 {
				t.Errorf("unchanged=%d updates=%d creates=%d, want the pulled workflow unchanged",
					len(plan.Workflows.Unchanged), len(plan.Workflows.Updates), len(plan.Workflows.Creates))
			}
			if len(plan.Workflows.Warnings) != 0 {
				t.Errorf("warnings = %v, want none for a freshly pulled file", plan.Workflows.Warnings)
			}
		})
	}
}

func TestPulledWorkflow_StaysFlatUnlessTheDefinitionNeedsTheWrapper(t *testing.T) {
	flat := pullWorkflowInto(t, workflowPlatform(t, roundTripDefinition(nil)), t.TempDir())
	if got := slices.Sorted(maps.Keys(flat)); !slices.Equal(got, []string{"nodes", "output_mapping"}) {
		t.Errorf("spec keys = %v, want the definition's keys directly under spec", got)
	}

	wrapped := pullWorkflowInto(t, workflowPlatform(t, roundTripDefinition(map[string]any{"description": "A definition-level description."})), t.TempDir())
	if got := slices.Sorted(maps.Keys(wrapped)); !slices.Equal(got, []string{"appConfig"}) {
		t.Fatalf("spec keys = %v, want the definition under spec.appConfig", got)
	}
	definition, _ := wrapped["appConfig"].(map[string]any)
	if definition["description"] != "A definition-level description." {
		t.Errorf("appConfig.description = %v, want the definition's own description", definition["description"])
	}
}

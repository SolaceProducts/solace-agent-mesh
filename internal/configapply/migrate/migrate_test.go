// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package migrate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func writeFile(t *testing.T, root, rel, contents string) {
	t.Helper()
	full := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(full, []byte(contents), 0o644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}

func TestRun_WalksDirectoryAndDispatches(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()

	writeFile(t, src, "agents/employee.yaml", legacyAgentYAML)
	writeFile(t, src, "gateways/slack.yaml", legacyGatewaySlackYAML)
	writeFile(t, src, "workflows/wf.yaml", legacyWorkflowYAML)
	writeFile(t, src, "shared_config.yaml", `
shared_config:
  - models:
      planning:
        model: claude-opus-4-7
        api_key: ${A}
`)

	report, err := Run(src, dst, Options{Force: true})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if report.HasFailures() {
		t.Fatalf("unexpected errors: %+v", report.Errored)
	}

	for _, want := range [][]string{
		{"agents", "Employee Agent.yaml"},
		{"entrypoints", "SlackBot.yaml"},
		{"workflows", "SFDC Case Advisor.yaml"},
		{"models", "planning.yaml"},
	} {
		full := filepath.Join(append([]string{dst}, want...)...)
		if _, statErr := os.Stat(full); statErr != nil {
			t.Errorf("expected %s to be written: %v", filepath.Join(want...), statErr)
		}
	}

	body, err := os.ReadFile(filepath.Join(dst, "agents", "Employee Agent.yaml"))
	if err != nil {
		t.Fatalf("read employee: %v", err)
	}
	var top map[string]any
	if err := yaml.Unmarshal(body, &top); err != nil {
		t.Fatalf("parse employee: %v", err)
	}
	if top["kind"] != "agent" || top["name"] != "Employee Agent" {
		t.Errorf("employee top-level = %v", top)
	}
}

func TestRun_DryRunDoesNotWrite(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	writeFile(t, src, "agents/employee.yaml", legacyAgentYAML)

	report, err := Run(src, dst, Options{DryRun: true})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(report.Migrated)+len(report.Warnings) == 0 {
		t.Fatalf("expected at least one migrated/warning entry; report=%+v", report)
	}
	entries, _ := os.ReadDir(dst)
	if len(entries) != 0 {
		t.Errorf("dry-run wrote files to %s: %v", dst, entries)
	}
}

func TestRun_RefusesNonEmptyOutputWithoutForce(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	writeFile(t, src, "agents/employee.yaml", legacyAgentYAML)
	writeFile(t, dst, "stale.txt", "hi")

	if _, err := Run(src, dst, Options{}); err == nil {
		t.Fatalf("expected error for non-empty output dir")
	}

	if _, err := Run(src, dst, Options{Force: true}); err != nil {
		t.Fatalf("--force still failed: %v", err)
	}
}

func TestRun_OverlaySkipped(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	writeFile(t, src, "overlays/dev.yaml", `apps: []`)

	report, err := Run(src, dst, Options{Force: true})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(report.Skipped) == 0 {
		t.Fatalf("expected overlay to be skipped; report=%+v", report)
	}
	if !strings.Contains(report.Skipped[0].Messages[0], "overlay") {
		t.Errorf("expected overlay reason; got %v", report.Skipped[0].Messages)
	}
}

func TestRun_UnknownFilesSkipped(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	writeFile(t, src, "stuff/unknown.yaml", `foo: bar`)

	report, err := Run(src, dst, Options{Force: true})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(report.Skipped) == 0 {
		t.Fatalf("expected unknown to be skipped; report=%+v", report)
	}
}

func TestRun_PathTraversalNameStrippedToBase(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	writeFile(t, src, "agents/evil.yaml", `
apps:
  - app_module: solace_agent_mesh.agent.sac.app
    app_config:
      agent_name: ../../escape
      instruction: x
      agent_card:
        description: d
`)

	report, err := Run(src, dst, Options{Force: true})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if report.HasFailures() {
		t.Fatalf("unexpected errors: %+v", report.Errored)
	}
	if _, statErr := os.Stat(filepath.Join(dst, "agents", "escape.yaml")); statErr != nil {
		t.Errorf("expected sanitised file at agents/escape.yaml: %v", statErr)
	}
	parentSibling := filepath.Join(filepath.Dir(dst), "escape.yaml")
	if _, statErr := os.Stat(parentSibling); statErr == nil {
		t.Errorf("path traversal succeeded: file written outside dst at %s", parentSibling)
	}
}

func TestRun_MixedSuccessReportsBothKindsOfOutcome(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	writeFile(t, src, "agents/ok.yaml", legacyAgentYAML)
	writeFile(t, src, "agents/bad.yaml", `
apps:
  - app_module: solace_agent_mesh.agent.sac.app
`)
	writeFile(t, src, "overlays/dev.yaml", `apps: []`)

	report, err := Run(src, dst, Options{Force: true})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !report.HasFailures() {
		t.Errorf("expected at least one errored entry; got %+v", report)
	}
	if len(report.Skipped) == 0 {
		t.Errorf("expected at least one skipped entry; got %+v", report.Skipped)
	}
	if len(report.Migrated)+len(report.Warnings) == 0 {
		t.Errorf("expected the good file to still migrate; report=%+v", report)
	}
}

func TestReport_JSONShape(t *testing.T) {
	r := &Report{}
	r.AddMigrated("a", "b", "agent", "x")
	r.AddWarning("c", "d", "gateway", "y", []string{"warn"})
	r.AddSkipped("e", "model", "reason")
	r.AddError("f", os.ErrNotExist)

	body, err := r.ToJSON()
	if err != nil {
		t.Fatalf("ToJSON: %v", err)
	}
	if !strings.Contains(string(body), "migrated") ||
		!strings.Contains(string(body), "warnings") ||
		!strings.Contains(string(body), "skipped") ||
		!strings.Contains(string(body), "errored") {
		t.Errorf("missing expected keys: %s", body)
	}
}

func TestClassifyFile(t *testing.T) {
	cases := []struct {
		name string
		path string
		doc  string
		want string
	}{
		{"agent by module", "/x/agents/a.yaml", "apps:\n  - app_module: solace_agent_mesh.agent.sac.app", "agent"},
		{"gateway by module", "/x/g/g.yaml", "apps:\n  - app_module: solace_agent_mesh.gateway.generic.app", "entrypoint"},
		{"workflow by module", "/x/w/w.yaml", "apps:\n  - app_module: solace_agent_mesh.workflow.app", "workflow"},
		{"shared by filename", "/x/shared_config.yaml", "shared_config: []", "shared"},
		{"overlay by filename", "/x/overlays/dev.yaml", "apps: []", "overlay"},
		{"agent by directory", "/x/agents/a.yaml", "foo: bar", "agent"},
		{"unknown", "/x/random.yaml", "foo: bar", "unknown"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var doc map[string]any
			_ = yaml.Unmarshal([]byte(c.doc), &doc)
			if got := classifyFile(c.path, doc); got != c.want {
				t.Errorf("classifyFile(%s) = %q; want %q", c.name, got, c.want)
			}
		})
	}
}

// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package runner

import (
	"os"
	"path/filepath"
	"testing"
)

func TestClassifyApps_PythonOnly(t *testing.T) {
	apps := []any{
		map[string]any{
			"name":       "orchestrator",
			"app_module": "solace_agent_mesh.agents.orchestrator",
		},
		map[string]any{
			"name":       "gateway",
			"app_module": "solace_agent_mesh.gateway.http_sse.app",
		},
	}

	result := ClassifyApps(apps)

	if len(result.PythonApps) != 2 {
		t.Fatalf("expected 2 python apps, got %d", len(result.PythonApps))
	}
	if len(result.ExecGroups) != 0 {
		t.Fatalf("expected 0 exec groups, got %d", len(result.ExecGroups))
	}
}

func TestClassifyApps_ExecOnly(t *testing.T) {
	apps := []any{
		map[string]any{
			"name":     "agent1",
			"app_exec": "./bin/sam-awe",
		},
		map[string]any{
			"name":     "agent2",
			"app_exec": "./bin/sam-awe",
		},
	}

	result := ClassifyApps(apps)

	if len(result.PythonApps) != 0 {
		t.Fatalf("expected 0 python apps, got %d", len(result.PythonApps))
	}
	if len(result.ExecGroups) != 1 {
		t.Fatalf("expected 1 exec group, got %d", len(result.ExecGroups))
	}
	if apps := result.ExecGroups["./bin/sam-awe"]; len(apps) != 2 {
		t.Fatalf("expected 2 apps in exec group, got %d", len(apps))
	}
}

func TestClassifyApps_Mixed(t *testing.T) {
	apps := []any{
		map[string]any{
			"name":       "gateway",
			"app_module": "solace_agent_mesh.gateway.http_sse.app",
		},
		map[string]any{
			"name":     "go_agent",
			"app_exec": "./bin/sam-awe",
		},
		map[string]any{
			"name":     "custom_exec",
			"app_exec": "/usr/local/bin/my-agent",
		},
	}

	result := ClassifyApps(apps)

	if len(result.PythonApps) != 1 {
		t.Fatalf("expected 1 python app, got %d", len(result.PythonApps))
	}
	if len(result.ExecGroups) != 2 {
		t.Fatalf("expected 2 exec groups, got %d", len(result.ExecGroups))
	}
}

func TestClassifyApps_EmptyExecPath(t *testing.T) {
	apps := []any{
		map[string]any{
			"name":     "agent",
			"app_exec": "",
		},
	}

	result := ClassifyApps(apps)

	if len(result.PythonApps) != 1 {
		t.Fatalf("expected 1 python app, got %d", len(result.PythonApps))
	}
}

func TestPrescanForExec(t *testing.T) {
	dir := t.TempDir()

	pyFile := filepath.Join(dir, "python.yaml")
	_ = os.WriteFile(pyFile, []byte("apps:\n  - name: foo\n    app_module: bar\n"), 0o644)

	goFile := filepath.Join(dir, "go.yaml")
	_ = os.WriteFile(goFile, []byte("apps:\n  - name: foo\n    app_exec: ./bin/sam-awe\n"), 0o644)

	if PrescanForExec([]string{pyFile}) {
		t.Fatal("expected no app_exec in python-only file")
	}

	if !PrescanForExec([]string{goFile}) {
		t.Fatal("expected app_exec in go file")
	}

	if !PrescanForExec([]string{pyFile, goFile}) {
		t.Fatal("expected app_exec when mixed files")
	}
}

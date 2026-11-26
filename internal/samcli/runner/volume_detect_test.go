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

func TestDetectVolumeRequirements_VolumeSlots(t *testing.T) {
	cfg := map[string]any{
		"apps": []any{
			map[string]any{
				"app_config": map[string]any{
					"agent_name": "test-agent",
					"namespace":  "test-ns",
					"volumes": []any{
						map[string]any{
							"name":      "project",
							"provision": "static",
							"volume_id": "vol-123",
						},
					},
				},
			},
		},
	}

	result := DetectVolumeRequirements(cfg)
	if !result.NeedsVolumes {
		t.Error("expected NeedsVolumes=true for config with volume slots")
	}
	if result.Namespace != "test-ns" {
		t.Errorf("Namespace = %q, want test-ns", result.Namespace)
	}
}

func TestDetectVolumeRequirements_ToolVolumeBindings(t *testing.T) {
	cfg := map[string]any{
		"apps": []any{
			map[string]any{
				"app_config": map[string]any{
					"agent_name": "test-agent",
					"tool_volume_bindings": map[string]any{
						"my-tool": map[string]any{
							"workspace": "project-slot",
						},
					},
				},
			},
		},
	}

	result := DetectVolumeRequirements(cfg)
	if !result.NeedsVolumes {
		t.Error("expected NeedsVolumes=true for config with tool_volume_bindings")
	}
}

func TestDetectVolumeRequirements_ToolVolumeParams(t *testing.T) {
	cfg := map[string]any{
		"apps": []any{
			map[string]any{
				"app_config": map[string]any{
					"agent_name": "test-agent",
					"tools": []any{
						map[string]any{
							"name": "code-tool",
							"volume_params": []any{
								map[string]any{
									"name":       "workspace",
									"mode":       "readwrite",
									"mount_path": "/workspace",
								},
							},
						},
					},
				},
			},
		},
	}

	result := DetectVolumeRequirements(cfg)
	if !result.NeedsVolumes {
		t.Error("expected NeedsVolumes=true for config with volume_params in tools")
	}
}

func TestDetectVolumeRequirements_ToolVolumeBindingsInToolEntry(t *testing.T) {
	cfg := map[string]any{
		"apps": []any{
			map[string]any{
				"app_config": map[string]any{
					"agent_name": "test-agent",
					"tools": []any{
						map[string]any{
							"name": "code-tool",
							"volume_bindings": map[string]any{
								"workspace": "project-slot",
							},
						},
					},
				},
			},
		},
	}

	result := DetectVolumeRequirements(cfg)
	if !result.NeedsVolumes {
		t.Error("expected NeedsVolumes=true for config with volume_bindings in tool entry")
	}
}

func TestDetectVolumeRequirements_NoVolumes(t *testing.T) {
	cfg := map[string]any{
		"apps": []any{
			map[string]any{
				"app_config": map[string]any{
					"agent_name": "test-agent",
					"tools": []any{
						map[string]any{
							"name": "echo-tool",
						},
					},
				},
			},
		},
	}

	result := DetectVolumeRequirements(cfg)
	if result.NeedsVolumes {
		t.Error("expected NeedsVolumes=false for config without volume references")
	}
}

func TestDetectVolumeRequirements_ExtractsSTRMetadata(t *testing.T) {
	cfg := map[string]any{
		"apps": []any{
			map[string]any{
				"app_config": map[string]any{
					"agent_name": "test-agent",
					"namespace":  "my-ns",
				},
			},
			map[string]any{
				"app_config": map[string]any{
					"worker_id":    "str-worker",
					"packages_dir": "/path/to/tools",
				},
			},
		},
	}

	result := DetectVolumeRequirements(cfg)
	if result.ToolsDir != "/path/to/tools" {
		t.Errorf("ToolsDir = %q", result.ToolsDir)
	}
	if result.Namespace != "my-ns" {
		t.Errorf("Namespace = %q", result.Namespace)
	}
}

func TestDetectVolumeRequirements_DefaultNamespace(t *testing.T) {
	cfg := map[string]any{
		"apps": []any{
			map[string]any{
				"app_config": map[string]any{
					"agent_name": "test-agent",
				},
			},
		},
	}

	result := DetectVolumeRequirements(cfg)
	if result.Namespace != "solace-agent-mesh" {
		t.Errorf("Namespace = %q, want solace-agent-mesh", result.Namespace)
	}
}

func TestDetectVolumeRequirements_EmptyApps(t *testing.T) {
	result := DetectVolumeRequirements(map[string]any{})
	if result.NeedsVolumes {
		t.Error("expected NeedsVolumes=false for empty config")
	}
}

func TestDetectVolumeRequirements_ManifestScan(t *testing.T) {
	toolsDir := t.TempDir()

	toolDir := filepath.Join(toolsDir, "my-tool")
	if err := os.Mkdir(toolDir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `version: 1
tools:
  my_tool:
    executable: ./my-tool
    volume_params:
      - name: workspace
        mode: readwrite
        mount_path: /workspace
`
	if err := os.WriteFile(filepath.Join(toolDir, "manifest.yaml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := map[string]any{
		"apps": []any{
			map[string]any{
				"app_config": map[string]any{
					"worker_id": "str-worker",
					"tools_dir": toolsDir,
				},
			},
		},
	}

	result := DetectVolumeRequirements(cfg)
	if !result.NeedsVolumes {
		t.Error("expected NeedsVolumes=true when manifest under tools_dir declares volume_params")
	}
	if result.ToolsDir != toolsDir {
		t.Errorf("ToolsDir = %q, want %q", result.ToolsDir, toolsDir)
	}
}

func TestDetectVolumeRequirements_ManifestScan_NoVolumes(t *testing.T) {
	toolsDir := t.TempDir()

	toolDir := filepath.Join(toolsDir, "simple-tool")
	if err := os.Mkdir(toolDir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `version: 1
tools:
  simple_tool:
    executable: ./simple-tool
`
	if err := os.WriteFile(filepath.Join(toolDir, "manifest.yaml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := map[string]any{
		"apps": []any{
			map[string]any{
				"app_config": map[string]any{
					"worker_id": "str-worker",
					"tools_dir": toolsDir,
				},
			},
		},
	}

	result := DetectVolumeRequirements(cfg)
	if result.NeedsVolumes {
		t.Error("expected NeedsVolumes=false when manifest under tools_dir declares no volume_params")
	}
}

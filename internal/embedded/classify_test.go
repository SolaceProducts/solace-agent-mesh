// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package embedded

import (
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/samapp"
)

func TestClassifyForEmbedded(t *testing.T) {
	tests := []struct {
		name string
		apps []any
		want EmbeddedApps
	}{
		{
			name: "gateway config detected by gateway_id",
			apps: []any{
				map[string]any{
					"name":       "gw",
					"app_config": map[string]any{"gateway_id": "gw-1"},
				},
			},
			want: EmbeddedApps{
				Gateway: []samapp.AppEntry{{Name: "gw", AppConfig: map[string]any{"gateway_id": "gw-1"}}},
			},
		},
		{
			name: "platform detected by component_type",
			apps: []any{
				map[string]any{
					"name":       "plat",
					"app_config": map[string]any{"component_type": "platform"},
				},
			},
			want: EmbeddedApps{
				Platform: []samapp.AppEntry{{Name: "plat", AppConfig: map[string]any{"component_type": "platform"}}},
			},
		},
		{
			name: "awe is the default for unrecognized configs",
			apps: []any{
				map[string]any{
					"name":       "agent1",
					"app_config": map[string]any{"kind": "agent"},
				},
			},
			want: EmbeddedApps{
				AWE: []samapp.AppEntry{{Name: "agent1", AppConfig: map[string]any{"kind": "agent"}}},
			},
		},
		{
			name: "str detected by manifest_paths",
			apps: []any{
				map[string]any{
					"name":       "tools",
					"app_config": map[string]any{"manifest_paths": []any{"/tools/manifest.yaml"}},
				},
			},
			want: EmbeddedApps{
				STR: []samapp.AppEntry{{Name: "tools", AppConfig: map[string]any{"manifest_paths": []any{"/tools/manifest.yaml"}}}},
			},
		},
		{
			name: "str detected by legacy manifest_path",
			apps: []any{
				map[string]any{
					"name":       "tools-legacy",
					"app_config": map[string]any{"manifest_path": "/tools/manifest.yaml"},
				},
			},
			want: EmbeddedApps{
				STR: []samapp.AppEntry{{Name: "tools-legacy", AppConfig: map[string]any{"manifest_path": "/tools/manifest.yaml"}}},
			},
		},
		{
			name: "python apps have app_module",
			apps: []any{
				map[string]any{
					"name":       "py",
					"app_module": "my.module",
					"app_config": map[string]any{},
				},
			},
			want: EmbeddedApps{
				Python: []map[string]any{{"name": "py", "app_module": "my.module", "app_config": map[string]any{}}},
			},
		},
		{
			name: "str detected by worker_id",
			apps: []any{
				map[string]any{
					"name": "str_builtin_tools",
					"app_config": map[string]any{
						"worker_id":  "builtin-tools-worker",
						"skills_dir": "/opt/sam/skills",
					},
				},
			},
			want: EmbeddedApps{
				STR: []samapp.AppEntry{{
					Name: "str_builtin_tools",
					AppConfig: map[string]any{
						"worker_id":  "builtin-tools-worker",
						"skills_dir": "/opt/sam/skills",
					},
				}},
			},
		},
		{
			name: "str detected by sandbox config",
			apps: []any{
				map[string]any{
					"name": "str_sandbox",
					"app_config": map[string]any{
						"sandbox": map[string]any{"mode": "direct"},
					},
				},
			},
			want: EmbeddedApps{
				STR: []samapp.AppEntry{{
					Name: "str_sandbox",
					AppConfig: map[string]any{
						"sandbox": map[string]any{"mode": "direct"},
					},
				}},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ClassifyForEmbedded(tt.apps)
			if len(got.Platform) != len(tt.want.Platform) {
				t.Errorf("Platform count = %d, want %d", len(got.Platform), len(tt.want.Platform))
			}
			if len(got.Gateway) != len(tt.want.Gateway) {
				t.Errorf("Gateway count = %d, want %d", len(got.Gateway), len(tt.want.Gateway))
			}
			if len(got.AWE) != len(tt.want.AWE) {
				t.Errorf("AWE count = %d, want %d", len(got.AWE), len(tt.want.AWE))
			}
			if len(got.STR) != len(tt.want.STR) {
				t.Errorf("STR count = %d, want %d", len(got.STR), len(tt.want.STR))
			}
			if len(got.Python) != len(tt.want.Python) {
				t.Errorf("Python count = %d, want %d", len(got.Python), len(tt.want.Python))
			}
		})
	}
}

func TestRuntimeNeedsAWEExecutor(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		apps EmbeddedApps
		want bool
	}{
		{"static AWE apps", EmbeddedApps{AWE: []samapp.AppEntry{{Name: "agent"}}}, true},
		{"platform present, agent-less AWE base", EmbeddedApps{Platform: []samapp.AppEntry{{Name: "platform"}}}, true},
		{"platform + gateway, no AWE apps", EmbeddedApps{
			Platform: []samapp.AppEntry{{Name: "platform"}},
			Gateway:  []samapp.AppEntry{{Name: "gw"}},
		}, true},
		{"gateway only, no platform", EmbeddedApps{Gateway: []samapp.AppEntry{{Name: "gw"}}}, false},
		{"STR only", EmbeddedApps{STR: []samapp.AppEntry{{Name: "str"}}}, false},
		{"empty", EmbeddedApps{}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := runtimeNeedsAWEExecutor(tt.apps); got != tt.want {
				t.Errorf("runtimeNeedsAWEExecutor() = %v, want %v", got, tt.want)
			}
		})
	}
}

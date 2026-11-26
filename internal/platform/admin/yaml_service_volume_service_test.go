// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"log/slog"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"gopkg.in/yaml.v3"
)

func appConfigFromYAML(t *testing.T, yamlStr string) map[string]any {
	t.Helper()
	var parsed map[string]any
	if err := yaml.Unmarshal([]byte(yamlStr), &parsed); err != nil {
		t.Fatalf("invalid YAML: %v\n%s", err, yamlStr)
	}
	apps := parsed["apps"].([]any)
	return apps[0].(map[string]any)["app_config"].(map[string]any)
}

func volumeSlotAgent(t *testing.T) *store.Agent {
	t.Helper()
	a := builtinAgent(t)
	a.AdditionalConfigurations = []byte(`{"volumes":[{"name":"test_ws","provision":"auto_create","ttl":"1h"}]}`)
	return a
}

func TestBuildAgentConfigFull_EmitsVolumeServiceWhenVolumesDeclared(t *testing.T) {
	t.Setenv("SAM_VOLUME_ROOT", "")

	svc := NewYAMLService("test-ns", slog.Default())
	yamlStr, err := svc.GenerateAgentYAMLFull(volumeSlotAgent(t), []string{"builtin_volume_tools"}, nil, nil, nil,
		ToolEmission{BuiltinVolumeBindings: map[string]map[string]string{"builtin_volume_tools": {"workspace": "test_ws"}}})
	if err != nil {
		t.Fatalf("GenerateAgentYAMLFull: %v", err)
	}
	cfg := appConfigFromYAML(t, yamlStr)
	vs, ok := cfg["volume_service"].(map[string]any)
	if !ok {
		t.Fatalf("expected volume_service block, got %v", cfg["volume_service"])
	}
	if vs["type"] != "filesystem" {
		t.Errorf("volume_service.type = %v, want filesystem", vs["type"])
	}
	if vs["root"] != "/app/data/volumes" {
		t.Errorf("volume_service.root = %v, want resolved default /app/data/volumes (NOT a ${VAR} literal)", vs["root"])
	}
}

func TestBuildAgentConfigFull_VolumeServiceRootFromEnv(t *testing.T) {
	t.Setenv("SAM_VOLUME_ROOT", "/mnt/efs/volumes")

	svc := NewYAMLService("test-ns", slog.Default())
	yamlStr, err := svc.GenerateAgentYAMLFull(volumeSlotAgent(t), []string{"builtin_volume_tools"}, nil, nil, nil, ToolEmission{})
	if err != nil {
		t.Fatalf("GenerateAgentYAMLFull: %v", err)
	}
	cfg := appConfigFromYAML(t, yamlStr)
	vs := cfg["volume_service"].(map[string]any)
	if vs["root"] != "/mnt/efs/volumes" {
		t.Errorf("volume_service.root = %v, want /mnt/efs/volumes from env", vs["root"])
	}
}

func TestBuildAgentConfigFull_VolumeServiceNotClobbered(t *testing.T) {
	t.Setenv("SAM_VOLUME_ROOT", "")

	a := builtinAgent(t)
	a.AdditionalConfigurations = []byte(`{"volumes":[{"name":"ws"}],"volume_service":{"type":"memory"}}`)

	svc := NewYAMLService("test-ns", slog.Default())
	yamlStr, err := svc.GenerateAgentYAMLFull(a, []string{"builtin_volume_tools"}, nil, nil, nil, ToolEmission{})
	if err != nil {
		t.Fatalf("GenerateAgentYAMLFull: %v", err)
	}
	cfg := appConfigFromYAML(t, yamlStr)
	vs := cfg["volume_service"].(map[string]any)
	if vs["type"] != "memory" {
		t.Errorf("volume_service = %v, want caller-supplied {type: memory} preserved", vs)
	}
}

func TestBuildAgentConfigFull_NoVolumeServiceWithoutVolumes(t *testing.T) {
	t.Parallel()
	svc := NewYAMLService("test-ns", slog.Default())
	yamlStr, err := svc.GenerateAgentYAMLFull(builtinAgent(t), []string{"builtin_artifact_tools"}, nil, nil, nil, ToolEmission{})
	if err != nil {
		t.Fatalf("GenerateAgentYAMLFull: %v", err)
	}
	cfg := appConfigFromYAML(t, yamlStr)
	if _, ok := cfg["volume_service"]; ok {
		t.Errorf("did not expect volume_service for a non-volume agent, got %v", cfg["volume_service"])
	}
}

func TestHasVolumeSlots(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		cfg  map[string]any
		want bool
	}{
		{name: "absent", cfg: map[string]any{}, want: false},
		{name: "empty slice", cfg: map[string]any{"volumes": []any{}}, want: false},
		{name: "non-empty []any", cfg: map[string]any{"volumes": []any{map[string]any{"name": "ws"}}}, want: true},
		{name: "non-empty []map", cfg: map[string]any{"volumes": []map[string]any{{"name": "ws"}}}, want: true},
		{name: "wrong type", cfg: map[string]any{"volumes": "ws"}, want: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := hasVolumeSlots(tc.cfg); got != tc.want {
				t.Errorf("hasVolumeSlots(%v) = %v, want %v", tc.cfg, got, tc.want)
			}
		})
	}
}

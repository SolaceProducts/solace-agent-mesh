// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package bootstrap

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samapp"
	"github.com/SolaceProducts/solace-agent-mesh/internal/settings"
)

func volumeServiceBlock(node any) map[string]any {
	switch v := node.(type) {
	case map[string]any:
		if block, ok := v["volume_service"].(map[string]any); ok {
			return block
		}
		for _, child := range v {
			if block := volumeServiceBlock(child); block != nil {
				return block
			}
		}
	case []any:
		for _, child := range v {
			if block := volumeServiceBlock(child); block != nil {
				return block
			}
		}
	}
	return nil
}

func gatewayVolumeListDatabase(t *testing.T, cfg config.Config, err error) string {
	t.Helper()
	if err != nil {
		t.Fatalf("load gateway config: %v", err)
	}
	block := volumeServiceBlock(config.AsMap(cfg))
	if block == nil {
		t.Fatal("gateway config has no volume_service block")
	}
	databaseURL, _ := block["database_url"].(string)
	return databaseURL
}

func aweVolumeListDatabase(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "sam.yaml")
	if err := os.WriteFile(path, settings.AWEClusterBaseConfigYAML(), 0o644); err != nil {
		t.Fatalf("os.WriteFile: %v", err)
	}
	cfg, err := samapp.LoadFullConfig(t.Context(), &config.YAMLLoader{}, path)
	if err != nil {
		t.Fatalf("load awe_base.yaml: %v", err)
	}
	svcCfg := resolveVolumeServiceConfig(config.AsMap(cfg), nil, true)
	if svcCfg == nil {
		t.Fatal("awe_base.yaml resolves no volume service with the volumes flag on")
	}
	databaseURL, _ := svcCfg["database_url"].(string)
	return databaseURL
}

func setOrUnsetEnv(t *testing.T, name string, value *string) {
	t.Helper()
	t.Setenv(name, "")
	if value != nil {
		t.Setenv(name, *value)
		return
	}
	if err := os.Unsetenv(name); err != nil {
		t.Fatalf("os.Unsetenv: %v", err)
	}
}

func TestGatewayAndAWEBundledConfigsNameTheSameVolumeListDatabase(t *testing.T) {
	t.Setenv("SAM_DATA_DIR", t.TempDir())
	t.Setenv("SAM_VOLUME_ROOT", t.TempDir())

	for _, tc := range []struct {
		name              string
		databaseURL       *string
		volumeDatabaseURL *string
		want              string
	}{
		{name: "a shared database", databaseURL: new("postgres://sam@db:5432/orchestrator"), want: "postgres://sam@db:5432/orchestrator"},
		{name: "DATABASE_URL exported empty", databaseURL: new(""), want: ""},
		{name: "DATABASE_URL unset", want: ""},
		{
			name:              "a database named for the volume list wins over DATABASE_URL",
			databaseURL:       new("postgres://sam@db:5432/orchestrator"),
			volumeDatabaseURL: new("postgres://sam@db:5432/volumes"),
			want:              "postgres://sam@db:5432/volumes",
		},
		{name: "a database named for the volume list alone", volumeDatabaseURL: new("postgres://sam@db:5432/volumes"), want: "postgres://sam@db:5432/volumes"},
		{
			name:              "SAM_VOLUME_DATABASE_URL exported empty falls back to DATABASE_URL",
			databaseURL:       new("postgres://sam@db:5432/orchestrator"),
			volumeDatabaseURL: new(""),
			want:              "postgres://sam@db:5432/orchestrator",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setOrUnsetEnv(t, "DATABASE_URL", tc.databaseURL)
			setOrUnsetEnv(t, "SAM_VOLUME_DATABASE_URL", tc.volumeDatabaseURL)

			loader := &config.YAMLLoader{}
			image, err := loader.Load(t.Context(), filepath.Join("..", "..", "configs", "gwe", "gwe.yaml"))
			imageGateway := gatewayVolumeListDatabase(t, image, err)
			embedded, err := loader.LoadBytes(t.Context(), settings.DefaultConfigYAML()["default_gateway.yaml"])
			embeddedGateway := gatewayVolumeListDatabase(t, embedded, err)
			awe := aweVolumeListDatabase(t)

			if imageGateway != tc.want || embeddedGateway != tc.want || awe != tc.want {
				t.Fatalf("volume list database: configs/gwe gateway %q, embedded gateway %q, AWE %q; all three must be %q",
					imageGateway, embeddedGateway, awe, tc.want)
			}
		})
	}
}

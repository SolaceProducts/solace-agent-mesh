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

func TestAWEBase_PptxProfileDefaultsToNeutral(t *testing.T) {
	for _, tc := range []struct {
		name, env, want string
		set             bool
	}{
		{"unset", "", "neutral", false},
		{"solace opt-in", "solace_2025", "solace_2025", true},
		{"plain slides", "none", "none", true},
		{"set but empty", "", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.set {
				t.Setenv("SAM_PPTX_TEMPLATE_PROFILE", tc.env)
			} else {
				t.Setenv("SAM_PPTX_TEMPLATE_PROFILE", "")
				if err := os.Unsetenv("SAM_PPTX_TEMPLATE_PROFILE"); err != nil {
					t.Fatal(err)
				}
			}
			path := filepath.Join(t.TempDir(), "sam.yaml")
			if err := os.WriteFile(path, settings.AWEClusterBaseConfigYAML(), 0o644); err != nil {
				t.Fatal(err)
			}
			cfg, err := samapp.LoadFullConfig(t.Context(), &config.YAMLLoader{}, path)
			if err != nil {
				t.Fatalf("load awe_base.yaml: %v", err)
			}
			defaults := samapp.ExtractToolDefaults(config.AsMap(cfg)["tool_defaults"])
			if got := defaults["pptx_create_presentation"]["template_profile"]; got != tc.want {
				t.Errorf("tool_defaults.pptx_create_presentation.template_profile = %v, want %q", got, tc.want)
			}
		})
	}
}

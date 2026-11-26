// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/settings"
	"gopkg.in/yaml.v3"
)

func TestBuilderSkillRefsAreReconciled(t *testing.T) {
	reconciled := bundledSkillNames()
	if len(reconciled) == 0 {
		t.Fatal("no bundled skills — the assertion below would pass vacuously")
	}

	for _, ref := range builderAgentSkillRefs(t) {
		if _, ok := reconciled[ref]; !ok {
			t.Errorf("Builder declares skill %q, but no reconcile source ships it — "+
				"add it under internal/builtinskills/files/, internal/skills/design, or "+
				"internal/settings/files/skills/, or drop the reference", ref)
		}
	}
}

func builderAgentSkillRefs(t *testing.T) []string {
	t.Helper()

	raw, ok := settings.BuilderConfigYAML()["builder_agent.yaml"]
	if !ok {
		t.Fatal("settings.BuilderConfigYAML has no builder_agent.yaml entry")
	}

	var doc struct {
		Apps []struct {
			Name      string `yaml:"name"`
			AppConfig struct {
				Skills []struct {
					Name string `yaml:"name"`
				} `yaml:"skills"`
			} `yaml:"app_config"`
		} `yaml:"apps"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse builder_agent.yaml: %v", err)
	}

	var refs []string
	for _, app := range doc.Apps {
		for _, sk := range app.AppConfig.Skills {
			if sk.Name != "" {
				refs = append(refs, sk.Name)
			}
		}
	}
	if len(refs) == 0 {
		t.Fatal("no skill refs parsed from builder_agent.yaml — the parse shape drifted")
	}
	return refs
}

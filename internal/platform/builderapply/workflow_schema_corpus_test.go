// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package builderapply

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/SolaceProducts/solace-agent-mesh/internal/testing/sourcetree"
)

func TestWorkflowSchema_RealShippedConfigsAreQuiet(t *testing.T) {
	sourcetree.RequireInternal(t)
	t.Parallel()
	matches, err := filepath.Glob(filepath.Join("..", "..", "..", "configs-e2e-harness", "workflow_*.yaml"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(matches) == 0 {
		t.Fatal("no shipped workflow configs found to check against")
	}

	checked := 0
	for _, path := range matches {
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatalf("read %s: %v", path, readErr)
		}
		var doc struct {
			Apps []struct {
				Name      string         `yaml:"name"`
				AppConfig map[string]any `yaml:"app_config"`
			} `yaml:"apps"`
		}
		if err := yaml.Unmarshal(raw, &doc); err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}

		for _, app := range doc.Apps {
			if _, isWorkflow := app.AppConfig["workflow"]; !isWorkflow {
				continue
			}
			checked++
			if w := workflowSchemaWarnings(app.AppConfig); len(w) != 0 {
				t.Errorf("%s / %s: warnings = %v, want silence for a config the repo ships and runs",
					filepath.Base(path), app.Name, w)
			}
		}
	}
	if checked == 0 {
		t.Fatal("matched files but found no workflow apps — the corpus stopped covering anything")
	}
	t.Logf("checked %d shipped workflow configs", checked)
}

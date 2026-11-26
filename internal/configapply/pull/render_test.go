// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package pull

import (
	"bytes"
	"strings"
	"testing"
)

func TestRenderResultPrintsAllSections(t *testing.T) {
	res := &Result{
		OutputDir:    "/tmp/snapshot",
		ManifestPath: "/tmp/snapshot/manifests/pulled-2026-05-05.yaml",
		Counts:       map[string]int{"model": 2, "toolset": 3, "agent": 1},
		SecretPlaceholders: []SecretWarning{
			{Kind: "model", ResourceName: "planning", FieldPath: "authConfig.api_key", Placeholder: "${MODEL_PLANNING_AUTH_CONFIG_API_KEY}"},
		},
		Warnings: []string{"some warning"},
	}
	var buf bytes.Buffer
	RenderResult(&buf, res)
	out := buf.String()
	for _, want := range []string{
		"Wrote 2 models, 3 toolsets, 1 agent to /tmp/snapshot",
		"Manifest: /tmp/snapshot/manifests/pulled-2026-05-05.yaml",
		"${MODEL_PLANNING_AUTH_CONFIG_API_KEY}",
		"warning: some warning",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("RenderResult output missing %q\noutput:\n%s", want, out)
		}
	}
}

func TestRenderResultZero(t *testing.T) {
	var buf bytes.Buffer
	RenderResult(&buf, &Result{OutputDir: "/x"})
	out := buf.String()
	if !strings.Contains(out, "Wrote 0 resources to /x") {
		t.Errorf("zero render = %q", out)
	}
}

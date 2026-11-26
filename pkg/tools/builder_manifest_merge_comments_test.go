// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func TestManifestMerge_ACommentedStatusUpdateFollowsTheStoredPromptExample(t *testing.T) {
	const stored = "name: my-system\ncomponents:\n  - filename: my_agent.yaml # Artifact filename\n    type: agent # agent | workflow\n    name: MyAgent\n    status: draft # validated | draft | error | deployed\n    origin: session\n    depends_on: [my_database] # Names of other components this one requires\n    connectors: []\n    tools: # Notable tools\n      - web_request\n"
	store := newManifestStore(t, stored)

	result := mergeYAMLAt(t, store, "components:\n  - name: MyAgent\n    type: agent\n    status: validated              # validated | draft | error | deployed\n", "")

	if result.Status == tool.StatusError {
		t.Fatalf("status update refused: %s", strings.ReplaceAll(result.Message, "\n", " | "))
	}
	if got := componentsByName(t, parseManifestMap(t, store.latest(t)))["MyAgent"]["status"]; got != "validated" {
		t.Errorf("status = %v", got)
	}
}

func TestManifestMerge_ACommentedFragmentOnAFlowEntryConvergesOnReapply(t *testing.T) {
	store := newManifestStore(t, "name: s\ncomponents:\n  - {name: A, type: agent, filename: a.yaml, status: draft, tools: [x]}\n")
	fragment := "components:\n  - name: A\n    type: agent\n    config: {k: v}   # tuning\n"
	if first := mergeYAMLAt(t, store, fragment, ""); first.Status == tool.StatusError {
		t.Fatalf("first apply refused: %s", first.Message)
	}
	afterFirst := store.latest(t)

	second := mergeYAMLAt(t, store, fragment, "")

	if second.Status == tool.StatusError {
		t.Fatalf("second apply refused: %s", strings.ReplaceAll(second.Message, "\n", " | "))
	}
	if store.latest(t) != afterFirst {
		t.Errorf("the second application changed the manifest:\n%s\n---\n%s", afterFirst, store.latest(t))
	}
}

func TestManifestMerge_TheYAMLToolRefusesAMergeThatWouldNotParse(t *testing.T) {
	const manifest = "name: s\ncomponents:\n  - {name: B, type: agent, filename: b.yaml}\n"
	store := newManifestStore(t, manifest)

	result := mergeYAMLAt(t, store, "description: |2-\n  \tfoo\n", "")

	if result.Status != tool.StatusError || !strings.Contains(result.Message, "the merged manifest would not parse as YAML (yaml: ") || !strings.Contains(result.Message, "found a tab character") || !strings.Contains(result.Message, "the manifest was not changed") {
		t.Fatalf("result = %q %q, want the merged-manifest refusal naming the cause", result.Status, result.Message)
	}
	if strings.Contains(result.Message, "the stored manifest") {
		t.Errorf("a good stored manifest was blamed: %s", result.Message)
	}
	if store.latest(t) != manifest || store.versions(t) != 1 {
		t.Errorf("the manifest changed (versions = %d)", store.versions(t))
	}
}

func TestManifestMerge_ACommentIsDroppedAtATopLevelPath(t *testing.T) {
	store := newManifestStore(t, "name: s\narchitecture: {note: a}\ncomponents:\n  - {name: A, type: agent, filename: a.yaml}\n")

	result := mergeYAMLAt(t, store, "steps:   # in order\n  - one\n", "architecture")

	if result.Status == tool.StatusError {
		t.Fatalf("merge refused: %s", result.Message)
	}
	if strings.Contains(store.latest(t), "in order") {
		t.Errorf("the comment was kept:\n%s", store.latest(t))
	}
}

func TestManifestMerge_OtherArtifactsKeepFragmentComments(t *testing.T) {
	routes := map[string]string{"": "extra: 2   # keep this note\n", "items": "- two   # keep this note\n"}
	for path, fragment := range routes {
		t.Run("path="+path, func(t *testing.T) {
			toolCtx, facade := newDataToolContext(t)
			saveDataToolArtifact(t, context.Background(), facade, "agent.yaml", []byte("name: a\nitems:\n  - one\n"), SAMAgentConfigMIMEType)
			args := map[string]any{"artifact_name": "agent.yaml", "yaml_fragment": fragment}
			if path != "" {
				args["path"] = path
			}

			result, err := executeWithinDeadline(&YAMLMergeArtifactTool{}, args, toolCtx)
			result = resultOrError(t, "yaml_merge_artifact", result, err)

			if result.Status == tool.StatusError {
				t.Fatalf("merge refused: %s", result.Message)
			}
			content, _, loadErr := facade.LoadArtifact(context.Background(), "agent.yaml", nil)
			if loadErr != nil {
				t.Fatal(loadErr)
			}
			if !strings.Contains(string(content), "# keep this note") {
				t.Errorf("a non-manifest artifact lost the fragment's comment:\n%s", content)
			}
		})
	}
}

func TestManifestMerge_AFlowComponentsListBecomesABlockListOnAnyMerge(t *testing.T) {
	store := newManifestStore(t, "name: s\ncomponents: [{name: A, type: agent, filename: a.yaml}]\n")

	result := mergeYAMLAt(t, store, "components:\n  - {name: N, type: agent, filename: n.yaml}\n", "")

	if result.Status == tool.StatusError {
		t.Fatalf("merge refused: %s", result.Message)
	}
	if !strings.Contains(store.latest(t), "components:\n  - ") {
		t.Errorf("the non-empty list stayed in flow style:\n%s", store.latest(t))
	}
}

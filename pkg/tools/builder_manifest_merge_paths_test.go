// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"context"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

const manifestTail = "architecture:\n  note: a\n  deep:\n    x: 1\n"

func mergeYAMLAt(t *testing.T, store *manifestStore, fragment, path string) *tool.Result {
	t.Helper()
	args := map[string]any{"artifact_name": manifestFilename, "yaml_fragment": fragment}
	if path != "" {
		args["path"] = path
	}
	result, err := executeWithinDeadline(&YAMLMergeArtifactTool{}, args, store.toolCtx)
	return resultOrError(t, "yaml_merge_artifact", result, err)
}

func mergeStructuredAt(t *testing.T, store *manifestStore, input string, patch map[string]any, path string) *tool.Result {
	t.Helper()
	args := map[string]any{"input_filename": input, "patch": patch}
	if path != "" {
		args["path"] = path
	}
	result, err := executeWithinDeadline(&MergeStructuredDataTool{}, args, store.toolCtx)
	return resultOrError(t, "merge_structured_data", result, err)
}

func TestManifestMerge_PathsIntoTheManifestAreRefused(t *testing.T) {
	yamlPaths := map[string]string{
		"an element":                 "components.0",
		"a field of an element":      "components.0.status",
		"a deeper element field":     "components.0.tools",
		"a nested root map":          "architecture.deep",
		"a path with an empty piece": "components..0",
	}
	for name, path := range yamlPaths {
		t.Run("yaml/"+name, func(t *testing.T) {
			manifest := fixtureManifest(numberedSpecs(3)) + manifestTail
			store := newManifestStore(t, manifest)

			result := mergeYAMLAt(t, store, "status: validated\n", path)

			if result.Status != tool.StatusError || !strings.Contains(result.Message, "goes deeper") || !strings.Contains(result.Message, "the manifest was not changed") {
				t.Fatalf("result = %q %q, want a refusal that says to merge at the root or a top-level key", result.Status, result.Message)
			}
			if store.latest(t) != manifest || store.versions(t) != 1 {
				t.Errorf("a refused merge must leave the manifest as it was (versions = %d)", store.versions(t))
			}
		})
		t.Run("structured/"+name, func(t *testing.T) {
			manifest := fixtureManifest(numberedSpecs(3)) + manifestTail
			store := newManifestStore(t, manifest)

			result := mergeStructuredAt(t, store, manifestFilename, map[string]any{"status": "validated"}, path)

			if result.Status != tool.StatusError || !strings.Contains(result.Message, "goes deeper") || !strings.Contains(result.Message, "the manifest was not changed") {
				t.Fatalf("result = %q %q, want a refusal that says to merge at the root or a top-level key", result.Status, result.Message)
			}
			if store.latest(t) != manifest || store.versions(t) != 1 {
				t.Errorf("a refused merge must leave the manifest as it was (versions = %d)", store.versions(t))
			}
		})
	}
}

func TestManifestMerge_ATopLevelKeyPathMergesOnlyThere(t *testing.T) {
	t.Run("yaml", func(t *testing.T) {
		store := newManifestStore(t, fixtureManifest(numberedSpecs(2))+manifestTail)
		before := decodeManifestComponents(t, store.latest(t))

		result := mergeYAMLAt(t, store, "note: b\n", "architecture")

		if result.Status == tool.StatusError {
			t.Fatalf("merge refused: %s", result.Message)
		}
		assertComponentsAfterStatusUpdate(t, before, decodeManifestComponents(t, store.latest(t)), nil)
		var root map[string]any
		if err := yaml.Unmarshal([]byte(store.latest(t)), &root); err != nil {
			t.Fatal(err)
		}
		architecture, _ := root["architecture"].(map[string]any)
		if architecture["note"] != "b" || architecture["deep"] == nil || root["note"] != nil {
			t.Errorf("root = %v, want only architecture.note changed", root)
		}
	})
	t.Run("structured", func(t *testing.T) {
		store := newManifestStore(t, fixtureManifest(numberedSpecs(2))+manifestTail)

		result := mergeStructuredAt(t, store, manifestFilename, map[string]any{"note": "b", "name": "Comp0"}, "architecture")

		if result.Status == tool.StatusError {
			t.Fatalf("merge refused: %s", result.Message)
		}
		var root map[string]any
		if err := yaml.Unmarshal([]byte(store.latest(t)), &root); err != nil {
			t.Fatal(err)
		}
		architecture, _ := root["architecture"].(map[string]any)
		if architecture["note"] != "b" || root["name"] != "support-mesh" || root["note"] != nil {
			t.Errorf("root = %v, want the patch to land only inside architecture", root)
		}
	})
}

func TestManifestMerge_VersionSuffixOnTheManifestIsRefused(t *testing.T) {
	for name, mimeType := range map[string]string{"manifest MIME type": SAMBuildManifestMIMEType, "generic YAML MIME type": "application/x-yaml"} {
		t.Run(name, func(t *testing.T) {
			toolCtx, facade := newDataToolContext(t)
			manifest := fixtureManifest(numberedSpecs(3))
			saveDataToolArtifact(t, context.Background(), facade, manifestFilename, []byte(manifest), mimeType)
			store := &manifestStore{ctx: context.Background(), toolCtx: toolCtx, facade: facade}

			result := mergeStructuredAt(t, store, manifestFilename+":0", map[string]any{"display_name": "X"}, "")

			if result.Status != tool.StatusError || !strings.Contains(result.Message, "latest version") || !strings.Contains(result.Message, "the manifest was not changed") {
				t.Fatalf("result = %q %q, want a refusal of the :version suffix", result.Status, result.Message)
			}
			if store.latest(t) != manifest || store.versions(t) != 1 {
				t.Errorf("a refused merge must leave the manifest as it was (versions = %d)", store.versions(t))
			}
		})
	}
}

func TestManifestMerge_NonManifestArtifactsKeepPathsAndVersionSuffixes(t *testing.T) {
	toolCtx, facade := newDataToolContext(t)
	ctx := context.Background()
	saveDataToolArtifact(t, ctx, facade, "agent.yaml", []byte("name: a\napps:\n  - name: one\n    tools: []\n"), "application/x-yaml")
	saveDataToolArtifact(t, ctx, facade, "agent.yaml", []byte("name: a\napps:\n  - name: one\n    tools: []\nextra: 1\n"), "application/x-yaml")

	yamlResult, err := (&YAMLMergeArtifactTool{}).Execute(ctx, map[string]any{
		"artifact_name": "agent.yaml", "yaml_fragment": "status: on\n", "path": "apps.0",
	}, toolCtx)
	if err != nil || yamlResult.Status == tool.StatusError {
		t.Fatalf("a deep path on a non-manifest artifact was refused: %v %v", err, yamlResult)
	}
	structuredResult, err := (&MergeStructuredDataTool{}).Execute(ctx, map[string]any{
		"input_filename": "agent.yaml:0", "patch": map[string]any{"status": "pinned"}, "path": "apps.0",
	}, toolCtx)
	if err != nil || structuredResult.Status == tool.StatusError {
		t.Fatalf("a :version suffix on a non-manifest artifact was refused: %v %v", err, structuredResult)
	}

	content, _, loadErr := facade.LoadArtifact(ctx, "agent.yaml", nil)
	if loadErr != nil || !strings.Contains(string(content), "status: pinned") || strings.Contains(string(content), "extra") {
		t.Errorf("want the version-0 content merged and saved as latest (as on main), got %q %v", content, loadErr)
	}
}

func TestManifestMerge_FragmentWithSeveralDocuments(t *testing.T) {
	refused := map[string]string{
		"a second document with content":            "display_name: X\n---\nrisks: [z]\n",
		"a third document after an empty second":    "display_name: X\n---\n---\nrisks: [z]\n",
		"a third document after a commented second": "a: 1\n---\n# note\n---\nb: 2\n",
		"an empty-string second document":           "display_name: X\n---\n\"\"\n",
		"an explicit null second document":          "display_name: X\n---\n~\n",
		"a second document after an anchored empty": "display_name: X\n--- &a\n---\nb: 2\n",
	}
	for name, fragment := range refused {
		t.Run(name, func(t *testing.T) {
			store := newManifestStore(t, fixtureManifest(numberedSpecs(2)))
			manifest := store.latest(t)

			result := mergeYAMLAt(t, store, fragment, "")

			if result.Status != tool.StatusError || !strings.Contains(result.Message, "single YAML document") {
				t.Fatalf("result = %q %q", result.Status, result.Message)
			}
			if store.latest(t) != manifest || store.versions(t) != 1 {
				t.Errorf("the manifest changed (versions = %d)", store.versions(t))
			}
		})
	}
	t.Run("only empty documents after the first are accepted", func(t *testing.T) {
		store := newManifestStore(t, fixtureManifest(numberedSpecs(2)))

		result := mergeYAMLAt(t, store, "display_name: X\n---\n---\n", "")

		if result.Status == tool.StatusError {
			t.Errorf("trailing empty documents were refused: %s", result.Message)
		}
	})
}

func TestManifestMerge_ReportShape(t *testing.T) {
	for _, merger := range manifestMergers() {
		t.Run(merger.name+"/separators", func(t *testing.T) {
			store := newManifestStore(t, fixtureManifest(numberedSpecs(3)))

			result := merger.apply(t, store.toolCtx, manifestFilename, "components:\n  - {name: Comp0, status: validated, description: d}\n  - {name: Comp1, status: validated}\n")

			fieldsJoined := strings.Contains(result.Message, `("status", "description")`) || strings.Contains(result.Message, `("description", "status")`)
			if !fieldsJoined || !strings.Contains(result.Message, `); "Comp1" ("status")`) {
				t.Errorf("message = %q, want fields joined by ', ' and components by '; '", result.Message)
			}
		})
		t.Run(merger.name+"/updated before added", func(t *testing.T) {
			store := newManifestStore(t, fixtureManifest(numberedSpecs(2)))

			result := merger.apply(t, store.toolCtx, manifestFilename, "components:\n  - {name: Late, type: agent, filename: l.yaml}\n  - {name: Comp0, status: validated}\n")

			if u, a := strings.Index(result.Message, "Updated:"), strings.Index(result.Message, "Added:"); u < 0 || a < u {
				t.Errorf("message = %q", result.Message)
			}
		})
	}
	t.Run("yaml report data has no stray whitespace and none for other artifacts", func(t *testing.T) {
		store := newManifestStore(t, fixtureManifest(numberedSpecs(2)))
		result := manifestMergers()[0].apply(t, store.toolCtx, manifestFilename, "components:\n  - {name: Comp0, status: validated}\n")
		report, _ := result.Data["merge_report"].(string)
		if report == "" || report != strings.TrimSpace(report) {
			t.Errorf("merge_report = %q", report)
		}
		toolCtx, facade := newDataToolContext(t)
		saveDataToolArtifact(t, context.Background(), facade, "agent.yaml", []byte("name: a\nx: 1\n"), "application/x-yaml")
		plain, err := (&YAMLMergeArtifactTool{}).Execute(context.Background(), map[string]any{"artifact_name": "agent.yaml", "yaml_fragment": "x: 2\n"}, toolCtx)
		if err != nil {
			t.Fatal(err)
		}
		if got, _ := plain.Data["merge_report"].(string); got != "" || strings.HasSuffix(plain.Message, " ") {
			t.Errorf("non-manifest merge_report = %q, message = %q", got, plain.Message)
		}
		if noop := manifestMergers()[0].apply(t, store.toolCtx, manifestFilename, "display_name: Other\n"); strings.HasSuffix(noop.Message, " ") {
			t.Errorf("message has a trailing space: %q", noop.Message)
		}
	})
}

func TestManifestMerge_EscapesInTheUpdatedLabelAndNullKey(t *testing.T) {
	const odd = "name: mesh\ncomponents:\n  - {name: \"Odd\\nname\", type: agent, filename: o.yaml, status: draft}\n"
	for _, merger := range manifestMergers() {
		t.Run(merger.name, func(t *testing.T) {
			store := newManifestStore(t, odd)

			result := merger.apply(t, store.toolCtx, manifestFilename, "components:\n  - {name: \"Odd\\nname\", status: validated}\n")

			if result.Status == tool.StatusError || strings.Contains(result.Message, "Odd\nname") || !strings.Contains(result.Message, `"Odd\nname" (`) {
				t.Errorf("result = %q %q", result.Status, result.Message)
			}
		})
	}
}

func TestManifestMerge_AMergeKeyIsRefusedWhereverItArrives(t *testing.T) {
	yamlCases := map[string]struct{ fragment, path string }{
		"a plain key at the root":            {"<<: {full_validation_passed: true}\n", ""},
		"a quoted key at the root":           {"\"<<\": {full_validation_passed: true}\n", ""},
		"a key inside a root map":            {"architecture:\n  <<: {note: b}\n", ""},
		"a key inside an entry":              {"components:\n  - {<<: {id: forged}, name: Comp0, status: validated}\n", ""},
		"a key in a new entry":               {"components:\n  - {<<: {deployed_version: 4}, name: Late, type: agent, filename: l.yaml}\n", ""},
		"a key at a top-level path":          {"<<: {note: b}\n", "architecture"},
		"a path that is the key":             {"full_validation_passed: true\n", "<<"},
		"a scalar at a path that is the key": {"v\n", "<<"},
	}
	for name, tc := range yamlCases {
		t.Run("yaml/"+name, func(t *testing.T) {
			manifest := fixtureManifest(numberedSpecs(2)) + manifestTail
			store := newManifestStore(t, manifest)

			result := mergeYAMLAt(t, store, tc.fragment, tc.path)

			if result.Status != tool.StatusError || !strings.Contains(result.Message, "<<") || !strings.Contains(result.Message, "the manifest was not changed") {
				t.Fatalf("result = %q %q, want a refusal naming the merge key", result.Status, result.Message)
			}
			if store.latest(t) != manifest || store.versions(t) != 1 {
				t.Errorf("the manifest changed (versions = %d)", store.versions(t))
			}
		})
	}
	structuredCases := map[string]struct {
		patch map[string]any
		path  string
	}{
		"a key at the root":         {map[string]any{"<<": map[string]any{"full_validation_passed": true}}, ""},
		"a key inside a root map":   {map[string]any{"architecture": map[string]any{"<<": map[string]any{"note": "b"}}}, ""},
		"a key inside an entry":     {map[string]any{"components": []any{map[string]any{"name": "Comp0", "<<": map[string]any{"id": "forged"}}}}, ""},
		"a key in a new entry":      {map[string]any{"components": []any{map[string]any{"name": "Late", "type": "agent", "filename": "l.yaml", "<<": map[string]any{"deployed_version": 4}}}}, ""},
		"a key inside a list item":  {map[string]any{"risks": []any{map[string]any{"<<": "x"}}}, ""},
		"a key at a top-level path": {map[string]any{"<<": map[string]any{"note": "b"}}, "architecture"},
		"a path that is the key":    {map[string]any{"x": 1}, "<<"},
	}
	for name, tc := range structuredCases {
		t.Run("structured/"+name, func(t *testing.T) {
			manifest := fixtureManifest(numberedSpecs(2)) + manifestTail
			store := newManifestStore(t, manifest)

			result := mergeStructuredAt(t, store, manifestFilename, tc.patch, tc.path)

			if result.Status != tool.StatusError || !strings.Contains(result.Message, "<<") || !strings.Contains(result.Message, "the manifest was not changed") {
				t.Fatalf("result = %q %q, want a refusal naming the merge key", result.Status, result.Message)
			}
			if store.latest(t) != manifest || store.versions(t) != 1 {
				t.Errorf("the manifest changed (versions = %d)", store.versions(t))
			}
		})
	}
}

func TestManifestMerge_ATextValueOfLessThanLessThanIsNotAMergeKey(t *testing.T) {
	t.Run("yaml", func(t *testing.T) {
		store := newManifestStore(t, fixtureManifest(numberedSpecs(2)))

		result := mergeYAMLAt(t, store, "display_name: \"<<\"\nrisks:\n  - \"<<\"\n", "")

		if result.Status == tool.StatusError {
			t.Errorf("a string value of << was refused: %s", result.Message)
		}
	})
	t.Run("structured", func(t *testing.T) {
		store := newManifestStore(t, fixtureManifest(numberedSpecs(2)))

		result := mergeStructuredAt(t, store, manifestFilename, map[string]any{"display_name": "<<", "risks": []any{"<<"}}, "")

		if result.Status == tool.StatusError {
			t.Errorf("a string value of << was refused: %s", result.Message)
		}
	})
}

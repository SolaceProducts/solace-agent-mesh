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

const storedInertMerge = "name: mesh\ndisplay_name: D\narchitecture:\n  note: a\ncomponents:\n  - {name: B, type: agent, filename: b.yaml, \"<<\": {id: " + forgedID + "}}\n"

func TestManifestMerge_AStoredMergeKeyBlocksEveryStructuredMerge(t *testing.T) {
	cases := map[string]struct {
		manifest string
		patch    map[string]any
		path     string
	}{
		"root patch without components":                {storedInertMerge, map[string]any{"display_name": "X"}, ""},
		"root patch with components":                   {storedInertMerge, map[string]any{"components": []any{map[string]any{"name": "B", "status": "validated"}}}, ""},
		"top-level path":                               {storedInertMerge, map[string]any{"note": "z"}, "architecture"},
		"components path":                              {storedInertMerge, map[string]any{"x": "y"}, "components"},
		"key under a non-component key":                {"name: mesh\narchitecture:\n  \"<<\": {note: a}\ncomponents:\n  - {name: B, type: agent, filename: b.yaml}\n", map[string]any{"display_name": "X"}, ""},
		"key in a list under a root key":               {"name: mesh\nrisks:\n  - {\"<<\": {a: b}}\ncomponents:\n  - {name: B, type: agent, filename: b.yaml}\n", map[string]any{"display_name": "X"}, ""},
		"key in a map with a non-string key":           {"name: mesh\ncomponents:\n  - {name: B, type: agent, filename: b.yaml, 1: x, \"<<\": {id: " + forgedID + "}}\n", map[string]any{"display_name": "X"}, ""},
		"key nested under a map with a non-string key": {"name: mesh\ncomponents:\n  - {name: B, type: agent, filename: b.yaml, extra: {1: x, nested: {\"<<\": {id: " + forgedID + "}}}}\n", map[string]any{"display_name": "X"}, ""},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			store := newManifestStore(t, tc.manifest)
			result := mergeStructuredAt(t, store, manifestFilename, tc.patch, tc.path)
			if result.Status != tool.StatusError || !strings.Contains(result.Message, "the stored manifest holds a << key") {
				t.Fatalf("result = %q %q, want the stored-key refusal", result.Status, result.Message)
			}
			for _, advice := range []string{"editing the artifact text", "the manifest was not changed"} {
				if !strings.Contains(result.Message, advice) {
					t.Errorf("message = %q, want it to say %q", result.Message, advice)
				}
			}
			if store.latest(t) != tc.manifest || store.versions(t) != 1 {
				t.Errorf("manifest changed")
			}
		})
	}
}

func TestManifestMerge_TagsOnCollectionsAndTheRootAreRefused(t *testing.T) {
	cases := map[string]struct{ fragment, path string }{
		"a tagged components list":      {"components: !!seq\n  - {name: Late, type: agent, filename: l.yaml}\n", ""},
		"a tagged components list path": {"!!seq\n- {name: Late, type: agent, filename: l.yaml}\n", "components"},
		"a tagged entry":                {"components:\n  - !!map {name: Late, type: agent, filename: l.yaml}\n", ""},
		"a tagged root mapping":         {"!!map\ndisplay_name: X\n", ""},
		"an omap":                       {"risks: !!omap [{a: 1}]\n", ""},
		"a pairs":                       {"risks: !!pairs [{a: 1}]\n", ""},
		"a tagged scalar root at path":  {"!!str x\n", "display_name"},
		"a tagged int":                  {"display_name: !!int 5\n", ""},
		"a tagged timestamp":            {"display_name: !!timestamp 2020-01-01\n", ""},
		"a tagged merge key tag":        {"!!merge x: y\n", ""},
		"tag deep in an entry list":     {"components:\n  - {name: Late, type: agent, filename: l.yaml, tools: [a, !!str b]}\n", ""},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			manifest := fixtureManifest(numberedSpecs(2)) + manifestTail
			store := newManifestStore(t, manifest)
			result := mergeYAMLAt(t, store, tc.fragment, tc.path)
			if result.Status != tool.StatusError || !strings.Contains(result.Message, "tag") {
				t.Fatalf("result = %q %q, want a tag refusal", result.Status, result.Message)
			}
			if store.latest(t) != manifest {
				t.Errorf("manifest changed")
			}
		})
	}
}

func TestManifestMerge_BothUIOwnedKeysAreReported(t *testing.T) {
	for _, merger := range manifestMergers() {
		t.Run(merger.name, func(t *testing.T) {
			store := newManifestStore(t, "name: mesh\ncomponents:\n  - {name: Legacy, type: agent, filename: l.yaml}\n")
			res := merger.apply(t, store.toolCtx, manifestFilename, "components:\n  - {name: Late, type: agent, filename: l2.yaml, id: "+forgedID+", deployed_version: 3}\n  - {name: Legacy, status: validated, id: "+forgedID+", deployed_version: 4}\n")
			if res.Status == tool.StatusError {
				t.Fatal(res.Message)
			}
			for _, want := range []string{`id on "Late"`, `deployed_version on "Late"`, `id on "Legacy"`, `deployed_version on "Legacy"`} {
				if !strings.Contains(res.Message, want) {
					t.Errorf("message lacks %s: %s", want, res.Message)
				}
			}
		})
	}
}

func TestManifestMerge_AVersionPinAboveZeroIsRefused(t *testing.T) {
	store := newManifestStore(t, fixtureManifest(numberedSpecs(2)))
	saveDataToolArtifact(t, context.Background(), store.facade, manifestFilename, []byte(fixtureManifest(numberedSpecs(3))), SAMBuildManifestMIMEType)
	before := store.latest(t)
	for _, pin := range []string{":1", ":0"} {
		res := mergeStructuredAt(t, store, manifestFilename+pin, map[string]any{"display_name": "X"}, "")
		if res.Status != tool.StatusError || !strings.Contains(res.Message, "latest version") {
			t.Errorf("pin %s: %q %q", pin, res.Status, res.Message)
		}
	}
	if store.latest(t) != before || store.versions(t) != 2 {
		t.Error("manifest changed")
	}
}

func TestManifestMerge_TheRestOfAPatchWithComponentsStillMerges(t *testing.T) {
	manifest := fixtureManifest(numberedSpecs(2)) + manifestTail
	t.Run("structured", func(t *testing.T) {
		store := newManifestStore(t, manifest)
		res := mergeStructuredAt(t, store, manifestFilename, map[string]any{
			"components":   []any{map[string]any{"name": "Comp0", "status": "validated"}},
			"architecture": map[string]any{"note": "b"},
			"risks":        nil,
			"display_name": "X",
		}, "")
		if res.Status == tool.StatusError {
			t.Fatal(res.Message)
		}
		var root map[string]any
		if err := yamlUnmarshal(store.latest(t), &root); err != nil {
			t.Fatal(err)
		}
		architecture, _ := root["architecture"].(map[string]any)
		if architecture["note"] != "b" || architecture["deep"] == nil {
			t.Errorf("architecture = %v, want note changed and deep kept", architecture)
		}
		if _, still := root["risks"]; still {
			t.Errorf("risks was not deleted by null: %v", root["risks"])
		}
		if root["display_name"] != "X" {
			t.Errorf("display_name = %v", root["display_name"])
		}
	})
	t.Run("yaml", func(t *testing.T) {
		store := newManifestStore(t, manifest)
		res := mergeYAMLAt(t, store, "components:\n  - {name: Comp0, status: validated}\narchitecture:\n  note: b\n", "")
		if res.Status == tool.StatusError {
			t.Fatal(res.Message)
		}
		var root map[string]any
		if err := yamlUnmarshal(store.latest(t), &root); err != nil {
			t.Fatal(err)
		}
		architecture, _ := root["architecture"].(map[string]any)
		if architecture["note"] != "b" || architecture["deep"] == nil {
			t.Errorf("architecture = %v, want note changed and deep kept", architecture)
		}
	})
}

func yamlUnmarshal(s string, out any) error { return yaml.Unmarshal([]byte(s), out) }

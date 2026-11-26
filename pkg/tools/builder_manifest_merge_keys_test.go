// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

const (
	forgedID     = "01a14999-0000-7000-8000-000000000009"
	realID       = "01a14000-0000-7000-8000-0000000000aa"
	binaryMerge  = "!!binary PDw="
	binaryID     = "!!binary aWQ="
	binaryDeploy = "!!binary ZGVwbG95ZWRfdmVyc2lvbg=="
)

func TestManifestMerge_AValidUUIDIDIsNeverWritten(t *testing.T) {
	const manifest = "name: mesh\ncomponents:\n  - {name: Legacy, type: agent, filename: l.yaml, status: draft}\n"
	cases := map[string]string{
		"on a new entry":                    "components:\n  - {name: Late, type: agent, filename: l2.yaml, id: " + forgedID + "}\n",
		"on a matched component with no id": "components:\n  - {name: Legacy, type: agent, status: validated, id: " + forgedID + "}\n",
		"a deployed_version on a new entry": "components:\n  - {name: Late, type: agent, filename: l2.yaml, deployed_version: 3}\n",
		"a deployed_version on a match":     "components:\n  - {name: Legacy, type: agent, status: validated, deployed_version: 3}\n",
	}
	for _, merger := range manifestMergers() {
		for name, fragment := range cases {
			t.Run(merger.name+"/"+name, func(t *testing.T) {
				store := newManifestStore(t, manifest)

				result := merger.apply(t, store.toolCtx, manifestFilename, fragment)

				if result.Status == tool.StatusError {
					t.Fatalf("merge refused: %s", result.Message)
				}
				for _, component := range decodeManifestComponents(t, store.latest(t)) {
					if _, written := component["id"]; written {
						t.Errorf("id was written: %v", component)
					}
					if _, written := component["deployed_version"]; written {
						t.Errorf("deployed_version was written: %v", component)
					}
				}
				if !strings.Contains(result.Message, "Not written, the Builder UI owns them") {
					t.Errorf("message = %q, want the ignored field reported", result.Message)
				}
			})
		}
	}
}

func TestManifestMerge_SyntaxIsRefusedAtTheComponentsPath(t *testing.T) {
	cases := map[string]string{
		"a merge key in an entry":        "- {<<: {id: forged}, name: Late, type: agent, filename: l.yaml}\n",
		"a merge key in a later entry":   "- {name: Late, type: agent, filename: l.yaml}\n- {<<: {id: forged}, name: Later, type: agent, filename: l2.yaml}\n",
		"an anchor":                      "- {name: Late, type: agent, filename: l.yaml, tools: &t [x]}\n",
		"an explicit tag on a key":       "- {" + binaryMerge + ": {id: forged}, name: Late, type: agent, filename: l.yaml}\n",
		"an explicit tag on a value":     "- {name: Late, type: agent, filename: l.yaml, status: !custom draft}\n",
		"a second document":              "- {name: Late, type: agent, filename: l.yaml}\n---\n- {name: Z, type: agent, filename: z.yaml}\n",
		"a repeated key inside an entry": "- {name: Late, type: agent, filename: l.yaml, filename: m.yaml}\n",
	}
	for name, fragment := range cases {
		t.Run(name, func(t *testing.T) {
			manifest := fixtureManifest(numberedSpecs(2))
			store := newManifestStore(t, manifest)

			result := mergeYAMLAt(t, store, fragment, "components")

			if result.Status != tool.StatusError || !strings.Contains(result.Message, "the manifest was not changed") && !strings.Contains(result.Message, "nothing was saved") {
				t.Fatalf("result = %q %q, want a refusal", result.Status, result.Message)
			}
			if store.latest(t) != manifest || store.versions(t) != 1 {
				t.Errorf("the manifest changed (versions = %d)", store.versions(t))
			}
		})
	}
	t.Run("a merge key in a later entry of a structured components patch", func(t *testing.T) {
		manifest := fixtureManifest(numberedSpecs(2))
		store := newManifestStore(t, manifest)

		result := mergeStructuredAt(t, store, manifestFilename, map[string]any{"components": []any{
			map[string]any{"name": "Comp0", "status": "validated"},
			map[string]any{"name": "Comp1", "<<": map[string]any{"id": "x"}},
		}}, "")

		if result.Status != tool.StatusError || store.latest(t) != manifest {
			t.Errorf("result = %q %q", result.Status, result.Message)
		}
	})
}

func TestManifestMerge_ExplicitTagsAndNonTextKeysAreRefused(t *testing.T) {
	cases := map[string]string{
		"a tagged key that decodes to <<":               "components:\n  - {name: B, type: agent, filename: b.yaml, " + binaryMerge + ": {id: " + forgedID + ", deployed_version: 9}}\n",
		"a tagged key that decodes to id":               "components:\n  - {name: Comp0, status: validated, " + binaryID + ": " + forgedID + "}\n",
		"a tagged key that decodes to deployed_version": "components:\n  - {name: Comp0, status: validated, " + binaryDeploy + ": 42}\n",
		"a tagged key at the root":                      binaryMerge + ": {display_name: forged}\n",
		"a tagged key in a nested map":                  "architecture:\n  " + binaryMerge + ": {note: b}\n",
		"a tagged string key":                           "!!str display_name: Renamed\n",
		"a tagged value":                                "components:\n  - {name: Comp0, status: !custom validated}\n",
		"a set":                                         "risks: !!set {a, b}\n",
		"a python object":                               "display_name: !!python/object:os.system x\n",
		"a verbatim tag":                                "display_name: !<tag:yaml.org,2002:str> x\n",
		"a tag from a directive":                        "%TAG !e! tag:example.com,2000:\n---\ndisplay_name: !e!foo x\n",
		"a sequence as a key":                           "? [a, b]\n: c\n",
		"a mapping as a key":                            "? {a: b}\n: c\n",
	}
	for name, fragment := range cases {
		t.Run(name, func(t *testing.T) {
			manifest := fixtureManifest(numberedSpecs(2)) + manifestTail
			store := newManifestStore(t, manifest)

			result := mergeYAMLAt(t, store, fragment, "")

			if result.Status != tool.StatusError {
				t.Fatalf("status = %q %q, want a refusal", result.Status, result.Message)
			}
			if store.latest(t) != manifest || store.versions(t) != 1 {
				t.Errorf("the manifest changed (versions = %d)", store.versions(t))
			}
		})
	}
}

func TestManifestMerge_ATaggedKeyNextToTheRealOnesCannotOverwriteThem(t *testing.T) {
	manifest := "name: mesh\ncomponents:\n  - {name: A, type: agent, filename: a.yaml, id: " + realID + ", deployed_version: 3, status: draft}\n"
	store := newManifestStore(t, manifest)

	result := mergeYAMLAt(t, store, "components:\n  - {name: A, status: validated, "+binaryID+": "+forgedID+", "+binaryDeploy+": 42}\n", "")

	if result.Status != tool.StatusError {
		t.Fatalf("status = %q %q, want a refusal", result.Status, result.Message)
	}
	var stored struct {
		Components []map[string]any `yaml:"components"`
	}
	if err := yaml.Unmarshal([]byte(store.latest(t)), &stored); err != nil {
		t.Fatal(err)
	}
	if stored.Components[0]["id"] != realID || stored.Components[0]["deployed_version"] != 3 {
		t.Errorf("the component's own id and deployed_version changed: %v", stored.Components[0])
	}
}

func TestManifestMerge_AStoredMergeKeyIsNotWrittenBackByTheStructuredTool(t *testing.T) {
	manifest := "name: mesh\ncomponents:\n  - {name: B, type: agent, filename: b.yaml, " + binaryMerge + ": {id: " + forgedID + "}}\n"
	store := newManifestStore(t, manifest)

	result := mergeStructuredAt(t, store, manifestFilename, map[string]any{"display_name": "X"}, "")

	if result.Status != tool.StatusError || !strings.Contains(result.Message, "the stored manifest holds a << key") {
		t.Fatalf("result = %q %q, want a refusal", result.Status, result.Message)
	}
	if store.latest(t) != manifest || store.versions(t) != 1 {
		t.Errorf("the manifest changed (versions = %d)", store.versions(t))
	}
}

func TestManifestMerge_TheTaggedKeyChainCannotForgeAnIDInEitherOrder(t *testing.T) {
	tagged := "components:\n  - {name: B, type: agent, filename: b.yaml, " + binaryMerge + ": {id: " + forgedID + ", deployed_version: 9}}\n"
	assertNoForgery := func(t *testing.T, store *manifestStore) {
		t.Helper()
		if len(parsedManifestComponents(t, store.latest(t))) != 1 {
			t.Errorf("the stored manifest lost its component:\n%s", store.latest(t))
		}
		if strings.Contains(store.latest(t), forgedID) {
			t.Errorf("the forged id reached the stored manifest:\n%s", store.latest(t))
		}
	}
	t.Run("yaml first, then structured", func(t *testing.T) {
		store := newManifestStore(t, fixtureManifest(numberedSpecs(1)))
		first := mergeYAMLAt(t, store, tagged, "")
		if first.Status != tool.StatusError {
			t.Fatalf("the tagged fragment was accepted: %q", first.Message)
		}
		second := mergeStructuredAt(t, store, manifestFilename, map[string]any{"risks": []any{"two"}}, "")
		if second.Status == tool.StatusError {
			t.Fatalf("structured merge refused: %s", second.Message)
		}
		assertNoForgery(t, store)
	})
	t.Run("structured first, then yaml", func(t *testing.T) {
		store := newManifestStore(t, fixtureManifest(numberedSpecs(1)))
		first := mergeStructuredAt(t, store, manifestFilename, map[string]any{"risks": []any{"two"}}, "")
		if first.Status == tool.StatusError {
			t.Fatalf("structured merge refused: %s", first.Message)
		}
		second := mergeYAMLAt(t, store, tagged, "")
		if second.Status != tool.StatusError {
			t.Fatalf("the tagged fragment was accepted: %q", second.Message)
		}
		assertNoForgery(t, store)
	})
}

func TestManifestMerge_ABlankTypeIsRefused(t *testing.T) {
	cases := map[string]string{
		"on a typeless stored component": "components:\n  - {name: Legacy, type: \"\", status: validated}\n",
		"on a new entry":                 "components:\n  - {name: N, type: \"\", filename: n.yaml}\n",
		"made of spaces":                 "components:\n  - {name: Legacy, type: \"  \", status: validated}\n",
	}
	for _, merger := range manifestMergers() {
		for name, fragment := range cases {
			t.Run(merger.name+"/"+name, func(t *testing.T) {
				manifest := "name: mesh\ncomponents:\n  - {name: Legacy, filename: l.yaml, status: draft}\n"
				store := newManifestStore(t, manifest)

				result := merger.apply(t, store.toolCtx, manifestFilename, fragment)

				if result.Status != tool.StatusError || !strings.Contains(result.Message, "type cannot be blank") {
					t.Fatalf("result = %q %q", result.Status, result.Message)
				}
				if store.latest(t) != manifest {
					t.Error("the manifest changed")
				}
			})
		}
	}
}

func TestManifestLocks_AreKeptPerManifest(t *testing.T) {
	first, _ := newDataToolContext(t)
	second, _ := newDataToolContext(t)
	second.SessionID = "another-session"
	releaseFirst := holdManifest(t, first)
	defer releaseFirst()

	acquired := make(chan struct{})
	go func() {
		defer close(acquired)
		holdManifest(t, second)()
	}()

	select {
	case <-acquired:
	case <-time.After(2 * time.Second):
		t.Fatal("a manifest of another session waited for this one's lock")
	}
}

func TestManifestMerge_TheMergeToolsDescribeTheManifestRules(t *testing.T) {
	yamlDescription := (&YAMLMergeArtifactTool{}).Description()
	for _, phrase := range []string{"merged by component name and type", "a path with a dot (components.0)", "explicit tag", "<< key", "second YAML document", "comments in the fragment are dropped", "null full_validation_passed is removed"} {
		if !strings.Contains(yamlDescription, phrase) {
			t.Errorf("yaml_merge_artifact's Description lost %q", phrase)
		}
	}
	structuredDescription := (&MergeStructuredDataTool{}).Description()
	for _, phrase := range []string{"merged by component name and type", "a path with a dot (components.0)", "numeric :N version suffix", "<< key", "null full_validation_passed is removed"} {
		if !strings.Contains(structuredDescription, phrase) {
			t.Errorf("merge_structured_data's Description lost %q", phrase)
		}
	}
	found := false
	for name := range (&YAMLMergeArtifactTool{}).ResultSchema().Properties {
		found = found || name == "merge_report"
	}
	if !found {
		t.Error("yaml_merge_artifact's result schema lost merge_report")
	}
}

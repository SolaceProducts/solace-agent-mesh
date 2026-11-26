// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

const builderPromptPath = "../../internal/settings/files/builder_agent.yaml"

func builderPromptManifestExample(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(builderPromptPath)
	if err != nil {
		t.Fatal(err)
	}
	prompt := string(raw)
	_, afterHeading, found := strings.Cut(prompt, "### Manifest schema")
	if !found {
		t.Fatal("the Builder prompt has no '### Manifest schema' section")
	}
	_, afterFence, found := strings.Cut(afterHeading, "```yaml\n")
	if !found {
		t.Fatal("the manifest schema section has no yaml example")
	}
	example, _, found := strings.Cut(afterFence, "\n        ```")
	if !found {
		t.Fatal("the manifest schema example is not closed")
	}
	var lines []string
	for _, line := range strings.Split(example, "\n") {
		lines = append(lines, strings.TrimPrefix(line, "        "))
	}
	return strings.Join(lines, "\n")
}

func parsedManifestComponents(t *testing.T, manifest string) []any {
	t.Helper()
	var root map[string]any
	if err := yaml.Unmarshal([]byte(manifest), &root); err != nil {
		t.Fatalf("the stored manifest does not parse: %v\n%s", err, manifest)
	}
	components, _ := root["components"].([]any)
	return components
}

func TestManifestMerge_ThePromptsOwnManifestExampleRoundTrips(t *testing.T) {
	example := builderPromptManifestExample(t)
	wantComponents := len(parsedManifestComponents(t, example))
	if wantComponents < 2 {
		t.Fatalf("the prompt's manifest example holds %d components", wantComponents)
	}
	bases := map[string]string{
		"an empty flow list":  "name: my-system\ncomponents: []\n",
		"no components key":   "name: my-system\n",
		"a block list of one": "name: my-system\ncomponents:\n  - {name: Existing, type: agent, filename: e.yaml, status: draft}\n",
		"a flow list of one":  "name: my-system\ncomponents: [{name: Existing, type: agent, filename: e.yaml, status: draft}]\n",
		"the example itself":  example,
	}
	for _, merger := range manifestMergers() {
		for name, base := range bases {
			t.Run(merger.name+"/"+name, func(t *testing.T) {
				store := newManifestStore(t, base)
				before := len(parsedManifestComponents(t, base))
				if base == example {
					before = 0
				}

				result := merger.apply(t, store.toolCtx, manifestFilename, example)

				if result.Status == tool.StatusError {
					t.Fatalf("merge refused: %s", result.Message)
				}
				after := parsedManifestComponents(t, store.latest(t))
				if len(after) != before+wantComponents {
					t.Errorf("components = %d, want %d", len(after), before+wantComponents)
				}
			})
		}
	}
}

func TestManifestMerge_EachComponentOfThePromptsExampleCanBeMergedAlone(t *testing.T) {
	var root struct {
		Components []yaml.Node `yaml:"components"`
	}
	if err := yaml.Unmarshal([]byte(builderPromptManifestExample(t)), &root); err != nil {
		t.Fatal(err)
	}
	for _, merger := range manifestMergers() {
		for i := range root.Components {
			t.Run(merger.name, func(t *testing.T) {
				entry, err := yaml.Marshal(&root.Components[i])
				if err != nil {
					t.Fatal(err)
				}
				fragment := "components:\n" + indentAsListItem(string(entry))
				store := newManifestStore(t, "name: my-system\ncomponents: []\n")

				result := merger.apply(t, store.toolCtx, manifestFilename, fragment)

				if result.Status == tool.StatusError {
					t.Fatalf("merge refused: %s\n%s", result.Message, fragment)
				}
				if got := len(parsedManifestComponents(t, store.latest(t))); got != 1 {
					t.Errorf("components = %d, want 1", got)
				}
			})
		}
	}
}

func indentAsListItem(block string) string {
	lines := strings.Split(strings.TrimRight(block, "\n"), "\n")
	for i, line := range lines {
		prefix := "    "
		if i == 0 {
			prefix = "  - "
		}
		lines[i] = prefix + line
	}
	return strings.Join(lines, "\n") + "\n"
}

func TestManifestMerge_ACommentIsNeverEmittedInsideAFlowCollection(t *testing.T) {
	const commented = "    tools:            # notable tools\n      - web_request\n"
	cases := map[string]struct{ manifest, fragment string }{
		"a new entry into an empty flow list": {
			"name: my-system\ncomponents: []\n",
			"components:\n  - filename: a.yaml\n    type: agent\n    name: A\n    status: draft\n" + commented,
		},
		"a new entry into a flow list of flow entries": {
			"name: my-system\ncomponents: [{filename: b.yaml, type: agent, name: B, status: draft}]\n",
			"components:\n  - filename: a.yaml\n    type: agent\n    name: A\n    status: draft\n" + commented,
		},
		"an update of a flow entry in a block list": {
			"name: my-system\ncomponents:\n  - {filename: b.yaml, type: agent, name: B, status: draft}\n",
			"components:\n  - name: B\n    type: agent\n" + commented,
		},
		"an update inside a nested flow map of a flow entry": {
			"name: my-system\ncomponents:\n  - {filename: b.yaml, type: agent, name: B, status: draft, config: {k: v}}\n",
			"components:\n  - name: B\n    type: agent\n    config:\n      steps:   # in order\n        - web_request\n",
		},
		"an update of a flow entry in a flow list": {
			"name: my-system\ncomponents: [{filename: b.yaml, type: agent, name: B, status: draft}]\n",
			"components:\n  - name: B\n    type: agent\n" + commented,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			store := newManifestStore(t, tc.manifest)

			result := mergeYAMLAt(t, store, tc.fragment, "")

			if result.Status == tool.StatusError {
				t.Fatalf("merge refused: %s", result.Message)
			}
			if got := len(parsedManifestComponents(t, store.latest(t))); got < 1 {
				t.Errorf("components = %d", got)
			}
			if tools := store.latest(t); !strings.Contains(tools, "web_request") {
				t.Errorf("the tools list was lost:\n%s", tools)
			}
		})
	}
}

func TestManifestMerge_ACommentInAFragmentIsDropped(t *testing.T) {
	cases := map[string]struct{ manifest, fragment, path string }{
		"into a flow map under a top-level key": {
			"name: my-system\narchitecture: {note: a}\ncomponents:\n  - {name: A, type: agent, filename: a.yaml}\n",
			"architecture:\n  steps:   # in order\n    - one\n", "",
		},
		"into a manifest whose root is a flow map": {
			"{name: my-system, components: [{name: A, type: agent, filename: a.yaml, tools: [x]}]}\n",
			"components:\n  - name: A\n    type: agent\n    status: validated   # validated | draft\n    tools:   # notable tools\n      - web_request\n", "",
		},
		"at the components path of a flow list": {
			"name: my-system\ncomponents: [{name: A, type: agent, filename: a.yaml, tools: [x], config: {}}]\n",
			"- name: A\n  type: agent\n  status: validated   # validated | draft\n", "components",
		},
		"head and foot comments on the keys of a new entry": {
			"name: my-system\ncomponents: []\n",
			"components:\n  - filename: a.yaml\n    # which agent\n    type: agent\n    name: A\n    status: draft\n    # the last word\n", "",
		},
		"a head and a foot comment": {
			"name: my-system\ncomponents: [{name: A, type: agent, filename: a.yaml, config: {}}]\n",
			"# about the update\ncomponents:\n  - name: A\n    status: validated\n# end\n", "",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			store := newManifestStore(t, tc.manifest)

			result := mergeYAMLAt(t, store, tc.fragment, tc.path)

			if result.Status == tool.StatusError {
				t.Fatalf("merge refused: %s", result.Message)
			}
			parseManifestMap(t, store.latest(t))
			for _, comment := range []string{"in order", "validated | draft", "notable tools", "about the update", "end", "which agent", "the last word"} {
				if strings.Contains(store.latest(t), "# "+comment) {
					t.Errorf("the comment %q was kept:\n%s", comment, store.latest(t))
				}
			}
		})
	}
}

func TestManifestMerge_TheStructuredToolRefusesAResultThatWouldNotParse(t *testing.T) {
	const manifest = "name: mesh\ncomponents:\n  - name: A\n    type: agent\n    filename: a.yaml\n    description: \"\\nFetches mail\\nand files it\"\n"
	store := newManifestStore(t, manifest)

	result := mergeStructuredAt(t, store, manifestFilename, map[string]any{"display_name": "X"}, "")

	if result.Status != tool.StatusError || !strings.Contains(result.Message, "would not parse as YAML") || !strings.Contains(result.Message, "the manifest was not changed") {
		t.Fatalf("result = %q %q, want the unparseable-result refusal", result.Status, result.Message)
	}
	if store.latest(t) != manifest || store.versions(t) != 1 {
		t.Errorf("the manifest changed (versions = %d)", store.versions(t))
	}
}

func TestManifestMerge_AStoredManifestThatDoesNotParseIsNamedAsTheCause(t *testing.T) {
	const manifest = "name: mesh\ncomponents:\n  - {name: A, type: agent, filename: a.yaml, status: draft, status: error}\n"
	store := newManifestStore(t, manifest)

	result := mergeYAMLAt(t, store, "components:\n  - {name: A, type: agent, status: validated}\n", "")

	if result.Status != tool.StatusError || !strings.Contains(result.Message, "the stored manifest does not parse as YAML") {
		t.Fatalf("result = %q %q, want the stored manifest named", result.Status, result.Message)
	}
	if store.latest(t) != manifest {
		t.Error("the manifest changed")
	}
}

func TestManifestMerge_OtherArtifactsAreNotParsedBack(t *testing.T) {
	const stored = "name: agent\nitems: [{a: 1, a: 2}]\n"
	toolCtx, facade := newDataToolContext(t)
	saveDataToolArtifact(t, context.Background(), facade, "other.yaml", []byte(stored), SAMAgentConfigMIMEType)

	result, err := (&YAMLMergeArtifactTool{}).Execute(context.Background(), map[string]any{"artifact_name": "other.yaml", "yaml_fragment": "name: renamed\n"}, toolCtx)
	result = resultOrError(t, "yaml_merge_artifact", result, err)

	if result.Status == tool.StatusError {
		t.Fatalf("merge refused: %s", result.Message)
	}
}

func TestManifestMerge_OtherArtifactsAreNotParsedBackByTheStructuredTool(t *testing.T) {
	const stored = "name: agent\nitems:\n  - description: \"\\nFetches mail\\nand files it\"\n"
	toolCtx, facade := newDataToolContext(t)
	saveDataToolArtifact(t, context.Background(), facade, "other.yaml", []byte(stored), SAMAgentConfigMIMEType)

	result, err := executeWithinDeadline(&MergeStructuredDataTool{}, map[string]any{"input_filename": "other.yaml", "patch": map[string]any{"name": "renamed"}}, toolCtx)
	result = resultOrError(t, "merge_structured_data", result, err)

	if result.Status == tool.StatusError {
		t.Fatalf("merge refused: %s", result.Message)
	}
}

func TestManifestMerge_AnEmptyFlowListBecomesABlockList(t *testing.T) {
	store := newManifestStore(t, "name: my-system\ncomponents: []\n")

	result := mergeYAMLAt(t, store, "components:\n  - {filename: a.yaml, type: agent, name: A, status: draft}\n", "")

	if result.Status == tool.StatusError {
		t.Fatalf("merge refused: %s", result.Message)
	}
	if !strings.Contains(store.latest(t), "components:\n  - ") {
		t.Errorf("the list was left in flow style:\n%s", store.latest(t))
	}
}

func TestManifestBytesProblem(t *testing.T) {
	const good = "name: a\ncomponents:\n  - {name: A}\n"
	const broken = "components: [{name: A, tools:, # c\n[x]}]\n"
	if err := manifestBytesProblem([]byte(good), []byte(good)); err != nil {
		t.Errorf("readable manifest refused: %v", err)
	}
	err := manifestBytesProblem([]byte(good), []byte(broken))
	if err == nil || !strings.Contains(err.Error(), "the merged manifest would not parse as YAML") || !strings.Contains(err.Error(), "the manifest was not changed") {
		t.Errorf("broken merge: err = %v", err)
	}
	err = manifestBytesProblem([]byte(broken), []byte(broken))
	if err == nil || !strings.Contains(err.Error(), "the stored manifest does not parse as YAML") {
		t.Errorf("broken stored manifest: err = %v", err)
	}
}

func TestManifestMerge_KeysThatAreNotTextAreRefused(t *testing.T) {
	cases := map[string]string{
		"an integer key at the root":    "1: x\n",
		"a negative integer key":        "-1: x\n",
		"a float key":                   "1.5: x\n",
		"an exponent key":               "1e3: x\n",
		"a hex key":                     "0x1F: x\n",
		"true as a key":                 "true: x\n",
		"True as a key":                 "True: x\n",
		"null as a key":                 "null: x\n",
		"a tilde key":                   "~: x\n",
		"an empty key":                  "? \n: x\n",
		"a date key":                    "2024-01-02: x\n",
		"an infinity key":               ".inf: x\n",
		"an integer key nested":         "architecture:\n  1: a\n  note: b\n",
		"an integer key inside entry":   "components:\n  - {name: Comp0, status: validated, 1: x}\n",
		"an integer key in a new entry": "components:\n  - {name: Late, type: agent, filename: l.yaml, 1: x}\n",
	}
	for name, fragment := range cases {
		t.Run(name, func(t *testing.T) {
			manifest := fixtureManifest(numberedSpecs(2)) + manifestTail
			store := newManifestStore(t, manifest)

			result := mergeYAMLAt(t, store, fragment, "")

			if result.Status != tool.StatusError {
				t.Fatalf("status = %q %q, want a refusal", result.Status, result.Message)
			}
			if !strings.Contains(result.Message, "must be text") || !strings.Contains(result.Message, `the key "`) {
				t.Errorf("message = %q, want it to name the key and say it must be text", result.Message)
			}
			if store.latest(t) != manifest || store.versions(t) != 1 {
				t.Errorf("the manifest changed (versions = %d)", store.versions(t))
			}
		})
	}
}

func TestManifestMerge_AQuotedNumberIsAnAcceptableKey(t *testing.T) {
	store := newManifestStore(t, fixtureManifest(numberedSpecs(2))+manifestTail)

	result := mergeYAMLAt(t, store, "architecture:\n  \"1\": a\n  'true': b\n", "")

	if result.Status == tool.StatusError {
		t.Fatalf("merge refused: %s", result.Message)
	}
}

func parseManifestMap(t *testing.T, manifest string) map[string]any {
	t.Helper()
	var root map[string]any
	if err := yaml.Unmarshal([]byte(manifest), &root); err != nil {
		t.Fatalf("does not parse: %v\n%s", err, manifest)
	}
	return root
}

func componentsByName(t *testing.T, root map[string]any) map[string]map[string]any {
	t.Helper()
	out := map[string]map[string]any{}
	comps, _ := root["components"].([]any)
	for _, c := range comps {
		m, _ := c.(map[string]any)
		out[fmt.Sprint(m["name"])] = m
	}
	return out
}

func TestManifestMerge_ThePromptsExampleRoundTripsWithItsDataAndConverges(t *testing.T) {
	example := builderPromptManifestExample(t)
	want := parseManifestMap(t, example)
	wantComps := componentsByName(t, want)
	bases := map[string]string{
		"emptyflow": "name: my-system\ncomponents: []\n",
		"none":      "name: my-system\n",
		"flowlist":  "name: my-system\ncomponents: [{name: Existing, type: agent, filename: e.yaml, status: draft}]\n",
		"blocklist": "name: my-system\ncomponents:\n  - {name: Existing, type: agent, filename: e.yaml, status: draft}\n",
		"example":   example,
	}
	for _, merger := range manifestMergers() {
		for bn, base := range bases {
			t.Run(merger.name+"/"+bn, func(t *testing.T) {
				store := newManifestStore(t, base)
				res := merger.apply(t, store.toolCtx, manifestFilename, example)
				if res.Status == tool.StatusError {
					t.Fatalf("refused: %s", res.Message)
				}
				first := store.latest(t)
				got := parseManifestMap(t, first)
				gotComps := componentsByName(t, got)
				for name, wc := range wantComps {
					gc, ok := gotComps[name]
					if !ok {
						t.Errorf("component %q missing", name)
						continue
					}
					for k, v := range wc {
						if k == "id" || k == "deployed_version" {
							continue
						}
						if !reflect.DeepEqual(gc[k], v) {
							t.Errorf("component %q field %q = %#v want %#v", name, k, gc[k], v)
						}
					}
				}
				for k, v := range want {
					if k == "components" {
						continue
					}
					if !reflect.DeepEqual(got[k], v) {
						t.Errorf("top-level %q = %#v want %#v", k, got[k], v)
					}
				}
				if _, ok := gotComps["Existing"]; bn != "example" && bn != "none" && bn != "emptyflow" && !ok {
					t.Errorf("Existing lost")
				}
				res2 := merger.apply(t, store.toolCtx, manifestFilename, example)
				if res2.Status == tool.StatusError {
					t.Fatalf("re-apply refused: %s", res2.Message)
				}
				if store.latest(t) != first {
					t.Errorf("re-apply not byte-identical (%d vs %d bytes)", len(first), len(store.latest(t)))
				}
			})
		}
	}
}

func builderPromptStatusFragment(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(builderPromptPath)
	if err != nil {
		t.Fatal(err)
	}
	_, after, found := strings.Cut(string(raw), "Changing one component's status is a targeted merge")
	if !found {
		t.Fatal("the prompt has no status-merge bullet")
	}
	_, afterFence, found := strings.Cut(after, "```\n")
	if !found {
		t.Fatal("no fenced fragment under the status bullet")
	}
	body, _, found := strings.Cut(afterFence, "\n          ```")
	if !found {
		t.Fatal("fragment not closed")
	}
	var lines []string
	for _, l := range strings.Split(body, "\n") {
		lines = append(lines, strings.TrimPrefix(l, "          "))
	}
	return strings.Join(lines, "\n") + "\n"
}

func TestManifestMerge_ThePromptsStatusFragmentUpdatesTheExampleComponent(t *testing.T) {
	example := builderPromptManifestExample(t)
	fragment := builderPromptStatusFragment(t)
	for _, merger := range manifestMergers() {
		t.Run(merger.name, func(t *testing.T) {
			store := newManifestStore(t, example)
			res := merger.apply(t, store.toolCtx, manifestFilename, fragment)
			if res.Status == tool.StatusError {
				t.Fatalf("refused: %s\n%s", res.Message, fragment)
			}
			if !strings.Contains(res.Message, "MyAgent") {
				t.Errorf("report does not name MyAgent: %s", res.Message)
			}
			got := componentsByName(t, parseManifestMap(t, store.latest(t)))
			if got["MyAgent"]["status"] != "validated" {
				t.Errorf("status = %v", got["MyAgent"]["status"])
			}
			if len(got) != len(componentsByName(t, parseManifestMap(t, example))) {
				t.Errorf("component count changed")
			}
			if strings.Contains(res.Message, "Not written") {
				t.Errorf("the sample fragment writes a UI-owned field: %s", res.Message)
			}
		})
	}
}

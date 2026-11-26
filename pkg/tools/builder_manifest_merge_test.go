// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact/memory"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

type manifestMerger struct {
	name  string
	apply func(t *testing.T, toolCtx *tool.Context, artifactName, fragment string) *tool.Result
}

func manifestMergers() []manifestMerger {
	return []manifestMerger{
		{name: "yaml_merge_artifact", apply: func(t *testing.T, toolCtx *tool.Context, artifactName, fragment string) *tool.Result {
			t.Helper()
			result, err := executeWithinDeadline(&YAMLMergeArtifactTool{}, map[string]any{
				"artifact_name": artifactName,
				"yaml_fragment": fragment,
			}, toolCtx)
			return resultOrError(t, "yaml_merge_artifact", result, err)
		}},
		{name: "merge_structured_data", apply: func(t *testing.T, toolCtx *tool.Context, artifactName, fragment string) *tool.Result {
			t.Helper()
			patch, err := jsonDecodedPatch(fragment)
			if err != nil {
				return resultOrError(t, "merge_structured_data", nil, err)
			}
			result, err := executeWithinDeadline(&MergeStructuredDataTool{}, map[string]any{
				"input_filename": artifactName,
				"patch":          patch,
			}, toolCtx)
			return resultOrError(t, "merge_structured_data", result, err)
		}},
	}
}

func executeWithinDeadline(t interface {
	Execute(context.Context, map[string]any, *tool.Context) (*tool.Result, error)
}, args map[string]any, toolCtx *tool.Context) (*tool.Result, error) {
	type outcome struct {
		result *tool.Result
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		result, err := t.Execute(context.Background(), args, toolCtx)
		done <- outcome{result, err}
	}()
	select {
	case got := <-done:
		return got.result, got.err
	case <-time.After(toolCallDeadline):
		return nil, errors.New("the tool call did not finish: a manifest lock was probably never released")
	}
}

const toolCallDeadline = 30 * time.Second

func resultOrError(t *testing.T, toolName string, result *tool.Result, err error) *tool.Result {
	t.Helper()
	if err != nil {
		t.Errorf("%s failed outside the tool contract: %v", toolName, err)
		return tool.Error(err.Error())
	}
	return result
}

func jsonDecodedPatch(fragment string) (map[string]any, error) {
	var generic any
	if err := yaml.Unmarshal([]byte(fragment), &generic); err != nil {
		return nil, fmt.Errorf("fragment is not YAML: %w", err)
	}
	encoded, err := json.Marshal(generic)
	if err != nil {
		return nil, fmt.Errorf("fragment is not JSON-representable: %w", err)
	}
	var patch map[string]any
	if err := json.Unmarshal(encoded, &patch); err != nil {
		return nil, fmt.Errorf("fragment is not an object: %w", err)
	}
	return patch, nil
}

type manifestComponentSpec struct {
	name string
	kind string
}

func fixtureComponent(i int, spec manifestComponentSpec) string {
	component := fmt.Sprintf(`  - filename: %[1]s.yaml
    type: %[2]s
    name: %[1]s
    description: >-
      Does the work of %[1]s.
    status: draft
    origin: session
    id: 01a14000-0000-7000-8000-%012[3]d
    deployed_version: %[4]d
    depends_on: [Dep%[3]d]
    connectors: [conn_%[3]d]
    tools:
      - web_request
      - artifact_grep
`, spec.name, spec.kind, i, i+1)
	if spec.kind == "workflow" {
		component += fmt.Sprintf(`    nodes:
      - id: step1
        type: agent
        agent_name: %[1]s
      - id: step2
        type: agent
        agent_name: %[1]s
        depends_on: [step1]
`, spec.name)
	}
	return component
}

func fixtureManifest(specs []manifestComponentSpec) string {
	var b strings.Builder
	b.WriteString("name: support-mesh\ndisplay_name: Support Mesh\nrisks:\n  - Writes to production\ncomponents:\n")
	for i, spec := range specs {
		b.WriteString(fixtureComponent(i, spec))
	}
	return b.String()
}

func numberedSpecs(n int) []manifestComponentSpec {
	kinds := []string{"agent", "workflow", "connector"}
	specs := make([]manifestComponentSpec, n)
	for i := range specs {
		specs[i] = manifestComponentSpec{name: fmt.Sprintf("Comp%d", i), kind: kinds[i%len(kinds)]}
	}
	return specs
}

type manifestStore struct {
	ctx     context.Context
	toolCtx *tool.Context
	facade  *tool.Facade
}

func newManifestStore(t *testing.T, content string) *manifestStore {
	t.Helper()
	toolCtx, _ := newDataToolContext(t)
	toolCtx.SessionID = "manifest-" + t.Name()
	facade := toolCtx.Facade()
	store := &manifestStore{ctx: context.Background(), toolCtx: toolCtx, facade: facade}
	if content != "" {
		saveDataToolArtifact(t, store.ctx, facade, manifestFilename, []byte(content), SAMBuildManifestMIMEType)
	}
	return store
}

func (s *manifestStore) latest(t *testing.T) string {
	t.Helper()
	content, _, err := s.facade.LoadArtifact(s.ctx, manifestFilename, nil)
	if err != nil {
		t.Fatalf("load manifest: %v", err)
	}
	return string(content)
}

func (s *manifestStore) versions(t *testing.T) int {
	t.Helper()
	versions, err := s.facade.ListArtifactVersions(s.ctx, manifestFilename)
	if err != nil {
		t.Fatalf("list manifest versions: %v", err)
	}
	return len(versions)
}

func (s *manifestStore) mimeType(t *testing.T) string {
	t.Helper()
	_, mimeType, err := s.facade.LoadArtifact(s.ctx, manifestFilename, nil)
	if err != nil {
		t.Fatalf("load manifest: %v", err)
	}
	return mimeType
}

func decodeManifestComponents(t *testing.T, manifestYAML string) []map[string]any {
	t.Helper()
	var doc struct {
		Components []map[string]any `yaml:"components"`
	}
	if err := yaml.Unmarshal([]byte(manifestYAML), &doc); err != nil {
		t.Fatalf("manifest does not parse with a components list: %v\n%s", err, manifestYAML)
	}
	return doc.Components
}

func withStatus(component map[string]any, status any) map[string]any {
	clone := maps.Clone(component)
	clone["status"] = status
	return clone
}

func assertComponentsAfterStatusUpdate(t *testing.T, before, after []map[string]any, updated map[string]string) {
	t.Helper()
	if len(after) != len(before) {
		t.Fatalf("component count = %d, want %d", len(after), len(before))
	}
	for i := range before {
		want := before[i]
		if status, ok := updated[before[i]["name"].(string)]; ok {
			want = withStatus(before[i], status)
		}
		if !reflect.DeepEqual(after[i], want) {
			t.Errorf("components[%d] (%v) = %v, want %v", i, before[i]["name"], after[i], want)
		}
	}
}

func statusFragment(names []string, shape string) string {
	var b strings.Builder
	b.WriteString("components:\n")
	for _, name := range names {
		switch shape {
		case "name and status":
			fmt.Fprintf(&b, "  - name: %s\n    status: validated\n", name)
		case "name type and status":
			fmt.Fprintf(&b, "  - name: %s\n    type: %s\n    status: validated\n", name, kindOf(name))
		case "issue stub with tools":
			fmt.Fprintf(&b, "  - name: %s\n    status: validated\n    tools:\n      - web_request\n      - artifact_grep\n", name)
		}
	}
	return b.String()
}

func kindOf(name string) string {
	var i int
	_, _ = fmt.Sscanf(name, "Comp%d", &i)
	return []string{"agent", "workflow", "connector"}[i%3]
}

func TestManifestMerge_StatusUpdateKeepsEveryComponentAndField(t *testing.T) {
	selections := map[string]func(n int) []string{
		"the last one":  func(n int) []string { return []string{fmt.Sprintf("Comp%d", n-1)} },
		"the first one": func(int) []string { return []string{"Comp0"} },
		"every one": func(n int) []string {
			names := make([]string, n)
			for i := range names {
				names[i] = fmt.Sprintf("Comp%d", i)
			}
			return names
		},
	}
	shapes := []string{"name and status", "name type and status", "issue stub with tools"}

	for _, merger := range manifestMergers() {
		for _, n := range []int{1, 2, 3, 9} {
			for selection, pick := range selections {
				for _, shape := range shapes {
					name := fmt.Sprintf("%s/%d components/status for %s/%s", merger.name, n, selection, shape)
					t.Run(name, func(t *testing.T) {
						store := newManifestStore(t, fixtureManifest(numberedSpecs(n)))
						before := decodeManifestComponents(t, store.latest(t))
						targets := pick(n)

						result := merger.apply(t, store.toolCtx, manifestFilename, statusFragment(targets, shape))
						if result.Status == tool.StatusError {
							t.Fatalf("merge refused: %s", result.Message)
						}

						updated := map[string]string{}
						for _, target := range targets {
							updated[target] = "validated"
						}
						assertComponentsAfterStatusUpdate(t, before, decodeManifestComponents(t, store.latest(t)), updated)
						if got := store.mimeType(t); got != SAMBuildManifestMIMEType {
							t.Errorf("manifest MIME type = %q, want %q", got, SAMBuildManifestMIMEType)
						}
					})
				}
			}
		}
	}
}

func TestManifestMerge_UIOwnedFieldsAreNeverWritten(t *testing.T) {
	cases := map[string]string{
		"a junk id":               "id: not-a-uuid\n    deployed_version: 7",
		"an empty id":             "id: \"\"",
		"only a deployed_version": "deployed_version: 99",
	}
	for _, merger := range manifestMergers() {
		for name, field := range cases {
			t.Run(merger.name+"/"+name, func(t *testing.T) {
				store := newManifestStore(t, fixtureManifest(numberedSpecs(3)))
				before := decodeManifestComponents(t, store.latest(t))

				result := merger.apply(t, store.toolCtx, manifestFilename, "components:\n  - name: Comp1\n    status: validated\n    "+field+"\n")

				if result.Status == tool.StatusError {
					t.Fatalf("merge refused: %s", result.Message)
				}
				assertComponentsAfterStatusUpdate(t, before, decodeManifestComponents(t, store.latest(t)), map[string]string{"Comp1": "validated"})
				if !strings.Contains(result.Message, "Not written, the Builder UI owns them") || !strings.Contains(result.Message, ` on "Comp1"`) {
					t.Errorf("message = %q, want it to say which UI-owned fields were not written", result.Message)
				}
			})
		}
	}
}

func TestManifestMerge_ResultSaysWhatChanged(t *testing.T) {
	for _, merger := range manifestMergers() {
		t.Run(merger.name, func(t *testing.T) {
			store := newManifestStore(t, fixtureManifest(numberedSpecs(2)))

			result := merger.apply(t, store.toolCtx, manifestFilename, "components:\n  - {name: Comp0, status: validated, description: new}\n  - {name: Late, type: agent, filename: late.yaml}\n")

			if !strings.Contains(result.Message, "Updated: \"Comp0\" (") || !strings.Contains(result.Message, "status") || !strings.Contains(result.Message, "description") || !strings.Contains(result.Message, `Added: "Late"`) {
				t.Errorf("message = %q, want the updated component with its fields and the added one", result.Message)
			}
		})
	}
}

func TestManifestMerge_ListFieldsAreReplacedWhole(t *testing.T) {
	for _, merger := range manifestMergers() {
		t.Run(merger.name, func(t *testing.T) {
			store := newManifestStore(t, fixtureManifest(numberedSpecs(3)))

			result := merger.apply(t, store.toolCtx, manifestFilename, `components:
  - name: Comp1
    depends_on: [Other]
    tools: [only_tool]
    nodes:
      - id: lone
        type: agent
        agent_name: Comp0
`)
			if result.Status == tool.StatusError {
				t.Fatalf("merge refused: %s", result.Message)
			}

			components := decodeManifestComponents(t, store.latest(t))
			if len(components) != 3 {
				t.Fatalf("components = %d, want 3", len(components))
			}
			workflow := components[1]
			if !reflect.DeepEqual(workflow["depends_on"], []any{"Other"}) || !reflect.DeepEqual(workflow["tools"], []any{"only_tool"}) {
				t.Errorf("list fields = %v / %v, want them replaced whole", workflow["depends_on"], workflow["tools"])
			}
			if nodes, _ := workflow["nodes"].([]any); len(nodes) != 1 {
				t.Errorf("nodes = %v, want the one node given", workflow["nodes"])
			}
			if workflow["filename"] != "Comp1.yaml" {
				t.Errorf("filename = %v, want it kept", workflow["filename"])
			}
		})
	}
}

func TestManifestMerge_AlreadyRunningComponentTakesAnEmptyFilename(t *testing.T) {
	for _, merger := range manifestMergers() {
		t.Run(merger.name, func(t *testing.T) {
			store := newManifestStore(t, fixtureManifest(numberedSpecs(1)))

			result := merger.apply(t, store.toolCtx, manifestFilename, "components:\n  - {name: Existing, type: agent, filename: \"\", status: deployed, origin: platform}\n")
			if result.Status == tool.StatusError {
				t.Fatalf("merge refused: %s", result.Message)
			}

			components := decodeManifestComponents(t, store.latest(t))
			if len(components) != 2 || components[1]["filename"] != "" || components[1]["origin"] != "platform" {
				t.Errorf("components = %v, want the platform component appended with an empty filename", components)
			}
		})
	}
}

func TestManifestMerge_RootFragmentMustBeAMapping(t *testing.T) {
	manifest := fixtureManifest(numberedSpecs(2))
	store := newManifestStore(t, manifest)

	result, err := (&YAMLMergeArtifactTool{}).Execute(context.Background(), map[string]any{
		"artifact_name": manifestFilename,
		"yaml_fragment": "- name: Comp0\n  status: validated\n",
	}, store.toolCtx)
	if err != nil {
		t.Fatal(err)
	}

	if result.Status != tool.StatusError || !strings.Contains(result.Message, "must be a YAML mapping") {
		t.Fatalf("result = %q %q, want a refusal of a list at the document root", result.Status, result.Message)
	}
	if store.latest(t) != manifest {
		t.Error("the manifest changed")
	}
}

func TestManifestMerge_PathBranchLeavesAListRootAlone(t *testing.T) {
	toolCtx, facade := newDataToolContext(t)
	saveDataToolArtifact(t, context.Background(), facade, "plan.yaml", []byte("- one\n- two\n"), SAMBuildManifestMIMEType)

	result, err := (&YAMLMergeArtifactTool{}).Execute(context.Background(), map[string]any{
		"artifact_name": "plan.yaml",
		"yaml_fragment": "- {name: A, type: agent, filename: a.yaml}\n",
		"path":          "components",
	}, toolCtx)
	if err != nil {
		t.Fatal(err)
	}

	if result.Status != tool.StatusError {
		t.Fatalf("status = %q, want an error for a manifest whose root is not a mapping", result.Status)
	}
	content, _, loadErr := facade.LoadArtifact(context.Background(), "plan.yaml", nil)
	if loadErr != nil || string(content) != "- one\n- two\n" {
		t.Errorf("the artifact changed: %q %v", content, loadErr)
	}
}

func TestManifestMerge_RootKeysOutsideComponentsStillMerge(t *testing.T) {
	for _, merger := range manifestMergers() {
		t.Run(merger.name, func(t *testing.T) {
			store := newManifestStore(t, fixtureManifest(numberedSpecs(2)))

			merger.apply(t, store.toolCtx, manifestFilename, "display_name: Renamed Mesh\ncomponents:\n  - name: Comp0\n    status: validated\n")

			var doc map[string]any
			if err := yaml.Unmarshal([]byte(store.latest(t)), &doc); err != nil {
				t.Fatal(err)
			}
			if doc["display_name"] != "Renamed Mesh" {
				t.Errorf("display_name = %v, want Renamed Mesh", doc["display_name"])
			}
			if doc["name"] != "support-mesh" {
				t.Errorf("name = %v, want it kept", doc["name"])
			}
			if list, _ := doc["components"].([]any); len(list) != 2 {
				t.Errorf("components = %v, want 2 entries", doc["components"])
			}
		})
	}
}

func TestManifestMerge_SameNameDifferentKind(t *testing.T) {
	const manifest = `name: mesh
components:
  - {filename: billing.json, type: connector, name: Billing, status: draft, origin: session}
  - {filename: billing.yaml, type: agent, name: Billing, status: draft, origin: session}
`
	for _, merger := range manifestMergers() {
		t.Run(merger.name+"/typed update touches only its kind", func(t *testing.T) {
			store := newManifestStore(t, manifest)

			result := merger.apply(t, store.toolCtx, manifestFilename, "components:\n  - name: Billing\n    type: agent\n    status: validated\n")
			if result.Status == tool.StatusError {
				t.Fatalf("merge refused: %s", result.Message)
			}

			components := decodeManifestComponents(t, store.latest(t))
			if len(components) != 2 {
				t.Fatalf("components = %d, want 2", len(components))
			}
			if components[0]["status"] != "draft" || components[1]["status"] != "validated" {
				t.Errorf("statuses = %v / %v, want the connector untouched and the agent validated", components[0]["status"], components[1]["status"])
			}
		})

		t.Run(merger.name+"/untyped update is ambiguous", func(t *testing.T) {
			store := newManifestStore(t, manifest)

			result := merger.apply(t, store.toolCtx, manifestFilename, "components:\n  - name: Billing\n    status: validated\n")

			if result.Status != tool.StatusError || !strings.Contains(result.Message, "to say which one") {
				t.Fatalf("result = %q %q, want an ambiguity refusal", result.Status, result.Message)
			}
			if store.latest(t) != manifest || store.versions(t) != 1 {
				t.Errorf("a refused merge must not write a version (versions = %d)", store.versions(t))
			}
		})

		t.Run(merger.name+"/a new kind cannot reuse a taken name", func(t *testing.T) {
			store := newManifestStore(t, manifest)

			result := merger.apply(t, store.toolCtx, manifestFilename, "components:\n  - {name: Billing, type: workflow, filename: billing_flow.yaml}\n")

			if result.Status != tool.StatusError || !strings.Contains(result.Message, "already held") {
				t.Fatalf("result = %q %q, want a name-collision refusal", result.Status, result.Message)
			}
			if store.versions(t) != 1 {
				t.Errorf("a refused merge must not write a version")
			}
		})
	}
}

func TestManifestMerge_MatchesByPlatformID(t *testing.T) {
	for _, merger := range manifestMergers() {
		t.Run(merger.name, func(t *testing.T) {
			store := newManifestStore(t, fixtureManifest(numberedSpecs(3)))
			before := decodeManifestComponents(t, store.latest(t))

			merger.apply(t, store.toolCtx, manifestFilename, "components:\n  - name: Comp1Renamed\n    id: 01a14000-0000-7000-8000-000000000001\n    description: renamed by id\n")

			after := decodeManifestComponents(t, store.latest(t))
			if len(after) != 3 {
				t.Fatalf("components = %d, want 3", len(after))
			}
			if after[1]["name"] != "Comp1Renamed" || after[1]["description"] != "renamed by id" || after[1]["filename"] != before[1]["filename"] {
				t.Errorf("components[1] = %v, want the id-matched entry renamed with its other fields kept", after[1])
			}
		})
	}
}

func TestManifestMerge_RefusesFragmentsItCannotPlace(t *testing.T) {
	cases := []struct {
		name     string
		fragment string
		wantMsg  string
	}{
		{"unknown component without a type", "components:\n  - name: Typo\n    status: validated\n", "no component named"},
		{"entry without a name", "components:\n  - status: validated\n", "needs a name"},
		{"entry that is not a mapping", "components:\n  - Comp0\n", "must be a mapping"},
		{"components given as a mapping", "components:\n  Comp0:\n    status: validated\n", "must be a list"},
		{"components given as a scalar", "components: validated\n", "must be a list"},
	}
	for _, merger := range manifestMergers() {
		for _, tc := range cases {
			t.Run(merger.name+"/"+tc.name, func(t *testing.T) {
				manifest := fixtureManifest(numberedSpecs(3))
				store := newManifestStore(t, manifest)

				result := merger.apply(t, store.toolCtx, manifestFilename, tc.fragment)

				if result.Status != tool.StatusError || !strings.Contains(result.Message, tc.wantMsg) {
					t.Fatalf("result = %q %q, want an error containing %q", result.Status, result.Message, tc.wantMsg)
				}
				if store.latest(t) != manifest || store.versions(t) != 1 {
					t.Errorf("a refused merge must leave the manifest as it was (versions = %d)", store.versions(t))
				}
			})
		}
	}
}

func TestManifestMerge_PatchCannotDeleteComponents(t *testing.T) {
	manifest := fixtureManifest(numberedSpecs(3))
	store := newManifestStore(t, manifest)

	result, err := (&MergeStructuredDataTool{}).Execute(context.Background(), map[string]any{
		"input_filename": manifestFilename,
		"patch":          map[string]any{"components": nil},
	}, store.toolCtx)
	if err != nil {
		t.Fatal(err)
	}

	if result.Status != tool.StatusError || !strings.Contains(result.Message, "cannot be deleted") {
		t.Fatalf("result = %q %q, want a refusal to delete components", result.Status, result.Message)
	}
	if store.latest(t) != manifest {
		t.Error("the manifest changed")
	}
}

func TestManifestMerge_NeverRemovesAComponent(t *testing.T) {
	fragments := map[string]string{
		"an empty list":        "components: []\n",
		"one entry of several": "components:\n  - name: Comp1\n    type: workflow\n    status: validated\n",
	}
	for _, merger := range manifestMergers() {
		for name, fragment := range fragments {
			t.Run(merger.name+"/"+name, func(t *testing.T) {
				store := newManifestStore(t, fixtureManifest(numberedSpecs(3)))

				merger.apply(t, store.toolCtx, manifestFilename, fragment)

				if got := len(decodeManifestComponents(t, store.latest(t))); got != 3 {
					t.Errorf("components = %d, want 3", got)
				}
			})
		}
	}
}

func TestManifestMerge_AppendsANewComponent(t *testing.T) {
	for _, merger := range manifestMergers() {
		t.Run(merger.name, func(t *testing.T) {
			store := newManifestStore(t, fixtureManifest(numberedSpecs(2)))
			before := decodeManifestComponents(t, store.latest(t))

			result := merger.apply(t, store.toolCtx, manifestFilename, `components:
  - filename: late.yaml
    type: agent
    name: LateAgent
    status: draft
    origin: session
`)
			if result.Status == tool.StatusError {
				t.Fatalf("merge refused: %s", result.Message)
			}

			after := decodeManifestComponents(t, store.latest(t))
			if len(after) != 3 || after[2]["name"] != "LateAgent" {
				t.Fatalf("components = %v, want LateAgent appended", after)
			}
			if !reflect.DeepEqual(after[:2], before) {
				t.Errorf("existing components changed: %v", after[:2])
			}
		})
	}
}

func TestManifestMerge_ReapplyConverges(t *testing.T) {
	fragments := map[string]string{
		"status update": "components:\n  - name: Comp1\n    type: workflow\n    status: validated\n",
		"new component": "components:\n  - {filename: late.yaml, type: agent, name: LateAgent, status: draft, origin: session}\n",
	}
	for _, merger := range manifestMergers() {
		for name, fragment := range fragments {
			t.Run(merger.name+"/"+name, func(t *testing.T) {
				store := newManifestStore(t, fixtureManifest(numberedSpecs(3)))

				merger.apply(t, store.toolCtx, manifestFilename, fragment)
				first := store.latest(t)
				merger.apply(t, store.toolCtx, manifestFilename, fragment)

				if second := store.latest(t); second != first {
					t.Errorf("a second application changed the manifest:\nfirst:\n%s\nsecond:\n%s", first, second)
				}
			})
		}
	}
}

func TestManifestMerge_AppendAtComponentsPathDoesNotDuplicate(t *testing.T) {
	store := newManifestStore(t, fixtureManifest(numberedSpecs(3)))
	apply := func(fragment string) *tool.Result {
		result, err := (&YAMLMergeArtifactTool{}).Execute(context.Background(), map[string]any{
			"artifact_name": manifestFilename,
			"yaml_fragment": fragment,
			"path":          "components",
		}, store.toolCtx)
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	const newcomer = "- {filename: late.yaml, type: agent, name: LateAgent, status: draft, origin: session}\n"

	if result := apply(newcomer); result.Status == tool.StatusError {
		t.Fatalf("append refused: %s", result.Message)
	}
	appended := store.latest(t)
	if result := apply(newcomer); result.Status == tool.StatusError {
		t.Fatalf("re-append refused: %s", result.Message)
	}

	if store.latest(t) != appended {
		t.Errorf("appending the same component twice must not duplicate it:\n%s", store.latest(t))
	}
	if got := len(decodeManifestComponents(t, appended)); got != 4 {
		t.Errorf("components = %d, want 4", got)
	}
}

func TestManifestMerge_ComponentsPathRefusesAMapping(t *testing.T) {
	manifest := fixtureManifest(numberedSpecs(3))
	store := newManifestStore(t, manifest)

	result, err := (&YAMLMergeArtifactTool{}).Execute(context.Background(), map[string]any{
		"artifact_name": manifestFilename,
		"yaml_fragment": "name: Comp0\nstatus: validated\n",
		"path":          "components",
	}, store.toolCtx)
	if err != nil {
		t.Fatal(err)
	}

	if result.Status != tool.StatusError || !strings.Contains(result.Message, "must be a list") {
		t.Fatalf("result = %q %q, want a refusal", result.Status, result.Message)
	}
	if store.latest(t) != manifest {
		t.Error("the manifest changed")
	}
}

func TestManifestMerge_StatusUpdateBeforeTheListIsWritten(t *testing.T) {
	const early = "name: mesh\ndisplay_name: Mesh\n"
	for _, merger := range manifestMergers() {
		t.Run(merger.name+"/full entries become the list", func(t *testing.T) {
			store := newManifestStore(t, early)

			result := merger.apply(t, store.toolCtx, manifestFilename, "components:\n  - {filename: a.yaml, type: agent, name: A, status: draft}\n  - {filename: b.yaml, type: agent, name: B, status: draft}\n")
			if result.Status == tool.StatusError {
				t.Fatalf("merge refused: %s", result.Message)
			}

			components := decodeManifestComponents(t, store.latest(t))
			if len(components) != 2 || components[0]["filename"] != "a.yaml" || components[1]["name"] != "B" {
				t.Errorf("components = %v", components)
			}
		})

		t.Run(merger.name+"/a bare status has nothing to update", func(t *testing.T) {
			store := newManifestStore(t, early)

			result := merger.apply(t, store.toolCtx, manifestFilename, "components:\n  - name: A\n    status: validated\n")

			if result.Status != tool.StatusError || !strings.Contains(result.Message, "no component named") {
				t.Fatalf("result = %q %q, want a refusal", result.Status, result.Message)
			}
			if store.latest(t) != early {
				t.Error("the manifest changed")
			}
		})
	}
}

func TestManifestMerge_RecoversAComponentsMapping(t *testing.T) {
	const clobbered = "name: mesh\ncomponents:\n  A:\n    status: validated\n"
	for _, merger := range manifestMergers() {
		t.Run(merger.name, func(t *testing.T) {
			store := newManifestStore(t, clobbered)

			result := merger.apply(t, store.toolCtx, manifestFilename, "components:\n  - {filename: a.yaml, type: agent, name: A, status: validated}\n")
			if result.Status == tool.StatusError {
				t.Fatalf("merge refused: %s", result.Message)
			}

			components := decodeManifestComponents(t, store.latest(t))
			if len(components) != 1 || components[0]["filename"] != "a.yaml" {
				t.Errorf("components = %v, want the full entry as the list", components)
			}
		})
	}
}

func waitWithinDeadline(t *testing.T, wg *sync.WaitGroup) {
	t.Helper()
	finished := make(chan struct{})
	go func() {
		wg.Wait()
		close(finished)
	}()
	select {
	case <-finished:
	case <-time.After(30 * time.Second):
		t.Fatal("the merges did not finish: a manifest lock was probably never released")
	}
}

func TestManifestMerge_ConcurrentStatusUpdates(t *testing.T) {
	const components = 9
	for _, mixed := range []bool{false, true} {
		t.Run(fmt.Sprintf("mixed tools=%v", mixed), func(t *testing.T) {
			store := newManifestStore(t, fixtureManifest(numberedSpecs(components)))
			before := decodeManifestComponents(t, store.latest(t))
			mergers := manifestMergers()

			var wg sync.WaitGroup
			results := make([]*tool.Result, components)
			for i := range components {
				wg.Go(func() {
					merger := mergers[0]
					if mixed {
						merger = mergers[i%len(mergers)]
					}
					name := fmt.Sprintf("Comp%d", i)
					results[i] = merger.apply(t, store.toolCtx, manifestFilename, statusFragment([]string{name}, "name type and status"))
				})
			}
			waitWithinDeadline(t, &wg)

			updated := map[string]string{}
			for i, result := range results {
				if result.Status == tool.StatusError {
					t.Fatalf("update %d refused: %s", i, result.Message)
				}
				updated[fmt.Sprintf("Comp%d", i)] = "validated"
			}
			assertComponentsAfterStatusUpdate(t, before, decodeManifestComponents(t, store.latest(t)), updated)
		})
	}
}

func TestManifestMerge_OtherArtifactsStillReplaceRootArrays(t *testing.T) {
	const original = "name: agent\ncomponents:\n  - one\n  - two\n"
	for _, merger := range manifestMergers() {
		t.Run(merger.name, func(t *testing.T) {
			toolCtx, facade := newDataToolContext(t)
			saveDataToolArtifact(t, context.Background(), facade, "agent.yaml", []byte(original), "application/x-yaml")

			merger.apply(t, toolCtx, "agent.yaml", "components:\n  - three\n")

			content, _, err := facade.LoadArtifact(context.Background(), "agent.yaml", nil)
			if err != nil {
				t.Fatal(err)
			}
			var doc struct {
				Components []string `yaml:"components"`
			}
			if err := yaml.Unmarshal(content, &doc); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(doc.Components, []string{"three"}) {
				t.Errorf("components = %v, want the array replaced for a non-manifest artifact", doc.Components)
			}
		})
	}
}

func TestManifestMerge_ManifestIdentityIsNameOrMIMEType(t *testing.T) {
	cases := []struct {
		name     string
		filename string
		mimeType string
	}{
		{"manifest name with the manifest MIME type", manifestFilename, SAMBuildManifestMIMEType},
		{"manifest name with a generic YAML MIME type", manifestFilename, "application/x-yaml"},
		{"another name with the manifest MIME type", "plan.yaml", SAMBuildManifestMIMEType},
	}
	for _, merger := range manifestMergers() {
		for _, tc := range cases {
			t.Run(merger.name+"/"+tc.name, func(t *testing.T) {
				toolCtx, facade := newDataToolContext(t)
				saveDataToolArtifact(t, context.Background(), facade, tc.filename, []byte(fixtureManifest(numberedSpecs(3))), tc.mimeType)

				merger.apply(t, toolCtx, tc.filename, "components:\n  - name: Comp0\n    status: validated\n")

				content, _, err := facade.LoadArtifact(context.Background(), tc.filename, nil)
				if err != nil {
					t.Fatal(err)
				}
				if got := len(decodeManifestComponents(t, string(content))); got != 3 {
					t.Errorf("components = %d, want 3", got)
				}
			})
		}
	}
}

func TestManifestMerge_YAMLToolKeepsKeyOrder(t *testing.T) {
	store := newManifestStore(t, fixtureManifest(numberedSpecs(2)))

	result := manifestMergers()[0].apply(t, store.toolCtx, manifestFilename, "components:\n  - name: Comp0\n    status: validated\n")
	if result.Status == tool.StatusError {
		t.Fatalf("merge refused: %s", result.Message)
	}

	merged := store.latest(t)
	if strings.Index(merged, "display_name:") > strings.Index(merged, "components:") || strings.Index(merged, "filename: Comp0.yaml") > strings.Index(merged, "type: agent") {
		t.Errorf("key order was not preserved:\n%s", merged)
	}
}

func holdManifest(t *testing.T, toolCtx *tool.Context) (release func()) {
	t.Helper()
	hold := lockBuildManifest(toolCtx, manifestFilename)
	var once sync.Once
	release = func() { once.Do(hold.Unlock) }
	t.Cleanup(release)
	return release
}

func TestManifestLocks_ReleaseTheirEntries(t *testing.T) {
	toolCtx, _ := newDataToolContext(t)

	holdManifest(t, toolCtx)()

	if got := heldLockCount(); got != 0 {
		t.Errorf("lock entries after unlock = %d, want 0", got)
	}
}

func TestManifestLocks_OnlyTheManifestTakesALock(t *testing.T) {
	toolCtx, _ := newDataToolContext(t)
	before := heldLockCount()

	hold := lockBuildManifest(toolCtx, "agent.yaml")
	defer hold.Unlock()

	if got := heldLockCount(); got != before {
		t.Errorf("lock entries = %d, want %d: an artifact that is not the manifest takes none", got, before)
	}
}

func heldLockCount() int {
	manifestLocks.mu.Lock()
	defer manifestLocks.mu.Unlock()
	return len(manifestLocks.held)
}

func lockedInBackground(t *testing.T, toolCtx *tool.Context) (acquired <-chan struct{}, release func()) {
	t.Helper()
	got := make(chan struct{})
	free := make(chan struct{})
	var once sync.Once
	release = func() { once.Do(func() { close(free) }) }
	t.Cleanup(release)
	go func() {
		hold := lockBuildManifest(toolCtx, manifestFilename)
		close(got)
		<-free
		hold.Unlock()
	}()
	return got, release
}

func TestManifestLocks_SerializeWritersInOrder(t *testing.T) {
	toolCtx, _ := newDataToolContext(t)
	releaseFirst := holdManifest(t, toolCtx)

	second, releaseSecond := lockedInBackground(t, toolCtx)
	waitForWaiters(t, 2)
	select {
	case <-second:
		t.Fatal("a second writer entered the manifest while the first held it")
	default:
	}

	releaseFirst()
	select {
	case <-second:
	case <-time.After(2 * time.Second):
		t.Fatal("the second writer never got the manifest after the first released it")
	}

	third, releaseThird := lockedInBackground(t, toolCtx)
	waitForWaiters(t, 2)
	select {
	case <-third:
		t.Fatal("a third writer entered the manifest while the second held it: the lock entry was dropped while still in use")
	default:
	}
	releaseSecond()
	select {
	case <-third:
	case <-time.After(2 * time.Second):
		t.Fatal("the third writer never got the manifest")
	}
	releaseThird()
}

func waitForWaiters(t *testing.T, n int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		manifestLocks.mu.Lock()
		users := 0
		for _, entry := range manifestLocks.held {
			users += entry.users
		}
		manifestLocks.mu.Unlock()
		if users == n {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("the manifest lock never reached %d users", n)
}

func TestManifestLocks_ValidateSaveWaitsForAMergeInFlight(t *testing.T) {
	store := newManifestStore(t, fixtureManifest(numberedSpecs(2)))
	release := holdManifest(t, store.toolCtx)

	saved := make(chan error, 1)
	go func() {
		saved <- saveManifestArtifact(store.ctx, []byte("name: saved\ncomponents: []\n"), store.toolCtx)
	}()
	select {
	case <-saved:
		t.Fatal("the validate save wrote while a merge held the manifest")
	case <-time.After(50 * time.Millisecond):
	}

	release()
	select {
	case err := <-saved:
		if err != nil {
			t.Fatalf("validate save failed after the merge released: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the validate save never ran after the merge released the manifest")
	}
}

type slowFirstSave struct {
	artifact.Service
	entered chan struct{}
	proceed chan struct{}
	once    sync.Once
}

func (s *slowFirstSave) Save(ctx context.Context, loc artifact.Locator, content []byte, mimeType string) (int, error) {
	s.once.Do(func() {
		close(s.entered)
		<-s.proceed
	})
	return s.Service.Save(ctx, loc, content, mimeType)
}

func TestManifestLocks_ValidateSaveHoldsTheLockAcrossItsSave(t *testing.T) {
	slow := &slowFirstSave{Service: memory.New(), entered: make(chan struct{}), proceed: make(chan struct{})}
	toolCtx := &tool.Context{
		ArtifactService: &artifact.ScopedService{Inner: slow, Scope: "agent"},
		AgentName:       "test-agent",
		UserID:          "test-user",
		SessionID:       "test-session",
	}
	var proceedOnce sync.Once
	release := func() { proceedOnce.Do(func() { close(slow.proceed) }) }
	t.Cleanup(release)

	stamped := fixtureManifest(numberedSpecs(2)) + "note: written-by-validate\n"
	saved := make(chan error, 1)
	go func() {
		saved <- saveManifestArtifact(context.Background(), []byte(stamped), toolCtx)
	}()
	select {
	case <-slow.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the validate save never reached the store")
	}

	merged := make(chan *tool.Result, 1)
	go func() {
		result, err := (&YAMLMergeArtifactTool{}).Execute(context.Background(), map[string]any{
			"artifact_name": manifestFilename,
			"yaml_fragment": "components:\n  - name: Comp0\n    status: validated\n",
		}, toolCtx)
		merged <- resultOrError(t, "yaml_merge_artifact", result, err)
	}()
	select {
	case <-merged:
		t.Fatal("a merge ran while the validate save was inside its write")
	case <-time.After(50 * time.Millisecond):
	}

	release()
	select {
	case err := <-saved:
		if err != nil {
			t.Fatalf("validate save failed: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the validate save never finished")
	}
	select {
	case result := <-merged:
		if result.Status == tool.StatusError {
			t.Fatalf("merge failed after the save finished: %s", result.Message)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the merge never finished after the save did")
	}

	content, _, err := toolCtx.Facade().LoadArtifact(context.Background(), manifestFilename, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "note: written-by-validate") || decodeManifestComponents(t, string(content))[0]["status"] != "validated" {
		t.Errorf("the validate save and the merged status must both survive:\n%s", content)
	}
}

const minIdentityVectors = 22

func TestManifestMerge_ComponentIdentityContract(t *testing.T) {
	raw, err := os.ReadFile("testdata/manifest_component_identity.json")
	if err != nil {
		t.Fatal(err)
	}
	var vectors []struct {
		Name string         `json:"name"`
		A    map[string]any `json:"a"`
		B    map[string]any `json:"b"`
		Same bool           `json:"same"`
	}
	if err := json.Unmarshal(raw, &vectors); err != nil {
		t.Fatal(err)
	}
	if len(vectors) < minIdentityVectors {
		t.Fatalf("the identity vectors hold %d rows, want at least %d: the file is the contract both sides are held to", len(vectors), minIdentityVectors)
	}
	node := func(fields map[string]any) *yaml.Node {
		var n yaml.Node
		if err := n.Encode(fields); err != nil {
			t.Fatal(err)
		}
		return &n
	}
	for _, v := range vectors {
		t.Run(v.Name, func(t *testing.T) {
			if got := matchesByIdentity(node(v.A), node(v.B), true); got != v.Same {
				t.Errorf("matchesByIdentity(%v, %v) = %v, want %v", v.A, v.B, got, v.Same)
			}
			if got := matchesByIdentity(node(v.B), node(v.A), true); got != v.Same {
				t.Errorf("the rule is not symmetric: (%v, %v) = %v, want %v", v.B, v.A, got, v.Same)
			}
		})
	}
}

var yamlOnlyRefusals = map[string]bool{
	"a repeated components key":          true,
	"a name repeated inside an entry":    true,
	"a type repeated inside an entry":    true,
	"a status repeated inside an entry":  true,
	"a name repeated inside a new entry": true,
	"a merge key":                        true,
	"an anchor and alias":                true,
	"an anchor with no alias":            true,
	"a key that arrives as an alias":     true,
	"a second YAML document":             true,
}

func TestManifestMerge_MoreRefusalsLeaveTheManifestAlone(t *testing.T) {
	cases := []struct {
		name     string
		fragment string
		wantMsg  string
	}{
		{"a typed typo without a filename", "components:\n  - {name: Comp0x, type: agent, status: validated}\n", "needs a filename"},
		{"a repeated components key", "components:\n  - {name: Comp0, status: validated}\ncomponents:\n  - {name: Comp1, status: validated}\n", "already defined"},
		{"a type change", "components:\n  - {name: Comp0, type: workflow, id: 01a14000-0000-7000-8000-000000000000, status: validated}\n", "leave id out"},
		{"a numeric name", "components:\n  - {name: 123, type: agent, filename: n.yaml}\n", "name must be a string"},
		{"a list where type is a string", "components:\n  - {name: Late, type: [agent], filename: l.yaml}\n", "type must be a string"},
		{"one bad entry among good ones", "components:\n  - {name: Comp0, status: validated}\n  - {name: Comp1, type: 5}\n", "components[1]"},
		{"a name repeated inside an entry", "components:\n  - {name: Comp0, name: Comp1}\n", "already defined"},
		{"a type repeated inside an entry", "components:\n  - {name: Comp0, type: agent, type: workflow}\n", "already defined"},
		{"a status repeated inside an entry", "components:\n  - {name: Comp0, status: validated, status: deployed}\n", "already defined"},
		{"a name repeated inside a new entry", "components:\n  - {name: Late, name: Comp0, type: agent, filename: l.yaml}\n", "already defined"},
		{"another component's id on a status update", "components:\n  - {name: Comp1, status: validated, id: 01a14999-0000-7000-8000-000000000009}\n", "leave id out"},
		{"a merge key", "components:\n  - {<<: {status: finished, id: 5}, name: New, type: agent, filename: n.yaml}\n", "merge key"},
		{"an anchor with no alias", "components:\n  - {name: Comp0, status: validated, tools: &t [x]}\n", "anchors and aliases"},
		{"an anchor and alias", "components:\n  - {name: N, type: agent, filename: n.yaml, tools: &t [x]}\n  - {name: Comp0, tools: *t}\n", "anchors and aliases"},
		{"a key that arrives as an alias", "x: &k components\n*k :\n  - {name: Comp0, status: validated}\n", "anchors and aliases"},
		{"a second YAML document", "components:\n  - {name: Comp0, status: validated}\n---\ncomponents:\n  - {name: Comp1, type: 5}\n", "single YAML document"},
		{"a rename onto a taken name", "components:\n  - {name: Comp1, id: 01a14000-0000-7000-8000-000000000000}\n", "leave id out"},
	}
	for _, merger := range manifestMergers() {
		for _, tc := range cases {
			if yamlOnlyRefusals[tc.name] && merger.name == "merge_structured_data" {
				continue
			}
			t.Run(merger.name+"/"+tc.name, func(t *testing.T) {
				manifest := fixtureManifest(numberedSpecs(3))
				store := newManifestStore(t, manifest)

				result := merger.apply(t, store.toolCtx, manifestFilename, tc.fragment)

				if result.Status != tool.StatusError || !strings.Contains(result.Message, tc.wantMsg) {
					t.Fatalf("result = %q %q, want an error containing %q", result.Status, result.Message, tc.wantMsg)
				}
				if !strings.Contains(result.Message, "the manifest was not changed") && !strings.Contains(result.Message, "nothing was saved") {
					t.Errorf("message = %q, want every refusal to say nothing was saved", result.Message)
				}
				if store.latest(t) != manifest || store.versions(t) != 1 {
					t.Errorf("a refused merge must leave the manifest as it was (versions = %d)", store.versions(t))
				}
			})
		}
	}
}

func TestManifestMerge_ErrorSaysNothingWasApplied(t *testing.T) {
	store := newManifestStore(t, fixtureManifest(numberedSpecs(3)))

	result := manifestMergers()[0].apply(t, store.toolCtx, manifestFilename, "components:\n  - {name: Comp0, status: validated}\n  - {name: Nope, status: validated}\n")

	if !strings.Contains(result.Message, "components[1]") || !strings.Contains(result.Message, "the manifest was not changed") {
		t.Fatalf("message = %q, want the failing index and a statement that nothing was applied", result.Message)
	}
	if store.versions(t) != 1 {
		t.Errorf("a refused fragment must not save its valid first entry (versions = %d)", store.versions(t))
	}
}

func TestManifestMerge_NonManifestComponentsPathStillConcatenates(t *testing.T) {
	toolCtx, facade := newDataToolContext(t)
	saveDataToolArtifact(t, context.Background(), facade, "agent.yaml", []byte("name: agent\ncomponents:\n  - one\n"), "application/x-yaml")

	result, err := (&YAMLMergeArtifactTool{}).Execute(context.Background(), map[string]any{
		"artifact_name": "agent.yaml",
		"yaml_fragment": "- two\n",
		"path":          "components",
	}, toolCtx)
	if err != nil || result.Status == tool.StatusError {
		t.Fatalf("merge failed: %v %v", err, result)
	}

	content, _, loadErr := facade.LoadArtifact(context.Background(), "agent.yaml", nil)
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	var doc struct {
		Components []string `yaml:"components"`
	}
	if err := yaml.Unmarshal(content, &doc); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(doc.Components, []string{"one", "two"}) {
		t.Errorf("components = %v, want the items concatenated for a non-manifest artifact", doc.Components)
	}
}

func TestManifestMerge_PatchWithoutComponentsMergesOtherKeys(t *testing.T) {
	store := newManifestStore(t, fixtureManifest(numberedSpecs(3)))
	before := decodeManifestComponents(t, store.latest(t))

	result, err := (&MergeStructuredDataTool{}).Execute(context.Background(), map[string]any{
		"input_filename": manifestFilename,
		"patch":          map[string]any{"display_name": "Renamed"},
	}, store.toolCtx)
	if err != nil || result.Status == tool.StatusError {
		t.Fatalf("patch refused: %v %v", err, result)
	}

	assertComponentsAfterStatusUpdate(t, before, decodeManifestComponents(t, store.latest(t)), nil)
	if !strings.Contains(store.latest(t), "display_name: Renamed") {
		t.Errorf("display_name was not merged:\n%s", store.latest(t))
	}
}

func TestManifestMerge_NewEntryUIOwnedFieldsAreStrippedAndReported(t *testing.T) {
	for _, merger := range manifestMergers() {
		t.Run(merger.name, func(t *testing.T) {
			store := newManifestStore(t, fixtureManifest(numberedSpecs(2)))

			result := merger.apply(t, store.toolCtx, manifestFilename, "components:\n  - {name: Late, type: agent, filename: l.yaml, id: forged, deployed_version: 5}\n")

			if result.Status == tool.StatusError {
				t.Fatalf("merge refused: %s", result.Message)
			}
			late := decodeManifestComponents(t, store.latest(t))[2]
			if _, written := late["id"]; written {
				t.Errorf("id was written on a new entry: %v", late)
			}
			if _, written := late["deployed_version"]; written {
				t.Errorf("deployed_version was written on a new entry: %v", late)
			}
			if !strings.Contains(result.Message, `id on "Late"`) || !strings.Contains(result.Message, `deployed_version on "Late"`) {
				t.Errorf("message = %q", result.Message)
			}
		})
	}
}

func TestManifestMerge_RepeatedKeyAtAComponentsPathOrInANodesItem(t *testing.T) {
	cases := map[string]struct{ fragment, path string }{
		"at path components":  {"- {name: Late, type: agent, filename: l.yaml, filename: m.yaml}\n", "components"},
		"inside a nodes item": {"components:\n  - name: Comp1\n    nodes:\n      - {id: s1, type: agent, agent_name: A, agent_name: B}\n", ""},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			manifest := fixtureManifest(numberedSpecs(3))
			store := newManifestStore(t, manifest)
			args := map[string]any{"artifact_name": manifestFilename, "yaml_fragment": tc.fragment}
			if tc.path != "" {
				args["path"] = tc.path
			}

			result, err := (&YAMLMergeArtifactTool{}).Execute(context.Background(), args, store.toolCtx)
			if err != nil {
				t.Fatal(err)
			}

			if result.Status != tool.StatusError || !strings.Contains(result.Message, "already defined") {
				t.Fatalf("result = %q %q", result.Status, result.Message)
			}
			if store.latest(t) != manifest || store.versions(t) != 1 {
				t.Error("the manifest changed")
			}
		})
	}
}

func TestManifestMerge_TypelessComponentIsNotMatchedByATypedEntry(t *testing.T) {
	const manifest = "name: mesh\ncomponents:\n  - {name: Legacy, filename: legacy.yaml, status: draft}\n"
	for _, merger := range manifestMergers() {
		t.Run(merger.name, func(t *testing.T) {
			store := newManifestStore(t, manifest)

			result := merger.apply(t, store.toolCtx, manifestFilename, "components:\n  - {name: Legacy, type: agent, filename: l.yaml}\n")

			if result.Status != tool.StatusError || !strings.Contains(result.Message, "already held") {
				t.Fatalf("result = %q %q, want the name-held refusal: a typed entry must not match a typeless component", result.Status, result.Message)
			}
			if store.latest(t) != manifest {
				t.Error("the manifest changed")
			}
		})
	}
}

func TestManifestMerge_ListRootedManifestRefusesAMappingFragment(t *testing.T) {
	toolCtx, facade := newDataToolContext(t)
	saveDataToolArtifact(t, context.Background(), facade, manifestFilename, []byte("- one\n- two\n"), SAMBuildManifestMIMEType)

	result, err := (&YAMLMergeArtifactTool{}).Execute(context.Background(), map[string]any{
		"artifact_name": manifestFilename,
		"yaml_fragment": "components:\n  - {name: A, type: agent, filename: a.yaml}\n",
	}, toolCtx)
	if err != nil {
		t.Fatal(err)
	}

	content, _, loadErr := facade.LoadArtifact(context.Background(), manifestFilename, nil)
	if loadErr != nil || result.Status != tool.StatusError || string(content) != "- one\n- two\n" {
		t.Errorf("result = %q %q; content %q %v", result.Status, result.Message, content, loadErr)
	}
}

func TestManifestMerge_StructuredReportIsInTheResultData(t *testing.T) {
	store := newManifestStore(t, fixtureManifest(numberedSpecs(2)))

	result, err := (&MergeStructuredDataTool{}).Execute(context.Background(), map[string]any{
		"input_filename": manifestFilename,
		"patch":          map[string]any{"components": []any{map[string]any{"name": "Comp0", "status": "validated", "id": "x"}}},
	}, store.toolCtx)
	if err != nil || result.Status == tool.StatusError {
		t.Fatalf("merge failed: %v %v", err, result)
	}

	message, _ := result.Data["message"].(string)
	if !strings.Contains(message, `Updated: "Comp0"`) || !strings.Contains(message, `id on "Comp0"`) {
		t.Errorf("data message = %q, want the merge report", message)
	}
}

func TestManifestMerge_YAMLReportIsInTheResultData(t *testing.T) {
	store := newManifestStore(t, fixtureManifest(numberedSpecs(2)))

	result := manifestMergers()[0].apply(t, store.toolCtx, manifestFilename, "components:\n  - {name: Comp0, status: validated}\n")

	report, _ := result.Data["merge_report"].(string)
	if !strings.Contains(report, `Updated: "Comp0" ("status")`) {
		t.Errorf("merge_report = %q, want the updated component and its fields", report)
	}
}

func TestManifestMerge_ReportNamesAreQuotedAndCapped(t *testing.T) {
	var fragment strings.Builder
	fragment.WriteString("components:\n")
	for i := range 14 {
		fmt.Fprintf(&fragment, "  - {name: N%d, type: agent, filename: n%d.yaml}\n", i, i)
	}
	fragment.WriteString("  - {name: \"Odd\\nname\", type: agent, filename: odd.yaml}\n")
	for _, merger := range manifestMergers() {
		t.Run(merger.name, func(t *testing.T) {
			store := newManifestStore(t, fixtureManifest(numberedSpecs(1)))

			result := merger.apply(t, store.toolCtx, manifestFilename, fragment.String())

			if result.Status == tool.StatusError {
				t.Fatalf("merge refused: %s", result.Message)
			}
			if !strings.Contains(result.Message, "and 5 more") || strings.Contains(result.Message, "N11") {
				t.Errorf("message = %q, want the added names capped at ten", result.Message)
			}
			if strings.Contains(result.Message, "Odd\nname") && !strings.Contains(result.Message, `Odd\nname`) {
				t.Errorf("message = %q, want a newline in a name escaped", result.Message)
			}
		})
	}
}

func TestManifestMerge_RenameByIDIsReported(t *testing.T) {
	store := newManifestStore(t, fixtureManifest(numberedSpecs(3)))

	result := manifestMergers()[0].apply(t, store.toolCtx, manifestFilename, "components:\n  - {name: Renamed, id: 01a14000-0000-7000-8000-000000000001, description: x}\n")

	if !strings.Contains(result.Message, `"Comp1" renamed to "Renamed"`) {
		t.Errorf("message = %q, want the rename reported", result.Message)
	}
}

func TestManifestMerge_BlankNameIsRefused(t *testing.T) {
	for _, merger := range manifestMergers() {
		t.Run(merger.name, func(t *testing.T) {
			manifest := fixtureManifest(numberedSpecs(2))
			store := newManifestStore(t, manifest)

			result := merger.apply(t, store.toolCtx, manifestFilename, "components:\n  - {name: \"  \", type: agent, filename: b.yaml}\n")

			if result.Status != tool.StatusError || !strings.Contains(result.Message, "needs a name") {
				t.Fatalf("result = %q %q", result.Status, result.Message)
			}
		})
	}
}

func TestManifestMerge_UnknownFieldIsMergedAndShownInTheReport(t *testing.T) {
	for _, merger := range manifestMergers() {
		t.Run(merger.name, func(t *testing.T) {
			store := newManifestStore(t, fixtureManifest(numberedSpecs(2)))

			result := merger.apply(t, store.toolCtx, manifestFilename, "components:\n  - {name: Comp0, statuss: validated}\n")

			if result.Status == tool.StatusError || !strings.Contains(result.Message, `Updated: "Comp0" ("statuss")`) {
				t.Fatalf("result = %q %q, want the unknown field merged and listed", result.Status, result.Message)
			}
		})
	}
}

func TestManifestMerge_SecondRefusalIsReportedWithItsIndexAndTheNothingChangedSuffix(t *testing.T) {
	cases := map[string]string{
		"structural": "components:\n  - {name: Comp0, status: validated}\n  - {name: Comp1, type: 5}\n",
		"merge time": "components:\n  - {name: Comp0, status: validated}\n  - {name: Nope, status: validated}\n",
		"id rename":  "components:\n  - {name: Comp0, status: validated}\n  - {name: Comp2, id: 01a14000-0000-7000-8000-000000000001}\n",
	}
	for _, merger := range manifestMergers() {
		for name, fragment := range cases {
			t.Run(merger.name+"/"+name, func(t *testing.T) {
				manifest := fixtureManifest(numberedSpecs(3))
				store := newManifestStore(t, manifest)

				result := merger.apply(t, store.toolCtx, manifestFilename, fragment)

				if result.Status != tool.StatusError || !strings.Contains(result.Message, "components[1]") || !strings.Contains(result.Message, "the manifest was not changed") {
					t.Fatalf("result = %q %q", result.Status, result.Message)
				}
				if store.latest(t) != manifest || store.versions(t) != 1 {
					t.Errorf("the manifest changed (versions = %d)", store.versions(t))
				}
			})
		}
	}
}

func TestManifestMerge_StructuralErrorIsReportedBeforeAnEarlierMergeTimeOne(t *testing.T) {
	for _, merger := range manifestMergers() {
		t.Run(merger.name, func(t *testing.T) {
			store := newManifestStore(t, fixtureManifest(numberedSpecs(3)))

			result := merger.apply(t, store.toolCtx, manifestFilename, "components:\n  - {name: Nope, status: validated}\n  - {name: Comp1, type: 5}\n")

			if result.Status != tool.StatusError || !strings.Contains(result.Message, "components[1]") {
				t.Fatalf("result = %q %q, want the structural error on components[1] reported first", result.Status, result.Message)
			}
		})
	}
}

func TestManifestMerge_ReportEscapesNamesAndKeys(t *testing.T) {
	for _, merger := range manifestMergers() {
		t.Run(merger.name, func(t *testing.T) {
			store := newManifestStore(t, fixtureManifest(numberedSpecs(3)))

			result := merger.apply(t, store.toolCtx, manifestFilename, "components:\n  - {name: \"Odd\\nname\", type: agent, filename: odd.yaml, id: forged}\n  - {name: Comp0, \"key\\nwith newline\": 1}\n")

			if result.Status == tool.StatusError {
				t.Fatalf("merge refused: %s", result.Message)
			}
			if strings.Contains(result.Message, "Odd\nname") || strings.Contains(result.Message, "key\nwith") {
				t.Errorf("a raw newline reached the report: %q", result.Message)
			}
			if !strings.Contains(result.Message, `Odd\nname`) || !strings.Contains(result.Message, `key\nwith newline`) {
				t.Errorf("escaped name and key missing: %q", result.Message)
			}
		})
	}
}

func TestManifestMerge_ReportCapsEveryList(t *testing.T) {
	var updates, fields strings.Builder
	updates.WriteString("components:\n")
	for i := range 14 {
		fmt.Fprintf(&updates, "  - {name: Comp%d, status: validated, id: x}\n", i)
	}
	fields.WriteString("components:\n  - name: Comp0\n")
	for i := range 14 {
		fmt.Fprintf(&fields, "    field%d: v\n", i)
	}
	for _, merger := range manifestMergers() {
		t.Run(merger.name+"/updated and ignored", func(t *testing.T) {
			store := newManifestStore(t, fixtureManifest(numberedSpecs(14)))

			result := merger.apply(t, store.toolCtx, manifestFilename, updates.String())

			if strings.Count(result.Message, "and 4 more") != 2 || strings.Contains(result.Message, `"Comp12"`) {
				t.Errorf("message = %q, want both lists capped at ten", result.Message)
			}
		})
		t.Run(merger.name+"/fields", func(t *testing.T) {
			store := newManifestStore(t, fixtureManifest(numberedSpecs(2)))

			result := merger.apply(t, store.toolCtx, manifestFilename, fields.String())

			if !strings.Contains(result.Message, "and 4 more") || strings.Contains(result.Message, "field12") {
				t.Errorf("message = %q, want the field list capped at ten", result.Message)
			}
		})
		t.Run(merger.name+"/eleven", func(t *testing.T) {
			store := newManifestStore(t, fixtureManifest(numberedSpecs(1)))
			var eleven strings.Builder
			eleven.WriteString("components:\n")
			for i := range 11 {
				fmt.Fprintf(&eleven, "  - {name: M%d, type: agent, filename: m.yaml}\n", i)
			}

			result := merger.apply(t, store.toolCtx, manifestFilename, eleven.String())

			if !strings.Contains(result.Message, "and 1 more") || strings.Contains(result.Message, `"M10"`) {
				t.Errorf("message = %q, want ten names and one more", result.Message)
			}
		})
		t.Run(merger.name+"/exactly ten", func(t *testing.T) {
			store := newManifestStore(t, fixtureManifest(numberedSpecs(1)))
			var ten strings.Builder
			ten.WriteString("components:\n")
			for i := range 10 {
				fmt.Fprintf(&ten, "  - {name: N%d, type: agent, filename: n.yaml}\n", i)
			}

			result := merger.apply(t, store.toolCtx, manifestFilename, ten.String())

			if strings.Contains(result.Message, "more") || !strings.Contains(result.Message, `"N9"`) {
				t.Errorf("message = %q, want all ten names and no cap note", result.Message)
			}
		})
	}
}

func TestManifestMerge_ReportWording(t *testing.T) {
	store := newManifestStore(t, fixtureManifest(numberedSpecs(3)))

	result := manifestMergers()[0].apply(t, store.toolCtx, manifestFilename, "components:\n  - {name: Comp0, type: agent}\n  - {name: Comp1, type: workflow, status: validated}\n")

	if !strings.Contains(result.Message, `"Comp0" (no fields)`) || !strings.Contains(result.Message, `"Comp1" ("status")`) {
		t.Errorf("message = %q", result.Message)
	}
}

func TestManifestMerge_RefusalsDescribeTheHeldComponent(t *testing.T) {
	const manifest = "name: mesh\ncomponents:\n  - {name: Legacy, filename: l.yaml}\n  - {name: Wf, type: workflow, filename: w.yaml}\n  - {name: Ag, type: agent, filename: a.yaml}\n"
	cases := map[string]string{
		"a component with no type": "components:\n  - {name: Legacy, type: agent, filename: x.yaml}\n",
		"a workflow component":     "components:\n  - {name: Wf, type: agent, filename: x.yaml}\n",
		"an agent component":       "components:\n  - {name: Ag, type: workflow, filename: x.yaml}\n",
	}
	for want, fragment := range cases {
		t.Run(want, func(t *testing.T) {
			store := newManifestStore(t, manifest)

			result := manifestMergers()[0].apply(t, store.toolCtx, manifestFilename, fragment)

			if result.Status != tool.StatusError || !strings.Contains(result.Message, want) {
				t.Errorf("message = %q, want it to mention %q", result.Message, want)
			}
		})
	}
}

func TestManifestMerge_FragmentDocumentRules(t *testing.T) {
	merge := func(t *testing.T, artifactName, fragment string, toolCtx *tool.Context) *tool.Result {
		t.Helper()
		result, err := (&YAMLMergeArtifactTool{}).Execute(context.Background(), map[string]any{
			"artifact_name": artifactName, "yaml_fragment": fragment,
		}, toolCtx)
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	t.Run("an unparseable second document is a parse error", func(t *testing.T) {
		store := newManifestStore(t, fixtureManifest(numberedSpecs(2)))
		result := merge(t, manifestFilename, "components:\n  - {name: Comp0, status: validated}\n---\n: : [unclosed\n", store.toolCtx)
		if result.Status != tool.StatusError || !strings.Contains(result.Message, "failed to parse yaml_fragment") {
			t.Errorf("result = %q %q", result.Status, result.Message)
		}
	})
	t.Run("a second document with content is refused for any artifact", func(t *testing.T) {
		toolCtx, facade := newDataToolContext(t)
		saveDataToolArtifact(t, context.Background(), facade, "agent.yaml", []byte("name: a\nx: 1\n"), "application/x-yaml")
		result := merge(t, "agent.yaml", "x: 2\n---\ny: 3\n", toolCtx)
		if result.Status != tool.StatusError || !strings.Contains(result.Message, "single YAML document") {
			t.Errorf("result = %q %q", result.Status, result.Message)
		}
	})
	t.Run("a comment-only fragment is empty", func(t *testing.T) {
		store := newManifestStore(t, fixtureManifest(numberedSpecs(2)))
		result := merge(t, manifestFilename, "# nothing here\n", store.toolCtx)
		if result.Status != tool.StatusError || !strings.Contains(result.Message, "empty or invalid") {
			t.Errorf("result = %q %q", result.Status, result.Message)
		}
	})
	for name, fragment := range map[string]string{
		"a leading marker":     "---\ncomponents:\n  - {name: Comp0, status: validated}\n",
		"a trailing marker":    "components:\n  - {name: Comp0, status: validated}\n---\n",
		"trailing dots":        "components:\n  - {name: Comp0, status: validated}\n...\n",
		"a leading comment":    "# status update\ncomponents:\n  - {name: Comp0, status: validated}\n",
		"markers at both ends": "---\ncomponents:\n  - {name: Comp0, status: validated}\n---\n",
	} {
		t.Run(name+" is accepted", func(t *testing.T) {
			store := newManifestStore(t, fixtureManifest(numberedSpecs(2)))
			result := merge(t, manifestFilename, fragment, store.toolCtx)
			if result.Status == tool.StatusError {
				t.Errorf("a harmless document marker was refused: %s", result.Message)
			}
		})
	}
}

func TestManifestMerge_NonManifestKeepsYAMLFeatures(t *testing.T) {
	toolCtx, facade := newDataToolContext(t)
	saveDataToolArtifact(t, context.Background(), facade, "agent.yaml", []byte("name: a\nx: 1\n"), "application/x-yaml")

	result, err := (&YAMLMergeArtifactTool{}).Execute(context.Background(), map[string]any{
		"artifact_name": "agent.yaml",
		"yaml_fragment": "base: &b {k: v}\ncopy: *b\nzero: null\n",
	}, toolCtx)

	if err != nil || result.Status == tool.StatusError {
		t.Fatalf("a non-manifest merge with anchors and nulls was refused: %v %v", err, result)
	}
}

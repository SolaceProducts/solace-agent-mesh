// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"strings"
	"testing"
)

func TestValidateBuildManifest_ShapesTheCanvasCannotParse(t *testing.T) {
	manifest := func(root, component string) string {
		return "name: probe\ndescription: A manifest used to pin canvas-parse shapes.\n" + root +
			"components:\n  - filename: wf.yaml\n    type: workflow\n    name: Flow\n    description: A workflow.\n" + component
	}
	withoutFilename := "name: probe\ndescription: A manifest used to pin canvas-parse shapes.\n" +
		"components:\n  - type: workflow\n    name: Flow\n    description: A workflow.\n"
	withNumericFilename := "name: probe\ndescription: A manifest used to pin canvas-parse shapes.\n" +
		"components:\n  - filename: 42\n    type: workflow\n    name: Flow\n    description: A workflow.\n"

	for _, tc := range []struct{ name, root, component, wantPath string }{
		{"a workflow node with no id", "", "    nodes:\n      - type: agent\n        agent_name: A\n", "components[0].nodes[0].id"},
		{"a workflow node with no type", "", "    nodes:\n      - id: n1\n", "components[0].nodes[0].type"},
		{"a node id that is not a string", "", "    nodes:\n      - id: 7\n        type: agent\n", "components[0].nodes[0].id"},
		{"a component that omits filename entirely", "", "", "components[0].filename"},
		{"a filename that is not a string", "", "", "components[0].filename"},
		{"a node depends_on entry that is a mapping", "", "    nodes:\n      - id: n1\n        type: agent\n        depends_on:\n          - name: other\n", "components[0].nodes[0].depends_on[0]"},
		{"nodes that is not a list at all", "", "    nodes: notalist\n", "components[0].nodes"},
		{"a node that is not a mapping", "", "    nodes:\n      - just a string\n", "components[0].nodes[0]"},
		{"a risks entry that is a mapping", "risks:\n  - name: a risk\n", "", "risks[0]"},
		{"risks that is not a list at all", "risks: notalist\n", "", "risks"},
		{"an env var with no name", "env_vars:\n  - description: no name here\n", "", "env_vars[0].name"},
		{"an env var that is not a mapping", "env_vars:\n  - JUST_A_STRING\n", "", "env_vars[0]"},
		{"env_vars that is not a list at all", "env_vars: notalist\n", "", "env_vars"},
	} {
		t.Run(tc.name+" is refused", func(t *testing.T) {
			doc := manifest(tc.root, tc.component)
			switch tc.name {
			case "a component that omits filename entirely":
				doc = withoutFilename
			case "a filename that is not a string":
				doc = withNumericFilename
			}
			errs := validateManifest(doc)
			for _, e := range errs {
				if e.Path == tc.wantPath {
					return
				}
			}
			paths := make([]string, 0, len(errs))
			for _, e := range errs {
				paths = append(paths, e.Path)
			}
			t.Errorf("errors at %v, want one at %q.\n"+
				"Unreported, this manifest is stamped full_validation_passed while the canvas cannot parse it — so the UI keeps showing the previous build and Save applies stale components.",
				paths, tc.wantPath)
		})
	}

	for _, tc := range []struct{ name, root, component string }{
		{"an empty node id", "", "    nodes:\n      - id: \"\"\n        type: agent\n"},
		{"an empty node type", "", "    nodes:\n      - id: n1\n        type: \"\"\n"},
		{"an empty env var name", "env_vars:\n  - name: \"\"\n", ""},
	} {
		t.Run(tc.name+" is accepted, because the canvas accepts it", func(t *testing.T) {
			for _, e := range validateManifest(manifest(tc.root, tc.component)) {
				if strings.HasPrefix(e.Path, "components[0].nodes") || strings.HasPrefix(e.Path, "env_vars") {
					t.Errorf("refused %s: %s\nBuildManifestSchema declares these as z.string(), which accepts \"\" — refusing one blocks a manifest the canvas parses.", e.Path, e.Message)
				}
			}
		})
	}

	t.Run("and the same manifest, well formed, is accepted", func(t *testing.T) {
		errs := validateManifest(manifest(
			"risks:\n  - a plain sentence about a risk\nenv_vars:\n  - name: MY_VAR\n    description: A variable.\n",
			"    nodes:\n      - id: n1\n        type: agent\n        agent_name: A\n        depends_on: []\n",
		))
		if len(errs) != 0 {
			messages := make([]string, 0, len(errs))
			for _, e := range errs {
				messages = append(messages, e.Path+": "+e.Message)
			}
			t.Errorf("a well-formed manifest was refused:\n%s", strings.Join(messages, "\n"))
		}
	})
}

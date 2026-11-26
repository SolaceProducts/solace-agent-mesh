// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package settings

import (
	"strings"
	"testing"
)

func TestBuilderPrompt_ManifestMergeGuidance(t *testing.T) {
	prompt := flattenForClaimScan(string(builderAgentYAML)).text
	required := map[string]string{
		"the recipe merges at the root":               "call `yaml_merge_artifact` (no `path`) with a fragment that names the component and lists only the fields that change",
		"status recipe uses yaml_merge_artifact":      "use `yaml_merge_artifact` for it, not `merge_structured_data`",
		"the merge is by component name and type":     "the entry matches the component with the same `name` and `type` (an entry's `id` also finds it)",
		"unlisted fields and components are kept":     "every other field and every other component is kept",
		"list fields are replaced whole":              "is replaced whole, so give the complete list or leave the field out",
		"id and deployed_version are never written":   "a merge uses an entry's `id` only to find the component, never writes either field, and says so in its result",
		"a path with a dot is refused":                "a path with a dot (`components.2`) is refused",
		"a version suffix is refused":                 "and so is a numeric `:n` version suffix on the input of `merge_structured_data`",
		"anchors, tags, merge keys and documents":     "a yaml fragment with an anchor, an alias, an explicit tag, a `<<` key, a key that is not text (quote `1` or `true`) or a second `---` document is refused",
		"a fragment is written out in full":           "so write every value out in full",
		"id and deployed_version are not the model's": "`id` and `deployed_version` are never yours to write",
		"the result lists the changes":                "the tool result lists what was updated and added",
		"a merge can go to one top-level key":         "or at one top-level key",
		"new entries give name, type and filename":    "(`name`, `type` and `filename`, plus the rest of the entry)",
		"adding appends at the components path":       "`path: \"components\"` does the same",
		"a stub is refused":                           "an entry that matches nothing and lacks `type` or `filename` is refused, so a mistyped name in a status update is refused rather than saved as a stub",
		"merging never removes a component":           "merging never removes a component",
		"phase 3 uses the targeted merge":             "targeted `yaml_merge_artifact` merge from the manifest rules (a fragment that names only that component), never by re-emitting or replacing the list",
	}
	for name, sentence := range required {
		if !strings.Contains(prompt, sentence) {
			t.Errorf("%s: the Builder prompt lost %q", name, sentence)
		}
	}

	forbidden := map[string]string{
		"the old advice to merge at the components path only": "never merge a `components` array at the root level",
		"an element path in the recipe":                       "components.0",
		"a numbered element path":                             "components.n",
		"a merge that writes the ui's fields":                 "a merge writes them",
		"a merge that ignores an id":                          "a merge ignores them in an entry",
	}
	for name, sentence := range forbidden {
		if strings.Contains(prompt, sentence) {
			t.Errorf("%s: the Builder prompt still says %q", name, sentence)
		}
	}
}

// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package settings

import (
	"strings"
	"testing"
)

func TestBuilderPrompt_BlockedComponentGuidance(t *testing.T) {
	prompt := flattenForClaimScan(string(builderAgentYAML)).text
	required := map[string]string{
		"the status is in the schema":         "# validated | draft | error | deployed | blocked",
		"a reason is stated":                  "`blocked_reason`",
		"a blocked component stays listed":    "never remove a component, or detach it from the components that use it, because it cannot be built",
		"a smaller manifest reads as built":   "a smaller manifest reads to the ui as a finished build that never planned it",
		"validation refuses a dropped entry":  "`validate_build_manifest` refuses to validate a manifest that omits a component the stored one has",
		"no draft without the spec":           "a placeholder is not a draft",
		"do not ask me does not waive it":     "\"do not ask me about it\" do not make the value optional",
		"the stamp is withheld":               "does not stamp `full_validation_passed` while one exists",
		"the closing message names the block": "your closing message says what is blocked, the exact thing the user must provide, and that nothing new has been deployed",
		"never call it ready":                 "never call such a build ready, built, created or complete",
		"the way out":                         "when the user gives the value, write the component's config",
		"the plan shows it before approval":   "goes into the plan as `status: blocked` with a `blocked_reason`, so the plan card shows what is missing before the user approves",
		"the connector paragraph blocks":      "set the connector's manifest `status: blocked` with a `blocked_reason` that asks for the openapi document url",
		"continuity names the exception":      "a component you cannot build is not removed",
		"credentials never block":             "credentials and secrets still take `<<__sam_required__>>` and never block a component",
		"completion is withheld":              "when any component is `status: blocked`, the build is not complete",
	}
	for name, sentence := range required {
		if !strings.Contains(prompt, sentence) {
			t.Errorf("%s: the Builder prompt lost %q", name, sentence)
		}
	}
}

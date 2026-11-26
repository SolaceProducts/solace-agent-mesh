// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package settings

import (
	"strings"
	"testing"
)

func TestBuilderPrompt_RevalidateGuidance(t *testing.T) {
	prompt := flattenForClaimScan(string(builderAgentYAML)).text
	required := map[string]string{
		"an edit is followed by full validation": "then run `validate_build_manifest` with `full_validation: true` again",
		"the stamp records versions":             "the stamp records each component's version, so any edit to a component's config after it makes the build unvalidated until you run it again",
		"the ui treats a later edit as stale":    "the stamp records each component's version, and the ui treats a component edited after it as unvalidated",
		"the version stamp is never authored":    "never write `full_validation_passed` or `full_validation_versions` yourself",
		"an unstamped pass is validated again":   "if its result carries `full_validation_not_stamped`, validate again before you say the build is validated",
	}
	for name, sentence := range required {
		if !strings.Contains(prompt, sentence) {
			t.Errorf("%s: the Builder prompt lost %q", name, sentence)
		}
	}
}

func TestBuilderPrompt_PlatformCheckGuidance(t *testing.T) {
	prompt := flattenForClaimScan(string(builderAgentYAML)).text
	if strings.Contains(prompt, "your build is ready!") {
		t.Errorf("the Builder prompt still tells it to announce \"Your build is ready!\"")
	}
	required := map[string]string{
		"completion says validated, page decides": "say the build is validated; the page shows when it is ready to activate",
		"the check message is named":              "a message starting `[automated] platform check` means the platform would refuse this build",
		"each listed component is fixed":          "fix each listed component, then run `validate_build_manifest` with `full_validation: true` again",
		"validated again only after the fix":      "don't call the build validated again until that is done",
	}
	for name, sentence := range required {
		if !strings.Contains(prompt, sentence) {
			t.Errorf("%s: the Builder prompt lost %q", name, sentence)
		}
	}
}

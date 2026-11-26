// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package pptxtool

import (
	"strings"
	"testing"

	sdk "github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"
)

func TestInstructions_NameWhatTheActiveProfileAdds(t *testing.T) {
	if got := createPresentationDynamicSchema(nil); got == nil || !strings.Contains(got.Instructions, `"Neutral" is active`) || !strings.Contains(got.Instructions, "adds no logo or decorations") {
		t.Errorf("the default instructions do not say the neutral profile adds no logo or decorations:\n%s", lastChars(got))
	}
	if got := createPresentationDynamicSchema(map[string]any{"template_profile": "solace_2025"}); got == nil || !strings.Contains(got.Instructions, `"Solace 2025" is active`) || !strings.Contains(got.Instructions, "logo, decorations and footer") {
		t.Errorf("the solace_2025 instructions do not say it applies a logo:\n%s", lastChars(got))
	}
	if got := createPresentationDynamicSchema(map[string]any{"template_profile": "none"}); got != nil {
		t.Errorf("a deck with no profile still gets an active-profile note:\n%s", lastChars(got))
	}
}

func lastChars(o *sdk.SchemaOverride) string {
	if o == nil {
		return "<no override>"
	}
	const n = 400
	if s := o.Instructions; len(s) > n {
		return s[len(s)-n:]
	}
	return o.Instructions
}

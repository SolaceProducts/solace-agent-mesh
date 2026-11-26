// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import "testing"

func TestUnsetRenderFlagsFollowTheFileTools(t *testing.T) {
	cases := []struct {
		name                                      string
		readsFiles, displaysTemplates             bool
		wantTemplates, wantEmbeds, wantArtContent bool
	}{
		{"no file tools", false, true, false, false, false},
		{"file tools, headless caller", true, false, false, true, true},
		{"file tools, gateway shows templates", true, true, true, true, true},
	}
	for _, c := range cases {
		templates, embeds, _, artContent := renderSections(&AgentConfig{}, c.readsFiles, c.displaysTemplates)
		if templates != c.wantTemplates || embeds != c.wantEmbeds || artContent != c.wantArtContent {
			t.Errorf("%s: got templates=%v embeds=%v artifactContent=%v, want %v %v %v",
				c.name, templates, embeds, artContent, c.wantTemplates, c.wantEmbeds, c.wantArtContent)
		}
	}
}

func TestExplicitRenderFlagsWinOverTheFileTools(t *testing.T) {
	templates, embeds, _, artContent := renderSections(&AgentConfig{
		EnableTemplateInstructions:       new(false),
		EnableEmbedResolution:            new(false),
		EnableArtifactContentInstruction: new(false),
	}, true, true)
	if templates || embeds || artContent {
		t.Error("explicit false flags must win over the file tools")
	}
	templates, embeds, _, artContent = renderSections(&AgentConfig{
		EnableTemplateInstructions:       new(true),
		EnableEmbedResolution:            new(true),
		EnableArtifactContentInstruction: new(true),
	}, false, false)
	if !templates || !embeds || !artContent {
		t.Error("explicit true flags must win over the missing file tools")
	}
}

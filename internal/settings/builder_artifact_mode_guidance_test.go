// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package settings_test

import (
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/settings"
)

const artifactModeSection = "## 9. Artifact Handling Mode"

func TestBuilderGuidance_PointsAttachedFilesAtTheArtifactTools(t *testing.T) {
	docs := markdownDocs(t, "settings.SchemaSkillsFS", settings.SchemaSkillsFS(), "")
	skill := ""
	for name, body := range docs {
		if strings.HasSuffix(name, "sam-agent-schema/SKILL.md") {
			skill = body
		}
	}
	_, afterHeading, headingFound := strings.Cut(skill, artifactModeSection)
	section, _, endFound := strings.Cut(afterHeading, "### 9.1")
	if !headingFound || !endFound {
		t.Fatalf("sam-agent-schema has no %q section followed by 9.1", artifactModeSection)
	}
	section = strings.Join(strings.Fields(section), " ")
	prompt := strings.Join(strings.Fields(loadBuilderPrompt(t).Apps[0].AppConfig.Instruction), " ")
	_, fromKey, keyFound := strings.Cut(prompt, "**Saved agent keys**")
	promptParagraph, _, promptEndFound := strings.Cut(fromKey, "**Workflow config essential fields**")
	if !keyFound || !promptEndFound {
		t.Fatal("the Builder prompt has no saved-agent-keys paragraph followed by the workflow config heading")
	}

	for where, text := range map[string]string{"sam-agent-schema section 9": section, "the Builder prompt": promptParagraph} {
		if !strings.Contains(text, "`list_artifacts`") {
			t.Errorf("%s no longer says to use `list_artifacts` to open attached files", where)
		}
	}
}

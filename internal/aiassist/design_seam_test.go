// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package aiassist

import (
	"regexp"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/skill"
	"github.com/SolaceProducts/solace-agent-mesh/internal/skills/design"
)

var designSkillToken = regexp.MustCompile("`sam-[a-z0-9-]+`")

func TestDesignGuides_CLICopyNamesOnlyInstalledSkills(t *testing.T) {
	installed := map[string]bool{skill.SkillName: true}
	for _, name := range ConciergeSkills() {
		installed[name] = true
	}
	for _, sk := range design.Skills() {
		body, err := sk.Body(design.CLI)
		if err != nil {
			t.Fatalf("%s.Body(CLI): %v", sk.Name, err)
		}
		for _, m := range designSkillToken.FindAllString(proseOnly(string(body)), -1) {
			if name := strings.Trim(m, "`"); !installed[name] {
				t.Errorf("%s: CLI copy names %s, which the suite does not install", sk.Guide, name)
			}
		}
	}
}

func proseOnly(text string) string {
	var out strings.Builder
	inFence := false
	for line := range strings.SplitSeq(text, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			inFence = !inFence
			continue
		}
		if !inFence {
			out.WriteString(line)
			out.WriteByte('\n')
		}
	}
	return out.String()
}

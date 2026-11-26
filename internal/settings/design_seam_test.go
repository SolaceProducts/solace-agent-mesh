// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package settings

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/skills/design"
)

var designSkillToken = regexp.MustCompile("`sam-[a-z0-9-]+`")

func builderSkillCatalog(t *testing.T) map[string]bool {
	t.Helper()
	entries, err := fs.ReadDir(SchemaSkillsFS(), ".")
	if err != nil {
		t.Fatalf("read schema skills: %v", err)
	}
	catalog := map[string]bool{}
	for _, e := range entries {
		if e.IsDir() {
			catalog[e.Name()] = true
		}
	}
	for _, sk := range design.Skills() {
		catalog[sk.Name] = true
	}
	return catalog
}

func TestDesignGuides_BuilderCopyNamesOnlySkillsItCanLoad(t *testing.T) {
	catalog := builderSkillCatalog(t)
	for _, sk := range design.Skills() {
		body, err := sk.Body(design.Builder)
		if err != nil {
			t.Fatalf("%s.Body(Builder): %v", sk.Name, err)
		}
		for _, m := range designSkillToken.FindAllString(proseOnly(string(body)), -1) {
			if name := strings.Trim(m, "`"); !catalog[name] {
				t.Errorf("%s: Builder copy names %s, which is not a skill the Builder can load", sk.Guide, name)
			}
		}
	}
}

func TestDesignGuides_CLICopyNamesNoBuilderSkill(t *testing.T) {
	catalog := builderSkillCatalog(t)
	for _, sk := range design.Skills() {
		body, err := sk.Body(design.CLI)
		if err != nil {
			t.Fatalf("%s.Body(CLI): %v", sk.Name, err)
		}
		for _, m := range designSkillToken.FindAllString(proseOnly(string(body)), -1) {
			if name := strings.Trim(m, "`"); catalog[name] {
				t.Errorf("%s: CLI copy names Builder skill %s — the CLI reader cannot load it; point at the sam-declarative-config reference instead", sk.Guide, name)
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

// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package skill

import (
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/skills/design"
)

var designReferencePath = regexp.MustCompile(`references/[A-Za-z0-9_./-]+\.md`)

func TestDesignGuides_CLICopyReferencePathsResolve(t *testing.T) {
	for _, sk := range design.Skills() {
		body, err := sk.Body(design.CLI)
		if err != nil {
			t.Fatalf("%s.Body(CLI): %v", sk.Name, err)
		}
		for _, m := range designReferencePath.FindAllString(proseOnly(string(body)), -1) {
			if rel := strings.TrimPrefix(m, "references/"); !slices.Contains(expectedReferences, rel) {
				t.Errorf("%s: CLI copy points at %s, which sam-declarative-config does not install", sk.Guide, m)
			}
		}
	}
}

func TestDesignGuides_BuilderCopyNamesNoCLIReferencePath(t *testing.T) {
	for _, sk := range design.Skills() {
		body, err := sk.Body(design.Builder)
		if err != nil {
			t.Fatalf("%s.Body(Builder): %v", sk.Name, err)
		}
		prose := proseOnly(string(body))
		for _, rel := range expectedReferences {
			if p := "references/" + rel; strings.Contains(prose, p) {
				t.Errorf("%s: Builder copy names %s, a file only the CLI reader has — move it to cli/%s.md", sk.Guide, p, sk.Guide)
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

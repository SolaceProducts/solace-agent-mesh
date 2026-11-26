// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package aiassist

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"
)

var auditForbidden = []*regexp.Regexp{
	regexp.MustCompile(`\binternal/`),
	regexp.MustCompile(`\bcmd/`),
	regexp.MustCompile(`\bexamples/`),
	regexp.MustCompile(`\bwebui/`),
	regexp.MustCompile(`\bpkg/`),
	regexp.MustCompile(`dev-docs`),
	regexp.MustCompile(`\bdocs/docs`),
	regexp.MustCompile(`github\.com/SolaceDev`),
	regexp.MustCompile(`(?i)\bsam-go\b`),
}

var auditAllowed = []string{"pkg/samtoolsdk", ".claude/skills"}

func coveredByAllowed(line string, start, end int) bool {
	for _, a := range auditAllowed {
		from := 0
		for {
			idx := strings.Index(line[from:], a)
			if idx < 0 {
				break
			}
			aStart := from + idx
			aEnd := aStart + len(a)
			if start >= aStart && end <= aEnd {
				return true
			}
			from = aStart + 1
		}
	}
	return false
}

func TestConciergeSuiteContentAudit(t *testing.T) {
	f := ConciergeFS()
	err := fs.WalkDir(f, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".md") {
			return err
		}
		data, readErr := fs.ReadFile(f, p)
		if readErr != nil {
			return readErr
		}
		for i, line := range strings.Split(string(data), "\n") {
			for _, re := range auditForbidden {
				for _, loc := range re.FindAllStringIndex(line, -1) {
					if coveredByAllowed(line, loc[0], loc[1]) {
						continue
					}
					t.Errorf("%s:%d source-tree reference %q — customer-facing skills must not contain source-tree paths/private-repo links: %s",
						p, i+1, line[loc[0]:loc[1]], strings.TrimSpace(line))
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

var (
	inlineCodeRe = regexp.MustCompile("`[^`]*`")
	samEnvRe     = regexp.MustCompile(`SAM_[A-Z0-9_]+`)
	bareSAMRe    = regexp.MustCompile(`\bSAM\b`)
)

func bareSAMLines(body string) []int {
	var out []int
	inFence := false
	for i, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			inFence = !inFence
			continue
		}
		if inFence || !strings.Contains(line, "SAM") {
			continue
		}
		stripped := samEnvRe.ReplaceAllString(inlineCodeRe.ReplaceAllString(line, ""), "")
		if bareSAMRe.MatchString(stripped) {
			out = append(out, i+1)
		}
	}
	return out
}

func TestConciergeSuiteUsesNoBareSAMAcronym(t *testing.T) {
	f := ConciergeFS()
	err := fs.WalkDir(f, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".md") {
			return err
		}
		data, readErr := fs.ReadFile(f, p)
		if readErr != nil {
			return readErr
		}
		for _, line := range bareSAMLines(string(data)) {
			t.Errorf(`%s:%d uses the bare acronym "SAM" — customer prose says "Solace Agent Mesh" on first reference and "Agent Mesh" after`, p, line)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

var featureFlagRe = regexp.MustCompile(`SAM_FEATURE_[A-Z0-9_]+`)

func TestSuiteNamesNoFeatureFlags(t *testing.T) {
	forEachSkillLine(t, func(path string, lineNo int, line string) {
		if strings.HasPrefix(path, "design/builder/") {
			return
		}
		for _, flag := range featureFlagRe.FindAllString(line, -1) {
			t.Errorf("%s:%d names feature flag %s — customer-facing skills describe shipped behaviour, not rollout switches", path, lineNo, flag)
		}
	})
}

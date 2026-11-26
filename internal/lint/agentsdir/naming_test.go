// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agentsdir

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

const agentsRoot = "../../../.agents"

var lowercaseHyphenated = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

var namesFixedByOtherTools = map[string]bool{
	"README.md": true,
	"AGENTS.md": true,
	".gitkeep":  true,
}

func nameProblem(rel string, isDir bool) string {
	name := rel[strings.LastIndex(rel, "/")+1:]
	if isDir {
		if !lowercaseHyphenated.MatchString(name) {
			return "folder names are lowercase words joined by hyphens"
		}
		return ""
	}
	if namesFixedByOtherTools[name] {
		return ""
	}
	stem, isMarkdown := strings.CutSuffix(name, ".md")
	if !isMarkdown {
		return "only .md files belong here"
	}
	if !lowercaseHyphenated.MatchString(stem) {
		return "file names are lowercase words joined by hyphens, ending in .md"
	}
	return ""
}

func isSkillFolder(rel string) bool {
	parts := strings.Split(rel, "/")
	return len(parts) == 2 && parts[0] == "skills"
}

func TestNameProblem(t *testing.T) {
	cases := []struct {
		rel   string
		isDir bool
		ok    bool
	}{
		{"rules", true, true},
		{"rules/backend", true, true},
		{"rules/backend/go.md", false, true},
		{"rules/authoring/writing-rules.md", false, true},
		{"rules/backend/api-standards-v2.md", false, true},
		{"skills/dev-up", true, true},
		{"rules/security/rbac-scopes.md", false, true},
		{"README.md", false, true},
		{"rules/backend/.gitkeep", false, true},

		{"rules/Backend", true, false},
		{"rules/back_end", true, false},
		{"skills/devUp", true, false},
		{"skills/dev--up", true, false},
		{"skills/-dev-up", true, false},
		{"rules/backend/Go.md", false, false},
		{"rules/backend/api_standards.md", false, false},
		{"rules/backend/api standards.md", false, false},
		{"rules/backend/go.MD", false, false},
		{"rules/backend/go.txt", false, false},
		{"rules/security/rbac-scopes-.md", false, false},
	}
	for _, c := range cases {
		problem := nameProblem(c.rel, c.isDir)
		if c.ok && problem != "" {
			t.Errorf("%s: want accepted, got %q", c.rel, problem)
		}
		if !c.ok && problem == "" {
			t.Errorf("%s: want rejected, got accepted", c.rel)
		}
	}
}

func TestAgentsTreeNaming(t *testing.T) {
	if _, err := os.Stat(agentsRoot); os.IsNotExist(err) {
		t.Skip(".agents is not part of the published source tree")
	}
	err := filepath.WalkDir(agentsRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil || path == agentsRoot {
			return err
		}
		rel, err := filepath.Rel(agentsRoot, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if problem := nameProblem(rel, d.IsDir()); problem != "" {
			t.Errorf(".agents/%s: %s", rel, problem)
		}
		if d.IsDir() && isSkillFolder(rel) {
			return fs.SkipDir
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", agentsRoot, err)
	}
}

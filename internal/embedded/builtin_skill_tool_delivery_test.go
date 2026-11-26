// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build !sam_lean

package embedded

import (
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/builtinskills"
	"github.com/SolaceProducts/solace-agent-mesh/internal/testing/sourcetree"
	"gopkg.in/yaml.v3"
)

type skillToolManifest struct {
	Tools map[string]struct {
		Runtime    string `yaml:"runtime"`
		Executable string `yaml:"executable"`
		ToolDir    string `yaml:"tool_dir"`
	} `yaml:"tools"`
}

type goSkillTool struct {
	skill      string
	toolName   string
	binaryPath string
}

func discoverGoSkillTools(t *testing.T) []goSkillTool {
	t.Helper()
	var out []goSkillTool
	for _, skill := range builtinskills.Names() {
		data, err := fs.ReadFile(builtinskills.FS(), path.Join(skill, "tools", "manifest.yaml"))
		if err != nil {
			continue
		}
		var m skillToolManifest
		if err := yaml.Unmarshal(data, &m); err != nil {
			t.Fatalf("parse %s tools/manifest.yaml: %v", skill, err)
		}
		for toolName, entry := range m.Tools {
			if entry.Runtime != "go" {
				continue
			}
			dir := entry.ToolDir
			if dir == "" {
				dir = entry.Executable
			}
			out = append(out, goSkillTool{
				skill:      skill,
				toolName:   toolName,
				binaryPath: path.Join(skill, "tools", dir, entry.Executable),
			})
		}
	}
	if len(out) == 0 {
		t.Fatal("no runtime: go skill-bundled tools discovered — the walk is broken, not the tree")
	}
	sort.Slice(out, func(i, j int) bool { return out[i].binaryPath < out[j].binaryPath })
	return out
}

func readRepoFile(t *testing.T, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(data)
}

func TestBuiltinSkillGoTools_BuiltInContainerImages(t *testing.T) {
	tools := discoverGoSkillTools(t)
	for _, dockerfile := range []string{"Dockerfile", "internal/container/Dockerfile.str"} {
		content := readRepoFile(t, dockerfile)
		for _, tool := range tools {
			if !strings.Contains(content, tool.binaryPath) {
				t.Errorf("%s has no build for skill-bundled Go tool %q.\n"+
					"Expected the output path %q to appear in a `go build -o` line.\n"+
					"Without it the image ships %s/SKILL.md and tools/manifest.yaml but no executable, "+
					"so load_skill succeeds and the tool then fails discovery.",
					dockerfile, tool.toolName, tool.binaryPath, tool.skill)
			}
		}
	}
}

func TestBuiltinSkillGoTools_RegisteredInProcess(t *testing.T) {
	registered := make(map[string]bool)
	for _, def := range inProcessToolDefs() {
		registered[def.Name()] = true
	}
	for _, tool := range discoverGoSkillTools(t) {
		if !registered[tool.toolName] {
			t.Errorf("skill-bundled Go tool %q (%s) is not registered in inProcessToolDefs().\n"+
				"Add <pkg>.Tools() to inprocess_tools.go, or builtin/embedded mode execs the "+
				"embedded per-arch binary and fails with exec format error on a cross-arch build.",
				tool.toolName, tool.skill)
		}
	}
}

var buildSkillTargetRE = regexp.MustCompile(`build-[a-z0-9-]+-skill`)

func TestBuildCLIAssetsWorkflow_MirrorsMakefileSkillTargets(t *testing.T) {
	sourcetree.RequireInternal(t)
	makefile := readRepoFile(t, "Makefile")

	var prereqs string
	for _, line := range strings.Split(makefile, "\n") {
		if strings.HasPrefix(line, "build-cli:") {
			prereqs = line
			break
		}
	}
	if prereqs == "" {
		t.Fatal(`no "build-cli:" rule found in Makefile — this test needs updating, not the Makefile`)
	}

	want := uniqueSorted(buildSkillTargetRE.FindAllString(prereqs, -1))

	var runLine string
	for _, line := range strings.Split(readRepoFile(t, ".github/workflows/build-cli-assets.yaml"), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "run:") && strings.Contains(trimmed, "build-test-harness-skill") {
			runLine = trimmed
			break
		}
	}
	if runLine == "" {
		t.Fatal(`no "run:" line building the skill binaries found in build-cli-assets.yaml`)
	}
	got := uniqueSorted(buildSkillTargetRE.FindAllString(runLine, -1))

	if strings.Join(want, ",") != strings.Join(got, ",") {
		t.Errorf("build-cli skill targets and .github/workflows/build-cli-assets.yaml have diverged.\n"+
			"Makefile build-cli: %v\nbuild-cli-assets.yaml: %v\n"+
			"The workflow step claims to mirror build-cli's deps; released binaries embed whatever it builds.",
			want, got)
	}
}

func uniqueSorted(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package skillinit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveSkillDir(t *testing.T) {
	root := t.TempDir()
	cases := map[string]string{
		root:                                 filepath.Join(root, "skills", "sum"),
		filepath.Join(root, "skills"):        filepath.Join(root, "skills", "sum"),
		filepath.Join(root, "skills", "sum"): filepath.Join(root, "skills", "sum"),
	}
	for in, want := range cases {
		got, err := ResolveSkillDir("sum", in)
		if err != nil {
			t.Fatalf("ResolveSkillDir(%q): %v", in, err)
		}
		if got != want {
			t.Errorf("ResolveSkillDir(sum, %q) = %q; want %q", in, got, want)
		}
	}
}

func TestInit_ScaffoldsSkillMDAndReferences(t *testing.T) {
	root := t.TempDir()
	res, err := Init(Options{Name: "summarizer", Path: root, CLIVersion: "1.2.3"})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}

	wantDir := filepath.Join(root, "skills", "summarizer")
	if res.SkillDir != wantDir {
		t.Errorf("SkillDir = %q; want %q", res.SkillDir, wantDir)
	}

	body, err := os.ReadFile(res.SkillMDPath)
	if err != nil {
		t.Fatalf("read SKILL.md: %v", err)
	}
	for _, want := range []string{"name: summarizer", "# summarizer"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("SKILL.md missing %q:\n%s", want, body)
		}
	}
	if strings.Contains(string(body), "version:") {
		t.Errorf("SKILL.md must not carry a version field:\n%s", body)
	}

	if fi, err := os.Stat(res.ReferencesDir); err != nil || !fi.IsDir() {
		t.Errorf("references dir missing: %v", err)
	}

	if _, err := os.Stat(filepath.Join(wantDir, "tools")); !os.IsNotExist(err) {
		t.Errorf("tools/ should not exist without WithTool, stat err = %v", err)
	}
	if res.ToolDir != "" {
		t.Errorf("ToolDir = %q; want empty without WithTool", res.ToolDir)
	}
}

func TestInit_WithToolScaffoldsBuildableGoTool(t *testing.T) {
	root := t.TempDir()
	res, err := Init(Options{Name: "greeter", Path: root, WithTool: true, CLIVersion: "9.9.9"})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}

	skillDir := filepath.Join(root, "skills", "greeter")
	manifest := filepath.Join(skillDir, "tools", "manifest.yaml")
	mBody, err := os.ReadFile(manifest)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	for _, want := range []string{"runtime: go", "tool_dir: greeter", "executable: greeter"} {
		if !strings.Contains(string(mBody), want) {
			t.Errorf("manifest missing %q:\n%s", want, mBody)
		}
	}

	if res.ToolDir == "" {
		t.Fatal("ToolDir should be set with WithTool")
	}
	for _, f := range []string{"main.go", "go.mod"} {
		if _, statErr := os.Stat(filepath.Join(res.ToolDir, f)); statErr != nil {
			t.Errorf("expected %s in tool dir: %v", f, statErr)
		}
	}
	goMod, err := os.ReadFile(filepath.Join(res.ToolDir, "go.mod"))
	if err != nil {
		t.Fatalf("read go.mod: %v", err)
	}
	if !strings.Contains(string(goMod), "replace github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk") {
		t.Errorf("go.mod missing SDK replace directive:\n%s", goMod)
	}
	if !res.SDKInjected {
		t.Error("expected SDKInjected = true with WithTool")
	}
}

func TestInit_WithToolPythonScaffoldsPyTool(t *testing.T) {
	root := t.TempDir()
	res, err := Init(Options{Name: "greeter", Path: root, WithTool: true, Lang: LangPython, CLIVersion: "9.9.9"})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}

	skillDir := filepath.Join(root, "skills", "greeter")
	mBody, err := os.ReadFile(filepath.Join(skillDir, "tools", "manifest.yaml"))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	for _, want := range []string{"runtime: python", "tool_dir: greeter", "executable: python/bin/greeter"} {
		if !strings.Contains(string(mBody), want) {
			t.Errorf("manifest missing %q:\n%s", want, mBody)
		}
	}

	if res.ToolDir == "" {
		t.Fatal("ToolDir should be set with WithTool")
	}
	for _, f := range []string{"pyproject.toml", filepath.Join("src", "greeter", "__init__.py"), filepath.Join("src", "greeter", "__main__.py")} {
		if _, statErr := os.Stat(filepath.Join(res.ToolDir, f)); statErr != nil {
			t.Errorf("expected %s in tool dir: %v", f, statErr)
		}
	}
	if _, statErr := os.Stat(filepath.Join(res.ToolDir, "go.mod")); !os.IsNotExist(statErr) {
		t.Errorf("Python tool must not have a go.mod, stat err = %v", statErr)
	}
	pyproj, err := os.ReadFile(filepath.Join(res.ToolDir, "pyproject.toml"))
	if err != nil {
		t.Fatalf("read pyproject.toml: %v", err)
	}
	for _, want := range []string{`"sam-tool-sdk>=0.1,<0.2"`, "[project.scripts]", `greeter = "greeter:cli"`} {
		if !strings.Contains(string(pyproj), want) {
			t.Errorf("pyproject.toml missing %q:\n%s", want, pyproj)
		}
	}
	if res.SDKInjected {
		t.Error("SDKInjected must be false for a Python tool (sam-tool-sdk comes from PyPI)")
	}
}

func TestInit_RejectsUnknownLang(t *testing.T) {
	_, err := Init(Options{Name: "x", Path: t.TempDir(), WithTool: true, Lang: "rust"})
	if err == nil || !strings.Contains(err.Error(), "--lang") {
		t.Errorf("Init(lang=rust) err = %v; want a --lang error", err)
	}
}

func TestInit_RefusesNonEmptyWithoutForce(t *testing.T) {
	root := t.TempDir()
	if _, err := Init(Options{Name: "dup", Path: root}); err != nil {
		t.Fatalf("first Init: %v", err)
	}
	_, err := Init(Options{Name: "dup", Path: root})
	if err == nil || !strings.Contains(err.Error(), "exists and is not empty") {
		t.Errorf("second Init err = %v; want 'exists and is not empty'", err)
	}
}

func TestInit_ForceOverwrites(t *testing.T) {
	root := t.TempDir()
	if _, err := Init(Options{Name: "dup", Path: root}); err != nil {
		t.Fatalf("first Init: %v", err)
	}
	stale := filepath.Join(root, "skills", "dup", "stale.txt")
	if err := os.WriteFile(stale, []byte("x"), 0o644); err != nil {
		t.Fatalf("plant stale: %v", err)
	}
	if _, err := Init(Options{Name: "dup", Path: root, Force: true}); err != nil {
		t.Fatalf("force Init: %v", err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("stale file should be gone after --force, stat err = %v", err)
	}
}

func TestInit_RejectsBadNames(t *testing.T) {
	for _, name := range []string{"", "../escape", "a/b", "."} {
		if _, err := Init(Options{Name: name, Path: t.TempDir()}); err == nil {
			t.Errorf("Init(name=%q) should error", name)
		}
	}
}

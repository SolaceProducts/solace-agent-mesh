// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package samcli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/samcli/skillinit"
)

func writeValidatableSkill(t *testing.T, name, schemaJSON string) (repoRoot string) {
	t.Helper()
	repoRoot = t.TempDir()
	skillDir := filepath.Join(repoRoot, "skills", name)
	toolDir := filepath.Join(skillDir, "tools", name)
	if err := os.MkdirAll(toolDir, 0o755); err != nil {
		t.Fatalf("mkdir tool dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"),
		[]byte("---\nname: "+name+"\ndescription: test skill\n---\n# "+name+"\n"), 0o644); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}
	writeManifest(t, filepath.Join(skillDir, "tools"),
		"version: 1\ntools:\n  "+name+"_greet:\n    runtime: go\n    tool_dir: "+name+"\n    executable: "+name+"\n")
	writeFakeTool(t, toolDir, name, "#!/bin/sh\necho '"+schemaJSON+"'\n")
	return repoRoot
}

func TestValidateSkillDiscovery_Success(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake tool is a POSIX shell script")
	}
	skillDir := t.TempDir()
	toolDir := filepath.Join(skillDir, "tools", "greeter")
	if err := os.MkdirAll(toolDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFakeTool(t, toolDir, "greeter",
		"#!/bin/sh\necho '{\"tools\":{\"greeter_greet\":{\"description\":\"Greets the named person\"}}}'\n")
	writeManifest(t, filepath.Join(skillDir, "tools"),
		"version: 1\ntools:\n  greeter_greet:\n    runtime: go\n    tool_dir: greeter\n    executable: greeter\n")

	var out bytes.Buffer
	manifestPath := filepath.Join(skillDir, "tools", "manifest.yaml")
	if err := validateSkillDiscovery(&out, "greeter", skillDir, "", nil, manifestPath); err != nil {
		t.Fatalf("validateSkillDiscovery: %v", err)
	}
	s := out.String()
	for _, want := range []string{"✓ greeter_greet", "exposed as greeter__greeter_greet", "Greets the named person", "greeter OK"} {
		if !strings.Contains(s, want) {
			t.Errorf("output missing %q; got %q", want, s)
		}
	}
}

func TestValidateSkillDiscovery_SoleToolFallback(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake tool is a POSIX shell script")
	}
	skillDir := t.TempDir()
	toolDir := filepath.Join(skillDir, "tools", "greeter")
	if err := os.MkdirAll(toolDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFakeTool(t, toolDir, "greeter",
		"#!/bin/sh\necho '{\"tools\":{\"some_other_name\":{\"description\":\"x\"}}}'\n")
	writeManifest(t, filepath.Join(skillDir, "tools"),
		"version: 1\ntools:\n  greeter_greet:\n    runtime: go\n    tool_dir: greeter\n    executable: greeter\n")

	var out bytes.Buffer
	manifestPath := filepath.Join(skillDir, "tools", "manifest.yaml")
	if err := validateSkillDiscovery(&out, "greeter", skillDir, "", nil, manifestPath); err != nil {
		t.Fatalf("validateSkillDiscovery (sole-tool fallback): %v", err)
	}
	if s := out.String(); !strings.Contains(s, "✓ greeter_greet") {
		t.Errorf("sole-tool fallback should pass; got %q", s)
	}
}

func TestValidateSkillDiscovery_KeyMismatchFails(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake tool is a POSIX shell script")
	}
	skillDir := t.TempDir()
	toolDir := filepath.Join(skillDir, "tools", "greeter")
	if err := os.MkdirAll(toolDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFakeTool(t, toolDir, "greeter",
		"#!/bin/sh\necho '{\"tools\":{\"foo\":{\"description\":\"a\"},\"bar\":{\"description\":\"b\"}}}'\n")
	writeManifest(t, filepath.Join(skillDir, "tools"),
		"version: 1\ntools:\n  greeter_greet:\n    runtime: go\n    tool_dir: greeter\n    executable: greeter\n")

	var out bytes.Buffer
	manifestPath := filepath.Join(skillDir, "tools", "manifest.yaml")
	err := validateSkillDiscovery(&out, "greeter", skillDir, "", nil, manifestPath)
	if err == nil || !strings.Contains(err.Error(), "failed discovery") {
		t.Fatalf("err = %v; want a 'failed discovery' error", err)
	}
	if s := out.String(); !strings.Contains(s, "matches none of the 2 registered tool(s)") {
		t.Errorf("output missing key-mismatch marker; got %q", s)
	}
}

func TestValidateSkillDiscovery_FailureExitsNonZero(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake tool is a POSIX shell script")
	}
	skillDir := t.TempDir()
	toolDir := filepath.Join(skillDir, "tools", "broken")
	if err := os.MkdirAll(toolDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFakeTool(t, toolDir, "broken", "#!/bin/sh\necho 'boom' >&2\nexit 1\n")
	writeManifest(t, filepath.Join(skillDir, "tools"),
		"version: 1\ntools:\n  broken_greet:\n    runtime: go\n    tool_dir: broken\n    executable: broken\n")

	var out bytes.Buffer
	manifestPath := filepath.Join(skillDir, "tools", "manifest.yaml")
	err := validateSkillDiscovery(&out, "broken", skillDir, "", nil, manifestPath)
	if err == nil || !strings.Contains(err.Error(), "failed discovery") {
		t.Fatalf("err = %v; want 'failed discovery'", err)
	}
	if s := out.String(); !strings.Contains(s, "✗ broken_greet") {
		t.Errorf("output missing failure marker; got %q", s)
	}
}

func TestValidateSkillDiscovery_OverlayBinaryProbed(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake tool is a POSIX shell script")
	}
	skillDir := t.TempDir()
	builtDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(skillDir, "tools"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeManifest(t, filepath.Join(skillDir, "tools"),
		"version: 1\ntools:\n  greeter_greet:\n    runtime: go\n    tool_dir: greeter\n    executable: greeter\n")
	builtToolDir := filepath.Join(builtDir, "tools", "greeter")
	if err := os.MkdirAll(builtToolDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFakeTool(t, builtToolDir, "greeter",
		"#!/bin/sh\necho '{\"tools\":{\"greeter_greet\":{\"description\":\"built\"}}}'\n")

	overlay := map[string][]byte{"tools/greeter/greeter": []byte("x")}
	var out bytes.Buffer
	manifestPath := filepath.Join(skillDir, "tools", "manifest.yaml")
	if err := validateSkillDiscovery(&out, "greeter", skillDir, builtDir, overlay, manifestPath); err != nil {
		t.Fatalf("validateSkillDiscovery (overlay): %v", err)
	}
	if s := out.String(); !strings.Contains(s, "✓ greeter_greet") {
		t.Errorf("overlay binary should be probed; got %q", s)
	}
}

func TestValidateSkillDiscovery_PythonToolPythonPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake tool is a POSIX shell script")
	}
	skillDir := t.TempDir()
	binDir := filepath.Join(skillDir, "tools", "greeter", "python", "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFakeTool(t, binDir, "greeter",
		`#!/bin/sh
echo "{\"tools\":{\"greeter_greet\":{\"description\":\"PYTHONPATH=$PYTHONPATH\"}}}"
`)
	writeManifest(t, filepath.Join(skillDir, "tools"),
		"version: 1\ntools:\n  greeter_greet:\n    runtime: python\n    tool_dir: greeter\n    executable: python/bin/greeter\n")

	var out bytes.Buffer
	manifestPath := filepath.Join(skillDir, "tools", "manifest.yaml")
	if err := validateSkillDiscovery(&out, "greeter", skillDir, "", nil, manifestPath); err != nil {
		t.Fatalf("validateSkillDiscovery (python): %v\n%s", err, out.String())
	}
	s := out.String()
	if !strings.Contains(s, "exposed as greeter__greeter_greet") {
		t.Errorf("python tool should discover; got %q", s)
	}
	wantLayer := filepath.Join(skillDir, "tools", "greeter", "python") + ":"
	if !strings.Contains(s, wantLayer) {
		t.Errorf("PYTHONPATH should contain the python layer %q; got %q", wantLayer, s)
	}
	if strings.Contains(s, "/python/bin") {
		t.Errorf("PYTHONPATH must not point at python/bin (pre-fix bug); got %q", s)
	}
}

func TestRunSkillValidate_MdOnly(t *testing.T) {
	repoRoot, _ := writeSkill(t, "notes")

	cmd := newSkillValidateCmd()
	var out, errb bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errb)
	if err := runSkillValidate(context.Background(), cmd, "notes", repoRoot); err != nil {
		t.Fatalf("runSkillValidate: %v", err)
	}
	if s := out.String(); !strings.Contains(s, "no bundled tools to discover") || !strings.Contains(s, "notes OK") {
		t.Errorf("md-only output unexpected; got %q", s)
	}
}

func TestRunSkillValidate_MissingSkillMd(t *testing.T) {
	cmd := newSkillValidateCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	err := runSkillValidate(context.Background(), cmd, "ghost", t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "no readable SKILL.md") {
		t.Errorf("err = %v; want 'no readable SKILL.md'", err)
	}
}

func TestRunSkillValidate_NoNameFrontmatter(t *testing.T) {
	repoRoot := t.TempDir()
	skillDir := filepath.Join(repoRoot, "skills", "anon")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"),
		[]byte("---\ndescription: no name here\n---\n# body\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := newSkillValidateCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	err := runSkillValidate(context.Background(), cmd, "anon", repoRoot)
	if err == nil || !strings.Contains(err.Error(), "no 'name' field") {
		t.Errorf("err = %v; want \"no 'name' field\"", err)
	}
}

func TestRunSkillValidate_StreamSeparation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake tool is a POSIX shell script")
	}
	repoRoot := writeValidatableSkill(t, "greeter",
		`{"tools":{"greeter_greet":{"description":"Greets the named person"}}}`)

	cmd := newSkillValidateCmd()
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"greeter", repoRoot})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	if so := stdout.String(); !strings.Contains(so, "✓ greeter_greet") || !strings.Contains(so, "greeter OK") {
		t.Errorf("stdout should carry the result; got %q", so)
	}
	if strings.Contains(stdout.String(), "building greeter") || strings.Contains(stdout.String(), "validating skill") {
		t.Errorf("stdout must not carry progress; got %q", stdout.String())
	}
	if se := stderr.String(); !strings.Contains(se, "validating skill greeter") {
		t.Errorf("stderr should carry progress; got %q", se)
	}
}

func TestReadSkillManifestEntries(t *testing.T) {
	dir := t.TempDir()
	mapping := filepath.Join(dir, "map.yaml")
	if err := os.WriteFile(mapping,
		[]byte("tools:\n  greet:\n    runtime: go\n    tool_dir: g\n    executable: g\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	entries, err := readSkillManifestEntries(mapping)
	if err != nil {
		t.Fatalf("readSkillManifestEntries(mapping): %v", err)
	}
	if got, ok := entries["greet"]; !ok || got.ToolDir != "g" || got.Executable != "g" || got.Runtime != "go" {
		t.Errorf("entry = %+v (ok=%v); want {go, g, g}", got, ok)
	}

	list := filepath.Join(dir, "list.yaml")
	if err = os.WriteFile(list, []byte("tools: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	entries, err = readSkillManifestEntries(list)
	if err != nil {
		t.Fatalf("readSkillManifestEntries(list): %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("list form entries = %v; want empty (no parse error)", entries)
	}
}

func TestSkillToolRelPath(t *testing.T) {
	cases := []struct {
		toolDir, executable, key, want string
	}{
		{"greeter", "greeter", "greeter_greet", "tools/greeter/greeter"},
		{"", "greeter", "greeter_greet", "tools/greeter_greet/greeter"},
		{"greeter", "", "greeter_greet", "tools/greeter/greeter_greet"},
		{"", "", "greet", "tools/greet/greet"},
	}
	for _, c := range cases {
		if got := skillToolRelPath(c.toolDir, c.executable, c.key); got != c.want {
			t.Errorf("skillToolRelPath(%q,%q,%q) = %q; want %q", c.toolDir, c.executable, c.key, got, c.want)
		}
	}
}

func TestRunSkillValidate_BundledTool(t *testing.T) {
	if testing.Short() {
		t.Skip("requires go build")
	}
	repoRoot := t.TempDir()
	if _, err := skillinit.Init(skillinit.Options{Name: "greeter", Path: repoRoot, WithTool: true, CLIVersion: "test"}); err != nil {
		t.Fatalf("scaffold skill: %v", err)
	}

	cmd := newSkillValidateCmd()
	var out, errb bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errb)
	if err := runSkillValidate(context.Background(), cmd, "greeter", repoRoot); err != nil {
		t.Fatalf("runSkillValidate: %v\nstdout: %s\nstderr: %s", err, out.String(), errb.String())
	}
	if s := out.String(); !strings.Contains(s, "exposed as greeter__greeter_greet") || !strings.Contains(s, "greeter OK") {
		t.Errorf("output missing discovery; got %q", s)
	}
}

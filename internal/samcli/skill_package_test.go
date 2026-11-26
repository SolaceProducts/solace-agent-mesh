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
	"strings"
	"testing"
)

func writeSkill(t *testing.T, name string) (repoRoot, skillDir string) {
	t.Helper()
	repoRoot = t.TempDir()
	skillDir = filepath.Join(repoRoot, "skills", name)
	if err := os.MkdirAll(filepath.Join(skillDir, "references"), 0o755); err != nil {
		t.Fatalf("mkdir skill: %v", err)
	}
	for rel, body := range map[string]string{
		"SKILL.md":            "---\nname: " + name + "\ndescription: test skill\n---\n# " + name + "\n",
		"references/guide.md": "reference body",
	} {
		if err := os.WriteFile(filepath.Join(skillDir, rel), []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	return repoRoot, skillDir
}

func TestRunSkillPackage_BundlesDir(t *testing.T) {
	t.Setenv("SAM_TOOL_TARGET_OS", "linux")
	t.Setenv("SAM_TOOL_TARGET_ARCH", "arm64")
	repoRoot, _ := writeSkill(t, "summarizer")
	outZip := filepath.Join(t.TempDir(), "summarizer.zip")

	cmd, out := newPackageCmdForTest()
	if err := runSkillPackage(context.Background(), cmd, "summarizer", repoRoot, "", "", outZip); err != nil {
		t.Fatalf("runSkillPackage: %v", err)
	}

	if _, err := os.Stat(outZip); err != nil {
		t.Fatalf("expected zip at %s: %v", outZip, err)
	}
	entries := zipEntries(t, outZip)
	if !entries["SKILL.md"] || !entries["references/guide.md"] {
		t.Errorf("zip missing skill files; got %v", entries)
	}
	if s := out.String(); !strings.Contains(s, "Next step") || !strings.Contains(s, "linux/arm64") {
		t.Errorf("output missing target line / next step: %q", s)
	}
}

func TestRunSkillPackage_StreamSeparation(t *testing.T) {
	t.Setenv("SAM_TOOL_TARGET_OS", "linux")
	t.Setenv("SAM_TOOL_TARGET_ARCH", "arm64")
	repoRoot, _ := writeSkill(t, "summarizer")
	outZip := filepath.Join(t.TempDir(), "summarizer.zip")

	cmd := newSkillPackageCmd()
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"summarizer", repoRoot, "-o", outZip})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	if got := strings.TrimSpace(stdout.String()); got != outZip {
		t.Errorf("stdout = %q, want only the bundle path %q", got, outZip)
	}
	for _, banned := range []string{"packaging", "Next step", "wrote "} {
		if strings.Contains(stdout.String(), banned) {
			t.Errorf("stdout must not carry diagnostic %q; got %q", banned, stdout.String())
		}
	}
	se := stderr.String()
	for _, want := range []string{"packaging skill summarizer", "linux/arm64", "Next step"} {
		if !strings.Contains(se, want) {
			t.Errorf("stderr missing %q; got %q", want, se)
		}
	}
}

func TestRunSkillPackage_ArchDefaultNote(t *testing.T) {
	t.Setenv("SAM_TOOL_TARGET_OS", "")
	t.Setenv("SAM_TOOL_TARGET_ARCH", "")
	repoRoot, _ := writeSkill(t, "summarizer")
	outZip := filepath.Join(t.TempDir(), "summarizer.zip")

	cmd := newSkillPackageCmd()
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"summarizer", repoRoot, "-o", outZip})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(stderr.String(), "build target defaulted to") {
		t.Errorf("stderr missing arch-default note; got %q", stderr.String())
	}
	if strings.Contains(stdout.String(), "build target defaulted to") {
		t.Errorf("arch-default note leaked to stdout: %q", stdout.String())
	}
}

func TestSkillPackageCmd_Execute(t *testing.T) {
	if testing.Short() {
		t.Skip("requires go build")
	}
	t.Setenv("SAM_TOOL_TARGET_OS", "linux")
	t.Setenv("SAM_TOOL_TARGET_ARCH", "arm64")
	repoRoot := t.TempDir()
	toolDir := filepath.Join(repoRoot, "skills", "greeter", "tools", "hello")
	if err := os.MkdirAll(toolDir, 0o755); err != nil {
		t.Fatalf("mkdir tool: %v", err)
	}
	files := map[string]string{
		filepath.Join(repoRoot, "skills", "greeter", "SKILL.md"):               "---\nname: greeter\ndescription: t\n---\n# greeter\n",
		filepath.Join(repoRoot, "skills", "greeter", "tools", "manifest.yaml"): "version: 1\ntools:\n  hello:\n    runtime: go\n    tool_dir: hello\n    executable: hello\n",
		filepath.Join(toolDir, "go.mod"):                                       "module hello\n\ngo 1.23\n",
		filepath.Join(toolDir, "main.go"):                                      "package main\n\nfunc main() {}\n",
	}
	for p, body := range files {
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", p, err)
		}
	}
	outZip := filepath.Join(t.TempDir(), "greeter.zip")

	cmd := newSkillPackageCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"greeter", repoRoot, "-o", outZip})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	entries := zipEntries(t, outZip)
	if !entries["SKILL.md"] || !entries["tools/hello/hello"] {
		t.Errorf("zip missing SKILL.md or compiled tool binary; got %v", entries)
	}
}

func TestRunSkillPackage_MissingSkillMdErrors(t *testing.T) {
	t.Setenv("SAM_TOOL_TARGET_OS", "linux")
	t.Setenv("SAM_TOOL_TARGET_ARCH", "arm64")
	cmd, _ := newPackageCmdForTest()
	err := runSkillPackage(context.Background(), cmd, "ghost", t.TempDir(), "", "", filepath.Join(t.TempDir(), "ghost.zip"))
	if err == nil || !strings.Contains(err.Error(), "no SKILL.md") {
		t.Errorf("err = %v; want 'no SKILL.md'", err)
	}
}

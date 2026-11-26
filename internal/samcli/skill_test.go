// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package samcli

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/skill"
)

func TestSkillCmd_HasToolsetParitySubcommands(t *testing.T) {
	want := map[string]bool{"init": false, "sync": false, "package": false, "build-target": false}
	for _, c := range newSkillCmd().Commands() {
		name := strings.Fields(c.Use)[0]
		if _, ok := want[name]; ok {
			want[name] = true
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("sam skill is missing the %q subcommand", name)
		}
	}
}

func TestSkillBuildTargetCmd_EnvOverride(t *testing.T) {
	t.Setenv("SAM_TOOL_TARGET_OS", "linux")
	t.Setenv("SAM_TOOL_TARGET_ARCH", "amd64")
	cmd := newSkillBuildTargetCmd()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{"--format", "text"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if got := strings.TrimSpace(out.String()); got != "linux/amd64" {
		t.Errorf("build-target = %q; want linux/amd64", got)
	}
}

func TestAIAssistanceEarlyAccessWarning(t *testing.T) {
	var logBuf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelWarn})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	dir := t.TempDir()
	stdout, _, err := runRoot(t, "ai-assistance", "skill", "install", "--to", dir)
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	logged := logBuf.String()
	if !strings.Contains(logged, "Early Access") || !strings.Contains(logged, "level=WARN") {
		t.Errorf("expected Early Access WARN log, got %q", logged)
	}
	if strings.Contains(stdout, "Early Access") {
		t.Errorf("warning must not pollute stdout, got %q", stdout)
	}
}

func TestConfigSkillInstall_WritesTree(t *testing.T) {
	dir := t.TempDir()
	out, errOut, err := runRoot(t, "ai-assistance", "skill", "install", "--to", dir)
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if got := strings.TrimSpace(out); !strings.Contains(got, dir) {
		t.Errorf("stdout should be the written path under %q, got %q", dir, out)
	}
	if !strings.Contains(errOut, "wrote ") {
		t.Errorf("expected 'wrote ' confirmation on stderr, got %q", errOut)
	}
	skillDir := filepath.Join(dir, skill.SkillName)
	skillMD := filepath.Join(skillDir, "SKILL.md")
	body, err := os.ReadFile(skillMD)
	if err != nil {
		t.Fatalf("read installed: %v", err)
	}
	for _, want := range []string{
		"name: " + skill.SkillName,
		"version: ",
		"# Agent Mesh Declarative Config",
		"## Resource kinds index",
		"references/agent.md",
		"references/entrypoint.md",
		"references/connector.md",
		"references/workflow.md",
	} {
		if !strings.Contains(string(body), want) {
			t.Errorf("installed SKILL.md missing %q", want)
		}
	}
	for _, ref := range []string{
		"references/manifest.md",
		"references/agent.md",
		"references/entrypoint.md",
		"references/connector.md",
		"references/workflow.md",
		"references/model.md",
		"references/toolset.md",
		"references/skill.md",
		"references/layout.md",
		"references/common-patterns.md",
		"references/common-mistakes.md",
	} {
		full := filepath.Join(skillDir, ref)
		if _, err := os.Stat(full); err != nil {
			t.Errorf("expected %s on disk: %v", ref, err)
		}
	}
}

func TestConfigSkillInstall_RefusesOverwrite(t *testing.T) {
	dir := t.TempDir()
	if _, _, err := runRoot(t, "ai-assistance", "skill", "install", "--to", dir); err != nil {
		t.Fatalf("first install: %v", err)
	}
	_, _, err := runRoot(t, "ai-assistance", "skill", "install", "--to", dir)
	if err == nil {
		t.Fatal("expected refusal on second install without --force")
	}
	if !strings.Contains(err.Error(), "refusing to overwrite") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestConfigSkillInstall_ForceWipesStaleReferences(t *testing.T) {
	dir := t.TempDir()
	if _, _, err := runRoot(t, "ai-assistance", "skill", "install", "--to", dir); err != nil {
		t.Fatalf("first install: %v", err)
	}
	stale := filepath.Join(dir, skill.SkillName, "references", "obsolete.md")
	if err := os.WriteFile(stale, []byte("# obsolete\n"), 0o644); err != nil {
		t.Fatalf("plant stale: %v", err)
	}
	if _, _, err := runRoot(t, "ai-assistance", "skill", "install", "--to", dir, "--force"); err != nil {
		t.Fatalf("force install: %v", err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("expected stale reference removed, stat err = %v", err)
	}
	gw := filepath.Join(dir, skill.SkillName, "references", "entrypoint.md")
	if _, err := os.Stat(gw); err != nil {
		t.Errorf("force install lost canonical reference entrypoint.md: %v", err)
	}
}

func replaceVersionLine(body, want string) string {
	lines := strings.Split(body, "\n")
	for i, l := range lines {
		if strings.HasPrefix(strings.TrimSpace(l), "version:") {
			indent := l[:len(l)-len(strings.TrimLeft(l, " \t"))]
			lines[i] = indent + "version: " + want
			return strings.Join(lines, "\n")
		}
	}
	return body
}

func TestConfigSkillCheck_SyncedAndStale(t *testing.T) {
	dir := t.TempDir()
	if _, _, err := runRoot(t, "ai-assistance", "skill", "install", "--to", dir); err != nil {
		t.Fatalf("install: %v", err)
	}
	out, _, err := runRoot(t, "ai-assistance", "skill", "check", "--path", dir)
	if err != nil {
		t.Fatalf("check (synced): %v", err)
	}
	if !strings.Contains(out, "in sync") {
		t.Errorf("expected 'in sync', got %q", out)
	}

	target := filepath.Join(dir, skill.SkillName, "SKILL.md")
	body, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read installed: %v", err)
	}
	mutated := replaceVersionLine(string(body), "stale-1.2.3")
	if mutated == string(body) {
		t.Fatal("test setup did not mutate version line — frontmatter format may have changed")
	}
	if writeErr := os.WriteFile(target, []byte(mutated), 0o644); writeErr != nil {
		t.Fatalf("write mutated: %v", writeErr)
	}
	out2, _, err := runRoot(t, "ai-assistance", "skill", "check", "--path", dir)
	if err == nil {
		t.Fatal("expected non-nil error for stale skill")
	}
	if !strings.Contains(out2, "OUT OF SYNC") {
		t.Errorf("expected 'OUT OF SYNC' in output, got %q", out2)
	}
}

func TestConfigSkillCheck_ReportsMissingReference(t *testing.T) {
	dir := t.TempDir()
	if _, _, err := runRoot(t, "ai-assistance", "skill", "install", "--to", dir); err != nil {
		t.Fatalf("install: %v", err)
	}
	target := filepath.Join(dir, skill.SkillName, "references", "entrypoint.md")
	if err := os.Remove(target); err != nil {
		t.Fatalf("remove ref: %v", err)
	}
	out, _, err := runRoot(t, "ai-assistance", "skill", "check", "--path", dir)
	if err == nil {
		t.Fatal("expected error from skill check with missing reference")
	}
	if !strings.Contains(out, "declarative-config missing reference") {
		t.Errorf("expected 'declarative-config missing reference' in output, got %q", out)
	}
	if !strings.Contains(out, "OUT OF SYNC") {
		t.Errorf("expected 'OUT OF SYNC' in output, got %q", out)
	}
	if !strings.Contains(out, "entrypoint.md") {
		t.Errorf("expected entrypoint.md called out, got %q", out)
	}
}

func TestSkillBuild_HasFrontmatter(t *testing.T) {
	body, err := skill.BuildSkill("test-version")
	if err != nil {
		t.Fatalf("BuildSkill: %v", err)
	}
	src := string(body)
	if !strings.HasPrefix(src, "---\n") {
		t.Error("missing leading frontmatter delimiter")
	}
	if !strings.Contains(src, "version: test-version") {
		t.Error("missing version field")
	}
	if !strings.Contains(src, "name: "+skill.SkillName) {
		t.Error("missing name field")
	}
}

func TestAIAssistanceInstall_DefaultsToAgentsSkills(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	fakeHome(t)
	out, _, err := runRoot(t, "ai-assistance", "skill", "install")
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if got := strings.TrimSpace(out); got != filepath.FromSlash(".agents/skills") {
		t.Errorf("stdout = %q, want the default root", got)
	}
	if _, err := os.Stat(filepath.Join(root, ".agents", "skills", skill.SkillName, "SKILL.md")); err != nil {
		t.Errorf("suite not written under .agents/skills: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, ".claude")); !os.IsNotExist(err) {
		t.Errorf(".claude must not be touched by the default install, stat err = %v", err)
	}
}

func TestAIAssistanceInstall_TargetClaudeWritesClaudeSkills(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	fakeHome(t)
	out, _, err := runRoot(t, "ai-assistance", "skill", "install", "--target", "claude")
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if got := strings.TrimSpace(out); got != filepath.FromSlash(".claude/skills") {
		t.Errorf("stdout = %q, want the claude root", got)
	}
	if _, err := os.Stat(filepath.Join(root, ".claude", "skills", "sam-concierge", "SKILL.md")); err != nil {
		t.Errorf("suite not written under .claude/skills: %v", err)
	}
}

func TestAIAssistanceInstall_RejectsUnknownTargetAndTargetWithTo(t *testing.T) {
	t.Chdir(t.TempDir())
	fakeHome(t)
	_, _, err := runRoot(t, "ai-assistance", "skill", "install", "--target", "cursor")
	if err == nil || !strings.Contains(err.Error(), "agents, claude") {
		t.Errorf("unknown target: got %v, want an error naming the valid targets", err)
	}
	_, _, err = runRoot(t, "ai-assistance", "skill", "install", "--target", "claude", "--to", "elsewhere")
	if err == nil {
		t.Error("--target with --to: expected a mutual-exclusion error")
	}
}

func TestAIAssistanceCheck_ReportsEveryRootThatHoldsASuite(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	fakeHome(t)
	for _, args := range [][]string{
		{"ai-assistance", "skill", "install"},
		{"ai-assistance", "skill", "install", "--target", "claude"},
	} {
		if _, _, err := runRoot(t, args...); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
	}
	staleSkill := filepath.Join(root, ".claude", "skills", skill.SkillName, "SKILL.md")
	body, err := os.ReadFile(staleSkill)
	if err != nil {
		t.Fatal(err)
	}
	mutated := replaceVersionLine(string(body), "stale-1.2.3")
	if mutated == string(body) {
		t.Fatal("test setup did not mutate the version line")
	}
	if writeErr := os.WriteFile(staleSkill, []byte(mutated), 0o644); writeErr != nil {
		t.Fatal(writeErr)
	}

	out, _, err := runRoot(t, "ai-assistance", "skill", "check")
	if err == nil {
		t.Fatalf("a stale suite in either root must fail the check\n%s", out)
	}
	for _, want := range []string{
		filepath.FromSlash(".agents/skills") + ": suite in sync",
		filepath.FromSlash(".claude/skills") + ": OUT OF SYNC",
		"--target claude --force",
		"  " + skill.SkillName + `: version "stale-1.2.3"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout missing %q:\n%s", want, out)
		}
	}
}

func TestAIAssistanceCheck_FindsTheSuiteInEitherRootWithoutAFlag(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	fakeHome(t)
	if _, _, err := runRoot(t, "ai-assistance", "skill", "install", "--target", "claude"); err != nil {
		t.Fatalf("install: %v", err)
	}
	out, _, err := runRoot(t, "ai-assistance", "skill", "check")
	if err != nil {
		t.Fatalf("check must pass when the only suite is current: %v\n%s", err, out)
	}
	if want := filepath.FromSlash(".claude/skills") + ": suite in sync"; !strings.Contains(out, want) {
		t.Errorf("stdout missing %q:\n%s", want, out)
	}
	if strings.Contains(out, filepath.FromSlash(".agents/skills")) {
		t.Errorf("an absent root must not be reported:\n%s", out)
	}
}

func TestAIAssistanceCheck_NothingInstalledNamesBothRootsAndTheInstallCommand(t *testing.T) {
	fakeHome(t)
	t.Chdir(t.TempDir())
	out, _, err := runRoot(t, "ai-assistance", "skill", "check")
	if err == nil {
		t.Fatalf("check with no suite anywhere must fail\n%s", out)
	}
	for _, want := range []string{filepath.FromSlash(".agents/skills"), filepath.FromSlash(".claude/skills"), "sam ai-assistance skill install"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q missing %q", err, want)
		}
	}
}

func TestAIAssistanceCheck_HasNoTargetFlag(t *testing.T) {
	fakeHome(t)
	t.Chdir(t.TempDir())
	_, _, err := runRoot(t, "ai-assistance", "skill", "check", "--target", "claude")
	if err == nil || !strings.Contains(err.Error(), "unknown flag") {
		t.Errorf("check --target: got %v, want an unknown-flag error (check scans both roots)", err)
	}
}

func TestInitTips_NameTheClaudeCodeTarget(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"toolset init", []string{"toolset", "init", "demo", "--lang", "go"}},
		{"skill init", []string{"skill", "init", "demo"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fakeHome(t)
			t.Chdir(t.TempDir())
			_, stderr, err := runRoot(t, tc.args...)
			if err != nil {
				t.Fatalf("%s: %v\n%s", tc.name, err, stderr)
			}
			if !strings.Contains(stderr, "sam ai-assistance skill install") {
				t.Errorf("%s tip missing the install command:\n%s", tc.name, stderr)
			}
			if !strings.Contains(stderr, "--target claude") {
				t.Errorf("%s tip does not mention --target claude, so a Claude Code reader is misdirected:\n%s", tc.name, stderr)
			}
		})
	}
}

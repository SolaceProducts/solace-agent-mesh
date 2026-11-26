// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package samcli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/aiassist"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/skill"
)

func fakeHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv(aiassist.ClaudeConfigDirEnv, "")
	return home
}

func TestAIAssistanceInstall_ScopeUserWritesUnderHome(t *testing.T) {
	home := fakeHome(t)
	project := t.TempDir()
	t.Chdir(project)

	out, _, err := runRoot(t, "ai-assistance", "skill", "install", "--scope", "user")
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	want := filepath.Join(home, ".agents", "skills")
	if got := strings.TrimSpace(out); got != want {
		t.Errorf("stdout = %q, want the user root %q", got, want)
	}
	if _, err := os.Stat(filepath.Join(want, skill.SkillName, "SKILL.md")); err != nil {
		t.Errorf("suite not written under the user root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(project, ".agents")); !os.IsNotExist(err) {
		t.Errorf("the project root must not be touched by a user-scope install, stat err = %v", err)
	}
}

func TestAIAssistanceInstall_ScopeUserClaudeFollowsClaudeConfigDir(t *testing.T) {
	fakeHome(t)
	t.Chdir(t.TempDir())
	configDir := t.TempDir()
	t.Setenv(aiassist.ClaudeConfigDirEnv, configDir)

	out, _, err := runRoot(t, "ai-assistance", "skill", "install", "--target", "claude", "--scope", "user")
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	want := filepath.Join(configDir, "skills")
	if got := strings.TrimSpace(out); got != want {
		t.Errorf("stdout = %q, want %q", got, want)
	}
	if _, err := os.Stat(filepath.Join(want, "sam-concierge", "SKILL.md")); err != nil {
		t.Errorf("suite not written under CLAUDE_CONFIG_DIR: %v", err)
	}
}

func TestAIAssistanceInstall_RejectsUnknownScopeAndScopeWithTo(t *testing.T) {
	fakeHome(t)
	t.Chdir(t.TempDir())
	_, _, err := runRoot(t, "ai-assistance", "skill", "install", "--scope", "machine")
	if err == nil || !strings.Contains(err.Error(), "project, user") {
		t.Errorf("unknown scope: got %v, want an error naming the valid scopes", err)
	}
	_, _, err = runRoot(t, "ai-assistance", "skill", "install", "--scope", "user", "--to", "elsewhere")
	if err == nil {
		t.Error("--scope with --to: expected a mutual-exclusion error")
	}
}

func TestAIAssistanceInstall_ScopeUserNeedsAHome(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")
	_, _, err := runRoot(t, "ai-assistance", "skill", "install", "--scope", "user")
	if err == nil || !strings.Contains(err.Error(), "--to <dir>") {
		t.Errorf("no home: got %v, want an error pointing at --to", err)
	}
}

func TestAIAssistanceCheck_FindsAUserScopeSuiteWithoutAFlag(t *testing.T) {
	home := fakeHome(t)
	t.Chdir(t.TempDir())
	if _, _, err := runRoot(t, "ai-assistance", "skill", "install", "--scope", "user"); err != nil {
		t.Fatalf("install: %v", err)
	}
	userRoot := filepath.Join(home, ".agents", "skills")
	out, _, err := runRoot(t, "ai-assistance", "skill", "check")
	if err != nil || !strings.Contains(out, userRoot+": suite in sync") {
		t.Fatalf("check right after a user-scope install: err=%v\n%s", err, out)
	}

	staleSkill := filepath.Join(userRoot, skill.SkillName, "SKILL.md")
	body, err := os.ReadFile(staleSkill)
	if err != nil {
		t.Fatal(err)
	}
	if writeErr := os.WriteFile(staleSkill, []byte(replaceVersionLine(string(body), "stale-1.2.3")), 0o644); writeErr != nil {
		t.Fatal(writeErr)
	}
	out, _, err = runRoot(t, "ai-assistance", "skill", "check")
	if err == nil {
		t.Fatal("check of a stale user-scope suite must fail")
	}
	if want := "install --target agents --scope user --force"; !strings.Contains(out, want) {
		t.Errorf("stdout missing the scoped refresh command %q:\n%s", want, out)
	}
}

func TestAIAssistanceCheck_ReportsBothScopesAndSaysToKeepOne(t *testing.T) {
	home := fakeHome(t)
	t.Chdir(t.TempDir())
	for _, args := range [][]string{
		{"ai-assistance", "skill", "install"},
		{"ai-assistance", "skill", "install", "--scope", "user"},
	} {
		if _, _, err := runRoot(t, args...); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
	}
	userRoot := filepath.Join(home, ".agents", "skills")
	staleSkill := filepath.Join(userRoot, skill.SkillName, "SKILL.md")
	body, err := os.ReadFile(staleSkill)
	if err != nil {
		t.Fatal(err)
	}
	if writeErr := os.WriteFile(staleSkill, []byte(replaceVersionLine(string(body), "stale-1.2.3")), 0o644); writeErr != nil {
		t.Fatal(writeErr)
	}

	out, _, err := runRoot(t, "ai-assistance", "skill", "check")
	if err == nil {
		t.Fatalf("a stale suite at either scope must fail the check\n%s", out)
	}
	for _, want := range []string{
		filepath.FromSlash(".agents/skills") + ": suite in sync",
		userRoot + ": OUT OF SYNC",
		"--target agents --scope user --force",
		"installed at both " + filepath.FromSlash(".agents/skills") + " and " + userRoot + "; keep one",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout missing %q:\n%s", want, out)
		}
	}
}

func TestAIAssistanceCheck_OnlyAUserScopeClaudeSuitePasses(t *testing.T) {
	home := fakeHome(t)
	t.Chdir(t.TempDir())
	if _, _, err := runRoot(t, "ai-assistance", "skill", "install", "--target", "claude", "--scope", "user"); err != nil {
		t.Fatalf("install: %v", err)
	}
	out, _, err := runRoot(t, "ai-assistance", "skill", "check")
	if err != nil {
		t.Fatalf("check must pass when the only suite is current: %v\n%s", err, out)
	}
	if want := filepath.Join(home, ".claude", "skills") + ": suite in sync"; !strings.Contains(out, want) {
		t.Errorf("stdout missing %q:\n%s", want, out)
	}
	if strings.Contains(out, "note:") {
		t.Errorf("a single copy must not draw a keep-one note:\n%s", out)
	}
}

func TestAIAssistanceCheck_HasNoScopeFlag(t *testing.T) {
	fakeHome(t)
	t.Chdir(t.TempDir())
	_, _, err := runRoot(t, "ai-assistance", "skill", "check", "--scope", "user")
	if err == nil || !strings.Contains(err.Error(), "unknown flag") {
		t.Errorf("check --scope: got %v, want an unknown-flag error (check scans every well-known root)", err)
	}
}

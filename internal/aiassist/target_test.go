// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package aiassist

import (
	"path/filepath"
	"strings"
	"testing"
)

func fakeHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv(ClaudeConfigDirEnv, "")
	return home
}

func TestParseTarget_AcceptsKnownAndNamesValidOnesOnError(t *testing.T) {
	for _, want := range Targets() {
		got, err := ParseTarget(string(want))
		if err != nil || got != want {
			t.Errorf("ParseTarget(%q) = %q, %v", want, got, err)
		}
	}
	if _, err := ParseTarget("cursor"); err == nil || !strings.Contains(err.Error(), "agents, claude") {
		t.Errorf("unknown target: got %v, want an error naming the valid targets", err)
	}
}

func TestParseScope_AcceptsKnownAndNamesValidOnesOnError(t *testing.T) {
	for _, want := range Scopes() {
		got, err := ParseScope(string(want))
		if err != nil || got != want {
			t.Errorf("ParseScope(%q) = %q, %v", want, got, err)
		}
	}
	if _, err := ParseScope("machine"); err == nil || !strings.Contains(err.Error(), "project, user") {
		t.Errorf("unknown scope: got %v, want an error naming the valid scopes", err)
	}
}

func TestDefaults(t *testing.T) {
	if DefaultTarget != TargetAgents {
		t.Errorf("default target = %q, want agents", DefaultTarget)
	}
	if DefaultScope != ScopeProject {
		t.Errorf("default scope = %q, want project", DefaultScope)
	}
}

func TestRoots_ProjectScopeFirstThenDefaultTargetFirst(t *testing.T) {
	want := []Root{
		{TargetAgents, ScopeProject}, {TargetClaude, ScopeProject},
		{TargetAgents, ScopeUser}, {TargetClaude, ScopeUser},
	}
	got := Roots()
	if len(got) != len(want) {
		t.Fatalf("Roots() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Roots()[%d] = %v, want %v", i, got[i], want[i])
		}
	}
}

func TestRootDir_ProjectScopeIsRelativeToTheWorkingDirectory(t *testing.T) {
	cases := map[Target]string{TargetAgents: ".agents/skills", TargetClaude: ".claude/skills"}
	for target, want := range cases {
		got, err := Root{Target: target, Scope: ScopeProject}.Dir()
		if err != nil || got != filepath.FromSlash(want) {
			t.Errorf("%s project dir = %q, %v; want %q", target, got, err, filepath.FromSlash(want))
		}
	}
}

func TestRootDir_UserScopeLivesUnderHome(t *testing.T) {
	home := fakeHome(t)
	cases := map[Target]string{
		TargetAgents: filepath.Join(home, ".agents", "skills"),
		TargetClaude: filepath.Join(home, ".claude", "skills"),
	}
	for target, want := range cases {
		got, err := Root{Target: target, Scope: ScopeUser}.Dir()
		if err != nil || got != want {
			t.Errorf("%s user dir = %q, %v; want %q", target, got, err, want)
		}
	}
}

func TestRootDir_ClaudeUserScopeFollowsClaudeConfigDir(t *testing.T) {
	home := fakeHome(t)
	configDir := t.TempDir()
	t.Setenv(ClaudeConfigDirEnv, configDir)

	got, err := Root{Target: TargetClaude, Scope: ScopeUser}.Dir()
	if err != nil || got != filepath.Join(configDir, "skills") {
		t.Errorf("claude user dir = %q, %v; want %q", got, err, filepath.Join(configDir, "skills"))
	}
	agents, err := Root{Target: TargetAgents, Scope: ScopeUser}.Dir()
	if err != nil || agents != filepath.Join(home, ".agents", "skills") {
		t.Errorf("CLAUDE_CONFIG_DIR must not move the agents root: got %q, %v", agents, err)
	}
}

func TestRootDir_UserScopeFailsWithoutAHome(t *testing.T) {
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")
	_, err := Root{Target: TargetAgents, Scope: ScopeUser}.Dir()
	if err == nil || !strings.Contains(err.Error(), "--scope user") {
		t.Errorf("no home: got %v, want an error naming --scope user", err)
	}
}

func TestSuitePresent(t *testing.T) {
	dir := t.TempDir()
	if SuitePresent(dir) {
		t.Error("empty dir reported as holding a suite")
	}
	if err := InstallSuite(dir, "v1", false, nil); err != nil {
		t.Fatal(err)
	}
	if !SuitePresent(dir) {
		t.Error("installed suite not detected")
	}
}

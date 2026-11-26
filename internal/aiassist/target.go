// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package aiassist

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

type Target string

const (
	TargetAgents Target = "agents"
	TargetClaude Target = "claude"
)

const DefaultTarget = TargetAgents

type Scope string

const (
	ScopeProject Scope = "project"
	ScopeUser    Scope = "user"
)

const DefaultScope = ScopeProject

const ClaudeConfigDirEnv = "CLAUDE_CONFIG_DIR"

type targetLayout struct {
	projectDir string
	userDir    func(home string) string
}

var targetLayouts = map[Target]targetLayout{
	TargetAgents: {
		projectDir: ".agents/skills",
		userDir:    func(home string) string { return filepath.Join(home, ".agents", "skills") },
	},
	TargetClaude: {
		projectDir: ".claude/skills",
		userDir:    claudeUserDir,
	},
}

func claudeUserDir(home string) string {
	if configDir := os.Getenv(ClaudeConfigDirEnv); configDir != "" {
		return filepath.Join(configDir, "skills")
	}
	return filepath.Join(home, ".claude", "skills")
}

func Targets() []Target { return []Target{TargetAgents, TargetClaude} }

func Scopes() []Scope { return []Scope{ScopeProject, ScopeUser} }

func ParseTarget(name string) (Target, error) {
	t := Target(name)
	if _, ok := targetLayouts[t]; !ok {
		return "", fmt.Errorf("unknown target %q (valid: %s)", name, strings.Join(targetNames(), ", "))
	}
	return t, nil
}

func targetNames() []string {
	names := make([]string, 0, len(targetLayouts))
	for _, t := range Targets() {
		names = append(names, string(t))
	}
	return names
}

func ParseScope(name string) (Scope, error) {
	s := Scope(name)
	if !slices.Contains(Scopes(), s) {
		return "", fmt.Errorf("unknown scope %q (valid: %s)", name, strings.Join(scopeNames(), ", "))
	}
	return s, nil
}

func scopeNames() []string {
	names := make([]string, 0, len(Scopes()))
	for _, s := range Scopes() {
		names = append(names, string(s))
	}
	return names
}

type Root struct {
	Target Target
	Scope  Scope
}

func Roots() []Root {
	roots := make([]Root, 0, len(Targets())*len(Scopes()))
	for _, scope := range Scopes() {
		for _, target := range Targets() {
			roots = append(roots, Root{Target: target, Scope: scope})
		}
	}
	return roots
}

func (r Root) Dir() (string, error) {
	layout := targetLayouts[r.Target]
	if r.Scope != ScopeUser {
		return filepath.FromSlash(layout.projectDir), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve the home directory for --scope user: %w", err)
	}
	return layout.userDir(home), nil
}

func SuitePresent(dir string) bool {
	for _, name := range allSuiteSkills() {
		if info, err := os.Stat(filepath.Join(dir, name, "SKILL.md")); err == nil && info.Size() > 0 {
			return true
		}
	}
	return false
}

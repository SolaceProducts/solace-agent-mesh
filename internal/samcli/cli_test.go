// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package samcli

import (
	"strings"
	"testing"
)

func TestRootCommand(t *testing.T) {
	cmd := newRootCmd()

	expectedSubs := []string{
		"run", "task", "eval", "docs", "commands", "storage",
		"config", "toolset", "skill", "ai-assistance", "auth", "api-key", "api",
	}

	for _, name := range expectedSubs {
		found := false
		for _, sub := range cmd.Commands() {
			if sub.Name() == name {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected subcommand %q not found", name)
		}
	}
}

func TestLegacyCommandsRemoved(t *testing.T) {
	cmd := newRootCmd()
	for _, name := range []string{"init", "add", "tools", "str", "volume", "control"} {
		for _, sub := range cmd.Commands() {
			if sub.Name() == name {
				t.Errorf("subcommand %q should have been removed", name)
			}
		}
	}
}

func TestVersionFlag(t *testing.T) {
	Version = "test-version-1.2.3"
	defer func() { Version = "dev" }()

	cmd := newRootCmd()
	buf := new(strings.Builder)
	cmd.SetOut(buf)
	cmd.SetArgs([]string{"--version"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(buf.String(), "test-version-1.2.3") {
		t.Errorf("version output %q does not contain expected version", buf.String())
	}
}

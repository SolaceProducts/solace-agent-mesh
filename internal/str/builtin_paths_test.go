// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import (
	"path/filepath"
	"testing"
)

func TestDefaultBuiltinSkillsDir(t *testing.T) {
	t.Run("env override wins", func(t *testing.T) {
		t.Setenv("SAM_BUILTIN_SKILLS_DIR", "/custom/path")
		t.Setenv("SAM_HOME", "/should/be/ignored")
		if got, want := defaultBuiltinSkillsDir(), "/custom/path"; got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
	})

	t.Run("sam_home fallback", func(t *testing.T) {
		samHome := t.TempDir()
		t.Setenv("SAM_BUILTIN_SKILLS_DIR", "")
		t.Setenv("SAM_HOME", samHome)
		want := filepath.Join(samHome, "builtin-skills")
		if got := defaultBuiltinSkillsDir(); got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
	})

	t.Run("cluster default when no env set", func(t *testing.T) {
		t.Setenv("SAM_BUILTIN_SKILLS_DIR", "")
		t.Setenv("SAM_HOME", "")
		if got, want := defaultBuiltinSkillsDir(), "/opt/sam/builtin-skills"; got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
	})
}

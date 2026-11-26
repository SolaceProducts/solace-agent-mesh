// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package skillinit

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func sdkGoMod(toolDir string) string {
	return filepath.Join(toolDir, "_sdk", "samtoolsdk", "go.mod")
}

func scaffoldSkill(t *testing.T, root, name string, withTool bool) string {
	t.Helper()
	if _, err := Init(Options{Name: name, Path: root, WithTool: withTool, CLIVersion: "9.9.9"}); err != nil {
		t.Fatalf("Init %s: %v", name, err)
	}
	return filepath.Join(root, "skills", name)
}

func TestSync_RevendorsAfterSDKDeleted(t *testing.T) {
	root := t.TempDir()
	skillDir := scaffoldSkill(t, root, "demo", true)
	toolDir := filepath.Join(skillDir, "tools", "demo")

	if err := os.RemoveAll(filepath.Join(toolDir, "_sdk")); err != nil {
		t.Fatalf("remove _sdk: %v", err)
	}
	if _, err := os.Stat(sdkGoMod(toolDir)); !os.IsNotExist(err) {
		t.Fatalf("precondition: _sdk should be gone, stat err = %v", err)
	}

	res, err := Sync(SyncOptions{Path: root})
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if !slices.Equal(res.Synced, []string{"demo"}) {
		t.Errorf("Synced = %v; want [demo]", res.Synced)
	}
	if res.ToolDirs != 1 {
		t.Errorf("ToolDirs = %d; want 1", res.ToolDirs)
	}
	if _, err := os.Stat(sdkGoMod(toolDir)); err != nil {
		t.Errorf("expected SDK re-vendored at %s: %v", sdkGoMod(toolDir), err)
	}
}

func TestSync_ByName(t *testing.T) {
	root := t.TempDir()
	skillDir := scaffoldSkill(t, root, "demo", true)
	toolDir := filepath.Join(skillDir, "tools", "demo")
	if err := os.RemoveAll(filepath.Join(toolDir, "_sdk")); err != nil {
		t.Fatalf("remove _sdk: %v", err)
	}

	res, err := Sync(SyncOptions{Path: root, Name: "demo"})
	if err != nil {
		t.Fatalf("Sync --name: %v", err)
	}
	if !slices.Equal(res.Synced, []string{"demo"}) {
		t.Errorf("Synced = %v; want [demo]", res.Synced)
	}
	if _, err := os.Stat(sdkGoMod(toolDir)); err != nil {
		t.Errorf("expected SDK re-vendored: %v", err)
	}
}

func TestSync_PathIsSkillsDir(t *testing.T) {
	root := t.TempDir()
	skillDir := scaffoldSkill(t, root, "demo", true)
	toolDir := filepath.Join(skillDir, "tools", "demo")
	if err := os.RemoveAll(filepath.Join(toolDir, "_sdk")); err != nil {
		t.Fatalf("remove _sdk: %v", err)
	}
	res, err := Sync(SyncOptions{Path: filepath.Join(root, "skills")})
	if err != nil {
		t.Fatalf("Sync skills/ dir: %v", err)
	}
	if !slices.Equal(res.Synced, []string{"demo"}) {
		t.Errorf("Synced = %v; want [demo]", res.Synced)
	}
	if _, err := os.Stat(sdkGoMod(toolDir)); err != nil {
		t.Errorf("expected SDK re-vendored: %v", err)
	}
}

func TestSync_PathIsSingleSkillDir(t *testing.T) {
	root := t.TempDir()
	skillDir := scaffoldSkill(t, root, "demo", true)
	toolDir := filepath.Join(skillDir, "tools", "demo")
	if err := os.RemoveAll(filepath.Join(toolDir, "_sdk")); err != nil {
		t.Fatalf("remove _sdk: %v", err)
	}
	res, err := Sync(SyncOptions{Path: skillDir})
	if err != nil {
		t.Fatalf("Sync skills/<name>/ dir: %v", err)
	}
	if !slices.Equal(res.Synced, []string{"demo"}) {
		t.Errorf("Synced = %v; want [demo]", res.Synced)
	}
	if _, err := os.Stat(sdkGoMod(toolDir)); err != nil {
		t.Errorf("expected SDK re-vendored: %v", err)
	}
}

func TestSync_SkipsSkillsWithoutGoTools(t *testing.T) {
	root := t.TempDir()
	withTool := scaffoldSkill(t, root, "withtool", true)
	scaffoldSkill(t, root, "notool", false)

	res, err := Sync(SyncOptions{Path: root})
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if !slices.Equal(res.Synced, []string{"withtool"}) {
		t.Errorf("Synced = %v; want [withtool]", res.Synced)
	}
	if res.ToolDirs != 1 {
		t.Errorf("ToolDirs = %d; want 1", res.ToolDirs)
	}
	if _, err := os.Stat(sdkGoMod(filepath.Join(withTool, "tools", "withtool"))); err != nil {
		t.Errorf("expected withtool SDK present: %v", err)
	}
}

func TestSync_NoSkillsDir(t *testing.T) {
	_, err := Sync(SyncOptions{Path: t.TempDir()})
	if err == nil {
		t.Fatal("expected error when no skills/ directory exists")
	}
}

func TestSync_NameNotFound(t *testing.T) {
	root := t.TempDir()
	scaffoldSkill(t, root, "demo", true)
	if _, err := Sync(SyncOptions{Path: root, Name: "ghost"}); err == nil {
		t.Fatal("expected error for a --name that does not exist")
	}
}

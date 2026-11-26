// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

func snapshotDirFiles(t *testing.T, root string) map[string]struct{} {
	t.Helper()
	files := make(map[string]struct{})
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		files[rel] = struct{}{}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	return files
}

func TestDiscoverSkills_DoesNotWriteIntoSkillsDir(t *testing.T) {
	skillsDir := t.TempDir()
	manifestDir := t.TempDir()
	writeBundledSkill(t, skillsDir, "documents", "Document routing.", false)

	before := snapshotDirFiles(t, skillsDir)

	result := DiscoverSkills(skillsDir, manifestDir, "ns1", slog.Default())

	after := snapshotDirFiles(t, skillsDir)
	for f := range after {
		if _, existed := before[f]; !existed {
			t.Errorf("DiscoverSkills created %q inside skillsDir; the signed bundle must stay untouched", f)
		}
	}

	if result.ManifestPath == "" {
		t.Fatal("expected a generated ManifestPath for a skill with bundled tools")
	}
	if _, err := os.Stat(result.ManifestPath); err != nil {
		t.Fatalf("generated manifest not found at %s: %v", result.ManifestPath, err)
	}
	if rel, err := filepath.Rel(skillsDir, result.ManifestPath); err == nil && !filepathEscapes(rel) {
		t.Errorf("ManifestPath %q is inside skillsDir; expected it under a user-writable dir", result.ManifestPath)
	}
	if rel, err := filepath.Rel(manifestDir, result.ManifestPath); err != nil || filepathEscapes(rel) {
		t.Errorf("ManifestPath %q is not under the provided manifestDir %q", result.ManifestPath, manifestDir)
	}
}

func filepathEscapes(rel string) bool {
	return rel == ".." || len(rel) >= 3 && rel[:3] == ".."+string(filepath.Separator)
}

func TestDiscoverSkills_DistinctManifestPerSkillsDir(t *testing.T) {
	manifestDir := t.TempDir()
	skillsA := t.TempDir()
	skillsB := t.TempDir()
	writeBundledSkill(t, skillsA, "documents", "Document routing.", false)
	writeBundledSkill(t, skillsB, "images", "Image routing.", false)

	a := DiscoverSkills(skillsA, manifestDir, "ns1", slog.Default())
	b := DiscoverSkills(skillsB, manifestDir, "ns1", slog.Default())

	if a.ManifestPath == "" || b.ManifestPath == "" {
		t.Fatalf("expected a manifest for each skills dir: a=%q b=%q", a.ManifestPath, b.ManifestPath)
	}
	if a.ManifestPath == b.ManifestPath {
		t.Fatalf("two skills dirs collided on one manifest path %q", a.ManifestPath)
	}
	for _, p := range []string{a.ManifestPath, b.ManifestPath} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("expected both manifests to coexist; missing %s: %v", p, err)
		}
	}
}

func TestDiscoverSkills_RefusesManifestDirInsideSkillsDir(t *testing.T) {
	cache := t.TempDir()
	t.Setenv("HOME", cache)
	t.Setenv("XDG_CACHE_HOME", cache)
	t.Setenv("LOCALAPPDATA", cache)

	skillsDir := t.TempDir()
	writeBundledSkill(t, skillsDir, "documents", "Document routing.", false)
	before := snapshotDirFiles(t, skillsDir)

	result := DiscoverSkills(skillsDir, skillsDir, "ns1", slog.Default())

	for f := range snapshotDirFiles(t, skillsDir) {
		if _, existed := before[f]; !existed {
			t.Errorf("DiscoverSkills wrote %q into skillsDir despite the guard", f)
		}
	}
	if result.ManifestPath == "" {
		t.Fatal("expected a manifest via the fallback dir")
	}
	if rel, err := filepath.Rel(skillsDir, result.ManifestPath); err == nil && !filepathEscapes(rel) {
		t.Errorf("ManifestPath %q ended up inside skillsDir", result.ManifestPath)
	}
}

func TestIsWithin(t *testing.T) {
	root := t.TempDir()
	cases := []struct {
		path string
		want bool
	}{
		{root, true},
		{filepath.Join(root, "sub"), true},
		{filepath.Join(root, "a", "b"), true},
		{filepath.Dir(root), false},
		{filepath.Join(filepath.Dir(root), "sibling"), false},
	}
	for _, c := range cases {
		if got := isWithin(c.path, root); got != c.want {
			t.Errorf("isWithin(%q, %q) = %v, want %v", c.path, root, got, c.want)
		}
	}
}

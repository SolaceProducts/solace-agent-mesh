// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package build

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var testTarget = Target{OS: "linux", Arch: "arm64"}

func TestCachePutGetRoundTrip(t *testing.T) {
	c := NewCache(t.TempDir())

	want := []byte("zip-bytes-here")
	if err := c.Put(testTarget, "hash1", want); err != nil {
		t.Fatalf("Put: %v", err)
	}

	got, ok, err := c.Get(testTarget, "hash1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !ok {
		t.Fatal("Get reported miss after Put")
	}
	if string(got) != string(want) {
		t.Errorf("got=%q, want=%q", got, want)
	}
}

func TestCacheGetMissNoError(t *testing.T) {
	c := NewCache(t.TempDir())

	got, ok, err := c.Get(testTarget, "never-written")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if ok {
		t.Error("Get reported hit for unwritten key")
	}
	if got != nil {
		t.Errorf("got non-nil bytes for miss: %q", got)
	}
}

func TestCacheSegmentsByTarget(t *testing.T) {
	c := NewCache(t.TempDir())
	dar := Target{OS: "darwin", Arch: "arm64"}
	lin := Target{OS: "linux", Arch: "arm64"}

	if err := c.Put(dar, "h", []byte("mach-o-bytes")); err != nil {
		t.Fatalf("Put darwin: %v", err)
	}

	if _, ok, err := c.Get(lin, "h"); err != nil {
		t.Fatalf("Get linux: %v", err)
	} else if ok {
		t.Error("darwin entry leaked into linux cache lookup")
	}

	if c.Path(dar, "h") == c.Path(lin, "h") {
		t.Errorf("cache paths for different targets collided: %s", c.Path(dar, "h"))
	}
	if !strings.Contains(c.Path(dar, "h"), filepath.Join("build", "darwin-arm64")) {
		t.Errorf("darwin path missing target subdir: %s", c.Path(dar, "h"))
	}
}

func TestCachePruneAll(t *testing.T) {
	c := NewCache(t.TempDir())
	mustPut(t, c, "a", []byte("a"))
	mustPut(t, c, "b", []byte("bb"))

	res, err := c.Prune(PruneOptions{All: true})
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if len(res.Removed) != 2 {
		t.Errorf("Removed=%d, want 2", len(res.Removed))
	}
	if res.RemovedBytes != 3 {
		t.Errorf("RemovedBytes=%d, want 3", res.RemovedBytes)
	}
	if _, ok, _ := c.Get(testTarget, "a"); ok {
		t.Error("entry 'a' still present after All prune")
	}
}

func TestCachePruneAllAcrossTargets(t *testing.T) {
	c := NewCache(t.TempDir())
	mustPutTarget(t, c, Target{OS: "linux", Arch: "arm64"}, "h1", []byte("a"))
	mustPutTarget(t, c, Target{OS: "darwin", Arch: "arm64"}, "h2", []byte("b"))

	res, err := c.Prune(PruneOptions{All: true})
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if len(res.Removed) != 2 {
		t.Errorf("Removed=%d, want 2 (one per target)", len(res.Removed))
	}
}

func TestCachePruneTargetScoped(t *testing.T) {
	c := NewCache(t.TempDir())
	lin := Target{OS: "linux", Arch: "arm64"}
	dar := Target{OS: "darwin", Arch: "arm64"}
	mustPutTarget(t, c, lin, "h1", []byte("a"))
	mustPutTarget(t, c, dar, "h2", []byte("b"))

	res, err := c.Prune(PruneOptions{All: true, Target: dar})
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if len(res.Removed) != 1 {
		t.Fatalf("Removed=%d, want 1", len(res.Removed))
	}
	if _, ok, _ := c.Get(lin, "h1"); !ok {
		t.Error("linux entry pruned despite Target=darwin")
	}
	if _, ok, _ := c.Get(dar, "h2"); ok {
		t.Error("darwin entry survived Target=darwin prune")
	}
}

func TestCachePruneAge(t *testing.T) {
	c := NewCache(t.TempDir())
	mustPut(t, c, "old", []byte("xxxxx"))
	mustPut(t, c, "new", []byte("y"))

	oldTime := time.Now().Add(-90 * 24 * time.Hour)
	if err := os.Chtimes(c.Path(testTarget, "old"), oldTime, oldTime); err != nil {
		t.Fatalf("backdate: %v", err)
	}

	res, err := c.Prune(PruneOptions{MaxAge: 30 * 24 * time.Hour, Now: time.Now()})
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if len(res.Removed) != 1 {
		t.Fatalf("Removed=%d, want 1", len(res.Removed))
	}
	if !strings.HasSuffix(res.Removed[0], "old.zip") {
		t.Errorf("wrong entry removed: %s", res.Removed[0])
	}
	if _, ok, _ := c.Get(testTarget, "new"); !ok {
		t.Error("fresh entry was pruned")
	}
}

func TestCachePruneSize(t *testing.T) {
	c := NewCache(t.TempDir())
	mustPut(t, c, "alpha", []byte("aaaaaaaaaa"))
	if err := os.Chtimes(c.Path(testTarget, "alpha"), time.Now().Add(-1*time.Hour), time.Now().Add(-1*time.Hour)); err != nil {
		t.Fatalf("backdate: %v", err)
	}
	mustPut(t, c, "beta", []byte("bbbbbbbbbb"))

	res, err := c.Prune(PruneOptions{MaxSize: 10, Now: time.Now()})
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if len(res.Removed) != 1 {
		t.Fatalf("Removed=%d, want 1 (oldest evicted)", len(res.Removed))
	}
	if _, ok, _ := c.Get(testTarget, "beta"); !ok {
		t.Error("newer entry was evicted under size cap")
	}
}

func TestEnsureGitignoreIdempotent(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte("toolsets/*/dist\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if err := EnsureGitignore(root); err != nil {
		t.Fatalf("first ensure: %v", err)
	}
	first, _ := os.ReadFile(filepath.Join(root, ".gitignore"))
	if err := EnsureGitignore(root); err != nil {
		t.Fatalf("second ensure: %v", err)
	}
	second, _ := os.ReadFile(filepath.Join(root, ".gitignore"))
	if string(first) != string(second) {
		t.Errorf("non-idempotent: first=%q second=%q", first, second)
	}
	if !strings.Contains(string(second), "toolsets/*/.sam-cache/") {
		t.Error("missing toolsets/*/.sam-cache/ entry")
	}
	count := strings.Count(string(second), "toolsets/*/dist")
	if count > 1 {
		t.Errorf(".gitignore added duplicate dist entries: %q", second)
	}
}

func TestEnsureGitignoreCreate(t *testing.T) {
	root := t.TempDir()
	if err := EnsureGitignore(root); err != nil {
		t.Fatalf("EnsureGitignore: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(root, ".gitignore"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	for _, want := range []string{
		"toolsets/*/.sam-cache/",
		"toolsets/*/dist/",
		"toolsets/*/src/vendor/",
	} {
		if !strings.Contains(string(body), want) {
			t.Errorf(".gitignore missing %q: %q", want, body)
		}
	}
}

func TestPruneRepo(t *testing.T) {
	repo := t.TempDir()
	for _, name := range []string{"alpha", "beta"} {
		c := NewCache(filepath.Join(repo, "toolsets", name))
		mustPut(t, c, "h-"+name, []byte("zip-bytes-"+name))
	}
	if err := os.MkdirAll(filepath.Join(repo, "toolsets", "no-cache"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	res, err := PruneRepo(repo, PruneOptions{All: true})
	if err != nil {
		t.Fatalf("PruneRepo: %v", err)
	}
	if len(res.Removed) != 2 {
		t.Errorf("Removed=%d, want 2 (alpha+beta)", len(res.Removed))
	}
	for _, name := range []string{"alpha", "beta"} {
		buildDir := filepath.Join(repo, "toolsets", name, ".sam-cache", "build")
		zips := 0
		_ = filepath.Walk(buildDir, func(_ string, info os.FileInfo, _ error) error {
			if info != nil && !info.IsDir() && strings.HasSuffix(info.Name(), ".zip") {
				zips++
			}
			return nil
		})
		if zips != 0 {
			t.Errorf("%s cache survived prune: %d zip entries", name, zips)
		}
	}
}

func TestPruneRepoCoversSkillBinaries(t *testing.T) {
	repo := t.TempDir()
	skillCache := NewCache(filepath.Join(repo, "skills", "dates"))
	if err := skillCache.PutBinary(testTarget, "bh", []byte("\x7fELF-stub")); err != nil {
		t.Fatalf("PutBinary: %v", err)
	}
	tsCache := NewCache(filepath.Join(repo, "toolsets", "ts"))
	mustPut(t, tsCache, "zh", []byte("zip-bytes"))

	res, err := PruneRepo(repo, PruneOptions{All: true})
	if err != nil {
		t.Fatalf("PruneRepo: %v", err)
	}
	if len(res.Removed) != 2 {
		t.Fatalf("Removed=%d, want 2 (one .zip toolset + one .bin skill blob)", len(res.Removed))
	}
	if _, ok, _ := skillCache.GetBinary(testTarget, "bh"); ok {
		t.Errorf("skill .bin survived prune --all")
	}
}

func TestPruneRepoNoToolsets(t *testing.T) {
	repo := t.TempDir()
	res, err := PruneRepo(repo, PruneOptions{All: true})
	if err != nil {
		t.Fatalf("PruneRepo on empty repo: %v", err)
	}
	if len(res.Removed) != 0 || res.Kept != 0 {
		t.Errorf("expected empty result, got %+v", res)
	}
}

func mustPut(t *testing.T, c *Cache, hash string, data []byte) {
	t.Helper()
	mustPutTarget(t, c, testTarget, hash, data)
}

func mustPutTarget(t *testing.T, c *Cache, target Target, hash string, data []byte) {
	t.Helper()
	if err := c.Put(target, hash, data); err != nil {
		t.Fatalf("Put %s: %v", hash, err)
	}
}

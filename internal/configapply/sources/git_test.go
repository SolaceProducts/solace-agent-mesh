// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package sources

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func gitFixture(t *testing.T) (path, sha string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	runGit := func(args ...string) string {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test",
			"GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=test",
			"GIT_COMMITTER_EMAIL=test@example.com",
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %s in %s: %v\n%s", strings.Join(args, " "), dir, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	runGit("init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	runGit("add", ".")
	runGit("commit", "-q", "-m", "initial")
	sha = runGit("rev-parse", "HEAD")
	return dir, sha
}

func TestFetchFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "marker"), []byte{}, 0o644); err != nil {
		t.Fatalf("write marker: %v", err)
	}
	src, err := Parse("file://" + dir)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	res, err := Fetch(context.Background(), src, FetchOptions{CacheRoot: t.TempDir()})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if res.Dir != dir {
		t.Errorf("Dir = %q; want %q", res.Dir, dir)
	}
	if res.SHA != "" {
		t.Errorf("SHA = %q; want empty (file:// has no SHA)", res.SHA)
	}
	if res.CacheHit {
		t.Errorf("CacheHit = true; file:// is never a cache hit")
	}
}

func TestFetchFileSubdirectory(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "tools")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	src, err := Parse("file://" + root + "#subdirectory=tools")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	res, err := Fetch(context.Background(), src, FetchOptions{})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if res.Dir != sub {
		t.Errorf("Dir = %q; want %q", res.Dir, sub)
	}
}

func TestFetchFileMissing(t *testing.T) {
	src, _ := Parse("file:///does/not/exist/" + t.Name())
	if _, err := Fetch(context.Background(), src, FetchOptions{}); err == nil {
		t.Fatal("expected error for missing file:// path")
	}
}

func TestFetchGitPinnedRef(t *testing.T) {
	repo, sha := gitFixture(t)
	src, err := Parse("git+file://" + repo + "@" + sha)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	cacheRoot := t.TempDir()
	res, err := Fetch(context.Background(), src, FetchOptions{CacheRoot: cacheRoot})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if res.SHA != sha {
		t.Errorf("SHA = %q; want %q", res.SHA, sha)
	}
	if res.CacheHit {
		t.Errorf("CacheHit = true on first fetch; want false")
	}
	if _, err := os.Stat(filepath.Join(res.CacheDir, ".git")); err != nil {
		t.Errorf("expected .git in CacheDir %s: %v", res.CacheDir, err)
	}
	if _, err := os.Stat(filepath.Join(res.Dir, "README.md")); err != nil {
		t.Errorf("expected README.md in Dir %s: %v", res.Dir, err)
	}
}

func TestFetchCacheHit(t *testing.T) {
	repo, sha := gitFixture(t)
	src, err := Parse("git+file://" + repo + "@" + sha)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	cacheRoot := t.TempDir()

	if _, ferr := Fetch(context.Background(), src, FetchOptions{CacheRoot: cacheRoot}); ferr != nil {
		t.Fatalf("first Fetch: %v", ferr)
	}

	if rerr := os.RemoveAll(repo); rerr != nil {
		t.Fatalf("rm upstream: %v", rerr)
	}

	res, err := Fetch(context.Background(), src, FetchOptions{CacheRoot: cacheRoot})
	if err != nil {
		t.Fatalf("second Fetch (cache hit): %v", err)
	}
	if !res.CacheHit {
		t.Errorf("CacheHit = false; want true (pinned ref should reuse cache)")
	}
	if res.SHA != sha {
		t.Errorf("SHA = %q; want %q", res.SHA, sha)
	}
}

func TestFetchFloatingRef(t *testing.T) {
	repo, _ := gitFixture(t)
	src, err := Parse("git+file://" + repo + "@main")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	cacheRoot := t.TempDir()
	res1, err := Fetch(context.Background(), src, FetchOptions{CacheRoot: cacheRoot})
	if err != nil {
		t.Fatalf("first Fetch: %v", err)
	}
	if res1.CacheHit {
		t.Errorf("CacheHit = true on first fetch")
	}
	if res1.SHA == "" {
		t.Errorf("SHA = empty; want resolved SHA")
	}

	if werr := os.WriteFile(filepath.Join(repo, "second.txt"), []byte("v2\n"), 0o644); werr != nil {
		t.Fatal(werr)
	}
	runGit := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test",
			"GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=test",
			"GIT_COMMITTER_EMAIL=test@example.com",
		)
		out, gerr := cmd.CombinedOutput()
		if gerr != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), gerr, out)
		}
	}
	runGit("add", ".")
	runGit("commit", "-q", "-m", "second")

	res2, err := Fetch(context.Background(), src, FetchOptions{CacheRoot: cacheRoot})
	if err != nil {
		t.Fatalf("second Fetch: %v", err)
	}
	if res2.CacheHit {
		t.Errorf("CacheHit = true; want false (floating ref must re-fetch)")
	}
	if res2.SHA == res1.SHA {
		t.Errorf("SHA unchanged after upstream commit; want re-fetched tip")
	}
}

func TestFetchNoCacheReclonesEvenPinnedRef(t *testing.T) {
	repo, sha := gitFixture(t)
	src, err := Parse("git+file://" + repo + "@" + sha)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	cacheRoot := t.TempDir()
	if _, err := Fetch(context.Background(), src, FetchOptions{CacheRoot: cacheRoot}); err != nil {
		t.Fatalf("first Fetch: %v", err)
	}
	if err := os.RemoveAll(repo); err != nil {
		t.Fatalf("rm upstream: %v", err)
	}
	if _, err := Fetch(context.Background(), src, FetchOptions{CacheRoot: cacheRoot, NoCache: true}); err == nil {
		t.Fatal("expected NoCache fetch to fail after upstream removal")
	}
}

func TestPurgeCache(t *testing.T) {
	cacheRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(cacheRoot, "abc"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := PurgeCache(cacheRoot); err != nil {
		t.Fatalf("PurgeCache: %v", err)
	}
	if _, err := os.Stat(cacheRoot); !os.IsNotExist(err) {
		t.Errorf("cache root still exists after PurgeCache: err=%v", err)
	}
	if err := PurgeCache(cacheRoot); err != nil {
		t.Errorf("second PurgeCache (missing): %v", err)
	}
}

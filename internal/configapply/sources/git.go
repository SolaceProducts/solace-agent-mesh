// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package sources

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type FetchOptions struct {
	CacheRoot string

	NoCache bool

	Logf func(format string, args ...any)
}

type FetchResult struct {
	Dir string

	CacheDir string

	SHA string

	CacheHit bool
}

func DefaultCacheRoot() (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("locate user cache dir: %w", err)
	}
	return filepath.Join(dir, "sam", "sources"), nil
}

func Fetch(ctx context.Context, src Source, opts FetchOptions) (FetchResult, error) {
	logf := opts.Logf
	if logf == nil {
		logf = func(string, ...any) {
		}
	}

	if src.Scheme == SchemeFile {
		return fetchFile(src)
	}

	cacheRoot := opts.CacheRoot
	if cacheRoot == "" {
		root, err := DefaultCacheRoot()
		if err != nil {
			return FetchResult{}, err
		}
		cacheRoot = root
	}

	if src.Ref == "" {
		return FetchResult{}, fmt.Errorf("source %q has no ref; refusing to fetch", src.Raw)
	}

	cacheDir := cacheDirFor(cacheRoot, src)
	if opts.NoCache {
		if err := os.RemoveAll(cacheDir); err != nil {
			return FetchResult{}, fmt.Errorf("remove cache dir %s: %w", cacheDir, err)
		}
	}

	if err := os.MkdirAll(filepath.Dir(cacheDir), 0o755); err != nil {
		return FetchResult{}, fmt.Errorf("create cache root %s: %w", cacheRoot, err)
	}

	cloned, statErr := dirIsGitClone(cacheDir)
	if statErr != nil {
		return FetchResult{}, statErr
	}

	pinned := IsPinned(src)
	if cloned && pinned && !opts.NoCache {
		if sha, ok := pinnedCacheHit(ctx, cacheDir, src.Ref); ok {
			logf("cache hit: %s @ %s (%s)", src.URL, src.Ref, shortSHAOf(sha))
			return FetchResult{
				Dir:      withSubdir(cacheDir, src.Subdirectory),
				CacheDir: cacheDir,
				SHA:      sha,
				CacheHit: true,
			}, nil
		}
	}

	if !cloned {
		logf("git clone %s -> %s", src.URL, cacheDir)
		if cloneErr := gitClone(ctx, src.URL, cacheDir); cloneErr != nil {
			return FetchResult{}, cloneErr
		}
	} else {
		logf("git fetch %s @ %s", src.URL, src.Ref)
		if fetchErr := gitFetch(ctx, cacheDir); fetchErr != nil {
			return FetchResult{}, fetchErr
		}
	}

	sha, resolveErr := resolveRef(ctx, cacheDir, src.Ref)
	if resolveErr != nil {
		return FetchResult{}, fmt.Errorf("source %q: %w", src.Raw, resolveErr)
	}
	if checkoutErr := gitCheckout(ctx, cacheDir, sha); checkoutErr != nil {
		return FetchResult{}, checkoutErr
	}

	return FetchResult{
		Dir:      withSubdir(cacheDir, src.Subdirectory),
		CacheDir: cacheDir,
		SHA:      sha,
		CacheHit: false,
	}, nil
}

func PurgeCache(cacheRoot string) error {
	if cacheRoot == "" {
		root, err := DefaultCacheRoot()
		if err != nil {
			return err
		}
		cacheRoot = root
	}
	if err := os.RemoveAll(cacheRoot); err != nil {
		return fmt.Errorf("remove cache root %s: %w", cacheRoot, err)
	}
	return nil
}

func fetchFile(src Source) (FetchResult, error) {
	abs, err := filepath.Abs(src.URL)
	if err != nil {
		return FetchResult{}, fmt.Errorf("resolve file:// path %q: %w", src.URL, err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return FetchResult{}, fmt.Errorf("file:// source %q: %w", src.Raw, err)
	}
	if !info.IsDir() {
		return FetchResult{}, fmt.Errorf("file:// source %q: %s is not a directory", src.Raw, abs)
	}
	return FetchResult{
		Dir:      withSubdir(abs, src.Subdirectory),
		CacheDir: abs,
		SHA:      "",
		CacheHit: false,
	}, nil
}

func cacheDirFor(cacheRoot string, src Source) string {
	key := src.URL + "@" + src.Ref
	sum := sha256.Sum256([]byte(key))
	return filepath.Join(cacheRoot, hex.EncodeToString(sum[:]))
}

func dirIsGitClone(dir string) (bool, error) {
	info, err := os.Stat(filepath.Join(dir, ".git"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("stat %s: %w", dir, err)
	}
	_ = info
	return true, nil
}

func pinnedCacheHit(ctx context.Context, cacheDir, ref string) (string, bool) {
	headSHA, err := gitRevParse(ctx, cacheDir, "HEAD")
	if err != nil {
		return "", false
	}
	refSHA, err := gitRevParse(ctx, cacheDir, ref)
	if err != nil {
		return "", false
	}
	if headSHA != refSHA {
		return "", false
	}
	return headSHA, true
}

func gitClone(ctx context.Context, url, dest string) error {
	cmd := exec.CommandContext(ctx, "git", "clone", "--", url, dest)
	cmd.Env = append(os.Environ(), gitTerminalPromptOff)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git clone %s: %w\n%s", url, err, strings.TrimSpace(string(out)))
	}
	return nil
}

func gitFetch(ctx context.Context, dir string) error {
	cmd := exec.CommandContext(ctx, "git", "fetch", "--tags", "--prune")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), gitTerminalPromptOff)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git fetch in %s: %w\n%s", dir, err, strings.TrimSpace(string(out)))
	}
	return nil
}

func gitCheckout(ctx context.Context, dir, sha string) error {
	cmd := exec.CommandContext(ctx, "git", "-c", "advice.detachedHead=false",
		"checkout", "--detach", sha)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), gitTerminalPromptOff)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git checkout %s in %s: %w\n%s", sha, dir, err, strings.TrimSpace(string(out)))
	}
	return nil
}

func gitRevParse(ctx context.Context, dir, ref string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "rev-parse", "--verify", "--end-of-options", ref+"^{commit}")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git rev-parse %s in %s: %w", ref, dir, err)
	}
	return strings.TrimSpace(string(out)), nil
}

func resolveRef(ctx context.Context, dir, ref string) (string, error) {
	candidates := []string{
		"refs/tags/" + ref,
		"refs/remotes/origin/" + ref,
		ref,
	}
	var lastErr error
	for _, c := range candidates {
		sha, err := gitRevParse(ctx, dir, c)
		if err == nil {
			return sha, nil
		}
		lastErr = err
	}
	return "", fmt.Errorf("resolve ref %q: %w", ref, lastErr)
}

func withSubdir(dir, subdir string) string {
	if subdir == "" {
		return dir
	}
	return filepath.Join(dir, subdir)
}

func shortSHAOf(sha string) string {
	if len(sha) < 7 {
		return sha
	}
	return sha[:7]
}

const (
	gitTerminalPromptOff = "GIT_TERMINAL_PROMPT=0"
)

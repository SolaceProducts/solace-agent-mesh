// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package build

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const CacheRoot = ".sam-cache"

const buildSubdir = "build"

type Target struct {
	OS   string
	Arch string
}

func (t Target) Subpath() string {
	o, a := t.OS, t.Arch
	if o == "" {
		o = "unknown"
	}
	if a == "" {
		a = "unknown"
	}
	return o + "-" + a
}

type Cache struct {
	Root string
}

func NewCache(parentDir string) *Cache {
	return &Cache{Root: filepath.Join(parentDir, CacheRoot)}
}

func (c *Cache) Path(target Target, hash string) string {
	return c.pathWithExt(target, hash, zipCacheExt)
}

func (c *Cache) pathWithExt(target Target, hash, ext string) string {
	return filepath.Join(c.Root, buildSubdir, target.Subpath(), hash+ext)
}

func (c *Cache) Get(target Target, hash string) ([]byte, bool, error) {
	path := c.Path(target, hash)
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("cache get %s: %w", hash, err)
	}
	now := time.Now()
	_ = os.Chtimes(path, now, now)
	return data, true, nil
}

func (c *Cache) GetBinary(target Target, hash string) ([]byte, bool, error) {
	path := c.pathWithExt(target, hash, binCacheExt)
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("cache get %s: %w", hash, err)
	}
	now := time.Now()
	_ = os.Chtimes(path, now, now)
	return data, true, nil
}

func (c *Cache) PutBinary(target Target, hash string, data []byte) error {
	return c.putWithExt(target, hash, binCacheExt, data)
}

func (c *Cache) putWithExt(target Target, hash, ext string, data []byte) error {
	dir := filepath.Join(c.Root, buildSubdir, target.Subpath())
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("cache mkdir: %w", err)
	}
	path := c.pathWithExt(target, hash, ext)
	if _, err := os.Stat(path); err == nil {
		now := time.Now()
		_ = os.Chtimes(path, now, now)
		return nil
	}
	tmp, err := os.CreateTemp(dir, "."+hash+".*")
	if err != nil {
		return fmt.Errorf("cache temp: %w", err)
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("cache write: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("cache close: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("cache rename: %w", err)
	}
	return nil
}

func (c *Cache) Put(target Target, hash string, data []byte) error {
	return c.putWithExt(target, hash, zipCacheExt, data)
}

func VerifyHash(data []byte, expected string) error {
	got := sha256.Sum256(data)
	if hex.EncodeToString(got[:]) != expected {
		return fmt.Errorf("cache integrity: SHA-256 mismatch (want %s)", expected)
	}
	return nil
}

type PruneOptions struct {
	All     bool
	MaxAge  time.Duration
	MaxSize int64
	Now     time.Time
	Target  Target
}

const DefaultMaxAge = 30 * 24 * time.Hour

type PruneResult struct {
	Removed      []string
	RemovedBytes int64
	Kept         int
}

func (c *Cache) Prune(opts PruneOptions) (PruneResult, error) {
	res := PruneResult{}
	scanDir := filepath.Join(c.Root, buildSubdir)
	targetFilter := opts.Target
	hasFilter := targetFilter.OS != "" || targetFilter.Arch != ""
	if hasFilter {
		scanDir = filepath.Join(scanDir, targetFilter.Subpath())
	}

	if opts.All {
		entries, err := listCacheEntries(scanDir)
		if err != nil {
			return res, err
		}
		for _, e := range entries {
			if err := os.Remove(e.path); err != nil {
				return res, fmt.Errorf(pruneErrFmt, e.path, err)
			}
			res.Removed = append(res.Removed, e.path)
			res.RemovedBytes += e.size
		}
		return res, nil
	}

	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}
	maxAge := opts.MaxAge
	if maxAge == 0 {
		maxAge = DefaultMaxAge
	}
	cutoff := now.Add(-maxAge)

	entries, err := listCacheEntries(scanDir)
	if err != nil {
		return res, err
	}
	var survivors []cacheEntry
	for _, e := range entries {
		if e.mtime.Before(cutoff) {
			if err := os.Remove(e.path); err != nil {
				return res, fmt.Errorf(pruneErrFmt, e.path, err)
			}
			res.Removed = append(res.Removed, e.path)
			res.RemovedBytes += e.size
			continue
		}
		survivors = append(survivors, e)
	}

	if opts.MaxSize > 0 {
		var total int64
		for _, e := range survivors {
			total += e.size
		}
		sort.Slice(survivors, func(i, j int) bool {
			return survivors[i].mtime.Before(survivors[j].mtime)
		})
		i := 0
		for i < len(survivors) && total > opts.MaxSize {
			e := survivors[i]
			if err := os.Remove(e.path); err != nil {
				return res, fmt.Errorf(pruneErrFmt, e.path, err)
			}
			res.Removed = append(res.Removed, e.path)
			res.RemovedBytes += e.size
			total -= e.size
			i++
		}
		survivors = survivors[i:]
	}

	res.Kept = len(survivors)
	return res, nil
}

type cacheEntry struct {
	path  string
	size  int64
	mtime time.Time
}

func listCacheEntries(dir string) ([]cacheEntry, error) {
	top, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read cache dir: %w", err)
	}
	out := make([]cacheEntry, 0, len(top))
	for _, e := range top {
		if e.IsDir() {
			sub := filepath.Join(dir, e.Name())
			subEntries, err := os.ReadDir(sub)
			if err != nil {
				if errors.Is(err, fs.ErrNotExist) {
					continue
				}
				return nil, fmt.Errorf("read cache subdir: %w", err)
			}
			for _, se := range subEntries {
				if se.IsDir() || !isCacheBlob(se.Name()) {
					continue
				}
				info, err := se.Info()
				if err != nil {
					return nil, fmt.Errorf("stat %s: %w", se.Name(), err)
				}
				out = append(out, cacheEntry{
					path:  filepath.Join(sub, se.Name()),
					size:  info.Size(),
					mtime: info.ModTime(),
				})
			}
			continue
		}
		if !isCacheBlob(e.Name()) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			return nil, fmt.Errorf("stat %s: %w", e.Name(), err)
		}
		out = append(out, cacheEntry{
			path:  filepath.Join(dir, e.Name()),
			size:  info.Size(),
			mtime: info.ModTime(),
		})
	}
	return out, nil
}

func EnsureGitignore(repoRoot string) error {
	path := filepath.Join(repoRoot, ".gitignore")
	want := []string{
		"toolsets/*/.sam-cache/",
		"toolsets/*/dist/",
		"toolsets/*/src/vendor/",
		"skills/*/.sam-cache/",
		"skills/*/tools/*/dist/",
	}

	existing, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("read .gitignore: %w", err)
	}
	have := make(map[string]struct{})
	for line := range strings.SplitSeq(string(existing), "\n") {
		trimmed := strings.TrimSpace(line)
		trimmed = strings.TrimPrefix(trimmed, "/")
		if trimmed != "" {
			have[trimmed] = struct{}{}
		}
	}
	var toAppend []string
	for _, w := range want {
		if _, ok := have[w]; ok {
			continue
		}
		if _, ok := have[strings.TrimSuffix(w, "/")]; ok {
			continue
		}
		toAppend = append(toAppend, w)
	}
	if len(toAppend) == 0 {
		return nil
	}

	var buf strings.Builder
	buf.Write(existing)
	if len(existing) > 0 && !strings.HasSuffix(string(existing), "\n") {
		buf.WriteByte('\n')
	}
	buf.WriteString("# Added by sam config — per-toolset build cache and outputs.\n")
	for _, w := range toAppend {
		buf.WriteString(w)
		buf.WriteByte('\n')
	}
	if err := os.WriteFile(path, []byte(buf.String()), 0o644); err != nil {
		return fmt.Errorf("write .gitignore: %w", err)
	}
	return nil
}

func PruneRepo(repoRoot string, opts PruneOptions) (PruneResult, error) {
	var agg PruneResult
	for _, root := range []string{"toolsets", "skills"} {
		if err := pruneResourceDir(filepath.Join(repoRoot, root), opts, &agg); err != nil {
			return agg, err
		}
	}
	return agg, nil
}

func pruneResourceDir(parent string, opts PruneOptions, agg *PruneResult) error {
	entries, err := os.ReadDir(parent)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("read %s: %w", parent, err)
	}
	for _, ent := range entries {
		if !ent.IsDir() {
			continue
		}
		cache := NewCache(filepath.Join(parent, ent.Name()))
		res, pruneErr := cache.Prune(opts)
		if pruneErr != nil {
			return pruneErr
		}
		agg.Removed = append(agg.Removed, res.Removed...)
		agg.RemovedBytes += res.RemovedBytes
		agg.Kept += res.Kept
	}
	return nil
}

func isCacheBlob(name string) bool {
	return strings.HasSuffix(name, zipCacheExt) || strings.HasSuffix(name, binCacheExt)
}

const (
	pruneErrFmt = "prune %s: %w"
	zipCacheExt = ".zip"
	binCacheExt = ".bin"
)

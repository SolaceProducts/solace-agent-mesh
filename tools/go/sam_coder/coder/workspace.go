// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package coder

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type workspace struct {
	root           string
	base           changeTracker
	readOnly       bool
	noExec         bool
	readDirs       []string
	env            []string
	shell          shellPolicy
	fetchDomains   []string
	fetchTransport http.RoundTripper
	hostTools      []HostTool
	hostSeq        int
}

func (w *workspace) resolve(rel string) (string, error) {
	if rel == "" || rel == "." {
		return w.root, nil
	}
	var joined string
	if filepath.IsAbs(rel) {
		joined = filepath.Clean(rel)
	} else {
		joined = filepath.Join(w.root, rel)
	}
	if !w.contains(joined) {
		return "", fmt.Errorf("path %q is outside the workspace", rel)
	}
	if resolved, err := evalExistingPrefix(joined); err == nil && !w.contains(resolved) {
		return "", fmt.Errorf("path %q resolves outside the workspace through a symlink", rel)
	}
	return joined, nil
}

func (w *workspace) contains(p string) bool {
	return within(w.root, p)
}

func within(root, p string) bool {
	return p == root || strings.HasPrefix(p, root+string(filepath.Separator))
}

func (w *workspace) resolveRead(rel string) (string, error) {
	if !filepath.IsAbs(rel) {
		return w.resolve(rel)
	}
	p := filepath.Clean(rel)
	for _, dir := range w.readDirs {
		if !within(dir, p) {
			continue
		}
		if resolved, err := evalExistingPrefix(p); err == nil && !within(dir, resolved) {
			return "", fmt.Errorf("path %q resolves outside %s through a symlink", rel, dir)
		}
		return p, nil
	}
	return w.resolve(rel)
}

func (w *workspace) rel(abs string) string {
	if !w.contains(abs) {
		return filepath.ToSlash(abs)
	}
	r, err := filepath.Rel(w.root, abs)
	if err != nil {
		return abs
	}
	return filepath.ToSlash(r)
}

func evalExistingPrefix(p string) (string, error) {
	missing := ""
	for cur := p; ; cur = filepath.Dir(cur) {
		if resolved, err := filepath.EvalSymlinks(cur); err == nil {
			return filepath.Join(resolved, missing), nil
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return "", errors.New("no existing prefix")
		}
		missing = filepath.Join(filepath.Base(cur), missing)
	}
}

func (w *workspace) searchFiles(ctx context.Context, dir string) ([]string, error) {
	if !w.noExec {
		if files, ok := gitFiles(ctx, dir); ok {
			return files, nil
		}
	}
	var files []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if d.IsDir() {
			if p != dir && skippedDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		rel, _ := filepath.Rel(dir, p)
		files = append(files, filepath.ToSlash(rel))
		return nil
	})
	return files, err
}

func gitFiles(ctx context.Context, dir string) ([]string, bool) {
	cmd := execCommand(ctx, "git", "-c", "safe.directory=*", "-C", dir,
		"ls-files", "-z", "--cached", "--others", "--exclude-standard")
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.Output()
	if err != nil {
		return nil, false
	}
	var files []string
	for _, rel := range strings.Split(string(out), "\x00") {
		if rel == "" || rel == stateDirName || strings.HasPrefix(rel, stateDirName+"/") {
			continue
		}
		if info, err := os.Lstat(filepath.Join(dir, rel)); err != nil || info.IsDir() {
			continue
		}
		files = append(files, rel)
	}
	sort.Strings(files)
	return files, true
}

var skippedDirs = map[string]bool{stateDirName: true, ".git": true, "node_modules": true, "vendor": true, "__pycache__": true, ".venv": true, "dist": true, "build": true}

// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package coder

import (
	"bytes"
	"context"
	"encoding/gob"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

const maxDiffBytes = 256 << 10

type baseline struct {
	gitDir  string
	workDir string
}

func takeBaseline(ctx context.Context, workDir string) (*baseline, error) {
	if _, err := exec.LookPath("git"); err != nil {
		return nil, fmt.Errorf("git is not installed: %w", err)
	}
	gitDir := filepath.Join(stateDir(workDir), "baseline.git")
	b := &baseline{gitDir: gitDir, workDir: workDir}
	if _, err := os.Stat(filepath.Join(gitDir, "HEAD")); err == nil {
		return b, nil
	}
	if err := ensureStateDir(workDir); err != nil {
		return nil, err
	}
	if _, err := b.git(ctx, "init", "-q"); err != nil {
		_ = os.RemoveAll(gitDir)
		return nil, err
	}
	exclude := filepath.Join(gitDir, "info", "exclude")
	if err := os.MkdirAll(filepath.Dir(exclude), 0o755); err != nil {
		_ = os.RemoveAll(gitDir)
		return nil, err
	}
	if err := os.WriteFile(exclude, []byte("/"+stateDirName+"/\n"), 0o644); err != nil {
		_ = os.RemoveAll(gitDir)
		return nil, err
	}
	for _, args := range [][]string{
		{"add", "-A"},
		{"commit", "-q", "--allow-empty", "--no-verify", "-m", "baseline"},
	} {
		if _, err := b.git(ctx, args...); err != nil {
			_ = os.RemoveAll(gitDir)
			return nil, err
		}
	}
	return b, nil
}

type ChangeSet struct {
	Diff      string   `json:"diff"`
	Truncated bool     `json:"diff_truncated,omitempty"`
	Files     []string `json:"files_changed"`
	Stat      string   `json:"stat"`
}

func (cs *ChangeSet) truncate() {
	if len(cs.Diff) > maxDiffBytes {
		cs.Diff = cs.Diff[:maxDiffBytes]
		cs.Truncated = true
	}
}

type changeTracker interface {
	changes(ctx context.Context) (ChangeSet, error)
}

func snapshotWorkspace(ctx context.Context, root string, noExec bool) (changeTracker, error) {
	if noExec {
		return takeMemBaseline(root)
	}
	return takeBaseline(ctx, root)
}

const (
	memBaselineFile     = "baseline.gob"
	memBaselineMaxBytes = 64 << 20
)

type memBaseline struct {
	root  string
	files map[string][]byte
}

func takeMemBaseline(root string) (*memBaseline, error) {
	b := &memBaseline{root: root}
	saved := filepath.Join(stateDir(root), memBaselineFile)
	data, err := os.ReadFile(saved)
	switch {
	case err == nil:
		if err = gob.NewDecoder(bytes.NewReader(data)).Decode(&b.files); err != nil {
			return nil, fmt.Errorf("reading the saved baseline: %w", err)
		}
		return b, nil
	case !errors.Is(err, fs.ErrNotExist):
		return nil, err
	}
	if b.files, err = readTree(root); err != nil {
		return nil, err
	}
	if err := ensureStateDir(root); err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(b.files); err != nil {
		return nil, err
	}
	if err := os.WriteFile(saved, buf.Bytes(), 0o600); err != nil {
		return nil, err
	}
	return b, nil
}

func readTree(root string) (map[string][]byte, error) {
	files := map[string][]byte{}
	total := 0
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if p != root && skippedDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if total += len(data); total > memBaselineMaxBytes {
			return fmt.Errorf("the workspace holds more than %d MiB, too much to diff in memory", memBaselineMaxBytes>>20)
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		files[filepath.ToSlash(rel)] = data
		return nil
	})
	return files, err
}

func (b *memBaseline) changes(context.Context) (ChangeSet, error) {
	now, err := readTree(b.root)
	if err != nil {
		return ChangeSet{}, err
	}
	paths := make([]string, 0, len(now))
	for p := range now {
		paths = append(paths, p)
	}
	for p := range b.files {
		if _, ok := now[p]; !ok {
			paths = append(paths, p)
		}
	}
	sort.Strings(paths)
	cs := ChangeSet{Files: []string{}}
	var diff strings.Builder
	insertions, deletions := 0, 0
	for _, p := range paths {
		before, had := b.files[p]
		after, has := now[p]
		status := "M"
		switch {
		case had && has && bytes.Equal(before, after):
			continue
		case !had:
			status = "A"
		case !has:
			status = "D"
		}
		cs.Files = append(cs.Files, status+" "+p)
		ins, del := writeFileDiff(&diff, p, before, after, had, has)
		insertions += ins
		deletions += del
	}
	cs.Diff = diff.String()
	cs.Stat = shortStat(len(cs.Files), insertions, deletions)
	cs.truncate()
	return cs, nil
}

func writeFileDiff(w *strings.Builder, path string, before, after []byte, had, has bool) (int, int) {
	from, to := "a/"+path, "b/"+path
	fmt.Fprintf(w, "diff --git %s %s\n", from, to)
	switch {
	case !had:
		from = "/dev/null"
		w.WriteString("new file mode 100644\n")
	case !has:
		to = "/dev/null"
		w.WriteString("deleted file mode 100644\n")
	}
	if bytes.IndexByte(before, 0) >= 0 || bytes.IndexByte(after, 0) >= 0 {
		fmt.Fprintf(w, "Binary files %s and %s differ\n", from, to)
		return 0, 0
	}
	hunks, ins, del := unifiedHunks(string(before), string(after))
	if hunks != "" {
		fmt.Fprintf(w, "--- %s\n+++ %s\n%s", from, to, hunks)
	}
	return ins, del
}

func shortStat(files, insertions, deletions int) string {
	if files == 0 {
		return ""
	}
	s := fmt.Sprintf("%d %s changed", files, plural(files, "file", "files"))
	if insertions > 0 {
		s += fmt.Sprintf(", %d %s(+)", insertions, plural(insertions, "insertion", "insertions"))
	}
	if deletions > 0 {
		s += fmt.Sprintf(", %d %s(-)", deletions, plural(deletions, "deletion", "deletions"))
	}
	return s
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func (b *baseline) changes(ctx context.Context) (ChangeSet, error) {
	if _, err := b.git(ctx, "add", "-A"); err != nil {
		return ChangeSet{}, err
	}
	diff, err := b.git(ctx, "diff", "--cached", "--no-color", "--no-ext-diff")
	if err != nil {
		return ChangeSet{}, err
	}
	names, err := b.git(ctx, "diff", "--cached", "--name-status")
	if err != nil {
		return ChangeSet{}, err
	}
	stat, err := b.git(ctx, "diff", "--cached", "--shortstat")
	if err != nil {
		return ChangeSet{}, err
	}
	cs := ChangeSet{Diff: diff, Stat: strings.TrimSpace(stat), Files: []string{}}
	cs.truncate()
	for _, l := range strings.Split(strings.TrimSpace(names), "\n") {
		if l != "" {
			cs.Files = append(cs.Files, strings.Join(strings.Fields(l), " "))
		}
	}
	return cs, nil
}

func (b *baseline) git(ctx context.Context, args ...string) (string, error) {
	full := append([]string{
		"--git-dir=" + b.gitDir, "--work-tree=" + b.workDir,
		"-c", "user.name=sam_coder", "-c", "user.email=sam_coder@localhost",
		"-c", "commit.gpgsign=false", "-c", "core.autocrlf=false",
		"-c", "gc.auto=0", "-c", "maintenance.auto=false",
	}, args...)
	cmd := execCommand(ctx, "git", full...)
	cmd.Dir = b.workDir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %v: %s", args[0], err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

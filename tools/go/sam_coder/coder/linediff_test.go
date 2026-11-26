// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package coder

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func numbered(from, to int) string {
	var b strings.Builder
	for i := from; i <= to; i++ {
		fmt.Fprintf(&b, "line %d\n", i)
	}
	return b.String()
}

func TestUnifiedHunks(t *testing.T) {
	for name, tc := range map[string]struct {
		a, b, want string
		ins, del   int
	}{
		"same":     {a: "x\n", b: "x\n", want: ""},
		"new file": {a: "", b: "a\nb\n", want: "@@ -0,0 +1,2 @@\n+a\n+b\n", ins: 2},
		"emptied":  {a: "a\n", b: "", want: "@@ -1 +0,0 @@\n-a\n", del: 1},
		"one line in the middle": {
			a: numbered(1, 10), b: strings.Replace(numbered(1, 10), "line 5\n", "five\n", 1),
			want: "@@ -2,7 +2,7 @@\n line 2\n line 3\n line 4\n-line 5\n+five\n line 6\n line 7\n line 8\n", ins: 1, del: 1,
		},
		"far apart changes make two hunks": {
			a: numbered(1, 20), b: strings.NewReplacer("line 2\n", "two\n", "line 19\n", "").Replace(numbered(1, 20)),
			want: "@@ -1,5 +1,5 @@\n line 1\n-line 2\n+two\n line 3\n line 4\n line 5\n" +
				"@@ -16,5 +16,4 @@\n line 16\n line 17\n line 18\n-line 19\n line 20\n", ins: 1, del: 2,
		},
		"near changes share a hunk": {
			a: numbered(1, 12), b: strings.NewReplacer("line 3\n", "three\n", "line 9\n", "nine\n").Replace(numbered(1, 12)),
			want: "@@ -1,12 +1,12 @@\n line 1\n line 2\n-line 3\n+three\n line 4\n line 5\n line 6\n line 7\n line 8\n-line 9\n+nine\n line 10\n line 11\n line 12\n", ins: 2, del: 2,
		},
		"missing final newline": {a: "a\nb", b: "a\nb\n", want: "@@ -1,2 +1,2 @@\n a\n-b\n\\ No newline at end of file\n+b\n", ins: 1, del: 1},
		"insertion between kept lines": {
			a: "a\nb\nc\n", b: "a\nb\nnew\nc\n", want: "@@ -1,3 +1,4 @@\n a\n b\n+new\n c\n", ins: 1,
		},
	} {
		got, ins, del := unifiedHunks(tc.a, tc.b)
		if got != tc.want || ins != tc.ins || del != tc.del {
			t.Errorf("%s: got +%d -%d\n%s\nwant +%d -%d\n%s", name, ins, del, got, tc.ins, tc.del, tc.want)
		}
	}
}

func TestLargeDifferingFilesFallBackToRemoveAndAdd(t *testing.T) {
	a, b := numbered(1, 3000), numbered(3001, 6000)
	got, ins, del := unifiedHunks(a, b)
	if ins != 3000 || del != 3000 || !strings.HasPrefix(got, "@@ -1,3000 +1,3000 @@\n-line 1\n") {
		t.Errorf("+%d -%d, starts %q", ins, del, got[:40])
	}
}

func TestTheInMemoryBaselineMatchesGit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	gitDir, memDir := t.TempDir(), t.TempDir()
	before := map[string]string{"keep.txt": "same\n", "edit.go": numbered(1, 30), "gone.md": "old\ntext\n", "sub/deep.txt": "a\nb\nc\n"}
	for _, dir := range []string{gitDir, memDir} {
		for name, content := range before {
			writeFile(t, filepath.Join(dir, name), content)
		}
	}
	gitBase, err := takeBaseline(context.Background(), gitDir)
	if err != nil {
		t.Fatal(err)
	}
	memBase, err := takeMemBaseline(memDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{gitDir, memDir} {
		writeFile(t, filepath.Join(dir, "edit.go"), strings.NewReplacer("line 4\n", "four\n", "line 25\n", "").Replace(numbered(1, 30))+"tail\n")
		writeFile(t, filepath.Join(dir, "sub", "deep.txt"), "a\nb\nc")
		writeFile(t, filepath.Join(dir, "added.txt"), "fresh\n")
		if err = os.Remove(filepath.Join(dir, "gone.md")); err != nil {
			t.Fatal(err)
		}
	}

	want, err := gitBase.changes(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got, err := memBase.changes(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if strings.Join(got.Files, ",") != strings.Join(want.Files, ",") || got.Stat != want.Stat {
		t.Errorf("files %v stat %q, git: %v %q", got.Files, got.Stat, want.Files, want.Stat)
	}
	if got.Diff != withoutGitExtras(want.Diff) {
		t.Errorf("diff:\n%s\ngit:\n%s", got.Diff, withoutGitExtras(want.Diff))
	}
}

func withoutGitExtras(diff string) string {
	var kept []string
	for _, l := range strings.SplitAfter(diff, "\n") {
		switch {
		case strings.HasPrefix(l, "index "):
		case strings.HasPrefix(l, "@@ "):
			end := strings.Index(l[3:], " @@") + 3
			kept = append(kept, l[:end+3]+"\n")
		default:
			kept = append(kept, l)
		}
	}
	return strings.Join(kept, "")
}

// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	sdk "github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"
)

func newCtx(t *testing.T, workspace string) *sdk.ToolContext {
	t.Helper()
	tc := &sdk.ToolContext{
		UserID:    "test-user",
		SessionID: "test-session",
		VolumeMounts: []sdk.VolumeMount{
			{VolumeID: "vol-test", MountPath: "/workspace", ReadOnly: false},
		},
	}
	tc.VolumeMounts[0].MountPath = workspace
	return tc
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func mustReadString(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestVolumeRead_HappyPath(t *testing.T) {
	ws := t.TempDir()
	mustWrite(t, filepath.Join(ws, "hello.txt"), "hi there\n")
	res, err := volumeRead(context.Background(), volumeReadParams{Path: "hello.txt"}, newCtx(t, ws))
	if err != nil {
		t.Fatal(err)
	}
	if res.Status == sdk.StatusError {
		t.Fatalf("expected success, got error: %s", res.Message)
	}
	if !strings.Contains(res.Message, "hi there") {
		t.Errorf("output %q does not contain expected content", res.Message)
	}
}

func TestVolumeRead_LineRange(t *testing.T) {
	ws := t.TempDir()
	mustWrite(t, filepath.Join(ws, "f.txt"), "1\n2\n3\n4\n5\n")
	res, _ := volumeRead(context.Background(), volumeReadParams{Path: "f.txt", StartLine: 2, EndLine: 4}, newCtx(t, ws))
	if res.Status == sdk.StatusError {
		t.Fatal(res.Message)
	}
	if res.Message != "2\n3\n4\n" {
		t.Errorf("got %q, want %q", res.Message, "2\n3\n4\n")
	}
}

func TestVolumeRead_TraversalRejected(t *testing.T) {
	ws := t.TempDir()
	res, _ := volumeRead(context.Background(), volumeReadParams{Path: "../../etc/passwd"}, newCtx(t, ws))
	if res.Status != sdk.StatusError {
		t.Errorf("traversal should be rejected, got success: %s", res.Message)
	}
}

func TestVolumeRead_DirectoryRejected(t *testing.T) {
	ws := t.TempDir()
	if err := os.Mkdir(filepath.Join(ws, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	res, _ := volumeRead(context.Background(), volumeReadParams{Path: "sub"}, newCtx(t, ws))
	if res.Status != sdk.StatusError || !strings.Contains(res.Message, "volume_list") {
		t.Errorf("expected directory-rejection with hint to volume_list, got %v: %s", res.Status == sdk.StatusError, res.Message)
	}
}

func TestVolumeList_Flat(t *testing.T) {
	ws := t.TempDir()
	mustWrite(t, filepath.Join(ws, "a.txt"), "a")
	mustWrite(t, filepath.Join(ws, "b.go"), "package main")
	if err := os.Mkdir(filepath.Join(ws, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	res, _ := volumeList(context.Background(), volumeListParams{}, newCtx(t, ws))
	if res.Status == sdk.StatusError {
		t.Fatal(res.Message)
	}
	for _, want := range []string{"a.txt", "b.go", "sub"} {
		if !strings.Contains(res.Message, want) {
			t.Errorf("output missing %q: %s", want, res.Message)
		}
	}
}

func TestVolumeList_RecursiveWithGlob(t *testing.T) {
	ws := t.TempDir()
	mustWrite(t, filepath.Join(ws, "x.txt"), "")
	mustWrite(t, filepath.Join(ws, "src", "main.go"), "")
	mustWrite(t, filepath.Join(ws, "src", "doc.txt"), "")
	res, _ := volumeList(context.Background(), volumeListParams{Recursive: true, Glob: "*.go"}, newCtx(t, ws))
	if res.Status == sdk.StatusError {
		t.Fatal(res.Message)
	}
	if !strings.Contains(res.Message, "main.go") {
		t.Error("recursive glob missed main.go")
	}
	if strings.Contains(res.Message, "x.txt") || strings.Contains(res.Message, "doc.txt") {
		t.Errorf("glob *.go should filter out non-Go files, got: %s", res.Message)
	}
}

func TestVolumeGlob_DoubleStarRecurses(t *testing.T) {
	ws := t.TempDir()
	mustWrite(t, filepath.Join(ws, "src", "a.go"), "")
	mustWrite(t, filepath.Join(ws, "src", "pkg", "b.go"), "")
	mustWrite(t, filepath.Join(ws, "README.md"), "")
	res, _ := volumeGlob(context.Background(), volumeGlobParams{Pattern: "**/*.go"}, newCtx(t, ws))
	if res.Status == sdk.StatusError {
		t.Fatal(res.Message)
	}
	for _, want := range []string{"src/a.go", "src/pkg/b.go"} {
		if !strings.Contains(res.Message, want) {
			t.Errorf("output missing %q: %s", want, res.Message)
		}
	}
	if strings.Contains(res.Message, "README.md") {
		t.Errorf("**/*.go should not match README.md: %s", res.Message)
	}
}

func TestVolumeGlob_SingleStarSingleSegment(t *testing.T) {
	ws := t.TempDir()
	mustWrite(t, filepath.Join(ws, "a.go"), "")
	mustWrite(t, filepath.Join(ws, "src", "b.go"), "")
	res, _ := volumeGlob(context.Background(), volumeGlobParams{Pattern: "*.go"}, newCtx(t, ws))
	if res.Status == sdk.StatusError {
		t.Fatal(res.Message)
	}
	if !strings.Contains(res.Message, "a.go") {
		t.Error("*.go should match top-level a.go")
	}
	if strings.Contains(res.Message, "src/b.go") {
		t.Errorf("single * should not match across separators: %s", res.Message)
	}
}

func TestVolumeGrep_ContentMode(t *testing.T) {
	ws := t.TempDir()
	mustWrite(t, filepath.Join(ws, "a.txt"), "alpha TODO line\nbeta\n")
	mustWrite(t, filepath.Join(ws, "b.txt"), "gamma TODO line\n")
	res, _ := volumeGrep(context.Background(), volumeGrepParams{Pattern: "TODO"}, newCtx(t, ws))
	if res.Status == sdk.StatusError {
		t.Fatal(res.Message)
	}
	if !strings.Contains(res.Message, "a.txt:1:") || !strings.Contains(res.Message, "b.txt:1:") {
		t.Errorf("expected matches in both files: %s", res.Message)
	}
}

func TestVolumeGrep_FilesWithMatches(t *testing.T) {
	ws := t.TempDir()
	mustWrite(t, filepath.Join(ws, "a.txt"), "alpha\n")
	mustWrite(t, filepath.Join(ws, "b.txt"), "beta\n")
	res, _ := volumeGrep(context.Background(), volumeGrepParams{Pattern: "alpha", OutputMode: "files_with_matches"}, newCtx(t, ws))
	if res.Status == sdk.StatusError {
		t.Fatal(res.Message)
	}
	if !strings.Contains(res.Message, "a.txt") || strings.Contains(res.Message, "b.txt") {
		t.Errorf("expected only a.txt: %s", res.Message)
	}
}

func TestVolumeGrep_CountMode(t *testing.T) {
	ws := t.TempDir()
	mustWrite(t, filepath.Join(ws, "a.txt"), "x\nx\nx\n")
	res, _ := volumeGrep(context.Background(), volumeGrepParams{Pattern: "x", OutputMode: "count"}, newCtx(t, ws))
	if res.Status == sdk.StatusError {
		t.Fatal(res.Message)
	}
	if !strings.Contains(res.Message, "3 a.txt") {
		t.Errorf("expected count of 3, got: %s", res.Message)
	}
}

func TestVolumeGrep_SkipsBinaryFiles(t *testing.T) {
	ws := t.TempDir()
	mustWrite(t, filepath.Join(ws, "text.txt"), "match here\n")
	mustWrite(t, filepath.Join(ws, "bin.dat"), "match\x00here\n")
	res, _ := volumeGrep(context.Background(), volumeGrepParams{Pattern: "match"}, newCtx(t, ws))
	if res.Status == sdk.StatusError {
		t.Fatal(res.Message)
	}
	if !strings.Contains(res.Message, "text.txt") {
		t.Error("text file missed")
	}
	if strings.Contains(res.Message, "bin.dat") {
		t.Errorf("binary file should be skipped: %s", res.Message)
	}
}

func TestVolumeGrep_SkipsNoiseDirs(t *testing.T) {
	ws := t.TempDir()
	mustWrite(t, filepath.Join(ws, "src", "a.txt"), "match\n")
	mustWrite(t, filepath.Join(ws, ".git", "config"), "match\n")
	mustWrite(t, filepath.Join(ws, "node_modules", "lib.js"), "match\n")
	res, _ := volumeGrep(context.Background(), volumeGrepParams{Pattern: "match", OutputMode: "files_with_matches"}, newCtx(t, ws))
	if res.Status == sdk.StatusError {
		t.Fatal(res.Message)
	}
	if !strings.Contains(res.Message, "src/a.txt") {
		t.Error("src/a.txt missed")
	}
	if strings.Contains(res.Message, ".git") || strings.Contains(res.Message, "node_modules") {
		t.Errorf("noise directories should be skipped: %s", res.Message)
	}
}

func TestVolumeGrep_BadRegexRejected(t *testing.T) {
	ws := t.TempDir()
	res, _ := volumeGrep(context.Background(), volumeGrepParams{Pattern: "(invalid"}, newCtx(t, ws))
	if res.Status != sdk.StatusError {
		t.Errorf("expected error for invalid regex, got %s", res.Message)
	}
}

func TestVolumeStat_File(t *testing.T) {
	ws := t.TempDir()
	mustWrite(t, filepath.Join(ws, "f.txt"), "12345")
	res, _ := volumeStat(context.Background(), volumeStatParams{Path: "f.txt"}, newCtx(t, ws))
	if res.Status == sdk.StatusError {
		t.Fatal(res.Message)
	}
	for _, want := range []string{"kind:    file", "size:    5", "path:    f.txt"} {
		if !strings.Contains(res.Message, want) {
			t.Errorf("output missing %q: %s", want, res.Message)
		}
	}
}

func TestVolumeStat_MissingPathErrors(t *testing.T) {
	ws := t.TempDir()
	res, _ := volumeStat(context.Background(), volumeStatParams{Path: "nope"}, newCtx(t, ws))
	if res.Status != sdk.StatusError {
		t.Errorf("expected error, got %s", res.Message)
	}
}

func TestVolumeWrite_RefusesOverwrite(t *testing.T) {
	ws := t.TempDir()
	mustWrite(t, filepath.Join(ws, "exists.txt"), "old")
	res, _ := volumeWrite(context.Background(), volumeWriteParams{Path: "exists.txt", Content: "new"}, newCtx(t, ws))
	if res.Status != sdk.StatusError {
		t.Errorf("expected overwrite refusal: %s", res.Message)
	}
	if mustReadString(t, filepath.Join(ws, "exists.txt")) != "old" {
		t.Error("file was modified despite refusal")
	}
}

func TestVolumeWrite_OverwriteFlagAllowsReplace(t *testing.T) {
	ws := t.TempDir()
	mustWrite(t, filepath.Join(ws, "f.txt"), "old")
	res, _ := volumeWrite(context.Background(), volumeWriteParams{Path: "f.txt", Content: "new", Overwrite: true}, newCtx(t, ws))
	if res.Status == sdk.StatusError {
		t.Fatal(res.Message)
	}
	if got := mustReadString(t, filepath.Join(ws, "f.txt")); got != "new" {
		t.Errorf("got %q, want %q", got, "new")
	}
}

func TestVolumeWrite_RefusesSymlinkOnOverwrite(t *testing.T) {
	ws := t.TempDir()
	real := filepath.Join(ws, "real.txt")
	mustWrite(t, real, "original")
	if err := os.Symlink(real, filepath.Join(ws, "link.txt")); err != nil {
		t.Skipf("symlinks not supported here: %v", err)
	}
	res, _ := volumeWrite(context.Background(),
		volumeWriteParams{Path: "link.txt", Content: "clobbered", Overwrite: true},
		newCtx(t, ws))
	if res.Status != sdk.StatusError || !strings.Contains(res.Message, "symlink") {
		t.Errorf("expected symlink refusal, got error=%v: %s", res.Status == sdk.StatusError, res.Message)
	}
	if got := mustReadString(t, real); got != "original" {
		t.Errorf("symlink target was overwritten despite refusal: %q", got)
	}
}

func TestVolumeWrite_CreateParents(t *testing.T) {
	ws := t.TempDir()
	res, _ := volumeWrite(context.Background(), volumeWriteParams{
		Path: "a/b/c.txt", Content: "deep", CreateParents: true,
	}, newCtx(t, ws))
	if res.Status == sdk.StatusError {
		t.Fatal(res.Message)
	}
	if got := mustReadString(t, filepath.Join(ws, "a", "b", "c.txt")); got != "deep" {
		t.Errorf("got %q, want %q", got, "deep")
	}
}

func TestVolumeEdit_SingleReplace(t *testing.T) {
	ws := t.TempDir()
	mustWrite(t, filepath.Join(ws, "f.txt"), "alpha beta gamma\n")
	res, _ := volumeEdit(context.Background(), volumeEditParams{Path: "f.txt", OldString: "beta", NewString: "BETA"}, newCtx(t, ws))
	if res.Status == sdk.StatusError {
		t.Fatal(res.Message)
	}
	if got := mustReadString(t, filepath.Join(ws, "f.txt")); got != "alpha BETA gamma\n" {
		t.Errorf("got %q", got)
	}
}

func TestVolumeEdit_AmbiguousMatchRejected(t *testing.T) {
	ws := t.TempDir()
	mustWrite(t, filepath.Join(ws, "f.txt"), "x x x\n")
	res, _ := volumeEdit(context.Background(), volumeEditParams{Path: "f.txt", OldString: "x", NewString: "y"}, newCtx(t, ws))
	if res.Status != sdk.StatusError {
		t.Errorf("expected ambiguity error, got %s", res.Message)
	}
	if got := mustReadString(t, filepath.Join(ws, "f.txt")); got != "x x x\n" {
		t.Errorf("file was modified despite refusal: %q", got)
	}
}

func TestVolumeEdit_ReplaceAll(t *testing.T) {
	ws := t.TempDir()
	mustWrite(t, filepath.Join(ws, "f.txt"), "x x x\n")
	res, _ := volumeEdit(context.Background(), volumeEditParams{Path: "f.txt", OldString: "x", NewString: "y", ReplaceAll: true}, newCtx(t, ws))
	if res.Status == sdk.StatusError {
		t.Fatal(res.Message)
	}
	if got := mustReadString(t, filepath.Join(ws, "f.txt")); got != "y y y\n" {
		t.Errorf("got %q", got)
	}
}

func TestVolumeEdit_NoMatchRejected(t *testing.T) {
	ws := t.TempDir()
	mustWrite(t, filepath.Join(ws, "f.txt"), "hello\n")
	res, _ := volumeEdit(context.Background(), volumeEditParams{Path: "f.txt", OldString: "absent", NewString: "x"}, newCtx(t, ws))
	if res.Status != sdk.StatusError {
		t.Errorf("expected no-match error, got %s", res.Message)
	}
}

func TestVolumeEdit_RefusesSymlink(t *testing.T) {
	ws := t.TempDir()
	real := filepath.Join(ws, "real.txt")
	mustWrite(t, real, "hello")
	if err := os.Symlink(real, filepath.Join(ws, "link.txt")); err != nil {
		t.Skipf("symlinks not supported here: %v", err)
	}
	res, _ := volumeEdit(context.Background(), volumeEditParams{Path: "link.txt", OldString: "hello", NewString: "x"}, newCtx(t, ws))
	if res.Status != sdk.StatusError || !strings.Contains(res.Message, "symlink") {
		t.Errorf("expected symlink refusal, got %v: %s", res.Status == sdk.StatusError, res.Message)
	}
}

func TestVolumeAppend_HappyPath(t *testing.T) {
	ws := t.TempDir()
	mustWrite(t, filepath.Join(ws, "log.txt"), "line 1\n")
	res, _ := volumeAppend(context.Background(), volumeAppendParams{Path: "log.txt", Content: "line 2\n"}, newCtx(t, ws))
	if res.Status == sdk.StatusError {
		t.Fatal(res.Message)
	}
	if got := mustReadString(t, filepath.Join(ws, "log.txt")); got != "line 1\nline 2\n" {
		t.Errorf("got %q", got)
	}
}

func TestVolumeAppend_RefusesSymlink(t *testing.T) {
	ws := t.TempDir()
	real := filepath.Join(ws, "real.txt")
	mustWrite(t, real, "x")
	if err := os.Symlink(real, filepath.Join(ws, "link.txt")); err != nil {
		t.Skipf("symlinks not supported: %v", err)
	}
	res, _ := volumeAppend(context.Background(), volumeAppendParams{Path: "link.txt", Content: "y"}, newCtx(t, ws))
	if res.Status != sdk.StatusError {
		t.Errorf("expected symlink refusal, got %s", res.Message)
	}
}

func TestVolumeDelete_File(t *testing.T) {
	ws := t.TempDir()
	mustWrite(t, filepath.Join(ws, "f.txt"), "x")
	res, _ := volumeDelete(context.Background(), volumeDeleteParams{Path: "f.txt"}, newCtx(t, ws))
	if res.Status == sdk.StatusError {
		t.Fatal(res.Message)
	}
	if _, err := os.Stat(filepath.Join(ws, "f.txt")); !os.IsNotExist(err) {
		t.Errorf("file still exists: %v", err)
	}
}

func TestVolumeDelete_NonEmptyDirRequiresRecursive(t *testing.T) {
	ws := t.TempDir()
	mustWrite(t, filepath.Join(ws, "sub", "f.txt"), "x")
	res, _ := volumeDelete(context.Background(), volumeDeleteParams{Path: "sub"}, newCtx(t, ws))
	if res.Status != sdk.StatusError {
		t.Errorf("expected refusal of non-empty dir without recursive, got %s", res.Message)
	}
	if _, err := os.Stat(filepath.Join(ws, "sub", "f.txt")); err != nil {
		t.Errorf("file should still exist: %v", err)
	}
}

func TestVolumeDelete_RecursiveTree(t *testing.T) {
	ws := t.TempDir()
	mustWrite(t, filepath.Join(ws, "sub", "a", "f.txt"), "x")
	res, _ := volumeDelete(context.Background(), volumeDeleteParams{Path: "sub", Recursive: true}, newCtx(t, ws))
	if res.Status == sdk.StatusError {
		t.Fatal(res.Message)
	}
	if _, err := os.Stat(filepath.Join(ws, "sub")); !os.IsNotExist(err) {
		t.Errorf("tree should be gone: %v", err)
	}
}

func TestVolumeDelete_RefusesWorkspaceRoot(t *testing.T) {
	ws := t.TempDir()
	for _, p := range []string{"", ".", "/"} {
		res, _ := volumeDelete(context.Background(), volumeDeleteParams{Path: p, Recursive: true}, newCtx(t, ws))
		if res.Status != sdk.StatusError {
			t.Errorf("expected root-refusal for %q, got success", p)
		}
	}
	if _, err := os.Stat(ws); err != nil {
		t.Errorf("workspace root was deleted: %v", err)
	}
}

func TestVolumeMove_HappyPath(t *testing.T) {
	ws := t.TempDir()
	mustWrite(t, filepath.Join(ws, "a.txt"), "x")
	res, _ := volumeMove(context.Background(), volumeMoveParams{From: "a.txt", To: "b.txt"}, newCtx(t, ws))
	if res.Status == sdk.StatusError {
		t.Fatal(res.Message)
	}
	if _, err := os.Stat(filepath.Join(ws, "a.txt")); !os.IsNotExist(err) {
		t.Error("source should be gone")
	}
	if got := mustReadString(t, filepath.Join(ws, "b.txt")); got != "x" {
		t.Errorf("destination content = %q", got)
	}
}

func TestVolumeMove_RefusesOverwrite(t *testing.T) {
	ws := t.TempDir()
	mustWrite(t, filepath.Join(ws, "a.txt"), "src")
	mustWrite(t, filepath.Join(ws, "b.txt"), "dst")
	res, _ := volumeMove(context.Background(), volumeMoveParams{From: "a.txt", To: "b.txt"}, newCtx(t, ws))
	if res.Status != sdk.StatusError {
		t.Errorf("expected overwrite refusal, got %s", res.Message)
	}
	if mustReadString(t, filepath.Join(ws, "a.txt")) != "src" {
		t.Error("source changed despite refusal")
	}
	if mustReadString(t, filepath.Join(ws, "b.txt")) != "dst" {
		t.Error("destination changed despite refusal")
	}
}

func TestVolumeMove_CreateParents(t *testing.T) {
	ws := t.TempDir()
	mustWrite(t, filepath.Join(ws, "a.txt"), "x")
	res, _ := volumeMove(context.Background(), volumeMoveParams{From: "a.txt", To: "deep/path/b.txt", CreateParents: true}, newCtx(t, ws))
	if res.Status == sdk.StatusError {
		t.Fatal(res.Message)
	}
	if got := mustReadString(t, filepath.Join(ws, "deep", "path", "b.txt")); got != "x" {
		t.Errorf("got %q", got)
	}
}

func TestVolumeMkdir_HappyPath(t *testing.T) {
	ws := t.TempDir()
	res, _ := volumeMkdir(context.Background(), volumeMkdirParams{Path: "newdir"}, newCtx(t, ws))
	if res.Status == sdk.StatusError {
		t.Fatal(res.Message)
	}
	info, err := os.Stat(filepath.Join(ws, "newdir"))
	if err != nil || !info.IsDir() {
		t.Errorf("directory not created: %v", err)
	}
}

func TestVolumeMkdir_Recursive(t *testing.T) {
	ws := t.TempDir()
	res, _ := volumeMkdir(context.Background(), volumeMkdirParams{Path: "a/b/c", Recursive: true}, newCtx(t, ws))
	if res.Status == sdk.StatusError {
		t.Fatal(res.Message)
	}
	if _, err := os.Stat(filepath.Join(ws, "a", "b", "c")); err != nil {
		t.Errorf("recursive mkdir failed: %v", err)
	}
}

func TestVolumeMkdir_AlreadyExistsIsNoop(t *testing.T) {
	ws := t.TempDir()
	if err := os.Mkdir(filepath.Join(ws, "existing"), 0o755); err != nil {
		t.Fatal(err)
	}
	res, _ := volumeMkdir(context.Background(), volumeMkdirParams{Path: "existing"}, newCtx(t, ws))
	if res.Status == sdk.StatusError {
		t.Errorf("expected no-op success when dir exists, got %s", res.Message)
	}
}

func TestVolumeMkdir_FailsIfFileExists(t *testing.T) {
	ws := t.TempDir()
	mustWrite(t, filepath.Join(ws, "f.txt"), "x")
	res, _ := volumeMkdir(context.Background(), volumeMkdirParams{Path: "f.txt"}, newCtx(t, ws))
	if res.Status != sdk.StatusError {
		t.Errorf("expected error when path exists as a file, got %s", res.Message)
	}
}

func TestVolumeWrite_SymlinkedParentEscapeRejected(t *testing.T) {
	ws := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(ws, "escape")); err != nil {
		t.Skipf("symlink create not supported: %v", err)
	}
	res, _ := volumeWrite(context.Background(),
		volumeWriteParams{Path: "escape/newfile.txt", Content: "x", CreateParents: true},
		newCtx(t, ws))
	if res.Status != sdk.StatusError {
		t.Fatalf("write through symlinked parent escaping the volume should be rejected, got: %s", res.Message)
	}
	if _, err := os.Stat(filepath.Join(outside, "newfile.txt")); !os.IsNotExist(err) {
		t.Errorf("file must NOT have been created outside the volume root (stat err=%v)", err)
	}
}

func TestVolumeWrite_SymlinkedMountPathTopLevel(t *testing.T) {
	realWS := t.TempDir()
	linkWS := filepath.Join(t.TempDir(), "wslink")
	if err := os.Symlink(realWS, linkWS); err != nil {
		t.Skipf("symlink create not supported: %v", err)
	}
	res, _ := volumeWrite(context.Background(),
		volumeWriteParams{Path: "top.txt", Content: "hi"},
		newCtx(t, linkWS))
	if res.Status == sdk.StatusError {
		t.Fatalf("top-level write under a symlinked mount path should succeed: %s", res.Message)
	}
	if got := mustReadString(t, filepath.Join(realWS, "top.txt")); got != "hi" {
		t.Errorf("write should land in the real workspace, got %q", got)
	}
}

func TestVolumeGrep_ContextLinesSymmetric(t *testing.T) {
	ws := t.TempDir()
	mustWrite(t, filepath.Join(ws, "f.txt"), "one\ntwo\nthree MATCH\nfour\nfive\n")
	res, _ := volumeGrep(context.Background(),
		volumeGrepParams{Pattern: "MATCH", ContextLines: 1},
		newCtx(t, ws))
	if res.Status == sdk.StatusError {
		t.Fatal(res.Message)
	}
	want := "f.txt-2- two\nf.txt:3: three MATCH\nf.txt-4- four\n"
	if res.Message != want {
		t.Errorf("symmetric context mismatch:\n got: %q\nwant: %q", res.Message, want)
	}
}

func TestVolumeGrep_ContextLinesSeparatesGroups(t *testing.T) {
	ws := t.TempDir()
	mustWrite(t, filepath.Join(ws, "f.txt"), "MATCH a\nx\ny\nz\nMATCH b\n")
	res, _ := volumeGrep(context.Background(),
		volumeGrepParams{Pattern: "MATCH", ContextLines: 1},
		newCtx(t, ws))
	if res.Status == sdk.StatusError {
		t.Fatal(res.Message)
	}
	want := "f.txt:1: MATCH a\nf.txt-2- x\n--\nf.txt-4- z\nf.txt:5: MATCH b\n"
	if res.Message != want {
		t.Errorf("group-separated context mismatch:\n got: %q\nwant: %q", res.Message, want)
	}
}

func TestVolumeDelete_SymlinkRemovesLinkNotTarget(t *testing.T) {
	ws := t.TempDir()
	target := filepath.Join(ws, "target.txt")
	mustWrite(t, target, "keep me")
	link := filepath.Join(ws, "link.txt")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink create not supported: %v", err)
	}
	res, _ := volumeDelete(context.Background(), volumeDeleteParams{Path: "link.txt"}, newCtx(t, ws))
	if res.Status == sdk.StatusError {
		t.Fatalf("deleting a symlink should succeed: %s", res.Message)
	}
	if _, err := os.Lstat(link); !os.IsNotExist(err) {
		t.Errorf("symlink should be gone (lstat err=%v)", err)
	}
	if got := mustReadString(t, target); got != "keep me" {
		t.Errorf("symlink target must survive deletion, got %q", got)
	}
}

func TestVolumeMove_OverwriteReplaces(t *testing.T) {
	ws := t.TempDir()
	mustWrite(t, filepath.Join(ws, "a.txt"), "A")
	mustWrite(t, filepath.Join(ws, "b.txt"), "B")
	res, _ := volumeMove(context.Background(),
		volumeMoveParams{From: "a.txt", To: "b.txt", Overwrite: true},
		newCtx(t, ws))
	if res.Status == sdk.StatusError {
		t.Fatalf("overwrite move should succeed: %s", res.Message)
	}
	if _, err := os.Lstat(filepath.Join(ws, "a.txt")); !os.IsNotExist(err) {
		t.Errorf("source should be gone after move (lstat err=%v)", err)
	}
	if got := mustReadString(t, filepath.Join(ws, "b.txt")); got != "A" {
		t.Errorf("destination should hold the moved content, got %q", got)
	}
}

func TestVolumeMove_RefusesSymlinkDestination(t *testing.T) {
	ws := t.TempDir()
	mustWrite(t, filepath.Join(ws, "a.txt"), "A")
	mustWrite(t, filepath.Join(ws, "real.txt"), "real")
	if err := os.Symlink(filepath.Join(ws, "real.txt"), filepath.Join(ws, "b.txt")); err != nil {
		t.Skipf("symlink create not supported: %v", err)
	}
	res, _ := volumeMove(context.Background(),
		volumeMoveParams{From: "a.txt", To: "b.txt", Overwrite: true},
		newCtx(t, ws))
	if res.Status != sdk.StatusError || !strings.Contains(res.Message, "symlink") {
		t.Fatalf("move onto a symlink should be refused, got: %s", res.Message)
	}
	if got := mustReadString(t, filepath.Join(ws, "real.txt")); got != "real" {
		t.Errorf("symlink target must be untouched, got %q", got)
	}
	if got := mustReadString(t, filepath.Join(ws, "a.txt")); got != "A" {
		t.Errorf("source must be untouched after a refused move, got %q", got)
	}
}

func TestVolumeList_InvalidGlobRejected(t *testing.T) {
	ws := t.TempDir()
	mustWrite(t, filepath.Join(ws, "a.txt"), "")
	res, _ := volumeList(context.Background(), volumeListParams{Glob: "[abc"}, newCtx(t, ws))
	if res.Status != sdk.StatusError || !strings.Contains(res.Message, "invalid glob") {
		t.Errorf("malformed glob should be rejected up-front, got: %s", res.Message)
	}
}

func TestVolumeGrep_InvalidFileGlobRejected(t *testing.T) {
	ws := t.TempDir()
	mustWrite(t, filepath.Join(ws, "a.txt"), "x")
	res, _ := volumeGrep(context.Background(),
		volumeGrepParams{Pattern: "x", FileGlob: "[abc"},
		newCtx(t, ws))
	if res.Status != sdk.StatusError || !strings.Contains(res.Message, "invalid file_glob") {
		t.Errorf("malformed file_glob should be rejected up-front, got: %s", res.Message)
	}
}

func TestVolumeList_MaxEntriesCapMarker(t *testing.T) {
	ws := t.TempDir()
	for _, n := range []string{"a", "b", "c", "d", "e"} {
		mustWrite(t, filepath.Join(ws, n+".txt"), "")
	}
	res, _ := volumeList(context.Background(), volumeListParams{MaxEntries: 2}, newCtx(t, ws))
	if res.Status == sdk.StatusError {
		t.Fatal(res.Message)
	}
	if !strings.Contains(res.Message, "capped at 2") {
		t.Errorf("hitting max_entries should append a cap marker, got: %s", res.Message)
	}
}

func TestVolumeGrep_HeadLimitCapMarker(t *testing.T) {
	ws := t.TempDir()
	mustWrite(t, filepath.Join(ws, "f.txt"), "x\nx\nx\nx\nx\n")
	res, _ := volumeGrep(context.Background(),
		volumeGrepParams{Pattern: "x", HeadLimit: 2},
		newCtx(t, ws))
	if res.Status == sdk.StatusError {
		t.Fatal(res.Message)
	}
	if !strings.Contains(res.Message, "head_limit=2") {
		t.Errorf("hitting head_limit should append a cap marker, got: %s", res.Message)
	}
}

func TestVolumeGrep_TooLongLineReportsSkip(t *testing.T) {
	ws := t.TempDir()
	mustWrite(t, filepath.Join(ws, "huge.txt"), strings.Repeat("a", 2*1024*1024))
	res, _ := volumeGrep(context.Background(), volumeGrepParams{Pattern: "zzz"}, newCtx(t, ws))
	if res.Status == sdk.StatusError {
		t.Fatal(res.Message)
	}
	if !strings.Contains(res.Message, "skipped due to read errors") {
		t.Errorf("a too-long line should be reported as a skipped file, got: %s", res.Message)
	}
}

func TestVolumeRead_WholeFileTruncates(t *testing.T) {
	ws := t.TempDir()
	const overflow = 2048
	mustWrite(t, filepath.Join(ws, "big.txt"), strings.Repeat("a", maxOutputBytes+overflow))
	res, _ := volumeRead(context.Background(), volumeReadParams{Path: "big.txt"}, newCtx(t, ws))
	if res.Status == sdk.StatusError {
		t.Fatal(res.Message)
	}
	if !strings.Contains(res.Message, "output truncated") {
		t.Errorf("a >256 KiB whole-file read should be truncated with a marker, got %d bytes", len(res.Message))
	}
	if !strings.Contains(res.Message, "2048 more bytes") {
		t.Errorf("truncation marker should report the real overflow (2048 more bytes), got: %s", res.Message[maxOutputBytes:])
	}
}

func TestVolumeAppend_EmptyContentNoop(t *testing.T) {
	ws := t.TempDir()
	mustWrite(t, filepath.Join(ws, "f.txt"), "hello")
	res, _ := volumeAppend(context.Background(),
		volumeAppendParams{Path: "f.txt", Content: ""},
		newCtx(t, ws))
	if res.Status == sdk.StatusError {
		t.Fatalf("empty append should be a harmless no-op, got error: %s", res.Message)
	}
	if got := mustReadString(t, filepath.Join(ws, "f.txt")); got != "hello" {
		t.Errorf("empty append must not change the file, got %q", got)
	}
}

func TestVolumeMkdir_NonRecursiveMissingParentFails(t *testing.T) {
	ws := t.TempDir()
	res, _ := volumeMkdir(context.Background(), volumeMkdirParams{Path: "a/b"}, newCtx(t, ws))
	if res.Status != sdk.StatusError {
		t.Errorf("non-recursive mkdir with a missing parent should fail, got: %s", res.Message)
	}
	res2, _ := volumeMkdir(context.Background(), volumeMkdirParams{Path: "a/b", Recursive: true}, newCtx(t, ws))
	if res2.Status == sdk.StatusError {
		t.Errorf("recursive mkdir should create missing parents: %s", res2.Message)
	}
}

func TestVolumeStat_SymlinkAndDirKinds(t *testing.T) {
	ws := t.TempDir()
	if err := os.Mkdir(filepath.Join(ws, "d"), 0o755); err != nil {
		t.Fatal(err)
	}
	resDir, _ := volumeStat(context.Background(), volumeStatParams{Path: "d"}, newCtx(t, ws))
	if resDir.Status == sdk.StatusError || !strings.Contains(resDir.Message, "kind:    dir") {
		t.Errorf("stat of a directory should report kind dir, got: %s", resDir.Message)
	}
	mustWrite(t, filepath.Join(ws, "target.txt"), "x")
	if err := os.Symlink(filepath.Join(ws, "target.txt"), filepath.Join(ws, "lnk")); err != nil {
		t.Skipf("symlink create not supported: %v", err)
	}
	resLink, _ := volumeStat(context.Background(), volumeStatParams{Path: "lnk"}, newCtx(t, ws))
	if resLink.Status == sdk.StatusError || !strings.Contains(resLink.Message, "kind:    link") {
		t.Errorf("stat of a symlink should report kind link without following, got: %s", resLink.Message)
	}
}

func TestVolumeEdit_PreservesModeAndDeletes(t *testing.T) {
	ws := t.TempDir()
	p := filepath.Join(ws, "f.txt")
	mustWrite(t, p, "aaabbbccc")
	if err := os.Chmod(p, 0o600); err != nil {
		t.Fatal(err)
	}
	res, _ := volumeEdit(context.Background(),
		volumeEditParams{Path: "f.txt", OldString: "bbb", NewString: ""},
		newCtx(t, ws))
	if res.Status == sdk.StatusError {
		t.Fatalf("edit deletion should succeed: %s", res.Message)
	}
	if got := mustReadString(t, p); got != "aaaccc" {
		t.Errorf("new_string=\"\" should delete the matched region, got %q", got)
	}
	info, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("edit should preserve file mode, got %#o want 0600", info.Mode().Perm())
	}
}

func TestVolumeMove_RefusesRootAndIdentity(t *testing.T) {
	ws := t.TempDir()
	mustWrite(t, filepath.Join(ws, "a.txt"), "A")
	if res, _ := volumeMove(context.Background(), volumeMoveParams{From: ".", To: "x"}, newCtx(t, ws)); res.Status != sdk.StatusError {
		t.Errorf("move with workspace root as source should be refused, got: %s", res.Message)
	}
	if res, _ := volumeMove(context.Background(), volumeMoveParams{From: "a.txt", To: "."}, newCtx(t, ws)); res.Status != sdk.StatusError {
		t.Errorf("move with workspace root as destination should be refused, got: %s", res.Message)
	}
	if res, _ := volumeMove(context.Background(), volumeMoveParams{From: "a.txt", To: "a.txt"}, newCtx(t, ws)); res.Status != sdk.StatusError {
		t.Errorf("identity move (from==to) should be refused, got: %s", res.Message)
	}
}

func TestVolumeRead_AbsolutePathUnderRoot(t *testing.T) {
	ws := t.TempDir()
	abs := filepath.Join(ws, "abs.txt")
	mustWrite(t, abs, "absolute-ok\n")
	res, _ := volumeRead(context.Background(), volumeReadParams{Path: abs}, newCtx(t, ws))
	if res.Status == sdk.StatusError {
		t.Fatalf("absolute path under the volume root should be accepted: %s", res.Message)
	}
	if !strings.Contains(res.Message, "absolute-ok") {
		t.Errorf("expected file contents for absolute-under-root path, got: %s", res.Message)
	}
}

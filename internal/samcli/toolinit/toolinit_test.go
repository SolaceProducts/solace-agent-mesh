// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package toolinit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveToolsetDirRepoRoot(t *testing.T) {
	repo := t.TempDir()
	dest, err := ResolveToolsetDir("pptx", repo)
	if err != nil {
		t.Fatalf("ResolveToolsetDir: %v", err)
	}
	want := filepath.Join(repo, "toolsets", "pptx")
	if dest != want {
		t.Errorf("dest=%s, want=%s", dest, want)
	}
}

func TestResolveToolsetDirToolsetsDir(t *testing.T) {
	repo := t.TempDir()
	toolsets := filepath.Join(repo, "toolsets")
	if err := os.MkdirAll(toolsets, 0o755); err != nil {
		t.Fatal(err)
	}
	dest, err := ResolveToolsetDir("pptx", toolsets)
	if err != nil {
		t.Fatalf("ResolveToolsetDir: %v", err)
	}
	want := filepath.Join(toolsets, "pptx")
	if dest != want {
		t.Errorf("dest=%s, want=%s", dest, want)
	}
}

func TestResolveToolsetDirFullPath(t *testing.T) {
	repo := t.TempDir()
	full := filepath.Join(repo, "toolsets", "pptx")
	dest, err := ResolveToolsetDir("pptx", full)
	if err != nil {
		t.Fatalf("ResolveToolsetDir: %v", err)
	}
	if dest != full {
		t.Errorf("dest=%s, want=%s", dest, full)
	}
}

func TestInitGoScaffold(t *testing.T) {
	repo := t.TempDir()
	res, err := Init(Options{
		Name:       "mytool",
		Path:       repo,
		Lang:       LangGo,
		CLIVersion: "1.0.0-test",
	})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	wantToolsetDir := filepath.Join(repo, "toolsets", "mytool")
	if res.ToolsetDir != wantToolsetDir {
		t.Errorf("ToolsetDir=%s, want %s", res.ToolsetDir, wantToolsetDir)
	}
	wantSrcDir := filepath.Join(wantToolsetDir, "src")
	if res.SrcDir != wantSrcDir {
		t.Errorf("SrcDir=%s, want %s", res.SrcDir, wantSrcDir)
	}
	wantMetadata := filepath.Join(repo, "toolsets", "mytool.yaml")
	if res.MetadataPath != wantMetadata {
		t.Errorf("MetadataPath=%s, want %s", res.MetadataPath, wantMetadata)
	}
	if !res.SDKInjected {
		t.Errorf("SDKInjected=false, expected true on first scaffold")
	}

	metadata, err := os.ReadFile(wantMetadata)
	if err != nil {
		t.Fatalf("read metadata: %v", err)
	}
	if !strings.Contains(string(metadata), "kind: toolset") {
		t.Errorf("metadata missing kind:toolset header:\n%s", metadata)
	}
	if !strings.Contains(string(metadata), "name: mytool") {
		t.Errorf("metadata missing name:\n%s", metadata)
	}

	wantFiles := []string{
		"main.go",
		"go.mod",
		"build.sh",
		"manifest.yaml",
		"README.md",
		filepath.Join("_sdk", "samtoolsdk", "run.go"),
	}
	for _, rel := range wantFiles {
		if _, statErr := os.Stat(filepath.Join(wantSrcDir, rel)); statErr != nil {
			t.Errorf("expected %s under src/: %v", rel, statErr)
		}
	}
	st, err := os.Stat(filepath.Join(wantSrcDir, "build.sh"))
	if err == nil && st.Mode()&0o111 == 0 {
		t.Errorf("build.sh is not executable: %s", st.Mode())
	}
	for _, script := range []string{"build.sh", "build.bat"} {
		body, _ := os.ReadFile(filepath.Join(wantSrcDir, script))
		if !strings.Contains(string(body), "go build -trimpath -buildvcs=false") {
			t.Errorf("%s missing reproducible go build flags:\n%s", script, body)
		}
	}
	data, _ := os.ReadFile(filepath.Join(wantSrcDir, "go.mod"))
	if !strings.Contains(string(data), "./_sdk/samtoolsdk") {
		t.Errorf("go.mod missing vendor replace:\n%s", data)
	}
	readme, _ := os.ReadFile(filepath.Join(wantSrcDir, "README.md"))
	if !strings.Contains(string(readme), "sam ai-assistance skill install") {
		t.Errorf("README.md missing ai-assistance authoring pointer:\n%s", readme)
	}
}

func TestInitPythonScaffold(t *testing.T) {
	repo := t.TempDir()
	res, err := Init(Options{
		Name:       "py-tool",
		Path:       repo,
		Lang:       LangPython,
		CLIVersion: "1.0.0",
	})
	if err != nil {
		t.Fatalf("Init python: %v", err)
	}
	wantFiles := []string{
		"pyproject.toml",
		"build.sh",
		"build.bat",
		"manifest.yaml",
		"README.md",
		filepath.Join("src", "py_tool", "__init__.py"),
		filepath.Join("src", "py_tool", "__main__.py"),
	}
	for _, rel := range wantFiles {
		if _, statErr := os.Stat(filepath.Join(res.SrcDir, rel)); statErr != nil {
			t.Errorf("expected %s under src/: %v", rel, statErr)
		}
	}
	if _, statErr := os.Stat(res.MetadataPath); statErr != nil {
		t.Errorf("expected metadata YAML at %s: %v", res.MetadataPath, statErr)
	}
	if _, statErr := os.Stat(filepath.Join(res.SrcDir, "_sdk")); !os.IsNotExist(statErr) {
		t.Errorf("expected no _sdk/ tree for python scaffold, got err=%v", statErr)
	}
	pyproj, err := os.ReadFile(filepath.Join(res.SrcDir, "pyproject.toml"))
	if err != nil {
		t.Fatalf("read pyproject.toml: %v", err)
	}
	if !strings.Contains(string(pyproj), `"sam-tool-sdk>=0.1,<0.2"`) {
		t.Errorf("pyproject.toml missing narrow sam-tool-sdk pin:\n%s", pyproj)
	}
	if !strings.Contains(string(pyproj), `py-tool = "py_tool:cli"`) {
		t.Errorf("pyproject.toml missing [project.scripts] entry:\n%s", pyproj)
	}
	manifest, err := os.ReadFile(filepath.Join(res.SrcDir, "manifest.yaml"))
	if err != nil {
		t.Fatalf("read manifest.yaml: %v", err)
	}
	if !strings.Contains(string(manifest), "runtime: python") {
		t.Errorf("manifest.yaml missing runtime: python:\n%s", manifest)
	}
	if !strings.Contains(string(manifest), "executable: python/bin/py-tool") {
		t.Errorf("manifest.yaml missing python/bin/<name> executable:\n%s", manifest)
	}
	readme, _ := os.ReadFile(filepath.Join(res.SrcDir, "README.md"))
	if !strings.Contains(string(readme), "sam ai-assistance skill install") {
		t.Errorf("README.md missing ai-assistance authoring pointer:\n%s", readme)
	}
	buildSh, err := os.ReadFile(filepath.Join(res.SrcDir, "build.sh"))
	if err != nil {
		t.Fatalf("read build.sh: %v", err)
	}
	if !strings.Contains(string(buildSh), `${PLATFORM_ARGS[@]+"${PLATFORM_ARGS[@]}"}`) {
		t.Errorf("build.sh must guard the empty PLATFORM_ARGS expansion (${PLATFORM_ARGS[@]+...}); a bare \"${PLATFORM_ARGS[@]}\" trips 'set -u' on bash 3.2:\n%s", buildSh)
	}
}

func TestInitPreservesExistingMetadata(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, "toolsets"), 0o755); err != nil {
		t.Fatal(err)
	}
	customMetadata := "kind: toolset\nname: alpha\ndescription: \"hand-edited description\"\n"
	metadataPath := filepath.Join(repo, "toolsets", "alpha.yaml")
	if err := os.WriteFile(metadataPath, []byte(customMetadata), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Init(Options{Name: "alpha", Path: repo, Lang: LangGo}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	got, err := os.ReadFile(metadataPath)
	if err != nil {
		t.Fatalf("read metadata: %v", err)
	}
	if string(got) != customMetadata {
		t.Errorf("metadata clobbered by Init: got %q, want %q", got, customMetadata)
	}
}

func TestInitRefusesNonEmptyWithoutForce(t *testing.T) {
	repo := t.TempDir()
	dest := filepath.Join(repo, "toolsets", "x", "src")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dest, "marker"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Init(Options{Name: "x", Path: repo, Lang: LangGo})
	if err == nil {
		t.Fatal("expected error on non-empty target")
	}
	if !strings.Contains(err.Error(), "exists and is not empty") {
		t.Errorf("expected 'exists and is not empty' error, got: %v", err)
	}
}

func TestInitForceOverwrites(t *testing.T) {
	repo := t.TempDir()
	dest := filepath.Join(repo, "toolsets", "x", "src")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dest, "stale"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Init(Options{Name: "x", Path: repo, Lang: LangGo, Force: true}); err != nil {
		t.Fatalf("Init with force: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "stale")); !os.IsNotExist(err) {
		t.Error("stale file survived --force")
	}
	if _, err := os.Stat(filepath.Join(dest, "main.go")); err != nil {
		t.Error("expected main.go after force")
	}
}

func TestInitRejectsBadName(t *testing.T) {
	cases := []string{"", ".hidden", "../escape", "a/b"}
	for _, n := range cases {
		t.Run(n, func(t *testing.T) {
			if _, err := Init(Options{Name: n, Path: t.TempDir(), Lang: LangGo}); err == nil {
				t.Errorf("expected error for name %q", n)
			}
		})
	}
}

func TestSyncSingleTool(t *testing.T) {
	repo := t.TempDir()
	if _, err := Init(Options{Name: "alpha", Path: repo, Lang: LangGo}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	src := filepath.Join(repo, "toolsets", "alpha", "src")
	if err := os.RemoveAll(filepath.Join(src, "_sdk")); err != nil {
		t.Fatal(err)
	}
	res, err := Sync(SyncOptions{Path: repo, Name: "alpha"})
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if len(res.Synced) != 1 || res.Synced[0] != "alpha" {
		t.Errorf("Synced=%v, want [alpha]", res.Synced)
	}
	if _, err := os.Stat(filepath.Join(src, "_sdk", "samtoolsdk", "run.go")); err != nil {
		t.Errorf("vendor not restored after sync: %v", err)
	}
}

func TestSyncAllTools(t *testing.T) {
	repo := t.TempDir()
	for _, n := range []string{"alpha", "beta"} {
		if _, err := Init(Options{Name: n, Path: repo, Lang: LangGo}); err != nil {
			t.Fatalf("Init %s: %v", n, err)
		}
	}
	_ = os.RemoveAll(filepath.Join(repo, "toolsets", "alpha", "src", "_sdk"))
	res, err := Sync(SyncOptions{Path: repo})
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if len(res.Synced) != 2 {
		t.Errorf("synced=%d, want 2 (got %v)", len(res.Synced), res.Synced)
	}
}

func TestSyncSkipsMirrorOnlyToolsets(t *testing.T) {
	repo := t.TempDir()
	if _, err := Init(Options{Name: "built", Path: repo, Lang: LangGo}); err != nil {
		t.Fatalf("Init built: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(repo, "toolsets", "pulled"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "toolsets", "pulled", "pulled.zip"), []byte("PK\x03\x04zip-bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := Sync(SyncOptions{Path: repo})
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if len(res.Synced) != 1 || res.Synced[0] != "built" {
		t.Errorf("Synced=%v, want only [built] (mirror-only toolset should be skipped)", res.Synced)
	}
}

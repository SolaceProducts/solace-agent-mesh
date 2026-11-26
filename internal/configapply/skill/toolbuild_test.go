// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package skill

import (
	"archive/zip"
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/bundle"
)

func writeFile(t *testing.T, root, rel, body string) {
	t.Helper()
	full := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}

func TestParseToolManifest_Malformed(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "tools/manifest.yaml", "version: not-an-int\ntools: [not-a-map\n")
	_, err := ParseToolManifest(dir)
	if err == nil {
		t.Fatal("expected error for malformed manifest")
	}
	if !strings.Contains(err.Error(), "parse") {
		t.Errorf("error should mention parse: %v", err)
	}
}

func TestValidateRelPath_Empty(t *testing.T) {
	if err := validateRelPath("tool_dir", ""); err == nil {
		t.Fatal("expected error for empty value")
	}
}

func TestParseToolManifest_Missing(t *testing.T) {
	dir := t.TempDir()
	tm, err := ParseToolManifest(dir)
	if err != nil {
		t.Fatalf("ParseToolManifest: %v", err)
	}
	if tm != nil {
		t.Errorf("expected nil ToolManifest for skill without manifest, got %#v", tm)
	}
}

func TestParseToolManifest_Multitool(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "tools/manifest.yaml", `version: 1
tools:
  weekday_of:
    runtime: go
    executable: dates_kit
    tool_dir: dates_kit
  date_math:
    runtime: go
    executable: dates_kit
    tool_dir: dates_kit
`)
	tm, err := ParseToolManifest(dir)
	if err != nil {
		t.Fatalf("ParseToolManifest: %v", err)
	}
	if tm == nil {
		t.Fatal("expected non-nil ToolManifest")
	}
	if len(tm.Tools) != 2 {
		t.Fatalf("expected 2 tools, got %d", len(tm.Tools))
	}
	if tm.Tools["weekday_of"].Runtime != "go" {
		t.Errorf("weekday_of.runtime = %q", tm.Tools["weekday_of"].Runtime)
	}
}

func TestResolveGoToolDirs_Multitool(t *testing.T) {
	dir := t.TempDir()
	tm := &ToolManifest{Tools: map[string]*ToolEntry{
		"a": {Runtime: "go", Executable: "kit", ToolDir: "kit"},
		"b": {Runtime: "go", Executable: "kit", ToolDir: "kit"},
		"c": {Runtime: "go", Executable: "other", ToolDir: "other"},
	}}
	dirs, err := resolveGoToolDirs(dir, tm)
	if err != nil {
		t.Fatalf("resolveGoToolDirs: %v", err)
	}
	if len(dirs) != 2 {
		t.Fatalf("expected 2 unique dirs, got %d (%v)", len(dirs), dirs)
	}
	got := map[string]string{}
	for _, d := range dirs {
		got[d.toolDir] = d.overlayPath
	}
	if got["kit"] != "tools/kit/kit" {
		t.Errorf("kit overlayPath = %q", got["kit"])
	}
	if got["other"] != "tools/other/other" {
		t.Errorf("other overlayPath = %q", got["other"])
	}
}

func TestGoToolDirs_SkipsNonGoAndMissingGoMod(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "tools/manifest.yaml", `version: 1
tools:
  builds:
    runtime: go
    executable: builds
    tool_dir: builds
  prebuilt:
    runtime: go
    executable: prebuilt
    tool_dir: prebuilt
  pyone:
    runtime: python
    executable: pyone
    tool_dir: pyone
`)
	writeFile(t, dir, "tools/builds/go.mod", "module builds\n\ngo 1.26\n")
	writeFile(t, dir, "tools/builds/main.go", "package main\n\nfunc main() {}\n")
	writeFile(t, dir, "tools/prebuilt/README.md", "ships a pre-built binary\n")

	dirs, err := GoToolDirs(dir)
	if err != nil {
		t.Fatalf("GoToolDirs: %v", err)
	}
	if len(dirs) != 1 {
		t.Fatalf("expected 1 go tool dir, got %d (%v)", len(dirs), dirs)
	}
	if filepath.Base(dirs[0]) != "builds" {
		t.Errorf("expected the builds dir, got %q", dirs[0])
	}
}

func TestGoToolDirs_NoManifest(t *testing.T) {
	dirs, err := GoToolDirs(t.TempDir())
	if err != nil {
		t.Fatalf("GoToolDirs: %v", err)
	}
	if len(dirs) != 0 {
		t.Errorf("expected no go tool dirs for a skill without a manifest, got %v", dirs)
	}
}

func TestGoToolDirs_RejectsTraversal(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "tools/manifest.yaml", `version: 1
tools:
  evil:
    runtime: go
    executable: x
    tool_dir: ../../evil
`)
	if _, err := GoToolDirs(dir); err == nil {
		t.Fatal("expected error for a tool_dir that escapes the tools/ root")
	}
}

func TestResolveGoToolDirs_ConflictingExecutables(t *testing.T) {
	tm := &ToolManifest{Tools: map[string]*ToolEntry{
		"a": {Runtime: "go", Executable: "kit", ToolDir: "shared"},
		"b": {Runtime: "go", Executable: "different", ToolDir: "shared"},
	}}
	_, err := resolveGoToolDirs("/skill", tm)
	if err == nil {
		t.Fatal("expected error for conflicting executables sharing a tool_dir")
	}
	if !strings.Contains(err.Error(), "conflicting executables") {
		t.Errorf("error should mention conflicting executables: %v", err)
	}
}

func TestResolveGoToolDirs_RejectsTraversal(t *testing.T) {
	cases := []struct {
		name string
		tm   *ToolManifest
	}{
		{
			name: "parent_in_tool_dir",
			tm:   &ToolManifest{Tools: map[string]*ToolEntry{"a": {Runtime: "go", ToolDir: "../../evil", Executable: "x"}}},
		},
		{
			name: "absolute_tool_dir",
			tm:   &ToolManifest{Tools: map[string]*ToolEntry{"a": {Runtime: "go", ToolDir: "/etc/passwd", Executable: "x"}}},
		},
		{
			name: "parent_in_executable",
			tm:   &ToolManifest{Tools: map[string]*ToolEntry{"a": {Runtime: "go", ToolDir: "k", Executable: "../runme"}}},
		},
		{
			name: "absolute_executable",
			tm:   &ToolManifest{Tools: map[string]*ToolEntry{"a": {Runtime: "go", ToolDir: "k", Executable: "/usr/bin/sh"}}},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := resolveGoToolDirs("/skill", c.tm)
			if err == nil {
				t.Fatalf("expected validation error for %s", c.name)
			}
		})
	}
}

func TestResolveGoToolDirs_SkipsNonGoRuntime(t *testing.T) {
	tm := &ToolManifest{Tools: map[string]*ToolEntry{
		"a": {Runtime: "python", Executable: "thing", ToolDir: "thing"},
	}}
	dirs, err := resolveGoToolDirs("/skill", tm)
	if err != nil {
		t.Fatalf("resolveGoToolDirs: %v", err)
	}
	if len(dirs) != 0 {
		t.Errorf("expected python tools to be skipped, got %v", dirs)
	}
}

func TestBuildSkillTools_NoManifest(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "SKILL.md", "---\nname: empty\n---\n# empty\n")
	res, err := BuildSkillTools(context.Background(), "empty", dir, SkillBuildOptions{})
	if err != nil {
		t.Fatalf("BuildSkillTools: %v", err)
	}
	if res == nil {
		t.Fatal("expected non-nil result")
	}
	if len(res.Overlay) != 0 || len(res.Outcomes) != 0 {
		t.Errorf("expected empty overlay/outcomes, got overlay=%v outcomes=%v", res.Overlay, res.Outcomes)
	}
}

func TestBuildSkillTools_SkipsToolDirWithoutGoMod(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "SKILL.md", "---\nname: prebuilt\n---\n# prebuilt\n")
	writeFile(t, dir, "tools/manifest.yaml", `version: 1
tools:
  thing:
    runtime: go
    executable: thing
    tool_dir: thing
`)
	writeFile(t, dir, "tools/thing/thing", "binary-bytes")
	res, err := BuildSkillTools(context.Background(), "prebuilt", dir, SkillBuildOptions{})
	if err != nil {
		t.Fatalf("BuildSkillTools: %v", err)
	}
	if len(res.Outcomes) != 0 {
		t.Errorf("expected zero outcomes (no go.mod), got %v", res.Outcomes)
	}
}

func TestBuildSkillTools_GoBuild(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not available")
	}
	t.Setenv("SAM_TOOL_TARGET_OS", "linux")
	t.Setenv("SAM_TOOL_TARGET_ARCH", "arm64")

	dir := t.TempDir()
	writeFile(t, dir, "SKILL.md", "---\nname: tinybuild\n---\n# tinybuild\n")
	writeFile(t, dir, "tools/manifest.yaml", `version: 1
tools:
  hello:
    runtime: go
    executable: tinybin
    tool_dir: tinybin
`)
	writeFile(t, dir, "tools/tinybin/go.mod", "module tinybin\n\ngo 1.22\n")
	writeFile(t, dir, "tools/tinybin/main.go", "package main\nfunc main(){}\n")

	res, err := BuildSkillTools(context.Background(), "tinybuild", dir, SkillBuildOptions{})
	if err != nil {
		t.Fatalf("BuildSkillTools: %v", err)
	}
	if len(res.Outcomes) != 1 {
		t.Fatalf("expected 1 outcome, got %d", len(res.Outcomes))
	}
	if res.Outcomes[0].Action != ToolBuildActionBuilt {
		t.Errorf("first build should be Built, got %q", res.Outcomes[0].Action)
	}
	bin, ok := res.Overlay["tools/tinybin/tinybin"]
	if !ok {
		t.Fatalf("overlay missing tools/tinybin/tinybin; keys=%v", overlayKeys(res.Overlay))
	}
	if !bytes.HasPrefix(bin, []byte("\x7fELF")) {
		t.Errorf("overlay binary should be ELF (cross-compiled linux/arm64); first 4 bytes = %x", bin[:min(4, len(bin))])
	}

	res2, err := BuildSkillTools(context.Background(), "tinybuild", dir, SkillBuildOptions{})
	if err != nil {
		t.Fatalf("BuildSkillTools (cached): %v", err)
	}
	if len(res2.Outcomes) != 1 || res2.Outcomes[0].Action != ToolBuildActionCacheHit {
		t.Errorf("expected cache-hit, got %v", res2.Outcomes)
	}
	if !bytes.Equal(res.Overlay["tools/tinybin/tinybin"], res2.Overlay["tools/tinybin/tinybin"]) {
		t.Errorf("cache hit should return identical bytes")
	}
}

func TestBuildSkillTools_NoBuildStrictErrors(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "SKILL.md", "---\nname: strict\n---\n# strict\n")
	writeFile(t, dir, "tools/manifest.yaml", `version: 1
tools:
  hello:
    runtime: go
    executable: bin
    tool_dir: bin
`)
	writeFile(t, dir, "tools/bin/go.mod", "module bin\n\ngo 1.22\n")
	writeFile(t, dir, "tools/bin/main.go", "package main\nfunc main(){}\n")

	_, err := BuildSkillTools(context.Background(), "strict", dir, SkillBuildOptions{NoBuild: true, Strict: true})
	if err == nil {
		t.Fatal("expected error on cache miss + --no-build with Strict=true")
	}
	if !strings.Contains(err.Error(), "--no-build") {
		t.Errorf("error should mention --no-build: %v", err)
	}
}

func TestBuildSkillTools_NoBuildPlanSkips(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "SKILL.md", "---\nname: skipped\n---\n# skipped\n")
	writeFile(t, dir, "tools/manifest.yaml", `version: 1
tools:
  hello:
    runtime: go
    executable: bin
    tool_dir: bin
`)
	writeFile(t, dir, "tools/bin/go.mod", "module bin\n\ngo 1.22\n")
	writeFile(t, dir, "tools/bin/main.go", "package main\nfunc main(){}\n")

	res, err := BuildSkillTools(context.Background(), "skipped", dir, SkillBuildOptions{NoBuild: true, Strict: false})
	if err != nil {
		t.Fatalf("BuildSkillTools: %v", err)
	}
	if len(res.Outcomes) != 1 || res.Outcomes[0].Action != ToolBuildActionSkipped {
		t.Errorf("expected skipped outcome, got %v", res.Outcomes)
	}
	if len(res.Overlay) != 0 {
		t.Errorf("skipped build should not populate overlay, got %v", overlayKeys(res.Overlay))
	}
}

func TestBundleDirWithOverlay_AddsNewFiles(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "SKILL.md", "---\nname: bn\n---\n# bn\n")
	writeFile(t, dir, "tools/manifest.yaml", "version: 1\n")

	overlay := map[string][]byte{
		"tools/kit/kit": []byte("\x7fELF-stub"),
	}
	data, _, err := bundle.BundleDirWithOverlay(dir, overlay)
	if err != nil {
		t.Fatalf("BundleDirWithOverlay: %v", err)
	}

	r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("zip read: %v", err)
	}
	var names []string
	var binFile *zip.File
	for _, f := range r.File {
		names = append(names, f.Name)
		if f.Name == "tools/kit/kit" {
			binFile = f
		}
	}
	if binFile == nil {
		t.Fatalf("zip missing overlay-added entry tools/kit/kit; got %v", names)
	}
	if binFile.Mode()&0o111 == 0 {
		t.Errorf("overlay-added binary should be executable; mode=%o", binFile.Mode())
	}
	rc, err := binFile.Open()
	if err != nil {
		t.Fatalf("open zip entry: %v", err)
	}
	got, _ := readAll(rc)
	_ = rc.Close()
	if !bytes.Equal(got, overlay["tools/kit/kit"]) {
		t.Errorf("overlay bytes mismatch")
	}
}

func TestResolveGoToolDirs_InfersGoFromGoMod(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "tools/kit/go.mod", "module kit\n\ngo 1.22\n")
	tm := &ToolManifest{Tools: map[string]*ToolEntry{
		"do_thing": {Executable: "kit", ToolDir: "kit"},
	}}
	dirs, err := resolveGoToolDirs(dir, tm)
	if err != nil {
		t.Fatalf("resolveGoToolDirs: %v", err)
	}
	if len(dirs) != 1 || dirs[0].toolDir != "kit" {
		t.Fatalf("expected 'kit' inferred as a Go tool from its go.mod, got %v", dirs)
	}
	if dirs[0].overlayPath != "tools/kit/kit" {
		t.Errorf("overlayPath = %q; want tools/kit/kit", dirs[0].overlayPath)
	}
}

func TestResolveGoToolDirs_UnsetRuntimeNoGoModSkipped(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "tools/prebuilt/prebuilt", "binary-bytes")
	tm := &ToolManifest{Tools: map[string]*ToolEntry{
		"p": {Executable: "prebuilt", ToolDir: "prebuilt"},
	}}
	dirs, err := resolveGoToolDirs(dir, tm)
	if err != nil {
		t.Fatalf("resolveGoToolDirs: %v", err)
	}
	if len(dirs) != 0 {
		t.Errorf("unset runtime + no go.mod must be skipped, got %v", dirs)
	}
}

func TestResolveGoToolDirs_ExplicitNonGoWinsOverGoMod(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "tools/pylike/go.mod", "module pylike\n")
	tm := &ToolManifest{Tools: map[string]*ToolEntry{
		"x": {Runtime: "python", Executable: "pylike", ToolDir: "pylike"},
	}}
	dirs, err := resolveGoToolDirs(dir, tm)
	if err != nil {
		t.Fatalf("resolveGoToolDirs: %v", err)
	}
	if len(dirs) != 0 {
		t.Errorf("explicit non-Go runtime must not be built even with a go.mod, got %v", dirs)
	}
}

func TestResolveGoToolDirs_UnsetRuntimeRejectsTraversal(t *testing.T) {
	tm := &ToolManifest{Tools: map[string]*ToolEntry{
		"a": {ToolDir: "../../evil", Executable: "x"},
	}}
	if _, err := resolveGoToolDirs("/skill", tm); err == nil {
		t.Fatal("expected a traversal validation error for an inferred entry")
	}
}

func TestBuildSkillTools_InfersGoBuildFromGoMod(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not available")
	}
	t.Setenv("SAM_TOOL_TARGET_OS", "linux")
	t.Setenv("SAM_TOOL_TARGET_ARCH", "arm64")

	dir := t.TempDir()
	writeFile(t, dir, "SKILL.md", "---\nname: inferred\n---\n# inferred\n")
	writeFile(t, dir, "tools/manifest.yaml", `version: 1
tools:
  count_chars:
    executable: count_chars
    tool_dir: count_chars
`)
	writeFile(t, dir, "tools/count_chars/go.mod", "module count_chars\n\ngo 1.22\n")
	writeFile(t, dir, "tools/count_chars/main.go", "package main\nfunc main(){}\n")

	res, err := BuildSkillTools(context.Background(), "inferred", dir, SkillBuildOptions{})
	if err != nil {
		t.Fatalf("BuildSkillTools: %v", err)
	}
	if len(res.Outcomes) != 1 || res.Outcomes[0].Action != ToolBuildActionBuilt {
		t.Fatalf("expected the go.mod tool to be built via inference, got %v", res.Outcomes)
	}
	if _, ok := res.Overlay["tools/count_chars/count_chars"]; !ok {
		t.Errorf("overlay missing built binary; keys=%v", overlayKeys(res.Overlay))
	}
}

func TestResolvePythonToolDirs(t *testing.T) {
	cases := []struct {
		name    string
		entry   *ToolEntry
		files   map[string]string
		wantDir bool
	}{
		{
			name:    "explicit python with pyproject",
			entry:   &ToolEntry{Runtime: "python", ToolDir: "pytool", Executable: "python/bin/pytool"},
			files:   map[string]string{"pyproject.toml": "[project]\nname = \"pytool\"\n"},
			wantDir: true,
		},
		{
			name:    "explicit python without dep file still claimed (pre-built layer)",
			entry:   &ToolEntry{Runtime: "python", ToolDir: "prebuilt", Executable: "python/bin/x"},
			files:   map[string]string{"python/bin/x": "#!/bin/sh\n"},
			wantDir: true,
		},
		{
			name:    "infers from pyproject when runtime unset",
			entry:   &ToolEntry{ToolDir: "inferpy"},
			files:   map[string]string{"pyproject.toml": "[project]\nname = \"inferpy\"\n"},
			wantDir: true,
		},
		{
			name:    "infers from requirements.txt when runtime unset",
			entry:   &ToolEntry{ToolDir: "reqpy"},
			files:   map[string]string{"requirements.txt": "requests\n"},
			wantDir: true,
		},
		{
			name:    "unset runtime with go.mod is the Go path, not python",
			entry:   &ToolEntry{ToolDir: "gomod"},
			files:   map[string]string{"go.mod": "module gomod\n", "pyproject.toml": "[project]\n"},
			wantDir: false,
		},
		{
			name:    "runtime go excluded even with pyproject",
			entry:   &ToolEntry{Runtime: "go", ToolDir: "gotool"},
			files:   map[string]string{"pyproject.toml": "[project]\n"},
			wantDir: false,
		},
		{
			name:    "unset runtime with neither dep file skipped",
			entry:   &ToolEntry{ToolDir: "bare"},
			files:   map[string]string{"bare.txt": "x"},
			wantDir: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for rel, body := range tc.files {
				writeFile(t, dir, filepath.Join("tools", tc.entry.ToolDir, rel), body)
			}
			tm := &ToolManifest{Tools: map[string]*ToolEntry{"k": tc.entry}}
			dirs, err := resolvePythonToolDirs(dir, tm)
			if err != nil {
				t.Fatalf("resolvePythonToolDirs: %v", err)
			}
			if got := len(dirs) == 1; got != tc.wantDir {
				t.Errorf("claimed=%v, want %v (dirs=%v)", got, tc.wantDir, dirs)
			}
		})
	}
}

func TestResolvePythonToolDirs_RejectsTraversal(t *testing.T) {
	tm := &ToolManifest{Tools: map[string]*ToolEntry{
		"a": {Runtime: "python", ToolDir: "../../evil", Executable: "x"},
	}}
	if _, err := resolvePythonToolDirs("/skill", tm); err == nil {
		t.Fatal("expected a traversal validation error")
	}
}

func TestOverlayPythonLayer(t *testing.T) {
	dist := t.TempDir()
	writeFile(t, dist, "python/bin/greet", "#!/usr/bin/env python3\n")
	writeFile(t, dist, "python/greet/__init__.py", "x = 1\n")
	writeFile(t, dist, "python/sam_tool_sdk/__init__.py", "")
	writeFile(t, dist, "manifest.yaml", "version: 1\n")

	overlay := map[string][]byte{}
	if err := overlayPythonLayer(overlay, dist, "greeter"); err != nil {
		t.Fatalf("overlayPythonLayer: %v", err)
	}
	for _, k := range []string{
		"tools/greeter/python/bin/greet",
		"tools/greeter/python/greet/__init__.py",
		"tools/greeter/python/sam_tool_sdk/__init__.py",
	} {
		if _, ok := overlay[k]; !ok {
			t.Errorf("overlay missing %q; keys=%v", k, overlayKeys(overlay))
		}
	}
	if _, ok := overlay["tools/greeter/manifest.yaml"]; ok {
		t.Error("overlay must only include the python/ layer, not sibling files")
	}
}

func TestOverlayPythonLayer_MissingLayerErrors(t *testing.T) {
	if err := overlayPythonLayer(map[string][]byte{}, t.TempDir(), "x"); err == nil {
		t.Fatal("expected an error when dist has no python/ layer")
	}
}

type fakeSkillTargetResolver struct{ os, arch, py string }

func (f fakeSkillTargetResolver) Resolve(context.Context) (string, string, string, error) {
	return f.os, f.arch, f.py, nil
}

func TestResolveSkillTarget_ThreadsPythonVersion(t *testing.T) {
	target, pyVer, err := resolveSkillTarget(context.Background(), SkillBuildOptions{
		TargetResolver: fakeSkillTargetResolver{os: "linux", arch: "amd64", py: "3.11"},
	})
	if err != nil {
		t.Fatalf("resolveSkillTarget: %v", err)
	}
	if pyVer != "3.11" {
		t.Errorf("pythonVersion = %q; want 3.11", pyVer)
	}
	if target.OS != "linux" || target.Arch != "amd64" {
		t.Errorf("target = %+v; want linux/amd64", target)
	}
}

func overlayKeys(m map[string][]byte) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

func readAll(r interface {
	Read(p []byte) (int, error)
}) ([]byte, error) {
	var buf bytes.Buffer
	tmp := make([]byte, 4096)
	for {
		n, err := r.Read(tmp)
		if n > 0 {
			buf.Write(tmp[:n])
		}
		if err != nil {
			if err.Error() == "EOF" {
				return buf.Bytes(), nil
			}
			return buf.Bytes(), err
		}
	}
}

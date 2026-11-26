// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package samcli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func writeValidatableToolset(t *testing.T, name, schemaJSON string) (repoRoot string) {
	t.Helper()
	repoRoot = t.TempDir()
	src := filepath.Join(repoRoot, "toolsets", name, "src")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatalf("mkdir src: %v", err)
	}
	writeFakeTool(t, src, "faketool", "#!/bin/sh\necho '"+schemaJSON+"'\n")
	writeManifest(t, src, "version: 1\ntools:\n  "+name+"_tools:\n    runtime: go\n    executable: ./faketool\n")
	return repoRoot
}

func writeFakeTool(t *testing.T, dir, name, script string) {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake tool %s: %v", name, err)
	}
}

func writeManifest(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "manifest.yaml"), []byte(body), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
}

func TestValidateDiscovery_Success(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake tool is a POSIX shell script")
	}
	dir := t.TempDir()
	writeFakeTool(t, dir, "schematool",
		"#!/bin/sh\necho '{\"tools\":{\"greeter_greet\":{\"description\":\"Greets the named person\"}}}'\n")
	writeManifest(t, dir, "version: 1\ntools:\n  greeter_tools:\n    runtime: go\n    executable: ./schematool\n")

	var out bytes.Buffer
	if err := validateDiscovery(&out, "greeter", dir); err != nil {
		t.Fatalf("validateDiscovery: %v", err)
	}
	s := out.String()
	for _, want := range []string{"✓ greeter_tools", "greeter_greet", "Greets the named person", "greeter OK"} {
		if !strings.Contains(s, want) {
			t.Errorf("output missing %q; got %q", want, s)
		}
	}
}

func TestValidateDiscovery_FailureExitsNonZero(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake tool is a POSIX shell script")
	}
	dir := t.TempDir()
	writeFakeTool(t, dir, "broken", "#!/bin/sh\necho 'ModuleNotFoundError: no module named foo' >&2\nexit 1\n")
	writeManifest(t, dir, "version: 1\ntools:\n  broken_tools:\n    runtime: go\n    executable: ./broken\n")

	var out bytes.Buffer
	err := validateDiscovery(&out, "broken", dir)
	if err == nil {
		t.Fatal("expected an error for a tool that fails --schema")
	}
	if s := out.String(); !strings.Contains(s, "✗ broken_tools") {
		t.Errorf("output missing failure marker; got %q", s)
	}
	if !strings.Contains(err.Error(), "failed discovery") {
		t.Errorf("error = %v; want it to mention failed discovery", err)
	}
}

func TestValidateDiscovery_NoTools(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, "version: 1\ntools: []\n")

	err := validateDiscovery(&bytes.Buffer{}, "empty", dir)
	if err == nil || !strings.Contains(err.Error(), "nothing to validate") {
		t.Errorf("err = %v; want 'nothing to validate'", err)
	}
}

func TestReadManifestToolEntries(t *testing.T) {
	dir := t.TempDir()

	mapping := filepath.Join(dir, "map.yaml")
	if err := os.WriteFile(mapping, []byte("tools:\n  t:\n    runtime: python\n    executable: python/bin/t\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	entries, err := readManifestToolEntries(mapping)
	if err != nil {
		t.Fatalf("readManifestToolEntries(mapping): %v", err)
	}
	if got, ok := entries["t"]; !ok || got.Runtime != "python" || got.Executable != "python/bin/t" {
		t.Errorf("mapping entry = %+v (ok=%v); want {python, python/bin/t}", got, ok)
	}

	list := filepath.Join(dir, "list.yaml")
	if err = os.WriteFile(list, []byte("tools: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	entries, err = readManifestToolEntries(list)
	if err != nil {
		t.Fatalf("readManifestToolEntries(list): %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("list form entries = %v; want empty (no parse error)", entries)
	}
}

func TestRunToolsetValidate_KindNoneSuccess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake tool is a POSIX shell script")
	}
	repoRoot := writeValidatableToolset(t, "noop", `{"tools":{"noop_do":{"description":"does a thing"}}}`)

	cmd, out := newPackageCmdForTest()
	if err := runToolsetValidate(context.Background(), cmd, "noop", repoRoot); err != nil {
		t.Fatalf("runToolsetValidate: %v", err)
	}
	s := out.String()
	if !strings.Contains(s, "noop_do") || !strings.Contains(s, "noop OK") {
		t.Errorf("output missing discovery; got %q", s)
	}
}

func TestRunToolsetValidate_MissingSrcErrors(t *testing.T) {
	cmd, _ := newPackageCmdForTest()
	err := runToolsetValidate(context.Background(), cmd, "ghost", t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "toolset src dir not found") {
		t.Errorf("err = %v; want 'toolset src dir not found'", err)
	}
}

func TestToolsetValidateCmd_Execute(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake tool is a POSIX shell script")
	}
	repoRoot := writeValidatableToolset(t, "noop", `{"tools":{"noop_do":{"description":"does a thing"}}}`)

	cmd := newToolsetValidateCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"noop", repoRoot})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
}

func TestReadManifestToolEntries_Errors(t *testing.T) {
	if _, err := readManifestToolEntries(filepath.Join(t.TempDir(), "nope.yaml")); err == nil {
		t.Error("expected read error for a missing manifest")
	}

	bad := filepath.Join(t.TempDir(), "bad.yaml")
	if err := os.WriteFile(bad, []byte("tools: ["), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readManifestToolEntries(bad); err == nil {
		t.Error("expected parse error for malformed yaml")
	}

	scalar := filepath.Join(t.TempDir(), "scalar.yaml")
	if err := os.WriteFile(scalar, []byte("tools:\n  t: not-a-mapping\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readManifestToolEntries(scalar); err == nil {
		t.Error("expected decode error when a tool entry is not a mapping")
	}
}

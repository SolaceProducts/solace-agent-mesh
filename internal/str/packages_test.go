// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func captureLogger(buf *bytes.Buffer) *slog.Logger {
	return slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

func venvBinPath(t *testing.T, dir string) string {
	t.Helper()
	p := filepath.Join(dir, "python", "bin")
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func venvScriptsPath(t *testing.T, dir string) string {
	t.Helper()
	p := filepath.Join(dir, "python", "Scripts")
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func fakeWindowsGOOS(t *testing.T) {
	t.Helper()
	orig := runtimeGOOS
	runtimeGOOS = func() string { return "windows" }
	t.Cleanup(func() { runtimeGOOS = orig })
}

func writeSchemaStub(t *testing.T, path, schemaJSON string) {
	t.Helper()
	stub := "#!/bin/sh\n" +
		"if [ \"$1\" = \"--schema\" ]; then\n" +
		"  printf '%s\\n' '" + schemaJSON + "'\n" +
		"  exit 0\n" +
		"fi\n" +
		"exit 0\n"
	if err := os.WriteFile(path, []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}
}

func writeNoSchemaStub(t *testing.T, path string) {
	t.Helper()
	stub := "#!/bin/sh\nexit 1\n"
	if err := os.WriteFile(path, []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}
}

func writePackageManifestYAML(t *testing.T, dir, content string) string {
	t.Helper()
	path := filepath.Join(dir, "manifest.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestProcessPackage_SchemaOnly(t *testing.T) {
	dir := t.TempDir()

	exePath := filepath.Join(venvBinPath(t, dir), "my_tool")
	writeSchemaStub(t, exePath,
		`{"tools":{"my_tool":{"description":"from schema","parameters":{"type":"object","properties":{"q":{"type":"string"}}}}}}`)

	p := NewPackageProcessor(dir, captureLogger(new(bytes.Buffer)))
	tools, errs := p.processPackage(t.Context(), dir, "mypkg")
	if len(errs) > 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if len(tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(tools))
	}
	tool := tools[0]
	if tool.name != "my_tool" {
		t.Errorf("name = %q, want my_tool", tool.name)
	}
	if tool.description != "from schema" {
		t.Errorf("description = %q, want 'from schema'", tool.description)
	}
}

func TestProcessPackage_ManifestOnlyFallback(t *testing.T) {
	dir := t.TempDir()

	exePath := filepath.Join(venvBinPath(t, dir), "my_tool")
	writeNoSchemaStub(t, exePath)

	writePackageManifestYAML(t, dir, `version: 1
tools:
  my_tool:
    runtime: python
    description: "from manifest"
    parameters:
      - name: query
        type: string
        required: true
`)

	p := NewPackageProcessor(dir, captureLogger(new(bytes.Buffer)))
	tools, _ := p.processPackage(t.Context(), dir, "mypkg")
	if len(tools) != 1 {
		t.Fatalf("expected 1 tool from manifest fallback, got %d", len(tools))
	}
	tool := tools[0]
	if tool.description != "from manifest" {
		t.Errorf("description = %q, want 'from manifest'", tool.description)
	}
	if len(tool.parameters) != 1 || tool.parameters[0].Name != "query" {
		t.Errorf("parameters not loaded from manifest fallback: %+v", tool.parameters)
	}
}

func TestProcessFromManifest_SharedExecutable(t *testing.T) {
	dir := t.TempDir()

	exePath := filepath.Join(dir, "shared_bin")
	writeNoSchemaStub(t, exePath)

	manifest := &packageManifest{
		Version: 1,
		Tools: map[string]packageToolDef{
			"alpha": {Runtime: "go", Description: "alpha", Executable: "./shared_bin"},
			"beta":  {Runtime: "go", Description: "beta", Executable: "./shared_bin"},
		},
	}

	p := NewPackageProcessor(dir, captureLogger(new(bytes.Buffer)))
	tools, errs, _ := p.processFromManifest(dir, "mypkg", manifest)
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if len(tools) != 2 {
		t.Fatalf("expected 2 tools resolved against shared executable, got %d", len(tools))
	}
	for _, tool := range tools {
		if filepath.Base(tool.executable) != "shared_bin" {
			t.Errorf("tool %s resolved executable %q, want shared_bin", tool.name, tool.executable)
		}
	}
}

func TestProcessFromManifest_NoExecutableFieldFallsBackToToolName(t *testing.T) {
	dir := t.TempDir()
	writeNoSchemaStub(t, filepath.Join(dir, "my_tool"))

	manifest := &packageManifest{
		Version: 1,
		Tools: map[string]packageToolDef{
			"my_tool": {Runtime: "go", Description: "tool"},
		},
	}

	p := NewPackageProcessor(dir, captureLogger(new(bytes.Buffer)))
	tools, errs, _ := p.processFromManifest(dir, "mypkg", manifest)
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if len(tools) != 1 || filepath.Base(tools[0].executable) != "my_tool" {
		t.Fatalf("expected my_tool resolved by tool name, got %+v", tools)
	}
}

func TestProcessPackage_SchemaAndManifestAgree(t *testing.T) {
	dir := t.TempDir()

	exePath := filepath.Join(venvBinPath(t, dir), "my_tool")
	writeSchemaStub(t, exePath,
		`{"tools":{"my_tool":{"description":"agreed","parameters":{"type":"object","properties":{"q":{"type":"string"}}}}}}`)

	writePackageManifestYAML(t, dir, `version: 1
tools:
  my_tool:
    runtime: python
    description: "agreed"
    parameters:
      - name: q
        type: string
        required: true
`)

	var buf bytes.Buffer
	p := NewPackageProcessor(dir, captureLogger(&buf))
	tools, errs := p.processPackage(t.Context(), dir, "mypkg")
	if len(errs) > 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if len(tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(tools))
	}

	logOutput := buf.String()
	if strings.Contains(logOutput, "disagreement") {
		t.Errorf("unexpected disagreement warning when both sources agree: %s", logOutput)
	}

	tool := tools[0]
	if tool.description != "agreed" {
		t.Errorf("description = %q, want 'agreed'", tool.description)
	}
}

func TestProcessPackage_SchemaWinsOnParameterDisagreement(t *testing.T) {
	dir := t.TempDir()

	exePath := filepath.Join(venvBinPath(t, dir), "my_tool")
	writeSchemaStub(t, exePath,
		`{"tools":{"my_tool":{"description":"tool","parameters":{"type":"object","properties":{"schema_param":{"type":"string"}}}}}}`)

	writePackageManifestYAML(t, dir, `version: 1
tools:
  my_tool:
    runtime: python
    description: "tool"
    parameters:
      - name: manifest_only_param
        type: string
        required: true
`)

	var buf bytes.Buffer
	p := NewPackageProcessor(dir, captureLogger(&buf))
	tools, errs := p.processPackage(t.Context(), dir, "mypkg")
	if len(errs) > 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if len(tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(tools))
	}

	logOutput := buf.String()
	if !strings.Contains(logOutput, "disagreement") {
		t.Errorf("expected disagreement warning, got log: %s", logOutput)
	}
	if !strings.Contains(logOutput, "manifest_only_param") {
		t.Errorf("expected diverging param name in warning, got log: %s", logOutput)
	}

	tool := tools[0]
	if tool.parameters != nil {
		for _, p := range tool.parameters {
			if p.Name == "manifest_only_param" {
				t.Error("manifest_only_param must not survive: --schema wins")
			}
		}
	}
}

func TestProcessPackage_SchemaToolKeepsManifestProfileAndTimeout(t *testing.T) {
	toolsDir := t.TempDir()
	pkgDir := filepath.Join(toolsDir, "mypkg")
	exePath := filepath.Join(venvBinPath(t, pkgDir), "my_tool")
	writeSchemaStub(t, exePath,
		`{"tools":{"my_tool":{"description":"d","timeout_seconds":45,"parameters":{"type":"object","properties":{"q":{"type":"string"}}}}}}`)
	writePackageManifestYAML(t, pkgDir, `version: 1
tools:
  my_tool:
    runtime: python
    description: "d"
    timeout_seconds: 30
    sandbox_profile: restrictive
    parameters:
      - name: q
        type: string
`)

	p := NewPackageProcessor(toolsDir, captureLogger(new(bytes.Buffer)))
	tools, errs := p.processPackage(t.Context(), pkgDir, "mypkg")
	if len(errs) > 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if len(tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(tools))
	}
	if tools[0].timeoutSeconds != 30 {
		t.Errorf("timeoutSeconds = %d, want manifest value 30 over schema value 45", tools[0].timeoutSeconds)
	}
	if tools[0].sandboxProfile != "restrictive" {
		t.Errorf("sandboxProfile = %q, want restrictive", tools[0].sandboxProfile)
	}

	if err := p.writeMergedManifest(tools); err != nil {
		t.Fatalf("writeMergedManifest: %v", err)
	}
	m := NewManifestFromSources([]string{filepath.Join(toolsDir, "manifest.yaml")}, captureLogger(new(bytes.Buffer)))
	if err := m.Load(t.Context()); err != nil {
		t.Fatalf("Load: %v", err)
	}
	entry := m.GetTool("mypkg__my_tool")
	if entry == nil {
		t.Fatal("merged manifest lacks mypkg__my_tool")
	}
	if entry.SandboxProfile != "restrictive" {
		t.Errorf("loaded SandboxProfile = %q, want restrictive", entry.SandboxProfile)
	}
	if got := advertisedExecTimeout(SandboxConfig{}, entry); got != 30 {
		t.Errorf("advertisedExecTimeout = %d, want 30", got)
	}

	cmd, err := hardeningTestRunner(t).buildBwrapCommand(context.Background(), entry, []string{"/tmp/args.json"}, "/tmp/work", nil, "", slog.Default())
	if err != nil {
		t.Fatalf("buildBwrapCommand: %v", err)
	}
	expectFlag(t, cmd.Args, "--unshare-net")
}

func TestProcessPackage_SchemaTimeoutFillsWhenManifestSilent(t *testing.T) {
	dir := t.TempDir()
	exePath := filepath.Join(venvBinPath(t, dir), "my_tool")
	writeSchemaStub(t, exePath,
		`{"tools":{"my_tool":{"description":"d","timeout_seconds":45,"parameters":{"type":"object","properties":{}}}}}`)
	writePackageManifestYAML(t, dir, `version: 1
tools:
  my_tool:
    runtime: python
    description: "d"
    sandbox_profile: permissive
`)

	p := NewPackageProcessor(dir, captureLogger(new(bytes.Buffer)))
	tools, errs := p.processPackage(t.Context(), dir, "mypkg")
	if len(errs) > 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if len(tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(tools))
	}
	if tools[0].timeoutSeconds != 45 {
		t.Errorf("timeoutSeconds = %d, want schema value 45", tools[0].timeoutSeconds)
	}
	if tools[0].sandboxProfile != "permissive" {
		t.Errorf("sandboxProfile = %q, want permissive", tools[0].sandboxProfile)
	}
}

func TestProcessPackage_SchemaOnlyKeepsSchemaTimeout(t *testing.T) {
	dir := t.TempDir()
	exePath := filepath.Join(venvBinPath(t, dir), "my_tool")
	writeSchemaStub(t, exePath,
		`{"tools":{"my_tool":{"description":"d","timeout_seconds":45,"parameters":{"type":"object","properties":{}}}}}`)

	p := NewPackageProcessor(dir, captureLogger(new(bytes.Buffer)))
	tools, errs := p.processPackage(t.Context(), dir, "mypkg")
	if len(errs) > 0 || len(tools) != 1 {
		t.Fatalf("tools=%d errs=%v", len(tools), errs)
	}
	if tools[0].timeoutSeconds != 45 {
		t.Errorf("timeoutSeconds = %d, want 45", tools[0].timeoutSeconds)
	}
}

func TestProcessPackage_SchemaWinsOnDescriptionDisagreement(t *testing.T) {
	dir := t.TempDir()

	exePath := filepath.Join(venvBinPath(t, dir), "my_tool")
	writeSchemaStub(t, exePath,
		`{"tools":{"my_tool":{"description":"schema description","parameters":{"type":"object","properties":{}}}}}`)

	writePackageManifestYAML(t, dir, `version: 1
tools:
  my_tool:
    runtime: python
    description: "manifest description"
`)

	var buf bytes.Buffer
	p := NewPackageProcessor(dir, captureLogger(&buf))
	tools, errs := p.processPackage(t.Context(), dir, "mypkg")
	if len(errs) > 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if len(tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(tools))
	}

	logOutput := buf.String()
	if !strings.Contains(logOutput, "disagreement") {
		t.Errorf("expected disagreement warning for description drift, got log: %s", logOutput)
	}

	tool := tools[0]
	if tool.description != "schema description" {
		t.Errorf("description = %q; --schema should win, want 'schema description'", tool.description)
	}
}

type recordedFailure struct {
	pkg    string
	binary string
	errMsg string
}

func failureRecorder(out *[]recordedFailure) DiscoveryFailedFunc {
	return func(pkg, binary, errMsg string) {
		*out = append(*out, recordedFailure{pkg: pkg, binary: binary, errMsg: errMsg})
	}
}

func writeAlwaysFailingStub(t *testing.T, path, stderrMsg string) {
	t.Helper()
	stub := "#!/bin/sh\necho '" + stderrMsg + "' >&2\nexit 2\n"
	if err := os.WriteFile(path, []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestProcessAll_FailedDiscoveryNotRepublishedAcrossScans(t *testing.T) {
	toolsDir := t.TempDir()
	pkgDir := filepath.Join(toolsDir, "badpkg")
	if err := os.MkdirAll(filepath.Join(pkgDir, "python", "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeAlwaysFailingStub(t, filepath.Join(pkgDir, "python", "bin", "bad_tool"), "boom")
	if err := os.WriteFile(filepath.Join(pkgDir, "package.zip.extracted"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	var failures []recordedFailure
	p := NewPackageProcessor(toolsDir, captureLogger(new(bytes.Buffer)))
	p.SetDiscoveryFailedHook(failureRecorder(&failures))

	for i := range 3 {
		if _, _, err := p.ProcessAll(t.Context()); err != nil {
			t.Fatalf("scan %d: %v", i, err)
		}
	}
	if len(failures) != 1 {
		t.Fatalf("expected exactly 1 failed-discovery emission across 3 scans, got %d: %+v", len(failures), failures)
	}
	if failures[0].binary != "bad_tool" || failures[0].pkg != "badpkg" {
		t.Errorf("unexpected failure: %+v", failures[0])
	}
}

func TestProcessAll_FailedDiscoverySuppressedWhenSiblingSucceeds(t *testing.T) {
	toolsDir := t.TempDir()
	pkgDir := filepath.Join(toolsDir, "atlassian_rest_request")
	if err := os.MkdirAll(filepath.Join(pkgDir, "python", "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeAlwaysFailingStub(t,
		filepath.Join(pkgDir, "python", "bin", "atlassian_rest_request"),
		"missing PYTHONPATH dep")
	writeSchemaStub(t,
		filepath.Join(pkgDir, "python", "bin", "atlassian_rest_request_v2"),
		`{"tools":{"atlassian_rest_request":{"description":"works","parameters":{"type":"object","properties":{}}}}}`)
	if err := os.WriteFile(filepath.Join(pkgDir, "package.zip.extracted"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	var failures []recordedFailure
	p := NewPackageProcessor(toolsDir, captureLogger(new(bytes.Buffer)))
	p.SetDiscoveryFailedHook(failureRecorder(&failures))

	if _, _, err := p.ProcessAll(t.Context()); err != nil {
		t.Fatalf("ProcessAll: %v", err)
	}
	for _, f := range failures {
		if f.binary == "atlassian_rest_request" {
			t.Fatalf("failed-discovery must be suppressed for binary name that another candidate successfully discovered: %+v", f)
		}
	}
}

func TestProcessAll_FailedDiscoveryRepublishedOnErrorChange(t *testing.T) {
	toolsDir := t.TempDir()
	pkgDir := filepath.Join(toolsDir, "badpkg")
	if err := os.MkdirAll(filepath.Join(pkgDir, "python", "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	stubPath := filepath.Join(pkgDir, "python", "bin", "bad_tool")
	writeAlwaysFailingStub(t, stubPath, "first error")
	if err := os.WriteFile(filepath.Join(pkgDir, "package.zip.extracted"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	var failures []recordedFailure
	p := NewPackageProcessor(toolsDir, captureLogger(new(bytes.Buffer)))
	p.SetDiscoveryFailedHook(failureRecorder(&failures))

	if _, _, err := p.ProcessAll(t.Context()); err != nil {
		t.Fatalf("first scan: %v", err)
	}
	if len(failures) != 1 {
		t.Fatalf("scan 1: expected 1 failure, got %d", len(failures))
	}

	stub := "#!/bin/sh\necho 'second error' >&2\nexit 3\n"
	if err := os.WriteFile(stubPath, []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, _, err := p.ProcessAll(t.Context()); err != nil {
		t.Fatalf("second scan: %v", err)
	}
	if len(failures) != 2 {
		t.Fatalf("expected republish on error change: got %d failures: %+v", len(failures), failures)
	}
}

func TestProcessAll_PackageRemovalClearsFailureCache(t *testing.T) {
	toolsDir := t.TempDir()
	pkgDir := filepath.Join(toolsDir, "badpkg")
	if err := os.MkdirAll(filepath.Join(pkgDir, "python", "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	stubPath := filepath.Join(pkgDir, "python", "bin", "bad_tool")
	writeAlwaysFailingStub(t, stubPath, "boom")
	if err := os.WriteFile(filepath.Join(pkgDir, "package.zip.extracted"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	var failures []recordedFailure
	p := NewPackageProcessor(toolsDir, captureLogger(new(bytes.Buffer)))
	p.SetDiscoveryFailedHook(failureRecorder(&failures))

	if _, _, err := p.ProcessAll(t.Context()); err != nil {
		t.Fatalf("scan 1: %v", err)
	}
	if len(failures) != 1 {
		t.Fatalf("scan 1: expected 1 failure, got %d", len(failures))
	}

	if err := os.RemoveAll(pkgDir); err != nil {
		t.Fatal(err)
	}
	if _, _, err := p.ProcessAll(t.Context()); err != nil {
		t.Fatalf("scan 2 (post-remove): %v", err)
	}
	if err := os.MkdirAll(filepath.Join(pkgDir, "python", "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeAlwaysFailingStub(t, stubPath, "boom")
	if err := os.WriteFile(filepath.Join(pkgDir, "package.zip.extracted"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := p.ProcessAll(t.Context()); err != nil {
		t.Fatalf("scan 3 (post-recreate): %v", err)
	}
	if len(failures) != 2 {
		t.Fatalf("expected fresh failure after removal+recreate: got %d failures: %+v", len(failures), failures)
	}
}

func TestProcessPackage_PropagatesManifestCapabilities(t *testing.T) {
	dir := t.TempDir()

	exePath := filepath.Join(venvBinPath(t, dir), "enrich_authenticated_user")
	writeSchemaStub(t, exePath,
		`{"tools":{"enrich_authenticated_user":{"description":"resolve user","parameters":{"type":"object","properties":{"lookup_value":{"type":"string"}}}}}}`)

	writePackageManifestYAML(t, dir, `version: 1
capabilities:
  profileProvider: true
tools:
  enrich_authenticated_user:
    runtime: go
    executable: ./enrich_authenticated_user
`)

	p := NewPackageProcessor(dir, captureLogger(new(bytes.Buffer)))
	tools, errs := p.processPackage(t.Context(), dir, "bamboohr_profile_provider")
	if len(errs) > 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if len(tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(tools))
	}
	got := tools[0].capabilities
	if got == nil {
		t.Fatalf("capabilities not propagated onto processedTool")
	}
	if v, ok := got["profileProvider"]; !ok || v != true {
		t.Errorf("capabilities[profileProvider] = %v, want true; full map = %+v", v, got)
	}
}

func TestWriteMergedManifest_EmitsPerToolCapabilities(t *testing.T) {
	toolsDir := t.TempDir()
	p := NewPackageProcessor(toolsDir, captureLogger(new(bytes.Buffer)))

	tool := processedTool{
		name:         "enrich_authenticated_user",
		packageName:  "bamboohr_profile_provider",
		executable:   filepath.Join(toolsDir, "bamboohr_profile_provider", "enrich_authenticated_user"),
		toolDir:      filepath.Join(toolsDir, "bamboohr_profile_provider"),
		description:  "resolve user",
		capabilities: map[string]any{"profileProvider": true},
	}
	if err := p.writeMergedManifest([]processedTool{tool}); err != nil {
		t.Fatalf("writeMergedManifest: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(toolsDir, "manifest.yaml"))
	if err != nil {
		t.Fatalf("read aggregate manifest: %v", err)
	}
	if !strings.Contains(string(data), "profileProvider: true") {
		t.Fatalf("aggregate manifest missing profileProvider: true\n%s", string(data))
	}

	m := NewManifestFromSources([]string{filepath.Join(toolsDir, "manifest.yaml")}, captureLogger(new(bytes.Buffer)))
	if err := m.Load(t.Context()); err != nil {
		t.Fatalf("Load(): %v", err)
	}
	prefixed := "bamboohr_profile_provider" + PackageToolSep + "enrich_authenticated_user"
	entry := m.GetTool(prefixed)
	if entry == nil {
		t.Fatalf("entry %q not found after round-trip", prefixed)
	}
	if entry.Capabilities == nil {
		t.Fatalf("entry.Capabilities nil after round-trip; want profileProvider=true")
	}
	if v, ok := entry.Capabilities["profileProvider"]; !ok || v != true {
		t.Errorf("entry.Capabilities[profileProvider] = %v, want true; full = %+v", v, entry.Capabilities)
	}
}

func TestProcessPackage_CapabilitiesWithoutToolsBlock(t *testing.T) {
	dir := t.TempDir()

	exePath := filepath.Join(venvBinPath(t, dir), "enrich_authenticated_user")
	writeSchemaStub(t, exePath,
		`{"tools":{"enrich_authenticated_user":{"description":"resolve user","parameters":{"type":"object","properties":{"lookup_value":{"type":"string"}}}}}}`)

	writePackageManifestYAML(t, dir, `version: 1
capabilities:
  profileProvider: true
`)

	p := NewPackageProcessor(dir, captureLogger(new(bytes.Buffer)))
	tools, errs := p.processPackage(t.Context(), dir, "bamboohr_profile_provider")
	if len(errs) > 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if len(tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(tools))
	}
	if tools[0].capabilities == nil {
		t.Fatalf("capabilities not propagated when manifest has no tools block")
	}
	if v, ok := tools[0].capabilities["profileProvider"]; !ok || v != true {
		t.Errorf("capabilities[profileProvider] = %v, want true", v)
	}
}

func TestLoadManifest_PerToolCapabilitiesWinsOverSourceLevel(t *testing.T) {
	dir := t.TempDir()
	manifestPath := filepath.Join(dir, "manifest.yaml")
	if err := os.WriteFile(manifestPath, []byte(`version: 1
capabilities:
  sourceLevel: true
tools:
  pkg__my_tool:
    runtime: go
    executable: ./my_tool
    capabilities:
      profileProvider: true
`), 0o644); err != nil {
		t.Fatal(err)
	}

	m := NewManifestFromSources([]string{manifestPath}, captureLogger(new(bytes.Buffer)))
	if err := m.Load(t.Context()); err != nil {
		t.Fatalf("Load: %v", err)
	}
	entry := m.GetTool("pkg__my_tool")
	if entry == nil {
		t.Fatalf("entry not found")
	}
	if v, ok := entry.Capabilities["profileProvider"]; !ok || v != true {
		t.Errorf("per-tool capability lost: %+v", entry.Capabilities)
	}
	if _, ok := entry.Capabilities["sourceLevel"]; ok {
		t.Errorf("source-level capability bled in: %+v", entry.Capabilities)
	}
}

func TestLoadManifest_EmptyPerToolCapabilitiesDefersToSourceLevel(t *testing.T) {
	dir := t.TempDir()
	manifestPath := filepath.Join(dir, "manifest.yaml")
	if err := os.WriteFile(manifestPath, []byte(`version: 1
capabilities:
  profileProvider: true
tools:
  pkg__my_tool:
    runtime: go
    executable: ./my_tool
    capabilities: {}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	m := NewManifestFromSources([]string{manifestPath}, captureLogger(new(bytes.Buffer)))
	if err := m.Load(t.Context()); err != nil {
		t.Fatalf("Load: %v", err)
	}
	entry := m.GetTool("pkg__my_tool")
	if entry == nil {
		t.Fatalf("entry not found")
	}
	if v, ok := entry.Capabilities["profileProvider"]; !ok || v != true {
		t.Errorf("source-level fallback didn't fire for empty per-tool block: %+v", entry.Capabilities)
	}
}

func TestProcessFromManifest_MissingExecutablePublishesFailed(t *testing.T) {
	dir := t.TempDir()

	goodExe := filepath.Join(dir, "good_tool")
	writeNoSchemaStub(t, goodExe)

	manifest := &packageManifest{
		Version: 1,
		Tools: map[string]packageToolDef{
			"good_tool": {Runtime: "go", Description: "works", Executable: "./good_tool"},
			"bad_tool":  {Runtime: "go", Description: "missing", Executable: "./nonexistent_binary"},
		},
	}

	var failures []recordedFailure
	p := NewPackageProcessor(dir, captureLogger(new(bytes.Buffer)))
	p.SetDiscoveryFailedHook(failureRecorder(&failures))

	tools, errs, manifestFailures := p.processFromManifest(dir, "mypkg", manifest)

	var names []string
	for _, tool := range tools {
		names = append(names, tool.name)
	}
	foundGood := false
	for _, n := range names {
		if n == "good_tool" {
			foundGood = true
		}
	}
	if !foundGood {
		t.Errorf("good_tool not in resolved tools: %v", names)
	}

	for _, n := range names {
		if n == "bad_tool" {
			t.Errorf("bad_tool should not be in resolved tools (its binary is missing)")
		}
	}

	if len(errs) == 0 {
		t.Error("expected at least one error string for the missing executable")
	}

	if manifestFailures == nil {
		t.Fatal("expected non-nil failures map from processFromManifest")
	}
	msg, ok := manifestFailures["bad_tool"]
	if !ok {
		t.Fatalf("bad_tool not in failures map: %+v", manifestFailures)
	}
	if !strings.Contains(msg, "executable") {
		t.Errorf("failure message %q does not mention 'executable'", msg)
	}
	if !strings.Contains(msg, "bad_tool") {
		t.Errorf("failure message %q does not name the tool", msg)
	}

	p.emitDiscoveryFailures("mypkg", tools, manifestFailures)

	if len(failures) == 0 {
		t.Fatal("onDiscoveryFailed hook was never called for the missing executable")
	}
	var found bool
	for _, f := range failures {
		if f.pkg == "mypkg" && f.binary == "bad_tool" && strings.Contains(f.errMsg, "executable") {
			found = true
		}
	}
	if !found {
		t.Errorf("hook was not called with pkg=mypkg binary=bad_tool and actionable message; got: %+v", failures)
	}
}

func TestProcessPackage_PythonScriptsDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script stubs are not runnable on Windows; assertion runs on non-Windows via fakeWindowsGOOS")
	}
	fakeWindowsGOOS(t)

	dir := t.TempDir()
	exePath := filepath.Join(venvScriptsPath(t, dir), "my_tool.exe")
	writeSchemaStub(t, exePath,
		`{"tools":{"my_tool":{"description":"from scripts dir","parameters":{"type":"object","properties":{"q":{"type":"string"}}}}}}`)

	p := NewPackageProcessor(dir, captureLogger(new(bytes.Buffer)))
	tools, errs := p.processPackage(t.Context(), dir, "mypkg")
	if len(errs) > 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if len(tools) != 1 {
		t.Fatalf("expected 1 tool from python/Scripts/, got %d", len(tools))
	}
	if tools[0].name != "my_tool" {
		t.Errorf("name = %q, want my_tool", tools[0].name)
	}
	if tools[0].description != "from scripts dir" {
		t.Errorf("description = %q, want 'from scripts dir'", tools[0].description)
	}
}

func TestProcessAll_UnchangedHealthyPackageNotReprobed(t *testing.T) {
	toolsDir := t.TempDir()
	pkgDir := filepath.Join(toolsDir, "healthypkg")
	if err := os.MkdirAll(filepath.Join(pkgDir, "python", "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	counter := filepath.Join(t.TempDir(), "schema-calls")
	schemaJSON := `{"tools":{"my_tool":{"description":"ok","parameters":{"type":"object","properties":{}}}}}`
	stub := "#!/bin/sh\n" +
		"if [ \"$1\" = \"--schema\" ]; then\n" +
		"  echo call >> '" + counter + "'\n" +
		"  printf '%s\\n' '" + schemaJSON + "'\n" +
		"  exit 0\n" +
		"fi\nexit 0\n"
	if err := os.WriteFile(filepath.Join(pkgDir, "python", "bin", "my_tool"), []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "package.zip.extracted"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	p := NewPackageProcessor(toolsDir, captureLogger(new(bytes.Buffer)))

	changed, results, err := p.ProcessAll(t.Context())
	if err != nil {
		t.Fatalf("first scan: %v", err)
	}
	if !changed {
		t.Fatal("first scan: expected changed=true for a new package")
	}
	if got := schemaCallCount(t, counter); got != 1 {
		t.Fatalf("first scan: expected 1 --schema call, got %d", got)
	}
	if !resultsHaveTool(results, "healthypkg", "my_tool") {
		t.Fatalf("first scan: my_tool missing from results: %+v", results)
	}

	changed, results, err = p.ProcessAll(t.Context())
	if err != nil {
		t.Fatalf("second scan: %v", err)
	}
	if changed {
		t.Fatal("second scan: expected changed=false for an unchanged package")
	}
	if got := schemaCallCount(t, counter); got != 1 {
		t.Fatalf("second scan: expected no additional --schema call (still 1), got %d", got)
	}
	if !resultsHaveTool(results, "healthypkg", "my_tool") {
		t.Fatalf("second scan: my_tool missing from reused results: %+v", results)
	}
}

func TestProcessAll_CacheMissReprobesInsteadOfDroppingTool(t *testing.T) {
	toolsDir := t.TempDir()
	pkgDir := filepath.Join(toolsDir, "healthypkg")
	if err := os.MkdirAll(filepath.Join(pkgDir, "python", "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	counter := filepath.Join(t.TempDir(), "schema-calls")
	schemaJSON := `{"tools":{"my_tool":{"description":"ok","parameters":{"type":"object","properties":{}}}}}`
	stub := "#!/bin/sh\n" +
		"if [ \"$1\" = \"--schema\" ]; then\n" +
		"  echo call >> '" + counter + "'\n" +
		"  printf '%s\\n' '" + schemaJSON + "'\n" +
		"  exit 0\n" +
		"fi\nexit 0\n"
	if err := os.WriteFile(filepath.Join(pkgDir, "python", "bin", "my_tool"), []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "package.zip.extracted"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	p := NewPackageProcessor(toolsDir, captureLogger(new(bytes.Buffer)))
	if _, _, err := p.ProcessAll(t.Context()); err != nil {
		t.Fatalf("first scan: %v", err)
	}
	if got := schemaCallCount(t, counter); got != 1 {
		t.Fatalf("first scan: expected 1 --schema call, got %d", got)
	}

	delete(p.toolCache, "healthypkg")

	changed, results, err := p.ProcessAll(t.Context())
	if err != nil {
		t.Fatalf("post-miss scan: %v", err)
	}
	if got := schemaCallCount(t, counter); got != 2 {
		t.Fatalf("cache miss must reprobe: expected 2 --schema calls, got %d", got)
	}
	if !resultsHaveTool(results, "healthypkg", "my_tool") {
		t.Fatalf("cache miss dropped the tool from results: %+v", results)
	}
	if !changed {
		t.Fatal("cache-miss self-heal must set changed=true so the manifest is rebuilt")
	}
}

func TestProcessAll_ExtractFailurePopulatesFailureCache(t *testing.T) {
	toolsDir := t.TempDir()
	pkgDir := filepath.Join(toolsDir, "corruptpkg")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "package.zip"), []byte("not a valid zip"), 0o600); err != nil {
		t.Fatal(err)
	}

	var failures []recordedFailure
	p := NewPackageProcessor(toolsDir, captureLogger(new(bytes.Buffer)))
	p.SetDiscoveryFailedHook(failureRecorder(&failures))

	if _, _, err := p.ProcessAll(t.Context()); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(p.publishedFailures["corruptpkg"]) == 0 {
		t.Fatal("extraction failure must record a package-level failure so the carve-out re-probes")
	}
	if len(failures) != 1 {
		t.Fatalf("expected 1 discovery-failed emission for the extraction failure, got %d: %+v", len(failures), failures)
	}
}

func TestProcessAll_OutstandingFailureForcesReprobe(t *testing.T) {
	toolsDir := t.TempDir()
	pkgDir := filepath.Join(toolsDir, "healthypkg")
	if err := os.MkdirAll(filepath.Join(pkgDir, "python", "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	counter := filepath.Join(t.TempDir(), "schema-calls")
	schemaJSON := `{"tools":{"my_tool":{"description":"ok","parameters":{"type":"object","properties":{}}}}}`
	stub := "#!/bin/sh\n" +
		"if [ \"$1\" = \"--schema\" ]; then\n" +
		"  echo call >> '" + counter + "'\n" +
		"  printf '%s\\n' '" + schemaJSON + "'\n" +
		"  exit 0\n" +
		"fi\nexit 0\n"
	if err := os.WriteFile(filepath.Join(pkgDir, "python", "bin", "my_tool"), []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "package.zip.extracted"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	p := NewPackageProcessor(toolsDir, captureLogger(new(bytes.Buffer)))
	if _, _, err := p.ProcessAll(t.Context()); err != nil {
		t.Fatalf("first scan: %v", err)
	}
	if got := schemaCallCount(t, counter); got != 1 {
		t.Fatalf("first scan: expected 1 --schema call, got %d", got)
	}

	p.publishedFailures["healthypkg"] = map[string]string{"_package": "boom"}

	if _, _, err := p.ProcessAll(t.Context()); err != nil {
		t.Fatalf("second scan: %v", err)
	}
	if got := schemaCallCount(t, counter); got != 2 {
		t.Fatalf("outstanding failure must force a re-probe: expected 2 --schema calls, got %d", got)
	}
}

func schemaCallCount(t *testing.T, path string) int {
	t.Helper()
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		t.Fatalf("read counter: %v", err)
	}
	return strings.Count(string(data), "call\n")
}

func resultsHaveTool(results []PackageResult, pkg, tool string) bool {
	for _, r := range results {
		if r.Name != pkg {
			continue
		}
		for _, tl := range r.Tools {
			if tl.name == tool {
				return true
			}
		}
	}
	return false
}

func TestProcessAll_StopsOnCancelledContext(t *testing.T) {
	dir := t.TempDir()
	pkgDir := filepath.Join(dir, "mypkg")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writePackageManifestYAML(t, pkgDir, "tools:\n  - name: my_tool\n    executable: my_tool\n")
	p := NewPackageProcessor(dir, captureLogger(new(bytes.Buffer)))
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	changed, _, err := p.ProcessAll(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("ProcessAll error = %v, want context.Canceled", err)
	}
	if changed {
		t.Fatal("a cancelled scan must not report the manifest as changed")
	}
	if _, cached := p.cache["mypkg"]; cached {
		t.Fatal("a package skipped by the cancel must not be cached as processed")
	}
}

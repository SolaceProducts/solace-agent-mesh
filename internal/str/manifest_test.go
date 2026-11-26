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
	"strings"
	"testing"
	"time"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

const testManifestYAML = `version: 1
tools:
  echo_tool:
    executable: echo_tool
    description: "Echo back the input"
    timeout_seconds: 30
  chart_tools:
    executable: chart_tools
    timeout_seconds: 60
    sandbox_profile: standard
  mermaid_diagram_generator:
    executable: mermaid_diagram_generator
    tool_dir: mermaid
    timeout_seconds: 120
`

func writeManifest(t *testing.T, dir, content string) string {
	t.Helper()
	path := filepath.Join(dir, "manifest.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		const prefix = "executable:"
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		exe := strings.TrimSpace(strings.TrimPrefix(line, prefix))
		if exe == "" || filepath.IsAbs(exe) {
			continue
		}
		full := filepath.Join(dir, exe)
		if _, err := os.Stat(full); err == nil {
			continue
		}
		_ = os.MkdirAll(filepath.Dir(full), 0o755)
		toolBase := filepath.Base(exe)
		stub := "#!/bin/sh\n" +
			"if [ \"$1\" = \"--schema\" ]; then\n" +
			"  cat <<EOF\n" +
			"{\"tools\":{\"" + toolBase + "\":{\"description\":\"stub\",\"parameters\":{\"type\":\"object\",\"properties\":{}}}}}\n" +
			"EOF\n" +
			"  exit 0\n" +
			"fi\n" +
			"exit 0\n"
		_ = os.WriteFile(full, []byte(stub), 0o755)
	}
	return path
}

func TestManifestLoad(t *testing.T) {
	dir := t.TempDir()
	path := writeManifest(t, dir, testManifestYAML)

	m := NewManifest(path, testLogger())
	if err := m.Load(t.Context()); err != nil {
		t.Fatal(err)
	}

	names := m.ToolNames()
	if len(names) != 3 {
		t.Fatalf("expected 3 tools, got %d", len(names))
	}

	echo := m.GetTool("echo_tool")
	if echo == nil {
		t.Fatal("echo_tool not found")
	}
	if echo.TimeoutSeconds != 30 {
		t.Errorf("timeout = %d, want 30", echo.TimeoutSeconds)
	}
	if echo.Description != "stub" {
		t.Errorf("description = %q, want %q (--schema wins)", echo.Description, "stub")
	}

	chart := m.GetTool("chart_tools")
	if chart == nil {
		t.Fatal("chart_tools not found")
	}
	if chart.SandboxProfile != "standard" {
		t.Errorf("sandbox_profile = %q, want %q", chart.SandboxProfile, "standard")
	}

	mermaid := m.GetTool("mermaid_diagram_generator")
	if mermaid == nil {
		t.Fatal("mermaid_diagram_generator not found")
	}
	if mermaid.TimeoutSeconds != 120 {
		t.Errorf("timeout = %d, want 120", mermaid.TimeoutSeconds)
	}
	expectedToolDir := filepath.Join(dir, "mermaid")
	if mermaid.ToolDir != expectedToolDir {
		t.Errorf("tool_dir = %q, want %q", mermaid.ToolDir, expectedToolDir)
	}
}

func TestManifestEntryDefaultsToolDirToManifestDir(t *testing.T) {
	dir := t.TempDir()
	path := writeManifest(t, dir, testManifestYAML)

	m := NewManifest(path, testLogger())
	if err := m.Load(t.Context()); err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{"echo_tool", "chart_tools"} {
		entry := m.GetTool(name)
		if entry == nil {
			t.Fatalf("%s not found", name)
		}
		if entry.ToolDir != dir {
			t.Errorf("%s ToolDir = %q, want manifest dir %q", name, entry.ToolDir, dir)
		}
	}
}

func TestManifestRelativeManifestPath(t *testing.T) {
	absDir := t.TempDir()
	yaml := `version: 1
tools:
  rel_tool:
    executable: rel_tool
    timeout_seconds: 30
`
	_ = writeManifest(t, absDir, yaml)

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}

	relManifestPath, err := filepath.Rel(cwd, filepath.Join(absDir, "manifest.yaml"))
	if err != nil {
		t.Fatalf("filepath.Rel: %v", err)
	}
	if filepath.IsAbs(relManifestPath) {
		t.Skip("could not construct a relative manifest path on this platform")
	}

	m := NewManifest(relManifestPath, testLogger())
	if err := m.Load(t.Context()); err != nil {
		t.Fatal(err)
	}

	entry := m.GetTool("rel_tool")
	if entry == nil {
		t.Fatal("rel_tool not found")
	}
	if !filepath.IsAbs(entry.Executable) {
		t.Errorf("executable %q must be absolute (cmd.Dir=workDir would otherwise resolve it relative to a per-invocation work dir)", entry.Executable)
	}
	expected := filepath.Join(absDir, "rel_tool")
	if entry.Executable != expected {
		t.Errorf("executable = %q, want %q", entry.Executable, expected)
	}
}

func TestManifestDefaultExecutable(t *testing.T) {
	dir := t.TempDir()
	yaml := `version: 1
tools:
  my_tool:
    timeout_seconds: 30
`
	path := writeManifest(t, dir, yaml)
	m := NewManifest(path, testLogger())
	if err := m.Load(t.Context()); err != nil {
		t.Fatal(err)
	}

	entry := m.GetTool("my_tool")
	if entry == nil {
		t.Fatal("my_tool not found")
	}
	expectedExe := filepath.Join(dir, "my_tool")
	if entry.Executable != expectedExe {
		t.Errorf("executable = %q, want %q", entry.Executable, expectedExe)
	}
}

func TestManifestHasChanged(t *testing.T) {
	dir := t.TempDir()
	path := writeManifest(t, dir, testManifestYAML)

	m := NewManifest(path, testLogger())
	if err := m.Load(t.Context()); err != nil {
		t.Fatal(err)
	}

	if m.HasChanged() {
		t.Error("should not report changed immediately after load")
	}

	time.Sleep(50 * time.Millisecond)
	if err := os.WriteFile(path, []byte(testManifestYAML+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if !m.HasChanged() {
		t.Error("should report changed after file modification")
	}
}

func TestManifestReload(t *testing.T) {
	dir := t.TempDir()
	path := writeManifest(t, dir, testManifestYAML)

	m := NewManifest(path, testLogger())
	if err := m.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(m.ToolNames()) != 3 {
		t.Fatalf("expected 3 tools, got %d", len(m.ToolNames()))
	}

	time.Sleep(50 * time.Millisecond)
	newYAML := `version: 1
tools:
  only_tool:
    executable: only_tool
`
	_ = writeManifest(t, dir, newYAML)

	diff, err := m.Reload(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if diff == nil {
		t.Fatal("expected reload to occur")
	}
	if len(m.ToolNames()) != 1 {
		t.Errorf("expected 1 tool after reload, got %d", len(m.ToolNames()))
	}
	if m.GetTool("only_tool") == nil {
		t.Error("only_tool not found after reload")
	}
	if _, ok := diff.Added["only_tool"]; !ok {
		t.Error("expected only_tool in Added")
	}
	for _, name := range []string{"echo_tool", "chart_tools", "mermaid_diagram_generator"} {
		if _, ok := diff.Removed[name]; !ok {
			t.Errorf("expected %s in Removed", name)
		}
	}
}

func TestManifestFileRemoved(t *testing.T) {
	dir := t.TempDir()
	path := writeManifest(t, dir, testManifestYAML)

	m := NewManifest(path, testLogger())
	if err := m.Load(t.Context()); err != nil {
		t.Fatal(err)
	}

	_ = os.Remove(path)

	if !m.HasChanged() {
		t.Error("should report changed when file removed")
	}
}

func TestManifestGetToolNotFound(t *testing.T) {
	dir := t.TempDir()
	path := writeManifest(t, dir, testManifestYAML)

	m := NewManifest(path, testLogger())
	if err := m.Load(t.Context()); err != nil {
		t.Fatal(err)
	}

	if m.GetTool("nonexistent") != nil {
		t.Error("expected nil for nonexistent tool")
	}
}

func TestManifestReloadDiff_Added(t *testing.T) {
	dir := t.TempDir()
	path := writeManifest(t, dir, testManifestYAML)

	m := NewManifest(path, testLogger())
	if err := m.Load(t.Context()); err != nil {
		t.Fatal(err)
	}

	time.Sleep(50 * time.Millisecond)
	newYAML := testManifestYAML + `  new_tool:
    executable: new_tool
`
	if err := os.WriteFile(path, []byte(newYAML), 0o644); err != nil {
		t.Fatal(err)
	}

	diff, err := m.Reload(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if diff == nil {
		t.Fatal("expected diff")
	}
	if _, ok := diff.Added["new_tool"]; !ok {
		t.Error("expected new_tool in Added")
	}
	if len(diff.Removed) != 0 {
		t.Errorf("expected no removals, got %d", len(diff.Removed))
	}
	if len(diff.Modified) != 0 {
		t.Errorf("expected no modifications, got %d", len(diff.Modified))
	}
}

func TestManifestReloadDiff_Modified(t *testing.T) {
	dir := t.TempDir()

	echoExe := filepath.Join(dir, "echo_tool")
	writeSchemaStubForManifestTest(t, echoExe, "echo_tool", "first description")

	path := writeManifest(t, dir, testManifestYAML)

	m := NewManifest(path, testLogger())
	if err := m.Load(t.Context()); err != nil {
		t.Fatal(err)
	}

	time.Sleep(50 * time.Millisecond)
	writeSchemaStubForManifestTest(t, echoExe, "echo_tool", "changed description")

	if err := os.WriteFile(path, []byte(testManifestYAML+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	diff, err := m.Reload(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if diff == nil {
		t.Fatal("expected diff")
	}
	if _, ok := diff.Modified["echo_tool"]; !ok {
		t.Error("expected echo_tool in Modified after schema description changed")
	}
	if len(diff.Added) != 0 {
		t.Errorf("expected no additions, got %d", len(diff.Added))
	}
	if len(diff.Removed) != 0 {
		t.Errorf("expected no removals, got %d", len(diff.Removed))
	}
}

func writeSchemaStubForManifestTest(t *testing.T, path, toolName, description string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(schemaStubScript(description, toolName)), 0o755); err != nil {
		t.Fatal(err)
	}
}

func schemaStubScript(description string, toolNames ...string) string {
	return schemaStubScriptWithTail("", description, toolNames...)
}

func schemaStubScriptWithTail(tail, description string, toolNames ...string) string {
	entries := make([]string, 0, len(toolNames))
	for _, n := range toolNames {
		entries = append(entries,
			`"`+n+`":{"description":"`+description+`","parameters":{"type":"object","properties":{}}}`)
	}
	return "#!/bin/sh\n" +
		"if [ \"$1\" = \"--schema\" ]; then\n" +
		"  printf '{\"tools\":{" + strings.Join(entries, ",") + "}}\n'\n" +
		"  exit 0\n" +
		"fi\n" +
		tail +
		"exit 0\n"
}

func TestManifestReloadDiff_NoChange(t *testing.T) {
	dir := t.TempDir()
	path := writeManifest(t, dir, testManifestYAML)

	m := NewManifest(path, testLogger())
	if err := m.Load(t.Context()); err != nil {
		t.Fatal(err)
	}

	time.Sleep(50 * time.Millisecond)
	if err := os.WriteFile(path, []byte(testManifestYAML), 0o644); err != nil {
		t.Fatal(err)
	}

	diff, err := m.Reload(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if diff == nil {
		t.Fatal("expected reload due to mtime change")
	}
	if len(diff.Added) != 0 || len(diff.Removed) != 0 || len(diff.Modified) != 0 {
		t.Errorf("expected empty diff, got added=%d removed=%d modified=%d",
			len(diff.Added), len(diff.Removed), len(diff.Modified))
	}
}

func TestManifestReloadPreservesBuiltinAndDynamic(t *testing.T) {
	dir := t.TempDir()
	path := writeManifest(t, dir, testManifestYAML)

	m := NewManifest(path, testLogger())
	if err := m.Load(t.Context()); err != nil {
		t.Fatal(err)
	}

	m.RegisterBuiltin(&ManifestEntry{
		ToolName:    "load_skill",
		Description: "Load a skill",
	})

	m.Register("my_skill__my_tool", &ManifestEntry{
		ToolName:    "my_tool",
		Executable:  "/usr/bin/my_tool",
		Description: "A skill-bundled tool",
	})

	if m.GetTool("load_skill") == nil {
		t.Fatal("load_skill should be in manifest")
	}
	if m.GetTool("my_skill__my_tool") == nil {
		t.Fatal("my_skill__my_tool should be in manifest")
	}

	time.Sleep(50 * time.Millisecond)
	if err := os.WriteFile(path, []byte(testManifestYAML), 0o644); err != nil {
		t.Fatal(err)
	}

	diff, err := m.Reload(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if diff == nil {
		t.Fatal("expected reload due to mtime change")
	}

	entry := m.GetTool("load_skill")
	if entry == nil {
		t.Fatal("load_skill was lost during manifest reload")
	}
	if !entry.IsBuiltin {
		t.Error("load_skill should still be marked as built-in")
	}

	entry = m.GetTool("my_skill__my_tool")
	if entry == nil {
		t.Fatal("my_skill__my_tool was lost during manifest reload")
	}
	if entry.Executable != "/usr/bin/my_tool" {
		t.Errorf("dynamic tool executable = %q, want /usr/bin/my_tool", entry.Executable)
	}

	if _, removed := diff.Removed["load_skill"]; removed {
		t.Error("load_skill should not be in Removed set")
	}
	if _, removed := diff.Removed["my_skill__my_tool"]; removed {
		t.Error("my_skill__my_tool should not be in Removed set")
	}
}

func TestManifestYAMLSchemaFields(t *testing.T) {
	dir := t.TempDir()

	exePath := filepath.Join(dir, "my_tool")
	stub := "#!/bin/sh\n" +
		"if [ \"$1\" = \"--schema\" ]; then\n" +
		"  printf '{\"tools\":{\"my_tool\":{\"description\":\"schema description\",\"parameters\":{\"type\":\"object\",\"properties\":{\"schema_param\":{\"type\":\"string\"}}}}}}\n'\n" +
		"  exit 0\n" +
		"fi\n" +
		"exit 0\n"
	if err := os.WriteFile(exePath, []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}

	yamlContent := `version: 1
tools:
  my_tool:
    executable: my_tool
    description: "A test tool"
    parameters:
      type: object
      properties:
        code:
          type: string
          description: "The code to run"
        files:
          type: array
          items:
            type: string
      required:
        - code
    artifact_params:
      files:
        is_list: true
        is_optional: true
    instructions: |
      Run code in a sandbox.
      Use print() for output.
`
	path := filepath.Join(dir, "manifest.yaml")
	if err := os.WriteFile(path, []byte(yamlContent), 0o644); err != nil {
		t.Fatal(err)
	}
	m := NewManifest(path, testLogger())
	if err := m.Load(t.Context()); err != nil {
		t.Fatal(err)
	}

	entry := m.GetTool("my_tool")
	if entry == nil {
		t.Fatal("my_tool not found")
	}

	if entry.Parameters == nil {
		t.Fatal("expected Parameters to be non-nil")
	}
	props, ok := entry.Parameters["properties"].(map[string]any)
	if !ok {
		t.Fatal("expected parameters.properties to be a map")
	}
	if _, hasSchemaParam := props["schema_param"]; !hasSchemaParam {
		t.Error("expected schema_param from --schema output, not manifest params")
	}
	if _, hasCode := props["code"]; hasCode {
		t.Error("manifest-only param 'code' should be absent: --schema wins")
	}

	if entry.Description != "schema description" {
		t.Errorf("expected description from --schema 'schema description', got %q", entry.Description)
	}

}

func TestResolveExecutable(t *testing.T) {
	dir := t.TempDir()

	exePath := filepath.Join(dir, "my_tool")
	if err := os.WriteFile(exePath, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	resolved, err := resolveExecutable(dir, "my_tool")
	if err != nil {
		t.Fatal(err)
	}
	if resolved != exePath {
		t.Errorf("resolved = %q, want %q", resolved, exePath)
	}

	layerBin := filepath.Join(dir, "python", "bin")
	if mkdirErr := os.MkdirAll(layerBin, 0o755); mkdirErr != nil {
		t.Fatal(mkdirErr)
	}
	layerExe := filepath.Join(layerBin, "venv_tool")
	if writeErr := os.WriteFile(layerExe, []byte("#!/bin/sh\n"), 0o755); writeErr != nil {
		t.Fatal(writeErr)
	}

	resolved, err = resolveExecutable(dir, "venv_tool")
	if err != nil {
		t.Fatal(err)
	}
	if resolved != layerExe {
		t.Errorf("resolved = %q, want %q", resolved, layerExe)
	}

	_, err = resolveExecutable(dir, "nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent executable")
	}
}

const packageManifestWithSchemaYAML = `version: 1
tools:
  search_tool:
    runtime: python
    description: "Search the web"
    timeout_seconds: 30
    sandbox_profile: standard
    parameters:
      - name: query
        type: string
        required: true
        description: "Search query"
      - name: max_results
        type: integer
        required: false
        description: "Maximum results to return"
    config_schema:
      - name: api_key
        type: string
        secret: true
        description: "API key for external service"
      - name: endpoint
        type: string
        secret: false
        description: "API endpoint URL"
`

func TestReadPackageManifest_WithSchemaFields(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "manifest.yaml")
	if err := os.WriteFile(path, []byte(packageManifestWithSchemaYAML), 0o644); err != nil {
		t.Fatal(err)
	}

	m, err := readPackageManifest(path)
	if err != nil {
		t.Fatalf("readPackageManifest: %v", err)
	}

	def, ok := m.Tools["search_tool"]
	if !ok {
		t.Fatal("expected 'search_tool' in manifest tools")
	}

	if len(def.Parameters) != 2 {
		t.Fatalf("Parameters len = %d, want 2", len(def.Parameters))
	}

	p0 := def.Parameters[0]
	if p0.Name != "query" {
		t.Errorf("Parameters[0].Name = %q, want %q", p0.Name, "query")
	}
	if p0.Type != "string" {
		t.Errorf("Parameters[0].Type = %q, want %q", p0.Type, "string")
	}
	if !p0.Required {
		t.Error("Parameters[0].Required should be true")
	}

	p1 := def.Parameters[1]
	if p1.Name != "max_results" {
		t.Errorf("Parameters[1].Name = %q, want %q", p1.Name, "max_results")
	}
	if p1.Required {
		t.Error("Parameters[1].Required should be false")
	}

	if len(def.ConfigSchema) != 2 {
		t.Fatalf("ConfigSchema len = %d, want 2", len(def.ConfigSchema))
	}

	c0 := def.ConfigSchema[0]
	if c0.Name != "api_key" {
		t.Errorf("ConfigSchema[0].Name = %q, want %q", c0.Name, "api_key")
	}
	if !c0.Secret {
		t.Error("ConfigSchema[0].Secret should be true")
	}

	c1 := def.ConfigSchema[1]
	if c1.Name != "endpoint" {
		t.Errorf("ConfigSchema[1].Name = %q, want %q", c1.Name, "endpoint")
	}
	if c1.Secret {
		t.Error("ConfigSchema[1].Secret should be false")
	}
}

func TestProcessFromManifest_SchemaFieldsPropagated(t *testing.T) {
	dir := t.TempDir()
	manifestPath := filepath.Join(dir, "manifest.yaml")
	if err := os.WriteFile(manifestPath, []byte(packageManifestWithSchemaYAML), 0o644); err != nil {
		t.Fatal(err)
	}

	exe := filepath.Join(dir, "search_tool")
	if err := os.WriteFile(exe, []byte("#!/bin/sh"), 0o755); err != nil {
		t.Fatal(err)
	}

	p := NewPackageProcessor(dir, testLogger())
	manifest, err := readPackageManifest(manifestPath)
	if err != nil {
		t.Fatal(err)
	}

	tools, errs, _ := p.processFromManifest(dir, "mypkg", manifest)
	if len(errs) > 0 {
		t.Fatalf("processFromManifest errors: %v", errs)
	}
	if len(tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(tools))
	}

	tool := tools[0]
	if len(tool.parameters) != 2 {
		t.Errorf("parameters len = %d, want 2", len(tool.parameters))
	}
	if len(tool.configSchema) != 2 {
		t.Errorf("configSchema len = %d, want 2", len(tool.configSchema))
	}
	if tool.parameters[0].Name != "query" {
		t.Errorf("parameters[0].Name = %q, want query", tool.parameters[0].Name)
	}
	if !tool.configSchema[0].Secret {
		t.Error("configSchema[0].Secret should be true")
	}
}

func TestWarnManifestSchemaDrift_ParameterTypeMismatch(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))

	entry := &ManifestEntry{
		ToolName: "my_tool",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"code": map[string]any{
					"type":        "integer",
					"description": "exit code",
				},
			},
		},
	}
	info := &DiscoveredTool{
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"code": map[string]any{
					"type":        "string",
					"description": "exit code",
				},
			},
		},
	}

	warnManifestSchemaDrift(logger, entry, info)

	got := buf.String()
	if !strings.Contains(got, "parameter type differs") {
		t.Errorf("expected 'parameter type differs' warning, got: %s", got)
	}
	if !strings.Contains(got, "my_tool") {
		t.Errorf("expected tool name in warning, got: %s", got)
	}
	if !strings.Contains(got, "integer") {
		t.Errorf("expected manifest_type 'integer' in warning, got: %s", got)
	}
	if !strings.Contains(got, "string") {
		t.Errorf("expected schema_type 'string' in warning, got: %s", got)
	}
}

func TestWarnManifestSchemaDrift_ParameterTypeMatch(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))

	entry := &ManifestEntry{
		ToolName: "my_tool",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"code": map[string]any{"type": "string"},
			},
		},
	}
	info := &DiscoveredTool{
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"code": map[string]any{"type": "string"},
			},
		},
	}

	warnManifestSchemaDrift(logger, entry, info)

	if buf.Len() != 0 {
		t.Errorf("expected no warnings for matching types, got: %s", buf.String())
	}
}

func TestWarnManifestSchemaDrift_ConfigSchemaTypeMismatch(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))

	entry := &ManifestEntry{
		ToolName: "my_tool",
		ConfigSchema: []map[string]any{
			{"name": "api_key", "type": "integer"},
		},
	}
	info := &DiscoveredTool{
		ConfigSchema: []map[string]any{
			{"name": "api_key", "type": "string"},
		},
	}

	warnManifestSchemaDrift(logger, entry, info)

	got := buf.String()
	if !strings.Contains(got, "config_schema key type differs") {
		t.Errorf("expected 'config_schema key type differs' warning, got: %s", got)
	}
	if !strings.Contains(got, "api_key") {
		t.Errorf("expected key name 'api_key' in warning, got: %s", got)
	}
	if !strings.Contains(got, "integer") {
		t.Errorf("expected manifest_type 'integer' in warning, got: %s", got)
	}
	if !strings.Contains(got, "string") {
		t.Errorf("expected schema_type 'string' in warning, got: %s", got)
	}
}

func TestManifestLoad_TopLevelCapabilities(t *testing.T) {
	dir := t.TempDir()
	yaml := `version: 1
capabilities:
  profileProvider: true
  ignoredExtra: ["a", "b"]
tools:
  echo_tool:
    executable: echo_tool
    description: "Echo"
`
	path := writeManifest(t, dir, yaml)

	m := NewManifest(path, testLogger())
	if err := m.Load(t.Context()); err != nil {
		t.Fatal(err)
	}

	entry := m.GetTool("echo_tool")
	if entry == nil {
		t.Fatal("echo_tool not loaded")
	}
	if entry.Capabilities == nil {
		t.Fatal("expected capabilities to be populated")
	}
	if entry.Capabilities["profileProvider"] != true {
		t.Errorf("profileProvider = %v, want true", entry.Capabilities["profileProvider"])
	}
	if _, ok := entry.Capabilities["ignoredExtra"]; !ok {
		t.Error("expected ignoredExtra to be preserved verbatim")
	}
}

func TestManifestLoad_MalformedCapabilitiesFallsBackToEmpty(t *testing.T) {
	dir := t.TempDir()
	yaml := `version: 1
capabilities: not-a-map
tools:
  echo_tool:
    executable: echo_tool
    description: "Echo"
`
	path := writeManifest(t, dir, yaml)

	buf := &bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
	m := NewManifest(path, logger)
	if err := m.Load(t.Context()); err != nil {
		t.Fatalf("Load should tolerate malformed capabilities, got error: %v", err)
	}

	entry := m.GetTool("echo_tool")
	if entry == nil {
		t.Fatal("echo_tool not loaded")
	}
	if entry.Capabilities != nil {
		t.Errorf("expected nil capabilities on malformed input, got %v", entry.Capabilities)
	}
	if !strings.Contains(buf.String(), "capabilities") {
		t.Errorf("expected warning mentioning capabilities, got: %s", buf.String())
	}
}

func TestManifestLoad_NoCapabilitiesLeavesNil(t *testing.T) {
	dir := t.TempDir()
	path := writeManifest(t, dir, testManifestYAML)

	m := NewManifest(path, testLogger())
	if err := m.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	entry := m.GetTool("echo_tool")
	if entry == nil {
		t.Fatal("echo_tool not loaded")
	}
	if entry.Capabilities != nil {
		t.Errorf("expected nil capabilities when manifest omits the key, got %v", entry.Capabilities)
	}
}

func TestManifestLoad_StopsOnCancelledContext(t *testing.T) {
	dir := t.TempDir()
	path := writeManifest(t, dir, testManifestYAML)
	m := NewManifest(path, testLogger())
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if err := m.Load(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Load error = %v, want context.Canceled", err)
	}
}

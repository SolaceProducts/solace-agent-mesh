// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func writePipConsoleScriptTool(t *testing.T, dir, shebang string) string {
	t.Helper()

	binDir := filepath.Join(dir, "python", "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}

	module := `import json, sys

def main():
    cfg = {}
    if "--config" in sys.argv:
        with open(sys.argv[sys.argv.index("--config") + 1]) as fh:
            cfg = json.load(fh)
    params = {"type": "object", "properties": {"query": {"type": "string"}}}
    for name in cfg.get("dynamic_params", []):
        params["properties"][name] = {"type": "string"}
    json.dump({"tools": {"schema_probe": {"description": "probe", "parameters": params}}}, sys.stdout)
`
	if err := os.WriteFile(filepath.Join(dir, "python", "schema_probe_mod.py"), []byte(module), 0o644); err != nil {
		t.Fatal(err)
	}

	script := "#!" + shebang + "\nimport sys\nfrom schema_probe_mod import main\nsys.exit(main())\n"
	scriptPath := filepath.Join(binDir, "schema_probe")
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return scriptPath
}

func probeConfigAwareSchema(t *testing.T, shebang string) map[string]any {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("pip --target console-script shebang layout is POSIX-specific")
	}
	dir := t.TempDir()
	script := writePipConsoleScriptTool(t, dir, shebang)

	configPath := filepath.Join(dir, "tool_config.json")
	if err := os.WriteFile(configPath, []byte(`{"dynamic_params": ["cla"]}`), 0o600); err != nil {
		t.Fatal(err)
	}

	entry := &ManifestEntry{Executable: script, ToolDir: dir}
	discovered, err := probeSchema(nil, entry.Executable, entry.ToolDir, PythonPathFor(entry.ToolDir), nil, "--schema", "--config", configPath)
	if err != nil {
		t.Fatalf("config-aware discovery failed: %v", err)
	}
	tool, ok := discovered["schema_probe"]
	if !ok {
		t.Fatalf("tool %q not in discovery output; got %d tools: %v", "schema_probe", len(discovered), discovered)
	}
	props, ok := tool.Parameters["properties"].(map[string]any)
	if !ok {
		t.Fatalf("schema for %q has no properties object; got parameters %v", "schema_probe", tool.Parameters)
	}
	return props
}

func TestDiscoverToolsFromBinaryWithConfig_PipConsoleScript(t *testing.T) {
	props := probeConfigAwareSchema(t, findPython(t))
	if _, ok := props["cla"]; !ok {
		t.Errorf("config-derived parameter %q missing from schema; got %v", "cla", props)
	}
}

func TestDiscoverToolsFromBinaryWithConfig_ReroutesMissingShebangInterpreter(t *testing.T) {
	findPython(t)
	props := probeConfigAwareSchema(t, "/nonexistent/build-host/venv/bin/python3.11")
	if _, ok := props["cla"]; !ok {
		t.Errorf("config-derived parameter %q missing from schema; got %v", "cla", props)
	}
}

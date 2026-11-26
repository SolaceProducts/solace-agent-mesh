// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

const flatPyToolSource = `#!/usr/bin/env python3
import sys, json

SCHEMA = {"tools": {"format_message": {"description": "d", "parameters": {"type": "object", "properties": {"message": {"type": "string"}}}}}}

if "--schema" in sys.argv:
    print(json.dumps(SCHEMA))
    sys.exit(0)

with open(sys.argv[1]) as f:
    runner_args = json.load(f)
prefix = runner_args.get("tool_config", {}).get("prefix", "[INFO]")
message = runner_args["args"]["message"]
with open(runner_args["result_file"], "w") as f:
    json.dump({"result": {"formatted": prefix + " " + message}}, f)
`

func writeFlatPythonPackageZip(t *testing.T, toolsDir, pkgName, scriptName string) string {
	t.Helper()
	pkgDir := filepath.Join(toolsDir, pkgName)
	if err := os.MkdirAll(pkgDir, 0o750); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create(scriptName)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte(flatPyToolSource)); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "package.zip"), buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return pkgDir
}

func TestFlatPythonToolset_WindowsServableEndToEnd(t *testing.T) {
	fakeWindowsGOOS(t)

	var logBuf bytes.Buffer
	toolsDir := t.TempDir()
	writeFlatPythonPackageZip(t, toolsDir, "TestKit", "test_kit.py")

	p := NewPackageProcessor(toolsDir, captureLogger(&logBuf))
	changed, results, err := p.ProcessAll(t.Context())
	if err != nil {
		t.Fatalf("ProcessAll: %v\n%s", err, logBuf.String())
	}
	if !changed || len(results) != 1 {
		t.Fatalf("changed=%v results=%d; want one processed package\n%s", changed, len(results), logBuf.String())
	}
	if len(results[0].Tools) == 0 {
		t.Fatalf("discovery found no tools: errors=%v\n%s", results[0].Errors, logBuf.String())
	}

	m := NewManifest(filepath.Join(toolsDir, "manifest.yaml"), captureLogger(&logBuf))
	if err := m.Load(t.Context()); err != nil {
		t.Fatalf("manifest load: %v\n%s", err, logBuf.String())
	}

	const toolKey = "TestKit" + PackageToolSep + "format_message"
	entry := m.GetTool(toolKey)
	if entry == nil {
		t.Fatalf("tool %q absent from manifest (all: %v)\n%s", toolKey, m.AllToolNames(), logBuf.String())
	}
	if unservable, reason := m.UnservableState(entry); unservable {
		t.Fatalf("tool %q is unservable (%s) — the worker never subscribes to its invoke topic, so it can never be called\n%s",
			toolKey, reason, logBuf.String())
	}
	if _, served := m.ToolNames()[toolKey]; !served {
		t.Fatalf("tool %q missing from ToolNames\n%s", toolKey, logBuf.String())
	}

	assertToolActuallyRuns(t, entry)
}

func assertToolActuallyRuns(t *testing.T, entry *ManifestEntry) {
	t.Helper()

	workDir := t.TempDir()
	resultFile := filepath.Join(workDir, "result.json")
	argsFile := filepath.Join(workDir, "runner_args.json")
	payload, err := json.Marshal(&runnerArgs{
		ToolName:   "format_message",
		Args:       map[string]any{"message": "hello windows"},
		ToolConfig: map[string]any{"prefix": "[E2E]"},
		ResultFile: resultFile,
		OutputDir:  workDir,
	})
	if err != nil {
		t.Fatal(err)
	}
	if writeErr := os.WriteFile(argsFile, payload, 0o600); writeErr != nil {
		t.Fatal(writeErr)
	}

	argv := buildToolArgv(entry, []string{argsFile})
	if argv[0] != containerPython() {
		t.Fatalf("invocation argv = %v; want the resolved interpreter %q first", argv, containerPython())
	}

	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Env = envWithPythonPath(PythonPathFor(entry.ToolDir))
	cmd.Dir = workDir
	if out, runErr := cmd.CombinedOutput(); runErr != nil {
		t.Fatalf("running %v: %v\noutput: %s", argv, runErr, out)
	}

	raw, err := os.ReadFile(resultFile)
	if err != nil {
		t.Fatalf("tool wrote no result file: %v", err)
	}
	var got resultJSON
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("result file is not valid JSON (%s): %v", raw, err)
	}
	result, ok := got.Result.(map[string]any)
	if !ok {
		t.Fatalf("result = %#v; want an object", got.Result)
	}
	if result["formatted"] != "[E2E] hello windows" {
		t.Fatalf("formatted = %#v; want %q", result["formatted"], "[E2E] hello windows")
	}
}

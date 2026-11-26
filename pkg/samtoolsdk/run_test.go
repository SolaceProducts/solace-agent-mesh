// Copyright Solace Corporation
//
// Use of this software is governed by the Apache License, Version 2.0
// (Apache-2.0) included in the LICENSE-APACHE file.

package samtoolsdk

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExecute_BasicTool(t *testing.T) {
	tool := NewTool("greet", "Greets", greetHandler)

	tmpDir := t.TempDir()
	resultFile := filepath.Join(tmpDir, "result.json")
	outputDir := filepath.Join(tmpDir, "output")
	if err := os.MkdirAll(outputDir, 0o700); err != nil {
		t.Fatal(err)
	}

	args := runnerArgs{
		ToolName:   "greet",
		Args:       map[string]any{"name": "Bob"},
		ToolConfig: map[string]any{},
		ResultFile: resultFile,
		OutputDir:  outputDir,
		UserID:     "user1",
		SessionID:  "sess1",
		AppName:    "test-agent",
	}

	argsData, _ := json.Marshal(args)
	argsFile := filepath.Join(tmpDir, "runner_args.json")
	if err := os.WriteFile(argsFile, argsData, 0o600); err != nil {
		t.Fatal(err)
	}

	err := execute([]*ToolDef{tool}, argsFile)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	resultData, err := os.ReadFile(resultFile)
	if err != nil {
		t.Fatalf("read result: %v", err)
	}

	var result resultJSON
	if err := json.Unmarshal(resultData, &result); err != nil {
		t.Fatalf("parse result: %v", err)
	}

	if result.Error != "" {
		t.Errorf("unexpected error: %s", result.Error)
	}

	wireMap, ok := result.Result.(map[string]any)
	if !ok {
		t.Fatalf("result is not a map: %T", result.Result)
	}
	if wireMap["_schema"] != "ToolResult" {
		t.Errorf("_schema: got %v", wireMap["_schema"])
	}
	if wireMap["status"] != "success" {
		t.Errorf("status: got %v", wireMap["status"])
	}
	if wireMap["message"] != "Hello, Bob" {
		t.Errorf("message: got %v", wireMap["message"])
	}
}

func TestExecute_UnknownTool(t *testing.T) {
	tool := NewTool("greet", "Greets", greetHandler)

	tmpDir := t.TempDir()
	resultFile := filepath.Join(tmpDir, "result.json")

	args := runnerArgs{
		ToolName:   "unknown",
		Args:       map[string]any{},
		ResultFile: resultFile,
	}

	argsData, _ := json.Marshal(args)
	argsFile := filepath.Join(tmpDir, "runner_args.json")
	if err := os.WriteFile(argsFile, argsData, 0o600); err != nil {
		t.Fatal(err)
	}

	err := execute([]*ToolDef{tool}, argsFile)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	resultData, _ := os.ReadFile(resultFile)
	var result resultJSON
	if err := json.Unmarshal(resultData, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if result.Error == "" {
		t.Error("expected error in result.json for unknown tool")
	}
}

func TestExecute_WithArtifacts(t *testing.T) {
	type fileParams struct {
		Input Artifact `json:"input" desc:"Input file"`
	}

	handler := func(_ context.Context, p fileParams, _ *ToolContext) (*Result, error) {
		return OK("Read " + p.Input.Filename + ": " + p.Input.AsText()), nil
	}

	tool := NewTool("read_file", "Reads a file", handler)

	tmpDir := t.TempDir()
	inputDir := filepath.Join(tmpDir, "input")
	outputDir := filepath.Join(tmpDir, "output")
	if err := os.MkdirAll(inputDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(outputDir, 0o700); err != nil {
		t.Fatal(err)
	}

	artifactPath := filepath.Join(inputDir, "test.txt")
	if err := os.WriteFile(artifactPath, []byte("hello world"), 0o600); err != nil {
		t.Fatal(err)
	}

	resultFile := filepath.Join(tmpDir, "result.json")

	args := runnerArgs{
		ToolName:      "read_file",
		Args:          map[string]any{},
		ToolConfig:    map[string]any{},
		ArtifactPaths: map[string]string{"input": artifactPath},
		ArtifactMetadata: map[string]map[string]any{
			"input": {
				"filename":  "test.txt",
				"mime_type": "text/plain",
				"version":   float64(1),
			},
		},
		ResultFile: resultFile,
		OutputDir:  outputDir,
	}

	argsData, _ := json.Marshal(args)
	argsFile := filepath.Join(tmpDir, "runner_args.json")
	if err := os.WriteFile(argsFile, argsData, 0o600); err != nil {
		t.Fatal(err)
	}

	err := execute([]*ToolDef{tool}, argsFile)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	resultData, _ := os.ReadFile(resultFile)
	var result resultJSON
	if umErr := json.Unmarshal(resultData, &result); umErr != nil {
		t.Fatalf("unmarshal: %v", umErr)
	}

	if result.Error != "" {
		t.Fatalf("unexpected error: %s", result.Error)
	}

	wireMap := result.Result.(map[string]any)
	if wireMap["message"] != "Read test.txt: hello world" {
		t.Errorf("message: got %v", wireMap["message"])
	}
}

func TestExecute_DataObjectExtraction(t *testing.T) {
	type emptyParams struct{}

	handler := func(_ context.Context, _ emptyParams, _ *ToolContext) (*Result, error) {
		return OK("done", WithDataObjects(DataObject{
			Name:        "output.txt",
			Content:     []byte("generated content"),
			MIMEType:    "text/plain",
			Disposition: DispositionArtifact,
		})), nil
	}

	tool := NewTool("generate", "Generate output", handler)

	tmpDir := t.TempDir()
	outputDir := filepath.Join(tmpDir, "output")
	if err := os.MkdirAll(outputDir, 0o700); err != nil {
		t.Fatal(err)
	}
	resultFile := filepath.Join(tmpDir, "result.json")

	args := runnerArgs{
		ToolName:   "generate",
		Args:       map[string]any{},
		ToolConfig: map[string]any{},
		ResultFile: resultFile,
		OutputDir:  outputDir,
	}

	argsData, _ := json.Marshal(args)
	argsFile := filepath.Join(tmpDir, "runner_args.json")
	if err := os.WriteFile(argsFile, argsData, 0o600); err != nil {
		t.Fatal(err)
	}

	if err := execute([]*ToolDef{tool}, argsFile); err != nil {
		t.Fatalf("execute: %v", err)
	}

	outputFile := filepath.Join(outputDir, "output.txt")
	data, err := os.ReadFile(outputFile)
	if err != nil {
		t.Fatalf("output artifact not written: %v", err)
	}
	if string(data) != "generated content" {
		t.Errorf("output content: got %q", string(data))
	}
}

func TestExecute_PanicHandler(t *testing.T) {
	type emptyParams struct{}

	handler := func(_ context.Context, _ emptyParams, _ *ToolContext) (*Result, error) {
		panic("something went terribly wrong")
	}

	tool := NewTool("crasher", "A tool that panics", handler)

	tmpDir := t.TempDir()
	resultFile := filepath.Join(tmpDir, "result.json")

	args := runnerArgs{
		ToolName:   "crasher",
		Args:       map[string]any{},
		ToolConfig: map[string]any{},
		ResultFile: resultFile,
	}

	argsData, _ := json.Marshal(args)
	argsFile := filepath.Join(tmpDir, "runner_args.json")
	if err := os.WriteFile(argsFile, argsData, 0o600); err != nil {
		t.Fatal(err)
	}

	if err := execute([]*ToolDef{tool}, argsFile); err != nil {
		t.Fatalf("execute returned an error instead of writing result.json: %v", err)
	}

	resultData, err := os.ReadFile(resultFile)
	if err != nil {
		t.Fatalf("result.json not written after panic: %v", err)
	}

	var result resultJSON
	if err := json.Unmarshal(resultData, &result); err != nil {
		t.Fatalf("result.json is not valid JSON: %v", err)
	}

	if result.Error == "" {
		t.Fatal("expected a non-empty error field in result.json after handler panic")
	}
	if !strings.Contains(result.Error, "something went terribly wrong") {
		t.Errorf("error field does not contain the panic message; got: %s", result.Error)
	}
	if result.Result != nil {
		t.Errorf("result field should be nil/absent after panic, got: %v", result.Result)
	}
}

func TestCompactStack(t *testing.T) {
	syntheticStack := `goroutine 1 [running]:
runtime/debug.Stack(...)
	/usr/local/go/src/runtime/debug/stack.go:26
runtime.gopanic(0xc000118520)
	/usr/local/go/src/runtime/panic.go:914
github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk.invokeHandler.func1()
	/workspace/pkg/samtoolsdk/run.go:192
github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk.invokeHandler(...)
	/workspace/pkg/samtoolsdk/run.go:199
myproject/tools/calculator.myHandler(...)
	/workspace/tools/calculator/main.go:42
`

	out := compactStack([]byte(syntheticStack), 3)

	for _, banned := range []string{
		"runtime/debug.Stack",
		"runtime.gopanic",
		"samtoolsdk.invokeHandler",
	} {
		if strings.Contains(out, banned) {
			t.Errorf("compactStack output contains SDK frame %q; got: %s", banned, out)
		}
	}

	if !strings.Contains(out, "myproject/tools/calculator.myHandler") {
		t.Errorf("compactStack output does not contain user handler frame; got: %s", out)
	}
	if !strings.Contains(out, "tools/calculator/main.go:42") {
		t.Errorf("compactStack output does not contain file:line for user frame; got: %s", out)
	}
}

func TestExecute_ForwardsUserProfile(t *testing.T) {
	type noParams struct{}
	tool := NewTool("whoami", "Reports the caller's country",
		func(_ context.Context, _ noParams, tc *ToolContext) (*Result, error) {
			country, _ := tc.UserProfile()["country"].(string)
			return OK("ok", WithData(map[string]any{"country": country})), nil
		})

	tmpDir := t.TempDir()
	resultFile := filepath.Join(tmpDir, "result.json")
	args := runnerArgs{
		ToolName:      "whoami",
		Args:          map[string]any{},
		ResultFile:    resultFile,
		OutputDir:     tmpDir,
		UserID:        "user1",
		A2AUserConfig: `{"user_profile":{"id":"user1","country":"Netherlands"}}`,
	}
	argsData, _ := json.Marshal(args)
	argsFile := filepath.Join(tmpDir, "runner_args.json")
	if err := os.WriteFile(argsFile, argsData, 0o600); err != nil {
		t.Fatal(err)
	}

	if err := execute([]*ToolDef{tool}, argsFile); err != nil {
		t.Fatalf("execute: %v", err)
	}
	resultData, err := os.ReadFile(resultFile)
	if err != nil {
		t.Fatalf("read result: %v", err)
	}
	var result resultJSON
	if err := json.Unmarshal(resultData, &result); err != nil {
		t.Fatalf("parse result: %v", err)
	}
	if result.Error != "" {
		t.Fatalf("unexpected error: %s", result.Error)
	}
	wireMap, _ := result.Result.(map[string]any)
	data, _ := wireMap["data"].(map[string]any)
	if got := data["country"]; got != "Netherlands" {
		t.Errorf("country = %v, want Netherlands", got)
	}
}

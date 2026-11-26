// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package documentdb

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type schemaToolEntry struct {
	Description    string         `json:"description"`
	Parameters     map[string]any `json:"parameters"`
	Instructions   string         `json:"instructions"`
	TimeoutSeconds int            `json:"timeout_seconds"`
}

type schemaWire struct {
	Tools map[string]schemaToolEntry `json:"tools"`
}

func buildBinary(t *testing.T) string {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping binary integration test in -short mode")
	}
	out := filepath.Join(t.TempDir(), "document-db")
	cmd := exec.Command("go", "build", "-o", out, "..")
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("build document-db binary: %v", err)
	}
	return out
}

func runSchema(t *testing.T, binary string, config map[string]any) schemaWire {
	t.Helper()
	args := []string{"--schema"}
	if config != nil {
		cfgPath := filepath.Join(t.TempDir(), "config.json")
		raw, err := json.Marshal(config)
		if err != nil {
			t.Fatalf("marshal config: %v", err)
		}
		if err := os.WriteFile(cfgPath, raw, 0o600); err != nil {
			t.Fatalf("write config: %v", err)
		}
		args = append(args, "--config", cfgPath)
	}
	cmd := exec.Command(binary, args...)
	cmd.Stderr = os.Stderr
	stdout, err := cmd.Output()
	if err != nil {
		t.Fatalf("run %s %v: %v\nstdout=%s", binary, args, err, stdout)
	}
	var wire schemaWire
	if err := json.Unmarshal(stdout, &wire); err != nil {
		t.Fatalf("decode schema output: %v\nstdout=%s", err, stdout)
	}
	if len(wire.Tools) == 0 {
		t.Fatalf("schema output had no tools; stdout=%s", stdout)
	}
	return wire
}

func TestBinarySchema_DefaultsToGeneric(t *testing.T) {
	binary := buildBinary(t)

	wire := runSchema(t, binary, nil)
	if _, ok := wire.Tools[defaultGenericName]; !ok {
		var names []string
		for n := range wire.Tools {
			names = append(names, n)
		}
		t.Fatalf("--schema (no config) should expose %q; got %v", defaultGenericName, names)
	}
}

func TestBinarySchema_MongoDBConfig_RenamesTool(t *testing.T) {
	binary := buildBinary(t)

	cfg := map[string]any{
		"backend":           "mongodb",
		"connection_string": "mongodb+srv://user:pass@cluster.example.net/db",
		"collection":        "orders",
		"database":          "mydb",
		"tool_name":         "orders_mongo_query_990e8400",
		"tool_description":  "Query the orders MongoDB collection using aggregation pipelines.",
		"_str_binary":       "document_db_query",
	}

	wire := runSchema(t, binary, cfg)

	if _, ok := wire.Tools["orders_mongo_query_990e8400"]; !ok {
		var names []string
		for n := range wire.Tools {
			names = append(names, n)
		}
		t.Fatalf("expected dynamic tool name %q; got %v", "orders_mongo_query_990e8400", names)
	}
	entry := wire.Tools["orders_mongo_query_990e8400"]
	if !strings.Contains(strings.ToLower(entry.Description+" "+entry.Instructions), "mongo") {
		t.Errorf("mongo-flavored description/instructions expected; got desc=%q instr=%q", entry.Description, entry.Instructions)
	}
	props, _ := entry.Parameters["properties"].(map[string]any)
	if _, ok := props["pipeline_str"]; !ok {
		t.Errorf("mongo schema must expose pipeline_str (LLM-visible parameter); got %v", paramKeys(props))
	}
}

func TestBinarySchema_DynamoDBConfig_RenamesTool(t *testing.T) {
	binary := buildBinary(t)

	cfg := map[string]any{
		"backend":               "dynamodb",
		"region":                "us-east-1",
		"table_name":            "Orders",
		"aws_access_key_id":     "AKIAIOSFODNN7EXAMPLE",
		"aws_secret_access_key": "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLE",
		"tool_name":             "Orders_dynamodb_query_990e8400",
		"_str_binary":           "document_db_query",
	}

	wire := runSchema(t, binary, cfg)
	if _, ok := wire.Tools["Orders_dynamodb_query_990e8400"]; !ok {
		var names []string
		for n := range wire.Tools {
			names = append(names, n)
		}
		t.Fatalf("expected dynamic tool name; got %v", names)
	}
	entry := wire.Tools["Orders_dynamodb_query_990e8400"]
	props, _ := entry.Parameters["properties"].(map[string]any)
	if _, ok := props["statement"]; !ok {
		t.Errorf("dynamodb schema must expose statement (PartiQL) parameter; got %v", paramKeys(props))
	}
}

func paramKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

type runnerArgsWire struct {
	ToolName   string         `json:"tool_name"`
	Args       map[string]any `json:"args"`
	ToolConfig map[string]any `json:"tool_config"`
	ResultFile string         `json:"result_file"`
}

type resultWire struct {
	Result map[string]any `json:"result"`
	Error  string         `json:"error"`
}

func TestBinaryExec_RunnerArgsContract_InvalidBackend(t *testing.T) {
	binary := buildBinary(t)

	dir := t.TempDir()
	resultFile := filepath.Join(dir, "result.json")
	argsFile := filepath.Join(dir, "runner_args.json")

	args := runnerArgsWire{
		ToolName: "document_db_query",
		Args:     map[string]any{"pipeline_str": "[]"},
		ToolConfig: map[string]any{
			"backend":     "kaboom",
			"_str_binary": "document_db_query",
		},
		ResultFile: resultFile,
	}
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("marshal runner_args: %v", err)
	}
	if err = os.WriteFile(argsFile, raw, 0o600); err != nil {
		t.Fatalf("write runner_args: %v", err)
	}

	cmd := exec.Command(binary, argsFile)
	cmd.Stderr = os.Stderr
	if err = cmd.Run(); err != nil {
		t.Fatalf("binary exec: %v", err)
	}

	rawResult, err := os.ReadFile(resultFile)
	if err != nil {
		t.Fatalf("read result.json: %v", err)
	}
	var wire resultWire
	if err := json.Unmarshal(rawResult, &wire); err != nil {
		t.Fatalf("decode result.json: %v\nraw=%s", err, rawResult)
	}

	if wire.Error != "" {
		if !strings.Contains(strings.ToLower(wire.Error), "backend") &&
			!strings.Contains(strings.ToLower(wire.Error), "tool") {
			t.Errorf("unexpected runner-level error: %q", wire.Error)
		}
		return
	}
	status, _ := wire.Result["status"].(string)
	if status != "error" {
		t.Fatalf("expected error status, got %q (full=%v)", status, wire.Result)
	}
	msg, _ := wire.Result["message"].(string)
	if !strings.Contains(strings.ToLower(msg), "backend") {
		t.Errorf("expected message to mention backend; got %q", msg)
	}
}

func TestBinaryExec_RunnerArgsContract_PlatformConfigShape(t *testing.T) {
	binary := buildBinary(t)

	dir := t.TempDir()
	resultFile := filepath.Join(dir, "result.json")
	argsFile := filepath.Join(dir, "runner_args.json")

	args := runnerArgsWire{
		ToolName: "document_db_query",
		Args:     map[string]any{"pipeline_str": "[]"},
		ToolConfig: map[string]any{
			"backend":           "mongodb",
			"connection_string": "mongodb://127.0.0.1:1/test",
			"collection":        "events",
			"database":          "test",
			"tool_name":         "events_mongo_query_990e8400",
			"tool_description":  "test",
			"_str_binary":       "document_db_query",
		},
		ResultFile: resultFile,
	}
	raw, _ := json.Marshal(args)
	if err := os.WriteFile(argsFile, raw, 0o600); err != nil {
		t.Fatalf("write runner_args: %v", err)
	}

	cmd := exec.Command(binary, argsFile)
	cmd.Stderr = os.Stderr
	done := make(chan error, 1)
	go func() { done <- cmd.Run() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("binary exec: %v", err)
		}
	case <-time.After(30 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("binary hung waiting on mongo connect — driver may have changed timeout semantics")
	}

	rawResult, err := os.ReadFile(resultFile)
	if err != nil {
		t.Fatalf("read result.json: %v", err)
	}
	var wire resultWire
	if err := json.Unmarshal(rawResult, &wire); err != nil {
		t.Fatalf("decode result.json: %v\nraw=%s", err, rawResult)
	}
	status, _ := wire.Result["status"].(string)
	if status == "success" {
		t.Fatalf("unexpected success status; binary should have failed to connect: %v", wire.Result)
	}
}

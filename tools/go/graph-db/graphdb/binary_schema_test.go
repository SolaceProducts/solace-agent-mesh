// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package graphdb

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	sdk "github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"
)

type schemaToolEntry struct {
	Description  string         `json:"description"`
	Parameters   map[string]any `json:"parameters"`
	Instructions string         `json:"instructions"`
}

type schemaWire struct {
	Tools map[string]schemaToolEntry `json:"tools"`
}

func buildBinary(t *testing.T) string {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping binary integration test in -short mode")
	}
	out := filepath.Join(t.TempDir(), "graph-db")
	cmd := exec.Command("go", "build", "-o", out, "..")
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("build graph-db binary: %v", err)
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
	return wire
}

func TestBinarySchema_DefaultName(t *testing.T) {
	binary := buildBinary(t)
	wire := runSchema(t, binary, nil)
	if _, ok := wire.Tools["graph_db_query"]; !ok {
		var names []string
		for n := range wire.Tools {
			names = append(names, n)
		}
		t.Fatalf("expected default tool name graph_db_query; got %v", names)
	}
}

func TestBinarySchema_Neo4jConfig_AdoptsPlatformName(t *testing.T) {
	binary := buildBinary(t)
	cfg := map[string]any{
		"backend":           "neo4j",
		"connection_string": "bolt+s://neo4j.example.com:7687",
		"username":          "neo4j",
		"password":          "secret",
		"database":          "graph",
		"tool_name":         "graph_neo4j_query_aa0e8400",
		"_str_binary":       "graph_db_query",
	}
	wire := runSchema(t, binary, cfg)
	entry, ok := wire.Tools["graph_neo4j_query_aa0e8400"]
	if !ok {
		var names []string
		for n := range wire.Tools {
			names = append(names, n)
		}
		t.Fatalf("expected dynamic tool name; got %v", names)
	}
	if !strings.Contains(strings.ToLower(entry.Instructions+" "+entry.Description), "cypher") {
		t.Errorf("neo4j instructions/description should mention cypher; got %q / %q", entry.Instructions, entry.Description)
	}
}

func TestBinarySchema_NeptuneConfig_AdoptsPlatformName(t *testing.T) {
	binary := buildBinary(t)
	cfg := map[string]any{
		"backend":           "neptune",
		"connection_string": "bolt+s://my-cluster.cluster-abc.us-east-1.neptune.amazonaws.com:8182",
		"region":            "us-east-1",
		"role_arn":          "arn:aws:iam::123456789012:role/SolaceNeptune",
		"tool_name":         "neptune_query_aa0e8400",
		"_str_binary":       "graph_db_query",
	}
	wire := runSchema(t, binary, cfg)
	if _, ok := wire.Tools["neptune_query_aa0e8400"]; !ok {
		var names []string
		for n := range wire.Tools {
			names = append(names, n)
		}
		t.Fatalf("expected dynamic tool name neptune_query_aa0e8400; got %v", names)
	}
}

func TestQueryParamsSchemaRequiresOnlyQuery(t *testing.T) {
	got := sdk.BuildSchema[QueryParams]().Required
	want := []string{"query"}
	if !slices.Equal(got, want) {
		t.Errorf("required = %v, want %v", got, want)
	}
}

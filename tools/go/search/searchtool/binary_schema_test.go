// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package searchtool

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
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
	out := filepath.Join(t.TempDir(), "search")
	cmd := exec.Command("go", "build", "-o", out, "..")
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("build search binary: %v", err)
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
	if _, ok := wire.Tools["search_query"]; !ok {
		var names []string
		for n := range wire.Tools {
			names = append(names, n)
		}
		t.Fatalf("expected default tool name search_query; got %v", names)
	}
}

func TestBinarySchema_ElasticsearchConfig_AdoptsPlatformName(t *testing.T) {
	binary := buildBinary(t)
	cfg := map[string]any{
		"backend":           "elasticsearch",
		"connection_string": "https://elastic.example.com:9200",
		"api_key":           "ZGVtbzpkZW1v",
		"tool_name":         "elasticsearch_query_bb0e8400",
		"_str_binary":       "search_query",
	}
	wire := runSchema(t, binary, cfg)
	entry, ok := wire.Tools["elasticsearch_query_bb0e8400"]
	if !ok {
		var names []string
		for n := range wire.Tools {
			names = append(names, n)
		}
		t.Fatalf("expected dynamic tool name; got %v", names)
	}
	if !strings.Contains(strings.ToLower(entry.Instructions+" "+entry.Description), "elasticsearch") {
		t.Errorf("elasticsearch instructions/description should mention elasticsearch; got %q / %q", entry.Instructions, entry.Description)
	}
}

func TestBinarySchema_OpenSearchConfig_AdoptsPlatformName(t *testing.T) {
	binary := buildBinary(t)
	cfg := map[string]any{
		"backend":           "opensearch",
		"connection_string": "https://my-domain.us-east-1.es.amazonaws.com",
		"region":            "us-east-1",
		"role_arn":          "arn:aws:iam::123456789012:role/SolaceOS",
		"tool_name":         "opensearch_query_bb0e8400",
		"_str_binary":       "search_query",
	}
	wire := runSchema(t, binary, cfg)
	if _, ok := wire.Tools["opensearch_query_bb0e8400"]; !ok {
		var names []string
		for n := range wire.Tools {
			names = append(names, n)
		}
		t.Fatalf("expected dynamic tool name opensearch_query_bb0e8400; got %v", names)
	}
}

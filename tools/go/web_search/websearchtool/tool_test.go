// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package websearchtool

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"
	"github.com/SolaceProducts/solace-agent-mesh/pkg/websearch"
)

const exaResults = `{"results": [{"title": "Exa One", "url": "https://one.example/x", "highlights": ["first point"]}]}`

func fakeExa(t *testing.T) (string, *[]string) {
	t.Helper()
	var keys []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		keys = append(keys, r.Header.Get("x-api-key"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(exaResults))
	}))
	t.Cleanup(server.Close)
	return server.URL, &keys
}

func connectorConfig(apiBase string) map[string]any {
	return map[string]any{
		"_str_binary": "web_search", "tool_name": "web_search_1dec0538",
		"provider": "exa", "api_key": "EXA_KEY", "api_base": apiBase, "safe_search": true,
	}
}

func invoke(t *testing.T, cfg map[string]any, turn *int) map[string]any {
	t.Helper()
	wire, err := samtoolsdk.InvokeInProcess(t.Context(), Tools(), websearch.ToolName,
		map[string]any{"query": "agent mesh", "max_results": float64(3)}, cfg,
		samtoolsdk.InProcessBindings{SearchTurn: turn})
	if err != nil {
		t.Fatal(err)
	}
	return wire.(map[string]any)
}

func TestSearchReturnsCitationsNumberedWithTheAssignedTurn(t *testing.T) {
	base, keys := fakeExa(t)
	turn := 2
	result := invoke(t, connectorConfig(base), &turn)

	if result["status"] != "success" {
		t.Fatalf("result = %v", result)
	}
	if len(*keys) != 1 || (*keys)[0] != "EXA_KEY" {
		t.Fatalf("the search sent keys %v, want the connector's", *keys)
	}
	data, _ := result["data"].(map[string]any)
	if data["search_turn"] != float64(2) {
		t.Fatalf("data = %v, want search_turn 2", data)
	}
	signals, _ := result["signals"].([]any)
	if len(signals) != 1 {
		t.Fatalf("signals = %v, want one", result["signals"])
	}
	signal := signals[0].(map[string]any)
	update := signal["data"].(map[string]any)
	sources := update["sources"].([]any)
	if signal["type"] != "rag_info_update" || update["search_turn"] != float64(2) ||
		sources[0].(map[string]any)["citationId"] != "s2r0" {
		t.Fatalf("signal = %v, want a rag_info_update citing s2r0", signal)
	}
}

func TestSearchWithoutATurnReturnsResultsOnly(t *testing.T) {
	base, _ := fakeExa(t)
	result := invoke(t, connectorConfig(base), nil)
	if result["status"] != "success" {
		t.Fatalf("result = %v", result)
	}
	if _, sent := result["signals"]; sent {
		t.Fatalf("result = %v, want no signals without a turn", result)
	}
}

func TestSearchRefusesAnUnconfiguredProvider(t *testing.T) {
	turn := 0
	result := invoke(t, map[string]any{"tool_name": "web_search_x"}, &turn)
	if result["status"] != "error" {
		t.Fatalf("result = %v, want an error", result)
	}
	if _, sent := result["signals"]; sent {
		t.Fatal("a failed search sent signals")
	}
}

func TestSchemaTakesTheConnectorsNameAndTheProvidersOptions(t *testing.T) {
	raw, err := samtoolsdk.SchemaJSON(Tools(), connectorConfig("https://api.exa.ai"))
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		Tools map[string]struct {
			Parameters map[string]any `json:"parameters"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	entry, named := out.Tools["web_search_1dec0538"]
	if !named {
		t.Fatalf("schema tools = %v, want the connector's tool name", out.Tools)
	}
	props, _ := entry.Parameters["properties"].(map[string]any)
	if _, offered := props["search_type"]; offered {
		t.Error("Exa has no image search, but the schema offers search_type")
	}
	if _, offered := props["query"]; !offered {
		t.Error("the schema does not offer query")
	}
}

func TestTheManifestTimeoutCoversASearchWithRetries(t *testing.T) {
	raw, err := os.ReadFile("../manifest.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Tools map[string]struct {
			TimeoutSeconds int `yaml:"timeout_seconds"`
		} `yaml:"tools"`
	}
	if err := yaml.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	got := time.Duration(manifest.Tools[websearch.ToolName].TimeoutSeconds) * time.Second
	if got < websearch.MaxCallDuration() {
		t.Fatalf("manifest timeout = %s, want at least %s", got, websearch.MaxCallDuration())
	}
}

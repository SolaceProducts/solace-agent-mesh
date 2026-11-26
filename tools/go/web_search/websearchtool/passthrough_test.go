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
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"
	"github.com/SolaceProducts/solace-agent-mesh/pkg/websearch"
)

type recordingExa struct {
	mu     sync.Mutex
	bodies []map[string]any
	url    string
}

func newRecordingExa(t *testing.T, status int, body string) *recordingExa {
	t.Helper()
	r := &recordingExa{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		raw, _ := io.ReadAll(req.Body)
		var decoded map[string]any
		_ = json.Unmarshal(raw, &decoded)
		r.mu.Lock()
		r.bodies = append(r.bodies, decoded)
		r.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	r.url = server.URL
	return r
}

func (r *recordingExa) only(t *testing.T) map[string]any {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.bodies) != 1 {
		t.Fatalf("provider received %d requests, want 1", len(r.bodies))
	}
	return r.bodies[0]
}

func invokeWith(t *testing.T, cfg, args map[string]any, statuses *[]string) map[string]any {
	t.Helper()
	turn := 0
	wire, err := samtoolsdk.InvokeInProcess(t.Context(), Tools(), websearch.ToolName, args, cfg,
		samtoolsdk.InProcessBindings{SearchTurn: &turn, Status: func(s string) error {
			if statuses != nil {
				*statuses = append(*statuses, s)
			}
			return nil
		}})
	if err != nil {
		t.Fatal(err)
	}
	return wire.(map[string]any)
}

func TestSearchAppliesTheConnectorsConfig(t *testing.T) {
	exa := newRecordingExa(t, http.StatusOK, exaResults)
	cfg := connectorConfig(exa.url)
	cfg["max_results"] = 7
	var statuses []string

	invokeWith(t, cfg, map[string]any{"query": "agent mesh"}, &statuses)

	body := exa.only(t)
	if body["numResults"] != float64(7) {
		t.Errorf("numResults = %v, want the connector's max_results 7", body["numResults"])
	}
	if body["moderation"] != true {
		t.Errorf("moderation = %v, want true from safe_search", body["moderation"])
	}
	if len(statuses) != 1 || !strings.Contains(statuses[0], "agent mesh") {
		t.Errorf("statuses = %v, want one search status naming the query", statuses)
	}
}

func TestSearchAppliesTheModelsArguments(t *testing.T) {
	exa := newRecordingExa(t, http.StatusOK, exaResults)
	cfg := connectorConfig(exa.url)
	cfg["safe_search"] = false

	invokeWith(t, cfg, map[string]any{
		"query": "agent mesh", "max_results": float64(3), "recency": "week",
		"include_domains": []any{"solace.com"}, "exclude_domains": []any{"example.com"},
	}, nil)

	body := exa.only(t)
	if body["numResults"] != float64(3) {
		t.Errorf("numResults = %v, want 3", body["numResults"])
	}
	if _, set := body["moderation"]; set {
		t.Errorf("moderation = %v, want it off with safe_search false", body["moderation"])
	}
	if got, _ := body["includeDomains"].([]any); !slices.Equal(got, []any{"solace.com"}) {
		t.Errorf("includeDomains = %v", body["includeDomains"])
	}
	if got, _ := body["excludeDomains"].([]any); !slices.Equal(got, []any{"example.com"}) {
		t.Errorf("excludeDomains = %v", body["excludeDomains"])
	}
	if start, _ := body["startPublishedDate"].(string); start == "" {
		t.Errorf("startPublishedDate = %v, want a date from recency", body["startPublishedDate"])
	}
}

func TestSearchRefusesAnEmptyQuery(t *testing.T) {
	exa := newRecordingExa(t, http.StatusOK, exaResults)

	result := invokeWith(t, connectorConfig(exa.url), map[string]any{"query": ""}, nil)

	if message, _ := result["message"].(string); result["status"] != "error" || !strings.Contains(message, "query is required") {
		t.Fatalf("result = %v, want an error saying the query is required", result)
	}
	exa.mu.Lock()
	defer exa.mu.Unlock()
	if len(exa.bodies) != 0 {
		t.Fatalf("provider received %d requests, want none", len(exa.bodies))
	}
}

func TestSearchCodesAnOutOfCreditsAnswer(t *testing.T) {
	exa := newRecordingExa(t, http.StatusPaymentRequired, `{"error": "out of credits"}`)
	result := invokeWith(t, connectorConfig(exa.url), map[string]any{"query": "q"}, nil)
	if result["status"] != "error" || result["error_code"] != "RATE_LIMITED" {
		t.Fatalf("result = %v, want an error coded RATE_LIMITED", result)
	}
	if _, sent := result["signals"]; sent {
		t.Fatal("a failed search sent signals")
	}
}

func TestTheToolDeclaresEveryConnectorKey(t *testing.T) {
	declared := make([]string, 0, len(configFields))
	for _, f := range configFields {
		declared = append(declared, f.Key)
	}
	slices.Sort(declared)
	want := slices.Sorted(slices.Values(websearch.ConnectorConfigKeys))
	if !slices.Equal(declared, want) {
		t.Fatalf("configFields keys = %v, want %v", declared, want)
	}
}

func TestSchemaDescriptionFollowsTheConnector(t *testing.T) {
	cfg := connectorConfig("https://api.exa.ai")
	cfg["tool_description"] = "Searches regulatory filings."
	raw, err := samtoolsdk.SchemaJSON(Tools(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		Tools map[string]struct {
			Description string `json:"description"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	if got := out.Tools["web_search_1dec0538"].Description; !strings.HasPrefix(got, "Searches regulatory filings.") {
		t.Fatalf("description = %q, want it to lead with the connector's tool_description", got)
	}
}

func TestARateLimitedSearchReportsTheWaitAsAStatus(t *testing.T) {
	var mu sync.Mutex
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		mu.Lock()
		calls++
		first := calls == 1
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if first {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{}`))
			return
		}
		_, _ = w.Write([]byte(exaResults))
	}))
	t.Cleanup(server.Close)
	var statuses []string

	result := invokeWith(t, connectorConfig(server.URL), map[string]any{"query": "q"}, &statuses)

	if result["status"] != "success" {
		t.Fatalf("result = %v, want success after the retry", result)
	}
	if !slices.ContainsFunc(statuses, func(s string) bool { return strings.Contains(s, "rate limiting") }) {
		t.Fatalf("statuses = %v, want one reporting the rate limit", statuses)
	}
}

func TestSearchRefusesOptionsItDoesNotOffer(t *testing.T) {
	for option, value := range map[string]any{"date_restrict": "d7", "safe_search": "off"} {
		exa := newRecordingExa(t, http.StatusOK, exaResults)

		result := invokeWith(t, connectorConfig(exa.url), map[string]any{"query": "q", option: value}, nil)

		message, _ := result["message"].(string)
		if result["status"] != "error" || !strings.Contains(message, option) {
			t.Errorf("%s: result = %v, want an error whose message names the option", option, result)
		}
		if code := result["error_code"]; code != nil {
			t.Errorf("%s: error_code = %v, want none: the model can fix its own arguments", option, code)
		}
		exa.mu.Lock()
		sent := len(exa.bodies)
		exa.mu.Unlock()
		if sent != 0 {
			t.Errorf("%s: provider received %d requests, want none", option, sent)
		}
	}
}

func TestQuotaWaitStatusWording(t *testing.T) {
	for wait, want := range map[time.Duration]string{
		400 * time.Millisecond: "retrying in 1 second",
		time.Second:            "retrying in 1 second",
		8 * time.Second:        "retrying in 8 seconds",
	} {
		if got := quotaWaitStatus(wait); !strings.HasSuffix(got, want) {
			t.Errorf("quotaWaitStatus(%s) = %q, want it to end %q", wait, got, want)
		}
	}
}

func TestTheAPIKeyIsASecretConfigField(t *testing.T) {
	for _, f := range configFields {
		if f.Key == "api_key" && f.Secret {
			return
		}
	}
	t.Fatal("api_key is not declared secret")
}

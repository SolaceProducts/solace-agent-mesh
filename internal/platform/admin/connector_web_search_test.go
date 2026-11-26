// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool/remote"
	"github.com/SolaceProducts/solace-agent-mesh/internal/toolinit"
	"gopkg.in/yaml.v3"
)

const webSearchFlag = "web_search_connector"

type fakeSearchAPI struct {
	server *httptest.Server
	mu     sync.Mutex
	keys   []string
	status int
	body   string
}

func (f *fakeSearchAPI) respondWith(status int, body string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.status, f.body = status, body
}

func newFakeSearchAPI(t *testing.T, status int, body string) *fakeSearchAPI {
	t.Helper()
	f := &fakeSearchAPI{status: status, body: body}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get("x-api-key")
		if bearer := r.Header.Get("Authorization"); bearer != "" {
			key = strings.TrimPrefix(bearer, "Bearer ")
		}
		f.mu.Lock()
		f.keys = append(f.keys, key)
		status, body := f.status, f.body
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(f.server.Close)
	return f
}

func (f *fakeSearchAPI) receivedKeys() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.keys...)
}

const searchResultsBody = `{"results":[{"title":"Agent Mesh","url":"https://example.com/mesh","content":"An agent mesh.","highlights":["An agent mesh."]}]}`

func allowProbeTo(t *testing.T, serverURL string) {
	t.Helper()
	u, err := url.Parse(serverURL)
	if err != nil {
		t.Fatal(err)
	}
	previous := webSearchProbeTransport
	webSearchProbeTransport = func() http.RoundTripper { return httpClientWithSSRFGuardAllowing(u.Host).Transport }
	t.Cleanup(func() { webSearchProbeTransport = previous })
}

func newWebSearchService(t *testing.T, logger *slog.Logger) (*ConnectorService, *store.DB) {
	t.Helper()
	db := testOpenDB(t)
	svc := NewConnectorService(store.NewConnectorRepo(db), nil, "test-ns", logger)
	svc.SetRedeployDeps(db, nil, nil, nil)
	return svc, db
}

func newWebSearchServiceOnly(t *testing.T) *ConnectorService {
	t.Helper()
	svc, _ := newWebSearchService(t, slog.New(slog.DiscardHandler))
	return svc
}

func newWebSearchFlagRegistry(t *testing.T, enabled bool) *features.Registry {
	t.Helper()
	t.Setenv(features.EnvVarName(webSearchFlag), "")
	reg := features.NewRegistry()
	reg.Register(features.Definition{Key: webSearchFlag, ReleasePhase: features.PhaseEarlyAccess, Default: enabled})
	return reg
}

func createWebSearchThroughService(t *testing.T, svc *ConnectorService, name, subtype string, values map[string]any) (*ConnectorResponse, error) {
	t.Helper()
	return svc.Create(t.Context(), CreateConnectorRequest{
		Name: name, Description: "Web search for " + name, Type: "web_search", Subtype: subtype, Values: values,
	}, "tester")
}

func getConnectorThroughService(t *testing.T, svc *ConnectorService, id string) *ConnectorResponse {
	t.Helper()
	resp, err := svc.GetByID(t.Context(), id)
	if err != nil {
		t.Fatalf("GetByID %s: %v", id, err)
	}
	return resp
}

func storedWebSearchValue(t *testing.T, svc *ConnectorService, id, key string) any {
	t.Helper()
	values, _ := getConnectorThroughService(t, svc, id).Values.(map[string]any)
	return values[key]
}

func outcomeText(t *testing.T, resp *ConnectorResponse, err error) string {
	t.Helper()
	if err != nil {
		return err.Error()
	}
	raw, marshalErr := json.Marshal(resp)
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	return string(raw)
}

func requireRefusal(t *testing.T, err error) *ValidationError {
	t.Helper()
	verr, ok := errors.AsType[*ValidationError](err)
	if !ok {
		t.Fatalf("err = %v, want a *ValidationError", err)
	}
	return verr
}

func TestWebSearchConnector_CreatesReadsRedactedAndDeploysTheSTRSearch(t *testing.T) {
	for _, subtype := range []string{"tavily", "exa"} {
		t.Run(subtype, func(t *testing.T) {
			api := newFakeSearchAPI(t, http.StatusOK, searchResultsBody)
			allowProbeTo(t, api.server.URL)
			svc, db := newWebSearchService(t, slog.New(slog.DiscardHandler))

			created, err := createWebSearchThroughService(t, svc, "web-"+subtype, subtype, map[string]any{
				"api_key": "live-" + subtype + "-key", "api_base": api.server.URL, "max_results": 7, "safe_search": "yes",
			})
			if err != nil {
				t.Fatalf("create: %v", err)
			}
			if len(created.Warnings) != 0 {
				t.Errorf("warnings = %v on a key the provider accepted", created.Warnings)
			}
			if keys := api.receivedKeys(); len(keys) != 1 || keys[0] != "live-"+subtype+"-key" {
				t.Errorf("the key check sent keys %v, want the connector's key once", keys)
			}

			for read, resp := range map[string]*ConnectorResponse{"create": created, "get": getConnectorThroughService(t, svc, created.ID)} {
				values, _ := resp.Values.(map[string]any)
				if values["api_key"] != redactedSentinel {
					t.Errorf("%s: api_key = %v, want %s", read, values["api_key"], redactedSentinel)
				}
				if values["api_base"] != api.server.URL {
					t.Errorf("%s: api_base = %v, want it as stored", read, values["api_base"])
				}
			}

			agentID := deterministicUUID("web-search-agent-" + subtype)
			seedAgentWithConnector(t, db, agentID, "web-search-agent-"+subtype, created.ID)
			entry := deployedToolEntry(t, db, agentID, created.ID, "web_search_"+webSearchToolSuffix(created.ID))
			if entry["tool_type"] != "builtin" {
				t.Errorf("tool_type = %v, want builtin", entry["tool_type"])
			}
			config, _ := entry["tool_config"].(map[string]any)
			want := map[string]any{
				"_str_binary": "web_search", "tool_name": "web_search_" + webSearchToolSuffix(created.ID),
				"provider": subtype, "api_key": "live-" + subtype + "-key",
				"api_base": api.server.URL, "max_results": 7, "safe_search": true,
			}
			for key, value := range want {
				if config[key] != value {
					t.Errorf("tool_config.%s = %v (%T), want %v", key, config[key], config[key], value)
				}
			}
			if _, aliased := config["_builtin"]; aliased {
				t.Error("tool_config carries _builtin, which no runtime serves any more")
			}
			if entry["timeout_seconds"] != webSearchToolTimeoutSeconds {
				t.Errorf("timeout_seconds = %v, want %d", entry["timeout_seconds"], webSearchToolTimeoutSeconds)
			}
			if entry["source_connector_category"] != "web_search" {
				t.Errorf("source_connector_category = %v", entry["source_connector_category"])
			}
		})
	}
}

func deployedToolEntry(t *testing.T, db *store.DB, agentID, connID, toolName string) map[string]any {
	t.Helper()
	a, _, _, _, _, err := store.NewAgentRepo(db).GetByID(t.Context(), agentID)
	if err != nil || a == nil {
		t.Fatalf("load agent: %v", err)
	}
	configs, err := resolveConnectorValues(t.Context(), []string{connID}, store.NewConnectorRepo(db), nil)
	if err != nil {
		t.Fatalf("resolveConnectorValues: %v", err)
	}
	cfg, err := NewYAMLService("test-ns", slog.New(slog.DiscardHandler)).BuildAgentConfig(a, nil, []string{"text"}, []string{"text"}, configs)
	if err != nil {
		t.Fatalf("BuildAgentConfig: %v", err)
	}
	tools, err := unwrapTools(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, entry := range tools {
		name, _ := entry["tool_name"].(string)
		if name == toolName {
			return entry
		}
		names = append(names, name)
	}
	t.Fatalf("the agent's tools %v lack %s", names, toolName)
	return nil
}

func TestWebSearchConnector_RendersTheDefaultAPIBaseWhenNoneIsStored(t *testing.T) {
	t.Parallel()
	for subtype, base := range map[string]string{"tavily": "https://api.tavily.com", "exa": "https://api.exa.ai"} {
		entries := buildConnectorTools(ConnectorConfig{ID: "6ba7b810-9dad-11d1-80b4-00c04fd430c8", Type: "web_search", Subtype: subtype,
			Values: map[string]any{"api_key": "k"}})
		config, _ := entries[0]["tool_config"].(map[string]any)
		if config["api_base"] != base {
			t.Errorf("%s api_base = %v, want the type's default %s", subtype, config["api_base"], base)
		}
		if config["safe_search"] != false {
			t.Errorf("%s safe_search = %v, want false when unset", subtype, config["safe_search"])
		}
		if _, set := config["max_results"]; set {
			t.Errorf("%s renders max_results %v that was never set", subtype, config["max_results"])
		}
	}
}

func TestWebSearchConnector_TwoOnOneAgentGetDistinctNames(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	first := createStoredConnector(t, db, CreateConnectorRequest{Name: "web-a", Description: "first web search",
		Type: "web_search", Subtype: "tavily", Values: map[string]any{"api_key": "<<__SAM_REQUIRED__>>"}})
	second := createStoredConnector(t, db, CreateConnectorRequest{Name: "web-b", Description: "second web search",
		Type: "web_search", Subtype: "exa", Values: map[string]any{"api_key": "<<__SAM_REQUIRED__>>"}})
	repo := store.NewConnectorRepo(db)

	if err := validateConnectorToolNames(t.Context(), []string{first, second}, repo, "Researcher", nil); err != nil {
		t.Fatalf("two Web Search connectors were refused: %v", err)
	}
	configs, err := resolveConnectorValues(t.Context(), []string{first, second}, repo, nil)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, cc := range configs {
		for _, entry := range buildConnectorTools(cc) {
			names[entry["tool_name"].(string)] = true
		}
	}
	if !names["web_search_"+webSearchToolSuffix(first)] || !names["web_search_"+webSearchToolSuffix(second)] || len(names) != 2 {
		t.Errorf("tool names = %v, want one web_search_<suffix> per connector", names)
	}

	clashing := map[string]any{"api_key": "<<__SAM_REQUIRED__>>", "tool_name": "web_search_news"}
	third := createStoredConnector(t, db, CreateConnectorRequest{Name: "web-c", Description: "third web search",
		Type: "web_search", Subtype: "tavily", Values: clashing})
	fourth := createStoredConnector(t, db, CreateConnectorRequest{Name: "web-d", Description: "fourth web search",
		Type: "web_search", Subtype: "exa", Values: clashing})
	err = validateConnectorToolNames(t.Context(), []string{third, fourth}, repo, "Researcher", nil)
	if verr, ok := errors.AsType[*ValidationError](err); !ok || !strings.Contains(verr.Message, "web_search_news") {
		t.Errorf("err = %v, want the duplicate web_search_news refused", err)
	}
}

func TestWebSearchConnector_RefusesValuesTheRuntimeCannotServe(t *testing.T) {
	cases := map[string]struct {
		values   map[string]any
		wantText string
	}{
		"a tool_name the runtime would not alias":  {map[string]any{"tool_name": "company_search"}, "tool_name must start with"},
		"a tool_name with only the bare prefix":    {map[string]any{"tool_name": "web_search_"}, "tool_name must start with"},
		"a tool_name in capitals":                  {map[string]any{"tool_name": "web_search_News"}, "tool_name must start with"},
		"the canonical Google web search name":     {map[string]any{"tool_name": "web_search_google"}, "not be the name of a built-in tool"},
		"an api_base that is not http":             {map[string]any{"api_base": "ftp://search.example.com"}, "API Base URL"},
		"an api_base with no host":                 {map[string]any{"api_base": "https://"}, "API Base URL"},
		"an api_base carrying a user and password": {map[string]any{"api_base": "https://user:pw@search.example.com"}, "no user name or password"},
		"safe_search outside its options":          {map[string]any{"safe_search": "maybe"}, "safe_search"},
		"max_results over the provider ceiling":    {map[string]any{"max_results": 50}, "max_results"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			svc := newWebSearchServiceOnly(t)
			values := map[string]any{"api_key": "<<__SAM_REQUIRED__>>"}
			for k, v := range tc.values {
				values[k] = v
			}
			resp, err := createWebSearchThroughService(t, svc, "bad-web", "tavily", values)
			if err == nil {
				t.Fatalf("create = %+v, want a refusal", resp)
			}
			verr := requireRefusal(t, err)
			if tc.wantText != "" && !strings.Contains(verr.Message, tc.wantText) {
				t.Errorf("refusal %q does not say %q", verr.Message, tc.wantText)
			}
		})
	}
}

func TestWebSearchConnector_FlagOffHidesTheType(t *testing.T) {
	if !ConnectorTypeHidden(newWebSearchFlagRegistry(t, false), "web_search") {
		t.Error("ConnectorTypeHidden(web_search) = false with the flag off")
	}
	if ConnectorTypeHidden(newWebSearchFlagRegistry(t, true), "web_search") {
		t.Error("ConnectorTypeHidden(web_search) = true with the flag on")
	}
}

func TestWebSearchConnector_KeyCheckOnSave(t *testing.T) {
	cases := []struct {
		name     string
		status   int
		body     string
		refused  bool
		wantText string
	}{
		{"an accepted key saves", http.StatusOK, searchResultsBody, false, ""},
		{"a rejected key blocks the save", http.StatusUnauthorized, `{"detail":{"error":"Unauthorized: missing or invalid API key."}}`, true, "rejected the API key"},
		{"a rate limit is a warning", http.StatusTooManyRequests, `{"detail":{"error":"slow down"}}`, false, "rate limit"},
		{"a spent plan is a warning", 432, `{"detail":{"error":"plan limit"}}`, false, "HTTP 432"},
		{"a server error is a warning", http.StatusBadGateway, `{}`, false, "HTTP 502"},
		{"a 403 is a warning", http.StatusForbidden, `{"detail":{"error":"forbidden"}}`, false, "HTTP 403"},
		{"a 200 without results is a warning", http.StatusOK, `{"hello":"world"}`, false, "not with search results"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			api := newFakeSearchAPI(t, tc.status, tc.body)
			allowProbeTo(t, api.server.URL)
			svc := newWebSearchServiceOnly(t)

			resp, err := createWebSearchThroughService(t, svc, "web-check", "tavily", map[string]any{"api_key": "tvly-check-key", "api_base": api.server.URL})

			if tc.refused {
				_ = requireRefusal(t, err)
			} else if err != nil {
				t.Fatalf("create: %v, want it saved", err)
			}
			outcome := outcomeText(t, resp, err)
			if !strings.Contains(outcome, tc.wantText) {
				t.Errorf("outcome %s does not say %q", outcome, tc.wantText)
			}
			if strings.Contains(outcome, "tvly-check-key") {
				t.Errorf("outcome carries the key: %s", outcome)
			}
		})
	}

	t.Run("an unreachable provider is a warning", func(t *testing.T) {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		closed := "http://" + listener.Addr().String()
		_ = listener.Close()
		allowProbeTo(t, closed)
		svc := newWebSearchServiceOnly(t)

		resp, err := createWebSearchThroughService(t, svc, "web-down", "exa", map[string]any{"api_key": "exa-key", "api_base": closed})

		if err != nil || !strings.Contains(outcomeText(t, resp, err), "could not reach Exa") {
			t.Fatalf("create = %s, want a save with a warning that Exa could not be reached", outcomeText(t, resp, err))
		}
	})

	t.Run("a private address is not contacted", func(t *testing.T) {
		t.Setenv(allowPrivateMCPEnvVar, "")
		api := newFakeSearchAPI(t, http.StatusUnauthorized, `{}`)
		svc := newWebSearchServiceOnly(t)

		resp, err := createWebSearchThroughService(t, svc, "web-private", "tavily", map[string]any{"api_key": "k", "api_base": api.server.URL})

		if err != nil || !strings.Contains(outcomeText(t, resp, err), "SSRF") {
			t.Fatalf("create = %s, want a save with a warning that the address is not contacted", outcomeText(t, resp, err))
		}
		if keys := api.receivedKeys(); len(keys) != 0 {
			t.Errorf("the key check reached a loopback server: %v", keys)
		}
	})

	t.Run("a placeholder key is not checked", func(t *testing.T) {
		api := newFakeSearchAPI(t, http.StatusUnauthorized, `{}`)
		allowProbeTo(t, api.server.URL)
		svc := newWebSearchServiceOnly(t)

		for i, key := range []string{"<<__SAM_REQUIRED__>>", "${TAVILY_API_KEY}"} {
			if _, err := createWebSearchThroughService(t, svc, fmt.Sprintf("web-later-%d", i), "tavily", map[string]any{"api_key": key, "api_base": api.server.URL}); err != nil {
				t.Fatalf("create with %s: %v", key, err)
			}
		}
		if keys := api.receivedKeys(); len(keys) != 0 {
			t.Errorf("a placeholder key was sent to the provider: %v", keys)
		}
	})
}

func TestWebSearchConnector_RenderedEntryRegistersTheSTRSearch(t *testing.T) {
	for _, subtype := range []string{"tavily", "exa"} {
		t.Run(subtype, func(t *testing.T) {
			t.Parallel()
			conn := ConnectorConfig{ID: "c56a4180-65aa-42ec-a945-5fd21dec0538", Type: "web_search", Subtype: subtype,
				Values: map[string]any{"api_key": subtype + "-runtime-key", "api_base": "https://search.example"}}
			encoded, err := yaml.Marshal(buildConnectorTools(conn))
			if err != nil {
				t.Fatal(err)
			}
			var entries []any
			if err = yaml.Unmarshal(encoded, &entries); err != nil {
				t.Fatal(err)
			}
			host := newHILTestHost("agent-web-search")

			if _, _, err := toolinit.RegisterTools(host, entries, hilTestServices(t), toolinit.Options{}); err != nil {
				t.Fatalf("RegisterTools: %v", err)
			}

			name := "web_search_" + webSearchToolSuffix(conn.ID)
			registered, onSTR := host.tools.GetTool(name).(*remote.Tool)
			if !onSTR {
				t.Fatalf("%s registered as %T, want an STR tool", name, host.tools.GetTool(name))
			}
			if got := registered.ToolPassthroughConfig()["_str_binary"]; got != webSearchSTRBinary {
				t.Errorf("_str_binary = %v, want %s", got, webSearchSTRBinary)
			}
			if got := registered.TimeoutSeconds(); got != webSearchToolTimeoutSeconds {
				t.Errorf("timeout = %d, want %d", got, webSearchToolTimeoutSeconds)
			}
		})
	}
}

func TestWebSearchConnector_DescribeOffersItToAgentNodesOnly(t *testing.T) {
	t.Parallel()
	conn := &store.Connector{ID: "c56a4180-65aa-42ec-a945-5fd21dec0538", Name: "web", Type: "web_search"}
	cc := ConnectorConfig{ID: conn.ID, Type: "web_search", Subtype: "exa", Values: map[string]any{"api_key": "k"}}

	got, err := (&ToolDescriber{logger: slog.New(slog.DiscardHandler)}).describeAgentOnlyConnector(t.Context(), DescribeToolRef{Connector: "web"}, conn, cc)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "web_search" || got[0].ConcreteName != "web_search_1dec0538" {
		t.Fatalf("described = %+v, want the one web_search tool", got)
	}
	if got[0].Status != describeStatusUnavailable || got[0].ToolNode.Usable || got[0].Reason != reasonAgentNodeOnly {
		t.Errorf("described = %+v, want it unavailable to tool nodes with %q", got[0], reasonAgentNodeOnly)
	}
}

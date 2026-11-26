// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package toolinit

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
	mcppkg "github.com/SolaceProducts/solace-agent-mesh/internal/tool/mcp"
	openapipkg "github.com/SolaceProducts/solace-agent-mesh/internal/tool/openapi"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool/remote"
)

type testHost struct {
	name      string
	namespace string
	tools     *tool.Set
	mu        sync.Mutex
	subs      []Startable
	auth      []string
	authMCP   []string
	authDereg []string
}

func newTestHost(name string) *testHost {
	return &testHost{
		name:      name,
		namespace: "test-ns",
		tools:     tool.NewSet(),
	}
}

func (h *testHost) Name() string      { return h.name }
func (h *testHost) Namespace() string { return h.namespace }
func (h *testHost) Tools() *tool.Set  { return h.tools }
func (h *testHost) AddSubComponent(sc Startable) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.subs = append(h.subs, sc)
}

func (h *testHost) RegisterAuthHandler(ah tool.AuthResponseHandler) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.auth = append(h.auth, ah.CredentialKey())
}

func (h *testHost) DeregisterAuthHandler(credentialKey string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.authDereg = append(h.authDereg, credentialKey)
}

func (h *testHost) RegisterMCPAuthHandler(ah tool.AuthResponseHandler) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.authMCP = append(h.authMCP, ah.CredentialKey())
}

func newTestServices(t *testing.T) runtime.Services {
	t.Helper()
	brk := broker.NewDevBroker()
	if err := brk.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	return runtime.NewRuntime(brk, nil).Services()
}

func TestLookupBuiltinTool_CanonicalCatalog(t *testing.T) {
	t.Parallel()
	cases := []struct{ name string }{
		{"list_artifacts"},
		{"load_artifact"},
		{"delete_artifact"},
		{"get_current_time"},
		{"web_request"},
		{"artifact_grep"},
		{"index_search"},
		{"reinterpret_artifact"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := LookupBuiltinTool(c.name, nil, Options{})
			if got == nil {
				t.Fatalf("LookupBuiltinTool(%q) returned nil", c.name)
			}
			if got.Name() != c.name {
				t.Errorf("Name() = %q, want %q", got.Name(), c.name)
			}
		})
	}
}

func TestDataAnalysisGroup_IncludesReinterpretArtifact(t *testing.T) {
	t.Parallel()
	group := LookupBuiltinGroup("data_analysis", Options{})
	if !slices.Contains(group, "reinterpret_artifact") {
		t.Fatalf("data_analysis group %v missing reinterpret_artifact", group)
	}
	if LookupBuiltinTool("reinterpret_artifact", nil, Options{}) == nil {
		t.Fatal("reinterpret_artifact is in the group but does not resolve via LookupBuiltinTool")
	}
}

func TestLookupBuiltinTool_UnknownReturnsNil(t *testing.T) {
	t.Parallel()
	if got := LookupBuiltinTool("totally_unknown", nil, Options{}); got != nil {
		t.Errorf("expected nil for unknown tool, got %v", got)
	}
}

func TestLookupBuiltinTool_SubTaskReservedForAgent(t *testing.T) {
	t.Parallel()
	if got := LookupBuiltinTool("sub_task", nil, Options{}); got != nil {
		t.Errorf("sub_task should not be returned from the catalog")
	}
}

func TestLookupBuiltinTool_AdditionalBuiltinHook(t *testing.T) {
	t.Parallel()
	called := false
	opts := Options{
		AdditionalBuiltin: func(name string, _ map[string]any) tool.BuiltinTool {
			called = true
			if name == "extension_tool" {
				return nil
			}
			return nil
		},
	}
	_ = LookupBuiltinTool("extension_tool", nil, opts)
	if !called {
		t.Error("AdditionalBuiltin was not consulted on catalog miss")
	}
}

func TestLookupBuiltinTool_CatalogShadowsExtension(t *testing.T) {
	t.Parallel()
	called := false
	opts := Options{
		AdditionalBuiltin: func(_ string, _ map[string]any) tool.BuiltinTool {
			called = true
			return nil
		},
	}
	got := LookupBuiltinTool("get_current_time", nil, opts)
	if got == nil {
		t.Fatal("canonical catalog should resolve get_current_time")
	}
	if called {
		t.Error("AdditionalBuiltin should not be consulted on catalog hit")
	}
}

func TestLookupBuiltinTool_PassesToolConfig(t *testing.T) {
	t.Parallel()
	toolMap := map[string]any{
		"tool_config": map[string]any{"api_key": "secret"},
	}
	got := LookupBuiltinTool("web_search_google", toolMap, Options{})
	if got == nil {
		t.Fatal("web_search_google should resolve with tool_config")
	}
}

func TestLookupBuiltinTool_WebSearchByName(t *testing.T) {
	t.Parallel()
	toolMap := map[string]any{"tool_config": map[string]any{
		"provider": "exa", "api_key": "k", "api_base": "http://search.internal",
	}}
	got := LookupBuiltinTool("web_search", toolMap, Options{})
	if got == nil || got.Name() != "web_search" {
		t.Fatalf("LookupBuiltinTool(web_search) = %v", got)
	}
	if _, ok := got.Parameters().Properties["search_type"]; ok {
		t.Error("schema offers image search, so the Exa tool_config did not reach the constructor")
	}
}

func TestLookupBuiltinGroup_CanonicalGroups(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		minTools int
	}{
		{"artifact_management", 5},
		{"general_agent_tools", 3},
		{"image_tools", 3},
		{"web_tools", 1},
		{"data_analysis", 4},
		{"hil_tools", 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := LookupBuiltinGroup(c.name, Options{})
			if got == nil {
				t.Fatalf("LookupBuiltinGroup(%q) returned nil", c.name)
			}
			if len(got) < c.minTools {
				t.Errorf("LookupBuiltinGroup(%q) returned %d tools, want >= %d", c.name, len(got), c.minTools)
			}
		})
	}
}

func TestLookupBuiltinGroup_PlatformAliasResolves(t *testing.T) {
	t.Parallel()
	got := LookupBuiltinGroup("builtin_research_tools", Options{})
	want := LookupBuiltinGroup("research", Options{})
	if !slices.Equal(got, want) {
		t.Errorf("builtin_research_tools = %v, want %v", got, want)
	}
}

func TestLookupBuiltinGroup_CodeExecution(t *testing.T) {
	t.Parallel()
	want := []string{"execute_python_code"}
	if got := LookupBuiltinGroup("code_execution", Options{}); !slices.Equal(got, want) {
		t.Errorf("code_execution = %v, want %v", got, want)
	}
	if got := LookupBuiltinGroup("builtin_code_execution", Options{}); !slices.Equal(got, want) {
		t.Errorf("builtin_code_execution = %v, want %v", got, want)
	}
}

func TestLookupBuiltinGroup_UnknownReturnsNil(t *testing.T) {
	t.Parallel()
	if got := LookupBuiltinGroup("no_such_group", Options{}); got != nil {
		t.Errorf("expected nil for unknown group, got %v", got)
	}
}

func TestLookupBuiltinGroup_AdditionalHookOnMiss(t *testing.T) {
	t.Parallel()
	opts := Options{
		AdditionalBuiltinGroup: func(name string) []string {
			if name == "extension_group" {
				return []string{"foo", "bar"}
			}
			return nil
		},
	}
	got := LookupBuiltinGroup("extension_group", opts)
	if !slices.Equal(got, []string{"foo", "bar"}) {
		t.Errorf("extension_group = %v, want [foo bar]", got)
	}
}

func TestDescribeToolEntry(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   map[string]any
		want string
	}{
		{"tool_name wins", map[string]any{"tool_name": "foo", "tool_type": "builtin"}, "foo"},
		{"group_name fallback", map[string]any{"group_name": "g", "tool_type": "builtin-group"}, "group:g"},
		{"tool_type fallback", map[string]any{"tool_type": "mcp"}, "mcp"},
		{"unknown fallback", map[string]any{}, "<unknown>"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := DescribeToolEntry(c.in); got != c.want {
				t.Errorf("DescribeToolEntry = %q, want %q", got, c.want)
			}
		})
	}
}

func TestRegisterTools_BuiltinInProcess(t *testing.T) {
	t.Parallel()
	svc := newTestServices(t)
	host := newTestHost("agent-a")

	toolsCfg := []any{
		map[string]any{"tool_type": "builtin", "tool_name": "get_current_time"},
	}
	remoteExec, entries, err := RegisterTools(host, toolsCfg, svc, Options{})
	if err != nil {
		t.Fatalf("RegisterTools: %v", err)
	}
	if remoteExec != nil {
		t.Error("in-process builtin should not allocate a remote executor")
	}
	if host.tools.GetTool("get_current_time") == nil {
		t.Error("get_current_time not registered on host")
	}
	if len(entries) != 1 || len(entries[0].ToolNames) != 1 || entries[0].ToolNames[0] != "get_current_time" {
		t.Errorf("entries = %+v, want one entry naming get_current_time", entries)
	}
}

func TestRegisterTools_BuiltinGroupExpandsMembers(t *testing.T) {
	t.Parallel()
	svc := newTestServices(t)
	host := newTestHost("agent-b")

	toolsCfg := []any{
		map[string]any{"tool_type": "builtin-group", "group_name": "artifact_management"},
	}
	_, _, err := RegisterTools(host, toolsCfg, svc, Options{})
	if err != nil {
		t.Fatalf("RegisterTools: %v", err)
	}
	for _, name := range LookupBuiltinGroup("artifact_management", Options{}) {
		if host.tools.GetTool(name) == nil {
			t.Errorf("expected %q registered from group", name)
		}
	}
}

func TestRegisterTools_ResearchGroupRefusesAProviderBesideAGoogleKey(t *testing.T) {
	t.Parallel()
	const googleKey, exaKey = "GOOGLE_KEY_SECRET", "EXA_KEY_SECRET"
	var requests atomic.Int32
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_, _ = w.Write([]byte(`{"results":[],"items":[]}`))
	}))
	t.Cleanup(endpoint.Close)
	host := newTestHost("agent-research-conflict")
	_, _, err := RegisterTools(host, []any{map[string]any{
		"tool_type":  "builtin-group",
		"group_name": "research",
		"tool_config": map[string]any{
			"google_search_api_key": googleKey, "google_cse_id": "cx",
			"provider": "exa", "api_key": exaKey, "api_base": endpoint.URL,
			"interactive_plan_verification": false,
		},
	}}, newTestServices(t), Options{})
	if err != nil {
		t.Fatalf("RegisterTools: %v", err)
	}

	calls := map[string]map[string]any{
		"web_search_google": {"query": "q"},
		"deep_research":     {"research_question": "q", "max_runtime_seconds": float64(30)},
	}
	for name, args := range calls {
		t.Run(name, func(t *testing.T) {
			got, ok := host.tools.GetTool(name).(tool.BuiltinTool)
			if !ok {
				t.Fatalf("%s not registered in process", name)
			}
			toolCtx := &tool.Context{
				ParentTaskID: "task-" + name,
				InternalLLMCall: func(context.Context, string, string, float64) (string, error) {
					return "", errors.New("no model in this test")
				},
			}
			r, err := got.Execute(t.Context(), args, toolCtx)
			if err != nil {
				t.Fatal(err)
			}
			if sent := requests.Swap(0); sent != 0 {
				t.Errorf("api_base received %d requests carrying a key meant for another provider", sent)
			}
			if r.Status == tool.StatusSuccess {
				t.Errorf("Execute succeeded, want a refusal of the provider beside google_search_api_key")
			}
			for _, want := range []string{"provider exa", "google_search_api_key", "api_base"} {
				if !strings.Contains(r.Message, want) {
					t.Errorf("message = %q, want it to name %q", r.Message, want)
				}
			}
			for _, key := range []string{googleKey, exaKey} {
				if strings.Contains(r.Message, key) {
					t.Errorf("message leaks a key: %q", r.Message)
				}
			}
		})
	}
}

func TestRegisterTools_BuiltinUnknownFallsThroughToSTR(t *testing.T) {
	t.Parallel()
	svc := newTestServices(t)
	host := newTestHost("agent-c")

	toolsCfg := []any{
		map[string]any{"tool_type": "builtin", "tool_name": "create_chart_from_plotly_config"},
	}
	remoteExec, _, err := RegisterTools(host, toolsCfg, svc, Options{})
	if err != nil {
		t.Fatalf("RegisterTools: %v", err)
	}
	if remoteExec == nil {
		t.Error("STR fallthrough should construct a remote executor")
	}
	if host.tools.GetTool("create_chart_from_plotly_config") == nil {
		t.Error("STR-backed builtin not registered")
	}
	if len(host.subs) == 0 {
		t.Error("remote executor should have been added as sub-component")
	}
}

func TestRegisterTools_ExplicitSubTaskEntryRegistersNothing(t *testing.T) {
	t.Parallel()
	svc := newTestServices(t)
	host := newTestHost("agent-sub-task")

	toolsCfg := []any{
		map[string]any{"tool_type": "builtin", "tool_name": "sub_task"},
	}
	remoteExec, entries, err := RegisterTools(host, toolsCfg, svc, Options{})
	if err != nil {
		t.Fatalf("RegisterTools: %v", err)
	}
	if remoteExec != nil {
		t.Error("sub_task must not allocate a remote executor")
	}
	if host.tools.GetTool("sub_task") != nil {
		t.Error("sub_task registered by the tools list; the agent registers it itself")
	}
	if len(entries) != 0 {
		t.Errorf("entries = %+v, want none", entries)
	}
}

func TestRegisterTools_SamRemoteIsRejected(t *testing.T) {
	t.Parallel()
	svc := newTestServices(t)
	host := newTestHost("agent-d")

	toolsCfg := []any{
		map[string]any{"tool_type": "sam_remote", "tool_name": "legacy_tool"},
	}
	_, _, err := RegisterTools(host, toolsCfg, svc, Options{})
	if err == nil || !strings.Contains(err.Error(), "sam_remote is no longer supported") {
		t.Errorf("expected sam_remote rejection, got %v", err)
	}
}

func TestRegisterTools_AppliesToolDefaults(t *testing.T) {
	t.Parallel()
	svc := newTestServices(t)
	host := newTestHost("agent-e")

	called := false
	opts := Options{
		ApplyToolDefaults: func(name string, toolMap map[string]any) map[string]any {
			if name == "get_current_time" {
				called = true
			}
			return toolMap
		},
	}
	toolsCfg := []any{
		map[string]any{"tool_type": "builtin", "tool_name": "get_current_time"},
	}
	if _, _, err := RegisterTools(host, toolsCfg, svc, opts); err != nil {
		t.Fatalf("RegisterTools: %v", err)
	}
	if !called {
		t.Error("ApplyToolDefaults was not invoked for in-process builtin")
	}
}

func TestRegisterTools_SkillNamespacedBuiltin(t *testing.T) {
	t.Parallel()
	svc := newTestServices(t)
	host := newTestHost("agent-f")

	toolsCfg := []any{
		map[string]any{"tool_type": "builtin", "tool_name": "myskill:get_current_time"},
	}
	if _, _, err := RegisterTools(host, toolsCfg, svc, Options{}); err != nil {
		t.Fatalf("RegisterTools: %v", err)
	}
	if host.tools.GetTool("myskill:get_current_time") == nil {
		t.Error("skill-namespaced builtin should be registered under its prefixed name")
	}
}

func TestRegisterOneToolEntry_Builtin(t *testing.T) {
	t.Parallel()
	svc := newTestServices(t)
	host := newTestHost("skill-host")
	logger := slog.Default()
	exec := mustExec(t, svc, host)

	got, err := RegisterOneToolEntry(host, map[string]any{
		"tool_type": "builtin",
		"tool_name": "get_current_time",
	}, exec, Options{}, logger)
	if err != nil {
		t.Fatalf("RegisterOneToolEntry: %v", err)
	}
	if !slices.Equal(got.Added, []string{"get_current_time"}) || len(got.Existing) != 0 {
		t.Errorf("got %+v, want get_current_time added", got)
	}
}

func TestRegisterOneToolEntry_UnknownToolType(t *testing.T) {
	t.Parallel()
	svc := newTestServices(t)
	host := newTestHost("skill-host")
	exec := mustExec(t, svc, host)

	_, err := RegisterOneToolEntry(host, map[string]any{
		"tool_type": "totally_made_up",
		"tool_name": "x",
	}, exec, Options{}, slog.Default())
	if err == nil || !strings.Contains(err.Error(), "unsupported tool_type") {
		t.Errorf("err = %v, want unsupported tool_type", err)
	}
}

func TestRegisterOneToolEntry_MissingType(t *testing.T) {
	t.Parallel()
	svc := newTestServices(t)
	host := newTestHost("skill-host")
	exec := mustExec(t, svc, host)

	_, err := RegisterOneToolEntry(host, map[string]any{}, exec, Options{}, slog.Default())
	if err == nil || !strings.Contains(err.Error(), "missing tool_type") {
		t.Errorf("err = %v, want missing tool_type", err)
	}
}

func TestRegisterOneToolEntry_MCPInvalidConfig(t *testing.T) {
	t.Parallel()
	svc := newTestServices(t)
	host := newTestHost("skill-host")
	exec := mustExec(t, svc, host)

	_, err := RegisterOneToolEntry(host, map[string]any{"tool_type": "mcp"}, exec, Options{}, slog.Default())
	if err == nil || !strings.Contains(err.Error(), "invalid mcp tool config") {
		t.Errorf("err = %v, want invalid mcp tool config", err)
	}
}

func TestRegisterOneToolEntry_OpenAPIInvalidConfig(t *testing.T) {
	t.Parallel()
	svc := newTestServices(t)
	host := newTestHost("skill-host")
	exec := mustExec(t, svc, host)

	_, err := RegisterOneToolEntry(host, map[string]any{"tool_type": "openapi"}, exec, Options{}, slog.Default())
	if err == nil || !strings.Contains(err.Error(), "invalid openapi tool config") {
		t.Errorf("err = %v, want invalid openapi tool config", err)
	}
}

func TestRegisterOneToolEntry_UnknownGroup(t *testing.T) {
	t.Parallel()
	svc := newTestServices(t)
	host := newTestHost("skill-host")
	exec := mustExec(t, svc, host)

	_, err := RegisterOneToolEntry(host, map[string]any{
		"tool_type":  "builtin-group",
		"group_name": "no_such_group",
	}, exec, Options{}, slog.Default())
	if err == nil || !strings.Contains(err.Error(), "unknown builtin group") {
		t.Errorf("err = %v, want unknown builtin group", err)
	}
}

func TestRegisterOneToolEntry_GroupHandlesAlreadyRegistered(t *testing.T) {
	t.Parallel()
	svc := newTestServices(t)
	host := newTestHost("skill-host")
	exec := mustExec(t, svc, host)
	logger := slog.Default()

	if _, err := RegisterOneToolEntry(host, map[string]any{
		"tool_type": "builtin",
		"tool_name": "list_artifacts",
	}, exec, Options{}, logger); err != nil {
		t.Fatal(err)
	}

	got, err := RegisterOneToolEntry(host, map[string]any{
		"tool_type":  "builtin-group",
		"group_name": "artifact_management",
	}, exec, Options{}, logger)
	if err != nil {
		t.Fatalf("group expansion: %v", err)
	}
	want := LookupBuiltinGroup("artifact_management", Options{})
	if len(got.Added)+len(got.Existing) != len(want) {
		t.Errorf("group registered %+v, want %d members", got, len(want))
	}
	if !slices.Equal(got.Existing, []string{"list_artifacts"}) || slices.Contains(got.Added, "list_artifacts") {
		t.Errorf("got %+v, want list_artifacts reported as already registered and not added", got)
	}
}

func mustExec(_ *testing.T, svc runtime.Services, host *testHost) *remote.Executor {
	return remote.NewExecutor(svc.Broker(), host.Namespace(), host.Name())
}

type capturingHandler struct {
	mu      sync.Mutex
	records []capturedRecord
}

type capturedRecord struct {
	level slog.Level
	msg   string
	attrs map[string]string
}

func (h *capturingHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *capturingHandler) Handle(_ context.Context, r slog.Record) error {
	attrs := make(map[string]string)
	r.Attrs(func(a slog.Attr) bool {
		attrs[a.Key] = a.Value.String()
		return true
	})
	h.mu.Lock()
	defer h.mu.Unlock()
	h.records = append(h.records, capturedRecord{level: r.Level, msg: r.Message, attrs: attrs})
	return nil
}

func (h *capturingHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *capturingHandler) WithGroup(string) slog.Handler      { return h }

func (h *capturingHandler) find(msg string) (capturedRecord, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, r := range h.records {
		if r.msg == msg {
			return r, true
		}
	}
	return capturedRecord{}, false
}

func swapDefaultLogger(t *testing.T) *capturingHandler {
	t.Helper()
	capture := &capturingHandler{}
	prev := slog.Default()
	slog.SetDefault(slog.New(capture))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return capture
}

const minimalOpenAPISpec = `{
  "openapi": "3.0.0",
  "info": {"title": "Pets", "version": "1.0.0"},
  "servers": [{"url": "https://api.example.com"}],
  "paths": {
    "/pets": {
      "get": {
        "operationId": "listPets",
        "responses": {"200": {"description": "ok"}}
      }
    }
  }
}`

func TestRegisterTools_OpenAPISpecLoadFailure_ErrorLogAndFailedEntry(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	capture := swapDefaultLogger(t)
	host := newTestHost("openapi-fail-agent")

	_, entries, err := RegisterTools(host, []any{map[string]any{
		"tool_type":           "openapi",
		"specification_url":   srv.URL + "/spec.json?token=secret",
		"base_url":            "https://api.example.com",
		"source_connector_id": "conn-123",
	}}, newTestServices(t), Options{})
	if err != nil {
		t.Fatalf("RegisterTools must stay per-entry resilient on spec-load failure, got %v", err)
	}

	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1 failed record", len(entries))
	}
	e := entries[0]
	if e.Status != EntryStatusFailed {
		t.Errorf("Status = %q, want %q", e.Status, EntryStatusFailed)
	}
	if e.Error == "" {
		t.Error("failed entry must carry the error summary")
	}
	if strings.Contains(e.Error, "token=secret") {
		t.Errorf("Error = %q, must not carry the spec URL's query string (rides the agent card and platform API)", e.Error)
	}
	if len(e.ToolNames) != 0 {
		t.Errorf("ToolNames = %v, want none", e.ToolNames)
	}
	if SourceConnectorID(e.ToolMap) != "conn-123" {
		t.Errorf("SourceConnectorID = %q, want conn-123", SourceConnectorID(e.ToolMap))
	}

	rec, ok := capture.find("openapi spec load failed; no tools registered")
	if !ok {
		t.Fatal("expected the spec-load failure log record")
	}
	if rec.level != slog.LevelError {
		t.Errorf("log level = %v, want ERROR", rec.level)
	}
	if rec.attrs["connectorId"] != "conn-123" {
		t.Errorf("connectorId attr = %q, want conn-123", rec.attrs["connectorId"])
	}
	if got := rec.attrs["specSource"]; got != srv.URL+"/spec.json" {
		t.Errorf("specSource attr = %q, want URL with query stripped", got)
	}
	if strings.Contains(rec.attrs["error"], "token=secret") {
		t.Errorf("error attr = %q, must not carry the spec URL's query string", rec.attrs["error"])
	}
}

func TestRegisterTools_OpenAPISuccess_ReadyEntry(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(minimalOpenAPISpec))
	}))
	defer srv.Close()

	host := newTestHost("openapi-ok-agent")
	_, entries, err := RegisterTools(host, []any{map[string]any{
		"tool_type":           "openapi",
		"specification_url":   srv.URL + "/spec.json",
		"source_connector_id": "conn-ok",
	}}, newTestServices(t), Options{})
	if err != nil {
		t.Fatalf("RegisterTools: %v", err)
	}

	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(entries))
	}
	e := entries[0]
	if e.Status != EntryStatusReady {
		t.Errorf("Status = %q, want %q (error %q)", e.Status, EntryStatusReady, e.Error)
	}
	if !slices.Contains(e.ToolNames, "list_pets") {
		t.Errorf("ToolNames = %v, want list_pets registered", e.ToolNames)
	}
}

func TestRegisterTools_OpenAPIAllowListMatchesNothing_ReadyEntryZeroTools(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(minimalOpenAPISpec))
	}))
	defer srv.Close()

	host := newTestHost("openapi-empty-agent")
	_, entries, err := RegisterTools(host, []any{map[string]any{
		"tool_type":           "openapi",
		"specification_url":   srv.URL + "/spec.json",
		"allow_list":          []any{"noSuchOperation"},
		"source_connector_id": "conn-empty",
	}}, newTestServices(t), Options{})
	if err != nil {
		t.Fatalf("RegisterTools: %v", err)
	}

	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(entries))
	}
	e := entries[0]
	if e.Status != EntryStatusReady {
		t.Errorf("Status = %q, want %q (error %q)", e.Status, EntryStatusReady, e.Error)
	}
	if e.Error != "" {
		t.Errorf("Error = %q, want empty on a legitimately empty toolset", e.Error)
	}
	if len(e.ToolNames) != 0 {
		t.Errorf("ToolNames = %v, want none", e.ToolNames)
	}
}

type scopedBuiltinFake struct {
	tool.BuiltinTool
	scopes []string
}

func (s scopedBuiltinFake) RequiredScopes() []string { return s.scopes }

func TestRenamedBuiltinTool_PreservesInnerScopes(t *testing.T) {
	inner := scopedBuiltinFake{
		BuiltinTool: LookupBuiltinTool("get_current_time", nil, Options{}),
		scopes:      []string{"tool:scoped:invoke"},
	}
	renamed := &renamedBuiltinTool{BuiltinTool: inner, name: "my-skill:get_current_time"}

	got := tool.RequiredScopesOf(renamed)
	if len(got) != 1 || got[0] != "tool:scoped:invoke" {
		t.Errorf("RequiredScopesOf(renamed) = %v, want [tool:scoped:invoke] (inner scopes must survive renaming)", got)
	}
}

func TestRenamedBuiltinTool_UnscopedStaysUnscoped(t *testing.T) {
	renamed := &renamedBuiltinTool{
		BuiltinTool: LookupBuiltinTool("get_current_time", nil, Options{}),
		name:        "my-skill:get_current_time",
	}

	if got := tool.RequiredScopesOf(renamed); got != nil {
		t.Errorf("RequiredScopesOf(renamed) = %v, want nil (unscoped built-in must stay unscoped)", got)
	}
}

type resultSchemaBuiltinFake struct {
	tool.BuiltinTool
	schema *tool.JSONSchema
}

func (s resultSchemaBuiltinFake) ResultSchema() *tool.JSONSchema { return s.schema }

func TestRenamedBuiltinTool_PreservesInnerResultSchema(t *testing.T) {
	want := &tool.JSONSchema{
		Type:       "object",
		Properties: map[string]any{"current_time": map[string]any{"type": "string"}},
	}
	inner := resultSchemaBuiltinFake{
		BuiltinTool: LookupBuiltinTool("get_current_time", nil, Options{}),
		schema:      want,
	}
	renamed := &renamedBuiltinTool{BuiltinTool: inner, name: "my-skill:get_current_time"}

	got := tool.ResultSchemaOf(renamed)
	if got == nil {
		t.Fatal("ResultSchemaOf(renamed) = nil, want the inner schema (it must survive renaming)")
	}
	if _, ok := got.Properties["current_time"]; !ok {
		t.Errorf("ResultSchemaOf(renamed).Properties = %v, want a \"current_time\" property", got.Properties)
	}
}

type plainBuiltinFake struct{}

func (plainBuiltinFake) Name() string                                      { return "plain" }
func (plainBuiltinFake) Description() string                               { return "plain fake" }
func (plainBuiltinFake) Parameters() *tool.JSONSchema                      { return nil }
func (plainBuiltinFake) Instructions() string                              { return "" }
func (plainBuiltinFake) IsLongRunning() bool                               { return false }
func (plainBuiltinFake) RawStringArgs() []string                           { return nil }
func (plainBuiltinFake) ArtifactParams() map[string]tool.ArtifactParamInfo { return nil }
func (plainBuiltinFake) ContextParam() string                              { return "" }
func (plainBuiltinFake) Execute(context.Context, map[string]any, *tool.Context) (*tool.Result, error) {
	return tool.OK("ok"), nil
}

func TestRenamedBuiltinTool_UndeclaredResultSchemaStaysNil(t *testing.T) {
	renamed := &renamedBuiltinTool{BuiltinTool: plainBuiltinFake{}, name: "my-skill:plain"}

	if got := tool.ResultSchemaOf(renamed); got != nil {
		t.Errorf("ResultSchemaOf(renamed) = %v, want nil (an undeclared schema must stay undeclared)", got)
	}
}

func TestRegisterTools_OpenAPIDiscoveredButNoneRegistered_FailedEntry(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(minimalOpenAPISpec))
	}))
	defer srv.Close()

	capture := swapDefaultLogger(t)
	host := newTestHost("openapi-collide-agent")
	colliding := &renamedBuiltinTool{
		BuiltinTool: LookupBuiltinTool("get_current_time", nil, Options{}),
		name:        "list_pets",
	}
	if err := host.tools.Register(colliding); err != nil {
		t.Fatal(err)
	}

	_, entries, err := RegisterTools(host, []any{map[string]any{
		"tool_type":           "openapi",
		"specification_url":   srv.URL + "/spec.json",
		"source_connector_id": "conn-collide",
	}}, newTestServices(t), Options{})
	if err != nil {
		t.Fatalf("RegisterTools must stay per-entry resilient when registration fails, got %v", err)
	}

	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1 failed record", len(entries))
	}
	e := entries[0]
	if e.Status != EntryStatusFailed {
		t.Errorf("Status = %q, want %q", e.Status, EntryStatusFailed)
	}
	if !strings.Contains(e.Error, "1 tools discovered, 0 registered") {
		t.Errorf("Error = %q, want the discovered/registered counts", e.Error)
	}
	if len(e.ToolNames) != 0 {
		t.Errorf("ToolNames = %v, want none", e.ToolNames)
	}

	rec, ok := capture.find("openapi tools discovered but none registered")
	if !ok {
		t.Fatal("expected the none-registered failure log record")
	}
	if rec.level != slog.LevelError {
		t.Errorf("log level = %v, want ERROR", rec.level)
	}
	if rec.attrs["connectorId"] != "conn-collide" {
		t.Errorf("connectorId attr = %q, want conn-collide", rec.attrs["connectorId"])
	}
}

const twoOpOpenAPISpec = `{
  "openapi": "3.0.0",
  "info": {"title": "Pets", "version": "1.0.0"},
  "servers": [{"url": "https://api.example.com"}],
  "paths": {
    "/pets": {
      "get": {
        "operationId": "listPets",
        "responses": {"200": {"description": "ok"}}
      }
    },
    "/toys": {
      "get": {
        "operationId": "listToys",
        "responses": {"200": {"description": "ok"}}
      }
    }
  }
}`

func TestRegisterTools_OpenAPIPartiallyRegistered_ReadyEntryWithLossNote(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(twoOpOpenAPISpec))
	}))
	defer srv.Close()

	capture := swapDefaultLogger(t)
	host := newTestHost("openapi-partial-agent")
	colliding := &renamedBuiltinTool{
		BuiltinTool: LookupBuiltinTool("get_current_time", nil, Options{}),
		name:        "list_pets",
	}
	if err := host.tools.Register(colliding); err != nil {
		t.Fatal(err)
	}

	_, entries, err := RegisterTools(host, []any{map[string]any{
		"tool_type":           "openapi",
		"specification_url":   srv.URL + "/spec.json",
		"source_connector_id": "conn-partial",
	}}, newTestServices(t), Options{})
	if err != nil {
		t.Fatalf("RegisterTools: %v", err)
	}

	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(entries))
	}
	e := entries[0]
	if e.Status != EntryStatusReady {
		t.Errorf("Status = %q, want %q (partial registration keeps the entry ready)", e.Status, EntryStatusReady)
	}
	if len(e.ToolNames) != 1 || e.ToolNames[0] != "list_toys" {
		t.Errorf("ToolNames = %v, want [list_toys]", e.ToolNames)
	}
	if !strings.Contains(e.Error, "2 tools discovered, 1 registered") {
		t.Errorf("Error = %q, want the partial-loss counts", e.Error)
	}

	rec, ok := capture.find("openapi entry registered with losses")
	if !ok {
		t.Fatal("expected the partial-loss WARN log record")
	}
	if rec.level != slog.LevelWarn {
		t.Errorf("log level = %v, want WARN", rec.level)
	}
}

func TestOpenAPISpecSource_RedactsCredentialsAndQuery(t *testing.T) {
	t.Parallel()
	got := openAPISpecSource(&openapipkg.ToolsetConfig{
		SpecURL: "https://user:hunter2@api.example.com/spec.json?token=secret",
	})
	if strings.Contains(got, "hunter2") {
		t.Errorf("openAPISpecSource = %q, must mask the basic-auth password", got)
	}
	if strings.Contains(got, "token=secret") {
		t.Errorf("openAPISpecSource = %q, must strip the query string", got)
	}
	if !strings.Contains(got, "api.example.com/spec.json") {
		t.Errorf("openAPISpecSource = %q, must keep the host and path", got)
	}
}

func TestRegisterTools_MCPDiscoveryFailure_ErrorLogAndFailedEntry(t *testing.T) {
	capture := swapDefaultLogger(t)
	host := newTestHost("mcp-fail-agent")

	_, entries, err := RegisterTools(host, []any{map[string]any{
		"tool_type": "mcp",
		"connection_params": map[string]any{
			"type":    "stdio",
			"command": "/nonexistent/sam-test-mcp-server",
			"timeout": 2,
		},
		"source_connector_id": "conn-mcp",
	}}, newTestServices(t), Options{})
	if err != nil {
		t.Fatalf("RegisterTools must stay per-entry resilient on MCP failure, got %v", err)
	}

	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1 failed record", len(entries))
	}
	e := entries[0]
	if e.Status != EntryStatusFailed {
		t.Errorf("Status = %q, want %q", e.Status, EntryStatusFailed)
	}
	if e.Error == "" {
		t.Error("failed entry must carry the error summary")
	}
	if SourceConnectorID(e.ToolMap) != "conn-mcp" {
		t.Errorf("SourceConnectorID = %q, want conn-mcp", SourceConnectorID(e.ToolMap))
	}

	var rec capturedRecord
	var ok bool
	for _, msg := range []string{
		"mcp client connection failed; toolset tools not registered",
		"mcp tool discovery failed; toolset tools not registered",
	} {
		if rec, ok = capture.find(msg); ok {
			break
		}
	}
	if !ok {
		t.Fatal("expected an MCP failure log record")
	}
	if rec.level != slog.LevelError {
		t.Errorf("log level = %v, want ERROR", rec.level)
	}
	if rec.attrs["connectorId"] != "conn-mcp" {
		t.Errorf("connectorId attr = %q, want conn-mcp", rec.attrs["connectorId"])
	}
}

func TestMCPServerLogID_RedactsURLDerivedID(t *testing.T) {
	t.Parallel()
	got := mcpServerLogID(&mcppkg.ToolsetConfig{
		URL:      "https://mcp.example.com/sse?api_key=SECRET123",
		ClientID: "mcp_mcp_example_com_sse_api_key_SECRET123",
	})
	if strings.Contains(got, "SECRET123") {
		t.Errorf("mcpServerLogID = %q, must not carry the query token", got)
	}
	if !strings.Contains(got, "mcp.example.com/sse") {
		t.Errorf("mcpServerLogID = %q, want the host/path retained for correlation", got)
	}
	if stdio := mcpServerLogID(&mcppkg.ToolsetConfig{ClientID: "mcp_my_server"}); stdio != "mcp_my_server" {
		t.Errorf("stdio mcpServerLogID = %q, want the command-derived ClientID", stdio)
	}
}

func TestRegisterTools_BuiltinGroupEmitsEntryRecord(t *testing.T) {
	t.Parallel()
	svc := newTestServices(t)
	host := newTestHost("agent-group-record")

	cfg := map[string]any{
		"tool_type":   "builtin-group",
		"group_name":  "artifact_management",
		"tool_config": map[string]any{"tool_output_save_threshold_bytes": 1000},
	}
	_, entries, err := RegisterTools(host, []any{cfg}, svc, Options{})
	if err != nil {
		t.Fatalf("RegisterTools: %v", err)
	}

	if len(entries) != 1 {
		t.Fatalf("entries = %+v, want exactly one record for the group entry", entries)
	}
	want := LookupBuiltinGroup("artifact_management", Options{})
	got := entries[0].ToolNames
	if len(got) != len(want) {
		t.Fatalf("ToolNames = %v (%d), want the %d group members %v", got, len(got), len(want), want)
	}
	have := map[string]bool{}
	for _, n := range got {
		have[n] = true
	}
	for _, n := range want {
		if !have[n] {
			t.Errorf("group member %q missing from ToolNames %v", n, got)
		}
	}
	if entries[0].ToolMap["group_name"] != "artifact_management" {
		t.Errorf("ToolMap not carried on the record: %+v", entries[0].ToolMap)
	}
}

type authoredBuiltinFake struct {
	plainBuiltinFake
	authored bool
}

func (a authoredBuiltinFake) AuthoredSchemas() bool { return a.authored }

func TestRenamedBuiltinTool_ForwardsAuthoredSchemasBothWays(t *testing.T) {
	authored := &renamedBuiltinTool{
		BuiltinTool: authoredBuiltinFake{authored: true},
		name:        "my-skill:authored",
	}
	if !tool.HasAuthoredSchemas(authored) {
		t.Error("renaming dropped the authored marker, demoting the tool off the fatal tier")
	}

	discovered := &renamedBuiltinTool{
		BuiltinTool: authoredBuiltinFake{authored: false},
		name:        "my-skill:discovered",
	}
	if tool.HasAuthoredSchemas(discovered) {
		t.Error("renaming promoted a discovered tool onto the fatal tier")
	}

	plain := &renamedBuiltinTool{BuiltinTool: plainBuiltinFake{}, name: "my-skill:plain"}
	if tool.HasAuthoredSchemas(plain) {
		t.Error("renaming invented an authored marker for a tool that declares none")
	}
}

type hinterBuiltinFake struct {
	plainBuiltinFake
	hint string
}

func (h hinterBuiltinFake) ArtifactNotFoundHint() string { return h.hint }

func (h hinterBuiltinFake) ArgumentErrorHint(map[string]any) string { return "args: " + h.hint }

func TestRenamedBuiltinTool_ForwardsArgumentErrorHint(t *testing.T) {
	renamed := &renamedBuiltinTool{BuiltinTool: hinterBuiltinFake{hint: "x"}, name: "my-skill:editor"}
	if got := tool.ArgumentErrorHintOf(renamed, nil); got != "args: x" {
		t.Errorf("ArgumentErrorHintOf(renamed) = %q, want the inner tool's hint", got)
	}
	plain := &renamedBuiltinTool{BuiltinTool: plainBuiltinFake{}, name: "my-skill:plain"}
	if got := tool.ArgumentErrorHintOf(plain, nil); got != "" {
		t.Errorf("ArgumentErrorHintOf(renamed plain) = %q, want none", got)
	}
}

func TestRenamedBuiltinTool_ForwardsArtifactNotFoundHint(t *testing.T) {
	const want = "create the file first"
	renamed := &renamedBuiltinTool{
		BuiltinTool: hinterBuiltinFake{hint: want},
		name:        "my-skill:editor",
	}
	if got := tool.ArtifactNotFoundHintOf(renamed); got != want {
		t.Errorf("ArtifactNotFoundHintOf(renamed) = %q, want %q (the hint must survive renaming)", got, want)
	}

	plain := &renamedBuiltinTool{BuiltinTool: plainBuiltinFake{}, name: "my-skill:plain"}
	if got := tool.ArtifactNotFoundHintOf(plain); got != "" {
		t.Errorf("ArtifactNotFoundHintOf(renamed plain) = %q, want \"\" (renaming must not invent a hint)", got)
	}
}

func TestRegisterOneToolEntry_UnreachableMCPServerCanBeRetriedAndLeavesNothing(t *testing.T) {
	t.Parallel()
	svc := newTestServices(t)
	host := newTestHost("skill-host")
	exec := mustExec(t, svc, host)

	_, err := RegisterOneToolEntry(host, map[string]any{
		"tool_type": "mcp",
		"connection_params": map[string]any{
			"type":    "stdio",
			"command": "/nonexistent/sam-test-mcp-server",
			"timeout": 2,
		},
	}, exec, Options{}, slog.Default())
	if err == nil || !IsUnavailable(err) {
		t.Fatalf("err = %v, want a failure a later attempt can retry", err)
	}
	host.mu.Lock()
	defer host.mu.Unlock()
	if len(host.subs) != 0 {
		t.Errorf("a failed MCP entry left %d sub-components on the host, want none", len(host.subs))
	}
}

func TestRegisterOneToolEntry_AnInvalidEntryIsNotRetried(t *testing.T) {
	t.Parallel()
	svc := newTestServices(t)
	host := newTestHost("skill-host")
	exec := mustExec(t, svc, host)

	for _, entry := range []map[string]any{
		{"tool_type": "mcp"},
		{"tool_type": "openapi"},
		{"tool_type": "weird"},
	} {
		if _, err := RegisterOneToolEntry(host, entry, exec, Options{}, slog.Default()); err == nil || IsUnavailable(err) {
			t.Errorf("entry %v: err = %v, want an error a retry cannot fix", entry, err)
		}
	}
}

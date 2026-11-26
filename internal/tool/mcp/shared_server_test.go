// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package mcp

import (
	"bytes"
	"context"
	"log/slog"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	mcpgo "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

var sharedServerTools = []string{"get_weather", "get_time", "get_stock", "get_news"}

func newSharedMCPServer(t *testing.T, tls bool) *httptest.Server {
	t.Helper()
	srv := server.NewMCPServer("shared", "1.0.0", server.WithToolCapabilities(true))
	for _, name := range sharedServerTools {
		srv.AddTool(mcpgo.NewTool(name, mcpgo.WithDescription(name)),
			func(context.Context, mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
				return mcpgo.NewToolResultText(name), nil
			})
	}
	handler := server.NewStreamableHTTPServer(srv)
	var ts *httptest.Server
	if tls {
		ts = httptest.NewUnstartedServer(handler)
		ts.StartTLS()
	} else {
		ts = httptest.NewServer(handler)
	}
	t.Cleanup(ts.Close)
	return ts
}

type entryFilter struct {
	toolName string
	allow    []string
	deny     []string
	prefix   string
}

func (f entryFilter) apply(cfg *ToolsetConfig) *ToolsetConfig {
	cfg.ToolName, cfg.AllowList, cfg.DenyList, cfg.ToolNamePrefix = f.toolName, f.allow, f.deny, f.prefix
	return cfg
}

type entryOption func(raw, connParams map[string]any)

func withSSLVerifyOff() entryOption {
	return func(_, conn map[string]any) { conn["ssl_config"] = map[string]any{"verify": false} }
}

func withHeader(name, value string) entryOption {
	return func(_, conn map[string]any) { conn["headers"] = map[string]any{name: value} }
}

func withManifest(tools ...ManifestTool) entryOption {
	return func(raw, _ map[string]any) {
		manifest := make([]any, 0, len(tools))
		for _, mt := range tools {
			manifest = append(manifest, map[string]any{"name": mt.Name, "description": mt.Description})
		}
		raw["manifest"] = manifest
	}
}

func sharedEntryConfig(t *testing.T, url string, f entryFilter, opts ...entryOption) *ToolsetConfig {
	t.Helper()
	conn := map[string]any{"type": "streamable-http", "url": url}
	raw := map[string]any{
		"tool_type":         "mcp",
		"tool_name":         f.toolName,
		"tool_name_prefix":  f.prefix,
		"connection_params": conn,
	}
	if len(f.allow) > 0 {
		raw["allow_list"] = anySlice(f.allow)
	}
	if len(f.deny) > 0 {
		raw["deny_list"] = anySlice(f.deny)
	}
	for _, opt := range opts {
		opt(raw, conn)
	}
	cfg, err := ParseToolsetConfig(raw)
	if err != nil {
		t.Fatalf("ParseToolsetConfig: %v", err)
	}
	return cfg
}

func anySlice(in []string) []any {
	out := make([]any, len(in))
	for i, s := range in {
		out[i] = s
	}
	return out
}

func discoveredNames(t *testing.T, mgr *Manager, cfg *ToolsetConfig) []string {
	t.Helper()
	tools, err := mgr.DiscoverTools(cfg)
	if err != nil {
		t.Fatalf("DiscoverTools: %v", err)
	}
	names := make([]string, 0, len(tools))
	for _, mt := range tools {
		names = append(names, mt.Name())
	}
	slices.Sort(names)
	return names
}

func registerEntries(t *testing.T, mgr *Manager, cfgs []*ToolsetConfig) [][]string {
	t.Helper()
	got := make([][]string, len(cfgs))
	for i, cfg := range cfgs {
		if err := mgr.AddClient(cfg); err != nil {
			t.Fatalf("AddClient entry %d: %v", i, err)
		}
		got[i] = discoveredNames(t, mgr, cfg)
	}
	return got
}

var sharedServerEntries = []struct {
	name   string
	filter entryFilter
	want   []string
}{
	{"single tool_name", entryFilter{toolName: "get_weather"}, []string{"get_weather"}},
	{"another tool_name", entryFilter{toolName: "get_stock"}, []string{"get_stock"}},
	{"allow_list of two", entryFilter{allow: []string{"get_time", "get_news"}}, []string{"get_news", "get_time"}},
	{"deny_list", entryFilter{deny: []string{"get_weather", "get_time", "get_stock"}}, []string{"get_news"}},
	{"tool_name with prefix", entryFilter{toolName: "get_news", prefix: "x_"}, []string{"x_get_news"}},
}

func TestSharedServerEntriesEachRegisterTheirOwnTools(t *testing.T) {
	ts := newSharedMCPServer(t, false)

	for _, reversed := range []bool{false, true} {
		t.Run(map[bool]string{false: "declared order", true: "reversed order"}[reversed], func(t *testing.T) {
			var cfgs []*ToolsetConfig
			var wants [][]string
			for _, e := range sharedServerEntries {
				cfgs = append(cfgs, sharedEntryConfig(t, ts.URL, e.filter))
				wants = append(wants, e.want)
			}
			if reversed {
				slices.Reverse(cfgs)
				slices.Reverse(wants)
			}

			mgr := NewManager(slog.Default())
			t.Cleanup(mgr.Stop)

			got := registerEntries(t, mgr, cfgs)
			for i := range cfgs {
				if !slices.Equal(got[i], wants[i]) {
					t.Errorf("entry %d registered %v, want %v", i, got[i], wants[i])
				}
			}
		})
	}
}

func TestSharedServerEntriesAllShareOneClient(t *testing.T) {
	ts := newSharedMCPServer(t, false)
	first := sharedEntryConfig(t, ts.URL, entryFilter{toolName: "get_weather"})
	second := sharedEntryConfig(t, ts.URL, entryFilter{toolName: "get_stock"})
	if first.ClientID != second.ClientID {
		t.Fatalf("ClientIDs differ (%q vs %q); the entries would no longer share one connection or one credential key", first.ClientID, second.ClientID)
	}

	mgr := NewManager(slog.Default())
	t.Cleanup(mgr.Stop)
	registerEntries(t, mgr, []*ToolsetConfig{first, second})

	if n := len(mgr.bifrost.GetClients()); n != 1 {
		t.Errorf("manager holds %d Bifrost clients for two entries on one server, want 1", n)
	}
}

func TestToBifrostClientConfigNeverNarrowsASharedClient(t *testing.T) {
	for _, e := range sharedServerEntries {
		t.Run(e.name, func(t *testing.T) {
			cfg := e.filter.apply(&ToolsetConfig{
				ConnectionType: connTypeStreamableHTTP,
				URL:            "http://127.0.0.1:1/mcp",
				ClientID:       "shared",
			})
			bc := cfg.ToBifrostClientConfig()
			if !slices.Equal([]string(bc.ToolsToExecute), []string{"*"}) {
				t.Errorf("ToolsToExecute = %v, want [*]: the client is shared by every entry on its server, so the entry's own filter must be applied at DiscoverTools", bc.ToolsToExecute)
			}
		})
	}
}

func TestSharedServerEntriesOnTLSDirectClient(t *testing.T) {
	ts := newSharedMCPServer(t, true)

	var cfgs []*ToolsetConfig
	for _, e := range sharedServerEntries {
		cfgs = append(cfgs, sharedEntryConfig(t, ts.URL, e.filter, withSSLVerifyOff()))
	}

	mgr := NewManager(slog.Default())
	t.Cleanup(mgr.Stop)

	if err := mgr.AddClient(cfgs[0]); err != nil {
		t.Fatalf("AddClient first: %v", err)
	}
	mgr.mu.RLock()
	firstClient := mgr.direct[cfgs[0].ClientID]
	mgr.mu.RUnlock()

	got := registerEntries(t, mgr, cfgs[1:])
	for i, e := range sharedServerEntries[1:] {
		if !slices.Equal(got[i], e.want) {
			t.Errorf("%s registered %v, want %v", e.name, got[i], e.want)
		}
	}

	mgr.mu.RLock()
	defer mgr.mu.RUnlock()
	if mgr.direct[cfgs[0].ClientID] != firstClient {
		t.Error("a later entry replaced the direct client of the first; the first connection is orphaned and never closed")
	}
	if len(mgr.direct) != 1 {
		t.Errorf("manager holds %d direct clients for one server, want 1", len(mgr.direct))
	}
}

func TestSharedServerEntryAfterAFailedFirstEntryConnectsForItself(t *testing.T) {
	ts := newSharedMCPServer(t, false)
	mgr := NewManager(slog.Default())
	t.Cleanup(mgr.Stop)

	broken := sharedEntryConfig(t, ts.URL, entryFilter{toolName: "get_weather"})
	broken.SSLCABundle = "/nonexistent/ca.pem"
	if err := mgr.AddClient(broken); err == nil {
		t.Fatal("AddClient with an unreadable CA bundle succeeded; the case needs a failing first entry")
	}

	next := sharedEntryConfig(t, ts.URL, entryFilter{toolName: "get_stock"})
	if got := registerEntries(t, mgr, []*ToolsetConfig{next})[0]; !slices.Equal(got, []string{"get_stock"}) {
		t.Errorf("the entry after a failed first entry registered %v, want [get_stock]", got)
	}
}

func manifestTool(name, description string) ManifestTool {
	return ManifestTool{Name: name, Description: description}
}

const manifestServerURL = "http://mcp.manifest.invalid/mcp"

func TestSharedServerManifestEntriesEachRegisterTheirOwnManifest(t *testing.T) {
	entries := func() []*ToolsetConfig {
		return []*ToolsetConfig{
			sharedEntryConfig(t, manifestServerURL, entryFilter{}, withManifest(manifestTool("get_weather", "w"))),
			sharedEntryConfig(t, manifestServerURL, entryFilter{toolName: "get_stock"}, withManifest(manifestTool("get_weather", "w"), manifestTool("get_stock", "s"))),
			sharedEntryConfig(t, manifestServerURL, entryFilter{prefix: "all_"}, withManifest(manifestTool("get_time", "t"), manifestTool("get_stock", "s"))),
		}
	}
	want := [][]string{{"get_weather"}, {"get_stock"}, {"all_get_stock", "all_get_time"}}

	for _, reversed := range []bool{false, true} {
		cfgs := entries()
		wants := slices.Clone(want)
		if reversed {
			slices.Reverse(cfgs)
			slices.Reverse(wants)
		}
		mgr := NewManager(slog.Default())
		t.Cleanup(mgr.Stop)

		got := registerEntries(t, mgr, cfgs)
		for i := range cfgs {
			if !slices.Equal(got[i], wants[i]) {
				t.Errorf("reversed=%v: manifest entry %d registered %v, want %v", reversed, i, got[i], wants[i])
			}
		}
	}
}

func TestSharedServerManifestEntryUsesItsOwnDeclarationOfASharedName(t *testing.T) {
	mgr := NewManager(slog.Default())
	t.Cleanup(mgr.Stop)

	first := sharedEntryConfig(t, manifestServerURL, entryFilter{}, withManifest(manifestTool("get_weather", "from the first entry")))
	second := sharedEntryConfig(t, manifestServerURL, entryFilter{}, withManifest(manifestTool("get_weather", "from the second entry")))
	registerEntries(t, mgr, []*ToolsetConfig{first, second})

	for cfg, want := range map[*ToolsetConfig]string{first: "from the first entry", second: "from the second entry"} {
		tools, err := mgr.DiscoverTools(cfg)
		if err != nil || len(tools) != 1 || tools[0].Description() != want {
			t.Errorf("DiscoverTools = %v, %v; want the one tool described %q", tools, err, want)
		}
	}
}

func TestDiscoverToolsWarnsWhenAnEntryMatchesNoTools(t *testing.T) {
	ts := newSharedMCPServer(t, false)
	tlsServer := newSharedMCPServer(t, true)

	cases := []struct {
		name string
		url  string
		opts []entryOption
	}{
		{"live server", ts.URL, nil},
		{"tls direct client", tlsServer.URL, []entryOption{withSSLVerifyOff()}},
		{"manifest", manifestServerURL, []entryOption{withManifest(manifestTool("get_weather", "w"), manifestTool("get_time", "t"))}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			logs := &syncBuffer{}
			mgr := NewManager(slog.New(slog.NewTextHandler(logs, nil)))
			t.Cleanup(mgr.Stop)

			matching := sharedEntryConfig(t, tc.url, entryFilter{toolName: "get_weather"}, tc.opts...)
			typo := sharedEntryConfig(t, tc.url, entryFilter{toolName: "get_wether", prefix: "p_"}, tc.opts...)
			got := registerEntries(t, mgr, []*ToolsetConfig{matching, typo})
			if len(got[1]) != 0 {
				t.Fatalf("typo entry registered %v, want nothing", got[1])
			}

			out := logs.String()
			for _, want := range []string{
				"level=WARN",
				"filter matched none",
				"toolName=get_wether",
				"toolNamePrefix=p_",
				"entriesOnServer=2",
				"server=" + tc.url,
				"get_weather",
			} {
				if !strings.Contains(out, want) {
					t.Errorf("warning is missing %q; log:\n%s", want, out)
				}
			}
		})
	}
}

func TestDiscoverToolsWarnsWhenTheServerOffersNoTools(t *testing.T) {
	srv := server.NewMCPServer("empty", "1.0.0", server.WithToolCapabilities(true))
	ts := httptest.NewServer(server.NewStreamableHTTPServer(srv))
	t.Cleanup(ts.Close)

	logs := &syncBuffer{}
	mgr := NewManager(slog.New(slog.NewTextHandler(logs, nil)))
	t.Cleanup(mgr.Stop)

	cfg := sharedEntryConfig(t, ts.URL, entryFilter{})
	if got := registerEntries(t, mgr, []*ToolsetConfig{cfg})[0]; len(got) != 0 {
		t.Fatalf("registered %v from a server with no tools", got)
	}
	out := logs.String()
	if !strings.Contains(out, "level=WARN") || !strings.Contains(out, "server offers no tools") || strings.Contains(out, "filter matched") {
		t.Errorf("want a WARN saying the server offers no tools, without filter wording; log:\n%s", out)
	}
}

func TestDiscoverToolsStaysQuietWhenEveryEntryMatches(t *testing.T) {
	ts := newSharedMCPServer(t, false)
	logs := &syncBuffer{}
	mgr := NewManager(slog.New(slog.NewTextHandler(logs, nil)))
	t.Cleanup(mgr.Stop)

	var cfgs []*ToolsetConfig
	for _, e := range sharedServerEntries {
		cfgs = append(cfgs, sharedEntryConfig(t, ts.URL, e.filter))
	}
	registerEntries(t, mgr, cfgs)

	if strings.Contains(logs.String(), "level=WARN") {
		t.Errorf("entries that all matched logged a warning:\n%s", logs.String())
	}
}

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

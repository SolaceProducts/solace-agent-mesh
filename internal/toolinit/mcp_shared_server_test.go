// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package toolinit

import (
	"context"
	"maps"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	mcpgo "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

var sharedServerToolNames = []string{"get_weather", "get_time", "get_stock", "get_news"}

func startSharedMCPServer(t *testing.T, tls bool) string {
	t.Helper()
	srv := server.NewMCPServer("shared", "1.0.0", server.WithToolCapabilities(true))
	for _, name := range sharedServerToolNames {
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
	return ts.URL
}

const manifestOnlyServerURL = "http://mcp.manifest.invalid/mcp"

func mcpEntry(url string, filter map[string]any, connExtras map[string]any) map[string]any {
	conn := map[string]any{"type": "streamable-http", "url": url}
	maps.Copy(conn, connExtras)
	entry := map[string]any{"tool_type": "mcp", "connection_params": conn}
	maps.Copy(entry, filter)
	return entry
}

func manifestOf(names ...string) []any {
	manifest := make([]any, 0, len(names))
	for _, n := range names {
		manifest = append(manifest, map[string]any{"name": n, "description": n})
	}
	return manifest
}

func stopSubComponents(t *testing.T, host *testHost) {
	t.Helper()
	t.Cleanup(func() {
		host.mu.Lock()
		defer host.mu.Unlock()
		for _, sc := range host.subs {
			sc.Stop()
		}
	})
}

func registeredToolNames(host *testHost) []string {
	var names []string
	for _, tl := range host.Tools().ListTools() {
		names = append(names, tl.Name())
	}
	slices.Sort(names)
	return names
}

type sharedServerEntry struct {
	id     string
	filter map[string]any
	want   []string
}

func TestRegisterTools_MCPEntriesSharingAServerEachRegisterTheirTools(t *testing.T) {
	liveURL := startSharedMCPServer(t, false)
	tlsURL := startSharedMCPServer(t, true)
	tlsOff := map[string]any{"ssl_config": map[string]any{"verify": false}}
	filters := []sharedServerEntry{
		{"weather", map[string]any{"tool_name": "get_weather"}, []string{"get_weather"}},
		{"stock", map[string]any{"tool_name": "get_stock"}, []string{"get_stock"}},
		{"allowed", map[string]any{"allow_list": []any{"get_time", "get_news"}, "tool_name_prefix": "a_"}, []string{"a_get_news", "a_get_time"}},
		{"denied", map[string]any{"deny_list": []any{"get_weather", "get_time", "get_stock"}, "tool_name_prefix": "n_"}, []string{"n_get_news"}},
	}

	cases := []struct {
		name       string
		url        string
		connExtras map[string]any
		manifest   []any
		entries    []sharedServerEntry
	}{
		{name: "live, several tool_name entries", url: liveURL, entries: []sharedServerEntry{
			{"weather", map[string]any{"tool_name": "get_weather"}, []string{"get_weather"}},
			{"stock", map[string]any{"tool_name": "get_stock"}, []string{"get_stock"}},
			{"time", map[string]any{"tool_name": "get_time"}, []string{"get_time"}},
		}},
		{name: "live, tool_name then allow_list", url: liveURL, entries: []sharedServerEntry{filters[0], filters[2]}},
		{name: "live, mixed filters and prefixes", url: liveURL, entries: filters},
		{name: "tls direct client, mixed filters and prefixes", url: tlsURL, connExtras: tlsOff, entries: filters},
		{name: "manifest, mixed filters and prefixes", url: manifestOnlyServerURL, manifest: manifestOf(sharedServerToolNames...), entries: filters},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			toolsCfg := make([]any, 0, len(tc.entries))
			want := map[string][]string{}
			var wantAll []string
			for _, e := range tc.entries {
				filter := map[string]any{"source_connector_id": e.id}
				maps.Copy(filter, e.filter)
				if tc.manifest != nil {
					filter["manifest"] = tc.manifest
				}
				toolsCfg = append(toolsCfg, mcpEntry(tc.url, filter, tc.connExtras))
				want[e.id] = e.want
				wantAll = append(wantAll, e.want...)
			}
			slices.Sort(wantAll)

			for range 10 {
				host := newTestHost("shared-server-agent")
				stopSubComponents(t, host)

				_, entries, err := RegisterTools(host, toolsCfg, newTestServices(t), Options{})
				if err != nil {
					t.Fatalf("RegisterTools: %v", err)
				}
				if len(entries) != len(toolsCfg) {
					t.Fatalf("entries = %d, want %d", len(entries), len(toolsCfg))
				}
				for _, e := range entries {
					id := SourceConnectorID(e.ToolMap)
					got := slices.Clone(e.ToolNames)
					slices.Sort(got)
					if !slices.Equal(got, want[id]) || e.Status != EntryStatusReady || e.Error != "" {
						t.Errorf("entry %q registered %v (status %q, error %q), want %v ready", id, got, e.Status, e.Error, want[id])
					}
				}
				if got := registeredToolNames(host); !slices.Equal(got, wantAll) {
					t.Fatalf("host tools = %v, want %v", got, wantAll)
				}
			}
		})
	}
}

func TestRegisterTools_MCPManifestEntriesRegisterFromTheirOwnManifest(t *testing.T) {
	toolsCfg := []any{
		mcpEntry(manifestOnlyServerURL, map[string]any{"source_connector_id": "narrow", "manifest": manifestOf("get_weather")}, nil),
		mcpEntry(manifestOnlyServerURL, map[string]any{"source_connector_id": "wide", "manifest": manifestOf("get_weather", "get_stock", "get_time")}, nil),
		mcpEntry(manifestOnlyServerURL, map[string]any{"source_connector_id": "prefixed", "tool_name_prefix": "p_", "manifest": manifestOf("get_stock")}, nil),
	}
	want := map[string][]string{
		"narrow":   {"get_weather"},
		"wide":     {"get_stock", "get_time"},
		"prefixed": {"p_get_stock"},
	}
	for range 10 {
		host := newTestHost("manifest-agent")
		stopSubComponents(t, host)
		_, entries, err := RegisterTools(host, toolsCfg, newTestServices(t), Options{})
		if err != nil {
			t.Fatalf("RegisterTools: %v", err)
		}
		for _, e := range entries {
			id := SourceConnectorID(e.ToolMap)
			got := slices.Clone(e.ToolNames)
			slices.Sort(got)
			if e.Status != EntryStatusReady || !slices.Equal(got, want[id]) {
				t.Errorf("entry %q = status %q, tools %v; want ready with %v", id, e.Status, got, want[id])
			}
		}
	}
}

func TestRegisterTools_MCPEntriesWithDifferentConnectionsAreNotRefused(t *testing.T) {
	capture := swapDefaultLogger(t)
	url := startSharedMCPServer(t, false)
	const secretA, secretB = "SECRET_HEADER_A", "SECRET_HEADER_B"
	build := func() []any {
		return []any{
			mcpEntry(url, map[string]any{"source_connector_id": "plain", "tool_name": "get_weather"}, nil),
			mcpEntry(url, map[string]any{"source_connector_id": "tenant-a", "tool_name": "get_stock"},
				map[string]any{"headers": map[string]any{"Authorization": "Bearer " + secretA}}),
			mcpEntry(url, map[string]any{"source_connector_id": "tenant-b", "tool_name": "get_time"},
				map[string]any{"headers": map[string]any{"Authorization": "Bearer " + secretB}}),
			mcpEntry(url, map[string]any{"source_connector_id": "tenant-a-again", "tool_name": "get_news"},
				map[string]any{"headers": map[string]any{"Authorization": "Bearer " + secretA}}),
		}
	}
	want := map[string][]string{
		"plain": {"get_weather"}, "tenant-a": {"get_stock"}, "tenant-b": {"get_time"}, "tenant-a-again": {"get_news"},
	}

	for range 10 {
		toolsCfg := build()
		slices.Reverse(toolsCfg)
		host := newTestHost("different-connections-agent")
		stopSubComponents(t, host)
		_, entries, err := RegisterTools(host, toolsCfg, newTestServices(t), Options{})
		if err != nil {
			t.Fatalf("RegisterTools: %v", err)
		}
		for _, e := range entries {
			id := SourceConnectorID(e.ToolMap)
			if e.Status != EntryStatusReady || e.Error != "" || !slices.Equal(e.ToolNames, want[id]) {
				t.Errorf("entry %q = status %q, error %q, tools %v; want ready with %v", id, e.Status, e.Error, e.ToolNames, want[id])
			}
		}
		if got := registeredToolNames(host); !slices.Equal(got, []string{"get_news", "get_stock", "get_time", "get_weather"}) {
			t.Fatalf("host tools = %v, want all four", got)
		}
	}

	capture.mu.Lock()
	defer capture.mu.Unlock()
	for _, rec := range capture.records {
		line := rec.msg
		for k, v := range rec.attrs {
			line += " " + k + "=" + v
		}
		for _, secret := range []string{secretA, secretB} {
			if strings.Contains(line, secret) {
				t.Errorf("log record %q carries %s", rec.msg, secret)
			}
		}
	}
}

func TestRegisterTools_MCPEntryMatchingNoToolsIsReportedLoudly(t *testing.T) {
	liveURL := startSharedMCPServer(t, false)
	tlsURL := startSharedMCPServer(t, true)

	cases := []struct {
		name       string
		url        string
		connExtras map[string]any
		manifest   []any
	}{
		{name: "live", url: liveURL},
		{name: "tls direct client", url: tlsURL, connExtras: map[string]any{"ssl_config": map[string]any{"verify": false}}},
		{name: "manifest", url: manifestOnlyServerURL, manifest: manifestOf(sharedServerToolNames...)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			capture := swapDefaultLogger(t)
			host := newTestHost("no-match-agent")
			stopSubComponents(t, host)

			withManifest := func(entry map[string]any) map[string]any {
				if tc.manifest != nil {
					entry["manifest"] = tc.manifest
				}
				return entry
			}
			_, entries, err := RegisterTools(host, []any{
				mcpEntry(tc.url, withManifest(map[string]any{"tool_name": "get_weather"}), tc.connExtras),
				mcpEntry(tc.url, withManifest(map[string]any{"tool_name": "get_wether", "source_connector_id": "conn-typo"}), tc.connExtras),
			}, newTestServices(t), Options{})
			if err != nil {
				t.Fatalf("RegisterTools: %v", err)
			}

			byConnector := map[string]EntryRecord{}
			for _, e := range entries {
				byConnector[SourceConnectorID(e.ToolMap)] = e
			}
			if typo := byConnector["conn-typo"]; len(typo.ToolNames) != 0 || !typo.Degraded() {
				t.Errorf("typo entry = names %v, degraded %v; want no names and degraded", typo.ToolNames, typo.Degraded())
			}
			if ok := byConnector[""]; !slices.Equal(ok.ToolNames, []string{"get_weather"}) || ok.Error != "" {
				t.Errorf("the matching entry = names %v, error %q; want [get_weather] and no note", ok.ToolNames, ok.Error)
			}

			rec, found := capture.find("MCP entry's filter matched none of its server's tools; the entry registered none")
			if !found {
				t.Fatal("no warning for the entry that matched nothing")
			}
			if rec.level.String() != "WARN" || rec.attrs["toolName"] != "get_wether" || rec.attrs["entriesOnServer"] != "2" {
				t.Errorf("warning = level %v, attrs %v; want WARN naming get_wether on a server of 2 entries", rec.level, rec.attrs)
			}
		})
	}
}

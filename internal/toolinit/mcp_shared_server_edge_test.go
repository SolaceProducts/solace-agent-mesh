// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package toolinit

import (
	"slices"
	"strings"
	"testing"
)

func TestRegisterTools_MCPEntryAfterAFailedFirstEntryStillRegisters(t *testing.T) {
	url := startSharedMCPServer(t, false)
	host := newTestHost("failed-first-agent")
	stopSubComponents(t, host)

	_, entries, err := RegisterTools(host, []any{
		mcpEntry(url, map[string]any{"source_connector_id": "broken", "tool_name": "get_weather"},
			map[string]any{"ssl_config": map[string]any{"ca_bundle": "/nonexistent/ca.pem"}}),
		mcpEntry(url, map[string]any{"source_connector_id": "next", "tool_name": "get_stock"}, nil),
	}, newTestServices(t), Options{})
	if err != nil {
		t.Fatalf("RegisterTools: %v", err)
	}

	byID := map[string]EntryRecord{}
	for _, e := range entries {
		byID[SourceConnectorID(e.ToolMap)] = e
	}
	if got := byID["broken"]; got.Status != EntryStatusFailed {
		t.Errorf("broken entry = %+v, want failed", got)
	}
	if got := byID["next"]; got.Status != EntryStatusReady || !slices.Equal(got.ToolNames, []string{"get_stock"}) {
		t.Errorf("entry after the failed one = %+v, want ready with get_stock", got)
	}
}

func TestRegisterTools_MCPFailureAndWarningCarryNoCredentials(t *testing.T) {
	capture := swapDefaultLogger(t)
	const secretQuery, secretHeader = "SECRET_QUERY_TOKEN", "SECRET_HEADER_TOKEN"
	url := startSharedMCPServer(t, false) + "/mcp?api_key=" + secretQuery
	host := newTestHost("canary-agent")
	stopSubComponents(t, host)

	_, entries, err := RegisterTools(host, []any{
		mcpEntry(url, map[string]any{"source_connector_id": "first", "tool_name": "get_weather"}, nil),
		mcpEntry(url, map[string]any{"source_connector_id": "typo", "tool_name": "get_wether"}, nil),
		mcpEntry(url, map[string]any{"source_connector_id": "authed", "tool_name": "get_stock"},
			map[string]any{"headers": map[string]any{"Authorization": "Bearer " + secretHeader}}),
		mcpEntry(url, map[string]any{"source_connector_id": "broken", "tool_name": "get_news"},
			map[string]any{"headers": map[string]any{"Authorization": "Bearer " + secretHeader}, "ssl_config": map[string]any{"ca_bundle": "/nonexistent/ca.pem"}}),
	}, newTestServices(t), Options{})
	if err != nil {
		t.Fatalf("RegisterTools: %v", err)
	}

	var authedReady, brokenFailed bool
	for _, e := range entries {
		if SourceConnectorID(e.ToolMap) == "authed" {
			authedReady = e.Status == EntryStatusReady
		}
		if SourceConnectorID(e.ToolMap) == "broken" {
			brokenFailed = e.Status == EntryStatusFailed
		}
		for _, secret := range []string{secretQuery, secretHeader} {
			if strings.Contains(e.Error, secret) {
				t.Errorf("registration error %q carries %s", e.Error, secret)
			}
		}
	}
	if !authedReady {
		t.Fatal("the entry with its own headers did not register, so the path that carries its header value was not exercised")
	}
	if !brokenFailed {
		t.Fatal("the entry with an unreadable CA bundle did not fail, so the connection-failure path was not exercised")
	}
	if _, warned := capture.find("MCP entry's filter matched none of its server's tools; the entry registered none"); !warned {
		t.Fatal("the no-match warning was not logged, so that path was not exercised")
	}

	capture.mu.Lock()
	defer capture.mu.Unlock()
	for _, rec := range capture.records {
		line := rec.msg
		for k, v := range rec.attrs {
			line += " " + k + "=" + v
		}
		for _, secret := range []string{secretQuery, secretHeader} {
			if strings.Contains(line, secret) {
				t.Errorf("log record %q carries %s", rec.msg, secret)
			}
		}
	}
}

func TestRegisterTools_MCPFailedEntryWithoutAToolNameIsIdentifiedByIndex(t *testing.T) {
	capture := swapDefaultLogger(t)
	url := startSharedMCPServer(t, false)
	host := newTestHost("index-agent")
	stopSubComponents(t, host)

	_, _, err := RegisterTools(host, []any{
		map[string]any{"tool_type": "builtin", "tool_name": "get_current_time"},
		mcpEntry(url, map[string]any{"allow_list": []any{"get_weather"}}, nil),
		mcpEntry(url, map[string]any{"allow_list": []any{"get_stock"}},
			map[string]any{"ssl_config": map[string]any{"ca_bundle": "/nonexistent/ca.pem"}}),
		mcpEntry(url, map[string]any{"deny_list": []any{"get_weather", "get_stock"}}, nil),
	}, newTestServices(t), Options{})
	if err != nil {
		t.Fatalf("RegisterTools: %v", err)
	}

	rec, found := capture.find("mcp client connection failed; toolset tools not registered")
	if !found {
		t.Fatal("the entry with an unreadable CA bundle did not fail")
	}
	if rec.attrs["entry"] != "mcp" || rec.attrs["index"] != "2" {
		t.Errorf("failure log entry=%q index=%q; want entry=mcp (no tool_name) and index=2, the failed entry's position in the tools list",
			rec.attrs["entry"], rec.attrs["index"])
	}
}

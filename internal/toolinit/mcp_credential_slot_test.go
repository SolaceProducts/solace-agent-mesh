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

func oauthMCPEntry(connectorID string, scopes []any, manifestTool string, connExtras map[string]any) map[string]any {
	return mcpEntry(manifestOnlyServerURL, map[string]any{
		"source_connector_id": connectorID,
		"manifest":            manifestOf(manifestTool),
		"auth": map[string]any{
			"type": "oauth2",
			"scheme": map[string]any{
				"authorization_url": "https://idp.example.com/authorize",
				"token_url":         "https://idp.example.com/token",
				"scopes":            scopes,
			},
			"credential": map[string]any{"client_id": "client-1", "client_secret": "SECRET_CLIENT_VALUE"},
		},
	}, connExtras)
}

func TestRegisterTools_MCPEntriesInOneCredentialSlotMustAgreeOnAuth(t *testing.T) {
	capture := swapDefaultLogger(t)
	read, readWrite := []any{"read"}, []any{"read", "write"}

	for range 10 {
		host := newTestHost("credential-slot-agent")
		stopSubComponents(t, host)
		_, entries, err := RegisterTools(host, []any{
			oauthMCPEntry("first", read, "get_weather", nil),
			oauthMCPEntry("other-header", read, "get_stock", map[string]any{"headers": map[string]any{"X-Tenant": "b"}}),
			oauthMCPEntry("other-scopes", readWrite, "get_time", nil),
		}, newTestServices(t), Options{})
		if err != nil {
			t.Fatalf("RegisterTools: %v", err)
		}

		byID := map[string]EntryRecord{}
		for _, e := range entries {
			byID[SourceConnectorID(e.ToolMap)] = e
		}
		for id, wantTools := range map[string][]string{"first": {"get_weather"}, "other-header": {"get_stock"}} {
			if got := byID[id]; got.Status != EntryStatusReady || !slices.Equal(got.ToolNames, wantTools) {
				t.Fatalf("entry %q = %+v, want ready with %v", id, got, wantTools)
			}
		}
		refused := byID["other-scopes"]
		if refused.Status != EntryStatusFailed || len(refused.ToolNames) != 0 {
			t.Fatalf("entry with different scopes = %+v, want failed with no tools", refused)
		}
		for _, want := range []string{"credential_key", "auth.scheme"} {
			if !strings.Contains(refused.Error, want) {
				t.Errorf("failure summary %q should mention %q", refused.Error, want)
			}
		}
		if strings.Contains(refused.Error, "SECRET_CLIENT_VALUE") {
			t.Errorf("failure summary %q carries a credential", refused.Error)
		}
	}

	rec, found := capture.find("mcp entry conflicts with an earlier entry for its server; toolset tools not registered")
	if !found || rec.level.String() != "ERROR" || rec.attrs["connectorId"] != "other-scopes" {
		t.Errorf("conflict log = %+v (found %v), want an ERROR naming connector other-scopes", rec, found)
	}
	capture.mu.Lock()
	defer capture.mu.Unlock()
	for _, r := range capture.records {
		line := r.msg
		for k, v := range r.attrs {
			line += " " + k + "=" + v
		}
		if strings.Contains(line, "SECRET_CLIENT_VALUE") {
			t.Errorf("log record %q carries a credential", r.msg)
		}
	}
}

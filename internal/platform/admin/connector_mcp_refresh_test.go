// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

var (
	storedV1Tools = []string{"get_weather: Weather v1"}
	storedV2Tools = []string{"get_forecast: Forecast v2", "get_weather: Weather v2"}
)

func TestUpdateWithRedeploy_LeavesOutToolsWithOverlongNamesAndSaysSo(t *testing.T) {
	svc, db, server, connID := newMCPConnectorForUpdateTest(t)
	server.serve("long")

	resp, err := svc.UpdateWithRedeploy(t.Context(), connID, UpdateConnectorRequest{Values: mcpConnectorValues(server.url, nil)}, "u1", ConnectorUpdateOptions{})
	if err != nil {
		t.Fatalf("UpdateWithRedeploy: %v", err)
	}

	if got, want := storedManifestTools(t, db, connID), []string{"get_weather: Weather long"}; !slices.Equal(got, want) {
		t.Errorf("stored manifest = %v, want only the short tool %v", got, want)
	}
	want := []string{overlongToolNamesWarning([]string{overlongMCPToolName})}
	if !slices.Equal(resp.Warnings, want) {
		t.Errorf("warnings = %q, want %q", resp.Warnings, want)
	}
}

func TestUpdateWithRedeploy_KeepsTheStoredToolsWhenEveryDiscoveredNameIsOverlong(t *testing.T) {
	svc, db, server, connID := newMCPConnectorForUpdateTest(t)
	server.serve("all-long")

	resp, err := svc.UpdateWithRedeploy(t.Context(), connID, UpdateConnectorRequest{Values: mcpConnectorValues(server.url, nil)}, "u1", ConnectorUpdateOptions{})
	if err != nil {
		t.Fatalf("UpdateWithRedeploy: %v", err)
	}

	if got := storedManifestTools(t, db, connID); !slices.Equal(got, storedV1Tools) {
		t.Errorf("stored manifest = %v, want the stored %v kept", got, storedV1Tools)
	}
	want := []string{everyToolNameOverlongWarning([]string{overlongMCPToolName})}
	if !slices.Equal(resp.Warnings, want) {
		t.Errorf("warnings = %q, want %q", resp.Warnings, want)
	}
}

func TestConnectorService_Update_RefreshesTheMCPToolListFromTheServer(t *testing.T) {
	svc, db, server, connID := newMCPConnectorForUpdateTest(t)
	server.serve("v2")

	resp, err := svc.Update(t.Context(), connID, UpdateConnectorRequest{Values: mcpConnectorValues(server.url, nil)}, "u1")
	if err != nil {
		t.Fatalf("Update: %v", err)
	}

	if got := storedManifestTools(t, db, connID); !slices.Equal(got, storedV2Tools) {
		t.Errorf("stored manifest = %v, want %v", got, storedV2Tools)
	}
	if len(resp.Warnings) != 0 {
		t.Errorf("warnings = %q, want none", resp.Warnings)
	}
}

var keptWhileTheServerIsDown = mcpToolListKeptBecause + mcpStatusSentence("500")

func TestConnectorService_Update_WarnsThatTheStoredToolListWasKeptWhenTheServerIsDown(t *testing.T) {
	svc, db, server, connID := newMCPConnectorForUpdateTest(t)
	server.serve("down")

	resp, err := svc.Update(t.Context(), connID, UpdateConnectorRequest{Values: mcpConnectorValues(server.url, nil)}, "u1")
	if err != nil {
		t.Fatalf("Update: %v", err)
	}

	if got := storedManifestTools(t, db, connID); !slices.Equal(got, storedV1Tools) {
		t.Errorf("stored manifest = %v, want the stored %v kept", got, storedV1Tools)
	}
	if want := []string{keptWhileTheServerIsDown}; !slices.Equal(resp.Warnings, want) {
		t.Errorf("warnings = %q, want %q", resp.Warnings, want)
	}
}

func TestUpdateWithRedeploy_DiscoversWithTheStoredCredentialOfAnAuthenticatedMCPServer(t *testing.T) {
	const token = "bearer-token-v1"
	tests := []struct {
		name         string
		auth         map[string]any
		wantTools    []string
		wantWarnings []string
	}{
		{
			name:      "the token omitted from the request",
			auth:      map[string]any{"auth_type": "http", "auth_http_scheme": "bearer"},
			wantTools: storedV2Tools,
		},
		{
			name:      "the token sent as the redaction sentinel",
			auth:      bearerAuthValues(redactedSentinel),
			wantTools: storedV2Tools,
		},
		{
			name:         "a token the server rejects",
			auth:         bearerAuthValues("wrong-token-xyz"),
			wantTools:    storedV1Tools,
			wantWarnings: []string{mcpToolListKeptBecause + msgMCPAuthRequired},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc, db, server, connID := newMCPConnectorWithBearerForUpdateTest(t, token)
			server.serve("v2")

			resp, err := svc.UpdateWithRedeploy(t.Context(), connID, UpdateConnectorRequest{Values: mcpConnectorValues(server.url, tc.auth)}, "u1", ConnectorUpdateOptions{})
			if err != nil {
				t.Fatalf("UpdateWithRedeploy: %v", err)
			}

			if got := storedManifestTools(t, db, connID); !slices.Equal(got, tc.wantTools) {
				t.Errorf("stored manifest = %v, want %v", got, tc.wantTools)
			}
			if !slices.Equal(resp.Warnings, tc.wantWarnings) {
				t.Errorf("warnings = %q, want %q", resp.Warnings, tc.wantWarnings)
			}
		})
	}
}

func TestUpdateWithRedeploy_KeepsTheStoredToolsWhenTheConnectorSwitchesToOAuth(t *testing.T) {
	svc, db, server, connID := newMCPConnectorForUpdateTest(t)
	server.serve("v2")
	oauth := map[string]any{
		"auth_type":                    "oauth",
		"auth_oauth_mode":              "manual",
		"auth_oauth_authorization_url": "https://idp.example.com/authorize",
		"auth_oauth_token_url":         "https://idp.example.com/token",
		"auth_oauth_client_id":         "client-1",
	}

	resp, err := svc.UpdateWithRedeploy(t.Context(), connID, UpdateConnectorRequest{Values: mcpConnectorValues(server.url, oauth)}, "u1", ConnectorUpdateOptions{})
	if err != nil {
		t.Fatalf("UpdateWithRedeploy: %v", err)
	}

	if got := storedManifestTools(t, db, connID); !slices.Equal(got, storedV1Tools) {
		t.Errorf("stored manifest = %v, want the stored %v kept", got, storedV1Tools)
	}
	if len(resp.Warnings) != 0 {
		t.Errorf("warnings = %q, want none", resp.Warnings)
	}
}

func TestValidateUpdateReading_ValidatesAgainstTheToolsTheMCPServerListsNowAndWritesNothing(t *testing.T) {
	svc, db, server, connID := newMCPConnectorForUpdateTest(t)
	server.serve("v2")
	selectForecast := UpdateConnectorRequest{Values: mcpConnectorValues(server.url, map[string]any{"selected_tools": "get_forecast"})}

	warnings, read, err := svc.ValidateUpdateReading(t.Context(), connID, selectForecast)
	if err != nil {
		t.Fatalf("ValidateUpdateReading selecting a tool only v2 lists: %v", err)
	}

	if len(warnings) != 0 {
		t.Errorf("warnings = %q, want none", warnings)
	}
	if len(read.MCPManifest) != 2 {
		t.Errorf("read manifest has %d tools, want the 2 v2 lists", len(read.MCPManifest))
	}
	if got := storedManifestTools(t, db, connID); !slices.Equal(got, storedV1Tools) {
		t.Errorf("stored manifest = %v, want ValidateUpdateReading to write nothing (%v)", got, storedV1Tools)
	}
}

func TestValidateUpdateReading_RefusesASelectedToolTheMCPServerNoLongerLists(t *testing.T) {
	svc, db, server, connID := newMCPConnectorForUpdateTest(t)
	selectForecast := func() UpdateConnectorRequest {
		return UpdateConnectorRequest{Values: mcpConnectorValues(server.url, map[string]any{"selected_tools": "get_forecast"})}
	}
	server.serve("v2")
	if _, err := svc.UpdateWithRedeploy(t.Context(), connID, selectForecast(), "u1", ConnectorUpdateOptions{}); err != nil {
		t.Fatalf("select get_forecast on v2: %v", err)
	}
	server.serve("v1")

	_, _, err := svc.ValidateUpdateReading(t.Context(), connID, selectForecast())

	verr, ok := errors.AsType[*ValidationError](err)
	if !ok {
		t.Fatalf("ValidateUpdateReading error = %T (%v), want *ValidationError", err, err)
	}
	if !strings.Contains(verr.Message, "get_forecast") {
		t.Errorf("message = %q, want it to name the missing get_forecast", verr.Message)
	}
	if got := storedManifestTools(t, db, connID); !slices.Equal(got, storedV2Tools) {
		t.Errorf("stored manifest = %v, want ValidateUpdateReading to write nothing (%v)", got, storedV2Tools)
	}
}

func TestValidateUpdateReading_ReportsThatTheStoredToolListWillBeKept(t *testing.T) {
	svc, _, server, connID := newMCPConnectorForUpdateTest(t)
	server.serve("down")

	warnings, read, err := svc.ValidateUpdateReading(t.Context(), connID, UpdateConnectorRequest{Values: mcpConnectorValues(server.url, nil)})
	if err != nil {
		t.Fatalf("ValidateUpdateReading: %v", err)
	}

	want := []string{keptWhileTheServerIsDown}
	if !slices.Equal(warnings, want) || !slices.Equal(read.Warnings, want) {
		t.Errorf("warnings = %q, read warnings = %q, want both %q", warnings, read.Warnings, want)
	}
	if read.MCPManifest != nil {
		t.Errorf("read manifest = %v, want nil so the write keeps the stored one", read.MCPManifest)
	}
}

func TestUpdateConnectorWithRedeploy_StoresTheToolListThePrePassRead(t *testing.T) {
	svc, db, server, connID := newMCPConnectorForUpdateTest(t)
	server.serve("v2")
	req := UpdateConnectorRequest{Values: mcpConnectorValues(server.url, nil)}
	_, read, err := svc.ValidateUpdateReading(t.Context(), connID, req)
	if err != nil {
		t.Fatalf("ValidateUpdateReading: %v", err)
	}
	server.serve("v1")

	if _, err = UpdateConnectorWithRedeploy(t.Context(), db, svc, ConnectorRedeployUpdate{ID: connID, Request: req, UserID: "u1", Read: &read}, discardLogger()); err != nil {
		t.Fatalf("UpdateConnectorWithRedeploy: %v", err)
	}

	if got := storedManifestTools(t, db, connID); !slices.Equal(got, storedV2Tools) {
		t.Errorf("stored manifest = %v, want the pre-pass's %v, not a second discovery", got, storedV2Tools)
	}
}

func TestUpdateConnectorWithRedeploy_ReportsTheKeptToolListWarning(t *testing.T) {
	tests := []struct {
		name        string
		carriedRead bool
	}{
		{name: "from the pre-pass read it carries", carriedRead: true},
		{name: "from the discovery it makes itself", carriedRead: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc, db, server, connID := newMCPConnectorForUpdateTest(t)
			server.serve("down")
			req := UpdateConnectorRequest{Values: mcpConnectorValues(server.url, nil)}
			update := ConnectorRedeployUpdate{ID: connID, Request: req, UserID: "u1"}
			if tc.carriedRead {
				_, read, err := svc.ValidateUpdateReading(t.Context(), connID, req)
				if err != nil {
					t.Fatalf("ValidateUpdateReading: %v", err)
				}
				update.Read = &read
			}

			resp, err := UpdateConnectorWithRedeploy(t.Context(), db, svc, update, discardLogger())
			if err != nil {
				t.Fatalf("UpdateConnectorWithRedeploy: %v", err)
			}

			if !slices.Contains(resp.Warnings, keptWhileTheServerIsDown) {
				t.Errorf("warnings = %q, want them to include the kept-tool-list warning", resp.Warnings)
			}
			if got := storedManifestTools(t, db, connID); !slices.Equal(got, storedV1Tools) {
				t.Errorf("stored manifest = %v, want the stored %v kept", got, storedV1Tools)
			}
		})
	}
}

func TestConnectorService_Update_OfANonMCPConnectorNeverAsksAnMCPServerForTools(t *testing.T) {
	svc, db, _, _ := newRedeployServiceForTest(t)
	svc.SetMCPDiscoveryService(NewMCPDiscoveryService(discardLogger(), nil))
	connID, _ := seedSQLConnectorBoundToAgent(t, svc, db)

	resp, err := svc.Update(t.Context(), connID, UpdateConnectorRequest{Values: map[string]any{
		"hostname": "localhost",
		"port":     "5432",
		"database": "testdb",
		"username": "testuser",
		"password": redactedSentinel,
	}}, "u1")
	if err != nil {
		t.Fatalf("Update: %v", err)
	}

	for _, w := range resp.Warnings {
		if strings.Contains(w, "MCP") {
			t.Errorf("warnings = %q, want none about an MCP server", resp.Warnings)
		}
	}
}

func TestUpdateWithRedeploy_WarnsThatAnEmptyManifestClearsTheToolListOfAnOAuthConnector(t *testing.T) {
	svc, db, server, connID := newMCPConnectorForUpdateTest(t)
	oauth := map[string]any{
		"auth_type":                    "oauth",
		"auth_oauth_mode":              "manual",
		"auth_oauth_authorization_url": "https://idp.example.com/authorize",
		"auth_oauth_token_url":         "https://idp.example.com/token",
		"auth_oauth_client_id":         "client-1",
		"manifest":                     []any{},
	}

	resp, err := svc.UpdateWithRedeploy(t.Context(), connID, UpdateConnectorRequest{Values: mcpConnectorValues(server.url, oauth)}, "u1", ConnectorUpdateOptions{})
	if err != nil {
		t.Fatalf("UpdateWithRedeploy: %v", err)
	}

	if got := storedManifestTools(t, db, connID); len(got) != 0 {
		t.Errorf("stored manifest = %v, want the empty manifest the update sent", got)
	}
	if want := []string{mcpToolListEmptiedWarning}; !slices.Equal(resp.Warnings, want) {
		t.Errorf("warnings = %q, want %q", resp.Warnings, want)
	}
}

func TestUpdateWithRedeploy_SaysNothingKeptWhenNoToolListIsStored(t *testing.T) {
	svc, db, server, connID := newMCPConnectorCreatedWhileTheServerIsDown(t)

	resp, err := svc.UpdateWithRedeploy(t.Context(), connID, UpdateConnectorRequest{Values: mcpConnectorValues(server.url, nil)}, "u1", ConnectorUpdateOptions{})
	if err != nil {
		t.Fatalf("UpdateWithRedeploy: %v", err)
	}

	if got := storedManifestTools(t, db, connID); len(got) != 0 {
		t.Errorf("stored manifest = %v, want none", got)
	}
	if slices.Contains(resp.Warnings, mcpToolListKeptWarning) {
		t.Errorf("warnings = %q, want no warning that a tool list was kept", resp.Warnings)
	}
	if len(resp.Warnings) != 1 || !strings.HasPrefix(resp.Warnings[0], warningSavedWithoutTools) {
		t.Errorf("warnings = %q, want the single %q warning", resp.Warnings, warningSavedWithoutTools)
	}
}

func TestUpdateWithRedeploy_RefusesSelectingAnOverlongToolWithTheUnselectRemedy(t *testing.T) {
	svc, db, server, connID := newMCPConnectorForUpdateTest(t)
	server.serve("long")

	_, err := svc.UpdateWithRedeploy(t.Context(), connID, UpdateConnectorRequest{
		Values: mcpConnectorValues(server.url, map[string]any{"selected_tools": overlongMCPToolName}),
	}, "u1", ConnectorUpdateOptions{})

	verr, ok := errors.AsType[*ValidationError](err)
	if !ok {
		t.Fatalf("UpdateWithRedeploy error = %T (%v), want *ValidationError", err, err)
	}
	if !strings.Contains(verr.Message, "Unselect") {
		t.Errorf("message = %q, want the Unselect remedy", verr.Message)
	}
	if got := storedManifestTools(t, db, connID); !slices.Equal(got, storedV1Tools) {
		t.Errorf("stored manifest = %v, want the refused update to leave %v", got, storedV1Tools)
	}
}

func TestShownToolNames_BoundsTheServerSuppliedNames(t *testing.T) {
	long := strings.Repeat("y", overlongNameRunesMax+20)
	names := []string{long, "b", "c", "d", "e", "f", "g"}

	got := shownToolNames(names)

	want := strings.Repeat("y", overlongNameRunesMax) + "…, b, c, d, e, and 2 more"
	if got != want {
		t.Errorf("shownToolNames = %q, want %q", got, want)
	}
}

func TestUpdateWithRedeploy_SaysNothingWhenAnEmptyManifestMeetsAnOAuthConnectorWithNoStoredList(t *testing.T) {
	svc, db, server, connID := newMCPConnectorCreatedWhileTheServerIsDown(t)
	oauth := map[string]any{
		"auth_type":                    "oauth",
		"auth_oauth_mode":              "manual",
		"auth_oauth_authorization_url": "https://idp.example.com/authorize",
		"auth_oauth_token_url":         "https://idp.example.com/token",
		"auth_oauth_client_id":         "client-1",
		"manifest":                     []any{},
	}

	resp, err := svc.UpdateWithRedeploy(t.Context(), connID, UpdateConnectorRequest{Values: mcpConnectorValues(server.url, oauth)}, "u1", ConnectorUpdateOptions{})
	if err != nil {
		t.Fatalf("UpdateWithRedeploy: %v", err)
	}

	if got := storedManifestTools(t, db, connID); len(got) != 0 {
		t.Errorf("stored manifest = %v, want none", got)
	}
	if len(resp.Warnings) != 0 {
		t.Errorf("warnings = %q, want none", resp.Warnings)
	}
}

func TestUpdateWithRedeploy_NamesTheOverlongToolsWhenNoListIsStored(t *testing.T) {
	svc, db, server, connID := newMCPConnectorCreatedWhileTheServerIsDown(t)
	server.serve("all-long")

	resp, err := svc.UpdateWithRedeploy(t.Context(), connID, UpdateConnectorRequest{Values: mcpConnectorValues(server.url, nil)}, "u1", ConnectorUpdateOptions{})
	if err != nil {
		t.Fatalf("UpdateWithRedeploy: %v", err)
	}

	if got := storedManifestTools(t, db, connID); len(got) != 0 {
		t.Errorf("stored manifest = %v, want none", got)
	}
	if want := []string{overlongToolNamesWarning([]string{overlongMCPToolName})}; !slices.Equal(resp.Warnings, want) {
		t.Errorf("warnings = %q, want %q", resp.Warnings, want)
	}
}

func TestShownToolNames_EdgesOfTheBounds(t *testing.T) {
	tests := []struct {
		name  string
		names []string
		want  string
	}{
		{
			name:  "a name of exactly the rune limit is shown whole",
			names: []string{strings.Repeat("z", overlongNameRunesMax)},
			want:  strings.Repeat("z", overlongNameRunesMax),
		},
		{
			name:  "a multi-byte name is cut by runes, not bytes",
			names: []string{strings.Repeat("é", overlongNameRunesMax+1)},
			want:  strings.Repeat("é", overlongNameRunesMax) + "…",
		},
		{
			name:  "exactly the shown count of names has no remainder",
			names: []string{"a", "b", "c", "d", "e"},
			want:  "a, b, c, d, e",
		},
		{
			name:  "control characters are stripped",
			names: []string{"bad\nname\x1b[31m"},
			want:  "badname[31m",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := shownToolNames(tc.names); got != tc.want {
				t.Errorf("shownToolNames = %q, want %q", got, tc.want)
			}
		})
	}
}

func newMCPConnectorCreatedWhileTheServerIsDown(t *testing.T) (*ConnectorService, *store.DB, *versionedMCPServer, string) {
	t.Helper()
	t.Setenv(allowPrivateMCPEnvVar, "true")
	svc, db, _, _ := newRedeployServiceForTest(t)
	svc.SetMCPDiscoveryService(NewMCPDiscoveryService(discardLogger(), nil))
	server := newVersionedMCPServer(t, "")
	server.serve("down")
	conn, err := svc.Create(t.Context(), CreateConnectorRequest{
		Name:        "unlisted-mcp",
		Description: "An MCP server that was down when the connector was created",
		Type:        "mcp",
		Subtype:     "remote",
		Values:      mcpConnectorValues(server.url, nil),
	}, "u1")
	if err != nil {
		t.Fatalf("create connector: %v", err)
	}
	return svc, db, server, conn.ID
}

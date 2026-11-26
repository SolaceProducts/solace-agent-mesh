// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

const (
	namedConnectorID   = "5d1c7a42-8b3e-4f60-9a17-3c2e6b9d0f84"
	namedConnectorName = "Alpha CRM"
)

func seedOpenAPIOAuthConnector(t *testing.T, repo *store.ConnectorRepo, values map[string]any) {
	t.Helper()
	raw, err := json.Marshal(values)
	if err != nil {
		t.Fatalf("marshal connector values: %v", err)
	}
	stored := string(raw)
	subtype := "openapi"
	err = repo.Create(t.Context(), &store.Connector{
		ID:        namedConnectorID,
		Name:      namedConnectorName,
		Type:      "api",
		Subtype:   &subtype,
		Values:    &stored,
		CreatedBy: "alice",
		UpdatedBy: "alice",
	})
	if err != nil {
		t.Fatalf("seed connector: %v", err)
	}
}

func wantNamedConnectorConfig() ConnectorConfig {
	return ConnectorConfig{
		ID:      namedConnectorID,
		Name:    namedConnectorName,
		Type:    "api",
		Subtype: "openapi",
		Values: map[string]any{
			"specification_url":                     "https://api.example.com/openapi.json",
			"base_url":                              "https://api.example.com",
			"auth_type":                             "oauth",
			"auth_oauth_authorization_url":          "https://idp.example.com/authorize",
			"auth_oauth_token_url":                  "https://idp.example.com/token",
			"auth_oauth_client_id":                  "orders",
			"auth_oauth_token_endpoint_auth_method": "client_secret_basic",
		},
	}
}

func oauthAuthBlock(t *testing.T, conn ConnectorConfig) map[string]any {
	t.Helper()
	auth, ok := buildConnectorTool(conn)["auth"].(map[string]any)
	if !ok {
		t.Fatalf("buildConnectorTool(%q)[\"auth\"] is not a map", conn.Name)
	}
	return auth
}

func TestResolveConnectorValues_CarriesTheConnectorNameIntoTheAuthBlock(t *testing.T) {
	repo := store.NewConnectorRepo(testOpenDB(t))
	seedOpenAPIOAuthConnector(t, repo, openAPIOAuthValues("client_secret_basic", ""))

	got, err := resolveConnectorValues(t.Context(), []string{namedConnectorID}, repo, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("resolveConnectorValues: %v", err)
	}

	if diff := cmp.Diff([]ConnectorConfig{wantNamedConnectorConfig()}, got); diff != "" {
		t.Fatalf("resolveConnectorValues mismatch (-want +got):\n%s", diff)
	}
	if name := oauthAuthBlock(t, got[0])["display_name"]; name != "Alpha CRM" {
		t.Errorf("auth display_name = %v, want Alpha CRM", name)
	}
}

func TestConnectorConfigFromRow_CarriesTheConnectorNameIntoTheAuthBlock(t *testing.T) {
	repo := store.NewConnectorRepo(testOpenDB(t))
	seedOpenAPIOAuthConnector(t, repo, openAPIOAuthValues("client_secret_basic", ""))
	row, err := repo.GetByID(t.Context(), namedConnectorID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}

	got, err := connectorConfigFromRow(row)
	if err != nil {
		t.Fatalf("connectorConfigFromRow: %v", err)
	}

	if diff := cmp.Diff(wantNamedConnectorConfig(), got); diff != "" {
		t.Fatalf("connectorConfigFromRow mismatch (-want +got):\n%s", diff)
	}
	if name := oauthAuthBlock(t, got)["display_name"]; name != "Alpha CRM" {
		t.Errorf("auth display_name = %v, want Alpha CRM", name)
	}
}

func TestConnectorUpdate_PKCESettingAfterTheMigrationPinnedItDisabled(t *testing.T) {
	tests := []struct {
		name          string
		update        map[string]any
		wantStoredPKC any
		wantUsePKCE   any
	}{
		{
			name:          "an update that sends the stored PKCE setting back keeps PKCE off",
			update:        openAPIOAuthValues("client_secret_basic", "disabled"),
			wantStoredPKC: "disabled",
			wantUsePKCE:   nil,
		},
		{
			name:          "an update that leaves the PKCE setting out turns PKCE on",
			update:        openAPIOAuthValues("client_secret_basic", ""),
			wantStoredPKC: nil,
			wantUsePKCE:   true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			repo := store.NewConnectorRepo(testOpenDB(t))
			seedOpenAPIOAuthConnector(t, repo, openAPIOAuthValues("client_secret_basic", "disabled"))
			svc := NewConnectorService(repo, nil, "test-ns", slog.New(slog.DiscardHandler))

			if _, err := svc.Update(t.Context(), namedConnectorID, UpdateConnectorRequest{Values: tc.update}, "alice"); err != nil {
				t.Fatalf("Update: %v", err)
			}

			row, err := repo.GetByID(t.Context(), namedConnectorID)
			if err != nil {
				t.Fatalf("GetByID: %v", err)
			}
			conn, err := connectorConfigFromRow(row)
			if err != nil {
				t.Fatalf("connectorConfigFromRow: %v", err)
			}
			if diff := cmp.Diff(tc.wantStoredPKC, conn.Values["auth_oauth_pkce"]); diff != "" {
				t.Errorf("stored auth_oauth_pkce mismatch (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tc.wantUsePKCE, oauthAuthBlock(t, conn)["use_pkce"]); diff != "" {
				t.Errorf("generated auth use_pkce mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

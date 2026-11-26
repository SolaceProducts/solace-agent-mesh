// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"crypto/tls"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func TestConnectorService_ASaveThatListsNoTools_StillSaysItCannotReadTheTLSFiles(t *testing.T) {
	t.Setenv(allowPrivateMCPEnvVar, "true")
	pki := newMTLSFixture(t)
	pki.useAsTLSDir(t)
	missing := filepath.Join(pki.dir, "not-mounted.key")
	withMissingKey := func(extra map[string]any) map[string]any {
		values := saveTimeMCPValues("https://mcp.example.com/mcp", pki.caBundle)
		values["ssl_config"] = map[string]any{"ca_bundle": pki.caBundle, "client_cert": pki.clientCert, "client_key": missing}
		maps.Copy(values, extra)
		return values
	}
	saves := map[string]map[string]any{
		"an OAuth connector, which a save cannot list": {
			"auth_type": "oauth", "auth_oauth_mode": "manual",
			"auth_oauth_authorization_url": "https://auth.example.com/authorize",
			"auth_oauth_token_url":         "https://auth.example.com/token",
			"auth_oauth_client_id":         "client-id",
		},
		"a save that carries its own tool list": {
			"manifest": []any{map[string]any{"name": "echo", "description": "Echoes its input", "inputSchema": map[string]any{"type": "object"}}},
		},
	}
	for name, extra := range saves {
		t.Run(name, func(t *testing.T) {
			svc := newConnectorServiceWithDiscovery(t)
			created, err := svc.Create(t.Context(), CreateConnectorRequest{
				Name: "orders-mcp", Type: "mcp", Subtype: "remote", Values: withMissingKey(extra),
			}, "user-1")
			if err != nil {
				t.Fatalf("Create: %v", err)
			}
			expectUnreadableTLSFileWarning(t, "Create", created.Warnings, missing)

			updated, err := svc.UpdateWithRedeploy(t.Context(), created.ID, UpdateConnectorRequest{Values: withMissingKey(extra)}, "user-1", ConnectorUpdateOptions{})
			if err != nil {
				t.Fatalf("UpdateWithRedeploy: %v", err)
			}
			expectUnreadableTLSFileWarning(t, "UpdateWithRedeploy", updated.Warnings, missing)
		})
	}
}

func expectUnreadableTLSFileWarning(t *testing.T, save string, warnings []string, path string) {
	t.Helper()
	names := func(w string) bool {
		return strings.HasPrefix(w, warningTLSFilesUnreadable) && strings.Contains(w, path)
	}
	if !slices.ContainsFunc(warnings, names) {
		t.Errorf("%s warnings = %q, want one saying the platform cannot read %s", save, warnings, path)
	}
}

func TestConnectorService_AnUpdateWithoutValuesListsTheToolsOfAConnectorSavedWithoutThem(t *testing.T) {
	t.Setenv(allowPrivateMCPEnvVar, "true")
	pki := newMTLSFixture(t)
	pki.useAsTLSDir(t)
	server := startTLSMCPServer(t, pki, true)
	updates := map[string]func(svc *ConnectorService, id string, req UpdateConnectorRequest) (*ConnectorResponse, error){
		"Update": func(svc *ConnectorService, id string, req UpdateConnectorRequest) (*ConnectorResponse, error) {
			return svc.Update(t.Context(), id, req, "user-1")
		},
		"UpdateWithRedeploy": func(svc *ConnectorService, id string, req UpdateConnectorRequest) (*ConnectorResponse, error) {
			return svc.UpdateWithRedeploy(t.Context(), id, req, "user-1", ConnectorUpdateOptions{})
		},
	}
	for name, update := range updates {
		t.Run(name, func(t *testing.T) {
			cert, key := filepath.Join(pki.dir, name+".crt"), filepath.Join(pki.dir, name+".key")
			values := saveTimeMCPValues(server.URL, pki.caBundle)
			values["ssl_config"] = map[string]any{"ca_bundle": pki.caBundle, "client_cert": cert, "client_key": key}
			svc := newConnectorServiceWithDiscovery(t)
			created, err := svc.Create(t.Context(), CreateConnectorRequest{
				Name: "orders-mcp", Type: "mcp", Subtype: "remote", Values: values,
			}, "user-1")
			if err != nil {
				t.Fatalf("Create before the files are mounted: %v", err)
			}
			if len(created.Warnings) != 1 || !strings.HasPrefix(created.Warnings[0], warningSavedWithoutTools) || !strings.Contains(created.Warnings[0], cert) {
				t.Fatalf("Create warnings = %q, want it saved without tools, naming the unreadable %s", created.Warnings, cert)
			}
			mountTestFile(t, pki.clientCert, cert)
			mountTestFile(t, pki.clientKey, key)

			resp, err := update(svc, created.ID, UpdateConnectorRequest{Description: "changed once the files were mounted"})

			if err != nil {
				t.Fatalf("%s with only a description: %v", name, err)
			}
			if len(resp.Warnings) != 0 {
				t.Errorf("Warnings = %q, want none once the server lists its tools", resp.Warnings)
			}
			stored := storedConnectorValues(t, svc, created.ID)
			if !hasNonEmptyManifest(stored["manifest"]) {
				t.Errorf("stored values = %v, want the server's tools in manifest", stored)
			}
			if ssl, _ := stored["ssl_config"].(map[string]any); ssl["client_cert"] != cert {
				t.Errorf("stored ssl_config = %v, want it kept", stored["ssl_config"])
			}
		})
	}
}

func TestConnectorService_AnUpdateNamingAMissingKeyFile_SaysWhyTheToolListWasKept(t *testing.T) {
	t.Setenv(allowPrivateMCPEnvVar, "true")
	pki := newMTLSFixture(t)
	pki.useAsTLSDir(t)
	server := startTLSMCPServer(t, pki, true)
	svc := newConnectorServiceWithDiscovery(t)
	withKey := func(key string) map[string]any {
		values := saveTimeMCPValues(server.URL, pki.caBundle)
		values["ssl_config"] = map[string]any{"ca_bundle": pki.caBundle, "client_cert": pki.clientCert, "client_key": key}
		return values
	}
	created, err := svc.Create(t.Context(), CreateConnectorRequest{
		Name: "orders-mcp", Type: "mcp", Subtype: "remote", Values: withKey(pki.clientKey),
	}, "user-1")
	if err != nil || len(created.Warnings) != 0 {
		t.Fatalf("Create with the client certificate = %v, %v; want its tools listed", created, err)
	}
	typo := filepath.Join(pki.dir, "client-2027.key")

	resp, err := svc.UpdateWithRedeploy(t.Context(), created.ID, UpdateConnectorRequest{Values: withKey(typo)}, "user-1", ConnectorUpdateOptions{})

	if err != nil {
		t.Fatalf("UpdateWithRedeploy: %v", err)
	}
	if len(resp.Warnings) != 1 || !strings.HasPrefix(resp.Warnings[0], mcpToolListKeptBecause) || !strings.Contains(resp.Warnings[0], typo) {
		t.Errorf("Warnings = %q, want the kept tool list and the unreadable %s", resp.Warnings, typo)
	}
}

func mountTestFile(t *testing.T, from, to string) {
	t.Helper()
	data, err := os.ReadFile(from)
	if err != nil {
		t.Fatalf("read %s: %v", from, err)
	}
	if err := os.WriteFile(to, data, 0o600); err != nil {
		t.Fatalf("write %s: %v", to, err)
	}
}

func saveTimeMCPValues(url, caBundle string) map[string]any {
	return map[string]any{
		"server_url":      url,
		"connection_type": "streamable-http",
		"ssl_config":      map[string]any{"ca_bundle": caBundle},
	}
}

func TestEnrichMCPManifestIfNeeded_AFailedDiscoveryComesBackAsAWarning(t *testing.T) {
	t.Setenv(allowPrivateMCPEnvVar, "true")
	pki := newMTLSFixture(t)
	pki.useAsTLSDir(t)
	server := startTLSMCPServer(t, pki, true)
	cs := &ConnectorService{logger: discardLogger(), mcpSvc: NewMCPDiscoveryService(discardLogger(), nil)}

	values, enriched, warnings := cs.enrichMCPManifestIfNeeded(t.Context(), "mcp", saveTimeMCPValues(server.URL, pki.caBundle))

	if enriched || values != nil {
		t.Errorf("enriched = %v with values %v, want discovery to have failed", enriched, values)
	}
	if want := []string{warningSavedWithoutTools + msgMCPClientCertMissing}; !slices.Equal(warnings, want) {
		t.Errorf("warnings = %q, want %q", warnings, want)
	}
}

func newConnectorServiceWithDiscovery(t *testing.T) *ConnectorService {
	t.Helper()
	db := testOpenDB(t)
	svc := NewConnectorService(store.NewConnectorRepo(db), nil, "test-ns", discardLogger())
	svc.SetMCPDiscoveryService(NewMCPDiscoveryService(discardLogger(), nil))
	svc.SetRedeployDeps(db, store.NewAgentRepo(db), store.NewWorkflowRepo(db), store.NewOutboxRepo(db))
	return svc
}

func TestConnectorService_UpdateWithRedeploy_ReportsAFailedSaveTimeDiscovery(t *testing.T) {
	t.Setenv(allowPrivateMCPEnvVar, "true")
	pki := newMTLSFixture(t)
	pki.useAsTLSDir(t)
	server := startTLSMCPServer(t, pki, true)
	svc := newConnectorServiceWithDiscovery(t)
	created, err := svc.Create(t.Context(), CreateConnectorRequest{
		Name: "orders-mcp", Type: "mcp", Subtype: "remote", Values: saveTimeMCPValues(server.URL, pki.caBundle),
	}, "user-1")
	if err != nil {
		t.Fatalf("seed Create: %v", err)
	}

	resp, err := svc.UpdateWithRedeploy(t.Context(), created.ID, UpdateConnectorRequest{
		Description: "changed after the first save", Values: saveTimeMCPValues(server.URL, pki.caBundle),
	}, "user-1", ConnectorUpdateOptions{})

	if err != nil {
		t.Fatalf("UpdateWithRedeploy: %v", err)
	}
	if want := []string{warningSavedWithoutTools + msgMCPClientCertMissing}; !slices.Equal(resp.Warnings, want) {
		t.Errorf("Warnings = %q, want %q", resp.Warnings, want)
	}
}

func TestConnectorService_Create_ReportsAFailedSaveTimeDiscovery(t *testing.T) {
	t.Setenv(allowPrivateMCPEnvVar, "true")
	pki := newMTLSFixture(t)
	pki.useAsTLSDir(t)
	server := startTLSMCPServer(t, pki, true)
	svc := newConnectorServiceWithDiscovery(t)

	created, err := svc.Create(t.Context(), CreateConnectorRequest{
		Name: "orders-mcp", Type: "mcp", Subtype: "remote", Values: saveTimeMCPValues(server.URL, pki.caBundle),
	}, "user-1")

	if err != nil {
		t.Fatalf("Create: %v (a failed discovery must not block the save)", err)
	}
	if want := []string{warningSavedWithoutTools + msgMCPClientCertMissing}; !slices.Equal(created.Warnings, want) {
		t.Errorf("Warnings = %q, want %q", created.Warnings, want)
	}
}

func TestConnectorService_Update_ReportsAFailedSaveTimeDiscoveryUntilItSucceeds(t *testing.T) {
	t.Setenv(allowPrivateMCPEnvVar, "true")
	pki := newMTLSFixture(t)
	pki.useAsTLSDir(t)
	server := startTLSMCPServer(t, pki, true)
	svc := newConnectorServiceWithDiscovery(t)
	created, err := svc.Create(t.Context(), CreateConnectorRequest{
		Name: "orders-mcp", Type: "mcp", Subtype: "remote", Values: saveTimeMCPValues(server.URL, pki.caBundle),
	}, "user-1")
	if err != nil {
		t.Fatalf("seed Create: %v", err)
	}

	stillFailing, err := svc.Update(t.Context(), created.ID, UpdateConnectorRequest{
		Description: "changed after the first save", Values: saveTimeMCPValues(server.URL, pki.caBundle),
	}, "user-1")
	if err != nil {
		t.Fatalf("Update without the client certificate: %v", err)
	}
	if want := []string{warningSavedWithoutTools + msgMCPClientCertMissing}; !slices.Equal(stillFailing.Warnings, want) {
		t.Errorf("Warnings while discovery still fails = %q, want %q", stillFailing.Warnings, want)
	}

	values := saveTimeMCPValues(server.URL, pki.caBundle)
	values["ssl_config"] = map[string]any{"ca_bundle": pki.caBundle, "client_cert": pki.clientCert, "client_key": pki.clientKey}
	fixed, err := svc.Update(t.Context(), created.ID, UpdateConnectorRequest{Values: values}, "user-1")
	if err != nil {
		t.Fatalf("Update with the client certificate: %v", err)
	}
	if len(fixed.Warnings) != 0 {
		t.Errorf("Warnings once discovery works = %q, want none", fixed.Warnings)
	}
}

func TestConnectorService_Create_ListsToolsThroughTheServerName(t *testing.T) {
	t.Setenv(allowPrivateMCPEnvVar, "true")
	pki := newMTLSFixture(t)
	pki.useAsTLSDir(t)
	server := startTLSMCPServerWith(t, pki, func(cfg *tls.Config) {
		pair, err := tls.LoadX509KeyPair(pki.namedServerCert, pki.namedServerKey)
		if err != nil {
			t.Fatalf("load the named server's keypair: %v", err)
		}
		cfg.Certificates = []tls.Certificate{pair}
	})
	tests := map[string]struct {
		ssl  map[string]any
		want []string
	}{
		"with server_name":    {ssl: map[string]any{"ca_bundle": pki.caBundle, "server_name": namedServerHost}},
		"without server_name": {ssl: map[string]any{"ca_bundle": pki.caBundle}, want: []string{warningSavedWithoutTools + msgMCPTLS}},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			values := saveTimeMCPValues(server.URL, pki.caBundle)
			values["ssl_config"] = tc.ssl

			created, err := newConnectorServiceWithDiscovery(t).Create(t.Context(), CreateConnectorRequest{
				Name: "orders-mcp", Type: "mcp", Subtype: "remote", Values: values,
			}, "user-1")

			if err != nil {
				t.Fatalf("Create: %v", err)
			}
			if !slices.Equal(created.Warnings, tc.want) {
				t.Errorf("Warnings = %q, want %q", created.Warnings, tc.want)
			}
		})
	}
}

// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

const (
	mcpTLSDirEnv = "SAM_MCP_CONNECTOR_TLS_DIR"
	mcpTLSDir    = "/etc/sam/mcp-tls"
)

func mcpConnectorValues(serverURL string, ssl map[string]any) map[string]any {
	return map[string]any{"server_url": serverURL, "connection_type": "streamable-http", "ssl_config": ssl}
}

func createMCPConnector(name string, values map[string]any) map[string]any {
	return map[string]any{
		"name": name, "description": "An MCP server behind mutual TLS.",
		"connectorType": "mcp", "connectorSubtype": "remote", "values": values,
	}
}

type refusedSSLConfig struct {
	name   string
	ssl    map[string]any
	field  string
	saying string
}

func refusedSSLConfigs() []refusedSSLConfig {
	return []refusedSSLConfig{
		{"verify", map[string]any{"verify": false}, "values.ssl_config.verify", "SAM_MCP_CONNECTOR_TLS_VERIFY"},
		{"a file outside the directory", map[string]any{"ca_bundle": "/etc/passwd"}, "values.ssl_config.ca_bundle", "must be a file inside"},
		{"a file in a sibling that shares the prefix", map[string]any{"ca_bundle": mcpTLSDir + "-x/ca.pem"}, "values.ssl_config.ca_bundle", "must be a file inside"},
		{"a relative path", map[string]any{"client_cert": "client.crt", "client_key": mcpTLSDir + "/client.key"}, "values.ssl_config.client_cert", "absolute"},
		{"a path with dot-dot", map[string]any{"ca_bundle": mcpTLSDir + "/../ca.pem"}, "values.ssl_config.ca_bundle", "clean path"},
		{"an unknown key", map[string]any{"ca_cert": mcpTLSDir + "/ca.pem"}, "values.ssl_config.ca_cert", "not a key of ssl_config"},
		{"a certificate without its key", map[string]any{"client_cert": mcpTLSDir + "/client.crt"}, "values.ssl_config.client_key", "must be set when"},
	}
}

func TestConnectors_MCPSSLConfigIsRefusedWith422_OnCreateAndOnPatch(t *testing.T) {
	t.Setenv(mcpTLSDirEnv, mcpTLSDir)
	app := newConnectorAPI(t)
	server := fakeMCPServer(t, []string{"search_orders"})

	created := decodeData[Connector](t, mustStatus(t, app.call(http.MethodPost, "/connectors",
		createMCPConnector("mcp-patched", mcpConnectorValues(server.URL, map[string]any{"server_name": "mcp.internal"}))), http.StatusCreated))

	for _, tc := range refusedSSLConfigs() {
		t.Run(tc.name, func(t *testing.T) {
			expectDetailSaying(t, app.call(http.MethodPost, "/connectors",
				createMCPConnector("mcp-refused", mcpConnectorValues(server.URL, tc.ssl))), tc.field, tc.saying)

			expectDetailSaying(t, app.call(http.MethodPatch, "/connectors/"+created.ID,
				map[string]any{"values": mcpConnectorValues(server.URL, tc.ssl)}), tc.field, tc.saying)
		})
	}

	stored := decodeData[Connector](t, mustStatus(t, app.call(http.MethodGet, "/connectors/"+created.ID, nil), http.StatusOK))
	if ssl, _ := stored.Values["ssl_config"].(map[string]any); ssl["server_name"] != "mcp.internal" || len(ssl) != 1 {
		t.Errorf("a refused patch changed the stored ssl_config to %v", stored.Values["ssl_config"])
	}
}

func TestConnectors_MCPSSLFilesNeedTheDirectoryTheOperatorSets(t *testing.T) {
	t.Setenv(mcpTLSDirEnv, "")
	app := newConnectorAPI(t)
	server := fakeMCPServer(t, []string{"search_orders"})

	expectDetailSaying(t, app.call(http.MethodPost, "/connectors",
		createMCPConnector("mcp-no-dir", mcpConnectorValues(server.URL, map[string]any{"ca_bundle": mcpTLSDir + "/ca.pem"}))),
		"values.ssl_config.ca_bundle", mcpTLSDirEnv)

	mustStatus(t, app.call(http.MethodPost, "/connectors",
		createMCPConnector("mcp-sni-only", mcpConnectorValues(server.URL, map[string]any{"server_name": "mcp.internal"}))), http.StatusCreated)
}

func TestConnectors_MCPSSLConfigIsStoredAndReturnedAsWritten(t *testing.T) {
	t.Setenv(mcpTLSDirEnv, mcpTLSDir)
	app := newConnectorAPI(t)
	server := fakeMCPServer(t, []string{"search_orders"})
	ssl := map[string]any{
		"ca_bundle":   mcpTLSDir + "/ca.pem",
		"client_cert": mcpTLSDir + "/client.crt",
		"client_key":  mcpTLSDir + "/client.key",
		"server_name": "mcp.internal.example.com",
	}

	created := decodeData[Connector](t, mustStatus(t, app.call(http.MethodPost, "/connectors",
		createMCPConnector("mcp-mtls", mcpConnectorValues(server.URL, ssl))), http.StatusCreated))
	fetched := decodeData[Connector](t, mustStatus(t, app.call(http.MethodGet, "/connectors/"+created.ID, nil), http.StatusOK))

	got, _ := fetched.Values["ssl_config"].(map[string]any)
	for key, want := range ssl {
		if got[key] != want {
			t.Errorf("ssl_config.%s = %v, want %v: %v", key, got[key], want, fetched.Values["ssl_config"])
		}
	}
}

func TestMCPDiscover_NamingTLSFilesNeedsPermissionToCreateOrEditConnectors(t *testing.T) {
	t.Setenv(mcpTLSDirEnv, mcpTLSDir)
	app := newConnectorAPI(t)
	server := fakeMCPServer(t, []string{"search_orders"})
	files := map[string]any{"clientCert": mcpTLSDir + "/client.crt", "clientKey": mcpTLSDir + "/client.key"}
	discoverWith := func(ssl map[string]any) map[string]any {
		return map[string]any{"serverUrl": server.URL, "connectionType": "streamable_http", "sslConfig": ssl}
	}
	reader := app.as(rbac.ScopeConnectorRead)

	expectStatus(t, reader.call(http.MethodPut, "/connectors/mcp/discover", discoverWith(files)), http.StatusForbidden)
	mustStatus(t, reader.call(http.MethodPut, "/connectors/mcp/discover", discoverWith(map[string]any{"serverName": "mcp.internal"})), http.StatusOK)

	initiated := decodeData[MCPOAuthInitiateResult](t, app.call(http.MethodPut, "/connectors/mcp/oauth/initiate", map[string]any{
		"serverUrl": server.URL, "connectionType": "streamable_http", "mode": "manual",
		"authorizationUrl": "https://auth.example.com/authorize",
		"tokenUrl":         oauthTokenEndpoint(t, "access-token").URL,
		"clientId":         "sam-mcp-discovery",
	}))
	expectStatus(t, app.anonymous().call(http.MethodGet, "/connectors/mcp/oauth/callback?code=auth-code&state="+initiated.State, nil), http.StatusOK)
	expectStatus(t, reader.call(http.MethodPut, "/connectors/mcp/oauth/discoverWithToken", map[string]any{
		"sessionId": initiated.SessionID, "serverUrl": server.URL, "sslConfig": files,
	}), http.StatusForbidden)

	for _, editor := range []string{rbac.ScopeConnectorCreate, rbac.ScopeConnectorUpdate} {
		expectDetail(t, app.as(rbac.ScopeConnectorRead, editor).call(http.MethodPut, "/connectors/mcp/discover", discoverWith(files)),
			http.StatusUnprocessableEntity, "sslConfig")
	}
}

func mustStatus(t *testing.T, rec *httptest.ResponseRecorder, want int) *httptest.ResponseRecorder {
	t.Helper()
	expectStatus(t, rec, want)
	return rec
}

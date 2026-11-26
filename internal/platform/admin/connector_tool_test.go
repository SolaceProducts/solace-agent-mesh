// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"cmp"
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"

	mcppkg "github.com/SolaceProducts/solace-agent-mesh/internal/tool/mcp"
)

func sqlConnector(subtype string, values map[string]any) ConnectorConfig {
	return ConnectorConfig{
		ID:      "550e8400-e29b-41d4-a716-446655440000",
		Type:    "sql",
		Subtype: subtype,
		Values:  values,
	}
}

func mcpConnector(values map[string]any) ConnectorConfig {
	return ConnectorConfig{
		ID:     "660e8400-e29b-41d4-a716-446655440000",
		Type:   "mcp",
		Values: values,
	}
}

const testAPIConnectorResourceKey = "default/connectors/770e8400-e29b-41d4-a716-446655440000/spec.yaml"

const testAPIConnectorName = "Orders API"

func apiConnector(values map[string]any) ConnectorConfig {
	return ConnectorConfig{
		ID:          "770e8400-e29b-41d4-a716-446655440000",
		Name:        testAPIConnectorName,
		Type:        "api",
		Subtype:     "openapi",
		ResourceKey: testAPIConnectorResourceKey,
		Values:      values,
	}
}

func kbConnector(values map[string]any) ConnectorConfig {
	return ConnectorConfig{
		ID:     "880e8400-e29b-41d4-a716-446655440000",
		Type:   "knowledge_base",
		Values: values,
	}
}

func baseSQLValues() map[string]any {
	return map[string]any{
		"hostname": "db.example.com",
		"port":     "5432",
		"username": "admin",
		"password": "secret",
		"database": "mydb",
	}
}

func buildConnectorTool(conn ConnectorConfig) map[string]any {
	tools := buildConnectorTools(conn)
	if len(tools) == 0 {
		return nil
	}
	return tools[0]
}

func assertToolField(t *testing.T, tool map[string]any, key string, want any) {
	t.Helper()
	if got := tool[key]; got != want {
		t.Errorf("%s = %v, want %v", key, got, want)
	}
}

func assertStringContains(t *testing.T, s, substr string) {
	t.Helper()
	if !strings.Contains(s, substr) {
		t.Errorf("expected %q to contain %q", s, substr)
	}
}

func TestBuildSQLConnectorTool_PostgreSQL(t *testing.T) {
	t.Parallel()
	tool := buildConnectorTool(sqlConnector("postgres", baseSQLValues()))
	assertToolField(t, tool, "tool_type", "builtin")
	assertToolField(t, tool, "tool_name", "mydb_sql_query_550e8400")
	tc := tool["tool_config"].(map[string]any)
	connStr := tc["connection_string"].(string)
	assertStringContains(t, connStr, "postgresql://")
	assertStringContains(t, connStr, "admin:secret@db.example.com:5432/mydb")
	assertToolField(t, tc, "tool_name", "mydb_sql_query_550e8400")
	assertToolField(t, tc, "tool_description", "Query the mydb postgres database.")
	assertToolField(t, tc, "_str_binary", "execute_sql_query")
}

func TestBuildSQLConnectorTool_MySQL(t *testing.T) {
	t.Parallel()
	vals := baseSQLValues()
	vals["port"] = "3306"
	tool := buildConnectorTool(sqlConnector("mysql", vals))
	connStr := tool["tool_config"].(map[string]any)["connection_string"].(string)
	assertStringContains(t, connStr, "mysql://")
	assertStringContains(t, connStr, "admin:secret@db.example.com:3306/mydb")
}

func TestBuildSQLConnectorTool_MariaDB(t *testing.T) {
	t.Parallel()
	vals := baseSQLValues()
	vals["port"] = "3306"
	tool := buildConnectorTool(sqlConnector("mariadb", vals))
	connStr := tool["tool_config"].(map[string]any)["connection_string"].(string)
	assertStringContains(t, connStr, "mysql://")
}

func TestBuildSQLConnectorTool_MSSQL(t *testing.T) {
	t.Parallel()
	vals := baseSQLValues()
	vals["port"] = "1433"
	vals["encrypt"] = "yes"
	vals["trust_server_certificate"] = "yes"
	tool := buildConnectorTool(sqlConnector("mssql", vals))
	connStr := tool["tool_config"].(map[string]any)["connection_string"].(string)
	assertStringContains(t, connStr, "sqlserver://")
	assertStringContains(t, connStr, "database=mydb")
	assertStringContains(t, connStr, "encrypt=true")
	assertStringContains(t, connStr, "TrustServerCertificate=true")
}

func TestBuildSQLConnectorTool_MSSQL_EncryptMapping(t *testing.T) {
	t.Parallel()

	tests := []struct {
		encrypt string
		want    string
	}{
		{"yes", "encrypt=true"},
		{"no", "encrypt=disable"},
		{"strict", "encrypt=strict"},
	}
	for _, tt := range tests {
		t.Run(tt.encrypt, func(t *testing.T) {
			t.Parallel()
			vals := baseSQLValues()
			vals["port"] = "1433"
			vals["encrypt"] = tt.encrypt
			tool := buildConnectorTool(sqlConnector("mssql", vals))
			connStr := tool["tool_config"].(map[string]any)["connection_string"].(string)
			assertStringContains(t, connStr, tt.want)
		})
	}
}

func TestBuildSQLConnectorTool_MSSQL_NoTrustCertWhenEncryptNotYes(t *testing.T) {
	t.Parallel()
	vals := baseSQLValues()
	vals["port"] = "1433"
	vals["encrypt"] = "no"
	vals["trust_server_certificate"] = "yes"
	tool := buildConnectorTool(sqlConnector("mssql", vals))
	connStr := tool["tool_config"].(map[string]any)["connection_string"].(string)
	if strings.Contains(connStr, "TrustServerCertificate") {
		t.Error("TrustServerCertificate should only be set when encrypt=yes")
	}
}

func TestBuildSQLConnectorTool_Oracle(t *testing.T) {
	t.Parallel()
	vals := map[string]any{
		"hostname":     "oracle.example.com",
		"port":         "1521",
		"username":     "admin",
		"password":     "secret",
		"service_name": "ORCL",
	}
	tool := buildConnectorTool(sqlConnector("oracle", vals))
	connStr := tool["tool_config"].(map[string]any)["connection_string"].(string)
	assertStringContains(t, connStr, "oracle://")
	assertStringContains(t, connStr, "admin:secret@oracle.example.com:1521/ORCL")
}

func TestBuildSQLConnectorTool_SpecialCharsInCredentials(t *testing.T) {
	t.Parallel()
	vals := map[string]any{
		"hostname": "db.example.com",
		"port":     "5432",
		"username": "user@domain",
		"password": "p@ss:w/rd#1",
		"database": "mydb",
	}
	tool := buildConnectorTool(sqlConnector("postgres", vals))
	connStr := tool["tool_config"].(map[string]any)["connection_string"].(string)
	if strings.Contains(connStr, "user@domain") {
		t.Error("@ in username should be URL-encoded")
	}
	assertStringContains(t, connStr, "postgresql://")
	assertStringContains(t, connStr, "db.example.com:5432/mydb")
}

func TestBuildSQLConnectorTool_CustomToolName(t *testing.T) {
	t.Parallel()
	vals := baseSQLValues()
	vals["tool_name"] = "custom_query"
	tool := buildConnectorTool(sqlConnector("postgres", vals))
	assertToolField(t, tool, "tool_name", "custom_query")
	tc := tool["tool_config"].(map[string]any)
	assertToolField(t, tc, "tool_name", "custom_query")
}

func TestBuildSQLConnectorTool_EnvVarFallbackName(t *testing.T) {
	t.Parallel()
	tool := buildConnectorTool(sqlConnector("postgres", map[string]any{}))
	assertToolField(t, tool, "tool_name", "sql_query_550e8400")
	tc := tool["tool_config"].(map[string]any)
	assertToolField(t, tc, "tool_description", "Query the postgres database.")
}

func TestBuildSQLConnectorTool_DialectInDescription(t *testing.T) {
	t.Parallel()
	tests := []struct {
		subtype string
		dialect string
	}{
		{"postgres", "postgres"},
		{"mysql", "mysql"},
		{"mariadb", "mariadb"},
		{"mssql", "mssql"},
		{"oracle", "oracle"},
	}
	for _, tt := range tests {
		t.Run(tt.subtype, func(t *testing.T) {
			t.Parallel()
			vals := baseSQLValues()
			if tt.subtype == "oracle" {
				vals["service_name"] = vals["database"]
				delete(vals, "database")
			}
			tool := buildConnectorTool(sqlConnector(tt.subtype, vals))
			tc := tool["tool_config"].(map[string]any)
			desc := tc["tool_description"].(string)
			assertStringContains(t, desc, tt.dialect)
		})
	}
}

func TestBuildSQLConnectorTool_EnvVarFallbackConnStr(t *testing.T) {
	t.Parallel()
	tool := buildConnectorTool(sqlConnector("postgres", map[string]any{}))
	connStr := tool["tool_config"].(map[string]any)["connection_string"].(string)
	assertStringContains(t, connStr, "${HOSTNAME_")
	assertStringContains(t, connStr, "${PORT_")
	assertStringContains(t, connStr, "${USERNAME_")
	assertStringContains(t, connStr, "${PASSWORD_")
	assertStringContains(t, connStr, "${DATABASE_")
}

func TestBuildSQLConnectorTool_DefaultSubtype(t *testing.T) {
	t.Parallel()
	tool := buildConnectorTool(sqlConnector("", baseSQLValues()))
	connStr := tool["tool_config"].(map[string]any)["connection_string"].(string)
	assertStringContains(t, connStr, "postgresql://")
}

func TestBuildMCPConnectorTool_SSLConfigReachesTheRuntime(t *testing.T) {
	t.Setenv(mcpTLSDirEnvVar, "/etc/sam/mtls")
	ssl := map[string]any{
		"ca_bundle":   "/etc/sam/mtls/ca.pem",
		"client_cert": "/etc/sam/mtls/client.crt",
		"client_key":  "/etc/sam/mtls/client.key",
		"server_name": "mcp.internal",
	}
	tests := map[string]map[string]any{
		"no auth": {"server_url": "https://mcp.example.com", "ssl_config": ssl},
		"oauth": {
			"server_url":                   "https://mcp.example.com",
			"ssl_config":                   ssl,
			"auth_type":                    "oauth",
			"auth_oauth_mode":              "manual",
			"auth_oauth_authorization_url": "https://auth.example.com/authorize",
			"auth_oauth_token_url":         "https://auth.example.com/token",
			"auth_oauth_client_id":         "client-id",
		},
	}
	for name, values := range tests {
		t.Run(name, func(t *testing.T) {
			entry, err := asGenericJSON(buildConnectorTool(mcpConnector(values)))
			if err != nil {
				t.Fatalf("asGenericJSON: %v", err)
			}
			cfg, err := mcppkg.ParseToolsetConfig(entry)
			if err != nil {
				t.Fatalf("ParseToolsetConfig: %v", err)
			}
			if cfg.SSLCABundle != ssl["ca_bundle"] || cfg.SSLClientCert != ssl["client_cert"] ||
				cfg.SSLClientKey != ssl["client_key"] || cfg.SSLServerName != ssl["server_name"] {
				t.Errorf("runtime ssl_config = ca %q, cert %q, key %q, sni %q; want %v",
					cfg.SSLCABundle, cfg.SSLClientCert, cfg.SSLClientKey, cfg.SSLServerName, ssl)
			}
		})
	}
}

func TestBuildMCPConnectorTool_AStoredVerifyNeverReachesTheRuntime(t *testing.T) {
	t.Parallel()
	entry, err := asGenericJSON(buildConnectorTool(mcpConnector(map[string]any{
		"server_url": "https://mcp.example.com",
		"ssl_config": map[string]any{"verify": false, "ca_cert": "/etc/passwd", "server_name": "mcp.internal"},
	})))
	if err != nil {
		t.Fatalf("asGenericJSON: %v", err)
	}
	cfg, err := mcppkg.ParseToolsetConfig(entry)
	if err != nil {
		t.Fatalf("ParseToolsetConfig: %v", err)
	}
	if cfg.SSLVerify != nil || cfg.SSLServerName != "mcp.internal" {
		t.Errorf("runtime ssl_config = verify %v, sni %q; want verify unset and sni mcp.internal", cfg.SSLVerify, cfg.SSLServerName)
	}
	params, _ := entry["connection_params"].(map[string]any)
	if ssl, _ := params["ssl_config"].(map[string]any); len(ssl) != 1 {
		t.Errorf("connection_params.ssl_config = %v, want only server_name", params["ssl_config"])
	}
}

func TestBuildMCPConnectorTool_NoAuth(t *testing.T) {
	t.Parallel()
	tool := buildConnectorTool(mcpConnector(map[string]any{
		"server_url": "https://mcp.example.com/sse",
	}))
	if tool["tool_type"] != "mcp" {
		t.Errorf("tool_type = %v, want mcp", tool["tool_type"])
	}
	params := tool["connection_params"].(map[string]any)
	if params["url"] != "https://mcp.example.com/sse" {
		t.Errorf("url = %v", params["url"])
	}
	if params["type"] != "streamable-http" {
		t.Errorf("type = %v, want streamable-http", params["type"])
	}
	if _, hasAuth := tool["auth"]; hasAuth {
		t.Error("should have no auth block")
	}
}

func TestBuildMCPConnectorTool_BearerAuth(t *testing.T) {
	t.Parallel()
	tool := buildConnectorTool(mcpConnector(map[string]any{
		"server_url":             "https://mcp.example.com",
		"auth_type":              "http",
		"auth_http_scheme":       "bearer",
		"auth_http_bearer_token": "my-token-123",
	}))
	params := tool["connection_params"].(map[string]any)
	headers := params["headers"].(map[string]string)
	if headers["Authorization"] != "Bearer my-token-123" {
		t.Errorf("Authorization = %q", headers["Authorization"])
	}
}

func TestBuildMCPConnectorTool_BasicAuth(t *testing.T) {
	t.Parallel()
	tool := buildConnectorTool(mcpConnector(map[string]any{
		"server_url":               "https://mcp.example.com",
		"auth_type":                "http",
		"auth_http_scheme":         "basic",
		"auth_http_basic_username": "user",
		"auth_http_basic_password": "pass",
	}))
	params := tool["connection_params"].(map[string]any)
	headers := params["headers"].(map[string]string)
	auth := headers["Authorization"]
	if !strings.HasPrefix(auth, "Basic ") {
		t.Errorf("expected Basic auth header, got %q", auth)
	}
}

func TestBuildMCPConnectorTool_BasicAuthEnvVarRef(t *testing.T) {
	t.Parallel()
	tool := buildConnectorTool(mcpConnector(map[string]any{
		"server_url":               "https://mcp.example.com",
		"auth_type":                "http",
		"auth_http_scheme":         "basic",
		"auth_http_basic_username": "${MCP_USER}",
		"auth_http_basic_password": "${MCP_PASS}",
	}))
	params := tool["connection_params"].(map[string]any)
	authMap, ok := params["auth"].(map[string]any)
	if !ok {
		t.Fatal("expected auth map for env var refs, not header encoding")
	}
	if authMap["type"] != "basic" {
		t.Errorf("auth.type = %v", authMap["type"])
	}
	if authMap["username"] != "${MCP_USER}" {
		t.Errorf("auth.username = %v", authMap["username"])
	}
}

func TestBuildMCPConnectorTool_APIKeyInHeader(t *testing.T) {
	t.Parallel()
	tool := buildConnectorTool(mcpConnector(map[string]any{
		"server_url":           "https://mcp.example.com",
		"auth_type":            "apikey",
		"auth_apikey_location": "header",
		"auth_apikey_name":     "X-Api-Key",
		"auth_apikey_value":    "key-123",
	}))
	params := tool["connection_params"].(map[string]any)
	headers := params["headers"].(map[string]string)
	if headers["X-Api-Key"] != "key-123" {
		t.Errorf("header X-Api-Key = %q", headers["X-Api-Key"])
	}
}

func TestBuildMCPConnectorTool_APIKeyInQuery(t *testing.T) {
	t.Parallel()
	tool := buildConnectorTool(mcpConnector(map[string]any{
		"server_url":           "https://mcp.example.com",
		"auth_type":            "apikey",
		"auth_apikey_location": "query",
		"auth_apikey_name":     "api_key",
		"auth_apikey_value":    "key-123",
	}))
	params := tool["connection_params"].(map[string]any)
	url := params["url"].(string)
	assertStringContains(t, url, "?api_key=key-123")
}

func TestBuildMCPConnectorTool_OAuth(t *testing.T) {
	t.Parallel()
	tool := buildConnectorTool(mcpConnector(map[string]any{
		"server_url":                            "https://mcp.example.com",
		"auth_type":                             "oauth",
		"auth_oauth_mode":                       "manual",
		"auth_oauth_authorization_url":          "https://auth.example.com/authorize",
		"auth_oauth_token_url":                  "https://auth.example.com/token",
		"auth_oauth_client_id":                  "client-id",
		"auth_oauth_client_secret":              "client-secret",
		"auth_oauth_scopes":                     "read write",
		"auth_oauth_token_endpoint_auth_method": "client_secret_post",
	}))
	if tool["tool_type"] != "mcp" {
		t.Errorf("tool_type = %v", tool["tool_type"])
	}
	authBlock := tool["auth"].(map[string]any)
	if authBlock["type"] != "oauth2" {
		t.Errorf("auth.type = %v", authBlock["type"])
	}
	scheme := authBlock["scheme"].(map[string]any)
	if scheme["token_url"] != "https://auth.example.com/token" {
		t.Errorf("token_url = %v", scheme["token_url"])
	}
	if scheme["token_endpoint_auth_method"] != "client_secret_post" {
		t.Errorf("auth_method = %v", scheme["token_endpoint_auth_method"])
	}
}

func TestBuildMCPConnectorTool_OAuthDiscovery(t *testing.T) {
	t.Parallel()
	tool := buildConnectorTool(mcpConnector(map[string]any{
		"server_url":      "https://mcp.example.com",
		"auth_type":       "oauth",
		"auth_oauth_mode": "discovery",
	}))
	authBlock := tool["auth"].(map[string]any)
	if authBlock["type"] != "oauth2" {
		t.Errorf("auth.type = %v", authBlock["type"])
	}
	if _, hasScheme := authBlock["scheme"]; hasScheme {
		t.Errorf("discovery mode must omit scheme; got %v", authBlock["scheme"])
	}
	if _, hasCred := authBlock["credential"]; hasCred {
		t.Errorf("discovery mode must omit credential; got %v", authBlock["credential"])
	}
}

func TestBuildMCPConnectorTool_OAuthDiscoveryWithScopes(t *testing.T) {
	t.Parallel()
	tool := buildConnectorTool(mcpConnector(map[string]any{
		"server_url":        "https://mcp.example.com",
		"auth_type":         "oauth",
		"auth_oauth_mode":   "discovery",
		"auth_oauth_scopes": "read write",
	}))
	authBlock := tool["auth"].(map[string]any)
	scheme, ok := authBlock["scheme"].(map[string]any)
	if !ok {
		t.Fatalf("scheme expected when scopes are set; got %v", authBlock)
	}
	scopes, _ := scheme["scopes"].([]string)
	if len(scopes) != 2 || scopes[0] != "read" || scopes[1] != "write" {
		t.Errorf("scopes = %v", scopes)
	}
	if _, hasCred := authBlock["credential"]; hasCred {
		t.Errorf("discovery mode must omit credential; got %v", authBlock["credential"])
	}
}

func TestBuildMCPConnectorTool_CustomHeaders(t *testing.T) {
	t.Parallel()
	tool := buildConnectorTool(mcpConnector(map[string]any{
		"server_url":     "https://mcp.example.com",
		"custom_headers": map[string]any{"X-Custom": "value"},
	}))
	params := tool["connection_params"].(map[string]any)
	headers := params["headers"].(map[string]string)
	if headers["X-Custom"] != "value" {
		t.Errorf("X-Custom = %q", headers["X-Custom"])
	}
}

func TestBuildMCPConnectorTool_ConnectionType(t *testing.T) {
	t.Parallel()
	tool := buildConnectorTool(mcpConnector(map[string]any{
		"server_url":      "https://mcp.example.com",
		"connection_type": "sse",
	}))
	params := tool["connection_params"].(map[string]any)
	if params["type"] != "sse" {
		t.Errorf("type = %v, want sse", params["type"])
	}
}

func TestBuildMCPConnectorTool_Manifest(t *testing.T) {
	t.Parallel()
	manifest := []any{map[string]any{"name": "tool1"}}
	tool := buildConnectorTool(mcpConnector(map[string]any{
		"server_url": "https://mcp.example.com",
		"manifest":   manifest,
	}))
	if tool["manifest"] == nil {
		t.Error("manifest should be present in output")
	}
}

func TestBuildMCPConnectorTool_FilterFields_AllAuthBranches(t *testing.T) {
	t.Parallel()

	authConfigs := []struct {
		name      string
		extraVals map[string]any
	}{
		{
			name:      "no auth",
			extraVals: map[string]any{},
		},
		{
			name: "apikey",
			extraVals: map[string]any{
				"auth_type":            "apikey",
				"auth_apikey_location": "header",
				"auth_apikey_name":     "X-Api-Key",
				"auth_apikey_value":    "k",
			},
		},
		{
			name: "http bearer",
			extraVals: map[string]any{
				"auth_type":              "http",
				"auth_http_scheme":       "bearer",
				"auth_http_bearer_token": "tok",
			},
		},
		{
			name: "oauth",
			extraVals: map[string]any{
				"auth_type":                    "oauth",
				"auth_oauth_authorization_url": "https://auth.example.com/authorize",
				"auth_oauth_token_url":         "https://auth.example.com/token",
				"auth_oauth_client_id":         "client",
				"auth_oauth_client_secret":     "secret",
			},
		},
	}

	for _, ac := range authConfigs {
		t.Run(ac.name+" allow_list", func(t *testing.T) {
			t.Parallel()
			vals := map[string]any{
				"server_url": "https://mcp.example.com",
				"allow_list": "tool_a, tool_b , tool_c",
			}
			maps.Copy(vals, ac.extraVals)
			tool := buildConnectorTool(mcpConnector(vals))
			got, ok := tool["allow_list"].([]string)
			if !ok {
				t.Fatalf("allow_list missing or wrong type: %#v", tool["allow_list"])
			}
			want := []string{"tool_a", "tool_b", "tool_c"}
			if len(got) != len(want) {
				t.Fatalf("allow_list = %v, want %v", got, want)
			}
			for i := range want {
				if got[i] != want[i] {
					t.Errorf("allow_list[%d] = %q, want %q", i, got[i], want[i])
				}
			}
		})

		t.Run(ac.name+" deny_list", func(t *testing.T) {
			t.Parallel()
			vals := map[string]any{
				"server_url": "https://mcp.example.com",
				"deny_list":  "drop_x,drop_y",
			}
			maps.Copy(vals, ac.extraVals)
			tool := buildConnectorTool(mcpConnector(vals))
			got, ok := tool["deny_list"].([]string)
			if !ok {
				t.Fatalf("deny_list missing or wrong type: %#v", tool["deny_list"])
			}
			if len(got) != 2 || got[0] != "drop_x" || got[1] != "drop_y" {
				t.Errorf("deny_list = %v, want [drop_x drop_y]", got)
			}
		})

		t.Run(ac.name+" tool_name and prefix", func(t *testing.T) {
			t.Parallel()
			vals := map[string]any{
				"server_url":       "https://mcp.example.com",
				"tool_name":        "single_tool",
				"tool_name_prefix": "atlassian_",
			}
			maps.Copy(vals, ac.extraVals)
			tool := buildConnectorTool(mcpConnector(vals))
			if tool["tool_name"] != "single_tool" {
				t.Errorf("tool_name = %v, want single_tool", tool["tool_name"])
			}
			if tool["tool_name_prefix"] != "atlassian_" {
				t.Errorf("tool_name_prefix = %v, want atlassian_", tool["tool_name_prefix"])
			}
		})

		t.Run(ac.name+" manifest", func(t *testing.T) {
			t.Parallel()
			manifest := []any{map[string]any{"name": "explicit_tool", "description": "stuff"}}
			vals := map[string]any{
				"server_url": "https://mcp.example.com",
				"manifest":   manifest,
			}
			maps.Copy(vals, ac.extraVals)
			tool := buildConnectorTool(mcpConnector(vals))
			if tool["manifest"] == nil {
				t.Fatal("manifest should be present in output")
			}
			got, ok := tool["manifest"].([]any)
			if !ok {
				t.Fatalf("manifest is not []any: %T", tool["manifest"])
			}
			if len(got) != 1 {
				t.Fatalf("manifest len = %d, want 1", len(got))
			}
		})

		t.Run(ac.name+" no filter fields", func(t *testing.T) {
			t.Parallel()
			vals := map[string]any{
				"server_url": "https://mcp.example.com",
			}
			maps.Copy(vals, ac.extraVals)
			tool := buildConnectorTool(mcpConnector(vals))
			for _, key := range []string{"tool_name", "tool_name_prefix", "allow_list", "deny_list", "manifest"} {
				if _, present := tool[key]; present {
					t.Errorf("%s should be absent when not configured", key)
				}
			}
		})
	}
}

func TestBuildMCPConnectorTool_SelectedTools(t *testing.T) {
	t.Parallel()

	manifest := func() []any {
		return []any{
			map[string]any{"name": "supabase_query", "description": "query"},
			map[string]any{"name": "deploy_edge_function", "description": "deploy"},
			map[string]any{"name": "list_buckets", "description": "list"},
		}
	}

	names := func(t *testing.T, tool map[string]any) []string {
		t.Helper()
		return manifestNames(t, tool["manifest"])
	}

	t.Run("csv selection trims to selected tools in selection order", func(t *testing.T) {
		t.Parallel()
		tool := buildConnectorTool(mcpConnector(map[string]any{
			"server_url":     "https://mcp.example.com",
			"manifest":       manifest(),
			"selected_tools": "list_buckets, supabase_query",
		}))
		got := names(t, tool)
		want := []string{"list_buckets", "supabase_query"}
		if !slices.Equal(got, want) {
			t.Errorf("manifest tools = %v, want %v (selection order, disabled tool dropped)", got, want)
		}
	})

	t.Run("json-string manifest is also filtered", func(t *testing.T) {
		t.Parallel()
		jsonManifest := `[{"name":"supabase_query"},{"name":"deploy_edge_function"},{"name":"list_buckets"}]`
		tool := buildConnectorTool(mcpConnector(map[string]any{
			"server_url":     "https://mcp.example.com",
			"manifest":       jsonManifest,
			"selected_tools": "supabase_query,list_buckets",
		}))
		got := names(t, tool)
		if slices.Contains(got, "deploy_edge_function") {
			t.Errorf("deploy_edge_function disabled but present in filtered json-string manifest: %v", got)
		}
		if !slices.Equal(got, []string{"supabase_query", "list_buckets"}) {
			t.Errorf("manifest tools = %v, want [supabase_query list_buckets]", got)
		}
	})

	t.Run("disabled tool is absent from emitted manifest", func(t *testing.T) {
		t.Parallel()
		tool := buildConnectorTool(mcpConnector(map[string]any{
			"server_url":     "https://mcp.example.com",
			"manifest":       manifest(),
			"selected_tools": "supabase_query,list_buckets",
		}))
		if slices.Contains(names(t, tool), "deploy_edge_function") {
			t.Error("deploy_edge_function was disabled but is still present in the manifest")
		}
	})

	t.Run("slice selection (declarative shape) trims manifest", func(t *testing.T) {
		t.Parallel()
		tool := buildConnectorTool(mcpConnector(map[string]any{
			"server_url":     "https://mcp.example.com",
			"manifest":       manifest(),
			"selected_tools": []any{"supabase_query"},
		}))
		if got := names(t, tool); !slices.Equal(got, []string{"supabase_query"}) {
			t.Errorf("manifest tools = %v, want [supabase_query]", got)
		}
	})

	t.Run("single selected tool", func(t *testing.T) {
		t.Parallel()
		tool := buildConnectorTool(mcpConnector(map[string]any{
			"server_url":     "https://mcp.example.com",
			"manifest":       manifest(),
			"selected_tools": "deploy_edge_function",
		}))
		if got := names(t, tool); !slices.Equal(got, []string{"deploy_edge_function"}) {
			t.Errorf("manifest tools = %v, want [deploy_edge_function]", got)
		}
	})

	t.Run("absent selection emits full manifest", func(t *testing.T) {
		t.Parallel()
		tool := buildConnectorTool(mcpConnector(map[string]any{
			"server_url": "https://mcp.example.com",
			"manifest":   manifest(),
		}))
		if got := names(t, tool); len(got) != 3 {
			t.Errorf("manifest tools = %v, want all 3 when no selection", got)
		}
	})

	t.Run("empty-string selection emits full manifest (defensive)", func(t *testing.T) {
		t.Parallel()
		tool := buildConnectorTool(mcpConnector(map[string]any{
			"server_url":     "https://mcp.example.com",
			"manifest":       manifest(),
			"selected_tools": "",
		}))
		if got := names(t, tool); len(got) != 3 {
			t.Errorf("manifest tools = %v, want all 3 when selection empty", got)
		}
	})
}

func manifestNames(t *testing.T, raw any) []string {
	t.Helper()
	switch v := raw.(type) {
	case []map[string]any:
		out := make([]string, 0, len(v))
		for _, m := range v {
			out = append(out, m["name"].(string))
		}
		return out
	case []any:
		out := make([]string, 0, len(v))
		for _, e := range v {
			m, ok := e.(map[string]any)
			if !ok {
				t.Fatalf("manifest entry not a map: %#v", e)
			}
			out = append(out, m["name"].(string))
		}
		return out
	default:
		t.Fatalf("manifest missing or wrong type: %#v", raw)
		return nil
	}
}

func TestFilterManifestBySelection(t *testing.T) {
	t.Parallel()

	t.Run("unparseable manifest type passes through unchanged", func(t *testing.T) {
		t.Parallel()
		got := filterManifestBySelection("not-json", "a")
		if got != "not-json" {
			t.Errorf("got %#v, want input returned unchanged", got)
		}
	})

	t.Run("entry without name is dropped, no panic", func(t *testing.T) {
		t.Parallel()
		in := []any{
			map[string]any{"description": "no name here"},
			map[string]any{"name": "keep_me"},
		}
		if got := manifestNames(t, filterManifestBySelection(in, "keep_me")); !slices.Equal(got, []string{"keep_me"}) {
			t.Errorf("got %v, want [keep_me]", got)
		}
	})

	t.Run("nil selection returns manifest unchanged", func(t *testing.T) {
		t.Parallel()
		in := []any{map[string]any{"name": "a"}}
		got, ok := filterManifestBySelection(in, nil).([]any)
		if !ok || len(got) != 1 {
			t.Fatalf("got %#v, want original []any returned unchanged", got)
		}
	})

	t.Run("selection matching nothing yields empty manifest", func(t *testing.T) {
		t.Parallel()
		in := []any{map[string]any{"name": "a"}}
		if got := manifestNames(t, filterManifestBySelection(in, "z")); len(got) != 0 {
			t.Fatalf("got %v, want empty manifest", got)
		}
	})

	t.Run("json-string manifest is parsed and filtered", func(t *testing.T) {
		t.Parallel()
		in := `[{"name":"a"},{"name":"b"}]`
		if got := manifestNames(t, filterManifestBySelection(in, "b")); !slices.Equal(got, []string{"b"}) {
			t.Errorf("got %v, want [b]", got)
		}
	})
}

func TestBuildMCPConnectorTool_HIL_AllAuthBranches(t *testing.T) {
	t.Parallel()

	authConfigs := []struct {
		name      string
		extraVals map[string]any
	}{
		{name: "no auth", extraVals: map[string]any{}},
		{
			name: "apikey",
			extraVals: map[string]any{
				"auth_type":            "apikey",
				"auth_apikey_location": "header",
				"auth_apikey_name":     "X-Api-Key",
				"auth_apikey_value":    "k",
			},
		},
		{
			name: "http bearer",
			extraVals: map[string]any{
				"auth_type":              "http",
				"auth_http_scheme":       "bearer",
				"auth_http_bearer_token": "tok",
			},
		},
		{
			name: "oauth",
			extraVals: map[string]any{
				"auth_type":                    "oauth",
				"auth_oauth_authorization_url": "https://auth.example.com/authorize",
				"auth_oauth_token_url":         "https://auth.example.com/token",
				"auth_oauth_client_id":         "client",
				"auth_oauth_client_secret":     "secret",
			},
		},
	}

	hilStructured := map[string]any{
		"tools": map[string]any{
			"deleteIssue": map[string]any{
				"require_approval": true,
				"approval_message": "About to delete {issueKey}. Confirm?",
				"show_args":        true,
				"timeout":          "30m",
			},
			"transitionIssue": map[string]any{
				"require_approval": true,
			},
		},
	}

	hilStringBlob := `
tools:
  deleteIssue:
    require_approval: true
    approval_message: "About to delete {issueKey}. Confirm?"
    show_args: true
    timeout: "30m"
  transitionIssue:
    require_approval: true
`

	for _, ac := range authConfigs {
		t.Run(ac.name+" hil structured", func(t *testing.T) {
			t.Parallel()
			vals := map[string]any{
				"server_url": "https://mcp.example.com",
				"hil":        hilStructured,
			}
			maps.Copy(vals, ac.extraVals)
			tool := buildConnectorTool(mcpConnector(vals))
			got, ok := tool["hil"].(map[string]any)
			if !ok {
				t.Fatalf("hil missing or wrong type: %#v", tool["hil"])
			}
			toolsMap, ok := got["tools"].(map[string]any)
			if !ok {
				t.Fatalf("hil.tools missing or wrong type: %#v", got["tools"])
			}
			if _, ok := toolsMap["deleteIssue"].(map[string]any); !ok {
				t.Errorf("hil.tools.deleteIssue missing or wrong type: %#v", toolsMap["deleteIssue"])
			}
			if _, ok := toolsMap["transitionIssue"].(map[string]any); !ok {
				t.Errorf("hil.tools.transitionIssue missing or wrong type: %#v", toolsMap["transitionIssue"])
			}
		})

		t.Run(ac.name+" hil string blob", func(t *testing.T) {
			t.Parallel()
			vals := map[string]any{
				"server_url": "https://mcp.example.com",
				"hil":        hilStringBlob,
			}
			maps.Copy(vals, ac.extraVals)
			tool := buildConnectorTool(mcpConnector(vals))
			got, ok := tool["hil"].(map[string]any)
			if !ok {
				t.Fatalf("hil missing or wrong type after parse: %#v", tool["hil"])
			}
			toolsMap, ok := got["tools"].(map[string]any)
			if !ok {
				t.Fatalf("hil.tools missing or wrong type: %#v", got["tools"])
			}
			delEntry, ok := toolsMap["deleteIssue"].(map[string]any)
			if !ok {
				t.Fatalf("hil.tools.deleteIssue missing or wrong type: %#v", toolsMap["deleteIssue"])
			}
			if delEntry["require_approval"] != true {
				t.Errorf("require_approval = %v, want true", delEntry["require_approval"])
			}
			if delEntry["timeout"] != "30m" {
				t.Errorf("timeout = %v, want 30m", delEntry["timeout"])
			}
		})

		t.Run(ac.name+" hil unset omits key", func(t *testing.T) {
			t.Parallel()
			vals := map[string]any{
				"server_url": "https://mcp.example.com",
			}
			maps.Copy(vals, ac.extraVals)
			tool := buildConnectorTool(mcpConnector(vals))
			if _, present := tool["hil"]; present {
				t.Errorf("hil should be absent when not configured")
			}
		})

		t.Run(ac.name+" hil empty string omits key", func(t *testing.T) {
			t.Parallel()
			vals := map[string]any{
				"server_url": "https://mcp.example.com",
				"hil":        "   ",
			}
			maps.Copy(vals, ac.extraVals)
			tool := buildConnectorTool(mcpConnector(vals))
			if _, present := tool["hil"]; present {
				t.Errorf("whitespace-only hil should be omitted")
			}
		})
	}
}

func TestBuildMCPConnectorTool_HIL_CombinedWithFilters(t *testing.T) {
	t.Parallel()
	tool := buildConnectorTool(mcpConnector(map[string]any{
		"server_url":       "https://mcp.example.com",
		"tool_name_prefix": "atlassian_",
		"deny_list":        "confluence_search",
		"hil": map[string]any{
			"tools": map[string]any{
				"deleteIssue": map[string]any{"require_approval": true},
			},
		},
	}))
	if tool["tool_name_prefix"] != "atlassian_" {
		t.Errorf("tool_name_prefix lost: %v", tool["tool_name_prefix"])
	}
	if dl, _ := tool["deny_list"].([]string); len(dl) != 1 || dl[0] != "confluence_search" {
		t.Errorf("deny_list lost: %v", tool["deny_list"])
	}
	if _, ok := tool["hil"].(map[string]any); !ok {
		t.Errorf("hil missing or wrong type alongside filters: %#v", tool["hil"])
	}
}

func TestBuildMCPConnectorTool_AllowListFromSlice(t *testing.T) {
	t.Parallel()
	tool := buildConnectorTool(mcpConnector(map[string]any{
		"server_url": "https://mcp.example.com",
		"allow_list": []any{"a", "b"},
	}))
	got, ok := tool["allow_list"].([]string)
	if !ok {
		t.Fatalf("allow_list missing or wrong type: %#v", tool["allow_list"])
	}
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Errorf("allow_list = %v, want [a b]", got)
	}
}

func TestBuildAPIConnectorTool_FlatShape(t *testing.T) {
	t.Parallel()
	tool := buildConnectorTool(apiConnector(map[string]any{
		"specification_url": "https://api.example.com/spec.yaml",
		"base_url":          "https://api.example.com",
	}))
	assertToolField(t, tool, "tool_type", "openapi")
	assertToolField(t, tool, "specification_url", "objstore://"+testAPIConnectorResourceKey)
	assertToolField(t, tool, "base_url", "https://api.example.com")
	if _, wrapped := tool["tool_config"]; wrapped {
		t.Fatal("openapi tool must NOT wrap keys under tool_config — the AWE dispatcher would miss them at runtime")
	}
	if _, hasName := tool["tool_name"]; hasName {
		t.Error("openapi tool should not emit tool_name (one tool per operation is registered at runtime)")
	}
	if _, hasTimeout := tool["timeout_seconds"]; hasTimeout {
		t.Error("openapi tool should not emit timeout_seconds (STR-only)")
	}
	if _, hasPrefix := tool["tool_name_prefix"]; hasPrefix {
		t.Error("openapi tool should not emit tool_name_prefix — Python has no equivalent; collision detection happens at save-time validation")
	}
	if _, hasSpecURL := tool["spec_url"]; hasSpecURL {
		t.Error("openapi tool should emit Python-canonical `specification_url`, not Go-legacy `spec_url`")
	}
}

func TestBuildConnectorTools_StampsSourceConnectorID(t *testing.T) {
	t.Parallel()

	api := apiConnector(map[string]any{
		"specification_url": "https://api.example.com/spec.yaml",
		"base_url":          "https://api.example.com",
	})
	assertToolField(t, buildConnectorTool(api), "source_connector_id", api.ID)

	mcp := mcpConnector(map[string]any{"server_url": "https://mcp.example.com/mcp"})
	assertToolField(t, buildConnectorTool(mcp), "source_connector_id", mcp.ID)

	sql := buildConnectorTool(sqlConnector("postgres", baseSQLValues()))
	if _, ok := sql["source_connector_id"]; ok {
		t.Error("sql connector entries are builtin tools and must not carry source_connector_id")
	}
}

func TestBuildAPIConnectorTool_UnsetAuthTypeLeavesAuthToSpecAutoWire(t *testing.T) {
	t.Parallel()
	tool := buildConnectorTool(apiConnector(map[string]any{
		"specification_url": "https://api.example.com/spec.yaml",
		"base_url":          "https://api.example.com",
	}))
	if _, hasAuth := tool["auth"]; hasAuth {
		t.Error("auth block should be absent when auth_type is unset")
	}
	if _, hasAuthenticationKey := tool["authentication"]; hasAuthenticationKey {
		t.Error("emitter should use Python key `auth:`, not `authentication:`")
	}
}

func TestBuildAPIConnectorTool_BearerAuth(t *testing.T) {
	t.Parallel()
	tool := buildConnectorTool(apiConnector(map[string]any{
		"specification_url":      "https://api.example.com/spec.yaml",
		"base_url":               "https://api.example.com",
		"auth_type":              "http",
		"auth_http_scheme":       "bearer",
		"auth_http_bearer_token": "token-xyz",
	}))
	auth := tool["auth"].(map[string]any)
	assertToolField(t, auth, "type", "bearer")
	assertToolField(t, auth, "token", "token-xyz")
}

func TestBuildAPIConnectorTool_BasicAuth(t *testing.T) {
	t.Parallel()
	tool := buildConnectorTool(apiConnector(map[string]any{
		"specification_url":        "https://api.example.com/spec.yaml",
		"base_url":                 "https://api.example.com",
		"auth_type":                "http",
		"auth_http_scheme":         "basic",
		"auth_http_basic_username": "user",
		"auth_http_basic_password": "pass",
	}))
	auth := tool["auth"].(map[string]any)
	assertToolField(t, auth, "type", "basic")
	assertToolField(t, auth, "username", "user")
	assertToolField(t, auth, "password", "pass")
}

func TestBuildAPIConnectorTool_APIKeyAuth(t *testing.T) {
	t.Parallel()
	tool := buildConnectorTool(apiConnector(map[string]any{
		"specification_url":    "https://api.example.com/spec.yaml",
		"base_url":             "https://api.example.com",
		"auth_type":            "apikey",
		"auth_apikey_location": "header",
		"auth_apikey_name":     "X-Api-Key",
		"auth_apikey_value":    "key-abc",
	}))
	auth := tool["auth"].(map[string]any)
	assertToolField(t, auth, "type", "apikey")
	assertToolField(t, auth, "in", "header")
	assertToolField(t, auth, "name", "X-Api-Key")
	assertToolField(t, auth, "value", "key-abc")
}

func TestBuildAPIConnectorTool_WithAllowList(t *testing.T) {
	t.Parallel()
	tool := buildConnectorTool(apiConnector(map[string]any{
		"specification_url": "https://api.example.com/spec.yaml",
		"base_url":          "https://api.example.com",
		"allow_list":        "getUser, createUser, deleteUser",
	}))
	ops, ok := tool["allow_list"].([]string)
	if !ok {
		t.Fatalf("allow_list missing or wrong type: %T", tool["allow_list"])
	}
	if len(ops) != 3 {
		t.Errorf("allow_list length = %d, want 3", len(ops))
	}
	if ops[0] != "getUser" {
		t.Errorf("ops[0] = %q", ops[0])
	}
}

func TestBuildAPIConnectorTool_FallbackSpecURL(t *testing.T) {
	t.Parallel()
	conn := ConnectorConfig{
		ID:          "770e8400-e29b-41d4-a716-446655440000",
		Type:        "api",
		Values:      map[string]any{"base_url": "https://api.example.com"},
		ResourceKey: "specs/my-api.yaml",
	}
	tool := buildConnectorTool(conn)
	specURL := tool["specification_url"].(string)
	if specURL == "" {
		t.Error("specification_url should have a value from ResourceKey fallback")
	}
}

func TestBuildAPIConnectorTool_OAuthAuth(t *testing.T) {
	t.Parallel()
	tool := buildConnectorTool(apiConnector(map[string]any{
		"specification_url":                     "https://api.example.com/spec.yaml",
		"base_url":                              "https://api.example.com",
		"auth_type":                             "oauth",
		"auth_oauth_authorization_url":          "https://auth.example.com/authorize",
		"auth_oauth_token_url":                  "https://auth.example.com/token",
		"auth_oauth_client_id":                  "client-id",
		"auth_oauth_client_secret":              "client-secret",
		"auth_oauth_scopes":                     "read write",
		"auth_oauth_token_endpoint_auth_method": "client_secret_basic",
	}))
	auth := tool["auth"].(map[string]any)
	assertToolField(t, auth, "type", "oauth2")
	assertToolField(t, auth, "authorization_url", "https://auth.example.com/authorize")
	assertToolField(t, auth, "token_url", "https://auth.example.com/token")
	assertToolField(t, auth, "client_id", "client-id")
	assertToolField(t, auth, "client_secret", "client-secret")
	assertToolField(t, auth, "token_endpoint_auth_method", "client_secret_basic")

	scopes, ok := auth["scopes"].([]string)
	if !ok {
		t.Fatalf("scopes missing or wrong type: %T", auth["scopes"])
	}
	if len(scopes) != 2 || scopes[0] != "read" || scopes[1] != "write" {
		t.Errorf("scopes = %v, want [read write]", scopes)
	}
	if _, ok := auth["scheme"]; ok {
		t.Error("nested scheme block should not be emitted for openapi tool_type")
	}
	if _, ok := auth["credential"]; ok {
		t.Error("nested credential block should not be emitted for openapi tool_type")
	}
}

func TestBuildAPIConnectorTool_OAuthAuth_ClientSecretOptional(t *testing.T) {
	t.Parallel()
	tool := buildConnectorTool(apiConnector(map[string]any{
		"specification_url":            "https://api.example.com/spec.yaml",
		"base_url":                     "https://api.example.com",
		"auth_type":                    "oauth",
		"auth_oauth_authorization_url": "https://auth.example.com/authorize",
		"auth_oauth_token_url":         "https://auth.example.com/token",
		"auth_oauth_client_id":         "client-id",
	}))
	auth := tool["auth"].(map[string]any)
	if _, has := auth["client_secret"]; has {
		t.Errorf("client_secret should be omitted for PKCE-style public clients; got %v", auth["client_secret"])
	}
	if _, has := auth["refresh_url"]; has {
		t.Errorf("refresh_url should be omitted when not configured; got %v", auth["refresh_url"])
	}
}

func TestBuildAPIConnectorTool_CustomHeaders(t *testing.T) {
	t.Parallel()
	tool := buildConnectorTool(apiConnector(map[string]any{
		"specification_url": "https://api.example.com/spec.yaml",
		"base_url":          "https://api.example.com",
		"custom_headers":    map[string]any{"X-Custom": "val"},
	}))
	headers, ok := tool["headers"].(map[string]any)
	if !ok {
		t.Fatal("headers missing")
	}
	if headers["X-Custom"] != "val" {
		t.Errorf("X-Custom = %v", headers["X-Custom"])
	}
}

func TestBuildKBConnectorTool_IAMAuth(t *testing.T) {
	t.Parallel()
	tool := buildConnectorTool(kbConnector(map[string]any{
		"kb_id":          "kb-12345",
		"region":         "us-east-1",
		"auth_type":      "iam",
		"aws_account_id": "123456789012",
		"role_name":      "my-role",
		"external_id":    "ext-id",
		"session_name":   "my-session",
	}))
	assertToolField(t, tool, "tool_type", "builtin")
	assertToolField(t, tool, "tool_name", "query_bedrock_kb_880e8400")
	tc := tool["tool_config"].(map[string]any)
	assertToolField(t, tc, "_str_binary", "query_bedrock_kb")
	if tc["kb_id"] != "kb-12345" {
		t.Errorf("kb_id = %v", tc["kb_id"])
	}
	if tc["region"] != "us-east-1" {
		t.Errorf("region = %v", tc["region"])
	}
	roleArn := tc["role_arn"].(string)
	assertStringContains(t, roleArn, "arn:aws:iam::123456789012:role/my-role")
	if tc["external_id"] != "ext-id" {
		t.Errorf("external_id = %v", tc["external_id"])
	}
	if tc["session_name"] != "my-session" {
		t.Errorf("session_name = %v", tc["session_name"])
	}
	if tc["max_results"] != 10 {
		t.Errorf("max_results = %v", tc["max_results"])
	}
}

func TestBuildKBConnectorTool_IAMAuth_NoOptionalFields(t *testing.T) {
	t.Parallel()
	tool := buildConnectorTool(kbConnector(map[string]any{
		"kb_id":          "kb-12345",
		"region":         "us-east-1",
		"auth_type":      "iam",
		"aws_account_id": "123456789012",
		"role_name":      "my-role",
	}))
	tc := tool["tool_config"].(map[string]any)
	if _, has := tc["external_id"]; has {
		t.Error("external_id should not be set when not provided")
	}
	if _, has := tc["session_name"]; has {
		t.Error("session_name should not be set when not provided")
	}
}

func TestBuildKBConnectorTool_AccessKeyAuth(t *testing.T) {
	t.Parallel()
	tool := buildConnectorTool(kbConnector(map[string]any{
		"kb_id":                 "kb-12345",
		"region":                "us-west-2",
		"auth_type":             "access_key",
		"aws_access_key_id":     "AKIAIOSFODNN7EXAMPLE",
		"aws_secret_access_key": "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLE",
	}))
	tc := tool["tool_config"].(map[string]any)
	if tc["aws_access_key_id"] != "AKIAIOSFODNN7EXAMPLE" {
		t.Errorf("aws_access_key_id = %v", tc["aws_access_key_id"])
	}
	if tc["aws_secret_access_key"] != "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLE" {
		t.Errorf("aws_secret_access_key = %v", tc["aws_secret_access_key"])
	}
	if _, has := tc["role_arn"]; has {
		t.Error("role_arn should not be set for access_key auth")
	}
}

func TestBuildKBConnectorTool_ToolNameIncludesIDPrefix(t *testing.T) {
	t.Parallel()
	tool := buildConnectorTool(kbConnector(map[string]any{
		"kb_id":  "kb-12345",
		"region": "us-east-1",
	}))
	assertToolField(t, tool, "tool_name", "query_bedrock_kb_880e8400")
	tc := tool["tool_config"].(map[string]any)
	toolName := tc["tool_name"].(string)
	assertStringContains(t, toolName, "query_bedrock_kb_")
	assertStringContains(t, toolName, "880e8400")
	assertToolField(t, tc, "_str_binary", "query_bedrock_kb")
}

func TestBuildKBConnectorTool_IAMAuth_EnvVarFallback(t *testing.T) {
	t.Parallel()
	tool := buildConnectorTool(kbConnector(map[string]any{
		"auth_type":      "iam",
		"aws_account_id": "${AWS_ACCOUNT}",
		"role_name":      "${ROLE}",
	}))
	tc := tool["tool_config"].(map[string]any)
	roleArn := tc["role_arn"].(string)
	assertStringContains(t, roleArn, "${ROLE_ARN_")
}

func TestBuildConnectorTool_UnknownType(t *testing.T) {
	t.Parallel()
	tool := buildConnectorTool(ConnectorConfig{
		ID:     "test-id",
		Type:   "webhook",
		Values: map[string]any{},
	})
	if tool != nil {
		t.Errorf("unknown type should return nil, got %v", tool)
	}
}

func TestBuildConnectorTool_TimeoutSeconds(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		conn ConnectorConfig
	}{
		{"sql", sqlConnector("postgres", baseSQLValues())},
		{"knowledge_base", kbConnector(map[string]any{
			"kb_id":                 "KB123",
			"region":                "us-east-1",
			"aws_access_key_id":     "AKIA",
			"aws_secret_access_key": "secret",
		})},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tool := buildConnectorTool(tc.conn)
			if tool["tool_type"] != "builtin" {
				t.Fatalf("expected builtin, got %v", tool["tool_type"])
			}
			timeout, ok := tool["timeout_seconds"].(int)
			if !ok {
				t.Fatalf("timeout_seconds missing or wrong type: %v (%T)", tool["timeout_seconds"], tool["timeout_seconds"])
			}
			if timeout < 60 {
				t.Errorf("timeout_seconds = %d, want >= 60", timeout)
			}
		})
	}
}

func slackConnector(values map[string]any) ConnectorConfig {
	return ConnectorConfig{
		ID:     "990e8400-e29b-41d4-a716-446655440000",
		Type:   "slack",
		Values: values,
	}
}

func TestBuildSlackConnectorTool_Basic(t *testing.T) {
	t.Parallel()
	tool := buildConnectorTool(slackConnector(map[string]any{
		"slack_bot_token": "xoxb-test-token",
	}))
	assertToolField(t, tool, "tool_type", "builtin")
	assertToolField(t, tool, "tool_name", "slack_send_message_990e8400")
	tc := tool["tool_config"].(map[string]any)
	assertToolField(t, tc, "slack_bot_token", "xoxb-test-token")
	assertToolField(t, tc, "_str_binary", "slack")
	assertToolField(t, tc, "tool_name", "slack_send_message_990e8400")
	assertToolField(t, tc, "tool_description", "Send messages to Slack channels.")
}

func TestBuildSlackConnectorTool_CustomToolName(t *testing.T) {
	t.Parallel()
	tool := buildConnectorTool(slackConnector(map[string]any{
		"slack_bot_token": "xoxb-test-token",
		"tool_name":       "my_custom_slack",
	}))
	assertToolField(t, tool, "tool_name", "my_custom_slack")
	tc := tool["tool_config"].(map[string]any)
	assertToolField(t, tc, "tool_name", "my_custom_slack")
}

func TestBuildSlackConnectorTool_EnvVarFallback(t *testing.T) {
	t.Parallel()
	tool := buildConnectorTool(slackConnector(map[string]any{}))
	tc := tool["tool_config"].(map[string]any)
	token := tc["slack_bot_token"].(string)
	if !strings.HasPrefix(token, "${SLACK_BOT_TOKEN_") {
		t.Errorf("expected env var reference, got %q", token)
	}
}

func TestBuildSlackConnectorTool_Timeout(t *testing.T) {
	t.Parallel()
	tool := buildConnectorTool(slackConnector(map[string]any{
		"slack_bot_token": "xoxb-test-token",
	}))
	assertToolField(t, tool, "timeout_seconds", 30)
}

func TestBuildSlackConnectorTool_STRBinary(t *testing.T) {
	t.Parallel()
	tool := buildConnectorTool(slackConnector(map[string]any{
		"slack_bot_token": "xoxb-test-token",
	}))
	tc := tool["tool_config"].(map[string]any)
	assertToolField(t, tc, "_str_binary", "slack")
}

func TestBuildSlackConnectorTools_EmitsTwoTools(t *testing.T) {
	t.Parallel()
	tools := buildConnectorTools(slackConnector(map[string]any{
		"slack_bot_token": "xoxb-test-token",
	}))
	if len(tools) != 2 {
		t.Fatalf("expected 2 tools, got %d", len(tools))
	}

	send := tools[0]
	assertToolField(t, send, "tool_type", "builtin")
	assertToolField(t, send, "tool_name", "slack_send_message_990e8400")
	assertToolField(t, send, "timeout_seconds", 30)
	sendCfg := send["tool_config"].(map[string]any)
	assertToolField(t, sendCfg, "_str_binary", "slack")
	assertToolField(t, sendCfg, "slack_bot_token", "xoxb-test-token")

	update := tools[1]
	assertToolField(t, update, "tool_type", "builtin")
	assertToolField(t, update, "tool_name", "slack_update_990e8400")
	assertToolField(t, update, "timeout_seconds", 30)
	updateCfg := update["tool_config"].(map[string]any)
	assertToolField(t, updateCfg, "slack_bot_token", "xoxb-test-token")
	assertToolField(t, updateCfg, "_str_binary", "slack_update")
}

func TestBuildAPIConnectorTool_NoTimeoutSeconds(t *testing.T) {
	t.Parallel()
	tool := buildConnectorTool(apiConnector(map[string]any{
		"base_url":          "https://api.example.com",
		"specification_url": "https://api.example.com/openapi.json",
	}))
	if _, has := tool["timeout_seconds"]; has {
		t.Errorf("openapi tool must not emit timeout_seconds (STR-only); got %v", tool["timeout_seconds"])
	}
}

func documentDBConnector(subtype string, values map[string]any) ConnectorConfig {
	return ConnectorConfig{
		ID:      "990e8400-e29b-41d4-a716-446655440000",
		Type:    "document_db",
		Subtype: subtype,
		Values:  values,
	}
}

func TestBuildMongoDBConnectorTool_BasicFields(t *testing.T) {
	t.Parallel()
	tool := buildConnectorTool(documentDBConnector("mongodb", map[string]any{
		"scheme":     "mongodb+srv",
		"hostname":   "cluster.example.net",
		"username":   "user",
		"password":   "pass",
		"database":   "mydb",
		"collection": "orders",
	}))
	assertToolField(t, tool, "tool_type", "builtin")
	assertToolField(t, tool, "tool_name", "orders_mongo_query_990e8400")
	tc := tool["tool_config"].(map[string]any)
	assertToolField(t, tc, "backend", "mongodb")
	assertToolField(t, tc, "_str_binary", "document_db_query")
	assertToolField(t, tc, "connection_string", "mongodb+srv://user:pass@cluster.example.net/mydb")
	assertToolField(t, tc, "database", "mydb")
	assertToolField(t, tc, "collection", "orders")
	assertToolField(t, tc, "tool_name", "orders_mongo_query_990e8400")
	assertStringContains(t, tc["tool_description"].(string), "orders")
}

func TestBuildMongoDBConnectorTool_StandardSchemeWithPortAndOptions(t *testing.T) {
	t.Parallel()
	tool := buildConnectorTool(documentDBConnector("mongodb", map[string]any{
		"scheme":     "mongodb",
		"hostname":   "db.internal",
		"port":       27018,
		"username":   "svc",
		"password":   "p@ss/word",
		"database":   "orders",
		"collection": "events",
		"options":    "authSource=admin&tls=true",
	}))
	tc := tool["tool_config"].(map[string]any)
	assertToolField(t, tc, "connection_string", "mongodb://svc:p%40ss%2Fword@db.internal:27018/orders?authSource=admin&tls=true")
}

func TestBuildMongoDBConnectorTool_NoCredentials(t *testing.T) {
	t.Parallel()
	tool := buildConnectorTool(documentDBConnector("mongodb", map[string]any{
		"hostname":   "localhost",
		"port":       27017,
		"collection": "events",
	}))
	tc := tool["tool_config"].(map[string]any)
	assertToolField(t, tc, "connection_string", "mongodb://localhost:27017/")
}

func TestBuildMongoDBConnectorTool_OmitsDatabaseWhenBlank(t *testing.T) {
	t.Parallel()
	tool := buildConnectorTool(documentDBConnector("mongodb", map[string]any{
		"hostname":   "localhost",
		"port":       27017,
		"collection": "events",
	}))
	tc := tool["tool_config"].(map[string]any)
	if _, has := tc["database"]; has {
		t.Error("database should not be set when not provided")
	}
}

func TestBuildMongoDBConnectorTool_FallbackToolName(t *testing.T) {
	t.Parallel()
	tool := buildConnectorTool(documentDBConnector("mongodb", map[string]any{
		"hostname": "localhost",
	}))
	assertToolField(t, tool, "tool_name", "mongo_query_990e8400")
}

func TestBuildMongoDBConnectorTool_EnvVarFallback(t *testing.T) {
	t.Parallel()
	tool := buildConnectorTool(documentDBConnector("mongodb", map[string]any{
		"collection": "orders",
	}))
	tc := tool["tool_config"].(map[string]any)
	connStr := tc["connection_string"].(string)
	assertStringContains(t, connStr, "${HOSTNAME_")
}

func TestBuildMongoDBConnectorTool_LegacyConnectionStringWins(t *testing.T) {
	t.Parallel()
	tool := buildConnectorTool(documentDBConnector("mongodb", map[string]any{
		"connection_string": "mongodb+srv://user:pass@legacy.example.net/db",
		"collection":        "orders",
	}))
	tc := tool["tool_config"].(map[string]any)
	assertToolField(t, tc, "connection_string", "mongodb+srv://user:pass@legacy.example.net/db")
}

func TestBuildDynamoDBConnectorTool_AccessKeyAuth(t *testing.T) {
	t.Parallel()
	tool := buildConnectorTool(documentDBConnector("dynamodb", map[string]any{
		"region":                "us-west-2",
		"table_name":            "Orders",
		"auth_type":             "access_key",
		"aws_access_key_id":     "AKIAIOSFODNN7EXAMPLE",
		"aws_secret_access_key": "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLE",
	}))
	assertToolField(t, tool, "tool_type", "builtin")
	assertToolField(t, tool, "tool_name", "Orders_dynamodb_query_990e8400")
	tc := tool["tool_config"].(map[string]any)
	assertToolField(t, tc, "backend", "dynamodb")
	assertToolField(t, tc, "_str_binary", "document_db_query")
	assertToolField(t, tc, "region", "us-west-2")
	assertToolField(t, tc, "table_name", "Orders")
	assertToolField(t, tc, "aws_access_key_id", "AKIAIOSFODNN7EXAMPLE")
	assertToolField(t, tc, "aws_secret_access_key", "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLE")
	if _, has := tc["role_arn"]; has {
		t.Error("role_arn should not be set for access_key auth")
	}
}

func TestBuildDynamoDBConnectorTool_IAMAuth(t *testing.T) {
	t.Parallel()
	tool := buildConnectorTool(documentDBConnector("dynamodb", map[string]any{
		"region":       "us-east-1",
		"table_name":   "Events",
		"auth_type":    "iam",
		"role_arn":     "arn:aws:iam::123456789012:role/SolaceDDB",
		"session_name": "ddb-session",
		"external_id":  "ext-id",
	}))
	tc := tool["tool_config"].(map[string]any)
	assertToolField(t, tc, "role_arn", "arn:aws:iam::123456789012:role/SolaceDDB")
	assertToolField(t, tc, "session_name", "ddb-session")
	assertToolField(t, tc, "external_id", "ext-id")
	if _, has := tc["aws_access_key_id"]; has {
		t.Error("aws_access_key_id should not be set for iam auth")
	}
}

func TestBuildDynamoDBConnectorTool_IAMAuth_NoOptionalFields(t *testing.T) {
	t.Parallel()
	tool := buildConnectorTool(documentDBConnector("dynamodb", map[string]any{
		"region":     "us-east-1",
		"table_name": "Events",
		"auth_type":  "iam",
		"role_arn":   "arn:aws:iam::123456789012:role/SolaceDDB",
	}))
	tc := tool["tool_config"].(map[string]any)
	if _, has := tc["session_name"]; has {
		t.Error("session_name should not be set when not provided")
	}
	if _, has := tc["external_id"]; has {
		t.Error("external_id should not be set when not provided")
	}
}

func TestBuildDynamoDBConnectorTool_FallbackToolName(t *testing.T) {
	t.Parallel()
	tool := buildConnectorTool(documentDBConnector("dynamodb", map[string]any{
		"region":    "us-east-1",
		"auth_type": "access_key",
	}))
	assertToolField(t, tool, "tool_name", "dynamodb_query_990e8400")
}

func TestBuildDocumentDBConnectorTool_UnknownSubtype(t *testing.T) {
	t.Parallel()
	tool := buildConnectorTool(documentDBConnector("cassandra", map[string]any{}))
	if tool != nil {
		t.Errorf("unknown subtype should return nil, got %v", tool)
	}
}

func TestBuildDocumentDBConnectorTool_TimeoutSeconds(t *testing.T) {
	t.Parallel()
	cases := []ConnectorConfig{
		documentDBConnector("mongodb", map[string]any{
			"hostname":   "localhost",
			"collection": "c",
		}),
		documentDBConnector("dynamodb", map[string]any{
			"region":     "us-east-1",
			"table_name": "T",
			"auth_type":  "access_key",
		}),
	}
	for _, conn := range cases {
		tool := buildConnectorTool(conn)
		timeout, ok := tool["timeout_seconds"].(int)
		if !ok || timeout < 60 {
			t.Errorf("subtype %s: timeout_seconds = %v, want int >= 60", conn.Subtype, tool["timeout_seconds"])
		}
	}
}

func graphDBConnector(subtype string, values map[string]any) ConnectorConfig {
	return ConnectorConfig{
		ID:      "aa0e8400-e29b-41d4-a716-446655440000",
		Type:    "graph_db",
		Subtype: subtype,
		Values:  values,
	}
}

func TestBuildNeo4jConnectorTool_BasicFields(t *testing.T) {
	t.Parallel()
	tool := buildConnectorTool(graphDBConnector("neo4j", map[string]any{
		"scheme":   "bolt+s",
		"hostname": "neo4j.example.com",
		"port":     7687,
		"username": "neo4j",
		"password": "secret",
		"database": "graph",
	}))
	assertToolField(t, tool, "tool_type", "builtin")
	assertToolField(t, tool, "tool_name", "graph_neo4j_query_aa0e8400")
	tc := tool["tool_config"].(map[string]any)
	assertToolField(t, tc, "backend", "neo4j")
	assertToolField(t, tc, "_str_binary", "graph_db_query")
	assertToolField(t, tc, "connection_string", "bolt+s://neo4j.example.com:7687")
	assertToolField(t, tc, "username", "neo4j")
	assertToolField(t, tc, "password", "secret")
	assertToolField(t, tc, "database", "graph")
}

func TestBuildNeo4jConnectorTool_DefaultPort(t *testing.T) {
	t.Parallel()
	tool := buildConnectorTool(graphDBConnector("neo4j", map[string]any{
		"hostname": "neo4j.example.com",
		"username": "neo4j",
		"password": "secret",
	}))
	tc := tool["tool_config"].(map[string]any)
	assertToolField(t, tc, "connection_string", "neo4j://neo4j.example.com:7687")
}

func TestBuildNeo4jConnectorTool_OmitsDatabaseWhenBlank(t *testing.T) {
	t.Parallel()
	tool := buildConnectorTool(graphDBConnector("neo4j", map[string]any{
		"scheme":   "bolt",
		"hostname": "localhost",
		"username": "neo4j",
		"password": "secret",
	}))
	tc := tool["tool_config"].(map[string]any)
	if _, has := tc["database"]; has {
		t.Error("database should not be set when not provided (tool defaults to 'neo4j')")
	}
}

func TestBuildNeo4jConnectorTool_FallbackToolName(t *testing.T) {
	t.Parallel()
	tool := buildConnectorTool(graphDBConnector("neo4j", map[string]any{
		"scheme":   "bolt",
		"hostname": "localhost",
		"username": "neo4j",
		"password": "secret",
	}))
	assertToolField(t, tool, "tool_name", "neo4j_query_aa0e8400")
}

func TestBuildNeptuneConnectorTool_AccessKeyAuth(t *testing.T) {
	t.Parallel()
	tool := buildConnectorTool(graphDBConnector("neptune", map[string]any{
		"scheme":                "bolt+s",
		"hostname":              "my-cluster.cluster-abc.us-east-1.neptune.amazonaws.com",
		"port":                  8182,
		"region":                "us-east-1",
		"auth_type":             "access_key",
		"aws_access_key_id":     "AKIAIOSFODNN7EXAMPLE",
		"aws_secret_access_key": "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLE",
	}))
	assertToolField(t, tool, "tool_type", "builtin")
	assertToolField(t, tool, "tool_name", "neptune_query_aa0e8400")
	tc := tool["tool_config"].(map[string]any)
	assertToolField(t, tc, "backend", "neptune")
	assertToolField(t, tc, "_str_binary", "graph_db_query")
	assertToolField(t, tc, "connection_string", "bolt+s://my-cluster.cluster-abc.us-east-1.neptune.amazonaws.com:8182")
	assertToolField(t, tc, "region", "us-east-1")
	assertToolField(t, tc, "aws_access_key_id", "AKIAIOSFODNN7EXAMPLE")
	if _, has := tc["role_arn"]; has {
		t.Error("role_arn should not be set for access_key auth")
	}
}

func TestBuildNeptuneConnectorTool_IAMAuth(t *testing.T) {
	t.Parallel()
	tool := buildConnectorTool(graphDBConnector("neptune", map[string]any{
		"scheme":       "bolt+s",
		"hostname":     "my-cluster.neptune.amazonaws.com",
		"port":         8182,
		"region":       "us-west-2",
		"auth_type":    "iam",
		"role_arn":     "arn:aws:iam::123456789012:role/SolaceNeptune",
		"session_name": "neptune-session",
		"external_id":  "ext-id",
	}))
	tc := tool["tool_config"].(map[string]any)
	assertToolField(t, tc, "role_arn", "arn:aws:iam::123456789012:role/SolaceNeptune")
	assertToolField(t, tc, "session_name", "neptune-session")
	assertToolField(t, tc, "external_id", "ext-id")
	if _, has := tc["aws_access_key_id"]; has {
		t.Error("aws_access_key_id should not be set for iam auth")
	}
}

func TestBuildGraphDBConnectorTool_UnknownSubtype(t *testing.T) {
	t.Parallel()
	if tool := buildConnectorTool(graphDBConnector("janusgraph", map[string]any{})); tool != nil {
		t.Errorf("unknown subtype should return nil, got %v", tool)
	}
}

func searchConnector(subtype string, values map[string]any) ConnectorConfig {
	return ConnectorConfig{
		ID:      "bb0e8400-e29b-41d4-a716-446655440000",
		Type:    "search",
		Subtype: subtype,
		Values:  values,
	}
}

func TestBuildElasticsearchConnectorTool_URL(t *testing.T) {
	t.Parallel()
	tool := buildConnectorTool(searchConnector("elasticsearch", map[string]any{
		"scheme":   "https",
		"hostname": "elastic.example.com",
		"port":     9200,
		"api_key":  "ZGVtbzpkZW1v",
	}))
	assertToolField(t, tool, "tool_type", "builtin")
	assertToolField(t, tool, "tool_name", "elasticsearch_query_bb0e8400")
	tc := tool["tool_config"].(map[string]any)
	assertToolField(t, tc, "backend", "elasticsearch")
	assertToolField(t, tc, "_str_binary", "search_query")
	assertToolField(t, tc, "connection_string", "https://elastic.example.com:9200")
	assertToolField(t, tc, "api_key", "ZGVtbzpkZW1v")
	if _, has := tc["cloud_id"]; has {
		t.Error("cloud_id should not be set when not provided")
	}
}

func TestBuildElasticsearchConnectorTool_BasicAuth(t *testing.T) {
	t.Parallel()
	tool := buildConnectorTool(searchConnector("elasticsearch", map[string]any{
		"scheme":   "https",
		"hostname": "elastic.example.com",
		"port":     9200,
		"username": "elastic",
		"password": "changeme",
	}))
	tc := tool["tool_config"].(map[string]any)
	assertToolField(t, tc, "connection_string", "https://elastic.example.com:9200")
	assertToolField(t, tc, "username", "elastic")
	assertToolField(t, tc, "password", "changeme")
	if _, has := tc["api_key"]; has {
		t.Error("api_key should not be set when only basic auth is provided")
	}
}

func TestBuildElasticsearchConnectorTool_NoAuth(t *testing.T) {
	t.Parallel()
	tool := buildConnectorTool(searchConnector("elasticsearch", map[string]any{
		"scheme":   "http",
		"hostname": "localhost",
		"port":     9200,
	}))
	tc := tool["tool_config"].(map[string]any)
	assertToolField(t, tc, "connection_string", "http://localhost:9200")
	for _, k := range []string{"api_key", "username", "password"} {
		if _, has := tc[k]; has {
			t.Errorf("%s should not be set for an unsecured cluster", k)
		}
	}
}

func TestBuildElasticsearchConnectorTool_CloudID(t *testing.T) {
	t.Parallel()
	tool := buildConnectorTool(searchConnector("elasticsearch", map[string]any{
		"cloud_id": "my-deployment:dXMtZWFzdC0xLmF3cy5jbG91ZC5lcy5pbyQ",
		"api_key":  "ZGVtbzpkZW1v",
	}))
	tc := tool["tool_config"].(map[string]any)
	assertToolField(t, tc, "cloud_id", "my-deployment:dXMtZWFzdC0xLmF3cy5jbG91ZC5lcy5pbyQ")
	if _, has := tc["connection_string"]; has {
		t.Error("connection_string should not be set when not provided")
	}
}

func TestBuildOpenSearchConnectorTool_AccessKeyAuth(t *testing.T) {
	t.Parallel()
	tool := buildConnectorTool(searchConnector("opensearch", map[string]any{
		"scheme":                "https",
		"hostname":              "my-domain.us-east-1.es.amazonaws.com",
		"region":                "us-east-1",
		"auth_type":             "access_key",
		"aws_access_key_id":     "AKIAIOSFODNN7EXAMPLE",
		"aws_secret_access_key": "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLE",
	}))
	assertToolField(t, tool, "tool_type", "builtin")
	assertToolField(t, tool, "tool_name", "opensearch_query_bb0e8400")
	tc := tool["tool_config"].(map[string]any)
	assertToolField(t, tc, "backend", "opensearch")
	assertToolField(t, tc, "_str_binary", "search_query")
	assertToolField(t, tc, "connection_string", "https://my-domain.us-east-1.es.amazonaws.com")
	assertToolField(t, tc, "region", "us-east-1")
	assertToolField(t, tc, "aws_access_key_id", "AKIAIOSFODNN7EXAMPLE")
}

func TestBuildOpenSearchConnectorTool_IAMAuth(t *testing.T) {
	t.Parallel()
	tool := buildConnectorTool(searchConnector("opensearch", map[string]any{
		"scheme":    "https",
		"hostname":  "my-domain.us-east-1.es.amazonaws.com",
		"region":    "us-east-1",
		"auth_type": "iam",
		"role_arn":  "arn:aws:iam::123456789012:role/SolaceOS",
	}))
	tc := tool["tool_config"].(map[string]any)
	assertToolField(t, tc, "role_arn", "arn:aws:iam::123456789012:role/SolaceOS")
	if _, has := tc["aws_access_key_id"]; has {
		t.Error("aws_access_key_id should not be set for iam auth")
	}
}

func TestBuildSearchConnectorTool_UnknownSubtype(t *testing.T) {
	t.Parallel()
	if tool := buildConnectorTool(searchConnector("solr", map[string]any{})); tool != nil {
		t.Errorf("unknown subtype should return nil, got %v", tool)
	}
}

func TestBuildGraphAndSearchConnectorTools_TimeoutSeconds(t *testing.T) {
	t.Parallel()
	cases := []ConnectorConfig{
		graphDBConnector("neo4j", map[string]any{
			"hostname": "localhost",
			"username": "n",
			"password": "p",
		}),
		graphDBConnector("neptune", map[string]any{
			"hostname":  "x.neptune.amazonaws.com",
			"region":    "us-east-1",
			"auth_type": "access_key",
		}),
		searchConnector("elasticsearch", map[string]any{
			"hostname": "es.example.com",
			"api_key":  "k",
		}),
		searchConnector("opensearch", map[string]any{
			"hostname":  "os.example.com",
			"region":    "us-east-1",
			"auth_type": "access_key",
		}),
	}
	for _, conn := range cases {
		tool := buildConnectorTool(conn)
		timeout, ok := tool["timeout_seconds"].(int)
		if !ok || timeout < 60 {
			t.Errorf("type=%s subtype=%s: timeout_seconds = %v, want int >= 60", conn.Type, conn.Subtype, tool["timeout_seconds"])
		}
	}
}

func emailConnector(values map[string]any) ConnectorConfig {
	return ConnectorConfig{
		ID:     "990e8400-e29b-41d4-a716-446655440000",
		Type:   "email",
		Values: values,
	}
}

func baseEmailValues() map[string]any {
	return map[string]any{
		"smtp_host":           "smtp.example.com",
		"smtp_port":           float64(587),
		"smtp_tls":            "starttls",
		"smtp_auth_type":      "plain",
		"smtp_username":       "user@example.com",
		"smtp_password":       "secret",
		"envelope_from":       "noreply@example.com",
		"recipient_allowlist": "example.com\npartner.example.com",
	}
}

func TestBuildEmailConnectorTool_Basic(t *testing.T) {
	t.Parallel()
	tool := buildConnectorTool(emailConnector(baseEmailValues()))
	assertToolField(t, tool, "tool_type", "builtin")
	assertToolField(t, tool, "tool_name", "send_email_990e8400")
	tc := tool["tool_config"].(map[string]any)
	assertToolField(t, tc, "_str_binary", "send_email")
	assertToolField(t, tc, "smtp_host", "smtp.example.com")
	assertToolField(t, tc, "smtp_port", "587")
	assertToolField(t, tc, "smtp_tls", "starttls")
	assertToolField(t, tc, "smtp_auth_type", "plain")
	assertToolField(t, tc, "smtp_username", "user@example.com")
	assertToolField(t, tc, "smtp_password", "secret")
	assertToolField(t, tc, "envelope_from", "noreply@example.com")

	allowlist := tc["recipient_allowlist"].([]string)
	if len(allowlist) != 2 {
		t.Errorf("allowlist length = %d, want 2", len(allowlist))
	}
	if allowlist[0] != "example.com" || allowlist[1] != "partner.example.com" {
		t.Errorf("allowlist = %v", allowlist)
	}
}

func TestBuildEmailConnectorTool_LoginAuth(t *testing.T) {
	t.Parallel()
	values := map[string]any{
		"smtp_host":           "smtp.example.com",
		"smtp_port":           float64(587),
		"smtp_tls":            "starttls",
		"smtp_auth_type":      "login",
		"smtp_username_login": "user@example.com",
		"smtp_password_login": "secret",
		"envelope_from":       "noreply@example.com",
		"recipient_allowlist": "example.com",
	}
	tool := buildConnectorTool(emailConnector(values))
	tc := tool["tool_config"].(map[string]any)
	assertToolField(t, tc, "smtp_auth_type", "login")
	assertToolField(t, tc, "smtp_username", "user@example.com")
	assertToolField(t, tc, "smtp_password", "secret")
}

func TestBuildEmailConnectorTool_RateLimits(t *testing.T) {
	t.Parallel()
	values := baseEmailValues()
	values["rate_limit_per_minute"] = float64(30)
	values["rate_limit_per_hour"] = float64(100)
	tool := buildConnectorTool(emailConnector(values))
	tc := tool["tool_config"].(map[string]any)
	assertToolField(t, tc, "rate_limit_per_minute", 30)
	assertToolField(t, tc, "rate_limit_per_hour", 100)
}

func TestBuildEmailConnectorTool_AttachmentLimits(t *testing.T) {
	t.Parallel()
	values := baseEmailValues()
	values["max_attachment_size_mb"] = float64(5)
	values["max_total_attachment_size_mb"] = float64(15)
	tool := buildConnectorTool(emailConnector(values))
	tc := tool["tool_config"].(map[string]any)
	assertToolField(t, tc, "max_attachment_size_bytes", int64(5*1024*1024))
	assertToolField(t, tc, "max_total_attachment_bytes", int64(15*1024*1024))
}

func TestBuildEmailConnectorTool_Defaults(t *testing.T) {
	t.Parallel()
	values := map[string]any{
		"smtp_host":           "smtp.example.com",
		"smtp_tls":            "starttls",
		"smtp_auth_type":      "plain",
		"smtp_username":       "user",
		"smtp_password":       "pass",
		"envelope_from":       "noreply@example.com",
		"recipient_allowlist": "example.com",
	}
	tool := buildConnectorTool(emailConnector(values))
	tc := tool["tool_config"].(map[string]any)
	assertToolField(t, tc, "rate_limit_per_minute", 60)
	assertToolField(t, tc, "rate_limit_per_hour", 500)
	assertToolField(t, tc, "max_attachment_size_bytes", int64(10*1024*1024))
	assertToolField(t, tc, "max_total_attachment_bytes", int64(25*1024*1024))
}

func TestBuildEmailConnectorTool_TimeoutSeconds(t *testing.T) {
	t.Parallel()
	tool := buildConnectorTool(emailConnector(baseEmailValues()))
	timeout, ok := tool["timeout_seconds"].(int)
	if !ok {
		t.Fatalf("timeout_seconds missing or wrong type: %v (%T)", tool["timeout_seconds"], tool["timeout_seconds"])
	}
	if timeout != 30 {
		t.Errorf("timeout_seconds = %d, want 30", timeout)
	}
}

func TestBuildEmailConnectorTool_EnvVarFallback(t *testing.T) {
	t.Parallel()
	tool := buildConnectorTool(emailConnector(map[string]any{
		"smtp_tls":            "starttls",
		"smtp_auth_type":      "plain",
		"recipient_allowlist": "example.com",
	}))
	tc := tool["tool_config"].(map[string]any)
	smtpHost := tc["smtp_host"].(string)
	assertStringContains(t, smtpHost, "${SMTP_HOST_")
	smtpPort := tc["smtp_port"].(string)
	assertStringContains(t, smtpPort, "${SMTP_PORT_")
	envelopeFrom := tc["envelope_from"].(string)
	assertStringContains(t, envelopeFrom, "${ENVELOPE_FROM_")
}

func TestBuildAPIConnectorTool_ResourceKeyEmitsObjStore(t *testing.T) {
	conn := ConnectorConfig{
		Type:        "api",
		Subtype:     "openapi",
		ResourceKey: "default/connectors/abc/openapi.yaml",
		Values:      map[string]any{},
	}
	tool := buildAPIConnectorTool(conn, "abc")
	got, _ := tool["specification_url"].(string)
	want := "objstore://default/connectors/abc/openapi.yaml"
	if got != want {
		t.Errorf("specification_url = %q, want %q", got, want)
	}
}

func TestBuildAPIConnectorTool_ObjStoreWinsOverDisplayURL(t *testing.T) {
	conn := ConnectorConfig{
		Type:        "api",
		Subtype:     "openapi",
		ResourceKey: "default/connectors/abc/spec.yaml",
		Values:      map[string]any{"specification_url": "/api/v1/platform/connectors/abc/resource"},
	}
	tool := buildAPIConnectorTool(conn, "abc")
	if got := tool["specification_url"]; got != "objstore://default/connectors/abc/spec.yaml" {
		t.Errorf("specification_url = %v, want objstore://default/connectors/abc/spec.yaml", got)
	}
}

func TestBuildConnectorTools_HILIsEntryLevelForEveryRole(t *testing.T) {
	t.Parallel()
	for connType, bySubtype := range connectorHILRoles {
		for subtype, roles := range bySubtype {
			buildSubtype := cmp.Or(subtype, map[string]string{"email": "smtp", "slack": "bot", "sql": "postgres", "knowledge_base": "bedrock"}[connType])
			t.Run(connType+"/"+buildSubtype, func(t *testing.T) {
				t.Parallel()
				gates := map[string]any{}
				for _, role := range roles {
					gates[role] = map[string]any{"require_approval": true, "approval_message": role}
				}
				conn := ConnectorConfig{ID: "990e8400-e29b-41d4-a716-446655440000", Type: connType, Subtype: buildSubtype,
					Values: map[string]any{"hil": map[string]any{"tools": gates}}}
				var seen []string
				for _, entry := range buildConnectorTools(conn) {
					hil, _ := entry["hil"].(map[string]any)
					if _, keyed := hil["tools"]; keyed {
						t.Fatalf("%v carries a tool-keyed hil map, which fails boot on a builtin entry", entry["tool_name"])
					}
					msg, _ := hil["approval_message"].(string)
					seen = append(seen, msg)
				}
				if !slices.Equal(slices.Sorted(slices.Values(seen)), slices.Sorted(slices.Values(roles))) {
					t.Errorf("entry-level gates %v, want one per role %v", seen, roles)
				}
			})
		}
	}
}

func TestBuildConnectorTools_HILSurvivesCustomToolName(t *testing.T) {
	t.Parallel()
	conn := ConnectorConfig{ID: "990e8400-e29b-41d4-a716-446655440001", Type: "document_db", Subtype: "mongodb",
		Values: map[string]any{"tool_name": "orders_lookup",
			"hil": map[string]any{"tools": map[string]any{"mongo_query": map[string]any{"require_approval": true}}}}}
	entry := buildConnectorTools(conn)[0]
	if entry["tool_name"] != "orders_lookup" || entry["hil"].(map[string]any)["require_approval"] != true {
		t.Fatalf("entry = %v", entry)
	}
}

func TestBuildConnectorTools_HILOnEventMeshOnlyGatesTheNamedOperation(t *testing.T) {
	t.Parallel()
	conn := ConnectorConfig{ID: "990e8400-e29b-41d4-a716-446655440002", Type: "event_mesh", Values: map[string]any{
		"operations": []any{
			map[string]any{"tool_name": "place_order", "topic": "orders/place"},
			map[string]any{"tool_name": "look_up_order", "topic": "orders/lookup"},
		},
		"hil": map[string]any{"tools": map[string]any{"place_order": map[string]any{"require_approval": true}}},
	}}
	entries := buildConnectorTools(conn)
	if len(entries) != 2 {
		t.Fatalf("entries = %d, want 2", len(entries))
	}
	for _, entry := range entries {
		name, _ := entry["tool_name"].(string)
		_, gated := entry["hil"]
		if want := strings.HasPrefix(name, "place_order_"); gated != want {
			t.Errorf("%s: gated = %v, want %v", name, gated, want)
		}
	}
}

func TestBuildConnectorTools_HILOnAPIKeepsToolsKeyedByOperationName(t *testing.T) {
	t.Parallel()
	gates := map[string]any{"create_pet": map[string]any{"require_approval": true}}
	entry := buildConnectorTools(apiConnector(map[string]any{"hil": map[string]any{"tools": gates}}))[0]
	hil, _ := entry["hil"].(map[string]any)
	if !reflect.DeepEqual(hil["tools"], gates) {
		t.Fatalf("hil = %v, want tools %v", hil, gates)
	}
}

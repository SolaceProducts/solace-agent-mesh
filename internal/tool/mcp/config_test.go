// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package mcp

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/core/schemas"
)

func TestParseToolsetConfig_Stdio(t *testing.T) {
	raw := map[string]any{
		"tool_type": "mcp",
		"connection_params": map[string]any{
			"type":    "stdio",
			"command": "/usr/bin/mcp-server",
			"args":    []any{"--port", "8080"},
			"timeout": 60,
			"env": map[string]any{
				"API_KEY": "secret",
			},
		},
		"tool_name_prefix": "fs_",
		"allow_list":       []any{"read_file", "write_file"},
	}

	cfg, err := ParseToolsetConfig(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.ConnectionType != "stdio" {
		t.Errorf("ConnectionType = %q, want stdio", cfg.ConnectionType)
	}
	if cfg.Command != "/usr/bin/mcp-server" {
		t.Errorf("Command = %q, want /usr/bin/mcp-server", cfg.Command)
	}
	if len(cfg.CommandArgs) != 2 || cfg.CommandArgs[0] != "--port" {
		t.Errorf("CommandArgs = %v, want [--port 8080]", cfg.CommandArgs)
	}
	if cfg.Timeout != 60 {
		t.Errorf("Timeout = %d, want 60", cfg.Timeout)
	}
	if cfg.EnvVars["API_KEY"] != "secret" {
		t.Errorf("EnvVars[API_KEY] = %q, want secret", cfg.EnvVars["API_KEY"])
	}
	if cfg.ToolNamePrefix != "fs_" {
		t.Errorf("ToolNamePrefix = %q, want fs_", cfg.ToolNamePrefix)
	}
	if len(cfg.AllowList) != 2 {
		t.Errorf("AllowList = %v, want [read_file write_file]", cfg.AllowList)
	}
}

func TestParseToolsetConfig_SSE(t *testing.T) {
	raw := map[string]any{
		"tool_type": "mcp",
		"connection_params": map[string]any{
			"type": "sse",
			"url":  "https://mcp.example.com/sse",
			"ssl_config": map[string]any{
				"verify":    false,
				"ca_bundle": "/path/to/ca.pem",
			},
		},
		"tool_name": "single_tool",
	}

	cfg, err := ParseToolsetConfig(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.ConnectionType != "sse" {
		t.Errorf("ConnectionType = %q, want sse", cfg.ConnectionType)
	}
	if cfg.URL != "https://mcp.example.com/sse" {
		t.Errorf("URL = %q", cfg.URL)
	}
	if cfg.SSLVerify == nil || *cfg.SSLVerify != false {
		t.Errorf("SSLVerify = %v, want false", cfg.SSLVerify)
	}
	if cfg.SSLCABundle != "/path/to/ca.pem" {
		t.Errorf("SSLCABundle = %q", cfg.SSLCABundle)
	}
	if cfg.ToolName != "single_tool" {
		t.Errorf("ToolName = %q, want single_tool", cfg.ToolName)
	}
}

func TestParseToolsetConfig_StreamableHTTP(t *testing.T) {
	raw := map[string]any{
		"tool_type": "mcp",
		"connection_params": map[string]any{
			"type": "streamable-http",
			"url":  "https://mcp.example.com",
		},
	}

	cfg, err := ParseToolsetConfig(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.ConnectionType != "streamable-http" {
		t.Errorf("ConnectionType = %q, want streamable-http", cfg.ConnectionType)
	}
}

func TestParseToolsetConfig_MutuallyExclusiveFilters(t *testing.T) {
	raw := map[string]any{
		"tool_type": "mcp",
		"connection_params": map[string]any{
			"type":    "stdio",
			"command": "server",
		},
		"tool_name":  "tool1",
		"allow_list": []any{"tool2"},
	}

	_, err := ParseToolsetConfig(raw)
	if err == nil {
		t.Fatal("expected error for mutually exclusive filters")
	}
}

func TestParseToolsetConfig_MissingConnectionParams(t *testing.T) {
	raw := map[string]any{
		"tool_type": "mcp",
	}

	_, err := ParseToolsetConfig(raw)
	if err == nil {
		t.Fatal("expected error for missing connection_params")
	}
}

func TestParseToolsetConfig_UnsupportedType(t *testing.T) {
	raw := map[string]any{
		"tool_type": "mcp",
		"connection_params": map[string]any{
			"type": "websocket",
		},
	}

	_, err := ParseToolsetConfig(raw)
	if err == nil {
		t.Fatal("expected error for unsupported connection type")
	}
}

func TestToBifrostClientConfig_Stdio(t *testing.T) {
	cfg := &ToolsetConfig{
		ConnectionType: "stdio",
		Command:        "/usr/bin/server",
		CommandArgs:    []string{"--flag"},
		EnvVars:        map[string]string{"KEY": "val"},
		ClientID:       "mcp_server",
	}

	bc := cfg.ToBifrostClientConfig()

	if bc.ConnectionType != schemas.MCPConnectionTypeSTDIO {
		t.Errorf("ConnectionType = %v, want STDIO", bc.ConnectionType)
	}
	if bc.StdioConfig == nil {
		t.Fatal("StdioConfig is nil")
	}
	if bc.StdioConfig.Command != "/usr/bin/server" {
		t.Errorf("Command = %q", bc.StdioConfig.Command)
	}
	if len(bc.StdioConfig.Args) != 1 || bc.StdioConfig.Args[0] != "--flag" {
		t.Errorf("Args = %v", bc.StdioConfig.Args)
	}
	if len(bc.StdioConfig.Envs) != 1 {
		t.Errorf("Envs = %v", bc.StdioConfig.Envs)
	}
}

func TestToBifrostClientConfig_SSE(t *testing.T) {
	cfg := &ToolsetConfig{
		ConnectionType: "sse",
		URL:            "https://example.com/sse",
		ClientID:       "mcp_example",
	}

	bc := cfg.ToBifrostClientConfig()

	if bc.ConnectionType != schemas.MCPConnectionTypeSSE {
		t.Errorf("ConnectionType = %v, want SSE", bc.ConnectionType)
	}
	if bc.ConnectionString == nil || bc.ConnectionString.GetValue() != "https://example.com/sse" {
		t.Errorf("ConnectionString = %v", bc.ConnectionString)
	}
}

func TestToBifrostClientConfig_HTTP(t *testing.T) {
	cfg := &ToolsetConfig{
		ConnectionType: "streamable-http",
		URL:            "https://example.com",
		ClientID:       "mcp_example",
	}

	bc := cfg.ToBifrostClientConfig()

	if bc.ConnectionType != schemas.MCPConnectionTypeHTTP {
		t.Errorf("ConnectionType = %v, want HTTP", bc.ConnectionType)
	}
}

func TestToBifrostClientConfig_KeepsOnePersistentSession(t *testing.T) {
	for _, connType := range []string{"stdio", "sse", "streamable-http"} {
		cfg := &ToolsetConfig{ConnectionType: connType, URL: "https://example.com", Command: "srv", ClientID: "mcp_example"}

		bc := cfg.ToBifrostClientConfig()

		if bc.NeedsSessionStickiness == nil || !*bc.NeedsSessionStickiness {
			t.Errorf("%s: NeedsSessionStickiness = %v, want true so Bifrost keeps one session instead of a connection per call",
				connType, bc.NeedsSessionStickiness)
		}
	}
}

func TestShouldIncludeTool(t *testing.T) {
	tests := []struct {
		name     string
		cfg      ToolsetConfig
		toolName string
		want     bool
	}{
		{
			name:     "no filter",
			cfg:      ToolsetConfig{},
			toolName: "anything",
			want:     true,
		},
		{
			name:     "single tool match",
			cfg:      ToolsetConfig{ToolName: "read_file"},
			toolName: "read_file",
			want:     true,
		},
		{
			name:     "single tool no match",
			cfg:      ToolsetConfig{ToolName: "read_file"},
			toolName: "write_file",
			want:     false,
		},
		{
			name:     "allow list match",
			cfg:      ToolsetConfig{AllowList: []string{"read_file", "write_file"}},
			toolName: "read_file",
			want:     true,
		},
		{
			name:     "allow list no match",
			cfg:      ToolsetConfig{AllowList: []string{"read_file"}},
			toolName: "delete_file",
			want:     false,
		},
		{
			name:     "deny list excluded",
			cfg:      ToolsetConfig{DenyList: []string{"delete_file"}},
			toolName: "delete_file",
			want:     false,
		},
		{
			name:     "deny list allowed",
			cfg:      ToolsetConfig{DenyList: []string{"delete_file"}},
			toolName: "read_file",
			want:     true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.cfg.ShouldIncludeTool(tc.toolName)
			if got != tc.want {
				t.Errorf("ShouldIncludeTool(%q) = %v, want %v", tc.toolName, got, tc.want)
			}
		})
	}
}

func TestHasSSLConfig(t *testing.T) {
	trueV := true
	falseV := false
	tests := []struct {
		name string
		cfg  ToolsetConfig
		want bool
	}{
		{"zero value", ToolsetConfig{}, false},
		{"verify=true only", ToolsetConfig{SSLVerify: &trueV}, false},
		{"verify=false", ToolsetConfig{SSLVerify: &falseV}, true},
		{"ca_bundle only", ToolsetConfig{SSLCABundle: "/path/ca.pem"}, true},
		{"both set", ToolsetConfig{SSLVerify: &falseV, SSLCABundle: "/path/ca.pem"}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.cfg.HasSSLConfig(); got != tc.want {
				t.Errorf("HasSSLConfig() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestBuildHTTPClient_Default(t *testing.T) {
	cfg := &ToolsetConfig{}
	client, err := cfg.BuildHTTPClient()
	if err != nil {
		t.Fatalf("BuildHTTPClient: %v", err)
	}
	tc := clientTLSConfig(t, client)
	if tc.InsecureSkipVerify {
		t.Errorf("InsecureSkipVerify = true, want false")
	}
	if tc.RootCAs != nil {
		t.Errorf("RootCAs = %v, want nil", tc.RootCAs)
	}
}

func TestBuildHTTPClient_InsecureSkipVerify(t *testing.T) {
	verify := false
	cfg := &ToolsetConfig{SSLVerify: &verify}
	client, err := cfg.BuildHTTPClient()
	if err != nil {
		t.Fatalf("BuildHTTPClient: %v", err)
	}
	tc := clientTLSConfig(t, client)
	if !tc.InsecureSkipVerify {
		t.Errorf("InsecureSkipVerify = false, want true")
	}
}

func TestBuildHTTPClient_CABundle(t *testing.T) {
	caPath := writeSelfSignedCA(t)
	cfg := &ToolsetConfig{SSLCABundle: caPath}
	client, err := cfg.BuildHTTPClient()
	if err != nil {
		t.Fatalf("BuildHTTPClient: %v", err)
	}
	tc := clientTLSConfig(t, client)
	if tc.RootCAs == nil {
		t.Fatal("RootCAs is nil, want a populated pool")
	}
	expected := x509.NewCertPool()
	pemBytes, err := os.ReadFile(caPath)
	if err != nil {
		t.Fatalf("read CA: %v", err)
	}
	if !expected.AppendCertsFromPEM(pemBytes) {
		t.Fatal("expected pool could not parse the CA PEM")
	}
	if !tc.RootCAs.Equal(expected) {
		t.Errorf("RootCAs does not contain the CA bundle cert")
	}
}

func TestBuildHTTPClient_Both(t *testing.T) {
	verify := false
	caPath := writeSelfSignedCA(t)
	cfg := &ToolsetConfig{SSLVerify: &verify, SSLCABundle: caPath}
	client, err := cfg.BuildHTTPClient()
	if err != nil {
		t.Fatalf("BuildHTTPClient: %v", err)
	}
	tc := clientTLSConfig(t, client)
	if !tc.InsecureSkipVerify {
		t.Errorf("InsecureSkipVerify = false, want true")
	}
	if tc.RootCAs == nil {
		t.Errorf("RootCAs = nil, want populated")
	}
}

func TestBuildHTTPClient_BadPath(t *testing.T) {
	cfg := &ToolsetConfig{SSLCABundle: filepath.Join(t.TempDir(), "does-not-exist.pem")}
	if _, err := cfg.BuildHTTPClient(); err == nil {
		t.Fatal("expected error for missing CA bundle")
	}
}

func TestBuildHTTPClient_InvalidPEM(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "garbage.pem")
	if err := os.WriteFile(path, []byte("not a pem file"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := &ToolsetConfig{SSLCABundle: path}
	_, err := cfg.BuildHTTPClient()
	if err == nil {
		t.Fatal("expected error for invalid PEM")
	}
}

func clientTLSConfig(t *testing.T, client *http.Client) *struct {
	InsecureSkipVerify bool
	RootCAs            *x509.CertPool
} {
	t.Helper()
	tr, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("client.Transport is %T, want *http.Transport", client.Transport)
	}
	if tr.TLSClientConfig == nil {
		t.Fatal("TLSClientConfig is nil")
	}
	return &struct {
		InsecureSkipVerify bool
		RootCAs            *x509.CertPool
	}{
		InsecureSkipVerify: tr.TLSClientConfig.InsecureSkipVerify,
		RootCAs:            tr.TLSClientConfig.RootCAs,
	}
}

func writeSelfSignedCA(t *testing.T) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "mcp-test-ca"},
		NotBefore:             time.Now().Add(-time.Minute),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create cert: %v", err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})

	dir := t.TempDir()
	path := filepath.Join(dir, "ca.pem")
	if err := os.WriteFile(path, pemBytes, 0o600); err != nil {
		t.Fatalf("write pem: %v", err)
	}
	return path
}

func TestSanitizeName(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"hello_world", "hello_world"},
		{"hello-world", "hello_world"},
		{"hello.world", "hello_world"},
		{"hello world", "hello_world"},
		{"mcp://server", "mcp___server"},
	}

	for _, tc := range tests {
		got := sanitizeName(tc.input)
		if got != tc.want {
			t.Errorf("sanitizeName(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestParseToolsetConfig_EnvironmentVariables(t *testing.T) {
	raw := map[string]any{
		"tool_type": "mcp",
		"connection_params": map[string]any{
			"type":    "stdio",
			"command": "server",
		},
		"environment_variables": map[string]any{
			"API_KEY":   "${API_KEY}",
			"OTHER_VAR": "value",
		},
	}

	cfg, err := ParseToolsetConfig(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(cfg.EnvVars) != 2 {
		t.Errorf("EnvVars has %d entries, want 2", len(cfg.EnvVars))
	}
	if cfg.EnvVars["API_KEY"] != "${API_KEY}" {
		t.Errorf("EnvVars[API_KEY] = %q", cfg.EnvVars["API_KEY"])
	}
}

func TestParseToolsetConfig_Auth(t *testing.T) {
	raw := map[string]any{
		"tool_type": "mcp",
		"connection_params": map[string]any{
			"type": "sse",
			"url":  "https://example.com/sse",
		},
		"auth": map[string]any{
			"type": "oauth2",
		},
	}

	cfg, err := ParseToolsetConfig(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.AuthType != "oauth2" {
		t.Errorf("AuthType = %q, want oauth2", cfg.AuthType)
	}
}

func TestParseToolsetConfig_InlineManifest(t *testing.T) {
	raw := map[string]any{
		"tool_type": "mcp",
		"connection_params": map[string]any{
			"type": "sse",
			"url":  "https://mcp.example.com/v1/sse",
		},
		"auth": map[string]any{"type": "oauth2"},
		"manifest": []any{
			map[string]any{
				"name":        "listThings",
				"description": "List things.",
				"inputSchema": map[string]any{
					"type":                 "object",
					"properties":           map[string]any{},
					"additionalProperties": false,
				},
			},
			map[string]any{
				"id":          "getThing",
				"description": "Get a thing.",
				"inputSchema": map[string]any{"type": "object"},
			},
		},
	}

	cfg, err := ParseToolsetConfig(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.Manifest) != 2 {
		t.Fatalf("len(Manifest) = %d, want 2", len(cfg.Manifest))
	}
	if cfg.Manifest[0].Name != "listThings" {
		t.Errorf("Manifest[0].Name = %q, want listThings", cfg.Manifest[0].Name)
	}
	if cfg.Manifest[1].Name != "getThing" {
		t.Errorf("Manifest[1].Name = %q, want getThing (id fallback)", cfg.Manifest[1].Name)
	}
}

func TestParseToolsetConfig_ManifestFile(t *testing.T) {
	raw := map[string]any{
		"tool_type": "mcp",
		"connection_params": map[string]any{
			"type": "sse",
			"url":  "https://mcp.example.com/v1/sse",
		},
		"auth":          map[string]any{"type": "oauth2"},
		"manifest_file": filepath.Join("testdata", "manifest_ok.yaml"),
	}

	cfg, err := ParseToolsetConfig(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.Manifest) != 2 {
		t.Fatalf("len(Manifest) = %d, want 2", len(cfg.Manifest))
	}
	if cfg.Manifest[0].Name != "getConfluenceSpaces" {
		t.Errorf("Manifest[0].Name = %q, want getConfluenceSpaces", cfg.Manifest[0].Name)
	}
}

func TestParseToolsetConfig_BothManifestAndFile(t *testing.T) {
	raw := map[string]any{
		"tool_type": "mcp",
		"connection_params": map[string]any{
			"type": "sse",
			"url":  "https://mcp.example.com/v1/sse",
		},
		"manifest":      []any{map[string]any{"name": "x", "inputSchema": map[string]any{"type": "object"}}},
		"manifest_file": filepath.Join("testdata", "manifest_ok.yaml"),
	}

	_, err := ParseToolsetConfig(raw)
	if err == nil {
		t.Fatal("expected error when both manifest and manifest_file are set, got nil")
	}
}

func TestParseToolsetConfig_ManifestFile_Missing(t *testing.T) {
	raw := map[string]any{
		"tool_type": "mcp",
		"connection_params": map[string]any{
			"type": "sse",
			"url":  "https://mcp.example.com/v1/sse",
		},
		"manifest_file": filepath.Join("testdata", "does_not_exist.yaml"),
	}

	_, err := ParseToolsetConfig(raw)
	if err == nil {
		t.Fatal("expected error for missing manifest file, got nil")
	}
}

func TestParseToolsetConfig_ManifestWithStdio(t *testing.T) {
	raw := map[string]any{
		"tool_type": "mcp",
		"connection_params": map[string]any{
			"type":    "stdio",
			"command": "/usr/bin/mcp-server",
		},
		"manifest": []any{
			map[string]any{"name": "x", "inputSchema": map[string]any{"type": "object"}},
		},
	}

	cfg, err := ParseToolsetConfig(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.Manifest) != 1 {
		t.Errorf("len(Manifest) = %d, want 1", len(cfg.Manifest))
	}
}

func TestParseToolsetConfig_AuthCredentialKeyDefaultNamesTheServer(t *testing.T) {
	for name, tc := range map[string]struct{ url, wantKey string }{
		"plain url":            {"https://mcp.atlassian.com/v1/sse", "mcp_mcp_atlassian_com_v1_sse_oauth2"},
		"userinfo and a query": {"https://svc:pw@mcp.example.com/sse?api_key=abc", "mcp_mcp_example_com_sse_7da51395_oauth2"},
	} {
		t.Run(name, func(t *testing.T) {
			cfg, err := ParseToolsetConfig(map[string]any{
				"tool_type":         "mcp",
				"connection_params": map[string]any{"type": "sse", "url": tc.url},
				"auth":              map[string]any{"type": "oauth2"},
			})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if cfg.AuthConfig == nil {
				t.Fatal("AuthConfig is nil")
			}
			if cfg.AuthConfig.CredentialKey != tc.wantKey {
				t.Errorf("CredentialKey = %q, want %q", cfg.AuthConfig.CredentialKey, tc.wantKey)
			}
		})
	}
}

func TestDefaultCredentialKeyBase_HTTP_IncludesPath(t *testing.T) {
	cases := []struct {
		name string
		url  string
		want string
	}{
		{"no path", "https://mcp.example.com", "mcp_mcp_example_com"},
		{"root slash only", "https://mcp.example.com/", "mcp_mcp_example_com"},
		{"single segment", "https://mcp.example.com/tenant1", "mcp_mcp_example_com_tenant1"},
		{"multi segment", "https://mcp.example.com/v1/sse", "mcp_mcp_example_com_v1_sse"},
		{"trailing slash", "https://mcp.example.com/tenant1/", "mcp_mcp_example_com_tenant1"},
		{"hyphenated tenant", "https://mcp.example.com/tenant-a", "mcp_mcp_example_com_tenant_a"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &ToolsetConfig{ConnectionType: connTypeStreamableHTTP, URL: tc.url}
			got := defaultCredentialKeyBase(cfg)
			if got != tc.want {
				t.Errorf("defaultCredentialKeyBase(%q) = %q, want %q", tc.url, got, tc.want)
			}
		})
	}
}

func TestParseToolsetConfig_MultiTenant_DistinctCredentialKeys(t *testing.T) {
	build := func(url string) *ToolsetConfig {
		cfg, err := ParseToolsetConfig(map[string]any{
			"tool_type": "mcp",
			"connection_params": map[string]any{
				"type": "sse",
				"url":  url,
			},
			"auth": map[string]any{"type": "oauth2"},
		})
		if err != nil {
			t.Fatalf("ParseToolsetConfig(%q): %v", url, err)
		}
		if cfg.AuthConfig == nil {
			t.Fatalf("AuthConfig nil for %q", url)
		}
		return cfg
	}

	a := build("https://mcp.example.com/tenant1")
	b := build("https://mcp.example.com/tenant2")

	if a.AuthConfig.CredentialKey == b.AuthConfig.CredentialKey {
		t.Errorf("both tenants default to the same CredentialKey %q — cross-tenant token leak", a.AuthConfig.CredentialKey)
	}
}

func TestParseToolsetConfig_ManifestFileEmpty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.yaml")
	if err := os.WriteFile(path, []byte("[]\n"), 0o644); err != nil {
		t.Fatalf("write temp manifest: %v", err)
	}
	raw := map[string]any{
		"tool_type": "mcp",
		"connection_params": map[string]any{
			"type": "sse",
			"url":  "https://mcp.example.com/sse",
		},
		"manifest_file": path,
	}
	cfg, err := ParseToolsetConfig(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Manifest != nil {
		t.Errorf("Manifest = %v, want nil for empty manifest file (so AddClient falls through to live discovery)", cfg.Manifest)
	}
}

func TestParseToolsetConfig_ManifestNullWithManifestFile(t *testing.T) {
	raw := map[string]any{
		"tool_type": "mcp",
		"connection_params": map[string]any{
			"type": "sse",
			"url":  "https://mcp.example.com/sse",
		},
		"manifest":      nil,
		"manifest_file": filepath.Join("testdata", "manifest_ok.yaml"),
	}
	cfg, err := ParseToolsetConfig(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.Manifest) != 2 {
		t.Errorf("len(Manifest) = %d, want 2 (loaded from manifest_file)", len(cfg.Manifest))
	}
}

func TestParseToolsetConfig_ManifestNull(t *testing.T) {
	for name, val := range map[string]any{
		"nil":          nil,
		"explicit nil": (any)(nil),
	} {
		t.Run(name, func(t *testing.T) {
			raw := map[string]any{
				"tool_type": "mcp",
				"connection_params": map[string]any{
					"type": "sse",
					"url":  "https://mcp.example.com/sse",
				},
				"manifest": val,
			}
			cfg, err := ParseToolsetConfig(raw)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if cfg.Manifest != nil {
				t.Errorf("Manifest = %v, want nil for null input", cfg.Manifest)
			}
		})
	}
}

func TestMCPAuthConfig_toToolAuth_ResourceURL_SSE(t *testing.T) {
	raw := map[string]any{
		"tool_type": "mcp",
		"connection_params": map[string]any{
			"type": "sse",
			"url":  "https://mcp.example.com/sse",
		},
		"auth": map[string]any{"type": "oauth2"},
	}

	cfg, err := ParseToolsetConfig(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ta := cfg.AuthConfig
	if ta == nil {
		t.Fatal("expected non-nil toolauth.AuthConfig")
	}
	if ta.ResourceURL != "https://mcp.example.com/sse" {
		t.Errorf("ResourceURL = %q, want %q", ta.ResourceURL, "https://mcp.example.com/sse")
	}
}

func TestMCPAuthConfig_toToolAuth_ResourceURL_StreamableHTTP(t *testing.T) {
	raw := map[string]any{
		"tool_type": "mcp",
		"connection_params": map[string]any{
			"type": "streamable-http",
			"url":  "https://mcp.example.com/http",
		},
		"auth": map[string]any{"type": "oauth2"},
	}

	cfg, err := ParseToolsetConfig(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ta := cfg.AuthConfig
	if ta.ResourceURL != "https://mcp.example.com/http" {
		t.Errorf("ResourceURL = %q, want %q", ta.ResourceURL, "https://mcp.example.com/http")
	}
}

func TestMCPAuthConfig_toToolAuth_ResourceURL_Stdio_Empty(t *testing.T) {
	raw := map[string]any{
		"tool_type": "mcp",
		"connection_params": map[string]any{
			"type":    "stdio",
			"command": "/bin/echo",
		},
		"auth": map[string]any{"type": "oauth2"},
	}

	cfg, err := ParseToolsetConfig(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ta := cfg.AuthConfig
	if ta.ResourceURL != "" {
		t.Errorf("ResourceURL = %q, want \"\" for stdio connection", ta.ResourceURL)
	}
}

func TestParseToolsetConfig_OAuth2NoScheme_PreservesResourceURL(t *testing.T) {
	raw := map[string]any{
		"tool_type": "mcp",
		"connection_params": map[string]any{
			"type": "sse",
			"url":  "https://mcp.atlassian.com/v1/sse",
		},
		"auth": map[string]any{"type": "oauth2"},
	}

	cfg, err := ParseToolsetConfig(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ta := cfg.AuthConfig
	if ta.ResourceURL != cfg.URL {
		t.Errorf("ResourceURL = %q, want cfg.URL %q", ta.ResourceURL, cfg.URL)
	}
	if ta.Scheme != nil {
		t.Errorf("Scheme should be nil when YAML has no scheme block, got %+v", ta.Scheme)
	}
	if ta.Credential != nil {
		t.Errorf("Credential should be nil when YAML has no credential block, got %+v", ta.Credential)
	}
}

func TestParseToolsetConfig_EmptyManifestList(t *testing.T) {
	raw := map[string]any{
		"tool_type": "mcp",
		"connection_params": map[string]any{
			"type": "sse",
			"url":  "https://mcp.example.com/v1/sse",
		},
		"manifest": []any{},
	}

	cfg, err := ParseToolsetConfig(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Manifest != nil {
		t.Errorf("Manifest = %v, want nil for empty list", cfg.Manifest)
	}
}

func TestParseToolsetConfig_StaticHeaders_APIKey(t *testing.T) {
	raw := map[string]any{
		"tool_type": "mcp",
		"connection_params": map[string]any{
			"type": "streamable-http",
			"url":  "https://mcp.example.com/sse",
			"headers": map[string]any{
				"X-API-Key": "supersecret",
			},
		},
	}

	cfg, err := ParseToolsetConfig(raw)
	if err != nil {
		t.Fatalf("ParseToolsetConfig: %v", err)
	}
	if got, want := cfg.Headers["X-API-Key"], "supersecret"; got != want {
		t.Errorf("cfg.Headers[X-API-Key] = %q, want %q (header dropped — runtime would call MCP server with no auth)", got, want)
	}

	bc := cfg.ToBifrostClientConfig()
	if bc.AuthType != schemas.MCPAuthTypeHeaders {
		t.Errorf("bc.AuthType = %v, want MCPAuthTypeHeaders (so Bifrost injects request headers)", bc.AuthType)
	}
	hv, ok := bc.Headers["X-API-Key"]
	if !ok {
		t.Fatalf("bc.Headers missing X-API-Key — Bifrost will send no auth header at runtime")
	}
	if hv.GetValue() != "supersecret" {
		t.Errorf("bc.Headers[X-API-Key].GetValue() = %q, want supersecret", hv.GetValue())
	}
}

func TestParseToolsetConfig_StaticHeaders_BasicLiteral(t *testing.T) {
	raw := map[string]any{
		"tool_type": "mcp",
		"connection_params": map[string]any{
			"type": "streamable-http",
			"url":  "https://mcp.example.com/sse",
			"headers": map[string]any{
				"Authorization": "Basic dXNlcjpwYXNz",
			},
		},
	}

	cfg, err := ParseToolsetConfig(raw)
	if err != nil {
		t.Fatalf("ParseToolsetConfig: %v", err)
	}
	if got := cfg.Headers["Authorization"]; got != "Basic dXNlcjpwYXNz" {
		t.Errorf("cfg.Headers[Authorization] = %q (literal Basic header dropped)", got)
	}

	bc := cfg.ToBifrostClientConfig()
	if bc.AuthType != schemas.MCPAuthTypeHeaders {
		t.Errorf("bc.AuthType = %v, want MCPAuthTypeHeaders", bc.AuthType)
	}
	if hv, ok := bc.Headers["Authorization"]; !ok || hv.GetValue() != "Basic dXNlcjpwYXNz" {
		t.Errorf("bc.Headers[Authorization] missing or wrong: %+v", hv)
	}
}

func TestParseToolsetConfig_StaticHeaders_BasicAuthBlock(t *testing.T) {
	raw := map[string]any{
		"tool_type": "mcp",
		"connection_params": map[string]any{
			"type": "streamable-http",
			"url":  "https://mcp.example.com/sse",
			"auth": map[string]any{
				"type":     "basic",
				"username": "user",
				"password": "pass",
			},
		},
	}

	cfg, err := ParseToolsetConfig(raw)
	if err != nil {
		t.Fatalf("ParseToolsetConfig: %v", err)
	}
	if got := cfg.Headers["Authorization"]; got != "Basic dXNlcjpwYXNz" {
		t.Errorf("cfg.Headers[Authorization] = %q, want %q (basic auth not encoded)", got, "Basic dXNlcjpwYXNz")
	}

	bc := cfg.ToBifrostClientConfig()
	if bc.AuthType != schemas.MCPAuthTypeHeaders {
		t.Errorf("bc.AuthType = %v, want MCPAuthTypeHeaders", bc.AuthType)
	}
	if hv, ok := bc.Headers["Authorization"]; !ok || hv.GetValue() != "Basic dXNlcjpwYXNz" {
		t.Errorf("bc.Headers[Authorization] missing or wrong: %+v", hv)
	}
}

func TestParseToolsetConfig_TLSVerifyEnvOverride(t *testing.T) {
	httpRaw := func(sslVerify any) map[string]any {
		conn := map[string]any{
			"type": "streamable-http",
			"url":  "https://mcp.example.com",
		}
		if sslVerify != nil {
			conn["ssl_config"] = map[string]any{"verify": sslVerify}
		}
		return map[string]any{"tool_type": "mcp", "connection_params": conn}
	}

	tlsBoolPtr := func(b bool) *bool { return &b }

	tests := []struct {
		name          string
		envValue      string
		setEnv        bool
		yamlSSLVerify any
		wantSSLVerify *bool
	}{
		{name: "env unset / yaml unset → SSLVerify nil (verify by default)", setEnv: false, yamlSSLVerify: nil, wantSSLVerify: nil},
		{name: "env false / yaml unset → SSLVerify false", setEnv: true, envValue: "false", yamlSSLVerify: nil, wantSSLVerify: tlsBoolPtr(false)},
		{name: "env 0 / yaml unset → SSLVerify false", setEnv: true, envValue: "0", yamlSSLVerify: nil, wantSSLVerify: tlsBoolPtr(false)},
		{name: "env no / yaml unset → SSLVerify false", setEnv: true, envValue: "no", yamlSSLVerify: nil, wantSSLVerify: tlsBoolPtr(false)},
		{name: "env off / yaml unset → SSLVerify false", setEnv: true, envValue: "off", yamlSSLVerify: nil, wantSSLVerify: tlsBoolPtr(false)},
		{name: "env FALSE (uppercase) / yaml unset → SSLVerify false", setEnv: true, envValue: "FALSE", yamlSSLVerify: nil, wantSSLVerify: tlsBoolPtr(false)},
		{name: "env true / yaml unset → SSLVerify nil (default verify)", setEnv: true, envValue: "true", yamlSSLVerify: nil, wantSSLVerify: nil},
		{name: "env unrecognized / yaml unset → SSLVerify nil", setEnv: true, envValue: "maybe", yamlSSLVerify: nil, wantSSLVerify: nil},
		{name: "yaml verify=true wins over env false", setEnv: true, envValue: "false", yamlSSLVerify: true, wantSSLVerify: tlsBoolPtr(true)},
		{name: "yaml verify=false wins over env true", setEnv: true, envValue: "true", yamlSSLVerify: false, wantSSLVerify: tlsBoolPtr(false)},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_ = os.Unsetenv(mcpTLSVerifyEnvVar)
			if tc.setEnv {
				t.Setenv(mcpTLSVerifyEnvVar, tc.envValue)
			}
			cfg, err := ParseToolsetConfig(httpRaw(tc.yamlSSLVerify))
			if err != nil {
				t.Fatalf("ParseToolsetConfig: %v", err)
			}
			switch {
			case tc.wantSSLVerify == nil && cfg.SSLVerify != nil:
				t.Errorf("SSLVerify = %v, want nil", *cfg.SSLVerify)
			case tc.wantSSLVerify != nil && cfg.SSLVerify == nil:
				t.Errorf("SSLVerify = nil, want %v", *tc.wantSSLVerify)
			case tc.wantSSLVerify != nil && *cfg.SSLVerify != *tc.wantSSLVerify:
				t.Errorf("SSLVerify = %v, want %v", *cfg.SSLVerify, *tc.wantSSLVerify)
			}
		})
	}
}

func TestParseToolsetConfig_TLSVerifyEnv_IgnoredForStdio(t *testing.T) {
	t.Setenv(mcpTLSVerifyEnvVar, "false")
	raw := map[string]any{
		"tool_type": "mcp",
		"connection_params": map[string]any{
			"type":    "stdio",
			"command": "/usr/bin/mcp-server",
		},
	}
	cfg, err := ParseToolsetConfig(raw)
	if err != nil {
		t.Fatalf("ParseToolsetConfig: %v", err)
	}
	if cfg.SSLVerify != nil {
		t.Errorf("stdio cfg.SSLVerify = %v, want nil (env var is HTTP-only)", *cfg.SSLVerify)
	}
}

func TestParseToolsetConfig_BasicAuth_SpecialChars(t *testing.T) {
	const username = "us3r!@#$"
	const password = "p@$$w0rd!#$%^&*()"

	raw := map[string]any{
		"tool_type": "mcp",
		"connection_params": map[string]any{
			"type": "streamable-http",
			"url":  "https://mcp.example.com/sse",
			"auth": map[string]any{
				"type":     "basic",
				"username": username,
				"password": password,
			},
		},
	}

	cfg, err := ParseToolsetConfig(raw)
	if err != nil {
		t.Fatalf("ParseToolsetConfig: %v", err)
	}
	header := cfg.Headers["Authorization"]
	if !strings.HasPrefix(header, "Basic ") {
		t.Fatalf("Authorization header = %q, want 'Basic ' prefix", header)
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(header, "Basic "))
	if err != nil {
		t.Fatalf("base64 decode: %v", err)
	}
	want := username + ":" + password
	if string(decoded) != want {
		t.Errorf("decoded creds = %q, want %q", decoded, want)
	}
}

func TestParseToolsetConfig_BasicAuth_EnvVarPlaceholders(t *testing.T) {
	raw := map[string]any{
		"tool_type": "mcp",
		"connection_params": map[string]any{
			"type": "sse",
			"url":  "https://mcp.example.com/sse",
			"auth": map[string]any{
				"type":     "basic",
				"username": "${MCP_USER}",
				"password": "${MCP_PASS}",
			},
		},
	}

	cfg, err := ParseToolsetConfig(raw)
	if err != nil {
		t.Fatalf("ParseToolsetConfig: %v", err)
	}
	header := cfg.Headers["Authorization"]
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(header, "Basic "))
	if err != nil {
		t.Fatalf("base64 decode: %v", err)
	}
	if string(decoded) != "${MCP_USER}:${MCP_PASS}" {
		t.Errorf("decoded creds = %q, want %q (placeholders should survive parse)", decoded, "${MCP_USER}:${MCP_PASS}")
	}
}

func TestParseToolsetConfig_BearerAuth_JWTFormat(t *testing.T) {
	const jwt = "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9." +
		"eyJzdWIiOiIxMjM0NTY3ODkwIiwibmFtZSI6IkpvaG4gRG9lIiwiaWF0IjoxNTE2MjM5MDIyfQ." +
		"SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c"

	raw := map[string]any{
		"tool_type": "mcp",
		"connection_params": map[string]any{
			"type": "streamable-http",
			"url":  "https://mcp.example.com/mcp",
		},
		"auth": map[string]any{
			"type":       "bearer",
			"credential": map[string]any{"token": jwt},
		},
	}

	cfg, err := ParseToolsetConfig(raw)
	if err != nil {
		t.Fatalf("ParseToolsetConfig: %v", err)
	}
	if cfg.AuthType != "bearer" {
		t.Errorf("AuthType = %q, want bearer", cfg.AuthType)
	}
	if cfg.AuthConfig == nil || cfg.AuthConfig.Credential == nil {
		t.Fatal("AuthConfig.Credential = nil, want JWT to populate")
	}
	if cfg.AuthConfig.Credential.Token != jwt {
		t.Errorf("Token = %q, want %q (JWT should round-trip)", cfg.AuthConfig.Credential.Token, jwt)
	}
}

func TestParseToolsetConfig_BearerAuth_EnvVarPlaceholder(t *testing.T) {
	raw := map[string]any{
		"tool_type": "mcp",
		"connection_params": map[string]any{
			"type": "streamable-http",
			"url":  "https://mcp.example.com/mcp",
		},
		"auth": map[string]any{
			"type":       "bearer",
			"credential": map[string]any{"token": "${MCP_BEARER_TOKEN}"},
		},
	}

	cfg, err := ParseToolsetConfig(raw)
	if err != nil {
		t.Fatalf("ParseToolsetConfig: %v", err)
	}
	if cfg.AuthConfig == nil || cfg.AuthConfig.Credential == nil {
		t.Fatal("AuthConfig.Credential = nil, want placeholder preserved")
	}
	if cfg.AuthConfig.Credential.Token != "${MCP_BEARER_TOKEN}" {
		t.Errorf("Token = %q, want %q (placeholder must survive parse)",
			cfg.AuthConfig.Credential.Token, "${MCP_BEARER_TOKEN}")
	}
}

func TestParseToolsetConfig_NilConfig(t *testing.T) {
	_, err := ParseToolsetConfig(nil)
	if err == nil {
		t.Fatal("expected error for nil config")
	}
	if !strings.Contains(err.Error(), "connection_params") {
		t.Errorf("err should mention 'connection_params', got: %v", err)
	}

	_, err = ParseToolsetConfig(map[string]any{})
	if err == nil {
		t.Fatal("expected error for empty config")
	}
}

func TestParseToolsetConfig_EmptyAuthBlock(t *testing.T) {
	raw := map[string]any{
		"tool_type": "mcp",
		"connection_params": map[string]any{
			"type": "sse",
			"url":  "https://mcp.example.com/sse",
		},
		"auth": map[string]any{},
	}

	cfg, err := ParseToolsetConfig(raw)
	if err != nil {
		t.Fatalf("ParseToolsetConfig: %v", err)
	}
	if cfg.AuthType != "" {
		t.Errorf("AuthType = %q, want \"\" (empty block has no type)", cfg.AuthType)
	}
	if cfg.AuthConfig != nil {
		t.Errorf("AuthConfig = %+v, want nil (empty auth block)", cfg.AuthConfig)
	}
}

func TestParseToolsetConfig_UnsupportedAuthType(t *testing.T) {
	raw := map[string]any{
		"tool_type": "mcp",
		"connection_params": map[string]any{
			"type": "sse",
			"url":  "https://mcp.example.com/sse",
		},
		"auth": map[string]any{
			"type": "some_unknown_auth_type",
		},
	}

	cfg, err := ParseToolsetConfig(raw)
	if err != nil {
		t.Fatalf("ParseToolsetConfig: %v (unknown auth type should not block parse)", err)
	}
	if cfg.AuthType != "some_unknown_auth_type" {
		t.Errorf("AuthType = %q, want literal 'some_unknown_auth_type'", cfg.AuthType)
	}
	if cfg.AuthConfig == nil || cfg.AuthConfig.Type != "some_unknown_auth_type" {
		t.Errorf("AuthConfig.Type = %+v, want unknown type preserved", cfg.AuthConfig)
	}
}

func TestParseToolsetConfig_AuthIndependent_BasicThenBearer(t *testing.T) {
	basicRaw := map[string]any{
		"tool_type": "mcp",
		"connection_params": map[string]any{
			"type": "sse",
			"url":  "https://mcp.example.com/sse",
			"auth": map[string]any{
				"type":     "basic",
				"username": "u",
				"password": "p",
			},
		},
	}
	bearerRaw := map[string]any{
		"tool_type": "mcp",
		"connection_params": map[string]any{
			"type": "streamable-http",
			"url":  "https://mcp.example.com/mcp",
		},
		"auth": map[string]any{
			"type":       "bearer",
			"credential": map[string]any{"token": "tok-1"},
		},
	}

	basicCfg, err := ParseToolsetConfig(basicRaw)
	if err != nil {
		t.Fatalf("basic ParseToolsetConfig: %v", err)
	}
	bearerCfg, err := ParseToolsetConfig(bearerRaw)
	if err != nil {
		t.Fatalf("bearer ParseToolsetConfig: %v", err)
	}

	if got := basicCfg.Headers["Authorization"]; !strings.HasPrefix(got, "Basic ") {
		t.Errorf("basicCfg.Headers[Authorization] = %q, want 'Basic ' prefix", got)
	}
	if basicCfg.AuthType != "" {
		t.Errorf("basicCfg.AuthType = %q, want \"\" (basic via headers, not auth block)", basicCfg.AuthType)
	}
	if bearerCfg.AuthType != "bearer" {
		t.Errorf("bearerCfg.AuthType = %q, want bearer", bearerCfg.AuthType)
	}
	if bearerCfg.AuthConfig == nil || bearerCfg.AuthConfig.Credential == nil ||
		bearerCfg.AuthConfig.Credential.Token != "tok-1" {
		t.Errorf("bearerCfg.AuthConfig.Credential.Token = %+v, want tok-1", bearerCfg.AuthConfig)
	}
	basicCfg.Headers["Authorization"] = "Mutated"
	if bearerCfg.Headers["Authorization"] == "Mutated" {
		t.Error("mutating basicCfg.Headers leaked into bearerCfg — they share state")
	}
}

func TestParseToolsetConfig_SameAuth_DifferentTransports(t *testing.T) {
	authBlock := map[string]any{
		"type":       "bearer",
		"credential": map[string]any{"token": "same-token"},
	}
	mkRaw := func(transport, url string) map[string]any {
		return map[string]any{
			"tool_type": "mcp",
			"connection_params": map[string]any{
				"type": transport,
				"url":  url,
			},
			"auth": authBlock,
		}
	}

	sseCfg, err := ParseToolsetConfig(mkRaw("sse", "https://mcp.example.com/sse"))
	if err != nil {
		t.Fatalf("SSE ParseToolsetConfig: %v", err)
	}
	httpCfg, err := ParseToolsetConfig(mkRaw("streamable-http", "https://mcp.example.com/mcp"))
	if err != nil {
		t.Fatalf("HTTP ParseToolsetConfig: %v", err)
	}

	if sseCfg.AuthConfig == nil || httpCfg.AuthConfig == nil {
		t.Fatal("AuthConfig nil on one transport")
	}
	if sseCfg.AuthConfig.Type != httpCfg.AuthConfig.Type {
		t.Errorf("Type mismatch: sse=%q http=%q", sseCfg.AuthConfig.Type, httpCfg.AuthConfig.Type)
	}
	if sseCfg.AuthConfig.Credential.Token != httpCfg.AuthConfig.Credential.Token {
		t.Errorf("Token mismatch: sse=%q http=%q",
			sseCfg.AuthConfig.Credential.Token, httpCfg.AuthConfig.Credential.Token)
	}
}

func TestParseToolsetConfig_RealWorld_GitHubBearer(t *testing.T) {
	raw := map[string]any{
		"tool_type": "mcp",
		"connection_params": map[string]any{
			"type": "streamable-http",
			"url":  "https://api.githubcopilot.com/mcp/",
		},
		"auth": map[string]any{
			"type":       "bearer",
			"credential": map[string]any{"token": "${GITHUB_PAT}"},
		},
	}

	cfg, err := ParseToolsetConfig(raw)
	if err != nil {
		t.Fatalf("ParseToolsetConfig: %v", err)
	}
	if cfg.AuthType != "bearer" || cfg.URL != "https://api.githubcopilot.com/mcp/" {
		t.Errorf("AuthType/URL = %q/%q", cfg.AuthType, cfg.URL)
	}
	if cfg.AuthConfig == nil || cfg.AuthConfig.Credential == nil ||
		cfg.AuthConfig.Credential.Token != "${GITHUB_PAT}" {
		t.Errorf("token placeholder not preserved: %+v", cfg.AuthConfig)
	}
}

func TestParseToolsetConfig_RealWorld_Context7Bearer(t *testing.T) {
	raw := map[string]any{
		"tool_type": "mcp",
		"connection_params": map[string]any{
			"type": "streamable-http",
			"url":  "https://mcp.context7.com/mcp",
		},
		"auth": map[string]any{
			"type":       "bearer",
			"credential": map[string]any{"token": "${CONTEXT7_API_KEY}"},
		},
	}

	cfg, err := ParseToolsetConfig(raw)
	if err != nil {
		t.Fatalf("ParseToolsetConfig: %v", err)
	}
	if cfg.AuthType != "bearer" {
		t.Errorf("AuthType = %q, want bearer", cfg.AuthType)
	}
	if cfg.URL != "https://mcp.context7.com/mcp" {
		t.Errorf("URL = %q, want context7 URL", cfg.URL)
	}
	if cfg.AuthConfig == nil || cfg.AuthConfig.Credential == nil ||
		cfg.AuthConfig.Credential.Token != "${CONTEXT7_API_KEY}" {
		t.Errorf("token placeholder not preserved: %+v", cfg.AuthConfig)
	}
}

func TestParseToolsetConfig_RealWorld_PrivateBasic(t *testing.T) {
	raw := map[string]any{
		"tool_type": "mcp",
		"connection_params": map[string]any{
			"type": "sse",
			"url":  "https://private-mcp.example.com/v1/sse",
			"auth": map[string]any{
				"type":     "basic",
				"username": "${PRIVATE_MCP_USER}",
				"password": "${PRIVATE_MCP_PASS}",
			},
		},
	}

	cfg, err := ParseToolsetConfig(raw)
	if err != nil {
		t.Fatalf("ParseToolsetConfig: %v", err)
	}
	header := cfg.Headers["Authorization"]
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(header, "Basic "))
	if err != nil {
		t.Fatalf("base64 decode: %v", err)
	}
	if string(decoded) != "${PRIVATE_MCP_USER}:${PRIVATE_MCP_PASS}" {
		t.Errorf("decoded creds = %q, want placeholders preserved", decoded)
	}
}

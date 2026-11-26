// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"errors"
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

const testTLSDir = "/etc/sam/mcp-tls"

func sslValues(ssl any) map[string]any {
	return map[string]any{"server_url": "https://mcp.example.com/mcp", "ssl_config": ssl}
}

func detailKeys(t *testing.T, err error) []string {
	t.Helper()
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("err = %v (%T), want *ValidationError", err, err)
	}
	return slices.Sorted(maps.Keys(ve.Details))
}

func TestValidateMCPSSLConfig_Shape(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		values   map[string]any
		wantKeys []string
	}{
		{"absent", map[string]any{"server_url": "https://mcp.example.com/mcp"}, nil},
		{"null", sslValues(nil), nil},
		{"empty map", sslValues(map[string]any{}), nil},
		{"server_name alone", sslValues(map[string]any{"server_name": "mcp.internal"}), nil},
		{"verify true", sslValues(map[string]any{"verify": true}), []string{"values.ssl_config.verify"}},
		{"verify false", sslValues(map[string]any{"verify": false}), []string{"values.ssl_config.verify"}},
		{"unknown key", sslValues(map[string]any{"ca_cert": "/x"}), []string{"values.ssl_config.ca_cert"}},
		{"not a map", sslValues("/etc/sam/mcp-tls"), []string{"values.ssl_config"}},
		{"non-string value", sslValues(map[string]any{"server_name": 5}), []string{"values.ssl_config.server_name"}},
		{"null value", sslValues(map[string]any{"server_name": nil}), []string{"values.ssl_config.server_name"}},
		{
			"every problem at once",
			sslValues(map[string]any{"verify": false, "ca_cert": "/x", "server_name": 5}),
			[]string{"values.ssl_config.ca_cert", "values.ssl_config.server_name", "values.ssl_config.verify"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := validateMCPSSLConfig(tt.values)
			if tt.wantKeys == nil {
				if err != nil {
					t.Fatalf("validateMCPSSLConfig = %v, want nil", err)
				}
				return
			}
			if got := detailKeys(t, err); !slices.Equal(got, tt.wantKeys) {
				t.Errorf("details = %v, want %v (err: %v)", got, tt.wantKeys, err)
			}
		})
	}
}

func TestValidateMCPSSLConfig_VerifyRefusalPointsToTheOperatorSwitch(t *testing.T) {
	t.Parallel()
	err := validateMCPSSLConfig(sslValues(map[string]any{"verify": false}))
	if err == nil || !strings.Contains(err.Error(), mcpTLSVerifyEnvVar) {
		t.Fatalf("err = %v, want it to name %s", err, mcpTLSVerifyEnvVar)
	}
}

func files(cert, key string) map[string]any {
	return map[string]any{"client_cert": cert, "client_key": key}
}

func TestValidateMCPSSLConfig_FilesMustLieInTheConfiguredDirectory(t *testing.T) {
	inDir := func(name string) string { return testTLSDir + "/" + name }
	tests := []struct {
		name     string
		dir      string
		ssl      map[string]any
		wantKeys []string
	}{
		{"directory unset, ca_bundle", "", map[string]any{"ca_bundle": inDir("ca.pem")}, []string{"values.ssl_config.ca_bundle"}},
		{"directory unset, client pair", "", files(inDir("c.crt"), inDir("c.key")),
			[]string{"values.ssl_config.client_cert", "values.ssl_config.client_key"}},
		{"directory unset, server_name alone", "", map[string]any{"server_name": "mcp.internal"}, nil},
		{"directory relative", "certs", map[string]any{"ca_bundle": inDir("ca.pem")}, []string{"values.ssl_config.ca_bundle"}},

		{"valid paths", testTLSDir, map[string]any{
			"ca_bundle": inDir("ca.pem"), "client_cert": inDir("c.crt"), "client_key": inDir("c.key"), "server_name": "mcp.internal",
		}, nil},
		{"directory given with a trailing slash", testTLSDir + "/", files(inDir("c.crt"), inDir("c.key")), nil},
		{"file in a subdirectory", testTLSDir, files(inDir("team-a/c.crt"), inDir("team-a/c.key")), nil},

		{"relative path", testTLSDir, files("c.crt", inDir("c.key")), []string{"values.ssl_config.client_cert"}},
		{"dot-dot out of the directory", testTLSDir, files(inDir("../shadow"), inDir("c.key")), []string{"values.ssl_config.client_cert"}},
		{"dot-dot that stays inside", testTLSDir, files(inDir("sub/../c.crt"), inDir("c.key")), []string{"values.ssl_config.client_cert"}},
		{"repeated slash", testTLSDir, files(testTLSDir+"//c.crt", inDir("c.key")), []string{"values.ssl_config.client_cert"}},
		{"trailing slash", testTLSDir, files(inDir("c.crt")+"/", inDir("c.key")), []string{"values.ssl_config.client_cert"}},
		{"outside the directory", testTLSDir, files("/etc/passwd", inDir("c.key")), []string{"values.ssl_config.client_cert"}},
		{"sibling that shares the directory's prefix", testTLSDir, files("/etc/sam/mcp-tls-x/c.crt", inDir("c.key")),
			[]string{"values.ssl_config.client_cert"}},
		{"the directory itself", testTLSDir, map[string]any{"ca_bundle": testTLSDir}, []string{"values.ssl_config.ca_bundle"}},

		{"cert without key", testTLSDir, map[string]any{"client_cert": inDir("c.crt")}, []string{"values.ssl_config.client_key"}},
		{"key without cert", testTLSDir, map[string]any{"client_key": inDir("c.key")}, []string{"values.ssl_config.client_cert"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(mcpTLSDirEnvVar, tt.dir)
			err := validateMCPSSLConfig(sslValues(tt.ssl))
			if tt.wantKeys == nil {
				if err != nil {
					t.Fatalf("validateMCPSSLConfig = %v, want nil", err)
				}
				return
			}
			if got := detailKeys(t, err); !slices.Equal(got, tt.wantKeys) {
				t.Errorf("details = %v, want %v (err: %v)", got, tt.wantKeys, err)
			}
		})
	}
}

func TestValidateMCPSSLConfig_UnsetDirectoryNamesTheVariable(t *testing.T) {
	t.Setenv(mcpTLSDirEnvVar, "")
	err := validateMCPSSLConfig(sslValues(map[string]any{"ca_bundle": testTLSDir + "/ca.pem"}))
	if err == nil || !strings.Contains(err.Error(), mcpTLSDirEnvVar) {
		t.Fatalf("err = %v, want it to name %s", err, mcpTLSDirEnvVar)
	}
}

func TestMCPDiscoveryService_RefusesSSLPathsBeforeReadingAnyFile(t *testing.T) {
	t.Setenv(mcpTLSDirEnvVar, testTLSDir)
	svc := NewMCPDiscoveryService(discardLogger(), nil)

	_, err := svc.DiscoverTools(t.Context(), MCPDiscoverRequest{
		ServerURL: "https://mcp.example.com/mcp",
		SSLConfig: &MCPSSLConfig{CABundle: "/etc/passwd", ClientCert: testTLSDir + "/c.crt"},
	})

	want := []string{"sslConfig.caBundle", "sslConfig.clientKey"}
	if got := detailKeys(t, err); !slices.Equal(got, want) {
		t.Errorf("details = %v, want %v (err: %v)", got, want, err)
	}
}

func TestConnectorService_RefusesSSLConfigVerify_OnCreateAndOnUpdate(t *testing.T) {
	t.Parallel()
	svc := newConnectorServiceForTest(t)
	ctx := t.Context()
	insecure := map[string]any{"verify": false}

	_, err := svc.Create(ctx, CreateConnectorRequest{
		Name: "mcp-verify-off", Type: "mcp", Subtype: "remote", Values: sslValues(insecure),
	}, "user-1")
	if got := detailKeys(t, err); !slices.Equal(got, []string{"values.ssl_config.verify"}) {
		t.Errorf("create details = %v, want the verify key", got)
	}

	created, err := svc.Create(ctx, CreateConnectorRequest{
		Name: "mcp-plain", Type: "mcp", Subtype: "remote", Values: sslValues(nil),
	}, "user-1")
	if err != nil {
		t.Fatalf("Create without ssl_config: %v", err)
	}
	_, err = svc.Update(ctx, created.ID, UpdateConnectorRequest{Values: sslValues(insecure)}, "user-1")
	if got := detailKeys(t, err); !slices.Equal(got, []string{"values.ssl_config.verify"}) {
		t.Errorf("update details = %v, want the verify key", got)
	}
	stored, err := svc.GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if values, _ := stored.Values.(map[string]any); values["ssl_config"] != nil {
		t.Errorf("a refused update left ssl_config on the stored connector: %v", values["ssl_config"])
	}
}

func TestConnectorService_RefusesSSLFilesOutsideTheDirectory_OnCreateAndOnUpdate(t *testing.T) {
	t.Setenv(mcpTLSDirEnvVar, testTLSDir)
	svc := newConnectorServiceForTest(t)
	ctx := t.Context()
	outside := map[string]any{"ca_bundle": "/etc/sam/mcp-tls-x/ca.pem"}
	inside := map[string]any{"ca_bundle": testTLSDir + "/ca.pem"}
	wantKeys := []string{"values.ssl_config.ca_bundle"}

	_, err := svc.Create(ctx, CreateConnectorRequest{
		Name: "mcp-outside", Type: "mcp", Subtype: "remote", Values: sslValues(outside),
	}, "user-1")
	if got := detailKeys(t, err); !slices.Equal(got, wantKeys) {
		t.Errorf("create details = %v, want %v", got, wantKeys)
	}

	created, err := svc.Create(ctx, CreateConnectorRequest{
		Name: "mcp-inside", Type: "mcp", Subtype: "remote", Values: sslValues(inside),
	}, "user-1")
	if err != nil {
		t.Fatalf("Create with a file inside the directory: %v", err)
	}
	_, err = svc.Update(ctx, created.ID, UpdateConnectorRequest{Values: sslValues(outside)}, "user-1")
	if got := detailKeys(t, err); !slices.Equal(got, wantKeys) {
		t.Errorf("update details = %v, want %v", got, wantKeys)
	}
	stored, err := svc.GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	values, _ := stored.Values.(map[string]any)
	if ssl, _ := values["ssl_config"].(map[string]any); ssl["ca_bundle"] != inside["ca_bundle"] {
		t.Errorf("a refused update changed the stored ssl_config to %v", values["ssl_config"])
	}
}

func TestConnectorService_RefusesThePlatformsOwnTLSFiles_EvenThroughALink(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(mcpTLSDirEnvVar, dir)
	certs := filepath.Join(dir, "certs")
	mustMkdir(t, certs)
	t.Setenv("SSL_CERTFILE", writeTestFile(t, certs, "tls.crt", []byte("server certificate")))
	t.Setenv("SSL_KEYFILE", writeTestFile(t, certs, "tls.key", []byte("server key")))
	mustSymlink(t, filepath.Join("certs", "tls.key"), filepath.Join(dir, "alias.key"))
	clientCert := writeTestFile(t, dir, "client.crt", []byte("client certificate"))
	clientKey := writeTestFile(t, dir, "client.key", []byte("client key"))
	svc := newConnectorServiceForTest(t)

	tests := []struct {
		name     string
		ssl      map[string]any
		wantKeys []string
	}{
		{
			name:     "the server's certificate and key",
			ssl:      map[string]any{"client_cert": filepath.Join(certs, "tls.crt"), "client_key": filepath.Join(certs, "tls.key")},
			wantKeys: []string{"values.ssl_config.client_cert", "values.ssl_config.client_key"},
		},
		{
			name:     "a link to the server's key",
			ssl:      map[string]any{"client_cert": clientCert, "client_key": filepath.Join(dir, "alias.key")},
			wantKeys: []string{"values.ssl_config.client_key"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := svc.Create(t.Context(), CreateConnectorRequest{
				Name: "mcp-server-identity", Type: "mcp", Subtype: "remote", Values: sslValues(tt.ssl),
			}, "user-1")
			if got := detailKeys(t, err); !slices.Equal(got, tt.wantKeys) {
				t.Fatalf("details = %v, want %v", got, tt.wantKeys)
			}
			var ve *ValidationError
			errors.As(err, &ve)
			for _, key := range tt.wantKeys {
				if !slices.Contains(ve.Details[key], errTLSPathServerTLSFile.Error()) {
					t.Errorf("%s = %v, want %q", key, ve.Details[key], errTLSPathServerTLSFile)
				}
			}
		})
	}

	if _, err := svc.Create(t.Context(), CreateConnectorRequest{
		Name: "mcp-own-client-files", Type: "mcp", Subtype: "remote",
		Values: sslValues(map[string]any{"client_cert": clientCert, "client_key": clientKey}),
	}, "user-1"); err != nil {
		t.Fatalf("Create with the connector's own client files: %v", err)
	}
}

func TestUpdateConnectorWithRedeploy_RefusesTheSSLConfigTheServiceRefuses(t *testing.T) {
	t.Setenv(mcpTLSDirEnvVar, testTLSDir)
	db := testOpenDB(t)
	svc := NewConnectorService(store.NewConnectorRepo(db), nil, "test-ns", discardLogger())
	created, err := svc.Create(t.Context(), CreateConnectorRequest{
		Name: "mcp-builder", Type: "mcp", Subtype: "remote", Values: sslValues(nil),
	}, "user-1")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	refused := map[string]struct {
		ssl     map[string]any
		wantKey string
	}{
		"verify":          {map[string]any{"verify": false}, "values.ssl_config.verify"},
		"outside the dir": {map[string]any{"ca_bundle": "/etc/passwd"}, "values.ssl_config.ca_bundle"},
	}
	for name, tc := range refused {
		t.Run(name, func(t *testing.T) {
			_, err := UpdateConnectorWithRedeploy(t.Context(), db, svc, ConnectorRedeployUpdate{
				ID: created.ID, Request: UpdateConnectorRequest{Values: sslValues(tc.ssl)}, UserID: "user-1",
			}, discardLogger())
			if got := detailKeys(t, err); !slices.Equal(got, []string{tc.wantKey}) {
				t.Errorf("details = %v, want %v", got, tc.wantKey)
			}
		})
	}
}

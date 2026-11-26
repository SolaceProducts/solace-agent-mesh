// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"encoding/json"
	"log/slog"
	"maps"
	"reflect"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func storedSSLConfig(t *testing.T, svc *ConnectorService, id string) (any, bool) {
	t.Helper()
	stored, err := svc.GetByID(t.Context(), id)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	values, _ := stored.Values.(map[string]any)
	ssl, present := values["ssl_config"]
	return ssl, present
}

func TestConnectorService_AnUpdateThatLeavesSSLConfigOut_KeepsTheCertificateForDiscoveryAndTheAgent(t *testing.T) {
	t.Setenv(allowPrivateMCPEnvVar, "true")
	pki := newMTLSFixture(t)
	pki.useAsTLSDir(t)
	server := startTLSMCPServer(t, pki, true)
	svc := newConnectorServiceWithDiscovery(t)
	ssl := map[string]any{"ca_bundle": pki.caBundle, "client_cert": pki.clientCert, "client_key": pki.clientKey}
	created, err := svc.Create(t.Context(), CreateConnectorRequest{
		Name: "orders-mcp", Type: "mcp", Subtype: "remote",
		Values: map[string]any{"server_url": server.URL, "connection_type": "streamable-http", "ssl_config": ssl},
	}, "user-1")
	if err != nil {
		t.Fatalf("seed Create: %v", err)
	}

	resp, err := svc.UpdateWithRedeploy(t.Context(), created.ID, UpdateConnectorRequest{
		Values: map[string]any{"server_url": server.URL, "connection_type": "streamable-http", "auth_type": "none"},
	}, "user-1", ConnectorUpdateOptions{})

	if err != nil {
		t.Fatalf("UpdateWithRedeploy without ssl_config: %v", err)
	}
	if len(resp.Warnings) != 0 {
		t.Errorf("Warnings = %q, want none: the save lists the tools with the stored certificate", resp.Warnings)
	}
	if kept, _ := storedSSLConfig(t, svc, created.ID); !reflect.DeepEqual(kept, ssl) {
		t.Fatalf("stored ssl_config = %v, want the one from before the update %v", kept, ssl)
	}
	row, err := svc.connectorRepo.GetByID(t.Context(), created.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	conn, err := connectorConfigFromRow(row)
	if err != nil {
		t.Fatalf("connectorConfigFromRow: %v", err)
	}
	params, _ := buildConnectorTool(conn)["connection_params"].(map[string]any)
	if !reflect.DeepEqual(params["ssl_config"], ssl) {
		t.Errorf("rendered ssl_config = %v, want %v", params["ssl_config"], ssl)
	}
}

func TestConnectorService_SSLConfigOnUpdate_LeftOutOrNullKeepsIt_EmptyRemovesIt(t *testing.T) {
	t.Parallel()
	stored := map[string]any{"server_name": "mcp.internal"}
	replacement := map[string]any{"server_name": "mcp.other"}
	cases := map[string]struct {
		stored      any
		sent        func(values map[string]any)
		wantPresent bool
		want        any
	}{
		"left out keeps it":              {stored: stored, sent: func(map[string]any) {}, wantPresent: true, want: stored},
		"null keeps it":                  {stored: stored, sent: func(v map[string]any) { v["ssl_config"] = nil }, wantPresent: true, want: stored},
		"an empty block removes it":      {stored: stored, sent: func(v map[string]any) { v["ssl_config"] = map[string]any{} }},
		"a new block replaces it":        {stored: stored, sent: func(v map[string]any) { v["ssl_config"] = replacement }, wantPresent: true, want: replacement},
		"left out over none stores none": {sent: func(map[string]any) {}},
		"null over none stores none":     {sent: func(v map[string]any) { v["ssl_config"] = nil }},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			svc := newConnectorServiceForTest(t)
			seed := map[string]any{"server_url": "https://mcp.example.com/mcp"}
			if tc.stored != nil {
				seed["ssl_config"] = tc.stored
			}
			created, err := svc.Create(t.Context(), CreateConnectorRequest{Name: "mcp-kept", Type: "mcp", Subtype: "remote", Values: seed}, "user-1")
			if err != nil {
				t.Fatalf("seed Create: %v", err)
			}
			update := map[string]any{"server_url": "https://mcp.example.com/mcp"}
			tc.sent(update)

			if _, err := svc.Update(t.Context(), created.ID, UpdateConnectorRequest{Values: update}, "user-1"); err != nil {
				t.Fatalf("Update: %v", err)
			}

			got, present := storedSSLConfig(t, svc, created.ID)
			if present != tc.wantPresent || !reflect.DeepEqual(got, tc.want) {
				t.Errorf("stored ssl_config = %v (present %v), want %v (present %v)", got, present, tc.want, tc.wantPresent)
			}
		})
	}
}

func TestConnectorService_AStoredSSLConfigTheChecksNowRefuse_DoesNotBlockAnUpdateThatLeavesItOut(t *testing.T) {
	t.Setenv(mcpTLSDirEnvVar, testTLSDir)
	svc := newConnectorServiceForTest(t)
	legacy := map[string]any{"verify": false, "ca_bundle": "/etc/pki/ca.pem"}
	raw, err := json.Marshal(map[string]any{"server_url": "https://mcp.example.com/mcp", "ssl_config": legacy})
	if err != nil {
		t.Fatal(err)
	}
	subtype, values := "remote", string(raw)
	row := &store.Connector{Name: "mcp-from-an-older-release", Type: "mcp", Subtype: &subtype, Values: &values, CreatedBy: "user-1", UpdatedBy: "user-1"}
	if err = svc.connectorRepo.Create(t.Context(), row); err != nil {
		t.Fatalf("seed the row an older release stored: %v", err)
	}

	_, err = svc.Update(t.Context(), row.ID, UpdateConnectorRequest{
		Values: map[string]any{"server_url": "https://mcp.example.com/mcp", "connection_type": "streamable-http"},
	}, "user-1")

	if err != nil {
		t.Fatalf("Update that leaves ssl_config out: %v", err)
	}
	if kept, _ := storedSSLConfig(t, svc, row.ID); !reflect.DeepEqual(kept, legacy) {
		t.Errorf("stored ssl_config = %v, want the stored one kept as it was %v", kept, legacy)
	}
	_, err = svc.Update(t.Context(), row.ID, UpdateConnectorRequest{
		Values: map[string]any{"server_url": "https://mcp.example.com/mcp", "ssl_config": legacy},
	}, "user-1")
	if keys := detailKeys(t, err); !reflect.DeepEqual(keys, []string{"values.ssl_config.ca_bundle", "values.ssl_config.verify"}) {
		t.Errorf("sending the same block: details = %v, want the two refused keys", keys)
	}
}

func TestBuildMCPConnectorTool_LeavesOutAStoredSSLConfigThePlatformWouldRefuse(t *testing.T) {
	inDir := func(name string) string { return testTLSDir + "/" + name }
	tests := []struct {
		name       string
		dir        string
		ssl        map[string]any
		wantInTool map[string]any
	}{
		{"files inside the directory", testTLSDir,
			map[string]any{"ca_bundle": inDir("ca.pem"), "client_cert": inDir("c.crt"), "client_key": inDir("c.key"), "server_name": "mcp.internal"},
			map[string]any{"ca_bundle": inDir("ca.pem"), "client_cert": inDir("c.crt"), "client_key": inDir("c.key"), "server_name": "mcp.internal"}},
		{"server_name alone, directory unset", "", map[string]any{"server_name": "mcp.internal"}, map[string]any{"server_name": "mcp.internal"}},
		{"a file outside the directory", testTLSDir, map[string]any{"ca_bundle": "/etc/pki/ca.pem", "server_name": "mcp.internal"}, nil},
		{"a file while the directory is unset", "", map[string]any{"ca_bundle": inDir("ca.pem")}, nil},
		{"a path with dot-dot", testTLSDir, map[string]any{"ca_bundle": inDir("../ca.pem")}, nil},
		{"a certificate without its key", testTLSDir, map[string]any{"client_cert": inDir("c.crt")}, nil},
		{"a value that is not a string", testTLSDir, map[string]any{"server_name": 5}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(mcpTLSDirEnvVar, tt.dir)
			params, _ := buildConnectorTool(mcpConnector(map[string]any{
				"server_url": "https://mcp.example.com/mcp", "ssl_config": maps.Clone(tt.ssl),
			}))["connection_params"].(map[string]any)

			got, rendered := params["ssl_config"]
			if tt.wantInTool == nil {
				if rendered {
					t.Errorf("rendered ssl_config = %v, want it left out", got)
				}
				return
			}
			if !reflect.DeepEqual(got, tt.wantInTool) {
				t.Errorf("rendered ssl_config = %v, want %v", got, tt.wantInTool)
			}
		})
	}
}

func TestBuildMCPConnectorTool_WarnsWhenItLeavesOutTheSSLConfig(t *testing.T) {
	t.Setenv(mcpTLSDirEnvVar, testTLSDir)
	logs := &syncedBuffer{}
	prevDefault := slog.Default()
	defer slog.SetDefault(prevDefault)
	slog.SetDefault(slog.New(slog.NewTextHandler(logs, nil)))

	conn := mcpConnector(map[string]any{"server_url": "https://mcp.example.com/mcp", "ssl_config": map[string]any{"ca_bundle": "/etc/pki/ca.pem"}})
	buildConnectorTool(conn)

	out := logs.String()
	for _, want := range []string{"level=WARN", msgUndeployableSSLConfig, "connectorID=" + conn.ID, "ssl_config.ca_bundle must be a file inside " + testTLSDir} {
		if !strings.Contains(out, want) {
			t.Errorf("log %q does not contain %q", out, want)
		}
	}
}

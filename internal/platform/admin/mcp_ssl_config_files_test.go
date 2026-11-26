// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	mcppkg "github.com/SolaceProducts/solace-agent-mesh/internal/tool/mcp"
)

var fixtureFileNames = []string{"ca.pem", "client.crt", "client.key"}

func (f mtlsFixture) copyInto(t *testing.T, dir string) {
	t.Helper()
	for _, name := range fixtureFileNames {
		data, err := os.ReadFile(filepath.Join(f.dir, name))
		if err != nil {
			t.Fatalf("read fixture %s: %v", name, err)
		}
		writeTestFile(t, dir, name, data)
	}
}

func mustSymlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("symlink %s -> %s: %v", link, target, err)
	}
}

func mustMkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
}

func kubernetesSecretMount(t *testing.T, dir, version string, from mtlsFixture) {
	t.Helper()
	mustMkdir(t, filepath.Join(dir, version))
	from.copyInto(t, filepath.Join(dir, version))
	mustSymlink(t, version, filepath.Join(dir, "..data"))
	for _, name := range fixtureFileNames {
		mustSymlink(t, filepath.Join("..data", name), filepath.Join(dir, name))
	}
}

func discoverWithFilesIn(t *testing.T, server string, dir string) error {
	t.Helper()
	_, err := NewMCPDiscoveryService(discardLogger(), nil).DiscoverTools(t.Context(), MCPDiscoverRequest{
		ServerURL:      server,
		ConnectionType: "streamable-http",
		SSLConfig: &MCPSSLConfig{
			CABundle:   filepath.Join(dir, "ca.pem"),
			ClientCert: filepath.Join(dir, "client.crt"),
			ClientKey:  filepath.Join(dir, "client.key"),
		},
	})
	return err
}

func TestMCPDiscoveryService_ReadsAKubernetesSecretMount(t *testing.T) {
	t.Setenv(allowPrivateMCPEnvVar, "true")
	trusted := newMTLSFixture(t)
	untrusted := newMTLSFixture(t)
	server := startTLSMCPServer(t, trusted, true)
	mount := t.TempDir()
	t.Setenv(mcpTLSDirEnvVar, mount)
	kubernetesSecretMount(t, mount, "..2026_11_16_10_00_00.111", trusted)

	if err := discoverWithFilesIn(t, server.URL, mount); err != nil {
		t.Fatalf("discovery through the Secret mount's links: %v", err)
	}

	rotated := filepath.Join(mount, "..2026_11_16_11_00_00.222")
	mustMkdir(t, rotated)
	trusted.copyInto(t, rotated)
	for name, source := range map[string]string{"client.crt": untrusted.clientCert, "client.key": untrusted.clientKey} {
		data, err := os.ReadFile(source)
		if err != nil {
			t.Fatalf("read %s: %v", source, err)
		}
		writeTestFile(t, rotated, name, data)
	}
	mustSymlink(t, "..2026_11_16_11_00_00.222", filepath.Join(mount, "..data_tmp"))
	if err := os.Rename(filepath.Join(mount, "..data_tmp"), filepath.Join(mount, "..data")); err != nil {
		t.Fatalf("swap ..data: %v", err)
	}
	expectDiscoverySentence(t, discoverWithFilesIn(t, server.URL, mount), msgMCPTLSClientCert)
}

func TestMCPDiscoveryService_ARootedReadRefusesLinksThatLeaveTheDirectory(t *testing.T) {
	t.Setenv(allowPrivateMCPEnvVar, "true")
	pki := newMTLSFixture(t)
	server := startTLSMCPServer(t, pki, true)

	tests := []struct {
		name    string
		link    func(outside, name string) string
		wantErr string
	}{
		{"regular files, the control", nil, ""},
		{"relative link that stays inside", func(_, name string) string { return "inner/" + name }, ""},
		{"relative link that leaves", func(outside, name string) string {
			return filepath.Join("..", filepath.Base(outside), name)
		}, "escapes from parent"},
		{"absolute link to a file outside", func(outside, name string) string { return filepath.Join(outside, name) }, "escapes from parent"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			outside := t.TempDir()
			t.Setenv(mcpTLSDirEnvVar, dir)
			pki.copyInto(t, outside)
			writeTestFile(t, dir, "ca.pem", pki.caPEM)
			if tt.link == nil {
				pki.copyInto(t, dir)
			} else {
				mustMkdir(t, filepath.Join(dir, "inner"))
				pki.copyInto(t, filepath.Join(dir, "inner"))
				for _, name := range []string{"client.crt", "client.key"} {
					mustSymlink(t, tt.link(outside, name), filepath.Join(dir, name))
				}
			}

			err := discoverWithFilesIn(t, server.URL, dir)

			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("discovery: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("err = %v, want it to contain %q", err, tt.wantErr)
			}
		})
	}
}

func TestMCPDiscoveryService_AnAbsoluteLinkIsRefusedEvenWhenItStaysInside(t *testing.T) {
	t.Setenv(allowPrivateMCPEnvVar, "true")
	pki := newMTLSFixture(t)
	server := startTLSMCPServer(t, pki, true)
	dir := t.TempDir()
	t.Setenv(mcpTLSDirEnvVar, dir)
	mustMkdir(t, filepath.Join(dir, "inner"))
	pki.copyInto(t, filepath.Join(dir, "inner"))
	writeTestFile(t, dir, "ca.pem", pki.caPEM)
	for _, name := range []string{"client.crt", "client.key"} {
		mustSymlink(t, filepath.Join(dir, "inner", name), filepath.Join(dir, name))
	}

	if err := discoverWithFilesIn(t, server.URL, dir); err == nil {
		t.Fatal("discovery followed an absolute symlink; os.Root refuses them")
	}
}

func TestDeployedConnector_PresentsItsClientCertificateOnToolCalls(t *testing.T) {
	pki := newMTLSFixture(t)
	pki.useAsTLSDir(t)
	server := startTLSMCPServer(t, pki, true)
	stored := func(ssl map[string]any) map[string]any {
		values := map[string]any{"server_url": server.URL, "connection_type": "streamable-http"}
		if ssl != nil {
			values["ssl_config"] = ssl
		}
		return values
	}
	tests := []struct {
		name    string
		ssl     map[string]any
		wantErr bool
	}{
		{"ssl_config with the CA and the client pair", map[string]any{
			"ca_bundle": pki.caBundle, "client_cert": pki.clientCert, "client_key": pki.clientKey,
		}, false},
		{"ssl_config with the CA only", map[string]any{"ca_bundle": pki.caBundle}, true},
		{"no ssl_config", nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values := stored(tt.ssl)
			if err := validateConnectorValues("mcp", "remote", values); err != nil {
				t.Fatalf("the platform would not store these values: %v", err)
			}
			entry, err := asGenericJSON(buildConnectorTool(mcpConnector(values)))
			if err != nil {
				t.Fatalf("asGenericJSON: %v", err)
			}
			cfg, err := mcppkg.ParseToolsetConfig(entry)
			if err != nil {
				t.Fatalf("ParseToolsetConfig: %v", err)
			}
			client, err := cfg.BuildHTTPClient()
			if err != nil {
				t.Fatalf("BuildHTTPClient: %v", err)
			}

			resp, err := client.Post(server.URL, "application/json",
				strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize"}`))

			if tt.wantErr {
				if err == nil {
					_ = resp.Body.Close()
					t.Fatal("the call succeeded without the client certificate")
				}
				return
			}
			if err != nil {
				t.Fatalf("tool call: %v", err)
			}
			defer func() { _ = resp.Body.Close() }()
			if resp.StatusCode != http.StatusOK {
				t.Errorf("status = %d, want 200", resp.StatusCode)
			}
		})
	}
}

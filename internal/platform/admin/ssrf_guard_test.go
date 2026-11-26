// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"os"
	"strings"
	"testing"

	mcppkg "github.com/SolaceProducts/solace-agent-mesh/internal/tool/mcp"
)

func TestValidateOutboundURL(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		wantErr bool
		substr  string
	}{
		{name: "empty allowed", url: "", wantErr: false},
		{name: "http allowed", url: "http://example.com/mcp", wantErr: false},
		{name: "https allowed", url: "https://example.com/mcp", wantErr: false},
		{name: "ftp rejected", url: "ftp://example.com/", wantErr: true, substr: "scheme"},
		{name: "javascript rejected", url: "javascript:alert(1)", wantErr: true, substr: "scheme"},
		{name: "file rejected", url: "file:///etc/passwd", wantErr: true, substr: "scheme"},
		{name: "missing host", url: "http:///path", wantErr: true, substr: "host"},
		{name: "garbage", url: "://", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateOutboundURL(tt.url)
			if (err != nil) != tt.wantErr {
				t.Fatalf("validateOutboundURL(%q) err=%v, wantErr=%v", tt.url, err, tt.wantErr)
			}
			if err != nil {
				var ve *ValidationError
				if !errors.As(err, &ve) {
					t.Fatalf("expected *ValidationError, got %T: %v", err, err)
				}
				if tt.substr != "" && !strings.Contains(err.Error(), tt.substr) {
					t.Fatalf("error %q missing substring %q", err.Error(), tt.substr)
				}
			}
		})
	}
}

func TestBlockedAddress(t *testing.T) {
	tests := []struct {
		name             string
		ip               string
		blockedStrict    bool
		blockedAllowPriv bool
	}{
		{name: "loopback v4", ip: "127.0.0.1", blockedStrict: true, blockedAllowPriv: false},
		{name: "loopback v6", ip: "::1", blockedStrict: true, blockedAllowPriv: false},
		{name: "unspecified v4", ip: "0.0.0.0", blockedStrict: true, blockedAllowPriv: true},
		{name: "unspecified v6", ip: "::", blockedStrict: true, blockedAllowPriv: true},
		{name: "rfc1918 10/8", ip: "10.0.0.1", blockedStrict: true, blockedAllowPriv: false},
		{name: "rfc1918 172.16/12", ip: "172.16.5.5", blockedStrict: true, blockedAllowPriv: false},
		{name: "rfc1918 192.168/16", ip: "192.168.1.1", blockedStrict: true, blockedAllowPriv: false},
		{name: "AWS IMDS", ip: "169.254.169.254", blockedStrict: true, blockedAllowPriv: true},
		{name: "GCP metadata 169.254", ip: "169.254.169.254", blockedStrict: true, blockedAllowPriv: true},
		{name: "link-local v6", ip: "fe80::1", blockedStrict: true, blockedAllowPriv: true},
		{name: "ipv6 ULA", ip: "fc00::1", blockedStrict: true, blockedAllowPriv: false},
		{name: "broadcast", ip: "255.255.255.255", blockedStrict: true, blockedAllowPriv: true},
		{name: "multicast v4", ip: "224.0.0.1", blockedStrict: true, blockedAllowPriv: true},
		{name: "loopback v4 elsewhere in 127/8", ip: "127.255.255.254", blockedStrict: true, blockedAllowPriv: false},
		{name: "this-network 0/8", ip: "0.1.2.3", blockedStrict: true, blockedAllowPriv: true},
		{name: "cgnat low edge", ip: "100.64.0.0", blockedStrict: true, blockedAllowPriv: false},
		{name: "cgnat high edge", ip: "100.127.255.255", blockedStrict: true, blockedAllowPriv: false},
		{name: "just below cgnat", ip: "100.63.255.255", blockedStrict: false, blockedAllowPriv: false},
		{name: "just above cgnat", ip: "100.128.0.0", blockedStrict: false, blockedAllowPriv: false},
		{name: "just outside rfc1918 172.15", ip: "172.15.255.255", blockedStrict: false, blockedAllowPriv: false},
		{name: "just outside rfc1918 172.32", ip: "172.32.0.0", blockedStrict: false, blockedAllowPriv: false},
		{name: "ipv4-mapped loopback", ip: "::ffff:127.0.0.1", blockedStrict: true, blockedAllowPriv: false},
		{name: "ipv4-mapped rfc1918", ip: "::ffff:10.1.2.3", blockedStrict: true, blockedAllowPriv: false},
		{name: "ipv4-mapped metadata", ip: "::ffff:169.254.169.254", blockedStrict: true, blockedAllowPriv: true},
		{name: "ipv4-mapped public", ip: "::ffff:8.8.8.8", blockedStrict: false, blockedAllowPriv: false},
		{name: "nat64 embedding loopback", ip: "64:ff9b::7f00:1", blockedStrict: true, blockedAllowPriv: false},
		{name: "nat64 embedding metadata", ip: "64:ff9b::a9fe:a9fe", blockedStrict: true, blockedAllowPriv: true},
		{name: "nat64 embedding public", ip: "64:ff9b::808:808", blockedStrict: false, blockedAllowPriv: false},
		{name: "6to4 embedding loopback", ip: "2002:7f00:1::", blockedStrict: true, blockedAllowPriv: false},
		{name: "6to4 embedding rfc1918", ip: "2002:a00:1::", blockedStrict: true, blockedAllowPriv: false},
		{name: "6to4 embedding public", ip: "2002:808:808::", blockedStrict: false, blockedAllowPriv: false},
		{name: "ipv4-compatible loopback", ip: "::7f00:1", blockedStrict: true, blockedAllowPriv: false},
		{name: "ipv4-compatible metadata", ip: "::a9fe:a9fe", blockedStrict: true, blockedAllowPriv: true},
		{name: "ipv6 site-local", ip: "fec0::1", blockedStrict: true, blockedAllowPriv: false},
		{name: "link-local multicast v6", ip: "ff02::1", blockedStrict: true, blockedAllowPriv: true},
		{name: "global multicast v6", ip: "ff0e::1", blockedStrict: true, blockedAllowPriv: true},
		{name: "aws imds v6", ip: "fd00:ec2::254", blockedStrict: true, blockedAllowPriv: true},
		{name: "aws dns v6", ip: "fd00:ec2::23", blockedStrict: true, blockedAllowPriv: true},
		{name: "gcp metadata v6", ip: "fd20:ce::254", blockedStrict: true, blockedAllowPriv: true},
		{name: "alibaba metadata", ip: "100.100.100.200", blockedStrict: true, blockedAllowPriv: true},
		{name: "alibaba metadata ipv4-mapped", ip: "::ffff:100.100.100.200", blockedStrict: true, blockedAllowPriv: true},
		{name: "alibaba metadata 6to4", ip: "2002:6464:64c8::", blockedStrict: true, blockedAllowPriv: true},
		{name: "azure wireserver", ip: "168.63.129.16", blockedStrict: true, blockedAllowPriv: true},
		{name: "address next to azure wireserver", ip: "168.63.129.17", blockedStrict: false, blockedAllowPriv: false},
		{name: "cgnat neighbour of alibaba metadata", ip: "100.100.100.201", blockedStrict: true, blockedAllowPriv: false},
		{name: "6to4 embedding imds", ip: "2002:a9fe:a9fe::", blockedStrict: true, blockedAllowPriv: true},
		{name: "nat64 local-use embedding imds", ip: "64:ff9b:1::a9fe:a9fe", blockedStrict: true, blockedAllowPriv: true},
		{name: "nat64 local-use embedding public", ip: "64:ff9b:1::808:808", blockedStrict: true, blockedAllowPriv: true},
		{name: "siit embedding rfc1918", ip: "::ffff:0:10.0.0.1", blockedStrict: true, blockedAllowPriv: false},
		{name: "siit embedding imds", ip: "::ffff:0:169.254.169.254", blockedStrict: true, blockedAllowPriv: true},
		{name: "siit embedding public", ip: "::ffff:0:8.8.8.8", blockedStrict: false, blockedAllowPriv: false},
		{name: "public v4", ip: "8.8.8.8", blockedStrict: false, blockedAllowPriv: false},
		{name: "public v4 - cloudflare", ip: "1.1.1.1", blockedStrict: false, blockedAllowPriv: false},
		{name: "public v6", ip: "2606:4700:4700::1111", blockedStrict: false, blockedAllowPriv: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ip := net.ParseIP(tt.ip)
			if ip == nil {
				t.Fatalf("could not parse %q", tt.ip)
			}
			if got, why := blockedAddress(ip, false); got != tt.blockedStrict {
				t.Fatalf("blockedAddress(%s, false) = %v (%q); want %v", tt.ip, got, why, tt.blockedStrict)
			}
			if got, why := blockedAddress(ip, true); got != tt.blockedAllowPriv {
				t.Fatalf("blockedAddress(%s, true) = %v (%q); want %v", tt.ip, got, why, tt.blockedAllowPriv)
			}
		})
	}
}

func TestAllowPrivateMCPEnvVar(t *testing.T) {
	tests := []struct {
		val  string
		want bool
	}{
		{val: "", want: false},
		{val: "0", want: false},
		{val: "false", want: false},
		{val: "no", want: false},
		{val: "1", want: true},
		{val: "true", want: true},
		{val: "TRUE", want: true},
		{val: "yes", want: true},
		{val: " yes ", want: true},
	}
	for _, tt := range tests {
		t.Run(tt.val, func(t *testing.T) {
			t.Setenv(allowPrivateMCPEnvVar, tt.val)
			if got := allowPrivateMCP(); got != tt.want {
				t.Fatalf("allowPrivateMCP() with %q = %v; want %v", tt.val, got, tt.want)
			}
		})
	}
}

func TestSSRFGuardDial_RejectsLoopback(t *testing.T) {
	t.Setenv(allowPrivateMCPEnvVar, "")
	d := dialerWithSSRFGuard()

	conn, err := d.Dial("tcp", "127.0.0.1:1")
	if err == nil {
		_ = conn.Close()
		t.Fatal("expected loopback dial to be rejected, got connection")
	}
	if !errors.Is(err, errSSRFBlocked) {
		t.Fatalf("expected errSSRFBlocked, got %v", err)
	}
}

func TestSSRFGuardDial_AllowsPrivateWhenEnvSet(t *testing.T) {
	t.Setenv(allowPrivateMCPEnvVar, "true")
	d := dialerWithSSRFGuard()

	if d.Control == nil {
		t.Fatal("the guarded dialer has no Control hook")
	}
	if err := d.Control("tcp", "10.255.255.1:1", nil); err != nil {
		t.Fatalf("env var should bypass SSRF guard for RFC1918, got %v", err)
	}
}

func TestSSRFGuardDial_LinkLocalStaysBlocked(t *testing.T) {
	t.Setenv(allowPrivateMCPEnvVar, "true")
	d := dialerWithSSRFGuard()

	_, err := d.Dial("tcp", "169.254.169.254:80")
	if err == nil {
		t.Fatal("expected link-local dial to be rejected, got connection")
	}
	if !errors.Is(err, errSSRFBlocked) {
		t.Fatalf("expected errSSRFBlocked for link-local, got %v", err)
	}
}

func TestMCPDiscoverySkipTLSVerify(t *testing.T) {
	tests := []struct {
		name   string
		val    string
		setEnv bool
		want   bool
	}{
		{name: "unset → verify", setEnv: false, want: false},
		{name: "empty → verify", setEnv: true, val: "", want: false},
		{name: "false → skip", setEnv: true, val: "false", want: true},
		{name: "0 → skip", setEnv: true, val: "0", want: true},
		{name: "no → skip", setEnv: true, val: "no", want: true},
		{name: "off → skip", setEnv: true, val: "off", want: true},
		{name: "FALSE → skip (case-insensitive)", setEnv: true, val: "FALSE", want: true},
		{name: "true → verify", setEnv: true, val: "true", want: false},
		{name: "1 → verify", setEnv: true, val: "1", want: false},
		{name: "unrecognized → verify", setEnv: true, val: "maybe", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_ = os.Unsetenv(mcpTLSVerifyEnvVar)
			if tt.setEnv {
				t.Setenv(mcpTLSVerifyEnvVar, tt.val)
			}
			if got := mcpDiscoverySkipTLSVerify(); got != tt.want {
				t.Fatalf("mcpDiscoverySkipTLSVerify() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestHTTPClientWithSSRFGuard_TLSConfigPropagates(t *testing.T) {
	skipCfg := &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS12} //nolint:gosec // test
	tests := []struct {
		name             string
		tlsCfg           *tls.Config
		wantNonNil       bool
		wantInsecureSkip bool
	}{
		{"nil → TLSClientConfig nil (default trust store)", nil, false, false},
		{"caller provides InsecureSkipVerify config", skipCfg, true, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := httpClientWithSSRFGuard(tt.tlsCfg)
			tr, ok := c.Transport.(*http.Transport)
			if !ok {
				t.Fatalf("Transport is not *http.Transport: %T", c.Transport)
			}
			if tt.wantNonNil && tr.TLSClientConfig == nil {
				t.Fatalf("TLSClientConfig is nil, want non-nil")
			}
			if !tt.wantNonNil && tr.TLSClientConfig != nil {
				t.Fatalf("TLSClientConfig = %+v, want nil", tr.TLSClientConfig)
			}
			if tr.TLSClientConfig != nil {
				if tr.TLSClientConfig.InsecureSkipVerify != tt.wantInsecureSkip {
					t.Errorf("InsecureSkipVerify = %v, want %v", tr.TLSClientConfig.InsecureSkipVerify, tt.wantInsecureSkip)
				}
				if tr.TLSClientConfig.MinVersion != tls.VersionTLS12 {
					t.Errorf("MinVersion = %x, want TLS 1.2 (%x)", tr.TLSClientConfig.MinVersion, tls.VersionTLS12)
				}
			}
		})
	}
}

func TestBuildMCPDiscoveryTLSConfig_VerifyFollowsTheOperatorEnvVarLikeTheRuntime(t *testing.T) {
	tests := []struct {
		name     string
		env      string
		ssl      *MCPSSLConfig
		wantSkip bool
	}{
		{"no ssl_config, env verifies", "true", nil, false},
		{"no ssl_config, env skips", "false", nil, true},
		{"ssl_config, env verifies", "true", &MCPSSLConfig{ServerName: "mcp.internal"}, false},
		{"ssl_config, env skips", "false", &MCPSSLConfig{ServerName: "mcp.internal"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(mcpTLSVerifyEnvVar, tt.env)
			got, err := buildMCPDiscoveryTLSConfig(tt.ssl)
			if err != nil {
				t.Fatalf("buildMCPDiscoveryTLSConfig: %v", err)
			}
			if got.InsecureSkipVerify != tt.wantSkip {
				t.Errorf("InsecureSkipVerify = %v, want %v", got.InsecureSkipVerify, tt.wantSkip)
			}
			runtime := mcppkg.ToolsetConfig{ConnectionType: "streamable-http"}
			if got.InsecureSkipVerify == runtime.EffectiveSSLVerify() {
				t.Errorf("discovery skips verify = %v, agent runtime verifies = %v", got.InsecureSkipVerify, runtime.EffectiveSSLVerify())
			}
		})
	}
}

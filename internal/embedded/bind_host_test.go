// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package embedded

import (
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/samapp"
	"github.com/SolaceProducts/solace-agent-mesh/internal/settings"
)

func TestResolveBindHost(t *testing.T) {
	tests := []struct {
		name     string
		override string
		httpCfg  *samapp.HTTPAdapterConfig
		want     string
	}{
		{"no config defaults to loopback", "", nil, "127.0.0.1"},
		{"empty fastapi_host defaults to loopback", "", &samapp.HTTPAdapterConfig{Host: ""}, "127.0.0.1"},
		{"localhost normalizes to 127.0.0.1", "", &samapp.HTTPAdapterConfig{Host: "localhost"}, "127.0.0.1"},
		{"explicit all-interfaces honored", "", &samapp.HTTPAdapterConfig{Host: "0.0.0.0"}, "0.0.0.0"},
		{"explicit LAN ip honored", "", &samapp.HTTPAdapterConfig{Host: "192.168.1.10"}, "192.168.1.10"},
		{"override wins over config", "0.0.0.0", &samapp.HTTPAdapterConfig{Host: "localhost"}, "0.0.0.0"},
		{"override localhost normalizes", "localhost", &samapp.HTTPAdapterConfig{Host: "0.0.0.0"}, "127.0.0.1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolveBindHost(tt.override, tt.httpCfg); got != tt.want {
				t.Errorf("resolveBindHost(%q, %+v) = %q, want %q", tt.override, tt.httpCfg, got, tt.want)
			}
		})
	}
}

func TestEmbeddedBindHost(t *testing.T) {
	tests := []struct {
		name            string
		gatewayBindHost string
		want            string
	}{
		{"default loopback", "", "127.0.0.1"},
		{"localhost normalizes to loopback", "localhost", "127.0.0.1"},
		{"explicit all-interfaces honored", "0.0.0.0", "0.0.0.0"},
		{"explicit LAN ip honored", "192.168.1.10", "192.168.1.10"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inst := &Instance{cfg: Config{GatewayBindHost: tt.gatewayBindHost}}
			if got := inst.embeddedBindHost(); got != tt.want {
				t.Errorf("embeddedBindHost() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestNetworkBrokerBindHost(t *testing.T) {
	tests := []struct {
		name            string
		sandboxMode     settings.SandboxMode
		gatewayBindHost string
		port            int
		wantAddr        string
	}{
		{"builtin defaults to loopback", settings.SandboxModeBuiltin, "", 55554, "127.0.0.1:55554"},
		{"builtin kernel-assigned port on loopback", settings.SandboxModeBuiltin, "", 0, "127.0.0.1:0"},
		{"container binds all interfaces", settings.SandboxModeContainer, "", 55555, ":55555"},
		{"container ignores loopback override", settings.SandboxModeContainer, "localhost", 55555, ":55555"},
		{"container widens LAN bind to all interfaces", settings.SandboxModeContainer, "192.168.1.10", 55555, ":55555"},
		{"builtin honors explicit LAN bind", settings.SandboxModeBuiltin, "0.0.0.0", 55554, "0.0.0.0:55554"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inst := &Instance{cfg: Config{SandboxMode: tt.sandboxMode, GatewayBindHost: tt.gatewayBindHost, NetworkBrokerPort: tt.port}}
			if gotAddr := inst.networkBrokerBindAddr(); gotAddr != tt.wantAddr {
				t.Errorf("networkBrokerBindAddr() = %q, want %q", gotAddr, tt.wantAddr)
			}
		})
	}
}

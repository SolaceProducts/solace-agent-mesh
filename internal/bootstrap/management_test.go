// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package bootstrap

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
	"github.com/SolaceProducts/solace-agent-mesh/internal/observability"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

func TestManagementPortFromConfig(t *testing.T) {
	tests := []struct {
		name     string
		root     map[string]any
		wantPort int
		wantOK   bool
	}{
		{"int port", map[string]any{"management_server": map[string]any{"port": 8080}}, 8080, true},
		{"float64 port (YAML)", map[string]any{"management_server": map[string]any{"port": float64(9000)}}, 9000, true},
		{"int64 port", map[string]any{"management_server": map[string]any{"port": int64(7000)}}, 7000, true},
		{"no management_server", map[string]any{}, 0, false},
		{"no port key", map[string]any{"management_server": map[string]any{"observability": map[string]any{}}}, 0, false},
		{"zero port", map[string]any{"management_server": map[string]any{"port": 0}}, 0, false},
		{"negative port", map[string]any{"management_server": map[string]any{"port": -1}}, 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			port, ok := ManagementPortFromConfig(tt.root)
			if ok != tt.wantOK || port != tt.wantPort {
				t.Errorf("got (%d, %v), want (%d, %v)", port, ok, tt.wantPort, tt.wantOK)
			}
		})
	}
}

func TestResolveManagementAddr(t *testing.T) {
	yamlPort := map[string]any{"management_server": map[string]any{"port": 8080}}
	noPort := map[string]any{}

	tests := []struct {
		name     string
		root     map[string]any
		flagAddr string
		flagSet  bool
		want     string
	}{
		{"explicit flag wins over YAML", yamlPort, ":9090", true, ":9090"},
		{"explicit empty flag disables even with YAML", yamlPort, "", true, ""},
		{"YAML port used when flag not set", yamlPort, ":9090", false, ":8080"},
		{"flag default used when no YAML and not set", noPort, ":9090", false, ":9090"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ResolveManagementAddr(tt.root, tt.flagAddr, tt.flagSet); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestMountMetricsOnManagement_MountsHandlerWhenRegistryPresent(t *testing.T) {
	reg, err := observability.NewRegistry(observability.Config{
		Enabled: true,
		Path:    "/metrics",
	}, slog.Default())
	if err != nil {
		t.Fatal(err)
	}

	rt := runtime.NewRuntime(broker.NewDevBroker(), config.FromMap(nil),
		runtime.WithMetrics(reg),
	)
	ms := runtime.NewManagementServer(rt, ":0", slog.Default())

	MountMetricsOnManagement(ms, rt, slog.Default())

	addr, err := ms.Start()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ms.Stop(context.Background()) })

	resp, err := http.Get("http://" + addr + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/metrics status: got %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "# HELP") && !strings.Contains(string(body), "# TYPE") {
		t.Errorf("expected Prometheus exposition, got %q", string(body))
	}
}

func TestMountMetricsOnManagement_NoOpWhenNoRegistry(t *testing.T) {
	rt := runtime.NewRuntime(broker.NewDevBroker(), config.FromMap(nil))
	ms := runtime.NewManagementServer(rt, ":0", slog.Default())

	MountMetricsOnManagement(ms, rt, slog.Default())

	addr, err := ms.Start()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ms.Stop(context.Background()) })

	resp, err := http.Get("http://" + addr + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("/metrics status: got %d, want 404 (no registry)", resp.StatusCode)
	}

	hresp, err := http.Get("http://" + addr + "/health")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = hresp.Body.Close() }()
	if hresp.StatusCode != http.StatusOK {
		t.Errorf("/health status: got %d, want 200", hresp.StatusCode)
	}
}

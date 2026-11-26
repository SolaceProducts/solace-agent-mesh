// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

func newProductionPlatformWithBroker(t *testing.T) (*platpkg.PlatformCore, *http.ServeMux, *RouteDeps) {
	t.Helper()
	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })

	core := platpkg.NewPlatformCore(map[string]any{
		"namespace":    "test-ns",
		"database_url": platformCoreDBURL(t),
	}, platpkg.ExtensionPoints{})
	core.SetServices(runtime.NewRuntime(brk, nil, runtime.WithLogger(slog.Default())).Services())
	if err := core.Init(t.Context(), nil); err != nil {
		t.Fatalf("init platform core: %v", err)
	}
	t.Cleanup(func() { _ = core.DB().Close() })

	mux := http.NewServeMux()
	deps := &RouteDeps{FeatureRegistry: features.NewRegistry()}
	registerRoutesLeading(t, mux, core, deps)
	if deps.WorkflowDeploySvc == nil {
		t.Fatal("the route table built no workflow deployment service")
	}
	return core, mux, deps
}

func uploadedShipmentConnector(t *testing.T, deps *RouteDeps, name string) {
	t.Helper()
	created, err := deps.ConnectorSvc.Create(t.Context(), CreateConnectorRequest{
		Name: name, Description: "Shipping connector with an uploaded spec", Type: "api", Subtype: "openapi",
		Values: map[string]any{"base_url": "https://api.example.com", "auth_type": "none"},
	}, "tester")
	if err != nil {
		t.Fatalf("create connector: %v", err)
	}
	if _, err := deps.ConnectorSvc.UploadResource(t.Context(), created.ID, "spec.yaml", bytes.NewReader([]byte(shipmentSpec))); err != nil {
		t.Fatalf("upload spec: %v", err)
	}
}

func mcpEntryFor(url string, extra map[string]any) map[string]any {
	entry := map[string]any{
		"tool_type":         "mcp",
		"connection_params": map[string]any{"type": "streamable-http", "url": url},
	}
	for k, v := range extra {
		entry[k] = v
	}
	return entry
}

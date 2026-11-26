// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package embedded

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/mcp"
)

func metadataStatus(proxy *entrypoint.GatewayProxy, slug string) int {
	req := httptest.NewRequest("GET", "/.well-known/oauth-protected-resource/gw/"+slug, nil)
	w := httptest.NewRecorder()
	proxy.Handler().ServeHTTP(w, req)
	return w.Code
}

func addMCPGateway(t *testing.T, exe *entrypoint.GatewayExecutor, slug string) {
	t.Helper()
	appConfig := map[string]any{"gateway_id": slug, "enable_auth": true}
	adapter, err := mcp.NewAdapter(appConfig)
	if err != nil {
		t.Fatalf("mcp.NewAdapter: %v", err)
	}
	exe.AddGateway(slug, entrypoint.NewGatewayCore(adapter, appConfig), appConfig)
}

func TestWireRuntimeMCPMetadata_RegistersOnReadyUnregistersOnRemove(t *testing.T) {
	inst := newTestInstance(Config{})
	setupMinimalRuntime(t, inst)

	proxy := entrypoint.NewGatewayProxy(":0", inst.logger)
	exe := entrypoint.NewGatewayExecutor(inst.rt.Services(), nil)
	exe.OnGatewayReady = proxy.RegisterHandler
	exe.OnGatewayRemoved = proxy.UnregisterHandler

	addMCPGateway(t, exe, "mcp-gw")
	inst.wireRuntimeMCPMetadata(proxy, exe)

	if proxy.HasMCPMetadataRoute("mcp-gw") {
		t.Fatal("metadata route must not exist before the gateway becomes ready")
	}
	if got := metadataStatus(proxy, "mcp-gw"); got != http.StatusServiceUnavailable {
		t.Fatalf("before ready: metadata GET = %d, want 503 (no route, falls through)", got)
	}

	exe.OnGatewayReady("mcp-gw", http.NotFoundHandler())
	if !proxy.HasMCPMetadataRoute("mcp-gw") {
		t.Fatal("metadata route must be registered once the gateway is ready")
	}
	if got := metadataStatus(proxy, "mcp-gw"); got != http.StatusNotFound {
		t.Fatalf("after ready: metadata GET = %d, want 404 (route dispatched to the un-Init'd metadata handler)", got)
	}

	exe.OnGatewayReady("mcp-gw", http.NotFoundHandler())
	if !proxy.HasMCPMetadataRoute("mcp-gw") {
		t.Fatal("metadata route must survive a redeploy (re-fired ready)")
	}

	exe.OnGatewayRemoved("mcp-gw")
	if proxy.HasMCPMetadataRoute("mcp-gw") {
		t.Fatal("metadata route must be unregistered after the gateway is removed")
	}
	if got := metadataStatus(proxy, "mcp-gw"); got != http.StatusServiceUnavailable {
		t.Fatalf("after remove: metadata GET = %d, want 503 (route gone, falls through)", got)
	}
}

func TestWireRuntimeMCPMetadata_SkipsNonMCPGateway(t *testing.T) {
	inst := newTestInstance(Config{})
	setupMinimalRuntime(t, inst)

	proxy := entrypoint.NewGatewayProxy(":0", inst.logger)
	exe := entrypoint.NewGatewayExecutor(inst.rt.Services(), nil)
	exe.OnGatewayReady = proxy.RegisterHandler
	exe.OnGatewayRemoved = proxy.UnregisterHandler

	exe.AddGateway("web-gw", entrypoint.NewGatewayCore(nil, map[string]any{"gateway_id": "web-gw"}), map[string]any{"gateway_id": "web-gw"})
	inst.wireRuntimeMCPMetadata(proxy, exe)

	exe.OnGatewayReady("web-gw", http.NotFoundHandler())
	if proxy.HasMCPMetadataRoute("web-gw") {
		t.Fatal("non-MCP gateway must not get a metadata route")
	}
	exe.OnGatewayRemoved("web-gw")
}

func TestWireRuntimeMCPMetadata_PreservesBaseCallbacks(t *testing.T) {
	inst := newTestInstance(Config{})
	setupMinimalRuntime(t, inst)

	proxy := entrypoint.NewGatewayProxy(":0", inst.logger)
	exe := entrypoint.NewGatewayExecutor(inst.rt.Services(), nil)

	var readyName, removedName string
	exe.OnGatewayReady = func(name string, _ http.Handler) { readyName = name }
	exe.OnGatewayRemoved = func(name string) { removedName = name }

	addMCPGateway(t, exe, "mcp-gw")
	inst.wireRuntimeMCPMetadata(proxy, exe)

	exe.OnGatewayReady("mcp-gw", http.NotFoundHandler())
	if readyName != "mcp-gw" {
		t.Fatalf("base OnGatewayReady not called; got %q", readyName)
	}
	if !proxy.HasMCPMetadataRoute("mcp-gw") {
		t.Fatal("metadata route must be registered alongside the base data-path callback")
	}

	exe.OnGatewayRemoved("mcp-gw")
	if removedName != "mcp-gw" {
		t.Fatalf("base OnGatewayRemoved not called; got %q", removedName)
	}
}

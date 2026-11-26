// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/auth/toolauth"
	mcpgo "github.com/mark3labs/mcp-go/mcp"
)

type fakeMCPServer struct {
	advertisedTool mcpgo.Tool
}

func (f *fakeMCPServer) handle(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "only POST", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	defer func() { _ = r.Body.Close() }()

	var req struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Method  string          `json:"method"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if len(req.ID) == 0 || string(req.ID) == "null" {
		w.WriteHeader(http.StatusAccepted)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	switch req.Method {
	case "initialize":
		writeJSONRPC(w, req.ID, mcpgo.InitializeResult{
			ProtocolVersion: mcpgo.LATEST_LEGACY_PROTOCOL_VERSION,
			ServerInfo: mcpgo.Implementation{
				Name:    "fake-mcp-server",
				Version: "1.0.0",
			},
			Capabilities: mcpgo.ServerCapabilities{},
		})
	case "tools/list":
		writeJSONRPC(w, req.ID, mcpgo.ListToolsResult{
			Tools: []mcpgo.Tool{f.advertisedTool},
		})
	default:
		writeJSONRPC(w, req.ID, map[string]any{})
	}
}

func writeJSONRPC(w http.ResponseWriter, id json.RawMessage, result any) {
	rb, _ := json.Marshal(result)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"result":  json.RawMessage(rb),
	})
}

func newFakeMCPTLSServer(t *testing.T) (*httptest.Server, *fakeMCPServer) {
	t.Helper()
	fake := &fakeMCPServer{
		advertisedTool: mcpgo.Tool{
			Name:        "ping",
			Description: "test tool",
			InputSchema: mcpgo.ToolInputSchema{
				Type:       "object",
				Properties: map[string]any{"msg": map[string]any{"type": "string"}},
				Required:   []string{"msg"},
			},
		},
	}
	srv := httptest.NewTLSServer(http.HandlerFunc(fake.handle))
	t.Cleanup(srv.Close)
	return srv, fake
}

func writeServerCAToFile(t *testing.T, srv *httptest.Server) string {
	t.Helper()
	cert := srv.Certificate()
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw})
	dir := t.TempDir()
	path := filepath.Join(dir, "server-ca.pem")
	if err := os.WriteFile(path, pemBytes, 0o600); err != nil {
		t.Fatalf("write pem: %v", err)
	}
	return path
}

func TestAddDirectClient_SelfSignedServer_VerifyFalse(t *testing.T) {
	srv, _ := newFakeMCPTLSServer(t)

	verify := false
	cfg := &ToolsetConfig{
		ConnectionType: "streamable-http",
		URL:            srv.URL,
		SSLVerify:      &verify,
		ClientID:       "fake_mcp",
	}

	mgr := NewManager(slog.Default())
	defer mgr.Stop()

	if err := mgr.AddClient(cfg); err != nil {
		t.Fatalf("AddClient: %v", err)
	}

	tools, err := mgr.DiscoverTools(cfg)
	if err != nil {
		t.Fatalf("DiscoverTools: %v", err)
	}
	if len(tools) != 1 {
		t.Fatalf("tools len = %d, want 1", len(tools))
	}
	if got := tools[0].Description(); got != "test tool" {
		t.Errorf("Description = %q, want %q", got, "test tool")
	}
}

func TestAddDirectClient_SelfSignedServer_CABundle(t *testing.T) {
	srv, _ := newFakeMCPTLSServer(t)
	caPath := writeServerCAToFile(t, srv)

	cfg := &ToolsetConfig{
		ConnectionType: "streamable-http",
		URL:            srv.URL,
		SSLCABundle:    caPath,
		ClientID:       "fake_mcp_ca",
	}

	mgr := NewManager(slog.Default())
	defer mgr.Stop()

	if err := mgr.AddClient(cfg); err != nil {
		t.Fatalf("AddClient: %v", err)
	}
	tools, err := mgr.DiscoverTools(cfg)
	if err != nil {
		t.Fatalf("DiscoverTools: %v", err)
	}
	if len(tools) != 1 {
		t.Fatalf("tools len = %d, want 1", len(tools))
	}
}

func TestAddDirectClient_SelfSignedServer_DefaultVerifyFails(t *testing.T) {
	srv, _ := newFakeMCPTLSServer(t)

	cfg := &ToolsetConfig{
		ConnectionType: "streamable-http",
		URL:            srv.URL,
		ClientID:       "fake_mcp_default",
	}

	mgr := NewManager(slog.Default())
	defer mgr.Stop()

	err := mgr.AddClient(cfg)
	if err == nil {
		t.Fatal("AddClient succeeded against a self-signed server with no ssl_config — expected TLS verification failure")
	}
}

func TestAddClient_WithManifest_NoNetwork(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Fatal("AddClient with manifest must not contact the MCP server")
	}))
	defer srv.Close()

	cfg := &ToolsetConfig{
		ConnectionType: "sse",
		URL:            srv.URL,
		ClientID:       "manifest_no_net",
		Manifest: []ManifestTool{
			{
				Name:        "ping",
				Description: "Test tool",
				InputSchema: map[string]any{
					"type":       "object",
					"properties": map[string]any{"msg": map[string]any{"type": "string"}},
					"required":   []any{"msg"},
				},
			},
		},
	}

	mgr := NewManager(slog.Default())
	defer mgr.Stop()

	if err := mgr.AddClient(cfg); err != nil {
		t.Fatalf("AddClient: %v", err)
	}

	mgr.mu.RLock()
	dc, ok := mgr.direct[cfg.ClientID]
	mgr.mu.RUnlock()
	if !ok {
		t.Fatal("manifest client not registered in direct map")
	}
	if dc.transport != nil {
		t.Error("direct client transport is non-nil — manifest path should defer connection")
	}
	if dc.mcp != nil {
		t.Error("direct client mcp is non-nil — manifest path should defer initialization")
	}
	if len(dc.tools) != 1 {
		t.Errorf("direct client tools len = %d, want 1", len(dc.tools))
	}
}

func TestAddClient_StreamableHTTP_AuthWithoutManifest_Rejected(t *testing.T) {
	cfg := &ToolsetConfig{
		ConnectionType: connTypeStreamableHTTP,
		URL:            "https://auth.example.com/mcp",
		ClientID:       "streamable_http_auth_no_manifest",
		AuthConfig:     &toolauth.AuthConfig{Type: "oauth2"},
	}

	mgr := NewManager(slog.Default())
	defer mgr.Stop()

	err := mgr.AddClient(cfg)
	if err == nil {
		t.Fatal("AddClient(streamable-http + auth + no manifest) should return an error, got nil")
	}
	if !strings.Contains(err.Error(), "streamable-http") || !strings.Contains(err.Error(), "manifest") {
		t.Errorf("error = %q, want it to mention both streamable-http and manifest", err.Error())
	}
}

func TestAddClient_StreamableHTTP_AuthWithManifest_Allowed(t *testing.T) {
	cfg := &ToolsetConfig{
		ConnectionType: connTypeStreamableHTTP,
		URL:            "https://auth.example.com/mcp",
		ClientID:       "streamable_http_auth_manifest",
		AuthConfig:     &toolauth.AuthConfig{Type: "oauth2"},
		Manifest: []ManifestTool{
			{Name: "list_items", InputSchema: map[string]any{"type": "object"}},
		},
	}

	mgr := NewManager(slog.Default())
	defer mgr.Stop()

	err := mgr.AddClient(cfg)
	if err != nil {
		t.Fatalf("AddClient(streamable-http + auth + manifest) should succeed, got %v", err)
	}
}

func TestAddClient_WithManifest_Stdio_Rejected(t *testing.T) {
	cfg := &ToolsetConfig{
		ConnectionType: "stdio",
		Command:        "/usr/bin/mcp-server",
		ClientID:       "stdio_with_manifest",
		Manifest: []ManifestTool{
			{Name: "x", InputSchema: map[string]any{"type": "object"}},
		},
	}

	mgr := NewManager(slog.Default())
	defer mgr.Stop()

	err := mgr.AddClient(cfg)
	if err == nil {
		t.Fatal("AddClient(stdio + manifest) should return an error, got nil")
	}
	if !strings.Contains(err.Error(), "stdio") || !strings.Contains(err.Error(), "manifest") {
		t.Errorf("error = %q, want it to mention both stdio and manifest", err.Error())
	}
}

func TestDiscoverTools_FromManifest(t *testing.T) {
	cfg := &ToolsetConfig{
		ConnectionType: "sse",
		URL:            "https://unused.example.com/sse",
		ClientID:       "manifest_discover",
		Manifest: []ManifestTool{
			{
				Name:        "listSpaces",
				Description: "List Confluence spaces.",
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"cloudId": map[string]any{"type": "string"},
					},
					"required": []any{"cloudId"},
				},
			},
		},
	}

	mgr := NewManager(slog.Default())
	defer mgr.Stop()

	if err := mgr.AddClient(cfg); err != nil {
		t.Fatalf("AddClient: %v", err)
	}
	tools, err := mgr.DiscoverTools(cfg)
	if err != nil {
		t.Fatalf("DiscoverTools: %v", err)
	}
	if len(tools) != 1 {
		t.Fatalf("tools len = %d, want 1", len(tools))
	}
	tl := tools[0]
	if tl.Name() != "listSpaces" {
		t.Errorf("Name = %q, want listSpaces", tl.Name())
	}
	if tl.Description() != "List Confluence spaces." {
		t.Errorf("Description = %q, want 'List Confluence spaces.'", tl.Description())
	}
	params := tl.Parameters()
	if params == nil {
		t.Fatal("Parameters() is nil")
	}
	if params.Type != "object" {
		t.Errorf("Parameters().Type = %q, want object", params.Type)
	}
	if len(params.Required) != 1 || params.Required[0] != "cloudId" {
		t.Errorf("Parameters().Required = %v, want [cloudId]", params.Required)
	}
}

func TestAddClient_NoSSLConfig_UsesBifrost(t *testing.T) {
	cfg := &ToolsetConfig{
		ConnectionType: "streamable-http",
		URL:            "http://127.0.0.1:1",
		ClientID:       "no_ssl",
	}
	if cfg.HasSSLConfig() {
		t.Fatal("zero-value SSL config reports HasSSLConfig() = true")
	}

	mgr := NewManager(slog.Default())
	defer mgr.Stop()

	_ = mgr.AddClient(cfg)

	mgr.mu.RLock()
	_, inDirect := mgr.direct[cfg.ClientID]
	mgr.mu.RUnlock()
	if inDirect {
		t.Fatal("client without SSL config landed in direct map; expected Bifrost path")
	}
}

type lazyConnectServer struct {
	mu             sync.Mutex
	initializeN    atomic.Int64
	toolsCallN     atomic.Int64
	listToolsN     atomic.Int64
	requireBearer  string
	toolCallResult any
}

func (s *lazyConnectServer) handle(w http.ResponseWriter, r *http.Request) {
	if s.requireBearer != "" {
		if got := r.Header.Get("Authorization"); got != s.requireBearer {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
	}
	if r.Method != http.MethodPost {
		http.Error(w, "only POST", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	defer func() { _ = r.Body.Close() }()

	var req struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if len(req.ID) == 0 || string(req.ID) == "null" {
		w.WriteHeader(http.StatusAccepted)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	switch req.Method {
	case "initialize":
		s.initializeN.Add(1)
		writeJSONRPC(w, req.ID, mcpgo.InitializeResult{
			ProtocolVersion: mcpgo.LATEST_LEGACY_PROTOCOL_VERSION,
			ServerInfo:      mcpgo.Implementation{Name: "lazy-mock", Version: "1.0.0"},
			Capabilities:    mcpgo.ServerCapabilities{},
		})
	case "tools/list":
		s.listToolsN.Add(1)
		writeJSONRPC(w, req.ID, mcpgo.ListToolsResult{})
	case "tools/call":
		s.toolsCallN.Add(1)
		s.mu.Lock()
		payload := s.toolCallResult
		s.mu.Unlock()
		if payload == nil {
			payload = map[string]any{
				"content": []any{map[string]any{"type": "text", "text": "ok"}},
			}
		}
		writeJSONRPC(w, req.ID, payload)
	default:
		writeJSONRPC(w, req.ID, map[string]any{})
	}
}

func newLazyConnectServer(t *testing.T) (*httptest.Server, *lazyConnectServer) {
	t.Helper()
	s := &lazyConnectServer{}
	srv := httptest.NewServer(http.HandlerFunc(s.handle))
	t.Cleanup(srv.Close)
	return srv, s
}

func TestExecuteToolRaw_LazyConnect(t *testing.T) {
	srv, mock := newLazyConnectServer(t)

	cfg := &ToolsetConfig{
		ConnectionType: "streamable-http",
		URL:            srv.URL,
		ClientID:       "lazy_basic",
		Manifest: []ManifestTool{
			{
				Name:        "ping",
				Description: "ping tool",
				InputSchema: map[string]any{"type": "object"},
			},
		},
	}

	mgr := NewManager(slog.Default())
	defer mgr.Stop()

	if err := mgr.AddClient(cfg); err != nil {
		t.Fatalf("AddClient: %v", err)
	}

	ctx := t.Context()
	_, err := mgr.ExecuteToolRawWithHeaders(ctx, "lazy_basic-ping", map[string]any{}, nil, nil)
	if err != nil {
		t.Fatalf("ExecuteToolRawWithHeaders (first): %v", err)
	}
	if got := mock.initializeN.Load(); got != 1 {
		t.Fatalf("initialize count after first call = %d, want 1", got)
	}

	_, err = mgr.ExecuteToolRawWithHeaders(ctx, "lazy_basic-ping", map[string]any{}, nil, nil)
	if err != nil {
		t.Fatalf("ExecuteToolRawWithHeaders (second): %v", err)
	}
	if got := mock.initializeN.Load(); got != 1 {
		t.Errorf("initialize count after second call = %d, want 1 (transport must be reused)", got)
	}
	if got := mock.toolsCallN.Load(); got != 2 {
		t.Errorf("tools/call count = %d, want 2", got)
	}
}

func TestExecuteToolRaw_LazyConnect_AuthRequired(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Fatal("server must not be reached when fetch returns ErrAuthRequired")
	}))
	defer srv.Close()

	cfg := &ToolsetConfig{
		ConnectionType: "streamable-http",
		URL:            srv.URL,
		ClientID:       "lazy_authrequired",
		Manifest: []ManifestTool{
			{Name: "ping", InputSchema: map[string]any{"type": "object"}},
		},
	}

	mgr := NewManager(slog.Default())
	defer mgr.Stop()
	if err := mgr.AddClient(cfg); err != nil {
		t.Fatalf("AddClient: %v", err)
	}

	mgr.mu.RLock()
	dc := mgr.direct[cfg.ClientID]
	mgr.mu.RUnlock()
	dc.authFetch = func(_ context.Context) (http.Header, error) {
		return nil, ErrAuthRequired
	}

	_, err := mgr.ExecuteToolRawWithHeaders(t.Context(), "lazy_authrequired-ping", map[string]any{}, nil, nil)
	if err == nil {
		t.Fatal("expected error when auth is required, got nil")
	}
	if !errors.Is(err, ErrAuthRequired) {
		t.Errorf("error = %v, want it to wrap ErrAuthRequired", err)
	}
	if dc.transport != nil {
		t.Error("transport was opened despite ErrAuthRequired")
	}
}

func TestExecuteToolRaw_TearDownOn401(t *testing.T) {
	srv, mock := newLazyConnectServer(t)
	var callN atomic.Int64
	mock.toolCallResult = nil
	origHandle := mock.handle
	wrapped := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = r.Body.Close()
		var probe struct {
			Method string `json:"method"`
		}
		_ = json.Unmarshal(body, &probe)
		if probe.Method == "tools/call" {
			n := callN.Add(1)
			if n == 1 {
				mock.mu.Lock()
				mock.toolCallResult = map[string]any{
					"isError": true,
					"content": []any{map[string]any{"type": "text", "text": "401 Unauthorized"}},
				}
				mock.mu.Unlock()
			} else {
				mock.mu.Lock()
				mock.toolCallResult = nil
				mock.mu.Unlock()
			}
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		origHandle(w, r)
	})
	srv.Config.Handler = wrapped

	cfg := &ToolsetConfig{
		ConnectionType: "streamable-http",
		URL:            srv.URL,
		ClientID:       "teardown_401",
		Manifest: []ManifestTool{
			{Name: "ping", InputSchema: map[string]any{"type": "object"}},
		},
	}
	mgr := NewManager(slog.Default())
	defer mgr.Stop()
	if err := mgr.AddClient(cfg); err != nil {
		t.Fatalf("AddClient: %v", err)
	}

	result, err := mgr.ExecuteToolRawWithHeaders(t.Context(), "teardown_401-ping", map[string]any{}, nil, nil)
	if err != nil {
		t.Fatalf("first call error: %v", err)
	}
	if result == nil || !result.IsError {
		t.Fatalf("first call result.IsError = false, want true (got %+v)", result)
	}

	mgr.mu.RLock()
	dc := mgr.direct[cfg.ClientID]
	mgr.mu.RUnlock()
	dc.mu.Lock()
	tr := dc.transport
	dc.mu.Unlock()
	if tr != nil {
		t.Error("transport should be nil after MCP-level auth error (tear-down)")
	}

	result, err = mgr.ExecuteToolRawWithHeaders(t.Context(), "teardown_401-ping", map[string]any{}, nil, nil)
	if err != nil {
		t.Fatalf("second call error: %v", err)
	}
	if result.IsError {
		t.Errorf("second call result.IsError = true, want false (got %+v)", result)
	}
	if got := mock.initializeN.Load(); got != 2 {
		t.Errorf("initialize count = %d, want 2 (tear-down + re-open)", got)
	}
}

func TestExecuteToolRaw_TearDownOnTransportError(t *testing.T) {
	var callN atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = r.Body.Close()
		var probe struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		_ = json.Unmarshal(body, &probe)
		if len(probe.ID) == 0 || string(probe.ID) == "null" {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		switch probe.Method {
		case "initialize":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			writeJSONRPC(w, probe.ID, mcpgo.InitializeResult{
				ProtocolVersion: mcpgo.LATEST_LEGACY_PROTOCOL_VERSION,
				ServerInfo:      mcpgo.Implementation{Name: "x", Version: "1"},
			})
		case "tools/call":
			callN.Add(1)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
		default:
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			writeJSONRPC(w, probe.ID, map[string]any{})
		}
	}))
	defer srv.Close()

	cfg := &ToolsetConfig{
		ConnectionType: "streamable-http",
		URL:            srv.URL,
		ClientID:       "teardown_http401",
		Manifest: []ManifestTool{
			{Name: "ping", InputSchema: map[string]any{"type": "object"}},
		},
	}
	mgr := NewManager(slog.Default())
	defer mgr.Stop()
	if err := mgr.AddClient(cfg); err != nil {
		t.Fatalf("AddClient: %v", err)
	}

	_, err := mgr.ExecuteToolRawWithHeaders(t.Context(), "teardown_http401-ping", map[string]any{}, nil, nil)
	if err == nil {
		t.Fatal("expected transport-level error, got nil")
	}
	if !errors.Is(err, ErrAuthRequired) {
		t.Errorf("transport 401 error %v does not wrap ErrAuthRequired; executeWithAuth would miss the re-auth path", err)
	}

	mgr.mu.RLock()
	dc := mgr.direct[cfg.ClientID]
	mgr.mu.RUnlock()
	dc.mu.Lock()
	tr := dc.transport
	dc.mu.Unlock()
	if tr != nil {
		t.Error("transport should be nil after HTTP 401 (tear-down)")
	}
}

func TestDirectClient_Invalidate_ClearsAuthFetch(t *testing.T) {
	dc := &directClient{
		authFetch: func(_ context.Context) (http.Header, error) {
			return http.Header{"Authorization": []string{"Bearer stale-user-a"}}, nil
		},
	}
	dc.invalidate(dc.transport)
	if dc.authFetch != nil {
		t.Error("invalidate() left dc.authFetch non-nil; next reconnect will use stale user's closure")
	}
}

func TestEnsureConnected_SSLAndAuthCompose(t *testing.T) {
	const expectedBearer = "Bearer combined-ssl-auth-token"
	var (
		sawBearer   atomic.Pointer[string]
		initializeN atomic.Int64
	)
	handler := func(w http.ResponseWriter, r *http.Request) {
		got := r.Header.Get("Authorization")
		sawBearer.Store(&got)
		if got != expectedBearer {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		body, _ := io.ReadAll(r.Body)
		_ = r.Body.Close()
		var probe struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		_ = json.Unmarshal(body, &probe)
		if len(probe.ID) == 0 || string(probe.ID) == "null" {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if probe.Method == "initialize" {
			initializeN.Add(1)
			writeJSONRPC(w, probe.ID, mcpgo.InitializeResult{
				ProtocolVersion: mcpgo.LATEST_LEGACY_PROTOCOL_VERSION,
				ServerInfo:      mcpgo.Implementation{Name: "tls+auth", Version: "1"},
			})
			return
		}
		writeJSONRPC(w, probe.ID, map[string]any{})
	}
	srv := httptest.NewTLSServer(http.HandlerFunc(handler))
	defer srv.Close()
	caPath := writeServerCAToFile(t, srv)

	cfg := &ToolsetConfig{
		ConnectionType: "streamable-http",
		URL:            srv.URL,
		SSLCABundle:    caPath,
		ClientID:       "ssl_plus_auth",
		Manifest: []ManifestTool{
			{Name: "ping", InputSchema: map[string]any{"type": "object"}},
		},
	}
	mgr := NewManager(slog.Default())
	defer mgr.Stop()
	if err := mgr.AddClient(cfg); err != nil {
		t.Fatalf("AddClient: %v", err)
	}

	mgr.mu.RLock()
	dc := mgr.direct[cfg.ClientID]
	mgr.mu.RUnlock()
	dc.authFetch = func(_ context.Context) (http.Header, error) {
		return http.Header{"Authorization": []string{expectedBearer}}, nil
	}

	_, err := mgr.ensureConnected(t.Context(), dc, nil)
	if err != nil {
		t.Fatalf("ensureConnected failed — SSL config was likely discarded: %v", err)
	}
	if initializeN.Load() != 1 {
		t.Errorf("initialize count = %d, want 1", initializeN.Load())
	}
	if got := sawBearer.Load(); got == nil || *got != expectedBearer {
		t.Errorf("server saw Authorization = %v, want %q", got, expectedBearer)
	}
}

func TestEnsureConnected_ClearsAuthFetchOnBuildHTTPClientFailure(t *testing.T) {
	dir := t.TempDir()
	missingCA := filepath.Join(dir, "does-not-exist.pem")
	cfg := &ToolsetConfig{
		ConnectionType: "streamable-http",
		URL:            "https://mcp.example.invalid/v1",
		SSLCABundle:    missingCA,
		ClientID:       "build_httpclient_fail",
		Manifest: []ManifestTool{
			{Name: "ping", InputSchema: map[string]any{"type": "object"}},
		},
	}
	mgr := NewManager(slog.Default())
	defer mgr.Stop()
	if err := mgr.AddClient(cfg); err != nil {
		t.Fatalf("AddClient: %v", err)
	}

	mgr.mu.RLock()
	dc := mgr.direct[cfg.ClientID]
	mgr.mu.RUnlock()
	dc.authFetch = func(_ context.Context) (http.Header, error) {
		return http.Header{"Authorization": []string{"Bearer stale-first-caller"}}, nil
	}

	_, err := mgr.ensureConnected(t.Context(), dc, nil)
	if err == nil {
		t.Fatal("expected BuildHTTPClient to fail against a missing CA bundle")
	}

	dc.mu.Lock()
	remaining := dc.authFetch
	dc.mu.Unlock()
	if remaining != nil {
		t.Error("dc.authFetch still set after BuildHTTPClient failure; next caller will retry under stale closure")
	}
}

func TestExecuteToolRaw_WrapsAuthErrorFromEnsureConnected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = r.Body.Close()
		var probe struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		_ = json.Unmarshal(body, &probe)
		if len(probe.ID) == 0 || string(probe.ID) == "null" {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	defer srv.Close()

	cfg := &ToolsetConfig{
		ConnectionType: "streamable-http",
		URL:            srv.URL,
		ClientID:       "init_401",
		Manifest: []ManifestTool{
			{Name: "ping", InputSchema: map[string]any{"type": "object"}},
		},
	}
	mgr := NewManager(slog.Default())
	defer mgr.Stop()
	if err := mgr.AddClient(cfg); err != nil {
		t.Fatalf("AddClient: %v", err)
	}

	mgr.mu.RLock()
	dc := mgr.direct[cfg.ClientID]
	mgr.mu.RUnlock()
	dc.authFetch = func(_ context.Context) (http.Header, error) {
		return http.Header{"Authorization": []string{"Bearer revoked-token"}}, nil
	}

	_, err := mgr.ExecuteToolRawWithHeaders(t.Context(), "init_401-ping", map[string]any{}, nil, nil)
	if err == nil {
		t.Fatal("expected Initialize to fail with 401, got nil")
	}
	if !errors.Is(err, ErrAuthRequired) {
		t.Errorf("Initialize 401 error %v does not wrap ErrAuthRequired; executeWithAuth would miss the re-auth path", err)
	}
}

func TestEnsureConnected_ClearsAuthFetchOnStartFailure(t *testing.T) {
	cfg := &ToolsetConfig{
		ConnectionType: "streamable-http",
		URL:            "http://127.0.0.1:1",
		ClientID:       "start_fail",
		Manifest: []ManifestTool{
			{Name: "ping", InputSchema: map[string]any{"type": "object"}},
		},
	}
	mgr := NewManager(slog.Default())
	defer mgr.Stop()
	if err := mgr.AddClient(cfg); err != nil {
		t.Fatalf("AddClient: %v", err)
	}

	mgr.mu.RLock()
	dc := mgr.direct[cfg.ClientID]
	mgr.mu.RUnlock()
	dc.authFetch = func(_ context.Context) (http.Header, error) {
		return http.Header{"Authorization": []string{"Bearer stale-first-caller"}}, nil
	}

	ctx, cancel := context.WithTimeout(t.Context(), 2*directConnectTimeout)
	defer cancel()
	_, err := mgr.ensureConnected(ctx, dc, nil)
	if err == nil {
		t.Fatal("expected ensureConnected to fail against an unreachable URL")
	}

	dc.mu.Lock()
	remaining := dc.authFetch
	dc.mu.Unlock()
	if remaining != nil {
		t.Error("dc.authFetch still set after failed ensureConnected; next caller will retry under stale closure")
	}
}

func TestExecuteToolRaw_LazyConnect_Concurrent(t *testing.T) {
	srv, mock := newLazyConnectServer(t)

	cfg := &ToolsetConfig{
		ConnectionType: "streamable-http",
		URL:            srv.URL,
		ClientID:       "lazy_concurrent",
		Manifest: []ManifestTool{
			{Name: "ping", InputSchema: map[string]any{"type": "object"}},
		},
	}

	mgr := NewManager(slog.Default())
	defer mgr.Stop()
	if err := mgr.AddClient(cfg); err != nil {
		t.Fatalf("AddClient: %v", err)
	}

	const N = 10
	var wg sync.WaitGroup
	errs := make([]error, N)
	for i := range N {
		wg.Go(func() {
			_, err := mgr.ExecuteToolRawWithHeaders(t.Context(), "lazy_concurrent-ping", map[string]any{}, nil, nil)
			errs[i] = err
		})
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("goroutine %d: %v", i, err)
		}
	}
	if got := mock.initializeN.Load(); got != 1 {
		t.Errorf("initialize count with %d concurrent callers = %d, want 1", N, got)
	}
	if got := mock.toolsCallN.Load(); got != N {
		t.Errorf("tools/call count = %d, want %d", got, N)
	}
}

func TestDiscoverTools_SharesAuthHandlerAcrossTools(t *testing.T) {
	cfg := &ToolsetConfig{
		ConnectionType: "sse",
		URL:            "https://mcp.example.com/v1/sse",
		ClientID:       "shared_auth_handler",
		AuthConfig: &MCPAuthConfig{
			Type:          "oauth2",
			CredentialKey: "shared_creds",
			ResourceURL:   "https://mcp.example.com/v1/sse",
		},
		Manifest: []ManifestTool{
			{Name: "toolA", InputSchema: map[string]any{"type": "object"}},
			{Name: "toolB", InputSchema: map[string]any{"type": "object"}},
			{Name: "toolC", InputSchema: map[string]any{"type": "object"}},
		},
	}

	mgr := NewManager(slog.Default())
	defer mgr.Stop()

	if err := mgr.AddClient(cfg); err != nil {
		t.Fatalf("AddClient: %v", err)
	}
	tools, err := mgr.DiscoverTools(cfg)
	if err != nil {
		t.Fatalf("DiscoverTools: %v", err)
	}
	if len(tools) != 3 {
		t.Fatalf("tools len = %d, want 3", len(tools))
	}

	first := tools[0].AuthHandler()
	if first == nil {
		t.Fatal("tools[0].AuthHandler() is nil — expected a handler since AuthConfig was set")
	}
	for i, tl := range tools {
		if tl.AuthHandler() != first {
			t.Errorf("tools[%d].AuthHandler() = %p, want %p (all tools in a toolset must share one handler)",
				i, tl.AuthHandler(), first)
		}
	}
}

func TestAddClient_StaticHeaders_APIKey(t *testing.T) {
	const apiKeyName = "X-API-Key"
	const apiKeyValue = "supersecret"

	var observedHeader atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		observedHeader.Store(r.Header.Get(apiKeyName))
		if r.Header.Get(apiKeyName) != apiKeyValue {
			http.Error(w, "missing or wrong "+apiKeyName, http.StatusUnauthorized)
			return
		}
		(&fakeMCPServer{
			advertisedTool: mcpgo.Tool{
				Name:        "ping",
				Description: "test tool",
				InputSchema: mcpgo.ToolInputSchema{
					Type:       "object",
					Properties: map[string]any{"msg": map[string]any{"type": "string"}},
					Required:   []string{"msg"},
				},
			},
		}).handle(w, r)
	}))
	defer srv.Close()

	raw := map[string]any{
		"tool_type": "mcp",
		"connection_params": map[string]any{
			"type": "streamable-http",
			"url":  srv.URL,
			"headers": map[string]any{
				apiKeyName: apiKeyValue,
			},
		},
	}
	cfg, err := ParseToolsetConfig(raw)
	if err != nil {
		t.Fatalf("ParseToolsetConfig: %v", err)
	}

	mgr := NewManager(slog.Default())
	defer mgr.Stop()

	if addErr := mgr.AddClient(cfg); addErr != nil {
		t.Fatalf("AddClient: %v (header reaching server: %q)", addErr, observedHeader.Load())
	}

	tools, err := mgr.DiscoverTools(cfg)
	if err != nil {
		t.Fatalf("DiscoverTools: %v", err)
	}
	if len(tools) == 0 {
		t.Fatalf("DiscoverTools returned 0 tools — BUG-007: apikey-header dropped before reaching the MCP server (server saw header value %q, expected %q)",
			observedHeader.Load(), apiKeyValue)
	}
	if got, _ := observedHeader.Load().(string); got != apiKeyValue {
		t.Errorf("server received %s = %q, want %q", apiKeyName, got, apiKeyValue)
	}
}

func TestAddClient_StaticHeaders_BasicLiteral(t *testing.T) {
	const wantAuth = "Basic dXNlcjpwYXNz"

	var observed atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		observed.Store(r.Header.Get("Authorization"))
		if r.Header.Get("Authorization") != wantAuth {
			http.Error(w, "no auth", http.StatusUnauthorized)
			return
		}
		(&fakeMCPServer{
			advertisedTool: mcpgo.Tool{Name: "ping", Description: "ok",
				InputSchema: mcpgo.ToolInputSchema{Type: "object", Properties: map[string]any{}}},
		}).handle(w, r)
	}))
	defer srv.Close()

	cfg, err := ParseToolsetConfig(map[string]any{
		"tool_type": "mcp",
		"connection_params": map[string]any{
			"type": "streamable-http",
			"url":  srv.URL,
			"headers": map[string]any{
				"Authorization": wantAuth,
			},
		},
	})
	if err != nil {
		t.Fatalf("ParseToolsetConfig: %v", err)
	}

	mgr := NewManager(slog.Default())
	defer mgr.Stop()

	if addErr := mgr.AddClient(cfg); addErr != nil {
		t.Fatalf("AddClient: %v (server saw %q)", addErr, observed.Load())
	}
	tools, err := mgr.DiscoverTools(cfg)
	if err != nil {
		t.Fatalf("DiscoverTools: %v", err)
	}
	if len(tools) == 0 {
		t.Fatalf("got 0 tools — Basic auth header dropped (server saw %q)", observed.Load())
	}
}

func TestAddClient_StaticHeaders_BasicAuthBlock(t *testing.T) {
	const wantAuth = "Basic dXNlcjpwYXNz"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != wantAuth {
			http.Error(w, "no auth", http.StatusUnauthorized)
			return
		}
		(&fakeMCPServer{
			advertisedTool: mcpgo.Tool{Name: "ping", Description: "ok",
				InputSchema: mcpgo.ToolInputSchema{Type: "object", Properties: map[string]any{}}},
		}).handle(w, r)
	}))
	defer srv.Close()

	cfg, err := ParseToolsetConfig(map[string]any{
		"tool_type": "mcp",
		"connection_params": map[string]any{
			"type": "streamable-http",
			"url":  srv.URL,
			"auth": map[string]any{
				"type":     "basic",
				"username": "user",
				"password": "pass",
			},
		},
	})
	if err != nil {
		t.Fatalf("ParseToolsetConfig: %v", err)
	}

	mgr := NewManager(slog.Default())
	defer mgr.Stop()

	if addErr := mgr.AddClient(cfg); addErr != nil {
		t.Fatalf("AddClient: %v", addErr)
	}
	tools, err := mgr.DiscoverTools(cfg)
	if err != nil {
		t.Fatalf("DiscoverTools: %v", err)
	}
	if len(tools) == 0 {
		t.Fatal("got 0 tools — env-var basic-auth block was not encoded into Authorization header")
	}
}

func TestAddClient_SSLVerifyFalse_WarnsOnce(t *testing.T) {
	verifyFalse := false
	cfg := &ToolsetConfig{
		ConnectionType: "sse",
		URL:            "https://unused.example.com/sse",
		ClientID:       "verify_false_warn",
		SSLVerify:      &verifyFalse,
		Manifest: []ManifestTool{
			{Name: "ping", InputSchema: map[string]any{"type": "object"}},
		},
	}

	h, shared := newCaptureLogger()
	mgr := NewManager(slog.New(h))
	defer mgr.Stop()

	if err := mgr.AddClient(cfg); err != nil {
		t.Fatalf("AddClient: %v", err)
	}

	rec, ok := shared.firstWithMessage("mcp ssl_config.verify=false; TLS verification disabled — do not use in production")
	if !ok {
		t.Fatalf("expected ssl_config.verify=false WARN log, got records: %+v", shared.snapshot())
	}
	if rec.Level != slog.LevelWarn {
		t.Errorf("level = %v, want WARN", rec.Level)
	}
	if rec.Attrs["clientID"] != "verify_false_warn" {
		t.Errorf("clientID attr = %v, want verify_false_warn", rec.Attrs["clientID"])
	}
	if rec.Attrs["url"] != "https://unused.example.com/sse" {
		t.Errorf("url attr = %v, want sse URL", rec.Attrs["url"])
	}
}

func TestAddClient_TLSVerifyEnvFalse_WarnsOnce(t *testing.T) {
	t.Setenv(EnvMCPConnectorTLSVerify, "false")

	cfg := &ToolsetConfig{
		ConnectionType: "sse",
		URL:            "https://unused.example.com/sse",
		ClientID:       "env_verify_false_warn",
		Manifest: []ManifestTool{
			{Name: "ping", InputSchema: map[string]any{"type": "object"}},
		},
	}

	h, shared := newCaptureLogger()
	mgr := NewManager(slog.New(h))
	defer mgr.Stop()

	if err := mgr.AddClient(cfg); err != nil {
		t.Fatalf("AddClient: %v", err)
	}

	rec, ok := shared.firstWithMessage("mcp TLS verification disabled via SAM_MCP_CONNECTOR_TLS_VERIFY=false — do not use in production")
	if !ok {
		t.Fatalf("expected env-driven TLS-off WARN log, got records: %+v", shared.snapshot())
	}
	if rec.Level != slog.LevelWarn {
		t.Errorf("level = %v, want WARN", rec.Level)
	}
	if rec.Attrs["clientID"] != "env_verify_false_warn" {
		t.Errorf("clientID attr = %v, want env_verify_false_warn", rec.Attrs["clientID"])
	}
	if rec.Attrs["url"] != "https://unused.example.com/sse" {
		t.Errorf("url attr = %v, want sse URL", rec.Attrs["url"])
	}

	if _, ok := shared.firstWithMessage("mcp ssl_config.verify=false; TLS verification disabled — do not use in production"); ok {
		t.Fatal("per-connector WARN fired on the env-driven path; the two paths must use distinct messages so operators can tell them apart")
	}
}

func TestAddClient_SSLVerifyTrue_NoWarn(t *testing.T) {
	verifyTrue := true
	cfg := &ToolsetConfig{
		ConnectionType: "sse",
		URL:            "https://unused.example.com/sse",
		ClientID:       "verify_true_no_warn",
		SSLVerify:      &verifyTrue,
		Manifest: []ManifestTool{
			{Name: "ping", InputSchema: map[string]any{"type": "object"}},
		},
	}

	h, shared := newCaptureLogger()
	mgr := NewManager(slog.New(h))
	defer mgr.Stop()

	if err := mgr.AddClient(cfg); err != nil {
		t.Fatalf("AddClient: %v", err)
	}

	if _, ok := shared.firstWithMessage("mcp ssl_config.verify=false; TLS verification disabled — do not use in production"); ok {
		t.Fatal("WARN fired with SSLVerify=true; should only fire when verify is explicitly false")
	}
}

func TestAddClient_EnvVerifyFalse_SucceedsAgainstSelfSignedServer(t *testing.T) {
	t.Setenv(EnvMCPConnectorTLSVerify, "false")
	srv, _ := newFakeMCPTLSServer(t)

	raw := map[string]any{
		"tool_type": "mcp",
		"connection_params": map[string]any{
			"type": "streamable-http",
			"url":  srv.URL,
		},
	}
	cfg, err := ParseToolsetConfig(raw)
	if err != nil {
		t.Fatalf("ParseToolsetConfig: %v", err)
	}

	mgr := NewManager(slog.Default())
	defer mgr.Stop()

	if err = mgr.AddClient(cfg); err != nil {
		t.Fatalf("AddClient: %v (env override should have skipped TLS verification)", err)
	}
	tools, err := mgr.DiscoverTools(cfg)
	if err != nil {
		t.Fatalf("DiscoverTools: %v", err)
	}
	if len(tools) != 1 {
		t.Fatalf("tools len = %d, want 1", len(tools))
	}
	if got := tools[0].Description(); got != "test tool" {
		t.Errorf("Description = %q, want %q", got, "test tool")
	}
}

func TestAddClient_EnvVerifyTrue_FailsAgainstSelfSignedServer(t *testing.T) {
	if testing.Short() {
		t.Skip("waits ~30s for Bifrost connection-establish timeout; skipped under -short")
	}
	t.Setenv(EnvMCPConnectorTLSVerify, "true")
	srv, _ := newFakeMCPTLSServer(t)

	raw := map[string]any{
		"tool_type": "mcp",
		"connection_params": map[string]any{
			"type": "streamable-http",
			"url":  srv.URL,
		},
	}
	cfg, err := ParseToolsetConfig(raw)
	if err != nil {
		t.Fatalf("ParseToolsetConfig: %v", err)
	}
	mgr := NewManager(slog.Default())
	defer mgr.Stop()

	if err := mgr.AddClient(cfg); err == nil {
		t.Fatal("AddClient succeeded against self-signed cert with env=true — expected x509 failure")
	}
}

func TestAddClient_PerConnectorVerifyTrueOverridesEnvFalse_FailsAgainstSelfSignedServer(t *testing.T) {
	if testing.Short() {
		t.Skip("waits ~30s for Bifrost connection-establish timeout; skipped under -short")
	}
	t.Setenv(EnvMCPConnectorTLSVerify, "false")
	srv, _ := newFakeMCPTLSServer(t)

	raw := map[string]any{
		"tool_type": "mcp",
		"connection_params": map[string]any{
			"type": "streamable-http",
			"url":  srv.URL,
			"ssl_config": map[string]any{
				"verify": true,
			},
		},
	}
	cfg, err := ParseToolsetConfig(raw)
	if err != nil {
		t.Fatalf("ParseToolsetConfig: %v", err)
	}
	mgr := NewManager(slog.Default())
	defer mgr.Stop()

	if err := mgr.AddClient(cfg); err == nil {
		t.Fatal("AddClient succeeded — per-connector ssl_config.verify=true must override env=false")
	}
}

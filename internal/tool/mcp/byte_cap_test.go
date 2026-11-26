// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package mcp

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/client/transport"
	mcpgo "github.com/mark3labs/mcp-go/mcp"
)

func TestParseToolsetConfig_MaxResponseBytes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		raw       any
		want      int64
		effective int64
	}{
		{"unset", nil, 0, MCPDefaultMaxResponseBytes},
		{"int_override", 1024 * 1024, int64(1024 * 1024), int64(1024 * 1024)},
		{"int64_override", int64(2 * 1024 * 1024), int64(2 * 1024 * 1024), int64(2 * 1024 * 1024)},
		{"float_override", float64(512), int64(512), int64(512)},
		{"negative_disables", -1, int64(-1), 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			conn := map[string]any{
				"type": "streamable-http",
				"url":  "https://mcp.example.com/v1",
			}
			if tc.raw != nil {
				conn["max_response_bytes"] = tc.raw
			}
			raw := map[string]any{
				"tool_type":         "mcp",
				"connection_params": conn,
			}
			cfg, err := ParseToolsetConfig(raw)
			if err != nil {
				t.Fatalf("ParseToolsetConfig: %v", err)
			}
			if cfg.MaxResponseBytes != tc.want {
				t.Errorf("MaxResponseBytes = %d, want %d", cfg.MaxResponseBytes, tc.want)
			}
			if got := cfg.EffectiveMaxResponseBytes(); got != tc.effective {
				t.Errorf("EffectiveMaxResponseBytes = %d, want %d", got, tc.effective)
			}
		})
	}
}

func TestCapReadCloser_PassThroughBelowLimit(t *testing.T) {
	t.Parallel()
	const limit = 1024
	body := bytes.Repeat([]byte("x"), 512)
	tripped := false
	r := &capReadCloser{
		base:   io.NopCloser(bytes.NewReader(body)),
		limit:  limit,
		onTrip: func(int64) { tripped = true },
	}
	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if len(got) != len(body) {
		t.Errorf("read %d bytes, want %d", len(got), len(body))
	}
	if tripped {
		t.Error("trip callback fired below the cap")
	}
}

func TestCapReadCloser_AtLimit(t *testing.T) {
	t.Parallel()
	const limit = 1024
	body := bytes.Repeat([]byte("x"), limit)
	tripped := false
	r := &capReadCloser{
		base:   io.NopCloser(bytes.NewReader(body)),
		limit:  limit,
		onTrip: func(int64) { tripped = true },
	}
	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if int64(len(got)) != limit {
		t.Errorf("read %d bytes, want %d", len(got), limit)
	}
	if tripped {
		t.Error("trip callback fired at the cap (cap is inclusive)")
	}
}

func TestCapReadCloser_OverLimit(t *testing.T) {
	t.Parallel()
	const limit = 1024
	body := bytes.Repeat([]byte("x"), 2*limit)
	var trips atomic.Int64
	r := &capReadCloser{
		base:   io.NopCloser(bytes.NewReader(body)),
		limit:  limit,
		onTrip: func(int64) { trips.Add(1) },
	}
	_, err := io.ReadAll(r)
	if err == nil {
		t.Fatal("ReadAll succeeded; want ErrMCPResponseTooLarge")
	}
	if !errors.Is(err, ErrMCPResponseTooLarge) {
		t.Errorf("err = %v, want ErrMCPResponseTooLarge", err)
	}
	if got := trips.Load(); got != 1 {
		t.Errorf("trip callback fired %d times, want 1", got)
	}
}

type largeResponseFakeServer struct {
	textBytes int
}

func (f *largeResponseFakeServer) handle(w http.ResponseWriter, r *http.Request) {
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
		writeJSONRPC(w, req.ID, mcpgo.InitializeResult{
			ProtocolVersion: mcpgo.LATEST_LEGACY_PROTOCOL_VERSION,
			ServerInfo:      mcpgo.Implementation{Name: "fake", Version: "1.0.0"},
			Capabilities:    mcpgo.ServerCapabilities{},
		})
	case "tools/list":
		writeJSONRPC(w, req.ID, mcpgo.ListToolsResult{
			Tools: []mcpgo.Tool{{
				Name:        "ping",
				Description: "test tool",
				InputSchema: mcpgo.ToolInputSchema{
					Type:       "object",
					Properties: map[string]any{},
				},
			}},
		})
	case "tools/call":
		writeJSONRPC(w, req.ID, map[string]any{
			"content": []any{
				map[string]any{"type": "text", "text": strings.Repeat("x", f.textBytes)},
			},
		})
	default:
		writeJSONRPC(w, req.ID, map[string]any{})
	}
}

func newLargeResponseTLSServer(t *testing.T, textBytes int) *httptest.Server {
	t.Helper()
	f := &largeResponseFakeServer{textBytes: textBytes}
	srv := httptest.NewTLSServer(http.HandlerFunc(f.handle))
	t.Cleanup(srv.Close)
	return srv
}

func newDirectClientCfg(srv *httptest.Server, clientID string, cap int64) *ToolsetConfig {
	verify := false
	return &ToolsetConfig{
		ConnectionType:   "streamable-http",
		URL:              srv.URL,
		SSLVerify:        &verify,
		ClientID:         clientID,
		MaxResponseBytes: cap,
	}
}

func TestByteCap_HTTPTrips_BelowOverride(t *testing.T) {
	srv := newLargeResponseTLSServer(t, 2*1024*1024)
	cfg := newDirectClientCfg(srv, "fake_mcp_cap_below", 1024*1024)

	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, nil))
	mgr := NewManager(logger)
	defer mgr.Stop()

	if err := mgr.AddClient(cfg); err != nil {
		t.Fatalf("AddClient: %v", err)
	}

	bifrostName := cfg.ClientID + "-ping"
	_, err := mgr.ExecuteToolRaw(t.Context(), bifrostName, map[string]any{})
	if err == nil {
		t.Fatal("ExecuteToolRaw succeeded; want ErrMCPResponseTooLarge")
	}
	if !errors.Is(err, ErrMCPResponseTooLarge) {
		t.Errorf("err = %v, want ErrMCPResponseTooLarge", err)
	}
	if !strings.Contains(err.Error(), "MCP response exceeded 1048576 bytes") {
		t.Errorf("err message = %q, want it to mention the 1 MiB limit", err.Error())
	}
	if !strings.Contains(logBuf.String(), "mcp response cap exceeded") {
		t.Errorf("expected operator WARN; got log:\n%s", logBuf.String())
	}
	if !strings.Contains(logBuf.String(), `clientID=`+cfg.ClientID) {
		t.Errorf("WARN missing clientID; got log:\n%s", logBuf.String())
	}
}

func TestByteCap_HTTPSucceeds_BelowCap(t *testing.T) {
	srv := newLargeResponseTLSServer(t, 16*1024)
	cfg := newDirectClientCfg(srv, "fake_mcp_under_cap", 1024*1024)

	mgr := NewManager(slog.Default())
	defer mgr.Stop()

	if err := mgr.AddClient(cfg); err != nil {
		t.Fatalf("AddClient: %v", err)
	}

	bifrostName := cfg.ClientID + "-ping"
	res, err := mgr.ExecuteToolRaw(t.Context(), bifrostName, map[string]any{})
	if err != nil {
		t.Fatalf("ExecuteToolRaw: %v", err)
	}
	if res == nil || len(res.Content) != 1 {
		t.Fatalf("unexpected result: %+v", res)
	}
}

func TestByteCap_PerClientOverride_RaisesCap(t *testing.T) {
	srv := newLargeResponseTLSServer(t, 3*1024*1024)
	cfg := newDirectClientCfg(srv, "fake_mcp_override_higher", 4*1024*1024)

	mgr := NewManager(slog.Default())
	defer mgr.Stop()

	if err := mgr.AddClient(cfg); err != nil {
		t.Fatalf("AddClient: %v", err)
	}

	bifrostName := cfg.ClientID + "-ping"
	res, err := mgr.ExecuteToolRaw(t.Context(), bifrostName, map[string]any{})
	if err != nil {
		t.Fatalf("ExecuteToolRaw with raised cap: %v", err)
	}
	if res == nil || len(res.Content) != 1 {
		t.Fatalf("unexpected result: %+v", res)
	}
}

func TestByteCap_NegativeDisablesCap(t *testing.T) {
	srv := newLargeResponseTLSServer(t, 2*1024*1024)
	cfg := newDirectClientCfg(srv, "fake_mcp_no_cap", -1)
	if got := cfg.EffectiveMaxResponseBytes(); got != 0 {
		t.Fatalf("EffectiveMaxResponseBytes for negative override = %d, want 0", got)
	}

	mgr := NewManager(slog.Default())
	defer mgr.Stop()

	if err := mgr.AddClient(cfg); err != nil {
		t.Fatalf("AddClient: %v", err)
	}

	bifrostName := cfg.ClientID + "-ping"
	if _, err := mgr.ExecuteToolRaw(t.Context(), bifrostName, map[string]any{}); err != nil {
		t.Fatalf("ExecuteToolRaw with disabled cap: %v", err)
	}
}

const aboveTransportDefaultBound = int(transport.DefaultMaxMessageBytes) + 1024*1024

func TestByteCap_RaisedCapAboveTransportDefaultBound(t *testing.T) {
	srv := newLargeResponseTLSServer(t, aboveTransportDefaultBound)
	cfg := newDirectClientCfg(srv, "fake_mcp_above_transport_bound", int64(aboveTransportDefaultBound)*2)

	mgr := NewManager(slog.Default())
	defer mgr.Stop()

	if err := mgr.AddClient(cfg); err != nil {
		t.Fatalf("AddClient: %v", err)
	}

	res, err := mgr.ExecuteToolRaw(t.Context(), cfg.ClientID+"-ping", map[string]any{})
	if err != nil {
		t.Fatalf("ExecuteToolRaw: %v", err)
	}
	if res == nil || len(res.Content) != 1 {
		t.Fatalf("unexpected result: %+v", res)
	}
}

func TestByteCap_UncappedAboveTransportDefaultBound(t *testing.T) {
	srv := newLargeResponseTLSServer(t, aboveTransportDefaultBound)
	cfg := newDirectClientCfg(srv, "fake_mcp_uncapped_above_transport_bound", -1)

	mgr := NewManager(slog.Default())
	defer mgr.Stop()

	if err := mgr.AddClient(cfg); err != nil {
		t.Fatalf("AddClient: %v", err)
	}

	if _, err := mgr.ExecuteToolRaw(t.Context(), cfg.ClientID+"-ping", map[string]any{}); err != nil {
		t.Fatalf("ExecuteToolRaw: %v", err)
	}
}

func TestByteCap_ManifestPath_RaisedCapAboveTransportDefaultBound(t *testing.T) {
	srv := newLargeResponseServer(t, aboveTransportDefaultBound)
	cfg := &ToolsetConfig{
		ConnectionType:   "streamable-http",
		URL:              srv.URL,
		ClientID:         "fake_mcp_manifest_above_transport_bound",
		MaxResponseBytes: int64(aboveTransportDefaultBound) * 2,
		Manifest: []ManifestTool{
			{
				Name:        "ping",
				Description: "test tool",
				InputSchema: map[string]any{"type": "object"},
			},
		},
	}

	mgr := NewManager(slog.Default())
	defer mgr.Stop()

	if err := mgr.AddClient(cfg); err != nil {
		t.Fatalf("AddClient: %v", err)
	}

	if _, err := mgr.ExecuteToolRaw(t.Context(), cfg.ClientID+"-ping", map[string]any{}); err != nil {
		t.Fatalf("ExecuteToolRaw: %v", err)
	}
}

func TestByteCap_ManifestPath_Trips(t *testing.T) {
	srv := newLargeResponseServer(t, 2*1024*1024)
	cfg := &ToolsetConfig{
		ConnectionType:   "streamable-http",
		URL:              srv.URL,
		ClientID:         "fake_mcp_manifest_cap",
		MaxResponseBytes: 1024 * 1024,
		Manifest: []ManifestTool{
			{
				Name:        "ping",
				Description: "test tool",
				InputSchema: map[string]any{"type": "object"},
			},
		},
	}

	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, nil))
	mgr := NewManager(logger)
	defer mgr.Stop()

	if err := mgr.AddClient(cfg); err != nil {
		t.Fatalf("AddClient: %v", err)
	}

	bifrostName := cfg.ClientID + "-ping"
	_, err := mgr.ExecuteToolRaw(t.Context(), bifrostName, map[string]any{})
	if err == nil {
		t.Fatal("ExecuteToolRaw succeeded; want ErrMCPResponseTooLarge")
	}
	if !errors.Is(err, ErrMCPResponseTooLarge) {
		t.Errorf("err = %v, want ErrMCPResponseTooLarge", err)
	}
	if !strings.Contains(logBuf.String(), "mcp response cap exceeded") {
		t.Errorf("expected operator WARN; got log:\n%s", logBuf.String())
	}
}

func newLargeResponseServer(t *testing.T, textBytes int) *httptest.Server {
	t.Helper()
	f := &largeResponseFakeServer{textBytes: textBytes}
	srv := httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(srv.Close)
	return srv
}

func TestByteCap_WarnLoggedOnce(t *testing.T) {
	srv := newLargeResponseTLSServer(t, 2*1024*1024)
	cfg := newDirectClientCfg(srv, "fake_mcp_warn_once", 1024*1024)

	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, nil))
	mgr := NewManager(logger)
	defer mgr.Stop()

	if err := mgr.AddClient(cfg); err != nil {
		t.Fatalf("AddClient: %v", err)
	}

	bifrostName := cfg.ClientID + "-ping"
	_, _ = mgr.ExecuteToolRaw(t.Context(), bifrostName, map[string]any{})

	count := strings.Count(logBuf.String(), "mcp response cap exceeded")
	if count != 1 {
		t.Errorf("WARN log fired %d times, want exactly 1; log:\n%s", count, logBuf.String())
	}
}

func TestByteCap_CloseIdleConnectionsReachesTheConnectionPool(t *testing.T) {
	closed := make(chan struct{}, 1)
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	srv.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateClosed {
			closed <- struct{}{}
		}
	}
	srv.Start()
	t.Cleanup(srv.Close)

	client := &http.Client{Transport: NewByteCappedTransport(&http.Transport{}, 1024, "idle-test", slog.Default())}
	resp, err := client.Get(srv.URL)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()

	client.CloseIdleConnections()
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("the idle connection stayed open after CloseIdleConnections")
	}
}

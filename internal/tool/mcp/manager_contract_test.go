// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/client/transport"
	mcpgo "github.com/mark3labs/mcp-go/mcp"
)

func TestMCPDefaultTimeout_Is30s(t *testing.T) {
	if MCPDefaultTimeoutSeconds != 30 {
		t.Errorf("MCPDefaultTimeoutSeconds = %d, want 30 (Python MCP_DEFAULT_TIMEOUT)", MCPDefaultTimeoutSeconds)
	}
	want := 30 * time.Second
	if directConnectTimeout != want {
		t.Errorf("directConnectTimeout = %v, want %v", directConnectTimeout, want)
	}
}

func TestMCPDefaultSSEReadTimeout_Is300s(t *testing.T) {
	if MCPDefaultSSEReadTimeoutSeconds != 300 {
		t.Errorf("MCPDefaultSSEReadTimeoutSeconds = %d, want 300 (Python MCP_DEFAULT_SSE_READ_TIMEOUT)", MCPDefaultSSEReadTimeoutSeconds)
	}
	cfg := &ToolsetConfig{ConnectionType: "sse"}
	if got := cfg.EffectiveSSEReadTimeout(); got != 300*time.Second {
		t.Errorf("EffectiveSSEReadTimeout default = %v, want 300s", got)
	}
}

func TestEffectiveSSEReadTimeout_Override(t *testing.T) {
	cases := []struct {
		name    string
		timeout int
		want    time.Duration
	}{
		{"default", 0, 300 * time.Second},
		{"lowered", 60, 60 * time.Second},
		{"raised", 900, 900 * time.Second},
		{"disabled", -1, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &ToolsetConfig{ConnectionType: "sse", SSEReadTimeout: tc.timeout}
			if got := cfg.EffectiveSSEReadTimeout(); got != tc.want {
				t.Errorf("EffectiveSSEReadTimeout(%d) = %v, want %v", tc.timeout, got, tc.want)
			}
		})
	}
}

func TestParseToolsetConfig_SSEReadTimeout(t *testing.T) {
	raw := map[string]any{
		"tool_type": "mcp",
		"connection_params": map[string]any{
			"type":             "streamable-http",
			"url":              "https://mcp.example.com",
			"sse_read_timeout": 120,
		},
	}
	cfg, err := ParseToolsetConfig(raw)
	if err != nil {
		t.Fatalf("ParseToolsetConfig: %v", err)
	}
	if cfg.SSEReadTimeout != 120 {
		t.Errorf("SSEReadTimeout = %d, want 120", cfg.SSEReadTimeout)
	}
	if got := cfg.EffectiveSSEReadTimeout(); got != 120*time.Second {
		t.Errorf("EffectiveSSEReadTimeout = %v, want 120s", got)
	}
}

func TestWithSSEReadTimeout_AppliesToSSEAndStreamableHTTP(t *testing.T) {
	cases := []struct {
		name       string
		connection string
		wantCap    bool
	}{
		{"sse", connTypeSSE, true},
		{"streamable-http", connTypeStreamableHTTP, true},
		{"stdio", connTypeStdio, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &ToolsetConfig{ConnectionType: tc.connection}
			ctx, cancel := withSSEReadTimeout(context.Background(), cfg)
			defer cancel()
			_, hasDeadline := ctx.Deadline()
			if hasDeadline != tc.wantCap {
				t.Errorf("withSSEReadTimeout(%s) hasDeadline=%v, want %v", tc.connection, hasDeadline, tc.wantCap)
			}
		})
	}
}

func TestWithSSEReadTimeout_Disabled(t *testing.T) {
	cfg := &ToolsetConfig{ConnectionType: connTypeSSE, SSEReadTimeout: -1}
	ctx, cancel := withSSEReadTimeout(context.Background(), cfg)
	defer cancel()
	if _, ok := ctx.Deadline(); ok {
		t.Error("SSEReadTimeout=-1 must not add a deadline")
	}
}

func TestWithSSEReadTimeout_NilCfg(t *testing.T) {
	ctx, cancel := withSSEReadTimeout(context.Background(), nil)
	defer cancel()
	if _, ok := ctx.Deadline(); ok {
		t.Error("nil cfg must not add a deadline")
	}
}

func TestExecuteToolRaw_AppliesSSEReadTimeout(t *testing.T) {
	started := make(chan struct{})
	var once sync.Once
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = r.Body.Close()
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		_ = json.Unmarshal(body, &req)
		if len(req.ID) == 0 || string(req.ID) == "null" {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		switch req.Method {
		case "initialize":
			w.Header().Set("Content-Type", "application/json")
			writeJSONRPC(w, req.ID, mcpgo.InitializeResult{
				ProtocolVersion: mcpgo.LATEST_LEGACY_PROTOCOL_VERSION,
				ServerInfo:      mcpgo.Implementation{Name: "stall-mock", Version: "1"},
			})
		case "tools/call":
			once.Do(func() { close(started) })
			<-r.Context().Done()
		default:
			w.Header().Set("Content-Type", "application/json")
			writeJSONRPC(w, req.ID, map[string]any{})
		}
	}))
	defer srv.Close()

	cfg := &ToolsetConfig{
		ConnectionType: "streamable-http",
		URL:            srv.URL,
		ClientID:       "stall_test",
		SSEReadTimeout: 1,
		Manifest: []ManifestTool{
			{Name: "ping", InputSchema: map[string]any{"type": "object"}},
		},
	}
	mgr := NewManager(slog.Default())
	defer mgr.Stop()
	if err := mgr.AddClient(cfg); err != nil {
		t.Fatalf("AddClient: %v", err)
	}

	start := time.Now()
	_, err := mgr.ExecuteToolRawWithHeaders(context.Background(), "stall_test-ping", map[string]any{}, nil, nil)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected deadline-exceeded error, got nil")
	}
	if !errors.Is(err, context.DeadlineExceeded) && !strings.Contains(err.Error(), "deadline") && !strings.Contains(err.Error(), "context canceled") {
		t.Errorf("error = %v, want deadline-exceeded", err)
	}
	if elapsed > 5*time.Second {
		t.Errorf("call took %v — deadline was 1s; cap must be enforced", elapsed)
	}
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Error("tool-call never reached the server")
	}
}

func TestDiscoverTools_SnapshotOnce(t *testing.T) {
	srv, mock := newLazyConnectServer(t)
	cfg := &ToolsetConfig{
		ConnectionType: "streamable-http",
		URL:            srv.URL,
		SSLVerify:      nil,
		ClientID:       "snapshot_test",
	}
	f := false
	cfg.SSLVerify = &f

	mgr := NewManager(slog.Default())
	defer mgr.Stop()
	if err := mgr.AddClient(cfg); err != nil {
		t.Fatalf("AddClient: %v", err)
	}

	if got := mock.listToolsN.Load(); got != 1 {
		t.Fatalf("listTools after AddClient = %d, want 1", got)
	}

	for i := range 5 {
		if _, err := mgr.DiscoverTools(cfg); err != nil {
			t.Fatalf("DiscoverTools iter %d: %v", i, err)
		}
	}
	if got := mock.listToolsN.Load(); got != 1 {
		t.Errorf("listTools after 5 DiscoverTools calls = %d, want 1 (snapshot must not refresh)", got)
	}
}

func TestExecuteToolRaw_NoListToolsPerCall(t *testing.T) {
	srv, mock := newLazyConnectServer(t)
	verify := false
	cfg := &ToolsetConfig{
		ConnectionType: "streamable-http",
		URL:            srv.URL,
		SSLVerify:      &verify,
		ClientID:       "list_tools_once",
	}
	mgr := NewManager(slog.Default())
	defer mgr.Stop()
	if err := mgr.AddClient(cfg); err != nil {
		t.Fatalf("AddClient: %v", err)
	}
	if got := mock.listToolsN.Load(); got != 1 {
		t.Fatalf("listTools after AddClient = %d, want exactly 1", got)
	}

	ctx := t.Context()
	const calls = 5
	for i := range calls {
		_, _ = mgr.ExecuteToolRawWithHeaders(ctx, "list_tools_once-ping", map[string]any{}, nil, nil)
		if got := mock.listToolsN.Load(); got != 1 {
			t.Fatalf("listTools after ExecuteToolRaw iter %d = %d, want 1 (snapshot, not per-call)", i, got)
		}
	}
	if got := mock.toolsCallN.Load(); got != calls {
		t.Errorf("toolsCallN = %d after %d ExecuteToolRaw calls, want %d", got, calls, calls)
	}
}

func TestExecuteToolRaw_WrapsHTTP403(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = r.Body.Close()
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		_ = json.Unmarshal(body, &req)
		if len(req.ID) == 0 || string(req.ID) == "null" {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		switch req.Method {
		case "initialize":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			writeJSONRPC(w, req.ID, mcpgo.InitializeResult{
				ProtocolVersion: mcpgo.LATEST_LEGACY_PROTOCOL_VERSION,
				ServerInfo:      mcpgo.Implementation{Name: "x", Version: "1"},
			})
		case "tools/call":
			http.Error(w, "forbidden", http.StatusForbidden)
		default:
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			writeJSONRPC(w, req.ID, map[string]any{})
		}
	}))
	defer srv.Close()

	cfg := &ToolsetConfig{
		ConnectionType: "streamable-http",
		URL:            srv.URL,
		ClientID:       "http403_test",
		Manifest: []ManifestTool{
			{Name: "ping", InputSchema: map[string]any{"type": "object"}},
		},
	}
	mgr := NewManager(slog.Default())
	defer mgr.Stop()
	if err := mgr.AddClient(cfg); err != nil {
		t.Fatalf("AddClient: %v", err)
	}

	_, err := mgr.ExecuteToolRawWithHeaders(t.Context(), "http403_test-ping", map[string]any{}, nil, nil)
	if err == nil {
		t.Fatal("expected transport 403 error, got nil")
	}
	if !errors.Is(err, ErrAuthRequired) {
		t.Errorf("403 error %v does not wrap ErrAuthRequired; re-auth path would miss", err)
	}
}

func TestIsTransportAuthError_Matrix(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"plain", errors.New("connection refused"), false},
		{"ErrAuthRequired direct", ErrAuthRequired, true},
		{"ErrAuthRequired wrapped", errors.Join(errors.New("upstream"), ErrAuthRequired), true},
		{"HTTP 401", errors.New("unexpected status code: 401 Unauthorized"), true},
		{"HTTP 403", errors.New("unexpected status code: 403 Forbidden"), true},
		{"HTTP 500", errors.New("status code: 500 Internal Server Error"), false},
		{"mcp-go status 401", errors.New("request failed with status code: 401"), true},
		{"status-only 401", errors.New("status code: 401"), true},
		{"status-only 403", errors.New("status code: 403"), true},
		{"mcp-go 401", fmt.Errorf("send request: %w", &transport.AuthorizationRequiredError{}), true},
		{"mcp-go OAuth token missing", fmt.Errorf("send request: %w", &transport.OAuthAuthorizationRequiredError{}), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isTransportAuthError(tc.err); got != tc.want {
				t.Errorf("isTransportAuthError(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

func TestSharedTransport_PerUserHeadersReachServer(t *testing.T) {
	var observed []string
	var mu sync.Mutex
	recordAuth := func(v string) {
		mu.Lock()
		observed = append(observed, v)
		mu.Unlock()
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = r.Body.Close()
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		_ = json.Unmarshal(body, &req)
		if len(req.ID) == 0 || string(req.ID) == "null" {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		if req.Method == "tools/call" {
			recordAuth(r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		switch req.Method {
		case "initialize":
			writeJSONRPC(w, req.ID, mcpgo.InitializeResult{
				ProtocolVersion: mcpgo.LATEST_LEGACY_PROTOCOL_VERSION,
				ServerInfo:      mcpgo.Implementation{Name: "shared-tx", Version: "1"},
			})
		case "tools/call":
			writeJSONRPC(w, req.ID, map[string]any{
				"content": []any{map[string]any{"type": "text", "text": "ok"}},
			})
		default:
			writeJSONRPC(w, req.ID, map[string]any{})
		}
	}))
	defer srv.Close()

	cfg := &ToolsetConfig{
		ConnectionType: "streamable-http",
		URL:            srv.URL,
		ClientID:       "shared_tx_peruser",
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
		return http.Header{"Authorization": []string{"Bearer first-connector-userA"}}, nil
	}

	hdrsA := http.Header{"Authorization": []string{"Bearer userA-tool-token"}}
	if _, err := mgr.ExecuteToolRawWithHeaders(t.Context(), "shared_tx_peruser-ping", map[string]any{}, hdrsA, nil); err != nil {
		t.Fatalf("User A call: %v", err)
	}

	hdrsB := http.Header{"Authorization": []string{"Bearer userB-tool-token"}}
	if _, err := mgr.ExecuteToolRawWithHeaders(t.Context(), "shared_tx_peruser-ping", map[string]any{}, hdrsB, nil); err != nil {
		t.Fatalf("User B call: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(observed) != 2 {
		t.Fatalf("observed %d tool-call bearers, want 2 (saw %v)", len(observed), observed)
	}
	if observed[0] != "Bearer userA-tool-token" {
		t.Errorf("tool-call 1 Authorization = %q, want User A's token", observed[0])
	}
	if observed[1] != "Bearer userB-tool-token" {
		t.Errorf("tool-call 2 Authorization = %q, want User B's token (shared transport leaked first user's token)", observed[1])
	}
}

func TestSharedTransport_ConcurrentMultiUserInvocations(t *testing.T) {
	const N = 16
	var initN atomic.Int64
	var callN atomic.Int64
	var auths sync.Map

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = r.Body.Close()
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params struct {
				Arguments map[string]any `json:"arguments"`
			} `json:"params"`
		}
		_ = json.Unmarshal(body, &req)
		if len(req.ID) == 0 || string(req.ID) == "null" {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch req.Method {
		case "initialize":
			initN.Add(1)
			writeJSONRPC(w, req.ID, mcpgo.InitializeResult{
				ProtocolVersion: mcpgo.LATEST_LEGACY_PROTOCOL_VERSION,
				ServerInfo:      mcpgo.Implementation{Name: "concurrent-mu", Version: "1"},
			})
		case "tools/call":
			callN.Add(1)
			if uid, ok := req.Params.Arguments["user_tag"].(string); ok {
				auths.Store(uid, r.Header.Get("Authorization"))
			}
			writeJSONRPC(w, req.ID, map[string]any{
				"content": []any{map[string]any{"type": "text", "text": "ok"}},
			})
		default:
			writeJSONRPC(w, req.ID, map[string]any{})
		}
	}))
	defer srv.Close()

	cfg := &ToolsetConfig{
		ConnectionType: "streamable-http",
		URL:            srv.URL,
		ClientID:       "concurrent_multiuser",
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
		return http.Header{"Authorization": []string{"Bearer connector"}}, nil
	}

	var wg sync.WaitGroup
	errs := make([]error, N)
	for i := range N {
		wg.Go(func() {
			userTag := "user-" + itoaFixed(i)
			hdrs := http.Header{"Authorization": []string{"Bearer " + userTag + "-token"}}
			_, err := mgr.ExecuteToolRawWithHeaders(t.Context(), "concurrent_multiuser-ping",
				map[string]any{"user_tag": userTag}, hdrs, nil)
			errs[i] = err
		})
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("goroutine %d: %v", i, err)
		}
	}
	if got := initN.Load(); got != 1 {
		t.Errorf("initialize count = %d under %d concurrent users, want 1 (per-client mutex must serialize connect)", got, N)
	}
	if got := callN.Load(); got != N {
		t.Errorf("tools/call count = %d, want %d", got, N)
	}

	for i := range N {
		userTag := "user-" + itoaFixed(i)
		v, ok := auths.Load(userTag)
		if !ok {
			t.Errorf("no Authorization observed for %s", userTag)
			continue
		}
		want := "Bearer " + userTag + "-token"
		if v.(string) != want {
			t.Errorf("%s Authorization = %q, want %q", userTag, v, want)
		}
	}
}

func TestSession_401InvalidateRace_ConcurrentUsers(t *testing.T) {
	var callN atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = r.Body.Close()
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		_ = json.Unmarshal(body, &req)
		if len(req.ID) == 0 || string(req.ID) == "null" {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch req.Method {
		case "initialize":
			writeJSONRPC(w, req.ID, mcpgo.InitializeResult{
				ProtocolVersion: mcpgo.LATEST_LEGACY_PROTOCOL_VERSION,
				ServerInfo:      mcpgo.Implementation{Name: "race", Version: "1"},
			})
		case "tools/call":
			if callN.Add(1) == 1 {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			writeJSONRPC(w, req.ID, map[string]any{
				"content": []any{map[string]any{"type": "text", "text": "ok"}},
			})
		default:
			writeJSONRPC(w, req.ID, map[string]any{})
		}
	}))
	defer srv.Close()

	cfg := &ToolsetConfig{
		ConnectionType: "streamable-http",
		URL:            srv.URL,
		ClientID:       "race_401",
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
	_ = dc

	const N = 8
	var wg sync.WaitGroup
	errs := make([]error, N)
	for i := range N {
		wg.Go(func() {
			_, err := mgr.ExecuteToolRawWithHeaders(t.Context(), "race_401-ping",
				map[string]any{}, nil, nil)
			errs[i] = err
		})
	}
	wg.Wait()

	var authErrs int
	for _, err := range errs {
		if errors.Is(err, ErrAuthRequired) {
			authErrs++
		}
	}
	if authErrs < 1 {
		t.Errorf("got %d ErrAuthRequired errors, want at least 1", authErrs)
	}
	if _, err := mgr.ExecuteToolRawWithHeaders(t.Context(), "race_401-ping",
		map[string]any{}, nil, nil); err != nil {
		t.Errorf("follow-up call after race: %v (manager stuck after invalidate)", err)
	}
}

func TestSession_CleanupOnStartFailure(t *testing.T) {
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
		return http.Header{"Authorization": []string{"Bearer x"}}, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*directConnectTimeout)
	defer cancel()
	if _, err := mgr.ensureConnected(ctx, dc, nil); err == nil {
		t.Fatal("expected ensureConnected to fail against unreachable URL")
	}

	dc.mu.Lock()
	defer dc.mu.Unlock()
	if dc.transport != nil {
		t.Error("transport not cleared after Start failure")
	}
	if dc.mcp != nil {
		t.Error("mcp client not cleared after Start failure")
	}
	if dc.authFetch != nil {
		t.Error("authFetch not cleared after Start failure")
	}
}

func TestSession_CleanupOnInvalidate(t *testing.T) {
	srv, mock := newLazyConnectServer(t)
	mock.toolCallResult = map[string]any{
		"isError": true,
		"content": []any{map[string]any{"type": "text", "text": "401 Unauthorized"}},
	}
	cfg := &ToolsetConfig{
		ConnectionType: "streamable-http",
		URL:            srv.URL,
		ClientID:       "cleanup_invalidate",
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
		return http.Header{"Authorization": []string{"Bearer stale"}}, nil
	}

	result, err := mgr.ExecuteToolRawWithHeaders(t.Context(), "cleanup_invalidate-ping",
		map[string]any{}, nil, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result == nil || !result.IsError {
		t.Fatalf("expected IsError result, got %+v", result)
	}

	dc.mu.Lock()
	defer dc.mu.Unlock()
	if dc.transport != nil {
		t.Error("transport not cleared after 401 invalidate()")
	}
	if dc.mcp != nil {
		t.Error("mcp client not cleared after 401 invalidate()")
	}
	if dc.authFetch != nil {
		t.Error("authFetch not cleared after 401 invalidate()")
	}
}

func itoaFixed(n int) string {
	if n < 0 || n > 99 {
		return "xx"
	}
	return string(rune('0'+n/10)) + string(rune('0'+n%10))
}

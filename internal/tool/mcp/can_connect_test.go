// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package mcp

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	mcpgo "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func canConnectConfig(t *testing.T, connType, url string) *ToolsetConfig {
	t.Helper()
	cfg, err := ParseToolsetConfig(map[string]any{
		"tool_type":         "mcp",
		"connection_params": map[string]any{"type": connType, "url": url},
	})
	if err != nil {
		t.Fatalf("ParseToolsetConfig: %v", err)
	}
	return cfg
}

func statusServer(t *testing.T, status int) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, http.StatusText(status), status)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestCanConnect(t *testing.T) {
	mcpServer := server.NewMCPServer("up", "1.0.0")
	mcpServer.AddTool(mcpgo.NewTool("ping"), func(context.Context, mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		return mcpgo.NewToolResultText("pong"), nil
	})
	up := httptest.NewServer(server.NewStreamableHTTPServer(mcpServer))
	t.Cleanup(up.Close)
	sse := httptest.NewUnstartedServer(nil)
	sse.Config.Handler = server.NewSSEServer(mcpServer, server.WithBaseURL("http://"+sse.Listener.Addr().String()))
	sse.Start()
	t.Cleanup(sse.Close)
	refused := httptest.NewServer(http.NotFoundHandler())
	refused.Close()

	type outcome int
	const (
		connects outcome = iota
		retryable
		permanent
	)
	cases := []struct {
		name     string
		connType string
		url      string
		want     outcome
	}{
		{name: "streamable-http server up", connType: connTypeStreamableHTTP, url: up.URL, want: connects},
		{name: "SSE server up", connType: connTypeSSE, url: sse.URL + "/sse", want: connects},
		{name: "refuses on auth grounds", connType: connTypeStreamableHTTP, url: statusServer(t, http.StatusUnauthorized), want: permanent},
		{name: "SSE refuses on auth grounds", connType: connTypeSSE, url: statusServer(t, http.StatusUnauthorized), want: permanent},
		{name: "bad request", connType: connTypeStreamableHTTP, url: statusServer(t, http.StatusBadRequest), want: retryable},
		{name: "not found", connType: connTypeStreamableHTTP, url: statusServer(t, http.StatusNotFound), want: retryable},
		{name: "SSE not found", connType: connTypeSSE, url: statusServer(t, http.StatusNotFound), want: retryable},
		{name: "method not allowed", connType: connTypeStreamableHTTP, url: statusServer(t, http.StatusMethodNotAllowed), want: retryable},
		{name: "proxy wants auth", connType: connTypeStreamableHTTP, url: statusServer(t, http.StatusProxyAuthRequired), want: retryable},
		{name: "rate limited", connType: connTypeStreamableHTTP, url: statusServer(t, http.StatusTooManyRequests), want: retryable},
		{name: "still starting", connType: connTypeStreamableHTTP, url: statusServer(t, http.StatusServiceUnavailable), want: retryable},
		{name: "gateway in front of a dead server", connType: connTypeSSE, url: statusServer(t, http.StatusBadGateway), want: retryable},
		{name: "nothing listening", connType: connTypeStreamableHTTP, url: refused.URL, want: retryable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mgr := NewManager(slog.Default())
			t.Cleanup(mgr.Stop)
			cfg := canConnectConfig(t, tc.connType, tc.url)
			start := time.Now()
			err := mgr.CanConnect(t.Context(), cfg)
			if took := time.Since(start); took > 2*time.Second {
				t.Errorf("CanConnect took %v, want an answer at once", took)
			}
			got := connects
			switch {
			case err != nil && IsPermanentConnectError(cfg, err):
				got = permanent
			case err != nil:
				got = retryable
			}
			if got != tc.want {
				t.Fatalf("CanConnect = %v (outcome %d), want outcome %d", err, got, tc.want)
			}
		})
	}
}

func TestCanConnect_GivesUpWhenItsContextEnds(t *testing.T) {
	hang := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { <-hang }))
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(hang) })

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	mgr := NewManager(slog.Default())
	t.Cleanup(mgr.Stop)
	start := time.Now()
	if err := mgr.CanConnect(ctx, canConnectConfig(t, connTypeStreamableHTTP, srv.URL)); err == nil {
		t.Fatal("CanConnect = nil for a server that never answered before the context ended")
	}
	if took := time.Since(start); took > time.Second {
		t.Fatalf("CanConnect took %v after its context ended, want it to give up at once", took)
	}
}

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

func pingServer(t *testing.T) string {
	t.Helper()
	s := server.NewMCPServer("ping", "1.0.0")
	s.AddTool(mcpgo.NewTool("ping"), func(context.Context, mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		return mcpgo.NewToolResultText("pong"), nil
	})
	srv := httptest.NewServer(server.NewStreamableHTTPServer(s))
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestAddClientAndToolCallsDoNotWaitForEachOther(t *testing.T) {
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	busy := server.NewMCPServer("busy", "1.0.0")
	busy.AddTool(mcpgo.NewTool("slow"), func(ctx context.Context, _ mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		entered <- struct{}{}
		select {
		case <-release:
		case <-ctx.Done():
		}
		return mcpgo.NewToolResultText("done"), nil
	})
	busy.AddTool(mcpgo.NewTool("fast"), func(context.Context, mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		return mcpgo.NewToolResultText("done"), nil
	})
	busySrv := httptest.NewServer(server.NewStreamableHTTPServer(busy))
	t.Cleanup(busySrv.Close)

	hang := make(chan struct{})
	listing := make(chan struct{}, 1)
	slowToList := server.NewStreamableHTTPServer(server.NewMCPServer("slow-to-list", "1.0.0"))
	hanging := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(mcpgo.HeaderMethod) == string(mcpgo.MethodToolsList) {
			select {
			case listing <- struct{}{}:
			default:
			}
			<-hang
			return
		}
		slowToList.ServeHTTP(w, r)
	}))
	t.Cleanup(hanging.Close)
	pingURL := pingServer(t)

	mgr := NewManager(slog.Default())
	t.Cleanup(mgr.Stop)
	t.Cleanup(func() {
		close(release)
		close(hang)
	})

	busyCfg := canConnectConfig(t, connTypeStreamableHTTP, busySrv.URL)
	if err := mgr.AddClient(busyCfg); err != nil {
		t.Fatalf("AddClient(busy): %v", err)
	}

	go func() { _, _ = mgr.ExecuteToolRaw(context.Background(), busyCfg.ClientID+"-slow", map[string]any{}) }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the slow call never reached the server")
	}

	pingCfg := canConnectConfig(t, connTypeStreamableHTTP, pingURL)
	hangingCfg := canConnectConfig(t, connTypeStreamableHTTP, hanging.URL)
	added := make(chan error, 1)
	go func() { added <- mgr.AddClient(pingCfg) }()
	select {
	case err := <-added:
		if err != nil {
			t.Fatalf("AddClient(ping): %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("AddClient waited for a tool call in flight on another server")
	}

	go func() { _ = mgr.AddClient(hangingCfg) }()
	select {
	case <-listing:
	case <-time.After(5 * time.Second):
		t.Fatal("the slow server was never asked for its tools")
	}
	called := make(chan error, 1)
	go func() {
		_, err := mgr.ExecuteToolRaw(context.Background(), busyCfg.ClientID+"-fast", map[string]any{})
		called <- err
	}()
	select {
	case err := <-called:
		if err != nil {
			t.Fatalf("fast call: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a tool call waited for another server to connect")
	}
}

func TestConcurrentAddClientsForOneServerShareTheClient(t *testing.T) {
	s := server.NewMCPServer("slow-start", "1.0.0")
	s.AddTool(mcpgo.NewTool("ping"), func(context.Context, mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		return mcpgo.NewToolResultText("pong"), nil
	})
	mcpHandler := server.NewStreamableHTTPServer(s)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond)
		mcpHandler.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)

	mgr := NewManager(slog.Default())
	t.Cleanup(mgr.Stop)
	cfg := canConnectConfig(t, connTypeStreamableHTTP, srv.URL)

	errs := make(chan error, 2)
	for range 2 {
		go func() { errs <- mgr.AddClient(cfg) }()
	}
	for range 2 {
		if err := <-errs; err != nil {
			t.Fatalf("AddClient: %v", err)
		}
	}
	mgr.mu.RLock()
	defer mgr.mu.RUnlock()
	if shared := mgr.clients[cfg.ClientID]; shared == nil || shared.entries != 2 {
		t.Fatalf("client = %+v, want one client shared by both entries", shared)
	}
}

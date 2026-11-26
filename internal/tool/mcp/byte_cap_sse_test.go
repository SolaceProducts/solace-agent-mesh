// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package mcp

import (
	"context"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	mcpgo "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func TestByteCap_ManifestPath_SSE_RaisedCapAboveTransportDefaultBound(t *testing.T) {
	mcpServer := server.NewMCPServer("large", "1.0.0")
	mcpServer.AddTool(mcpgo.NewTool("ping"), func(context.Context, mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		return mcpgo.NewToolResultText(strings.Repeat("a", aboveTransportDefaultBound)), nil
	})
	sse := httptest.NewUnstartedServer(nil)
	sse.Config.Handler = server.NewSSEServer(mcpServer, server.WithBaseURL("http://"+sse.Listener.Addr().String()))
	sse.Start()
	t.Cleanup(sse.Close)

	cfg := &ToolsetConfig{
		ConnectionType:   connTypeSSE,
		URL:              sse.URL + "/sse",
		ClientID:         "fake_mcp_sse_manifest_above_transport_bound",
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

	res, err := mgr.ExecuteToolRaw(t.Context(), cfg.ClientID+"-ping", map[string]any{})
	if err != nil {
		t.Fatalf("ExecuteToolRaw: %v", err)
	}
	if res == nil || len(res.Content) != 1 {
		t.Fatalf("unexpected result: %+v", res)
	}
}

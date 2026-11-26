// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package mcp

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	mcpgo "github.com/mark3labs/mcp-go/mcp"
)

type slowMCPServer struct {
	initializeDelay time.Duration
	listToolsDelay  time.Duration
}

func (s *slowMCPServer) handle(w http.ResponseWriter, r *http.Request) {
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

	switch req.Method {
	case "initialize":
		time.Sleep(s.initializeDelay)
	case "tools/list":
		time.Sleep(s.listToolsDelay)
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	switch req.Method {
	case "initialize":
		writeJSONRPC(w, req.ID, mcpgo.InitializeResult{
			ProtocolVersion: mcpgo.LATEST_LEGACY_PROTOCOL_VERSION,
			ServerInfo:      mcpgo.Implementation{Name: "slow-mock", Version: "1.0.0"},
			Capabilities:    mcpgo.ServerCapabilities{},
		})
	case "tools/list":
		writeJSONRPC(w, req.ID, mcpgo.ListToolsResult{
			Tools: []mcpgo.Tool{{
				Name:        "ping",
				Description: "test tool",
				InputSchema: mcpgo.ToolInputSchema{Type: "object"},
			}},
		})
	default:
		writeJSONRPC(w, req.ID, map[string]any{})
	}
}

func TestAddClient_BlocksOnSlowUpstream(t *testing.T) {
	const upstreamDelay = 5 * time.Second

	slow := &slowMCPServer{
		initializeDelay: upstreamDelay,
		listToolsDelay:  0,
	}
	srv := httptest.NewTLSServer(http.HandlerFunc(slow.handle))
	defer srv.Close()

	verify := false
	cfg := &ToolsetConfig{
		ConnectionType: connTypeStreamableHTTP,
		URL:            srv.URL,
		SSLVerify:      &verify,
		ClientID:       "slow_init_mcp",
	}

	mgr := NewManager(slog.Default())
	defer mgr.Stop()

	start := time.Now()
	err := mgr.AddClient(cfg)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("AddClient: %v", err)
	}

	t.Logf("AddClient elapsed=%s upstream initialize delay=%s", elapsed, upstreamDelay)

	if elapsed < upstreamDelay {
		t.Fatalf("AddClient returned in %s but upstream took %s — "+
			"expected the call to block for the full upstream duration", elapsed, upstreamDelay)
	}

	if elapsed > upstreamDelay*2 {
		t.Errorf("AddClient took %s, more than 2x the upstream delay %s — unexpected overhead", elapsed, upstreamDelay)
	}
}

func TestAddClient_BlocksAndPlatformRPCWouldTimeOut(t *testing.T) {
	const upstreamDelay = 6 * time.Second
	const platformRPCBudget = 4 * time.Second

	slow := &slowMCPServer{initializeDelay: upstreamDelay}
	srv := httptest.NewTLSServer(http.HandlerFunc(slow.handle))
	defer srv.Close()

	verify := false
	cfg := &ToolsetConfig{
		ConnectionType: connTypeStreamableHTTP,
		URL:            srv.URL,
		SSLVerify:      &verify,
		ClientID:       "platform_rpc_repro",
	}

	mgr := NewManager(slog.Default())
	defer mgr.Stop()

	platformDeadline := time.Now().Add(platformRPCBudget)
	platformRPCTimedOut := make(chan time.Duration, 1)
	go func() {
		time.Sleep(platformRPCBudget)
		platformRPCTimedOut <- time.Since(platformDeadline.Add(-platformRPCBudget))
	}()

	start := time.Now()
	err := mgr.AddClient(cfg)
	addClientElapsed := time.Since(start)

	platformElapsed := <-platformRPCTimedOut

	t.Logf("platform RPC fired its deadline at t=%s (budget=%s)",
		platformElapsed.Round(time.Millisecond), platformRPCBudget)
	t.Logf("AWE AddClient returned at t=%s (upstream delay=%s, err=%v)",
		addClientElapsed.Round(time.Millisecond), upstreamDelay, err)

	if err != nil {
		t.Fatalf("AddClient: %v (expected eventual success)", err)
	}

	if addClientElapsed <= platformRPCBudget {
		t.Fatalf("AddClient returned in %s — but the bug requires AddClient > platform RPC budget (%s). "+
			"Either upstream is too fast or the test setup is off.",
			addClientElapsed, platformRPCBudget)
	}

	t.Logf("BUG CONFIRMED: AddClient took %s; the platform RPC at %s would already have returned "+
		"context deadline exceeded → deploy_failed in DB, but the agent eventually started in AWE.",
		addClientElapsed.Round(time.Millisecond), platformRPCBudget)
}

func TestAddClient_ManifestPath_DoesNotBlock(t *testing.T) {
	slow := &slowMCPServer{
		initializeDelay: 10 * time.Second,
		listToolsDelay:  10 * time.Second,
	}
	srv := httptest.NewServer(http.HandlerFunc(slow.handle))
	defer srv.Close()

	cfg := &ToolsetConfig{
		ConnectionType: connTypeSSE,
		URL:            srv.URL,
		ClientID:       "slow_with_manifest",
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

	start := time.Now()
	err := mgr.AddClient(cfg)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("AddClient: %v", err)
	}

	t.Logf("AddClient (manifest) elapsed=%s", elapsed)

	if elapsed > 500*time.Millisecond {
		t.Fatalf("manifest AddClient took %s, expected <500ms — manifest path should not block on the network", elapsed)
	}
}

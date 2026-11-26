// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package mcp

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	mcpclient "github.com/mark3labs/mcp-go/client"
	bifrostMCP "github.com/maximhq/bifrost/core/mcp"
	"github.com/maximhq/bifrost/core/schemas"
)

type scriptedBifrost struct {
	bifrostMCP.MCPManagerInterface
	onReconnect func(s *scriptedBifrost) error

	mu         sync.Mutex
	conn       *mcpclient.Client
	reconnects int
}

func (s *scriptedBifrost) GetClients() []schemas.MCPClientState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return []schemas.MCPClientState{{
		Name:            "c1",
		ExecutionConfig: &schemas.MCPClientConfig{ID: "c1"},
		Conn:            s.conn,
	}}
}

func (s *scriptedBifrost) GetToolsForClient(_ context.Context, clientID string) map[string]schemas.ChatTool {
	if clientID != "c1" {
		return nil
	}
	return map[string]schemas.ChatTool{"c1-ping": {}}
}

func (s *scriptedBifrost) ReconnectClient(string) error {
	s.mu.Lock()
	s.reconnects++
	s.mu.Unlock()
	return s.onReconnect(s)
}

func (s *scriptedBifrost) setConn(c *mcpclient.Client) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.conn = c
}

func (s *scriptedBifrost) reconnectCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reconnects
}

func managerOver(sb *scriptedBifrost) *Manager {
	return &Manager{bifrost: sb, direct: map[string]*directClient{}, logger: slog.Default()}
}

func TestReconnect_CallerDeadlineDoesNotWaitForAStuckAttempt(t *testing.T) {
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	mgr := managerOver(&scriptedBifrost{onReconnect: func(*scriptedBifrost) error {
		<-release
		return errors.New("unreachable")
	}})

	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err := mgr.reconnectBifrostClient(ctx, mgr.bifrost, "c1", nil)

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want the caller's own deadline", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("caller waited %s on an attempt that outlived its deadline", elapsed)
	}
}

func TestReconnect_WaitsForAReconnectBifrostIsAlreadyRunning(t *testing.T) {
	replacement := new(mcpclient.Client)
	mgr := managerOver(&scriptedBifrost{onReconnect: func(s *scriptedBifrost) error {
		go func() {
			time.Sleep(150 * time.Millisecond)
			s.setConn(replacement)
		}()
		return errors.New("reconnect already in progress for this client")
	}})

	got, err := mgr.reconnectBifrostClient(t.Context(), mgr.bifrost, "c1", nil)
	if err != nil || got != replacement {
		t.Fatalf("got %p, %v; want the connection the in-progress reconnect produced", got, err)
	}
}

func TestReconnect_ReportsAReconnectThatLeavesNoConnection(t *testing.T) {
	mgr := managerOver(&scriptedBifrost{onReconnect: func(*scriptedBifrost) error { return nil }})

	_, err := mgr.reconnectBifrostClient(t.Context(), mgr.bifrost, "c1", nil)
	if err == nil || !strings.Contains(err.Error(), "no connection after reconnect") {
		t.Fatalf("err = %v, want a missing-connection error", err)
	}
}

func TestReconnect_ReusesAConnectionReplacedMeanwhile(t *testing.T) {
	failed, replacement := new(mcpclient.Client), new(mcpclient.Client)
	sb := &scriptedBifrost{conn: replacement, onReconnect: func(*scriptedBifrost) error {
		t.Error("reconnected although the failed connection had already been replaced")
		return nil
	}}
	mgr := managerOver(sb)

	got, err := mgr.reconnectOnce(mgr.bifrost, "c1", failed)
	if err != nil || got != replacement {
		t.Fatalf("got %p, %v; want the existing replacement", got, err)
	}
	if n := sb.reconnectCount(); n != 0 {
		t.Fatalf("ReconnectClient called %d times, want 0", n)
	}
}

func TestReconnect_UnknownClientSurfacesBifrostsError(t *testing.T) {
	mgr := managerOver(&scriptedBifrost{onReconnect: func(*scriptedBifrost) error {
		return errors.New("client other not found")
	}})

	if _, err := mgr.reconnectBifrostClient(t.Context(), mgr.bifrost, "other", nil); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("err = %v, want Bifrost's not-found error", err)
	}
}

func TestExecuteTool_NoConnectionAndRecoveryFails(t *testing.T) {
	mgr := managerOver(&scriptedBifrost{onReconnect: func(*scriptedBifrost) error {
		return errors.New("dial: connection refused")
	}})

	_, err := mgr.ExecuteToolRawWithHeaders(t.Context(), "c1-ping", map[string]any{}, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "has no connection") || !strings.Contains(err.Error(), "connection refused") {
		t.Fatalf("err = %v, want a no-connection error carrying the recovery failure", err)
	}
}

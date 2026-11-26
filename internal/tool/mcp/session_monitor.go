// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package mcp

import (
	"context"
	"errors"
	"time"

	mcpclient "github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	mcpgo "github.com/mark3labs/mcp-go/mcp"
	bifrostMCP "github.com/maximhq/bifrost/core/mcp"
	"github.com/maximhq/bifrost/core/schemas"
)

var sessionHealthCheck = struct {
	interval time.Duration
	timeout  time.Duration
	failures int
}{interval: 10 * time.Second, timeout: 5 * time.Second, failures: 5}

func (m *Manager) startSessionMonitor() {
	ctx, cancel := context.WithCancel(context.Background())
	m.stopMonitor = cancel
	m.monitorDone.Go(func() { m.monitorSessions(ctx) })
}

func (m *Manager) stopSessionMonitor() {
	if m.stopMonitor == nil {
		return
	}
	m.stopMonitor()
	m.monitorDone.Wait()
}

func (m *Manager) monitorSessions(ctx context.Context) {
	ticker := time.NewTicker(sessionHealthCheck.interval)
	defer ticker.Stop()
	failedPings := map[string]int{}
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.checkSessions(ctx, failedPings)
		}
	}
}

func (m *Manager) checkSessions(ctx context.Context, failedPings map[string]int) {
	b, clients := m.liveBifrostClients()
	for _, c := range clients {
		id := c.ExecutionConfig.ID
		probeCtx, cancel := context.WithTimeout(ctx, sessionHealthCheck.timeout)
		err := probeSession(probeCtx, c.Conn)
		cancel()
		if ctx.Err() != nil {
			return
		}
		if err == nil {
			delete(failedPings, id)
			continue
		}
		failedPings[id]++
		gone := connectionGone(err)
		if failedPings[id] < sessionHealthCheck.failures && !gone {
			continue
		}
		delete(failedPings, id)
		reason := "unresponsive"
		if gone {
			reason = "connection_gone"
		}
		m.logger.Warn("MCP session unresponsive; reconnecting", "clientID", id, "reason", reason, "error", err)
		if _, err := m.reconnectBifrostClient(ctx, b, id, c.Conn); err != nil {
			m.logger.Warn("MCP session reconnect failed", "clientID", id, "error", err)
		}
	}
}

func probeSession(ctx context.Context, conn *mcpclient.Client) error {
	if mcpgo.IsModernProtocol(conn.ProtocolVersion()) {
		_, err := conn.Discover(ctx, mcpgo.DiscoverRequest{})
		return err
	}
	//nolint:staticcheck // ping is deprecated only for protocol 2026-07-28, which takes the branch above.
	return conn.Ping(ctx)
}

func connectionGone(err error) bool {
	return errors.Is(err, transport.ErrTransportClosed) || errors.Is(err, transport.ErrSessionTerminated)
}

func (m *Manager) liveBifrostClients() (bifrostMCP.MCPManagerInterface, []schemas.MCPClientState) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.bifrost == nil {
		return nil, nil
	}
	var live []schemas.MCPClientState
	for _, c := range m.bifrost.GetClients() {
		if c.ExecutionConfig != nil && c.Conn != nil && c.State != schemas.MCPConnectionStateDisabled {
			live = append(live, c)
		}
	}
	return m.bifrost, live
}

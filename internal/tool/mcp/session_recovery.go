// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package mcp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"syscall"
	"time"

	mcpclient "github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	mcpgo "github.com/mark3labs/mcp-go/mcp"
	bifrostMCP "github.com/maximhq/bifrost/core/mcp"
)

type sessionFailure int

const (
	sessionAlive sessionFailure = iota
	sessionLostBeforeSend
	sessionLostAfterSend
)

const (
	sseTransportClosedMsg  = "transport has been closed"
	sseConnectionClosedMsg = "connection has been closed"
)

var errSessionMissing = errors.New("MCP server reports no valid session")

const (
	typeScriptSessionErrorCode = -32000
	plainTextBadRequestPrefix  = "request failed with status 400:"
)

func rejectsMissingSession(tr transport.Interface, reqID mcpgo.RequestId, resp *transport.JSONRPCResponse) bool {
	if !carriesSessionID(tr) || resp.ID.String() == reqID.String() {
		return false
	}
	code := resp.Error.Code
	return (code == mcpgo.INVALID_REQUEST || code == typeScriptSessionErrorCode) && mentionsSession(resp.Error.Message)
}

func rejectsMissingSessionAsText(tr transport.Interface, err error) bool {
	msg := err.Error()
	return carriesSessionID(tr) && strings.HasPrefix(msg, plainTextBadRequestPrefix) && mentionsSession(msg)
}

func carriesSessionID(tr transport.Interface) bool {
	_, ok := tr.(*transport.StreamableHTTP)
	return ok
}

func mentionsSession(s string) bool {
	return strings.Contains(strings.ToLower(s), "session")
}

const bifrostReconnectInProgressMsg = "reconnect already in progress"

const (
	replacedConnPollInterval = 100 * time.Millisecond
	replacedConnMaxWait      = 30 * time.Second
	reconnectCooldown        = 5 * time.Second
)

type transportError struct{ err error }

func (e *transportError) Error() string { return e.err.Error() }
func (e *transportError) Unwrap() error { return e.err }

func classifySessionFailure(err error) sessionFailure {
	te, ok := errors.AsType[*transportError](err)
	if !ok {
		return sessionAlive
	}
	switch {
	case errors.Is(te, syscall.EPIPE),
		errors.Is(te, io.ErrClosedPipe),
		errors.Is(te, os.ErrClosed),
		errors.Is(te, transport.ErrSessionTerminated),
		errors.Is(te, errSessionMissing),
		te.err.Error() == sseTransportClosedMsg:
		return sessionLostBeforeSend
	case errors.Is(te, io.EOF),
		errors.Is(te, io.ErrUnexpectedEOF),
		errors.Is(te, transport.ErrTransportClosed),
		te.err.Error() == sseConnectionClosedMsg:
		return sessionLostAfterSend
	}
	return sessionAlive
}

type failedReconnect struct {
	at  time.Time
	err error
}

func (m *Manager) reconnectBifrostClient(ctx context.Context, b bifrostMCP.MCPManagerInterface, clientID string, failed *mcpclient.Client) (*mcpclient.Client, error) {
	if conn := bifrostConn(b, clientID); conn != nil && conn != failed {
		return conn, nil
	}
	if err := m.recentReconnectFailure(clientID); err != nil {
		return nil, err
	}
	flight := m.bifrostRecovery.DoChan(clientID, func() (any, error) {
		return m.reconnectOnce(b, clientID, failed)
	})
	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("waiting for MCP session recovery: %w", ctx.Err())
	case res := <-flight:
		if res.Err != nil {
			return nil, res.Err
		}
		return res.Val.(*mcpclient.Client), nil
	}
}

func (m *Manager) reconnectOnce(b bifrostMCP.MCPManagerInterface, clientID string, failed *mcpclient.Client) (*mcpclient.Client, error) {
	if conn := bifrostConn(b, clientID); conn != nil && conn != failed {
		return conn, nil
	}
	conn, err := reconnectNow(b, clientID, failed)
	m.recordReconnectOutcome(clientID, err)
	return conn, err
}

func reconnectNow(b bifrostMCP.MCPManagerInterface, clientID string, failed *mcpclient.Client) (*mcpclient.Client, error) {
	err := b.ReconnectClient(clientID)
	switch {
	case err == nil:
		if conn := bifrostConn(b, clientID); conn != nil {
			return conn, nil
		}
		return nil, errors.New("no connection after reconnect")
	case strings.Contains(err.Error(), bifrostReconnectInProgressMsg):
		return awaitReplacedConn(b, clientID, failed)
	default:
		return nil, err
	}
}

func (m *Manager) recentReconnectFailure(clientID string) error {
	m.recoveryMu.Lock()
	defer m.recoveryMu.Unlock()
	last, ok := m.failedReconnects[clientID]
	if !ok || time.Since(last.at) >= reconnectCooldown {
		return nil
	}
	return fmt.Errorf("MCP session recovery failed %s ago, next attempt after cool-down: %w",
		time.Since(last.at).Round(time.Millisecond), last.err)
}

func (m *Manager) recordReconnectOutcome(clientID string, err error) {
	m.recoveryMu.Lock()
	defer m.recoveryMu.Unlock()
	if err == nil {
		delete(m.failedReconnects, clientID)
		return
	}
	if m.failedReconnects == nil {
		m.failedReconnects = make(map[string]failedReconnect)
	}
	m.failedReconnects[clientID] = failedReconnect{at: time.Now(), err: err}
}

func bifrostConn(b bifrostMCP.MCPManagerInterface, clientID string) *mcpclient.Client {
	for _, c := range b.GetClients() {
		if c.ExecutionConfig != nil && c.ExecutionConfig.ID == clientID {
			return c.Conn
		}
	}
	return nil
}

func awaitReplacedConn(b bifrostMCP.MCPManagerInterface, clientID string, failed *mcpclient.Client) (*mcpclient.Client, error) {
	deadline := time.NewTimer(replacedConnMaxWait)
	defer deadline.Stop()
	ticker := time.NewTicker(replacedConnPollInterval)
	defer ticker.Stop()
	for {
		if conn := bifrostConn(b, clientID); conn != nil && conn != failed {
			return conn, nil
		}
		select {
		case <-deadline.C:
			return nil, errors.New("timed out waiting for an in-progress reconnect")
		case <-ticker.C:
		}
	}
}

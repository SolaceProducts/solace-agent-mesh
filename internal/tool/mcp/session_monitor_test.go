// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package mcp

import (
	"bytes"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	mcpgo "github.com/mark3labs/mcp-go/mcp"
)

func useFastSessionMonitor(t *testing.T) {
	t.Helper()
	saved := sessionHealthCheck
	sessionHealthCheck.interval = 50 * time.Millisecond
	sessionHealthCheck.timeout = time.Second
	sessionHealthCheck.failures = 2
	t.Cleanup(func() { sessionHealthCheck = saved })
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestSessionMonitorRebuildsADeadStdioSessionWithoutACall(t *testing.T) {
	useFastSessionMonitor(t)
	mgr, marker := newDyingStdioClient(t)
	first := callWhoami(t, mgr)
	before := bifrostConn(mgr.bifrost, "stdio_recover")
	stopOneShotServer(t, marker)

	eventually(t, "the monitor to replace the dead connection", func() bool {
		conn := bifrostConn(mgr.bifrost, "stdio_recover")
		return conn != nil && conn != before
	})

	if second := callWhoami(t, mgr); second == first {
		t.Fatalf("call answered by %q, the process that exited", second)
	}
}

type sessionfulMCPServer struct {
	fake fakeMCPServer

	mu      sync.Mutex
	session string
	issued  int
}

const forgottenSessionBody = `{"jsonrpc":"2.0","error":{"code":-32000,"message":"Bad Request: No valid session ID provided"},"id":null}`

func (s *sessionfulMCPServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "only POST", http.StatusMethodNotAllowed)
		return
	}
	body, _ := io.ReadAll(r.Body)
	r.Body = io.NopCloser(bytes.NewReader(body))

	s.mu.Lock()
	if bytes.Contains(body, []byte(`"method":"initialize"`)) {
		s.issued++
		s.session = fmt.Sprintf("session-%d", s.issued)
		w.Header().Set("Mcp-Session-Id", s.session)
		s.mu.Unlock()
		s.fake.handle(w, r)
		return
	}
	valid := s.session != "" && r.Header.Get("Mcp-Session-Id") == s.session
	s.mu.Unlock()

	if !valid {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, forgottenSessionBody)
		return
	}
	s.fake.handle(w, r)
}

func (s *sessionfulMCPServer) forgetSessions() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.session = ""
}

type flakyDiscoverServer struct {
	next http.Handler

	mu       sync.Mutex
	refusals int
}

func (s *flakyDiscoverServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	r.Body = io.NopCloser(bytes.NewReader(body))
	if bytes.Contains(body, []byte(`"method":"`+string(mcpgo.MethodServerDiscover)+`"`)) {
		s.mu.Lock()
		refuse := s.refusals > 0
		if refuse {
			s.refusals--
		}
		s.mu.Unlock()
		if refuse {
			http.Error(w, "restarting", http.StatusServiceUnavailable)
			return
		}
	}
	s.next.ServeHTTP(w, r)
}

func (s *flakyDiscoverServer) refuseNext(n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refusals = n
}

func TestSessionMonitorRebuildsAModernConnectionTheServerStoppedAnswering(t *testing.T) {
	useFastSessionMonitor(t)
	ts, _ := newTenantEchoServerRecordingClientNames(t, false)
	flaky := &flakyDiscoverServer{next: ts.Config.Handler}
	srv := httptest.NewServer(flaky)
	t.Cleanup(srv.Close)
	cfg := sharedEntryConfig(t, srv.URL, entryFilter{})

	mgr := NewManager(slog.Default())
	t.Cleanup(mgr.Stop)
	registerEntries(t, mgr, []*ToolsetConfig{cfg})
	if got := mgr.ProtocolVersion(cfg); got != mcpgo.LATEST_PROTOCOL_VERSION {
		t.Fatalf("negotiated protocol version %q, want %q", got, mcpgo.LATEST_PROTOCOL_VERSION)
	}
	before := bifrostConn(mgr.bifrost, cfg.ClientID)

	flaky.refuseNext(sessionHealthCheck.failures)

	eventually(t, "the monitor to rebuild the connection", func() bool {
		conn := bifrostConn(mgr.bifrost, cfg.ClientID)
		return conn != nil && conn != before
	})
	whoamiAnswer(t, mgr, cfg)
}

func TestSessionMonitorRebuildsAnHTTPSessionTheServerForgot(t *testing.T) {
	useFastSessionMonitor(t)
	server := &sessionfulMCPServer{fake: fakeMCPServer{advertisedTool: mcpgo.Tool{
		Name:        "ping",
		InputSchema: mcpgo.ToolInputSchema{Type: "object"},
	}}}
	srv := httptest.NewServer(server)
	t.Cleanup(srv.Close)

	mgr := NewManager(slog.Default())
	t.Cleanup(mgr.Stop)
	cfg := &ToolsetConfig{ConnectionType: connTypeStreamableHTTP, URL: srv.URL, ClientID: "http_forgot"}
	if err := mgr.AddClient(cfg); err != nil {
		t.Fatalf("AddClient: %v", err)
	}
	call := func() error {
		_, err := mgr.ExecuteToolRawWithHeaders(t.Context(), "http_forgot-ping", map[string]any{}, nil, nil)
		return err
	}
	if err := call(); err != nil {
		t.Fatalf("call before the restart: %v", err)
	}

	server.forgetSessions()

	eventually(t, "the monitor to rebuild the forgotten session", func() bool { return call() == nil })
}

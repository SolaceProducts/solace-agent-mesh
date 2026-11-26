// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/client/transport"
	mcpgo "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	bifrostMCP "github.com/maximhq/bifrost/core/mcp"
)

const (
	oneShotStdioServerEnv = "SAM_MCP_TEST_ONE_SHOT_STDIO_SERVER"
	oneShotExitMarkerEnv  = "SAM_MCP_TEST_EXIT_MARKER"
	goRaceNoAtexitSleep   = "atexit_sleep_ms=0"
)

func TestMain(m *testing.M) {
	if os.Getenv(oneShotStdioServerEnv) != "" {
		serveOneShotStdio()
		return
	}
	if os.Getenv(echoStdioServerEnv) != "" {
		serveEchoStdio()
		return
	}
	os.Exit(m.Run())
}

func serveOneShotStdio() {
	marker := os.Getenv(oneShotExitMarkerEnv)
	var exitOnce sync.Once
	s := server.NewMCPServer("one-shot", "1.0.0")
	s.AddTool(mcpgo.NewTool("whoami", mcpgo.WithDescription("reports the serving process")),
		func(context.Context, mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
			if _, err := os.Stat(marker); err != nil {
				exitOnce.Do(func() { go exitWhenSignalled(marker) })
			}
			return mcpgo.NewToolResultText(fmt.Sprintf("pid %d", os.Getpid())), nil
		})
	_ = server.ServeStdio(s)
}

func exitWhenSignalled(marker string) {
	for {
		if _, err := os.Stat(exitSignalPath(marker)); err == nil {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	_ = os.Stdout.Close()
	_ = os.WriteFile(marker, nil, 0o600)
	os.Exit(0)
}

func exitSignalPath(marker string) string {
	return marker + ".signal"
}

func whoami(ctx context.Context, mgr *Manager) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	result, err := mgr.ExecuteToolRawWithHeaders(ctx, "stdio_recover-whoami", map[string]any{}, nil, nil)
	if err != nil {
		return "", fmt.Errorf("tool call: %w", err)
	}
	var block struct {
		Text string `json:"text"`
	}
	if len(result.Content) == 0 || json.Unmarshal(result.Content[0], &block) != nil {
		return "", fmt.Errorf("unexpected result content: %+v", result)
	}
	return block.Text, nil
}

func callWhoami(t *testing.T, mgr *Manager) string {
	t.Helper()
	answer, err := whoami(t.Context(), mgr)
	if err != nil {
		t.Fatal(err)
	}
	return answer
}

func newDyingStdioClient(t *testing.T) (*Manager, string) {
	t.Helper()
	return newDyingStdioClientVia(t, os.Args[0])
}

func newDyingStdioClientVia(t *testing.T, command string) (*Manager, string) {
	t.Helper()
	marker := filepath.Join(t.TempDir(), "exited")
	cfg := &ToolsetConfig{
		ConnectionType: connTypeStdio,
		Command:        command,
		CommandArgs:    []string{"-test.run=^$"},
		EnvVars: map[string]string{
			oneShotStdioServerEnv: "1",
			oneShotExitMarkerEnv:  marker,
			"GORACE":              goRaceNoAtexitSleep,
		},
		ClientID: "stdio_recover",
	}
	mgr := NewManager(slog.Default())
	t.Cleanup(mgr.Stop)
	if err := mgr.AddClient(cfg); err != nil {
		t.Fatalf("AddClient: %v", err)
	}
	return mgr, marker
}

func TestBifrostStdioClientRecoversAfterServerExits(t *testing.T) {
	mgr, marker := newDyingStdioClient(t)

	first := callWhoami(t, mgr)
	stopOneShotServer(t, marker)

	answer, err := whoami(t.Context(), mgr)
	if err == nil && answer == first {
		t.Fatalf("call answered by %q, the process that exited", answer)
	}
	if err != nil && classifySessionFailure(err) == sessionAlive {
		t.Fatalf("first call after the exit failed with %v, want a lost session", err)
	}
	if next := callWhoami(t, mgr); next == first {
		t.Fatalf("next call answered by %q, the process that exited; want a reconnected one", next)
	}
}

func TestBifrostStdioConcurrentCallsShareOneRecovery(t *testing.T) {
	mgr, marker := newDyingStdioClient(t)
	first := callWhoami(t, mgr)
	stopOneShotServer(t, marker)

	const calls = 8
	concurrently := func() ([]string, []error) {
		answers := make([]string, calls)
		errs := make([]error, calls)
		var wg sync.WaitGroup
		for i := range calls {
			wg.Go(func() { answers[i], errs[i] = whoami(t.Context(), mgr) })
		}
		wg.Wait()
		return answers, errs
	}
	acrossExit, exitErrs := concurrently()
	after, afterErrs := concurrently()

	if err := errors.Join(afterErrs...); err != nil {
		t.Fatalf("calls after the recovery failed: %v", err)
	}
	for i, a := range acrossExit {
		if exitErrs[i] == nil {
			after = append(after, a)
		}
	}
	for _, a := range after {
		if a == first {
			t.Fatalf("a call was answered by %q, the process that exited", a)
		}
		if a != after[0] {
			t.Fatalf("calls answered by %q and %q: concurrent recovery rebuilt the connection more than once", after[0], a)
		}
	}
}

func stopOneShotServer(t *testing.T, marker string) {
	t.Helper()
	if err := os.WriteFile(exitSignalPath(marker), nil, 0o600); err != nil {
		t.Fatalf("write exit signal %s: %v", exitSignalPath(marker), err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(marker); err == nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("one-shot stdio server never exited")
}

func TestClassifySessionFailure(t *testing.T) {
	for name, tc := range map[string]struct {
		err  error
		want sessionFailure
	}{
		"success":    {nil, sessionAlive},
		"tool error": {errors.New("MCP tool \"x\" RPC error: code=-32602 bad args"), sessionAlive},
		"rpc error naming a transport failure": {
			errors.New("MCP tool \"x\" RPC error: code=-32000 upstream: transport has been closed; connection has been closed"),
			sessionAlive,
		},
		"http body naming a transport failure": {
			transportFailure(errors.New("request failed with status 502: transport has been closed")),
			sessionAlive,
		},
		"stdio write to dead proc": {transportFailure(fmt.Errorf("failed to write request: %w", syscall.EPIPE)), sessionLostBeforeSend},
		"closed pipe":              {transportFailure(fmt.Errorf("write: %w", io.ErrClosedPipe)), sessionLostBeforeSend},
		"http session dropped":     {transportFailure(fmt.Errorf("call: %w", transport.ErrSessionTerminated)), sessionLostBeforeSend},
		"sse closed before send":   {transportFailure(errors.New("transport has been closed")), sessionLostBeforeSend},
		"no valid session id":      {transportFailure(fmt.Errorf("%w: No valid session ID provided", errSessionMissing)), sessionLostBeforeSend},
		"stream ended mid-call":    {transportFailure(fmt.Errorf("read: %w", io.ErrUnexpectedEOF)), sessionLostAfterSend},
		"sse closed after send":    {transportFailure(errors.New("connection has been closed")), sessionLostAfterSend},
		"stdio transport closed":   {transportFailure(transport.ErrTransportClosed), sessionLostAfterSend},
		"deadline":                 {transportFailure(context.DeadlineExceeded), sessionAlive},
	} {
		if got := classifySessionFailure(tc.err); got != tc.want {
			t.Errorf("%s: classifySessionFailure = %d, want %d", name, got, tc.want)
		}
	}
}

func transportFailure(err error) error {
	return fmt.Errorf("MCP tool %q call failed: %w", "x", &transportError{err: err})
}

func TestBifrostStdioServerThatStaysDownFailsFast(t *testing.T) {
	saved := bifrostMCP.ConnectRetryConfig
	bifrostMCP.ConnectRetryConfig = bifrostMCP.RetryConfig{InitialBackoff: time.Millisecond, MaxBackoff: time.Millisecond}
	t.Cleanup(func() { bifrostMCP.ConnectRetryConfig = saved })

	launcher := filepath.Join(t.TempDir(), "mcp-server")
	if err := os.Symlink(os.Args[0], launcher); err != nil {
		t.Skipf("symlink: %v", err)
	}
	mgr, marker := newDyingStdioClientVia(t, launcher)
	callWhoami(t, mgr)
	stopOneShotServer(t, marker)
	if err := os.Remove(launcher); err != nil {
		t.Fatal(err)
	}

	const calls = 6
	const deadline = 5 * time.Second
	errs := make([]error, calls)
	started := time.Now()
	var wg sync.WaitGroup
	for i := range calls {
		wg.Go(func() {
			ctx, cancel := context.WithTimeout(t.Context(), deadline)
			defer cancel()
			_, errs[i] = whoami(ctx, mgr)
		})
	}
	wg.Wait()
	if elapsed := time.Since(started); elapsed > deadline {
		t.Fatalf("calls to a server that stays down took %s, past their %s deadline", elapsed, deadline)
	}
	for i, err := range errs {
		if err == nil {
			t.Fatalf("call %d succeeded against a server that cannot start", i)
		}
	}

	started = time.Now()
	_, err := whoami(t.Context(), mgr)
	if err == nil || !strings.Contains(err.Error(), "cool-down") {
		t.Fatalf("call inside the cool-down: err = %v, want a fail-fast cool-down error", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("call inside the cool-down took %s; it should fail without a new attempt", elapsed)
	}
}

func TestBifrostHTTPCallDroppedAfterSendIsNotReplayed(t *testing.T) {
	var toolCalls atomic.Int32
	fake := &fakeMCPServer{advertisedTool: mcpgo.Tool{
		Name:        "ping",
		InputSchema: mcpgo.ToolInputSchema{Type: "object"},
	}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewReader(body))
		if bytes.Contains(body, []byte(`"tools/call"`)) && toolCalls.Add(1) == 1 {
			conn, _, err := w.(http.Hijacker).Hijack()
			if err == nil {
				_ = conn.Close()
			}
			return
		}
		fake.handle(w, r)
	}))
	t.Cleanup(srv.Close)

	mgr := NewManager(slog.Default())
	t.Cleanup(mgr.Stop)
	cfg := &ToolsetConfig{ConnectionType: connTypeStreamableHTTP, URL: srv.URL, ClientID: "http_drop"}
	if err := mgr.AddClient(cfg); err != nil {
		t.Fatalf("AddClient: %v", err)
	}

	_, err := mgr.ExecuteToolRawWithHeaders(t.Context(), "http_drop-ping", map[string]any{}, nil, nil)
	if err == nil {
		t.Fatal("call whose connection dropped after the request was sent succeeded")
	}
	if got := toolCalls.Load(); got != 1 {
		t.Fatalf("server saw %d tools/call requests; a call that may have run must not be replayed", got)
	}

	if _, err := mgr.ExecuteToolRawWithHeaders(t.Context(), "http_drop-ping", map[string]any{}, nil, nil); err != nil {
		t.Fatalf("next call after the drop: %v", err)
	}
}

type rejection func(w http.ResponseWriter, callID json.RawMessage)

func jsonRPCRejection(status, code int, message string, id any) rejection {
	return func(w http.ResponseWriter, _ json.RawMessage) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0",
			"id":      id,
			"error":   map[string]any{"code": code, "message": message},
		})
	}
}

func plainTextRejection(message string) rejection {
	return func(w http.ResponseWriter, _ json.RawMessage) {
		http.Error(w, message, http.StatusBadRequest)
	}
}

func echoingRejection(code int, message string) rejection {
	return func(w http.ResponseWriter, callID json.RawMessage) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0",
			"id":      callID,
			"error":   map[string]any{"code": code, "message": message},
		})
	}
}

func sessionTerminated(w http.ResponseWriter, _ json.RawMessage) {
	http.Error(w, "Session not found", http.StatusNotFound)
}

func droppedAfterSend(w http.ResponseWriter, _ json.RawMessage) {
	conn, _, err := w.(http.Hijacker).Hijack()
	if err == nil {
		_ = conn.Close()
	}
}

var missingSessionRejections = map[string]rejection{
	"python":                     jsonRPCRejection(http.StatusBadRequest, mcpgo.INVALID_REQUEST, "Bad Request: Missing session ID", nil),
	"python 1.x":                 jsonRPCRejection(http.StatusBadRequest, mcpgo.INVALID_REQUEST, "Bad Request: Missing session ID", "server-error"),
	"python 1.x unknown session": plainTextRejection("Bad Request: No valid session ID provided"),
	"typescript":                 jsonRPCRejection(http.StatusBadRequest, typeScriptSessionErrorCode, "Bad Request: Mcp-Session-Id header is required", nil),
	"typescript example server":  jsonRPCRejection(http.StatusBadRequest, typeScriptSessionErrorCode, "Bad Request: No valid session ID provided", nil),
	"mcp-go":                     plainTextRejection("Invalid session ID"),
}

type sessionRejectingServer struct {
	reject      rejection
	rejectCalls int32
	initializes atomic.Int32
	toolCalls   atomic.Int32
}

func newSessionRejectingServer(t *testing.T, reject rejection) (*httptest.Server, *sessionRejectingServer) {
	t.Helper()
	return newRejectingServer(t, reject, 1)
}

func newRejectingServer(t *testing.T, reject rejection, rejectCalls int32) (*httptest.Server, *sessionRejectingServer) {
	t.Helper()
	s := &sessionRejectingServer{reject: reject, rejectCalls: rejectCalls}
	fake := &fakeMCPServer{advertisedTool: mcpgo.Tool{
		Name:        "ping",
		InputSchema: mcpgo.ToolInputSchema{Type: "object"},
	}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewReader(body))
		switch {
		case bytes.Contains(body, []byte(`"initialize"`)):
			s.initializes.Add(1)
		case bytes.Contains(body, []byte(`"tools/call"`)) && s.toolCalls.Add(1) <= s.rejectCalls:
			var call struct {
				ID json.RawMessage `json:"id"`
			}
			_ = json.Unmarshal(body, &call)
			s.reject(w, call.ID)
			return
		}
		fake.handle(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv, s
}

func newBifrostHTTPClient(t *testing.T, url, clientID string) *Manager {
	t.Helper()
	mgr := NewManager(slog.Default())
	t.Cleanup(mgr.Stop)
	cfg := &ToolsetConfig{ConnectionType: connTypeStreamableHTTP, URL: url, ClientID: clientID}
	if err := mgr.AddClient(cfg); err != nil {
		t.Fatalf("AddClient: %v", err)
	}
	return mgr
}

func newDirectHTTPClient(t *testing.T, url, clientID string) *Manager {
	t.Helper()
	mgr := NewManager(slog.Default())
	t.Cleanup(mgr.Stop)
	cfg := &ToolsetConfig{
		ConnectionType: connTypeStreamableHTTP,
		URL:            url,
		ClientID:       clientID,
		Manifest:       []ManifestTool{{Name: "ping", InputSchema: map[string]any{"type": "object"}}},
	}
	if err := mgr.AddClient(cfg); err != nil {
		t.Fatalf("AddClient: %v", err)
	}
	return mgr
}

func TestBifrostHTTPCallWithoutSessionIsReplayedOnANewSession(t *testing.T) {
	for sdk, reject := range missingSessionRejections {
		t.Run(sdk, func(t *testing.T) {
			srv, server := newSessionRejectingServer(t, reject)
			mgr := newBifrostHTTPClient(t, srv.URL, "http_nosession")
			initializesBefore := server.initializes.Load()

			if _, err := mgr.ExecuteToolRawWithHeaders(t.Context(), "http_nosession-ping", map[string]any{}, nil, nil); err != nil {
				t.Fatalf("call the server rejected for a missing session: %v", err)
			}
			if got := server.initializes.Load() - initializesBefore; got != 1 {
				t.Fatalf("client initialized %d new sessions, want exactly one reconnect", got)
			}
			if got := server.toolCalls.Load(); got != 2 {
				t.Fatalf("server saw %d tools/call requests, want the rejected call and its replay", got)
			}
		})
	}
}

func TestBifrostHTTPCallToARestartedServerIsReplayedWithASession(t *testing.T) {
	fake := &fakeMCPServer{advertisedTool: mcpgo.Tool{Name: "ping", InputSchema: mcpgo.ToolInputSchema{Type: "object"}}}
	var (
		mu          sync.Mutex
		session     string
		issued      int
		sessionless []string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewReader(body))
		var req struct {
			Method string `json:"method"`
		}
		_ = json.Unmarshal(body, &req)
		sent := r.Header.Get("Mcp-Session-Id")

		mu.Lock()
		switch {
		case req.Method == string(mcpgo.MethodInitialize):
			issued++
			session = fmt.Sprintf("session-%d", issued)
			w.Header().Set("Mcp-Session-Id", session)
		case req.Method == string(mcpgo.MethodServerDiscover):
		case r.Method == http.MethodPost && sent == "":
			sessionless = append(sessionless, req.Method)
		case sent != session:
			mu.Unlock()
			http.Error(w, "session not found", http.StatusNotFound)
			return
		}
		mu.Unlock()
		fake.handle(w, r)
	}))
	t.Cleanup(srv.Close)
	mgr := newBifrostHTTPClient(t, srv.URL, "http_restarted")
	call := func() error {
		_, err := mgr.ExecuteToolRawWithHeaders(t.Context(), "http_restarted-ping", map[string]any{}, nil, nil)
		return err
	}
	if err := call(); err != nil {
		t.Fatalf("call before the restart: %v", err)
	}

	mu.Lock()
	session = ""
	mu.Unlock()

	if err := call(); err != nil {
		t.Fatalf("call after the restart: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(sessionless) != 0 {
		t.Errorf("requests %v reached the server without a session ID", sessionless)
	}
	if issued != 2 {
		t.Errorf("server issued %d sessions, want the first and one after the restart", issued)
	}
}

func TestBifrostHTTPErrorsFromTheToolAreNotReplayed(t *testing.T) {
	for name, reject := range map[string]rejection{
		"invalid request without session wording": jsonRPCRejection(http.StatusBadRequest, mcpgo.INVALID_REQUEST, "Invalid request: arguments must be an object", nil),
		"session wording answered with our id":    echoingRejection(mcpgo.INVALID_REQUEST, "Upstream session expired, sign in to the CRM again"),
	} {
		t.Run(name, func(t *testing.T) {
			srv, server := newSessionRejectingServer(t, reject)
			mgr := newBifrostHTTPClient(t, srv.URL, "http_invalid")
			initializesBefore := server.initializes.Load()

			_, err := mgr.ExecuteToolRawWithHeaders(t.Context(), "http_invalid-ping", map[string]any{}, nil, nil)
			if err == nil || !strings.Contains(err.Error(), "RPC error") {
				t.Fatalf("err = %v, want the tool's own RPC error", err)
			}
			if got := server.initializes.Load() - initializesBefore; got != 0 {
				t.Fatalf("client initialized %d new sessions over the tool's own error", got)
			}
			if got := server.toolCalls.Load(); got != 1 {
				t.Fatalf("server saw %d tools/call requests; the tool's own error must not be replayed", got)
			}
		})
	}
}

func TestDirectHTTPCallThatLostItsSessionIsReplayedOnANewSession(t *testing.T) {
	cases := map[string]rejection{"session terminated": sessionTerminated}
	for sdk, reject := range missingSessionRejections {
		cases[sdk] = reject
	}
	for name, reject := range cases {
		t.Run(name, func(t *testing.T) {
			srv, server := newSessionRejectingServer(t, reject)
			mgr := newDirectHTTPClient(t, srv.URL, "direct_nosession")

			if _, err := mgr.ExecuteToolRawWithHeaders(t.Context(), "direct_nosession-ping", map[string]any{}, nil, nil); err != nil {
				t.Fatalf("call that lost its session: %v", err)
			}
			if got := server.initializes.Load(); got != 2 {
				t.Fatalf("client initialized %d sessions, want the first and one after the rejection", got)
			}
			if got := server.toolCalls.Load(); got != 2 {
				t.Fatalf("server saw %d tools/call requests, want the rejected call and its replay", got)
			}
		})
	}
}

func TestDirectHTTPCallIsReplayedAtMostOnce(t *testing.T) {
	srv, server := newRejectingServer(t, missingSessionRejections["mcp-go"], 2)
	mgr := newDirectHTTPClient(t, srv.URL, "direct_rejected_twice")

	if _, err := mgr.ExecuteToolRawWithHeaders(t.Context(), "direct_rejected_twice-ping", map[string]any{}, nil, nil); err == nil {
		t.Fatal("call rejected on both sessions succeeded")
	}
	if got := server.toolCalls.Load(); got != 2 {
		t.Fatalf("server saw %d tools/call requests, want the call and a single replay", got)
	}

	if _, err := mgr.ExecuteToolRawWithHeaders(t.Context(), "direct_rejected_twice-ping", map[string]any{}, nil, nil); err != nil {
		t.Fatalf("next call after the rejections: %v", err)
	}
}

func TestDirectHTTPCallThatMayHaveRunIsNotReplayed(t *testing.T) {
	for name, reject := range map[string]rejection{
		"dropped after send":                   droppedAfterSend,
		"session wording answered with our id": echoingRejection(mcpgo.INVALID_REQUEST, "Upstream session expired, sign in to the CRM again"),
	} {
		t.Run(name, func(t *testing.T) {
			srv, server := newSessionRejectingServer(t, reject)
			mgr := newDirectHTTPClient(t, srv.URL, "direct_dispatched")

			if _, err := mgr.ExecuteToolRawWithHeaders(t.Context(), "direct_dispatched-ping", map[string]any{}, nil, nil); err == nil {
				t.Fatal("call the server failed succeeded")
			}
			if got := server.toolCalls.Load(); got != 1 {
				t.Fatalf("server saw %d tools/call requests; a call that may have run must not be replayed", got)
			}

			if _, err := mgr.ExecuteToolRawWithHeaders(t.Context(), "direct_dispatched-ping", map[string]any{}, nil, nil); err != nil {
				t.Fatalf("next call: %v", err)
			}
		})
	}
}

func TestDirectClientInvalidateLeavesAReplacedTransportAlone(t *testing.T) {
	srv, _ := newSessionRejectingServer(t, missingSessionRejections["mcp-go"])
	mgr := newDirectHTTPClient(t, srv.URL, "direct_replaced")
	dc := mgr.direct["direct_replaced"]

	staleConn, err := mgr.ensureConnected(t.Context(), dc, nil)
	if err != nil {
		t.Fatalf("first connect: %v", err)
	}
	stale := staleConn.GetTransport()
	dc.invalidate(stale)
	replacement, err := mgr.ensureConnected(t.Context(), dc, nil)
	if err != nil {
		t.Fatalf("reconnect: %v", err)
	}

	dc.invalidate(stale)

	dc.mu.Lock()
	current := dc.transport
	dc.mu.Unlock()
	if current != replacement.GetTransport() {
		t.Fatal("a late failure on the old transport tore down its replacement")
	}
}

// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package mcp

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/mcpauth"
)

func noAuthStreamAdapter() *Adapter {
	return &Adapter{
		cfg:    &Config{EnableAuth: false, DefaultUserIdentity: "alice@example.com"},
		logger: noopLogger(),
	}
}

func authEnabledStreamAdapter() *Adapter {
	a := &Adapter{
		cfg: &Config{
			EnableAuth:          true,
			DevMode:             true,
			DefaultUserIdentity: "alice@example.com",
			Issuer:              "https://mcp.example.com",
			Host:                "127.0.0.1",
			Port:                8090,
			RequirePKCE:         true,
		},
		logger: noopLogger(),
	}
	a.mcpauthHandler = mcpauth.New(mcpauth.Config{
		EnableAuth:     true,
		Issuer:         "https://mcp.example.com",
		Host:           "127.0.0.1",
		Port:           8090,
		RequirePKCE:    true,
		IDPClient:      fakeMcpauthIDPClient{},
		SAMTokenMinter: fakeMcpauthMinter{},
		AuthzResolver:  fakeMcpauthResolver{},
		Logger:         noopLogger(),
	})
	return a
}

func cacheControlDirectives(resp *http.Response) map[string]bool {
	directives := map[string]bool{}
	for directive := range strings.SplitSeq(strings.Join(resp.Header.Values("Cache-Control"), ","), ",") {
		directives[strings.TrimSpace(directive)] = true
	}
	return directives
}

func assertStreamPassesThroughProxies(t *testing.T, resp *http.Response) {
	t.Helper()
	if got := resp.Header.Get("Content-Type"); !strings.HasPrefix(got, "text/event-stream") {
		t.Fatalf("Content-Type = %q, want text/event-stream", got)
	}
	if got := resp.Header.Get("X-Accel-Buffering"); got != "no" {
		t.Errorf("X-Accel-Buffering = %q, want %q", got, "no")
	}
	directives := cacheControlDirectives(resp)
	for _, want := range []string{"no-cache", "no-transform"} {
		if !directives[want] {
			t.Errorf("Cache-Control = %q, want it to contain %s", resp.Header.Get("Cache-Control"), want)
		}
	}
}

func postMCPMessage(t *testing.T, url, sessionID, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if sessionID != "" {
		req.Header.Set("Mcp-Session-Id", sessionID)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

func initializeMCPSession(t *testing.T, url string) string {
	t.Helper()
	initialize := postMCPMessage(t, url, "", `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}`)
	sessionID := initialize.Header.Get("Mcp-Session-Id")
	if initialize.StatusCode != http.StatusOK || sessionID == "" {
		t.Fatalf("initialize: status = %d, session = %q", initialize.StatusCode, sessionID)
	}
	if got := initialize.Header.Get("X-Accel-Buffering"); got != "" {
		t.Errorf("JSON initialize response: X-Accel-Buffering = %q, want it unset", got)
	}
	initialized := postMCPMessage(t, url, sessionID, `{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	if initialized.StatusCode < 200 || initialized.StatusCode > 299 {
		t.Fatalf("notifications/initialized: status = %d, want 2xx", initialized.StatusCode)
	}
	return sessionID
}

func TestMCPNotificationStream_TellsProxiesNotToBufferOrTransform(t *testing.T) {
	t.Setenv("SAM_DEV_USER_ID", "")
	for _, tc := range []struct {
		name    string
		adapter *Adapter
	}{
		{"auth disabled", noAuthStreamAdapter()},
		{"auth enabled", authEnabledStreamAdapter()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mcpServer := server.NewMCPServer("test", "1.0.0", server.WithToolCapabilities(true))
			srv := httptest.NewServer(tc.adapter.assembleHTTPHandler(server.NewStreamableHTTPServer(mcpServer)))
			t.Cleanup(srv.Close)
			sessionID := initializeMCPSession(t, srv.URL)

			ctx, cancel := context.WithCancel(t.Context())
			t.Cleanup(cancel)
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Accept", "text/event-stream")
			req.Header.Set("Mcp-Session-Id", sessionID)
			stream, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = stream.Body.Close() })

			if stream.StatusCode != http.StatusOK {
				t.Fatalf("notification stream: status = %d, want 200", stream.StatusCode)
			}
			assertStreamPassesThroughProxies(t, stream)
		})
	}
}

func TestMCPToolCallUpgradedToStream_TellsProxiesNotToBufferOrTransform(t *testing.T) {
	mcpServer := server.NewMCPServer("test", "1.0.0", server.WithToolCapabilities(true))
	mcpServer.AddTool(mcplib.NewTool("report_progress"), func(ctx context.Context, _ mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
		if err := server.ServerFromContext(ctx).SendNotificationToClient(ctx, "notifications/message", map[string]any{"level": "info", "data": "working"}); err != nil {
			return nil, err
		}
		return mcplib.NewToolResultText("done"), nil
	})
	srv := httptest.NewServer(noAuthStreamAdapter().assembleHTTPHandler(server.NewStreamableHTTPServer(mcpServer)))
	t.Cleanup(srv.Close)
	sessionID := initializeMCPSession(t, srv.URL)

	call := postMCPMessage(t, srv.URL, sessionID, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"report_progress","arguments":{}}}`)

	if call.StatusCode != http.StatusOK {
		t.Fatalf("tools/call: status = %d, want 200", call.StatusCode)
	}
	assertStreamPassesThroughProxies(t, call)
}

func TestMCPStream_BodyStartedWithoutWriteHeader_StillTellsProxiesNotToBuffer(t *testing.T) {
	for _, tc := range []struct {
		name  string
		start func(w http.ResponseWriter)
	}{
		{"flush first", func(w http.ResponseWriter) {
			w.(http.Flusher).Flush()
			_, _ = io.WriteString(w, "event: message\ndata: {}\n\n")
		}},
		{"write first", func(w http.ResponseWriter) {
			_, _ = io.WriteString(w, "event: message\ndata: {}\n\n")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			streams := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				w.Header().Set("Cache-Control", "no-cache")
				tc.start(w)
			})
			srv := httptest.NewServer(noAuthStreamAdapter().assembleHTTPHandler(streams))
			t.Cleanup(srv.Close)

			resp := postMCPMessage(t, srv.URL, "", `{}`)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status = %d, want 200", resp.StatusCode)
			}
			assertStreamPassesThroughProxies(t, resp)
		})
	}
}

var errDeadlineHandlerNotRun = errors.New("deadline handler did not run")

func TestMCPStream_KeepsWriteDeadlineSupport(t *testing.T) {
	deadlineErr := errDeadlineHandlerNotRun
	setsDeadline := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		deadlineErr = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(time.Minute))
		w.WriteHeader(http.StatusNoContent)
	})
	srv := httptest.NewServer(noAuthStreamAdapter().assembleHTTPHandler(setsDeadline))
	t.Cleanup(srv.Close)

	resp := postMCPMessage(t, srv.URL, "", `{}`)

	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want 204 from the deadline handler", resp.StatusCode)
	}
	if deadlineErr != nil {
		t.Fatalf("SetWriteDeadline through the stream wrapper: %v", deadlineErr)
	}
}

type nonFlushingWriter struct {
	http.ResponseWriter
}

func TestMCPStream_DoesNotAdvertiseFlushOverANonFlushingWriter(t *testing.T) {
	var advertisedFlush bool
	streams := passStreamsThroughProxies(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, advertisedFlush = w.(http.Flusher)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
	}))
	rec := httptest.NewRecorder()

	streams.ServeHTTP(nonFlushingWriter{rec}, httptest.NewRequest(http.MethodGet, "/", nil))

	if advertisedFlush {
		t.Error("wrapper exposed http.Flusher over a writer that cannot flush; mcp-go would try to stream")
	}
	if got := rec.Header().Get("X-Accel-Buffering"); got != "no" {
		t.Errorf("X-Accel-Buffering = %q, want %q", got, "no")
	}
}

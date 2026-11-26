// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package a2a

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	a2av1 "github.com/a2aproject/a2a-go/v2/a2a"

	"github.com/SolaceProducts/solace-agent-mesh/internal/observability"
	"github.com/SolaceProducts/solace-agent-mesh/internal/proxy"
)

func cardAt(streaming bool, binding, interfaceURL string) func(string) map[string]any {
	return func(base string) map[string]any {
		card := v1Card(streaming)(base)
		card["supportedInterfaces"] = []any{map[string]any{"url": interfaceURL, "protocolBinding": binding, "protocolVersion": "1.0"}}
		return card
	}
}

func notFound(w http.ResponseWriter, _ remoteCall) { w.WriteHeader(http.StatusNotFound) }

func writeSSE(w http.ResponseWriter, call remoteCall, results ...string) {
	id, _ := json.Marshal(call.ID)
	w.Header().Set("Content-Type", "text/event-stream")
	for _, r := range results {
		_, _ = fmt.Fprintf(w, "data: {\"jsonrpc\":\"2.0\",\"id\":%s,%s}\n\n", id, r)
	}
}

func answerStatus(status int, contentType string) func(http.ResponseWriter, remoteCall) {
	return func(w http.ResponseWriter, _ remoteCall) {
		w.Header().Set("Content-Type", contentType)
		w.WriteHeader(status)
		_, _ = fmt.Fprint(w, "<html>gateway page CANARY-PROMPT-TEXT</html>")
	}
}

func writeOversizeTask(w http.ResponseWriter, call remoteCall) {
	idJSON, _ := json.Marshal(call.ID)
	_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":{"task":{"id":"t","contextId":"c","status":{"state":"TASK_STATE_COMPLETED","message":{"messageId":"m","role":"ROLE_AGENT","parts":[{"text":"CANARY-PROMPT-TEXT%s"}]}}}}}`,
		idJSON, strings.Repeat("A", maxV1ResponseBytes))
}

const workingStatusUpdate = `"result":{"statusUpdate":{"taskId":"remote-1","contextId":"ctx","status":{"state":"TASK_STATE_WORKING"}}}`

func TestForwardRequest_V1FailuresAreSanitizedAndClassified(t *testing.T) {
	longCanary := strings.Repeat("x", 2000) + "CANARY-PROMPT-TEXT"
	streamingRemote := func(t *testing.T, answer func(http.ResponseWriter, remoteCall)) *proxy.ProxyConfig {
		remote := newScriptedRemote(t, v1Card(true, iface("/rpc", "JSONRPC", "1.0")), answer)
		return &proxy.ProxyConfig{Name: "p", URL: remote.srv.URL, UseAgentCardURL: true}
	}
	nonStreamingRemote := func(t *testing.T, at map[string]any, answer func(http.ResponseWriter, remoteCall)) *proxy.ProxyConfig {
		remote := newScriptedRemote(t, v1Card(false, at), answer)
		return &proxy.ProxyConfig{Name: "p", URL: remote.srv.URL, UseAgentCardURL: true}
	}

	tests := []struct {
		name      string
		setup     func(t *testing.T) *proxy.ProxyConfig
		wantText  string
		wantLabel string
		wantIs    error
	}{
		{
			name: "an A2A error on a stream",
			setup: func(t *testing.T) *proxy.ProxyConfig {
				return streamingRemote(t, func(w http.ResponseWriter, call remoteCall) {
					writeSSE(w, call, fmt.Sprintf(`"error":{"code":-32001,"message":%q}`, longCanary))
				})
			},
			wantText:  "task not found: " + strings.Repeat("x", maxRemoteErrorMessage) + "…",
			wantLabel: "jsonrpc_error",
			wantIs:    a2av1.ErrTaskNotFound,
		},
		{
			name: "a JSON-RPC A2A error whose message looks like an HTTP status",
			setup: func(t *testing.T) *proxy.ProxyConfig {
				return streamingRemote(t, func(w http.ResponseWriter, call remoteCall) {
					writeSSE(w, call, `"error":{"code":-32000,"message":"unexpected HTTP status 401: upstream"}`)
				})
			},
			wantText:  "server error: unexpected HTTP status 401: upstream",
			wantLabel: "jsonrpc_error",
			wantIs:    a2av1.ErrServerError,
		},
		{
			name: "a 204 from a JSON-RPC interface",
			setup: func(t *testing.T) *proxy.ProxyConfig {
				return nonStreamingRemote(t, iface("/rpc", "JSONRPC", "1.0"), answerStatus(http.StatusNoContent, "application/json"))
			},
			wantText:  "unexpected HTTP status: 204 No Content",
			wantLabel: "error",
		},
		{
			name: "a 202 from an HTTP+JSON interface",
			setup: func(t *testing.T) *proxy.ProxyConfig {
				return nonStreamingRemote(t, iface("/rest", "HTTP+JSON", "1.0"), answerStatus(http.StatusAccepted, "text/html"))
			},
			wantText:  "unexpected HTTP status: 202 Accepted",
			wantLabel: "error",
		},
		{
			name: "a 302 without a Location from an HTTP+JSON interface",
			setup: func(t *testing.T) *proxy.ProxyConfig {
				return nonStreamingRemote(t, iface("/rest", "HTTP+JSON", "1.0"), answerStatus(http.StatusFound, "text/html"))
			},
			wantText:  "unexpected HTTP status: 302 Found",
			wantLabel: "error",
		},
		{
			name: "a 403 from an HTTP+JSON interface",
			setup: func(t *testing.T) *proxy.ProxyConfig {
				return nonStreamingRemote(t, iface("/rest", "HTTP+JSON", "1.0"), answerStatus(http.StatusForbidden, "text/plain"))
			},
			wantText:  "unexpected HTTP status: 403 Forbidden",
			wantLabel: "auth_error",
			wantIs:    a2av1.ErrUnauthorized,
		},
		{
			name: "a 403 from a streaming HTTP+JSON interface",
			setup: func(t *testing.T) *proxy.ProxyConfig {
				remote := newScriptedRemote(t, v1Card(true, iface("/rest", "HTTP+JSON", "1.0")), answerStatus(http.StatusForbidden, "text/plain"))
				return &proxy.ProxyConfig{Name: "p", URL: remote.srv.URL, UseAgentCardURL: true}
			},
			wantText:  "unexpected HTTP status: 403 Forbidden",
			wantLabel: "auth_error",
			wantIs:    a2av1.ErrUnauthorized,
		},
		{
			name: "a 404 from an HTTP+JSON interface",
			setup: func(t *testing.T) *proxy.ProxyConfig {
				return nonStreamingRemote(t, iface("/rest", "HTTP+JSON", "1.0"), answerStatus(http.StatusNotFound, "text/plain"))
			},
			wantText:  "unexpected HTTP status: 404 Not Found",
			wantLabel: "4xx_error",
			wantIs:    a2av1.ErrMethodNotFound,
		},
		{
			name: "a 502 from an HTTP+JSON interface",
			setup: func(t *testing.T) *proxy.ProxyConfig {
				return nonStreamingRemote(t, iface("/rest", "HTTP+JSON", "1.0"), answerStatus(http.StatusBadGateway, "text/plain"))
			},
			wantText:  "unexpected HTTP status: 502 Bad Gateway",
			wantLabel: "5xx_error",
			wantIs:    a2av1.ErrServerError,
		},
		{
			name: "a non-streaming answer over the size cap without a Content-Type",
			setup: func(t *testing.T) *proxy.ProxyConfig {
				return nonStreamingRemote(t, iface("/rpc", "JSONRPC", "1.0"), func(w http.ResponseWriter, call remoteCall) {
					writeOversizeTask(noContentTypeWriter{w}, call)
				})
			},
			wantText:  errV1ResponseTooLarge.Error(),
			wantLabel: "error",
		},
		{
			name: "a non-streaming answer over the size cap labelled as SSE",
			setup: func(t *testing.T) *proxy.ProxyConfig {
				return nonStreamingRemote(t, iface("/rpc", "JSONRPC", "1.0"), func(w http.ResponseWriter, call remoteCall) {
					w.Header().Set("Content-Type", "text/event-stream")
					writeOversizeTask(w, call)
				})
			},
			wantText:  errV1ResponseTooLarge.Error(),
			wantLabel: "error",
		},
		{
			name: "a stream event that is not JSON",
			setup: func(t *testing.T) *proxy.ProxyConfig {
				return streamingRemote(t, func(w http.ResponseWriter, _ remoteCall) {
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = fmt.Fprint(w, "data: {CANARY-PROMPT-TEXT not json\n\n")
				})
			},
			wantText:  errInvalidV1Response.Error(),
			wantLabel: "error",
		},
		{
			name: "a 502 from a JSON-RPC interface",
			setup: func(t *testing.T) *proxy.ProxyConfig {
				return streamingRemote(t, func(w http.ResponseWriter, _ remoteCall) {
					w.WriteHeader(http.StatusBadGateway)
					_, _ = fmt.Fprint(w, "CANARY-PROMPT-TEXT")
				})
			},
			wantText:  "unexpected HTTP status: 502 Bad Gateway",
			wantLabel: "5xx_error",
		},
		{
			name: "a 401 from an HTTP+JSON interface",
			setup: func(t *testing.T) *proxy.ProxyConfig {
				remote := newScriptedRemote(t, v1Card(false, iface("/rest", "HTTP+JSON", "1.0")), func(w http.ResponseWriter, _ remoteCall) {
					w.WriteHeader(http.StatusUnauthorized)
					_, _ = fmt.Fprint(w, "CANARY-PROMPT-TEXT")
				})
				return &proxy.ProxyConfig{Name: "p", URL: remote.srv.URL, UseAgentCardURL: true}
			},
			wantText:  "unexpected HTTP status: 401 Unauthorized",
			wantLabel: "auth_error",
			wantIs:    a2av1.ErrUnauthenticated,
		},
		{
			name: "a closed port at a url carrying a credential",
			setup: func(t *testing.T) *proxy.ProxyConfig {
				ln, err := net.Listen("tcp", "127.0.0.1:0")
				if err != nil {
					t.Fatal(err)
				}
				closed := "http://" + ln.Addr().String() + "/rpc?token=SECRET-TOKEN"
				_ = ln.Close()
				cardHost := newScriptedRemote(t, cardAt(true, "JSONRPC", closed), notFound)
				return &proxy.ProxyConfig{Name: "p", URL: cardHost.srv.URL, UseAgentCardURL: true}
			},
			wantText:  "network connection failed",
			wantLabel: "connection_error",
		},
		{
			name: "an untrusted TLS certificate",
			setup: func(t *testing.T) *proxy.ProxyConfig {
				tlsRemote := httptest.NewTLSServer(http.NotFoundHandler())
				t.Cleanup(tlsRemote.Close)
				cardHost := newScriptedRemote(t, cardAt(true, "JSONRPC", tlsRemote.URL+"/rpc"), notFound)
				return &proxy.ProxyConfig{Name: "p", URL: cardHost.srv.URL, UseAgentCardURL: true, SSLVerify: true}
			},
			wantText:  errV1TLS.Error(),
			wantLabel: "error",
		},
		{
			name: "a remote that does not answer within the request timeout",
			setup: func(t *testing.T) *proxy.ProxyConfig {
				stall := make(chan struct{})
				cfg := streamingRemote(t, func(http.ResponseWriter, remoteCall) { <-stall })
				t.Cleanup(func() { close(stall) })
				cfg.RequestTimeoutSeconds = 1
				return cfg
			},
			wantText:  "connection timed out",
			wantLabel: "timeout",
		},
		{
			name: "a stream that stalls after its first event",
			setup: func(t *testing.T) *proxy.ProxyConfig {
				stall := make(chan struct{})
				cfg := streamingRemote(t, func(w http.ResponseWriter, call remoteCall) {
					writeSSE(w, call, workingStatusUpdate)
					w.(http.Flusher).Flush()
					<-stall
				})
				t.Cleanup(func() { close(stall) })
				cfg.RequestTimeoutSeconds = 1
				return cfg
			},
			wantText:  "connection timed out",
			wantLabel: "timeout",
		},
		{
			name: "a JSON answer to a stream call over the size cap",
			setup: func(t *testing.T) *proxy.ProxyConfig {
				return streamingRemote(t, func(w http.ResponseWriter, call remoteCall) {
					writeRPCError(w, call.ID, -32603, "CANARY-PROMPT-TEXT"+strings.Repeat("A", maxV1ResponseBytes))
				})
			},
			wantText:  errV1ResponseTooLarge.Error(),
			wantLabel: "error",
		},
		{
			name: "a stream the remote cuts before the task finishes",
			setup: func(t *testing.T) *proxy.ProxyConfig {
				return streamingRemote(t, func(w http.ResponseWriter, call remoteCall) {
					writeSSE(w, call, workingStatusUpdate)
					w.(http.Flusher).Flush()
					if conn, _, err := w.(http.Hijacker).Hijack(); err == nil {
						_ = conn.Close()
					}
				})
			},
			wantText:  errV1StreamClosed.Error(),
			wantLabel: "error",
		},
		{
			name: "a token endpoint that refuses the proxy",
			setup: func(t *testing.T) *proxy.ProxyConfig {
				tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusBadRequest)
					_, _ = fmt.Fprint(w, `{"error":"invalid_client","error_description":"CANARY-IDP-TEXT"}`)
				}))
				t.Cleanup(tokenSrv.Close)
				cfg := streamingRemote(t, notFound)
				cfg.TaskAuth = &proxy.AuthConfig{Type: "oauth2_client_credentials", TokenURL: tokenSrv.URL, ClientID: "c", ClientSecret: "s", AllowInsecureTokenURL: true}
				return cfg
			},
			wantText:  errV1TaskAuth.Error(),
			wantLabel: "error",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := tt.setup(t)
			cfg.AgentCardPath = "/.well-known/agent-card.json"
			var logs bytes.Buffer
			adapter, err := NewA2AAdapter(cfg, slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
			if err != nil {
				t.Fatalf("NewA2AAdapter: %v", err)
			}
			t.Cleanup(func() { _ = adapter.Close() })
			if _, err = adapter.FetchAgentCard(t.Context()); err != nil {
				t.Fatalf("FetchAgentCard: %v", err)
			}
			err = lastError(forwardAndDrain(t, adapter, &proxy.TaskContext{TaskID: "t", IsStreaming: true}))
			if err == nil || err.Error() != tt.wantText {
				t.Fatalf("error = %.160v, want %.160q", err, tt.wantText)
			}
			causeLog := lineContaining(logs.String(), `msg="remote agent A2A 1.0 call failed"`)
			if causeLog == "" {
				t.Fatal("the failure's cause was not logged")
			}
			if strings.Contains(causeLog, "CANARY") || strings.Contains(causeLog, "SECRET-TOKEN") {
				t.Errorf("cause log quotes the remote's response, an identity provider's error or a url credential: %s", causeLog)
			}
			if got := observability.ClassifyA2AError(err); got != tt.wantLabel {
				t.Errorf("error.type label = %q, want %q", got, tt.wantLabel)
			}
			if tt.wantIs != nil && !errors.Is(err, tt.wantIs) {
				t.Errorf("errors.Is(%v) = false", tt.wantIs)
			}
		})
	}
}

func TestForwardRequest_V1SendCarriesTheInterfaceTenant(t *testing.T) {
	tenantIface := func(path, binding string) map[string]any {
		return map[string]any{"url": path, "protocolBinding": binding, "protocolVersion": "1.0", "tenant": "acme"}
	}
	restCompleted := func(w http.ResponseWriter, call remoteCall) {
		if strings.HasSuffix(call.Path, ":stream") {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = fmt.Fprint(w, `data: {"statusUpdate":{"taskId":"remote-1","contextId":"ctx","status":{"state":"TASK_STATE_COMPLETED"}}}`+"\n\n")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, completedV1Task)
	}
	tests := []struct {
		name      string
		streaming bool
		iface     map[string]any
		answer    func(http.ResponseWriter, remoteCall)
		check     func(t *testing.T, call remoteCall)
	}{
		{"JSON-RPC, in the body", false, tenantIface("/rpc", "JSONRPC"),
			func(w http.ResponseWriter, call remoteCall) { writeRPCResult(w, call.ID, completedV1Task) },
			func(t *testing.T, call remoteCall) {
				if !strings.Contains(call.Body, `"tenant":"acme"`) {
					t.Errorf("body = %s, want the tenant", call.Body)
				}
			}},
		{"HTTP+JSON send, in the path", false, tenantIface("/rest", "HTTP+JSON"), restCompleted,
			func(t *testing.T, call remoteCall) {
				if call.Path != "/rest/acme/message:send" {
					t.Errorf("path = %s, want /rest/acme/message:send", call.Path)
				}
			}},
		{"HTTP+JSON stream, in the path", true, tenantIface("/rest", "HTTP+JSON"), restCompleted,
			func(t *testing.T, call remoteCall) {
				if call.Path != "/rest/acme/message:stream" {
					t.Errorf("path = %s, want /rest/acme/message:stream", call.Path)
				}
			}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			remote := newScriptedRemote(t, v1Card(tt.streaming, tt.iface), tt.answer)
			adapter := newV1TestAdapter(t, &proxy.ProxyConfig{Name: "p", URL: remote.srv.URL, UseAgentCardURL: true})
			if _, err := adapter.FetchAgentCard(t.Context()); err != nil {
				t.Fatalf("FetchAgentCard: %v", err)
			}
			if err := lastError(forwardAndDrain(t, adapter, &proxy.TaskContext{TaskID: "t", IsStreaming: true})); err != nil {
				t.Fatalf("downstream error: %v", err)
			}
			calls := remote.recorded()
			if len(calls) != 1 {
				t.Fatalf("calls = %+v, want one", calls)
			}
			tt.check(t, calls[0])
		})
	}
}

func TestForwardRequest_V1InvalidInterfaceURLIsNotQuoted(t *testing.T) {
	cardHost := newScriptedRemote(t, cardAt(false, "HTTP+JSON", "http://u:SECRET@127.0.0.1:bad/rest"), notFound)
	adapter := newV1TestAdapter(t, &proxy.ProxyConfig{Name: "p", URL: cardHost.srv.URL, UseAgentCardURL: true})
	if _, err := adapter.FetchAgentCard(t.Context()); err != nil {
		t.Fatalf("FetchAgentCard: %v", err)
	}
	err := lastError(forwardAndDrain(t, adapter, &proxy.TaskContext{TaskID: "t"}))
	if err == nil || err.Error() != "remote agent HTTP+JSON interface URL is invalid" {
		t.Errorf("error = %v, want the fixed invalid-url text", err)
	}
}

func lineContaining(text, substr string) string {
	for line := range strings.Lines(text) {
		if strings.Contains(line, substr) {
			return line
		}
	}
	return ""
}

func TestCancelTask_V1ErrorIsSanitized(t *testing.T) {
	tests := []struct {
		name    string
		binding string
		path    string
		answer  func(http.ResponseWriter, remoteCall)
		want    string
	}{
		{
			name:    "a long A2A error from a JSON-RPC interface",
			binding: proxy.RemoteBindingJSONRPC,
			path:    "/rpc",
			answer: func(w http.ResponseWriter, call remoteCall) {
				writeRPCError(w, call.ID, -32001, strings.Repeat("x", 2000)+"CANARY-PROMPT-TEXT")
			},
			want: "task not found: " + strings.Repeat("x", maxRemoteErrorMessage) + "…",
		},
		{
			name:    "a 502 from a JSON-RPC interface",
			binding: proxy.RemoteBindingJSONRPC,
			path:    "/rpc",
			answer:  answerStatus(http.StatusBadGateway, "text/html"),
			want:    "unexpected HTTP status: 502 Bad Gateway",
		},
		{
			name:    "a 403 from an HTTP+JSON interface",
			binding: proxy.RemoteBindingHTTPJSON,
			path:    "/rest",
			answer:  answerStatus(http.StatusForbidden, "text/plain"),
			want:    "unexpected HTTP status: 403 Forbidden",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			remote := newScriptedRemote(t, v1Card(false), tt.answer)
			adapter := newV1TestAdapter(t, &proxy.ProxyConfig{Name: "p", URL: remote.srv.URL, UseAgentCardURL: true})
			tc := &proxy.TaskContext{TaskID: "t", DownstreamTaskID: "remote-1"}
			tc.SetRemoteInterface(&proxy.RemoteInterface{URL: remote.srv.URL + tt.path, Binding: tt.binding, Version: proxy.ProtocolVersion10})

			if err := adapter.CancelTask(t.Context(), tc); err == nil || err.Error() != tt.want {
				t.Errorf("error = %.160v, want %.160q", err, tt.want)
			}
			if calls := remote.recorded(); len(calls) != 1 {
				t.Errorf("calls = %+v, want the one cancel", calls)
			}
		})
	}
}

func TestCancelTask_WarnsWhenTheRemoteTaskIsNotNamedYet(t *testing.T) {
	tests := []struct {
		name     string
		sent     bool
		wantWarn bool
	}{
		{"sent, remote task not named yet", true, true},
		{"never sent", false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var logs bytes.Buffer
			adapter, err := NewA2AAdapter(&proxy.ProxyConfig{Name: "p", URL: "http://unused.example.com"}, slog.New(slog.NewTextHandler(&logs, nil)))
			if err != nil {
				t.Fatalf("NewA2AAdapter: %v", err)
			}
			t.Cleanup(func() { _ = adapter.Close() })
			tc := &proxy.TaskContext{TaskID: "t"}
			if tt.sent {
				tc.SetRemoteInterface(&proxy.RemoteInterface{URL: "http://unused.example.com/rpc", Binding: proxy.RemoteBindingJSONRPC, Version: proxy.ProtocolVersion10})
			}

			if err := adapter.CancelTask(t.Context(), tc); err != nil {
				t.Fatalf("CancelTask: %v", err)
			}
			warned := strings.Contains(logs.String(), "level=WARN") && strings.Contains(logs.String(), "cancel not forwarded, remote task not yet named")
			if warned != tt.wantWarn {
				t.Errorf("warned = %v, want %v; logs:\n%s", warned, tt.wantWarn, logs.String())
			}
		})
	}
}

func TestCancelTask_V1RefusesATaskIDThatLeavesItsPathSegment(t *testing.T) {
	remote := newScriptedRemote(t, v1Card(false), notFound)
	adapter := newV1TestAdapter(t, &proxy.ProxyConfig{Name: "p", URL: remote.srv.URL, UseAgentCardURL: true})
	tc := &proxy.TaskContext{TaskID: "t", DownstreamTaskID: "../../admin"}
	tc.SetRemoteInterface(&proxy.RemoteInterface{URL: remote.srv.URL + "/agents/foo/rest", Binding: proxy.RemoteBindingHTTPJSON, Version: proxy.ProtocolVersion10})

	if err := adapter.CancelTask(t.Context(), tc); !errors.Is(err, errUnsafeRemoteTaskID) {
		t.Errorf("CancelTask error = %v, want errUnsafeRemoteTaskID", err)
	}
	if calls := remote.recorded(); len(calls) != 0 {
		t.Errorf("calls = %+v, want none", calls)
	}

	t.Run("a JSON-RPC cancel carries any task id, in its body", func(t *testing.T) {
		remote := newScriptedRemote(t, v1Card(false), func(w http.ResponseWriter, call remoteCall) {
			writeRPCResult(w, call.ID, `{"id":"a/b c","contextId":"ctx","status":{"state":"TASK_STATE_CANCELED"}}`)
		})
		adapter := newV1TestAdapter(t, &proxy.ProxyConfig{Name: "p", URL: remote.srv.URL, UseAgentCardURL: true})
		tc := &proxy.TaskContext{TaskID: "t", DownstreamTaskID: "a/b c"}
		tc.SetRemoteInterface(&proxy.RemoteInterface{URL: remote.srv.URL + "/rpc", Binding: proxy.RemoteBindingJSONRPC, Version: proxy.ProtocolVersion10})
		if err := adapter.CancelTask(t.Context(), tc); err != nil {
			t.Fatalf("CancelTask: %v", err)
		}
		if calls := remote.recorded(); len(calls) != 1 || calls[0].Method != "CancelTask" {
			t.Errorf("calls = %+v, want one CancelTask", calls)
		}
	})
}

func TestRefuseCrossOriginRedirect(t *testing.T) {
	via := func(n int) []*http.Request {
		reqs := make([]*http.Request, n)
		for i := range reqs {
			reqs[i] = httptest.NewRequest(http.MethodPost, "https://agent.example.com/rpc", nil)
		}
		return reqs
	}
	tests := []struct {
		name string
		next string
		hops int
		want error
	}{
		{"same origin", "https://agent.example.com/rpc2", 1, nil},
		{"https to http on the same host", "http://agent.example.com/rpc", 1, errCrossOriginRedirect},
		{"another host", "https://other.example.com/rpc", 1, errCrossOriginRedirect},
		{"another port", "https://agent.example.com:8443/rpc", 1, errCrossOriginRedirect},
		{"the hop limit", "https://agent.example.com/rpc", maxV1Redirects, errTooManyV1Redirects},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			next := httptest.NewRequest(http.MethodPost, tt.next, nil)
			if err := refuseCrossOriginRedirect(next, via(tt.hops)); !errors.Is(err, tt.want) || (tt.want == nil) != (err == nil) {
				t.Errorf("err = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestForwardRequest_V1RedirectLoopStops(t *testing.T) {
	remote := newScriptedRemote(t, v1Card(false, iface("/rpc", "JSONRPC", "1.0")), func(w http.ResponseWriter, _ remoteCall) {
		w.Header().Set("Location", "/rpc")
		w.WriteHeader(http.StatusTemporaryRedirect)
	})
	adapter := newV1TestAdapter(t, &proxy.ProxyConfig{Name: "p", URL: remote.srv.URL, UseAgentCardURL: true})
	if _, err := adapter.FetchAgentCard(t.Context()); err != nil {
		t.Fatalf("FetchAgentCard: %v", err)
	}
	err := lastError(forwardAndDrain(t, adapter, &proxy.TaskContext{TaskID: "t"}))
	if err == nil || err.Error() != errTooManyV1Redirects.Error() {
		t.Errorf("error = %v, want the redirect limit", err)
	}
	if calls := len(remote.recorded()); calls != maxV1Redirects {
		t.Errorf("remote received %d calls, want %d", calls, maxV1Redirects)
	}
}

func TestForwardRequest_V1StreamIsNotSizeCapped(t *testing.T) {
	chunk := strings.Repeat("A", 1<<20)
	working := fmt.Sprintf(`{"statusUpdate":{"taskId":"remote-1","contextId":"ctx","status":{"state":"TASK_STATE_WORKING","message":{"messageId":"m","role":"ROLE_AGENT","parts":[{"text":%q}]}}}}`, chunk)
	completed := `{"statusUpdate":{"taskId":"remote-1","contextId":"ctx","status":{"state":"TASK_STATE_COMPLETED"}}}`
	updates := make([]string, 0, 13)
	for range 12 {
		updates = append(updates, working)
	}
	updates = append(updates, completed)

	jsonRPCStream := func(contentType string) func(http.ResponseWriter, remoteCall) {
		return func(w http.ResponseWriter, call remoteCall) {
			id, _ := json.Marshal(call.ID)
			w.Header().Set("Content-Type", contentType)
			if contentType == "" {
				w = noContentTypeWriter{w}
			}
			for _, u := range updates {
				_, _ = fmt.Fprintf(w, "data: {\"jsonrpc\":\"2.0\",\"id\":%s,\"result\":%s}\n\n", id, u)
			}
		}
	}
	tests := []struct {
		name   string
		at     map[string]any
		answer func(http.ResponseWriter, remoteCall)
	}{
		{"JSON-RPC without a Content-Type", iface("/rpc", "JSONRPC", "1.0"), jsonRPCStream("")},
		{"JSON-RPC with a charset", iface("/rpc", "JSONRPC", "1.0"), jsonRPCStream("text/event-stream; charset=utf-8")},
		{"HTTP+JSON labelled as JSON", iface("/rest", "HTTP+JSON", "1.0"), func(w http.ResponseWriter, _ remoteCall) {
			w.Header().Set("Content-Type", "application/json")
			for _, u := range updates {
				_, _ = fmt.Fprintf(w, "data: %s\n\n", u)
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			remote := newScriptedRemote(t, v1Card(true, tt.at), tt.answer)
			adapter := newV1TestAdapter(t, &proxy.ProxyConfig{Name: "p", URL: remote.srv.URL, UseAgentCardURL: true})
			if _, err := adapter.FetchAgentCard(t.Context()); err != nil {
				t.Fatalf("FetchAgentCard: %v", err)
			}
			events := forwardAndDrain(t, adapter, &proxy.TaskContext{TaskID: "t", IsStreaming: true})
			if err := lastError(events); err != nil || len(events) != len(updates) {
				t.Errorf("got %d events, last error %v; want %d events and no error", len(events), err, len(updates))
			}
		})
	}
}

type noContentTypeWriter struct{ http.ResponseWriter }

func (w noContentTypeWriter) WriteHeader(code int) {
	w.Header()["Content-Type"] = nil
	w.ResponseWriter.WriteHeader(code)
}

func (w noContentTypeWriter) Write(b []byte) (int, error) {
	if w.Header()["Content-Type"] != nil {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(b)
}

func TestFetchAgentCard_UnreadableInterfaces(t *testing.T) {
	t.Run("a card with a top-level url is spoken to there at 0.3", func(t *testing.T) {
		remote := newScriptedRemote(t, func(base string) map[string]any {
			return map[string]any{"name": "old", "url": base + "/v03", "protocolVersion": "0.3.0", "supportedInterfaces": "nonsense"}
		}, func(w http.ResponseWriter, call remoteCall) { writeLegacyTask(w, call.ID) })
		adapter := newV1TestAdapter(t, &proxy.ProxyConfig{Name: "p", URL: remote.srv.URL, UseAgentCardURL: true})
		if _, err := adapter.FetchAgentCard(t.Context()); err != nil {
			t.Fatalf("FetchAgentCard: %v", err)
		}
		if !adapter.negotiated.Load().interfacesUnread {
			t.Error("interfacesUnread not set, so the fallback is not logged")
		}
		if err := lastError(forwardAndDrain(t, adapter, &proxy.TaskContext{TaskID: "t"})); err != nil {
			t.Fatalf("downstream error: %v", err)
		}
		if calls := remote.recorded(); len(calls) != 1 || calls[0].Path != "/v03" || calls[0].Method != "message/send" {
			t.Errorf("calls = %+v, want message/send at the card's url", calls)
		}
	})

	t.Run("a half-decoded interface list is not used", func(t *testing.T) {
		remote := newScriptedRemote(t, func(base string) map[string]any {
			return map[string]any{
				"name": "old", "url": base + "/v03", "protocolVersion": "0.3.0",
				"supportedInterfaces": []any{map[string]any{"url": base + "/rpc", "protocolBinding": "JSONRPC", "protocolVersion": 1.0}},
			}
		}, func(w http.ResponseWriter, call remoteCall) { writeLegacyTask(w, call.ID) })
		adapter := newV1TestAdapter(t, &proxy.ProxyConfig{Name: "p", URL: remote.srv.URL, UseAgentCardURL: true})
		if _, err := adapter.FetchAgentCard(t.Context()); err != nil {
			t.Fatalf("FetchAgentCard: %v", err)
		}
		n := adapter.negotiated.Load()
		if chosen := n.chosen(); !n.interfacesUnread || chosen.Version != proxy.ProtocolVersion03 || chosen.URL != remote.srv.URL+"/v03" {
			t.Fatalf("negotiation = %+v, want the card's 0.3 url with interfacesUnread", n)
		}
		if err := lastError(forwardAndDrain(t, adapter, &proxy.TaskContext{TaskID: "t"})); err != nil {
			t.Fatalf("downstream error: %v", err)
		}
		if calls := remote.recorded(); len(calls) != 1 || calls[0].Path != "/v03" {
			t.Errorf("calls = %+v, want one call at /v03", calls)
		}
	})

	t.Run("the fallback is logged when it starts, once", func(t *testing.T) {
		var unreadable atomic.Bool
		remote := newScriptedRemote(t, func(base string) map[string]any {
			card := map[string]any{"name": "old", "url": base + "/v03", "protocolVersion": "0.3.0"}
			if unreadable.Load() {
				card["supportedInterfaces"] = "nonsense"
			}
			return card
		}, notFound)
		var logs bytes.Buffer
		adapter, err := NewA2AAdapter(&proxy.ProxyConfig{Name: "p", URL: remote.srv.URL, UseAgentCardURL: true, AgentCardPath: "/.well-known/agent-card.json"},
			slog.New(slog.NewTextHandler(&logs, nil)))
		if err != nil {
			t.Fatalf("NewA2AAdapter: %v", err)
		}
		t.Cleanup(func() { _ = adapter.Close() })

		for _, flip := range []bool{false, true, true} {
			unreadable.Store(flip)
			if _, err := adapter.FetchAgentCard(t.Context()); err != nil {
				t.Fatalf("FetchAgentCard: %v", err)
			}
		}
		if got := strings.Count(logs.String(), "agent card interface list could not be read"); got != 1 {
			t.Errorf("fallback logged %d times, want once:\n%s", got, logs.String())
		}
	})

	t.Run("a card without one fails the fetch", func(t *testing.T) {
		remote := newScriptedRemote(t, func(string) map[string]any {
			return map[string]any{"name": "new", "supportedInterfaces": []any{map[string]any{"url": "https://agent/rpc", "protocolVersion": 1.0}}}
		}, notFound)
		adapter := newV1TestAdapter(t, &proxy.ProxyConfig{Name: "p", URL: remote.srv.URL, UseAgentCardURL: true})
		if _, err := adapter.FetchAgentCard(t.Context()); !errors.Is(err, errUnreadableInterfaces) {
			t.Errorf("FetchAgentCard error = %v, want errUnreadableInterfaces", err)
		}
	})
}

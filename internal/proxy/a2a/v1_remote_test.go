// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package a2a

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	a2av1 "github.com/a2aproject/a2a-go/v2/a2a"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/proxy"
)

type remoteCall struct {
	Path   string
	Method string
	ID     any
	Body   string
	Header http.Header
}

type scriptedRemote struct {
	srv *httptest.Server

	mu    sync.Mutex
	calls []remoteCall
}

func newScriptedRemote(t *testing.T, card func(base string) map[string]any, answer func(w http.ResponseWriter, call remoteCall)) *scriptedRemote {
	t.Helper()
	r := &scriptedRemote{}
	r.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if strings.HasSuffix(req.URL.Path, "/.well-known/agent-card.json") {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(card(r.srv.URL))
			return
		}
		body, _ := io.ReadAll(req.Body)
		var rpc struct {
			Method string `json:"method"`
			ID     any    `json:"id"`
		}
		_ = json.Unmarshal(body, &rpc)
		call := remoteCall{Path: req.URL.Path, Method: rpc.Method, ID: rpc.ID, Body: string(body), Header: req.Header.Clone()}
		r.mu.Lock()
		r.calls = append(r.calls, call)
		r.mu.Unlock()
		answer(w, call)
	}))
	t.Cleanup(r.srv.Close)
	return r
}

func (r *scriptedRemote) recorded() []remoteCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]remoteCall(nil), r.calls...)
}

func v1Card(streaming bool, interfaces ...map[string]any) func(string) map[string]any {
	return func(base string) map[string]any {
		ifaces := make([]any, 0, len(interfaces))
		for _, iface := range interfaces {
			resolved := map[string]any{}
			for k, v := range iface {
				resolved[k] = v
			}
			resolved["url"] = base + iface["url"].(string)
			ifaces = append(ifaces, resolved)
		}
		return map[string]any{
			"name": "remote", "description": "scripted", "version": "1.0.0",
			"capabilities":        map[string]any{"streaming": streaming},
			"defaultInputModes":   []string{"text/plain"},
			"defaultOutputModes":  []string{"text/plain"},
			"skills":              []any{},
			"supportedInterfaces": ifaces,
		}
	}
}

func iface(path, binding, version string) map[string]any {
	return map[string]any{"url": path, "protocolBinding": binding, "protocolVersion": version}
}

const completedV1Task = `{"task":{"id":"remote-1","contextId":"ctx","status":{"state":"TASK_STATE_COMPLETED","message":{"messageId":"m1","role":"ROLE_AGENT","parts":[{"text":"done"}]}}}}`

func writeRPCResult(w http.ResponseWriter, id any, rawResult string) {
	w.Header().Set("Content-Type", "application/json")
	idJSON, _ := json.Marshal(id)
	_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":%s}`, idJSON, rawResult)
}

func writeRPCError(w http.ResponseWriter, id any, code int, message string) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": code, "message": message}})
}

func writeLegacyTask(w http.ResponseWriter, id any) {
	writeRPCResult(w, id, `{"kind":"task","id":"remote-03","contextId":"ctx","status":{"state":"completed"}}`)
}

func newV1TestAdapter(t *testing.T, cfg *proxy.ProxyConfig) *A2AAdapter {
	t.Helper()
	if cfg.AgentCardPath == "" {
		cfg.AgentCardPath = "/.well-known/agent-card.json"
	}
	adapter := mustNewA2AAdapter(t, cfg)
	t.Cleanup(func() { _ = adapter.Close() })
	return adapter
}

func forwardAndDrain(t *testing.T, adapter *A2AAdapter, tc *proxy.TaskContext) []proxy.ResponseEvent {
	t.Helper()
	events, err := adapter.ForwardRequest(t.Context(), tc, sendTextRequest("hello"))
	if err != nil {
		t.Fatalf("ForwardRequest: %v", err)
	}
	var all []proxy.ResponseEvent
	timeout := time.After(10 * time.Second)
	for {
		select {
		case evt, ok := <-events:
			if !ok {
				return all
			}
			all = append(all, evt)
		case <-timeout:
			t.Fatal("timed out waiting for the event stream to end")
		}
	}
}

func lastError(events []proxy.ResponseEvent) error {
	if len(events) == 0 {
		return nil
	}
	return events[len(events)-1].Error
}

var redirectBindings = []struct {
	name      string
	iface     map[string]any
	completed func(w http.ResponseWriter, call remoteCall)
}{
	{"JSON-RPC", iface("/rpc", "JSONRPC", "1.0"), func(w http.ResponseWriter, call remoteCall) { writeRPCResult(w, call.ID, completedV1Task) }},
	{"HTTP+JSON", iface("/rest", "HTTP+JSON", "1.0"), func(w http.ResponseWriter, _ remoteCall) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, completedV1Task)
	}},
}

func TestForwardRequest_V1RefusesCrossOriginRedirect(t *testing.T) {
	var foreignHits int
	var foreignMu sync.Mutex
	foreign := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		foreignMu.Lock()
		foreignHits++
		foreignMu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer foreign.Close()

	for _, b := range redirectBindings {
		remote := newScriptedRemote(t, v1Card(false, b.iface), func(w http.ResponseWriter, call remoteCall) {
			w.Header().Set("Location", foreign.URL+call.Path)
			w.WriteHeader(http.StatusTemporaryRedirect)
		})
		for _, userToken := range []string{"", "user-token"} {
			t.Run(b.name+" user token "+fmt.Sprint(userToken != ""), func(t *testing.T) {
				adapter := newV1TestAdapter(t, &proxy.ProxyConfig{
					Name: "p", URL: remote.srv.URL, UseAgentCardURL: true,
					TaskAuth: &proxy.AuthConfig{Type: "static_bearer", Token: "SERVICE-TOKEN"},
				})
				if _, err := adapter.FetchAgentCard(t.Context()); err != nil {
					t.Fatalf("FetchAgentCard: %v", err)
				}
				events := forwardAndDrain(t, adapter, &proxy.TaskContext{TaskID: "t", OAuth2Token: userToken})
				if err := lastError(events); err == nil || !strings.Contains(err.Error(), "another origin") {
					t.Errorf("last event error = %v, want the cross-origin refusal", err)
				}
			})
		}
	}
	foreignMu.Lock()
	defer foreignMu.Unlock()
	if foreignHits != 0 {
		t.Errorf("foreign host received %d requests, want none", foreignHits)
	}
}

func TestForwardRequest_V1SameOriginRedirectKeepsAuth(t *testing.T) {
	for _, b := range redirectBindings {
		t.Run(b.name, func(t *testing.T) {
			remote := newScriptedRemote(t, v1Card(false, b.iface), func(w http.ResponseWriter, call remoteCall) {
				if !strings.HasPrefix(call.Path, "/moved") {
					w.Header().Set("Location", "/moved"+call.Path)
					w.WriteHeader(http.StatusTemporaryRedirect)
					return
				}
				b.completed(w, call)
			})
			adapter := newV1TestAdapter(t, &proxy.ProxyConfig{
				Name: "p", URL: remote.srv.URL, UseAgentCardURL: true,
				TaskAuth: &proxy.AuthConfig{Type: "static_bearer", Token: "SERVICE-TOKEN"},
			})
			if _, err := adapter.FetchAgentCard(t.Context()); err != nil {
				t.Fatalf("FetchAgentCard: %v", err)
			}

			events := forwardAndDrain(t, adapter, &proxy.TaskContext{TaskID: "t"})
			if err := lastError(events); err != nil {
				t.Fatalf("downstream error: %v", err)
			}
			calls := remote.recorded()
			if len(calls) != 2 || !strings.HasPrefix(calls[1].Path, "/moved") || calls[1].Header.Get("Authorization") != "Bearer SERVICE-TOKEN" {
				t.Errorf("calls = %+v, want the redirected call to carry the task credential", calls)
			}
		})
	}
}

func TestForwardRequest_V1ErrorsDoNotEchoTheRemotesBody(t *testing.T) {
	tests := []struct {
		name   string
		answer func(w http.ResponseWriter, call remoteCall)
		want   string
	}{
		{
			name: "a result that is not an A2A response",
			answer: func(w http.ResponseWriter, call remoteCall) {
				writeRPCResult(w, call.ID, `{"secretUserContent":"CANARY-PROMPT-TEXT","kind":"bogus"}`)
			},
			want: errInvalidV1Response.Error(),
		},
		{
			name: "a long A2A error message",
			answer: func(w http.ResponseWriter, call remoteCall) {
				writeRPCError(w, call.ID, -32001, strings.Repeat("x", 2000)+"CANARY-PROMPT-TEXT")
			},
			want: "task not found: " + strings.Repeat("x", maxRemoteErrorMessage) + "…",
		},
		{
			name: "a non-streaming body over the size cap",
			answer: func(w http.ResponseWriter, call remoteCall) {
				huge := strings.Repeat("A", maxV1ResponseBytes)
				writeRPCResult(w, call.ID, `{"task":{"id":"t","contextId":"c","status":{"state":"TASK_STATE_COMPLETED","message":{"messageId":"m","role":"ROLE_AGENT","parts":[{"text":"`+huge+`CANARY-PROMPT-TEXT"}]}}}}`)
			},
			want: errV1ResponseTooLarge.Error(),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			remote := newScriptedRemote(t, v1Card(false, iface("/rpc", "JSONRPC", "1.0")), tt.answer)
			adapter := newV1TestAdapter(t, &proxy.ProxyConfig{Name: "p", URL: remote.srv.URL, UseAgentCardURL: true})
			if _, err := adapter.FetchAgentCard(t.Context()); err != nil {
				t.Fatalf("FetchAgentCard: %v", err)
			}
			err := lastError(forwardAndDrain(t, adapter, &proxy.TaskContext{TaskID: "t"}))
			if err == nil || err.Error() != tt.want {
				t.Errorf("error = %.120v, want %.120q", err, tt.want)
			}
		})
	}
}

func TestForwardRequest_V1FallbackGuards(t *testing.T) {
	hybrid := v1Card(true, iface("/rpc", "JSONRPC", "1.0"), iface("/v03", "JSONRPC", "0.3"))

	t.Run("an error other than VersionNotSupported is not retried on 0.3", func(t *testing.T) {
		remote := newScriptedRemote(t, hybrid, func(w http.ResponseWriter, call remoteCall) {
			if call.Path == "/v03" {
				writeLegacyTask(w, call.ID)
				return
			}
			w.WriteHeader(http.StatusInternalServerError)
		})
		adapter := newV1TestAdapter(t, &proxy.ProxyConfig{Name: "p", URL: remote.srv.URL, UseAgentCardURL: true})
		if _, err := adapter.FetchAgentCard(t.Context()); err != nil {
			t.Fatalf("FetchAgentCard: %v", err)
		}
		events := forwardAndDrain(t, adapter, &proxy.TaskContext{TaskID: "t"})
		if err := lastError(events); err == nil || !strings.Contains(err.Error(), "500") {
			t.Errorf("last error = %v, want the HTTP 500 relayed", err)
		}
		if calls := remote.recorded(); len(calls) != 1 || calls[0].Path != "/rpc" {
			t.Errorf("calls = %+v, want exactly the one 1.0 call", calls)
		}
	})

	t.Run("VersionNotSupported after an event was relayed is not retried", func(t *testing.T) {
		remote := newScriptedRemote(t, hybrid, func(w http.ResponseWriter, call remoteCall) {
			if call.Path == "/v03" {
				writeLegacyTask(w, call.ID)
				return
			}
			id, _ := json.Marshal(call.ID)
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = fmt.Fprintf(w, "data: {\"jsonrpc\":\"2.0\",\"id\":%s,\"result\":{\"statusUpdate\":{\"taskId\":\"remote-1\",\"contextId\":\"ctx\",\"status\":{\"state\":\"TASK_STATE_WORKING\"}}}}\n\n", id)
			_, _ = fmt.Fprintf(w, "data: {\"jsonrpc\":\"2.0\",\"id\":%s,\"error\":{\"code\":-32009,\"message\":\"version not supported\"}}\n\n", id)
		})
		adapter := newV1TestAdapter(t, &proxy.ProxyConfig{Name: "p", URL: remote.srv.URL, UseAgentCardURL: true})
		if _, err := adapter.FetchAgentCard(t.Context()); err != nil {
			t.Fatalf("FetchAgentCard: %v", err)
		}
		events := forwardAndDrain(t, adapter, &proxy.TaskContext{TaskID: "t", IsStreaming: true})
		if len(events) != 2 || events[0].StatusUpdate == nil || !errors.Is(events[1].Error, a2av1.ErrVersionNotSupported) {
			t.Errorf("events = %+v, want the working update then the version error", events)
		}
		if calls := remote.recorded(); len(calls) != 1 {
			t.Errorf("calls = %+v, want no second submission", calls)
		}
	})

	t.Run("the retry skips interfaces of the rejected version", func(t *testing.T) {
		card := v1Card(false, iface("/rpc", "JSONRPC", "1.0"), iface("/rest", "HTTP+JSON", "1.0"), iface("/v03", "JSONRPC", "0.3"))
		remote := newScriptedRemote(t, card, func(w http.ResponseWriter, call remoteCall) {
			if call.Path == "/v03" {
				writeLegacyTask(w, call.ID)
				return
			}
			writeRPCError(w, call.ID, -32009, "version not supported")
		})
		adapter := newV1TestAdapter(t, &proxy.ProxyConfig{Name: "p", URL: remote.srv.URL, UseAgentCardURL: true})
		if _, err := adapter.FetchAgentCard(t.Context()); err != nil {
			t.Fatalf("FetchAgentCard: %v", err)
		}
		if err := lastError(forwardAndDrain(t, adapter, &proxy.TaskContext{TaskID: "t"})); err != nil {
			t.Fatalf("downstream error: %v", err)
		}
		calls := remote.recorded()
		if len(calls) != 2 || calls[0].Path != "/rpc" || calls[1].Path != "/v03" {
			t.Errorf("calls = %+v, want /rpc then /v03", calls)
		}
	})

	t.Run("no retry when every interface speaks the rejected version", func(t *testing.T) {
		remote := newScriptedRemote(t, v1Card(false, iface("/rpc", "JSONRPC", "1.0"), iface("/rest", "HTTP+JSON", "1.0")), func(w http.ResponseWriter, call remoteCall) {
			writeRPCError(w, call.ID, -32009, "version not supported")
		})
		adapter := newV1TestAdapter(t, &proxy.ProxyConfig{Name: "p", URL: remote.srv.URL, UseAgentCardURL: true})
		if _, err := adapter.FetchAgentCard(t.Context()); err != nil {
			t.Fatalf("FetchAgentCard: %v", err)
		}
		events := forwardAndDrain(t, adapter, &proxy.TaskContext{TaskID: "t"})
		if len(events) != 1 || !errors.Is(events[0].Error, a2av1.ErrVersionNotSupported) {
			t.Errorf("events = %+v, want one version error", events)
		}
		if calls := remote.recorded(); len(calls) != 1 {
			t.Errorf("calls = %+v, want only the first interface", calls)
		}
	})

	t.Run("a lone 1.0 interface's A2A error reaches the caller as an error", func(t *testing.T) {
		remote := newScriptedRemote(t, v1Card(false, iface("/rpc", "JSONRPC", "1.0")), func(w http.ResponseWriter, call remoteCall) {
			writeRPCError(w, call.ID, -32009, "version not supported")
		})
		adapter := newV1TestAdapter(t, &proxy.ProxyConfig{Name: "p", URL: remote.srv.URL, UseAgentCardURL: true})
		if _, err := adapter.FetchAgentCard(t.Context()); err != nil {
			t.Fatalf("FetchAgentCard: %v", err)
		}
		events := forwardAndDrain(t, adapter, &proxy.TaskContext{TaskID: "t"})
		if len(events) != 1 || !errors.Is(events[0].Error, a2av1.ErrVersionNotSupported) {
			t.Errorf("events = %+v, want one version error", events)
		}
	})
}

func TestCancelTask_Routing(t *testing.T) {
	t.Run("a 1.0 task is canceled where it was sent, not where the current negotiation points", func(t *testing.T) {
		remote := newScriptedRemote(t, v1Card(false), func(w http.ResponseWriter, call remoteCall) {
			writeRPCResult(w, call.ID, `{"id":"remote-1","contextId":"ctx","status":{"state":"TASK_STATE_CANCELED"}}`)
		})
		adapter := newV1TestAdapter(t, &proxy.ProxyConfig{Name: "p", URL: remote.srv.URL + "/configured", UseAgentCardURL: true})
		adapter.negotiated.Store(staticNegotiation(proxy.ProtocolVersion03, false))

		tc := &proxy.TaskContext{TaskID: "t", DownstreamTaskID: "remote-1"}
		tc.SetRemoteInterface(&proxy.RemoteInterface{URL: remote.srv.URL + "/rpc", Binding: proxy.RemoteBindingJSONRPC, Version: proxy.ProtocolVersion10, Tenant: "acme"})
		if err := adapter.CancelTask(t.Context(), tc); err != nil {
			t.Fatalf("CancelTask: %v", err)
		}
		calls := remote.recorded()
		if len(calls) != 1 || calls[0].Path != "/rpc" || calls[0].Method != "CancelTask" {
			t.Fatalf("calls = %+v, want one CancelTask on /rpc", calls)
		}
		if !strings.Contains(calls[0].Body, `"tenant":"acme"`) || !strings.Contains(calls[0].Body, `"id":"remote-1"`) {
			t.Errorf("cancel body = %s, want the task id and tenant", calls[0].Body)
		}
	})

	t.Run("a task that fell back to 0.3 is canceled on the 0.3 interface", func(t *testing.T) {
		remote := newScriptedRemote(t, v1Card(false, iface("/rpc", "JSONRPC", "1.0"), iface("/v03", "JSONRPC", "0.3")), func(w http.ResponseWriter, call remoteCall) {
			if call.Path == "/rpc" {
				writeRPCError(w, call.ID, -32009, "version not supported")
				return
			}
			writeLegacyTask(w, call.ID)
		})
		adapter := newV1TestAdapter(t, &proxy.ProxyConfig{Name: "p", URL: remote.srv.URL, UseAgentCardURL: true})
		if _, err := adapter.FetchAgentCard(t.Context()); err != nil {
			t.Fatalf("FetchAgentCard: %v", err)
		}
		tc := &proxy.TaskContext{TaskID: "t"}
		if err := lastError(forwardAndDrain(t, adapter, tc)); err != nil {
			t.Fatalf("downstream error: %v", err)
		}
		tc.DownstreamTaskID = "remote-03"
		if err := adapter.CancelTask(t.Context(), tc); err != nil {
			t.Fatalf("CancelTask: %v", err)
		}
		calls := remote.recorded()
		last := calls[len(calls)-1]
		if last.Path != "/v03" || last.Method != "tasks/cancel" {
			t.Errorf("last call = %+v, want tasks/cancel on /v03", last)
		}
	})

	t.Run("a 0.3 task is canceled at the card's url, which it was sent to", func(t *testing.T) {
		legacy := newScriptedRemote(t, v1Card(false), func(w http.ResponseWriter, call remoteCall) {
			writeLegacyTask(w, call.ID)
		})
		cardHost := newScriptedRemote(t, func(string) map[string]any {
			return map[string]any{"name": "old", "url": legacy.srv.URL + "/v03", "protocolVersion": "0.3.0"}
		}, func(w http.ResponseWriter, _ remoteCall) { w.WriteHeader(http.StatusNotFound) })
		adapter := newV1TestAdapter(t, &proxy.ProxyConfig{Name: "p", URL: cardHost.srv.URL, UseAgentCardURL: true})
		if _, err := adapter.FetchAgentCard(t.Context()); err != nil {
			t.Fatalf("FetchAgentCard: %v", err)
		}
		tc := &proxy.TaskContext{TaskID: "t"}
		if err := lastError(forwardAndDrain(t, adapter, tc)); err != nil {
			t.Fatalf("downstream error: %v", err)
		}
		if ri := tc.RemoteInterface(); ri == nil || ri.URL != legacy.srv.URL+"/v03" {
			t.Fatalf("RemoteInterface = %+v, want the card's 0.3 url", ri)
		}
		tc.DownstreamTaskID = "remote-03"
		if err := adapter.CancelTask(t.Context(), tc); err != nil {
			t.Fatalf("CancelTask: %v", err)
		}
		calls := legacy.recorded()
		if len(calls) != 2 || calls[1].Method != "tasks/cancel" {
			t.Errorf("calls = %+v, want message/send then tasks/cancel at the card's url", calls)
		}
		if got := len(cardHost.recorded()); got != 0 {
			t.Errorf("configured url received %d task calls, want none", got)
		}
	})
}

func TestForwardRequest_V1ConfigWiring(t *testing.T) {
	t.Run("a card that does not stream gets SendMessage", func(t *testing.T) {
		remote := newScriptedRemote(t, v1Card(false, iface("/rpc", "JSONRPC", "1.0")), func(w http.ResponseWriter, call remoteCall) {
			writeRPCResult(w, call.ID, completedV1Task)
		})
		adapter := newV1TestAdapter(t, &proxy.ProxyConfig{Name: "p", URL: remote.srv.URL, UseAgentCardURL: true})
		if _, err := adapter.FetchAgentCard(t.Context()); err != nil {
			t.Fatalf("FetchAgentCard: %v", err)
		}
		events := forwardAndDrain(t, adapter, &proxy.TaskContext{TaskID: "t", IsStreaming: true})
		if len(events) != 1 || events[0].Task == nil || events[0].Task.Status.State != a2a.TaskStateCompleted {
			t.Errorf("events = %+v, want the completed task", events)
		}
		if calls := remote.recorded(); len(calls) != 1 || calls[0].Method != "SendMessage" {
			t.Errorf("calls = %+v, want one SendMessage", calls)
		}
	})

	t.Run("a streaming card is streamed only for a message/stream caller", func(t *testing.T) {
		const taskWithArtifact = `{"task":{"id":"remote-1","contextId":"ctx","status":{"state":"TASK_STATE_COMPLETED"},"artifacts":[{"artifactId":"art-1","name":"report","parts":[{"text":"the full report"}]}]}}`
		remote := newScriptedRemote(t, v1Card(true, iface("/rpc", "JSONRPC", "1.0")), func(w http.ResponseWriter, call remoteCall) {
			if call.Method == "SendStreamingMessage" {
				writeSSE(w, call, `"result":{"statusUpdate":{"taskId":"remote-1","contextId":"ctx","status":{"state":"TASK_STATE_COMPLETED"}}}`)
				return
			}
			writeRPCResult(w, call.ID, taskWithArtifact)
		})
		adapter := newV1TestAdapter(t, &proxy.ProxyConfig{Name: "p", URL: remote.srv.URL, UseAgentCardURL: true})
		if _, err := adapter.FetchAgentCard(t.Context()); err != nil {
			t.Fatalf("FetchAgentCard: %v", err)
		}

		events := forwardAndDrain(t, adapter, &proxy.TaskContext{TaskID: "send"})
		if len(events) != 1 || events[0].Task == nil || len(events[0].Task.Artifacts) != 1 || events[0].Task.Artifacts[0].ArtifactID != "art-1" {
			t.Errorf("message/send events = %+v, want the completed task with its artifact", events)
		}
		forwardAndDrain(t, adapter, &proxy.TaskContext{TaskID: "stream", IsStreaming: true})

		calls := remote.recorded()
		if len(calls) != 2 || calls[0].Method != "SendMessage" || calls[1].Method != "SendStreamingMessage" {
			t.Errorf("calls = %+v, want SendMessage for message/send then SendStreamingMessage for message/stream", calls)
		}
	})

	t.Run("a blocking call answered with an unfinished task is an error", func(t *testing.T) {
		remote := newScriptedRemote(t, v1Card(false, iface("/rpc", "JSONRPC", "1.0")), func(w http.ResponseWriter, call remoteCall) {
			writeRPCResult(w, call.ID, `{"task":{"id":"remote-1","contextId":"ctx","status":{"state":"TASK_STATE_WORKING"}}}`)
		})
		adapter := newV1TestAdapter(t, &proxy.ProxyConfig{Name: "p", URL: remote.srv.URL, UseAgentCardURL: true})
		if _, err := adapter.FetchAgentCard(t.Context()); err != nil {
			t.Fatalf("FetchAgentCard: %v", err)
		}
		events := forwardAndDrain(t, adapter, &proxy.TaskContext{TaskID: "t"})
		if len(events) != 1 || !errors.Is(events[0].Error, errV1TaskUnfinished) || !strings.Contains(events[0].Error.Error(), "TASK_STATE_WORKING") {
			t.Errorf("events = %+v, want errV1TaskUnfinished naming the state", events)
		}
	})

	t.Run("with use_agent_card_url off, a card interface elsewhere leaves the configured url on 0.3", func(t *testing.T) {
		remote := newScriptedRemote(t, v1Card(true, iface("/rpc", "JSONRPC", "1.0")), func(w http.ResponseWriter, call remoteCall) {
			writeLegacyTask(w, call.ID)
		})
		adapter := newV1TestAdapter(t, &proxy.ProxyConfig{Name: "p", URL: remote.srv.URL})
		if _, err := adapter.FetchAgentCard(t.Context()); err != nil {
			t.Fatalf("FetchAgentCard: %v", err)
		}
		if err := lastError(forwardAndDrain(t, adapter, &proxy.TaskContext{TaskID: "t"})); err != nil {
			t.Fatalf("downstream error: %v", err)
		}
		if calls := remote.recorded(); len(calls) != 1 || calls[0].Path != "/" || calls[0].Method != "message/send" {
			t.Errorf("calls = %+v, want 0.3 message/send at the configured url", calls)
		}
	})

	t.Run("with use_agent_card_url off, a 1.0 interface at the configured url is used", func(t *testing.T) {
		remote := newScriptedRemote(t, v1Card(false, iface("/rpc", "JSONRPC", "1.0")), func(w http.ResponseWriter, call remoteCall) {
			writeRPCResult(w, call.ID, completedV1Task)
		})
		adapter := newV1TestAdapter(t, &proxy.ProxyConfig{Name: "p", URL: remote.srv.URL + "/rpc"})
		if _, err := adapter.FetchAgentCard(t.Context()); err != nil {
			t.Fatalf("FetchAgentCard: %v", err)
		}
		if err := lastError(forwardAndDrain(t, adapter, &proxy.TaskContext{TaskID: "t"})); err != nil {
			t.Fatalf("downstream error: %v", err)
		}
		if calls := remote.recorded(); len(calls) != 1 || calls[0].Path != "/rpc" || calls[0].Method != "SendMessage" {
			t.Errorf("calls = %+v, want SendMessage at the configured url", calls)
		}
	})

	t.Run("a static card pinned to 1.0 speaks 1.0 to the configured url", func(t *testing.T) {
		remote := newScriptedRemote(t, v1Card(false), func(w http.ResponseWriter, call remoteCall) {
			writeRPCResult(w, call.ID, completedV1Task)
		})
		adapter := newV1TestAdapter(t, &proxy.ProxyConfig{
			Name: "p", URL: remote.srv.URL + "/rpc", ProtocolVersion: proxy.ProtocolVersion10,
			AgentCardData: map[string]any{"name": "static"},
		})
		if _, err := adapter.FetchAgentCard(t.Context()); err != nil {
			t.Fatalf("FetchAgentCard: %v", err)
		}
		if err := lastError(forwardAndDrain(t, adapter, &proxy.TaskContext{TaskID: "t"})); err != nil {
			t.Fatalf("downstream error: %v", err)
		}
		calls := remote.recorded()
		if len(calls) != 1 || calls[0].Method != "SendMessage" || calls[0].Header.Get("A2A-Version") != "1.0" {
			t.Errorf("calls = %+v, want SendMessage with A2A-Version 1.0", calls)
		}
	})

	t.Run("a pin no interface satisfies fails the fetch and keeps no negotiation", func(t *testing.T) {
		remote := newScriptedRemote(t, func(base string) map[string]any {
			return map[string]any{"name": "old", "url": base + "/v03", "protocolVersion": "0.3.0"}
		}, func(w http.ResponseWriter, _ remoteCall) { w.WriteHeader(http.StatusNotFound) })
		adapter := newV1TestAdapter(t, &proxy.ProxyConfig{Name: "p", URL: remote.srv.URL, UseAgentCardURL: true, ProtocolVersion: proxy.ProtocolVersion10})
		if _, err := adapter.FetchAgentCard(t.Context()); err == nil || !strings.Contains(err.Error(), "pins 1.0") {
			t.Errorf("FetchAgentCard error = %v, want the pin error", err)
		}
		if adapter.negotiated.Load() != nil {
			t.Error("negotiation stored despite the failed fetch")
		}
	})

	t.Run("before any card is read a 1.0 pin still speaks 1.0", func(t *testing.T) {
		remote := newScriptedRemote(t, v1Card(false), func(w http.ResponseWriter, call remoteCall) {
			writeRPCResult(w, call.ID, completedV1Task)
		})
		adapter := newV1TestAdapter(t, &proxy.ProxyConfig{Name: "p", URL: remote.srv.URL + "/rpc", ProtocolVersion: proxy.ProtocolVersion10})
		if err := lastError(forwardAndDrain(t, adapter, &proxy.TaskContext{TaskID: "t"})); err != nil {
			t.Fatalf("downstream error: %v", err)
		}
		if calls := remote.recorded(); len(calls) != 1 || calls[0].Method != "SendMessage" {
			t.Errorf("calls = %+v, want SendMessage", calls)
		}
	})
}

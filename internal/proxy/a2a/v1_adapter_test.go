// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package a2a

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"iter"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	a2av1 "github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/proxy"
)

type v1Agent struct {
	srv *httptest.Server

	mu      sync.Mutex
	headers []http.Header
}

func newV1Agent(t *testing.T, exec a2asrv.AgentExecutorFunc, extraCardJSON map[string]any) *v1Agent {
	t.Helper()
	agent := &v1Agent{}
	rpc := a2asrv.NewJSONRPCHandler(a2asrv.NewHandler(exec))
	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/agent-card.json", func(w http.ResponseWriter, _ *http.Request) {
		card := map[string]any{
			"name":                "v1-agent",
			"description":         "A2A 1.0 test agent",
			"version":             "1.0.0",
			"capabilities":        map[string]any{"streaming": true},
			"defaultInputModes":   []string{"text/plain"},
			"defaultOutputModes":  []string{"text/plain"},
			"skills":              []any{},
			"supportedInterfaces": []any{map[string]any{"url": agent.srv.URL + "/rpc", "protocolBinding": "JSONRPC", "protocolVersion": "1.0"}},
		}
		for k, v := range extraCardJSON {
			card[k] = v
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(card)
	})
	mux.Handle("/rpc", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		agent.mu.Lock()
		agent.headers = append(agent.headers, r.Header.Clone())
		agent.mu.Unlock()
		rpc.ServeHTTP(w, r)
	}))
	agent.srv = httptest.NewServer(mux)
	t.Cleanup(agent.srv.Close)
	return agent
}

func (a *v1Agent) recordedHeaders() []http.Header {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]http.Header(nil), a.headers...)
}

func sendTextRequest(text string) *a2a.Request {
	return a2a.NewRequest("sam-task-1", "message/send", map[string]any{
		"message": map[string]any{"kind": "message", "messageId": "m1", "role": "user", "parts": []any{map[string]any{"kind": "text", "text": text}}},
	})
}

func completeWith(text string) a2asrv.AgentExecutorFunc {
	return func(_ context.Context, ec *a2asrv.ExecutorContext) iter.Seq2[a2av1.Event, error] {
		return func(yield func(a2av1.Event, error) bool) {
			if !yield(a2av1.NewSubmittedTask(ec, ec.Message), nil) {
				return
			}
			yield(a2av1.NewStatusUpdateEvent(ec, a2av1.TaskStateCompleted, a2av1.NewMessage(a2av1.MessageRoleAgent, a2av1.NewTextPart(text))), nil)
		}
	}
}

func fetchedV1Adapter(t *testing.T, cfg *proxy.ProxyConfig) *A2AAdapter {
	t.Helper()
	adapter := mustNewA2AAdapter(t, cfg)
	t.Cleanup(func() { _ = adapter.Close() })
	if _, err := adapter.FetchAgentCard(t.Context()); err != nil {
		t.Fatalf("FetchAgentCard: %v", err)
	}
	return adapter
}

func TestFetchAgentCard_StampsRemoteProtocolOverTheRemotesOwn(t *testing.T) {
	spoof := map[string]any{"streaming": true, "extensions": []any{map[string]any{
		"uri":    a2a.ExtensionURIRemoteProtocol,
		"params": map[string]any{"protocol_version": "9.9", "deprecated": false},
	}}}
	agent := newV1Agent(t, completeWith("ok"), map[string]any{"capabilities": spoof})
	adapter := mustNewA2AAdapter(t, &proxy.ProxyConfig{Name: "p", URL: agent.srv.URL, AgentCardPath: "/.well-known/agent-card.json", UseAgentCardURL: true})
	defer func() { _ = adapter.Close() }()

	card, err := adapter.FetchAgentCard(t.Context())
	if err != nil {
		t.Fatalf("FetchAgentCard: %v", err)
	}
	var stamped []a2a.AgentExtension
	for _, ext := range card.Capabilities.Extensions {
		if ext.URI == a2a.ExtensionURIRemoteProtocol {
			stamped = append(stamped, ext)
		}
	}
	if len(stamped) != 1 {
		t.Fatalf("remote-protocol extensions = %+v, want exactly the proxy's own", stamped)
	}
	want := map[string]any{"protocol_version": "1.0", "protocol_binding": "JSONRPC", "pin": "auto", "deprecated": false}
	for k, v := range want {
		if stamped[0].Params[k] != v {
			t.Errorf("param %s = %v, want %v", k, stamped[0].Params[k], v)
		}
	}
}

func TestFetchAgentCard_FlagsA03AgentDeprecated(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"name": "old", "url": "https://old.example.com", "protocolVersion": "0.3.0"})
	}))
	defer srv.Close()
	adapter := mustNewA2AAdapter(t, &proxy.ProxyConfig{Name: "p", URL: srv.URL, AgentCardPath: "/.well-known/agent-card.json"})
	defer func() { _ = adapter.Close() }()

	card, err := adapter.FetchAgentCard(t.Context())
	if err != nil {
		t.Fatalf("FetchAgentCard: %v", err)
	}
	ext := card.Capabilities.Extensions[len(card.Capabilities.Extensions)-1]
	if ext.URI != a2a.ExtensionURIRemoteProtocol || ext.Params["protocol_version"] != "0.3" || ext.Params["deprecated"] != true {
		t.Errorf("extension = %+v, want 0.3 flagged deprecated", ext)
	}
}

func TestForwardRequest_V1SendsTheUsersOAuthTokenAndTaskHeaders(t *testing.T) {
	agent := newV1Agent(t, completeWith("done"), nil)
	adapter := fetchedV1Adapter(t, &proxy.ProxyConfig{
		Name:            "p",
		URL:             agent.srv.URL,
		AgentCardPath:   "/.well-known/agent-card.json",
		UseAgentCardURL: true,
		TaskAuth:        &proxy.AuthConfig{Type: "oauth2_authorization_code"},
		TaskHeaders:     []proxy.HeaderConfig{{Name: "X-Tenant", Value: "acme"}, {Name: "Authorization", Value: "Bearer header-loses"}},
	})

	tc := &proxy.TaskContext{TaskID: "sam-task-1", OAuth2Token: "user-token"}
	events, err := adapter.ForwardRequest(t.Context(), tc, sendTextRequest("hi"))
	if err != nil {
		t.Fatalf("ForwardRequest: %v", err)
	}
	final := lastEvent(t, events)
	if final.Error != nil {
		t.Fatalf("downstream error: %v", final.Error)
	}

	headers := agent.recordedHeaders()
	if len(headers) != 1 {
		t.Fatalf("calls = %d, want 1", len(headers))
	}
	if got := headers[0].Get("Authorization"); got != "Bearer user-token" {
		t.Errorf("Authorization = %q, want the user's token over the configured header", got)
	}
	if got := headers[0].Get("X-Tenant"); got != "acme" {
		t.Errorf("X-Tenant = %q", got)
	}
	if got := headers[0].Get("A2A-Version"); got != "1.0" {
		t.Errorf("A2A-Version = %q", got)
	}
	if ri := tc.RemoteInterface(); ri == nil || ri.Version != proxy.ProtocolVersion10 {
		t.Errorf("RemoteInterface = %+v, want the 1.0 interface recorded", ri)
	}
}

func TestCancelTask_V1CancelsOnTheInterfaceTheTaskUsed(t *testing.T) {
	working := make(chan struct{})
	agent := newV1Agent(t, func(ctx context.Context, ec *a2asrv.ExecutorContext) iter.Seq2[a2av1.Event, error] {
		return func(yield func(a2av1.Event, error) bool) {
			if !yield(a2av1.NewSubmittedTask(ec, ec.Message), nil) {
				return
			}
			if !yield(a2av1.NewStatusUpdateEvent(ec, a2av1.TaskStateWorking, nil), nil) {
				return
			}
			close(working)
			<-ctx.Done()
		}
	}, nil)
	adapter := fetchedV1Adapter(t, &proxy.ProxyConfig{Name: "p", URL: agent.srv.URL, AgentCardPath: "/.well-known/agent-card.json", UseAgentCardURL: true})

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	tc := &proxy.TaskContext{TaskID: "sam-task-1", IsStreaming: true}
	events, err := adapter.ForwardRequest(ctx, tc, sendTextRequest("long job"))
	if err != nil {
		t.Fatalf("ForwardRequest: %v", err)
	}
	first := <-events
	if first.Task == nil {
		t.Fatalf("first event = %+v, want the submitted task", first)
	}
	<-working
	tc.DownstreamTaskID = first.Task.ID

	if err := adapter.CancelTask(ctx, tc); err != nil {
		t.Fatalf("CancelTask: %v", err)
	}
	final := lastEvent(t, events)
	if final.StatusUpdate == nil || !final.StatusUpdate.Final || final.StatusUpdate.Status.State != a2a.TaskStateCancelled {
		t.Errorf("last event = %+v, want a final canceled status update", final)
	}
	headers := agent.recordedHeaders()
	if got := headers[len(headers)-1].Get("A2A-Version"); got != "1.0" {
		t.Errorf("cancel A2A-Version = %q", got)
	}
}

func TestTaskAuthTransport_ClientCredentialsRefreshesOn401(t *testing.T) {
	var tokenCalls int
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		tokenCalls++
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": []string{"stale", "fresh"}[min(tokenCalls-1, 1)], "expires_in": 3600})
	}))
	defer tokenSrv.Close()

	var seen []string
	downstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		seen = append(seen, r.Header.Get("Authorization")+" "+string(body))
		if r.Header.Get("Authorization") != "Bearer fresh" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer downstream.Close()

	auth, err := newAuthProvider(&proxy.AuthConfig{Type: "oauth2_client_credentials", TokenURL: tokenSrv.URL, ClientID: "c", ClientSecret: "s", AllowInsecureTokenURL: true}, nil)
	if err != nil {
		t.Fatalf("newAuthProvider: %v", err)
	}
	client := &http.Client{Transport: &taskAuthTransport{base: http.DefaultTransport, auth: auth}}
	req, _ := http.NewRequestWithContext(t.Context(), http.MethodPost, downstream.URL, bytes.NewBufferString(`{"x":1}`))
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	_ = resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200 after the refresh", resp.StatusCode)
	}
	want := []string{`Bearer stale {"x":1}`, `Bearer fresh {"x":1}`}
	if len(seen) != 2 || seen[0] != want[0] || seen[1] != want[1] {
		t.Errorf("requests = %q, want %q", seen, want)
	}
}

func lastEvent(t *testing.T, events <-chan proxy.ResponseEvent) proxy.ResponseEvent {
	t.Helper()
	var last proxy.ResponseEvent
	timeout := time.After(10 * time.Second)
	for {
		select {
		case evt, ok := <-events:
			if !ok {
				return last
			}
			last = evt
		case <-timeout:
			t.Fatal("timed out waiting for the event stream to end")
		}
	}
}

// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package a2a

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/observability"
	"github.com/SolaceProducts/solace-agent-mesh/internal/proxy"
)

var v1Bindings = []struct {
	name     string
	iface    map[string]any
	complete func(w http.ResponseWriter, call remoteCall)
}{
	{"JSON-RPC", iface("/rpc", "JSONRPC", "1.0"), func(w http.ResponseWriter, call remoteCall) {
		writeSSE(w, call, `"result":{"statusUpdate":{"taskId":"remote-1","contextId":"ctx","status":{"state":"TASK_STATE_COMPLETED"}}}`)
	}},
	{"HTTP+JSON", iface("/rest", "HTTP+JSON", "1.0"), func(w http.ResponseWriter, _ remoteCall) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, `data: {"statusUpdate":{"taskId":"remote-1","contextId":"ctx","status":{"state":"TASK_STATE_COMPLETED"}}}`+"\n\n")
	}},
}

func TestForwardRequest_V1ClientCredentialsRefreshOn401(t *testing.T) {
	for _, b := range v1Bindings {
		t.Run(b.name, func(t *testing.T) {
			var tokenCalls atomic.Int32
			tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				token := "stale"
				if tokenCalls.Add(1) > 1 {
					token = "fresh"
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{"access_token": token, "expires_in": 3600})
			}))
			t.Cleanup(tokenSrv.Close)

			remote := newScriptedRemote(t, v1Card(true, b.iface), func(w http.ResponseWriter, call remoteCall) {
				if call.Header.Get("Authorization") != "Bearer fresh" {
					w.WriteHeader(http.StatusUnauthorized)
					return
				}
				b.complete(w, call)
			})
			adapter := newV1TestAdapter(t, &proxy.ProxyConfig{
				Name: "p", URL: remote.srv.URL, UseAgentCardURL: true,
				TaskAuth: &proxy.AuthConfig{Type: "oauth2_client_credentials", TokenURL: tokenSrv.URL, ClientID: "c", ClientSecret: "s", AllowInsecureTokenURL: true},
			})
			if _, err := adapter.FetchAgentCard(t.Context()); err != nil {
				t.Fatalf("FetchAgentCard: %v", err)
			}

			events := forwardAndDrain(t, adapter, &proxy.TaskContext{TaskID: "t", IsStreaming: true})
			if err := lastError(events); err != nil {
				t.Fatalf("downstream error: %v", err)
			}
			calls := remote.recorded()
			if len(calls) != 2 || calls[0].Body != calls[1].Body || calls[1].Header.Get("Authorization") != "Bearer fresh" {
				t.Errorf("calls = %+v, want the same request resent with the refreshed token", calls)
			}
		})
	}

	t.Run("a user's own token is not refreshed", func(t *testing.T) {
		tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "service", "expires_in": 3600})
		}))
		t.Cleanup(tokenSrv.Close)
		remote := newScriptedRemote(t, v1Card(true, iface("/rpc", "JSONRPC", "1.0")), func(w http.ResponseWriter, _ remoteCall) {
			w.WriteHeader(http.StatusUnauthorized)
		})
		adapter := newV1TestAdapter(t, &proxy.ProxyConfig{
			Name: "p", URL: remote.srv.URL, UseAgentCardURL: true,
			TaskAuth: &proxy.AuthConfig{Type: "oauth2_client_credentials", TokenURL: tokenSrv.URL, ClientID: "c", ClientSecret: "s", AllowInsecureTokenURL: true},
		})
		if _, err := adapter.FetchAgentCard(t.Context()); err != nil {
			t.Fatalf("FetchAgentCard: %v", err)
		}

		err := lastError(forwardAndDrain(t, adapter, &proxy.TaskContext{TaskID: "t", IsStreaming: true, OAuth2Token: "user-token"}))
		if got := observability.ClassifyA2AError(err); got != "auth_error" {
			t.Errorf("error = %v labelled %q, want auth_error", err, got)
		}
		calls := remote.recorded()
		if len(calls) != 1 || calls[0].Header.Get("Authorization") != "Bearer user-token" {
			t.Errorf("calls = %+v, want one call with the user's token", calls)
		}
	})
}

func TestForwardRequest_V1URLUserinfoKeepsTaskAuth(t *testing.T) {
	for _, b := range v1Bindings {
		t.Run(b.name, func(t *testing.T) {
			remote := newScriptedRemote(t, func(base string) map[string]any {
				card := v1Card(true, b.iface)(base)
				entry := card["supportedInterfaces"].([]any)[0].(map[string]any)
				entry["url"] = strings.Replace(entry["url"].(string), "http://", "http://user:pw@", 1)
				return card
			}, b.complete)
			adapter := newV1TestAdapter(t, &proxy.ProxyConfig{
				Name: "p", URL: remote.srv.URL, UseAgentCardURL: true,
				TaskAuth: &proxy.AuthConfig{Type: "static_bearer", Token: "SERVICE-TOKEN"},
			})
			if _, err := adapter.FetchAgentCard(t.Context()); err != nil {
				t.Fatalf("FetchAgentCard: %v", err)
			}
			if err := lastError(forwardAndDrain(t, adapter, &proxy.TaskContext{TaskID: "t", IsStreaming: true})); err != nil {
				t.Fatalf("downstream error: %v", err)
			}
			if calls := remote.recorded(); len(calls) != 1 || calls[0].Header.Get("Authorization") != "Bearer SERVICE-TOKEN" {
				t.Errorf("calls = %+v, want the configured bearer token", calls)
			}
		})
	}
}

func TestForwardRequest_V1StopsAtTheStreamsLastEvent(t *testing.T) {
	for state, want := range map[string]a2a.TaskState{"TASK_STATE_COMPLETED": a2a.TaskStateCompleted, "TASK_STATE_INPUT_REQUIRED": a2a.TaskStateInputNeeded} {
		t.Run(state, func(t *testing.T) {
			stall := make(chan struct{})
			remote := newScriptedRemote(t, v1Card(true, iface("/rpc", "JSONRPC", "1.0")), func(w http.ResponseWriter, call remoteCall) {
				writeSSE(w, call, fmt.Sprintf(`"result":{"statusUpdate":{"taskId":"remote-1","contextId":"ctx","status":{"state":%q}}}`, state))
				w.(http.Flusher).Flush()
				<-stall
			})
			t.Cleanup(func() { close(stall) })
			adapter := newV1TestAdapter(t, &proxy.ProxyConfig{Name: "p", URL: remote.srv.URL, UseAgentCardURL: true})
			if _, err := adapter.FetchAgentCard(t.Context()); err != nil {
				t.Fatalf("FetchAgentCard: %v", err)
			}

			start := time.Now()
			events := forwardAndDrain(t, adapter, &proxy.TaskContext{TaskID: "t", IsStreaming: true})
			if elapsed := time.Since(start); elapsed > 2*time.Second {
				t.Errorf("forwarding ended after %v, want it to stop at the last event", elapsed)
			}
			if len(events) != 1 || events[0].StatusUpdate == nil || !events[0].StatusUpdate.Final {
				t.Fatalf("events = %+v, want one final status update", events)
			}
			if got := events[0].StatusUpdate.Status.State; got != want {
				t.Errorf("state = %q, want %q", got, want)
			}
		})
	}
}

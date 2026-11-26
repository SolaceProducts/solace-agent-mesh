// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package slack

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

func initTestCore(t *testing.T) (*entrypoint.GatewayCore, *httptest.Server) {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/auth.test", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "user_id": "U-bot"})
	})
	mux.HandleFunc("/users.profile.get", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "profile": map[string]any{"email": ""}})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatalf("connect dev broker: %v", err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })

	core := entrypoint.NewGatewayCore(&entrypoint.LoggingAdapter{}, map[string]any{
		"namespace":  "test-ns",
		"gateway_id": "test-gw",
	})
	rt := runtime.NewRuntime(brk, nil)
	core.SetServices(rt.Services())
	if err := core.Init(t.Context(), nil); err != nil {
		t.Fatalf("core.Init: %v", err)
	}
	return core, srv
}

func TestAdapterConstruction(t *testing.T) {
	t.Parallel()

	adapters := entrypoint.RegisteredAdapters()
	found := false
	for _, name := range adapters {
		if name == "slack" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("slack adapter not registered; registered adapters: %v", adapters)
	}

	adapter, err := NewSlackGatewayAdapter(map[string]any{"slack_bot_token": "xoxb-test", "slack_app_token": "xapp-test"})
	if err != nil {
		t.Fatalf("NewSlackGatewayAdapter: %v", err)
	}
	if adapter.queues == nil {
		t.Fatal("queues map not initialized")
	}
	if spec := adapter.MountIntent(); spec.Kind != entrypoint.MountNone {
		t.Fatalf("MountIntent kind = %v, want MountNone", spec.Kind)
	}
}

func TestInit_RateLimiting(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		disableRL   bool
		wantDisable bool
	}{
		{"Disabled", true, true},
		{"Enabled", false, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			core, srv := initTestCore(t)

			adapter, err := NewSlackGatewayAdapter(map[string]any{
				"slack_bot_token":       "xoxb-test",
				"slack_app_token":       "xapp-test",
				"slack_api_url":         srv.URL + "/",
				"disable_rate_limiting": tc.disableRL,
			})
			if err != nil {
				t.Fatalf("NewSlackGatewayAdapter: %v", err)
			}
			if err := adapter.Init(t.Context(), core); err != nil {
				t.Fatalf("Init: %v", err)
			}
			if adapter.api == nil {
				t.Fatal("api not initialized")
			}
			if adapter.cfg.DisableRateLimiting != tc.wantDisable {
				t.Fatalf("DisableRateLimiting = %v, want %v", adapter.cfg.DisableRateLimiting, tc.wantDisable)
			}
		})
	}
}

func TestResolveBotIdentity(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name         string
		authTest     func(w http.ResponseWriter, r *http.Request)
		wantUserID   string
		wantBotID    string
		wantResolved bool
	}{
		{
			name: "Success",
			authTest: func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "user_id": "U-bot", "bot_id": "B-self"})
			},
			wantUserID: "U-bot", wantBotID: "B-self", wantResolved: true,
		},
		{
			name: "AuthTestError",
			authTest: func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "invalid_auth"})
			},
			wantResolved: false,
		},
		{
			name: "EmptyUserID",
			authTest: func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "user_id": ""})
			},
			wantResolved: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			core, _ := initTestCore(t)

			mux := http.NewServeMux()
			mux.HandleFunc("/auth.test", tc.authTest)
			srv := httptest.NewServer(mux)
			t.Cleanup(srv.Close)

			adapter, err := NewSlackGatewayAdapter(map[string]any{
				"slack_bot_token":       "xoxb-test",
				"slack_app_token":       "xapp-test",
				"slack_api_url":         srv.URL + "/",
				"disable_rate_limiting": true,
			})
			if err != nil {
				t.Fatalf("NewSlackGatewayAdapter: %v", err)
			}
			if err := adapter.Init(t.Context(), core); err != nil {
				t.Fatalf("Init: %v", err)
			}
			if err := adapter.Start(t.Context()); err != nil {
				t.Fatalf("Start: %v", err)
			}
			t.Cleanup(func() { _ = adapter.Stop(context.Background()) })

			if adapter.identityResolved != tc.wantResolved {
				t.Errorf("identityResolved = %v, want %v", adapter.identityResolved, tc.wantResolved)
			}
			if adapter.botUserID != tc.wantUserID {
				t.Errorf("botUserID = %q, want %q", adapter.botUserID, tc.wantUserID)
			}
			if adapter.botID != tc.wantBotID {
				t.Errorf("botID = %q, want %q", adapter.botID, tc.wantBotID)
			}
		})
	}
}

func TestStart_SocketModeRefusedAuthIsNotRetriedAndStopReturns(t *testing.T) {
	t.Parallel()
	core, _ := initTestCore(t)

	var opens atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/auth.test", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "user_id": "U-bot"})
	})
	mux.HandleFunc("/apps.connections.open", func(w http.ResponseWriter, _ *http.Request) {
		opens.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "invalid_auth"})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	adapter, err := NewSlackGatewayAdapter(map[string]any{
		"slack_bot_token":       "xoxb-test",
		"slack_app_token":       "xapp-test",
		"slack_api_url":         srv.URL + "/",
		"force_socket_mode":     true,
		"disable_rate_limiting": true,
	})
	if err != nil {
		t.Fatalf("NewSlackGatewayAdapter: %v", err)
	}
	if err := adapter.Init(t.Context(), core); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if adapter.socketClient == nil {
		t.Fatal("force_socket_mode did not create a socket client")
	}
	if err := adapter.Start(t.Context()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && opens.Load() == 0 {
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(200 * time.Millisecond)

	stopped := make(chan error, 1)
	go func() { stopped <- adapter.Stop(context.Background()) }()
	select {
	case err := <-stopped:
		if err != nil {
			t.Fatalf("Stop: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Stop did not return after the socket mode client gave up")
	}
	if got := opens.Load(); got != 1 {
		t.Fatalf("apps.connections.open calls = %d, want 1: a refused app token is fatal and must not be retried", got)
	}
}

func TestStartStop_NoSocketClient(t *testing.T) {
	t.Parallel()
	core, srv := initTestCore(t)

	adapter, err := NewSlackGatewayAdapter(map[string]any{
		"slack_bot_token":       "xoxb-test",
		"slack_app_token":       "xapp-test",
		"slack_api_url":         srv.URL + "/",
		"disable_rate_limiting": true,
	})
	if err != nil {
		t.Fatalf("NewSlackGatewayAdapter: %v", err)
	}
	if err := adapter.Init(t.Context(), core); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := adapter.Start(t.Context()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := adapter.Stop(t.Context()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}

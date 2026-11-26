// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

func newDBBackedCore(t *testing.T, brk broker.Broker, databaseURL string) *entrypoint.GatewayCore {
	t.Helper()
	adapter := NewHTTPSSEAdapter()
	appConfig := map[string]any{
		"namespace":             "test-ns",
		"session_secret_key":    "test-secret",
		"gateway_id":            "test-gw",
		"fastapi_host":          "127.0.0.1",
		"fastapi_port":          0,
		"default_user_identity": "sam_dev_user",
		"session_service": map[string]any{
			"type":         "sql",
			"database_url": databaseURL,
		},
	}
	core := entrypoint.NewGatewayCore(adapter, appConfig)

	flagRegistry := features.NewRegistry()
	if err := features.LoadFlags(flagRegistry); err != nil {
		t.Fatalf("load flags: %v", err)
	}
	rt := runtime.NewRuntime(brk, nil,
		runtime.WithLogger(slog.Default()),
		runtime.WithFeatureRegistry(flagRegistry),
	)
	core.SetServices(rt.Services())
	if err := core.Init(context.Background(), nil); err != nil {
		t.Fatalf("core.Init: %v", err)
	}
	if core.Store() == nil {
		t.Fatal("expected DB-backed core, got nil store")
	}
	t.Cleanup(func() { _ = core.Store().Close() })
	return core
}

func TestOAuthCallback_DBBacked_CrossPodHandoff(t *testing.T) {
	brk := broker.NewDevBroker()
	if err := brk.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })

	dbURL := testDBURL(t, "tool-auth-xpod")
	initiator := newDBBackedCore(t, brk, dbURL)
	responder := newDBBackedCore(t, brk, dbURL)

	ctx := context.Background()
	const agentName = "agent-x"

	q, err := brk.CreateDirectSubscription()
	if err != nil {
		t.Fatalf("create subscription: %v", err)
	}
	if subErr := brk.Subscribe(q, a2a.AgentRequestTopic("test-ns", agentName)); subErr != nil {
		t.Fatalf("subscribe: %v", subErr)
	}

	stateData, err := json.Marshal(&entrypoint.ToolAuthState{
		ToolType:       entrypoint.ToolAuthTypeMCP,
		TargetAgent:    agentName,
		TaskID:         "task-1",
		UserID:         "u1",
		ClientID:       "test-gw",
		FunctionCallID: "call-1",
		CredentialKey:  "tool__oauth2",
		CodeVerifier:   "verifier-1",
	})
	if err != nil {
		t.Fatalf("marshal state: %v", err)
	}
	now := time.Now().UnixMilli()
	if putErr := store.NewToolAuthStateRepo(initiator.Store()).Put(ctx, "STATE-XPOD", stateData, now+600_000, now); putErr != nil {
		t.Fatalf("seed state on initiator: %v", putErr)
	}

	handler := &oauthCallbackHandler{core: responder, logger: slog.Default()}
	req := httptest.NewRequest("GET", "/api/v1/auth/tool/callback?code=AUTH-CODE-1&state=STATE-XPOD", nil)
	w := httptest.NewRecorder()
	handler.handleCallback(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html success page", ct)
	}

	rctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	msg, err := brk.Receive(rctx, q)
	if err != nil {
		t.Fatalf("expected auth_response dispatched to agent: %v", err)
	}
	if !strings.Contains(string(msg.Payload), "AUTH-CODE-1") || !strings.Contains(string(msg.Payload), "auth_response") {
		t.Errorf("dispatched payload missing auth_response/code: %s", string(msg.Payload))
	}

	if _, ok := responder.TakeToolAuthState(ctx, "STATE-XPOD"); ok {
		t.Error("state should be consumed after a successful callback (replay must miss)")
	}
}

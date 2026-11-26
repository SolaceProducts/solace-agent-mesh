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
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
)

func publishToolAuthRegistration(t *testing.T, brk broker.Broker, reg a2a.ToolAuthStateRegistration) {
	t.Helper()
	payload, err := json.Marshal(reg)
	if err != nil {
		t.Fatal(err)
	}
	if err := brk.PublishGuaranteed(context.Background(), a2a.ToolAuthRegisterTopic("test-ns"),
		&broker.Message{Payload: payload, UserProperties: a2a.EncodeUserProperties(&a2a.UserProperties{ClientID: "platform"})}); err != nil {
		t.Fatal(err)
	}
}

func TestOAuthCallback_PlatformSeededState_CompletesFlow(t *testing.T) {
	brk := broker.NewDevBroker()
	if err := brk.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })

	dbURL := testDBURL(t, "tool-auth-register")
	seeder := newDBBackedCore(t, brk, dbURL)
	responder := newDBBackedCore(t, brk, dbURL)

	const (
		agentName   = "agent-x"
		callbackURL = "/api/v1/auth/tool/callback?code=AUTH-CODE-1&state=STATE-SEEDED"
	)

	q, err := brk.CreateDirectSubscription()
	if err != nil {
		t.Fatalf("create subscription: %v", err)
	}
	if subErr := brk.Subscribe(q, a2a.AgentRequestTopic("test-ns", agentName)); subErr != nil {
		t.Fatalf("subscribe: %v", subErr)
	}

	seeder.StartToolAuthRegisterConsumer(t.Context(), slog.Default())
	publishToolAuthRegistration(t, brk, a2a.ToolAuthStateRegistration{
		State:          "STATE-SEEDED",
		TargetAgent:    agentName,
		TaskID:         "task-1",
		UserID:         "system:eval",
		ClientID:       "platform",
		FunctionCallID: "call-1",
		CredentialKey:  "tool__oauth2",
		CodeVerifier:   "verifier-1",
	})

	handler := &oauthCallbackHandler{core: responder, logger: slog.Default()}
	var w *httptest.ResponseRecorder
	for range 200 {
		w = httptest.NewRecorder()
		handler.handleCallback(w, httptest.NewRequest("GET", callbackURL, nil))
		if w.Code == http.StatusOK {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", w.Code, w.Body.String())
	}

	rctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	msg, err := brk.Receive(rctx, q)
	if err != nil {
		t.Fatalf("expected auth_response dispatched to agent: %v", err)
	}
	authResp := decodeAuthResponseData(t, msg.Payload)

	wantFields := map[string]any{
		"type":                   "auth_response",
		"target_agent":           agentName,
		"target_agent_task_id":   "task-1",
		"user_id":                "system:eval",
		"client_id":              "platform",
		"function_call_id":       "call-1",
		"credential_key":         "tool__oauth2",
		"code_verifier":          "verifier-1",
		"code":                   "AUTH-CODE-1",
		"state":                  "STATE-SEEDED",
		"is_mcp_credential_save": true,
	}
	for key, want := range wantFields {
		if got := authResp[key]; got != want {
			t.Errorf("auth_response[%q] = %v, want %v", key, got, want)
		}
	}

	replayW := httptest.NewRecorder()
	handler.handleCallback(replayW, httptest.NewRequest("GET", callbackURL, nil))
	if replayW.Code != http.StatusBadRequest {
		t.Errorf("replayed callback status = %d, want 400", replayW.Code)
	}
}

func decodeAuthResponseData(t *testing.T, payload []byte) map[string]any {
	t.Helper()
	var env struct {
		Params struct {
			Message struct {
				Parts []struct {
					Kind string         `json:"kind"`
					Data map[string]any `json:"data"`
				} `json:"parts"`
			} `json:"message"`
		} `json:"params"`
	}
	if err := json.Unmarshal(payload, &env); err != nil {
		t.Fatalf("unmarshal auth_response envelope: %v (payload=%s)", err, string(payload))
	}
	for _, part := range env.Params.Message.Parts {
		if part.Kind == "data" && part.Data["type"] == "auth_response" {
			return part.Data
		}
	}
	t.Fatalf("no auth_response data part in payload: %s", string(payload))
	return nil
}

func TestHTTPSSEAdapterStart_BindsToolAuthRegisterQueue(t *testing.T) {
	adapter, core, _, _, _ := setupTestAdapterWithDB(t)
	if err := adapter.Start(t.Context()); err != nil {
		t.Fatalf("adapter.Start: %v", err)
	}
	t.Cleanup(func() { _ = adapter.Stop(context.Background()) })

	brk := core.Services().Broker()
	publishToolAuthRegistration(t, brk, a2a.ToolAuthStateRegistration{
		State:         "STATE-ADAPTER-START",
		TargetAgent:   "agent-x",
		TaskID:        "task-1",
		UserID:        "system:eval",
		CredentialKey: "tool__oauth2",
	})

	var got *entrypoint.ToolAuthState
	for range 200 {
		var ok bool
		if got, ok = core.TakeToolAuthState(context.Background(), "STATE-ADAPTER-START"); ok {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got == nil {
		t.Fatal("adapter.Start did not bind the tool auth register queue")
	}
	if got.UserID != "system:eval" {
		t.Errorf("UserID = %q, want system:eval", got.UserID)
	}
}

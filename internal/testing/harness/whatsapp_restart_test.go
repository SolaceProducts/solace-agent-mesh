// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package harness

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact/memory"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

func TestWhatsAppRestart_CollectsAFreshTurn(t *testing.T) {
	ctx := t.Context()
	gw := startWhatsAppGateway(t)

	const namespace, gatewayID = "test-ns", "test-gw-whatsapp"
	publishFinalResponse(t, gw.brk, namespace, gatewayID)

	before, err := gw.CollectEvents(ctx, 3*time.Second)
	if err != nil {
		t.Fatalf("pre-restart CollectEvents: %v", err)
	}
	if len(before) == 0 {
		t.Fatal("pre-restart CollectEvents returned no events")
	}

	if restartErr := gw.Restart(ctx); restartErr != nil {
		t.Fatalf("Restart: %v", restartErr)
	}

	start := time.Now()
	after, err := gw.CollectEvents(ctx, 500*time.Millisecond)
	if err == nil {
		t.Error("post-restart CollectEvents reported a final response with nothing published since the restart")
	}
	if len(after) != 0 {
		t.Errorf("post-restart CollectEvents returned %d stale pre-restart events", len(after))
	}
	if elapsed := time.Since(start); elapsed < 400*time.Millisecond {
		t.Errorf("post-restart CollectEvents returned after %v; it did not wait for its own turn", elapsed)
	}

	publishFinalResponse(t, gw.brk, namespace, gatewayID)
	fresh, err := gw.CollectEvents(ctx, 3*time.Second)
	if err != nil {
		t.Fatalf("post-restart CollectEvents after a fresh publish: %v", err)
	}
	if len(fresh) == 0 {
		t.Error("the restarted gateway collected nothing from a fresh publish")
	}
}

func TestWhatsAppRestart_PreservesTheMockGraphSide(t *testing.T) {
	ctx := t.Context()
	gw := startWhatsAppGateway(t)

	gw.MockServer().RegisterMedia("MEDIA1", []byte("bytes"), "image/jpeg")
	sendAsAdapter(t, gw.MockServer(),
		`{"messaging_product":"whatsapp","to":"15551230000","type":"text","text":{"body":"before"}}`)

	if err := gw.Restart(ctx); err != nil {
		t.Fatalf("Restart: %v", err)
	}

	if got := len(gw.WhatsAppCalls()); got != 1 {
		t.Errorf("captured calls after restart = %d, want the 1 sent before it", got)
	}
	gw.MockServer().mu.Lock()
	_, mediaKept := gw.MockServer().media["MEDIA1"]
	gw.MockServer().mu.Unlock()
	if !mediaKept {
		t.Error("registered media did not survive the restart")
	}
}

func sendAsAdapter(t *testing.T, m *MockWhatsAppServer, body string) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, m.URL()+"/v26.0/x/messages", strings.NewReader(body))
	if err != nil {
		t.Fatalf("build send: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+whatsappTestAccessToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	defer resp.Body.Close() //nolint:errcheck // test
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("send status = %d, want 200", resp.StatusCode)
	}
}

func startWhatsAppGateway(t *testing.T) *WhatsAppTestGateway {
	t.Helper()
	ctx := t.Context()
	brk := broker.NewDevBroker()
	if err := brk.Connect(ctx); err != nil {
		t.Fatalf("connect broker: %v", err)
	}
	gw := NewWhatsAppTestGateway(brk, &WhatsAppTestConfig{DefaultAgent: "test_agent"},
		"test-ns", "test-gw-whatsapp", slog.New(slog.NewTextHandler(io.Discard, nil)))
	gw.SetServices(&testServices{
		brk:  brk,
		arts: &artifact.ScopedService{Inner: memory.New(), Namespace: "test-ns"},
	})
	if err := gw.Setup(ctx); err != nil {
		t.Fatalf("whatsapp gateway setup: %v", err)
	}
	t.Cleanup(gw.Cleanup)
	return gw
}

func publishFinalResponse(t *testing.T, brk broker.Broker, namespace, gatewayID string) {
	t.Helper()
	taskID := idgen.New()
	payload, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      taskID,
		"result": map[string]any{
			"kind":   "task",
			"id":     taskID,
			"status": map[string]any{"state": "completed"},
		},
	})
	if err != nil {
		t.Fatalf("marshal final response: %v", err)
	}
	topic := a2a.GatewayResponseTopic(namespace, gatewayID, taskID)
	if err := brk.Publish(t.Context(), topic, &broker.Message{Payload: payload}); err != nil {
		t.Fatalf("publish final response: %v", err)
	}
}

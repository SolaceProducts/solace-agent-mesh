// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
)

type captured struct {
	topic   string
	payload []byte
	props   map[string]string
}

func subscribeAndCaptureResearchEvent(t *testing.T, brk broker.Broker, topic string) (chan *captured, func()) {
	t.Helper()
	q, err := brk.CreateDirectSubscription()
	if err != nil {
		t.Fatalf("create direct sub: %v", err)
	}
	if err := brk.Subscribe(q, topic); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	out := make(chan *captured, 1)
	stop := make(chan struct{})
	var once sync.Once
	go func() {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go func() {
			<-stop
			cancel()
		}()
		for {
			msg, recvErr := brk.Receive(ctx, q)
			if recvErr != nil || ctx.Err() != nil {
				return
			}
			if msg == nil {
				continue
			}
			once.Do(func() {
				out <- &captured{topic: msg.Topic, payload: msg.Payload, props: msg.UserProperties}
			})
			msg.Ack()
		}
	}()
	return out, func() { close(stop); _ = brk.DeleteQueue(q.Name()) }
}

func TestResearchHandler_PublishesPlanResponseSignal(t *testing.T) {
	adapter, _, brk := setupTestAdapter(t)

	topic := a2a.SamEventsTopic("test-ns", "deep_research", "plan_response")
	got, stop := subscribeAndCaptureResearchEvent(t, brk, topic)
	defer stop()

	body, _ := json.Marshal(map[string]any{
		"planId":    "plan-abc",
		"agentName": "ResearchAgent",
		"action":    "start",
		"steps":     []string{"edited step"},
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/research/planResponse", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	adapter.handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var resp PlanResponseResult
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v (body=%s)", err, rec.Body.String())
	}
	if resp.Status != "accepted" || resp.PlanID != "plan-abc" || resp.Action != "start" {
		t.Errorf("response wrong: %+v", resp)
	}

	select {
	case c := <-got:
		if c.topic != topic {
			t.Errorf("captured topic = %q, want %q", c.topic, topic)
		}
		var env map[string]any
		if err := json.Unmarshal(c.payload, &env); err != nil {
			t.Fatalf("decode published payload: %v", err)
		}
		if env["event_type"] != "plan_response" {
			t.Errorf("event_type = %v", env["event_type"])
		}
		data, ok := env["data"].(map[string]any)
		if !ok {
			t.Fatalf("data not a map: %T", env["data"])
		}
		if data["plan_id"] != "plan-abc" || data["agent_name"] != "ResearchAgent" || data["action"] != "start" {
			t.Errorf("data fields wrong: %+v", data)
		}
		steps, ok := data["steps"].([]any)
		if !ok || len(steps) != 1 || steps[0] != "edited step" {
			t.Errorf("steps wrong: %+v", data["steps"])
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for published plan_response")
	}
}

func TestResearchHandler_RejectsMissingPlanID(t *testing.T) {
	adapter, _, _ := setupTestAdapter(t)

	body, _ := json.Marshal(map[string]any{
		"agentName": "ResearchAgent",
		"action":    "start",
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/research/planResponse", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	adapter.handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d (body=%s)", rec.Code, rec.Body.String())
	}
}

func TestResearchHandler_RejectsMissingAgentName(t *testing.T) {
	adapter, _, _ := setupTestAdapter(t)

	body, _ := json.Marshal(map[string]any{
		"planId": "plan-1",
		"action": "start",
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/research/planResponse", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	adapter.handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d (body=%s)", rec.Code, rec.Body.String())
	}
}

func TestResearchHandler_RejectsCancelAction(t *testing.T) {
	adapter, _, _ := setupTestAdapter(t)

	body, _ := json.Marshal(map[string]any{
		"planId":    "plan-1",
		"agentName": "ResearchAgent",
		"action":    "cancel",
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/research/planResponse", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	adapter.handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("expected 422, got %d (body=%s)", rec.Code, rec.Body.String())
	}
}

func TestHTTPSSEAdapter_CapabilitiesAdvertisesPlanVerification(t *testing.T) {
	a := NewHTTPSSEAdapter()
	caps := a.Capabilities()
	if !caps["interactive_plan_verification"] {
		t.Errorf("HTTPSSEAdapter should advertise interactive_plan_verification, got %#v", caps)
	}
}

func TestGatewayConfig_CapabilitiesMergedFromAdapter(t *testing.T) {
	_, core, _ := setupTestAdapter(t)
	caps := core.Config().Capabilities
	if !caps["interactive_plan_verification"] {
		t.Errorf("expected gateway config Capabilities to include interactive_plan_verification, got %#v", caps)
	}
}

func TestGatewayConfig_ConfigSideOverridesAdapter(t *testing.T) {
	cfg, err := entrypoint.ParseGatewayConfig(map[string]any{
		"namespace":  "test-ns",
		"gateway_id": "test-gw",
		"gateway_capabilities": map[string]any{
			"interactive_plan_verification": false,
		},
	})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	adapterCaps := NewHTTPSSEAdapter().Capabilities()
	for k, v := range adapterCaps {
		if _, exists := cfg.Capabilities[k]; !exists {
			cfg.Capabilities[k] = v
		}
	}
	if cfg.Capabilities["interactive_plan_verification"] {
		t.Errorf("config override should win, got %#v", cfg.Capabilities)
	}
}

func TestResearchHandler_DefaultsActionToStart(t *testing.T) {
	adapter, _, brk := setupTestAdapter(t)

	topic := a2a.SamEventsTopic("test-ns", "deep_research", "plan_response")
	got, stop := subscribeAndCaptureResearchEvent(t, brk, topic)
	defer stop()

	body, _ := json.Marshal(map[string]any{
		"planId":    "plan-1",
		"agentName": "ResearchAgent",
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/research/planResponse", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	adapter.handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	select {
	case <-got:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for published plan_response on default action")
	}
}

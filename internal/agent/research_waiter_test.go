// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
)

func TestDispatchDeepResearchEvent_ResolvesWaiter(t *testing.T) {
	agent, _, _ := newTestAgent()
	agent.cfg.Name = "ResearchAgent"

	planID := "plan-1"
	userID := "user-1"
	ch := agent.planWaiters.Register(planID, userID)

	envelope, err := json.Marshal(deepResearchEventEnvelope{
		EventType: "plan_response",
		Data: &deepResearchEventData{
			PlanID:    planID,
			AgentName: "ResearchAgent",
			UserID:    userID,
			Action:    "start",
			Steps:     []string{"edited-step"},
		},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	msg := broker.NewDeliveredMessage("id", "test-ns/sam/events/deep_research/plan_response", envelope, func() {}, func() {})
	agent.dispatchDeepResearchEvent(msg)

	select {
	case got := <-ch:
		if got.Action != "start" || len(got.Steps) != 1 || got.Steps[0] != "edited-step" {
			t.Errorf("resolved with unexpected data: %+v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("dispatcher did not resolve the waiter")
	}
}

func TestDispatchDeepResearchEvent_DropsForeignAgent(t *testing.T) {
	agent, _, _ := newTestAgent()
	agent.cfg.Name = "AgentA"

	ch := agent.planWaiters.Register("plan-1", "user-1")

	envelope, err := json.Marshal(deepResearchEventEnvelope{
		EventType: "plan_response",
		Data: &deepResearchEventData{
			PlanID:    "plan-1",
			AgentName: "AgentB",
			UserID:    "user-1",
			Action:    "start",
		},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	msg := broker.NewDeliveredMessage("id", "test-ns/sam/events/deep_research/plan_response", envelope, func() {}, func() {})
	agent.dispatchDeepResearchEvent(msg)

	select {
	case got := <-ch:
		t.Errorf("unexpected resolution for foreign agent: %+v", got)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestDispatchDeepResearchEvent_RejectsMalformedAndUnknownEventType(t *testing.T) {
	agent, _, _ := newTestAgent()
	agent.cfg.Name = "ResearchAgent"
	ch := agent.planWaiters.Register("plan-1", "user-1")

	cases := []struct {
		name    string
		payload []byte
	}{
		{"non-json", []byte("not json")},
		{"unknown event_type", []byte(`{"event_type":"something_else","data":{"plan_id":"plan-1","user_id":"user-1","agent_name":"ResearchAgent","action":"start"}}`)},
		{"missing data block", []byte(`{"event_type":"plan_response"}`)},
		{"missing plan_id", []byte(`{"event_type":"plan_response","data":{"user_id":"u","agent_name":"ResearchAgent","action":"start"}}`)},
		{"wrong action", []byte(`{"event_type":"plan_response","data":{"plan_id":"plan-1","user_id":"user-1","agent_name":"ResearchAgent","action":"other"}}`)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			msg := broker.NewDeliveredMessage("id", "test-ns/sam/events/deep_research/plan_response", tc.payload, func() {}, func() {})
			agent.dispatchDeepResearchEvent(msg)
		})
	}
	select {
	case got := <-ch:
		t.Errorf("waiter resolved unexpectedly: %+v", got)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestDeepResearchEventEnvelope_PythonWireFormat(t *testing.T) {
	raw := []byte(`{
		"event_type": "plan_response",
		"source_component": "webui_gateway",
		"data": {
			"plan_id": "plan-abc",
			"agent_name": "ResearchAgent",
			"user_id": "user-1",
			"action": "start",
			"steps": ["edited step"]
		}
	}`)
	var env deepResearchEventEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if env.EventType != "plan_response" {
		t.Errorf("EventType = %q", env.EventType)
	}
	if env.Data == nil {
		t.Fatalf("Data is nil")
	}
	if env.Data.PlanID != "plan-abc" || env.Data.AgentName != "ResearchAgent" || env.Data.UserID != "user-1" || env.Data.Action != "start" {
		t.Errorf("Data fields wrong: %+v", env.Data)
	}
	if len(env.Data.Steps) != 1 || env.Data.Steps[0] != "edited step" {
		t.Errorf("Steps = %#v", env.Data.Steps)
	}
}

func TestPlanWaiterRegistry_RegisterResolve(t *testing.T) {
	r := newPlanWaiterRegistry()
	ch := r.Register("plan-1", "user-1")
	go func() {
		_ = r.Resolve("plan-1", "user-1", &DeepResearchPlanResponse{Action: "start", Steps: []string{"a"}})
	}()
	select {
	case got := <-ch:
		if got.Action != "start" || len(got.Steps) != 1 || got.Steps[0] != "a" {
			t.Errorf("unexpected response: %+v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("waiter timed out")
	}
}

func TestPlanWaiterRegistry_ResolveUnknownReturnsFalse(t *testing.T) {
	r := newPlanWaiterRegistry()
	if r.Resolve("nope", "u", &DeepResearchPlanResponse{Action: "start"}) {
		t.Errorf("expected Resolve on unknown plan_id to return false")
	}
}

func TestPlanWaiterRegistry_RejectsUserMismatch(t *testing.T) {
	r := newPlanWaiterRegistry()
	_ = r.Register("plan-1", "user-1")
	if r.Resolve("plan-1", "user-2", &DeepResearchPlanResponse{Action: "start"}) {
		t.Errorf("expected user-mismatch resolution to be rejected")
	}
}

func TestPlanWaiterRegistry_DropMakesResolveFail(t *testing.T) {
	r := newPlanWaiterRegistry()
	_ = r.Register("plan-1", "u")
	r.Drop("plan-1")
	if r.Resolve("plan-1", "u", &DeepResearchPlanResponse{Action: "start"}) {
		t.Errorf("Resolve on dropped waiter should return false")
	}
}

func TestPlanWaiterRegistry_DoubleResolveOnlyOnce(t *testing.T) {
	r := newPlanWaiterRegistry()
	ch := r.Register("plan-1", "u")
	first := r.Resolve("plan-1", "u", &DeepResearchPlanResponse{Action: "start", Steps: []string{"first"}})
	second := r.Resolve("plan-1", "u", &DeepResearchPlanResponse{Action: "start", Steps: []string{"second"}})
	if !first || second {
		t.Errorf("expected first=true second=false, got first=%v second=%v", first, second)
	}
	select {
	case got, ok := <-ch:
		if !ok {
			t.Fatal("channel closed unexpectedly")
		}
		if len(got.Steps) != 1 || got.Steps[0] != "first" {
			t.Errorf("unexpected response: %+v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("waiter timed out")
	}
	if _, ok := <-ch; ok {
		t.Errorf("channel should be closed after delivery")
	}
}

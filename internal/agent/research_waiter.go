// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"encoding/json"
	"sync"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
)

type DeepResearchPlanResponse struct {
	Action string   `json:"action"`
	Steps  []string `json:"steps,omitempty"`
}

type planWaiter struct {
	userID string
	ch     chan *DeepResearchPlanResponse
}

type planWaiterRegistry struct {
	mu      sync.Mutex
	waiters map[string]*planWaiter
}

func newPlanWaiterRegistry() *planWaiterRegistry {
	return &planWaiterRegistry{waiters: make(map[string]*planWaiter)}
}

func (r *planWaiterRegistry) Register(planID, userID string) <-chan *DeepResearchPlanResponse {
	ch := make(chan *DeepResearchPlanResponse, 1)
	r.mu.Lock()
	r.waiters[planID] = &planWaiter{userID: userID, ch: ch}
	r.mu.Unlock()
	return ch
}

func (r *planWaiterRegistry) Drop(planID string) {
	r.mu.Lock()
	delete(r.waiters, planID)
	r.mu.Unlock()
}

func (r *planWaiterRegistry) Resolve(planID, userID string, resp *DeepResearchPlanResponse) bool {
	r.mu.Lock()
	w, ok := r.waiters[planID]
	if !ok {
		r.mu.Unlock()
		return false
	}
	if w.userID != userID {
		r.mu.Unlock()
		return false
	}
	delete(r.waiters, planID)
	r.mu.Unlock()
	w.ch <- resp
	close(w.ch)
	return true
}

type deepResearchEventEnvelope struct {
	EventType       string                 `json:"event_type"`
	SourceComponent string                 `json:"source_component,omitempty"`
	Data            *deepResearchEventData `json:"data,omitempty"`
}

type deepResearchEventData struct {
	PlanID    string   `json:"plan_id"`
	AgentName string   `json:"agent_name"`
	UserID    string   `json:"user_id"`
	Action    string   `json:"action"`
	Steps     []string `json:"steps,omitempty"`
}

func (a *Agent) dispatchDeepResearchEvent(msg *broker.Message) {
	defer msg.Ack()
	var env deepResearchEventEnvelope
	if err := json.Unmarshal(msg.Payload, &env); err != nil {
		a.logger.Debug("deep_research event: malformed payload", "error", err)
		return
	}
	if env.EventType != "plan_response" {
		a.logger.Debug("deep_research event: unknown event_type", "eventType", env.EventType)
		return
	}
	if env.Data == nil {
		a.logger.Debug("deep_research event: missing data block")
		return
	}
	if env.Data.AgentName != "" && env.Data.AgentName != a.cfg.Name {
		return
	}
	if env.Data.PlanID == "" || env.Data.UserID == "" || env.Data.Action != "start" {
		a.logger.Warn("deep_research plan_response: malformed",
			"planIDPresent", env.Data.PlanID != "",
			"userIDPresent", env.Data.UserID != "",
			"action", env.Data.Action,
		)
		return
	}
	resolved := a.planWaiters.Resolve(env.Data.PlanID, env.Data.UserID, &DeepResearchPlanResponse{
		Action: env.Data.Action,
		Steps:  env.Data.Steps,
	})
	if !resolved {
		a.logger.Debug("deep_research plan_response: no matching waiter",
			"planID", env.Data.PlanID,
		)
	}
}

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
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
)

const planResponsePublishTimeout = 5 * time.Second

type researchHandler struct {
	core   *entrypoint.GatewayCore
	logger *slog.Logger
}

type PlanResponseRequest struct {
	PlanID    string   `json:"planId"`
	AgentName string   `json:"agentName"`
	Action    string   `json:"action,omitempty"`
	Steps     []string `json:"steps,omitempty"`
}

type PlanResponseResult struct {
	Status string `json:"status"`
	PlanID string `json:"planId"`
	Action string `json:"action"`
}

func (h *researchHandler) handleSubmit(w http.ResponseWriter, r *http.Request) {
	var req PlanResponseRequest
	if err := decodeJSON(r, &req); err != nil {
		badRequestFromDecode(w, err)
		return
	}
	if req.Action == "" {
		req.Action = "start"
	}
	if req.Action != "start" {
		writeError(w, http.StatusUnprocessableEntity, "action must be 'start'")
		return
	}
	if req.PlanID == "" {
		writeError(w, http.StatusBadRequest, "planId is required")
		return
	}
	if req.AgentName == "" {
		writeError(w, http.StatusBadRequest, "agentName is required")
		return
	}

	userID := resolveUserID(r, h.core)
	if userID == "" {
		writeError(w, http.StatusUnauthorized, "authenticated user not available")
		return
	}

	cfg := h.core.Config()
	if cfg == nil || cfg.Namespace == "" {
		h.logger.Error("plan-response: gateway namespace not configured")
		writeError(w, http.StatusServiceUnavailable, "gateway namespace not configured")
		return
	}

	brk := h.core.Services().Broker()
	if brk == nil {
		h.logger.Error("plan-response: gateway broker not initialized")
		writeError(w, http.StatusServiceUnavailable, "gateway broker not available")
		return
	}

	topic := a2a.SamEventsTopic(cfg.Namespace, "deep_research", "plan_response")
	payload := map[string]any{
		"event_type":       "plan_response",
		"source_component": cfg.GatewayID + "_gateway",
		"data": map[string]any{
			"plan_id":    req.PlanID,
			"agent_name": req.AgentName,
			"user_id":    userID,
			"action":     req.Action,
			"steps":      req.Steps,
		},
	}
	data, err := json.Marshal(payload)
	if err != nil {
		h.logger.Error("plan-response: marshal payload", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to encode plan response")
		return
	}

	props := a2a.EncodeUserProperties(&a2a.UserProperties{
		ClientID: cfg.GatewayID,
		UserID:   userID,
	})

	pubCtx, cancel := context.WithTimeout(r.Context(), planResponsePublishTimeout)
	defer cancel()
	if pubErr := brk.Publish(pubCtx, topic, &broker.Message{
		Payload:        data,
		UserProperties: props,
	}); pubErr != nil {
		h.logger.Error("plan-response: publish failed", "topic", topic, "error", pubErr)
		writeError(w, http.StatusInternalServerError, "failed to publish plan response")
		return
	}

	h.logger.Debug("plan-response published",
		"planID", req.PlanID, "agent", req.AgentName, "user", userID, "action", req.Action,
	)
	writeJSON(w, http.StatusOK, PlanResponseResult{
		Status: "accepted",
		PlanID: req.PlanID,
		Action: req.Action,
	})
}

// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"cmp"
	"context"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/httputil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/spend"
)

type taskBuilderHandler struct {
	assistant *taskBuilderAssistant
	core      *entrypoint.GatewayCore
	logger    *slog.Logger
}

func newTaskBuilderHandler(core *entrypoint.GatewayCore, logger *slog.Logger) *taskBuilderHandler {
	if core.ScheduledTasks() == nil || !gatewayHasLLM(core) {
		return nil
	}
	return &taskBuilderHandler{
		assistant: &taskBuilderAssistant{logger: logger},
		core:      core,
		logger:    logger,
	}
}

func (h *taskBuilderHandler) requestSpend(r *http.Request, model generalModel) *llm.SpendAttribution {
	return h.core.RequestSpend(contextSubject(r), resolveUserID(r, h.core), httputil.ClientSurface(r),
		spend.SurfaceTaskBuilder, model.spendKey, model.configID, "")
}

func (h *taskBuilderHandler) handleChat(w http.ResponseWriter, r *http.Request) {
	var req TaskBuilderChatRequest
	if err := decodeJSON(r, &req); err != nil {
		badRequestFromDecode(w, err)
		return
	}
	if strings.TrimSpace(req.Message) == "" {
		writeError(w, http.StatusBadRequest, "message is required")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	model := resolveGeneralModel(ctx, h.core)
	resp, err := h.assistant.processMessage(ctx, &req, model, h.requestSpend(r, model))
	if err != nil {
		logAssistantLLMFailure(h.logger, "task builder LLM error", err)
		refusal := entrypoint.BudgetRefusalText(err)
		writeJSON(w, http.StatusOK, &TaskBuilderChatResponse{
			Message:     cmp.Or(refusal, "Something went wrong while processing that. Please try again."),
			TaskUpdates: map[string]any{},
			Confidence:  0.0,
			ReadyToSave: false,
			IsError:     true,
			IsWarning:   refusal != "",
		})
		return
	}

	writeJSON(w, http.StatusOK, resp)
}

func (h *taskBuilderHandler) handleGreeting(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, h.assistant.greeting())
}

func (h *taskBuilderHandler) handleValidateConflict(w http.ResponseWriter, r *http.Request) {
	var req ValidateConflictRequest
	if err := decodeJSON(r, &req); err != nil {
		badRequestFromDecode(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	model := resolveGeneralModel(ctx, h.core)
	writeJSON(w, http.StatusOK, h.assistant.validateConflict(ctx, &req, model, h.requestSpend(r, model)))
}

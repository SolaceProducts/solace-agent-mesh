// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"log/slog"
	"net/http"

	"github.com/SolaceProducts/solace-agent-mesh/internal/httputil"
)

type aiAssistantHandler struct {
	svc    *AIAssistantService
	logger *slog.Logger
}

func (h *aiAssistantHandler) handleGenerateAgent(w http.ResponseWriter, r *http.Request) {
	var req GenerateAgentRequest
	if err := decodeJSON(r, &req); err != nil {
		badRequestFromDecode(w, err)
		return
	}
	if err := validateRequest(&req); err != nil {
		handleServiceError(w, err)
		return
	}

	resp, err := h.svc.GenerateAgent(r.Context(), req, userIDFromRequest(r), httputil.ClientSurface(r))
	if err != nil {
		handleServiceError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, resp)
}

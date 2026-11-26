// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"net/http"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
)

type budgetStatusResponse struct {
	OwnLevel         string  `json:"ownLevel"`
	MeshLimitReached bool    `json:"meshLimitReached"`
	PeriodEnd        string  `json:"periodEnd"`
	CapKey           *string `json:"capKey"`
}

type budgetStatusHandler struct {
	core *entrypoint.GatewayCore
}

func (h *budgetStatusHandler) handleStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, callerBudgetStatus(r, h.core))
}

func callerBudgetStatus(r *http.Request, core *entrypoint.GatewayCore) budgetStatusResponse {
	userID := auth.UserIDFromRequest(r, core.Config().DefaultUserIdentity)
	status := core.BudgetStatus(requestCaller(r, userID), userID)
	resp := budgetStatusResponse{
		OwnLevel:         string(status.OwnLevel),
		MeshLimitReached: status.MeshLimitReached,
		PeriodEnd:        epochMsToISO8601(status.PeriodEnd.UnixMilli()),
	}
	if status.WarnCapKey != "" {
		resp.CapKey = &status.WarnCapKey
	}
	return resp
}

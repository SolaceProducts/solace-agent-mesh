// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"context"
	"net/http"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
)

type agentCardHandler struct {
	core *entrypoint.GatewayCore
}

func (h *agentCardHandler) handleList(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, unpagedResponse(h.visibleCards(r.Context(), contextSubject(r))))
}

func (h *agentCardHandler) visibleCards(ctx context.Context, caller *auth.Subject) []a2a.AgentCard {
	reg := h.core.Services().FeatureRegistry()
	activityGated := reg == nil || !reg.IsEnabled(features.KeyActivityMonitor)

	all := h.core.AgentRegistry().List()
	cards := make([]a2a.AgentCard, 0, len(all))
	for _, c := range all {
		if a2a.IsEphemeralCard(c) {
			continue
		}
		if !h.core.CanAccessAgent(ctx, caller, c) {
			continue
		}
		if activityGated && platpkg.DisplayName(&c) == platpkg.BuiltinActivityMonitorName {
			continue
		}
		cards = append(cards, c)
	}

	return sortedByDisplayName(cards)
}

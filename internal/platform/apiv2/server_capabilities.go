// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"context"
	"net/http"

	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
)

type ServerCapabilities struct {
	ManageRbac           bool `json:"manageRbac" example:"true"`
	CreateDecisionModels bool `json:"createDecisionModels" example:"true"`
}

const signedInReasonServerCapabilities = "server capabilities probe: answered from server configuration rather than the caller's permissions; discloses only whether the built-in identity provider is in use and whether decision models are enabled"

type capabilityHandler struct {
	miniIDP  bool
	features *features.Registry
}

func serverCapabilityRoutes(deps *entplatform.RouteDeps) []Route {
	if deps.RBACSvc == nil {
		return nil
	}
	h := &capabilityHandler{miniIDP: deps.MiniIDPMode, features: deps.FeatureRegistry}
	return []Route{
		{Method: http.MethodGet, Path: "/serverCapabilities", Gate: GateSignedIn, AllowAPIKey: true, Published: true,
			Stability: StabilityStable,
			Doc: Doc{ID: "getServerCapabilities", Summary: "Get what this server lets callers do",
				Text: "Returns what the server's configuration allows, whatever the caller's own permissions. A pipeline can use it to learn up front whether access-control writes and decision-model creates are honored.", Tag: serverCapabilitiesTag},
			Handler: Handle(h.get, "Unable to get server capabilities")},
	}
}

func (h *capabilityHandler) get(context.Context, struct{}) (ServerCapabilities, error) {
	return ServerCapabilities{
		ManageRbac:           !h.miniIDP,
		CreateDecisionModels: platpkg.DecisionModelsEnabled(h.features),
	}, nil
}

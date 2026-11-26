// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package builderapply

import (
	"log/slog"
	"net/http"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/httputil"
	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
)

const buildsSuffix = "/builder/builds"

const BuildsPath = platpkg.V1Prefix + buildsSuffix

const InternalBuildsPath = platpkg.InternalPrefix + buildsSuffix

type handler struct {
	svc    *Service
	logger *slog.Logger
}

const FeatureKey = "builder_server_apply"

func RegisterRoutes(mux *http.ServeMux, deps *entplatform.RouteDeps, core *platpkg.PlatformCore, logger *slog.Logger) {
	if deps == nil || deps.FeatureRegistry == nil || !deps.FeatureRegistry.IsEnabled(FeatureKey) {
		return
	}
	var bounds Bounds
	if core != nil {
		if cfg := core.Config(); cfg != nil {
			bounds = Bounds{
				AgentInstructionMinChars: cfg.AgentInstructionMinChars,
				AgentInstructionMaxChars: cfg.AgentInstructionMaxChars,
			}
		}
	}
	var analytics *platpkg.ResourceReporter
	if core != nil {
		analytics = core.ResourceAnalytics()
	}
	h := &handler{svc: NewService(deps, bounds, analytics, logger), logger: logger}
	h.mount(mux)
}

func (h *handler) mount(mux *http.ServeMux) {
	platpkg.HandleWebUIRoute(mux, http.MethodPost, buildsSuffix, entplatform.RequireAnyOfScope(buildEntryScopes, h.handleApply))

	platpkg.HandleWebUIRoute(mux, http.MethodPost, removalsSuffix, entplatform.RequireAnyOfScope(removalScopes, h.handleRemove))
}

func requireComponents(req ApplyBuildRequest) error {
	if len(req.Components) > 0 || (req.DryRun && req.BuilderSessionID != "") {
		return nil
	}
	const message = "components must contain at least 1 item; only a dry run for a Builder session may be empty"
	return &entplatform.ValidationError{Message: message, Field: "components", Details: map[string][]string{"components": {message}}}
}

const removalsSuffix = "/builder/removals"

const RemovalsPath = platpkg.V1Prefix + removalsSuffix

const InternalRemovalsPath = platpkg.InternalPrefix + removalsSuffix

func (h *handler) handleRemove(w http.ResponseWriter, r *http.Request) {
	var req RemoveDroppedRequest
	if err := entplatform.DecodeJSON(r, &req); err != nil {
		entplatform.BadRequestFromDecode(w, err)
		return
	}
	if err := entplatform.ValidateRequest(&req); err != nil {
		entplatform.HandleServiceError(w, err)
		return
	}
	identity, _ := auth.SubjectFromContext(r.Context())
	entplatform.WriteJSON(w, http.StatusOK, h.svc.RemoveDropped(r.Context(), req, identity))
}

func (h *handler) handleApply(w http.ResponseWriter, r *http.Request) {
	var req ApplyBuildRequest
	if err := entplatform.DecodeJSON(r, &req); err != nil {
		entplatform.BadRequestFromDecode(w, err)
		return
	}
	if err := entplatform.ValidateRequest(&req); err != nil {
		entplatform.HandleServiceError(w, err)
		return
	}

	if err := requireComponents(req); err != nil {
		entplatform.HandleServiceError(w, err)
		return
	}

	identity, _ := auth.SubjectFromContext(r.Context())

	plans, reg, err := h.svc.Plan(r.Context(), req, identity)
	if err != nil {
		entplatform.HandleServiceErrorWithCause(w, err)
		return
	}
	if err := authorizePlan(identity, plans); err != nil {
		entplatform.HandleServiceError(w, err)
		return
	}

	resp := h.svc.applyRequest(r.Context(), req, identity, plans, reg, httputil.ClientSurface(r))

	entplatform.WriteJSON(w, http.StatusOK, resp)
}

func actorFrom(identity *auth.Subject) string {
	if identity == nil {
		return ""
	}
	if identity.Email() != "" {
		return identity.Email()
	}
	return identity.UserID()
}

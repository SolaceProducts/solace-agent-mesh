// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"log/slog"
	"net/http"

	"github.com/SolaceProducts/solace-agent-mesh/internal/apicontract"
	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/openapispec"
)

type (
	Route     = apicontract.Route
	Doc       = apicontract.Doc
	Tag       = apicontract.Tag
	Stability = apicontract.Stability
	Gate      = apicontract.Gate
)

const (
	StabilityDraft  = apicontract.StabilityDraft
	StabilityStable = apicontract.StabilityStable
)

const (
	GateAPIKeyRead     Gate = "apiKeyRead"
	GateAPIKeyWrite    Gate = "apiKeyWrite"
	GateRBACWrite      Gate = "rbacWrite"
	GateSuperAdminRead Gate = "superAdminRead"
	GateSignedIn       Gate = "signedIn"
	GatePublic         Gate = "public"
	GateBudgetAssign   Gate = "budgetAssign"
	GateBudgetDelete   Gate = "budgetDelete"
	GateUsageRead      Gate = "usageRead"

	GateLinkAgentRotateToken Gate = "linkAgentRotateToken"
)

type gateDoc struct {
	Text          string
	AdmitsAPIKeys bool
}

var gateDocs = map[Gate]gateDoc{
	GateAPIKeyRead:     {Text: "the universal grant `*:*:*`.", AdmitsAPIKeys: true},
	GateAPIKeyWrite:    {Text: "the universal grant `*:*:*`, held by a person who signed in. API keys are refused."},
	GateRBACWrite:      {Text: "the universal grant `*:*:*`. While Agent Mesh uses its built-in identity provider, the request fails with 403.", AdmitsAPIKeys: true},
	GateSuperAdminRead: {Text: "the universal grant `*:*:*`.", AdmitsAPIKeys: true},
	GateSignedIn:       {Text: "any signed-in caller. No scope is required.", AdmitsAPIKeys: true},
	GatePublic:         {Text: "none. The operation needs no sign-in."},

	GateBudgetAssign:         {Text: "both `budget:*:assign` and `budget:*:read`. The role editor does not offer `budget:*:assign`. The built-in `sam_manager` role and the universal grant `*:*:*` hold it, and a role created through the API or `sam config` can include it.", AdmitsAPIKeys: true},
	GateBudgetDelete:         {Text: "both `budget:*:delete` and `budget:*:assign`, because a deleted budget's users revert to the default. The role editor offers neither. The built-in `sam_manager` role and the universal grant `*:*:*` hold them, and a role created through the API or `sam config` can include them.", AdmitsAPIKeys: true},
	GateLinkAgentRotateToken: {Text: "both `agent_builder:*:update`, because it changes the stored token, and `agent_builder:*:deploy`, because it deploys the link agent again."},
	GateUsageRead:            {Text: "`*:*:read_usage`. A scope with a partial wildcard, such as `a*:*:read_usage`, does not grant it.", AdmitsAPIKeys: true},
}

func RegisterRoutes(mux *http.ServeMux, deps *entplatform.RouteDeps, core *platpkg.PlatformCore, logger *slog.Logger) {
	if deps == nil {
		return
	}
	logger.Info("management API v2 mounted", "prefix", platpkg.APIV2Prefix)
	if !openapispec.Available() {
		logger.Warn("this build embeds no OpenAPI description, so it answers 503; build with make generate", "path", openapispec.Path)
	}
	register(mux, core, deps.MiniIDPMode, routeTable(deps, core, logger))
}

func register(mux *http.ServeMux, core *platpkg.PlatformCore, miniIDPMode bool, routes []Route) {
	apicontract.Mount(mux, platpkg.APIV2Prefix, routes,
		func(rt Route, next http.HandlerFunc) http.HandlerFunc { return guard(rt, core, miniIDPMode, next) },
		func(pattern string) { core.AllowAPIKeys(pattern) })
	mux.HandleFunc("GET "+openapispec.Path, openapispec.Handler(loggerOf(core)))
	mux.HandleFunc("GET "+openapispec.ViewerPath, openapispec.ViewerHandler())
	for _, asset := range openapispec.ViewerAssets {
		mux.HandleFunc("GET "+openapispec.ViewerAssetPath(asset.Name), openapispec.ViewerAssetHandler(asset))
	}
}

func loggerOf(core *platpkg.PlatformCore) *slog.Logger {
	if core == nil {
		return slog.New(slog.DiscardHandler)
	}
	return core.Logger()
}

func guard(rt Route, core *platpkg.PlatformCore, miniIDPMode bool, next http.HandlerFunc) http.HandlerFunc {
	switch rt.Gate {
	case GateAPIKeyRead:
		return entplatform.APIKeyReadGate(core.Logger(), next)
	case GateAPIKeyWrite:
		return entplatform.APIKeyWriteGate(core.Logger(), next)
	case GateRBACWrite:
		return entplatform.RequireSuperAdmin(core.Logger(), rt.AuditResource, entplatform.RefuseUnderMiniIDP(miniIDPMode, next))
	case GateSuperAdminRead:
		return entplatform.SuperAdminReadGate(loggerOf(core), next)
	case GateBudgetAssign:
		return entplatform.BudgetAssignGate(next)
	case GateBudgetDelete:
		return entplatform.BudgetDeleteGate(next)
	case GateLinkAgentRotateToken:
		return entplatform.LinkAgentRotateTokenGate(next)
	case GateUsageRead:
		return entplatform.UsageReadGate(loggerOf(core), next)
	case GateSignedIn:
		return entplatform.RequireIdentity(next)
	case GatePublic:
		return next
	default:
		return entplatform.RequireAnyOfScope(rt.Scopes(), next)
	}
}

type routeGroup struct {
	name  string
	build func() []Route
}

func routeGroups(deps *entplatform.RouteDeps, core *platpkg.PlatformCore, logger *slog.Logger) []routeGroup {
	groups := []routeGroup{
		{"entrypointRoutes", func() []Route { return entrypointRoutes(deps, core, logger) }},
		{"agentRoutes", func() []Route { return agentRoutes(deps, core, logger) }},
		{"workflowRoutes", func() []Route { return workflowRoutes(deps, core, logger) }},
		{"remoteAgentRoutes", func() []Route { return remoteAgentRoutes(deps, core, logger) }},
		{"linkAgentRoutes", func() []Route { return linkAgentRoutes(deps, core, logger) }},
		{"evaluationRoutes", func() []Route { return evaluationRoutes(deps, core, logger) }},
		{"eventCatalogRoutes", func() []Route { return eventCatalogRoutes(deps) }},
		{"roleRoutes", func() []Route { return roleRoutes(deps) }},
		{"grantRoutes", func() []Route { return grantRoutes(deps) }},
		{"systemUserRoutes", func() []Route { return systemUserRoutes(deps) }},
		{"serviceAccountToolAuthRoutes", func() []Route { return serviceAccountToolAuthRoutes(deps) }},
		{"claimMappingRoutes", func() []Route { return claimMappingRoutes(deps) }},
		{"defaultRoleRoutes", func() []Route { return defaultRoleRoutes(deps) }},
		{"userRoutes", func() []Route { return userRoutes(deps) }},
		{"identityClaimRoutes", func() []Route { return identityClaimRoutes(deps) }},
		{"scopeRoutes", func() []Route { return scopeRoutes(deps) }},
		{"serverCapabilityRoutes", func() []Route { return serverCapabilityRoutes(deps) }},
		{"toolsetRoutes", func() []Route { return toolsetRoutes(deps, core, logger) }},
		{"toolsetUploadRoutes", func() []Route { return toolsetUploadRoutes(deps, core, logger) }},
		{"skillRoutes", func() []Route { return skillRoutes(deps, core, logger) }},
		{"apiKeyRoutes", func() []Route { return apiKeyRoutes(deps, logger) }},
		{"budgetRoutes", func() []Route { return budgetRoutes(deps) }},
		{"budgetAssignmentRoutes", func() []Route { return budgetAssignmentRoutes(deps) }},
		{"budgetSubjectRoutes", func() []Route { return budgetSubjectRoutes(deps) }},
		{"budgetSubjectTopUpRoutes", func() []Route { return budgetSubjectTopUpRoutes(deps) }},
		{"usageRoutes", func() []Route { return usageRoutes(deps, logger) }},
		{"modelRoutes", func() []Route { return modelRoutes(deps, core, logger) }},
		{"modelLookupRoutes", func() []Route { return modelLookupRoutes(deps, core, logger) }},
		{"serviceRoutes", func() []Route { return serviceRoutes(logger) }},
		{"profileProviderRoutes", func() []Route { return profileProviderRoutes(deps, logger) }},
		{"webuiSettingsRoutes", func() []Route { return webuiSettingsRoutes(deps, logger) }},
		{"cliIngestRoutes", func() []Route { return cliIngestRoutes(core.CLIIngest(), logger) }},
		{"connectorRoutes", func() []Route { return connectorRoutes(deps, core, logger) }},
	}
	for _, g := range withheldRouteGroups {
		groups = append(groups, routeGroup{g.name, func() []Route { return g.build(deps) }})
	}
	return groups
}

func routeTable(deps *entplatform.RouteDeps, core *platpkg.PlatformCore, logger *slog.Logger) []Route {
	var routes []Route
	for _, g := range routeGroups(deps, core, logger) {
		routes = append(routes, g.build()...)
	}
	return routes
}

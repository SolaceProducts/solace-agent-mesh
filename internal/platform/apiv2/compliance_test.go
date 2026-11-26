// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"context"
	"fmt"
	"maps"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/apicontract/compliancetest"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

type (
	fixtureResource struct {
		ID   string `json:"id"`
		Type string `json:"type"`
	}
	fixtureGetVersion struct {
		AgentID string `path:"agentId" json:"-"`
		ID      string `path:"id" json:"-"`
	}
	fixtureListRequest struct {
		Page Page `json:"-"`
	}
)

var openObjects = map[string]string{
	"Entrypoint.values":                           "the entrypoint type's schema, served at /entrypointSchemas, sets its keys",
	"DeployedEntrypoint.values":                   "the entrypoint type's schema, served at /entrypointSchemas, sets its keys",
	"EntrypointSchema.schema":                     "a JSON Schema document, shaped by JSON Schema rather than by SAM",
	"CreateEntrypointRequest.values":              "the entrypoint type's schema, served at /entrypointSchemas, sets its keys",
	"UpdateEntrypointRequest.values":              "the entrypoint type's schema, served at /entrypointSchemas, sets its keys",
	"TestConnectionRequest.values":                "the entrypoint type's schema, served at /entrypointSchemas, sets its keys",
	"SecretsDiffRequest.values":                   "the entrypoint type's schema, served at /entrypointSchemas, sets its keys",
	"WhatsAppMessageTemplatesRequest.values":      "the entrypoint type's schema, served at /entrypointSchemas, sets its keys",
	"ToolsetConfig.configValues":                  "the toolset's manifest sets its keys",
	"SkillConfig.configValues":                    "the skill's manifest sets its keys",
	"SkillConfigField.default":                    "takes whatever type the manifest declares for the setting",
	"Agent.additionalConfigurations":              "extra agent runtime settings, open by design; unknown keys come back as warnings",
	"DeployedAgent.additionalConfigurations":      "extra agent runtime settings, open by design; unknown keys come back as warnings",
	"CreateAgentRequest.additionalConfigurations": "extra agent runtime settings, open by design; unknown keys come back as warnings",
	"UpdateAgentRequest.additionalConfigurations": "extra agent runtime settings, open by design; unknown keys come back as warnings",
	"Workflow.appConfig":                          "the workflow definition; typing it is a post-GA ticket",
	"Connector.values":                            "the connector subtype's published fieldSchema sets its keys, and sam config apply round-trips it",
	"CreateConnectorRequest.values":               "the connector subtype's published fieldSchema sets its keys, and sam config apply round-trips it",
	"UpdateConnectorRequest.values":               "the connector subtype's published fieldSchema sets its keys, and sam config apply round-trips it",
	"ConnectorSchemaField.default":                "takes the string or number the subtype's type definition declares for the setting",
	"TestConnectorConnectionRequest.values":       "the event_mesh connector subtype's schema, served at /connectorSchemas, sets its keys",
	"ConnectorSecretsDiffRequest.values":          "the connector subtype's schema, served at /connectorSchemas, sets its keys",
	"SuggestedAuth.config":                        "its keys depend on the suggested scheme's authType, and the specification supplies the scopes",
	"Toolset.capabilities":                        "the toolset's manifest declares its capabilities",
	"ToolsetTool.parameters":                      "a JSON Schema document the tool declares",
	"ToolsetTool.configSchema":                    "the setting list the tool's manifest declares",
	"ToolsetConfiguration.values":                 "each tool's configSchema, from the toolset's manifest, sets its keys",
	"UpdateToolsetConfigurationRequest.values":    "each tool's configSchema, from the toolset's manifest, sets its keys",
	"ToolsetSecretsDiffRequest.values":            "each tool's configSchema, from the toolset's manifest, sets its keys",
	"SkillBundledTool.parameters":                 "a JSON Schema document the tool declares",
	"SkillBundledTool.configSchema":               "the setting list the tool's manifest declares",
	"SkillConfiguration.values":                   "each tool's configSchema, from the skill's manifest, sets its keys",
	"UpdateSkillConfigurationRequest.values":      "each tool's configSchema, from the skill's manifest, sets its keys",
	"SkillSecretsDiffRequest.values":              "each tool's configSchema, from the skill's manifest, sets its keys",
	"CreateWorkflowRequest.appConfig":             "the workflow definition; typing it is a post-GA ticket",
	"UpdateWorkflowRequest.appConfig":             "the workflow definition; typing it is a post-GA ticket",
	"RunModelConfigSnapshot.modelParams":          "the inference parameters the model configuration set, whose keys are the provider's",
	"Evaluator.choiceScores":                      "the evaluator's author names the choices, so the keys are theirs",
	"CreateEvaluatorRequest.choiceScores":         "the evaluator's author names the choices, so the keys are theirs",
	"UpdateEvaluatorRequest.choiceScores":         "the evaluator's author names the choices, so the keys are theirs",
	"RunResultMessage.payload":                    "an A2A message as published; the A2A protocol shapes it, not SAM, and it stays as recorded",
	"RunResultTaskEvents.events":                  "recorded A2A visualization events kept as stored, with the snake_case keys the web UI's activity diagram reads; the A2A message stream shapes them, not SAM",
	"MCPTool.inputSchema":                         "JSON Schema the MCP server publishes",
	"MCPTool.outputSchema":                        "JSON Schema the MCP server publishes",
	"Model.authConfig":                            "the auth type's credential fields; the set of keys follows authType",
	"Model.modelParams":                           "provider-specific generation parameters, open by design",
	"CreateModelRequest.authConfig":               "the auth type's credential fields; the set of keys follows authType",
	"CreateModelRequest.modelParams":              "provider-specific generation parameters, open by design",
	"SupportedModelsRequest.authConfig":           "the auth type's credential fields; the set of keys follows authConfig.type",
	"ProviderModelsRequest.authConfig":            "the auth type's credential fields; the set of keys follows authConfig.type",
	"UpdateModelRequest.authConfig":               "the auth type's credential fields; the set of keys follows authType",
	"UpdateModelRequest.modelParams":              "provider-specific generation parameters, open by design",
	"UpdateModelRequest.pricing":                  "the four token rates; a shape that is both optional and nullable cannot be published as a typed object",
	"ModelTestConnectionRequest.authConfig":       "the auth type's credential fields; the set of keys follows authType",
	"ModelTestConnectionRequest.modelParams":      "provider-specific generation parameters, open by design",
	"ModelSecretsDiffRequest.authConfig":          "the auth type's credential fields; the set of keys follows authType",
	"CLIIngestEvent.properties":                   "the event's properties; the analytics catalog decides which are kept",

	"EventCatalogEntry.payloadSchema":               eventPayloadSchemaIsOpen,
	"EventCatalogEntry.examplePayload":              eventPayloadIsOpen,
	"CreateEventCatalogEntryRequest.payloadSchema":  eventPayloadSchemaIsOpen,
	"CreateEventCatalogEntryRequest.examplePayload": eventPayloadIsOpen,
	"UpdateEventCatalogEntryRequest.payloadSchema":  eventPayloadSchemaIsOpen,
	"UpdateEventCatalogEntryRequest.examplePayload": eventPayloadIsOpen,
}

var keyPathVars = map[keyPathVar]string{
	{Path: "/providers/{provider}/models", Name: "provider"}: providerIsAKey,
	{Path: "/providers/{provider}/params", Name: "provider"}: providerIsAKey,
}

const (
	eventPayloadSchemaIsOpen = "a JSON Schema document, shaped by JSON Schema rather than by SAM"
	eventPayloadIsOpen       = "an event's payload, which the publisher shapes and payloadSchema describes"
)

const providerIsAKey = "an LLM provider is one of a fixed set built into SAM, with no record to fetch, and it is addressed in the path as a key"

var results = map[reflect.Type]bool{
	reflect.TypeFor[BudgetAssignmentChanges]():         true,
	reflect.TypeFor[BudgetTopUp]():                     true,
	reflect.TypeFor[UsageSummary]():                    true,
	reflect.TypeFor[SelfUsage]():                       true,
	reflect.TypeFor[UsageSavings]():                    true,
	reflect.TypeFor[NetworkingDetails]():               true,
	reflect.TypeFor[TestConnectionResult]():            true,
	reflect.TypeFor[SecretsDiffResult]():               true,
	reflect.TypeFor[DetectAuthResult]():                true,
	reflect.TypeFor[MessageTemplateLookup]():           true,
	reflect.TypeFor[AgentPlatformDefaults]():           true,
	reflect.TypeFor[UserAccess]():                      true,
	reflect.TypeFor[WorkflowConfiguration]():           true,
	reflect.TypeFor[RemoteAgentCard]():                 true,
	reflect.TypeFor[WatchlistEntry]():                  true,
	reflect.TypeFor[WatchlistChartSeries]():            true,
	reflect.TypeFor[RunResultExecutionData]():          true,
	reflect.TypeFor[ExperimentToolAuth]():              true,
	reflect.TypeFor[ToolAuthAuthorization]():           true,
	reflect.TypeFor[ServiceAccountAgentToolAuth]():     true,
	reflect.TypeFor[ServiceAccountToolAuthorization](): true,
	reflect.TypeFor[ToolAuthCandidateAgent]():          true,
	reflect.TypeFor[PinnedToolAuthAgent]():             true,
	reflect.TypeFor[RunGroupAgent]():                   true,
	reflect.TypeFor[RunGroupModelResult]():             true,
	reflect.TypeFor[RunResultTaskEvents]():             true,
	reflect.TypeFor[RawBody]():                         true,
	reflect.TypeFor[MCPTool]():                         true,
	reflect.TypeFor[MCPOAuthInitiateResult]():          true,
	reflect.TypeFor[MCPOAuthStatusResult]():            true,
	reflect.TypeFor[ConnectorSpecification]():          true,
	reflect.TypeFor[ModelStatus]():                     true,
	reflect.TypeFor[AzureWorkloadIdentity]():           true,
	reflect.TypeFor[ModelDependent]():                  true,
	reflect.TypeFor[ModelTestConnectionResult]():       true,
	reflect.TypeFor[ModelSecretsDiffResult]():          true,
	reflect.TypeFor[SupportedModel]():                  true,
	reflect.TypeFor[Health]():                          true,
	reflect.TypeFor[Version]():                         true,
	reflect.TypeFor[VersionProduct]():                  true,
	reflect.TypeFor[ProfileProvider]():                 true,
	reflect.TypeFor[WebuiSettings]():                   true,
	reflect.TypeFor[ProviderParam]():                   true,
	reflect.TypeFor[ScopeCatalogEntry]():               true,
	reflect.TypeFor[ServerCapabilities]():              true,
	reflect.TypeFor[DefaultRoles]():                    true,
	reflect.TypeFor[UserRoleGrants]():                  true,
	reflect.TypeFor[UserIdentityClaim]():               true,
	reflect.TypeFor[ToolsetConfiguration]():            true,
	reflect.TypeFor[ToolsetBuildTarget]():              true,
	reflect.TypeFor[SkillConfiguration]():              true,
	reflect.TypeFor[SkillFile]():                       true,
	reflect.TypeFor[ImportExamplesResult]():            true,
	reflect.TypeFor[ExampleArtifact]():                 true,
}

var restTimestampTypes = map[reflect.Type]bool{}

var restTimestampResources []reflect.Type

var auditResources = map[string]bool{
	entplatform.AuditResourceRole:               true,
	entplatform.AuditResourceGrant:              true,
	entplatform.AuditResourceClaimMapping:       true,
	entplatform.AuditResourceDefaultRoles:       true,
	entplatform.AuditResourceSystemUser:         true,
	entplatform.AuditResourceIdentityClaims:     true,
	entplatform.AuditResourceSystemUserToolAuth: true,
}

var knownGates = map[Gate]bool{GateAPIKeyRead: true, GateAPIKeyWrite: true, GateRBACWrite: true, GateSuperAdminRead: true, GateSignedIn: true, GatePublic: true, GateBudgetAssign: true, GateBudgetDelete: true, GateUsageRead: true, GateLinkAgentRotateToken: true}

type keyPathVar = compliancetest.KeyPathVar

func complianceConfig() compliancetest.Config {
	gates := map[Gate]string{}
	for gate := range knownGates {
		gates[gate] = gateDocs[gate].Text
	}
	scopes := catalogScopes()
	for scope := range unofferedBudgetScopes {
		scopes[scope] = true
	}
	return compliancetest.Config{
		Scopes:               scopes,
		Gates:                gates,
		KeyPathVars:          keyPathVars,
		OpenObjects:          openObjects,
		Results:              results,
		LegacyTimestamp:      restTimestampType,
		LegacyTimestampUsers: restTimestampTypes,
		WirePackage:          reflect.TypeFor[Agent]().PkgPath(),
		MountFunc:            "register",
	}
}

var unofferedBudgetScopes = map[string]bool{
	rbac.ScopeBudgetCreate: true,
	rbac.ScopeBudgetUpdate: true,
	rbac.ScopeBudgetDelete: true,
	rbac.ScopeBudgetAssign: true,
}

func TestUnofferedBudgetScopesAreTheUnassignableOnes(t *testing.T) {
	for scope := range unofferedBudgetScopes {
		if !rbac.IsUnassignableScope(scope) {
			t.Errorf("%s is exempt from the catalog check but is assignable", scope)
		}
	}
}

func TestV2Compliance(t *testing.T) {
	t.Setenv(features.EnvVarName(features.KeyEventCatalog), "true")
	core, deps := newTestCore(t)
	routes := routeTable(deps, core, core.Logger())
	compliancetest.Check(t, complianceConfig(), routes)

	for _, rule := range []struct {
		name     string
		problems []string
	}{
		{"PlatformGates", gateViolations(routes, realInventories)},
		{"Inventories", staleInventoryRows(routes, realInventories)},
		{"PublicRoutesAreSkipped", publicSkipPathViolations(routes, auth.SkipPaths)},
	} {
		t.Run(rule.name, func(t *testing.T) {
			if len(rule.problems) > 0 {
				t.Errorf("%d violation(s):\n%s", len(rule.problems), strings.Join(rule.problems, "\n"))
			}
		})
	}
}

func gateViolations(routes []Route, inv inventories) []string {
	var out []string
	for _, rt := range routes {
		op := rt.Method + " " + rt.Path
		bad := func(format string, args ...any) { out = append(out, op+": "+fmt.Sprintf(format, args...)) }
		switch {
		case rt.Gate == GateRBACWrite && !auditResources[rt.AuditResource]:
			bad("AuditResource %q is not an entplatform.AuditResource constant", rt.AuditResource)
		case rt.Gate != GateRBACWrite && rt.AuditResource != "":
			bad("AuditResource is only for a GateRBACWrite route")
		}
		if rt.Gate == GateRBACWrite && rt.Method == http.MethodGet {
			bad("GateRBACWrite requires a write method: it refuses every call under mini-IdP")
		}
		for _, problem := range inventoryViolations(rt, op, inv) {
			bad("%s", problem)
		}
	}
	return out
}

func TestV2Compliance_CatchesViolations(t *testing.T) {
	scopes := catalogScopes()
	cfg := complianceConfig()
	routeViolations := func(routes []Route, _ map[string]bool, inv inventories) []string {
		return append(compliancetest.RouteViolations(cfg, routes), gateViolations(routes, inv)...)
	}
	typeViolations := func(resources, requests []reflect.Type) ([]string, map[string]bool) {
		return compliancetest.TypeViolations(cfg, resources, requests)
	}
	wireTypes := compliancetest.WireTypes
	t.Run("Routes", func(t *testing.T) {
		getVersion := stub[fixtureGetVersion, fixtureResource]()
		valid := Route{Method: http.MethodGet, Path: "/agents/{agentId}/versions/{id}", Scope: rbac.ScopeAgentBuilderRead, Handler: getVersion}
		rbacWrite := Route{Method: http.MethodPut, Path: valid.Path, Gate: GateRBACWrite, AuditResource: entplatform.AuditResourceRole, Handler: getVersion}
		keyWrite := Route{Method: http.MethodPost, Path: "/gadgets/{agentId}/versions/{id}", Gate: GateAPIKeyWrite, Handler: getVersion}
		if got := routeViolations([]Route{valid, rbacWrite, keyWrite}, scopes, realInventories); len(got) > 0 {
			t.Fatalf("valid route rejected: %v", got)
		}
		const fixtureReason = "open for a reason"
		const openPath = "/open/{agentId}/versions/{id}"
		fixtures := inventories{
			signedIn:       map[string]string{http.MethodGet + " " + valid.Path: fixtureReason, http.MethodGet + " /unreviewed/row": "", http.MethodPut + " /unreviewed/row": fixtureReason},
			signedInWrites: map[string]string{http.MethodPut + " " + valid.Path: fixtureReason, http.MethodPut + " /unreviewed/write": "", http.MethodGet + " /unreviewed/write": fixtureReason},
			public:         map[string]string{http.MethodGet + " " + openPath: fixtureReason, http.MethodGet + " /unreviewed/open": ""},
		}
		open := Route{Method: http.MethodGet, Path: valid.Path, Gate: GateSignedIn, Handler: getVersion}
		if got := routeViolations([]Route{open}, scopes, fixtures); len(got) > 0 {
			t.Fatalf("signed-in route rejected: %v", got)
		}
		signedWrite := Route{Method: http.MethodPut, Path: valid.Path, Gate: GateSignedIn, Handler: getVersion}
		if got := routeViolations([]Route{signedWrite}, scopes, fixtures); len(got) > 0 {
			t.Fatalf("listed signed-in write rejected: %v", got)
		}
		public := Route{Method: http.MethodGet, Path: openPath, Gate: GatePublic, Handler: getVersion}
		if got := routeViolations([]Route{public}, scopes, fixtures); len(got) > 0 {
			t.Fatalf("listed public route rejected: %v", got)
		}
		keyed := Route{Method: http.MethodPut, Path: "/providers/{provider}/params", Scope: valid.Scope, Handler: stub[struct {
			Provider string `path:"provider,foldedkey" json:"-"`
		}, fixtureResource]()}
		if got := routeViolations([]Route{keyed}, scopes, realInventories); len(got) > 0 {
			t.Fatalf("key-addressed route rejected: %v", got)
		}
		noPathID := stub[struct{}, fixtureResource]()
		listUnpaged := stub[struct{}, List[fixtureResource]]()
		plainKeyed := Route{Method: http.MethodPut, Path: "/providers/{provider}/params", Scope: valid.Scope, Handler: stub[struct {
			Provider string `path:"provider,key" json:"-"`
		}, fixtureResource]()}
		if got := routeViolations([]Route{plainKeyed}, scopes, realInventories); len(got) > 0 {
			t.Fatalf("plain key-addressed route rejected: %v", got)
		}
		for name, bad := range map[string]Route{
			"HEAD":                    {Method: "HEAD", Path: valid.Path, Scope: valid.Scope, Handler: getVersion},
			"no scope":                {Method: http.MethodGet, Path: valid.Path, Handler: getVersion},
			"gate and scope":          {Method: http.MethodGet, Path: valid.Path, Scope: valid.Scope, Gate: GateAPIKeyRead, Handler: getVersion},
			"unknown gate":            {Method: http.MethodGet, Path: valid.Path, Gate: "everyone", Handler: getVersion},
			"scope not in catalog":    {Method: http.MethodGet, Path: valid.Path, Scope: "agent:*:view", Handler: getVersion},
			"any-of not in catalog":   {Method: http.MethodGet, Path: valid.Path, AnyOfScopes: []string{valid.Scope, "agent:*:view"}, Handler: getVersion},
			"any-of of one":           {Method: http.MethodGet, Path: valid.Path, AnyOfScopes: []string{valid.Scope}, Handler: getVersion},
			"scope and any-of":        {Method: http.MethodGet, Path: valid.Path, Scope: valid.Scope, AnyOfScopes: []string{valid.Scope, rbac.ScopeRBACRead}, Handler: getVersion},
			"gate and any-of":         {Method: http.MethodGet, Path: valid.Path, AnyOfScopes: []string{valid.Scope, rbac.ScopeRBACRead}, Gate: GateSignedIn, Handler: getVersion},
			"no gate at all":          {Method: http.MethodGet, Path: valid.Path, Handler: getVersion},
			"rbac write unaudited":    {Method: http.MethodPut, Path: valid.Path, Gate: GateRBACWrite, Handler: getVersion},
			"unknown audit resource":  {Method: http.MethodPut, Path: valid.Path, Gate: GateRBACWrite, AuditResource: "rol", Handler: getVersion},
			"audit resource on scope": {Method: http.MethodPut, Path: valid.Path, Scope: valid.Scope, AuditResource: entplatform.AuditResourceRole, Handler: getVersion},
			"audit resource on gate":  {Method: http.MethodPut, Path: valid.Path, Gate: GateAPIKeyWrite, AuditResource: entplatform.AuditResourceRole, Handler: getVersion},
			"rbac write on a read":    {Method: http.MethodGet, Path: valid.Path, Gate: GateRBACWrite, AuditResource: entplatform.AuditResourceRole, Handler: getVersion},
			"no handler":              {Method: http.MethodGet, Path: valid.Path, Scope: valid.Scope},
			"path var not bound":      {Method: http.MethodGet, Path: "/agents/{id}", Scope: valid.Scope, Handler: noPathID},
			"bound var not in path":   {Method: http.MethodGet, Path: "/agents", Scope: valid.Scope, Handler: getVersion},
			"DELETE sends a body":     {Method: http.MethodDelete, Path: valid.Path, Scope: valid.Scope, Handler: getVersion},
			"list without page query": {Method: http.MethodGet, Path: "/agents", Scope: valid.Scope, Handler: listUnpaged},
			"bad path":                {Method: http.MethodGet, Path: "/agents/", Scope: valid.Scope, Handler: noPathID},
			"key var read as a resource id": {Method: http.MethodPut, Path: "/providers/{provider}/params", Scope: valid.Scope, Handler: stub[struct {
				Provider string `path:"provider" json:"-"`
			}, fixtureResource]()},
			"key var reused on another route": {Method: http.MethodPut, Path: "/widgets/{provider}/params", Scope: valid.Scope, Handler: stub[struct {
				Provider string `path:"provider,key" json:"-"`
			}, fixtureResource]()},
			"snake_case query param": {Method: http.MethodGet, Path: "/agents", Scope: valid.Scope, Handler: stub[struct {
				OwnerID string `query:"owner_id" json:"-"`
			}, fixtureResource]()},
			"paging query param": {Method: http.MethodGet, Path: "/agents", Scope: valid.Scope, Handler: stub[struct {
				Limit string `query:"limit" json:"-"`
			}, fixtureResource]()},
			"toggle query param": {Method: http.MethodGet, Path: "/agents", Scope: valid.Scope, Handler: stub[struct {
				IncludeCount string `query:"includeCount" json:"-"`
			}, fixtureResource]()},
		} {
			if got := routeViolations([]Route{bad}, scopes, realInventories); len(got) == 0 {
				t.Errorf("%s: passed", name)
			}
		}
		gated := Route{Method: http.MethodGet, Path: valid.Path, Gate: GateAPIKeyWrite, Handler: getVersion}
		if got := routeViolations([]Route{gated}, scopes, realInventories); len(got) > 0 {
			t.Fatalf("gated route rejected: %v", got)
		}
		const undescribed Gate = "undescribed"
		describedLess := cfg
		describedLess.Gates = map[Gate]string{undescribed: ""}
		if got := compliancetest.RouteViolations(describedLess, []Route{{Method: http.MethodGet, Path: valid.Path, Gate: undescribed, Handler: getVersion}}); len(got) == 0 {
			t.Error("a gate without a gateDocs entry passed")
		}
		if got := routeViolations([]Route{valid, valid}, scopes, realInventories); len(got) == 0 {
			t.Error("duplicate route passed")
		}

		for name, tc := range map[string]struct {
			route Route
			want  string
		}{
			"open route outside the inventory": {
				route: Route{Method: http.MethodGet, Path: "/widgets/{agentId}/versions/{id}", Gate: GateSignedIn, Handler: getVersion},
				want:  "not in signedInRoutes",
			},
			"open route the inventory gives no reason": {
				route: Route{Method: http.MethodGet, Path: "/unreviewed/row", Gate: GateSignedIn, Handler: getVersion},
				want:  "records no reason",
			},
			"signed-in write outside signedInWrites": {
				route: Route{Method: http.MethodPut, Path: "/widgets/{agentId}/versions/{id}", Gate: GateSignedIn, Handler: getVersion},
				want:  "not in signedInWrites",
			},
			"signed-in write listed as a read": {
				route: Route{Method: http.MethodPut, Path: "/unreviewed/row", Gate: GateSignedIn, Handler: getVersion},
				want:  "wrong inventory",
			},
			"signed-in read listed as a write": {
				route: Route{Method: http.MethodGet, Path: "/unreviewed/write", Gate: GateSignedIn, Handler: getVersion},
				want:  "wrong inventory",
			},
			"signed-in write the inventory gives no reason": {
				route: Route{Method: http.MethodPut, Path: "/unreviewed/write", Gate: GateSignedIn, Handler: getVersion},
				want:  "signedInWrites records no reason",
			},
			"public route on a state-changing method": {
				route: Route{Method: http.MethodPost, Path: openPath, Gate: GatePublic, Handler: getVersion},
				want:  "GatePublic is for a read",
			},
			"public route outside the inventory": {
				route: Route{Method: http.MethodGet, Path: "/widgets/open", Gate: GatePublic, Handler: getVersion},
				want:  "not in publicRoutes",
			},
			"public route the inventory gives no reason": {
				route: Route{Method: http.MethodGet, Path: "/unreviewed/open", Gate: GatePublic, Handler: getVersion},
				want:  "publicRoutes records no reason",
			},
			"public route listed as a signed-in read": {
				route: Route{Method: http.MethodGet, Path: valid.Path, Gate: GatePublic, Handler: getVersion},
				want:  "wrong inventory",
			},
		} {
			got := routeViolations([]Route{tc.route}, scopes, fixtures)
			if !slices.ContainsFunc(got, func(v string) bool { return strings.Contains(v, tc.want) }) {
				t.Errorf("%s: violations %v, want one naming %q", name, got, tc.want)
			}
		}
	})

	t.Run("Inventories", func(t *testing.T) {
		const path = "/agents/{agentId}/versions/{id}"
		handler := stub[fixtureGetVersion, fixtureResource]()
		const reason = "open for a reason"
		for name, tc := range map[string]struct {
			route Route
			inv   inventories
			want  string
		}{
			"signedInRoutes": {Route{Method: http.MethodGet, Path: path, Gate: GateSignedIn, Handler: handler},
				inventories{signedIn: map[string]string{"GET " + path: reason}}, "signedInRoutes"},
			"signedInWrites": {Route{Method: http.MethodPut, Path: path, Gate: GateSignedIn, Handler: handler},
				inventories{signedInWrites: map[string]string{"PUT " + path: reason}}, "signedInWrites"},
			"publicRoutes": {Route{Method: http.MethodGet, Path: path, Gate: GatePublic, Handler: handler},
				inventories{public: map[string]string{"GET " + path: reason}}, "publicRoutes"},
		} {
			if got := staleInventoryRows([]Route{tc.route}, tc.inv); len(got) > 0 {
				t.Fatalf("%s: a row its route still carries was called stale: %v", name, got)
			}
			scoped := tc.route
			scoped.Gate, scoped.Scope = "", rbac.ScopeAgentBuilderRead
			for cause, routes := range map[string][]Route{
				"the route is gone":    nil,
				"the route was scoped": {scoped},
			} {
				got := staleInventoryRows(routes, tc.inv)
				if !slices.ContainsFunc(got, func(v string) bool {
					return strings.Contains(v, "no route carries") && strings.Contains(v, tc.want)
				}) {
					t.Errorf("%s, %s: violations %v, want one naming the stale row and its map", name, cause, got)
				}
			}
		}
	})

	t.Run("WireTypes", func(t *testing.T) {
		resources, requests := wireTypes([]Route{
			{Method: http.MethodGet, Handler: stub[fixtureListRequest, List[fixtureResource]]()},
			{Method: http.MethodDelete, Handler: stub[fixtureGetVersion, struct{}]()},
			{Method: http.MethodPut, Handler: stub[fixtureGetVersion, struct{}]()},
			{Method: http.MethodGet, Handler: stub[fixtureGetVersion, *fixtureResource]()},
			{Method: http.MethodGet, Handler: stub[fixtureGetVersion, Download]()},
			{Method: http.MethodPut, Handler: stub[fixtureGetVersion, Accepted]()},
		})
		if want := []reflect.Type{reflect.TypeFor[fixtureResource](), reflect.TypeFor[fixtureResource](), reflect.TypeFor[Operation]()}; !slices.Equal(resources, want) {
			t.Errorf("wireTypes resources = %v, want %v: a list's item, nothing for a 204, a pointer's element, nothing for a download, a 202's operation", resources, want)
		}
		if want := []reflect.Type{reflect.TypeFor[fixtureListRequest](), reflect.TypeFor[fixtureGetVersion](), reflect.TypeFor[fixtureGetVersion](), reflect.TypeFor[fixtureGetVersion](), reflect.TypeFor[fixtureGetVersion](), reflect.TypeFor[fixtureGetVersion]()}; !slices.Equal(requests, want) {
			t.Errorf("wireTypes requests = %v, want %v", requests, want)
		}
		if files, _ := wireTypes([]Route{{Method: http.MethodGet, Handler: stub[fixtureGetVersion, FileResponse]()}}); len(files) > 0 {
			t.Errorf("wireTypes resources for a file = %v, want none: a file has no JSON body", files)
		}
		type bodiedWithoutIDOrType struct {
			Name string `json:"name"`
		}
		if got, _ := typeViolations(wireTypes([]Route{{Method: http.MethodGet, Handler: stub[fixtureGetVersion, bodiedWithoutIDOrType]()}})); len(got) == 0 {
			t.Error("a JSON-bodied response without id and type passed: the no-body skip swallowed it")
		}
		type goodResource struct {
			ID          string         `json:"id"`
			Type        string         `json:"type"`
			CreatedTime Instant        `json:"createdTime"`
			EndTime     *Instant       `json:"endTime"`
			Label       Patch[string]  `json:"label,omitempty"`
			Tags        []string       `json:"tags"`
			Limits      map[string]int `json:"limits"`
			State       string         `json:"state" validate:"required,oneof=in_progress done"`
		}
		if got, _ := typeViolations([]reflect.Type{reflect.TypeFor[goodResource]()}, nil); len(got) > 0 {
			t.Fatalf("valid type rejected: %v", got)
		}
		type restTimestampResource struct {
			ID       string                    `json:"id"`
			Type     string                    `json:"type"`
			SeenTime entplatform.RESTTimestamp `json:"seenTime"`
		}
		if got, _ := typeViolations([]reflect.Type{reflect.TypeFor[restTimestampResource]()}, nil); len(got) == 0 {
			t.Error("a v1 timestamp on a type outside restTimestampTypes passed")
		}
		for _, typ := range restTimestampResources {
			if got, _ := typeViolations([]reflect.Type{typ}, nil); len(got) > 0 {
				t.Errorf("%v, a resource allowed v1 timestamps, rejected: %v", typ, got)
			}
		}
		type optionalRequest struct {
			Label string `json:"label,omitempty"`
		}
		if got, _ := typeViolations(nil, []reflect.Type{reflect.TypeFor[optionalRequest]()}); len(got) > 0 {
			t.Fatalf("omitempty on a request rejected: %v", got)
		}
		type nested struct {
			Extra any `json:"extra"`
		}
		for name, bad := range map[string]reflect.Type{
			"no id or type": reflect.TypeFor[struct {
				Name string `json:"name"`
			}](),
			"interface field": reflect.TypeFor[struct {
				ID     string `json:"id"`
				Type   string `json:"type"`
				Nested nested `json:"nested"`
			}](),
			"untyped map": reflect.TypeFor[struct {
				ID     string         `json:"id"`
				Type   string         `json:"type"`
				Config map[string]any `json:"config"`
			}](),
			"Time name, not an Instant": reflect.TypeFor[struct {
				ID        string `json:"id"`
				Type      string `json:"type"`
				StartTime int64  `json:"startTime"`
			}](),
			"time.Time": reflect.TypeFor[struct {
				ID      string    `json:"id"`
				Type    string    `json:"type"`
				Created time.Time `json:"created"`
			}](),
			"untagged field": reflect.TypeFor[struct {
				ID   string `json:"id"`
				Type string `json:"type"`
				Name string
			}](),
			"omitempty on a response field": reflect.TypeFor[struct {
				ID    string `json:"id"`
				Type  string `json:"type"`
				Label string `json:"label,omitempty"`
			}](),
			"omitzero on a nested response field": reflect.TypeFor[struct {
				ID   string `json:"id"`
				Type string `json:"type"`
				Rest struct {
					Count int `json:"count,omitzero"`
				} `json:"rest"`
			}](),
			"uppercase enum": reflect.TypeFor[struct {
				ID    string `json:"id"`
				Type  string `json:"type"`
				State string `json:"state" validate:"oneof=active PENDING"`
			}](),
		} {
			if got, _ := typeViolations([]reflect.Type{bad}, nil); len(got) == 0 {
				t.Errorf("%s: passed", name)
			}
		}
	})
}

func stub[Req, Resp any]() Handler {
	return Handle(func(context.Context, Req) (Resp, error) {
		var resp Resp
		return resp, nil
	}, "stub")
}

func catalogScopes() map[string]bool {
	scopes := map[string]bool{}
	for _, s := range rbac.Catalog() {
		scopes[s.Scope] = true
	}
	return scopes
}

var signedInRoutes = map[string]string{
	"GET /models":             signedInReasonModelCatalogRead,
	"GET /models/{id}":        signedInReasonModelCatalogRead,
	"GET /modelStatus":        signedInReasonModelCatalogRead,
	"GET /serverCapabilities": signedInReasonServerCapabilities,
	"GET /userUsage":          signedInReasonOwnUsage,
	"GET /userUsage/savings":  signedInReasonOwnUsage,
}

var publicRoutes = map[string]string{
	"GET /health":                          publicReasonHealthProbe,
	"GET /version":                         publicReasonVersionProbe,
	http.MethodGet + " " + mcpCallbackPath: publicReasonMCPOAuthCallback,
}

var signedInWrites = map[string]string{
	"PUT /productAnalytics/ingest/cli": signedInReasonCLIIngest,
}

type inventories struct {
	signedIn       map[string]string
	signedInWrites map[string]string
	public         map[string]string
}

var realInventories = inventories{signedIn: signedInRoutes, signedInWrites: signedInWrites, public: publicRoutes}

func carriesInventory(rt Route) (signedIn, write, public bool) {
	isGet := rt.Method == http.MethodGet
	return rt.Gate == GateSignedIn && isGet, rt.Gate == GateSignedIn && !isGet, rt.Gate == GatePublic
}

func inventoryViolations(rt Route, op string, inv inventories) []string {
	var out []string
	signedIn, write, public := carriesInventory(rt)
	for _, in := range []struct {
		name   string
		rows   map[string]string
		belong bool
	}{
		{"signedInRoutes", inv.signedIn, signedIn},
		{"signedInWrites", inv.signedInWrites, write},
		{"publicRoutes", inv.public, public},
	} {
		reason, listed := in.rows[op]
		switch {
		case in.belong && !listed:
			out = append(out, fmt.Sprintf("the route skips the scope check but is not in %s; add it there and have the opening reviewed", in.name))
		case in.belong && strings.TrimSpace(reason) == "":
			out = append(out, fmt.Sprintf("%s records no reason for this opening; name the reviewed constant", in.name))
		case !in.belong && listed && (rt.Gate == GateSignedIn || rt.Gate == GatePublic):
			out = append(out, fmt.Sprintf("the route is in %s, the wrong inventory for %s on %s", in.name, rt.Gate, rt.Method))
		}
	}
	if rt.Gate == GatePublic && rt.Method != http.MethodGet {
		out = append(out, "GatePublic is for a read; only a GET may answer without an identity, a state-changing route states a scope")
	}
	return out
}

func publicSkipPathViolations(routes []Route, skip map[string]bool) []string {
	var out []string
	declared := map[string]bool{}
	for _, rt := range routes {
		if rt.Gate != GatePublic {
			continue
		}
		mounted := platpkg.APIV2Prefix + rt.Path
		declared[mounted] = true
		if !skip[mounted] {
			out = append(out, fmt.Sprintf("%s %s: GatePublic, so %s must be in auth.SkipPaths; the gateway would otherwise authenticate it and the route would answer 401",
				rt.Method, rt.Path, mounted))
		}
	}
	for path := range skip {
		if strings.HasPrefix(path, platpkg.APIV2Prefix) && !declared[path] {
			out = append(out, path+": in auth.SkipPaths, so it is served unauthenticated, but no v2 route carries GatePublic for it")
		}
	}
	slices.Sort(out)
	return out
}

func staleInventoryRows(routes []Route, inv inventories) []string {
	carried := map[string]map[string]bool{"signedInRoutes": {}, "signedInWrites": {}, "publicRoutes": {}}
	for _, rt := range routes {
		op := rt.Method + " " + rt.Path
		signedIn, write, public := carriesInventory(rt)
		carried["signedInRoutes"][op] = carried["signedInRoutes"][op] || signedIn
		carried["signedInWrites"][op] = carried["signedInWrites"][op] || write
		carried["publicRoutes"][op] = carried["publicRoutes"][op] || public
	}
	var out []string
	for _, in := range []struct {
		name string
		rows map[string]string
	}{
		{"signedInRoutes", inv.signedIn},
		{"signedInWrites", inv.signedInWrites},
		{"publicRoutes", inv.public},
	} {
		for _, op := range slices.Sorted(maps.Keys(in.rows)) {
			if !carried[in.name][op] {
				out = append(out, op+": "+in.name+" records an opening no route carries; drop the row")
			}
		}
	}
	slices.Sort(out)
	return out
}

func jsonNames(t reflect.Type) map[string]bool {
	names := map[string]bool{}
	for _, f := range reflect.VisibleFields(t) {
		tag, ok := f.Tag.Lookup("json")
		if !ok {
			continue
		}
		if name, _, _ := strings.Cut(tag, ","); name != "" {
			names[name] = true
		}
	}
	return names
}

func oneofValues(f reflect.StructField) []string {
	for rule := range strings.SplitSeq(f.Tag.Get("validate"), ",") {
		if values, ok := strings.CutPrefix(rule, "oneof="); ok {
			return strings.Fields(values)
		}
	}
	return nil
}

// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
	"github.com/SolaceProducts/solace-agent-mesh/internal/control"
	"github.com/SolaceProducts/solace-agent-mesh/internal/leader"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/budgetstate"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/tokenbroker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

type ManageablePlatform struct {
	*platpkg.PlatformCore
	deps              *RouteDeps
	logger            *slog.Logger
	describeSTRSchema strSchemaFunc
}

var (
	_ runtime.Component  = (*ManageablePlatform)(nil)
	_ control.Manageable = (*ManageablePlatform)(nil)
	_ leader.JobSource   = (*ManageablePlatform)(nil)
)

const (
	errMethodNotAllowedFmt = "method not allowed: %s"
	errUnknownResourceFmt  = "unknown resource: %s"
	errPlatformDBMissing   = "platform database not available"
)

func NewManageablePlatform(core *platpkg.PlatformCore, deps *RouteDeps, logger *slog.Logger) *ManageablePlatform {
	return &ManageablePlatform{
		PlatformCore: core,
		deps:         deps,
		logger:       withLoggerName(logger, "manageable-platform"),
	}
}

func (m *ManageablePlatform) Init(ctx context.Context, cfg config.Config) error {
	return m.PlatformCore.Init(ctx, cfg)
}

func (m *ManageablePlatform) Start(ctx context.Context) error {
	return m.PlatformCore.Start(ctx)
}

func (m *ManageablePlatform) Stop(ctx context.Context) error {
	return m.PlatformCore.Stop(ctx)
}

func (m *ManageablePlatform) Health() error {
	return m.PlatformCore.Health()
}

func (m *ManageablePlatform) ManagementEndpoints() []control.Endpoint {
	return []control.Endpoint{
		{Method: "get", Path: "agents", Description: "List all agents"},
		{Method: "get", Path: "agents/{id}", Description: "Get agent by ID"},
		{Method: "get", Path: "workflows", Description: "List all workflows"},
		{Method: "get", Path: "workflows/{id}", Description: "Get workflow by ID"},
		{Method: "post", Path: WorkflowConnectorToolInputsPath, Description: "Grade a workflow's connector-bound tool nodes' inputs against the OpenAPI operations their connectors resolve to"},
		{Method: "get", Path: "gateways", Description: "List all gateways"},
		{Method: "get", Path: "gateways/{id}", Description: "Get gateway by ID"},
		{Method: "get", Path: "connectors", Description: "List all connectors"},
		{Method: "get", Path: "connectors/{id}", Description: "Get connector by ID"},
		{Method: "get", Path: "connectors/schemas", Description: "Get connector type schemas"},
		{Method: "get", Path: "connectors/types", Description: "List the connector types enabled on this deployment, with their subtypes"},
		{Method: "post", Path: "connectors/lookup/byName", Description: "Report whether a connector exists under a name, its type, and whether it has an uploaded resource or a stored allow_list"},
		{Method: "post", Path: "connectors/mcp/discover", Description: "Discover MCP server tools"},
		{Method: "post", Path: "connectors/openapi/detectAuth", Description: "Detect OpenAPI auth schemes"},
		{Method: "post", Path: "connectors/openapi/parse", Description: "Read an OpenAPI specification's auth schemes and operations"},
		{Method: "get", Path: "a2a-agents", Description: "List all A2A agents"},
		{Method: "get", Path: "a2a-agents/{id}", Description: "Get A2A agent by ID"},
		{Method: "get", Path: "skills", Description: "List all skills"},
		{Method: "get", Path: "skills/{id}", Description: "Get skill by ID"},
		{Method: "get", Path: "tools", Description: "List all tools"},
		{Method: "get", Path: "tools/{id}", Description: "Get tool by ID"},
		{Method: "post", Path: "tools/describe", Description: "Describe tools and connector tools: parameters, result fields, and whether a workflow tool node can use them"},
		{Method: "get", Path: "model-configs/{aliasOrID}", Description: "Resolve a model alias or row ID on demand"},
		{Method: "post", Path: "model-grants/{id}/token", Description: "Get an access token for an OAuth grant, refreshing it when due or rejected"},
		{Method: "put", Path: "model-grants/{id}", Description: "Save an OAuth grant from a sign-in made outside the platform"},
		{Method: "delete", Path: "model-grants/{id}", Description: "Discard an OAuth grant"},
		{Method: "get", Path: "models", Description: "List models by name and type, without credentials"},
		{Method: "get", Path: "webui-settings", Description: "Resolve the instance's web UI settings overrides"},
		{Method: "get", Path: "budget-state", Description: "Resolve the budget enforcement state"},
	}
}

func getByValidatedID(id string, get func(string) (any, error)) (any, error) {
	if err := validatePathUUID(id); err != nil {
		return nil, err
	}
	return get(id)
}

func (m *ManageablePlatform) requireResourceService(resource string) error {
	var requiredSvc any
	switch resource {
	case "agents":
		requiredSvc = m.deps.AgentSvc
	case "a2a-agents":
		requiredSvc = m.deps.A2AAgentSvc
	case "workflows":
		requiredSvc = m.deps.WorkflowSvc
	case "gateways":
		requiredSvc = m.deps.GatewaySvc
	case "connectors":
		requiredSvc = m.deps.ConnectorSvc
	case "skills":
		requiredSvc = m.deps.SkillSvc
	case "tools":
		requiredSvc = m.deps.ToolSvc
	default:
		return nil
	}
	if requiredSvc == nil {
		return fmt.Errorf("platform services not initialized for resource %q", resource)
	}
	return nil
}

func (m *ManageablePlatform) HandleManagement(ctx context.Context, method string, path []string, body json.RawMessage, identity *auth.Identity) (any, error) {
	m.logger.Debug("HandleManagement", "method", method, "path", path, "hasIdentity", identity != nil)
	if len(path) == 0 {
		return nil, fmt.Errorf("missing resource path")
	}

	resource := path[0]
	subPath := path[1:]

	if err := m.requireResourceService(resource); err != nil {
		return nil, err
	}

	switch resource {
	case "agents":
		if method != "get" {
			return nil, fmt.Errorf(errMethodNotAllowedFmt, method)
		}
		if err := requireIdentityScope(identity, rbac.ScopeAgentBuilderRead); err != nil {
			return nil, err
		}
		if len(subPath) == 0 {
			return m.listAgentsWithRuntime(ctx)
		}
		return getByValidatedID(subPath[0], func(id string) (any, error) { return m.deps.AgentSvc.GetByID(ctx, id) })

	case "workflows":
		if method == "post" {
			return m.handleWorkflowAction(ctx, subPath, body, identity)
		}
		if method != "get" {
			return nil, fmt.Errorf(errMethodNotAllowedFmt, method)
		}
		if err := requireIdentityScope(identity, rbac.ScopeWorkflowBuilderRead); err != nil {
			return nil, err
		}
		if len(subPath) == 0 {
			return listEveryPage(func(page, size int) (*PaginatedResponse[WorkflowResponse], error) {
				return m.deps.WorkflowSvc.ListPaginated(ctx, page, size)
			})
		}
		return getByValidatedID(subPath[0], func(id string) (any, error) { return m.deps.WorkflowSvc.GetByID(ctx, id) })

	case "gateways":
		if method != "get" {
			return nil, fmt.Errorf(errMethodNotAllowedFmt, method)
		}
		if err := requireIdentityScope(identity, rbac.ScopeEntrypointRead); err != nil {
			return nil, err
		}
		if len(subPath) == 0 {
			return m.listGatewaysWithRuntime(ctx)
		}
		return getByValidatedID(subPath[0], func(id string) (any, error) { return m.deps.GatewaySvc.GetByID(ctx, id) })

	case "connectors":
		return m.handleConnectorManagement(ctx, method, subPath, body, identity)

	case "a2a-agents":
		if method != "get" {
			return nil, fmt.Errorf(errMethodNotAllowedFmt, method)
		}
		if err := requireIdentityScope(identity, rbac.ScopeAgentBuilderRead); err != nil {
			return nil, err
		}
		if len(subPath) == 0 {
			return listEveryPage(func(page, size int) (*PaginatedResponse[RemoteAgentResponse], error) {
				return m.deps.A2AAgentSvc.ListPaginated(ctx, page, size)
			})
		}
		return getByValidatedID(subPath[0], func(id string) (any, error) { return m.deps.A2AAgentSvc.GetByID(ctx, id) })

	case "skills":
		if method != "get" {
			return nil, fmt.Errorf(errMethodNotAllowedFmt, method)
		}
		if err := requireIdentityScope(identity, rbac.ScopeSkillRead); err != nil {
			return nil, err
		}
		if len(subPath) == 0 {
			return listEveryPage(func(page, size int) (*PaginatedResponse[SkillDetailResponse], error) {
				return m.deps.SkillSvc.ListPaginated(ctx, page, size, "")
			})
		}
		return getByValidatedID(subPath[0], func(id string) (any, error) { return m.deps.SkillSvc.GetByID(ctx, id) })

	case "tools":
		if method == "post" {
			return m.handleToolAction(ctx, subPath, body, identity)
		}
		if method != "get" {
			return nil, fmt.Errorf(errMethodNotAllowedFmt, method)
		}
		if err := requireIdentityScope(identity, rbac.ScopeToolRead); err != nil {
			return nil, err
		}
		if len(subPath) == 0 {
			return listEveryPage(func(page, size int) (*PaginatedResponse[ToolDetailResponse], error) {
				return m.deps.ToolSvc.ListPaginated(ctx, page, size)
			})
		}
		return m.deps.ToolSvc.GetByID(ctx, subPath[0])

	case "model-configs":
		if err := requireIdentityScope(identity, rbac.ScopeModelConfigRead); err != nil {
			return nil, err
		}
		return m.handleModelConfigManagement(ctx, method, subPath)

	case tokenbroker.ControlResource:
		scopeErr := requireIdentityScope(identity, modelGrantScope(method))
		m.auditGrantRequest(ctx, method, subPath, identity, scopeErr == nil)
		if scopeErr != nil {
			return nil, scopeErr
		}
		return m.handleModelGrants(ctx, method, subPath, body, identity)

	case "models":
		if err := requireAnyIdentityScope(identity, rbac.ScopeAgentBuilderRead, rbac.ScopeWorkflowBuilderRead); err != nil {
			return nil, err
		}
		return m.handleModelCatalog(ctx, method, subPath)

	case "webui-settings":
		if err := requireIdentityScope(identity, rbac.ScopeWebuiSettingsRead); err != nil {
			return nil, err
		}
		return m.handleWebuiSettingsManagement(ctx, method)

	case "budget-state":
		return m.handleBudgetStateManagement(ctx, method, identity)

	default:
		return nil, fmt.Errorf(errUnknownResourceFmt, resource)
	}
}

func (m *ManageablePlatform) handleBudgetStateManagement(ctx context.Context, method string, identity *auth.Identity) (any, error) {
	if err := requireIdentityScope(identity, rbac.ScopeBudgetRead); err != nil {
		return nil, err
	}
	if method != "get" {
		return nil, fmt.Errorf(errMethodNotAllowedFmt, method)
	}
	db := m.DB()
	if db == nil {
		return nil, errors.New(errPlatformDBMissing)
	}
	return budgetstate.Compute(ctx, budgetstate.NewStore(db), time.Now())
}

func (m *ManageablePlatform) handleModelConfigManagement(ctx context.Context, method string, subPath []string) (any, error) {
	if method != "get" {
		return nil, fmt.Errorf(errMethodNotAllowedFmt, method)
	}
	key := strings.Join(subPath, "/")
	if key == "" {
		return nil, fmt.Errorf("model-configs requires an alias or ID path segment")
	}
	if m.deps.ModelConfigSvc == nil {
		return nil, fmt.Errorf("model config service not available")
	}
	raw, err := m.deps.ModelConfigSvc.GetByAliasOrIDRaw(ctx, key)
	if err != nil {
		return nil, err
	}
	if raw == nil {
		return nil, fmt.Errorf("model alias or id %q: %w", key, control.ErrResourceNotFound)
	}
	return raw, nil
}

func modelGrantScope(method string) string {
	switch method {
	case "put":
		return rbac.ScopeModelConfigUpdate
	case "delete":
		return rbac.ScopeModelConfigDelete
	default:
		return rbac.ScopeModelConfigRead
	}
}

func (m *ManageablePlatform) auditGrantRequest(ctx context.Context, method string, subPath []string, identity *auth.Identity, allowed bool) {
	if m.PlatformCore == nil || m.Services() == nil {
		return
	}
	grantID := ""
	if len(subPath) > 0 && validatePathUUID(subPath[0]) == nil {
		grantID = subPath[0]
	}
	userID := ""
	if identity != nil {
		userID = identity.UserID
	}
	m.Services().AuditLogger().LogControlAccess(ctx, auth.ControlAccessEvent{
		UserID:  userID,
		Method:  method,
		AppName: "platform/" + tokenbroker.ControlResource + "/" + grantID,
		Allowed: allowed,
	})
}

func (m *ManageablePlatform) handleModelGrants(ctx context.Context, method string, subPath []string, body json.RawMessage, identity *auth.Identity) (any, error) {
	isToken := len(subPath) == 2 && subPath[1] == tokenbroker.ControlAction
	switch {
	case isToken && method == "post":
	case len(subPath) == 1 && (method == "put" || method == "delete"):
	case isToken || len(subPath) == 1:
		return nil, fmt.Errorf(errMethodNotAllowedFmt, method)
	default:
		return nil, fmt.Errorf("%s requires the path {id} or {id}/%s", tokenbroker.ControlResource, tokenbroker.ControlAction)
	}
	id := subPath[0]
	if err := validatePathUUID(id); err != nil {
		return nil, err
	}
	grants := m.TokenBroker()
	if grants == nil {
		return nil, errors.New(errPlatformDBMissing)
	}
	switch method {
	case "put":
		return struct{}{}, m.saveGrant(ctx, grants, id, body, identity)
	case "delete":
		return struct{}{}, m.discardGrant(ctx, grants, id)
	}
	var req tokenbroker.TokenRequest
	if len(body) > 0 {
		if err := json.Unmarshal(body, &req); err != nil {
			return nil, fmt.Errorf("decode OAuth grant token request: %w", err)
		}
	}
	return grants.Answer(ctx, id, req)
}

func (m *ManageablePlatform) saveGrant(ctx context.Context, grants *tokenbroker.Service, id string, body json.RawMessage, identity *auth.Identity) error {
	var req tokenbroker.SaveRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return fmt.Errorf("decode OAuth grant: %w", err)
	}
	actor := grantActor
	if identity != nil && identity.UserID != "" {
		actor = identity.UserID
	}
	return grants.Save(ctx, req.Grant(id), actor, func(ctx context.Context) (bool, error) {
		return m.modelNamesGrant(ctx, id)
	})
}

func (m *ManageablePlatform) discardGrant(ctx context.Context, grants *tokenbroker.Service, id string) error {
	used, err := m.modelNamesGrant(ctx, id)
	if err != nil {
		return fmt.Errorf("check whether a model names OAuth grant %s: %w", id, err)
	}
	if used {
		return tokenbroker.ErrGrantInUse
	}
	return grants.Delete(ctx, id)
}

const grantActor = "system"

func (m *ManageablePlatform) modelNamesGrant(ctx context.Context, id string) (bool, error) {
	db := m.DB()
	if db == nil {
		return false, errors.New(errPlatformDBMissing)
	}
	models, err := store.NewModelConfigRepo(db).GetAll(ctx)
	if err != nil {
		return false, err
	}
	for _, mc := range models {
		if parseJSONMap(mc.ModelAuthConfig)[llm.GrantIDKey] == id {
			return true, nil
		}
	}
	return false, nil
}

func (m *ManageablePlatform) handleWebuiSettingsManagement(ctx context.Context, method string) (any, error) {
	if method != "get" {
		return nil, fmt.Errorf(errMethodNotAllowedFmt, method)
	}
	db := m.DB()
	if db == nil {
		return nil, errors.New(errPlatformDBMissing)
	}
	row, err := store.NewPlatformWebuiSettingsRepo(db).Get(ctx)
	if err != nil {
		return nil, fmt.Errorf("get webui settings: %w", err)
	}
	if row == nil {
		return nil, fmt.Errorf("webui settings: %w", control.ErrResourceNotFound)
	}
	return WebuiSettingsFromRow(row), nil
}

func (m *ManageablePlatform) handleToolAction(ctx context.Context, subPath []string, body json.RawMessage, identity *auth.Identity) (any, error) {
	if strings.Join(subPath, "/") != "describe" {
		return nil, fmt.Errorf("unknown tool action: %s", strings.Join(subPath, "/"))
	}
	req, err := decodeDescribeRequest(body)
	if err != nil {
		return nil, err
	}
	return newToolDescriber(m.deps, identity, m.describeSTRSchema, m.logger).Describe(ctx, req)
}

const WorkflowConnectorToolInputsPath = "workflows/validate/connectorToolInputs"

func (m *ManageablePlatform) handleWorkflowAction(ctx context.Context, subPath []string, body json.RawMessage, identity *auth.Identity) (any, error) {
	if strings.Join(subPath, "/") != "validate/connectorToolInputs" {
		return nil, fmt.Errorf("unknown workflow action: %s", strings.Join(subPath, "/"))
	}
	if err := requireIdentityScope(identity, rbac.ScopeWorkflowBuilderRead); err != nil {
		return nil, err
	}
	if err := requireIdentityScope(identity, rbac.ScopeConnectorRead); err != nil {
		return nil, err
	}
	var req struct {
		AppConfig map[string]any `json:"appConfig"`
	}
	if err := json.Unmarshal(body, &req); err != nil || req.AppConfig == nil {
		return nil, &ValidationError{Message: WorkflowConnectorToolInputsPath + " needs a body with an appConfig object"}
	}
	return m.deps.WorkflowSvc.CheckConnectorToolInputs(ctx, req.AppConfig), nil
}

func (m *ManageablePlatform) handleConnectorManagement(ctx context.Context, method string, subPath []string, body json.RawMessage, identity *auth.Identity) (any, error) {
	if method == "post" && len(subPath) >= 2 {
		action := strings.Join(subPath, "/")
		switch action {
		case "mcp/discover":
			if err := requireIdentityScope(identity, rbac.ScopeConnectorCreate); err != nil {
				return nil, err
			}
			if m.deps.MCPSvc == nil {
				return nil, fmt.Errorf("MCP discovery service not available")
			}
			var req MCPDiscoverRequest
			if err := json.Unmarshal(body, &req); err != nil {
				return nil, fmt.Errorf("invalid request body: %w", err)
			}
			tools, err := m.deps.MCPSvc.DiscoverTools(ctx, req)
			return tools, withLoggableCause(err)

		case "lookup/byName":
			if err := requireIdentityScope(identity, rbac.ScopeConnectorRead); err != nil {
				return nil, err
			}
			var req struct {
				Name string `json:"name"`
			}
			if err := json.Unmarshal(body, &req); err != nil || strings.TrimSpace(req.Name) == "" {
				return nil, &ValidationError{Message: "connectors/lookup/byName needs a body with a non-empty name"}
			}
			return m.deps.ConnectorSvc.LookupByName(ctx, req.Name)

		case "openapi/detectAuth":
			if err := requireIdentityScope(identity, rbac.ScopeConnectorRead); err != nil {
				return nil, err
			}
			if m.deps.AuthDetector == nil {
				return nil, errOpenAPIDetectorUnavailable
			}
			var req struct {
				SpecURL string `json:"specUrl"`
			}
			if err := json.Unmarshal(body, &req); err != nil {
				return nil, fmt.Errorf("invalid request body: %w", err)
			}
			resp, err := m.deps.AuthDetector.DetectAuth(ctx, req.SpecURL, nil)
			return resp, withLoggableCause(err)

		case "openapi/parse":
			return m.handleOpenAPIParse(ctx, body, identity)

		default:
			return nil, fmt.Errorf("unknown connector action: %s", action)
		}
	}

	if method == "get" {
		if err := requireIdentityScope(identity, rbac.ScopeConnectorRead); err != nil {
			return nil, err
		}
		if len(subPath) == 0 {
			visible := store.ConnectorFilter{ExcludeTypes: hiddenConnectorTypes(m.deps.FeatureRegistry)}
			return listEveryPage(func(page, size int) (*PaginatedResponse[ConnectorResponse], error) {
				return m.deps.ConnectorSvc.ListPaginated(ctx, page, size, visible, store.ConnectorOrder{})
			})
		}
		if subPath[0] == "schemas" {
			return VisibleConnectorSchemas(m.deps.FeatureRegistry), nil
		}
		if subPath[0] == "types" {
			return unpagedResponse(builderConnectorTypes(m.deps.FeatureRegistry)), nil
		}
		return getByValidatedID(subPath[0], func(id string) (any, error) { return m.deps.ConnectorSvc.GetByID(ctx, id) })
	}

	return nil, fmt.Errorf(errMethodNotAllowedFmt, method)
}

func requireIdentityScope(identity *auth.Identity, required string) error {
	if identity == nil || len(identity.Scopes) == 0 {
		return nil
	}
	for _, granted := range identity.Scopes {
		if auth.MatchScope(granted, required) {
			return nil
		}
	}
	return fmt.Errorf("insufficient permissions: requires %s", required)
}

func requireAnyIdentityScope(identity *auth.Identity, required ...string) error {
	for _, scope := range required {
		if requireIdentityScope(identity, scope) == nil {
			return nil
		}
	}
	return fmt.Errorf("insufficient permissions: requires one of %s", strings.Join(required, ", "))
}

func (m *ManageablePlatform) listAgentsWithRuntime(ctx context.Context) (*PaginatedResponse[AgentResponse], error) {
	dbResult, err := listEveryPage(func(page, size int) (*PaginatedResponse[AgentResponse], error) {
		return m.deps.AgentSvc.ListPaginated(ctx, page, size, "")
	})
	if err != nil {
		return nil, err
	}

	registry := m.AgentRegistry()
	if registry == nil {
		return dbResult, nil
	}

	dbInstanceNames := make(map[string]bool, len(dbResult.Data))
	for i, a := range dbResult.Data {
		key := a.InstanceName
		if key == "" {
			key = a.Name
		}
		dbInstanceNames[key] = true
		if card := registry.Get(key); card != nil {
			dbResult.Data[i].RuntimeStatus = RunningStatus(registry.RuntimeProblems(key))
		} else if dbResult.Data[i].DeploymentStatus == DisplayStatusDeployed {
			dbResult.Data[i].RuntimeStatus = DisconnectedStatus()
		} else {
			dbResult.Data[i].RuntimeStatus = StoppedStatus()
		}
	}

	discovered := 0
	for _, card := range registry.List() {
		if !includeRegistryCardInList(card, dbInstanceNames) {
			continue
		}
		resp := agentResponseFromCard(card)
		resp.RuntimeStatus = RunningStatus(registry.RuntimeProblems(card.Name))
		dbResult.Data = append(dbResult.Data, resp)
		discovered++
	}
	dbResult.Meta.Pagination.Count += discovered
	m.logger.Debug("listAgentsWithRuntime", "dbCount", len(dbInstanceNames), "registryCount", registry.Count(), "totalReturned", len(dbResult.Data))
	return dbResult, nil
}

func includeRegistryCardInList(card a2a.AgentCard, dbInstanceNames map[string]bool) bool {
	if dbInstanceNames[card.Name] {
		return false
	}
	if a2a.IsEphemeralCard(card) {
		return false
	}
	return true
}

func (m *ManageablePlatform) listGatewaysWithRuntime(ctx context.Context) (*PaginatedResponse[GatewayResponse], error) {
	return listEveryPage(func(page, size int) (*PaginatedResponse[GatewayResponse], error) {
		return m.deps.GatewaySvc.ListPaginated(ctx, page, size, "")
	})
}

func agentResponseFromCard(card a2a.AgentCard) AgentResponse {
	resp := AgentResponse{
		Name:             card.Name,
		Description:      card.Description,
		RuntimeStatus:    RunningStatus(platpkg.RuntimeProblemsFromCard(&card)),
		DeploymentStatus: "discovered",
		InputModes:       card.DefaultInputModes,
		OutputModes:      card.DefaultOutputModes,
	}
	for _, skill := range card.Skills {
		resp.Skills = append(resp.Skills, SkillResponse{
			ID:          skill.ID,
			Name:        skill.Name,
			Description: skill.Description,
			Tags:        skill.Tags,
			Examples:    skill.Examples,
			InputModes:  skill.InputModes,
			OutputModes: skill.OutputModes,
		})
	}
	return resp
}

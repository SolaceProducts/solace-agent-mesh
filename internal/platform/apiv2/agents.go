// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/httputil"
	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

type Agent struct {
	ID                       string                        `json:"id" example:"0190f5e4-8b2c-7c3e-9a1d-2f4b6c8d0e1f"`
	Type                     string                        `json:"type" example:"agent"`
	AgentType                string                        `json:"agentType" example:"standard"`
	Name                     string                        `json:"name" example:"Order support agent"`
	InstanceName             string                        `json:"instanceName" example:"agent_0190f5e4_8b2c_7c3e_9a1d_2f4b6c8d0e1f"`
	Description              string                        `json:"description" example:"Answers customer questions about order status and delivery dates."`
	SystemPrompt             string                        `json:"systemPrompt" example:"You help customers track their orders. Look up the order before you answer."`
	Toolsets                 []string                      `json:"toolsets" example:"[\"builtin_artifact_tools\",\"builtin_web_request_tools\"]"`
	ToolsetExcludedTools     map[string][]string           `json:"toolsetExcludedTools" nullable:"true" example:"{\"builtin_web_request_tools\":[\"web_search_google\"]}"`
	ToolsetConfigs           []ToolsetConfig               `json:"toolsetConfigs"`
	SkillConfigs             []SkillConfig                 `json:"skillConfigs"`
	SkillConfigFields        map[string][]SkillConfigField `json:"skillConfigFields" nullable:"true" example:"{\"0190f5e6-3c4d-7a5b-8c6d-9e0f1a2b3c4d\":[{\"toolName\":\"lookup_order\",\"key\":\"region\",\"fieldType\":\"string\",\"secret\":false,\"required\":true,\"description\":\"Region the order service runs in.\",\"default\":\"us-east-1\",\"coveredBySkill\":false,\"coveredByAgent\":true}]}"`
	Skills                   []AgentSkill                  `json:"skills"`
	SkillRefs                []string                      `json:"skillRefs" example:"[\"sam-docs\"]"`
	SkillIDs                 []string                      `json:"skillIds" example:"[\"0190f5e6-3c4d-7a5b-8c6d-9e0f1a2b3c4d\"]"`
	InputModes               []string                      `json:"inputModes" example:"[\"text\"]"`
	OutputModes              []string                      `json:"outputModes" example:"[\"text\"]"`
	ConnectorIDs             []string                      `json:"connectorIds" example:"[\"0190f5e7-5e6f-7b8c-9d0e-1f2a3b4c5d6e\"]"`
	ModelProvider            []string                      `json:"modelProvider" example:"[\"planning\"]"`
	AdditionalConfigurations map[string]any                `json:"additionalConfigurations" nullable:"true" example:"{\"maxLlmCallsPerTask\":20,\"injectCurrentTime\":true}"`
	DeploymentStatus         string                        `json:"deploymentStatus" example:"deployed"`
	SyncStatus               *string                       `json:"syncStatus" example:"in_sync"`
	Deployed                 *DeployedAgent                `json:"deployed"`
	RuntimeStatus            RuntimeStatus                 `json:"runtimeStatus"`
	ToolRegistrations        []ToolRegistration            `json:"toolRegistrations"`
	Warnings                 []string                      `json:"warnings" example:"[\"additionalConfigurations: unknown key \\\"maxLlmCalls\\\" is not consumed by the agent runtime; check for typos\"]"`
	IsEphemeral              bool                          `json:"isEphemeral" example:"false"`
	SourceSessionID          *string                       `json:"sourceSessionId" example:"0190f5e8-7a8b-7c9d-8e0f-1a2b3c4d5e6f"`
	SourceComponentName      *string                       `json:"sourceComponentName" example:"Order support agent"`
	SourceComponentKind      *string                       `json:"sourceComponentKind" example:"agent"`
	ExpiresTime              Instant                       `json:"expiresTime" example:"\"2026-01-02T04:04:05.678Z\""`
	CreatedBy                string                        `json:"createdBy" example:"jane.doe@example.com"`
	UpdatedBy                string                        `json:"updatedBy" example:"jane.doe@example.com"`
	CreatedTime              Instant                       `json:"createdTime" example:"\"2026-01-02T03:04:05.678Z\""`
	UpdatedTime              Instant                       `json:"updatedTime" example:"\"2026-01-03T10:15:30.000Z\""`
}

type DeployedAgent struct {
	Name                     string         `json:"name" example:"Order support agent"`
	Description              string         `json:"description" example:"Answers customer questions about order status and delivery dates."`
	SystemPrompt             string         `json:"systemPrompt" example:"You help customers track their orders. Look up the order before you answer."`
	Toolsets                 []string       `json:"toolsets" example:"[\"builtin_artifact_tools\",\"builtin_web_request_tools\"]"`
	InputModes               []string       `json:"inputModes" example:"[\"text\"]"`
	OutputModes              []string       `json:"outputModes" example:"[\"text\"]"`
	ConnectorIDs             []string       `json:"connectorIds" example:"[\"0190f5e7-5e6f-7b8c-9d0e-1f2a3b4c5d6e\"]"`
	AdditionalConfigurations map[string]any `json:"additionalConfigurations" nullable:"true" example:"{\"maxLlmCallsPerTask\":20,\"injectCurrentTime\":true}"`
	SkillIDs                 []string       `json:"skillIds" example:"[\"0190f5e6-3c4d-7a5b-8c6d-9e0f1a2b3c4d\"]"`
	SkillRefs                []string       `json:"skillRefs" example:"[\"sam-docs\"]"`
	Skills                   []AgentSkill   `json:"skills"`
	DeployedTime             Instant        `json:"deployedTime" example:"\"2026-01-03T10:16:00.000Z\""`
	DeployedBy               string         `json:"deployedBy" example:"jane.doe@example.com"`
}

type AgentSkill struct {
	ID          *string  `json:"id" example:"track_order"`
	Name        string   `json:"name" example:"Track order"`
	Description string   `json:"description" example:"Looks up an order and reports where it is."`
	Tags        []string `json:"tags" example:"[\"orders\",\"shipping\"]"`
	Examples    []string `json:"examples" example:"[\"Where is order 10023?\"]"`
	InputModes  []string `json:"inputModes" example:"[\"text\"]"`
	OutputModes []string `json:"outputModes" example:"[\"text\"]"`
	Required    []string `json:"required" nullable:"true" example:"[\"order_id\"]"`
	Optional    []string `json:"optional" nullable:"true" example:"[\"customer_email\"]"`
}

type ToolsetConfig struct {
	ToolsetName     string            `json:"toolsetName" example:"builtin_web_request_tools"`
	ConfigValues    map[string]any    `json:"configValues" example:"{\"web_request\":{\"timeout_seconds\":30}}"`
	VolumeBindings  map[string]string `json:"volumeBindings" nullable:"true" example:"{\"workspace\":\"data\"}"`
	NetworkBindings map[string]string `json:"networkBindings" nullable:"true" example:"{\"base_url\":\"egress-default\"}"`
}

type SkillConfig struct {
	SkillID      string                    `json:"skillId" example:"0190f5e6-3c4d-7a5b-8c6d-9e0f1a2b3c4d"`
	SkillName    *string                   `json:"skillName" example:"order-lookup"`
	ConfigValues map[string]map[string]any `json:"configValues" example:"{\"lookup_order\":{\"region\":\"us-east-1\",\"api_token\":\"<REDACTED>\"}}"`
}

type SkillConfigField struct {
	ToolName       string  `json:"toolName" example:"lookup_order"`
	Key            string  `json:"key" example:"region"`
	FieldType      string  `json:"fieldType" example:"string"`
	Secret         bool    `json:"secret" example:"false"`
	Required       bool    `json:"required" example:"true"`
	Description    *string `json:"description" example:"Region the order service runs in."`
	Default        any     `json:"default" example:"\"us-east-1\""`
	CoveredBySkill bool    `json:"coveredBySkill" example:"false"`
	CoveredByAgent bool    `json:"coveredByAgent" example:"true"`
}

type ToolRegistration struct {
	ToolType    string   `json:"toolType" example:"openapi"`
	ConnectorID *string  `json:"connectorId" example:"0190f5e7-5e6f-7b8c-9d0e-1f2a3b4c5d6e"`
	Name        *string  `json:"name" example:"orders_api"`
	Status      string   `json:"status" example:"ready"`
	Error       *string  `json:"error" example:"Some of this entry's tools were not registered. The agent's log has the cause."`
	ToolCount   int      `json:"toolCount" example:"3"`
	ToolNames   []string `json:"toolNames" example:"[\"get_order\",\"list_orders\",\"update_order\"]"`
}

type CreateAgentRequest struct {
	Name                     string              `json:"name" validate:"required,min=3,max=255,safetext" example:"Order support agent"`
	Description              string              `json:"description" validate:"required,min=10,max=1000,safetext" example:"Answers customer questions about order status and delivery dates."`
	SystemPrompt             string              `json:"systemPrompt" validate:"omitempty,safetext" example:"You help customers track their orders. Look up the order before you answer."`
	AgentType                string              `json:"agentType" validate:"omitempty,oneof=standard" example:"standard"`
	Skills                   []AgentSkill        `json:"skills" validate:"max=20"`
	SkillRefs                []string            `json:"skillRefs" example:"[\"sam-docs\"]"`
	SkillIDs                 []string            `json:"skillIds" example:"[\"0190f5e6-3c4d-7a5b-8c6d-9e0f1a2b3c4d\"]"`
	Toolsets                 []string            `json:"toolsets" example:"[\"builtin_artifact_tools\",\"builtin_web_request_tools\"]"`
	ToolsetExcludedTools     map[string][]string `json:"toolsetExcludedTools" example:"{\"builtin_web_request_tools\":[\"web_search_google\"]}"`
	ToolsetConfigs           []ToolsetConfig     `json:"toolsetConfigs"`
	SkillConfigs             []SkillConfig       `json:"skillConfigs"`
	ConnectorIDs             []string            `json:"connectorIds" validate:"max=5" example:"[\"0190f5e7-5e6f-7b8c-9d0e-1f2a3b4c5d6e\"]"`
	InputModes               []string            `json:"inputModes" example:"[\"text\"]"`
	OutputModes              []string            `json:"outputModes" example:"[\"text\"]"`
	ModelProvider            []string            `json:"modelProvider" example:"[\"planning\"]"`
	AdditionalConfigurations map[string]any      `json:"additionalConfigurations" example:"{\"maxLlmCallsPerTask\":20,\"injectCurrentTime\":true}"`
	Surface                  string              `header:"X-Sam-Client-Surface" json:"-" example:"ui"`
}

type UpdateAgentRequest struct {
	ID                       string              `path:"id" json:"-" example:"0190f5e4-8b2c-7c3e-9a1d-2f4b6c8d0e1f"`
	Name                     string              `json:"name" validate:"required,min=3,max=255,safetext" example:"Order support agent"`
	Description              string              `json:"description" validate:"required,min=10,max=1000,safetext" example:"Answers customer questions about order status and delivery dates."`
	SystemPrompt             string              `json:"systemPrompt" validate:"omitempty,safetext" example:"You help customers track their orders. Look up the order before you answer."`
	Skills                   []AgentSkill        `json:"skills" validate:"max=20"`
	SkillRefs                []string            `json:"skillRefs" example:"[\"sam-docs\"]"`
	SkillIDs                 []string            `json:"skillIds" example:"[\"0190f5e6-3c4d-7a5b-8c6d-9e0f1a2b3c4d\"]"`
	Toolsets                 []string            `json:"toolsets" example:"[\"builtin_artifact_tools\",\"builtin_web_request_tools\"]"`
	ToolsetExcludedTools     map[string][]string `json:"toolsetExcludedTools" example:"{\"builtin_web_request_tools\":[\"web_search_google\"]}"`
	ToolsetConfigs           []ToolsetConfig     `json:"toolsetConfigs" nullable:"true"`
	SkillConfigs             []SkillConfig       `json:"skillConfigs" nullable:"true"`
	ConnectorIDs             []string            `json:"connectorIds" validate:"max=5" example:"[\"0190f5e7-5e6f-7b8c-9d0e-1f2a3b4c5d6e\"]"`
	InputModes               []string            `json:"inputModes" example:"[\"text\"]"`
	OutputModes              []string            `json:"outputModes" example:"[\"text\"]"`
	ModelProvider            []string            `json:"modelProvider" example:"[\"planning\"]"`
	AdditionalConfigurations map[string]any      `json:"additionalConfigurations" example:"{\"maxLlmCallsPerTask\":20,\"injectCurrentTime\":true}"`
}

type AgentSecretsDiffRequest struct {
	ID     string                `path:"id" json:"-" example:"0190f5e4-8b2c-7c3e-9a1d-2f4b6c8d0e1f"`
	Values *AgentSecretsDiffForm `json:"values" validate:"required"`
}

type AgentSecretsDiffForm struct {
	ToolsetConfigs []ToolsetConfig `json:"toolsetConfigs"`
	SkillConfigs   []SkillConfig   `json:"skillConfigs"`
}

type AgentPlatformDefaults struct {
	Broker           BrokerDefaults          `json:"broker"`
	Model            ModelDefaults           `json:"model"`
	SessionService   SessionServiceDefaults  `json:"sessionService"`
	ArtifactService  ArtifactServiceDefaults `json:"artifactService"`
	Log              LogDefaults             `json:"log"`
	ValidationLimits AgentValidationLimits   `json:"validationLimits"`
}

type BrokerDefaults struct {
	DevMode        string `json:"devMode" example:"false"`
	BrokerURL      string `json:"brokerUrl" example:"wss://broker.example.com:443"`
	BrokerUsername string `json:"brokerUsername" example:"sam-agent"`
	BrokerVPN      string `json:"brokerVpn" example:"default"`
	TemporaryQueue string `json:"temporaryQueue" example:"true"`
}

type ModelDefaults struct {
	Model   *string `json:"model" example:"openai/gpt-4o"`
	APIBase *string `json:"apiBase" example:"https://llm.example.com/v1"`
}

type SessionServiceDefaults struct {
	ServiceType     string `json:"serviceType" example:"sql"`
	DefaultBehavior string `json:"defaultBehavior" example:"persistent"`
}

type ArtifactServiceDefaults struct {
	ServiceType   string  `json:"serviceType" example:"s3"`
	BasePath      string  `json:"basePath" example:"/tmp/samv2"`
	BucketName    *string `json:"bucketName" example:"sam-artifacts"`
	EndpointURL   *string `json:"endpointUrl" example:"https://s3.example.com"`
	ArtifactScope string  `json:"artifactScope" example:"namespace"`
}

type LogDefaults struct {
	StdoutLogLevel string `json:"stdoutLogLevel" example:"info"`
	LogFileLevel   string `json:"logFileLevel" example:"info"`
	LogFile        string `json:"logFile" example:"{{agentName}}.log"`
}

type AgentValidationLimits struct {
	AgentInstructionMin int `json:"agentInstructionMin" example:"1"`
	AgentInstructionMax int `json:"agentInstructionMax" example:"32000"`
}

type UserAccess struct {
	UserID           string  `json:"userId" example:"0190f5e9-9c0d-7e1f-8a2b-3c4d5e6f7a8b"`
	DisplayName      *string `json:"displayName" example:"Jane Doe"`
	UserName         *string `json:"userName" example:"jane.doe"`
	Email            *string `json:"email" example:"jane.doe@example.com"`
	IsSystemUser     bool    `json:"isSystemUser" example:"false"`
	IsServiceAccount bool    `json:"isServiceAccount" example:"false"`
}

type listAgentsRequest struct {
	Page        Page   `json:"-"`
	ConnectorID string `query:"connectorId" json:"-" example:"0190f5e7-5e6f-7b8c-9d0e-1f2a3b4c5d6e"`
}

type componentAccessRequest struct {
	ID     string `path:"id" json:"-" example:"0190f5e4-8b2c-7c3e-9a1d-2f4b6c8d0e1f"`
	Page   Page   `json:"-"`
	Search string `query:"search" json:"-" example:"jane"`
}

type agentHandler struct {
	svc       *entplatform.AgentService
	deploySvc *entplatform.DeploymentService
	readSvc   *entplatform.AgentDeploymentService
	users     *entplatform.UsersService
	analytics *platpkg.ResourceReporter
	logger    *slog.Logger

	minInstructionChars int
	maxInstructionChars int
}

const agentPath = "/agents/{id}"

func agentRoutes(deps *entplatform.RouteDeps, core *platpkg.PlatformCore, logger *slog.Logger) []Route {
	if deps.AgentSvc == nil || deps.AgentDeploymentReadSvc == nil {
		return nil
	}
	h := &agentHandler{
		svc:                 deps.AgentSvc,
		deploySvc:           deps.AgentDeploySvc,
		readSvc:             deps.AgentDeploymentReadSvc,
		users:               deps.UsersSvc,
		analytics:           core.ResourceAnalytics(),
		logger:              logger,
		minInstructionChars: core.Config().AgentInstructionMinChars,
		maxInstructionChars: core.Config().AgentInstructionMaxChars,
	}
	routes := []Route{
		{Method: http.MethodGet, Path: "/agents", Scope: rbac.ScopeAgentBuilderRead, AllowAPIKey: true, Published: true,
			Stability: StabilityStable,
			Doc: Doc{ID: "listAgents", Summary: "Get a list of agents",
				Text: "Returns one page of agents, optionally only those that use one connector. Secret values are redacted.", Tag: agentsTag},
			Handler: Handle(h.list, "Unable to list agents")},
		{Method: http.MethodPost, Path: "/agents", Scope: rbac.ScopeAgentBuilderCreate, AllowAPIKey: true, Published: true,
			Stability: StabilityStable,
			Doc: Doc{ID: "createAgent", Summary: "Create an agent",
				Text: "Creates an agent without deploying it. Deploy it with POST /agentDeployments. Unknown additionalConfigurations keys are kept and listed in warnings.", Tag: agentsTag},
			Handler: Handle(h.create, "Unable to create agent")},
		{Method: http.MethodGet, Path: agentPath, Scope: rbac.ScopeAgentBuilderRead, AllowAPIKey: true, Published: true,
			Stability: StabilityStable,
			Doc: Doc{ID: "getAgent", Summary: "Get an agent",
				Text: "Returns one agent with its deployment and runtime status. Secret values are redacted.", Tag: agentsTag},
			Handler: Handle(h.get, "Unable to get agent")},
		{Method: http.MethodPut, Path: agentPath, Scope: rbac.ScopeAgentBuilderUpdate, AllowAPIKey: true, Published: true,
			Stability: StabilityStable,
			Doc: Doc{ID: "replaceAgent", Summary: "Replace an agent",
				Text: "Replaces the agent with the fields sent. A field left out is cleared, except toolsetConfigs and skillConfigs, which are kept unless sent as an empty array. The agent type cannot change. A deployed agent runs the new settings after an update through POST /agentDeployments.", Tag: agentsTag},
			Handler: Handle(h.update, "Unable to update agent")},
		{Method: http.MethodDelete, Path: agentPath, Scope: rbac.ScopeAgentBuilderDelete, AllowAPIKey: true, Published: true,
			Stability: StabilityStable,
			Doc: Doc{ID: "deleteAgent", Summary: "Delete an agent",
				Text: "Undeploys the agent if it is deployed, then deletes it. If the undeploy fails, the agent is kept and the request fails with 502.", Tag: agentsTag,
				Errors: []int{http.StatusBadGateway}},
			Handler: Handle(h.delete, "Unable to delete agent")},
		{Method: http.MethodPut, Path: "/agents/{id}/secretsDiff", Scope: rbac.ScopeAgentBuilderUpdate, AllowAPIKey: true, Published: true,
			Stability: StabilityStable,
			Doc: Doc{ID: "compareAgentSecrets", Summary: "Validate values against an agent's stored secrets",
				Text: "Returns, for each secret setting of the agent's toolsets and skills, whether saving the values sent would keep the stored secret. Secret values are never returned.", Tag: agentsTag},
			Handler: Handle(h.secretsDiff, "Unable to compare agent secrets")},
		{Method: http.MethodGet, Path: "/agentPlatformDefaults", Scope: rbac.ScopeAgentBuilderRead,
			Stability: StabilityDraft,
			Doc: Doc{ID: "getAgentPlatformDefaults", Summary: "Get the settings a new agent starts from",
				Text: "Returns the broker, model, session, artifact, and log settings a new agent starts from, and the bounds on an agent's instructions. Secrets are left out.", Tag: agentPlatformDefaultsTag},
			Handler: Handle(h.platformDefaults, "Unable to get agent platform defaults")},
		{Method: http.MethodPost, Path: "/agentDeployments", Scope: rbac.ScopeAgentBuilderDeploy, AllowAPIKey: true, Published: true,
			Stability: StabilityStable,
			Doc: Doc{ID: "createAgentDeployment", Summary: "Initiate an agent deployment",
				Text: "Deploys, updates, or undeploys an agent or remote agent and returns the deployment record. Send deploymentId to roll an agent back to the configuration an earlier deployment ran with. If the deploy runs and fails, the request fails with 502. If Agent Mesh refuses the settings while deploying, the request fails with 422. Both keep the deployment record, and the Location header links to it. If the server cannot deploy agents, the request fails with 503.", Tag: agentDeploymentsTag,
				Errors: []int{http.StatusBadGateway, http.StatusServiceUnavailable}},
			Handler: Handle(h.deploy, msgAgentDeployFailed)},
		{Method: http.MethodGet, Path: "/agentDeployments", Scope: rbac.ScopeDeploymentRead, AllowAPIKey: true, Published: true,
			Stability: StabilityStable,
			Doc: Doc{ID: "listAgentDeployments", Summary: "Get a list of agent deployments",
				Text: "Returns one page of agent and remote agent deployments, failed ones included, optionally only those of one agent.", Tag: agentDeploymentsTag},
			Handler: Handle(h.listDeployments, "Unable to list agent deployments")},
		{Method: http.MethodGet, Path: "/agentDeployments/{id}", Scope: rbac.ScopeDeploymentRead, AllowAPIKey: true, Published: true,
			Stability: StabilityStable,
			Doc: Doc{ID: "getAgentDeployment", Summary: "Get an agent deployment",
				Text: "Returns one deployment of an agent or remote agent, with why it failed when it did.", Tag: agentDeploymentsTag},
			Handler: Handle(h.getDeployment, "Unable to get agent deployment")},
	}
	if deps.UsersSvc != nil {
		routes = append(routes, Route{Method: http.MethodGet, Path: "/agents/{id}/access",
			AnyOfScopes: []string{rbac.ScopeRBACRead, rbac.ScopeAgentBuilderRead},
			Stability:   StabilityDraft,
			Deprecation: isSystemUserFieldDeprecation,
			Doc: Doc{ID: "listAgentAccess", Summary: "Get a list of the users who can invoke a agent",
				Text: "Returns one page of the users whose roles let them invoke the agent, optionally only those matching the search text. If user roles cannot be read, the request fails with 503.", Tag: agentsTag,
				Errors: []int{http.StatusServiceUnavailable}},
			Handler: Handle(h.access, "Unable to list agent access")})
	}
	return routes
}

func (h *agentHandler) list(ctx context.Context, req listAgentsRequest) (List[Agent], error) {
	if err := CheckUUIDQuery("connectorId", req.ConnectorID); err != nil {
		return List[Agent]{}, err
	}
	resp, err := h.svc.ListPaginated(ctx, req.Page.Number, req.Page.Size, req.ConnectorID)
	if err != nil {
		return List[Agent]{}, err
	}
	return ListOf(resp, toAgent), nil
}

func (h *agentHandler) create(ctx context.Context, req CreateAgentRequest) (Agent, error) {
	create := entplatform.CreateAgentRequest{
		Name:                     req.Name,
		Description:              req.Description,
		SystemPrompt:             req.SystemPrompt,
		Type:                     req.AgentType,
		Skills:                   toSkillRequests(req.Skills),
		SkillRefs:                req.SkillRefs,
		SkillIDs:                 req.SkillIDs,
		Toolsets:                 req.Toolsets,
		ToolsetExcludedTools:     req.ToolsetExcludedTools,
		ToolsetConfigs:           toToolsetConfigBindings(req.ToolsetConfigs),
		SkillConfigs:             toSkillConfigBindings(req.SkillConfigs),
		Connectors:               req.ConnectorIDs,
		InputModes:               req.InputModes,
		OutputModes:              req.OutputModes,
		ModelProvider:            req.ModelProvider,
		AdditionalConfigurations: rawConfigurations(req.AdditionalConfigurations),
	}
	warnings, err := h.validate(create.SystemPrompt, create.AdditionalConfigurations, nil)
	if err != nil {
		return Agent{}, err
	}
	resp, err := h.svc.Create(ctx, create, entplatform.UserIDFromContext(ctx))
	if err != nil {
		return Agent{}, inV2FieldNames(err)
	}
	resp.Warnings = warnings
	h.analytics.AgentCreated(ctx, len(resp.Toolsets), httputil.ParseClientSurface(req.Surface))
	return toAgent(resp), nil
}

func (h *agentHandler) get(ctx context.Context, req IDRequest) (Agent, error) {
	resp, err := h.svc.GetByID(ctx, req.ID)
	if err != nil {
		return Agent{}, err
	}
	return toAgent(resp), nil
}

func (h *agentHandler) update(ctx context.Context, req UpdateAgentRequest) (Agent, error) {
	skillIDs := nonNilList(req.SkillIDs)
	update := entplatform.UpdateAgentRequest{
		Name:                     req.Name,
		Description:              req.Description,
		SystemPrompt:             req.SystemPrompt,
		Skills:                   toSkillRequests(req.Skills),
		SkillRefs:                req.SkillRefs,
		SkillIDs:                 &skillIDs,
		Toolsets:                 req.Toolsets,
		ToolsetExcludedTools:     req.ToolsetExcludedTools,
		ToolsetConfigs:           toToolsetConfigBindings(req.ToolsetConfigs),
		SkillConfigs:             toSkillConfigBindings(req.SkillConfigs),
		Connectors:               req.ConnectorIDs,
		InputModes:               req.InputModes,
		OutputModes:              req.OutputModes,
		ModelProvider:            req.ModelProvider,
		AdditionalConfigurations: rawConfigurations(req.AdditionalConfigurations),
	}
	warnings, err := h.validate(update.SystemPrompt, update.AdditionalConfigurations, func() (json.RawMessage, error) {
		return h.svc.StoredAdditionalConfigurations(ctx, req.ID)
	})
	if err != nil {
		return Agent{}, err
	}
	resp, err := h.svc.Update(ctx, req.ID, update, entplatform.UserIDFromContext(ctx))
	if err != nil {
		return Agent{}, inV2FieldNames(err)
	}
	resp.Warnings = warnings
	return toAgent(resp), nil
}

func inV2FieldNames(err error) error {
	validation, ok := errors.AsType[*entplatform.ValidationError](err)
	if !ok {
		return err
	}
	problems, named := validation.Details["connectors"]
	if !named {
		return err
	}
	details := maps.Clone(validation.Details)
	delete(details, "connectors")
	details["connectorIds"] = problems
	return &entplatform.ValidationError{Message: validation.Message, Field: validation.Field, Details: details}
}

func (h *agentHandler) validate(systemPrompt string, configurations json.RawMessage, storedFor func() (json.RawMessage, error)) ([]string, error) {
	if err := entplatform.ValidateAgentInstruction(systemPrompt, h.minInstructionChars, h.maxInstructionChars); err != nil {
		return nil, err
	}
	return entplatform.ValidateAgentConfigurationsUpdate(configurations, storedFor)
}

func (h *agentHandler) delete(ctx context.Context, req IDRequest) (struct{}, error) {
	if err := h.svc.DeleteWithUndeploy(ctx, req.ID, entplatform.UserIDFromContext(ctx), h.deploySvc); err != nil {
		return struct{}{}, err
	}
	h.analytics.AgentDeleted(ctx)
	return struct{}{}, nil
}

func (h *agentHandler) secretsDiff(ctx context.Context, req AgentSecretsDiffRequest) (SecretsDiffResult, error) {
	candidate := entplatform.AgentOverlayCandidate{
		ToolsetConfigs: toToolsetConfigBindings(req.Values.ToolsetConfigs),
		SkillConfigs:   toSkillConfigBindings(req.Values.SkillConfigs),
	}
	diff, err := h.svc.GetOverlaySecretsDiff(ctx, req.ID, candidate)
	if err != nil {
		return SecretsDiffResult{}, err
	}
	entplatform.LogSecretsDiff(ctx, h.logger, "agent", req.ID, diff)
	return toSecretsDiffResult(diff), nil
}

func (h *agentHandler) platformDefaults(context.Context, struct{}) (AgentPlatformDefaults, error) {
	d := entplatform.ReadPlatformDefaults()
	return AgentPlatformDefaults{
		Broker: BrokerDefaults{
			DevMode:        d.BrokerDevMode,
			BrokerURL:      d.BrokerURL,
			BrokerUsername: d.BrokerUsername,
			BrokerVPN:      d.BrokerVPN,
			TemporaryQueue: d.BrokerTemporaryQueue,
		},
		Model:          ModelDefaults{Model: NullIfZero(d.Model), APIBase: NullIfZero(d.ModelAPIBase)},
		SessionService: SessionServiceDefaults{ServiceType: d.SessionType, DefaultBehavior: strings.ToLower(d.SessionDefaultBehavior)},
		ArtifactService: ArtifactServiceDefaults{
			ServiceType:   d.ArtifactType,
			BasePath:      d.ArtifactBasePath,
			BucketName:    NullIfZero(d.ArtifactBucketName),
			EndpointURL:   NullIfZero(d.ArtifactEndpointURL),
			ArtifactScope: d.ArtifactScope,
		},
		Log: LogDefaults{StdoutLogLevel: strings.ToLower(d.LogStdoutLevel), LogFileLevel: strings.ToLower(d.LogFileLevel), LogFile: d.LogFile},
		ValidationLimits: AgentValidationLimits{
			AgentInstructionMin: h.minInstructionChars,
			AgentInstructionMax: h.maxInstructionChars,
		},
	}, nil
}

func (h *agentHandler) access(ctx context.Context, req componentAccessRequest) (List[UserAccess], error) {
	resp, err := h.users.ListAgentAccess(ctx, req.ID, accessQuery(req))
	if err != nil {
		return List[UserAccess]{}, err
	}
	return ListOf(resp, toUserAccess), nil
}

func accessQuery(req componentAccessRequest) entplatform.ComponentAccessQuery {
	return entplatform.ComponentAccessQuery{
		PageNumber: req.Page.Number,
		PageSize:   req.Page.Size,
		Search:     strings.TrimSpace(req.Search),
	}
}

func toUserAccess(u *entplatform.ComponentAccessUser) UserAccess {
	return UserAccess{
		UserID:           u.ID,
		DisplayName:      NullIfZero(u.DisplayName),
		UserName:         NullIfZero(u.UserName),
		Email:            NullIfZero(u.Email),
		IsSystemUser:     u.IsSystemUser,
		IsServiceAccount: u.IsSystemUser,
	}
}

func toSecretsDiffResult(diff map[string]entplatform.SecretFieldDiffResult) SecretsDiffResult {
	result := SecretsDiffResult{Secrets: make(map[string]SecretMatch, len(diff))}
	for field, d := range diff {
		result.Secrets[field] = SecretMatch{Matches: d.Matches}
	}
	return result
}

const (
	toolEntryFailed       = "failed"
	msgToolEntryFailed    = "SAM could not register this entry's tools. The agent's log has the cause."
	msgToolEntryLostTools = "Some of this entry's tools were not registered. The agent's log has the cause."
)

func toolRegistrationErrorWithoutUpstreamText(r entplatform.ToolRegistrationResponse) *string {
	switch {
	case r.Status == toolEntryFailed:
		return NullIfZero(msgToolEntryFailed)
	case r.Error != "":
		return NullIfZero(msgToolEntryLostTools)
	}
	return nil
}

func toAgent(resp *entplatform.AgentResponse) Agent {
	var expires Instant
	if resp.ExpiresAt != nil {
		expires = Instant(*resp.ExpiresAt)
	}
	registrations := make([]ToolRegistration, len(resp.ToolRegistrations))
	for i, r := range resp.ToolRegistrations {
		registrations[i] = ToolRegistration{
			ToolType:    r.ToolType,
			ConnectorID: NullIfZero(r.ConnectorID),
			Name:        NullIfZero(r.Name),
			Status:      r.Status,
			Error:       toolRegistrationErrorWithoutUpstreamText(r),
			ToolCount:   r.ToolCount,
			ToolNames:   nonNilList(r.ToolNames),
		}
	}
	return Agent{
		ID:                       resp.ID,
		Type:                     "agent",
		AgentType:                resp.Type,
		Name:                     resp.Name,
		InstanceName:             resp.InstanceName,
		Description:              resp.Description,
		SystemPrompt:             resp.SystemPrompt,
		Toolsets:                 nonNilList(resp.Toolsets),
		ToolsetExcludedTools:     nilIfEmpty(resp.ToolsetExcludedTools),
		ToolsetConfigs:           toToolsetConfigs(resp.ToolsetConfigs),
		SkillConfigs:             toSkillConfigs(resp.SkillConfigs),
		SkillConfigFields:        toSkillConfigFields(resp.SkillConfigFields),
		Skills:                   toAgentSkills(resp.Skills),
		SkillRefs:                nonNilList(resp.SkillRefs),
		SkillIDs:                 nonNilList(resp.SkillIDs),
		InputModes:               nonNilList(resp.InputModes),
		OutputModes:              nonNilList(resp.OutputModes),
		ConnectorIDs:             nonNilList(resp.Connectors),
		ModelProvider:            nonNilList(resp.ModelProvider),
		AdditionalConfigurations: configurationsMap(resp.AdditionalConfigurations),
		DeploymentStatus:         displayStatus(resp.DeploymentStatus),
		SyncStatus:               resp.SyncStatus,
		Deployed:                 toDeployedAgent(resp.Deployed),
		RuntimeStatus:            toRuntimeStatus(resp.RuntimeStatus),
		ToolRegistrations:        registrations,
		Warnings:                 nonNilList(resp.Warnings),
		IsEphemeral:              resp.IsEphemeral,
		SourceSessionID:          NullIfZero(resp.SourceSessionID),
		SourceComponentName:      NullIfZero(resp.SourceComponentName),
		SourceComponentKind:      NullIfZero(resp.SourceComponentKind),
		ExpiresTime:              expires,
		CreatedBy:                resp.CreatedBy,
		UpdatedBy:                resp.UpdatedBy,
		CreatedTime:              Instant(resp.CreatedTime),
		UpdatedTime:              Instant(resp.UpdatedTime),
	}
}

func toDeployedAgent(deployed any) *DeployedAgent {
	info, ok := deployed.(*entplatform.DeployedAgentInfo)
	if !ok || info == nil {
		return nil
	}
	return &DeployedAgent{
		Name:                     info.Name,
		Description:              info.Description,
		SystemPrompt:             info.SystemPrompt,
		Toolsets:                 nonNilList(info.Toolsets),
		InputModes:               nonNilList(info.InputModes),
		OutputModes:              nonNilList(info.OutputModes),
		ConnectorIDs:             nonNilList(info.Connectors),
		AdditionalConfigurations: configurationsMap(info.AdditionalConfigurations),
		SkillIDs:                 nonNilList(info.SkillIDs),
		SkillRefs:                nonNilList(info.SkillRefs),
		Skills:                   toAgentSkills(info.Skills),
		DeployedTime:             Instant(info.DeployedAt),
		DeployedBy:               info.DeployedBy,
	}
}

func toAgentSkills(skills []entplatform.SkillResponse) []AgentSkill {
	out := make([]AgentSkill, len(skills))
	for i, s := range skills {
		out[i] = AgentSkill{
			ID:          NullIfZero(s.ID),
			Name:        s.Name,
			Description: s.Description,
			Tags:        nonNilList(s.Tags),
			Examples:    nonNilList(s.Examples),
			InputModes:  nonNilList(s.InputModes),
			OutputModes: nonNilList(s.OutputModes),
			Required:    s.Required,
			Optional:    s.Optional,
		}
	}
	return out
}

func toSkillRequests(skills []AgentSkill) []entplatform.SkillRequest {
	if skills == nil {
		return nil
	}
	out := make([]entplatform.SkillRequest, len(skills))
	for i, s := range skills {
		out[i] = entplatform.SkillRequest{
			ID:          orEmpty(s.ID),
			Name:        s.Name,
			Description: s.Description,
			Tags:        s.Tags,
			Examples:    s.Examples,
			InputModes:  s.InputModes,
			OutputModes: s.OutputModes,
			Required:    s.Required,
			Optional:    s.Optional,
		}
	}
	return out
}

func toToolsetConfigs(bindings []entplatform.ToolsetConfigBinding) []ToolsetConfig {
	out := make([]ToolsetConfig, len(bindings))
	for i, b := range bindings {
		out[i] = ToolsetConfig(b)
		out[i].VolumeBindings = nilIfEmpty(b.VolumeBindings)
		out[i].NetworkBindings = nilIfEmpty(b.NetworkBindings)
	}
	return out
}

func toToolsetConfigBindings(configs []ToolsetConfig) []entplatform.ToolsetConfigBinding {
	if configs == nil {
		return nil
	}
	out := make([]entplatform.ToolsetConfigBinding, len(configs))
	for i, c := range configs {
		out[i] = entplatform.ToolsetConfigBinding(c)
	}
	return out
}

func toSkillConfigs(bindings []entplatform.SkillConfigBinding) []SkillConfig {
	out := make([]SkillConfig, len(bindings))
	for i, b := range bindings {
		out[i] = SkillConfig{SkillID: b.SkillID, SkillName: NullIfZero(b.SkillName), ConfigValues: b.ConfigValues}
	}
	return out
}

func toSkillConfigBindings(configs []SkillConfig) []entplatform.SkillConfigBinding {
	if configs == nil {
		return nil
	}
	out := make([]entplatform.SkillConfigBinding, len(configs))
	for i, c := range configs {
		out[i] = entplatform.SkillConfigBinding{SkillID: c.SkillID, SkillName: orEmpty(c.SkillName), ConfigValues: c.ConfigValues}
	}
	return out
}

func toSkillConfigFields(fields map[string][]entplatform.SkillConfigField) map[string][]SkillConfigField {
	if len(fields) == 0 {
		return nil
	}
	out := make(map[string][]SkillConfigField, len(fields))
	for skill, list := range fields {
		converted := make([]SkillConfigField, len(list))
		for i, f := range list {
			converted[i] = SkillConfigField{
				ToolName:       f.ToolName,
				Key:            f.Key,
				FieldType:      f.Type,
				Secret:         f.Secret,
				Required:       f.Required,
				Description:    NullIfZero(f.Description),
				Default:        f.Default,
				CoveredBySkill: f.CoveredBySkill,
				CoveredByAgent: f.CoveredByAgent,
			}
		}
		out[skill] = converted
	}
	return out
}

func rawConfigurations(configurations map[string]any) json.RawMessage {
	if configurations == nil {
		return nil
	}
	raw, err := json.Marshal(configurations)
	if err != nil {
		panic(fmt.Sprintf("apiv2: a decoded JSON object failed to encode: %v", err))
	}
	return raw
}

func configurationsMap(raw json.RawMessage) map[string]any {
	var configurations map[string]any
	_ = json.Unmarshal(raw, &configurations)
	return nilIfEmpty(configurations)
}

func nilIfEmpty[M ~map[K]V, K comparable, V any](m M) M {
	if len(m) == 0 {
		return nil
	}
	return m
}

func orEmpty(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

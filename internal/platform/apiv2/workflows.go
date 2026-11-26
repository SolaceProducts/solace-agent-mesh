// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"context"
	"log/slog"
	"net/http"

	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

type Workflow struct {
	ID               string         `json:"id" example:"0190f5ea-2b3c-7d4e-9f5a-6b7c8d9e0f1a"`
	Type             string         `json:"type" example:"workflow"`
	Name             string         `json:"name" example:"Order triage"`
	Description      string         `json:"description" example:"Routes each new order to the team that fulfils it."`
	AppConfig        map[string]any `json:"appConfig" example:"{\"agent_type\":\"workflow\",\"workflow\":{\"nodes\":[{\"id\":\"start\",\"name\":\"start\",\"type\":\"agent\",\"agent_name\":\"agent_0190f5e4_8b2c_7c3e_9a1d_2f4b6c8d0e1f\"}],\"output_mapping\":{\"result\":\"{{start.output}}\"}}}"`
	DeploymentStatus string         `json:"deploymentStatus" example:"deployed"`
	RuntimeStatus    RuntimeStatus  `json:"runtimeStatus"`
	CreatedBy        string         `json:"createdBy" example:"jane.doe@example.com"`
	UpdatedBy        string         `json:"updatedBy" example:"jane.doe@example.com"`
	CreatedTime      Instant        `json:"createdTime" example:"\"2026-01-02T03:04:05.678Z\""`
	UpdatedTime      Instant        `json:"updatedTime" example:"\"2026-01-03T10:15:30.000Z\""`
}

type WorkflowConfiguration struct {
	ConfigurationFile string `json:"configurationFile" example:"apps:\n    - app_config:\n        agent_type: workflow\n      name: workflow_0190f5ea_2b3c_7d4e_9f5a_6b7c8d9e0f1a\n"`
}

type CreateWorkflowRequest struct {
	Name        string         `json:"name" validate:"required,min=3,max=255" example:"Order triage"`
	Description string         `json:"description" validate:"required,min=10,max=2000" example:"Routes each new order to the team that fulfils it."`
	AppConfig   map[string]any `json:"appConfig" validate:"required" example:"{\"agent_type\":\"workflow\",\"workflow\":{\"nodes\":[{\"id\":\"start\",\"name\":\"start\",\"type\":\"agent\",\"agent_name\":\"agent_0190f5e4_8b2c_7c3e_9a1d_2f4b6c8d0e1f\"}],\"output_mapping\":{\"result\":\"{{start.output}}\"}}}"`
}

type UpdateWorkflowRequest struct {
	ID          string         `path:"id" json:"-" example:"0190f5ea-2b3c-7d4e-9f5a-6b7c8d9e0f1a"`
	Name        string         `json:"name" validate:"required,min=3,max=255" example:"Order triage"`
	Description string         `json:"description" validate:"required,min=10,max=2000" example:"Routes each new order to the team that fulfils it."`
	AppConfig   map[string]any `json:"appConfig" validate:"required" example:"{\"agent_type\":\"workflow\",\"workflow\":{\"nodes\":[{\"id\":\"start\",\"name\":\"start\",\"type\":\"agent\",\"agent_name\":\"agent_0190f5e4_8b2c_7c3e_9a1d_2f4b6c8d0e1f\"}],\"output_mapping\":{\"result\":\"{{start.output}}\"}}}"`
}

type listWorkflowsRequest struct {
	Page Page `json:"-"`
}

type workflowHandler struct {
	svc       *entplatform.WorkflowService
	deploySvc *entplatform.WorkflowDeploymentService
	users     *entplatform.UsersService
	analytics *platpkg.ResourceReporter
}

const workflowPath = "/workflows/{id}"

func workflowRoutes(deps *entplatform.RouteDeps, core *platpkg.PlatformCore, _ *slog.Logger) []Route {
	if deps.WorkflowSvc == nil {
		return nil
	}
	h := &workflowHandler{
		svc:       deps.WorkflowSvc,
		deploySvc: deps.WorkflowDeploySvc,
		users:     deps.UsersSvc,
		analytics: core.ResourceAnalytics(),
	}
	routes := []Route{
		{Method: http.MethodGet, Path: "/workflows", Scope: rbac.ScopeWorkflowBuilderRead, AllowAPIKey: true, Published: true,
			Stability: StabilityStable,
			Doc: Doc{ID: "listWorkflows", Summary: "Get a list of workflows",
				Text: "Returns one page of workflows with their deployment and runtime status.", Tag: workflowsTag},
			Handler: Handle(h.list, "Unable to list workflows")},
		{Method: http.MethodPost, Path: "/workflows", Scope: rbac.ScopeWorkflowBuilderCreate, AllowAPIKey: true, Published: true,
			Stability: StabilityStable,
			Doc: Doc{ID: "createWorkflow", Summary: "Create a workflow",
				Text: "Creates a workflow without deploying it. Deploy it with POST /workflowDeployments. If the name is taken, the request fails with 409.", Tag: workflowsTag},
			Handler: Handle(h.create, "Unable to create workflow")},
		{Method: http.MethodGet, Path: workflowPath, Scope: rbac.ScopeWorkflowBuilderRead, AllowAPIKey: true, Published: true,
			Stability: StabilityStable,
			Doc: Doc{ID: "getWorkflow", Summary: "Get a workflow",
				Text: "Returns one workflow with its deployment and runtime status.", Tag: workflowsTag},
			Handler: Handle(h.get, "Unable to get workflow")},
		{Method: http.MethodPut, Path: workflowPath, Scope: rbac.ScopeWorkflowBuilderUpdate, AllowAPIKey: true, Published: true,
			Stability: StabilityStable,
			Doc: Doc{ID: "replaceWorkflow", Summary: "Replace a workflow",
				Text: "Replaces the workflow's name, description, and app configuration. A deployed workflow runs its new settings after it is updated through POST /workflowDeployments.", Tag: workflowsTag},
			Handler: Handle(h.update, "Unable to update workflow")},
		{Method: http.MethodDelete, Path: workflowPath, Scope: rbac.ScopeWorkflowBuilderDelete, AllowAPIKey: true, Published: true,
			Stability: StabilityStable,
			Doc: Doc{ID: "deleteWorkflow", Summary: "Delete a workflow",
				Text: "Undeploys the workflow if it is deployed, then deletes it. If the undeploy fails, the workflow is kept and the request fails with 502.", Tag: workflowsTag,
				Errors: []int{http.StatusBadGateway}},
			Handler: Handle(h.delete, "Unable to delete workflow")},
		{Method: http.MethodGet, Path: "/workflows/{id}/configuration", Scope: rbac.ScopeWorkflowBuilderRead,
			Stability: StabilityDraft,
			Doc: Doc{ID: "getWorkflowConfiguration", Summary: "Get a workflow's configuration",
				Text: "Returns the YAML the workflow deploys as.", Tag: workflowsTag},
			Handler: Handle(h.configuration, "Unable to get workflow configuration")},
	}
	if h.deploySvc != nil {
		routes = append(routes, workflowDeploymentRoutes(h)...)
	}
	if deps.UsersSvc != nil {
		routes = append(routes, Route{Method: http.MethodGet, Path: "/workflows/{id}/access",
			AnyOfScopes: []string{rbac.ScopeRBACRead, rbac.ScopeWorkflowBuilderRead},
			Stability:   StabilityDraft,
			Deprecation: isSystemUserFieldDeprecation,
			Doc: Doc{ID: "listWorkflowAccess", Summary: "Get a list of the users who can invoke a workflow",
				Text: "Returns one page of the users whose roles let them invoke the workflow, optionally only those whose display name, username, or email contains the search text. If user roles cannot be read, the request fails with 503.", Tag: workflowsTag,
				Errors: []int{http.StatusServiceUnavailable}},
			Handler: Handle(h.access, "Unable to list workflow access")})
	}
	return routes
}

func (h *workflowHandler) list(ctx context.Context, req listWorkflowsRequest) (List[Workflow], error) {
	resp, err := h.svc.ListPaginated(ctx, req.Page.Number, req.Page.Size)
	if err != nil {
		return List[Workflow]{}, err
	}
	return ListOf(resp, toWorkflow), nil
}

const msgWorkflowNameTaken = "A workflow with that name already exists"

func (h *workflowHandler) create(ctx context.Context, req CreateWorkflowRequest) (Workflow, error) {
	resp, err := h.svc.Create(ctx, entplatform.CreateWorkflowRequest(req), entplatform.UserIDFromContext(ctx))
	if err != nil {
		return Workflow{}, explainConflict(err, msgWorkflowNameTaken)
	}
	h.analytics.WorkflowCreated(ctx)
	return toWorkflow(resp), nil
}

func (h *workflowHandler) get(ctx context.Context, req IDRequest) (Workflow, error) {
	resp, err := h.svc.GetByID(ctx, req.ID)
	if err != nil {
		return Workflow{}, err
	}
	return toWorkflow(resp), nil
}

func (h *workflowHandler) update(ctx context.Context, req UpdateWorkflowRequest) (Workflow, error) {
	resp, err := h.svc.Update(ctx, req.ID, entplatform.UpdateWorkflowRequest{
		Name: req.Name, Description: req.Description, AppConfig: req.AppConfig,
	}, entplatform.UserIDFromContext(ctx))
	if err != nil {
		return Workflow{}, explainConflict(err, msgWorkflowNameTaken)
	}
	return toWorkflow(resp), nil
}

func (h *workflowHandler) delete(ctx context.Context, req IDRequest) (struct{}, error) {
	if err := h.svc.DeleteWithUndeploy(ctx, req.ID, entplatform.UserIDFromContext(ctx), h.deploySvc); err != nil {
		return struct{}{}, err
	}
	return struct{}{}, nil
}

func (h *workflowHandler) configuration(ctx context.Context, req IDRequest) (WorkflowConfiguration, error) {
	resp, err := h.svc.GetConfiguration(ctx, req.ID)
	if err != nil {
		return WorkflowConfiguration{}, err
	}
	return WorkflowConfiguration(*resp), nil
}

func (h *workflowHandler) access(ctx context.Context, req componentAccessRequest) (List[UserAccess], error) {
	resp, err := h.users.ListWorkflowAccess(ctx, req.ID, accessQuery(req))
	if err != nil {
		return List[UserAccess]{}, err
	}
	return ListOf(resp, toUserAccess), nil
}

func toWorkflow(resp *entplatform.WorkflowResponse) Workflow {
	return Workflow{
		ID:               resp.ID,
		Type:             "workflow",
		Name:             resp.Name,
		Description:      resp.Description,
		AppConfig:        resp.AppConfig,
		DeploymentStatus: displayStatus(resp.DeploymentStatus),
		RuntimeStatus:    toRuntimeStatus(resp.RuntimeStatus),
		CreatedBy:        resp.CreatedBy,
		UpdatedBy:        resp.UpdatedBy,
		CreatedTime:      Instant(resp.CreatedTime),
		UpdatedTime:      Instant(resp.UpdatedTime),
	}
}

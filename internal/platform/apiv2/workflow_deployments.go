// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"context"
	"net/http"

	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

type WorkflowDeployment struct {
	ID          string             `json:"id" example:"0190f5eb-4d5e-7f6a-8b7c-9d0e1f2a3b4c"`
	Type        string             `json:"type" example:"workflowDeployment"`
	WorkflowID  string             `json:"workflowId" example:"0190f5ea-2b3c-7d4e-9f5a-6b7c8d9e0f1a"`
	Status      string             `json:"status" example:"success"`
	Action      string             `json:"action" example:"deploy"`
	Failure     *DeploymentFailure `json:"failure"`
	CreatedBy   string             `json:"createdBy" example:"jane.doe@example.com"`
	CreatedTime Instant            `json:"createdTime" example:"\"2026-01-02T03:04:05.678Z\""`
	UpdatedTime Instant            `json:"updatedTime" example:"\"2026-01-03T10:15:30.000Z\""`
}

type CreateWorkflowDeploymentRequest struct {
	WorkflowID   string `json:"workflowId" validate:"required,uuid" example:"0190f5ea-2b3c-7d4e-9f5a-6b7c8d9e0f1a"`
	Action       string `json:"action" validate:"required,oneof=deploy update undeploy" example:"deploy"`
	TestMode     bool   `json:"testMode" example:"false"`
	DeploymentID string `json:"deploymentId" validate:"omitempty,uuid" example:"0190f5eb-1e2f-7a3b-9c4d-5e6f7a8b9c0d"`
}

const msgWorkflowDeployFailed = "Unable to deploy workflow"

func workflowDeploymentRoutes(h *workflowHandler) []Route {
	return []Route{
		{Method: http.MethodPost, Path: "/workflowDeployments", Scope: rbac.ScopeWorkflowBuilderDeploy, AllowAPIKey: true, Published: true,
			Stability: StabilityStable,
			Doc: Doc{ID: "createWorkflowDeployment", Summary: "Initiate a workflow deployment",
				Text: "Deploys, updates, or undeploys a workflow and returns the deployment record. Send deploymentId to roll back to the configuration an earlier deployment ran with. If the deploy runs and fails, the request fails with 502. If it is refused while deploying, such as for the workflow's own configuration, the request fails with 422. Both keep the record and link it in the Location header.", Tag: workflowDeploymentsTag,
				Errors: []int{http.StatusBadGateway}},
			Handler: Handle(h.deploy, msgWorkflowDeployFailed)},
		{Method: http.MethodGet, Path: "/workflowDeployments", Scope: rbac.ScopeDeploymentRead, AllowAPIKey: true, Published: true,
			Stability: StabilityStable,
			Doc: Doc{ID: "listWorkflowDeployments", Summary: "Get a list of workflow deployments",
				Text: "Returns one page of workflow deployments, failed ones included.", Tag: workflowDeploymentsTag},
			Handler: Handle(h.listDeployments, "Unable to list workflow deployments")},
		{Method: http.MethodGet, Path: "/workflowDeployments/{id}", Scope: rbac.ScopeDeploymentRead, AllowAPIKey: true, Published: true,
			Stability: StabilityStable,
			Doc: Doc{ID: "getWorkflowDeployment", Summary: "Get a workflow deployment",
				Text: "Returns one deployment of a workflow, with why it failed when it did.", Tag: workflowDeploymentsTag},
			Handler: Handle(h.getDeployment, "Unable to get workflow deployment")},
	}
}

func (h *workflowHandler) deploy(ctx context.Context, req CreateWorkflowDeploymentRequest) (WorkflowDeployment, error) {
	if details := actionProblems(req.Action, req.DeploymentID, "testMode", req.TestMode); len(details) > 0 {
		return WorkflowDeployment{}, &CallerError{Status: http.StatusUnprocessableEntity, Message: msgValidationFailed, Details: details}
	}
	svcReq := entplatform.CreateWorkflowDeploymentRequest{WorkflowID: req.WorkflowID, Action: req.Action, TestMode: req.TestMode}
	var resp *entplatform.WorkflowDeploymentResponse
	var err error
	if req.DeploymentID != "" {
		resp, err = h.deploySvc.Rollback(ctx, svcReq, req.DeploymentID, entplatform.UserIDFromContext(ctx))
	} else {
		resp, err = h.deploySvc.Create(ctx, svcReq, entplatform.UserIDFromContext(ctx))
	}
	if err != nil {
		return WorkflowDeployment{}, keepUnfilledSentence(err)
	}
	h.analytics.WorkflowDeployed(ctx, resp.ID)
	if resp.Status == entplatform.DeployStatusFailed {
		return WorkflowDeployment{}, failedDeployment(msgWorkflowDeployFailed, "workflowDeployments", resp.ID, "workflowId", resp.FailureReason, resp.ErrorMessage)
	}
	return toWorkflowDeployment(resp), nil
}

func (h *workflowHandler) listDeployments(ctx context.Context, req listWorkflowsRequest) (List[WorkflowDeployment], error) {
	resp, err := h.deploySvc.ListPaginated(ctx, req.Page.Number, req.Page.Size)
	if err != nil {
		return List[WorkflowDeployment]{}, err
	}
	return ListOf(resp, toWorkflowDeployment), nil
}

func (h *workflowHandler) getDeployment(ctx context.Context, req IDRequest) (WorkflowDeployment, error) {
	resp, err := h.deploySvc.GetByID(ctx, req.ID)
	if err != nil {
		return WorkflowDeployment{}, err
	}
	return toWorkflowDeployment(resp), nil
}

func toWorkflowDeployment(d *entplatform.WorkflowDeploymentResponse) WorkflowDeployment {
	return WorkflowDeployment{
		ID:          d.ID,
		Type:        "workflowDeployment",
		WorkflowID:  d.WorkflowID,
		Status:      d.Status,
		Action:      d.Action,
		Failure:     deploymentFailure("workflow", d.Status, d.FailureReason, d.ErrorID),
		CreatedBy:   d.CreatedBy,
		CreatedTime: Instant(d.CreatedTime),
		UpdatedTime: Instant(d.UpdatedTime),
	}
}

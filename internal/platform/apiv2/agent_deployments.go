// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"context"
	"net/http"

	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
)

type AgentDeployment struct {
	ID          string             `json:"id" example:"0190f5e5-1a2b-7d4e-8f60-718293a4b5c6"`
	Type        string             `json:"type" example:"agentDeployment"`
	AgentID     string             `json:"agentId" example:"0190f5e4-8b2c-7c3e-9a1d-2f4b6c8d0e1f"`
	Status      string             `json:"status" example:"success"`
	Action      string             `json:"action" example:"deploy"`
	Failure     *DeploymentFailure `json:"failure"`
	CreatedBy   string             `json:"createdBy" example:"jane.doe@example.com"`
	CreatedTime Instant            `json:"createdTime" example:"\"2026-01-02T03:04:05.678Z\""`
	UpdatedTime Instant            `json:"updatedTime" example:"\"2026-01-03T10:15:30.000Z\""`
}

type CreateAgentDeploymentRequest struct {
	AgentID       string `json:"agentId" validate:"required,uuid" example:"0190f5e4-8b2c-7c3e-9a1d-2f4b6c8d0e1f"`
	Action        string `json:"action" validate:"required,oneof=deploy update undeploy" example:"deploy"`
	SkipIfCurrent bool   `json:"skipIfCurrent" example:"false"`
	DeploymentID  string `json:"deploymentId" validate:"omitempty,uuid" example:"0190f5e5-0c1d-7e2f-9a3b-4c5d6e7f8091"`
}

type listAgentDeploymentsRequest struct {
	Page    Page   `json:"-"`
	AgentID string `query:"agentId" json:"-" example:"0190f5e4-8b2c-7c3e-9a1d-2f4b6c8d0e1f"`
}

const (
	msgAgentDeployFailed        = "Unable to deploy agent"
	msgDeployServiceUnavailable = "Deployment service not available"
)

func (h *agentHandler) deploy(ctx context.Context, req CreateAgentDeploymentRequest) (AgentDeployment, error) {
	if h.deploySvc == nil {
		return AgentDeployment{}, &CallerError{Status: http.StatusServiceUnavailable, Message: msgDeployServiceUnavailable}
	}
	if details := actionProblems(req.Action, req.DeploymentID, "skipIfCurrent", req.SkipIfCurrent); len(details) > 0 {
		return AgentDeployment{}, &CallerError{Status: http.StatusUnprocessableEntity, Message: msgValidationFailed, Details: details}
	}
	svcReq := entplatform.CreateAgentDeploymentRequest{AgentID: req.AgentID, Action: req.Action, SkipIfCurrent: req.SkipIfCurrent}
	var resp *entplatform.AgentDeploymentResponse
	var err error
	if req.DeploymentID != "" {
		resp, err = h.deploySvc.Rollback(ctx, svcReq, req.DeploymentID, entplatform.UserIDFromContext(ctx))
	} else {
		resp, err = h.deploySvc.Create(ctx, svcReq, entplatform.UserIDFromContext(ctx))
	}
	if err != nil {
		return AgentDeployment{}, keepUnfilledSentence(err)
	}
	h.analytics.AgentDeployed(ctx, resp.ID)
	if resp.Status == entplatform.DeployStatusFailed {
		return AgentDeployment{}, failedDeployment(msgAgentDeployFailed, "agentDeployments", resp.ID, "agentId", resp.FailureReason, resp.ErrorMessage)
	}
	return toAgentDeployment(resp), nil
}

func actionProblems(action, deploymentID, option string, optionSet bool) map[string][]string {
	details := map[string][]string{}
	switch {
	case action == entplatform.DeployActionUndeploy:
		if deploymentID != "" {
			details["deploymentId"] = []string{"must be left out for undeploy"}
		}
		if optionSet {
			details[option] = []string{"must be false for undeploy"}
		}
	case deploymentID != "" && optionSet:
		details[option] = []string{"must be false with deploymentId"}
	}
	return details
}

func (h *agentHandler) listDeployments(ctx context.Context, req listAgentDeploymentsRequest) (List[AgentDeployment], error) {
	if err := CheckUUIDQuery("agentId", req.AgentID); err != nil {
		return List[AgentDeployment]{}, err
	}
	resp, err := h.readSvc.ListPaginated(ctx, req.Page.Number, req.Page.Size, req.AgentID)
	if err != nil {
		return List[AgentDeployment]{}, err
	}
	return ListOf(resp, toAgentDeployment), nil
}

func (h *agentHandler) getDeployment(ctx context.Context, req IDRequest) (AgentDeployment, error) {
	resp, err := h.readSvc.GetByID(ctx, req.ID)
	if err != nil {
		return AgentDeployment{}, err
	}
	return toAgentDeployment(resp), nil
}

func toAgentDeployment(d *entplatform.AgentDeploymentResponse) AgentDeployment {
	return AgentDeployment{
		ID:          d.ID,
		Type:        "agentDeployment",
		AgentID:     d.AgentID,
		Status:      d.Status,
		Action:      d.Action,
		Failure:     deploymentFailure("agent", d.Status, d.FailureReason, d.ErrorID),
		CreatedBy:   d.CreatedBy,
		CreatedTime: Instant(d.CreatedTime),
		UpdatedTime: Instant(d.UpdatedTime),
	}
}

func displayStatus(status string) string {
	if status == "" {
		return entplatform.DisplayStatusNotDeployed
	}
	return status
}

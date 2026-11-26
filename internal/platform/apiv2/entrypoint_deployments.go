// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"context"
	"errors"
	"net/http"

	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/apierr"
)

type EntrypointDeployment struct {
	ID           string             `json:"id" example:"0190b2c5-3a7e-7d21-8f4b-6c2d9e1a5b37"`
	Type         string             `json:"type" example:"entrypointDeployment"`
	EntrypointID string             `json:"entrypointId" example:"0190b2c4-6f1a-7c3e-9d52-3a1f5b7e8c90"`
	Status       string             `json:"status" example:"failed"`
	Action       string             `json:"action" example:"deploy"`
	Failure      *DeploymentFailure `json:"failure"`
	CreatedBy    string             `json:"createdBy" example:"user@example.com"`
	CreatedTime  Instant            `json:"createdTime"`
	UpdatedTime  Instant            `json:"updatedTime"`
}

type CreateEntrypointDeploymentRequest struct {
	EntrypointID string `json:"entrypointId" validate:"required,uuid" example:"0190b2c4-6f1a-7c3e-9d52-3a1f5b7e8c90"`
	Action       string `json:"action" validate:"required,oneof=deploy update undeploy" example:"deploy"`
}

type listEntrypointDeploymentsRequest struct {
	Page         Page   `json:"-"`
	EntrypointID string `query:"entrypointId" json:"-" example:"0190b2c4-6f1a-7c3e-9d52-3a1f5b7e8c90"`
}

const msgDeployFailed = "Unable to deploy entrypoint"

func (h *entrypointHandler) deploy(ctx context.Context, req CreateEntrypointDeploymentRequest) (EntrypointDeployment, error) {
	if !h.deploySvc.HasControlPlane() {
		return EntrypointDeployment{}, &CallerError{Status: http.StatusServiceUnavailable, Message: msgDeployFailed, Cause: errors.New("no broker connection")}
	}
	resp, err := h.deploySvc.Deploy(ctx, entplatform.CreateGatewayDeploymentRequest{
		GatewayID: req.EntrypointID, Action: req.Action,
	}, entplatform.UserIDFromContext(ctx))
	switch {
	case err == nil:
		return toEntrypointDeployment(resp), nil
	case resp == nil:
		return EntrypointDeployment{}, keepUnfilledSentence(UnderValues(err))
	}
	location := platpkg.APIV2Prefix + "/entrypointDeployments/" + resp.ID
	status, details := http.StatusBadGateway, map[string][]string(nil)
	if IsErr[*apierr.ValidationError](err) {
		status, details = ClassifyServiceError(err)
	}
	err = &CallerError{Status: status, Message: msgDeployFailed, Details: details, Cause: err, ErrorID: resp.ErrorID}
	return EntrypointDeployment{}, &LocatedError{Err: err, Location: location}
}

func (h *entrypointHandler) listDeployments(ctx context.Context, req listEntrypointDeploymentsRequest) (List[EntrypointDeployment], error) {
	if err := CheckUUIDQuery("entrypointId", req.EntrypointID); err != nil {
		return List[EntrypointDeployment]{}, err
	}
	resp, err := h.deploySvc.ListPaginated(ctx, req.Page.Number, req.Page.Size, req.EntrypointID)
	if err != nil {
		return List[EntrypointDeployment]{}, err
	}
	items := make([]EntrypointDeployment, len(resp.Data))
	for i := range resp.Data {
		items[i] = toEntrypointDeployment(&resp.Data[i])
	}
	return List[EntrypointDeployment]{Items: items, Total: resp.Meta.Pagination.Count}, nil
}

func (h *entrypointHandler) getDeployment(ctx context.Context, req IDRequest) (EntrypointDeployment, error) {
	resp, err := h.deploySvc.GetByID(ctx, req.ID)
	if err != nil {
		return EntrypointDeployment{}, err
	}
	return toEntrypointDeployment(resp), nil
}

func toEntrypointDeployment(d *entplatform.GatewayDeploymentResponse) EntrypointDeployment {
	return EntrypointDeployment{
		ID:           d.ID,
		Type:         "entrypointDeployment",
		EntrypointID: d.GatewayID,
		Status:       d.Status,
		Action:       d.Action,
		Failure:      deploymentFailure("entrypoint", d.Status, d.FailureReason, d.ErrorID),
		CreatedBy:    d.CreatedBy,
		CreatedTime:  Instant(d.CreatedTime),
		UpdatedTime:  Instant(d.UpdatedTime),
	}
}

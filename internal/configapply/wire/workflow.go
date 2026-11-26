// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package wire

import (
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/apiv2"
)

type CreateWorkflowRequest struct {
	Name        string         `json:"name" validate:"required,min=3,max=255"`
	Description string         `json:"description" validate:"required,min=10,max=2000"`
	AppConfig   map[string]any `json:"appConfig" validate:"required"`
}

type UpdateWorkflowRequest struct {
	Name        string         `json:"name" validate:"required,min=3,max=255"`
	Description string         `json:"description" validate:"required,min=10,max=2000"`
	AppConfig   map[string]any `json:"appConfig" validate:"required"`
}

type WorkflowResponse struct {
	ID               string              `json:"id" cfgapply:"read-only"`
	Type             string              `json:"type" cfgapply:"read-only"`
	Name             string              `json:"name"`
	Description      string              `json:"description"`
	AppConfig        map[string]any      `json:"appConfig"`
	DeploymentStatus string              `json:"deploymentStatus" cfgapply:"read-only"`
	RuntimeStatus    apiv2.RuntimeStatus `json:"runtimeStatus" cfgapply:"read-only"`
	CreatedBy        string              `json:"createdBy" cfgapply:"read-only"`
	UpdatedBy        string              `json:"updatedBy" cfgapply:"read-only"`
	CreatedTime      apiv2.Instant       `json:"createdTime" cfgapply:"read-only"`
	UpdatedTime      apiv2.Instant       `json:"updatedTime" cfgapply:"read-only"`
}

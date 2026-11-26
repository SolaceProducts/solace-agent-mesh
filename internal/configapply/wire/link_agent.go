// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package wire

import (
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/apiv2"
)

type CreateLinkAgentRequest struct {
	Name             string                      `json:"name" validate:"required,min=3,max=40,safename"`
	Description      string                      `json:"description" validate:"omitempty,safetext"`
	EntrypointID     string                      `json:"entrypointId" validate:"required,uuid" cfgapply:"key=entrypoint,xref=entrypoint"`
	TokenSHA256      string                      `json:"tokenSha256" validate:"omitempty,max=64" cfgapply:"secret" doc:"required"`
	Enabled          *bool                       `json:"enabled"`
	Owner            string                      `json:"owner" validate:"omitempty,max=256"`
	Team             string                      `json:"team" validate:"omitempty,max=256"`
	Peers            []string                    `json:"peers" validate:"omitempty,max=64"`
	ShareUserProfile bool                        `json:"shareUserProfile"`
	Wake             *apiv2.LinkAgentWakeRequest `json:"wake"`
}

type UpdateLinkAgentRequest struct {
	Name             string                      `json:"name" validate:"required,min=3,max=40,safename"`
	Description      string                      `json:"description" validate:"omitempty,safetext"`
	EntrypointID     string                      `json:"entrypointId" validate:"required,uuid" cfgapply:"key=entrypoint,xref=entrypoint"`
	TokenSHA256      string                      `json:"tokenSha256" validate:"omitempty,max=64" cfgapply:"secret" doc:"required"`
	Enabled          *bool                       `json:"enabled"`
	Owner            string                      `json:"owner" validate:"omitempty,max=256"`
	Team             string                      `json:"team" validate:"omitempty,max=256"`
	Peers            []string                    `json:"peers" validate:"omitempty,max=64"`
	ShareUserProfile bool                        `json:"shareUserProfile"`
	Wake             *apiv2.LinkAgentWakeRequest `json:"wake"`
}

type LinkAgentResponse struct {
	ID               string                   `json:"id" cfgapply:"read-only"`
	Type             string                   `json:"type" cfgapply:"read-only"`
	Name             string                   `json:"name"`
	InstanceName     string                   `json:"instanceName" cfgapply:"read-only"`
	Description      *string                  `json:"description"`
	EntrypointID     string                   `json:"entrypointId" cfgapply:"xref=entrypoint"`
	ConnectURL       *string                  `json:"connectUrl" cfgapply:"read-only"`
	ConnectPath      string                   `json:"connectPath" cfgapply:"read-only"`
	TokenPrefix      *string                  `json:"tokenPrefix" cfgapply:"read-only"`
	Enabled          bool                     `json:"enabled"`
	Owner            *string                  `json:"owner"`
	Team             *string                  `json:"team"`
	Peers            []string                 `json:"peers"`
	ShareUserProfile bool                     `json:"shareUserProfile"`
	Wake             *apiv2.LinkAgentWake     `json:"wake"`
	DeploymentStatus string                   `json:"deploymentStatus" cfgapply:"read-only"`
	SyncStatus       *string                  `json:"syncStatus" cfgapply:"read-only"`
	RuntimeStatus    apiv2.RuntimeStatus      `json:"runtimeStatus" cfgapply:"read-only"`
	Deployed         *apiv2.DeployedLinkAgent `json:"deployed" cfgapply:"read-only"`
	CreatedBy        string                   `json:"createdBy" cfgapply:"read-only"`
	UpdatedBy        string                   `json:"updatedBy" cfgapply:"read-only"`
	CreatedTime      apiv2.Instant            `json:"createdTime" cfgapply:"read-only"`
	UpdatedTime      apiv2.Instant            `json:"updatedTime" cfgapply:"read-only"`
}

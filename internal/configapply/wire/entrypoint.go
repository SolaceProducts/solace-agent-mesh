// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package wire

import (
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/apiv2"
)

type CreateEntrypointRequest struct {
	Name           string         `json:"name" validate:"required,min=3,max=255,safename"`
	Slug           string         `json:"slug" validate:"omitempty,min=3,max=63,gatewayslug" cfgapply:"omit-hash"`
	Description    string         `json:"description" validate:"required,min=10,max=1000,safetext"`
	EntrypointType string         `json:"entrypointType" validate:"required,min=2,max=50" cfgapply:"key=type,discriminator"`
	PublicURL      string         `json:"publicUrl"`
	Values         map[string]any `json:"values" cfgapply:"secret"`
}

type UpdateEntrypointRequest struct {
	Name           Patch[string]         `json:"name,omitzero"`
	Description    Patch[string]         `json:"description,omitzero"`
	PublicURL      Patch[string]         `json:"publicUrl,omitzero" clearable:"true"`
	Values         Patch[map[string]any] `json:"values,omitzero" cfgapply:"secret,merge"`
	EntrypointType Patch[string]         `json:"entrypointType,omitzero" cfgapply:"key=type,discriminator,read-only"`
	Slug           Patch[string]         `json:"slug,omitzero" cfgapply:"read-only"`
}

type EntrypointResponse struct {
	ID               string                    `json:"id" cfgapply:"read-only"`
	Type             string                    `json:"type" cfgapply:"read-only"`
	EntrypointType   string                    `json:"entrypointType" cfgapply:"discriminator"`
	Slug             *string                   `json:"slug" cfgapply:"read-only"`
	Name             string                    `json:"name"`
	Description      string                    `json:"description"`
	PublicURL        *string                   `json:"publicUrl"`
	Values           map[string]any            `json:"values" cfgapply:"secret"`
	DeploymentStatus string                    `json:"deploymentStatus" cfgapply:"read-only"`
	SyncStatus       *string                   `json:"syncStatus" cfgapply:"read-only"`
	Deployed         *apiv2.DeployedEntrypoint `json:"deployed" cfgapply:"read-only"`
	RuntimeStatus    apiv2.RuntimeStatus       `json:"runtimeStatus" cfgapply:"read-only"`
	CreatedBy        string                    `json:"createdBy" cfgapply:"read-only"`
	UpdatedBy        string                    `json:"updatedBy" cfgapply:"read-only"`
	CreatedTime      apiv2.Instant             `json:"createdTime" cfgapply:"read-only"`
	UpdatedTime      apiv2.Instant             `json:"updatedTime" cfgapply:"read-only"`
}

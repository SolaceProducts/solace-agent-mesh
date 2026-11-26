// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package wire

import (
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/apiv2"
)

type CreateConnectorRequest struct {
	Name             string         `json:"name" validate:"required,min=3,max=255,safename"`
	Description      string         `json:"description" validate:"required,min=10,max=1000,safetext"`
	ConnectorType    string         `json:"connectorType" validate:"required,max=50" cfgapply:"key=type,discriminator"`
	ConnectorSubtype string         `json:"connectorSubtype" validate:"required,max=50" cfgapply:"key=subtype,discriminator"`
	Values           map[string]any `json:"values" cfgapply:"secret"`
}

type UpdateConnectorRequest struct {
	Name             Patch[string]         `json:"name,omitzero"`
	Description      Patch[string]         `json:"description,omitzero" clearable:"true"`
	Values           Patch[map[string]any] `json:"values,omitzero" cfgapply:"secret,clear-empty"`
	ConnectorType    Patch[string]         `json:"connectorType,omitzero" cfgapply:"key=type,discriminator,read-only"`
	ConnectorSubtype Patch[string]         `json:"connectorSubtype,omitzero" cfgapply:"key=subtype,discriminator,read-only"`
}

type ConnectorResponse struct {
	ID                 string                             `json:"id" cfgapply:"read-only"`
	Type               string                             `json:"type" cfgapply:"read-only"`
	ConnectorType      string                             `json:"connectorType" cfgapply:"discriminator"`
	ConnectorSubtype   string                             `json:"connectorSubtype" cfgapply:"discriminator"`
	Name               string                             `json:"name"`
	Description        *string                            `json:"description"`
	Values             map[string]any                     `json:"values" cfgapply:"secret"`
	AgentCount         int                                `json:"agentCount" cfgapply:"read-only"`
	HasResource        bool                               `json:"hasResource" cfgapply:"read-only"`
	ContentHash        *string                            `json:"contentHash" cfgapply:"read-only"`
	Warnings           []string                           `json:"warnings" cfgapply:"read-only"`
	AgentRegistrations []apiv2.ConnectorAgentRegistration `json:"agentRegistrations" cfgapply:"read-only"`
	CreatedBy          *string                            `json:"createdBy" cfgapply:"read-only"`
	UpdatedBy          *string                            `json:"updatedBy" cfgapply:"read-only"`
	CreatedTime        apiv2.Instant                      `json:"createdTime" cfgapply:"read-only"`
	UpdatedTime        apiv2.Instant                      `json:"updatedTime" cfgapply:"read-only"`
}

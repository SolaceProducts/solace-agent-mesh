// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package wire

import (
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/apiv2"
)

type CreateToolsetRequest struct {
	Name        string `json:"name" validate:"required"`
	Description string `json:"description"`
}

type UpdateToolsetRequest struct {
	Name        Patch[string] `json:"name,omitzero" cfgapply:"read-only"`
	Description Patch[string] `json:"description,omitzero" clearable:"true"`
}

type ToolsetResponse struct {
	ID              string               `json:"id" cfgapply:"read-only"`
	Type            string               `json:"type" cfgapply:"read-only"`
	Origin          string               `json:"origin" cfgapply:"read-only"`
	Name            string               `json:"name"`
	Description     *string              `json:"description"`
	Runtime         *string              `json:"runtime" cfgapply:"read-only"`
	DiscoveryStatus *string              `json:"discoveryStatus" cfgapply:"read-only"`
	DiscoveryErrors []string             `json:"discoveryErrors" cfgapply:"read-only"`
	Bundle          *apiv2.ToolsetBundle `json:"bundle" cfgapply:"read-only"`
	Tools           []apiv2.ToolsetTool  `json:"tools" cfgapply:"read-only"`
	Agents          []apiv2.ToolsetAgent `json:"agents" cfgapply:"read-only"`
	ConfigSummary   *apiv2.ConfigSummary `json:"configSummary" cfgapply:"read-only"`
	Capabilities    map[string]any       `json:"capabilities" cfgapply:"read-only"`
	Default         bool                 `json:"default" cfgapply:"read-only"`
	Aliases         []string             `json:"aliases" cfgapply:"read-only"`
	CreatedBy       *string              `json:"createdBy" cfgapply:"read-only"`
	UpdatedBy       *string              `json:"updatedBy" cfgapply:"read-only"`
	CreatedTime     apiv2.Instant        `json:"createdTime" cfgapply:"read-only"`
	UpdatedTime     apiv2.Instant        `json:"updatedTime" cfgapply:"read-only"`
}

type ToolsetConfiguration struct {
	Values      map[string]any `json:"values" cfgapply:"secret"`
	UpdatedBy   *string        `json:"updatedBy" cfgapply:"read-only"`
	UpdatedTime apiv2.Instant  `json:"updatedTime" cfgapply:"read-only"`
}

type UpdateToolsetConfigurationRequest struct {
	Values Patch[map[string]any] `json:"values,omitzero" cfgapply:"secret,merge=2"`
}

// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package wire

import (
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/apiv2"
)

type CreateSkillRequest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type UpdateSkillRequest struct {
	Description Patch[string] `json:"description,omitzero"`
	Name        Patch[string] `json:"name,omitzero" cfgapply:"read-only"`
}

type SkillResponse struct {
	ID              string                   `json:"id" cfgapply:"read-only"`
	Type            string                   `json:"type" cfgapply:"read-only"`
	Origin          string                   `json:"origin" cfgapply:"read-only"`
	Name            string                   `json:"name"`
	Label           *string                  `json:"label" cfgapply:"read-only"`
	Description     *string                  `json:"description"`
	Tags            []string                 `json:"tags"`
	DiscoveryStatus string                   `json:"discoveryStatus" cfgapply:"read-only"`
	HasReferences   bool                     `json:"hasReferences" cfgapply:"read-only"`
	HasAssets       bool                     `json:"hasAssets" cfgapply:"read-only"`
	HasTools        bool                     `json:"hasTools" cfgapply:"read-only"`
	ResourceCount   int                      `json:"resourceCount" cfgapply:"read-only"`
	ReferenceCount  int                      `json:"referenceCount" cfgapply:"read-only"`
	AssetCount      int                      `json:"assetCount" cfgapply:"read-only"`
	BundledTools    []apiv2.SkillBundledTool `json:"bundledTools" cfgapply:"read-only"`
	Toolsets        []string                 `json:"toolsets" cfgapply:"read-only"`
	Bundle          *apiv2.SkillBundle       `json:"bundle" cfgapply:"read-only"`
	Agents          []apiv2.SkillAgent       `json:"agents" cfgapply:"read-only"`
	ConfigSummary   *apiv2.ConfigSummary     `json:"configSummary" cfgapply:"read-only"`
	CreatedBy       *string                  `json:"createdBy" cfgapply:"read-only"`
	UpdatedBy       *string                  `json:"updatedBy" cfgapply:"read-only"`
	CreatedTime     apiv2.Instant            `json:"createdTime" cfgapply:"read-only"`
	UpdatedTime     apiv2.Instant            `json:"updatedTime" cfgapply:"read-only"`
}

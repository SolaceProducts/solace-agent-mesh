// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

type CreateSkillRequest struct {
	Name        string `json:"-"`
	Description string `json:"description"`
}

type UpdateSkillRequest struct {
	Description string `json:"description"`
}

type SkillDetailResponse struct {
	ID              string          `json:"id" cfgapply:"read-only"`
	Name            string          `json:"name"`
	Label           string          `json:"label,omitempty" cfgapply:"read-only"`
	Description     string          `json:"description"`
	Tags            []string        `json:"tags,omitempty"`
	Origin          string          `json:"origin" cfgapply:"read-only"`
	DiscoveryStatus string          `json:"discoveryStatus" cfgapply:"read-only"`
	HasReferences   bool            `json:"hasReferences" cfgapply:"read-only"`
	HasAssets       bool            `json:"hasAssets" cfgapply:"read-only"`
	HasTools        bool            `json:"hasTools" cfgapply:"read-only"`
	ResourceCount   int             `json:"resourceCount" cfgapply:"read-only"`
	ReferenceCount  int             `json:"referenceCount" cfgapply:"read-only"`
	AssetCount      int             `json:"assetCount" cfgapply:"read-only"`
	BundledTools    []any           `json:"bundledTools,omitempty" cfgapply:"read-only"`
	Toolsets        []string        `json:"toolsets,omitempty" cfgapply:"read-only"`
	ContentHash     string          `json:"contentHash" cfgapply:"read-only"`
	Agents          []SkillAgentRef `json:"agents" cfgapply:"read-only"`
	ConfigSummary   *ConfigSummary  `json:"configSummary,omitempty" cfgapply:"read-only"`
	CreatedBy       string          `json:"createdBy" cfgapply:"read-only"`
	UpdatedBy       string          `json:"updatedBy" cfgapply:"read-only"`
	CreatedTime     Timestamp       `json:"createdAt" cfgapply:"read-only"`
	UpdatedTime     Timestamp       `json:"updatedAt" cfgapply:"read-only"`
}

type SkillAgentRef struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

type SkillFileRef struct {
	Path      string `json:"path"`
	SizeBytes int64  `json:"sizeBytes"`
}

type SkillFilesResponse struct {
	References []SkillFileRef `json:"references"`
	Assets     []SkillFileRef `json:"assets"`
}

type SkillConfigResponse struct {
	Values    map[string]map[string]any `json:"values"`
	UpdatedBy string                    `json:"updatedBy"`
	UpdatedAt Timestamp                 `json:"updatedAt"`
}

type SkillConfigPatchRequest struct {
	Values map[string]map[string]any `json:"values"`
}

type SkillConfigField struct {
	ToolName       string `json:"toolName"`
	Key            string `json:"key"`
	Type           string `json:"type"`
	Secret         bool   `json:"secret"`
	Required       bool   `json:"required"`
	Description    string `json:"description,omitempty"`
	Default        any    `json:"default"`
	CoveredBySkill bool   `json:"coveredBySkill"`
	CoveredByAgent bool   `json:"coveredByAgent"`
}

// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package remotetool

const (
	MethodSkillInit    = "sam_skill/init"
	MethodSkillRemoved = "sam_skill/removed"
)

type SkillRemovedParams struct {
	SkillName string `json:"skill_name"`
}

type SkillInitParams struct {
	SkillName        string             `json:"skill_name"`
	Description      string             `json:"description"`
	Tags             []string           `json:"tags,omitempty"`
	Examples         []string           `json:"examples,omitempty"`
	RequiredBuiltins []string           `json:"required_builtins,omitempty"`
	Tools            []map[string]any   `json:"tools,omitempty"`
	BundledTools     []SkillBundledTool `json:"bundled_tools,omitempty"`
	Toolsets         []string           `json:"toolsets,omitempty"`
	HasReferences    bool               `json:"has_references"`
	HasAssets        bool               `json:"has_assets"`
	HasTemplates     bool               `json:"has_templates,omitempty"`
	Templates        []SkillTemplate    `json:"templates,omitempty"`
	ResourceCount    int                `json:"resource_count"`
	ReferenceCount   int                `json:"reference_count"`
	AssetCount       int                `json:"asset_count"`
	DiscoveryFailed  bool               `json:"discovery_failed,omitempty"`
}

type SkillTemplate struct {
	Name           string `json:"name"`
	Description    string `json:"description,omitempty"`
	OutputFilename string `json:"output_filename,omitempty"`
	Asset          string `json:"asset,omitempty"`
}

type SkillBundledTool struct {
	ToolName       string                       `json:"tool_name"`
	Description    string                       `json:"description"`
	Parameters     map[string]any               `json:"parameters,omitempty"`
	ArtifactParams map[string]ArtifactParamInfo `json:"artifact_params,omitempty"`
	Instructions   string                       `json:"instructions,omitempty"`
	ConfigSchema   []map[string]any             `json:"config_schema,omitempty"`
}

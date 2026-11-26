// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package skill

import (
	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a/remotetool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent"
)

type SkillInfo struct {
	Name string

	Description string

	Tags []string

	Examples []string

	RequiredBuiltins []string

	Tools []map[string]any

	BundledTools []remotetool.SkillBundledTool

	HasReferences bool

	HasAssets bool

	Templates []remotetool.SkillTemplate

	ResourceCount int
}

func SkillInfoFromInit(params *remotetool.SkillInitParams) *SkillInfo {
	return &SkillInfo{
		Name:             params.SkillName,
		Description:      params.Description,
		Tags:             params.Tags,
		Examples:         params.Examples,
		RequiredBuiltins: params.RequiredBuiltins,
		Tools:            params.Tools,
		BundledTools:     params.BundledTools,
		HasReferences:    params.HasReferences,
		HasAssets:        params.HasAssets,
		Templates:        params.Templates,
		ResourceCount:    params.ResourceCount,
	}
}

type LoadedSkill struct {
	Name string

	InstructionContent string

	BundledTools []remotetool.SkillBundledTool

	RequiredBuiltins []string

	Tools []map[string]any

	HasReferences bool

	HasAssets bool

	Templates []remotetool.SkillTemplate

	ResourceCount int

	ResourceFiles []string

	ContentHash string
}

func (ls *LoadedSkill) hasInstructions() bool {
	return ls != nil && ls.InstructionContent != ""
}

func (ls *LoadedSkill) ToLoadedSkillInfo() agent.LoadedSkillInfo {
	refCount := 0
	assetCount := 0
	if ls.HasReferences {
		refCount = ls.ResourceCount
	}
	if ls.HasAssets && !ls.HasReferences {
		assetCount = ls.ResourceCount
	}
	return agent.LoadedSkillInfo{
		Name:               ls.Name,
		InstructionContent: ls.InstructionContent,
		ReferenceFileCount: refCount,
		AssetFileCount:     assetCount,
	}
}

type SkillRef = agent.SkillRef

// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package wire

import (
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/apiv2"
)

type CreateAgentRequest struct {
	Name                     string                `json:"name" validate:"required,min=3,max=255,safetext"`
	Description              string                `json:"description" validate:"required,min=10,max=1000,safetext"`
	SystemPrompt             string                `json:"systemPrompt" validate:"omitempty,safetext" doc:"required"`
	AgentType                string                `json:"agentType" validate:"omitempty,oneof=standard" cfgapply:"key=type,omit-hash"`
	Skills                   []apiv2.AgentSkill    `json:"skills" validate:"max=20"`
	SkillRefs                []string              `json:"skillRefs"`
	SkillIDs                 []string              `json:"skillIds" cfgapply:"xref=skill,omit-hash"`
	Toolsets                 []string              `json:"toolsets"`
	ToolsetExcludedTools     map[string][]string   `json:"toolsetExcludedTools"`
	ToolsetConfigs           []apiv2.ToolsetConfig `json:"toolsetConfigs"`
	SkillConfigs             []apiv2.SkillConfig   `json:"skillConfigs"`
	ConnectorIDs             []string              `json:"connectorIds" validate:"max=5" cfgapply:"key=connectors,xref=connector"`
	InputModes               []string              `json:"inputModes"`
	OutputModes              []string              `json:"outputModes"`
	ModelProvider            []string              `json:"modelProvider"`
	AdditionalConfigurations map[string]any        `json:"additionalConfigurations"`
}

type UpdateAgentRequest struct {
	Name                     string                `json:"name" validate:"required,min=3,max=255,safetext"`
	Description              string                `json:"description" validate:"required,min=10,max=1000,safetext"`
	SystemPrompt             string                `json:"systemPrompt" validate:"omitempty,safetext" doc:"required"`
	Skills                   []apiv2.AgentSkill    `json:"skills" validate:"max=20"`
	SkillRefs                []string              `json:"skillRefs"`
	SkillIDs                 []string              `json:"skillIds" cfgapply:"xref=skill,omit-hash"`
	Toolsets                 []string              `json:"toolsets"`
	ToolsetExcludedTools     map[string][]string   `json:"toolsetExcludedTools"`
	ToolsetConfigs           []apiv2.ToolsetConfig `json:"toolsetConfigs"`
	SkillConfigs             []apiv2.SkillConfig   `json:"skillConfigs"`
	ConnectorIDs             []string              `json:"connectorIds" validate:"max=5" cfgapply:"key=connectors,xref=connector"`
	InputModes               []string              `json:"inputModes"`
	OutputModes              []string              `json:"outputModes"`
	ModelProvider            []string              `json:"modelProvider"`
	AdditionalConfigurations map[string]any        `json:"additionalConfigurations"`
}

type AgentResponse struct {
	ID                       string                              `json:"id" cfgapply:"read-only"`
	Type                     string                              `json:"type" cfgapply:"read-only"`
	AgentType                string                              `json:"agentType" cfgapply:"read-only"`
	Name                     string                              `json:"name"`
	InstanceName             string                              `json:"instanceName" cfgapply:"read-only"`
	Description              string                              `json:"description"`
	SystemPrompt             string                              `json:"systemPrompt"`
	Toolsets                 []string                            `json:"toolsets"`
	ToolsetExcludedTools     map[string][]string                 `json:"toolsetExcludedTools"`
	ToolsetConfigs           []apiv2.ToolsetConfig               `json:"toolsetConfigs"`
	SkillConfigs             []apiv2.SkillConfig                 `json:"skillConfigs"`
	SkillConfigFields        map[string][]apiv2.SkillConfigField `json:"skillConfigFields" cfgapply:"read-only"`
	Skills                   []apiv2.AgentSkill                  `json:"skills"`
	SkillRefs                []string                            `json:"skillRefs"`
	SkillIDs                 []string                            `json:"skillIds" cfgapply:"read-only"`
	InputModes               []string                            `json:"inputModes"`
	OutputModes              []string                            `json:"outputModes"`
	ConnectorIDs             []string                            `json:"connectorIds" cfgapply:"xref=connector"`
	ModelProvider            []string                            `json:"modelProvider"`
	AdditionalConfigurations map[string]any                      `json:"additionalConfigurations"`
	DeploymentStatus         string                              `json:"deploymentStatus" cfgapply:"read-only"`
	SyncStatus               *string                             `json:"syncStatus" cfgapply:"read-only"`
	Deployed                 *apiv2.DeployedAgent                `json:"deployed" cfgapply:"read-only"`
	RuntimeStatus            apiv2.RuntimeStatus                 `json:"runtimeStatus" cfgapply:"read-only"`
	ToolRegistrations        []apiv2.ToolRegistration            `json:"toolRegistrations" cfgapply:"read-only"`
	Warnings                 []string                            `json:"warnings" cfgapply:"read-only"`
	IsEphemeral              bool                                `json:"isEphemeral" cfgapply:"read-only"`
	SourceSessionID          *string                             `json:"sourceSessionId" cfgapply:"read-only"`
	SourceComponentName      *string                             `json:"sourceComponentName" cfgapply:"read-only"`
	SourceComponentKind      *string                             `json:"sourceComponentKind" cfgapply:"read-only"`
	ExpiresTime              apiv2.Instant                       `json:"expiresTime" cfgapply:"read-only"`
	CreatedBy                string                              `json:"createdBy" cfgapply:"read-only"`
	UpdatedBy                string                              `json:"updatedBy" cfgapply:"read-only"`
	CreatedTime              apiv2.Instant                       `json:"createdTime" cfgapply:"read-only"`
	UpdatedTime              apiv2.Instant                       `json:"updatedTime" cfgapply:"read-only"`
}

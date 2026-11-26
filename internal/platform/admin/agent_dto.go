// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"encoding/json"
)

type ToolsetConfigBinding struct {
	ToolsetName     string            `json:"toolsetName"`
	ConfigValues    map[string]any    `json:"configValues"`
	VolumeBindings  map[string]string `json:"volumeBindings,omitempty"`
	NetworkBindings map[string]string `json:"networkBindings,omitempty"`
}

type SkillConfigBinding struct {
	SkillID      string                    `json:"skillId,omitempty"`
	SkillName    string                    `json:"skillName,omitempty"`
	ConfigValues map[string]map[string]any `json:"configValues"`
}

type CreateAgentRequest struct {
	Name                     string                 `json:"name" validate:"required,min=3,max=255,safetext"`
	Description              string                 `json:"description" validate:"required,min=10,max=1000,safetext"`
	SystemPrompt             string                 `json:"systemPrompt" validate:"omitempty,safetext" doc:"required"`
	Type                     string                 `json:"type,omitempty" validate:"omitempty,oneof=standard" cfgapply:"omit-hash"`
	Skills                   []SkillRequest         `json:"skills" validate:"max=20"`
	SkillRefs                []string               `json:"skillRefs"`
	SkillIDs                 []string               `json:"skillIds" cfgapply:"xref=skill,omit-hash"`
	Toolsets                 []string               `json:"toolsets"`
	ToolsetExcludedTools     map[string][]string    `json:"toolsetExcludedTools,omitempty"`
	ToolsetConfigs           []ToolsetConfigBinding `json:"toolsetConfigs,omitempty"`
	SkillConfigs             []SkillConfigBinding   `json:"skillConfigs,omitempty"`
	Connectors               []string               `json:"connectors" validate:"max=5" cfgapply:"xref=connector"`
	InputModes               []string               `json:"inputModes"`
	OutputModes              []string               `json:"outputModes"`
	ModelProvider            []string               `json:"modelProvider,omitempty"`
	AdditionalConfigurations json.RawMessage        `json:"additionalConfigurations,omitempty" cfgapply:"json-object"`
	Deploy                   bool                   `json:"deploy" cfgapply:"omit-hash"`
}

type UpdateAgentRequest struct {
	Name                     string                 `json:"name" validate:"required,min=3,max=255,safetext"`
	Description              string                 `json:"description" validate:"required,min=10,max=1000,safetext"`
	SystemPrompt             string                 `json:"systemPrompt" validate:"omitempty,safetext" doc:"required"`
	Skills                   []SkillRequest         `json:"skills" validate:"max=20"`
	SkillRefs                []string               `json:"skillRefs"`
	SkillIDs                 *[]string              `json:"skillIds" cfgapply:"xref=skill,omit-hash"`
	Toolsets                 []string               `json:"toolsets"`
	ToolsetExcludedTools     map[string][]string    `json:"toolsetExcludedTools,omitempty"`
	ToolsetConfigs           []ToolsetConfigBinding `json:"toolsetConfigs,omitempty"`
	SkillConfigs             []SkillConfigBinding   `json:"skillConfigs,omitempty"`
	Connectors               []string               `json:"connectors" validate:"max=5" cfgapply:"xref=connector"`
	InputModes               []string               `json:"inputModes"`
	OutputModes              []string               `json:"outputModes"`
	ModelProvider            []string               `json:"modelProvider,omitempty"`
	AdditionalConfigurations json.RawMessage        `json:"additionalConfigurations,omitempty" cfgapply:"json-object"`
	Deploy                   bool                   `json:"deploy" cfgapply:"omit-hash"`
}

type DeployedAgentInfo struct {
	Name                     string              `json:"name"`
	Description              string              `json:"description"`
	SystemPrompt             string              `json:"systemPrompt"`
	Toolsets                 []string            `json:"toolsets"`
	InputModes               []string            `json:"inputModes"`
	OutputModes              []string            `json:"outputModes"`
	Connectors               []string            `json:"connectors"`
	ConnectorSnapshots       []ConnectorSnapshot `json:"connectorSnapshots,omitempty"`
	AdditionalConfigurations json.RawMessage     `json:"additionalConfigurations,omitempty"`
	DeployedAt               Timestamp           `json:"deployedAt"`
	DeployedBy               string              `json:"deployedBy"`
	SkillIDs                 []string            `json:"skillIds,omitempty"`
	SkillRefs                []string            `json:"skillRefs,omitempty"`
	Skills                   []SkillResponse     `json:"skills,omitempty"`
}

type ConnectorSnapshot struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Type    string `json:"type"`
	Subtype string `json:"subtype,omitempty"`
}

type DeployedGatewayInfo struct {
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Type        string    `json:"type"`
	Values      any       `json:"values,omitempty"`
	DeployedAt  Timestamp `json:"deployedAt"`
	DeployedBy  string    `json:"deployedBy"`
}

type SkillRequest struct {
	ID          string   `json:"id,omitempty"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Tags        []string `json:"tags,omitempty"`
	Examples    []string `json:"examples,omitempty"`
	InputModes  []string `json:"inputModes,omitempty"`
	OutputModes []string `json:"outputModes,omitempty"`
	Required    []string `json:"required"`
	Optional    []string `json:"optional"`
}

type AgentResponse struct {
	ID                       string                        `json:"id" cfgapply:"read-only"`
	Name                     string                        `json:"name"`
	InstanceName             string                        `json:"instanceName,omitempty" cfgapply:"read-only"`
	Type                     string                        `json:"type" cfgapply:"read-only"`
	Description              string                        `json:"description"`
	SystemPrompt             string                        `json:"systemPrompt"`
	Toolsets                 []string                      `json:"toolsets"`
	ToolsetExcludedTools     map[string][]string           `json:"toolsetExcludedTools,omitempty"`
	ToolsetConfigs           []ToolsetConfigBinding        `json:"toolsetConfigs,omitempty"`
	SkillConfigs             []SkillConfigBinding          `json:"skillConfigs,omitempty"`
	SkillConfigFields        map[string][]SkillConfigField `json:"skillConfigFields,omitempty" cfgapply:"read-only"`
	Skills                   []SkillResponse               `json:"skills"`
	SkillRefs                []string                      `json:"skillRefs"`
	SkillIDs                 []string                      `json:"skillIds" cfgapply:"read-only"`
	InputModes               []string                      `json:"inputModes"`
	OutputModes              []string                      `json:"outputModes"`
	Connectors               []string                      `json:"connectors" cfgapply:"xref=connector"`
	ModelProvider            []string                      `json:"modelProvider,omitempty"`
	AdditionalConfigurations json.RawMessage               `json:"additionalConfigurations,omitempty" cfgapply:"json-object"`
	DeploymentStatus         string                        `json:"deploymentStatus" cfgapply:"read-only"`
	SyncStatus               *string                       `json:"syncStatus" cfgapply:"read-only"`
	Deployed                 any                           `json:"deployed" cfgapply:"read-only"`
	RuntimeStatus            RuntimeStatus                 `json:"runtimeStatus" cfgapply:"read-only"`
	CreatedBy                string                        `json:"createdBy" cfgapply:"read-only"`
	UpdatedBy                string                        `json:"updatedBy" cfgapply:"read-only"`
	CreatedTime              Timestamp                     `json:"createdAt" cfgapply:"read-only"`
	UpdatedTime              Timestamp                     `json:"updatedAt" cfgapply:"read-only"`

	IsEphemeral         bool       `json:"isEphemeral,omitempty" cfgapply:"read-only"`
	SourceSessionID     string     `json:"sourceSessionId,omitempty" cfgapply:"read-only"`
	SourceComponentName string     `json:"sourceComponentName,omitempty" cfgapply:"read-only"`
	SourceComponentKind string     `json:"sourceComponentKind,omitempty" cfgapply:"read-only"`
	ExpiresAt           *Timestamp `json:"expiresAt,omitempty" cfgapply:"read-only"`

	Warnings []string `json:"warnings,omitempty" cfgapply:"read-only"`

	ToolRegistrations []ToolRegistrationResponse `json:"toolRegistrations,omitempty" cfgapply:"read-only"`
}

type ToolRegistrationResponse struct {
	ToolType    string   `json:"toolType"`
	ConnectorID string   `json:"connectorId,omitempty"`
	Name        string   `json:"name,omitempty"`
	Status      string   `json:"status"`
	Error       string   `json:"error,omitempty"`
	ToolCount   int      `json:"toolCount"`
	ToolNames   []string `json:"toolNames,omitempty"`
}

type SkillResponse struct {
	ID          string   `json:"id,omitempty"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Tags        []string `json:"tags,omitempty"`
	Examples    []string `json:"examples,omitempty"`
	InputModes  []string `json:"inputModes,omitempty"`
	OutputModes []string `json:"outputModes,omitempty"`
	Required    []string `json:"required"`
	Optional    []string `json:"optional"`
}

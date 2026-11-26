// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

type GenerateAgentRequest struct {
	Description string `json:"description" validate:"required,min=10,max=1000"`
}

type SkillSuggestion struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type GenerateAgentResponse struct {
	Name          string            `json:"name"`
	Description   string            `json:"description"`
	SystemPrompt  string            `json:"systemPrompt,omitempty"`
	ModelProvider []string          `json:"modelProvider"`
	Toolsets      []string          `json:"toolsets,omitempty"`
	Skills        []SkillSuggestion `json:"skills,omitempty"`
	Connectors    []string          `json:"connectors,omitempty"`
	InputModes    []string          `json:"inputModes,omitempty"`
	OutputModes   []string          `json:"outputModes,omitempty"`
}

// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

type SecretsDiffRequest struct {
	Values map[string]any `json:"values"`
}

type ModelSecretsDiffRequest struct {
	AuthConfig map[string]any `json:"authConfig"`
}

type AgentSecretsDiffRequest struct {
	Values *AgentOverlayCandidate `json:"values"`
}

type AgentOverlayCandidate struct {
	ToolsetConfigs []ToolsetConfigBinding `json:"toolsetConfigs,omitempty"`
	SkillConfigs   []SkillConfigBinding   `json:"skillConfigs,omitempty"`
}

type SecretFieldDiffResult struct {
	Matches bool `json:"matches"`
}

type SecretsDiffResponse struct {
	Secrets map[string]SecretFieldDiffResult `json:"secrets"`
}

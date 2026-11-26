// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/modelfamily"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samapp"
	"github.com/SolaceProducts/solace-agent-mesh/internal/taxonomy"
	"github.com/SolaceProducts/solace-agent-mesh/internal/usagefacts"
)

type taskUsageFacts struct {
	usagefacts.Facts
}

func (f *taskUsageFacts) recordModel(modelID, provider, apiBase string) usagefacts.Model {
	model := ModelFacts(modelID, provider, apiBase)
	f.RecordModel(model)
	return model
}

func ModelFacts(modelID, provider, apiBase string) usagefacts.Model {
	model := usagefacts.Model{Provider: llm.ProviderLabel(provider)}
	if modelID != "" {
		model.Family = modelfamily.Of(modelID)
	}
	if model != (usagefacts.Model{}) {
		model.ServedFrom = taxonomy.ModelServedFrom(model.Provider, apiBase)
	}
	return model
}

func (f *taskUsageFacts) recordToolModel(modelID string) {
	if modelID == "" {
		return
	}
	f.AddToolModelFamily(modelfamily.Of(modelID))
}

func callModel(state *TaskExecutionState, reportModel, reportAPIBase string) (modelID, provider, apiBase string) {
	if state.ModelOverride != nil {
		return state.ModelOverride.Model, state.ModelOverride.Provider, state.ModelOverride.BaseURL
	}
	if reportModel == "" {
		return "", "", ""
	}
	provider, _ = samapp.ParseModelProvider(reportModel)
	return reportModel, provider, reportAPIBase
}

func (a *Agent) recordToolUsage(state *TaskExecutionState, toolCalls []llm.ToolCall) {
	for _, tc := range toolCalls {
		if _, isPeer := a.tools.GetTool(tc.Name).(*peerAgentTool); isPeer {
			state.usageFacts.MarkPeerDelegated()
			continue
		}
		provenance, classified := a.ToolProvenanceFor(tc.Name)
		if !classified {
			continue
		}
		state.usageFacts.AddToolCategory(provenance.Category)
		state.usageFacts.AddConnectorCategory(provenance.ConnectorCategory)
		state.usageFacts.AddBuiltinCategory(provenance.BuiltinCategory)
	}
}

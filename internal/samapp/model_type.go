// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package samapp

import (
	"cmp"

	"github.com/SolaceProducts/solace-agent-mesh/internal/decision"
)

const ModelTypeKey = "model_type"

const DecisionModelType = "decision"

func IsDecisionModel(raw map[string]any) bool {
	modelType, _ := raw[ModelTypeKey].(string)
	return modelType == DecisionModelType
}

const DecisionProviderKey = "decision_provider"

func DecisionProvider(raw map[string]any) string {
	provider, _ := raw[DecisionProviderKey].(string)
	return cmp.Or(provider, decision.ProviderTypeSafe)
}

// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package spend

import (
	"github.com/SolaceProducts/solace-agent-mesh/internal/budget"
	"github.com/SolaceProducts/solace-agent-mesh/internal/decision"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
)

func NewMeteredClient(inner llm.Client, boundModel string, publisher *Publisher, gate *budget.Gate) llm.Client {
	return NewGatedClient(NewRecordingClient(inner, boundModel, publisher), gate)
}

func NewMeteredEvaluator(inner decision.Evaluator, publisher *Publisher, attribution *llm.SpendAttribution, gate *budget.Gate) decision.Evaluator {
	return NewGatedEvaluator(NewRecordingEvaluator(inner, publisher, attribution), attribution, gate)
}

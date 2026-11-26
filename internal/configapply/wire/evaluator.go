// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package wire

import (
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/apiv2"
)

type CreateEvaluatorRequest struct {
	Name                  string                   `json:"name" validate:"required,max=255"`
	Description           string                   `json:"description" validate:"required,max=1000" doc:"min=1"`
	EvaluatorType         string                   `json:"evaluatorType" cfgapply:"key=type,discriminator" doc:"oneof=llm_judge decision"`
	Model                 *string                  `json:"model"`
	PromptTemplate        *string                  `json:"promptTemplate"`
	ChoiceScores          map[string]float64       `json:"choiceScores" doc:"min=2,max=26"`
	Question              *apiv2.EvaluatorQuestion `json:"question"`
	Scoring               string                   `json:"scoring" doc:"oneof=chosen expected"`
	Reads                 []string                 `json:"reads" doc:"oneof=prompt response expected_response transcript artifacts"`
	MinConfidence         *float64                 `json:"minConfidence"`
	EscalateToEvaluatorID *string                  `json:"escalateToEvaluatorId" cfgapply:"xref=evaluator"`
}

type UpdateEvaluatorRequest struct {
	Name                  Patch[string]                  `json:"name,omitzero"`
	Description           Patch[string]                  `json:"description,omitzero"`
	EvaluatorType         Patch[string]                  `json:"evaluatorType,omitzero" cfgapply:"key=type,read-only"`
	Model                 Patch[string]                  `json:"model,omitzero" clearable:"true"`
	PromptTemplate        Patch[string]                  `json:"promptTemplate,omitzero" clearable:"true"`
	ChoiceScores          Patch[map[string]float64]      `json:"choiceScores,omitzero" clearable:"true"`
	Question              Patch[apiv2.EvaluatorQuestion] `json:"question,omitzero"`
	Scoring               Patch[string]                  `json:"scoring,omitzero" clearable:"true"`
	Reads                 Patch[[]string]                `json:"reads,omitzero" clearable:"true"`
	MinConfidence         Patch[float64]                 `json:"minConfidence,omitzero" clearable:"true"`
	EscalateToEvaluatorID Patch[string]                  `json:"escalateToEvaluatorId,omitzero" clearable:"true"`
}

type EvaluatorResponse struct {
	ID                    string                   `json:"id" cfgapply:"read-only"`
	Type                  string                   `json:"type" cfgapply:"read-only"`
	Name                  string                   `json:"name"`
	Description           *string                  `json:"description"`
	EvaluatorType         string                   `json:"evaluatorType" cfgapply:"discriminator"`
	HeuristicType         *string                  `json:"heuristicType" cfgapply:"discriminator"`
	IsDefault             bool                     `json:"isDefault" cfgapply:"read-only"`
	Model                 *string                  `json:"model"`
	PromptTemplate        *string                  `json:"promptTemplate"`
	ChoiceScores          map[string]float64       `json:"choiceScores"`
	Question              *apiv2.EvaluatorQuestion `json:"question"`
	Scoring               *string                  `json:"scoring"`
	Reads                 []string                 `json:"reads"`
	MinConfidence         *float64                 `json:"minConfidence"`
	EscalateToEvaluatorID *string                  `json:"escalateToEvaluatorId" cfgapply:"xref=evaluator"`
	ExperimentCount       int                      `json:"experimentCount" cfgapply:"read-only"`
	CreatedBy             string                   `json:"createdBy" cfgapply:"read-only"`
	UpdatedBy             string                   `json:"updatedBy" cfgapply:"read-only"`
	CreatedTime           apiv2.Instant            `json:"createdTime" cfgapply:"read-only"`
	UpdatedTime           apiv2.Instant            `json:"updatedTime" cfgapply:"read-only"`
}

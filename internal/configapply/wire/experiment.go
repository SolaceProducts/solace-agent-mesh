// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package wire

import (
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/apiv2"
)

type CreateExperimentRequest struct {
	Name               string                  `json:"name" validate:"required,max=255"`
	Description        string                  `json:"description"`
	DatasetID          string                  `json:"datasetId" cfgapply:"xref=dataset" validate:"required,uuid" doc:"max=255"`
	TargetAgent        string                  `json:"targetAgent" cfgapply:"xref=agent" validate:"omitempty,max=255"`
	TargetWorkflow     string                  `json:"targetWorkflow" cfgapply:"xref=workflow" validate:"omitempty,max=255"`
	Models             []apiv2.ExperimentModel `json:"models" validate:"max=3,dive"`
	RunsPerExample     *int                    `json:"runsPerExample" validate:"omitempty,min=1,max=10"`
	MaxWorkers         *int                    `json:"maxWorkers" validate:"omitempty,min=1,max=20"`
	EvaluatorIDs       []string                `json:"evaluatorIds" cfgapply:"xref=evaluator" validate:"required,min=1,max=5,dive,uuid"`
	PrimaryEvaluatorID *string                 `json:"primaryEvaluatorId" cfgapply:"xref=evaluator" validate:"omitempty,uuid"`
	RunAs              *string                 `json:"runAs"`
}

type UpdateExperimentRequest struct {
	Name               Patch[string]                  `json:"name,omitzero"`
	Description        Patch[string]                  `json:"description,omitzero" clearable:"true"`
	DatasetID          Patch[string]                  `json:"datasetId,omitzero" cfgapply:"xref=dataset"`
	TargetAgent        Patch[string]                  `json:"targetAgent,omitzero" cfgapply:"xref=agent"`
	TargetWorkflow     Patch[string]                  `json:"targetWorkflow,omitzero" cfgapply:"xref=workflow"`
	Models             Patch[[]apiv2.ExperimentModel] `json:"models,omitzero" clearable:"true"`
	RunsPerExample     Patch[int]                     `json:"runsPerExample,omitzero"`
	MaxWorkers         Patch[int]                     `json:"maxWorkers,omitzero"`
	EvaluatorIDs       Patch[[]string]                `json:"evaluatorIds,omitzero" cfgapply:"xref=evaluator"`
	PrimaryEvaluatorID Patch[string]                  `json:"primaryEvaluatorId,omitzero" clearable:"true" cfgapply:"xref=evaluator"`
	RunAs              Patch[string]                  `json:"runAs,omitzero" clearable:"true"`
}

type ExperimentResponse struct {
	ID                 string                  `json:"id" cfgapply:"read-only"`
	Type               string                  `json:"type" cfgapply:"read-only"`
	Name               string                  `json:"name"`
	Description        *string                 `json:"description"`
	DatasetID          *string                 `json:"datasetId" cfgapply:"xref=dataset"`
	DatasetName        *string                 `json:"datasetName" cfgapply:"read-only"`
	DatasetUpdatedTime apiv2.Instant           `json:"datasetUpdatedTime" cfgapply:"read-only"`
	TargetType         string                  `json:"targetType" cfgapply:"read-only"`
	TargetAgent        string                  `json:"targetAgent" cfgapply:"xref=agent"`
	TargetWorkflow     *string                 `json:"targetWorkflow" cfgapply:"xref=workflow"`
	Models             []apiv2.ExperimentModel `json:"models"`
	RunsPerExample     int                     `json:"runsPerExample"`
	MaxWorkers         int                     `json:"maxWorkers"`
	EvaluatorIDs       []string                `json:"evaluatorIds" cfgapply:"xref=evaluator"`
	PrimaryEvaluatorID *string                 `json:"primaryEvaluatorId" cfgapply:"xref=evaluator"`
	IsDefault          bool                    `json:"isDefault" cfgapply:"read-only"`
	RunAs              *string                 `json:"runAs"`
	LatestRunStatus    *string                 `json:"latestRunStatus" cfgapply:"read-only"`
	LatestRunTime      apiv2.Instant           `json:"latestRunTime" cfgapply:"read-only"`
	CreatedBy          string                  `json:"createdBy" cfgapply:"read-only"`
	UpdatedBy          string                  `json:"updatedBy" cfgapply:"read-only"`
	CreatedTime        apiv2.Instant           `json:"createdTime" cfgapply:"read-only"`
	UpdatedTime        apiv2.Instant           `json:"updatedTime" cfgapply:"read-only"`
}

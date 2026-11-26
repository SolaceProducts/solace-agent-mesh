// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package wire

import (
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/apiv2"
)

type CreateDatasetRequest struct {
	Name         string `json:"name" validate:"required,max=255"`
	Description  string `json:"description" validate:"max=1000"`
	ExamplesFile string `json:"-" cfgapply:"local=examples_file"`
}

type UpdateDatasetRequest struct {
	Name        Patch[string] `json:"name,omitzero"`
	Description Patch[string] `json:"description,omitzero" clearable:"true"`
}

type DatasetResponse struct {
	ID                    string        `json:"id" cfgapply:"read-only"`
	Type                  string        `json:"type" cfgapply:"read-only"`
	Name                  string        `json:"name"`
	Description           *string       `json:"description"`
	IsDefault             bool          `json:"isDefault" cfgapply:"read-only"`
	ExampleCount          int           `json:"exampleCount" cfgapply:"read-only"`
	InputDataExampleCount int           `json:"inputDataExampleCount" cfgapply:"read-only"`
	ExperimentCount       int           `json:"experimentCount" cfgapply:"read-only"`
	CreatedBy             string        `json:"createdBy" cfgapply:"read-only"`
	UpdatedBy             string        `json:"updatedBy" cfgapply:"read-only"`
	CreatedTime           apiv2.Instant `json:"createdTime" cfgapply:"read-only"`
	UpdatedTime           apiv2.Instant `json:"updatedTime" cfgapply:"read-only"`
}

type CreateExampleRequest struct {
	Prompt           string               `json:"prompt"`
	ExpectedResponse *string              `json:"expectedResponse"`
	InputData        apiv2.JSONObject     `json:"inputData"`
	ExpectedPath     *apiv2.ExpectedPath  `json:"expectedPath"`
	ExpectedFacts    *apiv2.ExpectedFacts `json:"expectedFacts"`
	SequenceNumber   *int                 `json:"sequenceNumber"`
}

type UpdateExampleRequest struct {
	Prompt           Patch[string]              `json:"prompt,omitzero"`
	ExpectedResponse Patch[string]              `json:"expectedResponse,omitzero" clearable:"true"`
	InputData        Patch[apiv2.JSONObject]    `json:"inputData,omitzero" clearable:"true"`
	ExpectedPath     Patch[apiv2.ExpectedPath]  `json:"expectedPath,omitzero" clearable:"true"`
	ExpectedFacts    Patch[apiv2.ExpectedFacts] `json:"expectedFacts,omitzero" clearable:"true"`
	SequenceNumber   Patch[int]                 `json:"sequenceNumber,omitzero" cfgapply:"read-only"`
	DatasetID        Patch[string]              `json:"datasetId,omitzero" cfgapply:"read-only"`
}

type ExampleResponse struct {
	ID               string                 `json:"id" cfgapply:"read-only"`
	Type             string                 `json:"type" cfgapply:"read-only"`
	DatasetID        string                 `json:"datasetId" cfgapply:"read-only"`
	SequenceNumber   int                    `json:"sequenceNumber"`
	Prompt           string                 `json:"prompt"`
	ExpectedResponse *string                `json:"expectedResponse"`
	InputData        apiv2.JSONObject       `json:"inputData"`
	ExpectedPath     *apiv2.ExpectedPath    `json:"expectedPath"`
	ExpectedFacts    *apiv2.ExpectedFacts   `json:"expectedFacts"`
	InputArtifact    *apiv2.ExampleArtifact `json:"inputArtifact" cfgapply:"read-only"`
	ExpectedArtifact *apiv2.ExampleArtifact `json:"expectedArtifact" cfgapply:"read-only"`
	CreatedTime      apiv2.Instant          `json:"createdTime" cfgapply:"read-only"`
	UpdatedTime      apiv2.Instant          `json:"updatedTime" cfgapply:"read-only"`
}

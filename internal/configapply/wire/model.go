// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package wire

import (
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/apiv2"
)

type CreateModelRequest struct {
	Alias          string         `json:"alias" validate:"required,min=1,max=100,safename"`
	Provider       string         `json:"provider" validate:"required,min=1,max=50,safename"`
	ModelName      string         `json:"modelName" validate:"required,min=1,max=255,safename"`
	APIBase        string         `json:"apiBase" validate:"max=2048"`
	AuthType       string         `json:"authType" cfgapply:"read-only" doc:"hidden"`
	AuthConfig     map[string]any `json:"authConfig" cfgapply:"secret" doc:"required"`
	ModelParams    map[string]any `json:"modelParams"`
	ModelType      string         `json:"modelType" validate:"omitempty,oneof=chat decision" cfgapply:"default=chat"`
	Description    string         `json:"description"`
	MaxInputTokens *int           `json:"maxInputTokens" validate:"omitempty,min=1"`
	Pricing        *apiv2.Pricing `json:"pricing" doc:"hidden"`
}

type UpdateModelRequest struct {
	Alias          Patch[string]         `json:"alias,omitzero"`
	Provider       Patch[string]         `json:"provider,omitzero"`
	ModelName      Patch[string]         `json:"modelName,omitzero"`
	APIBase        Patch[string]         `json:"apiBase,omitzero" cfgapply:"clear-empty"`
	AuthType       Patch[string]         `json:"authType,omitzero" cfgapply:"read-only"`
	AuthConfig     Patch[map[string]any] `json:"authConfig,omitzero" cfgapply:"secret,merge,switch=type"`
	ModelParams    Patch[map[string]any] `json:"modelParams,omitzero" cfgapply:"clear-empty"`
	Description    Patch[string]         `json:"description,omitzero" clearable:"true"`
	MaxInputTokens Patch[int]            `json:"maxInputTokens,omitzero" clearable:"true"`
	Pricing        Patch[map[string]any] `json:"pricing,omitzero" clearable:"true"`
	ModelType      Patch[string]         `json:"modelType,omitzero" cfgapply:"read-only"`
}

type ModelResponse struct {
	ID             string         `json:"id" cfgapply:"read-only"`
	Type           string         `json:"type" cfgapply:"read-only"`
	Alias          string         `json:"alias"`
	Provider       *string        `json:"provider"`
	ModelName      *string        `json:"modelName"`
	APIBase        *string        `json:"apiBase"`
	AuthType       string         `json:"authType" cfgapply:"read-only"`
	AuthConfig     map[string]any `json:"authConfig" cfgapply:"secret"`
	ModelParams    map[string]any `json:"modelParams"`
	ModelType      string         `json:"modelType" cfgapply:"default=chat"`
	Description    *string        `json:"description"`
	MaxInputTokens *int           `json:"maxInputTokens"`
	Pricing        *apiv2.Pricing `json:"pricing"`
	CreatedBy      *string        `json:"createdBy" cfgapply:"read-only"`
	UpdatedBy      *string        `json:"updatedBy" cfgapply:"read-only"`
	CreatedTime    apiv2.Instant  `json:"createdTime" cfgapply:"read-only"`
	UpdatedTime    apiv2.Instant  `json:"updatedTime" cfgapply:"read-only"`
}

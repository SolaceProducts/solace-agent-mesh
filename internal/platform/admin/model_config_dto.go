// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"

type CreateModelConfigRequest struct {
	Alias          string                `json:"alias" validate:"required,min=1,max=100,safename"`
	Provider       string                `json:"provider" validate:"required,min=1,max=50,safename"`
	ModelName      string                `json:"modelName" validate:"required,min=1,max=255,safename"`
	APIBase        string                `json:"apiBase,omitempty" validate:"omitempty,max=2048"`
	AuthType       string                `json:"authType,omitempty"`
	AuthConfig     map[string]any        `json:"authConfig,omitempty" cfgapply:"secret"`
	ModelParams    map[string]any        `json:"modelParams,omitempty"`
	Description    string                `json:"description,omitempty"`
	MaxInputTokens *int                  `json:"maxInputTokens,omitempty" validate:"omitempty,min=1"`
	Pricing        *platpkg.ModelPricing `json:"pricing,omitempty"`
	ModelType      string                `json:"modelType,omitempty" validate:"omitempty,oneof=chat decision"`
}

type UpdateModelConfigRequest struct {
	Alias             *string               `json:"alias,omitempty" validate:"omitempty,min=1,max=100,safename"`
	Provider          *string               `json:"provider,omitempty" validate:"omitempty,min=1,max=50,safename"`
	ModelName         *string               `json:"modelName,omitempty" validate:"omitempty,min=1,max=255,safename"`
	APIBase           *string               `json:"apiBase,omitempty" validate:"omitempty,max=2048"`
	AuthType          *string               `json:"authType,omitempty"`
	AuthConfig        map[string]any        `json:"authConfig,omitempty" cfgapply:"secret"`
	ModelParams       map[string]any        `json:"modelParams,omitempty"`
	Description       *string               `json:"description,omitempty"`
	DescriptionSet    bool                  `json:"-"`
	MaxInputTokens    *int                  `json:"maxInputTokens,omitempty" validate:"omitempty,min=1"`
	MaxInputTokensSet bool                  `json:"-"`
	Pricing           *platpkg.ModelPricing `json:"pricing,omitempty"`
	PricingSet        bool                  `json:"-"`
}

type ModelConfigResponse struct {
	ID             string                        `json:"id" cfgapply:"read-only"`
	Alias          string                        `json:"alias"`
	Provider       *string                       `json:"provider"`
	ModelName      *string                       `json:"modelName"`
	APIBase        *string                       `json:"apiBase"`
	AuthType       string                        `json:"authType" cfgapply:"read-only"`
	AuthConfig     map[string]any                `json:"authConfig" cfgapply:"secret"`
	ModelParams    map[string]any                `json:"modelParams"`
	Description    *string                       `json:"description"`
	MaxInputTokens *int                          `json:"maxInputTokens"`
	ModelType      string                        `json:"modelType"`
	Pricing        *platpkg.ModelPricingResponse `json:"pricing,omitempty"`
	CreatedBy      string                        `json:"createdBy" cfgapply:"read-only"`
	UpdatedBy      string                        `json:"updatedBy" cfgapply:"read-only"`
	CreatedTime    Timestamp                     `json:"createdAt" cfgapply:"read-only"`
	UpdatedTime    Timestamp                     `json:"updatedAt" cfgapply:"read-only"`
}

type ModelConfigStatusResponse struct {
	Configured bool `json:"configured"`
}

type ModelConfigTestRequest struct {
	ModelID     string         `json:"modelId,omitempty"`
	Provider    string         `json:"provider,omitempty"`
	ModelName   string         `json:"modelName,omitempty"`
	APIBase     string         `json:"apiBase,omitempty"`
	AuthType    string         `json:"authType,omitempty"`
	AuthConfig  map[string]any `json:"authConfig,omitempty"`
	ModelParams map[string]any `json:"modelParams,omitempty"`
	ModelType   string         `json:"modelType,omitempty" validate:"omitempty,oneof=chat decision"`
}

type ModelConfigTestResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

type SupportedModelsRequest struct {
	Provider           string         `json:"provider" validate:"required,min=1,max=50,safename"`
	ModelID            string         `json:"modelId,omitempty"`
	APIBase            string         `json:"apiBase,omitempty"`
	AuthType           string         `json:"authType,omitempty"`
	APIKey             string         `json:"apiKey,omitempty"`
	ClientID           string         `json:"clientId,omitempty"`
	ClientSecret       string         `json:"clientSecret,omitempty"`
	TokenURL           string         `json:"tokenUrl,omitempty"`
	AWSAccessKeyID     string         `json:"awsAccessKeyId,omitempty"`
	AWSSecretAccessKey string         `json:"awsSecretAccessKey,omitempty"`
	AWSSessionToken    string         `json:"awsSessionToken,omitempty"`
	AWSRegionName      string         `json:"awsRegionName,omitempty"`
	VertexCredentials  string         `json:"vertexCredentials,omitempty"`
	VertexLocation     string         `json:"vertexLocation,omitempty"`
	ModelParams        map[string]any `json:"modelParams,omitempty"`
	ModelType          string         `json:"modelType,omitempty" validate:"omitempty,oneof=chat decision"`
}

type SupportedModel struct {
	ID       string `json:"id"`
	Label    string `json:"label"`
	Provider string `json:"provider"`
}

type ModelDependentResponse struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	Type             string `json:"type"`
	DeploymentStatus string `json:"deploymentStatus"`
	MayStillRun      bool   `json:"-"`
}

type ProviderModelsRequest struct {
	ModelID     string         `json:"modelId,omitempty"`
	APIBase     string         `json:"apiBase,omitempty"`
	AuthConfig  map[string]any `json:"authConfig,omitempty"`
	ModelParams map[string]any `json:"modelParams,omitempty"`
	ModelType   string         `json:"modelType,omitempty" validate:"omitempty,oneof=chat decision"`
}

type SupportedParamsRequest struct {
	ModelName string `json:"modelName" validate:"required,min=1,max=255,safename"`
}

type SupportedParamsResponse struct {
	SupportedParams []string `json:"supportedParams"`
}

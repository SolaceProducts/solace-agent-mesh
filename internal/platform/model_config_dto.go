// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

type DataResponse[T any] struct {
	Data T `json:"data"`
}

type PaginationInfo struct {
	PageNumber int  `json:"pageNumber"`
	PageSize   int  `json:"pageSize"`
	Count      int  `json:"count"`
	TotalPages int  `json:"totalPages"`
	NextPage   *int `json:"nextPage"`
}

type PaginationMeta struct {
	Pagination PaginationInfo `json:"pagination"`
}

type PaginatedResponse[T any] struct {
	Data []T            `json:"data"`
	Meta PaginationMeta `json:"meta"`
}

func unpagedResponse[T any](data []T) PaginatedResponse[T] {
	count := len(data)
	return PaginatedResponse[T]{
		Data: nonNilList(data),
		Meta: PaginationMeta{Pagination: PaginationInfo{
			PageNumber: 1,
			PageSize:   count,
			Count:      count,
			TotalPages: 1,
			NextPage:   nil,
		}},
	}
}

func nonNilList[T any](list []T) []T {
	if list == nil {
		return []T{}
	}
	return list
}

type ModelPricing struct {
	Input      *string `json:"input"`
	Output     *string `json:"output"`
	CacheRead  *string `json:"cacheRead"`
	CacheWrite *string `json:"cacheWrite"`
}

type ModelPricingResponse struct {
	Input      *string `json:"input"`
	Output     *string `json:"output"`
	CacheRead  *string `json:"cacheRead"`
	CacheWrite *string `json:"cacheWrite"`
}

type CreateModelConfigRequest struct {
	Alias          string         `json:"alias"`
	Provider       string         `json:"provider"`
	ModelName      string         `json:"modelName"`
	APIBase        string         `json:"apiBase,omitempty"`
	AuthConfig     map[string]any `json:"authConfig"                cfgapply:"secret"`
	ModelParams    map[string]any `json:"modelParams,omitempty"`
	Description    string         `json:"description,omitempty"`
	MaxInputTokens *int           `json:"maxInputTokens,omitempty"`
	Pricing        *ModelPricing  `json:"pricing,omitempty"`
	ModelType      string         `json:"modelType,omitempty" validate:"omitempty,oneof=chat decision" cfgapply:"default=chat"`
}

type UpdateModelConfigRequest struct {
	Alias             *string        `json:"alias,omitempty"`
	Provider          *string        `json:"provider,omitempty"`
	ModelName         *string        `json:"modelName,omitempty"`
	APIBase           *string        `json:"apiBase,omitempty"`
	AuthConfig        map[string]any `json:"authConfig,omitempty"     cfgapply:"secret"`
	ModelParams       map[string]any `json:"modelParams,omitempty"`
	Description       *string        `json:"description,omitempty"`
	DescriptionSet    bool           `json:"-"`
	MaxInputTokens    *int           `json:"maxInputTokens,omitempty"`
	MaxInputTokensSet bool           `json:"-"`
	Pricing           *ModelPricing  `json:"pricing,omitempty"`
}

type ModelConfigResponse struct {
	ID             string                `json:"id"                         cfgapply:"read-only"`
	Alias          string                `json:"alias"`
	Provider       string                `json:"provider"`
	ModelName      string                `json:"modelName"`
	APIBase        *string               `json:"apiBase"`
	AuthType       string                `json:"authType"                   cfgapply:"read-only"`
	AuthConfig     map[string]any        `json:"authConfig"                 cfgapply:"secret"`
	ModelParams    map[string]any        `json:"modelParams"`
	Description    *string               `json:"description"`
	MaxInputTokens *int                  `json:"maxInputTokens"`
	ModelType      string                `json:"modelType"                  cfgapply:"default=chat"`
	Pricing        *ModelPricingResponse `json:"pricing,omitempty"`
	CreatedBy      string                `json:"createdBy"                  cfgapply:"read-only"`
	UpdatedBy      string                `json:"updatedBy"                  cfgapply:"read-only"`
	CreatedAt      string                `json:"createdAt"                  cfgapply:"read-only"`
	UpdatedAt      string                `json:"updatedAt"                  cfgapply:"read-only"`
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
	Provider   string         `json:"provider"`
	ModelID    string         `json:"modelId,omitempty"`
	APIBase    string         `json:"apiBase,omitempty"`
	AuthType   string         `json:"authType,omitempty"`
	AuthConfig map[string]any `json:"authConfig,omitempty"`
}

type SupportedModel struct {
	ID       string `json:"id"`
	Label    string `json:"label"`
	Provider string `json:"provider"`
}

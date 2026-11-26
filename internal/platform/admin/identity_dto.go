// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

type ProfileProviderResponse struct {
	ToolsetID *string   `json:"toolsetId"`
	LookupKey string    `json:"lookupKey"`
	CreatedBy string    `json:"createdBy"`
	UpdatedBy string    `json:"updatedBy"`
	CreatedAt Timestamp `json:"createdAt"`
	UpdatedAt Timestamp `json:"updatedAt"`
}

type UpdateProfileProviderRequest struct {
	ToolsetID string `json:"toolsetId"`
	LookupKey string `json:"lookupKey,omitempty"`
}

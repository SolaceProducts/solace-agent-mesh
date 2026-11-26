// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

type ToolDetailResponse struct {
	ID             string    `json:"id"`
	Name           string    `json:"name"`
	Description    string    `json:"description"`
	Origin         string    `json:"origin"`
	Status         string    `json:"status"`
	Parameters     any       `json:"parameters,omitempty"`
	Instructions   string    `json:"instructions,omitempty"`
	ArtifactParams any       `json:"artifactParams,omitempty"`
	CreatedBy      string    `json:"createdBy"`
	UpdatedBy      string    `json:"updatedBy"`
	CreatedTime    Timestamp `json:"createdAt"`
	UpdatedTime    Timestamp `json:"updatedAt"`
}

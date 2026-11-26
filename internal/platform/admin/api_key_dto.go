// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

const apiKeyResourceType = "apiKey"

type APIKeyResponse struct {
	ID     string `json:"id"`
	Type   string `json:"type"`
	UserID string `json:"userId"`
	Label  string `json:"label"`
	Prefix string `json:"prefix"`

	Scopes []string `json:"scopes"`
	Status string   `json:"status"`

	ExpiresTime  *RESTTimestamp `json:"expiresTime"`
	LastUsedTime *RESTTimestamp `json:"lastUsedTime"`

	CreatedBy   string        `json:"createdBy"`
	CreatedTime RESTTimestamp `json:"createdTime"`
	UpdatedBy   string        `json:"updatedBy"`
	UpdatedTime RESTTimestamp `json:"updatedTime"`

	Secret string `json:"secret,omitempty"`
}

type CreateAPIKeyRequest struct {
	UserID       string         `json:"userId"`
	Label        string         `json:"label"`
	Scopes       []string       `json:"scopes"`
	ExpiresTime  *RESTTimestamp `json:"expiresTime,omitempty"`
	NeverExpires bool           `json:"neverExpires,omitempty"`
}

type UpdateAPIKeyRequest struct {
	Label string `json:"label"`
}

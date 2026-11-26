// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package wire

import (
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/apiv2"
)

type ReplaceWebuiSettingsRequest struct {
	AppName               *string `json:"appName"`
	WelcomeMessage        *string `json:"welcomeMessage"`
	CollectFeedback       *bool   `json:"collectFeedback"`
	PublishFeedback       *bool   `json:"publishFeedback"`
	FeedbackTaskInfoLevel *string `json:"feedbackTaskInfoLevel"`
	DisclaimerText        *string `json:"disclaimerText"`
	LogoURL               *string `json:"logoUrl"`
	SmallLogoURL          *string `json:"smallLogoUrl"`
	SystemPurpose         *string `json:"systemPurpose"`
	ResponseFormat        *string `json:"responseFormat"`
}

type WebuiSettingsResponse struct {
	AppName               *string       `json:"appName"`
	WelcomeMessage        *string       `json:"welcomeMessage"`
	CollectFeedback       *bool         `json:"collectFeedback"`
	PublishFeedback       *bool         `json:"publishFeedback"`
	FeedbackTaskInfoLevel *string       `json:"feedbackTaskInfoLevel"`
	DisclaimerText        *string       `json:"disclaimerText"`
	LogoURL               *string       `json:"logoUrl"`
	SmallLogoURL          *string       `json:"smallLogoUrl"`
	SystemPurpose         *string       `json:"systemPurpose"`
	ResponseFormat        *string       `json:"responseFormat"`
	CreatedBy             *string       `json:"createdBy"`
	UpdatedBy             *string       `json:"updatedBy"`
	CreatedTime           apiv2.Instant `json:"createdTime"`
	UpdatedTime           apiv2.Instant `json:"updatedTime"`
}

type ReplaceProfileProviderRequest struct {
	ToolsetID string `json:"toolsetId" validate:"required,uuid"`
	LookupKey string `json:"lookupKey" validate:"required"`
}

type ProfileProviderResponse struct {
	ToolsetID   *string       `json:"toolsetId"`
	LookupKey   string        `json:"lookupKey"`
	CreatedBy   *string       `json:"createdBy"`
	UpdatedBy   *string       `json:"updatedBy"`
	CreatedTime apiv2.Instant `json:"createdTime"`
	UpdatedTime apiv2.Instant `json:"updatedTime"`
}

type CreateAPIKeyRequest struct {
	UserID       string         `json:"userId"`
	Label        string         `json:"label"`
	Scopes       []string       `json:"scopes"`
	ExpiresTime  *apiv2.Instant `json:"expiresTime"`
	NeverExpires bool           `json:"neverExpires"`
}

type UpdateAPIKeyRequest struct {
	Label string `json:"label"`
}

type APIKeyResponse struct {
	ID           string        `json:"id"`
	Type         string        `json:"type"`
	UserID       string        `json:"userId"`
	Label        string        `json:"label"`
	Prefix       string        `json:"prefix"`
	Scopes       []string      `json:"scopes"`
	Status       string        `json:"status"`
	ExpiresTime  apiv2.Instant `json:"expiresTime"`
	LastUsedTime apiv2.Instant `json:"lastUsedTime"`
	CreatedBy    string        `json:"createdBy"`
	UpdatedBy    string        `json:"updatedBy"`
	CreatedTime  apiv2.Instant `json:"createdTime"`
	UpdatedTime  apiv2.Instant `json:"updatedTime"`
}

type CreatedAPIKey struct {
	ID           string        `json:"id"`
	Type         string        `json:"type"`
	UserID       string        `json:"userId"`
	Label        string        `json:"label"`
	Prefix       string        `json:"prefix"`
	Secret       string        `json:"secret"`
	Scopes       []string      `json:"scopes"`
	Status       string        `json:"status"`
	ExpiresTime  apiv2.Instant `json:"expiresTime"`
	LastUsedTime apiv2.Instant `json:"lastUsedTime"`
	CreatedBy    string        `json:"createdBy"`
	UpdatedBy    string        `json:"updatedBy"`
	CreatedTime  apiv2.Instant `json:"createdTime"`
	UpdatedTime  apiv2.Instant `json:"updatedTime"`
}

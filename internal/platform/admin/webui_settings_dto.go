// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

type WebuiSettingsResponse struct {
	AppName               *string   `json:"appName"`
	WelcomeMessage        *string   `json:"welcomeMessage"`
	CollectFeedback       *bool     `json:"collectFeedback"`
	PublishFeedback       *bool     `json:"publishFeedback"`
	FeedbackTaskInfoLevel *string   `json:"feedbackTaskInfoLevel"`
	DisclaimerText        *string   `json:"disclaimerText"`
	LogoURL               *string   `json:"logoUrl"`
	SmallLogoURL          *string   `json:"smallLogoUrl"`
	SystemPurpose         *string   `json:"systemPurpose"`
	ResponseFormat        *string   `json:"responseFormat"`
	CreatedBy             string    `json:"createdBy"`
	UpdatedBy             string    `json:"updatedBy"`
	CreatedAt             Timestamp `json:"createdAt"`
	UpdatedAt             Timestamp `json:"updatedAt"`
}

type UpdateWebuiSettingsRequest struct {
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

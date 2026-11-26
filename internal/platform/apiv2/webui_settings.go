// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"context"
	"log/slog"
	"net/http"

	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

type WebuiSettings struct {
	AppName               *string `json:"appName" example:"Acme Assistant"`
	WelcomeMessage        *string `json:"welcomeMessage" example:"Ask me anything about your orders."`
	CollectFeedback       *bool   `json:"collectFeedback" example:"true"`
	PublishFeedback       *bool   `json:"publishFeedback" example:"false"`
	FeedbackTaskInfoLevel *string `json:"feedbackTaskInfoLevel" example:"summary"`
	DisclaimerText        *string `json:"disclaimerText" example:"Responses may be inaccurate."`
	LogoURL               *string `json:"logoUrl" example:"https://assets.example.com/logo.svg"`
	SmallLogoURL          *string `json:"smallLogoUrl" example:"/assets/logo-small.svg"`
	SystemPurpose         *string `json:"systemPurpose" example:"You help customers track their orders."`
	ResponseFormat        *string `json:"responseFormat" example:"Answer in short paragraphs."`
	CreatedBy             *string `json:"createdBy" example:"jane.doe@example.com"`
	UpdatedBy             *string `json:"updatedBy" example:"jane.doe@example.com"`
	CreatedTime           Instant `json:"createdTime"`
	UpdatedTime           Instant `json:"updatedTime"`
}

type ReplaceWebuiSettingsRequest struct {
	AppName               *string `json:"appName" example:"Acme Assistant"`
	WelcomeMessage        *string `json:"welcomeMessage" example:"Ask me anything about your orders."`
	CollectFeedback       *bool   `json:"collectFeedback" example:"true"`
	PublishFeedback       *bool   `json:"publishFeedback" example:"false"`
	FeedbackTaskInfoLevel *string `json:"feedbackTaskInfoLevel" example:"summary"`
	DisclaimerText        *string `json:"disclaimerText" example:"Responses may be inaccurate."`
	LogoURL               *string `json:"logoUrl" example:"https://assets.example.com/logo.svg"`
	SmallLogoURL          *string `json:"smallLogoUrl" example:"/assets/logo-small.svg"`
	SystemPurpose         *string `json:"systemPurpose" example:"You help customers track their orders."`
	ResponseFormat        *string `json:"responseFormat" example:"Answer in short paragraphs."`
}

type webuiSettingsHandler struct {
	svc *entplatform.WebuiSettingsService
}

func webuiSettingsRoutes(deps *entplatform.RouteDeps, _ *slog.Logger) []Route {
	if deps.WebuiSettingsSvc == nil {
		return nil
	}
	h := &webuiSettingsHandler{svc: deps.WebuiSettingsSvc}
	return []Route{
		{Method: http.MethodGet, Path: "/webuiSettings", Scope: rbac.ScopeWebuiSettingsRead,
			AllowAPIKey: true, Published: true, Stability: StabilityStable,
			Doc: Doc{ID: "getWebuiSettings", Summary: "Get the web UI settings",
				Text: "Returns the web UI settings an administrator has overridden for the whole instance. Every field is null when no override is stored, meaning the web UI entrypoint's own value applies.", Tag: webuiSettingsTag},
			Handler: Handle(h.get, "Unable to get web UI settings")},
		{Method: http.MethodPut, Path: "/webuiSettings", Scope: rbac.ScopeWebuiSettingsUpdate,
			AllowAPIKey: true, Published: true, Stability: StabilityStable,
			Doc: Doc{ID: "replaceWebuiSettings", Summary: "Replace the web UI settings",
				Text: "Replaces every override with the body. A field left out or null stores no override. A body with every field null clears all overrides. If fields fail validation, the request fails with 422 and lists each one under its own name.", Tag: webuiSettingsTag},
			Handler: Handle(h.replace, "Unable to replace web UI settings")},
	}
}

func (h *webuiSettingsHandler) get(ctx context.Context, _ struct{}) (WebuiSettings, error) {
	resp, err := h.svc.Get(ctx)
	if err != nil {
		return WebuiSettings{}, err
	}
	return toWebuiSettings(resp), nil
}

func (h *webuiSettingsHandler) replace(ctx context.Context, req ReplaceWebuiSettingsRequest) (WebuiSettings, error) {
	resp, err := h.svc.Set(ctx, entplatform.UpdateWebuiSettingsRequest(req), entplatform.UserIDFromContext(ctx))
	if err != nil {
		return WebuiSettings{}, err
	}
	return toWebuiSettings(resp), nil
}

func toWebuiSettings(resp *entplatform.WebuiSettingsResponse) WebuiSettings {
	return WebuiSettings{
		AppName:               resp.AppName,
		WelcomeMessage:        resp.WelcomeMessage,
		CollectFeedback:       resp.CollectFeedback,
		PublishFeedback:       resp.PublishFeedback,
		FeedbackTaskInfoLevel: resp.FeedbackTaskInfoLevel,
		DisclaimerText:        resp.DisclaimerText,
		LogoURL:               resp.LogoURL,
		SmallLogoURL:          resp.SmallLogoURL,
		SystemPurpose:         resp.SystemPurpose,
		ResponseFormat:        resp.ResponseFormat,
		CreatedBy:             NullIfZero(resp.CreatedBy),
		UpdatedBy:             NullIfZero(resp.UpdatedBy),
		CreatedTime:           Instant(resp.CreatedAt),
		UpdatedTime:           Instant(resp.UpdatedAt),
	}
}

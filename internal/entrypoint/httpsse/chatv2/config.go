// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package chatv2

import (
	"context"
	"net/http"
	"reflect"

	"github.com/SolaceProducts/solace-agent-mesh/internal/apicontract"
)

const GatePreLogin apicontract.Gate = "preLogin"

type ChatLoginConfig struct {
	AuthorizationEnabled bool `json:"authorizationEnabled" example:"true"`

	LoginURL *string `json:"loginUrl" example:"/api/v1/auth/login"`

	IdentityServiceType string `json:"identityServiceType" example:"enterprise"`

	AppName string `json:"appName" example:"Solace Agent Mesh"`

	WelcomeMessage *string `json:"welcomeMessage" example:"Welcome back"`

	LogoURL *string `json:"logoUrl" example:"https://example.com/logo.svg"`

	SmallLogoURL *string `json:"smallLogoUrl" example:"https://example.com/logo-small.svg"`

	DisclaimerText *string `json:"disclaimerText" example:"Responses may be inaccurate."`
}

type ChatValidationLimits struct {
	ProjectNameMax int `json:"projectNameMax" example:"255"`

	ProjectDescriptionMax int `json:"projectDescriptionMax" example:"1000"`

	ProjectInstructionsMax int `json:"projectInstructionsMax" example:"4000"`

	MaxPerFileUploadSizeBytes int64 `json:"maxPerFileUploadSizeBytes" example:"52428800"`

	MaxBatchUploadSizeBytes int64 `json:"maxBatchUploadSizeBytes" example:"104857600"`

	MaxZipUploadSizeBytes int64 `json:"maxZipUploadSizeBytes" example:"104857600"`

	MaxProjectSizeBytes int64 `json:"maxProjectSizeBytes" example:"104857600"`
}

type ChatDataRetention struct {
	TaskDays int `json:"taskDays" example:"90"`

	FeedbackDays int `json:"feedbackDays" example:"90"`
}

type ChatConfig struct {
	AuthorizationEnabled bool `json:"authorizationEnabled" example:"true"`

	LoginURL *string `json:"loginUrl" example:"/api/v1/auth/login"`

	IdentityServiceType string `json:"identityServiceType" example:"enterprise"`

	AppName string `json:"appName" example:"Solace Agent Mesh"`

	WelcomeMessage *string `json:"welcomeMessage" example:"Welcome back"`

	LogoURL *string `json:"logoUrl" example:"https://example.com/logo.svg"`

	SmallLogoURL *string `json:"smallLogoUrl" example:"https://example.com/logo-small.svg"`

	DisclaimerText *string `json:"disclaimerText" example:"Responses may be inaccurate."`

	CollectFeedback bool `json:"collectFeedback" example:"true"`

	PersistenceEnabled bool `json:"persistenceEnabled" example:"true"`

	SchedulerEnabled bool `json:"schedulerEnabled" example:"true"`

	ActivationGateEnabled bool `json:"activationGateEnabled" example:"false"`

	PeopleSearchAvailable bool `json:"peopleSearchAvailable" example:"true"`

	GoogleChatEndpointBase *string `json:"googleChatEndpointBase" example:"https://mesh.example.com"`

	AzureWorkloadIdentityEnabled bool `json:"azureWorkloadIdentityEnabled" example:"false"`

	ProductAnalyticsEnabled bool `json:"productAnalyticsEnabled" example:"false"`

	TextToSpeechEnabled bool `json:"textToSpeechEnabled" example:"false"`

	SpeechToTextEnabled bool `json:"speechToTextEnabled" example:"false"`

	BackgroundTaskTimeoutMs int `json:"backgroundTaskTimeoutMs" example:"3600000"`

	ValidationLimits ChatValidationLimits `json:"validationLimits"`

	DataRetention *ChatDataRetention `json:"dataRetention"`

	RedirectURL *string `json:"redirectUrl" example:"https://chat.example.com/auth-callback.html"`

	ServerURL *string `json:"serverUrl" example:"https://mesh.example.com"`

	PlatformServerURL *string `json:"platformServerUrl" example:"https://platform.example.com"`

	PlatformServiceEnabled bool `json:"platformServiceEnabled" example:"true"`

	ClaimKey *string `json:"claimKey" example:"groups"`

	SessionIdleTimeoutSeconds *int `json:"sessionIdleTimeoutSeconds" example:"1800"`
}

func configType() reflect.Type { return reflect.TypeFor[ChatConfig]() }

func loginConfigType() reflect.Type { return reflect.TypeFor[ChatLoginConfig]() }

type configHandler struct {
	deps Deps
}

func (h *configHandler) get(ctx context.Context, _ struct{}) (ChatConfig, error) {
	if h.deps.ReadConfig == nil {
		return ChatConfig{}, nil
	}
	return h.deps.ReadConfig(ctx)
}

func (h *configHandler) getLogin(ctx context.Context, _ struct{}) (ChatLoginConfig, error) {
	if h.deps.ReadLoginConfig == nil {
		return ChatLoginConfig{}, nil
	}
	return h.deps.ReadLoginConfig(ctx)
}

func ConfigRoutes(deps Deps) []apicontract.Route {
	h := &configHandler{deps: deps}
	return []apicontract.Route{
		{Method: http.MethodGet, Path: "/config", Gate: GateSignedIn,
			Stability: apicontract.StabilityDraft,
			Doc: apicontract.Doc{ID: "getChatConfig", Summary: "Get the chat configuration",
				Text: "Returns what this gateway offers its chat users once they have signed in: its branding, which optional features it has, and the limits it enforces. " +
					"A client reads it once after login and uses it to decide which parts of its interface to show. " +
					"To learn how to sign in, read the login configuration instead, which needs no sign-in.",
				Tag: configTag},
			Handler: apicontract.Handle(h.get, "Unable to get chat configuration")},
		{Method: http.MethodGet, Path: "/config/login", Gate: GatePreLogin,
			Stability: apicontract.StabilityDraft,
			Doc: apicontract.Doc{ID: "getChatLoginConfig", Summary: "Get the login configuration",
				Text: "Returns what a client needs before anyone has signed in: whether the gateway requires a sign-in, where to send the browser to start one, and the branding for the sign-in screen. " +
					"This is the one chat route that needs no sign-in. Read it first, then read the chat configuration after login.",
				Tag: configTag},
			Handler: apicontract.Handle(h.getLogin, "Unable to get login configuration")},
	}
}

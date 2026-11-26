// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"context"

	"github.com/SolaceProducts/solace-agent-mesh/internal/apicontract"
	"github.com/SolaceProducts/solace-agent-mesh/internal/buildedition"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/httpsse/chatv2"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
)

func (h *configHandler) chatV2Routes() []apicontract.Route {
	return chatv2.ConfigRoutes(chatv2.Deps{ReadConfig: h.readChatConfig, ReadLoginConfig: h.readChatLoginConfig})
}

func (h *configHandler) readChatLoginConfig(context.Context) (chatv2.ChatLoginConfig, error) {
	cfg := h.core.Config()
	branding := h.resolveBranding(cfg)
	return chatv2.ChatLoginConfig{
		AuthorizationEnabled: cfg.FrontendUseAuthorization,
		LoginURL:             nonEmpty(h.frontendLoginURL(cfg)),
		IdentityServiceType:  h.authProviderType,
		AppName:              branding.AppName,
		WelcomeMessage:       configuredBranding(branding.WelcomeMessage, branding.Sources["welcomeMessage"]),
		LogoURL:              configuredBranding(branding.LogoURL, branding.Sources["logoUrl"]),
		SmallLogoURL:         configuredBranding(branding.SmallLogoURL, branding.Sources["smallLogoUrl"]),
		DisclaimerText:       nonEmpty(branding.DisclaimerText),
	}, nil
}

func (h *configHandler) readChatConfig(ctx context.Context) (chatv2.ChatConfig, error) {
	cfg := h.core.Config()
	rawCfg := h.core.RawConfig()
	login, err := h.readChatLoginConfig(ctx)
	if err != nil {
		return chatv2.ChatConfig{}, err
	}
	persistenceEnabled := h.core.Store() != nil
	ttsSettings, sttSettings, _ := h.computeSpeechSettings(cfg)

	var idleTimeout *int
	if seconds := h.sessionIdleTimeoutSeconds(rawCfg); seconds > 0 {
		idleTimeout = &seconds
	}

	return chatv2.ChatConfig{
		AuthorizationEnabled:         login.AuthorizationEnabled,
		LoginURL:                     login.LoginURL,
		IdentityServiceType:          login.IdentityServiceType,
		AppName:                      login.AppName,
		WelcomeMessage:               login.WelcomeMessage,
		LogoURL:                      login.LogoURL,
		SmallLogoURL:                 login.SmallLogoURL,
		DisclaimerText:               login.DisclaimerText,
		CollectFeedback:              h.feedbackEnabled(h.resolveBranding(cfg), persistenceEnabled),
		PersistenceEnabled:           persistenceEnabled,
		SchedulerEnabled:             h.core.ScheduledTasks() != nil,
		ActivationGateEnabled:        h.core.Activation() != nil,
		PeopleSearchAvailable:        h.peopleSearchAvailable(ctx),
		GoogleChatEndpointBase:       nonEmpty(googleChatEndpointBase()),
		AzureWorkloadIdentityEnabled: llm.AzureWorkloadIdentityAvailable(),
		ProductAnalyticsEnabled:      buildedition.ProductAnalytics,
		TextToSpeechEnabled:          ttsSettings["textToSpeech"] == true,
		SpeechToTextEnabled:          sttSettings["speechToText"] == true,
		BackgroundTaskTimeoutMs:      cfgIntDefault(cfgMap(rawCfg, "background_tasks"), "default_timeout_ms", defaultBackgroundTaskTimeoutMs),
		ValidationLimits:             h.validationLimits(cfg),
		DataRetention:                h.dataRetention(),
		RedirectURL:                  nonEmpty(cfg.FrontendRedirectURL),
		ServerURL:                    nonEmpty(cfg.FrontendServerURL),
		PlatformServerURL:            nonEmpty(h.platformServerURL(cfg)),
		PlatformServiceEnabled:       h.platformServiceEnabled(cfg),
		ClaimKey:                     nonEmpty(effectiveClaimKey(cfg)),
		SessionIdleTimeoutSeconds:    idleTimeout,
	}, nil
}

func nonEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

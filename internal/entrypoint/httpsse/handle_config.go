// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"context"
	"log/slog"
	"net/http"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/buildedition"
	"github.com/SolaceProducts/solace-agent-mesh/internal/capabilities"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/httpsse/chatv2"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/oidcauth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/speech"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/configbuilder"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samversion"
	"github.com/SolaceProducts/solace-agent-mesh/internal/webuisettings"
)

type configHandler struct {
	core               *entrypoint.GatewayCore
	authProviderType   string
	featureRegistry    *features.Registry
	capabilityRegistry *capabilities.Registry
	logger             *slog.Logger
}

const defaultBackgroundTaskTimeoutMs = 3600000

func (h *configHandler) handleConfig(w http.ResponseWriter, r *http.Request) {
	cfg := h.core.Config()
	rawCfg := h.core.RawConfig()
	persistenceEnabled := h.core.Store() != nil

	schedulerEnabled := h.core.ScheduledTasks() != nil

	branding := h.resolveBranding(cfg)

	feedbackEnabled := h.feedbackEnabled(branding, persistenceEnabled)

	ttsSettings, sttSettings, toolConfigStatus := h.computeSpeechSettings(cfg)

	validationLimits := h.computeValidationLimits(cfg)

	var dataRetention map[string]any
	if retention := h.dataRetention(); retention != nil {
		dataRetention = map[string]any{"taskDays": retention.TaskDays, "feedbackDays": retention.FeedbackDays}
	}

	bgTasksConfig := map[string]any{
		"defaultTimeoutMs": cfgIntDefault(cfgMap(rawCfg, "background_tasks"), "default_timeout_ms", defaultBackgroundTaskTimeoutMs),
	}

	frontendLoginURL := h.frontendLoginURL(cfg)

	platformServerURL := h.platformServerURL(cfg)

	claimKey := effectiveClaimKey(cfg)

	resp := map[string]any{
		"frontendUseAuthorization":       cfg.FrontendUseAuthorization,
		"frontendWelcomeMessage":         configuredBranding(branding.WelcomeMessage, branding.Sources["welcomeMessage"]),
		"frontendAppName":                branding.AppName,
		"frontendCollectFeedback":        feedbackEnabled,
		"frontendLogoUrl":                configuredBranding(branding.LogoURL, branding.Sources["logoUrl"]),
		"frontendSmallLogoUrl":           configuredBranding(branding.SmallLogoURL, branding.Sources["smallLogoUrl"]),
		"frontendDisclaimerText":         branding.DisclaimerText,
		"persistenceEnabled":             persistenceEnabled,
		"schedulerEnabled":               schedulerEnabled,
		"activationGateEnabled":          h.core.Activation() != nil,
		"peopleSearchAvailable":          h.peopleSearchAvailable(r.Context()),
		"azureWorkloadIdentityEnabled":   llm.AzureWorkloadIdentityAvailable(),
		"productAnalyticsEnabled":        buildedition.ProductAnalytics,
		"validationLimits":               validationLimits,
		"toolConfigStatus":               toolConfigStatus,
		"ttsSettings":                    ttsSettings,
		"sttSettings":                    sttSettings,
		"backgroundTasksConfig":          bgTasksConfig,
		"identityServiceType":            h.authProviderType,
		"frontendAuthLoginUrl":           frontendLoginURL,
		"frontendRedirectUrl":            cfg.FrontendRedirectURL,
		"frontendServerUrl":              cfg.FrontendServerURL,
		"googleChatEndpointBase":         googleChatEndpointBase(),
		"frontendPlatformServerUrl":      platformServerURL,
		"frontendPlatformServiceEnabled": h.platformServiceEnabled(cfg),
		"externalAuthServiceUrl":         cfg.ExternalAuthServiceURL,
		"externalAuthCallbackUri":        cfg.ExternalAuthCallbackURI,
		"externalAuthProvider":           cfg.ExternalAuthProvider,
		"externalAuthClaimKey":           claimKey,
		"sessionIdleTimeoutSeconds":      h.sessionIdleTimeoutSeconds(rawCfg),
	}
	if dataRetention != nil {
		resp["dataRetention"] = dataRetention
	}

	writeJSON(w, http.StatusOK, resp)
}

func (h *configHandler) feedbackEnabled(branding webuisettings.Effective, persistenceEnabled bool) bool {
	if branding.CollectFeedback && !persistenceEnabled {
		h.logger.Warn("feedback configured but persistence not enabled; disabling for frontend", "configKey", "frontend_collect_feedback")
		return false
	}
	return branding.CollectFeedback
}

func (h *configHandler) frontendLoginURL(cfg *entrypoint.GatewayConfig) string {
	if cfg.FrontendAuthLoginURL == "" && h.authProviderType == auth.ProviderTypeEnterprise {
		return "/api/v1/auth/login"
	}
	return cfg.FrontendAuthLoginURL
}

func googleChatEndpointBase() string {
	return strings.TrimRight(configbuilder.ResolveEnv("WEBUI_FRONTEND_SERVER_URL", ""), "/")
}

func (h *configHandler) platformServiceEnabled(cfg *entrypoint.GatewayConfig) bool {
	return h.core.PlatformMounted() || cfg.PlatformServiceURL != ""
}

func (h *configHandler) dataRetention() *chatv2.ChatDataRetention {
	taskDays, feedbackDays, ok := h.core.RetentionWindow()
	if !ok {
		return nil
	}
	return &chatv2.ChatDataRetention{TaskDays: taskDays, FeedbackDays: feedbackDays}
}

func (h *configHandler) platformServerURL(cfg *entrypoint.GatewayConfig) string {
	if h.core.PlatformMounted() {
		return ""
	}
	return cfg.PlatformServiceURL
}

func effectiveClaimKey(cfg *entrypoint.GatewayConfig) string {
	if cfg.ExternalAuthClaimKey != "" {
		return cfg.ExternalAuthClaimKey
	}
	return cfgString(cfgMap(cfg.AuthorizationServiceRaw, "idp_claims_config"), "claim_key")
}

func (h *configHandler) peopleSearchAvailable(ctx context.Context) bool {
	probe, ok := h.core.IdentityService().(auth.PeopleSearchProbe)
	if !ok {
		return false
	}
	available, err := probe.SearchConfigured(ctx)
	if err != nil {
		h.logger.Warn("people search availability check failed; hiding people search", "error", err)
		return false
	}
	return available
}

func (h *configHandler) sessionIdleTimeoutSeconds(rawCfg map[string]any) int {
	if h.authProviderType != auth.ProviderTypeEnterprise {
		return 0
	}
	satCfg, _ := oidcauth.ParseSAMAccessTokenConfig(rawCfg)
	return int(satCfg.IdleTimeout().Seconds())
}

func (h *configHandler) resolveBranding(cfg *entrypoint.GatewayConfig) webuisettings.Effective {
	var row *webuisettings.Settings
	if reg := h.core.WebuiSettingsRegistry(); reg != nil {
		row, _ = reg.Cached()
	}
	return webuisettings.Resolve(row, entrypoint.WebuiSettingsFallbackFromConfig(cfg.FrontendConfigRaw, cfg.FeedbackPublishingRaw))
}

func configuredBranding(value string, source webuisettings.Source) *string {
	if source == webuisettings.SourceDefault {
		return nil
	}
	return &value
}

func (h *configHandler) computeSpeechSettings(cfg *entrypoint.GatewayConfig) (ttsSettings, sttSettings, toolConfigStatus map[string]any) {
	ttsConfigured := false
	sttConfigured := false
	ttsProvider := "gemini"

	if speechRaw := voiceSpeechConfig(h.featureRegistry, cfg); speechRaw != nil {
		parsed := speech.ParseAudioConfig(speechRaw)

		if ttsCfg := cfgMap(speechRaw, "tts"); len(ttsCfg) > 0 {
			preferred := cfgStringDefault(ttsCfg, "provider", "gemini")

			geminiAvailable := speech.IsValidAPIKey(parsed.Gemini.APIKey)
			azureAvailable := speech.IsValidAPIKey(parsed.Azure.APIKey) &&
				speech.IsValidAPIKey(parsed.Azure.Region)
			pollyAvailable := speech.IsValidAPIKey(parsed.Polly.AWSAccessKeyID) &&
				speech.IsValidAPIKey(parsed.Polly.AWSSecretAccessKey)

			ttsConfigured = geminiAvailable || azureAvailable || pollyAvailable

			switch {
			case preferred == "azure" && azureAvailable:
				ttsProvider = "azure"
			case preferred == "gemini" && geminiAvailable:
				ttsProvider = "gemini"
			case preferred == "polly" && pollyAvailable:
				ttsProvider = "polly"
			case geminiAvailable:
				ttsProvider = "gemini"
			case azureAvailable:
				ttsProvider = "azure"
			case pollyAvailable:
				ttsProvider = "polly"
			default:
				ttsProvider = "gemini"
			}
		}

		switch parsed.STTProviderName {
		case "openai":
			sttConfigured = speech.IsValidAPIKey(parsed.OpenAI.APIKey) && parsed.OpenAI.URL != ""
		case "azure":
			sttConfigured = speech.IsValidAPIKey(parsed.Azure.APIKey) && speech.IsValidAPIKey(parsed.Azure.Region)
		}
	}

	engineTTS := "browser"
	if ttsConfigured {
		engineTTS = "external"
	}

	ttsSettings = map[string]any{
		"textToSpeech": ttsConfigured,
		"engineTTS":    engineTTS,
		"ttsProvider":  ttsProvider,
	}
	sttSettings = map[string]any{
		"speechToText": sttConfigured,
	}
	toolConfigStatus = map[string]any{
		"textToSpeech": ttsConfigured,
		"speechToText": sttConfigured,
	}
	return ttsSettings, sttSettings, toolConfigStatus
}

func (h *configHandler) validationLimits(cfg *entrypoint.GatewayConfig) chatv2.ChatValidationLimits {
	const defaultBatchOrArchiveBytes = 100 * 1024 * 1024
	return chatv2.ChatValidationLimits{
		ProjectNameMax:            maxProjectNameLength,
		ProjectDescriptionMax:     cfg.GatewayMaxProjectDescriptionChars,
		ProjectInstructionsMax:    cfg.GatewayMaxProjectInstructionsChars,
		MaxPerFileUploadSizeBytes: int64(cfg.GatewayMaxUploadSizeBytes),
		MaxBatchUploadSizeBytes:   defaultBatchOrArchiveBytes,
		MaxZipUploadSizeBytes:     defaultBatchOrArchiveBytes,
		MaxProjectSizeBytes:       defaultBatchOrArchiveBytes,
	}
}

func (h *configHandler) computeValidationLimits(cfg *entrypoint.GatewayConfig) map[string]any {
	l := h.validationLimits(cfg)
	return map[string]any{
		"projectNameMax":            l.ProjectNameMax,
		"projectDescriptionMax":     l.ProjectDescriptionMax,
		"projectInstructionsMax":    l.ProjectInstructionsMax,
		"maxPerFileUploadSizeBytes": int(l.MaxPerFileUploadSizeBytes),
		"maxBatchUploadSizeBytes":   int(l.MaxBatchUploadSizeBytes),
		"maxZipUploadSizeBytes":     int(l.MaxZipUploadSizeBytes),
		"maxProjectSizeBytes":       int(l.MaxProjectSizeBytes),
	}
}

func hasLLMModel(cfg *entrypoint.GatewayConfig) bool {
	if cfg.ModelRaw == nil {
		return false
	}
	switch v := cfg.ModelRaw.(type) {
	case map[string]any:
		model, _ := v["model"].(string)
		return model != ""
	case string:
		return v != ""
	}
	return false
}

func cfgMap(m map[string]any, key string) map[string]any {
	if m == nil {
		return nil
	}
	v, _ := m[key].(map[string]any)
	return v
}

func cfgBool(m map[string]any, key string) bool {
	if m == nil {
		return false
	}
	v, _ := m[key].(bool)
	return v
}

func cfgString(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	v, _ := m[key].(string)
	return v
}

func cfgStringDefault(m map[string]any, key string, def string) string {
	if m == nil {
		return def
	}
	v, ok := m[key].(string)
	if !ok || v == "" {
		return def
	}
	return v
}

func cfgIntDefault(m map[string]any, key string, def int) int {
	if m == nil {
		return def
	}
	switch v := m[key].(type) {
	case int:
		return v
	case float64:
		return int(v)
	case int64:
		return int(v)
	}
	return def
}

func (h *configHandler) handleFeatureFlags(w http.ResponseWriter, _ *http.Request) {
	type flagResponse struct {
		Key             string `json:"key"`
		Name            string `json:"name"`
		ReleasePhase    string `json:"releasePhase"`
		Resolved        bool   `json:"resolved"`
		HasEnvOverride  bool   `json:"hasEnvOverride"`
		RegistryDefault bool   `json:"registryDefault"`
		Description     string `json:"description"`
	}

	if h.featureRegistry == nil {
		writeJSON(w, http.StatusOK, unpagedResponse([]flagResponse{}))
		return
	}

	defs := h.featureRegistry.All()
	result := make([]flagResponse, len(defs))
	for i, d := range defs {
		resolved := h.featureRegistry.IsEnabled(d.Key)
		if d.Key == features.KeyNotifyService && (h.core == nil || !h.core.NotifyMounted()) {
			resolved = false
		}
		result[i] = flagResponse{
			Key:             d.Key,
			Name:            d.Name,
			ReleasePhase:    string(d.ReleasePhase),
			Resolved:        resolved,
			HasEnvOverride:  h.featureRegistry.HasEnvOverride(d.Key),
			RegistryDefault: d.Default,
			Description:     d.Description,
		}
	}

	writeJSON(w, http.StatusOK, unpagedResponse(result))
}

func (h *configHandler) handleCapabilities(w http.ResponseWriter, _ *http.Request) {
	if h.capabilityRegistry == nil {
		writeJSON(w, http.StatusOK, unpagedResponse([]capabilities.Resolved{}))
		return
	}
	writeJSON(w, http.StatusOK, unpagedResponse(h.capabilityRegistry.All()))
}

func (h *configHandler) handleVersion(w http.ResponseWriter, _ *http.Request) {
	resp := VersionResponse{
		Products: []ProductInfo{
			{
				ID:      "sam-gateway-go",
				Name:    "Solace Agent Mesh",
				Version: samversion.Version,
			},
			{
				ID:      "sam-str-go",
				Name:    "Solace Agent Mesh Secure Tool Runtime",
				Version: samversion.STRVersion,
			},
		},
		MinClientVersion: samversion.MinClientVersion,
		APIVersion:       samversion.APIVersion,
	}
	writeJSON(w, http.StatusOK, resp)
}

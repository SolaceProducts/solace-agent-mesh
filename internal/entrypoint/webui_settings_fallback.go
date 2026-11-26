// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/webuisettings"
)

func WebuiSettingsFallbackFromConfig(raw, feedbackPublishingRaw map[string]any) webuisettings.Fallback {
	return webuisettings.Fallback{
		AppName:               cfgRawStringPtr(raw, "frontend_app_name"),
		WelcomeMessage:        cfgRawStringPtr(raw, "frontend_welcome_message"),
		CollectFeedback:       cfgRawBoolPtr(raw, "frontend_collect_feedback", "frontend"),
		PublishFeedback:       cfgRawBoolPtr(feedbackPublishingRaw, "enabled", "feedback_publishing"),
		FeedbackTaskInfoLevel: cfgRawFeedbackTaskInfoLevelPtr(feedbackPublishingRaw),
		DisclaimerText:        cfgRawStringPtr(raw, "frontend_disclaimer_text"),
		LogoURL:               cfgRawStringPtr(raw, "frontend_logo_url"),
		SmallLogoURL:          cfgRawStringPtr(raw, "frontend_small_logo_url"),
	}
}

func (g *GatewayCore) FeedbackMaxPayloadSizeBytes() int64 {
	if ptr := cfgRawInt64Ptr(g.cfg.FeedbackPublishingRaw, "max_payload_size_bytes", "feedback_publishing"); ptr != nil {
		return *ptr
	}
	return webuisettings.DefaultFeedbackMaxPayloadSizeBytes
}

func cfgRawStringPtr(raw map[string]any, key string) *string {
	if raw == nil {
		return nil
	}
	v, ok := raw[key]
	if !ok {
		return nil
	}
	s, ok := v.(string)
	if !ok {
		slog.Default().Warn("frontend config value is not a string; falling back to the default",
			"key", key, "gotType", fmt.Sprintf("%T", v))
		return nil
	}
	return &s
}

func cfgRawBoolPtr(raw map[string]any, key, block string) *bool {
	if raw == nil {
		return nil
	}
	v, ok := raw[key]
	if !ok {
		return nil
	}
	b, ok := v.(bool)
	if !ok {
		slog.Default().Warn("config value is not a bool; falling back to the default",
			"block", block, "key", key, "gotType", fmt.Sprintf("%T", v))
		return nil
	}
	return &b
}

func cfgRawInt64Ptr(raw map[string]any, key, block string) *int64 {
	if raw == nil {
		return nil
	}
	v, ok := raw[key]
	if !ok {
		return nil
	}
	var n int64
	switch t := v.(type) {
	case int:
		n = int64(t)
	case int32:
		n = int64(t)
	case int64:
		n = t
	case float64:
		if t != float64(int64(t)) {
			slog.Default().Warn("config value must be a whole number; falling back to the default",
				"block", block, "key", key, "value", t)
			return nil
		}
		n = int64(t)
	default:
		slog.Default().Warn("config value is not an integer; falling back to the default",
			"block", block, "key", key, "gotType", fmt.Sprintf("%T", v))
		return nil
	}
	if n <= 0 {
		slog.Default().Warn("config value must be a positive integer; falling back to the default",
			"block", block, "key", key, "value", n)
		return nil
	}
	return &n
}

func cfgRawFeedbackTaskInfoLevelPtr(raw map[string]any) *string {
	v := cfgRawStringPtr(raw, "include_task_info")
	if v == nil {
		return nil
	}
	if !webuisettings.IsValidFeedbackTaskInfoLevel(*v) {
		slog.Default().Warn("feedback_publishing.include_task_info is not a valid level; falling back to the default",
			"value", *v, "validValues", webuisettings.ValidFeedbackTaskInfoLevels())
		return nil
	}
	return v
}

func stringPtrOrNil(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func (g *GatewayCore) ResolveAssistantContext(ctx context.Context) webuisettings.Effective {
	fallback := webuisettings.Fallback{
		SystemPurpose:  stringPtrOrNil(g.cfg.SystemPurpose),
		ResponseFormat: stringPtrOrNil(g.cfg.ResponseFormat),
	}
	var row *webuisettings.Settings
	if g.servesWebUI && g.webuiSettingsRegistry != nil {
		row, _ = g.webuiSettingsRegistry.Cached()
	}
	return webuisettings.Resolve(row, fallback)
}

func (g *GatewayCore) ResolveFeedbackPublishing() webuisettings.Effective {
	var row *webuisettings.Settings
	if g.servesWebUI && g.webuiSettingsRegistry != nil {
		row, _ = g.webuiSettingsRegistry.Cached()
	}
	return webuisettings.Resolve(row, WebuiSettingsFallbackFromConfig(
		g.cfg.FrontendConfigRaw, g.cfg.FeedbackPublishingRaw))
}

func (g *GatewayCore) stampAssistantContext(ctx context.Context, message *a2a.Message) {
	eff := g.ResolveAssistantContext(ctx)
	if message.Metadata == nil {
		message.Metadata = make(map[string]any, 2)
	}
	if _, set := message.Metadata["system_purpose"]; !set {
		message.Metadata["system_purpose"] = eff.SystemPurpose
	}
	if _, set := message.Metadata["response_format"]; !set {
		message.Metadata["response_format"] = eff.ResponseFormat
	}
}

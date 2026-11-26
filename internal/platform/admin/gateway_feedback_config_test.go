// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func appConfigFrom(t *testing.T, cfg map[string]any) map[string]any {
	t.Helper()
	apps, _ := cfg["apps"].([]map[string]any)
	if len(apps) == 0 {
		t.Fatalf("config has no apps: %v", cfg)
	}
	appConfig, _ := apps[0]["app_config"].(map[string]any)
	return appConfig
}

func adapterConfigFrom(t *testing.T, cfg map[string]any) map[string]any {
	t.Helper()
	appConfig := appConfigFrom(t, cfg)
	ga, _ := appConfig["gateway_adapter"].(map[string]any)
	ac, _ := ga["adapter_config"].(map[string]any)
	return ac
}

func TestGatewayBuilders_FeedbackPublishingPropagates(t *testing.T) {
	svc := NewYAMLService("test-ns", nil)
	gw := &store.Gateway{ID: "gw-fp-1", Name: "fp", Description: strPtr("feedback publishing propagation fixture")}

	build := map[string]func(map[string]any) map[string]any{
		"slack":       func(v map[string]any) map[string]any { return svc.buildSlackGatewayConfig(gw, v) },
		"teams":       func(v map[string]any) map[string]any { return svc.buildTeamsGatewayConfig(gw, v) },
		"whatsapp":    func(v map[string]any) map[string]any { return svc.buildWhatsAppGatewayConfig(gw, v) },
		"google_chat": func(v map[string]any) map[string]any { return svc.buildGoogleChatGatewayConfig(gw, v) },
	}
	for name, b := range build {
		t.Run(name+": feedback_publishing propagates to app_config, not adapter_config", func(t *testing.T) {
			appConfig := appConfigFrom(t, b(map[string]any{
				"feedback_publishing": map[string]any{"enabled": true},
			}))
			fp, ok := appConfig["feedback_publishing"].(map[string]any)
			if !ok {
				t.Fatalf("%s app_config.feedback_publishing = %v (%T), want a map", name, appConfig["feedback_publishing"], appConfig["feedback_publishing"])
			}
			if fp["enabled"] != true {
				t.Errorf("%s app_config.feedback_publishing.enabled = %v, want true", name, fp["enabled"])
			}
			ac := adapterConfigFrom(t, b(map[string]any{"feedback_publishing": map[string]any{"enabled": true}}))
			if _, present := ac["feedback_publishing"]; present {
				t.Errorf("%s adapter_config must not carry feedback_publishing (it belongs on app_config)", name)
			}
		})
		t.Run(name+": absent when not set in values", func(t *testing.T) {
			appConfig := appConfigFrom(t, b(map[string]any{}))
			if v, present := appConfig["feedback_publishing"]; present {
				t.Errorf("%s app_config.feedback_publishing should be absent when unset, got %v", name, v)
			}
		})
	}
}

func TestGatewayBuilders_FeedbackEnabledPropagates(t *testing.T) {
	svc := NewYAMLService("test-ns", nil)
	gw := &store.Gateway{ID: "gw-fb-1", Name: "fb", Description: strPtr("feedback propagation fixture")}

	t.Run("slack carries feedback_enabled into adapter_config", func(t *testing.T) {
		ac := adapterConfigFrom(t, svc.buildSlackGatewayConfig(gw, map[string]any{"feedback_enabled": true}))
		if ac["feedback_enabled"] != true {
			t.Errorf("slack adapter_config.feedback_enabled = %v, want true (buttons won't render otherwise)", ac["feedback_enabled"])
		}
	})

	t.Run("teams carries feedback_enabled into adapter_config", func(t *testing.T) {
		ac := adapterConfigFrom(t, svc.buildTeamsGatewayConfig(gw, map[string]any{"feedback_enabled": true}))
		if ac["feedback_enabled"] != true {
			t.Errorf("teams adapter_config.feedback_enabled = %v, want true", ac["feedback_enabled"])
		}
	})

	t.Run("whatsapp carries feedback_enabled into adapter_config", func(t *testing.T) {
		ac := adapterConfigFrom(t, svc.buildWhatsAppGatewayConfig(gw, map[string]any{"feedback_enabled": true}))
		if ac["feedback_enabled"] != true {
			t.Errorf("whatsapp adapter_config.feedback_enabled = %v, want true (reactions are ignored otherwise)", ac["feedback_enabled"])
		}
	})

	t.Run("whatsapp explicit false propagates (capture defaults off)", func(t *testing.T) {
		ac := adapterConfigFrom(t, svc.buildWhatsAppGatewayConfig(gw, map[string]any{"feedback_enabled": false}))
		if v, present := ac["feedback_enabled"]; !present || v != false {
			t.Errorf("whatsapp feedback_enabled=false should propagate as false, got present=%v value=%v", present, v)
		}
	})

	t.Run("explicit false propagates (not dropped)", func(t *testing.T) {
		ac := adapterConfigFrom(t, svc.buildSlackGatewayConfig(gw, map[string]any{"feedback_enabled": false}))
		if v, present := ac["feedback_enabled"]; !present || v != false {
			t.Errorf("feedback_enabled=false should propagate as false, got present=%v value=%v", present, v)
		}
	})

	t.Run("absent when not set in values", func(t *testing.T) {
		ac := adapterConfigFrom(t, svc.buildSlackGatewayConfig(gw, map[string]any{}))
		if v, present := ac["feedback_enabled"]; present {
			t.Errorf("feedback_enabled should be absent when unset in values, got %v", v)
		}
	})
}

func TestGatewayBuilders_ActivityTimelinePropagates(t *testing.T) {
	svc := NewYAMLService("test-ns", nil)
	gw := &store.Gateway{ID: "gw-tl-1", Name: "tl", Description: strPtr("activity timeline propagation fixture")}

	t.Run("slack carries show_activity_timeline + activity_display_mode into adapter_config", func(t *testing.T) {
		ac := adapterConfigFrom(t, svc.buildSlackGatewayConfig(gw, map[string]any{
			"show_activity_timeline": true,
			"activity_display_mode":  "timeline",
		}))
		if ac["show_activity_timeline"] != true {
			t.Errorf("slack adapter_config.show_activity_timeline = %v, want true (streaming won't start otherwise)", ac["show_activity_timeline"])
		}
		if ac["activity_display_mode"] != "timeline" {
			t.Errorf("slack adapter_config.activity_display_mode = %v, want \"timeline\"", ac["activity_display_mode"])
		}
	})

	t.Run("absent when not set in values", func(t *testing.T) {
		ac := adapterConfigFrom(t, svc.buildSlackGatewayConfig(gw, map[string]any{}))
		if v, present := ac["show_activity_timeline"]; present {
			t.Errorf("show_activity_timeline should be absent when unset, got %v", v)
		}
		if v, present := ac["activity_display_mode"]; present {
			t.Errorf("activity_display_mode should be absent when unset, got %v", v)
		}
	})
}

func TestGatewayBuilders_HistoryLimitsPropagate(t *testing.T) {
	svc := NewYAMLService("test-ns", nil)
	gw := &store.Gateway{ID: "gw-hl-1", Name: "hl", Description: strPtr("history limit propagation fixture")}

	t.Run("slack carries slack_thread_history_limit + slack_channel_history_limit + slack_history_max_attachments into adapter_config", func(t *testing.T) {
		ac := adapterConfigFrom(t, svc.buildSlackGatewayConfig(gw, map[string]any{
			"slack_thread_history_limit":    float64(25),
			"slack_channel_history_limit":   float64(10),
			"slack_history_max_attachments": float64(2),
		}))
		if ac["slack_thread_history_limit"] != float64(25) {
			t.Errorf("slack adapter_config.slack_thread_history_limit = %v, want 25", ac["slack_thread_history_limit"])
		}
		if ac["slack_channel_history_limit"] != float64(10) {
			t.Errorf("slack adapter_config.slack_channel_history_limit = %v, want 10", ac["slack_channel_history_limit"])
		}
		if ac["slack_history_max_attachments"] != float64(2) {
			t.Errorf("slack adapter_config.slack_history_max_attachments = %v, want 2", ac["slack_history_max_attachments"])
		}
	})

	t.Run("explicit zero propagates (not dropped)", func(t *testing.T) {
		ac := adapterConfigFrom(t, svc.buildSlackGatewayConfig(gw, map[string]any{
			"slack_channel_history_limit":   float64(0),
			"slack_history_max_attachments": float64(0),
		}))
		if v, present := ac["slack_channel_history_limit"]; !present || v != float64(0) {
			t.Errorf("slack_channel_history_limit=0 should propagate as 0, got present=%v value=%v", present, v)
		}
		if v, present := ac["slack_history_max_attachments"]; !present || v != float64(0) {
			t.Errorf("slack_history_max_attachments=0 should propagate as 0, got present=%v value=%v", present, v)
		}
	})

	t.Run("absent when not set in values", func(t *testing.T) {
		ac := adapterConfigFrom(t, svc.buildSlackGatewayConfig(gw, map[string]any{}))
		if v, present := ac["slack_thread_history_limit"]; present {
			t.Errorf("slack_thread_history_limit should be absent when unset, got %v", v)
		}
		if v, present := ac["slack_channel_history_limit"]; present {
			t.Errorf("slack_channel_history_limit should be absent when unset, got %v", v)
		}
		if v, present := ac["slack_history_max_attachments"]; present {
			t.Errorf("slack_history_max_attachments should be absent when unset, got %v", v)
		}
	})
}

func TestGatewayBuilders_SlackMiscFieldsPropagate(t *testing.T) {
	svc := NewYAMLService("test-ns", nil)
	gw := &store.Gateway{ID: "gw-misc-1", Name: "misc", Description: strPtr("slack misc field propagation fixture")}

	t.Run("carries all three into adapter_config", func(t *testing.T) {
		ac := adapterConfigFrom(t, svc.buildSlackGatewayConfig(gw, map[string]any{
			"slack_initial_status_message":  "On it...",
			"correct_markdown_formatting":   false,
			"slack_email_cache_ttl_seconds": float64(1800),
		}))
		if ac["slack_initial_status_message"] != "On it..." {
			t.Errorf("slack adapter_config.slack_initial_status_message = %v, want \"On it...\"", ac["slack_initial_status_message"])
		}
		if ac["correct_markdown_formatting"] != false {
			t.Errorf("slack adapter_config.correct_markdown_formatting = %v, want false", ac["correct_markdown_formatting"])
		}
		if ac["slack_email_cache_ttl_seconds"] != float64(1800) {
			t.Errorf("slack adapter_config.slack_email_cache_ttl_seconds = %v, want 1800", ac["slack_email_cache_ttl_seconds"])
		}
	})

	t.Run("explicit false and zero propagate (not dropped)", func(t *testing.T) {
		ac := adapterConfigFrom(t, svc.buildSlackGatewayConfig(gw, map[string]any{
			"correct_markdown_formatting":   false,
			"slack_email_cache_ttl_seconds": float64(0),
		}))
		if v, present := ac["correct_markdown_formatting"]; !present || v != false {
			t.Errorf("correct_markdown_formatting=false should propagate as false, got present=%v value=%v", present, v)
		}
		if v, present := ac["slack_email_cache_ttl_seconds"]; !present || v != float64(0) {
			t.Errorf("slack_email_cache_ttl_seconds=0 should propagate as 0, got present=%v value=%v", present, v)
		}
	})

	t.Run("absent when not set in values", func(t *testing.T) {
		ac := adapterConfigFrom(t, svc.buildSlackGatewayConfig(gw, map[string]any{}))
		for _, key := range []string{"slack_initial_status_message", "correct_markdown_formatting", "slack_email_cache_ttl_seconds"} {
			if v, present := ac[key]; present {
				t.Errorf("%s should be absent when unset, got %v", key, v)
			}
		}
	})
}

func TestGatewayBuilders_RunAsPropagates(t *testing.T) {
	svc := NewYAMLService("test-ns", nil)
	gw := &store.Gateway{ID: "gw-runas-1", Name: "ra", Description: strPtr("run_as propagation fixture")}

	build := map[string]func(map[string]any) map[string]any{
		"slack": func(v map[string]any) map[string]any { return svc.buildSlackGatewayConfig(gw, v) },
		"teams": func(v map[string]any) map[string]any { return svc.buildTeamsGatewayConfig(gw, v) },
	}
	for name, b := range build {
		t.Run(name+": non-empty run_as threads", func(t *testing.T) {
			ac := adapterConfigFrom(t, b(map[string]any{"run_as": "channel-user"}))
			if ac["run_as"] != "channel-user" {
				t.Errorf("%s adapter_config.run_as = %v, want channel-user", name, ac["run_as"])
			}
		})
		t.Run(name+": EMPTY run_as threads (disable must reach the adapter)", func(t *testing.T) {
			ac := adapterConfigFrom(t, b(map[string]any{"run_as": ""}))
			v, present := ac["run_as"]
			if !present || v != "" {
				t.Errorf("%s adapter_config.run_as: present=%v val=%v, want present empty string (else the swap can't be disabled via platform)", name, present, v)
			}
		})
		t.Run(name+": absent when unset", func(t *testing.T) {
			ac := adapterConfigFrom(t, b(map[string]any{}))
			if v, present := ac["run_as"]; present {
				t.Errorf("%s run_as should be absent when unset, got %v", name, v)
			}
		})
	}
}

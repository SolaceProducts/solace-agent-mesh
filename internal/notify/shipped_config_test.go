// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package notify

import (
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
)

func shippedNotifyAppConfig(t *testing.T, env map[string]string) map[string]any {
	t.Helper()
	loader := &config.YAMLLoader{Lookup: func(name string) (string, bool) {
		v, ok := env[name]
		return v, ok
	}}
	cfg, err := loader.Load(t.Context(), "../../configs/gwe/platform.yaml")
	if err != nil {
		t.Fatal(err)
	}
	apps, _ := cfg.GetRaw("apps").([]any)
	for _, app := range apps {
		appMap, _ := app.(map[string]any)
		appConfig, _ := appMap["app_config"].(map[string]any)
		if _, ok := appConfig["notify"].(map[string]any); ok {
			return appConfig
		}
	}
	t.Fatal("platform.yaml has no notify block")
	return nil
}

func TestTheShippedPlatformConfigNotifyBlockParses(t *testing.T) {
	cfg, err := ParseNotifyConfig(shippedNotifyAppConfig(t, nil))
	if err != nil {
		t.Fatalf("with nothing set: %v", err)
	}
	if cfg.ConfirmBaseURL != "" {
		t.Errorf("with nothing set -> confirm_base_url %q, want empty", cfg.ConfirmBaseURL)
	}

	const base = "https://sam.example.com/#/notifications/confirm"
	cfg, err = ParseNotifyConfig(shippedNotifyAppConfig(t, map[string]string{"NOTIFY_CONFIRM_BASE_URL": base}))
	if err != nil {
		t.Fatalf("with NOTIFY_CONFIRM_BASE_URL set: %v", err)
	}
	if cfg.ConfirmBaseURL != base {
		t.Errorf("with NOTIFY_CONFIRM_BASE_URL set -> %q, want %q", cfg.ConfirmBaseURL, base)
	}

	_, err = ParseNotifyConfig(shippedNotifyAppConfig(t, map[string]string{"NOTIFY_CONFIRM_BASE_URL": "https://sam.example.com/notifications"}))
	if err == nil || !strings.Contains(err.Error(), "confirm_base_url") {
		t.Errorf("a base URL without the web UI hash route must fail at startup naming confirm_base_url, got %v", err)
	}
}

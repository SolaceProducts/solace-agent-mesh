// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package samapp

import (
	"bytes"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
)

func TestIsDevMode(t *testing.T) {
	tests := []struct {
		name string
		cfg  map[string]any
		want bool
	}{
		{"nil config", nil, true},
		{"empty config", map[string]any{}, true},
		{"dev_mode true bool", map[string]any{"dev_mode": true}, true},
		{"dev_mode false bool", map[string]any{"dev_mode": false}, false},
		{"dev_mode true string", map[string]any{"dev_mode": "true"}, true},
		{"dev_mode TRUE string", map[string]any{"dev_mode": "TRUE"}, true},
		{"dev_mode false string", map[string]any{"dev_mode": "false"}, false},
		{"no dev_mode with broker_url", map[string]any{"broker_url": "ws://host:8008"}, false},
		{"no dev_mode no broker_url", map[string]any{"broker_vpn": "default"}, true},
		{"dev_mode non-bool type", map[string]any{"dev_mode": 42}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsDevMode(tt.cfg); got != tt.want {
				t.Errorf("IsDevMode() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestFirstBrokerConfig(t *testing.T) {
	t.Run("returns first non-nil", func(t *testing.T) {
		entries := []AppEntry{
			{Name: "a", BrokerConfig: nil},
			{Name: "b", BrokerConfig: map[string]any{"dev_mode": true}},
			{Name: "c", BrokerConfig: map[string]any{"dev_mode": false}},
		}
		got := FirstBrokerConfig(entries)
		if got == nil || got["dev_mode"] != true {
			t.Errorf("expected first non-nil broker config")
		}
	})

	t.Run("returns nil when all nil", func(t *testing.T) {
		entries := []AppEntry{{Name: "a"}, {Name: "b"}}
		if got := FirstBrokerConfig(entries); got != nil {
			t.Errorf("expected nil, got %v", got)
		}
	})

	t.Run("returns nil for empty slice", func(t *testing.T) {
		if got := FirstBrokerConfig(nil); got != nil {
			t.Errorf("expected nil, got %v", got)
		}
	})
}

func TestExtractBrokerConfigFromRaw(t *testing.T) {
	t.Run("from apps list", func(t *testing.T) {
		cfg := config.FromMap(map[string]any{
			"apps": []any{
				map[string]any{
					"broker": map[string]any{"dev_mode": true, "broker_url": "ws://test:8008"},
				},
			},
		})
		got := ExtractBrokerConfigFromRaw(cfg)
		if got == nil || got["broker_url"] != "ws://test:8008" {
			t.Errorf("expected broker config from apps, got %v", got)
		}
	})

	t.Run("from top-level broker key", func(t *testing.T) {
		cfg := config.FromMap(map[string]any{
			"broker": map[string]any{"dev_mode": false, "broker_url": "ws://prod:8008"},
		})
		got := ExtractBrokerConfigFromRaw(cfg)
		if got == nil || got["broker_url"] != "ws://prod:8008" {
			t.Errorf("expected broker config from top-level, got %v", got)
		}
	})

	t.Run("returns nil when no broker", func(t *testing.T) {
		cfg := config.FromMap(map[string]any{"app_config": map[string]any{"worker_id": "test"}})
		if got := ExtractBrokerConfigFromRaw(cfg); got != nil {
			t.Errorf("expected nil, got %v", got)
		}
	})

	t.Run("apps takes precedence over top-level", func(t *testing.T) {
		cfg := config.FromMap(map[string]any{
			"apps": []any{
				map[string]any{
					"broker": map[string]any{"source": "apps"},
				},
			},
			"broker": map[string]any{"source": "top-level"},
		})
		got := ExtractBrokerConfigFromRaw(cfg)
		if got == nil || got["source"] != "apps" {
			t.Errorf("expected apps broker config to take precedence, got %v", got)
		}
	})
}

func TestCreateBrokerFromConfig_DevModeNoHost(t *testing.T) {
	t.Setenv("SAM_DEV_BROKER_HOST", "")
	t.Setenv("SAM_DEV_BROKER_PORT", "")

	_, err := CreateBrokerFromConfig(nil, nil, "test")
	if err == nil {
		t.Fatal("expected error for dev mode without dev broker host")
	}
}

func TestCreateBrokerFromConfig_ProdMissingURL(t *testing.T) {
	cfg := map[string]any{"dev_mode": false}
	_, err := CreateBrokerFromConfig(cfg, nil, "test")
	if err == nil {
		t.Fatal("expected error for prod mode without broker_url")
	}
}

func TestGetStringHelpers(t *testing.T) {
	m := map[string]any{"key": "value", "num": 42}

	if got := getString(m, "key"); got != "value" {
		t.Errorf("getString() = %q, want %q", got, "value")
	}
	if got := getString(m, "missing"); got != "" {
		t.Errorf("getString(missing) = %q, want empty", got)
	}
	if got := getString(m, "num"); got != "" {
		t.Errorf("getString(num) = %q, want empty (not a string)", got)
	}

	if got := getStringDefault(m, "key", "def"); got != "value" {
		t.Errorf("getStringDefault() = %q, want %q", got, "value")
	}
	if got := getStringDefault(m, "missing", "def"); got != "def" {
		t.Errorf("getStringDefault(missing) = %q, want %q", got, "def")
	}
}

func TestGetBoolAndInt(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	m := map[string]any{
		"b_true": true, "b_false": false, "s_true": "true", "s_TRUE": "TRUE", "s_garbage": "nope",
		"i": 42, "i64": int64(7), "f": float64(9), "s_num": "13", "s_bad": "x",
	}
	boolCases := map[string]bool{"b_true": true, "b_false": false, "s_true": true, "s_TRUE": true, "s_garbage": false, "missing": false}
	for k, want := range boolCases {
		if got := getBool(m, k); got != want {
			t.Errorf("getBool(%q) = %v, want %v", k, got, want)
		}
	}
	intCases := map[string]struct {
		want int
		ok   bool
	}{
		"i": {42, true}, "i64": {7, true}, "f": {9, true}, "s_num": {13, true},
		"s_bad": {0, false}, "missing": {0, false},
	}
	for k, want := range intCases {
		if got, ok := intField(m, k, logger); got != want.want || ok != want.ok {
			t.Errorf("intField(%q) = (%d, %v), want (%d, %v)", k, got, ok, want.want, want.ok)
		}
	}
}

func TestIntFieldWarnsOnUnparseable(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
	if _, ok := intField(map[string]any{"retry_count": "five"}, "retry_count", logger); ok {
		t.Error("want ok=false for unparseable value")
	}
	if !strings.Contains(buf.String(), "retry_count") {
		t.Errorf("expected warning naming the key, got %q", buf.String())
	}
	buf.Reset()
	if _, ok := intField(map[string]any{}, "retry_count", logger); ok {
		t.Error("want ok=false for absent value")
	}
	if buf.Len() != 0 {
		t.Errorf("absent value should not warn, got %q", buf.String())
	}
}

func TestApplyReconnectionConfig(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	t.Run("defaults to forever_retry, leaves retries/delay untouched", func(t *testing.T) {
		cfg := broker.SolaceBrokerConfig{ReconnectRetries: 60, ReconnectDelay: 5 * time.Second}
		applyReconnectionConfig(&cfg, map[string]any{}, logger)
		if !cfg.ReconnectForever {
			t.Error("want ReconnectForever true by default (Python parity)")
		}
		if cfg.ReconnectRetries != 60 || cfg.ReconnectDelay != 5*time.Second {
			t.Errorf("defaults clobbered: retries=%d delay=%v", cfg.ReconnectRetries, cfg.ReconnectDelay)
		}
	})

	t.Run("parametrized_retry disables forever and honors count/interval", func(t *testing.T) {
		cfg := broker.SolaceBrokerConfig{ReconnectRetries: 60, ReconnectDelay: 5 * time.Second}
		applyReconnectionConfig(&cfg, map[string]any{
			"reconnection_strategy": "parametrized_retry",
			"retry_count":           10,
			"retry_interval":        3000,
		}, logger)
		if cfg.ReconnectForever {
			t.Error("want ReconnectForever false for parametrized_retry")
		}
		if cfg.ReconnectRetries != 10 {
			t.Errorf("ReconnectRetries = %d, want 10", cfg.ReconnectRetries)
		}
		if cfg.ReconnectDelay != 3*time.Second {
			t.Errorf("ReconnectDelay = %v, want 3s (3000ms)", cfg.ReconnectDelay)
		}
	})

	for _, strategy := range []string{"parameterized_retry", "PARAMETRIZED_RETRY", " parametrized_retry ", "bogus"} {
		t.Run("non-exact spelling falls back to forever: "+strategy, func(t *testing.T) {
			cfg := broker.SolaceBrokerConfig{ReconnectRetries: 60, ReconnectDelay: 5 * time.Second}
			applyReconnectionConfig(&cfg, map[string]any{"reconnection_strategy": strategy}, logger)
			if !cfg.ReconnectForever {
				t.Errorf("strategy %q: want forever fallback (Python parity)", strategy)
			}
		})
	}

	t.Run("retry_interval as quoted string", func(t *testing.T) {
		cfg := broker.SolaceBrokerConfig{}
		applyReconnectionConfig(&cfg, map[string]any{"retry_interval": "10000"}, logger)
		if cfg.ReconnectDelay != 10*time.Second {
			t.Errorf("ReconnectDelay = %v, want 10s", cfg.ReconnectDelay)
		}
	})

	t.Run("retry_count under forever_retry is ignored and warned", func(t *testing.T) {
		var buf bytes.Buffer
		warnLogger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
		cfg := broker.SolaceBrokerConfig{ReconnectRetries: 60}
		applyReconnectionConfig(&cfg, map[string]any{"reconnection_strategy": "forever_retry", "retry_count": 5}, warnLogger)
		if cfg.ReconnectRetries != 60 {
			t.Errorf("retry_count should not bind under forever_retry: ReconnectRetries = %d, want 60", cfg.ReconnectRetries)
		}
		if !strings.Contains(buf.String(), "retry_count is ignored under reconnection_strategy=forever_retry") {
			t.Errorf("expected forever-retry warning, got %q", buf.String())
		}
	})
}

func TestResolveBrokerConfig(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	t.Run("prefers first app broker over top-level", func(t *testing.T) {
		entries := []AppEntry{{Name: "a", BrokerConfig: map[string]any{"broker_url": "ws://app:8008"}}}
		cfg := config.FromMap(map[string]any{"broker": map[string]any{"broker_url": "ws://top:8008"}})
		got := ResolveBrokerConfig(entries, cfg, logger)
		if got == nil || got["broker_url"] != "ws://app:8008" {
			t.Errorf("expected app broker, got %v", got)
		}
	})
	t.Run("falls back to top-level broker when no app declares one", func(t *testing.T) {
		entries := []AppEntry{{Name: "a"}}
		cfg := config.FromMap(map[string]any{"broker": map[string]any{"broker_url": "ws://top:8008"}})
		got := ResolveBrokerConfig(entries, cfg, logger)
		if got == nil || got["broker_url"] != "ws://top:8008" {
			t.Errorf("expected top-level broker fallback, got %v", got)
		}
	})
	t.Run("nil when neither app nor top-level broker present", func(t *testing.T) {
		if got := ResolveBrokerConfig(nil, config.FromMap(map[string]any{}), logger); got != nil {
			t.Errorf("expected nil, got %v", got)
		}
	})
	t.Run("warns when a top-level broker block is shadowed by a per-app block", func(t *testing.T) {
		var buf bytes.Buffer
		warnLogger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
		entries := []AppEntry{{Name: "a", BrokerConfig: map[string]any{"broker_url": "ws://app:8008"}}}
		cfg := config.FromMap(map[string]any{"broker": map[string]any{"reconnection_strategy": "parametrized_retry"}})
		ResolveBrokerConfig(entries, cfg, warnLogger)
		if !strings.Contains(buf.String(), "top-level broker: block ignored") {
			t.Errorf("expected shadowing warning, got %q", buf.String())
		}
	})
	t.Run("no warning when only a per-app block exists", func(t *testing.T) {
		var buf bytes.Buffer
		warnLogger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
		entries := []AppEntry{{Name: "a", BrokerConfig: map[string]any{"broker_url": "ws://app:8008"}}}
		ResolveBrokerConfig(entries, config.FromMap(map[string]any{}), warnLogger)
		if buf.Len() != 0 {
			t.Errorf("expected no warning, got %q", buf.String())
		}
	})
}

// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"context"
	"testing"
)

func TestParseGatewayConfig_CapabilitiesFromYAML(t *testing.T) {
	cfg, err := ParseGatewayConfig(map[string]any{
		"namespace":  "test-ns",
		"gateway_id": "test-gw",
		"gateway_capabilities": map[string]any{
			"interactive_plan_verification": false,
			"some_future_capability":        true,
			"not_a_bool":                    "skipped",
		},
	})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if cfg.Capabilities == nil {
		t.Fatal("Capabilities is nil; expected populated map")
	}
	if cfg.Capabilities["interactive_plan_verification"] {
		t.Errorf("interactive_plan_verification expected false, got true")
	}
	if !cfg.Capabilities["some_future_capability"] {
		t.Errorf("some_future_capability expected true, got false")
	}
	if _, ok := cfg.Capabilities["not_a_bool"]; ok {
		t.Errorf("non-bool entry should not be propagated")
	}
}

func TestParseGatewayConfig_CapabilitiesAbsentLeavesNil(t *testing.T) {
	cfg, err := ParseGatewayConfig(map[string]any{
		"namespace":  "test-ns",
		"gateway_id": "test-gw",
	})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if cfg.Capabilities != nil {
		t.Errorf("expected nil Capabilities when YAML omits the section, got %#v", cfg.Capabilities)
	}
}

type initCapsAdapter struct {
	LoggingAdapter
	canPrompt bool
}

func (a *initCapsAdapter) Init(ctx context.Context, core *GatewayCore) error {
	a.canPrompt = true
	return a.LoggingAdapter.Init(ctx, core)
}

func (a *initCapsAdapter) Capabilities() map[string]bool {
	return map[string]bool{"interactive_user_input": a.canPrompt, "web_ui_redirect": true}
}

func TestGatewayInit_MergesCapabilitiesTheAdapterKnowsAfterInit(t *testing.T) {
	core := NewGatewayCore(&initCapsAdapter{}, map[string]any{
		"namespace":            "test-ns",
		"gateway_id":           "test-gw",
		"gateway_capabilities": map[string]any{"web_ui_redirect": false},
	})
	if err := core.Init(t.Context(), nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	caps := core.Config().Capabilities
	if !caps["interactive_user_input"] {
		t.Error("interactive_user_input = false, want the value the adapter has after Init")
	}
	if caps["web_ui_redirect"] {
		t.Error("web_ui_redirect = true, want the config's false to win")
	}
}

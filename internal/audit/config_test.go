// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package audit

import (
	"testing"
)

func TestConfig_DefaultIsEnabled(t *testing.T) {
	t.Setenv("SAM_AUDIT_LOG", "")
	c := Config{}
	if !c.IsEnabled() {
		t.Error("expected default config to be enabled")
	}
}

func TestConfig_ExplicitEnabled(t *testing.T) {
	t.Setenv("SAM_AUDIT_LOG", "")
	enabled := true
	c := Config{Enabled: &enabled}
	if !c.IsEnabled() {
		t.Error("expected explicitly enabled config to be enabled")
	}
}

func TestConfig_ExplicitDisabled(t *testing.T) {
	t.Setenv("SAM_AUDIT_LOG", "")
	disabled := false
	c := Config{Enabled: &disabled}
	if c.IsEnabled() {
		t.Error("expected explicitly disabled config to be disabled")
	}
}

func TestConfig_EnvVarOverridesYAML_DisableFalse(t *testing.T) {
	t.Setenv("SAM_AUDIT_LOG", "false")

	enabled := true
	c := Config{Enabled: &enabled}
	if c.IsEnabled() {
		t.Error("expected SAM_AUDIT_LOG=false to override YAML enabled=true")
	}
}

func TestConfig_EnvVarOverridesYAML_DisableZero(t *testing.T) {
	t.Setenv("SAM_AUDIT_LOG", "0")

	c := Config{}
	if c.IsEnabled() {
		t.Error("expected SAM_AUDIT_LOG=0 to disable")
	}
}

func TestConfig_EnvVarOverridesYAML_EnableTrue(t *testing.T) {
	t.Setenv("SAM_AUDIT_LOG", "true")

	disabled := false
	c := Config{Enabled: &disabled}
	if !c.IsEnabled() {
		t.Error("expected SAM_AUDIT_LOG=true to override YAML enabled=false")
	}
}

func TestConfig_EnvVarCaseInsensitive(t *testing.T) {
	t.Setenv("SAM_AUDIT_LOG", "FALSE")

	c := Config{}
	if c.IsEnabled() {
		t.Error("expected SAM_AUDIT_LOG=FALSE (uppercase) to disable")
	}
}

func TestDefaultConfig(t *testing.T) {
	c := DefaultConfig()
	if c.Enabled == nil {
		t.Fatal("expected Enabled to be non-nil")
	}
	if !*c.Enabled {
		t.Error("expected DefaultConfig to be enabled")
	}
}

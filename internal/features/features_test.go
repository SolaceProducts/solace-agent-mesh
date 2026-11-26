// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package features

import (
	"regexp"
	"slices"
	"testing"
)

func TestIsEnabled_Default(t *testing.T) {
	r := NewRegistry()
	r.Register(
		Definition{Key: "enabled_flag", Default: true},
		Definition{Key: "disabled_flag", Default: false},
	)

	if !r.IsEnabled("enabled_flag") {
		t.Error("enabled_flag should be enabled by default")
	}
	if r.IsEnabled("disabled_flag") {
		t.Error("disabled_flag should be disabled by default")
	}
}

func TestIsEnabled_UnknownKey(t *testing.T) {
	r := NewRegistry()
	if r.IsEnabled("nonexistent") {
		t.Error("unknown key should return false")
	}
}

func TestIsEnabled_UnknownKeyIgnoresEnvVar(t *testing.T) {
	r := NewRegistry()
	t.Setenv("SAM_FEATURE_PHANTOM", "true")
	if r.IsEnabled("phantom") {
		t.Error("unregistered key must return false even when env var is set")
	}
}

func TestIsEnabled_EnvVarOverride(t *testing.T) {
	r := NewRegistry()
	r.Register(Definition{Key: "my_feature", Default: false})

	t.Setenv("SAM_FEATURE_MY_FEATURE", "true")
	if !r.IsEnabled("my_feature") {
		t.Error("env var should override default to enabled")
	}

	r.Register(Definition{Key: "other_feature", Default: true})
	t.Setenv("SAM_FEATURE_OTHER_FEATURE", "false")
	if r.IsEnabled("other_feature") {
		t.Error("env var should override default to disabled")
	}
}

func TestIsEnabled_EnvVarCaseInsensitive(t *testing.T) {
	r := NewRegistry()
	r.Register(Definition{Key: "case_test", Default: false})

	t.Setenv("SAM_FEATURE_CASE_TEST", "True")
	if !r.IsEnabled("case_test") {
		t.Error("env var value should be case-insensitive")
	}
}

func TestIsEnabled_EnvVarOne(t *testing.T) {
	r := NewRegistry()
	r.Register(Definition{Key: "one_test", Default: false})

	t.Setenv("SAM_FEATURE_ONE_TEST", "1")
	if !r.IsEnabled("one_test") {
		t.Error("env var value of '1' should be truthy")
	}
}

func TestIsEnabled_HyphenatedKey(t *testing.T) {
	r := NewRegistry()
	r.Register(Definition{Key: "my-feature", Default: false})

	t.Setenv("SAM_FEATURE_MY_FEATURE", "true")
	if !r.IsEnabled("my-feature") {
		t.Error("hyphenated keys should convert to underscore for env var lookup")
	}
}

func TestLoadFromYAML(t *testing.T) {
	r := NewRegistry()
	yaml := []byte(`
features:
  - key: flag_one
    name: Flag One
    release_phase: beta
    default: true
    description: "First flag"
  - key: flag_two
    name: Flag Two
    release_phase: early_access
    default: false
`)
	if err := r.LoadFromYAML(yaml); err != nil {
		t.Fatalf("LoadFromYAML: %v", err)
	}

	if !r.IsEnabled("flag_one") {
		t.Error("flag_one should be enabled")
	}
	if r.IsEnabled("flag_two") {
		t.Error("flag_two should be disabled")
	}

	def, ok := r.Get("flag_one")
	if !ok {
		t.Fatal("flag_one not found")
	}
	if def.Name != "Flag One" {
		t.Errorf("Name = %q, want Flag One", def.Name)
	}
	if def.ReleasePhase != PhaseBeta {
		t.Errorf("ReleasePhase = %q, want beta", def.ReleasePhase)
	}
}

func TestLoadFromYAML_OverwritesExisting(t *testing.T) {
	r := NewRegistry()
	r.Register(Definition{Key: "my_flag", Default: false})

	yaml := []byte(`
features:
  - key: my_flag
    name: Updated
    default: true
`)
	if err := r.LoadFromYAML(yaml); err != nil {
		t.Fatal(err)
	}
	if !r.IsEnabled("my_flag") {
		t.Error("later load should overwrite earlier registration")
	}
}

func TestLoadFromYAML_InvalidYAML(t *testing.T) {
	r := NewRegistry()
	if err := r.LoadFromYAML([]byte("not: [valid: yaml")); err == nil {
		t.Error("expected error for invalid YAML")
	}
}

func TestAll(t *testing.T) {
	r := NewRegistry()
	r.Register(
		Definition{Key: "a"},
		Definition{Key: "b"},
		Definition{Key: "c"},
	)

	all := r.All()
	if len(all) != 3 {
		t.Errorf("All() returned %d items, want 3", len(all))
	}
}

func TestGet_NotFound(t *testing.T) {
	r := NewRegistry()
	_, ok := r.Get("missing")
	if ok {
		t.Error("Get should return false for missing key")
	}
}

func TestLoadFlags(t *testing.T) {
	r := NewRegistry()
	if err := LoadFlags(r); err != nil {
		t.Fatalf("LoadFlags: %v", err)
	}
	all := r.All()
	declared := declaredFlagKeys(t)
	loaded := map[string]bool{}
	for _, d := range all {
		loaded[d.Key] = true
	}
	for key := range declared {
		if !loaded[key] {
			t.Errorf("features.yaml declares %q but the registry did not load it", key)
		}
	}
	for key := range loaded {
		if !declared[key] {
			t.Errorf("registry loaded %q, which is not declared in features.yaml", key)
		}
	}
	if d, ok := r.Get("builder"); !ok {
		t.Error("builder flag missing from catalog")
	} else if !d.Default {
		t.Errorf("builder: want Default=true in merged catalog, got false")
	}
	if !r.IsEnabled("builder") {
		t.Error("builder should be enabled by default in the merged catalog")
	}
	if d, ok := r.Get("builder_server_apply"); !ok {
		t.Error("builder_server_apply flag missing from catalog")
	} else if !d.Default {
		t.Error("builder_server_apply: want Default=true, Quick Build saves through the build endpoint unless a deployment opts out")
	}
	if !r.IsEnabled("scheduler") {
		t.Error("scheduler should be enabled by default")
	}
}

var exportedKeyConsts = map[string]string{
	"KeyObservabilityAgent":                   KeyObservabilityAgent,
	"KeyDecisionMediaInput":                   KeyDecisionMediaInput,
	"KeyNotifyService":                        KeyNotifyService,
	"KeyEvidenceStore":                        KeyEvidenceStore,
	"KeyEvidenceCoderProposals":               KeyEvidenceCoderProposals,
	"KeyActivityMonitor":                      KeyActivityMonitor,
	"KeyEventCatalog":                         KeyEventCatalog,
	"KeyWorkflowToolValidationPermissive":     KeyWorkflowToolValidationPermissive,
	"KeyBuilderConnectorNodeChecksPermissive": KeyBuilderConnectorNodeChecksPermissive,
	"KeyDurableTaskResume":                    KeyDurableTaskResume,
	"KeyFeedbackManagement":                   KeyFeedbackManagement,
	"KeyMachineTokens":                        KeyMachineTokens,
	"KeyEvalDecisionEvaluator":                KeyEvalDecisionEvaluator,
	"KeyLegacyPythonModelBootstrap":           KeyLegacyPythonModelBootstrap,
	"KeyToolSearch":                           KeyToolSearch,
	"KeyIsolatedToolExecution":                KeyIsolatedToolExecution,
	"KeyToolChatModel":                        KeyToolChatModel,
	"KeyQuietScheduledRuns":                   KeyQuietScheduledRuns,
	"KeyScheduledTaskRunAs":                   KeyScheduledTaskRunAs,
	"KeySystemUserToolAuth":                   KeySystemUserToolAuth,
}

func TestExportedKeysResolveInRegistry(t *testing.T) {
	r := NewRegistry()
	if err := LoadFlags(r); err != nil {
		t.Fatalf("LoadFlags: %v", err)
	}
	if d, ok := r.Get(KeyMachineTokens); !ok || !d.Default {
		t.Errorf("%s must be in the catalog and default to on", KeyMachineTokens)
	}
	if d, ok := r.Get(KeyLegacyPythonModelBootstrap); !ok || d.Default {
		t.Errorf("%s must be in the catalog and default to off", KeyLegacyPythonModelBootstrap)
	}
	if d, ok := r.Get(KeySystemUserToolAuth); !ok || d.Default {
		t.Errorf("%s must be in the catalog and default to off", KeySystemUserToolAuth)
	}
	if d, ok := r.Get(KeyQuietScheduledRuns); !ok || d.Default {
		t.Errorf("%s must be in the catalog and default to off, so every scheduled run ends as before until it is turned on", KeyQuietScheduledRuns)
	}
	for name, key := range exportedKeyConsts {
		if _, ok := r.Get(key); !ok {
			t.Errorf("%s = %q does not resolve via registry.Get after LoadFlags — typo, or features.yaml entry missing/renamed", name, key)
		}
	}
}

func TestHasEnvOverride(t *testing.T) {
	r := NewRegistry()
	r.Register(Definition{Key: "test_flag", Default: false})

	if r.HasEnvOverride("test_flag") {
		t.Error("HasEnvOverride should return false when env var is not set")
	}

	t.Setenv("SAM_FEATURE_TEST_FLAG", "true")
	if !r.HasEnvOverride("test_flag") {
		t.Error("HasEnvOverride should return true when env var is set")
	}
}

func TestHasEnvOverride_HyphenatedKey(t *testing.T) {
	r := NewRegistry()
	r.Register(Definition{Key: "my-flag", Default: false})

	t.Setenv("SAM_FEATURE_MY_FLAG", "false")
	if !r.HasEnvOverride("my-flag") {
		t.Error("HasEnvOverride should convert hyphens to underscores for env var lookup")
	}
}

func TestUnknownEnvOverrides_DetectsUnregistered(t *testing.T) {
	r := NewRegistry()
	r.Register(Definition{Key: "real_flag", Default: false})

	t.Setenv("SAM_FEATURE_REAL_FLAG", "true")
	t.Setenv("SAM_FEATURE_PHANTOM", "true")
	t.Setenv("SAM_FEATURE_TYPO_FLAG", "false")
	t.Setenv("UNRELATED_VAR", "value")

	got := r.UnknownEnvOverrides()
	want := []string{"SAM_FEATURE_PHANTOM", "SAM_FEATURE_TYPO_FLAG"}

	filtered := make([]string, 0, len(got))
	for _, name := range got {
		if name == "SAM_FEATURE_PHANTOM" || name == "SAM_FEATURE_TYPO_FLAG" {
			filtered = append(filtered, name)
		}
	}
	if !slices.Equal(filtered, want) {
		t.Errorf("UnknownEnvOverrides = %v, want subset matching %v", got, want)
	}
}

func TestUnknownEnvOverrides_HyphenatedRegisteredKey(t *testing.T) {
	r := NewRegistry()
	r.Register(Definition{Key: "my-flag", Default: false})

	t.Setenv("SAM_FEATURE_MY_FLAG", "true")
	for _, name := range r.UnknownEnvOverrides() {
		if name == "SAM_FEATURE_MY_FLAG" {
			t.Error("env var matching a hyphenated registered key must not be flagged as unknown")
		}
	}
}

func declaredFlagKeys(t *testing.T) map[string]bool {
	t.Helper()
	re := regexp.MustCompile(`(?m)^\s*-\s+key:\s*"?([a-z0-9_]+)"?\s*$`)
	matches := re.FindAllSubmatch(featuresYAML, -1)
	if len(matches) == 0 {
		t.Fatal("no `- key:` entries found in features.yaml — the scan pattern is broken, not the catalog")
	}
	keys := make(map[string]bool, len(matches))
	for _, m := range matches {
		keys[string(m[1])] = true
	}
	return keys
}

func TestGenerallyAvailableFlagsShipOn(t *testing.T) {
	r := NewRegistry()
	if err := LoadFlags(r); err != nil {
		t.Fatalf("LoadFlags: %v", err)
	}
	for _, key := range []string{KeyFeedbackManagement} {
		t.Run(key, func(t *testing.T) {
			d, ok := r.Get(key)
			if !ok {
				t.Fatalf("%s missing from the catalog", key)
			}
			if d.ReleasePhase != PhaseGeneralAvailability {
				t.Errorf("ReleasePhase = %q, want %q", d.ReleasePhase, PhaseGeneralAvailability)
			}
			if !d.Default {
				t.Errorf("Default = false, want true")
			}
		})
	}
}

func TestNotifyServiceIsOnByDefaultInTheShippedCatalog(t *testing.T) {
	r := NewRegistry()
	if err := LoadFlags(r); err != nil {
		t.Fatalf("LoadFlags: %v", err)
	}

	t.Setenv("SAM_FEATURE_NOTIFY_SERVICE", "")
	if !r.IsEnabled(KeyNotifyService) {
		t.Errorf("%s is off with no override; the notification service would not mount by default", KeyNotifyService)
	}

	t.Setenv("SAM_FEATURE_NOTIFY_SERVICE", "false")
	if r.IsEnabled(KeyNotifyService) {
		t.Errorf("%s is on with SAM_FEATURE_NOTIFY_SERVICE=false", KeyNotifyService)
	}
}

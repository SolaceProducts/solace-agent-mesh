// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"strings"
	"testing"

	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

var everyEntrypointType = []string{
	"httpsse", "slack", "teams", "email", "whatsapp", "event_mesh", "mcp", "webhook", "google_chat",
}

var entrypointTypesThatSubmitNoTasks = []string{"agent_link"}

func appConfigFor(t *testing.T, gwType string, values map[string]any) map[string]any {
	t.Helper()
	t.Setenv("OBJECT_STORAGE_TYPE", "")
	t.Setenv("S3_BUCKET_NAME", "")

	raw, err := json.Marshal(values)
	if err != nil {
		t.Fatalf("marshal values: %v", err)
	}
	stored := string(raw)

	svc := NewYAMLService("test-ns", slog.Default())
	cfg, err := svc.BuildGatewayConfig(&store.Gateway{
		ID:          "ac-gw",
		Name:        "Assistant Context GW",
		Description: strPtr("assistant-context fixture"),
		Type:        gwType,
		Values:      &stored,
	})
	if err != nil {
		t.Fatalf("BuildGatewayConfig(%s): %v", gwType, err)
	}
	apps, ok := cfg["apps"].([]map[string]any)
	if !ok || len(apps) == 0 {
		t.Fatalf("%s: apps[] missing or wrong shape: %T", gwType, cfg["apps"])
	}
	appCfg, ok := apps[0]["app_config"].(map[string]any)
	if !ok {
		t.Fatalf("%s: app_config missing or wrong shape: %T", gwType, apps[0]["app_config"])
	}
	return appCfg
}

func TestBuildGatewayConfig_AssistantContextReachesEveryEntrypointRuntime(t *testing.T) {
	for _, gwType := range everyEntrypointType {
		t.Run(gwType, func(t *testing.T) {
			appCfg := appConfigFor(t, gwType, map[string]any{
				"systemPurpose":  "Answer support questions about billing.",
				"responseFormat": "Two sentences, no markdown.",
			})

			runtimeCfg, err := entrypoint.ParseGatewayConfig(appCfg)
			if err != nil {
				t.Fatalf("ParseGatewayConfig: %v", err)
			}
			if got, want := runtimeCfg.SystemPurpose, "Answer support questions about billing."; got != want {
				t.Errorf("SystemPurpose = %q, want %q", got, want)
			}
			if got, want := runtimeCfg.ResponseFormat, "Two sentences, no markdown."; got != want {
				t.Errorf("ResponseFormat = %q, want %q", got, want)
			}
		})
	}
}

func TestBuildGatewayConfig_AssistantContextAcceptsSnakeCase(t *testing.T) {
	for _, gwType := range everyEntrypointType {
		t.Run(gwType, func(t *testing.T) {
			appCfg := appConfigFor(t, gwType, map[string]any{
				"system_purpose":  "legacy purpose",
				"response_format": "legacy format",
			})
			runtimeCfg, err := entrypoint.ParseGatewayConfig(appCfg)
			if err != nil {
				t.Fatalf("ParseGatewayConfig: %v", err)
			}
			if got := runtimeCfg.SystemPurpose; got != "legacy purpose" {
				t.Errorf("SystemPurpose = %q, want %q", got, "legacy purpose")
			}
			if got := runtimeCfg.ResponseFormat; got != "legacy format" {
				t.Errorf("ResponseFormat = %q, want %q", got, "legacy format")
			}
		})
	}
}

func TestApplyAssistantContext_FirstPresentAliasWins(t *testing.T) {
	for name, tc := range map[string]struct {
		values map[string]any
		want   map[string]any
	}{
		"camelCase wins when both are set": {
			values: map[string]any{
				"systemPurpose": "canonical", "system_purpose": "legacy",
				"responseFormat": "canonical format", "response_format": "legacy format",
			},
			want: map[string]any{"system_purpose": "canonical", "response_format": "canonical format"},
		},
		"blank camelCase clears a stored snake_case value": {
			values: map[string]any{
				"systemPurpose": "", "system_purpose": "You are Acme internal IT.",
				"responseFormat": "", "response_format": "Terse.",
			},
			want: map[string]any{},
		},
		"whitespace-only camelCase also clears": {
			values: map[string]any{"systemPurpose": "   ", "system_purpose": "stored"},
			want:   map[string]any{},
		},
		"snake_case still applies when camelCase is absent": {
			values: map[string]any{"system_purpose": "legacy"},
			want:   map[string]any{"system_purpose": "legacy"},
		},
		"null camelCase leaves a stored snake_case value alone": {
			values: map[string]any{"systemPurpose": nil, "system_purpose": "legacy"},
			want:   map[string]any{"system_purpose": "legacy"},
		},
		"empty-map camelCase leaves a stored snake_case value alone": {
			values: map[string]any{"systemPurpose": map[string]any{}, "system_purpose": "legacy"},
			want:   map[string]any{"system_purpose": "legacy"},
		},
		"empty-list camelCase leaves a stored snake_case value alone": {
			values: map[string]any{"systemPurpose": []any{}, "system_purpose": "legacy"},
			want:   map[string]any{"system_purpose": "legacy"},
		},
		"numeric camelCase leaves a stored snake_case value alone": {
			values: map[string]any{"systemPurpose": 42, "system_purpose": "legacy"},
			want:   map[string]any{"system_purpose": "legacy"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			appCfg := map[string]any{}
			applyAssistantContext(appCfg, tc.values)
			if !maps.Equal(appCfg, tc.want) {
				t.Errorf("app_config = %v, want %v", appCfg, tc.want)
			}
		})
	}
}

func TestApplyAssistantContext_BlankAndAbsentEmitNothing(t *testing.T) {
	for name, values := range map[string]map[string]any{
		"absent":     {},
		"empty":      {"systemPurpose": "", "responseFormat": ""},
		"whitespace": {"systemPurpose": "   ", "responseFormat": "\t\n"},
		"wrong type": {"systemPurpose": 42, "responseFormat": true},
	} {
		t.Run(name, func(t *testing.T) {
			appCfg := map[string]any{}
			applyAssistantContext(appCfg, values)
			if len(appCfg) != 0 {
				t.Errorf("app_config = %v, want no assistant-context keys", appCfg)
			}
		})
	}
}

func TestBuildGatewayConfig_AssistantContextCoversEveryType(t *testing.T) {
	svc := NewYAMLService("test-ns", slog.Default())
	empty := "{}"

	candidates := []string{"httpsse"}
	for gwType := range validGatewayTypes(getAllGatewaySchemas()) {
		candidates = append(candidates, gwType)
	}
	for _, def := range platpkg.AllGatewayTypeDefs() {
		candidates = append(candidates, def.Type)
	}

	buildable := map[string]bool{}
	for _, gwType := range candidates {
		if _, err := svc.BuildGatewayConfig(&store.Gateway{
			ID: "coverage", Name: "coverage", Type: gwType, Values: &empty,
		}); err == nil {
			buildable[gwType] = true
		}
	}

	covered := map[string]bool{}
	for _, gwType := range entrypointTypesThatSubmitNoTasks {
		covered[gwType] = true
	}
	for _, gwType := range everyEntrypointType {
		covered[gwType] = true
		if !buildable[gwType] {
			t.Errorf("everyEntrypointType lists %q, but BuildGatewayConfig cannot build it", gwType)
		}
	}
	for gwType := range buildable {
		if !covered[gwType] {
			t.Errorf("entrypoint type %q builds a runtime config but is missing from everyEntrypointType; "+
				"add it so the assistant-context guards cover it", gwType)
		}
	}
}

func TestAssistantContext_ChangesNoValidatorVerdict(t *testing.T) {
	for gwType, values := range validValueBags() {
		t.Run(gwType, func(t *testing.T) {
			if base := validateGatewayValues(gwType, maps.Clone(values)); base != nil {
				t.Fatalf("fixture is not valid, so the registry walk never reaches the appended pair: %s",
					describeValidationError(base))
			}

			with := maps.Clone(values)
			with["systemPurpose"] = "purpose"
			with["responseFormat"] = "format"

			if err := validateGatewayValues(gwType, with); err != nil {
				t.Errorf("valid bag rejected once the assistant-context keys are present: %s", describeValidationError(err))
			}
		})
	}
}

func TestAssistantContext_NonStringRejectedWhereTheRegistryValidates(t *testing.T) {
	registryValidated := registryValidatedTypes(t)

	for gwType, values := range validValueBags() {
		t.Run(gwType, func(t *testing.T) {
			with := maps.Clone(values)
			with["systemPurpose"] = 42

			if base := validateGatewayValues(gwType, maps.Clone(values)); base != nil {
				t.Fatalf("fixture is not valid, so any rejection below may be unrelated: %s", describeValidationError(base))
			}

			err := validateGatewayValues(gwType, with)
			if registryValidated[gwType] {
				if err == nil {
					t.Error("non-string systemPurpose accepted; the registry walk should reject it")
				}
				return
			}
			if err != nil {
				t.Errorf("hand-maintained type gained validation unexpectedly: %s", describeValidationError(err))
			}
		})
	}
}

func TestAssistantContext_ExceedingTheLengthCapIsRejected(t *testing.T) {
	registryValidated := registryValidatedTypes(t)

	for gwType, values := range validValueBags() {
		if !registryValidated[gwType] {
			continue
		}
		for _, field := range []string{"systemPurpose", "responseFormat"} {
			t.Run(gwType+"/"+field, func(t *testing.T) {
				with := maps.Clone(values)
				with[field] = strings.Repeat("x", platpkg.MaxPromptFieldLen+1)
				if err := validateGatewayValues(gwType, with); err == nil {
					t.Errorf("a %s of %d chars was accepted; cap is %d",
						field, platpkg.MaxPromptFieldLen+1, platpkg.MaxPromptFieldLen)
				}
			})
		}
	}
}

func TestAssistantContext_RuleLevelResponseFormatIsUncapped(t *testing.T) {
	values := validValueBags()["event_mesh"]
	rules, _ := values["event_rules"].([]any)
	if len(rules) == 0 {
		t.Fatal("event_mesh fixture has no rule to put a response format on")
	}

	rule := maps.Clone(rules[0].(map[string]any))
	rule["responseFormat"] = strings.Repeat("x", platpkg.MaxPromptFieldLen*2)
	with := maps.Clone(values)
	with["event_rules"] = []any{rule}

	if err := validateGatewayValues("event_mesh", with); err != nil {
		t.Errorf("an oversized rule-level responseFormat was rejected: %s\n"+
			"Capping it breaks editing every existing entrypoint that stores a longer value.",
			describeValidationError(err))
	}
}

func TestAssistantContext_FixturesCoverEveryType(t *testing.T) {
	bags := validValueBags()
	for _, gwType := range everyEntrypointType {
		if gwType == "httpsse" {
			continue
		}
		if _, ok := bags[gwType]; !ok {
			t.Errorf("no valid value bag for entrypoint type %q; every validator-backed type needs one "+
				"or it drops out of the assistant-context validation tests without failing them", gwType)
		}
	}
}

func registryValidatedTypes(t *testing.T) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	for _, def := range platpkg.AllGatewayTypeDefs() {
		out[def.Type] = true
	}
	return out
}

func validValueBags() map[string]map[string]any {
	return map[string]map[string]any{
		"slack": {"bot_token": "xoxb-abc", "app_token": "xapp-abc"},
		"teams": {
			"microsoft_app_id":       "12345678-1234-1234-1234-123456789abc",
			"microsoft_app_password": "pw",
		},
		"whatsapp": {"phone_number_id": "1234567890", "access_token": "t", "app_secret": "s", "verify_token": "v"},
		"email": {
			"mailbox": "a@b.com", "imap_host": "h", "smtp_host": "h",
			"auth_mode": "basic", "imap_password": "p", "trusted_authserv_ids": "mx.google.com",
		},
		"webhook": {
			"routePath": "/x", "method": "POST", "authMode": "token", "authToken": "abcdefgh",
			"authTokenLocation": "bearer", "targetType": "agent", "targetAgentName": "A",
			"inputExpression": "input.payload",
		},
		"mcp":         {"enableAuth": "false"},
		"google_chat": {"delivery": "pubsub", "pubsub_subscription": "projects/p/subscriptions/s"},
		"event_mesh": {"event_rules": []any{
			map[string]any{
				"name":           "default_rule",
				"subscriptions":  []any{map[string]any{"topic": "events/>"}},
				"targetAgent":    "agent1",
				"promptTemplate": "Process {{message}}",
			},
		}},
	}
}

func describeValidationError(err error) string {
	var ve *ValidationError
	if errors.As(err, &ve) {
		return fmt.Sprintf("%s details=%v", ve.Message, ve.Details)
	}
	return fmt.Sprint(err)
}

func TestAnAgentLinkEntrypointRefusesTheAssistantContextKeysAndBuildsWithoutThem(t *testing.T) {
	for _, key := range []string{"systemPurpose", "responseFormat", "system_purpose", "response_format"} {
		t.Run(key, func(t *testing.T) {
			err := validateGatewayValues(platpkg.AgentLinkGatewayType, map[string]any{"maxInflight": 16, key: "purpose"})
			if err == nil {
				t.Fatalf("validateGatewayValues(agent_link, %s) = nil, want it refused", key)
			}
			if !strings.Contains(describeValidationError(err), key+" is not a setting of an agent_link entrypoint") {
				t.Errorf("refusal = %s, want it to name %s", describeValidationError(err), key)
			}
		})
	}
	appCfg := appConfigFor(t, platpkg.AgentLinkGatewayType, map[string]any{"maxInflight": 16, "systemPurpose": "purpose", "response_format": "format"})
	for _, key := range []string{"system_purpose", "response_format", "systemPurpose", "responseFormat"} {
		if _, carried := appCfg[key]; carried {
			t.Errorf("agent_link app_config carries %q, which nothing in the agent link applies", key)
		}
	}
}

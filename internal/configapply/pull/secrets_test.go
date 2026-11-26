// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package pull

import (
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestSubstituteSecretsTopLevel(t *testing.T) {
	in := map[string]any{
		"alias":       "planning",
		"provider":    "openai",
		"modelName":   "gpt-5",
		"description": "general-purpose model",
		"authConfig": map[string]any{
			"type":    "apikey",
			"api_key": redactedSentinel,
		},
	}
	out, warnings := substituteSecrets("model", "planning", in)
	if got, want := out["alias"], "planning"; got != want {
		t.Errorf("alias mutated: got %v want %v", got, want)
	}
	auth, ok := out["authConfig"].(map[string]any)
	if !ok {
		t.Fatalf("authConfig type = %T", out["authConfig"])
	}
	got, _ := auth["api_key"].(string)
	if got != "${MODEL_PLANNING_API_KEY}" {
		t.Errorf("api_key placeholder = %q", got)
	}
	if len(warnings) != 1 {
		t.Fatalf("warnings = %d, want 1", len(warnings))
	}
	w := warnings[0]
	if w.Kind != "model" || w.ResourceName != "planning" {
		t.Errorf("warning identity = %+v", w)
	}
	if w.FieldPath != "authConfig.api_key" {
		t.Errorf("warning path = %q", w.FieldPath)
	}
	if w.Placeholder != "${MODEL_PLANNING_API_KEY}" {
		t.Errorf("placeholder = %q", w.Placeholder)
	}
}

func TestSubstituteSecretsNested(t *testing.T) {
	in := map[string]any{
		"toolsetConfigs": []any{
			map[string]any{
				"toolsetName": "github",
				"configValues": map[string]any{
					"token": redactedSentinel,
				},
			},
		},
	}
	out, warnings := substituteSecrets("agent", "orchestrator", in)
	cfg := out["toolsetConfigs"].([]any)[0].(map[string]any)["configValues"].(map[string]any)
	if got := cfg["token"].(string); got != "${AGENT_ORCHESTRATOR_TOKEN}" {
		t.Errorf("nested placeholder = %q", got)
	}
	if len(warnings) != 1 {
		t.Errorf("warnings = %d", len(warnings))
	}
}

func TestSubstituteSecretsLeavesPlainStringsAlone(t *testing.T) {
	in := map[string]any{
		"description": "Has <REDACTED-LIKE> content but isn't the sentinel",
		"values": map[string]any{
			"username": "alice",
		},
	}
	out, warnings := substituteSecrets("connector", "db", in)
	if got := out["description"].(string); got != "Has <REDACTED-LIKE> content but isn't the sentinel" {
		t.Errorf("description mutated: %q", got)
	}
	if got := out["values"].(map[string]any)["username"].(string); got != "alice" {
		t.Errorf("non-secret mutated: %q", got)
	}
	if len(warnings) != 0 {
		t.Errorf("warnings = %d", len(warnings))
	}
}

func TestSanitizeVarNameStripsSpecials(t *testing.T) {
	got := sanitizeVarName("model_planning_authConfig.api-key")
	want := "MODEL_PLANNING_AUTHCONFIG_API_KEY"
	if got != want {
		t.Errorf("sanitize = %q want %q", got, want)
	}
}

func TestSanitizeVarNameAvoidsConsecutiveUnderscores(t *testing.T) {
	got := sanitizeVarName("a..b...c")
	want := "A_B_C"
	if got != want {
		t.Errorf("sanitize = %q want %q", got, want)
	}
}

func TestSubstituteSecrets_NamesAPlaceholderAfterTheLeafField(t *testing.T) {
	spec := map[string]any{"toolsetConfigs": []any{map[string]any{"configValues": map[string]any{"token": redactedSentinel}}}}

	_, warnings := substituteSecrets("agent", "orchestrator", spec)

	if len(warnings) != 1 || warnings[0].Placeholder != "${AGENT_ORCHESTRATOR_TOKEN}" {
		t.Errorf("warnings = %+v, want one placeholder ${AGENT_ORCHESTRATOR_TOKEN}", warnings)
	}
}

func TestLeafSegment(t *testing.T) {
	cases := map[string]string{
		"":                        "",
		"values":                  "values",
		"values[0]":               "values",
		"authConfig.api_key":      "api_key",
		"a.b[2].c":                "c",
		"toolsetConfigs[0].token": "token",
	}
	for in, want := range cases {
		if got := leafSegment(in); got != want {
			t.Errorf("leafSegment(%q) = %q want %q", in, got, want)
		}
	}
}

func TestSubstituteSecrets_AHeaderNameWithAPeriodKeepsItsWholeName(t *testing.T) {
	spec := map[string]any{
		"wake": map[string]any{
			"clientSecret": redactedSentinel,
			"headers":      map[string]any{"X.Custom": redactedSentinel, "A.Key": redactedSentinel, "B.Key": redactedSentinel, "Api-Key": redactedSentinel},
		},
		"Top.Level": redactedSentinel,
		"X.Tokens":  []any{redactedSentinel, redactedSentinel},
		"Y.Nested":  []any{[]any{redactedSentinel}},
	}

	_, warnings := substituteSecrets("linkAgent", "Claims Desk", spec)

	got := map[string]string{}
	for _, w := range warnings {
		got[w.FieldPath] = w.Placeholder
	}
	want := map[string]string{
		"wake.clientSecret":     "${LINKAGENT_CLAIMS_DESK_CLIENTSECRET}",
		"wake.headers.X.Custom": "${LINKAGENT_CLAIMS_DESK_X_CUSTOM}",
		"wake.headers.A.Key":    "${LINKAGENT_CLAIMS_DESK_A_KEY}",
		"wake.headers.B.Key":    "${LINKAGENT_CLAIMS_DESK_B_KEY}",
		"wake.headers.Api-Key":  "${LINKAGENT_CLAIMS_DESK_API_KEY}",
		"Top.Level":             "${LINKAGENT_CLAIMS_DESK_TOP_LEVEL}",
		"X.Tokens[0]":           "${LINKAGENT_CLAIMS_DESK_X_TOKENS_0}",
		"X.Tokens[1]":           "${LINKAGENT_CLAIMS_DESK_X_TOKENS_1}",
		"Y.Nested[0][0]":        "${LINKAGENT_CLAIMS_DESK_Y_NESTED_0_0}",
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("placeholders by field path (-want +got):\n%s", diff)
	}
}

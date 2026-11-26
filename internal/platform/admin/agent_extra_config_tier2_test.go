// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestValidateAdditionalConfigurationsTier2_RecognisedKeyWrongType(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		raw       string
		wantInMsg string
	}{
		{
			name:      "supportsStreaming string instead of bool",
			raw:       `{"supportsStreaming":"yes"}`,
			wantInMsg: "expected boolean",
		},
		{
			name:      "maxTokens string instead of int",
			raw:       `{"maxTokens":"4096"}`,
			wantInMsg: "expected integer",
		},
		{
			name:      "agentCardPublishing.intervalSeconds object instead of int",
			raw:       `{"agentCardPublishing":{"intervalSeconds":{"x":1}}}`,
			wantInMsg: "expected integer",
		},
		{
			name:      "requiredScopes string instead of list",
			raw:       `{"requiredScopes":"admin"}`,
			wantInMsg: "expected array",
		},
		{
			name:      "autoSummarization.compactionPercentage string instead of number",
			raw:       `{"autoSummarization":{"compactionPercentage":"0.25"}}`,
			wantInMsg: "expected number",
		},
		{
			name:      "agent_card scalar instead of object",
			raw:       `{"agentCard":42}`,
			wantInMsg: "expected object",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			for _, strict := range []bool{false, true} {
				warnings, err := validateAdditionalConfigurationsTier2(json.RawMessage(tc.raw), nil, strict)
				if err == nil {
					t.Fatalf("strict=%v: expected blocking error, got nil (warnings=%v)", strict, warnings)
				}
				ve, ok := errors.AsType[*ValidationError](err)
				if !ok {
					t.Fatalf("strict=%v: expected *ValidationError, got %T: %v", strict, err, err)
				}
				if !containsAnyDetail(ve, tc.wantInMsg) {
					t.Fatalf("strict=%v: expected detail to contain %q, got %#v", strict, tc.wantInMsg, ve.Details)
				}
				if len(warnings) > 0 {
					t.Fatalf("strict=%v: expected no warnings on type-mismatch error, got %v", strict, warnings)
				}
			}
		})
	}
}

func TestValidateAdditionalConfigurationsTier2_UnknownKey_NonStrictWarns(t *testing.T) {
	t.Parallel()

	raw := json.RawMessage(`{"agent_card_pulishing":{"intervalSeconds":30}}`)

	warnings, err := validateAdditionalConfigurationsTier2(raw, nil, false)
	if err != nil {
		t.Fatalf("expected nil error in non-strict mode, got %v", err)
	}
	if len(warnings) == 0 {
		t.Fatalf("expected at least one warning, got none")
	}
	if !strings.Contains(warnings[0], "agent_card_pulishing") {
		t.Fatalf("warning should name the unknown key, got %q", warnings[0])
	}
	if !strings.Contains(warnings[0], "unknown key") {
		t.Fatalf("warning should say 'unknown key', got %q", warnings[0])
	}
}

func TestValidateAdditionalConfigurationsTier2_UnknownKey_StrictErrors(t *testing.T) {
	t.Parallel()

	raw := json.RawMessage(`{"agent_card_pulishing":{"intervalSeconds":30}}`)

	warnings, err := validateAdditionalConfigurationsTier2(raw, nil, true)
	if err == nil {
		t.Fatalf("expected strict-mode error, got nil (warnings=%v)", warnings)
	}
	ve, ok := errors.AsType[*ValidationError](err)
	if !ok {
		t.Fatalf("expected *ValidationError, got %T: %v", err, err)
	}
	if !containsAnyDetail(ve, "agent_card_pulishing") {
		t.Fatalf("error detail should name the unknown key, got %#v", ve.Details)
	}
	if !containsAnyDetail(ve, "strict") {
		t.Fatalf("error detail should mention strict mode, got %#v", ve.Details)
	}
	if len(warnings) > 0 {
		t.Fatalf("expected no warnings on strict-mode error, got %v", warnings)
	}
}

func TestValidateAdditionalConfigurationsTier2_AcceptsRecognisedKeys(t *testing.T) {
	t.Parallel()

	raw := json.RawMessage(`{
		"supportsStreaming": true,
		"maxTokens": 4096,
		"agentCardPublishing": {"intervalSeconds": 30},
		"agentDiscovery": {"enabled": true, "healthCheckTtlSeconds": 300},
		"requiredScopes": ["agent:*:invoke"],
		"autoSummarization": {"enabled": true, "compactionPercentage": 0.25}
	}`)

	for _, strict := range []bool{false, true} {
		warnings, err := validateAdditionalConfigurationsTier2(raw, nil, strict)
		if err != nil {
			t.Fatalf("strict=%v: unexpected error: %v", strict, err)
		}
		if len(warnings) > 0 {
			t.Fatalf("strict=%v: expected no warnings, got %v", strict, warnings)
		}
	}
}

func TestValidateAdditionalConfigurationsTier2_ReportsAllUnknownKeys(t *testing.T) {
	t.Parallel()

	raw := json.RawMessage(`{"foo":1,"bar":"x","baz":true}`)

	warnings, err := validateAdditionalConfigurationsTier2(raw, nil, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	joined := strings.Join(warnings, "\n")
	for _, want := range []string{"foo", "bar", "baz"} {
		if !strings.Contains(joined, want) {
			t.Errorf("expected warnings to mention %q; got:\n%s", want, joined)
		}
	}
}

func TestValidateAdditionalConfigurationsTier2_TypeEdgeCases(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		raw       string
		wantInMsg string
	}{
		{
			name:      "fractional int rejected",
			raw:       `{"maxTokens":4096.5}`,
			wantInMsg: "fractional",
		},
		{
			name:      "int below minimum rejected",
			raw:       `{"agentCardPublishing":{"intervalSeconds":-1}}`,
			wantInMsg: "below minimum",
		},
		{
			name:      "huge negative int below minimum rejected",
			raw:       `{"maxLlmCallsPerTask":-1e30}`,
			wantInMsg: "value -1e+30 is below minimum 1",
		},
		{
			name:      "zero model calls rejected",
			raw:       `{"maxLlmCallsPerTask":0}`,
			wantInMsg: "below minimum 1",
		},
		{
			name:      "negative model calls rejected",
			raw:       `{"maxLlmCallsPerTask":-5}`,
			wantInMsg: "below minimum 1",
		},
		{
			name:      "fractional model calls rejected",
			raw:       `{"maxLlmCallsPerTask":1.5}`,
			wantInMsg: "fractional",
		},
		{
			name:      "text model calls rejected",
			raw:       `{"maxLlmCallsPerTask":"60"}`,
			wantInMsg: "expected integer",
		},
		{
			name:      "non-string element in string list rejected",
			raw:       `{"requiredScopes":["ok",42]}`,
			wantInMsg: "expected string",
		},
		{
			name:      "non-array for list type rejected",
			raw:       `{"volumes":"not-a-list"}`,
			wantInMsg: "expected array",
		},
		{
			name:      "model number rejected",
			raw:       `{"model":42}`,
			wantInMsg: "expected string or object",
		},
		{
			name:      "expected boolean got array exercises jsonTypeName(array)",
			raw:       `{"supportsStreaming":[]}`,
			wantInMsg: "expected boolean, got array",
		},
		{
			name:      "expected integer got boolean exercises jsonTypeName(boolean)",
			raw:       `{"maxTokens":true}`,
			wantInMsg: "expected integer, got boolean",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := validateAdditionalConfigurationsTier2(json.RawMessage(tc.raw), nil, false)
			if err == nil {
				t.Fatalf("expected blocking error, got nil")
			}
			ve, ok := errors.AsType[*ValidationError](err)
			if !ok {
				t.Fatalf("expected *ValidationError, got %T: %v", err, err)
			}
			if !containsAnyDetail(ve, tc.wantInMsg) {
				t.Fatalf("expected detail to contain %q, got %#v", tc.wantInMsg, ve.Details)
			}
		})
	}
}

func TestValidateAdditionalConfigurationsTier2_ModelStringOrObjectAccepted(t *testing.T) {
	t.Parallel()
	cases := []string{
		`{"model":"openai/gpt-4o"}`,
		`{"model":{"name":"openai/gpt-4o","cache_strategy":"5m"}}`,
	}
	for _, raw := range cases {
		warnings, err := validateAdditionalConfigurationsTier2(json.RawMessage(raw), nil, true)
		if err != nil {
			t.Fatalf("input %s: unexpected error: %v", raw, err)
		}
		if len(warnings) > 0 {
			t.Fatalf("input %s: unexpected warnings: %v", raw, warnings)
		}
	}
}

func TestValidateAdditionalConfigurationsTier2_DefensiveNoOpForBadInput(t *testing.T) {
	t.Parallel()
	cases := []string{
		``,
		`null`,
		`"a string"`,
		`[1,2,3]`,
		`not valid json at`,
	}
	for _, raw := range cases {
		warnings, err := validateAdditionalConfigurationsTier2(json.RawMessage(raw), nil, true)
		if err != nil {
			t.Errorf("input %q: expected nil err, got %v", raw, err)
		}
		if len(warnings) > 0 {
			t.Errorf("input %q: expected no warnings, got %v", raw, warnings)
		}
	}
}

func TestValidateAdditionalConfigurationsTier2_NullOnRecognisedKey(t *testing.T) {
	t.Parallel()

	raw := json.RawMessage(`{"supportsStreaming":null,"maxTokens":null}`)
	_, err := validateAdditionalConfigurationsTier2(raw, nil, false)
	if err == nil {
		t.Fatal("expected type-mismatch error for null on typed keys, got nil")
	}
	ve, ok := errors.AsType[*ValidationError](err)
	if !ok {
		t.Fatalf("expected *ValidationError, got %T: %v", err, err)
	}
	if !containsAnyDetail(ve, "got null") {
		t.Errorf("expected error to describe null value; got %#v", ve.Details)
	}
}

func TestValidateAdditionalConfigurationsTier2_LegacySnakeCaseIsUnknown(t *testing.T) {
	t.Parallel()

	raw := json.RawMessage(`{"supports_streaming":true}`)
	warnings, err := validateAdditionalConfigurationsTier2(raw, nil, false)
	if err != nil {
		t.Fatalf("expected nil error in non-strict mode for legacy snake_case key, got %v", err)
	}
	if len(warnings) == 0 {
		t.Fatal("expected unknown-key warning for legacy snake_case 'supports_streaming'")
	}
	if !strings.Contains(warnings[0], "supports_streaming") {
		t.Errorf("expected warning to name the legacy key, got %q", warnings[0])
	}

	_, err = validateAdditionalConfigurationsTier2(raw, nil, true)
	if err == nil {
		t.Fatal("expected strict-mode error for legacy snake_case key, got nil")
	}
	ve, ok := errors.AsType[*ValidationError](err)
	if !ok {
		t.Fatalf("expected *ValidationError, got %T: %v", err, err)
	}
	if !containsAnyDetail(ve, "supports_streaming") {
		t.Errorf("expected strict-mode error to name the legacy key, got %#v", ve.Details)
	}

	canonical := json.RawMessage(`{"supportsStreaming":true}`)
	warnings, err = validateAdditionalConfigurationsTier2(canonical, nil, true)
	if err != nil {
		t.Fatalf("expected canonical camelCase to be accepted, got error: %v", err)
	}
	if len(warnings) > 0 {
		t.Fatalf("expected no warnings for canonical camelCase, got %v", warnings)
	}
}

func TestValidateAdditionalConfigurationsTier2_WelcomeSuggestionsUnknownItemKey(t *testing.T) {
	t.Parallel()

	raw := json.RawMessage(`{"agentCard":{"welcome":{"suggestions":[{"label":"x","prompt":"y","mystery":1}]}}}`)

	warnings, err := validateAdditionalConfigurationsTier2(raw, nil, false)
	if err != nil {
		t.Fatalf("non-strict: expected nil error, got %v", err)
	}
	if len(warnings) == 0 {
		t.Fatalf("non-strict: expected warning for unknown suggestion key, got none")
	}
	if !strings.Contains(warnings[0], "agentCard.welcome.suggestions[0].mystery") {
		t.Errorf("non-strict: expected warning to carry indexed path, got %q", warnings[0])
	}

	_, err = validateAdditionalConfigurationsTier2(raw, nil, true)
	if err == nil {
		t.Fatal("strict: expected blocking error for unknown suggestion key, got nil")
	}
	ve, ok := errors.AsType[*ValidationError](err)
	if !ok {
		t.Fatalf("strict: expected *ValidationError, got %T: %v", err, err)
	}
	if !containsAnyDetail(ve, "agentCard.welcome.suggestions[0].mystery") {
		t.Errorf("strict: expected detail to carry indexed path, got %#v", ve.Details)
	}
}

func TestValidateAdditionalConfigurationsTier2_WelcomeSuggestionsNonObjectItem(t *testing.T) {
	t.Parallel()

	raw := json.RawMessage(`{"agentCard":{"welcome":{"suggestions":["just a string"]}}}`)
	for _, strict := range []bool{false, true} {
		_, err := validateAdditionalConfigurationsTier2(raw, nil, strict)
		if err == nil {
			t.Fatalf("strict=%v: expected blocking error for non-object suggestion item, got nil", strict)
		}
		ve, ok := errors.AsType[*ValidationError](err)
		if !ok {
			t.Fatalf("strict=%v: expected *ValidationError, got %T: %v", strict, err, err)
		}
		if !containsAnyDetail(ve, "agentCard.welcome.suggestions[0]") {
			t.Errorf("strict=%v: expected detail to carry indexed path, got %#v", strict, ve.Details)
		}
		if !containsAnyDetail(ve, "expected object") {
			t.Errorf("strict=%v: expected detail to say 'expected object', got %#v", strict, ve.Details)
		}
	}
}

func TestValidateAdditionalConfigurationsTier2_WelcomeSuggestionsAccepted(t *testing.T) {
	t.Parallel()

	raw := json.RawMessage(`{"agentCard":{"welcome":{"message":"hi","suggestions":[{"label":"x","prompt":"y","autoSend":true},{"label":"z","prompt":"w"}]}}}`)
	for _, strict := range []bool{false, true} {
		warnings, err := validateAdditionalConfigurationsTier2(raw, nil, strict)
		if err != nil {
			t.Fatalf("strict=%v: unexpected error: %v", strict, err)
		}
		if len(warnings) > 0 {
			t.Fatalf("strict=%v: expected no warnings, got %v", strict, warnings)
		}
	}
}

func TestValidateAdditionalConfigurationsTier2_WelcomeSuggestionsEmptyList(t *testing.T) {
	t.Parallel()

	raw := json.RawMessage(`{"agentCard":{"welcome":{"suggestions":[]}}}`)
	for _, strict := range []bool{false, true} {
		warnings, err := validateAdditionalConfigurationsTier2(raw, nil, strict)
		if err != nil {
			t.Fatalf("strict=%v: unexpected error: %v", strict, err)
		}
		if len(warnings) > 0 {
			t.Fatalf("strict=%v: expected no warnings, got %v", strict, warnings)
		}
	}
}

func TestValidateAdditionalConfigurationsTier2_WelcomeSuggestionsItemTypeMismatch(t *testing.T) {
	t.Parallel()

	raw := json.RawMessage(`{"agentCard":{"welcome":{"suggestions":[{"label":"x","prompt":"y","autoSend":"yes"}]}}}`)
	_, err := validateAdditionalConfigurationsTier2(raw, nil, false)
	if err == nil {
		t.Fatal("expected blocking type-mismatch error, got nil")
	}
	ve, ok := errors.AsType[*ValidationError](err)
	if !ok {
		t.Fatalf("expected *ValidationError, got %T: %v", err, err)
	}
	if !containsAnyDetail(ve, "agentCard.welcome.suggestions[0].autoSend") {
		t.Errorf("expected detail to carry indexed path, got %#v", ve.Details)
	}
	if !containsAnyDetail(ve, "expected boolean") {
		t.Errorf("expected detail to say 'expected boolean', got %#v", ve.Details)
	}
}

func containsAnyDetail(ve *ValidationError, needle string) bool {
	if strings.Contains(ve.Message, needle) {
		return true
	}
	for _, msgs := range ve.Details {
		for _, m := range msgs {
			if strings.Contains(m, needle) {
				return true
			}
		}
	}
	return false
}

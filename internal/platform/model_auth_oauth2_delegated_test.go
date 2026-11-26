// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"errors"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

const testGrantID = "0192f7e2-8a4b-7c3d-9e5f-1a2b3c4d5e6f"

func TestWriteAuthConfigToRaw_OnlyAGrantConfigNamesAGrant(t *testing.T) {
	raw := map[string]any{}
	WriteAuthConfigToRaw(raw, map[string]any{"type": AuthTypeOAuth2Delegated, llm.GrantIDKey: testGrantID})
	if raw[llm.GrantIDKey] != testGrantID || len(raw) != 1 {
		t.Errorf("oauth2_delegated raw = %v, want only grant_id", raw)
	}

	raw = map[string]any{}
	WriteAuthConfigToRaw(raw, map[string]any{"type": "apikey", "api_key": "k", llm.GrantIDKey: testGrantID})
	if _, named := raw[llm.GrantIDKey]; named {
		t.Errorf("apikey raw = %v; another auth type must not pass grant_id on", raw)
	}
}

func TestToRawLiteLLMConfig_GrantModel(t *testing.T) {
	mc := &store.ModelConfiguration{
		ID: "m1", Provider: "openai", ModelName: "free-model", ModelType: store.ModelTypeChat,
		ModelAuthType:   AuthTypeOAuth2Delegated,
		ModelAuthConfig: `{"type":"oauth2_delegated","grant_id":"` + testGrantID + `"}`,
		ModelParams:     `{}`,
	}
	raw := ToRawLiteLLMConfig(mc)
	if raw[llm.GrantIDKey] != testGrantID {
		t.Fatalf("raw = %v, want grant_id", raw)
	}
	if err := ValidateRuntimeModelConfig(raw); err != nil {
		t.Errorf("ValidateRuntimeModelConfig: %v", err)
	}
}

func TestValidateRuntimeModelConfig_Grant(t *testing.T) {
	cases := map[string]struct {
		raw       map[string]any
		wantField string
	}{
		"not a uuid":       {map[string]any{llm.GrantIDKey: "solace"}, "authConfig." + llm.GrantIDKey},
		"no grant":         {grantRaw(map[string]any{"type": AuthTypeOAuth2Delegated}), "authConfig." + llm.GrantIDKey},
		"no grant, a key":  {grantRaw(map[string]any{"type": AuthTypeOAuth2Delegated, "api_key": "k"}), "api_key"},
		"with an api key":  {map[string]any{llm.GrantIDKey: testGrantID, "api_key": "k"}, "api_key"},
		"with oauth2":      {map[string]any{llm.GrantIDKey: testGrantID, "oauth_client_secret": "s"}, "oauth_client_secret"},
		"with azure block": {map[string]any{llm.GrantIDKey: testGrantID, llm.AzureWorkloadIdentityKey: map[string]any{}}, llm.AzureWorkloadIdentityKey},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			err := ValidateRuntimeModelConfig(tc.raw)
			var vErr *ValidationError
			if !errors.As(err, &vErr) || len(vErr.Details[tc.wantField]) == 0 {
				t.Fatalf("err = %v, want a detail on %s", err, tc.wantField)
			}
		})
	}
}

func TestValidateExtraHeaders_RefusesAGrantInModelParams(t *testing.T) {
	err := validateExtraHeaders(map[string]any{llm.GrantIDKey: testGrantID})
	var vErr *ValidationError
	if !errors.As(err, &vErr) || vErr.Message != GrantIDInModelParamsMessage {
		t.Fatalf("err = %v, want the grant_id refusal", err)
	}
}

func TestAuthTypeSwitchReplacesConfig_Grant(t *testing.T) {
	if !AuthTypeSwitchReplacesConfig("apikey", AuthTypeOAuth2Delegated) || !AuthTypeSwitchReplacesConfig(AuthTypeOAuth2Delegated, "apikey") {
		t.Error("switching to or from oauth2_delegated must replace the stored auth config")
	}
}

func grantRaw(authConfig map[string]any) map[string]any {
	raw := map[string]any{}
	WriteAuthConfigToRaw(raw, authConfig)
	return raw
}

// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package a2a

import (
	"maps"
	"reflect"
	"strings"
	"testing"
)

func TestRedactAuthSecretsWalksStatusEvent(t *testing.T) {
	t.Parallel()
	const (
		verifier = "pkce-verifier-must-not-leak"
		authURI  = "https://idp.example.com/authorize?state=state-must-not-leak"
	)
	authData := mustUnmarshal[map[string]any](t, mustMarshal(t, AuthenticationRequiredData{
		Type:         "authentication_required",
		TargetAgent:  "Orchestrator",
		AuthURI:      authURI,
		CodeVerifier: verifier,
	}))
	for key := range authSecretKeys {
		if _, present := authData[key]; !present {
			authData[key] = "unseeded-" + key + "-must-not-leak"
		}
	}
	rootWrapped := maps.Clone(authData)

	payload := map[string]any{"result": map[string]any{"status": map[string]any{
		"state": string(TaskStateInputNeeded),
		"message": map[string]any{"parts": []any{
			map[string]any{"kind": "text", "text": "please sign in"},
			map[string]any{"kind": "data", "data": authData},
			map[string]any{"root": map[string]any{"kind": "data", "data": rootWrapped}},
		}},
	}}}

	RedactAuthSecrets(payload)
	out := mustMarshal(t, payload)

	for _, secret := range []string{verifier, authURI, "state-must-not-leak"} {
		if strings.Contains(out, secret) {
			t.Errorf("payload still contains %q: %s", secret, out)
		}
	}
	for _, keep := range []string{"authentication_required", "Orchestrator", "please sign in"} {
		if !strings.Contains(out, keep) {
			t.Errorf("payload lost non-secret field %q: %s", keep, out)
		}
	}
	for key := range authSecretKeys {
		if authData[key] != RedactedAuthValue {
			t.Errorf("%s = %v, want masked in place", key, authData[key])
		}
		if rootWrapped[key] != RedactedAuthValue {
			t.Errorf("root-wrapped %s = %v, want masked in place", key, rootWrapped[key])
		}
	}
}

func TestRedactAuthSecretsLeavesNonSecretsIntact(t *testing.T) {
	t.Parallel()
	cases := map[string]map[string]any{
		"unrelatedTraffic": {"result": map[string]any{
			"status":   map[string]any{"state": "completed", "message": map[string]any{"parts": []any{map[string]any{"kind": "text", "text": "done"}}}},
			"metadata": map[string]any{"token_usage": map[string]any{"total_tokens": float64(150)}},
		}},
		"nonStringValues": {"code_verifier": nil, "auth_uri": map[string]any{"nested": "value"}},
	}
	for name, payload := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			before := mustMarshal(t, payload)
			RedactAuthSecrets(payload)
			if after := mustMarshal(t, payload); after != before {
				t.Errorf("payload was modified:\nbefore %s\n after %s", before, after)
			}
		})
	}
}

func TestAuthSecretKeysCoversAuthPayload(t *testing.T) {
	t.Parallel()
	reviewedNonSecret := map[string]struct{}{
		"type": {}, "target_agent": {}, "target_agent_task_id": {},
		"user_id": {}, "client_id": {}, "auth_request_function_call_id": {},
		"credential_key": {}, "gateway_task_id": {}, "tool_display_name": {},
	}
	assertWireKeysClassified(t, reflect.TypeFor[AuthenticationRequiredData](), authSecretKeys, reviewedNonSecret)
}

func TestAuthSignalSecretKeysCoversAuthResponse(t *testing.T) {
	t.Parallel()
	reviewedNonSecret := map[string]struct{}{
		"type": {}, "target_agent": {}, "target_agent_task_id": {},
		"user_id": {}, "client_id": {}, "function_call_id": {},
		"credential_key": {}, "user_identity": {}, "task_call_stack": {},
		"is_mcp_credential_save": {},
	}
	masked := maps.Clone(authSecretKeys)
	maps.Copy(masked, authSignalSecretKeys[SignalAuthResponse])
	assertWireKeysClassified(t, reflect.TypeFor[AuthResponseData](), masked, reviewedNonSecret)
}

func assertWireKeysClassified(t *testing.T, structType reflect.Type, masked, reviewed map[string]struct{}) {
	t.Helper()
	for i := range structType.NumField() {
		field := structType.Field(i)
		tag, _, _ := strings.Cut(field.Tag.Get("json"), ",")
		if tag == "-" {
			continue
		}
		fieldType := field.Type
		for fieldType.Kind() == reflect.Pointer {
			fieldType = fieldType.Elem()
		}
		if fieldType.Kind() == reflect.Struct && (field.IsExported() || field.Anonymous) {
			assertWireKeysClassified(t, fieldType, masked, reviewed)
			continue
		}
		if !field.IsExported() {
			continue
		}
		if tag == "" {
			tag = field.Name
		}
		if _, isMasked := masked[tag]; isMasked {
			continue
		}
		if _, ok := reviewed[tag]; !ok {
			t.Errorf("field %q is classified neither as a masked secret nor as a reviewed non-secret", tag)
		}
	}
}

func TestRedactAuthSecretsMasksAuthResponseByType(t *testing.T) {
	t.Parallel()
	const (
		code     = "authorization-code-must-not-leak"
		state    = "callback-state-must-not-leak"
		callback = "https://gw.example.com/callback?code=authorization-code-must-not-leak"
	)
	authData := mustUnmarshal[map[string]any](t, mustMarshal(t, AuthResponseData{
		Type:            string(SignalAuthResponse),
		TargetAgent:     "Orchestrator",
		UserID:          "u1",
		Code:            code,
		State:           state,
		AuthResponseURI: callback,
		CodeVerifier:    "pkce-verifier-must-not-leak",
	}))
	payload := map[string]any{
		"error":  map[string]any{"code": float64(-32603), "message": "boom"},
		"result": map[string]any{"status": map[string]any{"state": string(TaskStateFailed)}},
		"params": map[string]any{"message": map[string]any{"parts": []any{
			map[string]any{"kind": "data", "data": authData},
		}}},
	}

	RedactAuthSecrets(payload)
	out := mustMarshal(t, payload)
	for _, secret := range []string{code, state, callback, "pkce-verifier-must-not-leak"} {
		if strings.Contains(out, secret) {
			t.Errorf("payload still contains %q: %s", secret, out)
		}
	}
	for key := range authSignalSecretKeys[SignalAuthResponse] {
		if authData[key] != RedactedAuthValue {
			t.Errorf("auth_response %s = %v, want masked in place", key, authData[key])
		}
	}
	if got := payload["error"].(map[string]any)["code"]; got != float64(-32603) {
		t.Errorf("error.code = %v, want -32603", got)
	}
	status := payload["result"].(map[string]any)["status"].(map[string]any)
	if got := status["state"]; got != string(TaskStateFailed) {
		t.Errorf("result.status.state = %v, want %v", got, TaskStateFailed)
	}
}

func TestPayloadHasAuthMaterial(t *testing.T) {
	t.Parallel()
	if PayloadHasAuthMaterial([]byte(`{"result":{"status":{"state":"completed"}}}`)) {
		t.Error("ordinary event should skip the walk")
	}
	if PayloadHasAuthMaterial([]byte(`{"error":{"code":-32603},"result":{"status":{"state":"failed"}}}`)) {
		t.Error("colliding keys alone should skip the walk")
	}
	for key := range authSecretKeys {
		if !PayloadHasAuthMaterial([]byte(`{"data":{"` + key + `":"x"}}`)) {
			t.Errorf("payload carrying %q was not detected", key)
		}
	}
	for signalType := range authSignalSecretKeys {
		if !PayloadHasAuthMaterial([]byte(`{"data":{"type":"` + string(signalType) + `"}}`)) {
			t.Errorf("payload declaring type %q was not detected", signalType)
		}
	}
}

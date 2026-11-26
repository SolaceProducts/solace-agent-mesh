// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"encoding/json"
	"testing"
)

func TestRedactSecretFields_EventMesh(t *testing.T) {
	t.Parallel()
	values := map[string]any{
		"broker_url":      "tcp://host:55555",
		"broker_vpn":      "default",
		"broker_username": "admin",
		"broker_password": "super_secret",
	}

	secretFields := getSecretRegistry().GatewaySecretFields("event_mesh")
	result := redactSecretFields(values, secretFields)

	if result["broker_password"] != redactedSentinel {
		t.Errorf("broker_password = %q, want %q", result["broker_password"], redactedSentinel)
	}
	if result["broker_url"] != "tcp://host:55555" {
		t.Errorf("broker_url = %q, should not be redacted", result["broker_url"])
	}
	if result["broker_username"] != "admin" {
		t.Errorf("broker_username = %q, should not be redacted", result["broker_username"])
	}
}

func TestRedactSecretFields_NilValueOmitted(t *testing.T) {
	t.Parallel()
	values := map[string]any{
		"broker_url":      "tcp://host:55555",
		"broker_password": nil,
	}

	secretFields := getSecretRegistry().GatewaySecretFields("event_mesh")
	result := redactSecretFields(values, secretFields)

	if _, ok := result["broker_password"]; ok {
		t.Error("nil broker_password should be omitted from redacted result")
	}
	if result["broker_url"] != "tcp://host:55555" {
		t.Errorf("broker_url = %q, should not be redacted", result["broker_url"])
	}
}

func TestRedactSecretFields_Slack(t *testing.T) {
	t.Parallel()
	values := map[string]any{
		"bot_token": "xoxb-secret",
		"app_token": "xapp-secret",
		"channel":   "general",
	}

	secretFields := getSecretRegistry().GatewaySecretFields("slack")
	result := redactSecretFields(values, secretFields)

	if result["bot_token"] != redactedSentinel {
		t.Errorf("bot_token = %q, want %q", result["bot_token"], redactedSentinel)
	}
	if result["app_token"] != redactedSentinel {
		t.Errorf("app_token = %q, want %q", result["app_token"], redactedSentinel)
	}
	if result["channel"] != "general" {
		t.Errorf("channel = %q, should not be redacted", result["channel"])
	}
}

func TestRedactSecretFields_Teams(t *testing.T) {
	t.Parallel()
	values := map[string]any{
		"microsoft_app_id":       "app-123",
		"microsoft_app_password": "teams_secret",
	}

	secretFields := getSecretRegistry().GatewaySecretFields("teams")
	result := redactSecretFields(values, secretFields)

	if result["microsoft_app_password"] != redactedSentinel {
		t.Errorf("microsoft_app_password = %q, want %q", result["microsoft_app_password"], redactedSentinel)
	}
	if result["microsoft_app_id"] != "app-123" {
		t.Errorf("microsoft_app_id = %q, should not be redacted", result["microsoft_app_id"])
	}
}

func TestRedactSecretFields_UnknownType(t *testing.T) {
	t.Parallel()
	values := map[string]any{
		"password": "some_value",
		"key":      "other_value",
	}

	secretFields := getSecretRegistry().GatewaySecretFields("unknown_type")
	result := redactSecretFields(values, secretFields)

	if result["password"] != "some_value" {
		t.Errorf("password = %q, want %q (unknown type should not redact)", result["password"], "some_value")
	}
}

func TestMergeSecretFields_PreservesExisting(t *testing.T) {
	t.Parallel()
	existingValues := map[string]any{
		"broker_url":             "tcp://host:55555",
		"broker_password":        "real_secret",
		"broker_username":        "admin",
		"broker_connection_type": "custom",
	}

	newValues := map[string]any{
		"broker_url":             "tcp://newhost:55555",
		"broker_username":        "new_admin",
		"broker_connection_type": "custom",
	}

	secretFields := getSecretRegistry().GatewaySecretFields("event_mesh")
	result, mergeErr := mergeSecretFields(newValues, existingValues, secretFields)
	if mergeErr != nil {
		t.Fatalf("merge: %v", mergeErr)
	}

	if result["broker_password"] != "real_secret" {
		t.Errorf("broker_password = %q, want %q (should preserve existing)", result["broker_password"], "real_secret")
	}
	if result["broker_username"] != "new_admin" {
		t.Errorf("broker_username = %q, want %q (should take incoming)", result["broker_username"], "new_admin")
	}
	if result["broker_url"] != "tcp://newhost:55555" {
		t.Errorf("broker_url = %q, want %q (should take incoming)", result["broker_url"], "tcp://newhost:55555")
	}
}

func TestMergeSecretFields_ExplicitValueOverrides(t *testing.T) {
	t.Parallel()
	existingValues := map[string]any{
		"broker_password":        "old_secret",
		"broker_username":        "admin",
		"broker_connection_type": "custom",
	}

	newValues := map[string]any{
		"broker_password":        "brand_new_secret",
		"broker_username":        "new_user",
		"broker_connection_type": "custom",
	}

	secretFields := getSecretRegistry().GatewaySecretFields("event_mesh")
	result, mergeErr := mergeSecretFields(newValues, existingValues, secretFields)
	if mergeErr != nil {
		t.Fatalf("merge: %v", mergeErr)
	}

	if result["broker_password"] != "brand_new_secret" {
		t.Errorf("broker_password = %q, want %q (explicit value should override)", result["broker_password"], "brand_new_secret")
	}
}

func TestMergeSecretFields_RedactedSentinel(t *testing.T) {
	t.Parallel()
	existingValues := map[string]any{
		"broker_password":        "real_secret",
		"broker_connection_type": "custom",
	}

	newValues := map[string]any{
		"broker_password":        redactedSentinel,
		"broker_connection_type": "custom",
	}

	secretFields := getSecretRegistry().GatewaySecretFields("event_mesh")
	result, mergeErr := mergeSecretFields(newValues, existingValues, secretFields)
	if mergeErr != nil {
		t.Fatalf("merge: %v", mergeErr)
	}

	if result["broker_password"] != "real_secret" {
		t.Errorf("broker_password = %q, want %q (sentinel should preserve existing)", result["broker_password"], "real_secret")
	}
}

func TestMergeSecretFields_EmptyStringRequiredField(t *testing.T) {
	t.Parallel()
	existingValues := map[string]any{
		"bot_token": "xoxb-real-token",
		"app_token": "xapp-real-token",
	}

	newValues := map[string]any{
		"bot_token": "",
		"app_token": "xapp-new-token",
	}

	secretFields := getSecretRegistry().GatewaySecretFields("slack")
	result, mergeErr := mergeSecretFields(newValues, existingValues, secretFields)
	if mergeErr != nil {
		t.Fatalf("merge: %v", mergeErr)
	}

	if result["bot_token"] != "xoxb-real-token" {
		t.Errorf("bot_token = %q, want %q (empty string on required field should preserve)", result["bot_token"], "xoxb-real-token")
	}
	if result["app_token"] != "xapp-new-token" {
		t.Errorf("app_token = %q, want %q (explicit new value)", result["app_token"], "xapp-new-token")
	}
}

func TestMergeSecretFields_EventMeshPasswordEmptyStringPreserved(t *testing.T) {
	t.Parallel()
	existingValues := map[string]any{
		"broker_password":        "old_password",
		"broker_connection_type": "custom",
	}

	newValues := map[string]any{
		"broker_password":        "",
		"broker_connection_type": "custom",
	}

	secretFields := getSecretRegistry().GatewaySecretFields("event_mesh")
	result, mergeErr := mergeSecretFields(newValues, existingValues, secretFields)
	if mergeErr != nil {
		t.Fatalf("merge: %v", mergeErr)
	}

	if result["broker_password"] != "old_password" {
		t.Errorf("broker_password = %v, want \"old_password\" preserved (empty string on required field)", result["broker_password"])
	}
}

func TestMergeSecretFields_ConditionalBranchSwitch(t *testing.T) {
	t.Parallel()
	existingValues := map[string]any{
		"broker_connection_type": "custom",
		"broker_password":        "old_secret",
	}

	newValues := map[string]any{
		"broker_connection_type": "default",
	}

	secretFields := getSecretRegistry().GatewaySecretFields("event_mesh")
	result, mergeErr := mergeSecretFields(newValues, existingValues, secretFields)
	if mergeErr != nil {
		t.Fatalf("merge: %v", mergeErr)
	}

	if result["broker_password"] != "old_secret" {
		t.Errorf("broker_password = %v, want \"old_secret\" preserved across mode switch", result["broker_password"])
	}
}

func TestMergeSecretFields_ConditionalBranchActive(t *testing.T) {
	t.Parallel()
	existingValues := map[string]any{
		"broker_connection_type": "custom",
		"broker_password":        "kept_secret",
	}

	newValues := map[string]any{
		"broker_connection_type": "custom",
		"broker_url":             "tcp://newhost:55555",
	}

	secretFields := getSecretRegistry().GatewaySecretFields("event_mesh")
	result, mergeErr := mergeSecretFields(newValues, existingValues, secretFields)
	if mergeErr != nil {
		t.Fatalf("merge: %v", mergeErr)
	}

	if result["broker_password"] != "kept_secret" {
		t.Errorf("broker_password = %q, want %q (should preserve when conditional branch stays active)", result["broker_password"], "kept_secret")
	}
}

func TestSecretRoundTrip(t *testing.T) {
	t.Parallel()
	originalValues := map[string]any{
		"broker_url":             "tcp://host:55555",
		"broker_vpn":             "default",
		"broker_username":        "admin",
		"broker_password":        "super_secret_password",
		"broker_connection_type": "custom",
	}

	storedJSON, _ := json.Marshal(originalValues)

	var parsed map[string]any
	_ = json.Unmarshal(storedJSON, &parsed)

	secretFields := getSecretRegistry().GatewaySecretFields("event_mesh")
	redacted := redactSecretFields(parsed, secretFields)

	if redacted["broker_password"] != redactedSentinel {
		t.Fatalf("redacted broker_password = %q, want %q", redacted["broker_password"], redactedSentinel)
	}
	if redacted["broker_url"] != "tcp://host:55555" {
		t.Fatal("broker_url should not be redacted")
	}

	var existingValues map[string]any
	_ = json.Unmarshal(storedJSON, &existingValues)

	updatePayload := map[string]any{
		"broker_url":             "tcp://newhost:55555",
		"broker_vpn":             "default",
		"broker_username":        "new_admin",
		"broker_password":        redactedSentinel,
		"broker_connection_type": "custom",
	}

	merged, mergeErr := mergeSecretFields(updatePayload, existingValues, secretFields)
	if mergeErr != nil {
		t.Fatalf("merge: %v", mergeErr)
	}

	if merged["broker_password"] != "super_secret_password" {
		t.Errorf("broker_password = %q, want original %q", merged["broker_password"], "super_secret_password")
	}
	if merged["broker_username"] != "new_admin" {
		t.Errorf("broker_username = %q, want %q", merged["broker_username"], "new_admin")
	}
	if merged["broker_url"] != "tcp://newhost:55555" {
		t.Errorf("broker_url = %q, want %q", merged["broker_url"], "tcp://newhost:55555")
	}
}

func TestGetSchemas_EventMeshPasswordRequiredByEnv(t *testing.T) {
	passwordRequired := func(t *testing.T) bool {
		t.Helper()
		svc := newEventMeshGatewayService(t)
		for _, s := range svc.GetSchemas() {
			if s.Type != "event_mesh" {
				continue
			}
			fields, ok := s.Schema.(map[string]any)
			if !ok {
				t.Fatalf("event_mesh schema not a map: %T", s.Schema)
			}
			pw, ok := fields["broker_password"].(map[string]any)
			if !ok {
				t.Fatal("broker_password field missing from event_mesh schema")
			}
			req, _ := pw["required"].(bool)
			return req
		}
		t.Fatal("event_mesh schema not found")
		return false
	}

	t.Run("dev (no system broker) — not required", func(t *testing.T) {
		t.Setenv("SOLACE_BROKER_URL", "")
		if passwordRequired(t) {
			t.Error("broker_password should not be required in the dev UI schema")
		}
	})

	t.Run("prod (system broker set) — required", func(t *testing.T) {
		t.Setenv("SOLACE_BROKER_URL", "tcps://platform-broker.example.com:55443")
		if !passwordRequired(t) {
			t.Error("broker_password should be required in the UI schema when a system broker is configured")
		}
	})
}

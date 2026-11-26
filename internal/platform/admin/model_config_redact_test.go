// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"testing"

	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
)

func TestRedactAuthConfig_APIKey(t *testing.T) {
	t.Parallel()
	input := map[string]any{
		"type":    "apikey",
		"api_key": "sk-secret-key",
		"extra":   "value",
	}
	got := redactModelAuthConfig(input)

	if got["api_key"] != redactedSentinel {
		t.Errorf("api_key = %q, want %q", got["api_key"], redactedSentinel)
	}
	if got["type"] != "apikey" {
		t.Error("type should be preserved")
	}
	if got["extra"] != "value" {
		t.Error("non-secret fields should be preserved")
	}

	if input["api_key"] != "sk-secret-key" {
		t.Error("original map should not be mutated")
	}
}

func TestRedactAuthConfig_OAuth2(t *testing.T) {
	t.Parallel()
	input := map[string]any{
		"type":          "oauth2",
		"client_id":     "my-client",
		"client_secret": "super-secret",
		"token_url":     "https://auth.example.com/token",
	}
	got := redactModelAuthConfig(input)

	if got["client_secret"] != redactedSentinel {
		t.Errorf("client_secret = %q, want %q", got["client_secret"], redactedSentinel)
	}
	if got["client_id"] != "my-client" {
		t.Error("client_id should be preserved")
	}
	if got["token_url"] != "https://auth.example.com/token" {
		t.Error("token_url should be preserved")
	}
}

func TestRedactAuthConfig_AzureWorkloadIdentity(t *testing.T) {
	t.Parallel()
	input := map[string]any{
		"type":      "azure_workload_identity",
		"client_id": "my-client",
		"tenant_id": "my-tenant",
	}
	got := redactModelAuthConfig(input)

	if got["client_id"] != "my-client" {
		t.Errorf("client_id = %v, want it preserved", got["client_id"])
	}
	if got["tenant_id"] != "my-tenant" {
		t.Errorf("tenant_id = %v, want it preserved", got["tenant_id"])
	}
}

func TestRedactAuthConfig_Bearer(t *testing.T) {
	t.Parallel()
	input := map[string]any{
		"type":  "bearer",
		"token": "my-bearer-token",
	}
	got := redactModelAuthConfig(input)

	if got["token"] != redactedSentinel {
		t.Errorf("token = %q, want %q", got["token"], redactedSentinel)
	}
}

func TestRedactAuthConfig_Basic(t *testing.T) {
	t.Parallel()
	input := map[string]any{
		"type":     "basic",
		"username": "user",
		"password": "pass",
	}
	got := redactModelAuthConfig(input)

	if got["password"] != redactedSentinel {
		t.Errorf("password = %q, want %q", got["password"], redactedSentinel)
	}
	if got["username"] != "user" {
		t.Error("username should be preserved")
	}
}

func TestRedactAuthConfig_AWSIAM(t *testing.T) {
	t.Parallel()
	input := map[string]any{
		"type":                  "aws_iam",
		"aws_access_key_id":     "AKIAIOSFODNN7EXAMPLE",
		"aws_secret_access_key": "secret",
		"aws_session_token":     "token",
		"region":                "us-east-1",
	}
	got := redactModelAuthConfig(input)

	if got["aws_secret_access_key"] != redactedSentinel {
		t.Errorf("aws_secret_access_key = %q, want %q", got["aws_secret_access_key"], redactedSentinel)
	}
	if got["aws_session_token"] != redactedSentinel {
		t.Errorf("aws_session_token = %q, want %q", got["aws_session_token"], redactedSentinel)
	}
	if got["aws_access_key_id"] != "AKIAIOSFODNN7EXAMPLE" {
		t.Error("aws_access_key_id should be preserved")
	}
	if got["region"] != "us-east-1" {
		t.Error("region should be preserved")
	}
}

func TestRedactAuthConfig_GCPServiceAccount(t *testing.T) {
	t.Parallel()
	input := map[string]any{
		"type":                 "gcp_service_account",
		"service_account_json": `{"private_key":"..."}`,
		"project_id":           "my-project",
	}
	got := redactModelAuthConfig(input)

	if got["service_account_json"] != redactedSentinel {
		t.Errorf("service_account_json = %q, want %q", got["service_account_json"], redactedSentinel)
	}
	if got["project_id"] != "my-project" {
		t.Error("project_id should be preserved")
	}
}

func TestRedactAuthConfig_None(t *testing.T) {
	t.Parallel()
	input := map[string]any{
		"type": "none",
	}
	got := redactModelAuthConfig(input)

	if got["type"] != "none" {
		t.Errorf("type = %v, want 'none'", got["type"])
	}
}

func TestRedactAuthConfig_UnknownType(t *testing.T) {
	t.Parallel()
	input := map[string]any{
		"type":       "custom",
		"some_field": "value",
	}
	got := redactModelAuthConfig(input)

	if got["type"] != "custom" {
		t.Errorf("type = %v, want 'custom'", got["type"])
	}
	if got["some_field"] != "value" {
		t.Error("other fields should be preserved for unknown types")
	}
}

func TestRedactAuthConfig_Nil(t *testing.T) {
	t.Parallel()
	got := redactModelAuthConfig(nil)
	if got == nil {
		t.Error("expected non-nil empty map")
	}
	if len(got) != 0 {
		t.Errorf("expected empty map, got %v", got)
	}
}

func TestRedactAuthConfig_Empty(t *testing.T) {
	t.Parallel()
	got := redactModelAuthConfig(map[string]any{})
	if len(got) != 0 {
		t.Errorf("expected empty map, got %v", got)
	}
}

func TestModelAuthSecretKeysCoverSupportedAuthTypes(t *testing.T) {
	for _, authType := range platpkg.SupportedModelAuthTypes {
		if _, ok := modelAuthSecretKeys[authType]; !ok {
			t.Errorf("auth type %q has no entry in modelAuthSecretKeys; add it (an empty list if it has no secrets)", authType)
		}
	}
}

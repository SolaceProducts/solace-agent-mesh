// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"slices"
	"testing"
)

func TestRedactAuthConfig(t *testing.T) {
	tests := []struct {
		name       string
		authType   string
		input      map[string]any
		wantAbsent []string
		wantKeep   []string
	}{
		{
			name:       "apikey redacts api_key and type",
			authType:   "apikey",
			input:      map[string]any{"type": "apikey", "api_key": "sk-secret-123"},
			wantAbsent: []string{"api_key", "type"},
		},
		{
			name:       "oauth2 redacts client_secret",
			authType:   "oauth2",
			input:      map[string]any{"type": "oauth2", "client_id": "id123", "client_secret": "sec", "token_url": "https://auth.example.com/token"},
			wantAbsent: []string{"client_secret", "type"},
			wantKeep:   []string{"client_id", "token_url"},
		},
		{
			name:       "bearer redacts token",
			authType:   "bearer",
			input:      map[string]any{"type": "bearer", "token": "tok123"},
			wantAbsent: []string{"token", "type"},
		},
		{
			name:       "basic redacts password",
			authType:   "basic",
			input:      map[string]any{"type": "basic", "username": "user", "password": "pass"},
			wantAbsent: []string{"password", "type"},
			wantKeep:   []string{"username"},
		},
		{
			name:       "aws_iam redacts secret_access_key and session_token",
			authType:   "aws_iam",
			input:      map[string]any{"type": "aws_iam", "aws_access_key_id": "AKIA...", "aws_secret_access_key": "secret", "aws_session_token": "tok"},
			wantAbsent: []string{"aws_secret_access_key", "aws_session_token", "type"},
			wantKeep:   []string{"aws_access_key_id"},
		},
		{
			name:       "gcp_service_account redacts service_account_json",
			authType:   "gcp_service_account",
			input:      map[string]any{"type": "gcp_service_account", "service_account_json": `{"key":"val"}`},
			wantAbsent: []string{"service_account_json", "type"},
		},
		{
			name:       "azure_workload_identity keeps the identifiers",
			authType:   "azure_workload_identity",
			input:      map[string]any{"type": "azure_workload_identity", "client_id": "c", "tenant_id": "t"},
			wantAbsent: []string{"type"},
			wantKeep:   []string{"client_id", "tenant_id"},
		},
		{
			name:       "unknown type only removes type",
			authType:   "unknown",
			input:      map[string]any{"type": "unknown", "foo": "bar"},
			wantAbsent: []string{"type"},
			wantKeep:   []string{"foo"},
		},
		{
			name:       "nil input returns empty map",
			authType:   "apikey",
			input:      nil,
			wantAbsent: nil,
		},
		{
			name:       "none type keeps non-secret fields",
			authType:   "none",
			input:      map[string]any{"type": "none"},
			wantAbsent: []string{"type"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := RedactAuthConfig(tt.authType, tt.input)
			for _, key := range tt.wantAbsent {
				if _, ok := result[key]; ok {
					t.Errorf("expected %q to be redacted, but it was present", key)
				}
			}
			for _, key := range tt.wantKeep {
				if _, ok := result[key]; !ok {
					t.Errorf("expected %q to be kept, but it was absent", key)
				}
			}
			if tt.input != nil {
				if _, modified := tt.input["type"]; !modified {
					t.Error("original input should not be modified (type field removed from original)")
				}
			}
		})
	}
}

func TestAuthSecretFieldsCoverSupportedAuthTypes(t *testing.T) {
	for _, authType := range SupportedModelAuthTypes {
		if _, ok := authSecretFields[authType]; !ok {
			t.Errorf("auth type %q has no entry in authSecretFields; add it (an empty list if it has no secrets)", authType)
		}
	}
}

func TestSupportedModelAuthTypesIncludesAzureWorkloadIdentityConstant(t *testing.T) {
	if !slices.Contains(SupportedModelAuthTypes, AuthTypeAzureWorkloadIdentity) {
		t.Errorf("SupportedModelAuthTypes is missing AuthTypeAzureWorkloadIdentity (%q)", AuthTypeAzureWorkloadIdentity)
	}
}

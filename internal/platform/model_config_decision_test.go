// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"errors"
	"strings"
	"testing"
)

func decisionCreateRequest(authConfig map[string]any) CreateModelConfigRequest {
	return CreateModelConfigRequest{
		Alias:      "jev",
		Provider:   ProviderTypeSafe,
		ModelName:  "jev-1.13.0",
		ModelType:  ModelTypeDecision,
		APIBase:    "https://api.typesafe.ai",
		AuthConfig: authConfig,
	}
}

func requireAuthConfigValidationError(t *testing.T, err error) {
	t.Helper()
	validErr, ok := errors.AsType[*ValidationError](err)
	if !ok {
		t.Fatalf("err = %v, want a *ValidationError", err)
	}
	if len(validErr.Details["authConfig"]) == 0 {
		t.Fatalf("validation details = %v, want an authConfig entry", validErr.Details)
	}
}

func TestModelConfigService_DecisionCreateRequiresAPIKey(t *testing.T) {
	cases := []struct {
		name       string
		authConfig map[string]any
	}{
		{name: "no auth config", authConfig: nil},
		{name: "auth type none", authConfig: map[string]any{"type": "none"}},
		{name: "oauth2", authConfig: map[string]any{"type": "oauth2", "client_id": "c", "client_secret": "s"}},
		{name: "apikey without a key", authConfig: map[string]any{"type": "apikey"}},
		{name: "apikey with a blank key", authConfig: map[string]any{"type": "apikey", "api_key": "  "}},
		{name: "apikey with the redacted sentinel", authConfig: map[string]any{"type": "apikey", "api_key": RedactedSentinel}},
		{name: "key without the apikey type", authConfig: map[string]any{"api_key": "ts-secret"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := newTestModelConfigService(t)
			_, err := svc.Create(t.Context(), decisionCreateRequest(tc.authConfig), "tester")
			requireAuthConfigValidationError(t, err)
		})
	}
}

func TestModelConfigService_DecisionCreateWithAPIKey(t *testing.T) {
	svc := newTestModelConfigService(t)
	resp, err := svc.Create(t.Context(), decisionCreateRequest(map[string]any{"type": "apikey", "api_key": "ts-secret"}), "tester")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if resp.ModelType != ModelTypeDecision {
		t.Errorf("modelType = %q, want decision", resp.ModelType)
	}
}

func TestModelConfigService_ChatCreateNeedsNoAPIKey(t *testing.T) {
	svc := newTestModelConfigService(t)
	_, err := svc.Create(t.Context(), CreateModelConfigRequest{
		Alias: "local", Provider: "ollama", ModelName: "llama3", APIBase: "http://localhost:11434",
		AuthConfig: map[string]any{"type": "none"},
	}, "tester")
	if err != nil {
		t.Fatalf("a chat model without an API key must still save: %v", err)
	}
}

func TestModelConfigService_DecisionUpdateKeepsAPIKey(t *testing.T) {
	cases := []struct {
		name       string
		authConfig map[string]any
		wantErr    bool
	}{
		{name: "switch to no auth", authConfig: map[string]any{"type": "none"}, wantErr: true},
		{name: "switch to oauth2", authConfig: map[string]any{"type": "oauth2", "client_id": "c", "client_secret": "s"}, wantErr: true},
		{name: "rotate the key", authConfig: map[string]any{"type": "apikey", "api_key": "ts-rotated"}},
		{name: "echo the redacted key", authConfig: map[string]any{"type": "apikey", "api_key": RedactedSentinel}},
		{name: "blank key keeps the stored one", authConfig: map[string]any{"api_key": ""}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := newTestModelConfigService(t)
			created, err := svc.Create(t.Context(), decisionCreateRequest(map[string]any{"type": "apikey", "api_key": "ts-secret"}), "tester")
			if err != nil {
				t.Fatalf("create: %v", err)
			}
			_, err = svc.Update(t.Context(), created.ID, UpdateModelConfigRequest{AuthConfig: tc.authConfig}, "tester")
			if tc.wantErr {
				requireAuthConfigValidationError(t, err)
				return
			}
			if err != nil {
				t.Fatalf("update: %v", err)
			}
			raw, err := svc.GetRawByID(t.Context(), created.ID)
			if err != nil {
				t.Fatalf("get raw: %v", err)
			}
			if !strings.Contains(raw.ModelAuthConfig, `"api_key":"ts-`) {
				t.Errorf("stored auth config lost its key: %s", raw.ModelAuthConfig)
			}
		})
	}
}

func TestModelConfigService_UpdateWithoutAuthChangeSkipsKeyCheck(t *testing.T) {
	svc := newTestModelConfigService(t)
	created, err := svc.Create(t.Context(), decisionCreateRequest(map[string]any{"type": "apikey", "api_key": "ts-secret"}), "tester")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	pinned := "jev-1.14.0"
	if _, err := svc.Update(t.Context(), created.ID, UpdateModelConfigRequest{ModelName: &pinned}, "tester"); err != nil {
		t.Fatalf("update: %v", err)
	}
}

// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"testing"
)

func TestCreate_AuthTypeTopLevelOnly(t *testing.T) {
	t.Parallel()
	svc, _ := setupModelConfigSvc(t)
	ctx := context.Background()

	resp, err := svc.Create(ctx, &CreateModelConfigRequest{
		Alias:     "toplevel-only",
		Provider:  "openai",
		ModelName: "gpt-4",
		APIBase:   "https://llm.example.com/v1",
		AuthType:  "apikey",
	}, "user1")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if resp.AuthType != "apikey" {
		t.Errorf("AuthType = %q, want apikey", resp.AuthType)
	}
	if got, _ := resp.AuthConfig["type"].(string); got != "apikey" {
		t.Errorf("authConfig.type = %q, want apikey", got)
	}
}

func TestCreate_AuthTypeFoldedIntoConfig(t *testing.T) {
	t.Parallel()
	svc, _ := setupModelConfigSvc(t)
	ctx := context.Background()

	resp, err := svc.Create(ctx, &CreateModelConfigRequest{
		Alias:     "folded",
		Provider:  "openai",
		ModelName: "gpt-4",
		APIBase:   "https://llm.example.com/v1",
		AuthType:  "apikey",
		AuthConfig: map[string]any{
			"api_key": "sk-secret",
		},
	}, "user1")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if resp.AuthType != "apikey" {
		t.Errorf("AuthType = %q, want apikey", resp.AuthType)
	}
	if got, _ := resp.AuthConfig["type"].(string); got != "apikey" {
		t.Errorf("authConfig.type = %q, want apikey (should have been folded in)", got)
	}
}

func TestCreate_AuthConfigTypeWinsOverTopLevel(t *testing.T) {
	t.Parallel()
	svc, _ := setupModelConfigSvc(t)
	ctx := context.Background()

	resp, err := svc.Create(ctx, &CreateModelConfigRequest{
		Alias:     "conflict",
		Provider:  "openai",
		ModelName: "gpt-4",
		APIBase:   "https://llm.example.com/v1",
		AuthType:  "apikey",
		AuthConfig: map[string]any{
			"type":  "oauth2",
			"token": "x",
		},
	}, "user1")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if resp.AuthType != "oauth2" {
		t.Errorf("AuthType = %q, want oauth2 (authConfig wins)", resp.AuthType)
	}
}

func TestUpdate_AuthTypeTopLevelFoldsIntoConfig(t *testing.T) {
	t.Parallel()
	svc, _ := setupModelConfigSvc(t)
	ctx := t.Context()

	created, err := svc.Create(ctx, &CreateModelConfigRequest{
		Alias:      "patch-me-foldsame",
		Provider:   "openai",
		ModelName:  "gpt-4",
		APIBase:    "https://llm.example.com/v1",
		AuthConfig: map[string]any{"type": "apikey", "api_key": "sk-original"},
	}, "user1")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	sameType := "apikey"
	updated, err := svc.Update(ctx, created.ID, &UpdateModelConfigRequest{
		AuthType: &sameType,
	}, "user1")
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.AuthType != "apikey" {
		t.Errorf("AuthType = %q, want apikey", updated.AuthType)
	}
	if got, _ := updated.AuthConfig["type"].(string); got != "apikey" {
		t.Errorf("authConfig.type = %q, want apikey (top-level should have been folded in)", got)
	}
	if got, _ := updated.AuthConfig["api_key"].(string); got != redactedSentinel {
		t.Errorf("api_key was dropped by the patch — existing creds should be preserved on a same-type fold (got %v)", updated.AuthConfig["api_key"])
	}
}

func TestUpdate_AuthTypeFlip_DropsPriorSecret(t *testing.T) {
	t.Parallel()
	svc, repo := setupModelConfigSvc(t)
	ctx := t.Context()

	created, err := svc.Create(ctx, &CreateModelConfigRequest{
		Alias:      "patch-me-flip",
		Provider:   "openai",
		ModelName:  "gpt-4",
		APIBase:    "https://llm.example.com/v1",
		AuthConfig: map[string]any{"type": "apikey", "api_key": "sk-original"},
	}, "user1")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	flipped := "oauth2"
	updated, err := svc.Update(ctx, created.ID, &UpdateModelConfigRequest{
		AuthType: &flipped,
	}, "user1")
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.AuthType != "oauth2" {
		t.Errorf("AuthType = %q, want oauth2", updated.AuthType)
	}
	if got, _ := updated.AuthConfig["type"].(string); got != "oauth2" {
		t.Errorf("authConfig.type = %q, want oauth2 (top-level must fold in)", got)
	}

	if _, present := updated.AuthConfig["api_key"]; present {
		t.Errorf("api_key still present in GET response after flip apikey -> oauth2: %v", updated.AuthConfig)
	}
	stored := storedAuthConfig(t, repo, created.ID)
	if _, present := stored["api_key"]; present {
		t.Errorf("stored auth_config still carries api_key after flip apikey -> oauth2: %v", stored)
	}
}

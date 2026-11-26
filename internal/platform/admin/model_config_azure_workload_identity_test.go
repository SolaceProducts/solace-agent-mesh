// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"errors"
	"reflect"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samapp"
)

const (
	testAzureEndpoint = "https://sam.openai.azure.com/"
	testAzureClientID = "11111111-2222-3333-4444-555555555555"
	testAzureTenantID = "66666666-7777-8888-9999-000000000000"
)

func enableAzureWorkloadIdentity(t *testing.T) {
	t.Helper()
	t.Setenv(llm.AzureWorkloadIdentityEnabledEnv, "true")
	t.Setenv(llm.AzureFederatedTokenFileEnv, "/var/run/secrets/sam/azure/token")
}

func azureWorkloadIdentityAuth(extra map[string]any) map[string]any {
	auth := map[string]any{"type": platpkg.AuthTypeAzureWorkloadIdentity, "client_id": testAzureClientID, "tenant_id": testAzureTenantID}
	for k, v := range extra {
		auth[k] = v
	}
	return auth
}

func TestModelConfigService_Create_RejectsKeyWithAzureWorkloadIdentity(t *testing.T) {
	t.Parallel()
	svc, _ := setupModelConfigSvc(t)

	_, err := svc.Create(t.Context(), &CreateModelConfigRequest{
		Alias:      "azure-wi",
		Provider:   "azure_openai",
		ModelName:  "gpt-4.1-mini",
		APIBase:    testAzureEndpoint,
		AuthConfig: azureWorkloadIdentityAuth(map[string]any{"client_secret": "s"}),
	}, "user1")

	var validationErr *ValidationError
	if !errors.As(err, &validationErr) || validationErr.Details["client_secret"] == nil {
		t.Fatalf("Create error = %v, want a ValidationError on client_secret", err)
	}
}

func TestModelConfigService_Update_AzureAuthTypeSwitchReplacesConfig(t *testing.T) {
	enableAzureWorkloadIdentity(t)
	cases := []struct {
		name    string
		initial map[string]any
		patch   map[string]any
		want    map[string]any
	}{
		{
			name:    "apikey to azure_workload_identity drops the key",
			initial: map[string]any{"type": "apikey", "api_key": "stored-key", "api_version": "2024-10-21"},
			patch:   azureWorkloadIdentityAuth(nil),
			want:    azureWorkloadIdentityAuth(nil),
		},
		{
			name:    "azure_workload_identity to apikey starts clean",
			initial: azureWorkloadIdentityAuth(map[string]any{"api_version": "2024-10-21"}),
			patch:   map[string]any{"type": "apikey", "api_key": "new-key"},
			want:    map[string]any{"type": "apikey", "api_key": "new-key"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, repo := setupModelConfigSvc(t)
			ctx := t.Context()

			created, err := svc.Create(ctx, &CreateModelConfigRequest{
				Alias:      "azure-auth-switch",
				Provider:   "azure_openai",
				ModelName:  "gpt-4.1-mini",
				APIBase:    testAzureEndpoint,
				AuthConfig: tc.initial,
			}, "user1")
			if err != nil {
				t.Fatalf("create: %v", err)
			}
			if _, err = svc.Update(ctx, created.ID, &UpdateModelConfigRequest{AuthConfig: tc.patch}, "user2"); err != nil {
				t.Fatalf("update: %v", err)
			}

			mc, err := repo.GetByID(ctx, created.ID)
			if err != nil {
				t.Fatalf("get: %v", err)
			}
			if got := parseJSONMap(mc.ModelAuthConfig); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("stored authConfig = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestBuildProbeConfig_AzureSwitchDropsStoredKey(t *testing.T) {
	env := setupModelConfigMux(t)
	created, err := env.handler.svc.Create(t.Context(), &CreateModelConfigRequest{
		Alias:      "azure-switch",
		Provider:   "azure_openai",
		ModelName:  "gpt-4.1-mini",
		APIBase:    testAzureEndpoint,
		AuthConfig: map[string]any{"type": "apikey", "api_key": "stored-key"},
	}, "user1")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	incoming := azureWorkloadIdentityAuth(nil)
	probe, err := env.handler.buildProbeConfig(t.Context(), &ModelConfigTestRequest{ModelID: created.ID, AuthConfig: incoming}, incoming)
	if err != nil {
		t.Fatalf("buildProbeConfig: %v", err)
	}
	raw := probe.raw
	if _, ok := raw["api_key"]; ok {
		t.Errorf("stored api_key survived the switch to workload identity: %v", raw)
	}
	if _, ok := raw[llm.AzureWorkloadIdentityKey]; !ok {
		t.Errorf("switched config has no %s block: %v", llm.AzureWorkloadIdentityKey, raw)
	}
}

func TestModelConfigService_GetByAliasRaw_AzureWorkloadIdentityReachesAgents(t *testing.T) {
	enableAzureWorkloadIdentity(t)
	svc, _ := setupModelConfigSvc(t)
	if _, err := svc.Create(t.Context(), &CreateModelConfigRequest{
		Alias:      "azure-wi",
		Provider:   "azure_openai",
		ModelName:  "gpt-4.1-mini",
		APIBase:    testAzureEndpoint,
		AuthConfig: azureWorkloadIdentityAuth(nil),
	}, "user1"); err != nil {
		t.Fatalf("create: %v", err)
	}

	raw, err := svc.GetByAliasRaw(t.Context(), "azure-wi")
	if err != nil {
		t.Fatalf("GetByAliasRaw: %v", err)
	}
	for _, key := range []string{"client_id", "tenant_id"} {
		if _, ok := raw[key]; ok {
			t.Errorf("%s at the top level selects a client-secret sign-in: %v", key, raw)
		}
	}

	cfg, err := samapp.ParseModelConfig(raw, nil)
	if err != nil {
		t.Fatalf("ParseModelConfig: %v", err)
	}
	want := llm.AzureWorkloadIdentity{ClientID: testAzureClientID, TenantID: testAzureTenantID}
	if cfg.AzureWorkloadIdentity == nil || *cfg.AzureWorkloadIdentity != want {
		t.Errorf("agent's AzureWorkloadIdentity = %+v, want %+v", cfg.AzureWorkloadIdentity, want)
	}
}

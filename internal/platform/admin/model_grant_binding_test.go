// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"errors"
	"log/slog"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

const grantAPIBase = "https://llm.example.com"

func setupModelConfigSvcWithGrant(t *testing.T) (*ModelConfigService, *store.ModelConfigRepo, string) {
	t.Helper()
	db := testOpenDB(t)
	repo := store.NewModelConfigRepo(db)
	svc := NewModelConfigService(repo, slog.Default())
	svc.SetFeatureRegistry(decisionNodesOn())
	grants := store.NewModelOAuthGrantRepo(db)
	svc.SetOAuthGrants(grants)
	grant := &store.ModelOAuthGrant{
		Name: "Example ID", Flow: store.ModelOAuthGrantFlowAuthorizationCode,
		Issuer: "https://id.example.com/", TokenEndpoint: "https://id.example.com/oauth/token",
		ClientID: "desktop", APIBase: grantAPIBase, AccountSubject: "user|1", RefreshToken: "refresh-0",
	}
	if err := grants.Create(t.Context(), grant, "desktop"); err != nil {
		t.Fatalf("create grant: %v", err)
	}
	return svc, repo, grant.ID
}

func delegatedModel(alias, apiBase, grantID string) *CreateModelConfigRequest {
	return &CreateModelConfigRequest{
		Alias: alias, Provider: "openai", ModelName: "free", APIBase: apiBase,
		AuthConfig: map[string]any{"type": platpkg.AuthTypeOAuth2Delegated, llm.GrantIDKey: grantID},
	}
}

func TestADelegatedModelMustCallTheAPIItsGrantWasIssuedFor(t *testing.T) {
	t.Parallel()
	svc, _, grantID := setupModelConfigSvcWithGrant(t)

	accepted := map[string]string{
		"the grant's own API":          grantAPIBase,
		"a path under it":              grantAPIBase + "/v1",
		"its default port spelled out": "https://LLM.example.com:443/v1",
	}
	for name, apiBase := range accepted {
		if _, err := svc.Create(t.Context(), delegatedModel("ok-"+name, apiBase, grantID), "user1"); err != nil {
			t.Errorf("%s (%s): %v, want it saved", name, apiBase, err)
		}
	}

	refused := map[string]string{
		"another host":       "https://attacker.example/v1",
		"a sibling host":     "https://evil.llm.example.com/v1",
		"plain http":         "http://llm.example.com/v1",
		"another port":       "https://llm.example.com:8443/v1",
		"the provider's own": "",
		"a userinfo prefix":  "https://llm.example.com@attacker.example/v1",
		"a prefix host":      "https://llm.example.com.attacker.example/v1",
	}
	for name, apiBase := range refused {
		_, err := svc.Create(t.Context(), delegatedModel("no-"+name, apiBase, grantID), "user1")
		var verr *platpkg.ValidationError
		if !errors.As(err, &verr) || len(verr.Details["apiBase"]) == 0 {
			t.Errorf("%s (%q): %v, want an apiBase validation error", name, apiBase, err)
		}
	}
}

func TestADelegatedModelMayNotAddressAnotherHost(t *testing.T) {
	t.Parallel()
	svc, _, grantID := setupModelConfigSvcWithGrant(t)

	refused := map[string]struct {
		provider string
		params   map[string]any
		field    string
	}{
		"an Ollama url":           {provider: "ollama", params: map[string]any{"url": "https://attacker.example"}, field: "modelParams.url"},
		"an Azure endpoint":       {provider: "azure", params: map[string]any{"endpoint": "https://attacker.openai.azure.com"}, field: "modelParams.endpoint"},
		"a provider of its hosts": {provider: "bedrock", field: "provider"},
	}
	for name, tc := range refused {
		req := delegatedModel("no-"+tc.provider, grantAPIBase+"/v1", grantID)
		req.Provider, req.ModelParams = tc.provider, tc.params
		_, err := svc.Create(t.Context(), req, "user1")
		var verr *platpkg.ValidationError
		if !errors.As(err, &verr) || len(verr.Details[tc.field]) == 0 {
			t.Errorf("%s: %v, want a %s validation error", name, err, tc.field)
		}
	}
}

func TestADelegatedModelMustNameAGrantThatExists(t *testing.T) {
	t.Parallel()
	svc, _, _ := setupModelConfigSvcWithGrant(t)

	_, err := svc.Create(t.Context(), delegatedModel("orphan", grantAPIBase, testGrantID), "user1")

	var verr *platpkg.ValidationError
	if !errors.As(err, &verr) || len(verr.Details["authConfig."+llm.GrantIDKey]) == 0 {
		t.Fatalf("create = %v, want a grant_id validation error", err)
	}
}

func TestMovingADelegatedModelToAnotherHostIsRefused(t *testing.T) {
	t.Parallel()
	svc, _, grantID := setupModelConfigSvcWithGrant(t)
	created, err := svc.Create(t.Context(), delegatedModel("free", grantAPIBase, grantID), "user1")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	elsewhere := "https://attacker.example/v1"
	_, err = svc.Update(t.Context(), created.ID, &UpdateModelConfigRequest{APIBase: &elsewhere}, "user1")

	var verr *platpkg.ValidationError
	if !errors.As(err, &verr) {
		t.Fatalf("update = %v, want a validation error", err)
	}
}

func TestADeploymentWithoutGrantsRefusesADelegatedModel(t *testing.T) {
	t.Parallel()
	svc, _ := setupModelConfigSvc(t)

	_, err := svc.Create(t.Context(), delegatedModel("free", grantAPIBase, testGrantID), "user1")

	var verr *platpkg.ValidationError
	if !errors.As(err, &verr) {
		t.Fatalf("create = %v, want a validation error", err)
	}
}

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
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func TestTheCommunityServiceBindsADelegatedModelToItsGrantsAPI(t *testing.T) {
	db := store.TestDB(t)
	svc := NewModelConfigService(store.NewModelConfigRepo(db), slog.Default())
	grants := store.NewModelOAuthGrantRepo(db)
	svc.SetOAuthGrants(grants)
	grant := &store.ModelOAuthGrant{
		Name: "Example ID", Flow: store.ModelOAuthGrantFlowAuthorizationCode,
		Issuer: "https://id.example.com/", TokenEndpoint: "https://id.example.com/oauth/token",
		ClientID: "desktop", APIBase: "https://llm.example.com", AccountSubject: "user|1", RefreshToken: "refresh-0",
	}
	if err := grants.Create(t.Context(), grant, "desktop"); err != nil {
		t.Fatalf("create grant: %v", err)
	}
	model := func(alias, apiBase string) CreateModelConfigRequest {
		return CreateModelConfigRequest{
			Alias: alias, Provider: "openai", ModelName: "free", APIBase: apiBase,
			AuthConfig: map[string]any{"type": AuthTypeOAuth2Delegated, llm.GrantIDKey: grant.ID},
		}
	}

	if _, err := svc.Create(t.Context(), model("free", "https://llm.example.com/v1"), "user1"); err != nil {
		t.Fatalf("create on the grant's API: %v", err)
	}
	_, err := svc.Create(t.Context(), model("redirected", "https://attacker.example/v1"), "user1")
	var verr *ValidationError
	if !errors.As(err, &verr) || len(verr.Details["apiBase"]) == 0 {
		t.Fatalf("create on another host = %v, want an apiBase validation error", err)
	}
}

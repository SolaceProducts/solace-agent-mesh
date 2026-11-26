// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"log/slog"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/tokenbroker"
)

func freeEditionHome(t *testing.T) (*store.DB, *store.ModelConfigRepo, map[string]string) {
	t.Helper()
	ctx := t.Context()
	db := testOpenDB(t)
	repo := store.NewModelConfigRepo(db)
	if err := SeedModelConfigs(ctx, repo, nil, slog.Default()); err != nil {
		t.Fatalf("seed: %v", err)
	}
	grant := &store.ModelOAuthGrant{
		ID: llm.FreeModelGrantID, Name: "free model", Flow: store.ModelOAuthGrantFlowAuthorizationCode,
		Issuer: "https://id.example.com/", TokenEndpoint: "https://id.example.com/oauth/token",
		ClientID: "desktop", APIBase: grantAPIBase, AccountSubject: "user|1", RefreshToken: "refresh-0",
	}
	if err := store.NewModelOAuthGrantRepo(db).Create(ctx, grant, "system"); err != nil {
		t.Fatalf("create grant: %v", err)
	}
	ids := map[string]string{}
	for _, alias := range defaultModelAliases {
		mc, err := repo.GetByAlias(ctx, alias)
		if err != nil || mc == nil {
			t.Fatalf("get %s: %v", alias, err)
		}
		apiBase := grantAPIBase + "/v1"
		mc.Provider, mc.ModelName, mc.APIBase = "openai", "free-"+alias, &apiBase
		mc.ModelAuthType = platpkg.AuthTypeOAuth2Delegated
		mc.ModelAuthConfig = `{"type":"oauth2_delegated","grant_id":"` + llm.FreeModelGrantID + `"}`
		if err := repo.Update(ctx, mc); err != nil {
			t.Fatalf("point %s at the free model: %v", alias, err)
		}
		ids[alias] = mc.ID
	}
	own := &store.ModelConfiguration{
		Alias: "mine", Provider: "anthropic", ModelName: "claude", ModelAuthType: "apikey",
		ModelAuthConfig: `{"type":"apikey","api_key":"sk-own"}`, ModelParams: "{}", CreatedBy: "user1", UpdatedBy: "user1",
	}
	if err := repo.Create(ctx, own); err != nil {
		t.Fatalf("create own model: %v", err)
	}
	return db, repo, ids
}

func TestRetiringTheFreeModelGrantLeavesAFreshInstallsModels(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	db, repo, ids := freeEditionHome(t)

	retireGrants(ctx, repo, tokenbroker.New(db, slog.Default()), []string{llm.FreeModelGrantID}, slog.Default())

	if grant, err := store.NewModelOAuthGrantRepo(db).Get(ctx, llm.FreeModelGrantID); err != nil || grant != nil {
		t.Fatalf("grant after retiring = %+v, %v; want it removed with its refresh token", grant, err)
	}
	for alias, id := range ids {
		mc, err := repo.GetByAlias(ctx, alias)
		if err != nil || mc == nil {
			t.Fatalf("get %s: %v", alias, err)
		}
		if mc.ID != id {
			t.Errorf("%s id = %s, want %s kept so agents naming it by id still find it", alias, mc.ID, id)
		}
		if isConfigured(mc) || mc.APIBase != nil || mc.ModelAuthType != "none" || parseJSONMap(mc.ModelAuthConfig)[llm.GrantIDKey] != nil {
			t.Errorf("%s = %s %s %v %s %s, want the unconfigured default a fresh install has", alias, mc.Provider, mc.ModelName, mc.APIBase, mc.ModelAuthType, mc.ModelAuthConfig)
		}
	}
	own, err := repo.GetByAlias(ctx, "mine")
	if err != nil || own == nil || own.ModelName != "claude" || own.ModelAuthConfig != `{"type":"apikey","api_key":"sk-own"}` {
		t.Errorf("own model after retiring = %+v, %v; want it untouched", own, err)
	}
}

func TestRetiringAGrantTwiceChangesNothingMore(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	db, repo, _ := freeEditionHome(t)
	grants := tokenbroker.New(db, slog.Default())
	retireGrants(ctx, repo, grants, []string{llm.FreeModelGrantID}, slog.Default())
	before := modelsByAlias(ctx, t, repo)

	retireGrants(ctx, repo, grants, []string{llm.FreeModelGrantID}, slog.Default())

	after := modelsByAlias(ctx, t, repo)
	for alias, mc := range before {
		if after[alias] == nil || after[alias].UpdatedTime != mc.UpdatedTime {
			t.Errorf("%s was written again by a second start", alias)
		}
	}
}

func modelsByAlias(ctx context.Context, t *testing.T, repo *store.ModelConfigRepo) map[string]*store.ModelConfiguration {
	t.Helper()
	all, err := repo.GetAll(ctx)
	if err != nil {
		t.Fatalf("list models: %v", err)
	}
	byAlias := make(map[string]*store.ModelConfiguration, len(all))
	for _, mc := range all {
		byAlias[mc.Alias] = mc
	}
	return byAlias
}

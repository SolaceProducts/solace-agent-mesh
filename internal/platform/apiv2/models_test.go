// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"net/http"
	"net/http/httptest"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

const (
	modelSecret     = "sk-the-test-supplies-this"
	unknownAuthType = "custom_vault"
)

func newModelAPI(t *testing.T) (*entrypointAPI, *entplatform.ModelConfigService) {
	t.Helper()
	core, deps := newTestCore(t)
	return &entrypointAPI{apiClient: newAPIClient(t, core)}, deps.ModelConfigSvc
}

func createModel(api *entrypointAPI, alias string, authConfig map[string]any) Model {
	api.t.Helper()
	rec := api.call(http.MethodPost, "/models", map[string]any{
		"alias": alias, "provider": "openai", "modelName": "gpt-4o",
		"authConfig": authConfig,
	})
	return expectCreated[Model](api.t, rec, "models")
}

func apiKeyAuth() map[string]any {
	return map[string]any{"type": "apikey", "api_key": modelSecret}
}

func unknownTypeAuth() map[string]any {
	return map[string]any{"type": unknownAuthType, "secret_token": modelSecret, "region": "ca-central-1"}
}

func expectNoSecret(t *testing.T, rec *httptest.ResponseRecorder, authConfig map[string]any, key string) {
	t.Helper()
	if strings.Contains(rec.Body.String(), modelSecret) {
		t.Errorf("the stored credential reached the response: %s", rec.Body.String())
	}
	if got := authConfig[key]; got != redacted {
		t.Errorf("authConfig[%q] = %v, want %q", key, got, redacted)
	}
}

func TestModels_Lifecycle(t *testing.T) {
	api, _ := newModelAPI(t)

	rec := api.call(http.MethodPost, "/models", map[string]any{
		"alias": "support", "provider": "openai", "modelName": "gpt-4o",
		"authType": "apikey", "authConfig": apiKeyAuth(),
	})
	created := expectCreated[Model](t, rec, "models")
	if created.Type != "model" {
		t.Errorf("type = %q, want model", created.Type)
	}
	if created.AuthConfig["type"] != "apikey" {
		t.Errorf("authConfig.type = %v, want it unredacted so the edit form can read it", created.AuthConfig["type"])
	}
	expectNoSecret(t, rec, created.AuthConfig, "api_key")

	raw := rawData(t, rec)
	for _, key := range []string{"description", "maxInputTokens", "pricing", "apiBase"} {
		if _, present := raw[key]; !present {
			t.Errorf("key %q is absent; an empty field must be null", key)
		}
	}
	expectISOTimes(t, raw, "createdTime", "updatedTime")
	expectAbsent(t, raw, "createdAt", "updatedAt")

	got := api.call(http.MethodGet, "/models/"+created.ID, nil)
	expectStatus(t, got, http.StatusOK)
	if fetched := decodeData[Model](t, got); fetched.Alias != "support" {
		t.Errorf("alias = %q, want support", fetched.Alias)
	}

	listed := decodeList[Model](t, api.call(http.MethodGet, "/models", nil))
	if !slices.ContainsFunc(listed.Data, func(m Model) bool { return m.ID == created.ID }) {
		t.Errorf("list is missing the created model: %+v", listed.Data)
	}

	patched := api.call(http.MethodPatch, "/models/"+created.ID, map[string]any{
		"description": "Fast model for support replies", "maxInputTokens": 128000,
	})
	expectStatus(t, patched, http.StatusOK)
	updated := decodeData[Model](t, patched)
	if updated.Description == nil || *updated.Description != "Fast model for support replies" {
		t.Errorf("description = %v, want the patched sentence", updated.Description)
	}
	if updated.MaxInputTokens == nil || *updated.MaxInputTokens != 128000 {
		t.Errorf("maxInputTokens = %v, want 128000", updated.MaxInputTokens)
	}

	expectStatus(t, api.call(http.MethodDelete, "/models/"+created.ID, nil), http.StatusNoContent)
	expectStatus(t, api.call(http.MethodGet, "/models/"+created.ID, nil), http.StatusNotFound)
}

func TestModels_AliasTakenIs409(t *testing.T) {
	api, _ := newModelAPI(t)
	createModel(api, "taken", apiKeyAuth())

	rec := api.call(http.MethodPost, "/models", map[string]any{
		"alias": "taken", "provider": "openai", "modelName": "gpt-4o", "authType": "apikey",
	})
	expectStatus(t, rec, http.StatusConflict)
	if got, want := decodeError(t, rec).Message, "A model with that alias already exists"; got != want {
		t.Errorf("message = %q, want %q", got, want)
	}
}

func TestModels_RenamingToAnAliasInUseIs409(t *testing.T) {
	api, _ := newModelAPI(t)
	createModel(api, "taken", apiKeyAuth())
	other := createModel(api, "other", apiKeyAuth())

	rec := api.call(http.MethodPatch, "/models/"+other.ID, map[string]any{"alias": "taken"})
	expectStatus(t, rec, http.StatusConflict)
	if got, want := decodeError(t, rec).Message, "The alias is already used by another model, or a workflow still refers to this model by its current alias"; got != want {
		t.Errorf("message = %q, want %q", got, want)
	}

	if got := decodeData[Model](t, api.call(http.MethodGet, "/models/"+other.ID, nil)).Alias; got != "other" {
		t.Errorf("alias = %q after the refused rename, want it unchanged", got)
	}
}

func TestModels_SeededPlaceholdersSendNullNotEmptyString(t *testing.T) {
	api, _ := newModelAPI(t)

	rec := api.call(http.MethodGet, "/models", nil)
	expectStatus(t, rec, http.StatusOK)

	var seen bool
	for _, m := range decodeList[Model](t, rec).Data {
		if m.Alias != "general" {
			continue
		}
		seen = true
		if m.Provider != nil {
			t.Errorf("provider = %#v, want null for a placeholder", *m.Provider)
		}
		if m.ModelName != nil {
			t.Errorf("modelName = %#v, want null for a placeholder", *m.ModelName)
		}
	}
	if !seen {
		t.Fatal("the seeded general placeholder was not listed")
	}
}

func TestModels_RedactsTheCredentialOnEveryResponsePath(t *testing.T) {
	api, svc := newModelAPI(t)

	created := seedUnregisteredTypeModel(t, svc, "unknown-type", "", unknownTypeAuth())
	rec := api.call(http.MethodGet, "/models/"+created.ID, nil)
	expectStatus(t, rec, http.StatusOK)
	created = decodeData[Model](t, rec)
	expectNoSecret(t, rec, created.AuthConfig, "secret_token")
	if created.AuthConfig["type"] != unknownAuthType {
		t.Errorf("authConfig.type = %v, want it unredacted", created.AuthConfig["type"])
	}
	if created.AuthConfig["region"] != redacted {
		t.Errorf("region = %v, want it redacted: an unknown auth type redacts every value", created.AuthConfig["region"])
	}

	page := api.call(http.MethodGet, "/models", nil)
	expectStatus(t, page, http.StatusOK)
	for _, m := range decodeList[Model](t, page).Data {
		if m.ID == created.ID {
			expectNoSecret(t, page, m.AuthConfig, "secret_token")
		}
	}

	patched := api.call(http.MethodPatch, "/models/"+created.ID, map[string]any{"description": "Vault-backed model"})
	expectStatus(t, patched, http.StatusOK)
	expectNoSecret(t, patched, decodeData[Model](t, patched).AuthConfig, "secret_token")
}

func TestModels_PatchingBackARedactedAuthConfigKeepsTheStoredSecret(t *testing.T) {
	api, svc := newModelAPI(t)
	created := seedUnregisteredTypeModel(t, svc, "round-trip", "", unknownTypeAuth())

	got := api.call(http.MethodGet, "/models/"+created.ID, nil)
	expectStatus(t, got, http.StatusOK)
	returned := decodeData[Model](t, got).AuthConfig

	rec := api.call(http.MethodPatch, "/models/"+created.ID, map[string]any{"authConfig": returned})
	expectStatus(t, rec, http.StatusOK)

	_, stored, err := svc.GetAuthConfigCleartext(t.Context(), created.ID)
	if err != nil {
		t.Fatalf("read the stored auth config: %v", err)
	}
	if stored["secret_token"] != modelSecret {
		t.Errorf("stored secret_token = %v, want the credential the create stored", stored["secret_token"])
	}
	if stored["region"] != "ca-central-1" {
		t.Errorf("stored region = %v, want the value the create stored", stored["region"])
	}
}

func TestModels_PatchingBackARedactedSecretKeepsAKnownTypesCredential(t *testing.T) {
	api, svc := newModelAPI(t)
	created := createModel(api, "known-round-trip", apiKeyAuth())

	got := api.call(http.MethodGet, "/models/"+created.ID, nil)
	expectStatus(t, got, http.StatusOK)

	rec := api.call(http.MethodPatch, "/models/"+created.ID, map[string]any{"authConfig": decodeData[Model](t, got).AuthConfig})
	expectStatus(t, rec, http.StatusOK)

	_, stored, err := svc.GetAuthConfigCleartext(t.Context(), created.ID)
	if err != nil {
		t.Fatalf("read the stored auth config: %v", err)
	}
	if stored["api_key"] != modelSecret {
		t.Errorf("stored api_key = %v, want the credential the create stored", stored["api_key"])
	}
}

func TestModels_UpdateReplacesACredentialTheCallerRetypes(t *testing.T) {
	api, svc := newModelAPI(t)
	created := createModel(api, "rotate", apiKeyAuth())

	rec := api.call(http.MethodPatch, "/models/"+created.ID, map[string]any{
		"authConfig": map[string]any{"type": "apikey", "api_key": "sk-the-rotated-one"},
	})
	expectStatus(t, rec, http.StatusOK)

	_, stored, err := svc.GetAuthConfigCleartext(t.Context(), created.ID)
	if err != nil {
		t.Fatalf("read the stored auth config: %v", err)
	}
	if stored["api_key"] != "sk-the-rotated-one" {
		t.Errorf("stored api_key = %v, want the rotated credential", stored["api_key"])
	}
}

func TestModels_UpdateFieldRules(t *testing.T) {
	api, _ := newModelAPI(t)
	created := createModel(api, "patch-rules", apiKeyAuth())
	path := "/models/" + created.ID

	for _, tc := range []struct {
		name  string
		body  string
		field string
	}{
		{"alias cannot be cleared", `{"alias":null}`, "alias"},
		{"provider cannot be cleared", `{"provider":null}`, "provider"},
		{"modelName cannot be cleared", `{"modelName":null}`, "modelName"},
		{"apiBase cannot be cleared", `{"apiBase":null}`, "apiBase"},
		{"authType cannot be cleared", `{"authType":null}`, "authType"},
		{"authConfig cannot be cleared", `{"authConfig":null}`, "authConfig"},
		{"modelParams cannot be cleared", `{"modelParams":null}`, "modelParams"},
		{"modelType is fixed at create", `{"modelType":"decision"}`, "modelType"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			expectDetail(t, api.call(http.MethodPatch, path, tc.body), http.StatusUnprocessableEntity, tc.field)
		})
	}

	t.Run("description and maxInputTokens clear on null", func(t *testing.T) {
		expectStatus(t, api.call(http.MethodPatch, path, map[string]any{"description": "set", "maxInputTokens": 4096}), http.StatusOK)
		rec := api.call(http.MethodPatch, path, `{"description":null,"maxInputTokens":null}`)
		expectStatus(t, rec, http.StatusOK)
		cleared := decodeData[Model](t, rec)
		if cleared.Description != nil || cleared.MaxInputTokens != nil {
			t.Errorf("description = %v and maxInputTokens = %v, want both null", cleared.Description, cleared.MaxInputTokens)
		}
	})
}

func TestModels_ListPagesAndSorts(t *testing.T) {
	api, _ := newModelAPI(t)
	for _, alias := range []string{"alpha", "bravo", "charlie"} {
		createModel(api, alias, apiKeyAuth())
	}

	page := decodeList[Model](t, api.call(http.MethodGet, "/models?pageNumber=1&pageSize=2&sort=alias:asc", nil))
	if len(page.Data) != 2 || page.Data[0].Alias != "alpha" || page.Data[1].Alias != "bravo" {
		t.Errorf("first page = %v, want alpha and bravo", aliases(page.Data))
	}
	if page.Meta.Pagination.Count != 5 {
		t.Errorf("total = %d, want 5: three created plus the two seeded placeholders", page.Meta.Pagination.Count)
	}

	second := decodeList[Model](t, api.call(http.MethodGet, "/models?pageNumber=2&pageSize=2&sort=alias:asc", nil))
	if len(second.Data) != 2 || second.Data[0].Alias != "charlie" {
		t.Errorf("second page = %v, want charlie first", aliases(second.Data))
	}

	descending := decodeList[Model](t, api.call(http.MethodGet, "/models?pageSize=1&sort=alias:desc", nil))
	if len(descending.Data) != 1 || descending.Data[0].Alias != "planning" {
		t.Errorf("descending page = %v, want planning", aliases(descending.Data))
	}
}

func TestModels_ListDefaultOrderIsNewestFirst(t *testing.T) {
	api, _ := newModelAPI(t)
	var last Instant
	for _, m := range decodeList[Model](t, api.call(http.MethodGet, "/models?pageSize=100", nil)).Data {
		last = max(last, m.CreatedTime)
	}
	for _, alias := range []string{"alpha", "bravo", "charlie"} {
		for Instant(time.Now().UnixMilli()) <= last {
			runtime.Gosched()
		}
		last = createModel(api, alias, apiKeyAuth()).CreatedTime
	}

	def := decodeList[Model](t, api.call(http.MethodGet, "/models?pageSize=3", nil))
	if got := aliases(def.Data); !slices.Equal(got, []string{"charlie", "bravo", "alpha"}) {
		t.Errorf("default order = %v, want newest first: charlie, bravo, alpha", got)
	}

	asc := decodeList[Model](t, api.call(http.MethodGet, "/models?pageSize=100&sort=createdTime:asc", nil))
	if got := aliases(asc.Data); len(got) != 5 || !slices.Equal(got[2:], []string{"alpha", "bravo", "charlie"}) {
		t.Errorf("createdTime:asc = %v, want the seeded aliases first and charlie last", got)
	}
	desc := decodeList[Model](t, api.call(http.MethodGet, "/models?pageSize=3&sort=createdTime:desc", nil))
	if got := aliases(desc.Data); !slices.Equal(got, []string{"charlie", "bravo", "alpha"}) {
		t.Errorf("createdTime:desc = %v, want charlie, bravo, alpha", got)
	}
}

func aliases(models []Model) []string {
	out := make([]string, len(models))
	for i, m := range models {
		out[i] = m.Alias
	}
	return out
}

func TestModels_ListRejectsAPageTheServiceCannotQuery(t *testing.T) {
	api, _ := newModelAPI(t)
	for _, tc := range []struct {
		query  string
		field  string
		status int
	}{
		{"?pageSize=0", "pageSize", http.StatusUnprocessableEntity},
		{"?pageSize=-1", "pageSize", http.StatusUnprocessableEntity},
		{"?pageSize=101", "pageSize", http.StatusUnprocessableEntity},
		{"?pageNumber=0", "pageNumber", http.StatusUnprocessableEntity},
		{"?pageNumber=-3", "pageNumber", http.StatusUnprocessableEntity},
		{"?pageSize=many", "pageSize", http.StatusBadRequest},
		{"?sort=alias:up", "sort", http.StatusBadRequest},
		{"?sort=provider:asc", "sort", http.StatusUnprocessableEntity},
	} {
		t.Run(tc.query, func(t *testing.T) {
			expectDetail(t, api.call(http.MethodGet, "/models"+tc.query, nil), tc.status, tc.field)
		})
	}
}

func TestModels_CatalogReadsAreOpenAndWritesAreNot(t *testing.T) {
	api, _ := newModelAPI(t)
	created := createModel(api, "open-read", apiKeyAuth())

	expectStatus(t, api.call(http.MethodGet, "/models", nil, rbac.ScopeAgentBuilderRead), http.StatusOK)
	expectStatus(t, api.call(http.MethodGet, "/models/"+created.ID, nil, rbac.ScopeAgentBuilderRead), http.StatusOK)

	for _, tc := range []struct {
		method, path string
		body         any
	}{
		{http.MethodPost, "/models", `{}`},
		{http.MethodPatch, "/models/" + created.ID, `{}`},
		{http.MethodDelete, "/models/" + created.ID, nil},
	} {
		rec := api.call(tc.method, tc.path, tc.body, rbac.ScopeAgentBuilderRead)
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s %s with only %s = %d, want 403", tc.method, tc.path, rbac.ScopeAgentBuilderRead, rec.Code)
		}
	}
}

func TestModels_APIKeyRoutes(t *testing.T) {
	got := apiKeyRoutesTagged(t, modelsTag, modelStatusTag, providersTag, supportedModelsTag)
	want := []string{"DELETE /models/{id}", "GET /models", "GET /models/{id}", "GET /models/{id}/dependents", "PATCH /models/{id}", "POST /models", "PUT /models/{id}/secretsDiff"}
	if !slices.Equal(got, want) {
		t.Errorf("API key routes = %v, want %v", got, want)
	}
}

func TestModels_ScopeStory(t *testing.T) {
	core, _ := newTestCore(t)
	api := &entrypointAPI{apiClient: newAPIClient(t, core)}
	created := createModel(api, "scope-story", apiKeyAuth())

	bare := &entrypointAPI{apiClient: &apiClient{t: t, core: core, handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		dev := auth.DevIdentity()
		identity := auth.NewTestSubject(auth.SubjectFields{
			UserID: dev.UserID(), Email: dev.Email(), Name: dev.Name(), Authenticated: true, AuthMethod: dev.AuthMethod(),
		})
		core.Handler().ServeHTTP(w, r.WithContext(auth.ContextWithSubject(r.Context(), identity)))
	})}}

	for _, path := range []string{"/models", "/models/" + created.ID, "/modelStatus"} {
		if rec := bare.call(http.MethodGet, path, nil); rec.Code != http.StatusOK {
			t.Errorf("GET %s with no scopes = %d, want 200: the catalog reads are open", path, rec.Code)
		}
	}

	t.Run("every other route refuses a caller with no scopes", func(t *testing.T) {
		for _, route := range []struct{ method, path string }{
			{http.MethodPost, "/models"},
			{http.MethodPatch, "/models/" + created.ID},
			{http.MethodDelete, "/models/" + created.ID},
			{http.MethodGet, "/models/" + created.ID + "/dependents"},
			{http.MethodPut, "/models/testConnection"},
			{http.MethodPut, "/models/" + created.ID + "/secretsDiff"},
			{http.MethodPut, "/supportedModels"},
			{http.MethodPut, "/providers/openai/models"},
			{http.MethodPut, "/providers/openai/params"},
		} {
			if rec := bare.call(route.method, route.path, `{}`); rec.Code != http.StatusForbidden {
				t.Errorf("%s %s with no scopes = %d, want 403", route.method, route.path, rec.Code)
			}
		}
	})

	t.Run("a read-only caller is refused every write", func(t *testing.T) {
		for _, route := range []struct{ method, path string }{
			{http.MethodPost, "/models"},
			{http.MethodPatch, "/models/" + created.ID},
			{http.MethodDelete, "/models/" + created.ID},
			{http.MethodGet, "/models/" + created.ID + "/dependents"},
			{http.MethodPut, "/models/testConnection"},
			{http.MethodPut, "/models/" + created.ID + "/secretsDiff"},
			{http.MethodPut, "/supportedModels"},
			{http.MethodPut, "/providers/openai/models"},
			{http.MethodPut, "/providers/openai/params"},
		} {
			if rec := api.call(route.method, route.path, `{}`, rbac.ScopeModelConfigRead); rec.Code != http.StatusForbidden {
				t.Errorf("%s %s with only the read scope = %d, want 403", route.method, route.path, rec.Code)
			}
		}
	})

	t.Run("dependents takes the delete scope and no other verb", func(t *testing.T) {
		path := "/models/" + created.ID + "/dependents"
		expectStatus(t, api.call(http.MethodGet, path, nil, rbac.ScopeModelConfigDelete), http.StatusOK)
		for _, scope := range []string{rbac.ScopeModelConfigCreate, rbac.ScopeModelConfigUpdate} {
			expectStatus(t, api.call(http.MethodGet, path, nil, scope), http.StatusForbidden)
		}
	})
}

func TestModels_AuthorFieldsCarryTheCallerAndTheSeeder(t *testing.T) {
	api, _ := newModelAPI(t)
	created := createModel(api, "authored", apiKeyAuth())
	if created.CreatedBy == nil || *created.CreatedBy == "" {
		t.Errorf("createdBy = %v, want the caller", created.CreatedBy)
	}
	if created.UpdatedBy == nil || *created.UpdatedBy != *created.CreatedBy {
		t.Errorf("updatedBy = %v, want the creating caller", created.UpdatedBy)
	}

	for _, m := range decodeList[Model](t, api.call(http.MethodGet, "/models", nil)).Data {
		if m.Alias != "general" {
			continue
		}
		if m.CreatedBy == nil || *m.CreatedBy != "system" {
			t.Errorf("the seeded placeholder's createdBy = %v, want system", m.CreatedBy)
		}
	}
}

func TestModels_Pricing(t *testing.T) {
	api, _ := newModelAPI(t)

	rec := api.call(http.MethodPost, "/models", map[string]any{
		"alias": "priced", "provider": "openai", "modelName": "gpt-4o", "authConfig": apiKeyAuth(),
		"pricing": map[string]any{"input": "2.5", "output": "10", "cacheRead": "0.25", "cacheWrite": "3.125"},
	})
	created := expectCreated[Model](t, rec, "models")
	if created.Pricing == nil || created.Pricing.Input == nil || *created.Pricing.Input != "2.5" {
		t.Fatalf("pricing = %+v, want the rates the create sent", created.Pricing)
	}
	if created.Pricing.CacheRead == nil || *created.Pricing.CacheRead != "0.25" {
		t.Errorf("cacheRead = %v, want the rate the create sent", created.Pricing.CacheRead)
	}

	t.Run("an object replaces the whole price", func(t *testing.T) {
		patched := api.call(http.MethodPatch, "/models/"+created.ID, map[string]any{
			"pricing": map[string]any{"input": "5", "output": "20"},
		})
		expectStatus(t, patched, http.StatusOK)
		price := decodeData[Model](t, patched).Pricing
		if price == nil || price.Input == nil || *price.Input != "5" {
			t.Fatalf("input = %+v, want the patched rate", price)
		}
		if price.CacheRead != nil {
			t.Errorf("cacheRead = %v, want it cleared: a rate the object omits is dropped", *price.CacheRead)
		}
	})

	t.Run("a half price is rejected", func(t *testing.T) {
		expectDetail(t, api.call(http.MethodPatch, "/models/"+created.ID, map[string]any{
			"pricing": map[string]any{"input": "5"},
		}), http.StatusUnprocessableEntity, "pricing.output")
	})

	t.Run("an unknown rate is rejected", func(t *testing.T) {
		expectDetailSaying(t, api.call(http.MethodPatch, "/models/"+created.ID, `{"pricing":{"inpt":"5","output":"20"}}`),
			"pricing.inpt", "unknown rate")
	})

	t.Run("a rate that is not a string is rejected", func(t *testing.T) {
		expectDetailSaying(t, api.call(http.MethodPatch, "/models/"+created.ID, `{"pricing":{"input":5,"output":"20"}}`),
			"pricing.input", "must be a string")
	})

	t.Run("a pricing that is not an object is rejected", func(t *testing.T) {
		expectDetail(t, api.call(http.MethodPatch, "/models/"+created.ID, `{"pricing":5}`),
			http.StatusBadRequest, "pricing")
	})

	t.Run("null leaves the model unpriced", func(t *testing.T) {
		patched := api.call(http.MethodPatch, "/models/"+created.ID, `{"pricing":null}`)
		expectStatus(t, patched, http.StatusOK)
		if price := decodeData[Model](t, patched).Pricing; price != nil {
			t.Errorf("pricing = %+v, want null", price)
		}
	})

	t.Run("a body with no pricing keeps the price", func(t *testing.T) {
		expectStatus(t, api.call(http.MethodPatch, "/models/"+created.ID, map[string]any{
			"pricing": map[string]any{"input": "2.5", "output": "10"},
		}), http.StatusOK)
		patched := api.call(http.MethodPatch, "/models/"+created.ID, map[string]any{"description": "still priced"})
		expectStatus(t, patched, http.StatusOK)
		if price := decodeData[Model](t, patched).Pricing; price == nil || price.Output == nil || *price.Output != "10" {
			t.Errorf("pricing = %+v, want the stored rates", price)
		}
	})
}

func TestModels_UpdateWithNoFieldChangesNothing(t *testing.T) {
	api, svc := newModelAPI(t)
	created := createModel(api, "untouched", apiKeyAuth())

	const otherUser = "someone.else@example.com"
	description := "written by another user"
	if _, err := svc.Update(t.Context(), created.ID,
		&entplatform.UpdateModelConfigRequest{Description: &description, DescriptionSet: true}, otherUser); err != nil {
		t.Fatalf("write the model as another user: %v", err)
	}

	rec := api.call(http.MethodPatch, "/models/"+created.ID, `{}`)
	expectStatus(t, rec, http.StatusOK)
	if got := decodeData[Model](t, rec); got.UpdatedBy == nil || *got.UpdatedBy != otherUser {
		t.Errorf("updatedBy = %q, want %q: a body addressing no field must not restamp the model", orEmpty(got.UpdatedBy), otherUser)
	}
}

func modelUpdatedOutboxEvents(t *testing.T, api *entrypointAPI, modelID string) int {
	t.Helper()
	rows, err := dbutil.QueryRows[struct {
		ID string `db:"id"`
	}](t.Context(), api.core.DB(),
		`SELECT id FROM outbox_events WHERE event_type = ? AND aggregate_id = ?`, "model.updated", modelID)
	if err != nil {
		t.Fatalf("read the outbox: %v", err)
	}
	return len(rows)
}

func TestModels_PatchOfAnEmptyAuthConfigIsAChange(t *testing.T) {
	api, svc := newModelAPI(t)
	created := createModel(api, "empty-auth", apiKeyAuth())

	const otherUser = "someone.else@example.com"
	description := "written by another user"
	if _, err := svc.Update(t.Context(), created.ID,
		&entplatform.UpdateModelConfigRequest{Description: &description, DescriptionSet: true}, otherUser); err != nil {
		t.Fatalf("write the model as another user: %v", err)
	}

	rec := api.call(http.MethodPatch, "/models/"+created.ID, `{"authConfig":{}}`)
	expectStatus(t, rec, http.StatusOK)
	if got := decodeData[Model](t, rec); got.UpdatedBy != nil && *got.UpdatedBy == otherUser {
		t.Errorf("updatedBy = %q: an empty authConfig is a change and must restamp the model", *got.UpdatedBy)
	}
}

func TestModels_PatchOfOnlyRedactedValuesChangesNothing(t *testing.T) {
	api, svc := newModelAPI(t)
	created := createModel(api, "sentinel-only", apiKeyAuth())

	const otherUser = "someone.else@example.com"
	description := "written by another user"
	if _, err := svc.Update(t.Context(), created.ID,
		&entplatform.UpdateModelConfigRequest{Description: &description, DescriptionSet: true}, otherUser); err != nil {
		t.Fatalf("write the model as another user: %v", err)
	}
	before := decodeData[Model](t, api.call(http.MethodGet, "/models/"+created.ID, nil))
	eventsBefore := modelUpdatedOutboxEvents(t, api, created.ID)

	rec := api.call(http.MethodPatch, "/models/"+created.ID, map[string]any{
		"authConfig": map[string]any{"api_key": redacted},
	})
	expectStatus(t, rec, http.StatusOK)

	got := decodeData[Model](t, rec)
	if got.UpdatedBy == nil || *got.UpdatedBy != otherUser {
		t.Errorf("updatedBy = %q, want %q: a patch of only the sentinel must not restamp the model", orEmpty(got.UpdatedBy), otherUser)
	}
	if got.UpdatedTime != before.UpdatedTime {
		t.Errorf("updatedTime = %v, want %v unchanged", got.UpdatedTime, before.UpdatedTime)
	}
	if after := modelUpdatedOutboxEvents(t, api, created.ID); after != eventsBefore {
		t.Errorf("model.updated events = %d, want %d: the no-op patch told every executor to re-read the model", after, eventsBefore)
	}
}

func TestModels_DeletingAProtectedAliasIsRefused(t *testing.T) {
	api, _ := newModelAPI(t)
	for _, alias := range []string{"general", "planning"} {
		t.Run(alias, func(t *testing.T) {
			rec := api.call(http.MethodDelete, "/models/"+modelIDByAlias(t, api, alias), nil)
			expectStatus(t, rec, http.StatusConflict)
			if got, want := decodeError(t, rec).Message, "The model aliases SAM itself runs on cannot be deleted"; got != want {
				t.Errorf("message = %q, want %q: a client must learn why the delete was refused", got, want)
			}
		})
	}
}

func TestModels_CreateRefusesTheRedactionSentinel(t *testing.T) {
	api, svc := newModelAPI(t)
	source := seedUnregisteredTypeModel(t, svc, "source", "", unknownTypeAuth())

	got := api.call(http.MethodGet, "/models/"+source.ID, nil)
	expectStatus(t, got, http.StatusOK)

	rec := api.call(http.MethodPost, "/models", map[string]any{
		"alias": "duplicate", "provider": "openai", "modelName": "gpt-4o",
		"authConfig": decodeData[Model](t, got).AuthConfig,
	})
	expectDetail(t, rec, http.StatusUnprocessableEntity, "authConfig.secret_token")
	if body := decodeError(t, rec); len(body.ValidationDetails["authConfig.region"]) == 0 {
		t.Errorf("validationDetails names only some redacted keys: %s", rec.Body.String())
	}
}

func TestModels_CreateReportsEveryFieldErrorInOne422(t *testing.T) {
	api, _ := newModelAPI(t)

	rec := api.call(http.MethodPost, "/models", map[string]any{
		"alias": "", "provider": "openai", "modelName": "gpt-4o",
		"authConfig": map[string]any{"type": "apikey", "api_key": redacted},
	})

	expectStatus(t, rec, http.StatusUnprocessableEntity)
	details := decodeError(t, rec).ValidationDetails
	for _, field := range []string{"alias", "authConfig.api_key"} {
		if len(details[field]) == 0 {
			t.Errorf("validationDetails names no problem for %q: %s", field, rec.Body.String())
		}
	}
}

func expectDetailSaying(t *testing.T, rec *httptest.ResponseRecorder, field, want string) {
	t.Helper()
	expectStatus(t, rec, http.StatusUnprocessableEntity)
	problems := decodeError(t, rec).ValidationDetails[field]
	if !slices.ContainsFunc(problems, func(p string) bool { return strings.Contains(p, want) }) {
		t.Errorf("validationDetails[%q] = %v, want one saying %q: %s", field, problems, want, rec.Body.String())
	}
}

func modelIDByAlias(t *testing.T, api *entrypointAPI, alias string) string {
	t.Helper()
	for _, m := range decodeList[Model](t, api.call(http.MethodGet, "/models?pageSize=100", nil)).Data {
		if m.Alias == alias {
			return m.ID
		}
	}
	t.Fatalf("no model is aliased %q", alias)
	return ""
}

const undeployFailText = "instance failed to stop"

func createModelWithDeployedDependent(api *entrypointAPI, alias string) (Model, Agent) {
	api.t.Helper()
	model := createModel(api, alias, apiKeyAuth())
	agent := createAgentOnModel(api, alias+"-agent", alias)
	expectCreated[AgentDeployment](api.t, deployAgent(api, agent.ID), "agentDeployments")
	return model, agent
}

func createAgentOnModel(api *entrypointAPI, name, alias string) Agent {
	api.t.Helper()
	rec := api.call(http.MethodPost, "/agents", map[string]any{
		"name":          name,
		"description":   "Agent the test runs on the model.",
		"systemPrompt":  "You help with tests.",
		"inputModes":    []string{"text"},
		"modelProvider": []string{alias},
	})
	return expectCreated[Agent](api.t, rec, "agents")
}

func TestModelStatus_IsASingletonOutsideTheCollection(t *testing.T) {
	api, _ := newModelAPI(t)

	rec := api.call(http.MethodGet, "/modelStatus", nil)
	expectStatus(t, rec, http.StatusOK)
	if got := rawData(t, rec)["configured"]; got != false {
		t.Errorf("configured = %v, want false while the seeded aliases are placeholders", got)
	}

	expectStatus(t, api.call(http.MethodGet, "/models/status", nil), http.StatusBadRequest)
}

func TestModelStatus_IsConfiguredOnlyOnceBothRequiredAliasesAre(t *testing.T) {
	api, _ := newModelAPI(t)
	configure := func(alias string) {
		t.Helper()
		expectStatus(t, api.call(http.MethodPatch, "/models/"+modelIDByAlias(t, api, alias), map[string]any{
			"provider": "openai", "modelName": "gpt-4o", "apiBase": "https://api.openai.com/v1", "authType": "apikey",
			"authConfig": apiKeyAuth(),
		}), http.StatusOK)
	}
	configured := func() any {
		t.Helper()
		rec := api.call(http.MethodGet, "/modelStatus", nil)
		expectStatus(t, rec, http.StatusOK)
		return rawData(t, rec)["configured"]
	}

	configure("general")
	if got := configured(); got != false {
		t.Errorf("configured = %v, want false while planning is still a placeholder", got)
	}
	configure("planning")
	if got := configured(); got != true {
		t.Errorf("configured = %v, want true once general and planning are both configured", got)
	}
}

func TestModelDependents_TypeNamesTheResourceAndKindMovesOut(t *testing.T) {
	api, _ := newAWEAPI(t)
	model, agent := createModelWithDeployedDependent(api, "support")

	rec := api.call(http.MethodGet, "/models/"+model.ID+"/dependents", nil)
	expectStatus(t, rec, http.StatusOK)

	page := decodeList[map[string]any](t, rec)
	if len(page.Data) != 1 {
		t.Fatalf("dependents = %v, want the one agent that runs on the model", page.Data)
	}
	dependent := page.Data[0]
	if dependent["type"] != "modelDependent" {
		t.Errorf(`type = %v, want "modelDependent"`, dependent["type"])
	}
	if dependent["dependentType"] != "agent" {
		t.Errorf(`dependentType = %v, want "agent"; v1's "standard" is the agent's own type`, dependent["dependentType"])
	}
	if dependent["key"] != agent.ID {
		t.Errorf("key = %v, want the agent's UUID %s", dependent["key"], agent.ID)
	}
	if _, present := dependent["id"]; present {
		t.Errorf("a dependent is not fetchable by id; its identifier is key: %v", dependent)
	}
	if dependent["deploymentStatus"] != entplatform.DisplayStatusDeployed {
		t.Errorf("deploymentStatus = %v, want %q", dependent["deploymentStatus"], entplatform.DisplayStatusDeployed)
	}
}

func TestModelDependents_RejectsAPageOutsideTheBounds(t *testing.T) {
	api, _ := newAWEAPI(t)
	model, _ := createModelWithDeployedDependent(api, "bounds")

	for _, query := range []string{"?pageSize=0", "?pageSize=-1", "?pageNumber=0", "?pageNumber=-1"} {
		expectDetail(t, api.call(http.MethodGet, "/models/"+model.ID+"/dependents"+query, nil), http.StatusUnprocessableEntity, strings.TrimPrefix(strings.Split(query, "=")[0], "?"))
	}
}

func TestModelDependents_UnknownModelIs404(t *testing.T) {
	api, _ := newAWEAPI(t)
	expectStatus(t, api.call(http.MethodGet, "/models/"+missingID+"/dependents", nil), http.StatusNotFound)
}

func TestModels_DeleteKeepsTheModelWhenUndeployFails(t *testing.T) {
	api, awe := newAWEAPI(t)
	model, agent := createModelWithDeployedDependent(api, "undeploy-fails")
	awe.failRemoves.Store(true)

	rec := api.call(http.MethodDelete, "/models/"+model.ID, nil)
	expectStatus(t, rec, http.StatusBadGateway)
	expectLocation(t, rec, "/models/"+model.ID)
	if strings.Contains(rec.Body.String(), undeployFailText) {
		t.Errorf("the upstream error text reached the wire: %s", rec.Body.String())
	}
	if details := decodeError(t, rec).ValidationDetails; len(details) > 0 {
		t.Errorf("validationDetails = %v; a DELETE has no body, so there is no field to key them by", details)
	}
	expectStatus(t, api.call(http.MethodGet, "/models/"+model.ID, nil), http.StatusOK)

	beforeRetry := awe.removes.Load()
	expectStatus(t, api.call(http.MethodDelete, "/models/"+model.ID, nil), http.StatusBadGateway)
	if awe.removes.Load() == beforeRetry {
		t.Errorf("the retried delete did not ask the executor to remove %q again", agent.Name)
	}
	expectStatus(t, api.call(http.MethodGet, "/models/"+model.ID, nil), http.StatusOK)

	awe.failRemoves.Store(false)
	expectStatus(t, api.call(http.MethodDelete, "/models/"+model.ID, nil), http.StatusNoContent)
	expectStatus(t, api.call(http.MethodGet, "/models/"+model.ID, nil), http.StatusNotFound)
}

func TestModels_DeleteKeepsTheModelWhenAWorkflowUndeployFails(t *testing.T) {
	api, awe := newAWEAPI(t)
	rec := api.call(http.MethodPost, "/models", map[string]any{
		"alias": "wf-undeploy-fails", "provider": "typesafe", "modelName": "jev-1.13.0",
		"modelType": "decision", "authConfig": apiKeyAuth(),
	})
	model := expectCreated[Model](t, rec, "models")
	rec = api.call(http.MethodPost, "/workflows", map[string]any{
		"name":        "wf-on-model",
		"description": "Workflow whose decision node runs on the model.",
		"appConfig":   map[string]any{"decision": map[string]any{"model_alias": model.Alias}},
	})
	workflow := expectCreated[Workflow](t, rec, "workflows")
	expectCreated[WorkflowDeployment](t, deployWorkflow(api, workflow.ID), "workflowDeployments")

	deps := decodeList[map[string]any](t, api.call(http.MethodGet, "/models/"+model.ID+"/dependents", nil))
	if len(deps.Data) != 1 || deps.Data[0]["dependentType"] != "workflow" || deps.Data[0]["key"] != workflow.ID {
		t.Fatalf("dependents = %v, want the one workflow", deps.Data)
	}

	awe.failRemoves.Store(true)
	rec = api.call(http.MethodDelete, "/models/"+model.ID, nil)
	expectStatus(t, rec, http.StatusBadGateway)
	expectLocation(t, rec, "/models/"+model.ID)
	if strings.Contains(rec.Body.String(), undeployFailText) {
		t.Errorf("the upstream error text reached the wire: %s", rec.Body.String())
	}
	expectStatus(t, api.call(http.MethodGet, "/models/"+model.ID, nil), http.StatusOK)

	beforeRetry := awe.removes.Load()
	expectStatus(t, api.call(http.MethodDelete, "/models/"+model.ID, nil), http.StatusBadGateway)
	if awe.removes.Load() == beforeRetry {
		t.Errorf("the retried delete did not ask the executor to remove workflow %q again", workflow.Name)
	}
	expectStatus(t, api.call(http.MethodGet, "/models/"+model.ID, nil), http.StatusOK)

	awe.failRemoves.Store(false)
	expectStatus(t, api.call(http.MethodDelete, "/models/"+model.ID, nil), http.StatusNoContent)
	expectStatus(t, api.call(http.MethodGet, "/models/"+model.ID, nil), http.StatusNotFound)
}

func TestModels_OpenReadsRedactANonStringAuthType(t *testing.T) {
	api, svc := newModelAPI(t)
	const nestedSecret = "sk-nested-under-type"

	model := seedUnregisteredTypeModel(t, svc, "nested-type", "", map[string]any{"type": map[string]any{"api_key": nestedSecret}})
	created := api.call(http.MethodGet, "/models/"+model.ID, nil)

	reads := map[string]struct {
		rec  *httptest.ResponseRecorder
		want int
	}{
		"first read": {created, http.StatusOK},
		"get":        {api.call(http.MethodGet, "/models/"+model.ID, nil), http.StatusOK},
		"list":       {api.call(http.MethodGet, "/models", nil, "sam:nothing:read"), http.StatusOK},
	}
	for name, read := range reads {
		expectStatus(t, read.rec, read.want)
		if strings.Contains(read.rec.Body.String(), nestedSecret) {
			t.Errorf("%s returned the nested credential in clear: %s", name, read.rec.Body.String())
		}
	}
}

func TestModelDependents_MayStillRunNamesWhatBlockedAKeptDelete(t *testing.T) {
	api, awe := newAWEAPI(t)
	model, stuck := createModelWithDeployedDependent(api, "may-run")
	idle := createAgentOnModel(api, "may-run-idle", model.Alias)
	awe.failRemoves.Store(true)
	expectStatus(t, api.call(http.MethodDelete, "/models/"+model.ID, nil), http.StatusBadGateway)

	rows := map[string]map[string]any{}
	for _, dep := range decodeList[map[string]any](t, api.call(http.MethodGet, "/models/"+model.ID+"/dependents", nil)).Data {
		rows[dep["key"].(string)] = dep
	}

	if got := rows[stuck.ID]["mayStillRun"]; got != true {
		t.Errorf("mayStillRun for the agent whose undeploy failed = %v, want true: %v", got, rows[stuck.ID])
	}
	if got := rows[idle.ID]["mayStillRun"]; got != false {
		t.Errorf("mayStillRun for the never-deployed agent = %v, want false: %v", got, rows[idle.ID])
	}
}

func TestModelDependents_MayStillRunNamesAWorkflowWhoseUndeployFailed(t *testing.T) {
	api, awe := newAWEAPI(t)
	rec := api.call(http.MethodPost, "/models", map[string]any{
		"alias": "may-run-decision", "provider": "typesafe", "modelName": "jev-1.13.0",
		"modelType": "decision", "authConfig": apiKeyAuth(),
	})
	model := expectCreated[Model](t, rec, "models")
	stuck := createWorkflowOnModel(api, "may-run-workflow", model.Alias)
	expectCreated[WorkflowDeployment](t, deployWorkflow(api, stuck.ID), "workflowDeployments")
	idle := createWorkflowOnModel(api, "may-run-idle-workflow", model.Alias)
	awe.failRemoves.Store(true)
	expectStatus(t, api.call(http.MethodDelete, "/models/"+model.ID, nil), http.StatusBadGateway)

	rows := map[string]map[string]any{}
	for _, dep := range decodeList[map[string]any](t, api.call(http.MethodGet, "/models/"+model.ID+"/dependents", nil)).Data {
		rows[dep["key"].(string)] = dep
	}

	if got := rows[stuck.ID]["mayStillRun"]; got != true {
		t.Errorf("mayStillRun for the workflow whose undeploy failed = %v, want true: %v", got, rows[stuck.ID])
	}
	if got := rows[idle.ID]["mayStillRun"]; got != false {
		t.Errorf("mayStillRun for the never-deployed workflow = %v, want false: %v", got, rows[idle.ID])
	}
}

func createWorkflowOnModel(api *entrypointAPI, name, alias string) Workflow {
	api.t.Helper()
	rec := api.call(http.MethodPost, "/workflows", map[string]any{
		"name":        name,
		"description": "Workflow the test runs on the model.",
		"appConfig":   map[string]any{"decision": map[string]any{"model_alias": alias}},
	})
	return expectCreated[Workflow](api.t, rec, "workflows")
}

func TestModels_DeletingAProtectedAliasTearsNothingDown(t *testing.T) {
	api, awe := newAWEAPI(t)
	generalID := modelIDByAlias(t, api, "general")
	expectStatus(t, api.call(http.MethodPatch, "/models/"+generalID, map[string]any{
		"provider": "openai", "modelName": "gpt-4o", "apiBase": "https://api.openai.com/v1", "authType": "apikey",
		"authConfig": apiKeyAuth(),
	}), http.StatusOK)
	agent := createAgentOnModel(api, "on-general", "general")
	expectCreated[AgentDeployment](t, deployAgent(api, agent.ID), "agentDeployments")
	removesBefore := awe.removes.Load()

	rec := api.call(http.MethodDelete, "/models/"+generalID, nil)

	expectStatus(t, rec, http.StatusConflict)
	if got := awe.removes.Load(); got != removesBefore {
		t.Errorf("executor removes = %d, want %d: the refused delete undeployed the default model's agents first", got, removesBefore)
	}
}

func TestModels_WriteScopes(t *testing.T) {
	api, _ := newModelAPI(t)
	id := modelIDByAlias(t, api, "general")

	writeScopes := []string{rbac.ScopeModelConfigCreate, rbac.ScopeModelConfigUpdate, rbac.ScopeModelConfigDelete}
	for _, route := range []struct {
		method, path, scope string
	}{
		{http.MethodPost, "/models", rbac.ScopeModelConfigCreate},
		{http.MethodPatch, "/models/" + id, rbac.ScopeModelConfigUpdate},
		{http.MethodDelete, "/models/" + id, rbac.ScopeModelConfigDelete},
	} {
		t.Run(route.method+" "+route.path, func(t *testing.T) {
			if rec := api.call(route.method, route.path, `{}`, route.scope); rec.Code == http.StatusForbidden {
				t.Errorf("with %s = 403, want the route to accept its own scope", route.scope)
			}
			for _, other := range writeScopes {
				if other == route.scope {
					continue
				}
				if rec := api.call(route.method, route.path, `{}`, other); rec.Code != http.StatusForbidden {
					t.Errorf("with only %s = %d, want 403", other, rec.Code)
				}
			}
		})
	}
}

func TestModels_ServiceRejectionNamesItsField(t *testing.T) {
	api, _ := newModelAPI(t)

	for name, tc := range map[string]struct {
		body        map[string]any
		field, want string
	}{
		"a chat model on a decision-only provider": {
			body:  map[string]any{"alias": "typesafe-chat", "provider": "typesafe", "modelName": "guard", "authConfig": apiKeyAuth()},
			field: "modelType", want: "decision models only",
		},
		"a provider whose default apiBase needs a region it was not given": {
			body:  map[string]any{"alias": "bedrock-no-region", "provider": "bedrock", "modelName": "claude", "authConfig": apiKeyAuth()},
			field: "apiBase", want: "apiBase is required",
		},
		"a CR/LF header injection": {
			body: map[string]any{"alias": "crlf-reject", "provider": "openai", "modelName": "gpt-4o", "authConfig": apiKeyAuth(),
				"modelParams": map[string]any{"extra_headers": map[string]any{"X-Bad": "a\r\nInjected: yes"}}},
			field: "modelParams", want: "CR/LF",
		},
		"a grant id in modelParams of an API key model": {
			body: map[string]any{"alias": "grant-in-params", "provider": "openai", "modelName": "gpt-4o", "authConfig": apiKeyAuth(),
				"modelParams": map[string]any{"grant_id": "0b6f8c1e-2d4a-4f5b-9c3e-7a1d2e3f4a5b"}},
			field: "modelParams", want: "cannot be set in modelParams",
		},
	} {
		t.Run(name, func(t *testing.T) {
			expectDetailSaying(t, api.call(http.MethodPost, "/models", tc.body), tc.field, tc.want)
		})
	}
}

func TestModels_ByIDRoutesRejectABadUUIDAndMissAKnownOne(t *testing.T) {
	api, _ := newModelAPI(t)

	for name, tc := range map[string]struct {
		method string
		body   any
	}{
		"get":    {http.MethodGet, nil},
		"update": {http.MethodPatch, map[string]any{"modelName": "gpt-4o-mini"}},
		"delete": {http.MethodDelete, nil},
	} {
		t.Run(name, func(t *testing.T) {
			expectDetail(t, api.call(tc.method, "/models/not-a-uuid", tc.body), http.StatusBadRequest, "id")
			expectStatus(t, api.call(tc.method, "/models/"+missingID, tc.body), http.StatusNotFound)
		})
	}
}

func TestModels_TypeAndProviderRefusalsNameTheirField(t *testing.T) {
	api, _ := newModelAPI(t)
	saved := expectCreated[Model](t, api.call(http.MethodPost, "/models", map[string]any{
		"alias": "stored-decision", "provider": "typesafe", "modelName": "jev-1.13.0",
		"modelType": "decision", "authConfig": apiKeyAuth(),
	}), "models")

	t.Run("a saved decision model tested against another provider", func(t *testing.T) {
		expectDetailSaying(t, api.call(http.MethodPut, "/models/testConnection", map[string]any{
			"modelId": saved.ID, "provider": "openai",
		}), "provider", "cannot change")
	})

	t.Run("a saved decision model patched onto another provider", func(t *testing.T) {
		expectDetailSaying(t, api.call(http.MethodPatch, "/models/"+saved.ID, map[string]any{
			"provider": "openai",
		}), "provider", "cannot change")
	})

	t.Run("an unsaved model whose type its provider cannot serve", func(t *testing.T) {
		expectDetailSaying(t, api.call(http.MethodPut, "/models/testConnection", map[string]any{
			"provider": "typesafe", "modelName": "jev-1.13.0", "modelType": "chat",
			"authConfig": apiKeyAuth(),
		}), "modelType", "decision models only")
	})
}

func TestModels_AzureWorkloadIdentityIdentifiersAreNotRedacted(t *testing.T) {
	t.Setenv(llm.AzureWorkloadIdentityEnabledEnv, "true")
	t.Setenv(llm.AzureFederatedTokenFileEnv, "/var/run/secrets/sam/azure/token")
	api, _ := newModelAPI(t)
	const clientID = "11111111-1111-1111-1111-111111111111"
	const tenantID = "22222222-2222-2222-2222-222222222222"

	createRec := api.call(http.MethodPost, "/models", map[string]any{
		"alias": "azure-wi", "provider": "azure_openai", "modelName": "gpt-4o",
		"apiBase": "https://contoso.openai.azure.com/",
		"authConfig": map[string]any{
			"type": "azure_workload_identity", "client_id": clientID, "tenant_id": tenantID,
		},
	})
	created := expectCreated[Model](t, createRec, "models")

	rec := api.call(http.MethodGet, "/models/"+created.ID, nil)
	expectStatus(t, rec, http.StatusOK)
	got := decodeData[Model](t, rec).AuthConfig
	if got["client_id"] != clientID || got["tenant_id"] != tenantID {
		t.Errorf("authConfig = %v, want client_id and tenant_id returned as stored", got)
	}
}

func seedUnregisteredTypeModel(t *testing.T, svc *entplatform.ModelConfigService, alias, apiBase string, authConfig map[string]any) Model {
	t.Helper()
	resp, err := svc.Create(t.Context(), &entplatform.CreateModelConfigRequest{
		Alias: alias, Provider: "openai", ModelName: "gpt-4o", APIBase: apiBase, AuthConfig: authConfig,
	}, "seed")
	if err != nil {
		t.Fatalf("seed a model of an unregistered auth type: %v", err)
	}
	return toModel(resp)
}

func TestModels_WritesRefuseAnUnregisteredAuthType(t *testing.T) {
	api, svc := newModelAPI(t)
	existing := createModel(api, "refuse-target", apiKeyAuth())
	createBody := func(alias string, extra map[string]any) map[string]any {
		body := map[string]any{"alias": alias, "provider": "openai", "modelName": "gpt-4o"}
		for k, v := range extra {
			body[k] = v
		}
		return body
	}

	t.Run("create refuses an unregistered authConfig.type and lists the registered ones", func(t *testing.T) {
		rec := api.call(http.MethodPost, "/models", createBody("made-up-create", map[string]any{
			"authConfig": map[string]any{"type": "totally_made_up", "api_key": modelSecret},
		}))
		expectDetailSaying(t, rec, "authConfig.type", `unsupported auth type "totally_made_up"`)
		for _, registered := range platpkg.SupportedModelAuthTypes {
			expectDetailSaying(t, rec, "authConfig.type", registered)
		}
	})

	t.Run("update refuses an unregistered authConfig.type", func(t *testing.T) {
		rec := api.call(http.MethodPatch, "/models/"+existing.ID, map[string]any{
			"authConfig": map[string]any{"type": "totally_made_up"},
		})
		expectDetailSaying(t, rec, "authConfig.type", `unsupported auth type "totally_made_up"`)
	})

	t.Run("matching is exact, so a differently cased registered type is refused", func(t *testing.T) {
		for _, spelling := range []string{"APIKey", "ApiKey"} {
			rec := api.call(http.MethodPost, "/models", createBody("case-"+spelling, map[string]any{
				"authConfig": map[string]any{"type": spelling, "api_key": modelSecret},
			}))
			expectDetailSaying(t, rec, "authConfig.type", "unsupported auth type")
		}
	})

	t.Run("authConfig.type wins over a top-level authType that disagrees", func(t *testing.T) {
		rec := api.call(http.MethodPost, "/models", createBody("type-precedence", map[string]any{
			"authType":   "totally_made_up",
			"authConfig": map[string]any{"type": "apikey", "api_key": modelSecret},
		}))
		expectCreated[Model](t, rec, "models")
	})

	t.Run("a top-level authType is checked when authConfig names none", func(t *testing.T) {
		rec := api.call(http.MethodPost, "/models", createBody("top-level-made-up", map[string]any{
			"authType": "totally_made_up",
		}))
		expectDetailSaying(t, rec, "authConfig.type", "unsupported auth type")
	})

	t.Run("an update that leaves authType out succeeds", func(t *testing.T) {
		expectStatus(t, api.call(http.MethodPatch, "/models/"+existing.ID, map[string]any{"description": "no type sent"}), http.StatusOK)
	})

	t.Run("an update with an empty authType keeps its existing rejection", func(t *testing.T) {
		rec := api.call(http.MethodPatch, "/models/"+existing.ID, map[string]any{"authType": ""})
		expectDetailSaying(t, rec, "authType", "cannot be empty")
		if _, got := decodeError(t, rec).ValidationDetails["authConfig.type"]; got {
			t.Errorf("an empty authType also reported authConfig.type: %s", rec.Body.String())
		}
	})

	t.Run("patching a stored unregistered type back in succeeds", func(t *testing.T) {
		legacy := seedUnregisteredTypeModel(t, svc, "legacy-patch", "", unknownTypeAuth())
		expectStatus(t, api.call(http.MethodPatch, "/models/"+legacy.ID, map[string]any{
			"description": "edited", "authConfig": map[string]any{"type": unknownAuthType},
		}), http.StatusOK)
		expectStatus(t, api.call(http.MethodPatch, "/models/"+legacy.ID, map[string]any{"authType": unknownAuthType}), http.StatusOK)
	})

	t.Run("switching a stored unregistered type to a different unregistered one is refused", func(t *testing.T) {
		legacy := seedUnregisteredTypeModel(t, svc, "legacy-switch", "", unknownTypeAuth())
		rec := api.call(http.MethodPatch, "/models/"+legacy.ID, map[string]any{
			"authConfig": map[string]any{"type": "another_made_up"},
		})
		expectDetailSaying(t, rec, "authConfig.type", `unsupported auth type "another_made_up"`)
	})

	t.Run("a non-string authConfig.type is refused on create and update", func(t *testing.T) {
		rec := api.call(http.MethodPost, "/models", createBody("object-type", map[string]any{
			"authConfig": map[string]any{"type": map[string]any{"api_key": modelSecret}},
		}))
		expectDetailSaying(t, rec, "authConfig.type", "must be a string")
		expectDetailSaying(t, api.call(http.MethodPatch, "/models/"+existing.ID, map[string]any{
			"authConfig": map[string]any{"type": 7},
		}), "authConfig.type", "must be a string")
	})

	t.Run("a redacted authConfig.type is judged as absent, since the save drops it", func(t *testing.T) {
		expectStatus(t, api.call(http.MethodPatch, "/models/"+existing.ID, map[string]any{
			"description": "echoed back", "authConfig": map[string]any{"type": redacted},
		}), http.StatusOK)
	})

	t.Run("a binder error and an auth type error are both reported", func(t *testing.T) {
		rec := api.call(http.MethodPatch, "/models/"+existing.ID, map[string]any{
			"alias": "", "authConfig": map[string]any{"type": "totally_made_up"},
		})
		expectDetailSaying(t, rec, "alias", "cannot be empty")
		expectDetailSaying(t, rec, "authConfig.type", `unsupported auth type "totally_made_up"`)
	})

	t.Run("a null authConfig.type counts as absent", func(t *testing.T) {
		rec := api.call(http.MethodPost, "/models", createBody("null-type", map[string]any{
			"authType": "apikey", "authConfig": map[string]any{"type": nil, "api_key": modelSecret},
		}))
		expectCreated[Model](t, rec, "models")
	})

	t.Run("a stored model of an unregistered type still reads back, fully redacted", func(t *testing.T) {
		legacy := seedUnregisteredTypeModel(t, svc, "legacy-type", "", unknownTypeAuth())
		rec := api.call(http.MethodGet, "/models/"+legacy.ID, nil)
		expectStatus(t, rec, http.StatusOK)
		got := decodeData[Model](t, rec).AuthConfig
		expectNoSecret(t, rec, got, "secret_token")
		if got["region"] != redacted {
			t.Errorf("region = %v, want it redacted: an unregistered type redacts every value", got["region"])
		}
	})
}

// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"errors"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

const keyScope = "agent_builder:*:read"

type ownerScopes []string

func (o ownerScopes) ResolveOwnerScopes(string, string) (rbac.Resolution, error) {
	return rbac.Resolution{Scopes: o}, nil
}

type failingOwnerScopes struct{}

func (failingOwnerScopes) ResolveOwnerScopes(string, string) (rbac.Resolution, error) {
	return rbac.Resolution{}, errors.New("rbac backend: connection refused")
}

type apiKeyAPI struct {
	t     *testing.T
	mux   *http.ServeMux
	owner *store.User
	users *store.UsersRepo
	keys  *store.APIKeysRepo
}

func newAPIKeyAPI(t *testing.T) *apiKeyAPI {
	t.Helper()
	return newAPIKeyAPIWith(t, ownerScopes{keyScope}, "default_rbac")
}

func newAPIKeyAPIWith(t *testing.T, resolver interface {
	ResolveOwnerScopes(string, string) (rbac.Resolution, error)
}, authorizationMode string) *apiKeyAPI {
	t.Helper()
	core, deps := newTestCore(t)
	users := store.NewUsersRepo(deps.DB)
	owner := seedSystemUser(t, users, "system:ci-deployer")
	keys := store.NewAPIKeysRepo(deps.DB)
	svc := entplatform.NewAPIKeyService(keys, users, resolver, authorizationMode, core.Logger())
	mux := http.NewServeMux()
	register(mux, core, false, apiKeyRoutes(&entplatform.RouteDeps{APIKeySvc: svc}, core.Logger()))
	return &apiKeyAPI{t: t, mux: mux, owner: owner, users: users, keys: keys}
}

func seedSystemUser(t *testing.T, users *store.UsersRepo, sub string) *store.User {
	t.Helper()
	user := &store.User{
		Issuer: rbac.SystemUserIssuer, Sub: sub, ProvisionedVia: rbac.ProvisionedViaSystem,
		CreatedBy: "test", UpdatedBy: "test",
	}
	if err := users.UpsertUser(t.Context(), user, nil); err != nil {
		t.Fatalf("seed user %s: %v", sub, err)
	}
	return user
}

func (a *apiKeyAPI) seedExpiredKey(owner *store.User, label string) {
	a.t.Helper()
	created := time.Now().Add(-48 * time.Hour).UnixMilli()
	expired := created + time.Hour.Milliseconds()
	_, err := a.keys.Create(a.t.Context(), store.APIKey{
		UserID: owner.ID, Label: label, KeyHash: "hash-" + label, Prefix: "expired0",
		ExpiresTime: &expired, CreatedBy: "test", UpdatedBy: "test",
	}, []string{keyScope}, 10, created)
	if err != nil {
		a.t.Fatalf("seed expired key: %v", err)
	}
}

func labels(keys []APIKey) []string {
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, k.Label)
	}
	slices.Sort(out)
	return out
}

func signedInSuperAdmin() *auth.Subject {
	return auth.NewTestSubject(auth.SubjectFields{
		UserID: "0192f3aa-7c31-70b2-9e4d-9b1f2a5c8e77", Email: "admin@example.com", Authenticated: true,
		AuthMethod: "sam_access_token", Scopes: []string{"*"},
	})
}

func holdingKey(scopes ...string) *auth.Subject {
	return auth.NewTestSubject(auth.SubjectFields{
		UserID: "0192f3aa-7c31-70b2-9e4d-9b1f2a5c8e77", Email: "admin@example.com", Authenticated: true,
		AuthMethod: auth.APIKeyAuthMethod, Scopes: scopes,
	})
}

func (a *apiKeyAPI) call(method, path string, body any, identity *auth.Subject) *httptest.ResponseRecorder {
	a.t.Helper()
	req := newJSONRequest(a.t, method, path, body)
	req = req.WithContext(auth.ContextWithSubject(req.Context(), identity))
	rec := httptest.NewRecorder()
	a.mux.ServeHTTP(rec, req)
	return rec
}

func (a *apiKeyAPI) create(label string) CreatedAPIKey {
	a.t.Helper()
	rec := a.call(http.MethodPost, "/apiKeys", map[string]any{
		"userId": a.owner.ID, "label": label, "scopes": []string{keyScope}, "neverExpires": true,
	}, signedInSuperAdmin())
	expectStatus(a.t, rec, http.StatusCreated)
	return decodeData[CreatedAPIKey](a.t, rec)
}

func TestAPIKeys_MachineTokensOffIs404(t *testing.T) {
	core, deps := newTestCoreWithMachineTokens(t, false, nil)
	if deps.APIKeySvc != nil {
		t.Fatal("APIKeySvc is set with machine_tokens off")
	}
	requireAPIKeyRoutesAnswer404(t, core)
}

func requireAPIKeyRoutesAnswer404(t *testing.T, core *platpkg.PlatformCore) {
	t.Helper()
	handler := auth.DevMiddleware()(core.Handler())
	for _, route := range []struct{ method, path string }{
		{http.MethodGet, "/apiKeys"},
		{http.MethodPost, "/apiKeys"},
		{http.MethodGet, "/apiKeys/" + missingID},
		{http.MethodPut, "/apiKeys/" + missingID},
		{http.MethodDelete, "/apiKeys/" + missingID},
		{http.MethodGet, "/users/" + missingID + "/apiKeys"},
	} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(route.method, platpkg.APIV2Prefix+route.path, nil))
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s %s = %d, want 404", route.method, route.path, rec.Code)
		}
	}
}

func TestAPIKeys_Lifecycle(t *testing.T) {
	api := newAPIKeyAPI(t)
	admin := signedInSuperAdmin()

	rec := api.call(http.MethodPost, "/apiKeys", map[string]any{
		"userId": api.owner.ID, "label": "deploy-pipeline", "scopes": []string{keyScope},
		"expiresTime": time.Now().Add(24 * time.Hour).UTC().Format("2006-01-02T15:04:05.000Z"),
	}, admin)
	expectStatus(t, rec, http.StatusCreated)
	created := decodeData[CreatedAPIKey](t, rec)
	expectLocation(t, rec, "/apiKeys/"+created.ID)
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store on the response that carries the secret", got)
	}
	if !strings.HasPrefix(created.Secret, "samk_") || created.Type != "apiKey" || created.Status != "active" {
		t.Errorf("created key = %+v", created)
	}
	if created.ExpiresTime == 0 || created.LastUsedTime != 0 {
		t.Errorf("expiresTime %d and lastUsedTime %d, want an expiry and no use yet", created.ExpiresTime, created.LastUsedTime)
	}

	got := decodeData[APIKey](t, api.call(http.MethodGet, "/apiKeys/"+created.ID, nil, admin))
	if got.ID != created.ID || got.Label != "deploy-pipeline" || !slices.Equal(got.Scopes, []string{keyScope}) {
		t.Errorf("get = %+v", got)
	}

	renamed := api.call(http.MethodPut, "/apiKeys/"+created.ID, map[string]any{"label": "renamed"}, admin)
	expectStatus(t, renamed, http.StatusOK)
	if label := decodeData[APIKey](t, renamed).Label; label != "renamed" {
		t.Errorf("label after PUT = %q", label)
	}

	expectStatus(t, api.call(http.MethodDelete, "/apiKeys/"+created.ID, nil, admin), http.StatusNoContent)
	expectStatus(t, api.call(http.MethodGet, "/apiKeys/"+created.ID, nil, admin), http.StatusNotFound)
	expectStatus(t, api.call(http.MethodDelete, "/apiKeys/"+created.ID, nil, admin), http.StatusNotFound)
}

func TestAPIKeys_EveryFieldIsPresentAndTheSecretOnlyOnCreate(t *testing.T) {
	api := newAPIKeyAPI(t)
	admin := signedInSuperAdmin()
	createdRec := api.call(http.MethodPost, "/apiKeys", map[string]any{
		"userId": api.owner.ID, "label": "shown-once", "scopes": []string{keyScope}, "neverExpires": true,
	}, admin)
	expectStatus(t, createdRec, http.StatusCreated)
	created := decodeData[CreatedAPIKey](t, createdRec)

	wantKeys := []string{
		"createdBy", "createdTime", "expiresTime", "id", "label", "lastUsedTime",
		"prefix", "scopes", "status", "type", "updatedBy", "updatedTime", "userId",
	}
	createdData := rawData(t, createdRec)
	if keys, want := slices.Sorted(maps.Keys(createdData)), slices.Sorted(slices.Values(append(slices.Clone(wantKeys), "secret"))); !slices.Equal(keys, want) {
		t.Errorf("create keys = %v, want %v", keys, want)
	}
	expectNull(t, createdData, "expiresTime", "lastUsedTime")
	expectISOTimes(t, createdData, "createdTime", "updatedTime")
	for _, field := range []string{"id", "userId", "label", "prefix", "secret", "status", "type", "createdBy", "updatedBy"} {
		if v, _ := createdData[field].(string); v == "" {
			t.Errorf("create %s = %v, want a value", field, createdData[field])
		}
	}
	if scopes, _ := createdData["scopes"].([]any); len(scopes) != 1 || scopes[0] != keyScope {
		t.Errorf("create scopes = %v, want [%s]", createdData["scopes"], keyScope)
	}
	for name, rec := range map[string]*httptest.ResponseRecorder{
		"get":    api.call(http.MethodGet, "/apiKeys/"+created.ID, nil, admin),
		"update": api.call(http.MethodPut, "/apiKeys/"+created.ID, map[string]any{"label": "shown-once"}, admin),
	} {
		expectStatus(t, rec, http.StatusOK)
		data := rawData(t, rec)
		if keys := slices.Sorted(maps.Keys(data)); !slices.Equal(keys, wantKeys) {
			t.Errorf("%s keys = %v, want %v (no secret, nothing left out)", name, keys, wantKeys)
		}
		expectNull(t, data, "expiresTime", "lastUsedTime")
		expectISOTimes(t, data, "createdTime", "updatedTime")
	}

	listed := api.call(http.MethodGet, "/apiKeys", nil, admin)
	expectStatus(t, listed, http.StatusOK)
	if strings.Contains(listed.Body.String(), created.Secret) || strings.Contains(listed.Body.String(), `"secret"`) {
		t.Errorf("the list carries a secret: %s", listed.Body.String())
	}
}

func TestAPIKeys_PagingAndSorting(t *testing.T) {
	api := newAPIKeyAPI(t)
	admin := signedInSuperAdmin()
	for _, label := range []string{"charlie", "alpha", "bravo"} {
		api.create(label)
	}

	page1 := decodeList[APIKey](t, api.call(http.MethodGet, "/apiKeys?pageSize=2&sort=label:asc", nil, admin))
	if len(page1.Data) != 2 || page1.Data[0].Label != "alpha" || page1.Data[1].Label != "bravo" {
		t.Errorf("first page = %+v", page1.Data)
	}
	if p := page1.Meta.Pagination; p.Count != 3 || p.PageSize != 2 || p.TotalPages != 2 || p.NextPage == nil {
		t.Errorf("pagination = %+v", p)
	}
	page2 := decodeList[APIKey](t, api.call(http.MethodGet, "/apiKeys?pageSize=2&pageNumber=2&sort=label:desc", nil, admin))
	if len(page2.Data) != 1 || page2.Data[0].Label != "alpha" {
		t.Errorf("second page by label:desc = %+v", page2.Data)
	}
	empty := decodeList[APIKey](t, api.call(http.MethodGet, "/apiKeys?pageNumber=9", nil, admin))
	if len(empty.Data) != 0 {
		t.Errorf("a page past the end = %+v", empty.Data)
	}

	for target, want := range map[string]int{
		"/apiKeys?pageSize=101":        http.StatusUnprocessableEntity,
		"/apiKeys?pageSize=abc":        http.StatusBadRequest,
		"/apiKeys?sort=label:up":       http.StatusBadRequest,
		"/apiKeys?sort=secret:asc":     http.StatusUnprocessableEntity,
		"/apiKeys?status=revoked":      http.StatusUnprocessableEntity,
		"/apiKeys?userId=not-a-uuid":   http.StatusBadRequest,
		"/apiKeys?userID=" + missingID: http.StatusBadRequest,
		"/apiKeys?statuss=expired":     http.StatusBadRequest,
		"/apiKeys?status=expired":      http.StatusOK,
		"/apiKeys?userId=" + missingID: http.StatusOK,
	} {
		if rec := api.call(http.MethodGet, target, nil, admin); rec.Code != want {
			t.Errorf("GET %s = %d, want %d; body %s", target, rec.Code, want, rec.Body.String())
		}
	}
}

func TestAPIKeys_SortsByEveryField(t *testing.T) {
	api := newAPIKeyAPI(t)
	admin := signedInSuperAdmin()
	base := time.Now().Add(-time.Hour).UnixMilli()
	seed := func(label, prefix string, offset int64, expires, lastUsed *int64) {
		t.Helper()
		key, err := api.keys.Create(t.Context(), store.APIKey{
			UserID: api.owner.ID, Label: label, KeyHash: "hash-" + label, Prefix: prefix,
			ExpiresTime: expires, CreatedBy: "test", UpdatedBy: "test",
		}, []string{keyScope}, 10, base+offset)
		if err != nil {
			t.Fatalf("seed %s: %v", label, err)
		}
		if lastUsed != nil {
			if err := api.keys.TouchLastUsed(t.Context(), key.ID, *lastUsed); err != nil {
				t.Fatalf("touch %s: %v", label, err)
			}
		}
	}
	future := func(d time.Duration) *int64 { return new(time.Now().Add(d).UnixMilli()) }
	seed("first", "cccccccc", 0, future(72*time.Hour), new(base+3000))
	seed("second", "aaaaaaaa", 1000, nil, nil)
	seed("third", "bbbbbbbb", 2000, future(24*time.Hour), new(base+1000))

	order := func(target string) []string {
		rec := api.call(http.MethodGet, target, nil, admin)
		expectStatus(t, rec, http.StatusOK)
		var out []string
		for _, k := range decodeList[APIKey](t, rec).Data {
			out = append(out, k.Label)
		}
		return out
	}
	for target, want := range map[string][]string{
		"/apiKeys":                        {"third", "second", "first"},
		"/apiKeys?sort=createdTime:asc":   {"first", "second", "third"},
		"/apiKeys?sort=updatedTime:desc":  {"third", "second", "first"},
		"/apiKeys?sort=prefix:asc":        {"second", "third", "first"},
		"/apiKeys?sort=expiresTime:asc":   {"third", "first", "second"},
		"/apiKeys?sort=lastUsedTime:desc": {"first", "third", "second"},
	} {
		if got := order(target); !slices.Equal(got, want) {
			t.Errorf("GET %s = %v, want %v", target, got, want)
		}
	}

	first := decodeList[APIKey](t, api.call(http.MethodGet, "/apiKeys?sort=createdTime:asc&pageSize=1", nil, admin)).Data[0]
	data := rawData(t, api.call(http.MethodGet, "/apiKeys/"+first.ID, nil, admin))
	expectISOTimes(t, data, "expiresTime", "lastUsedTime", "createdTime", "updatedTime")
}

func TestAPIKeys_OwnerAliasIsTheSameList(t *testing.T) {
	api := newAPIKeyAPI(t)
	admin := signedInSuperAdmin()
	api.create("one")
	api.create("two")

	aliased := decodeList[APIKey](t, api.call(http.MethodGet, "/users/"+api.owner.ID+"/apiKeys?sort=label:asc", nil, admin))
	filtered := decodeList[APIKey](t, api.call(http.MethodGet, "/apiKeys?userId="+api.owner.ID+"&sort=label:asc", nil, admin))
	if len(aliased.Data) != 2 || !slices.EqualFunc(aliased.Data, filtered.Data, func(a, b APIKey) bool { return a.ID == b.ID }) {
		t.Errorf("alias %+v, filtered list %+v", aliased.Data, filtered.Data)
	}
	if other := decodeList[APIKey](t, api.call(http.MethodGet, "/users/"+missingID+"/apiKeys", nil, admin)); len(other.Data) != 0 {
		t.Errorf("another user's keys = %+v", other.Data)
	}
	expectStatus(t, api.call(http.MethodGet, "/users/not-a-uuid/apiKeys", nil, admin), http.StatusBadRequest)
	expectDetail(t, api.call(http.MethodGet, "/users/"+api.owner.ID+"/apiKeys?userId="+missingID, nil, admin), http.StatusBadRequest, "userId")
	expectDetail(t, api.call(http.MethodGet, "/users/"+api.owner.ID+"/apiKeys?status=revoked", nil, admin), http.StatusUnprocessableEntity, "status")
}

func TestAPIKeys_FiltersNarrowTheList(t *testing.T) {
	api := newAPIKeyAPI(t)
	admin := signedInSuperAdmin()
	other := seedSystemUser(t, api.users, "system:other-pipeline")
	api.create("mine")
	api.seedExpiredKey(api.owner, "mine-old")
	api.seedExpiredKey(other, "theirs-old")

	list := func(target string) []string {
		rec := api.call(http.MethodGet, target, nil, admin)
		expectStatus(t, rec, http.StatusOK)
		return labels(decodeList[APIKey](t, rec).Data)
	}
	cases := map[string][]string{
		"/apiKeys":                                          {"mine", "mine-old", "theirs-old"},
		"/apiKeys?userId=" + api.owner.ID:                   {"mine", "mine-old"},
		"/apiKeys?userId=" + other.ID:                       {"theirs-old"},
		"/apiKeys?userId=" + missingID:                      {},
		"/apiKeys?status=active":                            {"mine"},
		"/apiKeys?status=expired":                           {"mine-old", "theirs-old"},
		"/apiKeys?status=expired&userId=" + other.ID:        {"theirs-old"},
		"/users/" + other.ID + "/apiKeys":                   {"theirs-old"},
		"/users/" + api.owner.ID + "/apiKeys?status=active": {"mine"},
	}
	for target, want := range cases {
		if got := list(target); !slices.Equal(got, want) {
			t.Errorf("GET %s = %v, want %v", target, got, want)
		}
	}
}

func TestAPIKeys_CreateFailuresKeepServiceTextOffTheWire(t *testing.T) {
	body := func(api *apiKeyAPI) *httptest.ResponseRecorder {
		return api.call(http.MethodPost, "/apiKeys", map[string]any{
			"userId": api.owner.ID, "label": "x", "scopes": []string{keyScope}, "neverExpires": true,
		}, signedInSuperAdmin())
	}

	unavailable := body(newAPIKeyAPIWith(t, failingOwnerScopes{}, "default_rbac"))
	expectStatus(t, unavailable, http.StatusServiceUnavailable)
	if got := decodeError(t, unavailable).Message; got != "Unable to create API key" {
		t.Errorf("503 message = %q", got)
	}
	if strings.Contains(unavailable.Body.String(), "connection refused") {
		t.Errorf("503 body carries upstream text: %s", unavailable.Body.String())
	}

}

func TestAPIKeys_CreateOutsideDefaultRBACNamesTheMode(t *testing.T) {
	api := newAPIKeyAPIWith(t, ownerScopes{keyScope}, "")
	rec := api.call(http.MethodPost, "/apiKeys", map[string]any{
		"userId": api.owner.ID, "label": "x", "scopes": []string{keyScope}, "neverExpires": true,
	}, signedInSuperAdmin())
	expectStatus(t, rec, http.StatusForbidden)
	if got := decodeError(t, rec).Message; !strings.Contains(got, "default_rbac") {
		t.Errorf("403 message = %q, want it to name default_rbac", got)
	}
}

func TestAPIKeys_Validation(t *testing.T) {
	api := newAPIKeyAPI(t)
	admin := signedInSuperAdmin()

	empty := api.call(http.MethodPost, "/apiKeys", `{}`, admin)
	expectStatus(t, empty, http.StatusUnprocessableEntity)
	body := decodeError(t, empty)
	for _, field := range []string{"userId", "label", "scopes", "expiresTime"} {
		if len(body.ValidationDetails[field]) == 0 {
			t.Errorf("an empty create reports nothing for %q: %s", field, empty.Body.String())
		}
	}
	if body.Message != "Unable to create API key" {
		t.Errorf("message = %q, want the handler's own sentence", body.Message)
	}

	for name, tc := range map[string]struct {
		body   any
		status int
		field  string
	}{
		"both expiry forms": {map[string]any{
			"userId": api.owner.ID, "label": "x", "scopes": []string{keyScope}, "neverExpires": true,
			"expiresTime": time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
		}, http.StatusUnprocessableEntity, "expiresTime"},
		"epoch zero expiry with neverExpires": {map[string]any{
			"userId": api.owner.ID, "label": "x", "scopes": []string{keyScope}, "neverExpires": true,
			"expiresTime": "1970-01-01T00:00:00.000Z",
		}, http.StatusUnprocessableEntity, "expiresTime"},
		"expiry in the past": {map[string]any{
			"userId": api.owner.ID, "label": "x", "scopes": []string{keyScope},
			"expiresTime": "2020-01-01T00:00:00.000Z",
		}, http.StatusUnprocessableEntity, "expiresTime"},
		"scope the owner lacks": {map[string]any{
			"userId": api.owner.ID, "label": "x", "scopes": []string{"rbac:_:update"}, "neverExpires": true,
		}, http.StatusUnprocessableEntity, "scopes"},
		"unknown owner": {map[string]any{
			"userId": missingID, "label": "x", "scopes": []string{keyScope}, "neverExpires": true,
		}, http.StatusUnprocessableEntity, "userId"},
		"expiry that is not a date-time": {`{"expiresTime":"tomorrow"}`, http.StatusBadRequest, "expiresTime"},
		"unknown field":                  {`{"secret":"mine"}`, http.StatusBadRequest, "secret"},
	} {
		t.Run(name, func(t *testing.T) {
			expectDetail(t, api.call(http.MethodPost, "/apiKeys", tc.body, admin), tc.status, tc.field)
		})
	}

	for _, target := range []string{
		"/apiKeys?userId=", "/apiKeys?status=", "/users/" + missingID + "/apiKeys?status=", "/apiKeys?userId=%zz",
	} {
		expectStatus(t, api.call(http.MethodGet, target, nil, admin), http.StatusBadRequest)
	}

	created := api.create("target")
	expectDetail(t, api.call(http.MethodPut, "/apiKeys/"+created.ID, `{"label":""}`, admin), http.StatusUnprocessableEntity, "label")
	for _, field := range []string{"scopes", "expiresTime", "userId"} {
		rec := api.call(http.MethodPut, "/apiKeys/"+created.ID, `{"label":"x","`+field+`":"y"}`, admin)
		expectDetail(t, rec, http.StatusBadRequest, field)
	}
	expectStatus(t, api.call(http.MethodGet, "/apiKeys/not-a-uuid", nil, admin), http.StatusBadRequest)
	expectStatus(t, api.call(http.MethodPut, "/apiKeys/"+missingID, `{"label":"x"}`, admin), http.StatusNotFound)
}

func TestAPIKeys_OwnerCapIs409(t *testing.T) {
	api := newAPIKeyAPI(t)
	for i := range 10 {
		api.create("key-" + string(rune('a'+i)))
	}
	rec := api.call(http.MethodPost, "/apiKeys", map[string]any{
		"userId": api.owner.ID, "label": "eleventh", "scopes": []string{keyScope}, "neverExpires": true,
	}, signedInSuperAdmin())
	expectStatus(t, rec, http.StatusConflict)
	if body := decodeError(t, rec); body.Message != "Unable to create API key" {
		t.Errorf("message = %q, want the handler's sentence", body.Message)
	}
}

func TestAPIKeys_NeedTheUniversalGrant(t *testing.T) {
	api := newAPIKeyAPI(t)
	created := api.create("gated")
	narrow := auth.NewTestSubject(auth.SubjectFields{
		UserID: "0192f3aa-7c31-70b2-9e4d-9b1f2a5c8e77", Email: "admin@example.com", Authenticated: true,
		AuthMethod: "sam_access_token", Scopes: []string{rbac.ScopeRBACRead, rbac.ScopeAgentBuilderRead},
	})

	for _, route := range apiKeyRouteCalls(created.ID) {
		rec := api.call(route.method, route.path, route.body, narrow)
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s %s without the universal grant = %d, want 403", route.method, route.path, rec.Code)
		}
	}
	for _, route := range apiKeyRouteCalls(created.ID) {
		rec := api.call(route.method, route.path, route.body, nil)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s with no identity = %d, want 401", route.method, route.path, rec.Code)
		}
	}
}

func TestAPIKeys_AKeyReadsButNeverWrites(t *testing.T) {
	api := newAPIKeyAPI(t)
	created := api.create("target")

	for _, universal := range []string{"*", "*:*:*"} {
		key := holdingKey(universal)
		for _, route := range apiKeyRouteCalls(created.ID) {
			rec := api.call(route.method, route.path, route.body, key)
			if route.write {
				expectStatus(t, rec, http.StatusForbidden)
				if !strings.Contains(rec.Body.String(), "signed-in super administrator") {
					t.Errorf("%s %s refusal does not say a person must do this: %s", route.method, route.path, rec.Body.String())
				}
			} else if rec.Code != http.StatusOK {
				t.Errorf("%s %s by a key holding %s = %d, want 200", route.method, route.path, universal, rec.Code)
			}
		}
	}
	expectStatus(t, api.call(http.MethodGet, "/apiKeys/"+created.ID, nil, signedInSuperAdmin()), http.StatusOK)
}

type keyRouteCall struct {
	method, path string
	body         any
	write        bool
}

func apiKeyRouteCalls(id string) []keyRouteCall {
	return []keyRouteCall{
		{http.MethodGet, "/apiKeys", nil, false},
		{http.MethodGet, "/apiKeys/" + id, nil, false},
		{http.MethodGet, "/users/" + id + "/apiKeys", nil, false},
		{http.MethodPost, "/apiKeys", `{}`, true},
		{http.MethodPut, "/apiKeys/" + id, `{"label":"x"}`, true},
		{http.MethodDelete, "/apiKeys/" + id, nil, true},
	}
}

func TestAPIKeys_AKeyReachesThePublishedRoutesThroughThePlatformGate(t *testing.T) {
	core, deps := newTestCore(t)
	if deps.APIKeySvc == nil {
		t.Fatal("APIKeySvc is not set with machine_tokens on")
	}
	key := holdingKey("*")
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		core.Handler().ServeHTTP(w, r.WithContext(auth.ContextWithSubject(r.Context(), key)))
	})
	const writeGateRefusal = "signed-in super administrator"
	for _, route := range apiKeyRouteCalls(missingID) {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(route.method, platpkg.APIV2Prefix+route.path, strings.NewReader(`{}`)))
		reachedWriteGate := strings.Contains(rec.Body.String(), writeGateRefusal)
		switch {
		case route.path == "/users/"+missingID+"/apiKeys":
			expectStatus(t, rec, http.StatusForbidden)
			if reachedWriteGate {
				t.Errorf("%s %s reached the write gate, so the platform's key gate admitted an unpublished route: %s", route.method, route.path, rec.Body.String())
			}
		case route.write:
			expectStatus(t, rec, http.StatusForbidden)
			if !reachedWriteGate {
				t.Errorf("%s %s was refused by the platform's key gate, not the write gate: %s", route.method, route.path, rec.Body.String())
			}
		default:
			want := http.StatusOK
			if route.path == "/apiKeys/"+missingID {
				want = http.StatusNotFound
			}
			if rec.Code != want {
				t.Errorf("%s %s by a key holding the universal grant = %d, want %d: %s", route.method, route.path, rec.Code, want, rec.Body.String())
			}
		}
	}
}

func TestAPIKeys_APIKeyRoutes(t *testing.T) {
	got := apiKeyRoutesTagged(t, apiKeysTag, usersTag)
	want := []string{
		"DELETE /apiKeys/{id}", "GET /apiKeys", "GET /apiKeys/{id}", "GET /users/{userId}/apiKeys",
		"POST /apiKeys", "PUT /apiKeys/{id}",
	}
	if !slices.Equal(got, want) {
		t.Errorf("API key routes = %v, want %v", got, want)
	}
}

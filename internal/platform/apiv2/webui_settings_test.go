// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

const webuiSettingsPath = "/webuiSettings"

var webuiOverrideKeys = []string{
	"appName", "welcomeMessage", "collectFeedback", "publishFeedback", "feedbackTaskInfoLevel",
	"disclaimerText", "logoUrl", "smallLogoUrl", "systemPurpose", "responseFormat",
}

func newWebuiSettingsAPI(t *testing.T) (*entrypointAPI, *entplatform.RouteDeps) {
	t.Helper()
	core, deps := newTestCore(t)
	return &entrypointAPI{apiClient: newAPIClient(t, core)}, deps
}

func TestWebuiSettings_UnsetIsAllNull(t *testing.T) {
	api, _ := newWebuiSettingsAPI(t)
	rec := api.call(http.MethodGet, webuiSettingsPath, nil)
	expectStatus(t, rec, http.StatusOK)

	data := rawData(t, rec)
	expectNull(t, data, slices.Concat(webuiOverrideKeys, []string{"createdBy", "updatedBy", "createdTime", "updatedTime"})...)
	expectAbsent(t, data, "createdAt", "updatedAt")
}

func TestWebuiSettings_PutReplaces(t *testing.T) {
	api, _ := newWebuiSettingsAPI(t)

	rec := api.call(http.MethodPut, webuiSettingsPath, map[string]any{"appName": "Acme", "welcomeMessage": "Hi"})
	expectStatus(t, rec, http.StatusOK)
	data := rawData(t, rec)
	if data["appName"] != "Acme" || data["welcomeMessage"] != "Hi" {
		t.Fatalf("first PUT = %v, want appName Acme and welcomeMessage Hi", data)
	}

	rec = api.call(http.MethodPut, webuiSettingsPath, map[string]any{"appName": "Acme"})
	expectStatus(t, rec, http.StatusOK)
	data = rawData(t, rec)
	expectNull(t, data, "welcomeMessage")
	if data["appName"] != "Acme" {
		t.Errorf("appName = %v, want Acme", data["appName"])
	}

	got := rawData(t, api.call(http.MethodGet, webuiSettingsPath, nil))
	expectNull(t, got, "welcomeMessage")
	if got["appName"] != "Acme" {
		t.Errorf("GET appName = %v, want Acme", got["appName"])
	}
	expectISOTimes(t, got, "createdTime", "updatedTime")
	if got["createdBy"] == nil || got["updatedBy"] == nil {
		t.Errorf("createdBy and updatedBy = %v and %v, want the caller", got["createdBy"], got["updatedBy"])
	}
}

func TestWebuiSettings_AllNullClears(t *testing.T) {
	api, _ := newWebuiSettingsAPI(t)
	expectStatus(t, api.call(http.MethodPut, webuiSettingsPath, map[string]any{"appName": "Acme", "collectFeedback": true}), http.StatusOK)

	rec := api.call(http.MethodPut, webuiSettingsPath, map[string]any{})
	expectStatus(t, rec, http.StatusOK)
	expectNull(t, rawData(t, rec), webuiOverrideKeys...)
	expectNull(t, rawData(t, api.call(http.MethodGet, webuiSettingsPath, nil)), webuiOverrideKeys...)
}

func TestWebuiSettings_EmptyStringIsAnOverride(t *testing.T) {
	api, _ := newWebuiSettingsAPI(t)
	rec := api.call(http.MethodPut, webuiSettingsPath, map[string]any{"appName": ""})
	expectStatus(t, rec, http.StatusOK)
	if got, ok := rawData(t, rec)["appName"].(string); !ok || got != "" {
		t.Errorf("appName = %#v, want an empty string", rawData(t, rec)["appName"])
	}
}

func TestWebuiSettings_Validation(t *testing.T) {
	api, _ := newWebuiSettingsAPI(t)
	rec := api.call(http.MethodPut, webuiSettingsPath, map[string]any{
		"appName":               strings.Repeat("a", 65),
		"disclaimerText":        strings.Repeat("d", 801),
		"feedbackTaskInfoLevel": "loud",
		"logoUrl":               "javascript:alert(1)",
	})
	expectStatus(t, rec, http.StatusUnprocessableEntity)
	details := decodeError(t, rec).ValidationDetails
	for _, field := range []string{"appName", "disclaimerText", "feedbackTaskInfoLevel", "logoUrl"} {
		if len(details[field]) == 0 {
			t.Errorf("validationDetails has nothing for %q: %v", field, details)
		}
	}
}

func TestWebuiSettings_Scopes(t *testing.T) {
	api, _ := newWebuiSettingsAPI(t)
	body := map[string]any{"appName": "Acme"}

	expectStatus(t, api.call(http.MethodGet, webuiSettingsPath, nil, rbac.ScopeWebuiSettingsRead), http.StatusOK)
	expectStatus(t, api.call(http.MethodPut, webuiSettingsPath, body, rbac.ScopeWebuiSettingsRead), http.StatusForbidden)
	expectStatus(t, api.call(http.MethodGet, webuiSettingsPath, nil, rbac.ScopeWebuiSettingsUpdate), http.StatusForbidden)
	expectStatus(t, api.call(http.MethodPut, webuiSettingsPath, body, rbac.ScopeWebuiSettingsUpdate), http.StatusOK)
}

func TestWebuiSettings_APIKeyRoutes(t *testing.T) {
	got := apiKeyRoutesTagged(t, webuiSettingsTag)
	for _, want := range []string{"GET /webuiSettings", "PUT /webuiSettings"} {
		if !slices.Contains(got, want) {
			t.Errorf("API key routes = %v, missing %s", got, want)
		}
	}
}

func TestWebuiSettings_APIKeyReachesEveryRoute(t *testing.T) {
	core, _ := newTestCore(t)
	send := func(method string, body any) int {
		req := newJSONRequest(t, method, webuiSettingsPath, body)
		req = req.WithContext(auth.ContextWithSubject(req.Context(), holdingKey("*")))
		return serveBare(t, core, req).Code
	}
	if got := send(http.MethodGet, nil); got != http.StatusOK {
		t.Errorf("GET with a key = %d, want 200", got)
	}
	if got := send(http.MethodPut, map[string]any{"appName": "Acme"}); got != http.StatusOK {
		t.Errorf("PUT with a key = %d, want 200", got)
	}
}

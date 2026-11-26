// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"net/http"
	"slices"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

func newProfileProviderAPI(t *testing.T) (*entrypointAPI, *store.DB) {
	t.Helper()
	core, deps := newTestCore(t)
	return &entrypointAPI{apiClient: newAPIClient(t, core)}, deps.DB
}

func seedToolset(t *testing.T, db *store.DB, name, status, capabilities string, tools ...string) string {
	t.Helper()
	repo := store.NewToolPackageRepo(db)
	description := name + " description"
	pkg := &store.ToolPackage{
		Name: name, Description: &description, DiscoveryStatus: status, Capabilities: capabilities,
		CreatedBy: "fixture", UpdatedBy: "fixture",
	}
	if err := repo.Create(t.Context(), pkg); err != nil {
		t.Fatalf("seed toolset: %v", err)
	}
	for _, tool := range tools {
		toolDescription := tool + " description"
		file := &store.ToolPackageFile{
			PackageID: pkg.ID, ToolName: tool, ToolDescription: &toolDescription,
			TimeoutSeconds: 30, SandboxProfile: "default",
		}
		if err := repo.UpsertToolFile(t.Context(), file); err != nil {
			t.Fatalf("seed tool %s: %v", tool, err)
		}
	}
	return pkg.ID
}

func seedProfileProviderToolset(t *testing.T, db *store.DB, name string) string {
	t.Helper()
	return seedToolset(t, db, name, "ready", `{"profileProvider": true}`, "enrich_authenticated_user")
}

func TestProfileProvider_UnsetDefaults(t *testing.T) {
	api, _ := newProfileProviderAPI(t)
	rec := api.call(http.MethodGet, profileProviderPath, nil)
	expectStatus(t, rec, http.StatusOK)

	data := rawData(t, rec)
	expectNull(t, data, "toolsetId", "createdBy", "updatedBy", "createdTime", "updatedTime")
	expectAbsent(t, data, "createdAt", "updatedAt")
	if data["lookupKey"] != "email" {
		t.Errorf("lookupKey = %v, want email", data["lookupKey"])
	}
}

func TestProfileProvider_ReplaceThenGet(t *testing.T) {
	api, db := newProfileProviderAPI(t)
	toolsetID := seedProfileProviderToolset(t, db, "directory")

	rec := api.call(http.MethodPut, profileProviderPath, map[string]any{"toolsetId": toolsetID, "lookupKey": "upn"})
	expectStatus(t, rec, http.StatusOK)
	got := decodeData[ProfileProvider](t, rec)
	if got.ToolsetID == nil || *got.ToolsetID != toolsetID || got.LookupKey != "upn" {
		t.Errorf("PUT = %+v, want toolset %s and upn", got, toolsetID)
	}

	rec = api.call(http.MethodGet, profileProviderPath, nil)
	expectStatus(t, rec, http.StatusOK)
	data := rawData(t, rec)
	if data["toolsetId"] != toolsetID || data["lookupKey"] != "upn" {
		t.Errorf("GET = %v, want toolset %s and upn", data, toolsetID)
	}
	expectISOTimes(t, data, "createdTime", "updatedTime")
	if data["createdBy"] == nil || data["updatedBy"] == nil {
		t.Errorf("createdBy and updatedBy = %v and %v, want the caller", data["createdBy"], data["updatedBy"])
	}
}

func TestProfileProvider_PutRequiresLookupKey(t *testing.T) {
	api, db := newProfileProviderAPI(t)
	toolsetID := seedProfileProviderToolset(t, db, "directory")
	expectStatus(t, api.call(http.MethodPut, profileProviderPath, map[string]any{"toolsetId": toolsetID, "lookupKey": "upn"}), http.StatusOK)

	expectDetail(t, api.call(http.MethodPut, profileProviderPath, map[string]any{"toolsetId": toolsetID}), http.StatusUnprocessableEntity, "lookupKey")

	data := rawData(t, api.call(http.MethodGet, profileProviderPath, nil))
	if data["lookupKey"] != "upn" {
		t.Errorf("lookupKey after the refused PUT = %v, want upn", data["lookupKey"])
	}
}

func TestProfileProvider_Validation(t *testing.T) {
	api, db := newProfileProviderAPI(t)
	notReady := seedToolset(t, db, "pending", "pending", `{"profileProvider": true}`, "enrich_authenticated_user")
	noCapability := seedToolset(t, db, "no-capability", "ready", `{}`, "enrich_authenticated_user")
	noTool := seedToolset(t, db, "no-tool", "ready", `{"profileProvider": true}`, "search_users")

	for _, tc := range []struct{ name, toolsetID, want string }{
		{"empty", "", "toolsetId is required"},
		{"not a UUID", "toolset-1", "toolsetId must be a valid UUID"},
		{"unknown", missingID, "toolset not found"},
		{"not ready", notReady, `toolset discovery status is "pending"; expected "ready"`},
		{"no capability", noCapability, "toolset must declare capabilities.profileProvider: true"},
		{"missing tool", noTool, `toolset must expose the "enrich_authenticated_user" tool`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := api.call(http.MethodPut, profileProviderPath, map[string]any{"toolsetId": tc.toolsetID, "lookupKey": "email"})
			expectStatus(t, rec, http.StatusUnprocessableEntity)
			if got := decodeError(t, rec).ValidationDetails["toolsetId"]; !slices.Contains(got, tc.want) {
				t.Errorf("validationDetails.toolsetId = %v, want %q", got, tc.want)
			}
		})
	}

	expectStatus(t, api.call(http.MethodPut, profileProviderPath, `{"toolsetId": "`), http.StatusBadRequest)
	expectStatus(t, api.call(http.MethodPut, profileProviderPath, `{"toolsetId":"`+noTool+`","lookupKey":"email","extra":1}`), http.StatusBadRequest)
}

func TestProfileProvider_DeleteClearsAndKeepsLookupKey(t *testing.T) {
	api, db := newProfileProviderAPI(t)
	toolsetID := seedProfileProviderToolset(t, db, "directory")
	expectStatus(t, api.call(http.MethodPut, profileProviderPath, map[string]any{"toolsetId": toolsetID, "lookupKey": "upn"}), http.StatusOK)

	expectStatus(t, api.call(http.MethodDelete, profileProviderPath, nil), http.StatusNoContent)
	expectStatus(t, api.call(http.MethodDelete, profileProviderPath, nil), http.StatusNoContent)

	data := rawData(t, api.call(http.MethodGet, profileProviderPath, nil))
	expectNull(t, data, "toolsetId")
	if data["lookupKey"] != "upn" {
		t.Errorf("lookupKey after DELETE = %v, want upn", data["lookupKey"])
	}
}

func TestProfileProvider_GetTakesEitherScope(t *testing.T) {
	api, _ := newProfileProviderAPI(t)
	for _, scope := range []string{rbac.ScopeProfileProviderRead, rbac.ScopeProfileProviderUpdate} {
		expectStatus(t, api.call(http.MethodGet, profileProviderPath, nil, scope), http.StatusOK)
	}
	expectStatus(t, api.call(http.MethodGet, profileProviderPath, nil, rbac.ScopeConnectorRead), http.StatusForbidden)
}

func TestProfileProvider_WritesNeedUpdate(t *testing.T) {
	api, db := newProfileProviderAPI(t)
	toolsetID := seedProfileProviderToolset(t, db, "directory")
	body := map[string]any{"toolsetId": toolsetID, "lookupKey": "email"}

	expectStatus(t, api.call(http.MethodPut, profileProviderPath, body, rbac.ScopeProfileProviderRead), http.StatusForbidden)
	expectStatus(t, api.call(http.MethodDelete, profileProviderPath, nil, rbac.ScopeProfileProviderRead), http.StatusForbidden)
	expectStatus(t, api.call(http.MethodPut, profileProviderPath, body, rbac.ScopeProfileProviderUpdate), http.StatusOK)
	expectStatus(t, api.call(http.MethodDelete, profileProviderPath, nil, rbac.ScopeProfileProviderUpdate), http.StatusNoContent)
}

func TestProfileProvider_APIKeyRoutes(t *testing.T) {
	got := apiKeyRoutesTagged(t, profileProviderTag)
	for _, want := range []string{"DELETE /profileProvider", "GET /profileProvider", "PUT /profileProvider"} {
		if !slices.Contains(got, want) {
			t.Errorf("API key routes = %v, missing %s", got, want)
		}
	}
}

func TestProfileProvider_APIKeyReachesEveryRoute(t *testing.T) {
	core, deps := newTestCore(t)
	toolsetID := seedProfileProviderToolset(t, deps.DB, "directory")
	send := func(method string, body any) int {
		req := newJSONRequest(t, method, profileProviderPath, body)
		req = req.WithContext(auth.ContextWithSubject(req.Context(), holdingKey("*")))
		return serveBare(t, core, req).Code
	}
	if got := send(http.MethodGet, nil); got != http.StatusOK {
		t.Errorf("GET with a key = %d, want 200", got)
	}
	if got := send(http.MethodPut, map[string]any{"toolsetId": toolsetID, "lookupKey": "email"}); got != http.StatusOK {
		t.Errorf("PUT with a key = %d, want 200", got)
	}
	if got := send(http.MethodDelete, nil); got != http.StatusNoContent {
		t.Errorf("DELETE with a key = %d, want 204", got)
	}
}

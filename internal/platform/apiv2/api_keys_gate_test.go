// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

const apiKeysWriteRefusal = "signed-in super administrator"

func apiKeysCaller(email, authMethod string, scopes ...string) *auth.Subject {
	return auth.NewTestSubject(auth.SubjectFields{
		UserID: "0192f3aa-7c31-70b2-9e4d-9b1f2a5c8e77", Email: email, Authenticated: true,
		AuthMethod: authMethod, Scopes: scopes,
	})
}

func apiKeysWriteCalls(id string) []keyRouteCall {
	return slices.DeleteFunc(apiKeyRouteCalls(id), func(c keyRouteCall) bool { return !c.write })
}

func apiKeysReadCalls(id string) []keyRouteCall {
	return slices.DeleteFunc(apiKeyRouteCalls(id), func(c keyRouteCall) bool { return c.write })
}

func apiKeysLogLines(logs, msg string) []map[string]any {
	var out []map[string]any
	for line := range strings.Lines(logs) {
		var entry map[string]any
		if json.Unmarshal([]byte(line), &entry) == nil && entry["msg"] == msg {
			out = append(out, entry)
		}
	}
	return out
}

func apiKeysAuditLines(logs, action, resource string) []map[string]any {
	return slices.DeleteFunc(apiKeysLogLines(logs, "rbac audit"), func(entry map[string]any) bool {
		return entry["event"] != "rbac_mutation" || entry["action"] != action || entry["resource"] != resource
	})
}

func (a *apiKeyAPI) validCreateBody(label string) map[string]any {
	return map[string]any{"userId": a.owner.ID, "label": label, "scopes": []string{keyScope}, "neverExpires": true}
}

func TestAPIKeys_WritesRefuseAKeyWhateverItHolds(t *testing.T) {
	logs := captureLogs(t)
	api := newAPIKeyAPI(t)
	for _, scopes := range [][]string{{"*"}, {"*:*:*"}, {rbac.ScopeRBACRead}, {}} {
		for _, route := range apiKeysWriteCalls(missingID) {
			rec := api.call(route.method, route.path, route.body, holdingKey(scopes...))
			if rec.Code != http.StatusForbidden {
				t.Errorf("%s %s by a key holding %v = %d, want 403", route.method, route.path, scopes, rec.Code)
				continue
			}
			if msg := decodeError(t, rec).Message; !strings.Contains(msg, apiKeysWriteRefusal) {
				t.Errorf("%s %s refusal %q does not say a person must do this", route.method, route.path, msg)
			}
		}
	}
	if strings.Contains(logs.String(), "not a super administrator") {
		t.Errorf("a key was audited as short of privilege rather than as a key:\n%s", logs.String())
	}
}

func TestAPIKeys_AKeysWriteRefusalIsAuditedAsNotInteractive(t *testing.T) {
	logs := captureLogs(t)
	api := newAPIKeyAPI(t)
	expectStatus(t, api.call(http.MethodDelete, "/apiKeys/"+missingID, nil, holdingKey("*")), http.StatusForbidden)

	lines := apiKeysAuditLines(logs.String(), "rejected", "api_key")
	if len(lines) != 1 {
		t.Fatalf("got %d rejected api_key audit lines, want 1:\n%s", len(lines), logs.String())
	}
	for field, want := range map[string]string{
		"reason":     "caller is not interactively authenticated",
		"authMethod": auth.APIKeyAuthMethod,
		"actor":      "admin@example.com",
	} {
		if lines[0][field] != want {
			t.Errorf("audit %s = %v, want %q", field, lines[0][field], want)
		}
	}
}

func TestAPIKeys_WritesRefuseAPersonWithoutTheUniversalGrant(t *testing.T) {
	api := newAPIKeyAPI(t)
	for _, scopes := range [][]string{
		{"rbac:_:create", "rbac:_:update", "rbac:_:delete", rbac.ScopeRBACRead},
		{"rbac:_:*"},
		{"rbac:*:*"},
	} {
		person := apiKeysCaller("admin@example.com", auth.AuthMethodSAMAccessToken, scopes...)
		for _, route := range apiKeysWriteCalls(missingID) {
			if rec := api.call(route.method, route.path, route.body, person); rec.Code != http.StatusForbidden {
				t.Errorf("%s %s by a person holding %v = %d, want 403", route.method, route.path, scopes, rec.Code)
			}
		}
	}
}

func TestAPIKeys_ReadsRefuseEveryGrantButTheUniversalOne(t *testing.T) {
	api := newAPIKeyAPI(t)
	for _, scopes := range [][]string{{rbac.ScopeAgentInvokeAny}, {rbac.ScopeRBACRead}, {"rbac:*:*"}} {
		person := apiKeysCaller("admin@example.com", auth.AuthMethodSAMAccessToken, scopes...)
		for _, route := range apiKeysReadCalls(missingID) {
			if rec := api.call(route.method, route.path, route.body, person); rec.Code != http.StatusForbidden {
				t.Errorf("%s %s by a caller holding %v = %d, want 403", route.method, route.path, scopes, rec.Code)
			}
		}
	}
}

func TestAPIKeys_ARefusedReadIsLoggedAsARead(t *testing.T) {
	logs := captureLogs(t)
	api := newAPIKeyAPI(t)
	caller := apiKeysCaller("admin@example.com", auth.AuthMethodSAMAccessToken, rbac.ScopeAgentInvokeAny)
	for i, route := range apiKeysReadCalls(missingID) {
		expectMessage(t, api.call(route.method, route.path, route.body, caller), http.StatusForbidden, "Reading API keys requires a super administrator")
		lines := apiKeysLogLines(logs.String(), "api key read refused")
		if len(lines) != i+1 || lines[i]["level"] != "WARN" || lines[i]["method"] != http.MethodGet {
			t.Errorf("%s %s: want one more WARN read refusal naming GET, got %v", route.method, route.path, lines)
		}
	}
	if strings.Contains(logs.String(), "rbac_mutation") {
		t.Errorf("a refused read was logged as an RBAC mutation:\n%s", logs.String())
	}
}

func TestAPIKeys_MalformedRequestsAre400AndUnhonourableOnes422(t *testing.T) {
	api := newAPIKeyAPI(t)
	created := api.create("target")
	for _, tc := range []struct {
		method, path string
		body         any
		want         int
	}{
		{http.MethodDelete, "/apiKeys/not-a-uuid", nil, http.StatusBadRequest},
		{http.MethodPut, "/apiKeys/not-a-uuid", `{"label":"x"}`, http.StatusBadRequest},
		{http.MethodGet, "/apiKeys?pageNumber=abc", nil, http.StatusBadRequest},
		{http.MethodGet, "/apiKeys?pageNumber=0", nil, http.StatusUnprocessableEntity},
		{http.MethodPut, "/apiKeys/" + created.ID, `{"label":"  "}`, http.StatusUnprocessableEntity},
	} {
		rec := api.call(tc.method, tc.path, tc.body, signedInSuperAdmin())
		if rec.Code != tc.want {
			t.Errorf("%s %s = %d, want %d; body %s", tc.method, tc.path, rec.Code, tc.want, rec.Body.String())
			continue
		}
		if tc.want == http.StatusUnprocessableEntity && len(decodeError(t, rec).ValidationDetails) == 0 {
			t.Errorf("%s %s: the 422 names no field: %s", tc.method, tc.path, rec.Body.String())
		}
	}
}

func TestAPIKeys_WritesRefuseEveryNonInteractiveAuthMethod(t *testing.T) {
	api := newAPIKeyAPI(t)
	for _, method := range []string{
		"slack", "teams", "email", "whatsapp", "webhook",
		"event_mesh", "scheduled_task", "synthetic", "eval_system", "", "development",
	} {
		caller := apiKeysCaller("admin@example.com", method, "*")
		for _, route := range apiKeysWriteCalls(missingID) {
			if rec := api.call(route.method, route.path, route.body, caller); rec.Code != http.StatusForbidden {
				t.Errorf("%s %s with authMethod %q = %d, want 403", route.method, route.path, method, rec.Code)
			}
		}
	}
}

func TestAPIKeys_WritesAdmitEveryInteractiveAuthMethod(t *testing.T) {
	api := newAPIKeyAPI(t)
	for _, method := range []string{auth.AuthMethodOIDC, auth.AuthMethodSAMAccessToken} {
		t.Run(method, func(t *testing.T) {
			person := apiKeysCaller("admin@example.com", method, "*")
			rec := api.call(http.MethodPost, "/apiKeys", api.validCreateBody("by-"+method), person)
			expectStatus(t, rec, http.StatusCreated)
			id := decodeData[CreatedAPIKey](t, rec).ID
			expectStatus(t, api.call(http.MethodPut, "/apiKeys/"+id, map[string]any{"label": "renamed-" + method}, person), http.StatusOK)
			expectStatus(t, api.call(http.MethodDelete, "/apiKeys/"+id, nil, person), http.StatusNoContent)
		})
	}
}

func TestAPIKeys_CreateAndRenameRecordTheCaller(t *testing.T) {
	logs := captureLogs(t)
	api := newAPIKeyAPI(t)
	created := api.create("attributed")
	if created.CreatedBy != "admin@example.com" || created.UpdatedBy != "admin@example.com" {
		t.Errorf("createdBy/updatedBy = %q/%q, want the caller", created.CreatedBy, created.UpdatedBy)
	}

	second := apiKeysCaller("second@example.com", auth.AuthMethodSAMAccessToken, "*")
	rec := api.call(http.MethodPut, "/apiKeys/"+created.ID, map[string]any{"label": "renamed"}, second)
	expectStatus(t, rec, http.StatusOK)
	if renamed := decodeData[APIKey](t, rec); renamed.CreatedBy != "admin@example.com" || renamed.UpdatedBy != "second@example.com" {
		t.Errorf("after a rename createdBy/updatedBy = %q/%q, want the creator and the renamer", renamed.CreatedBy, renamed.UpdatedBy)
	}
	if strings.Contains(logs.String(), created.Secret) {
		t.Error("the secret reached the logs")
	}
}

func TestAPIKeys_CreationFollowsTheConfiguredAuthorizationMode(t *testing.T) {
	for mode, want := range map[string]int{
		"default_rbac": http.StatusUnprocessableEntity,
		"deny_all":     http.StatusForbidden,
		"none":         http.StatusForbidden,
		"":             http.StatusForbidden,
	} {
		t.Run("mode="+mode, func(t *testing.T) {
			core, _ := newTestCoreWithConfig(t, nil, func(d *entplatform.RouteDeps) { d.AuthorizationMode = mode })
			req := newJSONRequest(t, http.MethodPost, "/apiKeys", `{}`)
			req = req.WithContext(auth.ContextWithSubject(req.Context(), signedInSuperAdmin()))
			rec := serveBare(t, core, req)
			expectStatus(t, rec, want)
			if msg := decodeError(t, rec).Message; want == http.StatusForbidden && !strings.Contains(msg, "default_rbac") {
				t.Errorf("403 message %q does not name default_rbac", msg)
			}
		})
	}
}

func TestAPIKeys_OnlyTheRBACAuthorityServesThem(t *testing.T) {
	core, deps := newTestCoreWithConfig(t, nil, func(d *entplatform.RouteDeps) {
		d.IsRBACAuthority = false
		d.AuthorizationMode = "default_rbac"
	})
	if deps.APIKeySvc != nil {
		t.Fatal("APIKeySvc is set on a platform that is not the RBAC authority")
	}
	requireAPIKeyRoutesAnswer404(t, core)
}

func TestAPIKeys_ListAndDeleteSurviveAnUnwiredResolver(t *testing.T) {
	api := newAPIKeyAPIWith(t, (*entplatform.AuthzLookupHandler)(nil), "default_rbac")
	admin := signedInSuperAdmin()
	key, err := api.keys.Create(t.Context(), store.APIKey{
		UserID: api.owner.ID, Label: "existing", KeyHash: "degraded-hash", Prefix: "pfx12345",
		CreatedBy: "test", UpdatedBy: "test",
	}, []string{keyScope}, 10, time.Now().UnixMilli())
	if err != nil {
		t.Fatalf("seed key: %v", err)
	}

	expectStatus(t, api.call(http.MethodPost, "/apiKeys", api.validCreateBody("new"), admin), http.StatusServiceUnavailable)
	listed := api.call(http.MethodGet, "/apiKeys", nil, admin)
	expectStatus(t, listed, http.StatusOK)
	if got := labels(decodeList[APIKey](t, listed).Data); !slices.Equal(got, []string{"existing"}) {
		t.Errorf("listed %v, want the existing key", got)
	}
	expectStatus(t, api.call(http.MethodDelete, "/apiKeys/"+key.ID, nil, admin), http.StatusNoContent)
}

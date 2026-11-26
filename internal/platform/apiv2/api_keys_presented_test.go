// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/apikey"
	samauth "github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/budget/budgettest"
	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

const (
	apiKeysPresentedSecret = "samk_AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8"
	apiKeysPersonToken     = "eyJhbGciOiJFUzI1NiJ9.e30.sig"
	apiKeysPresentedID     = "key-1"
	apiKeysPresentedOwner  = "system:ci-deployer"
	apiKeysInternalPrefix  = "/api/internal/platform"
)

type apiKeysFixedAuthority struct{ scopes []string }

func (a apiKeysFixedAuthority) Validate(context.Context, string) (apikey.Resolution, error) {
	return apikey.Resolution{
		KeyID: apiKeysPresentedID, OwnerID: "0192f3aa-7c31-70b2-9e4d-9b1f2a5c8e77",
		OwnerSub: apiKeysPresentedOwner, Scopes: a.scopes,
	}, nil
}

type apiKeysFixedAuthenticator struct{ identity *samauth.Identity }

func (a apiKeysFixedAuthenticator) Authenticate(context.Context, string, string) (*samauth.Identity, error) {
	return a.identity, nil
}

func apiKeysPlatformBehind(t *testing.T, person *samauth.Identity, keyScopes ...string) *platpkg.PlatformCore {
	t.Helper()
	middleware := entplatform.NewAuthMiddleware(apiKeysFixedAuthenticator{identity: person}, nil,
		apiKeysFixedAuthority{scopes: keyScopes}, slog.Default())
	core, _ := newTestCoreWithAuth(t, true, budgettest.NewGate(), nil, middleware,
		func(d *entplatform.RouteDeps) { d.AuthorizationMode = "default_rbac" })
	return core
}

func apiKeysPresentedCore(t *testing.T, keyScopes ...string) *platpkg.PlatformCore {
	t.Helper()
	return apiKeysPlatformBehind(t, &samauth.Identity{UserID: "admin@example.com", Scopes: []string{"*:*:*"}}, keyScopes...)
}

func apiKeysPresent(t *testing.T, core *platpkg.PlatformCore, method, target, body, bearer string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+bearer)
	req.Header.Set("Content-Type", "application/json")
	return serveBare(t, core, req)
}

func apiKeysCreateRole(t *testing.T, core *platpkg.PlatformCore, bearer string) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(map[string]any{"name": "pipeline-role", "scopes": []string{rbac.ScopeRBACRead}})
	if err != nil {
		t.Fatal(err)
	}
	return apiKeysPresent(t, core, http.MethodPost, platpkg.APIV2Prefix+"/rbac/roles", string(body), bearer)
}

func TestAPIKeys_APresentedKeyReachesOnlyThePublishedRoutes(t *testing.T) {
	core := apiKeysPresentedCore(t, "*:*:*")
	v2, web, id := platpkg.APIV2Prefix, apiKeysInternalPrefix, missingID
	for _, tc := range []struct {
		method, target string
		admitted       bool
	}{
		{http.MethodGet, v2 + "/agents", true},
		{http.MethodGet, v2 + "/evaluations/runs/" + id, true},
		{http.MethodPut, v2 + "/evaluations/experiments/" + id + "/trigger", true},
		{http.MethodGet, v2 + "/evaluations/runs/" + id + "/results", true},
		{http.MethodPut, v2 + "/evaluations/runs/" + id + "/cancel", true},
		{http.MethodGet, v2 + "/evaluations/runResults/" + id + "/executionData", true},
		{http.MethodGet, v2 + "/evaluations/runResults/" + id + "/taskEvents", true},
		{http.MethodGet, v2 + "/evaluations/experiments", true},
		{http.MethodGet, v2 + "/serverCapabilities", true},
		{http.MethodPost, v2 + "/agentDeployments", true},
		{http.MethodPut, v2 + "/webuiSettings", true},
		{http.MethodGet, v2 + "/apiKeys", true},
		{http.MethodGet, v2 + "/health", true},
		{http.MethodGet, v2 + "/version", true},
		{http.MethodGet, v2 + "/linkAgents", true},
		{http.MethodPut, v2 + "/linkAgents/" + id + "/secretsDiff", true},
		{http.MethodPut, v2 + "/linkAgents/" + id + "/rotateToken", false},
		{http.MethodGet, v2 + "/rbac/serviceAccounts/" + id + "/toolAuth/agents/support_agent", false},
		{http.MethodPut, v2 + "/rbac/serviceAccounts/" + id + "/toolAuth/begin", false},
		{http.MethodPut, v2 + "/rbac/serviceAccounts/" + id + "/toolAuth/disconnect", false},
		{http.MethodGet, v2 + "/remoteAgents", false},
		{http.MethodGet, v2 + "/users", false},
		{http.MethodGet, v2 + "/users/" + id + "/apiKeys", false},
		{http.MethodGet, v2 + "/evaluations/runs/" + id + "/export", false},
		{http.MethodDelete, v2 + "/evaluations/runs/" + id, false},
		{http.MethodPut, v2 + "/productAnalytics/ingest/cli", false},
		{http.MethodPost, web + "/aiAssistant/generateAgent", false},
		{http.MethodPost, web + "/builder/sessions/" + id + "/test-agent", false},
		{http.MethodGet, web + "/builder/sessions/" + id + "/test-agent", false},
		{http.MethodDelete, web + "/builder/sessions/" + id + "/test-agent", false},
		{http.MethodGet, web + "/builder/test-agents/lookup", false},
	} {
		t.Run(tc.method+" "+tc.target, func(t *testing.T) {
			rec := apiKeysPresent(t, core, tc.method, tc.target, `{}`, apiKeysPresentedSecret)
			refused := rec.Code == http.StatusForbidden && strings.Contains(rec.Body.String(), keyAllowlistRefusal)
			if refused == tc.admitted {
				t.Errorf("status %d, want admitted=%v; body %s", rec.Code, tc.admitted, rec.Body.String())
			}
		})
	}
}

func TestAPIKeys_APresentedKeyOnAnUnmountedRouteGetsMuxNotFoundNotKeyGate403(t *testing.T) {
	core := apiKeysPresentedCore(t, "*:*:*")
	rec := apiKeysPresent(t, core, http.MethodGet, platpkg.APIV2Prefix+"/neverMounted", "", apiKeysPresentedSecret)
	if rec.Code != http.StatusNotFound || strings.Contains(rec.Body.String(), keyAllowlistRefusal) {
		t.Errorf("status %d, want 404 without the key refusal; body %s", rec.Code, rec.Body.String())
	}
}

func TestAPIKeys_APresentedKeyReadsKeysButCannotManageThem(t *testing.T) {
	core := apiKeysPresentedCore(t, "*:*:*")
	for _, route := range apiKeysWriteCalls(missingID) {
		rec := apiKeysPresent(t, core, route.method, platpkg.APIV2Prefix+route.path, `{}`, apiKeysPresentedSecret)
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s %s = %d, want 403", route.method, route.path, rec.Code)
			continue
		}
		if msg := decodeError(t, rec).Message; !strings.Contains(msg, "API keys can only be") {
			t.Errorf("%s %s refusal %q is not the key-management one", route.method, route.path, msg)
		}
		if strings.Contains(rec.Body.String(), apiKeysPresentedSecret) {
			t.Errorf("%s %s echoes the presented key: %s", route.method, route.path, rec.Body.String())
		}
	}
	expectStatus(t, apiKeysPresent(t, core, http.MethodGet, platpkg.APIV2Prefix+"/apiKeys", "", apiKeysPresentedSecret), http.StatusOK)
}

func TestAPIKeys_WritesRefuseASystemPrincipalThroughTheRealMiddleware(t *testing.T) {
	for userID, refused := range map[string]bool{"system:pipeline": true, "person@example.com": false} {
		t.Run(userID, func(t *testing.T) {
			core := apiKeysPlatformBehind(t, &samauth.Identity{UserID: userID, Scopes: []string{"*:*:*"}})
			for _, route := range apiKeysWriteCalls(missingID) {
				rec := apiKeysPresent(t, core, route.method, platpkg.APIV2Prefix+route.path, `{}`, apiKeysPersonToken)
				if (rec.Code == http.StatusForbidden) != refused {
					t.Errorf("%s %s as %s = %d, want refused=%v; body %s", route.method, route.path, userID, rec.Code, refused, rec.Body.String())
				}
			}
		})
	}
}

func TestAPIKeys_AKeysRBACWriteIsAuditedWithItsID(t *testing.T) {
	logs := captureLogs(t)
	core := apiKeysPresentedCore(t, "*:*:*")
	expectStatus(t, apiKeysCreateRole(t, core, apiKeysPresentedSecret), http.StatusCreated)

	lines := apiKeysAuditLines(logs.String(), "created", entplatform.AuditResourceRole)
	if len(lines) != 1 {
		t.Fatalf("got %d created role audit lines, want 1:\n%s", len(lines), logs.String())
	}
	if lines[0]["actor"] != apiKeysPresentedOwner || lines[0]["credentialID"] != apiKeysPresentedID {
		t.Errorf("actor %v and credentialID %v, want the key's system user and id", lines[0]["actor"], lines[0]["credentialID"])
	}
	if strings.Contains(logs.String(), apiKeysPresentedSecret) || strings.Contains(logs.String(), apikey.Hash(apiKeysPresentedSecret)) {
		t.Error("the logs carry the key's plaintext or hash; only its id may appear")
	}
}

func TestAPIKeys_AKeysRefusedRBACWriteIsAuditedWithItsID(t *testing.T) {
	logs := captureLogs(t)
	core := apiKeysPresentedCore(t, rbac.ScopeRBACRead)
	expectStatus(t, apiKeysCreateRole(t, core, apiKeysPresentedSecret), http.StatusForbidden)

	lines := apiKeysAuditLines(logs.String(), "rejected", entplatform.AuditResourceRole)
	if len(lines) != 1 {
		t.Fatalf("got %d rejected role audit lines, want 1:\n%s", len(lines), logs.String())
	}
	if lines[0]["credentialID"] != apiKeysPresentedID {
		t.Errorf("credentialID %v, want %s: a key probing a surface it cannot use is the record most worth attributing", lines[0]["credentialID"], apiKeysPresentedID)
	}
}

func TestAPIKeys_APersonsRBACWriteCarriesNoCredentialID(t *testing.T) {
	logs := captureLogs(t)
	core := apiKeysPresentedCore(t, "*:*:*")
	expectStatus(t, apiKeysCreateRole(t, core, apiKeysPersonToken), http.StatusCreated)

	lines := apiKeysAuditLines(logs.String(), "created", entplatform.AuditResourceRole)
	if len(lines) != 1 {
		t.Fatalf("got %d created role audit lines, want 1:\n%s", len(lines), logs.String())
	}
	if credentialID, present := lines[0]["credentialID"]; present {
		t.Errorf("credentialID %v on a person's write; the field must be absent, not blank", credentialID)
	}
}

func TestAPIKeys_KeyManagementRefusalsNameTheKey(t *testing.T) {
	for name, tc := range map[string]struct {
		method, path, body, msg string
		scopes                  []string
	}{
		"a key creating a key": {
			method: http.MethodPost, path: "/apiKeys", msg: "rbac audit", scopes: []string{"*:*:*"},
			body: `{"userId":"` + missingID + `","label":"successor","scopes":["*:*:*"],"neverExpires":true}`,
		},
		"a key deleting a key": {
			method: http.MethodDelete, path: "/apiKeys/" + missingID, msg: "rbac audit", scopes: []string{"*:*:*"},
		},
		"a narrow key reading the inventory": {
			method: http.MethodGet, path: "/apiKeys", msg: "api key read refused", scopes: []string{rbac.ScopeAgentInvokeAny},
		},
	} {
		t.Run(name, func(t *testing.T) {
			logs := captureLogs(t)
			core := apiKeysPresentedCore(t, tc.scopes...)
			expectStatus(t, apiKeysPresent(t, core, tc.method, platpkg.APIV2Prefix+tc.path, tc.body, apiKeysPresentedSecret), http.StatusForbidden)

			lines := apiKeysLogLines(logs.String(), tc.msg)
			if len(lines) != 1 {
				t.Fatalf("got %d %q lines, want 1:\n%s", len(lines), tc.msg, logs.String())
			}
			if lines[0]["credentialID"] != apiKeysPresentedID {
				t.Errorf("%q line %v, want credentialID %s", tc.msg, lines[0], apiKeysPresentedID)
			}
		})
	}
}

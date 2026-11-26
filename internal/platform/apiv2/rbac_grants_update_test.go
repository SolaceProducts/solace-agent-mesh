// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

func loggedRBACAudit(logs, action, resource, resourceID string) bool {
	for line := range strings.Lines(logs) {
		var entry struct {
			Msg        string `json:"msg"`
			Event      string `json:"event"`
			Action     string `json:"action"`
			Resource   string `json:"resource"`
			ResourceID string `json:"resourceId"`
		}
		if json.Unmarshal([]byte(line), &entry) == nil && entry.Msg == "rbac audit" && entry.Event == "rbac_mutation" &&
			entry.Action == action && entry.Resource == resource && (resourceID == "" || entry.ResourceID == resourceID) {
			return true
		}
	}
	return false
}

func TestGrants_APIKeyReachesThePublishedRoutes(t *testing.T) {
	api := newRBACAPI(t)
	role := api.createRole("key-managed")
	api.seedUser(userSeed{sub: "nan-sub", email: "nan@example.com"})
	grant := api.createGrant(emailSubject("nan@example.com"), role.ID)

	asKey := func(method, path string, body any, scopes ...string) *httptest.ResponseRecorder {
		req := newJSONRequest(t, method, path, body)
		req = req.WithContext(auth.ContextWithSubject(req.Context(), holdingKey(scopes...)))
		rec := httptest.NewRecorder()
		api.core.Handler().ServeHTTP(rec, req)
		return rec
	}

	expectStatus(t, asKey(http.MethodGet, "/rbac/grants", nil, rbac.ScopeRBACRead), http.StatusOK)
	expectStatus(t, asKey(http.MethodPatch, "/rbac/grants/"+grant.ID, map[string]any{"subject": subSubject("nan-sub")}, rbac.ScopeRBACRead), http.StatusForbidden)

	rec := asKey(http.MethodPatch, "/rbac/grants/"+grant.ID, map[string]any{"subject": subSubject("nan-sub")}, "*")
	expectStatus(t, rec, http.StatusOK)
	if got := decodeData[Grant](t, rec); got.Subject == nil || got.Subject.Type != "sub" {
		t.Errorf("subject %+v after the key's update", got.Subject)
	}

	created := asKey(http.MethodPost, "/rbac/grants", map[string]any{"subject": emailSubject("pending-key@example.com"), "roleId": role.ID}, "*")
	expectStatus(t, created, http.StatusCreated)
	expectStatus(t, asKey(http.MethodDelete, "/rbac/grants/"+decodeData[Grant](t, created).ID, nil, "*"), http.StatusNoContent)
}

func TestGrants_UpdateAuditsASuccessAndARefusal(t *testing.T) {
	logs := captureLogs(t)
	api := newRBACAPI(t)
	role := api.createRole("update-audited")
	api.seedUser(userSeed{sub: "iris-sub", email: "iris@example.com"})
	grant := api.createGrant(emailSubject("iris@example.com"), role.ID)
	body := map[string]any{"subject": subSubject("iris-sub")}

	expectStatus(t, api.call(http.MethodPatch, "/rbac/grants/"+grant.ID, body, rbac.ScopeRBACRead), http.StatusForbidden)
	if !loggedRBACAudit(logs.String(), "rejected", entplatform.AuditResourceGrant, "") {
		t.Errorf("no rejected audit entry for a refused grant update:\n%s", logs.String())
	}
	if loggedRBACAudit(logs.String(), "updated", entplatform.AuditResourceGrant, grant.ID) {
		t.Fatal("a refused update was audited as updated")
	}

	expectStatus(t, api.call(http.MethodPatch, "/rbac/grants/"+grant.ID, body), http.StatusOK)
	if !loggedRBACAudit(logs.String(), "updated", entplatform.AuditResourceGrant, grant.ID) {
		t.Errorf("no updated audit entry for grant %s:\n%s", grant.ID, logs.String())
	}
}

func TestGrants_UpdateWithTheRecordedSubjectChangesNothing(t *testing.T) {
	logs := captureLogs(t)
	api := newRBACAPI(t)
	role := api.createRole("update-noop")
	api.seedUser(userSeed{sub: "jo-sub", email: "jo@example.com"})
	grant := api.createGrant(emailSubject("jo@example.com"), role.ID)

	rec := api.call(http.MethodPatch, "/rbac/grants/"+grant.ID, map[string]any{"subject": emailSubject("jo@example.com")})
	expectStatus(t, rec, http.StatusOK)
	got := decodeData[Grant](t, rec)

	if got.UpdatedTime != grant.UpdatedTime || got.UpdatedBy == nil || grant.UpdatedBy == nil || *got.UpdatedBy != *grant.UpdatedBy {
		t.Errorf("updatedTime %v and updatedBy %v changed from %v and %v", got.UpdatedTime, got.UpdatedBy, grant.UpdatedTime, grant.UpdatedBy)
	}
	if loggedRBACAudit(logs.String(), "updated", entplatform.AuditResourceGrant, "") {
		t.Errorf("an update that changed nothing was audited:\n%s", logs.String())
	}
}

func TestGrants_UpdateKeepsAnIssuerOnANonSubSubjectAsCreateDoes(t *testing.T) {
	api := newRBACAPI(t)
	role := api.createRole("issuer-on-email")
	api.seedUser(userSeed{sub: "kai-sub", email: "kai@example.com"})
	withIssuer := map[string]any{"type": "email", "value": "kai@example.com", "issuer": "https://idp.example.com"}

	created := api.createGrant(withIssuer, role.ID)
	if created.Subject == nil || created.Subject.Issuer == nil || *created.Subject.Issuer != "https://idp.example.com" {
		t.Fatalf("create kept subject %+v, want the issuer as sent", created.Subject)
	}

	other := api.createGrant(subSubject("kai-sub"), api.createRole("issuer-on-email-other").ID)
	rec := api.call(http.MethodPatch, "/rbac/grants/"+other.ID, map[string]any{"subject": withIssuer})
	expectStatus(t, rec, http.StatusOK)
	if s := decodeData[Grant](t, rec).Subject; s == nil || s.Type != "email" || s.Issuer == nil || *s.Issuer != "https://idp.example.com" {
		t.Errorf("update kept subject %+v, want the issuer as sent, as create does", s)
	}
}

func TestGrants_UpdateRefusesANullSubject(t *testing.T) {
	api := newRBACAPI(t)
	role := api.createRole("subject-required")
	api.seedUser(userSeed{sub: "lee-sub", email: "lee@example.com"})
	grant := api.createGrant(emailSubject("lee@example.com"), role.ID)

	expectDetailSaying(t, api.call(http.MethodPatch, "/rbac/grants/"+grant.ID, `{"subject":null}`), "subject", "cannot be empty")
}

func TestGrants_UpdateRejectsAnUnknownFieldInSubject(t *testing.T) {
	api := newRBACAPI(t)
	role := api.createRole("subject-unknown-field")
	api.seedUser(userSeed{sub: "max-sub", email: "max@example.com"})
	grant := api.createGrant(emailSubject("max@example.com"), role.ID)

	rec := api.call(http.MethodPatch, "/rbac/grants/"+grant.ID, `{"subject":{"type":"email","value":"max@example.com","foo":1}}`)
	expectDetail(t, rec, http.StatusBadRequest, "subject.foo")
}

// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

type rbacGateWrite struct {
	method, path, auditResource string
	body                        any
}

func rbacGateWrites(id string) []rbacGateWrite {
	role := map[string]any{"name": "gate_probe", "scopes": []string{rbac.ScopeRBACRead}}
	mapping := map[string]any{"name": "n", "oidcProvider": "okta", "claimKey": "groups", "claimValue": "v", "roleIds": []string{id}}
	roleIDs := map[string]any{"roleIds": []string{id}}
	toolAuth := map[string]any{"agentName": "support_agent", "credentialKey": "google_drive"}
	return []rbacGateWrite{
		{http.MethodPost, "/rbac/roles", entplatform.AuditResourceRole, role},
		{http.MethodPut, "/rbac/roles/" + id, entplatform.AuditResourceRole, role},
		{http.MethodDelete, "/rbac/roles/" + id, entplatform.AuditResourceRole, nil},
		{http.MethodPost, "/rbac/grants", entplatform.AuditResourceGrant, map[string]any{"subject": emailSubject("x@example.com"), "roleId": id}},
		{http.MethodPatch, "/rbac/grants/" + id, entplatform.AuditResourceGrant, map[string]any{"subject": emailSubject("x@example.com")}},
		{http.MethodDelete, "/rbac/grants/" + id, entplatform.AuditResourceGrant, nil},
		{http.MethodPost, "/rbac/systemUsers", entplatform.AuditResourceSystemUser, map[string]any{"name": "gate_probe_sysuser", "roleIds": []string{id}}},
		{http.MethodPut, "/rbac/systemUsers/" + id, entplatform.AuditResourceSystemUser, roleIDs},
		{http.MethodDelete, "/rbac/systemUsers/" + id, entplatform.AuditResourceSystemUser, nil},
		{http.MethodPost, "/rbac/serviceAccounts", entplatform.AuditResourceSystemUser, map[string]any{"name": "gate_probe_svcacct", "roleIds": []string{id}}},
		{http.MethodPut, "/rbac/serviceAccounts/" + id, entplatform.AuditResourceSystemUser, roleIDs},
		{http.MethodDelete, "/rbac/serviceAccounts/" + id, entplatform.AuditResourceSystemUser, nil},
		{http.MethodPost, "/rbac/claimMappings", entplatform.AuditResourceClaimMapping, mapping},
		{http.MethodPut, "/rbac/claimMappings/" + id, entplatform.AuditResourceClaimMapping, mapping},
		{http.MethodDelete, "/rbac/claimMappings/" + id, entplatform.AuditResourceClaimMapping, nil},
		{http.MethodPut, "/rbac/defaultRoles", entplatform.AuditResourceDefaultRoles, roleIDs},
		{http.MethodPut, "/users/" + id + "/roleGrants", entplatform.AuditResourceGrant, roleIDs},
		{http.MethodDelete, "/users/" + id + "/identityClaims", entplatform.AuditResourceIdentityClaims, nil},
		{http.MethodPut, "/rbac/serviceAccounts/" + id + "/toolAuth/begin", entplatform.AuditResourceSystemUserToolAuth, toolAuth},
		{http.MethodPut, "/rbac/serviceAccounts/" + id + "/toolAuth/disconnect", entplatform.AuditResourceSystemUserToolAuth, toolAuth},
	}
}

func rbacGateRefusals(logs, resource, actor string) int {
	n := 0
	for line := range strings.Lines(logs) {
		var entry struct {
			Msg      string `json:"msg"`
			Level    string `json:"level"`
			Event    string `json:"event"`
			Action   string `json:"action"`
			Resource string `json:"resource"`
			Actor    string `json:"actor"`
		}
		if json.Unmarshal([]byte(line), &entry) == nil && entry.Msg == "rbac audit" && entry.Level == "WARN" &&
			entry.Event == "rbac_mutation" && entry.Action == "rejected" && entry.Resource == resource && entry.Actor == actor {
			n++
		}
	}
	return n
}

func TestRBACWrites_RefuseEveryoneButASuperAdmin(t *testing.T) {
	logs := captureLogs(t)
	api := newRBACAPI(t)

	for _, actor := range [][]string{
		{"rbac:_:create", "rbac:_:update", "rbac:_:delete", rbac.ScopeRBACRead},
		{"rbac:_:*"},
		{"rbac:*:*"},
		{"agent:*:*", "connector:*:*", "rbac:*:*"},
	} {
		for _, w := range rbacGateWrites(missingID) {
			before := rbacGateRefusals(logs.String(), w.auditResource, "sam@dev.local")
			rec := api.call(w.method, w.path, w.body, actor...)
			expectMessage(t, rec, http.StatusForbidden, msgNotSuperuser)
			if got := rbacGateRefusals(logs.String(), w.auditResource, "sam@dev.local") - before; got != 1 {
				t.Errorf("%s %s by %v: %d new rejected %s audit lines naming the actor's email, want 1", w.method, w.path, actor, got, w.auditResource)
			}
		}
	}
}

func TestRBACWrites_AdmitEitherUniversalSpelling(t *testing.T) {
	api := newRBACAPI(t)
	for i, actor := range []string{"*", "*:*:*"} {
		target := api.createRole(fmt.Sprintf("target %d", i))
		for _, w := range rbacGateWrites(target.ID) {
			if rec := api.call(w.method, w.path, w.body, actor); rec.Code == http.StatusForbidden {
				t.Errorf("%s %s by %s refused: %s", w.method, w.path, actor, rec.Body.String())
			}
		}
	}
}

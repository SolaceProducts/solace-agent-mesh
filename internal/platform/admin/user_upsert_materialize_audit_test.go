// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func TestUserUpsertHandler_MaterializationAudits(t *testing.T) {
	db := store.TestDB(t)
	usersRepo := store.NewUsersRepo(db)
	rbacRepo := store.NewRBACRepo(db)
	ctx := context.Background()

	role := &store.RBACRole{Name: "admin", CreatedBy: "sys", UpdatedBy: "sys"}
	if err := rbacRepo.CreateRole(ctx, role, nil, nil); err != nil {
		t.Fatalf("create role: %v", err)
	}
	email := "alice@example.com"
	u := &store.User{Issuer: "https://idp", Sub: "sub-1", Email: &email, CreatedBy: "sys", UpdatedBy: "sys"}
	if err := usersRepo.UpsertUser(ctx, u, nil); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if err := rbacRepo.CreatePendingUserRole(ctx, &store.RBACPendingUserRole{
		SubjectType: store.PendingSubjectEmail, SubjectValue: email,
		RoleID: role.ID, CreatedBy: "admin", UpdatedBy: "admin",
	}); err != nil {
		t.Fatalf("create pending: %v", err)
	}

	var buf bytes.Buffer
	h := NewUserUpsertHandler(usersRepo, nil, UserUpsertConfig{Namespace: "ns"},
		slog.New(slog.NewJSONHandler(&buf, nil)))
	h.SetRBACRepo(rbacRepo)

	h.refreshAfterUserWrite(ctx, u, "login", true)

	found := false
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if line == "" {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("audit line not JSON: %v", err)
		}
		if rec["event"] != "rbac_mutation" || rec["action"] != auditActionMaterialized {
			continue
		}
		if rec["resource"] != auditResourceGrant {
			t.Errorf("resource = %v, want %s", rec["resource"], auditResourceGrant)
		}
		if want := u.ID + "|" + role.ID; rec["resourceId"] != want {
			t.Errorf("resourceId = %v, want %s", rec["resourceId"], want)
		}
		found = true
	}
	if !found {
		t.Fatalf("no materialized-grant audit line emitted; got: %s", buf.String())
	}
}

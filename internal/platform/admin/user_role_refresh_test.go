// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

func TestMaterialization_VisibleToComposerSameLogin(t *testing.T) {
	db := store.TestDB(t)
	rbacRepo := store.NewRBACRepo(db)
	usersRepo := store.NewUsersRepo(db)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	composer := rbac.NewComposer(rbac.YAMLState{}, newRBACDBSource(rbacRepo), logger)
	ctx := context.Background()
	if err := composer.Rebuild(ctx); err != nil {
		t.Fatalf("rebuild: %v", err)
	}

	roleID := seedRole(ctx, t, rbacRepo, composer, "admin")
	u := &store.User{Issuer: "https://idp", Sub: "sub-1", CreatedBy: "x", UpdatedBy: "x"}
	if err := usersRepo.UpsertUser(ctx, u, nil); err != nil {
		t.Fatalf("provision user: %v", err)
	}
	if err := rbacRepo.CreatePendingUserRole(ctx, &store.RBACPendingUserRole{
		SubjectType: store.PendingSubjectSub, SubjectValue: "sub-1", Issuer: "https://idp",
		RoleID: roleID, CreatedBy: "a", UpdatedBy: "a",
	}); err != nil {
		t.Fatalf("create pending: %v", err)
	}

	if res, _ := composer.Lookup("sub-1", "webui", map[string]any{"sam_user_id": u.ID}); slices.Contains(res.Roles, "admin") {
		t.Fatal("precondition: role should not resolve before materialization")
	}

	h := NewUserUpsertHandler(usersRepo, nil, UserUpsertConfig{Namespace: "ns"}, logger)
	h.SetRBACRepo(rbacRepo)
	h.SetGrantRefresh(composer, nil, nil)
	h.refreshAfterUserWrite(ctx, u, "login", true)

	res, _ := composer.Lookup("sub-1", "webui", map[string]any{"sam_user_id": u.ID})
	if !slices.Contains(res.Roles, "admin") {
		t.Fatalf("materialized grant not visible to composer on the same login: roles=%v", res.Roles)
	}
}

func TestSetUserRoles_AuditsAndRefreshes(t *testing.T) {
	db := store.TestDB(t)
	rbacRepo := store.NewRBACRepo(db)
	usersRepo := store.NewUsersRepo(db)
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	composer := rbac.NewComposer(rbac.YAMLState{}, newRBACDBSource(rbacRepo), logger)
	ctx := context.Background()
	if err := composer.Rebuild(ctx); err != nil {
		t.Fatalf("rebuild: %v", err)
	}

	roleID := seedRole(ctx, t, rbacRepo, composer, "admin")
	email := "alice@example.com"
	u := &store.User{Issuer: "https://idp", Sub: "s1", Email: &email, CreatedBy: "x", UpdatedBy: "x"}
	if err := usersRepo.UpsertUser(ctx, u, nil); err != nil {
		t.Fatalf("provision user: %v", err)
	}

	svc := NewUsersService(usersRepo, rbacRepo, logger)
	svc.SetGrantRefresh(composer, nil, nil)
	if _, err := svc.SetUserRoleGrants(ctx, u.ID, []string{roleID}, "admin@corp.com", []string{"*"}); err != nil {
		t.Fatalf("SetUserRoles: %v", err)
	}

	res, _ := composer.Lookup("s1", "webui", map[string]any{"sam_user_id": u.ID})
	if !slices.Contains(res.Roles, "admin") {
		t.Errorf("SetUserRoles not visible to composer after refresh: roles=%v", res.Roles)
	}

	found := false
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		var rec map[string]any
		if json.Unmarshal([]byte(line), &rec) != nil {
			continue
		}
		if rec["event"] == "rbac_mutation" && rec["action"] == auditActionUpdated && rec["resourceId"] == u.ID {
			found = true
		}
	}
	if !found {
		t.Errorf("no rbac_mutation audit line for the SetUserRoles write; got: %s", buf.String())
	}
}

func TestMaterialization_ReachesALookupOnAnotherGWE(t *testing.T) {
	db := store.TestDB(t)
	rbacRepo := store.NewRBACRepo(db)
	usersRepo := store.NewUsersRepo(db)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx := t.Context()
	composerA := rbac.NewComposer(rbac.YAMLState{}, newRBACDBSource(rbacRepo), logger)
	composerB := rbac.NewComposer(rbac.YAMLState{}, newRBACDBSource(rbacRepo), logger)
	roleID := seedRole(ctx, t, rbacRepo, composerB, "admin")
	if err := composerA.Rebuild(ctx); err != nil {
		t.Fatalf("rebuild A: %v", err)
	}
	u := &store.User{Issuer: "https://idp", Sub: "sub-1", CreatedBy: "x", UpdatedBy: "x"}
	if err := usersRepo.UpsertUser(ctx, u, nil); err != nil {
		t.Fatalf("provision user: %v", err)
	}
	if err := rbacRepo.CreatePendingUserRole(ctx, &store.RBACPendingUserRole{
		SubjectType: store.PendingSubjectSub, SubjectValue: "sub-1", Issuer: "https://idp",
		RoleID: roleID, CreatedBy: "a", UpdatedBy: "a",
	}); err != nil {
		t.Fatalf("create pending: %v", err)
	}

	upsertB := NewUserUpsertHandler(usersRepo, nil, UserUpsertConfig{Namespace: "ns"}, logger)
	upsertB.SetRBACRepo(rbacRepo)
	upsertB.SetGrantRefresh(composerB, nil, nil)
	if !upsertB.refreshAfterUserWrite(ctx, u, "login", true) {
		t.Fatal("refreshAfterUserWrite did not report the materialized grant")
	}
	if !upsertB.refreshAfterUserWrite(ctx, u, "login", true) {
		t.Fatal("a retry of the same login did not report the grant its first attempt materialized")
	}
	if got := upsertReply(u.ID, true).Result.(map[string]any)["roles_changed"]; got != true {
		t.Fatalf("reply roles_changed = %v, want true", got)
	}
	if _, ok := upsertReply(u.ID, false).Result.(map[string]any)["roles_changed"]; ok {
		t.Fatal("reply carries roles_changed when no grant was materialized")
	}

	const ns = "rbac-login-other-gwe"
	brk := newTestBroker(t)
	startAuthzHandler(t, brk, composerA, ns)
	roles := func(gwCtx map[string]any) any {
		resp := roundTrip(t, nil, brk, ns, "sub-1", gwCtx)
		if resp.Error != nil {
			t.Fatalf("lookup: %+v", resp.Error)
		}
		return resp.Result.(map[string]any)["roles"]
	}
	if got := roles(map[string]any{"gateway_id": "webui", "sam_user_id": u.ID}); equalAny(got, []string{"admin"}) {
		t.Fatalf("precondition: GWE A resolved the grant without a rebuild: %v", got)
	}
	flagged := map[string]any{"gateway_id": "webui", "sam_user_id": u.ID, "roles_changed": true}
	if got := roles(flagged); !equalAny(got, []string{"admin"}) {
		t.Fatalf("lookup with roles_changed on GWE A: roles = %v, want [admin]", got)
	}

	auditorID := seedRoleUnseenByComposer(ctx, t, rbacRepo, "auditor", []string{"agent:*:read"})
	if err := rbacRepo.AddUserRole(ctx, u.ID, auditorID, "admin"); err != nil {
		t.Fatalf("grant auditor: %v", err)
	}
	got, _ := roles(flagged).([]any)
	names := make([]string, 0, len(got))
	for _, r := range got {
		name, _ := r.(string)
		names = append(names, name)
	}
	slices.Sort(names)
	if !slices.Equal(names, []string{"admin", "auditor"}) {
		t.Fatalf("second lookup with roles_changed on GWE A: roles = %v, want [admin auditor]", names)
	}
}

type failableSnapshotSource struct {
	inner rbac.DBSource
	fail  atomic.Bool
}

func (s *failableSnapshotSource) Snapshot(ctx context.Context) (*rbac.DBState, error) {
	if s.fail.Load() {
		return nil, errors.New("database unavailable")
	}
	return s.inner.Snapshot(ctx)
}

func TestFlaggedLookup_RebuildFailureAnswersFromTheCurrentGraph(t *testing.T) {
	db := store.TestDB(t)
	rbacRepo := store.NewRBACRepo(db)
	usersRepo := store.NewUsersRepo(db)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx := t.Context()
	source := &failableSnapshotSource{inner: newRBACDBSource(rbacRepo)}
	composer := rbac.NewComposer(rbac.YAMLState{}, source, logger)
	if err := composer.Rebuild(ctx); err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	roleID := seedRoleUnseenByComposer(ctx, t, rbacRepo, "admin", []string{"agent:*:read"})
	u := &store.User{Issuer: "https://idp", Sub: "sub-1", CreatedBy: "x", UpdatedBy: "x"}
	if err := usersRepo.UpsertUser(ctx, u, nil); err != nil {
		t.Fatalf("provision user: %v", err)
	}
	if err := rbacRepo.AddUserRole(ctx, u.ID, roleID, "admin"); err != nil {
		t.Fatalf("grant admin: %v", err)
	}

	const ns = "rbac-flagged-rebuild-fails"
	brk := newTestBroker(t)
	startAuthzHandler(t, brk, composer, ns)
	flagged := map[string]any{"gateway_id": "webui", "sam_user_id": u.ID, "roles_changed": true}
	roles := func() any {
		resp := roundTrip(t, nil, brk, ns, "sub-1", flagged)
		if resp.Error != nil {
			t.Fatalf("lookup: %+v", resp.Error)
		}
		return resp.Result.(map[string]any)["roles"]
	}

	source.fail.Store(true)
	if got := roles(); equalAny(got, []string{"admin"}) {
		t.Fatalf("lookup with a failed rebuild resolved the new grant: roles = %v", got)
	}
	source.fail.Store(false)
	if got := roles(); !equalAny(got, []string{"admin"}) {
		t.Fatalf("flagged lookup after the rebuild recovered: roles = %v, want [admin]; the failed answer was cached", got)
	}
}

func TestRefreshAfterUserWrite_RetryReportsOnlyItsOwnLoginsGrant(t *testing.T) {
	db := store.TestDB(t)
	rbacRepo := store.NewRBACRepo(db)
	usersRepo := store.NewUsersRepo(db)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx := t.Context()
	composer := rbac.NewComposer(rbac.YAMLState{}, newRBACDBSource(rbacRepo), logger)
	if err := composer.Rebuild(ctx); err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	h := NewUserUpsertHandler(usersRepo, nil, UserUpsertConfig{Namespace: "ns"}, logger)
	h.SetRBACRepo(rbacRepo)
	h.SetGrantRefresh(composer, nil, nil)
	provision := func(sub string) *store.User {
		t.Helper()
		u := &store.User{Issuer: "https://idp", Sub: sub, CreatedBy: "x", UpdatedBy: "x"}
		if err := usersRepo.UpsertUser(ctx, u, nil); err != nil {
			t.Fatalf("provision %s: %v", sub, err)
		}
		return u
	}

	adminGranted := provision("sub-admin-granted")
	roleID := seedRoleUnseenByComposer(ctx, t, rbacRepo, "admin", []string{"agent:*:read"})
	if err := rbacRepo.AddUserRole(ctx, adminGranted.ID, roleID, "an-admin@example.com"); err != nil {
		t.Fatalf("admin grant: %v", err)
	}
	if h.refreshAfterUserWrite(ctx, adminGranted, "sub-admin-granted", false) {
		t.Error("a login reported a grant an administrator wrote")
	}

	retried := provision("sub-retried")
	if err := rbacRepo.CreatePendingUserRole(ctx, &store.RBACPendingUserRole{
		SubjectType: store.PendingSubjectSub, SubjectValue: "sub-retried", Issuer: "https://idp",
		RoleID: roleID, CreatedBy: "a", UpdatedBy: "a",
	}); err != nil {
		t.Fatalf("create pending: %v", err)
	}
	if !h.refreshAfterUserWrite(ctx, retried, "sub-retried", false) {
		t.Fatal("first attempt did not report its materialized grant")
	}
	if !h.refreshAfterUserWrite(ctx, retried, "sub-retried", false) {
		t.Error("retry did not report the grant its first attempt materialized")
	}

	if _, err := db.ExecContext(ctx, `UPDATE rbac_user_roles SET updated_time = updated_time - ? WHERE user_id = ?`,
		(2 * earlierAttemptWindow).Milliseconds(), retried.ID); err != nil {
		t.Fatalf("age grant: %v", err)
	}
	if h.refreshAfterUserWrite(ctx, retried, "sub-retried", false) {
		t.Error("a login past the window reported an old grant")
	}

	if _, err := db.ExecContext(ctx, `ALTER TABLE rbac_user_roles RENAME COLUMN updated_by TO updated_by_renamed`); err != nil {
		t.Fatalf("rename column: %v", err)
	}
	if h.refreshAfterUserWrite(ctx, retried, "sub-retried", false) {
		t.Error("a failed check reported a grant")
	}
}

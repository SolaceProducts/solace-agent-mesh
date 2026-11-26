// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

func systemUserFixture(t *testing.T) (*RBACService, *rbac.Composer, *SystemUserResponse, string, string) {
	t.Helper()
	db := store.TestDB(t)
	repo := store.NewRBACRepo(db)
	usersRepo := store.NewUsersRepo(db)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	embRoles, embUsers, err := rbac.EmbeddedDefaults()
	if err != nil {
		t.Fatalf("EmbeddedDefaults: %v", err)
	}
	composer := rbac.NewComposer(rbac.YAMLState{Roles: embRoles, Users: embUsers}, newRBACDBSource(repo), logger)
	svc := NewRBACService(repo, composer, nil, newCounterMetrics(), logger)
	svc.SetUsersRepo(usersRepo)
	ctx := context.Background()
	if err = composer.Rebuild(ctx); err != nil {
		t.Fatalf("rebuild: %v", err)
	}

	roleA, err := svc.CreateRole(ctx, CreateRBACRoleRequest{Name: "su_role_a", Scopes: []string{"agent:Alpha:invoke"}}, "tester", []string{"*"})
	if err != nil {
		t.Fatalf("CreateRole roleA: %v", err)
	}
	roleB, err := svc.CreateRole(ctx, CreateRBACRoleRequest{Name: "su_role_b", Scopes: []string{"agent:Beta:invoke"}}, "tester", []string{"*"})
	if err != nil {
		t.Fatalf("CreateRole roleB: %v", err)
	}

	su, err := svc.CreateSystemUser(ctx, CreateSystemUserRequest{
		Name: "orders-bot", DisplayName: "Orders Bot", RoleIDs: []string{roleA.ID},
	}, "tester")
	if err != nil {
		t.Fatalf("CreateSystemUser: %v", err)
	}
	return svc, composer, su, roleA.ID, roleB.ID
}

func scopesFor(t *testing.T, composer *rbac.Composer, sub string) []string {
	t.Helper()
	res, err := composer.Lookup(sub, "webhook", nil)
	if err != nil {
		t.Fatalf("Lookup(%s): %v", sub, err)
	}
	return res.Scopes
}

func TestUpdateSystemUser_ReplacesRolesAndKeepsSubjectResolution(t *testing.T) {
	svc, composer, su, roleA, roleB := systemUserFixture(t)
	ctx := context.Background()

	if got := scopesFor(t, composer, su.Sub); !slices.Contains(got, "agent:Alpha:invoke") {
		t.Fatalf("precondition: scopes = %v, want to contain agent:Alpha:invoke", got)
	}

	resp, err := svc.UpdateSystemUser(ctx, su.ID, UpdateSystemUserRequest{
		DisplayName: "Orders Bot", RoleIDs: []string{roleB},
	}, "tester", []string{"*"})
	if err != nil {
		t.Fatalf("UpdateSystemUser: %v", err)
	}
	if !slices.Equal(resp.RoleIDs, []string{roleB}) {
		t.Errorf("RoleIDs = %v, want exactly [%s] — update replaces, it does not union", resp.RoleIDs, roleB)
	}
	if resp.Origin != RBACOriginCustom {
		t.Errorf("Origin = %q, want %q", resp.Origin, RBACOriginCustom)
	}

	got := scopesFor(t, composer, su.Sub)
	if !slices.Contains(got, "agent:Beta:invoke") {
		t.Errorf("scopes = %v, want agent:Beta:invoke — the replaced grant lost its subject provenance", got)
	}
	if slices.Contains(got, "agent:Alpha:invoke") {
		t.Errorf("scopes = %v, still carry the revoked role %q", got, roleA)
	}
}

func TestUpdateSystemUser_RejectsUnknownTargets(t *testing.T) {
	svc, _, _, roleA, _ := systemUserFixture(t)
	ctx := context.Background()

	human := &store.User{Issuer: "https://idp.example", Sub: "human-1", CreatedBy: "t", UpdatedBy: "t"}
	if err := svc.usersRepo.UpsertUser(ctx, human, nil); err != nil {
		t.Fatalf("seed human: %v", err)
	}

	for name, id := range map[string]string{
		"unknown id": "00000000-0000-0000-0000-000000000000",
		"human user": human.ID,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := svc.UpdateSystemUser(ctx, id, UpdateSystemUserRequest{RoleIDs: []string{roleA}}, "tester", []string{"*"})
			var nf *NotFoundError
			if !errors.As(err, &nf) {
				t.Fatalf("err = %v, want NotFoundError", err)
			}
		})
	}
}

func TestDeleteSystemUser_RemovesPrincipalGrantsAndPendingGrants(t *testing.T) {
	svc, composer, su, _, roleB := systemUserFixture(t)
	ctx := context.Background()

	pending := &store.RBACPendingUserRole{
		SubjectType:  store.PendingSubjectSub,
		SubjectValue: su.Sub,
		Issuer:       rbac.SystemUserIssuer,
		RoleID:       roleB,
		CreatedBy:    "tester",
		UpdatedBy:    "tester",
	}
	if err := svc.repo.CreatePendingUserRole(ctx, pending); err != nil {
		t.Fatalf("seed pending grant: %v", err)
	}

	if err := svc.DeleteSystemUser(ctx, su.ID, "tester", []string{"*"}); err != nil {
		t.Fatalf("DeleteSystemUser: %v", err)
	}

	if got := scopesFor(t, composer, su.Sub); len(got) != 0 {
		t.Errorf("deleted principal still resolves scopes %v", got)
	}
	grants, err := svc.ListGrants(ctx)
	if err != nil {
		t.Fatalf("ListGrants: %v", err)
	}
	for _, g := range grants {
		if g.Subject != nil && g.Subject.Value == su.Sub {
			t.Errorf("grant %s (%s) survived the delete for %s", g.ID, g.State, su.Sub)
		}
	}
	users, err := svc.ListSystemUsers(ctx)
	if err != nil {
		t.Fatalf("ListSystemUsers: %v", err)
	}
	for _, u := range users {
		if u.Sub == su.Sub {
			t.Errorf("deleted principal %s still listed", su.Sub)
		}
	}
}

func TestDeleteSystemUser_UnknownIDIsNotFoundNotRetryable503(t *testing.T) {
	svc, _, su, _, _ := systemUserFixture(t)
	ctx := context.Background()

	if err := svc.DeleteSystemUser(ctx, su.ID, "tester", []string{"*"}); err != nil {
		t.Fatalf("first delete: %v", err)
	}
	err := svc.DeleteSystemUser(ctx, su.ID, "tester", []string{"*"})
	var nf *NotFoundError
	if !errors.As(err, &nf) {
		t.Fatalf("second delete err = %v, want NotFoundError", err)
	}
}

func TestSystemUserOriginDistinguishesBuiltinFromCustom(t *testing.T) {
	svc, _, su, _, _ := systemUserFixture(t)

	users, err := svc.ListSystemUsers(context.Background())
	if err != nil {
		t.Fatalf("ListSystemUsers: %v", err)
	}
	bySub := map[string]SystemUserResponse{}
	for _, u := range users {
		bySub[u.Sub] = u
	}
	def := bySub[rbac.DefaultSystemUserSub]
	if def.Origin != RBACOriginBuiltin || def.ID != rbac.DefaultSystemUserID || def.DisplayName != "Default" {
		t.Errorf("%s = %+v, want origin %q, id %s, display name Default", rbac.DefaultSystemUserSub, def, RBACOriginBuiltin, rbac.DefaultSystemUserID)
	}
	if !slices.Equal(def.RoleIDs, []string{rbac.RoleSystemInvokeAll}) {
		t.Errorf("%s roleIds = %v, want the embedded [%s]", rbac.DefaultSystemUserSub, def.RoleIDs, rbac.RoleSystemInvokeAll)
	}
	if got := bySub[su.Sub].Origin; got != RBACOriginCustom {
		t.Errorf("%s origin = %q, want %q", su.Sub, got, RBACOriginCustom)
	}
	for _, hidden := range []string{rbac.ChannelSystemUserSub, rbac.EvalSystemUserSub} {
		if u, listed := bySub[hidden]; listed {
			t.Errorf("%s listed (%+v), want it kept out of the run_as picker", hidden, u)
		}
	}
}

func TestShadowRowReportsBuiltinWithBothRoleSources(t *testing.T) {
	svc, composer, _, roleA, _ := systemUserFixture(t)
	ctx := context.Background()
	shadow := seedDefaultSystemUserShadowRow(ctx, t, svc.usersRepo)
	if _, err := svc.repo.AddUserRoleWithSubject(ctx, shadow.ID, roleA, "legacy", systemUserGrantSubject(shadow.Sub)); err != nil {
		t.Fatalf("seed shadow grant: %v", err)
	}

	users, err := svc.ListSystemUsers(ctx)
	if err != nil {
		t.Fatalf("ListSystemUsers: %v", err)
	}
	var got []SystemUserResponse
	for _, u := range users {
		if u.Sub == rbac.DefaultSystemUserSub {
			got = append(got, u)
		}
	}
	if len(got) != 1 {
		t.Fatalf("%s listed %d times, want once: %+v", rbac.DefaultSystemUserSub, len(got), got)
	}
	if got[0].ID != shadow.ID || got[0].Origin != RBACOriginBuiltin {
		t.Errorf("shadow = %+v, want its own id %s and origin %q", got[0], shadow.ID, RBACOriginBuiltin)
	}
	if !slices.Contains(got[0].RoleIDs, rbac.RoleSystemInvokeAll) || !slices.Contains(got[0].RoleIDs, roleA) {
		t.Errorf("roleIds = %v, want the embedded %q and the DB grant %q", got[0].RoleIDs, rbac.RoleSystemInvokeAll, roleA)
	}

	if err = composer.Rebuild(ctx); err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	usersSvc := NewUsersService(svc.usersRepo, svc.repo, slog.New(slog.NewTextHandler(io.Discard, nil)))
	usersSvc.SetGrantRefresh(composer, nil, nil)
	user, err := usersSvc.GetByID(ctx, shadow.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	var sawDirect bool
	for _, r := range user.Roles {
		if r.RoleID != roleA {
			continue
		}
		sawDirect = true
		if r.Manageable {
			t.Errorf("shadow row direct grant %+v reports manageable, but PUT roleGrants refuses a built-in system user", r)
		}
	}
	if !sawDirect {
		t.Errorf("roles = %+v, want the DB grant %q listed", user.Roles, roleA)
	}
}

func TestListSystemUsersReportsOperatorOverrideOfBuiltinRoles(t *testing.T) {
	db := store.TestDB(t)
	repo := store.NewRBACRepo(db)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	embRoles, embUsers, err := rbac.EmbeddedDefaults()
	if err != nil {
		t.Fatalf("EmbeddedDefaults: %v", err)
	}
	embRoles.Roles["narrow"] = rbac.RoleDefinition{Scopes: []string{"agent:Alpha:invoke"}}
	embUsers.Users[rbac.DefaultSystemUserSub] = rbac.UserAssignment{Roles: []string{"narrow"}}
	composer := rbac.NewComposer(rbac.YAMLState{Roles: embRoles, Users: embUsers}, newRBACDBSource(repo), logger)
	svc := NewRBACService(repo, composer, nil, newCounterMetrics(), logger)
	svc.SetUsersRepo(store.NewUsersRepo(db))
	ctx := context.Background()
	if err = composer.Rebuild(ctx); err != nil {
		t.Fatalf("rebuild: %v", err)
	}

	users, err := svc.ListSystemUsers(ctx)
	if err != nil {
		t.Fatalf("ListSystemUsers: %v", err)
	}
	for _, u := range users {
		if u.Sub != rbac.DefaultSystemUserSub {
			continue
		}
		if !slices.Equal(u.RoleIDs, []string{"narrow"}) {
			t.Errorf("%s roleIds = %v, want the operator override [narrow]", u.Sub, u.RoleIDs)
		}
		return
	}
	t.Fatalf("ListSystemUsers = %+v, want %s listed", users, rbac.DefaultSystemUserSub)
}

func TestSetUserRoleGrantsKeepsSystemUserResolvable(t *testing.T) {
	svc, composer, su, _, roleB := systemUserFixture(t)
	ctx := context.Background()
	usersSvc := NewUsersService(svc.usersRepo, svc.repo, slog.New(slog.NewTextHandler(io.Discard, nil)))
	usersSvc.SetGrantRefresh(composer, nil, nil)

	if _, err := usersSvc.SetUserRoleGrants(ctx, su.ID, []string{roleB}, "tester", []string{"*"}); err != nil {
		t.Fatalf("SetUserRoleGrants: %v", err)
	}

	got := scopesFor(t, composer, su.Sub)
	if !slices.Contains(got, "agent:Beta:invoke") {
		t.Errorf("scopes = %v, want agent:Beta:invoke — the grant write dropped its subject provenance", got)
	}
	if slices.Contains(got, "agent:Alpha:invoke") {
		t.Errorf("scopes = %v, still carry the replaced role", got)
	}
}

func TestSetUserRoleGrantsLeavesHumanGrantsUnattributed(t *testing.T) {
	svc, composer, _, roleA, _ := systemUserFixture(t)
	ctx := context.Background()
	usersSvc := NewUsersService(svc.usersRepo, svc.repo, slog.New(slog.NewTextHandler(io.Discard, nil)))
	usersSvc.SetGrantRefresh(composer, nil, nil)

	human := &store.User{Issuer: "https://idp.example", Sub: "human-1", CreatedBy: "t", UpdatedBy: "t"}
	if err := svc.usersRepo.UpsertUser(ctx, human, nil); err != nil {
		t.Fatalf("seed human: %v", err)
	}
	if _, err := usersSvc.SetUserRoleGrants(ctx, human.ID, []string{roleA}, "tester", []string{"*"}); err != nil {
		t.Fatalf("SetUserRoleGrants: %v", err)
	}

	rows, err := svc.repo.ListRolesForUser(ctx, human.ID)
	if err != nil {
		t.Fatalf("ListRolesForUser: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d grants, want 1", len(rows))
	}
	if rows[0].SubjectType != "" || rows[0].SubjectValue != "" {
		t.Errorf("human grant carries subject (%q, %q); interactive grants must stay unattributed",
			rows[0].SubjectType, rows[0].SubjectValue)
	}
}

func TestBakedInSystemUsersAreNotWritable(t *testing.T) {
	svc, composer, _, roleA, _ := systemUserFixture(t)
	ctx := context.Background()
	usersSvc := NewUsersService(svc.usersRepo, svc.repo, slog.New(slog.NewTextHandler(io.Discard, nil)))
	usersSvc.SetGrantRefresh(composer, nil, nil)
	wantImmutable := func(t *testing.T, err error) {
		t.Helper()
		if !errors.Is(err, errBuiltinSystemUserImmutable) {
			t.Fatalf("err = %v, want errBuiltinSystemUserImmutable", err)
		}
	}

	for _, name := range []string{"default", rbac.ChannelSystemUserRunAs, rbac.DefaultSystemUserSub} {
		t.Run("create "+name+" refused", func(t *testing.T) {
			_, err := svc.CreateSystemUser(ctx, CreateSystemUserRequest{Name: name, RoleIDs: []string{roleA}}, "tester")
			wantImmutable(t, err)
		})
	}

	for _, r := range rbac.ReservedSystemUsers() {
		t.Run("delete "+r.Sub+" refused", func(t *testing.T) {
			wantImmutable(t, svc.DeleteSystemUser(ctx, r.ID, "tester", []string{"*"}))
			if u, _, err := svc.usersRepo.GetByID(ctx, r.ID); err != nil || u == nil {
				t.Errorf("row after refused delete = %+v (err %v), want it kept", u, err)
			}
		})
		t.Run("set role grants on "+r.Sub+" refused", func(t *testing.T) {
			_, err := usersSvc.SetUserRoleGrants(ctx, r.ID, []string{roleA}, "tester", []string{"*"})
			wantImmutable(t, err)
		})
		for _, subject := range []GrantSubject{
			{Type: store.PendingSubjectSub, Value: r.Sub, Issuer: rbac.SystemUserIssuer},
			{Type: store.PendingSubjectSub, Value: r.Sub, Issuer: "https://idp.example"},
			{Type: store.PendingSubjectLegacy, Value: r.Sub},
		} {
			t.Run("create "+subject.Type+" grant (issuer "+subject.Issuer+") for "+r.Sub+" refused", func(t *testing.T) {
				_, err := svc.CreateGrant(ctx, CreateGrantRequest{Subject: subject, RoleID: roleA}, "tester", []string{"*"})
				wantImmutable(t, err)
			})
		}
	}

	pending, err := svc.repo.ListPendingUserRoles(ctx)
	if err != nil {
		t.Fatalf("ListPendingUserRoles: %v", err)
	}
	for _, p := range pending {
		if rbac.IsReservedSystemUserSub(p.SubjectValue) {
			t.Errorf("pending grant parked for %s (type %s) after refused writes", p.SubjectValue, p.SubjectType)
		}
	}

	if got := slices.Sorted(slices.Values(scopesFor(t, composer, rbac.DefaultSystemUserSub))); !slices.Equal(got, slices.Sorted(slices.Values([]string{rbac.ScopeAgentInvokeAny, rbac.ScopeWorkflowInvokeAny}))) {
		t.Errorf("%s scopes after refused writes = %v, want exactly the embedded invoke-any pair", rbac.DefaultSystemUserSub, got)
	}
	if rows, err := svc.repo.ListRolesForUser(ctx, rbac.DefaultSystemUserID); err != nil || len(rows) != 0 {
		t.Errorf("%s DB grants = %v (err %v), want none written", rbac.DefaultSystemUserSub, rows, err)
	}
}

func TestShadowRowGrantIsRemovableViaDeleteGrant(t *testing.T) {
	svc, composer, _, roleA, _ := systemUserFixture(t)
	ctx := context.Background()

	grantID, err := svc.repo.AddUserRoleWithSubject(ctx, rbac.ChannelSystemUserID, roleA, "legacy",
		systemUserGrantSubject(rbac.ChannelSystemUserSub))
	if err != nil {
		t.Fatalf("seed shadow grant: %v", err)
	}
	svc.rebuildAndInvalidate(ctx)

	if got := scopesFor(t, composer, rbac.ChannelSystemUserSub); !slices.Contains(got, "agent:Alpha:invoke") {
		t.Fatalf("precondition: scopes = %v, want the shadow grant's role to resolve", got)
	}

	_, err = svc.UpdateSystemUser(ctx, rbac.ChannelSystemUserID, UpdateSystemUserRequest{RoleIDs: []string{}}, "tester", []string{"*"})
	if !errors.Is(err, errBuiltinSystemUserImmutable) {
		t.Fatalf("update err = %v, want errBuiltinSystemUserImmutable", err)
	}

	if err := svc.DeleteGrant(ctx, grantID, "tester", []string{"*"}); err != nil {
		t.Fatalf("DeleteGrant of a shadow grant: %v", err)
	}
	got := scopesFor(t, composer, rbac.ChannelSystemUserSub)
	if slices.Contains(got, "agent:Alpha:invoke") {
		t.Errorf("scopes = %v, still carry the deleted grant's role", got)
	}
	if !slices.Contains(got, "agent:Orchestrator:invoke") {
		t.Errorf("scopes = %v, want the embedded %s role to survive — removing the grant must narrow the principal, not remove it",
			got, rbac.RoleSystemChannelInvoke)
	}
}

func TestUpdateSystemUser_RefusesToMoveScopesTheActorLacks(t *testing.T) {
	svc, composer, su, _, roleB := systemUserFixture(t)
	ctx := context.Background()

	_, err := svc.UpdateSystemUser(ctx, su.ID, UpdateSystemUserRequest{RoleIDs: []string{roleB}},
		"limited", []string{"agent:Alpha:invoke"})
	var forbidden *ForbiddenError
	if !errors.As(err, &forbidden) {
		t.Fatalf("err = %v, want ForbiddenError", err)
	}

	if got := scopesFor(t, composer, su.Sub); !slices.Contains(got, "agent:Alpha:invoke") || slices.Contains(got, "agent:Beta:invoke") {
		t.Errorf("scopes = %v, want the principal untouched at [agent:Alpha:invoke] — a refused update must not partially apply", got)
	}
}

func TestDeleteSystemUser_RefusesToRevokeScopesTheActorLacks(t *testing.T) {
	svc, composer, su, _, _ := systemUserFixture(t)
	ctx := context.Background()

	err := svc.DeleteSystemUser(ctx, su.ID, "limited", []string{"agent:Beta:invoke"})
	var forbidden *ForbiddenError
	if !errors.As(err, &forbidden) {
		t.Fatalf("err = %v, want ForbiddenError", err)
	}

	if got := scopesFor(t, composer, su.Sub); !slices.Contains(got, "agent:Alpha:invoke") {
		t.Errorf("scopes = %v, want the principal still resolving — a refused delete must not remove the row", got)
	}
}

func TestUpdateSystemUser_UnknownRoleLeavesTheExistingSetIntact(t *testing.T) {
	svc, composer, su, roleA, roleB := systemUserFixture(t)
	ctx := context.Background()

	_, err := svc.UpdateSystemUser(ctx, su.ID, UpdateSystemUserRequest{
		RoleIDs: []string{roleB, "00000000-0000-0000-0000-0000000000ff"},
	}, "tester", []string{"*"})
	var invalid *ValidationError
	if !errors.As(err, &invalid) {
		t.Fatalf("err = %v, want ValidationError", err)
	}

	listed, err := svc.ListSystemUsers(ctx)
	if err != nil {
		t.Fatalf("ListSystemUsers: %v", err)
	}
	idx := slices.IndexFunc(listed, func(u SystemUserResponse) bool { return u.ID == su.ID })
	if idx < 0 {
		t.Fatalf("system user %s missing from %v", su.ID, listed)
	}
	if !slices.Equal(listed[idx].RoleIDs, []string{roleA}) {
		t.Errorf("RoleIDs = %v, want unchanged [%s] — the valid half of the request must not have been written", listed[idx].RoleIDs, roleA)
	}
	if got := scopesFor(t, composer, su.Sub); slices.Contains(got, "agent:Beta:invoke") {
		t.Errorf("scopes = %v, carry the role from a refused write", got)
	}
}

func TestDeleteSystemUser_AuditsHowManyAPIKeysTheCascadeRemoved(t *testing.T) {
	db := store.TestDB(t)
	repo := store.NewRBACRepo(db)
	logs := &syncedBuffer{}
	logger := slog.New(slog.NewJSONHandler(logs, nil))
	composer := rbac.NewComposer(rbac.YAMLState{}, newRBACDBSource(repo), logger)
	svc := NewRBACService(repo, composer, nil, newCounterMetrics(), logger)
	svc.SetUsersRepo(store.NewUsersRepo(db))
	keys := store.NewAPIKeysRepo(db)
	svc.SetAPIKeysRepo(keys)
	ctx := t.Context()
	if err := composer.Rebuild(ctx); err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	role, err := svc.CreateRole(ctx, CreateRBACRoleRequest{Name: "keyed", Scopes: []string{"agent:*:invoke"}}, "tester", []string{"*"})
	if err != nil {
		t.Fatalf("CreateRole: %v", err)
	}
	su, err := svc.CreateSystemUser(ctx, CreateSystemUserRequest{Name: "keyed-bot", RoleIDs: []string{role.ID}}, "tester")
	if err != nil {
		t.Fatalf("CreateSystemUser: %v", err)
	}
	for i := range 2 {
		if _, err := keys.Create(ctx, store.APIKey{
			UserID: su.ID, Label: "k", KeyHash: fmt.Sprintf("cascade-%d", i), Prefix: "pfx12345",
			CreatedBy: "tester", UpdatedBy: "tester",
		}, []string{"agent:*:invoke"}, 10, time.Now().UnixMilli()); err != nil {
			t.Fatalf("seed key %d: %v", i, err)
		}
	}

	if err := svc.DeleteSystemUser(ctx, su.ID, "tester", []string{"*"}); err != nil {
		t.Fatalf("DeleteSystemUser: %v", err)
	}
	if !strings.Contains(logs.String(), `"apiKeysDeleted":2`) {
		t.Errorf("system-user delete audit does not record the two cascaded keys: %s", logs.String())
	}
}

func TestDeleteSystemUser_AuditsHowManyBudgetRowsItRemoved(t *testing.T) {
	db := store.TestDB(t)
	repo := store.NewRBACRepo(db)
	logs := &syncedBuffer{}
	logger := slog.New(slog.NewJSONHandler(logs, nil))
	composer := rbac.NewComposer(rbac.YAMLState{}, newRBACDBSource(repo), logger)
	svc := NewRBACService(repo, composer, nil, newCounterMetrics(), logger)
	svc.SetUsersRepo(store.NewUsersRepo(db))
	signals := newBudgetsChangedRig(t)
	svc.SetBudgetsChanged(signals.signal)
	ctx := t.Context()
	if err := composer.Rebuild(ctx); err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	unbudgeted, err := svc.CreateSystemUser(ctx, CreateSystemUserRequest{Name: "unbudgeted-bot"}, "tester")
	if err != nil {
		t.Fatalf("CreateSystemUser: %v", err)
	}
	if err = svc.DeleteSystemUser(ctx, unbudgeted.ID, "tester", []string{"*"}); err != nil {
		t.Fatalf("DeleteSystemUser: %v", err)
	}
	signals.expect(t, 0, "deleting a system user with no budget rows")
	su, err := svc.CreateSystemUser(ctx, CreateSystemUserRequest{Name: "budgeted-bot"}, "tester")
	if err != nil {
		t.Fatalf("CreateSystemUser: %v", err)
	}
	budgets, assignments := store.NewBudgetsRepo(db), store.NewBudgetAssignmentsRepo(db)
	eng := &store.Budget{Name: "Engineering", SubjectType: store.BudgetSubjectUser, Amount: new(int64(100)), CreatedBy: "t", UpdatedBy: "t"}
	if err := budgets.Create(ctx, eng); err != nil {
		t.Fatalf("create budget: %v", err)
	}
	if _, err := assignments.Create(ctx, &store.BudgetAssignment{
		SubjectType: store.BudgetSubjectUser, SubjectRef: su.ID, BudgetID: eng.ID, CreatedBy: "t", UpdatedBy: "t",
	}); err != nil {
		t.Fatalf("assign: %v", err)
	}
	if _, err := store.NewBudgetTopUpsRepo(db).Grant(ctx, store.BudgetTopUpGrant{
		SubjectType: store.BudgetSubjectUser, SubjectRef: su.ID, Amount: 50,
		ExpiresTime: time.Now().Add(time.Hour).UnixMilli(), Actor: "t", NowMs: time.Now().UnixMilli(),
	}); err != nil {
		t.Fatalf("top up: %v", err)
	}

	if err := svc.DeleteSystemUser(ctx, su.ID, "tester", []string{"*"}); err != nil {
		t.Fatalf("DeleteSystemUser: %v", err)
	}
	signals.expect(t, 1, "deleting a system user's budget rows")
	if !strings.Contains(logs.String(), `"budgetAssignmentsDeleted":1`) {
		t.Errorf("system-user delete audit does not record the removed assignment: %s", logs.String())
	}
	if !strings.Contains(logs.String(), `"budgetTopUpsDeleted":1`) {
		t.Errorf("system-user delete audit does not record the removed top-up: %s", logs.String())
	}
	if n, err := assignments.Count(ctx, store.BudgetAssignmentFilter{SubjectRef: su.ID}); err != nil || n != 0 {
		t.Errorf("assignments after the delete = %d, %v; want 0", n, err)
	}
}

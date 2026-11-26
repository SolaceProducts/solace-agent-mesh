// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

type authzEvents struct {
	repo *store.OutboxRepo
	seen int
}

func (e *authzEvents) next(t *testing.T) []store.OutboxEvent {
	t.Helper()
	all, err := e.repo.GetDispatchable(t.Context(), 1000)
	if err != nil {
		t.Fatalf("GetDispatchable: %v", err)
	}
	fresh := all[e.seen:]
	e.seen = len(all)
	return fresh
}

func (e *authzEvents) expect(t *testing.T, step, eventType, aggregateID string) {
	t.Helper()
	got := e.next(t)
	if len(got) != 1 || got[0].EventType != eventType || got[0].AggregateID != aggregateID {
		t.Errorf("%s: outbox events = %+v, want one %s for %s", step, got, eventType, aggregateID)
	}
}

func (e *authzEvents) expectNone(t *testing.T, step string) {
	t.Helper()
	if got := e.next(t); len(got) != 0 {
		t.Errorf("%s: outbox events = %+v, want none", step, got)
	}
}

type authzOutboxFixture struct {
	db       *store.DB
	rbacRepo *store.RBACRepo
	users    *store.UsersRepo
	outbox   *store.OutboxRepo
	composer *rbac.Composer
	notifier *countingNotifier
	events   *authzEvents
	logger   *slog.Logger
}

func newAuthzOutboxFixture(t *testing.T) *authzOutboxFixture {
	t.Helper()
	db := store.TestDB(t)
	f := &authzOutboxFixture{
		db:       db,
		rbacRepo: store.NewRBACRepo(db),
		users:    store.NewUsersRepo(db),
		outbox:   store.NewOutboxRepo(db),
		notifier: &countingNotifier{},
		logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	f.events = &authzEvents{repo: f.outbox}
	f.composer = rbac.NewComposer(rbac.YAMLState{}, newRBACDBSource(f.rbacRepo), f.logger)
	if err := f.composer.Rebuild(t.Context()); err != nil {
		t.Fatalf("composer rebuild: %v", err)
	}
	return f
}

func (f *authzOutboxFixture) rbacService() *RBACService {
	svc := NewRBACService(f.rbacRepo, f.composer, nil, nil, f.logger)
	svc.SetUsersRepo(f.users)
	svc.WithAuthzOutbox(f.outbox, f.notifier)
	return svc
}

func (f *authzOutboxFixture) usersService() *UsersService {
	svc := NewUsersService(f.users, f.rbacRepo, f.logger)
	svc.SetGrantRefresh(f.composer, nil, nil)
	svc.SetIdentityClaims(store.NewUserIdentityClaimsRepo(f.db))
	svc.SetIdentityBindings(store.NewEntrypointIdentityBindingsRepo(f.db))
	svc.WithAuthzOutbox(f.outbox, f.notifier)
	return svc
}

func (f *authzOutboxFixture) upsertHandler() *UserUpsertHandler {
	h := NewUserUpsertHandler(f.users, nil, UserUpsertConfig{Namespace: "ns"}, f.logger)
	h.SetRBACRepo(f.rbacRepo)
	h.SetGrantRefresh(f.composer, nil, nil)
	h.WithAuthzOutbox(f.outbox, f.notifier)
	return h
}

func (f *authzOutboxFixture) seedUser(t *testing.T, sub, email string) *store.User {
	t.Helper()
	u := &store.User{Issuer: "https://idp", Sub: sub, Email: &email, CreatedBy: "x", UpdatedBy: "x"}
	if err := f.users.UpsertUser(t.Context(), u, nil); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	return u
}

func (f *authzOutboxFixture) dropOutbox(t *testing.T) {
	t.Helper()
	if _, err := f.db.ExecContext(t.Context(), `DROP TABLE outbox_events`); err != nil {
		t.Fatalf("drop outbox table: %v", err)
	}
}

var superAdmin = []string{"*"}

func TestRBACService_RecordsAuthzOutboxEventPerGraphWrite(t *testing.T) {
	f := newAuthzOutboxFixture(t)
	svc := f.rbacService()
	ctx := t.Context()
	ev := f.events

	role, roleErr := svc.CreateRole(ctx, CreateRBACRoleRequest{Name: "viewer", Scopes: []string{"rbac:_:read"}}, "admin", superAdmin)
	if roleErr != nil {
		t.Fatalf("create role: %v", roleErr)
	}
	ev.expect(t, "create role", authzOutboxRolesChanged, role.ID)

	if _, err := svc.UpdateRole(ctx, role.ID, UpdateRBACRoleRequest{Name: "viewer", Scopes: []string{"rbac:_:read", "agent:*:invoke"}}, "admin", superAdmin); err != nil {
		t.Fatalf("update role: %v", err)
	}
	ev.expect(t, "update role", authzOutboxRolesChanged, role.ID)

	if err := svc.SetDefaultRoles(ctx, []string{role.ID}, "admin", superAdmin); err != nil {
		t.Fatalf("set default roles: %v", err)
	}
	ev.expect(t, "set default roles", authzOutboxRolesChanged, authzOutboxAggregateDefaultRoles)
	if err := svc.SetDefaultRoles(ctx, nil, "admin", superAdmin); err != nil {
		t.Fatalf("clear default roles: %v", err)
	}
	ev.expect(t, "clear default roles", authzOutboxRolesChanged, authzOutboxAggregateDefaultRoles)

	mapping, mappingErr := svc.CreateClaimMapping(ctx, CreateRBACClaimMappingRequest{Name: "Viewers", OIDCProvider: "okta", ClaimValue: "viewers", RoleIDs: []string{role.ID}}, "admin", superAdmin)
	if mappingErr != nil {
		t.Fatalf("create claim mapping: %v", mappingErr)
	}
	ev.expect(t, "create claim mapping", authzOutboxRolesChanged, mapping.ID)
	if _, err := svc.UpdateClaimMapping(ctx, mapping.ID, UpdateRBACClaimMappingRequest{Name: "Viewers", OIDCProvider: "okta", ClaimValue: "readers", RoleIDs: []string{role.ID}}, "admin", superAdmin); err != nil {
		t.Fatalf("update claim mapping: %v", err)
	}
	ev.expect(t, "update claim mapping", authzOutboxRolesChanged, mapping.ID)
	if err := svc.DeleteClaimMapping(ctx, mapping.ID, "admin", superAdmin); err != nil {
		t.Fatalf("delete claim mapping: %v", err)
	}
	ev.expect(t, "delete claim mapping", authzOutboxRolesChanged, mapping.ID)

	alice := f.seedUser(t, "sub-alice", "alice@example.com")
	concrete, concreteErr := svc.CreateGrant(ctx, CreateGrantRequest{RoleID: role.ID, Subject: GrantSubject{Type: store.PendingSubjectEmail, Value: "alice@example.com"}}, "admin", superAdmin)
	if concreteErr != nil {
		t.Fatalf("create concrete grant: %v", concreteErr)
	}
	ev.expect(t, "create concrete grant", authzOutboxRolesChanged, alice.ID)
	if err := svc.DeleteGrant(ctx, concrete.ID, "admin", superAdmin); err != nil {
		t.Fatalf("delete concrete grant: %v", err)
	}
	ev.expect(t, "delete concrete grant", authzOutboxRolesChanged, concrete.ID)

	pending, pendingErr := svc.CreateGrant(ctx, CreateGrantRequest{RoleID: role.ID, Subject: GrantSubject{Type: store.PendingSubjectEmail, Value: "later@example.com"}}, "admin", superAdmin)
	if pendingErr != nil {
		t.Fatalf("create pending grant: %v", pendingErr)
	}
	ev.expectNone(t, "create pending grant")
	if err := svc.DeleteGrant(ctx, pending.ID, "admin", superAdmin); err != nil {
		t.Fatalf("delete pending grant: %v", err)
	}
	ev.expectNone(t, "delete pending grant")

	bot, botErr := svc.CreateSystemUser(ctx, CreateSystemUserRequest{Name: "bot", RoleIDs: []string{role.ID}}, "admin")
	if botErr != nil {
		t.Fatalf("create system user: %v", botErr)
	}
	ev.expect(t, "create system user", authzOutboxRolesChanged, bot.ID)
	if _, err := svc.UpdateSystemUser(ctx, bot.ID, UpdateSystemUserRequest{DisplayName: "Bot", RoleIDs: []string{role.ID}}, "admin", superAdmin); err != nil {
		t.Fatalf("update system user: %v", err)
	}
	ev.expect(t, "update system user", authzOutboxRolesChanged, bot.ID)
	if err := svc.DeleteSystemUser(ctx, bot.ID, "admin", superAdmin); err != nil {
		t.Fatalf("delete system user: %v", err)
	}
	ev.expect(t, "delete system user", authzOutboxRolesChanged, bot.ID)

	if err := svc.DeleteRole(ctx, role.ID, "admin", superAdmin); err != nil {
		t.Fatalf("delete role: %v", err)
	}
	ev.expect(t, "delete role", authzOutboxRolesChanged, role.ID)

	if got := f.notifier.n.Load(); got != 13 {
		t.Errorf("Notify called %d times, want 13, once per recorded event", got)
	}
}

func TestRBACService_AuthzOutboxFailureRollsBackWrite(t *testing.T) {
	f := newAuthzOutboxFixture(t)
	svc := f.rbacService()
	f.dropOutbox(t)

	if _, err := svc.CreateRole(t.Context(), CreateRBACRoleRequest{Name: "never", Scopes: []string{"rbac:_:read"}}, "admin", superAdmin); err == nil {
		t.Fatal("create role succeeded although its outbox event failed")
	}
	if got, _ := f.rbacRepo.GetRoleByName(t.Context(), "never"); got != nil {
		t.Error("role committed although its outbox event failed")
	}
	if got := f.notifier.n.Load(); got != 0 {
		t.Errorf("Notify called %d times after a failed commit, want 0", got)
	}
}

func TestUsersService_RecordsAuthzOutboxEvents(t *testing.T) {
	f := newAuthzOutboxFixture(t)
	svc := f.usersService()
	ctx := t.Context()
	roleID := seedRole(ctx, t, f.rbacRepo, f.composer, "admin")
	u := f.seedUser(t, "sub-1", "one@example.com")

	if _, err := svc.SetUserRoleGrants(ctx, u.ID, []string{roleID}, "admin", superAdmin); err != nil {
		t.Fatalf("set role grants: %v", err)
	}
	f.events.expect(t, "set role grants", authzOutboxRolesChanged, u.ID)

	if _, err := svc.RevokeUserIdentityClaims(ctx, u.ID, "admin"); err != nil {
		t.Fatalf("revoke identity claims: %v", err)
	}
	f.events.expect(t, "revoke identity claims", authzOutboxUsersChanged, u.ID)
}

func TestRBACService_SystemUserWritesRollBackWhenTheirOutboxEventFails(t *testing.T) {
	f := newAuthzOutboxFixture(t)
	svc := f.rbacService()
	ctx := t.Context()
	role := seedRole(ctx, t, f.rbacRepo, f.composer, "responder")
	bot, seedErr := svc.CreateSystemUser(ctx, CreateSystemUserRequest{Name: "bot", DisplayName: "before", RoleIDs: []string{role}}, "admin")
	if seedErr != nil {
		t.Fatalf("seed system user: %v", seedErr)
	}
	f.dropOutbox(t)

	if _, err := svc.CreateSystemUser(ctx, CreateSystemUserRequest{Name: "never", RoleIDs: []string{role}}, "admin"); err == nil {
		t.Error("create system user succeeded although its outbox event failed")
	}
	if got, _, _ := f.users.GetByIssuerSub(ctx, rbac.SystemUserIssuer, rbac.SystemUserPrincipal("never")); got != nil {
		t.Error("system user row committed although its outbox event failed")
	}

	if _, err := svc.UpdateSystemUser(ctx, bot.ID, UpdateSystemUserRequest{DisplayName: "after", RoleIDs: nil}, "admin", superAdmin); err == nil {
		t.Error("update system user succeeded although its outbox event failed")
	}
	if err := svc.DeleteSystemUser(ctx, bot.ID, "admin", superAdmin); err == nil {
		t.Error("delete system user succeeded although its outbox event failed")
	}
	got, _, err := f.users.GetByID(ctx, bot.ID)
	if err != nil || got == nil || got.DisplayName == nil || *got.DisplayName != "before" {
		t.Errorf("system user after failed update and delete = %+v, %v; want it unchanged", got, err)
	}
	roles, err := f.rbacRepo.ListRolesForUser(ctx, bot.ID)
	if err != nil || len(roles) != 1 || roles[0].RoleID != role {
		t.Errorf("system user grants after failed update and delete = %d, %v; want one grant of %s", len(roles), err, role)
	}
}

func TestUsersService_RevokeRollsBackWhenItsOutboxEventFails(t *testing.T) {
	f := newAuthzOutboxFixture(t)
	svc := f.usersService()
	ctx := t.Context()
	u := &store.User{Issuer: "https://idp", Sub: "sub-phone", CreatedBy: "x", UpdatedBy: "x"}
	claims := store.IdentityClaimWrite{Replace: true, Claims: []store.UserIdentityClaim{{ClaimKey: ClaimKeyPhoneNumber, ClaimValueCanonical: "15551234567", Verified: true}}}
	if err := f.users.UpsertUserWithClaims(ctx, u, nil, claims); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	bindings := store.NewEntrypointIdentityBindingsRepo(f.db)
	if _, err := bindings.ObserveContinuity(ctx, "whatsapp", ClaimKeyPhoneNumber, "15551234567", "sender-1", u.ID, 1); err != nil {
		t.Fatalf("seed binding: %v", err)
	}
	if b, err := bindings.Get(ctx, "whatsapp", ClaimKeyPhoneNumber, "15551234567"); err != nil || b == nil {
		t.Fatalf("precondition: binding = %v, %v; want one", b, err)
	}
	f.dropOutbox(t)

	if _, err := svc.RevokeUserIdentityClaims(ctx, u.ID, "admin"); err == nil {
		t.Fatal("revoke succeeded although its outbox event failed")
	}
	left, err := store.NewUserIdentityClaimsRepo(f.db).ClaimsForUser(ctx, u.ID)
	if err != nil || len(left) != 1 {
		t.Errorf("claims after a failed revoke = %d, %v; want 1", len(left), err)
	}
	if b, err := bindings.Get(ctx, "whatsapp", ClaimKeyPhoneNumber, "15551234567"); err != nil || b == nil {
		t.Errorf("binding after a failed revoke = %v, %v; want it kept", b, err)
	}
}

func TestUserUpsertHandler_RecordsAuthzOutboxEvents(t *testing.T) {
	f := newAuthzOutboxFixture(t)
	h := f.upsertHandler()
	ctx := t.Context()
	login := func(email string) *store.User {
		t.Helper()
		u := &store.User{Issuer: "https://idp", Sub: "sub-login", Email: &email, CreatedBy: "x", UpdatedBy: "x"}
		if _, _, err := h.upsertUser(ctx, u, nil, store.IdentityClaimWrite{}); err != nil {
			t.Fatalf("upsert: %v", err)
		}
		return u
	}

	u := login("one@example.com")
	f.events.expect(t, "first login", authzOutboxUsersChanged, u.ID)
	login("one@example.com")
	f.events.expectNone(t, "login with nothing changed")
	login("two@example.com")
	f.events.expect(t, "login with a new email", authzOutboxUsersChanged, u.ID)

	h.refreshAfterUserWrite(ctx, u, "login", false)
	f.events.expectNone(t, "login with no pending grant")

	roleID := seedRole(ctx, t, f.rbacRepo, f.composer, "admin")
	if err := f.rbacRepo.CreatePendingUserRole(ctx, &store.RBACPendingUserRole{
		SubjectType: store.PendingSubjectSub, SubjectValue: "sub-login", Issuer: "https://idp",
		RoleID: roleID, CreatedBy: "a", UpdatedBy: "a",
	}); err != nil {
		t.Fatalf("create pending grant: %v", err)
	}
	h.refreshAfterUserWrite(ctx, u, "login", false)
	f.events.expect(t, "login that materializes a pending grant", authzOutboxRolesChanged, u.ID)
}

func TestUserUpsertHandler_LoginRollsBackWhenItsOutboxEventFails(t *testing.T) {
	f := newAuthzOutboxFixture(t)
	h := f.upsertHandler()
	f.dropOutbox(t)

	email := "one@example.com"
	u := &store.User{Issuer: "https://idp", Sub: "sub-login", Email: &email, CreatedBy: "x", UpdatedBy: "x"}
	if _, _, err := h.upsertUser(t.Context(), u, nil, store.IdentityClaimWrite{}); err == nil {
		t.Fatal("upsert succeeded although its outbox event failed")
	}
	if got, _, _ := f.users.GetByIssuerSub(t.Context(), "https://idp", "sub-login"); got != nil {
		t.Error("user committed although its outbox event failed")
	}
}

func TestUserUpsertHandler_MaterializationRollsBackWhenItsOutboxEventFails(t *testing.T) {
	f := newAuthzOutboxFixture(t)
	h := f.upsertHandler()
	ctx := t.Context()
	u := f.seedUser(t, "sub-login", "one@example.com")
	roleID := seedRole(ctx, t, f.rbacRepo, f.composer, "admin")
	if err := f.rbacRepo.CreatePendingUserRole(ctx, &store.RBACPendingUserRole{
		SubjectType: store.PendingSubjectSub, SubjectValue: "sub-login", Issuer: "https://idp",
		RoleID: roleID, CreatedBy: "a", UpdatedBy: "a",
	}); err != nil {
		t.Fatalf("create pending grant: %v", err)
	}
	f.dropOutbox(t)

	if h.refreshAfterUserWrite(ctx, u, "login", false) {
		t.Error("reply reports a grant whose materialization rolled back")
	}
	granted, err := f.rbacRepo.ListRolesForUser(ctx, u.ID)
	if err != nil {
		t.Fatalf("list roles: %v", err)
	}
	if len(granted) != 0 {
		t.Errorf("grant committed although its outbox event failed: %+v", granted)
	}
	pending, err := f.rbacRepo.ListPendingUserRoles(ctx)
	if err != nil {
		t.Fatalf("list pending: %v", err)
	}
	if len(pending) != 1 {
		t.Errorf("pending grants = %d, want 1 kept for the next login", len(pending))
	}
}

func TestUserUpsertHandler_LoginFailsWhenItsReadFails(t *testing.T) {
	const ns = "users-read-fails"
	f := newAuthzOutboxFixture(t)
	brk := newTestBroker(t)
	h := NewUserUpsertHandler(f.users, brk, UserUpsertConfig{Namespace: ns}, f.logger)
	h.WithAuthzOutbox(f.outbox, f.notifier)
	if err := h.Start(t.Context()); err != nil {
		t.Fatalf("start handler: %v", err)
	}
	t.Cleanup(func() { _ = h.Stop(context.Background()) })
	time.Sleep(20 * time.Millisecond)
	if _, err := f.db.ExecContext(t.Context(), `ALTER TABLE users RENAME COLUMN active TO active_renamed`); err != nil {
		t.Fatalf("rename column: %v", err)
	}

	resp := upsertRoundTrip(t, brk, ns, "test-gw", userUpsertRequest{Issuer: "https://idp", Sub: "sub-login"})
	if resp.Error == nil || resp.Error.Message != "upsert failed" {
		t.Errorf("reply = %+v, want the upsert-failed error", resp)
	}
	var n int
	if err := f.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM users WHERE sub = ?`, "sub-login").Scan(&n); err != nil {
		t.Fatalf("count users: %v", err)
	}
	if n != 0 {
		t.Error("user written although the read inside its transaction failed")
	}
}

func TestIdentityResolver_RecordsAuthzOutboxEventOnAppliedRotation(t *testing.T) {
	f := newAuthzOutboxFixture(t)
	ctx := t.Context()
	bindings := store.NewEntrypointIdentityBindingsRepo(f.db)
	u := f.seedUser(t, "sub-1", "one@example.com")
	if _, err := bindings.ObserveContinuity(ctx, "gw", ClaimKeyPhoneNumber, "15551234567", "tok-1", u.ID, 1); err != nil {
		t.Fatalf("seed binding: %v", err)
	}
	r := newUsersRepoIdentityResolver(f.users, f.logger).withContinuity(bindings).withAuthzOutbox(f.outbox, f.notifier)

	rebind := identityLookupRequest{GatewayID: "gw", Key: ClaimKeyPhoneNumber, PredecessorToken: "tok-1", ContinuityToken: "tok-2"}
	if _, applied, err := r.continuityVerdict(ctx, rebind, "15551234567", u.ID); err != nil || !applied {
		t.Fatalf("rebind: applied %v, err %v", applied, err)
	}
	f.events.expect(t, "applied rebind", authzOutboxUsersChanged, u.ID)

	if _, applied, err := r.continuityVerdict(ctx, rebind, "15551234567", u.ID); err != nil || applied {
		t.Fatalf("replayed rebind: applied %v, err %v", applied, err)
	}
	f.events.expectNone(t, "replayed rebind")

	discard := identityLookupRequest{GatewayID: "gw", Key: ClaimKeyPhoneNumber, DiscardBinding: true}
	if _, applied, err := r.continuityVerdict(ctx, discard, "15551234567", u.ID); err != nil || !applied {
		t.Fatalf("discard: applied %v, err %v", applied, err)
	}
	f.events.expect(t, "applied discard", authzOutboxUsersChanged, u.ID)

	if _, applied, err := r.continuityVerdict(ctx, discard, "15550000000", u.ID); err != nil || applied {
		t.Fatalf("discard of an unseen value: applied %v, err %v", applied, err)
	}
	f.events.expectNone(t, "discard of an unseen value")
}

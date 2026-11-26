// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/control"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func TestUserUpsert_UnparseableClaimsWarnsOnConservativeFlush(t *testing.T) {
	const ns = "rbac-upsert-bad-claims"
	brk := newTestBroker(t)
	composer, usersRepo, _, _ := dbComposerWithClaimMapping(t, "azure", `{"groups":["CANARY-GROUP"`)

	logger, buf := captureLogger()
	resolver := &countingResolver{delegate: newUsersRepoIdentityResolver(usersRepo, discardLogger())}
	lookupHandler := startAuthzHandlerWithResolver(t, brk, composer, resolver, ns)
	h := NewUserUpsertHandler(usersRepo, brk, UserUpsertConfig{Namespace: ns}, logger)
	h.SetGrantRefresh(composer, lookupHandler, nil)
	if err := h.Start(context.Background()); err != nil {
		t.Fatalf("start upsert handler: %v", err)
	}
	t.Cleanup(func() { _ = h.Stop(context.Background()) })
	time.Sleep(20 * time.Millisecond)

	roundTrip(t, nil, brk, ns, claimUserEmail, nil)

	resp := upsertRoundTrip(t, brk, ns, "test-gw", userUpsertRequest{
		Issuer: "iss", Sub: "claimant", OIDCProvider: "azure",
		Email: claimUserEmail, Source: financeAdminClaims,
	})
	if resp.Error != nil {
		t.Fatalf("the upsert itself must still succeed: %+v", resp.Error)
	}

	roundTrip(t, nil, brk, ns, claimUserEmail, nil)
	if got := resolver.calls.Load(); got != 2 {
		t.Errorf("an unanswerable claim comparison must flush the cache: resolver called %d times, want 2", got)
	}

	out := buf.String()
	if !strings.Contains(out, "cannot compare mapped claims") {
		t.Errorf("an unanswerable claim comparison must say so; log = %q", out)
	}
	if strings.Contains(out, "CANARY-GROUP") {
		t.Errorf("the claims themselves must not be logged; log = %q", out)
	}
}

func startUserUpsertHandler(t *testing.T, brk broker.Broker, repo *store.UsersRepo, namespace string) *UserUpsertHandler {
	t.Helper()
	h := NewUserUpsertHandler(repo, brk, UserUpsertConfig{Namespace: namespace},
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := h.Start(context.Background()); err != nil {
		t.Fatalf("start handler: %v", err)
	}
	t.Cleanup(func() { _ = h.Stop(context.Background()) })
	time.Sleep(20 * time.Millisecond)
	return h
}

func upsertRoundTrip(t *testing.T, brk broker.Broker, namespace, gatewayID string, req userUpsertRequest) *control.Response {
	t.Helper()
	payload, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	reqCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	reply, err := brk.Request(reqCtx, a2a.UserUpsertTopic(namespace, gatewayID), &broker.Message{
		Payload: payload,
		ReplyTo: a2a.UserUpsertResponseTopic(namespace, gatewayID),
	}, time.Second)
	if err != nil {
		t.Fatalf("broker request: %v", err)
	}
	resp, err := control.UnmarshalResponse(reply.Payload)
	if err != nil {
		t.Fatalf("unmarshal reply: %v", err)
	}
	return resp
}

func TestUserUpsertHandler_PersistsUser(t *testing.T) {
	const ns = "users-test"
	db := store.TestDB(t)
	repo := store.NewUsersRepo(db)
	brk := newTestBroker(t)
	startUserUpsertHandler(t, brk, repo, ns)

	resp := upsertRoundTrip(t, brk, ns, "test-gw", userUpsertRequest{
		Issuer:       "https://idp.example.com",
		Sub:          "sub-alice",
		OIDCProvider: "azure",
		Email:        "alice@example.com",
		Groups:       []string{"sam-admins", "sam-users"},
	})
	if resp.Error != nil {
		t.Fatalf("expected success, got error: %+v", resp.Error)
	}

	got, groups, err := repo.GetByIssuerSub(context.Background(), "https://idp.example.com", "sub-alice")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got == nil {
		t.Fatal("expected a persisted user, got nil")
	}
	if got.Email == nil || *got.Email != "alice@example.com" {
		t.Errorf("email = %v, want alice@example.com", got.Email)
	}
	if got.OIDCProvider != "azure" {
		t.Errorf("oidcProvider = %q, want azure", got.OIDCProvider)
	}
	slices.Sort(groups)
	if !slices.Equal(groups, []string{"sam-admins", "sam-users"}) {
		t.Errorf("groups = %v, want [sam-admins sam-users]", groups)
	}
}

func TestUserUpsertHandler_MalformedPayloadErrors(t *testing.T) {
	const ns = "users-test"
	brk := newTestBroker(t)
	repo := store.NewUsersRepo(store.TestDB(t))
	startUserUpsertHandler(t, brk, repo, ns)

	reqCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	reply, err := brk.Request(reqCtx, a2a.UserUpsertTopic(ns, "test-gw"), &broker.Message{
		Payload: []byte("{not json"),
		ReplyTo: a2a.UserUpsertResponseTopic(ns, "test-gw"),
	}, time.Second)
	if err != nil {
		t.Fatalf("broker request: %v", err)
	}
	resp, err := control.UnmarshalResponse(reply.Payload)
	if err != nil {
		t.Fatalf("unmarshal reply: %v", err)
	}
	if resp.Error == nil {
		t.Fatal("expected an error reply for malformed payload")
	}
}

func TestUserUpsertHandler_PhoneConflictPersistsWithoutPhone(t *testing.T) {
	const ns = "users-test"
	db := store.TestDB(t)
	repo := store.NewUsersRepo(db)
	brk := newTestBroker(t)
	startUserUpsertHandler(t, brk, repo, ns)

	respA := upsertRoundTrip(t, brk, ns, "test-gw", userUpsertRequest{
		Issuer: "iss", Sub: "sub-a", Phone: "+15550000001",
	})
	if respA.Error != nil {
		t.Fatalf("user A upsert: %+v", respA.Error)
	}

	respB := upsertRoundTrip(t, brk, ns, "test-gw", userUpsertRequest{
		Issuer: "iss", Sub: "sub-b", Phone: "+15550000001", Groups: []string{"g1"},
	})
	if respB.Error != nil {
		t.Fatalf("user B upsert should succeed (phone dropped), got %+v", respB.Error)
	}

	gotB, groupsB, err := repo.GetByIssuerSub(context.Background(), "iss", "sub-b")
	if err != nil {
		t.Fatalf("get B: %v", err)
	}
	if gotB == nil {
		t.Fatal("user B was not persisted")
	}
	if gotB.Phone != nil {
		t.Errorf("user B phone = %v, want nil (dropped on conflict)", gotB.Phone)
	}
	if len(groupsB) != 1 || groupsB[0] != "g1" {
		t.Errorf("user B groups = %v, want [g1]", groupsB)
	}

	var ownerSub string
	if err := db.QueryRowContext(context.Background(), `SELECT sub FROM users WHERE phone = ?`, "+15550000001").Scan(&ownerSub); err != nil {
		t.Fatalf("get by phone: %v", err)
	}
	if ownerSub != "sub-a" {
		t.Errorf("phone owner = %q, want sub-a", ownerSub)
	}
}

func TestUserUpsertHandler_MissingIdentityErrors(t *testing.T) {
	const ns = "users-test"
	db := store.TestDB(t)
	repo := store.NewUsersRepo(db)
	brk := newTestBroker(t)
	startUserUpsertHandler(t, brk, repo, ns)

	resp := upsertRoundTrip(t, brk, ns, "test-gw", userUpsertRequest{
		Sub:   "sub-only",
		Email: "x@example.com",
	})
	if resp.Error == nil {
		t.Fatal("expected an error reply when issuer is missing")
	}

	got, _, err := repo.GetByEmail(context.Background(), "x@example.com")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got != nil {
		t.Errorf("expected no row written, got %+v", got)
	}
}

func TestUserUpsertHandler_ReplyReportsAMaterializedGrant(t *testing.T) {
	const ns = "users-roles-changed"
	ctx := t.Context()
	db := store.TestDB(t)
	repo := store.NewUsersRepo(db)
	rbacRepo := store.NewRBACRepo(db)
	brk := newTestBroker(t)
	h := NewUserUpsertHandler(repo, brk, UserUpsertConfig{Namespace: ns}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	h.SetRBACRepo(rbacRepo)
	if err := h.Start(ctx); err != nil {
		t.Fatalf("start handler: %v", err)
	}
	t.Cleanup(func() { _ = h.Stop(context.Background()) })
	time.Sleep(20 * time.Millisecond)

	roleID := seedRoleUnseenByComposer(ctx, t, rbacRepo, "responder", []string{"agent:*:read"})
	for _, sub := range []string{"sub-a", "sub-b"} {
		if err := rbacRepo.CreatePendingUserRole(ctx, &store.RBACPendingUserRole{
			SubjectType: store.PendingSubjectSub, SubjectValue: sub, Issuer: "iss",
			RoleID: roleID, CreatedBy: "a", UpdatedBy: "a",
		}); err != nil {
			t.Fatalf("create pending for %s: %v", sub, err)
		}
	}
	rolesChanged := func(sub string) any {
		resp := upsertRoundTrip(t, brk, ns, "test-gw", userUpsertRequest{Issuer: "iss", Sub: sub, Phone: "+15550000001"})
		if resp.Error != nil {
			t.Fatalf("upsert %s: %+v", sub, resp.Error)
		}
		return resp.Result.(map[string]any)["roles_changed"]
	}

	if got := rolesChanged("sub-a"); got != true {
		t.Errorf("first login of sub-a: roles_changed = %v, want true", got)
	}
	if got := rolesChanged("sub-b"); got != true {
		t.Errorf("first login of sub-b (phone conflict): roles_changed = %v, want true", got)
	}
	if _, err := db.ExecContext(ctx, `UPDATE rbac_user_roles SET updated_time = updated_time - ?`,
		(2 * earlierAttemptWindow).Milliseconds()); err != nil {
		t.Fatalf("age grants: %v", err)
	}
	if got := rolesChanged("sub-a"); got != nil {
		t.Errorf("second login of sub-a: roles_changed = %v, want absent", got)
	}
}

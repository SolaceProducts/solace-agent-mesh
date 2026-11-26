// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"errors"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func grantFor(ctx context.Context, t *testing.T, svc *RBACService, roleID string, subject GrantSubject) string {
	t.Helper()
	resp, err := svc.CreateGrant(ctx, CreateGrantRequest{Subject: subject, RoleID: roleID}, "admin", []string{"*"})
	if err != nil {
		t.Fatalf("CreateGrant(%+v): %v", subject, err)
	}
	if resp.State != GrantStateConcrete {
		t.Fatalf("grant %+v is %s, want concrete", subject, resp.State)
	}
	return resp.ID
}

func recordedSubject(ctx context.Context, t *testing.T, svc *RBACService, id string) GrantSubject {
	t.Helper()
	grants, err := svc.ListGrants(ctx)
	if err != nil {
		t.Fatalf("ListGrants: %v", err)
	}
	for _, g := range grants {
		if g.ID == id && g.Subject != nil {
			return *g.Subject
		}
	}
	t.Fatalf("grant %s not listed", id)
	return GrantSubject{}
}

func TestRBACService_UpdateGrantSubject_RefusesASubjectNamingAnotherUser(t *testing.T) {
	svc, ur, ctx := newGrantTestService(t)
	roleID := mustGrantRole(ctx, t, svc)
	seedGrantUser(ctx, t, ur, "https://idp", "sub-alice", "alice@newco.com")
	seedGrantUser(ctx, t, ur, "https://idp", "sub-bob", "bob@newco.com")
	seedGrantUser(ctx, t, ur, "https://idp", "sub-erin", "")
	id := grantFor(ctx, t, svc, roleID, GrantSubject{Type: store.PendingSubjectEmail, Value: "alice@newco.com"})

	for name, subject := range map[string]GrantSubject{
		"another user's email":               {Type: store.PendingSubjectEmail, Value: "bob@newco.com"},
		"another user's sub":                 {Type: store.PendingSubjectSub, Issuer: "https://idp", Value: "sub-bob"},
		"an email no user has":               {Type: store.PendingSubjectEmail, Value: "ghost@newco.com"},
		"the sub at another issuer":          {Type: store.PendingSubjectSub, Issuer: "https://other", Value: "sub-alice"},
		"a bare sub of a user with an email": {Type: store.PendingSubjectLegacy, Value: "sub-alice"},
		"a bare sub of another user":         {Type: store.PendingSubjectLegacy, Value: "sub-erin"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := svc.UpdateGrantSubject(ctx, id, UpdateGrantRequest{Subject: subject}, "admin")
			if !errors.Is(err, errGrantSubjectNamesAnotherUser) {
				t.Fatalf("UpdateGrantSubject(%+v) = %v, want errGrantSubjectNamesAnotherUser", subject, err)
			}
		})
	}
	if got := recordedSubject(ctx, t, svc, id); got.Type != store.PendingSubjectEmail || got.Value != "alice@newco.com" {
		t.Errorf("recorded subject = %+v after refusals, want it unchanged", got)
	}
}

func TestRBACService_UpdateGrantSubject_ABareSubNamesAUserWithNoEmail(t *testing.T) {
	svc, ur, ctx := newGrantTestService(t)
	roleID := mustGrantRole(ctx, t, svc)
	seedGrantUser(ctx, t, ur, "https://idp", "sub-erin", "")
	id := grantFor(ctx, t, svc, roleID, GrantSubject{Type: store.PendingSubjectSub, Issuer: "https://idp", Value: "sub-erin"})

	want := GrantSubject{Type: store.PendingSubjectLegacy, Value: "sub-erin"}
	if _, err := svc.UpdateGrantSubject(ctx, id, UpdateGrantRequest{Subject: want}, "admin"); err != nil {
		t.Fatalf("UpdateGrantSubject: %v", err)
	}
	if got := recordedSubject(ctx, t, svc, id); got != want {
		t.Errorf("recorded subject = %+v, want %+v", got, want)
	}
}

func TestRBACService_UpdateGrantSubject_RefusesASystemUsersGrant(t *testing.T) {
	svc, _, ctx := newGrantTestService(t)
	roleID := mustGrantRole(ctx, t, svc)
	su, err := svc.CreateSystemUser(ctx, CreateSystemUserRequest{Name: "helpdesk", RoleIDs: []string{roleID}}, "admin")
	if err != nil {
		t.Fatalf("CreateSystemUser: %v", err)
	}
	grants, err := svc.ListGrants(ctx)
	if err != nil {
		t.Fatalf("ListGrants: %v", err)
	}
	var id string
	for _, g := range grants {
		if g.UserID == su.ID && g.RoleID == roleID {
			id = g.ID
		}
	}
	if id == "" {
		t.Fatalf("no grant for system user %s in %+v", su.ID, grants)
	}
	_, err = svc.UpdateGrantSubject(ctx, id, UpdateGrantRequest{Subject: GrantSubject{Type: store.PendingSubjectLegacy, Value: su.Sub}}, "admin")
	if !errors.Is(err, errSystemUserGrantSubject) {
		t.Fatalf("UpdateGrantSubject on a system user's grant = %v, want errSystemUserGrantSubject", err)
	}
}

func TestRBACService_UpdateGrantSubject_NotFound(t *testing.T) {
	svc, _, ctx := newGrantTestService(t)
	roleID := mustGrantRole(ctx, t, svc)
	pending, err := svc.CreateGrant(ctx, CreateGrantRequest{
		Subject: GrantSubject{Type: store.PendingSubjectEmail, Value: "ghost@newco.com"}, RoleID: roleID,
	}, "admin", []string{"*"})
	if err != nil {
		t.Fatalf("CreateGrant: %v", err)
	}
	for _, id := range []string{pending.ID, "01a16480-0000-7000-8000-000000000000"} {
		_, err := svc.UpdateGrantSubject(ctx, id, UpdateGrantRequest{Subject: GrantSubject{Type: store.PendingSubjectEmail, Value: "ghost@newco.com"}}, "admin")
		var nf *NotFoundError
		if !errors.As(err, &nf) {
			t.Errorf("UpdateGrantSubject(%s) = %v, want not found", id, err)
		}
	}
}

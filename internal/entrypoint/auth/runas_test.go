// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package auth

import (
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

func TestResolveRunAsIdentity_SwapsToSystemUser(t *testing.T) {
	real := &Subject{
		userID: "alice@example.com", email: "alice@example.com", authMethod: "slack", authenticated: true,
		credentialID: "key-1",
	}

	cases := []struct {
		runAs         string
		wantPrincipal string
	}{
		{"default", rbac.DefaultSystemUserSub},
		{"helpdesk", "system:helpdesk"},
		{"system:finance", "system:finance"},
	}
	for _, tc := range cases {
		id, swapped := ResolveRunAsIdentity(RunAsRequest{Real: real, IsMultiParty: true, RunAs: tc.runAs})
		if !swapped {
			t.Fatalf("run_as=%q multi-party swap active: expected swap", tc.runAs)
		}
		if id.userID != tc.wantPrincipal {
			t.Errorf("run_as=%q: UserID = %q, want %q", tc.runAs, id.userID, tc.wantPrincipal)
		}
		if !rbac.IsNonDelegatablePrincipal(id.userID) {
			t.Errorf("run_as=%q: swapped principal %q must be non-delegatable", tc.runAs, id.userID)
		}
		if id.email != "" {
			t.Errorf("run_as=%q: synthetic identity must carry no email, got %q", tc.runAs, id.email)
		}
		if id.authMethod != "slack" {
			t.Errorf("run_as=%q: AuthMethod = %q, want inherited 'slack'", tc.runAs, id.authMethod)
		}
		if id.CredentialID() != "" {
			t.Errorf("run_as=%q: CredentialID = %q, want it dropped with the real principal", tc.runAs, id.CredentialID())
		}
	}
}

func TestResolveRunAsIdentity_DefaultsWhenUnset(t *testing.T) {
	real := &Subject{userID: "alice@example.com"}
	id, swapped := ResolveRunAsIdentity(RunAsRequest{Real: real, IsMultiParty: true})
	if !swapped || id == nil {
		t.Fatalf("want a swapped identity; got swapped=%v id=%v", swapped, id)
	}
	if id.userID != rbac.DefaultSystemUserSub {
		t.Errorf("UserID = %q, want %q", id.userID, rbac.DefaultSystemUserSub)
	}
}

func TestResolveRunAsIdentity_NoSwap(t *testing.T) {
	real := &Subject{userID: "alice@example.com"}

	if id, swapped := ResolveRunAsIdentity(RunAsRequest{Real: real, IsMultiParty: true, UseUserIdentityInChannels: true}); swapped || id != real {
		t.Errorf("use_user_identity_in_channels=true: want real identity unchanged; got swapped=%v", swapped)
	}
	if id, swapped := ResolveRunAsIdentity(RunAsRequest{Real: real, IsMultiParty: false, RunAs: "helpdesk"}); swapped || id != real {
		t.Errorf("1:1 surface: want real identity unchanged; got swapped=%v", swapped)
	}
}

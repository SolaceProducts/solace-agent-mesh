// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package rbac

import (
	"strings"
	"testing"
)

func TestSystemUserPrincipal(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"blank yields default", "", DefaultSystemUserSub},
		{"bare name is namespaced", "helpdesk", "system:helpdesk"},
		{"already prefixed is unchanged", "system:helpdesk", "system:helpdesk"},
		{"default sub is unchanged", DefaultSystemUserSub, DefaultSystemUserSub},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := SystemUserPrincipal(tc.in); got != tc.want {
				t.Errorf("SystemUserPrincipal(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestIsSystemPrincipal(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"system:default", true},
		{"system:helpdesk", true},
		{"channel-user", false},
		{"alice@example.com", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := IsSystemPrincipal(tc.in); got != tc.want {
			t.Errorf("IsSystemPrincipal(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestIsNonDelegatablePrincipal(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{DefaultSystemUserSub, true},
		{"system:helpdesk", true},
		{"alice@example.com", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := IsNonDelegatablePrincipal(tc.in); got != tc.want {
			t.Errorf("IsNonDelegatablePrincipal(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestReservedSystemUserIDNamesOnlyTheSeededRows(t *testing.T) {
	for _, u := range ReservedSystemUsers() {
		if got := ReservedSystemUserID(u.Sub); got != u.ID {
			t.Errorf("ReservedSystemUserID(%q) = %q, want %q", u.Sub, got, u.ID)
		}
		if got := ReservedSystemUserID(SystemUserPrincipal(strings.TrimPrefix(u.Sub, SystemSubPrefix))); got != u.ID {
			t.Errorf("the run_as principal for %q maps to %q, want %q", u.Sub, got, u.ID)
		}
	}
	for _, principal := range []string{"system:qa-bot", "alice@example.com", ""} {
		if got := ReservedSystemUserID(principal); got != "" {
			t.Errorf("ReservedSystemUserID(%q) = %q, want none", principal, got)
		}
	}
}

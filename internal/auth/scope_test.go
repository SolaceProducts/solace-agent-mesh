// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package auth

import "testing"

func TestMatchScope(t *testing.T) {
	tests := []struct {
		name     string
		granted  string
		required string
		want     bool
	}{
		{"wildcard matches anything", "*", "tool:file:read", true},
		{"wildcard matches simple", "*", "anything", true},

		{"exact match", "tool:file:read", "tool:file:read", true},
		{"exact mismatch", "tool:file:read", "tool:file:update", false},

		{"wildcard segment middle", "agent:*:invoke", "agent:my_agent:invoke", true},
		{"wildcard segment first", "*:file:read", "tool:file:read", true},
		{"wildcard segment mismatch", "agent:*:invoke", "agent:my_agent:read", false},

		{"suffix wildcard", "tool:basic:*", "tool:basic:read", true},
		{"suffix wildcard write", "tool:basic:*", "tool:basic:update", true},
		{"suffix wildcard deep", "tool:*:*", "tool:basic:read", true},
		{"suffix wildcard no match prefix", "tool:advanced:*", "tool:basic:read", false},

		{"shorter granted no wildcard", "tool:file", "tool:file:read", false},
		{"longer granted no wildcard", "tool:file:read:extra", "tool:file:read", false},

		{"multiple wildcards", "*:*:*", "agent:foo:invoke", true},
		{"wildcard with mismatch", "*:*:delegate", "agent:foo:invoke", false},

		{"empty granted", "", "tool:file:read", false},
		{"both empty", "", "", true},
		{"single segment exact", "admin", "admin", true},
		{"single segment mismatch", "admin", "user", false},

		{"monitor namespace wildcard", "monitor/namespace/*:a2a_messages:subscribe", "monitor/namespace/my-ns:a2a_messages:subscribe", true},

		{"user scope exact match", "agent:alice__my_agent:invoke", "agent:alice__my_agent:invoke", true},
		{"user scope wildcard", "agent:alice__*:invoke", "agent:alice__my_agent:invoke", true},
		{"user scope wrong user", "agent:bob__*:invoke", "agent:alice__my_agent:invoke", false},
		{"user scope universal grant", "agent:*:invoke", "agent:alice__my_agent:invoke", true},

		{"mid-segment wildcard *:*:read", "*:*:read", "agent_builder:foo:read", true},
		{"mid-segment wildcard agent_builder:*:read", "agent_builder:*:read", "agent_builder:foo:read", true},
		{"nested wildcard tool/ns/*:read", "tool/ns/*:read", "tool/ns/my-ns:read", true},

		{"held collection sentinel satisfies wildcard verb", "connector:_:*", "connector:*:read", true},
		{"held collection sentinel satisfies category wildcard", "connector:_:*", "connector:*:*", true},
		{"held collection sentinel satisfies same verb", "connector:_:read", "connector:*:read", true},
		{"held wildcard satisfies collection sentinel", "connector:*:read", "connector:_:read", true},
		{"flat category sentinel satisfies wildcard verb", "evaluation:_:*", "evaluation:*:read", true},
		{"equivalence does not cross categories", "connector:_:*", "skill:*:read", false},

		{"segment 3 sentinel is not a wildcard", "tool:email:_", "tool:email:send", false},
		{"sentinel verb satisfies nothing real", "connector:_:_", "connector:*:read", false},
		{"four segments are left alone", "x:_:y:z", "x:*:y:z", false},
		{"two-segment grant keeps prefix-wildcard reach", "rbac:*", "rbac:_:create", true},
		{"segment-2 glob is not the sentinel", "tool:*__search_users:invoke", "tool:*__search_users:invoke", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := MatchScope(tt.granted, tt.required)
			if got != tt.want {
				t.Errorf("MatchScope(%q, %q) = %v, want %v", tt.granted, tt.required, got, tt.want)
			}
		})
	}
}

func TestHasAllScopes(t *testing.T) {
	tests := []struct {
		name     string
		owned    []string
		required []string
		want     bool
	}{
		{"no required scopes", []string{"tool:x:read"}, nil, true},
		{"empty owned denies (fail closed)", nil, []string{"analytics:_:read"}, false},
		{"empty owned with no required is vacuously true", nil, nil, true},
		{"wildcard owned is the auth-off sentinel", []string{"*"}, []string{"analytics:_:read"}, true},
		{"holds the exact scope", []string{"analytics:_:read"}, []string{"analytics:_:read"}, true},
		{"missing the scope", []string{"agent:*:invoke"}, []string{"analytics:_:read"}, false},
		{"wildcard owned satisfies required", []string{"analytics:*"}, []string{"analytics:_:read"}, true},
		{"holds all of several", []string{"a:read", "b:write"}, []string{"a:read", "b:write"}, true},
		{"missing one of several", []string{"a:read"}, []string{"a:read", "b:write"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := HasAllScopes(tt.owned, tt.required); got != tt.want {
				t.Errorf("HasAllScopes(%v, %v) = %v, want %v", tt.owned, tt.required, got, tt.want)
			}
		})
	}
}

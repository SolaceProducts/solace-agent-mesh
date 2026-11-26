// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package rbac

import (
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
)

func TestCheckScopes(t *testing.T) {
	tests := []struct {
		name     string
		granted  []string
		required []string
		want     bool
	}{
		{"empty required", []string{"tool:file:read"}, nil, true},
		{"empty required empty slice", []string{"tool:file:read"}, []string{}, true},
		{"single match", []string{"tool:file:read"}, []string{"tool:file:read"}, true},
		{"single no match", []string{"tool:file:read"}, []string{"tool:file:update"}, false},
		{"all required satisfied", []string{"tool:*:*", "agent:*:invoke"}, []string{"tool:file:read", "agent:foo:invoke"}, true},
		{"one required unsatisfied", []string{"tool:file:read"}, []string{"tool:file:read", "agent:foo:invoke"}, false},
		{"wildcard grants all", []string{"*"}, []string{"tool:file:read", "agent:foo:invoke"}, true},
		{"multiple granted one required", []string{"tool:file:read", "tool:file:update"}, []string{"tool:file:update"}, true},
		{"empty granted nonempty required", nil, []string{"tool:file:read"}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CheckScopes(tt.granted, tt.required)
			if got != tt.want {
				t.Errorf("CheckScopes(%v, %v) = %v, want %v", tt.granted, tt.required, got, tt.want)
			}
		})
	}
}

func TestPattern(t *testing.T) {
	valid := []string{
		"tool:file:read",
		"agent_builder:_:create",
		"*",
		"*",
		"tool:*:read",
		"a",
		"a-b",
		"a_b",
		"a0",
	}
	for _, s := range valid {
		if !Pattern.MatchString(s) {
			t.Errorf("Pattern should match %q but did not", s)
		}
	}

	invalid := []string{
		"",
		"UPPERCASE",
		"has space",
		"has.dot",
		":leading-colon",
		"trailing-colon:",
		"double::colon",
	}
	for _, s := range invalid {
		if Pattern.MatchString(s) {
			t.Errorf("Pattern should not match %q but did", s)
		}
	}
}

func TestValidateScopes(t *testing.T) {
	tests := []struct {
		name      string
		scopes    []string
		wantOK    bool
		wantInMsg string
	}{
		{"nil input", nil, true, ""},
		{"empty slice", []string{}, true, ""},
		{"single valid", []string{"tool:file:read"}, true, ""},
		{"multiple valid", []string{"*", "tool:file:read"}, true, ""},
		{"empty string entry", []string{""}, false, "must not be empty"},
		{"invalid chars", []string{"LOUD:VOICE"}, false, "LOUD:VOICE"},
		{"mixed valid and invalid", []string{"tool:file:read", ""}, false, "must not be empty"},
		{"wildcard alone", []string{"*"}, true, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			details, ok := ValidateScopes(tt.scopes)
			if ok != tt.wantOK {
				t.Errorf("ValidateScopes(%v): ok = %v, want %v", tt.scopes, ok, tt.wantOK)
			}
			if tt.wantOK {
				if details != nil {
					t.Errorf("ValidateScopes(%v): expected nil details on success, got %v", tt.scopes, details)
				}
				return
			}
			msgs, present := details["scopes"]
			if !present {
				t.Fatalf("ValidateScopes(%v): expected details[\"scopes\"], got %v", tt.scopes, details)
			}
			found := false
			for _, m := range msgs {
				if strings.Contains(m, tt.wantInMsg) {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("ValidateScopes(%v): no message contains %q; messages: %v", tt.scopes, tt.wantInMsg, msgs)
			}
		})
	}
}

func TestUnsatisfiedScopes(t *testing.T) {
	tests := []struct {
		name     string
		granted  []string
		required []string
		want     []string
	}{
		{"exact match", []string{"agent:*:read"}, []string{"agent:*:read"}, nil},
		{"wildcard instance covers concrete", []string{"agent:*:invoke"}, []string{"agent:abc:invoke"}, nil},
		{"concrete does not confer wildcard", []string{"agent:abc:invoke"}, []string{"agent:*:invoke"}, []string{"agent:*:invoke"}},
		{"star covers everything", []string{"*"}, []string{"agent:*:read", "tool:_:read"}, nil},
		{"one missing", []string{"agent:*:read"}, []string{"agent:*:read", "tool:_:read"}, []string{"tool:_:read"}},
		{"none held", nil, []string{"agent:*:read"}, []string{"agent:*:read"}},
		{"nothing required", []string{"agent:*:read"}, nil, nil},
		{"three-segment star satisfies bare star", []string{"*:*:*"}, []string{"*"}, nil},
		{"bare star satisfies three-segment star", []string{"*"}, []string{"*:*:*"}, nil},
		{"category wildcard does not satisfy bare star", []string{"agent:*:*"}, []string{"*"}, []string{"*"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := UnsatisfiedScopes(tc.granted, tc.required)
			if !slices.Equal(got, tc.want) {
				t.Errorf("UnsatisfiedScopes(%v, %v) = %v, want %v", tc.granted, tc.required, got, tc.want)
			}
		})
	}
}

func TestCheckScopesTracksVisibilityPredicate(t *testing.T) {
	cases := []struct {
		name     string
		granted  []string
		required []string
	}{
		{"exact match", []string{"agent:*:read"}, []string{"agent:*:read"}},
		{"wildcard instance covers concrete", []string{"agent:*:invoke"}, []string{"agent:abc:invoke"}},
		{"concrete does not confer wildcard", []string{"agent:abc:invoke"}, []string{"agent:*:invoke"}},
		{"held bare star covers everything", []string{"*"}, []string{"agent:*:read", "tool:_:read"}},
		{"nothing required", []string{"agent:*:read"}, nil},
		{"none held", nil, []string{"agent:*:read"}},
		{"three-segment star against required bare star", []string{"*:*:*"}, []string{"*"}},
		{"held bare star against required three-segment star", []string{"*"}, []string{"*:*:*"}},
		{"category wildcard against required bare star", []string{"agent:*:*"}, []string{"*"}},
		{"segment-2 sentinel held", []string{"connector:_:read"}, []string{"connector:abc:read"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got, want := CheckScopes(tc.granted, tc.required), auth.HasAllScopes(tc.granted, tc.required); got != want {
				t.Errorf("CheckScopes(%v, %v) = %v, auth.HasAllScopes = %v — enforcement and visibility must not diverge",
					tc.granted, tc.required, got, want)
			}
		})
	}
}

func TestScopeSetDelta(t *testing.T) {
	tests := []struct {
		name          string
		before, after []string
		want          []string
	}{
		{"no change", []string{"a:_:read"}, []string{"a:_:read"}, nil},
		{"pure addition", []string{"a:_:read"}, []string{"a:_:read", "b:_:read"}, []string{"b:_:read"}},
		{"pure removal", []string{"a:_:read", "b:_:read"}, []string{"a:_:read"}, []string{"b:_:read"}},
		{"swap reports both", []string{"a:_:read"}, []string{"b:_:read"}, []string{"b:_:read", "a:_:read"}},
		{"seeding a new role", nil, []string{"a:_:read"}, []string{"a:_:read"}},
		{"clearing everything", []string{"a:_:read"}, nil, []string{"a:_:read"}},
		{"reordering is not a change", []string{"a:_:read", "b:_:read"}, []string{"b:_:read", "a:_:read"}, nil},
		{"duplicates collapse", []string{"a:_:read"}, []string{"a:_:read", "b:_:read", "b:_:read"}, []string{"b:_:read"}},
		{"exact comparison: narrowing a wildcard to an instance is a change", []string{"a:*:read"}, []string{"a:one:read"}, []string{"a:one:read", "a:*:read"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := ScopeSetDelta(tc.before, tc.after); !slices.Equal(got, tc.want) {
				t.Errorf("ScopeSetDelta(%v, %v) = %v, want %v", tc.before, tc.after, got, tc.want)
			}
		})
	}
}

func TestIsSuperAdmin(t *testing.T) {
	tests := []struct {
		name   string
		scopes []string
		want   bool
	}{
		{"canonical spelling", []string{"*:*:*"}, true},
		{"legacy bare star", []string{"*"}, true},
		{"universal alongside others", []string{"agent:*:invoke", "*:*:*"}, true},
		{"rbac category wildcard is not universal", []string{"rbac:*:*"}, false},
		{"rbac management scopes are not universal", []string{"rbac:_:create", "rbac:_:update", "rbac:_:delete"}, false},
		{"rbac collection wildcard is not universal", []string{"rbac:_:*"}, false},
		{"every category listed is still not universal", []string{"agent:*:*", "connector:*:*", "rbac:*:*"}, false},
		{"empty grants nothing", nil, false},
		{"two-segment star is not a supported universal grant", []string{"*:*"}, false},
		{"partially wildcarded universal", []string{"*:*:read"}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsSuperAdmin(tc.scopes); got != tc.want {
				t.Errorf("IsSuperAdmin(%v) = %v, want %v", tc.scopes, got, tc.want)
			}
		})
	}
}

// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package rbac

import "testing"

func TestPartialGlob(t *testing.T) {
	for _, tc := range []struct {
		segment string
		want    bool
	}{
		{"*", false},
		{"_", false},
		{"activity", false},
		{"3f2b8c1e-0000-4000-8000-000000000001", false},
		{"**", true},
		{"***", true},
		{"a*", true},
		{"agent?", true},
		{"", false},
	} {
		if got := PartialGlob(tc.segment); got != tc.want {
			t.Errorf("PartialGlob(%q) = %v, want %v", tc.segment, got, tc.want)
		}
	}
}

func TestScopeHasPartialGlobChecksCategoryAndResource(t *testing.T) {
	for _, tc := range []struct {
		scope string
		want  bool
	}{
		{"activity:*:read", false},
		{"activity:_:read", false},
		{"agent:3f2b8c1e-0000-4000-8000-000000000001:read_feedback", false},
		{"activity:**:read", true},
		{"a*:*:read", true},
		{"*:*:*", false},
		{"activity:*:re*d", false},
	} {
		if got := ScopeHasPartialGlob(tc.scope); got != tc.want {
			t.Errorf("ScopeHasPartialGlob(%q) = %v, want %v", tc.scope, got, tc.want)
		}
	}
}

func TestHasStrictScopeRefusesPartialGlobs(t *testing.T) {
	const probe = ScopeActivityRead

	for _, tc := range []struct {
		name    string
		granted []string
		want    bool
	}{
		{"the literal wildcard grants it", []string{"activity:*:read"}, true},
		{"the collection sentinel grants it", []string{"activity:_:read"}, true},
		{"a full wildcard grants it", []string{"*:*:*"}, true},
		{"a resource glob does not", []string{"activity:**:read"}, false},
		{"a category glob does not", []string{"a*:*:read"}, false},
		{"a concrete id does not", []string{"activity:some-task:read"}, false},
		{"no scopes at all", nil, false},
		{"a good grant beside a glob still grants it", []string{"activity:**:read", "activity:*:read"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := HasStrictScope(tc.granted, probe); got != tc.want {
				t.Errorf("HasStrictScope(%v, %q) = %v, want %v", tc.granted, probe, got, tc.want)
			}
		})
	}
}

func TestStripPartialGlobScopesKeepsOrderAndDropsOnlyGlobs(t *testing.T) {
	got := stripPartialGlobScopes([]string{"activity:*:read", "activity:**:read", "a*:*:read", "agent:x:invoke"})

	want := []string{"activity:*:read", "agent:x:invoke"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

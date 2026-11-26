// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package pptxtool

import "testing"

func TestIconAliasesResolve(t *testing.T) {
	for alias, target := range iconAliases {
		if lookupIcon(target) == nil {
			t.Errorf("alias %q -> %q: target glyph not found", alias, target)
		}
		if lookupIcon(alias) == nil {
			t.Errorf("alias %q does not resolve to a glyph", alias)
		}
	}
}

func TestLookupIconCanonicalAndMissing(t *testing.T) {
	for _, name := range []string{"database", "rocket", "trophy", "workflow", "expand"} {
		if lookupIcon(name) == nil {
			t.Errorf("canonical glyph %q should resolve", name)
		}
	}
	if lookupIcon("definitely-not-a-glyph") != nil {
		t.Error("unknown glyph name should return nil")
	}
	if lookupIcon("") != nil {
		t.Error("empty glyph name should return nil")
	}
	if lookupIcon("  DataBase  ") == nil {
		t.Error("lookup should be case-insensitive and trimmed")
	}
}

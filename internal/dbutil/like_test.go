// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package dbutil

import "testing"

func TestEscapeLike(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"plain text is untouched", "weather report", "weather report"},
		{"percent becomes literal", "50%", `50\%`},
		{"underscore becomes literal", "snake_case", `snake\_case`},
		{"backslash is doubled", `a\b`, `a\\b`},
		{"an escaped wildcard is not re-escaped", `\%`, `\\\%`},
		{"empty stays empty", "", ""},
	}
	for _, tt := range tests {
		if got := escapeLike(tt.in); got != tt.want {
			t.Errorf("%s: escapeLike(%q) = %q, want %q", tt.name, tt.in, got, tt.want)
		}
	}
}

func TestLikeContainsFold(t *testing.T) {
	pred, arg := LikeContainsFold("users.display_name", "50%")

	if want := `LOWER(users.display_name) LIKE LOWER(?) ESCAPE '\'`; pred != want {
		t.Errorf("predicate = %q, want %q", pred, want)
	}
	if want := `%50\%%`; arg != want {
		t.Errorf("arg = %q, want %q", arg, want)
	}
}

func TestLikeContains(t *testing.T) {
	pred, arg := LikeContains("budgets.name_lower", "50%_é")

	if want := `budgets.name_lower LIKE ? ESCAPE '\'`; pred != want {
		t.Errorf("predicate = %q, want %q", pred, want)
	}
	if want := `%50\%\_é%`; arg != want {
		t.Errorf("arg = %q, want %q", arg, want)
	}
}

func TestLikeContainsFoldEscapesCallerWildcards(t *testing.T) {
	_, arg := LikeContainsFold("name", "%anything%")
	if want := `%\%anything\%%`; arg != want {
		t.Errorf("arg = %q, want %q", arg, want)
	}
}

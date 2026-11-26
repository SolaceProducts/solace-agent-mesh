// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package artifact

import "testing"

func TestSameOwner(t *testing.T) {
	tests := []struct {
		name string
		a, b string
		want bool
	}{
		{"identical", "alice@example.com", "alice@example.com", true},
		{"ASCII case", "Alice@Example.COM", "alice@example.com", true},
		{"different", "alice@example.com", "bob@example.com", false},
		{"prefix", "alice", "alice2", false},
		{"the last letter", "Zed@x.com", "zed@x.com", true},
		{"a byte just above Z is not a letter", "a[b", "a{b", false},
		{"a byte just below a is not folded", "a@x.com", "a`x.com", false},
		{"empty", "", "", true},
		{"empty and not", "", "a", false},
		{"dotted capital I folds to i under ToLower", "İvan@x.com", "ivan@x.com", false},
		{"Kelvin sign folds to k under EqualFold", "Kate@x.com", "kate@x.com", false},
		{"long s folds to s under EqualFold", "ſam@x.com", "sam@x.com", false},
		{"dotless i", "ıvan@x.com", "ivan@x.com", false},
		{"dotless i against capital I", "ıvan@x.com", "Ivan@x.com", false},
		{"the same non-ASCII id", "Zoë@x.com", "Zoë@x.com", true},
		{"non-ASCII letters keep their case", "ÉMILE@x.com", "émile@x.com", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := SameOwner(tt.a, tt.b); got != tt.want {
				t.Errorf("SameOwner(%q, %q) = %v, want %v", tt.a, tt.b, got, tt.want)
			}
			if got := SameOwner(tt.b, tt.a); got != tt.want {
				t.Errorf("SameOwner(%q, %q) = %v, want %v", tt.b, tt.a, got, tt.want)
			}
		})
	}
}

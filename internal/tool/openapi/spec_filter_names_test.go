// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package openapi

import "testing"

func TestEntryNamesOperation_MatchesLikeTheAllowList(t *testing.T) {
	tests := []struct {
		entry, name string
		want        bool
	}{
		{"getUser", "get_user", true},
		{"get_user", "getUser", true},
		{"GetUser", "get-user", true},
		{"getUsers", "get_user", false},
		{"", "get_user", false},
	}
	for _, tc := range tests {
		if got := EntryNamesOperation(tc.entry, tc.name); got != tc.want {
			t.Errorf("EntryNamesOperation(%q, %q) = %v, want %v", tc.entry, tc.name, got, tc.want)
		}
	}
}

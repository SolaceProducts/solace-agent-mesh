// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import "testing"

func TestSeccompBindDenyEnabled(t *testing.T) {
	cases := []struct {
		val  string
		want bool
	}{
		{"", false},
		{"1", true},
		{"true", true},
		{"TRUE", true},
		{"yes", true},
		{"on", true},
		{"  1  ", true},
		{"0", false},
		{"false", false},
		{"no", false},
		{"off", false},
		{"garbage", false},
	}
	for _, tc := range cases {
		t.Run(tc.val, func(t *testing.T) {
			t.Setenv("STR_SECCOMP_BIND_DENY", tc.val)
			if got := seccompBindDenyEnabled(); got != tc.want {
				t.Errorf("seccompBindDenyEnabled() with %q = %v, want %v", tc.val, got, tc.want)
			}
		})
	}
}

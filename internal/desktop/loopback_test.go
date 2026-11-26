// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package desktop

import "testing"

func TestRewriteBindHostToLoopback(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{":8000", "127.0.0.1:8000"},
		{"0.0.0.0:8000", "127.0.0.1:8000"},
		{"[::]:8000", "127.0.0.1:8000"},
		{"127.0.0.1:8000", "127.0.0.1:8000"},
		{"localhost:8000", "localhost:8000"},
		{"10.0.0.1:8080", "10.0.0.1:8080"},
		{"not-an-address", "not-an-address"},
		{"", ""},
	}
	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			got := rewriteBindHostToLoopback(tc.in)
			if got != tc.want {
				t.Fatalf("rewriteBindHostToLoopback(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestIsWildcardBindHost(t *testing.T) {
	tests := []struct {
		host string
		want bool
	}{
		{"", true},
		{"::", true},
		{"[::]", true},
		{"0.0.0.0", true},
		{"127.0.0.1", false},
		{"localhost", false},
		{"10.0.0.1", false},
	}
	for _, tc := range tests {
		t.Run(tc.host, func(t *testing.T) {
			if got := isWildcardBindHost(tc.host); got != tc.want {
				t.Fatalf("isWildcardBindHost(%q) = %v, want %v", tc.host, got, tc.want)
			}
		})
	}
}

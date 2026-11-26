// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package pull

import "testing"

func TestSafeFileNameAcceptsSimple(t *testing.T) {
	cases := []string{"orchestrator", "ops-slack", "video_creation", "model.v2"}
	for _, in := range cases {
		if safeFileName(in) != in {
			t.Errorf("safeFileName(%q) rejected; want passthrough", in)
		}
	}
}

func TestSafeFileNameRejectsTraversal(t *testing.T) {
	cases := []string{
		"../etc/passwd",
		"./hidden",
		"..",
		".",
		"a/b",
		`a\b`,
		".env",
		"",
	}
	for _, in := range cases {
		if safeFileName(in) != "" {
			t.Errorf("safeFileName(%q) = %q; want empty rejection", in, safeFileName(in))
		}
	}
}

func TestResourceFileName(t *testing.T) {
	for in, want := range map[string]string{
		"orchestrator":  "orchestrator",
		"Sales/EMEA":    "Sales_EMEA",
		`a\b`:           "a_b",
		"../etc/passwd": "_etc_passwd",
		".env":          "env",
		"..":            "",
		"":              "",
	} {
		if got := resourceFileName(in); got != want {
			t.Errorf("resourceFileName(%q) = %q; want %q", in, got, want)
		}
	}
}

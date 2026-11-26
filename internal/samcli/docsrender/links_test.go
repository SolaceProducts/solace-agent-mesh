// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package docsrender

import "testing"

func TestResolveLink(t *testing.T) {
	cases := []struct {
		name    string
		baseDir string
		dest    string
		want    string
		changed bool
	}{
		{"sibling md", "overview", "./architecture.md", "/docs/documentation/overview/architecture", true},
		{"child index", "", "./overview/index.md", "/docs/documentation/overview", true},
		{"parent dir", "installing", "../administering/observability.md", "/docs/documentation/administering/observability", true},
		{"with fragment", "installing", "./install.md#kubernetes-helm", "/docs/documentation/installing/install#kubernetes-helm", true},
		{"bare index", "concepts", "./index.md", "/docs/documentation/concepts", true},
		{"external url", "", "https://docs.solace.com", "https://docs.solace.com", false},
		{"in-page anchor", "overview", "#section", "#section", false},
		{"mailto", "", "mailto:x@y.com", "mailto:x@y.com", false},
		{"absolute path", "", "/already/routed", "/already/routed", false},
		{"non-md asset", "overview", "./diagram.png", "./diagram.png", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, changed := ResolveLink(tc.baseDir, tc.dest)
			if got != tc.want || changed != tc.changed {
				t.Errorf("ResolveLink(%q, %q) = (%q, %v), want (%q, %v)",
					tc.baseDir, tc.dest, got, changed, tc.want, tc.changed)
			}
		})
	}
}

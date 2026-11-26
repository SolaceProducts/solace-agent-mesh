// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package hostmatch

import "testing"

func TestMatch(t *testing.T) {
	cases := []struct {
		name  string
		allow []string
		host  string
		want  bool
	}{
		{"exact match", []string{"github.com"}, "github.com", true},
		{"exact no-match subdomain", []string{"github.com"}, "api.github.com", false},
		{"exact registry", []string{"github.com", "*.github.com", "registry.npmjs.org"}, "registry.npmjs.org", true},

		{"wildcard matches subdomain", []string{"*.github.com"}, "api.github.com", true},
		{"wildcard matches deep subdomain", []string{"github.com", "*.github.com", "registry.npmjs.org"}, "a.b.github.com", true},
		{"wildcard does not match bare base", []string{"*.github.com"}, "github.com", false},
		{"wildcard respects dot boundary", []string{"*.github.com"}, "evilgithub.com", false},
		{"wildcard suffix on attacker host", []string{"*.github.com"}, "github.com.attacker.com", false},
		{"list both base and wildcard - base", []string{"github.com", "*.github.com"}, "github.com", true},
		{"list both base and wildcard - sub", []string{"github.com", "*.github.com"}, "a.github.com", true},
		{"unlisted host not helped by wildcard", []string{"github.com", "*.github.com", "registry.npmjs.org"}, "npmjs.org", false},

		{"case-insensitive allowlist entry", []string{"GITHUB.COM"}, "github.com", true},
		{"case-insensitive host", []string{"github.com"}, "GitHub.COM", true},

		{"empty allowlist denies all", []string{}, "anything.com", false},
		{"empty host", []string{"foo.com"}, "", false},
		{"whitespace-only host", []string{"foo.com"}, "   ", false},
		{"leading/trailing whitespace host trimmed", []string{"foo.com"}, "  foo.com  ", true},
		{"trailing dot does not match", []string{"foo.com"}, "foo.com.", false},
		{"NUL byte in host", []string{"foo.com"}, "foo.com\x00", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Match(c.host, c.allow); got != c.want {
				t.Errorf("Match(host=%q, allow=%v) = %v, want %v", c.host, c.allow, got, c.want)
			}
		})
	}
}

// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package artifact

import "testing"

func TestScopedServiceResolve(t *testing.T) {
	const (
		agent = "billing-agent"
		ns    = "acme/prod"
	)
	cases := []struct {
		scope       string
		wantAppName string
	}{
		{"namespace", ns},
		{"app", agent},
		{"custom", agent},
		{"agent", agent},
		{"", agent},
	}
	for _, tc := range cases {
		t.Run(tc.scope, func(t *testing.T) {
			s := &ScopedService{Scope: tc.scope, Namespace: ns}
			got := s.Resolve(agent, "u", "sess", "file.txt")
			if got.AppName != tc.wantAppName {
				t.Errorf("scope %q: AppName = %q, want %q", tc.scope, got.AppName, tc.wantAppName)
			}
		})
	}
}

func TestNormalizeNamespace(t *testing.T) {
	cases := map[string]string{
		"local-acme-dev/":  "local-acme-dev",
		"local-acme-dev//": "local-acme-dev",
		"local-acme-dev":   "local-acme-dev",
		"":                 "",
	}
	for in, want := range cases {
		if got := NormalizeNamespace(in); got != want {
			t.Errorf("NormalizeNamespace(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNormalizeNamespaceKeepsAnAllSlashNamespaceUnroutable(t *testing.T) {
	for _, ns := range []string{"/", "//"} {
		got := NormalizeNamespace(ns)
		if got != ns {
			t.Errorf("NormalizeNamespace(%q) = %q, want it returned unchanged", ns, got)
		}
		if _, err := FormatURI(Locator{AppName: got, UserID: "u", SessionID: "s", Filename: "f.txt"}, 0); err == nil {
			t.Errorf("FormatURI accepted AppName %q; it must stay unroutable", got)
		}
	}
}

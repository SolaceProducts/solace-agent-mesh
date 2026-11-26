// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import "testing"

func TestAppConfigAuthzEnforcing(t *testing.T) {
	svc := func(typ string) map[string]any {
		return map[string]any{"authorization_service": map[string]any{"type": typ}}
	}
	merge := func(m map[string]any, fua bool) map[string]any {
		if m == nil {
			m = map[string]any{}
		}
		m["frontend_use_authorization"] = fua
		return m
	}

	tests := []struct {
		name      string
		appConfig map[string]any
		want      bool
	}{
		{"explicit default_rbac enforces", svc("default_rbac"), true},
		{"explicit deny_all enforces", svc("deny_all"), true},
		{"explicit none does not enforce", svc("none"), false},
		{"explicit none wins over frontend_use_authorization", merge(svc("none"), true), false},
		{"absent type + frontend_use_authorization true enforces", merge(nil, true), true},
		{"absent type + frontend_use_authorization false does not enforce", merge(nil, false), false},
		{"empty config does not enforce", map[string]any{}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := AppConfigAuthzEnforcing(tt.appConfig); got != tt.want {
				t.Fatalf("AppConfigAuthzEnforcing(%v) = %v, want %v", tt.appConfig, got, tt.want)
			}
		})
	}
}

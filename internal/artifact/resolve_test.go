// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package artifact

import "testing"

func TestResolveFilename(t *testing.T) {
	tests := []struct {
		input      string
		wantName   string
		wantScoped bool
	}{
		{"report.csv", "report.csv", false},
		{"user:settings.json", "settings.json", true},
		{"user:", "", true},
		{"", "", false},
		{"user:user:nested", "user:nested", true},
		{"User:case.txt", "User:case.txt", false},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			name, scoped := ResolveFilename(tt.input)
			if name != tt.wantName || scoped != tt.wantScoped {
				t.Errorf("ResolveFilename(%q) = (%q, %v), want (%q, %v)",
					tt.input, name, scoped, tt.wantName, tt.wantScoped)
			}
		})
	}
}

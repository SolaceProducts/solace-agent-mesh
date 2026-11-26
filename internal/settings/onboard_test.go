// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package settings

import "testing"

func TestDisplayBaseURL(t *testing.T) {
	tests := []struct {
		name, input, want string
	}{
		{"empty input shows hint", "", "(unset — set base_url before testing)"},
		{"populated input echoed back", "https://llm-proxy.example.com/v1", "https://llm-proxy.example.com/v1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := displayBaseURL(tt.input); got != tt.want {
				t.Errorf("displayBaseURL(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package secretname

import "testing"

func TestIsSecretFieldName(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{"", false},
		{"password", true},
		{"dbPassword", true},
		{"client_secret", true},
		{"token", true},
		{"authToken", true},
		{"auth_token", true},
		{"GITHUB_TOKEN", true},
		{"api_key", true},
		{"apiKey", true},
		{"API_KEY", true},
		{"X-API-Key", true},
		{"x-api-key", true},
		{"google_search_api_key", true},
		{"credentials", true},
		{"maxInputTokens", false},
		{"tokens", false},
		{"google_cse_id", false},
		{"LOG_LEVEL", false},
		{"base_url", false},
		{"Authorization", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsSecretFieldName(tc.name); got != tc.want {
				t.Fatalf("IsSecretFieldName(%q) = %v, want %v", tc.name, got, tc.want)
			}
		})
	}
}

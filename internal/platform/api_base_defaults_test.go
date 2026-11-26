// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import "testing"

func TestDefaultAPIBase(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		provider   string
		authConfig map[string]any
		want       string
	}{
		{"openai fixed", "openai", nil, "https://api.openai.com/v1"},
		{"anthropic fixed", "anthropic", nil, "https://api.anthropic.com"},
		{"google_ai_studio fixed", "google_ai_studio", nil, "https://generativelanguage.googleapis.com/v1beta"},
		{"vertex_ai derives from vertex_location", "vertex_ai", map[string]any{"vertex_location": "europe-west1"}, "https://europe-west1-aiplatform.googleapis.com/v1"},
		{"vertex_ai trims whitespace", "vertex_ai", map[string]any{"vertex_location": "  asia-east1  "}, "https://asia-east1-aiplatform.googleapis.com/v1"},
		{"vertex_ai without location returns empty", "vertex_ai", map[string]any{}, ""},
		{"vertex_ai with empty location returns empty", "vertex_ai", map[string]any{"vertex_location": "   "}, ""},
		{"bedrock derives from aws_region_name", "bedrock", map[string]any{"aws_region_name": "us-west-2"}, "https://bedrock-runtime.us-west-2.amazonaws.com"},
		{"bedrock trims whitespace", "bedrock", map[string]any{"aws_region_name": "  eu-west-1 "}, "https://bedrock-runtime.eu-west-1.amazonaws.com"},
		{"bedrock without region returns empty", "bedrock", map[string]any{}, ""},
		{"azure_openai has no default", "azure_openai", nil, ""},
		{"ollama has no default", "ollama", nil, ""},
		{"custom has no default", "custom", nil, ""},
		{"unknown provider returns empty", "made_up", nil, ""},
		{"nil authConfig for region-derived returns empty", "bedrock", nil, ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := DefaultAPIBase(tc.provider, tc.authConfig); got != tc.want {
				t.Errorf("DefaultAPIBase(%q, %v) = %q, want %q", tc.provider, tc.authConfig, got, tc.want)
			}
		})
	}
}

func TestValidAPIBaseStructure(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		apiBase  string
		expected bool
	}{
		{"blank is valid (clear-to-default)", "", true},
		{"whitespace is valid", "   ", true},
		{"well-formed https URL", "https://api.openai.com/v1", true},
		{"well-formed http URL", "http://localhost:8080", true},
		{"https with path", "https://api.example.com/v1/completions", true},
		{"missing // after scheme", "https:example.com", false},
		{"bare hostname no scheme", "example.com", false},
		{"path only", "api.openai.com/v1", false},
		{"embedded space", "https://foo bar.com", false},
		{"space after scheme", "https:// foo.com", false},
		{"embedded non-breaking space (U+00A0, JS-\\s parity)", "https://foo bar.com", false},
		{"unsupported scheme ftp", "ftp://api.example.com", false},
		{"unsupported scheme ws", "ws://api.example.com", false},
		{"scheme with no host", "https://", false},
		{"http with no host", "http://", false},
		{"scheme alone", "https:", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := ValidAPIBaseStructure(tc.apiBase); got != tc.expected {
				t.Errorf("ValidAPIBaseStructure(%q) = %v, want %v", tc.apiBase, got, tc.expected)
			}
		})
	}
}

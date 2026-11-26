// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tmpl

import (
	"strings"
	"testing"
)

func TestFormatName(t *testing.T) {
	tests := []struct {
		input     string
		kebab     string
		pascal    string
		snake     string
		upper     string
		spaced    string
		spacedCap string
	}{
		{
			input: "my_agent", kebab: "my-agent", pascal: "MyAgent",
			snake: "my_agent", upper: "MY_AGENT",
			spaced: "my agent", spacedCap: "My Agent",
		},
		{
			input: "MyAgent", kebab: "my-agent", pascal: "MyAgent",
			snake: "my_agent", upper: "MY_AGENT",
			spaced: "my agent", spacedCap: "My Agent",
		},
		{
			input: "my-cool-agent", kebab: "my-cool-agent", pascal: "MyCoolAgent",
			snake: "my_cool_agent", upper: "MY_COOL_AGENT",
			spaced: "my cool agent", spacedCap: "My Cool Agent",
		},
		{
			input: "APIKey", kebab: "api-key", pascal: "ApiKey",
			snake: "api_key", upper: "API_KEY",
			spaced: "api key", spacedCap: "API Key",
		},
		{
			input: "web search agent", kebab: "web-search-agent", pascal: "WebSearchAgent",
			snake: "web_search_agent", upper: "WEB_SEARCH_AGENT",
			spaced: "web search agent", spacedCap: "Web Search Agent",
		},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			names := FormatName(tt.input)
			if names.KebabCase != tt.kebab {
				t.Errorf("KebabCase: got %q, want %q", names.KebabCase, tt.kebab)
			}
			if names.PascalCase != tt.pascal {
				t.Errorf("PascalCase: got %q, want %q", names.PascalCase, tt.pascal)
			}
			if names.SnakeCase != tt.snake {
				t.Errorf("SnakeCase: got %q, want %q", names.SnakeCase, tt.snake)
			}
			if names.SnakeUpperCase != tt.upper {
				t.Errorf("SnakeUpperCase: got %q, want %q", names.SnakeUpperCase, tt.upper)
			}
			if names.SpacedName != tt.spaced {
				t.Errorf("SpacedName: got %q, want %q", names.SpacedName, tt.spaced)
			}
			if names.SpacedCapitalized != tt.spacedCap {
				t.Errorf("SpacedCapitalized: got %q, want %q", names.SpacedCapitalized, tt.spacedCap)
			}
		})
	}
}

func TestRender(t *testing.T) {
	tmpl := "name: __AGENT_NAME__\nnamespace: __NAMESPACE__"
	result := Render(tmpl, map[string]string{
		"__AGENT_NAME__": "MyAgent",
		"__NAMESPACE__":  "my-namespace",
	})
	expected := "name: MyAgent\nnamespace: my-namespace"
	if result != expected {
		t.Fatalf("got %q, want %q", result, expected)
	}
}

func TestFormatYAMLList(t *testing.T) {
	if got := FormatYAMLList(nil); got != "[]" {
		t.Fatalf("empty: got %q", got)
	}
	if got := FormatYAMLList([]string{"text", "file"}); got != `["text", "file"]` {
		t.Fatalf("two items: got %q", got)
	}
}

func TestLoad(t *testing.T) {
	content, err := Load("shared_config.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if content == "" {
		t.Fatal("shared_config.yaml is empty")
	}
	if !strings.Contains(content, "broker_connection") {
		t.Fatal("shared_config.yaml missing broker_connection")
	}
}

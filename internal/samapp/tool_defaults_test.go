// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package samapp

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
)

func TestExtractToolDefaults_NilOrEmpty(t *testing.T) {
	cases := []struct {
		name string
		in   any
	}{
		{"nil", nil},
		{"empty-map", map[string]any{}},
		{"wrong-shape", []any{"oops"}},
		{"empty-block", map[string]any{"web_search_google": map[string]any{}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ExtractToolDefaults(c.in); got != nil {
				t.Errorf("got %v, want nil", got)
			}
		})
	}
}

func TestExtractToolDefaults_Valid(t *testing.T) {
	in := map[string]any{
		"web_search_google": map[string]any{
			"api_base": "https://internal-proxy/google",
		},
		"generate_image_with_gemini": map[string]any{
			"gemini_api_base": "https://internal-proxy/gemini",
		},
	}
	want := map[string]map[string]any{
		"web_search_google": {
			"api_base": "https://internal-proxy/google",
		},
		"generate_image_with_gemini": {
			"gemini_api_base": "https://internal-proxy/gemini",
		},
	}
	got := ExtractToolDefaults(in)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestExtractToolDefaults_SkipsMalformedEntries(t *testing.T) {
	in := map[string]any{
		"web_search_google": map[string]any{"api_base": "ok"},
		"bogus":             "this is not a map",
	}
	got := ExtractToolDefaults(in)
	if len(got) != 1 || got["web_search_google"]["api_base"] != "ok" {
		t.Errorf("got %v, want exactly the web_search_google entry", got)
	}
	if _, present := got["bogus"]; present {
		t.Errorf("malformed entry leaked into result: %v", got)
	}
}

func TestToolDefaultsOf_FromLoadedConfig(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, "with-defaults.yaml", `
tool_defaults:
  web_search_google:
    api_base: "https://proxy.example/google"
apps: []
`)
	cfg, err := (&config.YAMLLoader{}).Load(t.Context(), p)
	if err != nil {
		t.Fatalf("loader.Load: %v", err)
	}
	got := ToolDefaultsOf(cfg)
	if got["web_search_google"]["api_base"] != "https://proxy.example/google" {
		t.Errorf("got %v, want web_search_google.api_base = proxy URL", got)
	}
}

func TestToolDefaultsOf_AbsentBlock(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, "no-defaults.yaml", "apps: []\n")
	cfg, err := (&config.YAMLLoader{}).Load(t.Context(), p)
	if err != nil {
		t.Fatalf("loader.Load: %v", err)
	}
	if got := ToolDefaultsOf(cfg); got != nil {
		t.Errorf("expected nil for absent block, got %v", got)
	}
}

func TestLoadFileToolDefaults_Present(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, "with-defaults.yaml", `
tool_defaults:
  generate_image_with_gemini:
    gemini_api_base: "https://proxy.example/gemini"
apps: []
`)
	got := LoadFileToolDefaults(t.Context(), &config.YAMLLoader{}, p)
	if got["generate_image_with_gemini"]["gemini_api_base"] != "https://proxy.example/gemini" {
		t.Errorf("got %v, want gemini proxy URL", got)
	}
}

func TestLoadFileToolDefaults_LoadFailureReturnsNil(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "does-not-exist.yaml")
	if got := LoadFileToolDefaults(t.Context(), &config.YAMLLoader{}, missing); got != nil {
		t.Errorf("expected nil for missing file, got %v", got)
	}
}

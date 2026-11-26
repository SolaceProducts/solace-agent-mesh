// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package samapp

import (
	"reflect"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
)

const bundledBuilderYAML = `
apps:
  - name: builder
    app_config:
      agent_name: Builder
      display_name: SAM Builder
      namespace: solace-agent-mesh
`

const bundledTwoAppsYAML = `
tool_defaults:
  web_search_google:
    api_base: https://bundled.example/google
apps:
  - name: builder
    app_config:
      agent_name: Builder
      namespace: solace-agent-mesh
  - name: extra
    app_config:
      agent_name: Extra
      namespace: solace-agent-mesh
`

func TestMergeBundledAppEntries_AddsBundled(t *testing.T) {
	existing := []AppEntry{{Name: "concierge", AppConfig: map[string]any{"agent_name": "Concierge"}}}
	bundled := map[string][]byte{"builder_agent.yaml": []byte(bundledBuilderYAML)}

	merged, td, err := MergeBundledAppEntries(t.Context(), &config.YAMLLoader{}, existing, bundled)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if td != nil {
		t.Errorf("expected nil tool_defaults, got %v", td)
	}
	if got, want := len(merged), 2; got != want {
		t.Fatalf("merged length: got %d, want %d", got, want)
	}
	if merged[0].Name != "concierge" || merged[1].Name != "builder" {
		t.Errorf("merged order: got %v, want [concierge builder]", names(merged))
	}
	if got, want := merged[1].AppConfig["agent_name"], "Builder"; got != want {
		t.Errorf("builder agent_name: got %v, want %v", got, want)
	}
}

func TestMergeBundledAppEntries_UserWinsOnDuplicate(t *testing.T) {
	userBuilder := AppEntry{
		Name:      "builder",
		AppConfig: map[string]any{"agent_name": "BuilderOverride"},
	}
	existing := []AppEntry{userBuilder}
	bundled := map[string][]byte{"builder_agent.yaml": []byte(bundledBuilderYAML)}

	merged, _, err := MergeBundledAppEntries(t.Context(), &config.YAMLLoader{}, existing, bundled)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got, want := len(merged), 1; got != want {
		t.Fatalf("merged length: got %d, want %d", got, want)
	}
	if got, want := merged[0].AppConfig["agent_name"], "BuilderOverride"; got != want {
		t.Errorf("agent_name: got %v, want %v (user override should win)", got, want)
	}
}

func TestMergeBundledAppEntries_ReturnsToolDefaultsAndMultipleApps(t *testing.T) {
	bundled := map[string][]byte{"two_apps.yaml": []byte(bundledTwoAppsYAML)}
	merged, td, err := MergeBundledAppEntries(t.Context(), &config.YAMLLoader{}, nil, bundled)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got, want := names(merged), []string{"builder", "extra"}; !reflect.DeepEqual(got, want) {
		t.Errorf("names: got %v, want %v", got, want)
	}
	want := map[string]map[string]any{
		"web_search_google": {"api_base": "https://bundled.example/google"},
	}
	if !reflect.DeepEqual(td, want) {
		t.Errorf("tool_defaults: got %v, want %v", td, want)
	}
}

func TestMergeBundledAppEntries_MalformedYAMLReturnsError(t *testing.T) {
	bundled := map[string][]byte{"bad.yaml": []byte("apps: [unclosed")}
	_, _, err := MergeBundledAppEntries(t.Context(), &config.YAMLLoader{}, nil, bundled)
	if err == nil {
		t.Fatal("expected error for malformed YAML, got nil")
	}
	if !strings.Contains(err.Error(), "bad.yaml") {
		t.Errorf("error should reference filename, got %q", err.Error())
	}
}

func TestMergeBundledAppEntries_EmptyBundledIsNoop(t *testing.T) {
	existing := []AppEntry{{Name: "concierge"}}
	merged, td, err := MergeBundledAppEntries(t.Context(), &config.YAMLLoader{}, existing, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if td != nil {
		t.Errorf("td: got %v, want nil", td)
	}
	if !reflect.DeepEqual(merged, existing) {
		t.Errorf("merged: got %v, want %v", merged, existing)
	}
}

func names(entries []AppEntry) []string {
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.Name)
	}
	return out
}

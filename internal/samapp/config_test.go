// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package samapp

import (
	"testing"
)

func TestParseAppList(t *testing.T) {
	list := []any{
		map[string]any{
			"name":       "agent1",
			"app_config": map[string]any{"model": "gpt-4"},
		},
		map[string]any{
			"name":       "agent2",
			"app_config": map[string]any{"model": "claude-3"},
		},
	}

	entries, err := ParseAppList(list)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("got %d entries, want 2", len(entries))
	}
	if entries[0].Name != "agent1" {
		t.Errorf("entries[0].Name = %q, want agent1", entries[0].Name)
	}
	if entries[1].Name != "agent2" {
		t.Errorf("entries[1].Name = %q, want agent2", entries[1].Name)
	}
	if entries[0].ModelRaw != "gpt-4" {
		t.Errorf("entries[0].ModelRaw = %v, want gpt-4", entries[0].ModelRaw)
	}
}

func TestParseAppList_SkipsPythonEntries(t *testing.T) {
	list := []any{
		map[string]any{
			"name":       "go-agent",
			"app_config": map[string]any{"model": "gpt-4"},
		},
		map[string]any{
			"name":       "python-agent",
			"app_module": "my_module",
			"app_config": map[string]any{"model": "gpt-4"},
		},
	}

	entries, err := ParseAppList(list)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(entries))
	}
	if entries[0].Name != "go-agent" {
		t.Errorf("entry name = %q, want go-agent", entries[0].Name)
	}
}

func TestParseAppList_SkipsEmptyName(t *testing.T) {
	list := []any{
		map[string]any{
			"name":       "",
			"app_config": map[string]any{"model": "gpt-4"},
		},
		map[string]any{
			"app_config": map[string]any{"model": "gpt-4"},
		},
	}

	entries, err := ParseAppList(list)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("got %d entries, want 0", len(entries))
	}
}

func TestParseAppList_SkipsNoAppConfig(t *testing.T) {
	list := []any{
		map[string]any{
			"name": "no-config",
		},
	}

	entries, err := ParseAppList(list)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("got %d entries, want 0", len(entries))
	}
}

func TestParseAppList_SkipsNonMap(t *testing.T) {
	list := []any{
		"not-a-map",
		42,
		map[string]any{
			"name":       "valid",
			"app_config": map[string]any{},
		},
	}

	entries, err := ParseAppList(list)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(entries))
	}
}

func TestParseAppList_Empty(t *testing.T) {
	entries, err := ParseAppList(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("got %d entries, want 0", len(entries))
	}
}

func TestExtractModelAliases_SiblingForm(t *testing.T) {
	raw := []any{
		map[string]any{"broker_connection": map[string]any{"url": "x"}},
		map[string]any{
			"models":   nil,
			"general":  map[string]any{"model": "gm"},
			"planning": map[string]any{"model": "pm"},
		},
	}
	aliases := ExtractModelAliases(raw)
	if len(aliases) != 2 {
		t.Fatalf("got %d aliases, want 2 (%v)", len(aliases), aliases)
	}
	if m, ok := aliases["general"].(map[string]any); !ok || m["model"] != "gm" {
		t.Errorf("general alias missing or wrong: %v", aliases["general"])
	}
	if m, ok := aliases["planning"].(map[string]any); !ok || m["model"] != "pm" {
		t.Errorf("planning alias missing or wrong: %v", aliases["planning"])
	}
}

func TestExtractModelAliases_NestedForm(t *testing.T) {
	raw := []any{
		map[string]any{
			"models": map[string]any{
				"general":  map[string]any{"model": "gm"},
				"planning": map[string]any{"model": "pm"},
			},
		},
	}
	aliases := ExtractModelAliases(raw)
	if len(aliases) != 2 {
		t.Fatalf("got %d aliases, want 2", len(aliases))
	}
}

func TestExtractModelAliases_NestedFormIgnoresSiblings(t *testing.T) {
	raw := []any{
		map[string]any{
			"models": map[string]any{
				"general": map[string]any{"model": "gm"},
			},
			"session_service": map[string]any{"type": "memory"},
		},
	}
	aliases := ExtractModelAliases(raw)
	if _, ok := aliases["session_service"]; ok {
		t.Errorf("session_service leaked in as a model alias: %v", aliases)
	}
	if _, ok := aliases["general"]; !ok {
		t.Errorf("expected 'general' in aliases, got %v", aliases)
	}
}

func TestExtractModelAliases_NoSharedConfig(t *testing.T) {
	if aliases := ExtractModelAliases(nil); aliases != nil {
		t.Errorf("expected nil, got %v", aliases)
	}
	if aliases := ExtractModelAliases("not a list"); aliases != nil {
		t.Errorf("expected nil for wrong type, got %v", aliases)
	}
	if aliases := ExtractModelAliases([]any{}); aliases != nil {
		t.Errorf("expected nil for empty list, got %v", aliases)
	}
}

func TestEnvOrDefault(t *testing.T) {
	t.Setenv("TEST_SAMAPP_VAR", "hello")

	if got := EnvOrDefault("TEST_SAMAPP_VAR", "fallback"); got != "hello" {
		t.Errorf("got %q, want hello", got)
	}
	if got := EnvOrDefault("TEST_SAMAPP_MISSING", "fallback"); got != "fallback" {
		t.Errorf("got %q, want fallback", got)
	}
}

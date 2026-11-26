// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package awe

import (
	"reflect"
	"testing"
)

func TestApplyToolDefaults_NoDefaults(t *testing.T) {
	SetToolDefaults(nil)
	t.Cleanup(func() { SetToolDefaults(nil) })

	in := map[string]any{
		"tool_name":   "web_search_google",
		"tool_config": map[string]any{"google_cse_id": "cse"},
	}
	out := applyToolDefaults("web_search_google", in)
	if !reflect.DeepEqual(out, in) {
		t.Errorf("no-defaults path mutated input: got %v, want %v", out, in)
	}
	if reflect.ValueOf(out).Pointer() != reflect.ValueOf(in).Pointer() {
		t.Errorf("no-defaults path should return the same map reference")
	}
}

func TestApplyToolDefaults_FillsMissingKeys(t *testing.T) {
	SetToolDefaults(map[string]map[string]any{
		"web_search_google": {"api_base": "https://internal-proxy/google"},
	})
	t.Cleanup(func() { SetToolDefaults(nil) })

	in := map[string]any{
		"tool_name": "web_search_google",
		"tool_config": map[string]any{
			"google_search_api_key": "key",
			"google_cse_id":         "cse",
		},
	}
	out := applyToolDefaults("web_search_google", in)
	cfg := out["tool_config"].(map[string]any)
	if cfg["api_base"] != "https://internal-proxy/google" {
		t.Errorf("api_base = %v, want internal proxy URL", cfg["api_base"])
	}
	if cfg["google_search_api_key"] != "key" || cfg["google_cse_id"] != "cse" {
		t.Errorf("per-tool config dropped: %v", cfg)
	}
}

func TestApplyToolDefaults_PerToolWinsOnConflict(t *testing.T) {
	SetToolDefaults(map[string]map[string]any{
		"web_search_google": {"api_base": "https://default.example/google"},
	})
	t.Cleanup(func() { SetToolDefaults(nil) })

	in := map[string]any{
		"tool_name": "web_search_google",
		"tool_config": map[string]any{
			"api_base": "https://override.example/google",
		},
	}
	out := applyToolDefaults("web_search_google", in)
	cfg := out["tool_config"].(map[string]any)
	if cfg["api_base"] != "https://override.example/google" {
		t.Errorf("api_base = %v, want per-tool override to win", cfg["api_base"])
	}
}

func TestApplyToolDefaults_NoToolConfigBlock(t *testing.T) {
	SetToolDefaults(map[string]map[string]any{
		"generate_image_with_gemini": {"gemini_api_base": "https://internal-proxy/gemini"},
	})
	t.Cleanup(func() { SetToolDefaults(nil) })

	in := map[string]any{"tool_name": "generate_image_with_gemini"}
	out := applyToolDefaults("generate_image_with_gemini", in)
	cfg, ok := out["tool_config"].(map[string]any)
	if !ok {
		t.Fatalf("tool_config missing after merge: %v", out)
	}
	if cfg["gemini_api_base"] != "https://internal-proxy/gemini" {
		t.Errorf("gemini_api_base = %v, want defaulted", cfg["gemini_api_base"])
	}
}

func TestApplyToolDefaults_UnknownToolName(t *testing.T) {
	SetToolDefaults(map[string]map[string]any{
		"web_search_google": {"api_base": "https://internal-proxy/google"},
	})
	t.Cleanup(func() { SetToolDefaults(nil) })

	in := map[string]any{
		"tool_name":   "something_else",
		"tool_config": map[string]any{"k": "v"},
	}
	out := applyToolDefaults("something_else", in)
	if !reflect.DeepEqual(out, in) {
		t.Errorf("unknown tool name should bypass merge: got %v, want %v", out, in)
	}
}

func TestApplyToolDefaults_DoesNotMutateRegistry(t *testing.T) {
	SetToolDefaults(map[string]map[string]any{
		"web_search_google": {"api_base": "https://default.example/google"},
	})
	t.Cleanup(func() { SetToolDefaults(nil) })

	for range 3 {
		out := applyToolDefaults("web_search_google", map[string]any{
			"tool_name":   "web_search_google",
			"tool_config": map[string]any{"per_call": "value"},
		})
		cfg := out["tool_config"].(map[string]any)
		if cfg["api_base"] != "https://default.example/google" {
			t.Fatalf("default mutated across calls: %v", cfg)
		}
	}
}

func TestMergeToolDefaults_EmptyInputIsNoop(t *testing.T) {
	SetToolDefaults(map[string]map[string]any{
		"web_search_google": {"api_base": "https://default.example/google"},
	})
	t.Cleanup(func() { SetToolDefaults(nil) })

	MergeToolDefaults(nil)
	MergeToolDefaults(map[string]map[string]any{})

	if got := toolDefaultsFor("web_search_google")["api_base"]; got != "https://default.example/google" {
		t.Errorf("registry mutated by empty merge: %v", got)
	}
}

func TestMergeToolDefaults_InitializesEmptyRegistry(t *testing.T) {
	SetToolDefaults(nil)
	t.Cleanup(func() { SetToolDefaults(nil) })

	MergeToolDefaults(map[string]map[string]any{
		"web_search_google": {"api_base": "https://first.example/google"},
	})

	if got := toolDefaultsFor("web_search_google")["api_base"]; got != "https://first.example/google" {
		t.Errorf("merge into nil registry lost data: %v", got)
	}
}

func TestMergeToolDefaults_ExtendsExistingTool(t *testing.T) {
	SetToolDefaults(map[string]map[string]any{
		"web_search_google": {"api_base": "https://first.example/google"},
	})
	t.Cleanup(func() { SetToolDefaults(nil) })

	MergeToolDefaults(map[string]map[string]any{
		"web_search_google": {"extra_key": "extra_value"},
	})

	got := toolDefaultsFor("web_search_google")
	if got["api_base"] != "https://first.example/google" {
		t.Errorf("merge dropped earlier key: %v", got)
	}
	if got["extra_key"] != "extra_value" {
		t.Errorf("merge did not add new key: %v", got)
	}
}

func TestMergeToolDefaults_LaterCallWinsOnConflict(t *testing.T) {
	SetToolDefaults(map[string]map[string]any{
		"web_search_google": {"api_base": "https://first.example/google"},
	})
	t.Cleanup(func() { SetToolDefaults(nil) })

	MergeToolDefaults(map[string]map[string]any{
		"web_search_google": {"api_base": "https://second.example/google"},
	})

	if got := toolDefaultsFor("web_search_google")["api_base"]; got != "https://second.example/google" {
		t.Errorf("later merge should win on conflict, got %v", got)
	}
}

func TestMergeToolDefaults_AddsNewTool(t *testing.T) {
	SetToolDefaults(map[string]map[string]any{
		"web_search_google": {"api_base": "https://first.example/google"},
	})
	t.Cleanup(func() { SetToolDefaults(nil) })

	MergeToolDefaults(map[string]map[string]any{
		"generate_image_with_gemini": {"gemini_api_base": "https://first.example/gemini"},
	})

	if got := toolDefaultsFor("generate_image_with_gemini")["gemini_api_base"]; got != "https://first.example/gemini" {
		t.Errorf("merge did not add new tool: %v", got)
	}
	if got := toolDefaultsFor("web_search_google")["api_base"]; got != "https://first.example/google" {
		t.Errorf("pre-existing tool lost on new-tool merge: %v", got)
	}
}

func TestSetToolDefaults_DeepCopiesInnerBlocks(t *testing.T) {
	caller := map[string]map[string]any{
		"web_search_google": {"api_base": "https://orig.example/google"},
	}
	SetToolDefaults(caller)
	t.Cleanup(func() { SetToolDefaults(nil) })

	caller["web_search_google"]["api_base"] = "https://mutated.example/google"

	if got := toolDefaultsFor("web_search_google")["api_base"]; got != "https://orig.example/google" {
		t.Errorf("registry aliased caller map: got %v", got)
	}
}

func TestSetToolDefaults_SkipsEmptyInnerBlocks(t *testing.T) {
	SetToolDefaults(map[string]map[string]any{
		"web_search_google":          {"api_base": "https://example/google"},
		"generate_image_with_gemini": {},
	})
	t.Cleanup(func() { SetToolDefaults(nil) })

	if toolDefaultsFor("generate_image_with_gemini") != nil {
		t.Errorf("empty inner block should not register")
	}
	if got := toolDefaultsFor("web_search_google")["api_base"]; got != "https://example/google" {
		t.Errorf("populated entry should survive: got %v", got)
	}
}

func TestSetToolDefaults_AllEmptyResolvesToNil(t *testing.T) {
	SetToolDefaults(map[string]map[string]any{
		"web_search_google":          {"api_base": "https://example/google"},
		"generate_image_with_gemini": {"gemini_api_base": "https://example/gemini"},
	})
	if toolDefaultsFor("web_search_google") == nil {
		t.Fatalf("setup did not populate registry")
	}

	SetToolDefaults(map[string]map[string]any{
		"a": {},
		"b": {},
	})
	t.Cleanup(func() { SetToolDefaults(nil) })

	if toolDefaultsFor("web_search_google") != nil {
		t.Errorf("all-empty input should clear registry")
	}
	if toolDefaultsFor("a") != nil || toolDefaultsFor("b") != nil {
		t.Errorf("empty entries should not register")
	}
}

func TestApplyToolDefaults_SkillPinnedProfileSurvivesDeploymentDefault(t *testing.T) {
	SetToolDefaults(map[string]map[string]any{
		"pptx_create_presentation": {"template_profile": "neutral"},
	})
	t.Cleanup(func() { SetToolDefaults(nil) })

	pinned := map[string]any{
		"tool_type":       "builtin",
		"tool_name":       "pptx_create_presentation",
		"timeout_seconds": 180,
		"tool_config":     map[string]any{"template_profile": "solace_2025"},
	}
	cfg, _ := applyToolDefaults("pptx_create_presentation", pinned)["tool_config"].(map[string]any)
	if got := cfg["template_profile"]; got != "solace_2025" {
		t.Errorf("pinned entry template_profile = %v, want solace_2025", got)
	}

	unpinned := map[string]any{"tool_type": "builtin", "tool_name": "pptx_create_presentation"}
	cfg, _ = applyToolDefaults("pptx_create_presentation", unpinned)["tool_config"].(map[string]any)
	if got := cfg["template_profile"]; got != "neutral" {
		t.Errorf("unpinned entry template_profile = %v, want the deployment default neutral", got)
	}
}

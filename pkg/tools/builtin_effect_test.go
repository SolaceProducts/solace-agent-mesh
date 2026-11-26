// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

var reviewedBuiltinEffects = map[string]tool.Effect{
	"append_to_artifact":                tool.EffectSideEffecting,
	"artifact_grep":                     tool.EffectReadOnly,
	"artifact_search_and_replace_regex": tool.EffectSideEffecting,
	"ask_user_question":                 tool.EffectSideEffecting,
	"create_image_from_description":     tool.EffectSideEffecting,
	"create_sqlite_db":                  tool.EffectSideEffecting,
	"deep_research":                     tool.EffectSideEffecting,
	"delete_artifact":                   tool.EffectSideEffecting,
	"describe_audio":                    tool.EffectReadOnly,
	"describe_image":                    tool.EffectReadOnly,
	"edit_image_with_gemini":            tool.EffectSideEffecting,
	"generate_image_with_gemini":        tool.EffectSideEffecting,
	"get_current_time":                  tool.EffectReadOnly,
	"list_artifacts":                    tool.EffectReadOnly,
	"load_artifact":                     tool.EffectReadOnly,
	"merge_structured_data":             tool.EffectSideEffecting,
	"query_data_with_sql":               tool.EffectSideEffecting,
	"reinterpret_artifact":              tool.EffectSideEffecting,
	"select_voice":                      tool.EffectReadOnly,
	"transcribe_audio":                  tool.EffectSideEffecting,
	"transform_data_with_jmespath":      tool.EffectSideEffecting,
	"web_request":                       tool.EffectSideEffecting,
	"web_search":                        tool.EffectReadOnly,
	"web_search_google":                 tool.EffectReadOnly,
}

func TestEveryPackageBuiltinToolHasItsEffectReviewed(t *testing.T) {
	t.Parallel()
	seen := map[string]bool{}
	for _, bt := range BuiltinTools() {
		name := bt.Name()
		seen[name] = true
		got := tool.EffectClassOf(bt)
		if got.Source != tool.EffectSourceDeclared {
			t.Errorf("%s: effect source %q, want %q", name, got.Source, tool.EffectSourceDeclared)
			continue
		}
		want, reviewed := reviewedBuiltinEffects[name]
		if !reviewed {
			t.Errorf("%s declares %q but is missing from reviewedBuiltinEffects; add it so the classification is reviewed", name, got.Effect)
			continue
		}
		if got.Effect != want {
			t.Errorf("%s: effect %q, want %q", name, got.Effect, want)
		}
	}
	for name := range reviewedBuiltinEffects {
		if !seen[name] {
			t.Errorf("reviewedBuiltinEffects lists %q, which BuiltinTools() does not return", name)
		}
	}
}

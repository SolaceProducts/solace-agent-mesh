// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package toolinit

import (
	"context"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
	"github.com/SolaceProducts/solace-agent-mesh/pkg/tools"
)

const (
	readOnly      = tool.EffectReadOnly
	sideEffecting = tool.EffectSideEffecting
)

var builtinEffects = map[string]tool.Effect{
	"list_artifacts":                    readOnly,
	"load_artifact":                     readOnly,
	"delete_artifact":                   sideEffecting,
	"append_to_artifact":                sideEffecting,
	"artifact_search_and_replace_regex": sideEffecting,
	"artifact_grep":                     readOnly,
	"get_current_time":                  readOnly,
	"web_request":                       sideEffecting,
	"create_image_from_description":     sideEffecting,
	"describe_image":                    readOnly,
	"describe_audio":                    readOnly,
	"edit_image_with_gemini":            sideEffecting,
	"generate_image_with_gemini":        sideEffecting,
	"select_voice":                      readOnly,
	"transcribe_audio":                  sideEffecting,
	"ask_user_question":                 sideEffecting,
	"notify_user":                       sideEffecting,
	"schedule_task":                     sideEffecting,
	"list_scheduled_tasks":              readOnly,
	"update_scheduled_task":             sideEffecting,
	"delete_scheduled_task":             sideEffecting,
	"set_scheduled_task_enabled":        sideEffecting,
	"validate_component_config":         readOnly,
	"validate_build_manifest":           sideEffecting,
	"yaml_merge_artifact":               sideEffecting,
	"discover_mcp_tools":                sideEffecting,
	"check_mcp_server_auth":             sideEffecting,
	"parse_openapi_spec":                sideEffecting,
	"describe_tools":                    sideEffecting,
	"query_data_with_sql":               sideEffecting,
	"transform_data_with_jmespath":      sideEffecting,
	"merge_structured_data":             sideEffecting,
	"reinterpret_artifact":              sideEffecting,
	"index_search":                      readOnly,
	"platform_list_components":          sideEffecting,
	"platform_get_component_config":     sideEffecting,
	"web_search":                        readOnly,
	"web_search_google":                 readOnly,
	"deep_research":                     sideEffecting,
	"RedirectUser":                      readOnly,
	"redirect_user":                     readOnly,
	"request_build_activate":            sideEffecting,
	"scheduled_task_redirect":           readOnly,
	"create_sqlite_db":                  sideEffecting,
	"query_task_health":                 readOnly,
	"query_failed_tasks":                readOnly,
	"query_slow_tasks":                  readOnly,
	"query_agent_performance":           readOnly,
	"query_token_usage":                 readOnly,
	"query_scheduled_task_health":       readOnly,
	"query_feedback_summary":            readOnly,
	"query_tasks":                       readOnly,
	"report_activity":                   sideEffecting,
	"list_failed_tasks":                 sideEffecting,
	"summarize_feedback":                sideEffecting,
	"list_feedback":                     sideEffecting,
	"explain_task":                      sideEffecting,
	"explain_feedback":                  sideEffecting,
	"datadog_query_logs":                readOnly,
	"datadog_get_errors":                readOnly,
	"correlate_task_with_logs":          readOnly,
}

func TestEveryBuiltinDeclaresItsEffect(t *testing.T) {
	t.Parallel()
	for name, factory := range builtinToolFactories {
		built := factory(map[string]any{})
		if built == nil {
			continue
		}
		got := tool.EffectClassOf(built)
		if got.Source != tool.EffectSourceDeclared {
			t.Errorf("%s: effect source %q, want %q; embed tool.ReadOnlyTool or tool.SideEffectingTool in the tool's struct",
				name, got.Source, tool.EffectSourceDeclared)
			continue
		}
		want, listed := builtinEffects[name]
		if !listed {
			t.Errorf("%s declares %q but is missing from builtinEffects; add it so the classification is reviewed", name, got.Effect)
			continue
		}
		if got.Effect != want {
			t.Errorf("%s: effect %q, want %q", name, got.Effect, want)
		}
	}
	for name := range builtinEffects {
		if _, ok := builtinToolFactories[name]; !ok {
			t.Errorf("builtinEffects lists %q, which is not a built-in factory", name)
		}
	}
}

func TestBuiltinToolsListDeclaresEffects(t *testing.T) {
	t.Parallel()
	for _, bt := range tools.BuiltinTools() {
		if got := tool.EffectClassOf(bt); got.Source != tool.EffectSourceDeclared {
			t.Errorf("%s: effect source %q, want %q", bt.Name(), got.Source, tool.EffectSourceDeclared)
		}
	}
}

func TestRenamedBuiltinKeepsItsEffect(t *testing.T) {
	t.Parallel()
	renamed := &renamedBuiltinTool{BuiltinTool: &tools.ListArtifactsTool{}, name: "skill__list_artifacts"}
	if got := tool.EffectClassOf(renamed); got != tool.DeclaredEffect(tool.EffectReadOnly) {
		t.Fatalf("renamed list_artifacts = %+v, want its declared read_only", got)
	}
	web := &renamedBuiltinTool{BuiltinTool: tools.NewWebRequestTool(nil), name: "skill__web_request"}
	if got := tool.CallEffectClassOf(web, map[string]any{"url": "https://example.com"}); got.ReadOnly() {
		t.Fatalf("renamed web_request GET = %+v, want side_effecting: a fetch of a chosen URL is never read_only", got)
	}
}

type resolvedProbe struct {
	*tools.ListArtifactsTool
	seen []tool.EffectClass
}

func (p *resolvedProbe) ExecuteResolved(_ context.Context, _ map[string]any, _ *tool.Context, effect tool.EffectClass) (*tool.Result, error) {
	p.seen = append(p.seen, effect)
	return tool.OK("ok"), nil
}

func TestRenamedBuiltinForwardsTheResolvedEffect(t *testing.T) {
	t.Parallel()
	inner := &resolvedProbe{ListArtifactsTool: &tools.ListArtifactsTool{}}
	var renamed tool.BuiltinTool = &renamedBuiltinTool{BuiltinTool: inner, name: "skill__list_artifacts"}
	operator := tool.EffectClass{Effect: tool.EffectSideEffecting, Source: tool.EffectSourceOperator}
	if _, err := tool.ExecuteWithEffect(t.Context(), renamed, map[string]any{}, &tool.Context{}, operator); err != nil {
		t.Fatal(err)
	}
	if len(inner.seen) != 1 || inner.seen[0] != operator {
		t.Errorf("inner tool saw %+v, want [%+v]", inner.seen, operator)
	}
}

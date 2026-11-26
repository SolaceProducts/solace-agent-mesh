// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import (
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func planCfg(tools []any, connectors []any, nodes ...*NodeConfig) *WorkflowConfig {
	return &WorkflowConfig{Nodes: nodes, Tools: tools, Connectors: connectors}
}

func builtinEntry(name string) map[string]any {
	return map[string]any{"tool_type": "builtin", "tool_name": name}
}

func fatalMessages(problems []StaticProblem) []string {
	var out []string
	for _, p := range problems {
		if p.Fatal {
			out = append(out, p.Message)
		}
	}
	return out
}

func TestStaticToolNodeProblems_RejectsUndeclaredArgument(t *testing.T) {
	ts := toolSetWith(t, webRequestResultTool())
	cfg := planCfg([]any{builtinEntry("web_request")}, nil, &NodeConfig{
		ID: "fetch", Type: "tool", ToolName: "web_request",
		Input: map[string]any{"url": "https://example.com", "output_filename": "x.jpg"},
	})

	problems := StaticToolNodeProblems(cfg, ts, StaticCheckOptions{})
	fatal := fatalMessages(problems)
	if len(fatal) != 1 {
		t.Fatalf("expected exactly one fatal problem, got %d: %+v", len(fatal), problems)
	}
	for _, want := range []string{"fetch", "output_filename", "output_artifact_filename", "url"} {
		if !strings.Contains(fatal[0], want) {
			t.Errorf("message missing %q; got: %s", want, fatal[0])
		}
	}
}

func TestStaticToolNodeProblems_SkipsToolWithNoDeclaredParameters(t *testing.T) {
	tests := map[string]tool.Tool{
		"nil parameter schema":      &schemaTool{name: "opaque"},
		"schema with no properties": &schemaTool{name: "opaque", props: map[string]any{}},
	}
	for name, tl := range tests {
		t.Run(name, func(t *testing.T) {
			ts := toolSetWith(t, tl)
			cfg := planCfg([]any{builtinEntry("opaque")}, nil, &NodeConfig{
				ID: "n", Type: "tool", ToolName: "opaque",
				Input: map[string]any{"whatever": "x"},
			})
			if problems := StaticToolNodeProblems(cfg, ts, StaticCheckOptions{}); len(problems) != 0 {
				t.Errorf("expected no problems, got %+v", problems)
			}
		})
	}
}

func TestStaticToolNodeProblems_DiscoveredSchemaOnlyAdvises(t *testing.T) {
	discovered := webRequestResultTool()
	discovered.authored = false
	ts := toolSetWith(t, discovered)
	cfg := planCfg(nil, nil, &NodeConfig{
		ID: "fetch", Type: "tool", ToolName: "web_request",
		Input: map[string]any{"url": "https://example.com", "output_filename": "x.jpg"},
	})

	problems := StaticToolNodeProblems(cfg, ts, StaticCheckOptions{})
	if len(problems) != 1 {
		t.Fatalf("expected one problem, got %+v", problems)
	}
	if problems[0].Fatal {
		t.Errorf("a discovered schema must not produce a fatal problem: %+v", problems[0])
	}
}

func TestStaticToolNodeProblems_ShadowingDemotesToAdvisory(t *testing.T) {
	badArgs := map[string]any{"url": "https://example.com", "output_filename": "x.jpg"}
	node := func() *NodeConfig {
		return &NodeConfig{ID: "fetch", Type: "tool", ToolName: "web_request", Input: badArgs}
	}

	tests := map[string]struct {
		cfg       *WorkflowConfig
		opts      StaticCheckOptions
		wantFatal bool
	}{
		"no declaration at all": {
			cfg: planCfg(nil, nil, node()), wantFatal: true,
		},
		"explicit builtin entry": {
			cfg: planCfg([]any{builtinEntry("web_request")}, nil, node()), wantFatal: true,
		},
		"builtin-group entry": {
			cfg:       planCfg([]any{map[string]any{"tool_type": "builtin-group", "group_name": "web"}}, nil, node()),
			wantFatal: true,
		},
		"mcp entry": {
			cfg:       planCfg([]any{map[string]any{"tool_type": "mcp", "tool_name": "web_request"}}, nil, node()),
			wantFatal: false,
		},
		"openapi entry": {
			cfg: planCfg([]any{map[string]any{"tool_type": "openapi"}}, nil, node()), wantFatal: false,
		},
		"connector declared": {
			cfg: planCfg(nil, []any{map[string]any{"connector": "crm"}}, node()), wantFatal: false,
		},
		"explicit builtin entry wins over a sibling mcp entry": {
			cfg: planCfg([]any{
				builtinEntry("web_request"),
				map[string]any{"tool_type": "mcp", "tool_name": "something_else"},
			}, nil, node()),
			wantFatal: true,
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			ts := toolSetWith(t, webRequestResultTool())
			problems := StaticToolNodeProblems(tc.cfg, ts, tc.opts)
			if len(problems) != 1 {
				t.Fatalf("expected exactly one problem, got %+v", problems)
			}
			if problems[0].Fatal != tc.wantFatal {
				t.Errorf("Fatal = %v, want %v", problems[0].Fatal, tc.wantFatal)
			}
		})
	}
}

func TestStaticToolNodeProblems_PermissiveDemotesEveryProblem(t *testing.T) {
	ts := toolSetWith(t, webRequestResultTool())
	cfg := planCfg([]any{builtinEntry("web_request")}, nil,
		&NodeConfig{
			ID: "fetch", Type: "tool", ToolName: "web_request",
			Input: map[string]any{"url": "https://example.com", "output_filename": "x.jpg"},
		},
		&NodeConfig{
			ID: "use", Type: "tool", ToolName: "web_request", DependsOn: []string{"fetch"},
			Input: map[string]any{"url": "{{fetch.output.output_filename}}"},
		},
	)

	strict := StaticToolNodeProblems(cfg, ts, StaticCheckOptions{})
	if len(fatalMessages(strict)) == 0 {
		t.Fatalf("expected fatal problems without the flag, got %+v", strict)
	}

	permissive := StaticToolNodeProblems(cfg, ts, StaticCheckOptions{Permissive: true})
	if len(permissive) != len(strict) {
		t.Errorf("permissive changed the problem count: %d vs %d", len(permissive), len(strict))
	}
	if fatal := fatalMessages(permissive); len(fatal) != 0 {
		t.Errorf("permissive must leave nothing fatal, got %v", fatal)
	}
}

func TestStaticToolNodeProblems_ConnectorBoundNodeIsSkipped(t *testing.T) {
	badArgs := map[string]any{"url": "https://example.com", "output_filename": "x.jpg"}
	connectorNode := func() *NodeConfig {
		return &NodeConfig{
			ID: "push", Type: "tool", ToolName: "web_request",
			Connector: "my-crm", Input: map[string]any{"record_id": "1"},
		}
	}

	t.Run("its arguments are not checked, even with the name claimed as a builtin", func(t *testing.T) {
		ts := toolSetWith(t, webRequestResultTool())
		cfg := planCfg([]any{builtinEntry("web_request")}, nil, connectorNode())
		if problems := StaticToolNodeProblems(cfg, ts, StaticCheckOptions{}); len(problems) != 0 {
			t.Errorf("a connector-bound node must produce no problem, got %+v", problems)
		}
	})

	t.Run("a reference to its output is left alone", func(t *testing.T) {
		ts := toolSetWith(t, webRequestResultTool())
		cfg := planCfg([]any{builtinEntry("web_request")}, nil, connectorNode(),
			&NodeConfig{ID: "use", Type: "agent", AgentName: "a", DependsOn: []string{"push"},
				Input: map[string]any{"x": "{{push.output.record_id}}"}})
		if problems := StaticToolNodeProblems(cfg, ts, StaticCheckOptions{}); len(problems) != 0 {
			t.Errorf("a reference into a connector node must not be graded against the builtin, got %+v", problems)
		}
	})

	t.Run("an unrelated node still fails", func(t *testing.T) {
		ts := toolSetWith(t, webRequestResultTool())
		cfg := planCfg(nil, nil, connectorNode(),
			&NodeConfig{ID: "fetch", Type: "tool", ToolName: "web_request", Input: badArgs})
		fatal := fatalMessages(StaticToolNodeProblems(cfg, ts, StaticCheckOptions{}))
		if len(fatal) != 1 {
			t.Fatalf("expected the unrelated node to stay fatal, got %v", fatal)
		}
		if !strings.Contains(fatal[0], "fetch") {
			t.Errorf("fatal should name the offending node; got: %s", fatal[0])
		}
	})
}

func TestStaticToolNodeProblems_ShadowedNameSuppressesReferenceReport(t *testing.T) {
	nodes := func() []*NodeConfig {
		return []*NodeConfig{
			{ID: "fetch", Type: "tool", ToolName: "web_request",
				Input: map[string]any{"url": "https://example.com"}},
			{ID: "use", Type: "agent", AgentName: "a", DependsOn: []string{"fetch"},
				Input: map[string]any{"x": "{{fetch.output.page_text}}"}},
		}
	}

	t.Run("reported when nothing can shadow the name", func(t *testing.T) {
		ts := toolSetWith(t, webRequestResultTool())
		cfg := planCfg([]any{builtinEntry("web_request")}, nil, nodes()...)
		problems := StaticToolNodeProblems(cfg, ts, StaticCheckOptions{})
		if len(problems) != 1 {
			t.Fatalf("expected the reference to be reported, got %+v", problems)
		}
		if !strings.Contains(problems[0].Message, "page_text") {
			t.Errorf("message missing the field; got: %s", problems[0].Message)
		}
	})

	t.Run("suppressed when an mcp entry claims the name", func(t *testing.T) {
		ts := toolSetWith(t, webRequestResultTool())
		cfg := planCfg([]any{map[string]any{"tool_type": "mcp", "tool_name": "web_request"}},
			nil, nodes()...)
		if problems := StaticToolNodeProblems(cfg, ts, StaticCheckOptions{}); len(problems) != 0 {
			t.Errorf("a shadowable name must not be graded against the builtin, got %+v", problems)
		}
	})

	t.Run("an unknown node is still reported, shadowing or not", func(t *testing.T) {
		ts := toolSetWith(t, webRequestResultTool())
		cfg := planCfg([]any{map[string]any{"tool_type": "mcp", "tool_name": "web_request"}}, nil,
			&NodeConfig{ID: "fetch", Type: "tool", ToolName: "web_request",
				Input: map[string]any{"url": "{{ghost.output.x}}"}})
		if problems := StaticToolNodeProblems(cfg, ts, StaticCheckOptions{}); len(problems) != 1 {
			t.Fatalf("expected the unknown node to still be reported, got %+v", problems)
		}
	})
}

func TestStaticToolNodeProblems_SkipsNonToolAndUnknownTools(t *testing.T) {
	ts := toolSetWith(t, webRequestResultTool())
	tests := map[string]*NodeConfig{
		"agent node": {
			ID: "a", Type: "agent", AgentName: "x",
			Input: map[string]any{"anything": "y"},
		},
		"sub-workflow node": {
			ID: "w", Type: "workflow", WorkflowName: "child",
			Input: map[string]any{"anything": "y"},
		},
		"tool absent from the registry": {
			ID: "t", Type: "tool", ToolName: "my_toolset__do_thing",
			Input: map[string]any{"anything": "y"},
		},
		"tool node with no authored input": {
			ID: "t", Type: "tool", ToolName: "web_request",
		},
		"whole input is a coalesce operator": {
			ID: "t", Type: "tool", ToolName: "web_request",
			Input: map[string]any{"coalesce": []any{"a", "b"}},
		},
		"whole input is a concat operator": {
			ID: "t", Type: "tool", ToolName: "web_request",
			Input: map[string]any{"concat": []any{"a", "b"}},
		},
	}
	for name, node := range tests {
		t.Run(name, func(t *testing.T) {
			cfg := planCfg([]any{builtinEntry("web_request")}, nil, node)
			if problems := StaticToolNodeProblems(cfg, ts, StaticCheckOptions{}); len(problems) != 0 {
				t.Errorf("expected no problems, got %+v", problems)
			}
		})
	}
}

func TestStaticToolNodeProblems_OperatorNestedUnderAnArgumentStillChecked(t *testing.T) {
	ts := toolSetWith(t, webRequestResultTool())
	cfg := planCfg([]any{builtinEntry("web_request")}, nil, &NodeConfig{
		ID: "fetch", Type: "tool", ToolName: "web_request",
		Input: map[string]any{
			"not_a_param": map[string]any{"coalesce": []any{"a", "b"}},
		},
	})
	problems := StaticToolNodeProblems(cfg, ts, StaticCheckOptions{})
	if len(fatalMessages(problems)) != 1 {
		t.Fatalf("expected the argument name to still be checked, got %+v", problems)
	}
}

func TestStaticToolNodeProblems_NilArgumentsAreSafe(t *testing.T) {
	ts := toolSetWith(t, webRequestResultTool())
	if problems := StaticToolNodeProblems(nil, ts, StaticCheckOptions{}); problems != nil {
		t.Errorf("nil config: got %+v", problems)
	}
	if problems := StaticToolNodeProblems(planCfg(nil, nil), nil, StaticCheckOptions{}); problems != nil {
		t.Errorf("nil tool set: got %+v", problems)
	}
	cfg := planCfg(nil, nil, nil, &NodeConfig{ID: "", Type: "tool", ToolName: "web_request"})
	if problems := StaticToolNodeProblems(cfg, ts, StaticCheckOptions{}); problems != nil {
		t.Errorf("nil and unnamed nodes: got %+v", problems)
	}
}

func TestStaticToolNodeProblems_OrderIsDeterministic(t *testing.T) {
	ts := toolSetWith(t, webRequestResultTool())
	newCfg := func() *WorkflowConfig {
		return planCfg([]any{builtinEntry("web_request")}, nil,
			&NodeConfig{ID: "zeta", Type: "tool", ToolName: "web_request",
				Input: map[string]any{"bad_z": "x"}},
			&NodeConfig{ID: "alpha", Type: "tool", ToolName: "web_request",
				Input: map[string]any{"bad_a": "x"}},
			&NodeConfig{ID: "mid", Type: "tool", ToolName: "web_request",
				Input: map[string]any{"bad_m": "x"}},
		)
	}
	first := fatalMessages(StaticToolNodeProblems(newCfg(), ts, StaticCheckOptions{}))
	if len(first) != 3 {
		t.Fatalf("expected three problems, got %d", len(first))
	}
	if !strings.Contains(first[0], "alpha") || !strings.Contains(first[2], "zeta") {
		t.Errorf("expected node-ID order alpha, mid, zeta; got %v", first)
	}
	for range 5 {
		if got := fatalMessages(StaticToolNodeProblems(newCfg(), ts, StaticCheckOptions{})); !equalStrings(got, first) {
			t.Fatalf("order changed between runs:\n%v\n%v", first, got)
		}
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestStaticToolNodeProblems_DropsUnverifiedFieldReference(t *testing.T) {
	ts := toolSetWith(t, discoveredResultTool())
	cfg := planCfg(nil, nil,
		&NodeConfig{ID: "detect", Type: "tool", ToolName: "detect_objects", Input: map[string]any{"image": "x.jpg"}},
		&NodeConfig{ID: "draw", Type: "tool", ToolName: "detect_objects", DependsOn: []string{"detect"},
			Input: map[string]any{"image": "{{detect.output.extracted_fields}}"}},
	)

	atInit := validateRefs(t, cfg.Nodes, ts)
	if len(atInit) != 1 || atInit[0].kind != problemKindUnverifiedField {
		t.Fatalf("expected Init to raise one unverifiedField, got %v", atInit)
	}

	for _, p := range StaticToolNodeProblems(cfg, ts, StaticCheckOptions{}) {
		if strings.Contains(p.Message, "extracted_fields") {
			t.Errorf("a discovered-schema reference reached plan output: %q", p.Message)
		}
	}
}

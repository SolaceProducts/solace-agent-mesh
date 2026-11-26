// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import (
	"testing"
)

func TestResolveTemplate_WorkflowInput(t *testing.T) {
	outputs := map[string]map[string]any{
		"workflow_input": {"output": map[string]any{
			"text":  "hello",
			"count": 42,
			"nested": map[string]any{
				"deep": "value",
			},
		}},
	}

	tests := []struct {
		name    string
		expr    string
		want    any
		wantErr bool
	}{
		{"simple field", "{{workflow.input.text}}", "hello", false},
		{"number field", "{{workflow.input.count}}", 42, false},
		{"nested field", "{{workflow.input.nested.deep}}", "value", false},
		{"missing field resolves to absent", "{{workflow.input.missing}}", absent, false},
		{"whitespace in braces", "{{  workflow.input.text  }}", "hello", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveTemplate(tt.expr, outputs, nil)
			if (err != nil) != tt.wantErr {
				t.Fatalf("error = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("got %v (%T), want %v (%T)", got, got, tt.want, tt.want)
			}
		})
	}
}

func TestResolveTemplate_NodeOutput(t *testing.T) {
	outputs := map[string]map[string]any{
		"extract": {"output": map[string]any{
			"data":     "extracted",
			"priority": "high",
			"items":    []any{"a", "b", "c"},
		}},
		"classify": {"output": map[string]any{
			"category": "urgent",
			"score":    0.95,
		}},
	}

	tests := []struct {
		name    string
		expr    string
		want    any
		wantErr bool
	}{
		{"node output field", "{{extract.output.data}}", "extracted", false},
		{"node output number", "{{classify.output.score}}", 0.95, false},
		{"missing node returns nil", "{{unknown.output.data}}", nil, false},
		{"missing field errors", "{{extract.output.nonexistent}}", nil, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveTemplate(tt.expr, outputs, nil)
			if (err != nil) != tt.wantErr {
				t.Fatalf("error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if got != tt.want {
				t.Errorf("got %v (%T), want %v (%T)", got, got, tt.want, tt.want)
			}
		})
	}

	t.Run("full output", func(t *testing.T) {
		got, err := resolveTemplate("{{extract.output}}", outputs, nil)
		if err != nil {
			t.Fatal(err)
		}
		gotMap, ok := got.(map[string]any)
		if !ok {
			t.Fatalf("got type %T, want map[string]any", got)
		}
		if gotMap["data"] != "extracted" {
			t.Errorf("data = %v, want extracted", gotMap["data"])
		}
		if gotMap["priority"] != "high" {
			t.Errorf("priority = %v, want high", gotMap["priority"])
		}
		items, ok := gotMap["items"].([]any)
		if !ok || len(items) != 3 {
			t.Errorf("items = %v, want [a b c]", gotMap["items"])
		}
	})
}

func TestResolveTemplate_SpecialVariables(t *testing.T) {
	outputs := map[string]map[string]any{
		"_map_item":       {"output": map[string]any{"id": 1, "name": "item1"}},
		"_map_index":      {"output": 2},
		"_loop_iteration": {"output": 5},
	}

	tests := []struct {
		name string
		expr string
		want any
	}{
		{"map item field", "{{_map_item.id}}", 1},
		{"map item full", "{{_map_item}}", map[string]any{"id": 1, "name": "item1"}},
		{"map index", "{{_map_index}}", 2},
		{"loop iteration", "{{_loop_iteration}}", 5},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveTemplate(tt.expr, outputs, nil)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if wantMap, ok := tt.want.(map[string]any); ok {
				gotMap, ok := got.(map[string]any)
				if !ok {
					t.Fatalf("got type %T, want map", got)
				}
				for k, v := range wantMap {
					if gotMap[k] != v {
						t.Errorf("key %q: got %v, want %v", k, gotMap[k], v)
					}
				}
				return
			}
			if got != tt.want {
				t.Errorf("got %v (%T), want %v (%T)", got, got, tt.want, tt.want)
			}
		})
	}
}

func TestResolveTemplate_WorkflowRunID(t *testing.T) {
	outputs := map[string]map[string]any{
		"workflow": {"run_id": "exec-123", "task_id": "task-abc"},
	}

	tests := []struct {
		name string
		expr string
		want any
	}{
		{"run_id", "{{workflow.run_id}}", "exec-123"},
		{"task_id", "{{workflow.task_id}}", "task-abc"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveTemplate(tt.expr, outputs, nil)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("got %v (%T), want %v (%T)", got, got, tt.want, tt.want)
			}
		})
	}
}

func TestNewExecutionState_SeedsRunID(t *testing.T) {
	state := NewExecutionState("wf", "exec-xyz")
	got, err := resolveTemplate("{{workflow.run_id}}", state.NodeOutputs, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "exec-xyz" {
		t.Errorf("workflow.run_id = %v, want exec-xyz", got)
	}
}

func TestResolveTemplate_ArgoAliases(t *testing.T) {
	outputs := map[string]map[string]any{
		"_map_item":      {"output": map[string]any{"status": "ok"}},
		"workflow_input": {"output": map[string]any{"x": "param_value"}},
	}

	tests := []struct {
		name string
		expr string
		want any
	}{
		{"item alias", "{{item}}", map[string]any{"status": "ok"}},
		{"item field alias", "{{item.status}}", "ok"},
		{"workflow parameters alias", "{{workflow.parameters.x}}", "param_value"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveTemplate(tt.expr, outputs, nil)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if wantMap, ok := tt.want.(map[string]any); ok {
				gotMap, ok := got.(map[string]any)
				if !ok {
					t.Fatalf("got type %T, want map", got)
				}
				for k, v := range wantMap {
					if gotMap[k] != v {
						t.Errorf("key %q: got %v, want %v", k, gotMap[k], v)
					}
				}
				return
			}
			if got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestResolveTemplate_NotATemplate(t *testing.T) {
	outputs := map[string]map[string]any{}

	tests := []struct {
		name string
		expr string
		want any
	}{
		{"plain string", "hello world", "hello world"},
		{"embedded template", "prefix {{val}} suffix", "prefix {{val}} suffix"},
		{"unclosed brace", "{{unclosed", "{{unclosed"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveTemplate(tt.expr, outputs, nil)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestResolveValue_Recursive(t *testing.T) {
	outputs := map[string]map[string]any{
		"workflow_input": {"output": map[string]any{"name": "Alice"}},
		"extract":        {"output": map[string]any{"data": "result"}},
	}

	t.Run("dict with templates", func(t *testing.T) {
		input := map[string]any{
			"greeting": "{{workflow.input.name}}",
			"data":     "{{extract.output.data}}",
			"literal":  "static",
		}
		got, err := ResolveValue(input, outputs)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		m := got.(map[string]any)
		if m["greeting"] != "Alice" {
			t.Errorf("greeting = %v, want Alice", m["greeting"])
		}
		if m["data"] != "result" {
			t.Errorf("data = %v, want result", m["data"])
		}
		if m["literal"] != "static" {
			t.Errorf("literal = %v, want static", m["literal"])
		}
	})

	t.Run("list with templates", func(t *testing.T) {
		input := []any{"{{workflow.input.name}}", "literal", "{{extract.output.data}}"}
		got, err := ResolveValue(input, outputs)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		list := got.([]any)
		if list[0] != "Alice" {
			t.Errorf("[0] = %v, want Alice", list[0])
		}
		if list[1] != "literal" {
			t.Errorf("[1] = %v, want literal", list[1])
		}
		if list[2] != "result" {
			t.Errorf("[2] = %v, want result", list[2])
		}
	})

	t.Run("non-string passthrough", func(t *testing.T) {
		got, err := ResolveValue(42, outputs)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != 42 {
			t.Errorf("got %v, want 42", got)
		}
	})

	t.Run("nil passthrough", func(t *testing.T) {
		got, err := ResolveValue(nil, outputs)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != nil {
			t.Errorf("got %v, want nil", got)
		}
	})
}

func TestResolveValue_Coalesce(t *testing.T) {
	outputs := map[string]map[string]any{
		"extract": {"output": map[string]any{"data": "found"}},
	}

	t.Run("first non-nil wins", func(t *testing.T) {
		input := map[string]any{
			"coalesce": []any{
				"{{missing_node.output}}",
				"{{extract.output.data}}",
				"default",
			},
		}
		got, err := ResolveValue(input, outputs)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "found" {
			t.Errorf("got %v, want found", got)
		}
	})

	t.Run("all nil returns nil", func(t *testing.T) {
		input := map[string]any{
			"coalesce": []any{
				"{{missing1.output}}",
				"{{missing2.output}}",
			},
		}
		got, err := ResolveValue(input, outputs)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != nil {
			t.Errorf("got %v, want nil", got)
		}
	})

	t.Run("literal fallback", func(t *testing.T) {
		input := map[string]any{
			"coalesce": []any{
				"{{missing.output}}",
				"fallback",
			},
		}
		got, err := ResolveValue(input, outputs)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "fallback" {
			t.Errorf("got %v, want fallback", got)
		}
	})
}

func TestResolveValue_Concat(t *testing.T) {
	outputs := map[string]map[string]any{
		"workflow_input": {"output": map[string]any{"name": "Alice"}},
	}

	t.Run("basic concat", func(t *testing.T) {
		input := map[string]any{
			"concat": []any{"Hello, ", "{{workflow.input.name}}", "!"},
		}
		got, err := ResolveValue(input, outputs)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "Hello, Alice!" {
			t.Errorf("got %v, want Hello, Alice!", got)
		}
	})

	t.Run("skips nil values", func(t *testing.T) {
		input := map[string]any{
			"concat": []any{"prefix_", "{{missing.output}}", "_suffix"},
		}
		got, err := ResolveValue(input, outputs)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "prefix__suffix" {
			t.Errorf("got %v, want prefix__suffix", got)
		}
	})
}

func TestResolveStringWithTemplates(t *testing.T) {
	outputs := map[string]map[string]any{
		"classify": {"output": map[string]any{
			"category": "urgent",
			"score":    0.95,
		}},
		"workflow_input": {"output": map[string]any{"name": "Bob"}},
	}

	tests := []struct {
		name string
		s    string
		want string
	}{
		{
			"single embedded template",
			"Category is {{classify.output.category}}",
			"Category is urgent",
		},
		{
			"multiple templates",
			"{{classify.output.category}} with score {{classify.output.score}}",
			"urgent with score 0.95",
		},
		{
			"no templates",
			"plain text",
			"plain text",
		},
		{
			"unresolvable template kept",
			"value is {{missing.output.field}}",
			"value is {{missing.output.field}}",
		},
		{
			"argo alias in string",
			"Hello {{workflow.parameters.name}}",
			"Hello Bob",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ResolveStringWithTemplates(tt.s, outputs)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestResolveValue_TypePreservation(t *testing.T) {
	outputs := map[string]map[string]any{
		"node": {"output": map[string]any{
			"list":   []any{1, 2, 3},
			"number": 42,
			"flag":   true,
			"nested": map[string]any{"key": "val"},
		}},
	}

	t.Run("list preserved", func(t *testing.T) {
		got, err := ResolveValue("{{node.output.list}}", outputs)
		if err != nil {
			t.Fatal(err)
		}
		list, ok := got.([]any)
		if !ok {
			t.Fatalf("got type %T, want []any", got)
		}
		if len(list) != 3 {
			t.Errorf("len = %d, want 3", len(list))
		}
	})

	t.Run("number preserved", func(t *testing.T) {
		got, err := ResolveValue("{{node.output.number}}", outputs)
		if err != nil {
			t.Fatal(err)
		}
		if got != 42 {
			t.Errorf("got %v (%T), want 42 (int)", got, got)
		}
	})

	t.Run("bool preserved", func(t *testing.T) {
		got, err := ResolveValue("{{node.output.flag}}", outputs)
		if err != nil {
			t.Fatal(err)
		}
		if got != true {
			t.Errorf("got %v, want true", got)
		}
	})

	t.Run("map preserved", func(t *testing.T) {
		got, err := ResolveValue("{{node.output.nested}}", outputs)
		if err != nil {
			t.Fatal(err)
		}
		m, ok := got.(map[string]any)
		if !ok {
			t.Fatalf("got type %T, want map", got)
		}
		if m["key"] != "val" {
			t.Errorf("key = %v, want val", m["key"])
		}
	})
}

func TestResolveValue_EmbeddedTemplateInString(t *testing.T) {
	outputs := map[string]map[string]any{
		"fetch":          {"output": map[string]any{"filename": "web_content_abc.bin"}},
		"workflow_input": {"output": map[string]any{"greeting": "hello"}},
	}

	t.Run("embed markers survive interpolation for the tool's late resolver", func(t *testing.T) {
		got, err := ResolveValue("«artifact_content:{{fetch.output.filename}} >>> format:datauri»", outputs)
		if err != nil {
			t.Fatal(err)
		}
		want := "«artifact_content:web_content_abc.bin >>> format:datauri»"
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("adjacent templates interpolate instead of matching as one whole-string template", func(t *testing.T) {
		got, err := ResolveValue("{{fetch.output.filename}}/{{workflow_input.output.greeting}}", outputs)
		if err != nil {
			t.Fatal(err)
		}
		want := "web_content_abc.bin/hello"
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("two workflow.input templates spanning the whole string", func(t *testing.T) {
		wfOutputs := map[string]map[string]any{
			"workflow_input": {"output": map[string]any{"prefix": "run", "suffix": "final"}},
		}
		got, err := ResolveValue("{{workflow.input.prefix}}-{{workflow.input.suffix}}", wfOutputs)
		if err != nil {
			t.Fatal(err)
		}
		if got != "run-final" {
			t.Errorf("got %v, want %q", got, "run-final")
		}
	})

	t.Run("template mixed with plain text", func(t *testing.T) {
		got, err := ResolveValue("say {{workflow_input.output.greeting}} now", outputs)
		if err != nil {
			t.Fatal(err)
		}
		if got != "say hello now" {
			t.Errorf("got %q, want %q", got, "say hello now")
		}
	})

	t.Run("whole-string template still preserves native type", func(t *testing.T) {
		got, err := ResolveValue("{{fetch.output.filename}}", outputs)
		if err != nil {
			t.Fatal(err)
		}
		if got != "web_content_abc.bin" {
			t.Errorf("got %v, want web_content_abc.bin", got)
		}
	})

	t.Run("plain string with no template is unchanged", func(t *testing.T) {
		got, err := ResolveValue("no templates here", outputs)
		if err != nil {
			t.Fatal(err)
		}
		if got != "no templates here" {
			t.Errorf("got %q, want unchanged", got)
		}
	})
}

func TestResolveTemplate_WorkflowStatusError(t *testing.T) {
	outputs := map[string]map[string]any{
		"workflow_input": {"output": map[string]any{"text": "hello"}},
		"workflow": {
			"status": "failure",
			"error": map[string]any{
				"message": "node failed",
				"node_id": "step1",
			},
		},
	}

	tests := []struct {
		name string
		expr string
		want any
	}{
		{"workflow.status", "{{workflow.status}}", "failure"},
		{"workflow.error.message", "{{workflow.error.message}}", "node failed"},
		{"workflow.error.node_id", "{{workflow.error.node_id}}", "step1"},
		{"workflow.input still works", "{{workflow.input.text}}", "hello"},
		{"workflow.missing returns nil", "{{workflow.missing}}", nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveTemplate(tt.expr, outputs, nil)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("got %v (%T), want %v (%T)", got, got, tt.want, tt.want)
			}
		})
	}
}

func TestResolveTemplate_WorkflowStatusMissing(t *testing.T) {
	outputs := map[string]map[string]any{
		"workflow_input": {"output": map[string]any{"text": "hello"}},
	}

	got, err := resolveTemplate("{{workflow.status}}", outputs, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != nil {
		t.Errorf("got %v, want nil", got)
	}
}

// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"sync/atomic"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func workflowInputOutputs(supplied map[string]any) map[string]map[string]any {
	return map[string]map[string]any{
		"workflow_input": {"output": supplied},
	}
}

func TestResolveValue_UnsuppliedWorkflowInput_KeyIsOmitted(t *testing.T) {
	outputs := workflowInputOutputs(map[string]any{"supplied": "yes"})
	input := map[string]any{
		"present": "{{workflow.input.supplied}}",
		"absent":  "{{workflow.input.never_supplied}}",
	}

	got, err := ResolveValue(input, outputs)
	if err != nil {
		t.Fatalf("ResolveValue: %v", err)
	}
	m, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("got %T, want map[string]any", got)
	}
	if m["present"] != "yes" {
		t.Errorf("present = %v, want yes", m["present"])
	}
	if v, exists := m["absent"]; exists {
		t.Errorf("absent is present with value %v; an unsupplied input must not be sent at all", v)
	}
}

func TestResolveValue_ExplicitNullWorkflowInput_IsStillSent(t *testing.T) {
	outputs := workflowInputOutputs(map[string]any{"format": nil})

	got, err := ResolveValue(map[string]any{"format": "{{workflow.input.format}}"}, outputs)
	if err != nil {
		t.Fatalf("ResolveValue: %v", err)
	}
	m := got.(map[string]any)
	v, exists := m["format"]
	if !exists {
		t.Fatal("format was dropped; a caller-supplied null is a value, not an absence")
	}
	if v != nil {
		t.Errorf("format = %v, want nil", v)
	}
}

func TestResolveValue_UnsuppliedWorkflowInput_NestedAndListPositions(t *testing.T) {
	outputs := workflowInputOutputs(map[string]any{"supplied": "yes"})

	t.Run("nested map key is dropped", func(t *testing.T) {
		input := map[string]any{
			"substitutions": map[string]any{
				"NAME":  "{{workflow.input.supplied}}",
				"TITLE": "{{workflow.input.never_supplied}}",
			},
		}
		got, err := ResolveValue(input, outputs)
		if err != nil {
			t.Fatalf("ResolveValue: %v", err)
		}
		nested := got.(map[string]any)["substitutions"].(map[string]any)
		if _, exists := nested["TITLE"]; exists {
			t.Error("TITLE survived inside a nested map")
		}
		if nested["NAME"] != "yes" {
			t.Errorf("NAME = %v, want yes", nested["NAME"])
		}
	})

	t.Run("list element stays nil rather than shifting the list", func(t *testing.T) {
		input := map[string]any{
			"ids": []any{"{{workflow.input.supplied}}", "{{workflow.input.never_supplied}}", "last"},
		}
		got, err := ResolveValue(input, outputs)
		if err != nil {
			t.Fatalf("ResolveValue: %v", err)
		}
		list := got.(map[string]any)["ids"].([]any)
		if len(list) != 3 {
			t.Fatalf("ids = %v, want 3 elements", list)
		}
		if list[0] != "yes" || list[1] != nil || list[2] != "last" {
			t.Errorf("ids = %#v, want [yes <nil> last]", list)
		}
	})

	t.Run("a bare reference resolves to nil at the top level", func(t *testing.T) {
		got, err := ResolveValue("{{workflow.input.never_supplied}}", outputs)
		if err != nil {
			t.Fatalf("ResolveValue: %v", err)
		}
		if got != nil {
			t.Errorf("got %#v (%T), want nil", got, got)
		}
	})
}

func TestResolveValue_UnsuppliedWorkflowInput_EmbeddedReferenceUnchanged(t *testing.T) {
	outputs := workflowInputOutputs(map[string]any{"supplied": "yes"})

	got, err := ResolveValue(map[string]any{"path": "dir/{{workflow.input.never_supplied}}.txt"}, outputs)
	if err != nil {
		t.Fatalf("ResolveValue: %v", err)
	}
	m := got.(map[string]any)
	if m["path"] != "dir/{{workflow.input.never_supplied}}.txt" {
		t.Errorf("path = %v, want the literal reference left in place", m["path"])
	}
}

func TestResolveValue_Coalesce_AbsenceHandling(t *testing.T) {
	outputs := map[string]map[string]any{
		"workflow_input": {"output": map[string]any{"explicitNull": nil}},
		"ran":            {"output": map[string]any{}},
	}

	t.Run("a literal fallback still wins over an unsupplied input", func(t *testing.T) {
		input := map[string]any{"title": map[string]any{"coalesce": []any{
			"{{workflow.input.never_supplied}}", "Untitled",
		}}}
		got, err := ResolveValue(input, outputs)
		if err != nil {
			t.Fatalf("ResolveValue: %v", err)
		}
		if title := got.(map[string]any)["title"]; title != "Untitled" {
			t.Errorf("title = %v, want Untitled", title)
		}
	})

	t.Run("all-unsupplied coalesce drops the key", func(t *testing.T) {
		input := map[string]any{"userId": map[string]any{"coalesce": []any{
			"{{workflow.input.never_supplied}}", "{{workflow.input.also_missing}}",
		}}}
		got, err := ResolveValue(input, outputs)
		if err != nil {
			t.Fatalf("ResolveValue: %v", err)
		}
		if v, exists := got.(map[string]any)["userId"]; exists {
			t.Errorf("userId is present with value %v, want the key dropped", v)
		}
	})

	t.Run("a supplied null keeps the key", func(t *testing.T) {
		input := map[string]any{"format": map[string]any{"coalesce": []any{
			"{{workflow.input.explicitNull}}", "{{workflow.input.never_supplied}}",
		}}}
		got, err := ResolveValue(input, outputs)
		if err != nil {
			t.Fatalf("ResolveValue: %v", err)
		}
		v, exists := got.(map[string]any)["format"]
		if !exists {
			t.Fatal("format was dropped; one argument was a caller-supplied null")
		}
		if v != nil {
			t.Errorf("format = %v, want nil", v)
		}
	})

	t.Run("concat never stringifies an unsupplied input", func(t *testing.T) {
		input := map[string]any{"name": map[string]any{"concat": []any{
			"report-", "{{workflow.input.never_supplied}}", ".pdf",
		}}}
		got, err := ResolveValue(input, outputs)
		if err != nil {
			t.Fatalf("ResolveValue: %v", err)
		}
		if name := got.(map[string]any)["name"]; name != "report-.pdf" {
			t.Errorf("name = %v, want report-.pdf", name)
		}
	})
}

func TestResolveValue_UnsuppliedNestedField_KeyIsOmitted(t *testing.T) {
	outputs := workflowInputOutputs(map[string]any{
		"address": map[string]any{"city": "Ottawa"},
	})
	input := map[string]any{
		"city": "{{workflow.input.address.city}}",
		"zip":  "{{workflow.input.address.zip}}",
	}

	got, err := ResolveValue(input, outputs)
	if err != nil {
		t.Fatalf("ResolveValue: %v", err)
	}
	m := got.(map[string]any)
	if m["city"] != "Ottawa" {
		t.Errorf("city = %v, want Ottawa", m["city"])
	}
	if v, exists := m["zip"]; exists {
		t.Errorf("zip is present with value %v; the field is absent from a supplied parent", v)
	}
}

func TestResolveValue_SuppliedInputTooShallowToNavigate_KeepsTheKey(t *testing.T) {
	outputs := workflowInputOutputs(map[string]any{"user": "12345"})

	got, err := ResolveValue(map[string]any{"userId": "{{workflow.input.user.id}}"}, outputs)
	if err != nil {
		t.Fatalf("ResolveValue: %v", err)
	}
	m := got.(map[string]any)
	v, exists := m["userId"]
	if !exists {
		t.Fatal("userId was dropped; `user` was supplied, just not as a map")
	}
	if v != nil {
		t.Errorf("userId = %v, want nil", v)
	}
}

func TestResolveOutputMapping_UnsuppliedWorkflowInput_KeepsTheKeyAsNull(t *testing.T) {
	outputs := workflowInputOutputs(map[string]any{"supplied": "yes"})
	mapping := map[string]any{
		"echo":     "{{workflow.input.supplied}}",
		"optional": "{{workflow.input.never_supplied}}",
		"guarded":  map[string]any{"coalesce": []any{"{{workflow.input.never_supplied}}"}},
	}

	for _, tc := range []struct {
		name    string
		resolve func() map[string]any
	}{
		{"strict", func() map[string]any {
			got, err := resolveOutputMapping(mapping, outputs, nil)
			if err != nil {
				t.Fatalf("resolveOutputMapping: %v", err)
			}
			return got.(map[string]any)
		}},
		{"lenient", func() map[string]any {
			return resolveOutputMappingLeavingUnresolved(mapping, outputs, nil).(map[string]any)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := tc.resolve()
			if m["echo"] != "yes" {
				t.Errorf("echo = %v, want yes", m["echo"])
			}
			for _, key := range []string{"optional", "guarded"} {
				v, exists := m[key]
				if !exists {
					t.Errorf("%s was dropped; a parent's {{<node>.output.%s}} would fail on the missing field", key, key)
					continue
				}
				if v != nil {
					t.Errorf("%s = %v, want nil", key, v)
				}
			}
		})
	}
}

type argRecordingTool struct {
	*schemaTool
	args     atomic.Pointer[map[string]any]
	executed atomic.Bool
}

func newArgRecordingTool() *argRecordingTool {
	return &argRecordingTool{schemaTool: &schemaTool{
		name:     "create_draft",
		authored: true,
		props: map[string]any{
			"to":     map[string]any{"type": "string"},
			"userId": map[string]any{"type": "string"},
			"format": map[string]any{"type": "string"},
		},
	}}
}

func (t *argRecordingTool) Execute(_ context.Context, args map[string]any, _ *tool.Context) (*tool.Result, error) {
	t.executed.Store(true)
	t.args.Store(&args)
	return tool.OK("ok"), nil
}

func TestDispatchToolNode_UnsuppliedWorkflowInput_NotSentToTheTool(t *testing.T) {
	node := &NodeConfig{ID: "create", Type: "tool", ToolName: "create_draft", Input: map[string]any{
		"to":     "{{workflow.input.to}}",
		"userId": "{{workflow.input.userId}}",
		"format": "{{workflow.input.format}}",
	}}
	nodes := []*NodeConfig{node}
	cfg := &WorkflowConfig{Name: "wf", Namespace: "ns", Nodes: nodes}
	dag, err := BuildDependencyGraph(nodes)
	if err != nil {
		t.Fatalf("dag: %v", err)
	}

	e := newTestDAGExecutor(cfg, dag, NewExecutionState("wf", "exec-1"))
	defer e.execCtx.Cancel()

	rec := newArgRecordingTool()
	ts := tool.NewSet()
	if err := ts.Register(rec); err != nil {
		t.Fatalf("register: %v", err)
	}
	e.instance.tools = ts
	e.state.NodeOutputs["workflow_input"] = map[string]any{"output": map[string]any{
		"to":     "someone@example.com",
		"format": nil,
	}}

	e.dispatchToolNode(context.Background(), node, "sub-1")
	e.instance.wg.Wait()

	if e.state.ErrorState != nil {
		t.Fatalf("node failed: %q", e.state.ErrorState.ErrorMessage)
	}
	if !rec.executed.Load() {
		t.Fatal("the tool was never invoked")
	}
	args := *rec.args.Load()
	if _, exists := args["userId"]; exists {
		t.Errorf("userId reached the tool as %#v; an unsupplied input must not be sent", args["userId"])
	}
	if args["to"] != "someone@example.com" {
		t.Errorf("to = %v, want someone@example.com", args["to"])
	}
	v, exists := args["format"]
	if !exists {
		t.Error("format did not reach the tool; the caller supplied it as null")
	} else if v != nil {
		t.Errorf("format = %v, want nil", v)
	}
}

func TestBuildRequestParts_UnsuppliedWorkflowInput_AbsentFromTheAgentRequest(t *testing.T) {
	node := &NodeConfig{ID: "draft", Type: "agent", AgentName: "agentA", Input: map[string]any{
		"to":     "{{workflow.input.to}}",
		"userId": "{{workflow.input.userId}}",
	}}
	nodes := []*NodeConfig{node}
	cfg := &WorkflowConfig{Name: "wf", Namespace: "ns", Nodes: nodes}
	dag, err := BuildDependencyGraph(nodes)
	if err != nil {
		t.Fatalf("dag: %v", err)
	}

	e := newTestDAGExecutor(cfg, dag, NewExecutionState("wf", "exec-1"))
	defer e.execCtx.Cancel()
	e.state.NodeOutputs["workflow_input"] = map[string]any{
		"output": map[string]any{"to": "someone@example.com"},
	}

	input, err := e.resolveNodeInput(node)
	if err != nil {
		t.Fatalf("resolveNodeInput: %v", err)
	}

	var inputJSON []byte
	for _, part := range e.buildRequestParts(node, node.AgentName, "art", input, "", e.state.NodeOutputs) {
		if part.Kind != "file" || part.File == nil {
			continue
		}
		decoded, decErr := base64.StdEncoding.DecodeString(part.File.Bytes)
		if decErr != nil {
			t.Fatalf("decode input part: %v", decErr)
		}
		inputJSON = decoded
	}
	if inputJSON == nil {
		t.Fatal("no input file part in the request")
	}
	if want := `{"to":"someone@example.com"}`; string(inputJSON) != want {
		t.Errorf("agent request input = %s, want %s", inputJSON, want)
	}
}

func TestBuildRequestParts_AllKeysDropped_StillShipsAnEmptyInputPart(t *testing.T) {
	for _, nodeType := range []string{"agent", "workflow"} {
		t.Run(nodeType, func(t *testing.T) {
			node := &NodeConfig{ID: "only", Type: nodeType, AgentName: "agentA", WorkflowName: "childWf",
				Instruction: "Run the sub-workflow.",
				Input:       map[string]any{"value": "{{workflow.input.never_supplied}}"}}
			nodes := []*NodeConfig{node}
			cfg := &WorkflowConfig{Name: "wf", Namespace: "ns", Nodes: nodes}
			dag, err := BuildDependencyGraph(nodes)
			if err != nil {
				t.Fatalf("dag: %v", err)
			}

			e := newTestDAGExecutor(cfg, dag, NewExecutionState("wf", "exec-1"))
			defer e.execCtx.Cancel()
			e.state.NodeOutputs["workflow_input"] = map[string]any{"output": map[string]any{}}

			input, err := e.resolveNodeInput(node)
			if err != nil {
				t.Fatalf("resolveNodeInput: %v", err)
			}
			if len(input) != 0 {
				t.Fatalf("input = %#v, want empty — the only key referenced an unsupplied input", input)
			}

			var kinds []string
			var inputJSON []byte
			for _, part := range e.buildRequestParts(node, node.AgentName, "art", input, "", e.state.NodeOutputs) {
				kinds = append(kinds, part.Kind)
				if part.Kind == "file" && part.File != nil {
					decoded, decErr := base64.StdEncoding.DecodeString(part.File.Bytes)
					if decErr != nil {
						t.Fatalf("decode input part: %v", decErr)
					}
					inputJSON = decoded
				}
			}
			if inputJSON == nil {
				t.Fatalf("no input file part in the request (parts: %v); the receiver has no fallback for its absence", kinds)
			}
			if string(inputJSON) != "{}" {
				t.Errorf("input part = %s, want {}", inputJSON)
			}
		})
	}
}

func TestOutputMappingNull_SurvivesAParentsStrictReference(t *testing.T) {
	childMapping := map[string]any{"optional": "{{workflow.input.never_supplied}}"}
	childOutput, err := resolveOutputMapping(childMapping, workflowInputOutputs(map[string]any{}), nil)
	if err != nil {
		t.Fatalf("child output_mapping: %v", err)
	}

	parentInput := map[string]any{"passthrough": "{{child.output.optional}}"}

	got, err := ResolveValue(parentInput, map[string]map[string]any{
		"child": {"output": childOutput},
	})
	if err != nil {
		t.Fatalf("parent reference to the child's response failed: %v", err)
	}
	v, exists := got.(map[string]any)["passthrough"]
	if !exists || v != nil {
		t.Errorf("passthrough = %#v (present=%t), want a nil value on a present key", v, exists)
	}

	_, err = ResolveValue(parentInput, map[string]map[string]any{
		"child": {"output": map[string]any{}},
	})
	if err == nil {
		t.Error("a parent reference to a dropped field did not fail; the output_mapping asymmetry is unnecessary if so")
	}
}

func TestResolveNodeInput_UnsuppliedWorkflowInput_AbsentFromTheJSON(t *testing.T) {
	node := &NodeConfig{ID: "create", Type: "tool", ToolName: "create_draft", Input: map[string]any{
		"to":     "{{workflow.input.to}}",
		"userId": "{{workflow.input.userId}}",
	}}
	nodes := []*NodeConfig{node}
	cfg := &WorkflowConfig{Name: "wf", Namespace: "ns", Nodes: nodes}
	dag, err := BuildDependencyGraph(nodes)
	if err != nil {
		t.Fatalf("dag: %v", err)
	}

	e := newTestDAGExecutor(cfg, dag, NewExecutionState("wf", "exec-1"))
	defer e.execCtx.Cancel()
	e.state.NodeOutputs["workflow_input"] = map[string]any{
		"output": map[string]any{"to": "someone@example.com"},
	}

	input, err := e.resolveNodeInput(node)
	if err != nil {
		t.Fatalf("resolveNodeInput: %v", err)
	}
	argsJSON, err := json.Marshal(input)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if want := `{"to":"someone@example.com"}`; string(argsJSON) != want {
		t.Errorf("args = %s, want %s", argsJSON, want)
	}
}

// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package codegen

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestExtractWorkflowSchema_TopLevelFields(t *testing.T) {
	wf, err := ExtractWorkflowSchema("../../workflow/config.go")
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	wantPresent := []string{"description", "version", "input_schema", "output_schema",
		"nodes", "output_mapping", "fail_fast", "max_call_depth", "retry_strategy"}
	got := map[string]bool{}
	for _, f := range wf.Fields {
		got[f.JSONName] = true
	}
	for _, w := range wantPresent {
		if !got[w] {
			t.Errorf("workflow top-level missing %q (have %d fields)", w, len(wf.Fields))
		}
	}
	if got["name"] || got["namespace"] {
		t.Errorf("workflow schema should NOT include name/namespace (those come from parent)")
	}
}

func TestExtractWorkflowSchema_NodeTypePartitioning(t *testing.T) {
	wf, err := ExtractWorkflowSchema("../../workflow/config.go")
	if err != nil {
		t.Fatalf("extract: %v", err)
	}

	for _, want := range []string{"agent", "switch", "loop", "map", "workflow"} {
		if _, ok := wf.NodeTypes[want]; !ok {
			t.Errorf("missing node type %q", want)
		}
	}

	agent := wf.NodeTypes["agent"]
	hasField := func(fields []FieldSchema, name string) bool {
		return slices.ContainsFunc(fields, func(f FieldSchema) bool { return f.JSONName == name })
	}
	for _, want := range []string{"id", "type", "agent_name", "input_schema_override", "output_schema_override"} {
		if !hasField(agent.Fields, want) {
			t.Errorf("agent node missing field %q", want)
		}
	}
	for _, banned := range []string{"cases", "default", "node", "items", "workflow_name"} {
		if hasField(agent.Fields, banned) {
			t.Errorf("agent node should not have %q (belongs to other type)", banned)
		}
	}

	sw := wf.NodeTypes["switch"]
	if !hasField(sw.Fields, "cases") {
		t.Errorf("switch missing cases")
	}
	if hasField(sw.Fields, "agent_name") {
		t.Errorf("switch should not carry agent_name")
	}

	loop := wf.NodeTypes["loop"]
	for _, want := range []string{"node", "condition", "max_iterations"} {
		if !hasField(loop.Fields, want) {
			t.Errorf("loop missing %q", want)
		}
	}

	mp := wf.NodeTypes["map"]
	for _, want := range []string{"items", "node", "max_items"} {
		if !hasField(mp.Fields, want) {
			t.Errorf("map missing %q", want)
		}
	}

	wn := wf.NodeTypes["workflow"]
	if !hasField(wn.Fields, "workflow_name") {
		t.Errorf("workflow node missing workflow_name")
	}
}

func TestExtractWorkflowSchema_DisplayTypeOverrideStaysOnItsNodeType(t *testing.T) {
	configFile := filepath.Join(t.TempDir(), "config.go")
	const source = `package workflow

type WorkflowConfig struct {
	Nodes []NodeConfig
}

type NodeConfig struct {
	ID string

	// Shared fields (agent, decision).
	Questions []string
}
`
	if err := os.WriteFile(configFile, []byte(source), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	wf, err := ExtractWorkflowSchema(configFile)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	for nodeType, want := range map[string]string{"decision": "map<string, object>", "agent": ""} {
		i := slices.IndexFunc(wf.NodeTypes[nodeType].Fields, func(f FieldSchema) bool { return f.JSONName == "questions" })
		if i < 0 {
			t.Fatalf("%s node missing questions", nodeType)
		}
		if got := wf.NodeTypes[nodeType].Fields[i].DisplayTypeOverride; got != want {
			t.Errorf("%s questions DisplayTypeOverride = %q, want %q", nodeType, got, want)
		}
	}
}

func TestExtractWorkflowSchema_DecisionNodeDocumentsTheAuthoredShape(t *testing.T) {
	wf, err := ExtractWorkflowSchema("../../workflow/config.go")
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	fields := map[string]FieldSchema{}
	for _, f := range wf.NodeTypes["decision"].Fields {
		fields[f.JSONName] = f
	}

	questions, ok := fields["questions"]
	if !ok {
		t.Fatal("decision node missing questions")
	}
	if got := questions.DisplayType(); got != "map<string, object>" {
		t.Errorf("questions displays as %q; the parser reads a map keyed by question name", got)
	}
	if questions.Description == "" {
		t.Error("questions has no description; the first field of a section must keep its trailing comment")
	}

	for _, want := range []string{"when", "timeout", "input"} {
		if _, ok := fields[want]; !ok {
			t.Errorf("decision node missing common field %q", want)
		}
	}
	for _, banned := range []string{"retry_strategy", "instruction"} {
		if _, ok := fields[banned]; ok {
			t.Errorf("decision node lists %q, which the parser rejects or ignores for decision nodes", banned)
		}
	}
	for _, nodeType := range []string{"agent", "workflow"} {
		has := func(name string) bool {
			return slices.ContainsFunc(wf.NodeTypes[nodeType].Fields, func(f FieldSchema) bool { return f.JSONName == name })
		}
		for _, want := range []string{"when", "timeout", "input", "retry_strategy", "instruction"} {
			if !has(want) {
				t.Errorf("%s node missing %q after the common-fields split", nodeType, want)
			}
		}
	}

	for _, f := range wf.Fields {
		if f.JSONName == "decision" && !strings.Contains(f.Description, "model_alias") {
			t.Errorf("top-level decision description does not mention model_alias: %q", f.Description)
		}
	}
}

func TestGoNameToYAML(t *testing.T) {
	cases := []struct{ in, want string }{
		{"AgentName", "agent_name"},
		{"DependsOn", "depends_on"},
		{"OutputSchemaOverride", "output_schema_override"},
		{"ID", "id"},
		{"MaxIterations", "max_iterations"},
		{"Type", "type"},
		{"FailFast", "fail_fast"},
	}
	for _, c := range cases {
		got := goNameToYAML(c.in)
		if got != c.want {
			t.Errorf("goNameToYAML(%q) = %q; want %q", c.in, got, c.want)
		}
	}
}

// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package builderapply

import (
	"reflect"
	"strings"
	"testing"
)

func workflowWithNodes(name string, nodes ...string) BuildComponentInput {
	return workflowShaped(name, "      workflow:\n", "        ", nodes)
}

func flatWorkflowWithNodes(name string, nodes ...string) BuildComponentInput {
	return workflowShaped(name, "", "      ", nodes)
}

func workflowShaped(name, opening, indent string, nodes []string) BuildComponentInput {
	var b strings.Builder
	b.WriteString("apps:\n  - name: " + name + "\n    app_config:\n      agent_name: " + name + "\n      agent_type: workflow\n")
	b.WriteString(opening)
	b.WriteString(indent + "output_mapping:\n" + indent + "  result: \"{{a.output}}\"\n")
	b.WriteString(indent + "agent_card:\n" + indent + "  description: \"A workflow exercised by the structure tests.\"\n")
	b.WriteString(indent + "nodes:\n")
	for _, n := range nodes {
		b.WriteString(indent + "  - " + n + "\n")
	}
	return BuildComponentInput{Kind: kindWorkflow, Name: name, Content: b.String()}
}

func node(id string, dependsOn ...string) string {
	n := "{id: " + id + ", type: agent, agent_name: test_agent"
	if len(dependsOn) > 0 {
		n += ", depends_on: [" + strings.Join(dependsOn, ", ") + "]"
	}
	return n + "}"
}

type structureCase struct {
	name        string
	nodes       []string
	wantDetails []string
}

func invalidStructureCases(workflowName string) []structureCase {
	named := func(problems ...string) []string {
		out := make([]string, len(problems))
		for i, p := range problems {
			out[i] = `workflow "` + workflowName + `": ` + p
		}
		return out
	}
	return []structureCase{
		{"a two-node cycle", []string{node("a", "b"), node("b", "a")}, named(`dependency cycle among nodes ["a", "b"]`)},
		{"a three-node cycle", []string{node("a", "c"), node("b", "a"), node("c", "b")}, named(`dependency cycle among nodes ["a", "b", "c"]`)},
		{"a cycle behind a valid root", []string{node("root"), node("a", "root", "b"), node("b", "a")}, named(`dependency cycle among nodes ["a", "b"]`)},
		{"a self-dependency", []string{node("a", "a")}, named(`node "a" depends on itself`)},
		{"duplicate ids", []string{node("a"), node("a")}, named(`node id "a" is defined 2 times; node ids must be unique`)},
		{
			"a loop body id defined twice",
			[]string{node("a"), "{id: again, type: loop, node: body, condition: \"1 == 2\", depends_on: [a]}", node("body"), node("body")},
			named(`node id "body" is defined 2 times; node ids must be unique`),
		},
		{
			"a map template id defined twice",
			[]string{"{id: fan, type: map, node: item, items: \"{{workflow.input.list}}\"}", node("item"), node("item")},
			named(`node id "item" is defined 2 times; node ids must be unique`),
		},
		{
			"every problem at once",
			[]string{node("a"), node("a"), node("s", "s"), node("x", "y"), node("y", "x")},
			named(`node id "a" is defined 2 times; node ids must be unique`, `node "s" depends on itself`, `dependency cycle among nodes ["x", "y"]`),
		},
	}
}

func requireStructureRejection(t *testing.T, op BuildOperationResult, tc structureCase, field string) {
	t.Helper()
	if op.Status != statusFailed || op.Error == nil {
		t.Fatalf("op = %+v, want a failed operation", op)
	}
	if op.Error.HTTPStatus != 422 {
		t.Errorf("httpStatus = %d, want 422", op.Error.HTTPStatus)
	}
	if got := op.Error.ValidationDetails[field]; !reflect.DeepEqual(got, tc.wantDetails) {
		t.Errorf("validationDetails[%q] = %q, want %q", field, got, tc.wantDetails)
	}
	for _, want := range tc.wantDetails {
		if !strings.Contains(op.Error.Message, want) {
			t.Errorf("message %q does not carry %q", op.Error.Message, want)
		}
	}
}

func TestStructure_DryRunAndApplyRejectWhatDeployWould(t *testing.T) {
	t.Parallel()
	shapes := map[string]func(string, ...string) BuildComponentInput{
		"nested": workflowWithNodes,
		"flat":   flatWorkflowWithNodes,
	}
	for shapeName, build := range shapes {
		field := "appConfig.workflow.nodes"
		if shapeName == "flat" {
			field = "appConfig.nodes"
		}
		for _, tc := range invalidStructureCases("BadFlow") {
			t.Run(shapeName+"/"+tc.name, func(t *testing.T) {
				t.Parallel()
				svc := newTestService(t)
				if _, err := svc.Apply(t.Context(), ApplyBuildRequest{Components: []BuildComponentInput{agentComponent("test_agent")}}, testActor); err != nil {
					t.Fatalf("seed agent: %v", err)
				}
				component := build("BadFlow", tc.nodes...)

				dry, applied := applyBoth(t, svc, component)

				dryOp := opFor(t, dry, kindWorkflow, "BadFlow")
				realOp := opFor(t, applied, kindWorkflow, "BadFlow")
				requireStructureRejection(t, dryOp, tc, field)
				requireStructureRejection(t, realOp, tc, field)
				requireSameFailure(t, dryOp, realOp, tc.wantDetails[0])
				if dry.Summary != applied.Summary || applied.Summary.Failed != 1 {
					t.Errorf("summary: dry run %+v, real apply %+v, want one failure each", dry.Summary, applied.Summary)
				}
				if list, err := svc.deps.listWorkflows(t.Context()); err != nil || len(list) != 0 {
					t.Errorf("stored workflows = %v (err %v), want none: an unloadable workflow must never be persisted", list, err)
				}
			})
		}
	}
}

func TestStructure_RejectedUpdateLeavesTheStoredWorkflowUntouched(t *testing.T) {
	t.Parallel()
	shapes := map[string]func(string, ...string) BuildComponentInput{
		"nested": workflowWithNodes,
		"flat":   flatWorkflowWithNodes,
	}
	for shapeName, build := range shapes {
		field := "appConfig.workflow.nodes"
		if shapeName == "flat" {
			field = "appConfig.nodes"
		}
		for _, tc := range invalidStructureCases("EditedFlow") {
			t.Run(shapeName+"/"+tc.name, func(t *testing.T) {
				t.Parallel()
				svc := newTestService(t)
				if _, err := svc.Apply(t.Context(), ApplyBuildRequest{Components: []BuildComponentInput{agentComponent("test_agent")}}, testActor); err != nil {
					t.Fatalf("seed agent: %v", err)
				}
				valid := build("EditedFlow", node("a"), node("b", "a"))
				if resp, err := svc.Apply(t.Context(), ApplyBuildRequest{Components: []BuildComponentInput{valid}}, testActor); err != nil || resp.Summary.Failed != 0 {
					t.Fatalf("seed workflow: %v %+v", err, resp)
				}

				before := platformSnapshot(t, svc)

				dry, applied := applyBoth(t, svc, build("EditedFlow", tc.nodes...))

				dryOp := opFor(t, dry, kindWorkflow, "EditedFlow")
				realOp := opFor(t, applied, kindWorkflow, "EditedFlow")
				if realOp.Action != actionUpdate {
					t.Fatalf("action = %q, want update", realOp.Action)
				}
				requireStructureRejection(t, dryOp, tc, field)
				requireStructureRejection(t, realOp, tc, field)
				requireSameFailure(t, dryOp, realOp, tc.wantDetails[0])
				if after := platformSnapshot(t, svc); after != before {
					t.Errorf("a rejected update changed the stored workflow:\nbefore %s\nafter  %s", before, after)
				}
			})
		}
	}
}

func TestStructure_LoadableWorkflowsAreUnaffected(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		nodes []string
	}{
		{"a single node", []string{node("a")}},
		{"a chain", []string{node("a"), node("b", "a"), node("c", "b")}},
		{"a diamond", []string{node("a"), node("b", "a"), node("c", "a"), node("d", "b", "c")}},
		{"two independent roots joining", []string{node("a"), node("b"), node("c", "a", "b")}},
		{"a loop and a map with distinct body ids", []string{
			node("a"),
			"{id: again, type: loop, node: body, condition: \"1 == 2\", depends_on: [a]}",
			node("body"),
			"{id: fan, type: map, node: item, items: \"{{workflow.input.list}}\", depends_on: [a]}",
			node("item"),
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc := newTestService(t)
			if _, err := svc.Apply(t.Context(), ApplyBuildRequest{Components: []BuildComponentInput{agentComponent("test_agent")}}, testActor); err != nil {
				t.Fatalf("seed agent: %v", err)
			}
			component := workflowWithNodes("GoodFlow", tc.nodes...)

			dry, err := svc.Apply(t.Context(), ApplyBuildRequest{DryRun: true, Components: []BuildComponentInput{component}}, testActor)
			if err != nil {
				t.Fatalf("dry run: %v", err)
			}
			if op := opFor(t, dry, kindWorkflow, "GoodFlow"); op.Status != statusPlanned || op.Error != nil {
				t.Fatalf("dry run = %+v, want planned", op)
			}
			applied, err := svc.Apply(t.Context(), ApplyBuildRequest{Components: []BuildComponentInput{component}}, testActor)
			if err != nil {
				t.Fatalf("apply: %v", err)
			}
			if op := opFor(t, applied, kindWorkflow, "GoodFlow"); op.Status != statusSucceeded {
				t.Fatalf("apply = %+v, want succeeded", op)
			}
			if list, _ := svc.deps.listWorkflows(t.Context()); len(list) != 1 {
				t.Errorf("stored workflows = %d, want 1", len(list))
			}
		})
	}
}

func TestStructure_ADependencyOnAMissingNodeStaysAWarning(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	if _, err := svc.Apply(t.Context(), ApplyBuildRequest{Components: []BuildComponentInput{agentComponent("test_agent")}}, testActor); err != nil {
		t.Fatalf("seed agent: %v", err)
	}
	component := workflowWithNodes("DanglingFlow", node("a", "ghost"))

	dry, err := svc.Apply(t.Context(), ApplyBuildRequest{DryRun: true, Components: []BuildComponentInput{component}}, testActor)
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	op := opFor(t, dry, kindWorkflow, "DanglingFlow")
	if op.Status != statusPlanned || len(op.Warnings) == 0 || !strings.Contains(strings.Join(op.Warnings, "\n"), `"ghost"`) {
		t.Fatalf("dry run = %+v, want planned with the dangling-reference warning", op)
	}
}

func TestStructure_DryRunReportsEveryRejectedWorkflowAndAValidOneStaysPlanned(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	if _, err := svc.Apply(t.Context(), ApplyBuildRequest{Components: []BuildComponentInput{agentComponent("test_agent")}}, testActor); err != nil {
		t.Fatalf("seed agent: %v", err)
	}
	components := []BuildComponentInput{
		workflowWithNodes("CycFlow", node("a", "b"), node("b", "a")),
		workflowWithNodes("SelfFlow", node("a", "a")),
		workflowWithNodes("DupFlow", node("a"), node("a")),
		workflowWithNodes("OkFlow", node("a"), node("b", "a")),
	}

	dry, applied := applyBoth(t, svc, components...)

	if dry.Summary.Failed != 3 {
		t.Fatalf("dry-run summary = %+v, want three rejected workflows", dry.Summary)
	}
	for name, want := range map[string]string{
		"CycFlow":  `workflow "CycFlow": dependency cycle among nodes ["a", "b"]`,
		"SelfFlow": `workflow "SelfFlow": node "a" depends on itself`,
		"DupFlow":  `workflow "DupFlow": node id "a" is defined 2 times; node ids must be unique`,
	} {
		op := opFor(t, dry, kindWorkflow, name)
		if op.Status != statusFailed || op.Error == nil || !strings.Contains(op.Error.Message, want) {
			t.Errorf("dry run %s = %+v, want a rejection carrying %q", name, op, want)
		}
	}
	if op := opFor(t, dry, kindWorkflow, "OkFlow"); op.Status != statusPlanned {
		t.Errorf("OkFlow = %q in the dry run, want planned", op.Status)
	}
	for _, name := range []string{"CycFlow", "SelfFlow", "DupFlow"} {
		if op := opFor(t, applied, kindWorkflow, name); op.Status != statusFailed {
			t.Errorf("%s = %q in the real apply, want failed: the pre-pass reports every rejection", name, op.Status)
		}
	}
	if op := opFor(t, applied, kindWorkflow, "OkFlow"); op.Status != statusSkipped {
		t.Errorf("OkFlow = %q in the real apply, want skipped: nothing is written once the build is refused", op.Status)
	}
	if list, _ := svc.deps.listWorkflows(t.Context()); len(list) != 0 {
		t.Errorf("stored workflows = %v, want none", list)
	}
}

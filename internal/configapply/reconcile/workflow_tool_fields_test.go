// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package reconcile

import (
	"errors"
	"fmt"
	"maps"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/resolve"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/translate"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/wire"
	"github.com/SolaceProducts/solace-agent-mesh/internal/workflow"
)

func toolNodeWorkflow(tools []any, nodes ...map[string]any) map[string]any {
	rawNodes := make([]any, 0, len(nodes))
	for _, n := range nodes {
		rawNodes = append(rawNodes, n)
	}
	producer := "fetch"
	if len(nodes) > 0 {
		if id, ok := nodes[0]["id"].(string); ok && id != "" {
			producer = id
		}
	}
	spec := map[string]any{
		"version":        "1.0.0",
		"nodes":          rawNodes,
		"output_mapping": map[string]any{"result": fmt.Sprintf("{{%s.output.filename}}", producer)},
	}
	if tools != nil {
		spec["tools"] = tools
	}
	return spec
}

func builtinWebRequest() []any {
	return []any{map[string]any{"tool_type": "builtin", "tool_name": "web_request"}}
}

func fetchNode(input map[string]any) map[string]any {
	return map[string]any{"id": "fetch", "type": "tool", "tool_name": "web_request", "input": input}
}

func planOne(t *testing.T, name string, spec map[string]any) (*workflowKindPlan, error) {
	t.Helper()
	return planWorkflows([]resolve.ResolvedResource{resolvedWorkflow(name, spec)}, nil, translate.WorkflowHooks{})
}

func escalationChoiceTriage() map[string]any {
	return map[string]any{
		"id":   "triage",
		"type": "decision",
		"questions": map[string]any{"department": map[string]any{
			"type": "choice", "instructions": "Which team owns it?", "criteria": map[string]any{"billing": "Payments", "tech": "Bugs"},
		}},
		"route_on":          "department",
		"routes":            map[string]any{"billing": "billing", "tech": "tech"},
		"min_confidence":    0.8,
		"escalate":          "reclassify",
		"escalation_choice": "{{reclassify.output.department}}",
	}
}

func TestPlanWorkflows_RejectsTheRemovedEscalationChoice(t *testing.T) {
	_, err := planOne(t, "triage-wf", toolNodeWorkflow(nil, escalationChoiceTriage()))
	if !errors.Is(err, workflow.ErrEscalationChoiceRemoved) {
		t.Fatalf("plan err = %v, want the removed escalation_choice key to stop the plan", err)
	}
}

func TestPlanWorkflows_OnlyWarnsAboutTheRemovedEscalationChoiceOnAnUnchangedWorkflow(t *testing.T) {
	spec := toolNodeWorkflow(nil, escalationChoiceTriage())
	plan, err := planWorkflows([]resolve.ResolvedResource{resolvedWorkflow("triage-wf", spec)},
		[]wire.WorkflowResponse{actualWorkflow("id-1", "triage-wf", spec)}, translate.WorkflowHooks{})
	if err != nil {
		t.Fatalf("an unchanged workflow must not fail the plan: %v", err)
	}
	if len(plan.Warnings) != 1 || !strings.Contains(plan.Warnings[0], "escalation_choice was replaced by escalation_mode") ||
		!strings.Contains(plan.Warnings[0], "not fatal") {
		t.Fatalf("warnings = %v, want one non-fatal advisory naming the removed key", plan.Warnings)
	}
}

func TestPlanWorkflows_RejectsUndeclaredToolArgument(t *testing.T) {
	_, err := planOne(t, "annotate", toolNodeWorkflow(builtinWebRequest(),
		fetchNode(map[string]any{
			"url":             "https://example.com/frame.jpg",
			"output_filename": "frame.jpg",
		})))
	if err == nil {
		t.Fatal("expected an undeclared tool argument to fail plan, got nil")
	}
	for _, want := range []string{
		"annotate", "workflows/annotate.yaml", "fetch", "output_filename",
		"web_request", "output_artifact_filename", "url",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error missing %q; got: %s", want, err.Error())
		}
	}
}

func TestPlanWorkflows_AdvisesUnproducibleOutputReference(t *testing.T) {
	plan, err := planOne(t, "annotate", toolNodeWorkflow(builtinWebRequest(),
		fetchNode(map[string]any{"url": "https://example.com/frame.jpg"}),
		map[string]any{
			"id":         "describe",
			"type":       "agent",
			"agent_name": "vision",
			"depends_on": []any{"fetch"},
			"input": map[string]any{
				"image": "«artifact_content:{{fetch.output.output_filename}} >>> format:datauri»",
			},
		}))
	if err != nil {
		t.Fatalf("an unproducible output reference must not fail plan: %v", err)
	}
	if len(plan.Warnings) != 1 {
		t.Fatalf("expected exactly one advisory, got %v", plan.Warnings)
	}
	for _, want := range []string{"annotate", "describe", "output_filename", "filename"} {
		if !strings.Contains(plan.Warnings[0], want) {
			t.Errorf("advisory missing %q; got: %s", want, plan.Warnings[0])
		}
	}
}

func TestPlanWorkflows_AcceptsCorrectToolFields(t *testing.T) {
	plan, err := planOne(t, "annotate", toolNodeWorkflow(builtinWebRequest(),
		fetchNode(map[string]any{
			"url":                      "https://example.com/frame.jpg",
			"output_artifact_filename": "frame.jpg",
		}),
		map[string]any{
			"id":         "describe",
			"type":       "agent",
			"agent_name": "vision",
			"depends_on": []any{"fetch"},
			"input": map[string]any{
				"image": "«artifact_content:{{fetch.output.filename}} >>> format:datauri»",
			},
		}))
	if err != nil {
		t.Fatalf("correctly authored workflow failed plan: %v", err)
	}
	if len(plan.Creates) != 1 {
		t.Fatalf("expected one Create, got %+v", plan.Creates)
	}
	if len(plan.Warnings) != 0 {
		t.Errorf("expected no advisories, got %v", plan.Warnings)
	}
}

func TestPlanWorkflows_UnknownToolNameIsSkipped(t *testing.T) {
	cases := map[string]struct {
		source       map[string]any
		siblingNodes []map[string]any
	}{
		"my_toolset__do_thing": {source: map[string]any{"toolsets": []any{"my_toolset"}}},
		"send_slack_message":   {siblingNodes: []map[string]any{{"id": "notify", "type": "tool", "connector": "slack"}}},
		"jira_create_issue": {source: map[string]any{
			"tools": []any{map[string]any{"tool_type": "mcp", "tool_name": "jira_create_issue"}},
		}},
	}
	for toolName, tc := range cases {
		t.Run(toolName, func(t *testing.T) {
			nodes := append([]map[string]any{{
				"id":        "act",
				"type":      "tool",
				"tool_name": toolName,
				"input":     map[string]any{"anything_at_all": "x", "and_another": 1},
			}}, tc.siblingNodes...)
			spec := toolNodeWorkflow(nil, nodes...)
			maps.Copy(spec, tc.source)
			plan, err := planOne(t, "opaque", spec)
			if err != nil {
				t.Fatalf("unknown tool name must not fail plan: %v", err)
			}
			if len(plan.Warnings) != 0 {
				t.Errorf("unknown tool name should be silent, not advisory: %v", plan.Warnings)
			}
		})
	}
}

func TestPlanWorkflows_ShadowableNameOnlyWarns(t *testing.T) {
	shadowing := map[string][]any{
		"mcp entry": {
			map[string]any{"tool_type": "mcp", "tool_name": "web_request", "server_url": "http://mcp.example"},
		},
		"openapi entry": {
			map[string]any{"tool_type": "openapi", "spec_url": "http://api.example/openapi.json"},
		},
	}
	for name, tools := range shadowing {
		t.Run(name, func(t *testing.T) {
			plan, err := planOne(t, "shadowed", toolNodeWorkflow(tools,
				fetchNode(map[string]any{"url": "https://example.com/x", "output_filename": "x.jpg"})))
			if err != nil {
				t.Fatalf("a shadowable name must not fail plan: %v", err)
			}
			if len(plan.Warnings) != 1 {
				t.Fatalf("expected exactly one advisory, got %v", plan.Warnings)
			}
			for _, want := range []string{"shadowed", "fetch", "output_filename"} {
				if !strings.Contains(plan.Warnings[0], want) {
					t.Errorf("advisory missing %q; got: %s", want, plan.Warnings[0])
				}
			}
		})
	}
}

func TestPlanWorkflows_ConnectorsAreOpaqueButToolsetsAreNot(t *testing.T) {
	badNode := func() map[string]any {
		return fetchNode(map[string]any{"url": "https://example.com/x", "output_filename": "x.jpg"})
	}

	t.Run("a connectors declaration only warns", func(t *testing.T) {
		spec := toolNodeWorkflow(nil, badNode())
		spec["connectors"] = []any{map[string]any{"connector": "crm", "name": "crm"}}
		plan, err := planOne(t, "expanded", spec)
		if err != nil {
			t.Fatalf("a connector declaration must not fail plan: %v", err)
		}
		if len(plan.Warnings) != 1 {
			t.Fatalf("expected exactly one advisory, got %v", plan.Warnings)
		}
		for _, want := range []string{"expanded", "fetch", "output_filename"} {
			if !strings.Contains(plan.Warnings[0], want) {
				t.Errorf("advisory missing %q; got: %s", want, plan.Warnings[0])
			}
		}
	})

	t.Run("a toolsets declaration does not demote", func(t *testing.T) {
		spec := toolNodeWorkflow(nil, badNode())
		spec["toolsets"] = []any{"artifact_management"}
		_, err := planOne(t, "expanded", spec)
		if err == nil {
			t.Fatal("a toolsets declaration cannot shadow a built-in, so it must not demote the fatal")
		}
		for _, want := range []string{"expanded", "fetch", "output_filename"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error missing %q; got: %s", want, err.Error())
			}
		}
	})

	t.Run("the two equivalent toolset spellings agree", func(t *testing.T) {
		viaToolsets := toolNodeWorkflow(nil, badNode())
		viaToolsets["toolsets"] = []any{"artifact_management"}
		_, errToolsets := planOne(t, "a", viaToolsets)

		viaGroup := toolNodeWorkflow(
			[]any{map[string]any{"tool_type": "builtin-group", "group_name": "artifact_management"}},
			badNode())
		_, errGroup := planOne(t, "b", viaGroup)

		if (errToolsets == nil) != (errGroup == nil) {
			t.Errorf("equivalent spellings graded differently: toolsets err = %v, builtin-group err = %v",
				errToolsets, errGroup)
		}
	})
}

func TestPlanWorkflows_OperatorInputIsNotAnArgument(t *testing.T) {
	for _, op := range []string{"coalesce", "concat"} {
		t.Run(op, func(t *testing.T) {
			if _, err := planOne(t, "op", toolNodeWorkflow(builtinWebRequest(),
				fetchNode(map[string]any{op: []any{"https://example.com/a", "https://example.com/b"}}),
			)); err != nil {
				t.Fatalf("%s operator input must not be read as an argument name: %v", op, err)
			}
		})
	}
}

func TestPlanWorkflows_NoInputIsSkipped(t *testing.T) {
	if _, err := planOne(t, "implicit", toolNodeWorkflow(builtinWebRequest(),
		map[string]any{"id": "fetch", "type": "tool", "tool_name": "web_request"},
	)); err != nil {
		t.Fatalf("a node with no input: must not fail plan: %v", err)
	}
}

func TestPlanWorkflows_WrappedEnvelopeValidated(t *testing.T) {
	inner := toolNodeWorkflow(builtinWebRequest(),
		fetchNode(map[string]any{"url": "https://example.com/x", "output_filename": "x.jpg"}))
	_, err := planOne(t, "wrapped", map[string]any{"appConfig": inner})
	if err == nil {
		t.Fatal("expected the wrapped body shape to be validated too, got nil")
	}
	if !strings.Contains(err.Error(), "output_filename") {
		t.Errorf("error missing the rejected argument; got: %s", err.Error())
	}
}

func TestPlanWorkflows_NestedWorkflowBodyMergesSiblingTools(t *testing.T) {
	body := map[string]any{
		"workflow": map[string]any{
			"version": "1.0.0",
			"nodes": []any{map[string]any{
				"id": "fetch", "type": "tool", "tool_name": "web_request",
				"input": map[string]any{"url": "https://example.com", "output_filename": "x.jpg"},
			}},
			"output_mapping": map[string]any{"result": "{{fetch.output.filename}}"},
		},
		"connectors": []any{map[string]any{"connector": "crm"}},
	}

	plan, err := planOne(t, "nested", body)
	if err != nil {
		t.Fatalf("a connector declared beside `workflow:` must demote to advisory, not fail: %v", err)
	}
	if len(plan.Warnings) != 1 {
		t.Fatalf("expected exactly one advisory, got %v", plan.Warnings)
	}
	if !strings.Contains(plan.Warnings[0], "output_filename") {
		t.Errorf("advisory missing the rejected argument; got: %s", plan.Warnings[0])
	}
}

func TestPlanWorkflows_PermissiveFlagReachesPlanTime(t *testing.T) {
	workflowWithBadArg := func() map[string]any {
		return toolNodeWorkflow(builtinWebRequest(),
			fetchNode(map[string]any{"url": "https://example.com", "output_filename": "x.jpg"}))
	}

	if _, err := planOne(t, "strict", workflowWithBadArg()); err == nil {
		t.Fatal("expected a fatal without the flag, got nil")
	}

	t.Setenv("SAM_FEATURE_WORKFLOW_TOOL_VALIDATION_PERMISSIVE", "true")
	plan, err := planOne(t, "permissive", workflowWithBadArg())
	if err != nil {
		t.Fatalf("the flag must downgrade the fatal to an advisory: %v", err)
	}
	if len(plan.Warnings) != 1 {
		t.Errorf("expected the problem to survive as an advisory, got %v", plan.Warnings)
	}
}

func TestPlanWorkflows_AgentUpstreamReferenceSkipped(t *testing.T) {
	consumer := fetchNode(map[string]any{"url": "{{think.output.some_invented_field}}"})
	consumer["depends_on"] = []any{"think"}
	if _, err := planOne(t, "agent-upstream", toolNodeWorkflow(builtinWebRequest(),
		map[string]any{"id": "think", "type": "agent", "agent_name": "planner"},
		consumer,
	)); err != nil {
		t.Fatalf("a reference into an agent node must not fail plan: %v", err)
	}
}

func TestPlanWorkflows_OutputSchemaOverrideSkipped(t *testing.T) {
	upstream := fetchNode(map[string]any{"url": "https://example.com/x"})
	upstream["output_schema_override"] = map[string]any{
		"type":       "object",
		"properties": map[string]any{"custom_field": map[string]any{"type": "string"}},
	}
	if _, err := planOne(t, "override", toolNodeWorkflow(builtinWebRequest(),
		upstream,
		map[string]any{
			"id":         "use",
			"type":       "agent",
			"agent_name": "vision",
			"depends_on": []any{"fetch"},
			"input":      map[string]any{"x": "{{fetch.output.custom_field}}"},
		},
	)); err != nil {
		t.Fatalf("an explicit output_schema must suppress the reference check: %v", err)
	}
}

func TestPlanWorkflows_UnparseableBodyDoesNotFailPlan(t *testing.T) {
	spec := map[string]any{
		"version": "1.0.0",
		"nodes": []any{map[string]any{
			"id": "fetch", "type": "tool",
		}},
		"output_mapping": map[string]any{"result": "{{fetch.output.filename}}"},
	}
	plan, err := planOne(t, "malformed", spec)
	if err != nil {
		t.Fatalf("an unparseable body must not fail plan from the tool-field check: %v", err)
	}
	if len(plan.Warnings) != 1 {
		t.Fatalf("expected the skip to be reported, got %v", plan.Warnings)
	}
	for _, want := range []string{"malformed", "skipped", "did not parse"} {
		if !strings.Contains(plan.Warnings[0], want) {
			t.Errorf("skip advisory missing %q; got: %s", want, plan.Warnings[0])
		}
	}
}

func probeWorkflow(fetchInput map[string]any, detectImageRef string) map[string]any {
	return map[string]any{
		"version":  "1.0.0",
		"toolsets": []any{"image-tools"},
		"tools": []any{
			map[string]any{"tool_type": "builtin", "tool_name": "web_request"},
			map[string]any{"tool_type": "mcp", "tool_name": "detect_objects"},
		},
		"nodes": []any{
			map[string]any{"id": "fetch", "type": "tool", "tool_name": "web_request", "input": fetchInput},
			map[string]any{
				"id": "detect", "type": "tool", "tool_name": "detect_objects",
				"depends_on": []any{"fetch"},
				"input": map[string]any{
					"image":   detectImageRef,
					"classes": []any{"car", "truck", "bus"},
				},
			},
			map[string]any{
				"id": "draw", "type": "tool", "tool_name": "image-tools__draw_boxes",
				"depends_on": []any{"fetch", "detect"},
				"input": map[string]any{
					"image":           "{{fetch.output.filename}}",
					"boxes":           "{{detect.output.extracted_fields.boxes}}",
					"output_filename": "annotated.png",
				},
			},
		},
		"output_mapping": map[string]any{"annotated_image": "{{draw.output.output_filename}}"},
	}
}

func TestPlanWorkflows_ProbeWorkflowCorrectedFormPasses(t *testing.T) {
	plan, err := planOne(t, "detect-and-annotate", probeWorkflow(
		map[string]any{
			"url":                      "{{workflow.input.image_url}}",
			"output_artifact_filename": "webcam_frame.jpg",
		},
		"«artifact_content:{{fetch.output.filename}} >>> format:datauri»",
	))
	if err != nil {
		t.Fatalf("the corrected probe workflow must plan cleanly: %v", err)
	}
	if len(plan.Warnings) != 0 {
		t.Errorf("expected no advisories, got %v", plan.Warnings)
	}
}

func TestPlanWorkflows_ProbeWorkflowOriginalMistakesReported(t *testing.T) {
	tests := map[string]struct {
		fetchInput     map[string]any
		detectImageRef string
		want           string
		wantFatal      bool
	}{
		"result field name used as an argument": {
			fetchInput:     map[string]any{"url": "http://x", "filename": "webcam_frame.jpg"},
			detectImageRef: "«artifact_content:{{fetch.output.filename}} >>> format:datauri»",
			want:           "filename",
			wantFatal:      true,
		},
		"another tool's result field name in a reference": {
			fetchInput:     map[string]any{"url": "http://x", "output_artifact_filename": "f.jpg"},
			detectImageRef: "«artifact_content:{{fetch.output.output_filename}} >>> format:datauri»",
			want:           "output_filename",
		},
		"input parameter name reused in a reference": {
			fetchInput:     map[string]any{"url": "http://x", "output_artifact_filename": "f.jpg"},
			detectImageRef: "«artifact_content:{{fetch.output.output_artifact_filename}} >>> format:datauri»",
			want:           "output_artifact_filename",
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			plan, err := planOne(t, "detect-and-annotate", probeWorkflow(tc.fetchInput, tc.detectImageRef))
			if tc.wantFatal {
				if err == nil {
					t.Fatal("expected plan to fail, got nil")
				}
				if !strings.Contains(err.Error(), tc.want) {
					t.Errorf("error missing %q; got: %s", tc.want, err.Error())
				}
				return
			}
			if err != nil {
				t.Fatalf("a bad reference must not fail plan: %v", err)
			}
			if len(plan.Warnings) == 0 {
				t.Fatal("expected an advisory, got none")
			}
			if !strings.Contains(strings.Join(plan.Warnings, "; "), tc.want) {
				t.Errorf("advisories missing %q; got: %v", tc.want, plan.Warnings)
			}
		})
	}
}

func TestPlanWorkflows_ReportsEveryOffendingNode(t *testing.T) {
	_, err := planOne(t, "multi", toolNodeWorkflow(builtinWebRequest(),
		map[string]any{
			"id": "fetch_a", "type": "tool", "tool_name": "web_request",
			"input": map[string]any{"url": "https://example.com/a", "bad_one": "x"},
		},
		map[string]any{
			"id": "fetch_b", "type": "tool", "tool_name": "web_request",
			"input": map[string]any{"url": "https://example.com/b", "bad_two": "y"},
		}))
	if err == nil {
		t.Fatal("expected failure, got nil")
	}
	for _, want := range []string{"fetch_a", "bad_one", "fetch_b", "bad_two"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error missing %q; got: %s", want, err.Error())
		}
	}
}

func TestPlanWorkflows_ChecksAWorkflowTheDiffLeavesAlone(t *testing.T) {
	t.Run("an undeclared argument is reported but not fatal", func(t *testing.T) {
		spec := toolNodeWorkflow(builtinWebRequest(),
			fetchNode(map[string]any{
				"url":             "https://example.com/frame.jpg",
				"output_filename": "frame.jpg",
			}))
		desired := []resolve.ResolvedResource{resolvedWorkflow("annotate", spec)}
		actual := []wire.WorkflowResponse{actualWorkflow("id-1", "annotate", spec)}

		plan, err := planWorkflows(desired, actual, translate.WorkflowHooks{})
		if err != nil {
			t.Fatalf("an unchanged workflow must not abort the manifest: %v", err)
		}
		if len(plan.Unchanged) != 1 {
			t.Fatalf("expected the workflow to plan as unchanged, got %+v", plan)
		}
		if len(plan.Warnings) != 1 {
			t.Fatalf("expected exactly one advisory, got %v", plan.Warnings)
		}
		for _, want := range []string{"annotate", "fetch", "output_filename", "not changing this workflow"} {
			if !strings.Contains(plan.Warnings[0], want) {
				t.Errorf("advisory missing %q; got: %s", want, plan.Warnings[0])
			}
		}
	})

	t.Run("an unproducible reference still advises", func(t *testing.T) {
		spec := toolNodeWorkflow(builtinWebRequest(),
			fetchNode(map[string]any{"url": "https://example.com/frame.jpg"}),
			map[string]any{
				"id":         "describe",
				"type":       "agent",
				"agent_name": "vision",
				"depends_on": []any{"fetch"},
				"input": map[string]any{
					"image": "«artifact_content:{{fetch.output.output_filename}} >>> format:datauri»",
				},
			})
		desired := []resolve.ResolvedResource{resolvedWorkflow("annotate", spec)}
		actual := []wire.WorkflowResponse{actualWorkflow("id-1", "annotate", spec)}

		plan, err := planWorkflows(desired, actual, translate.WorkflowHooks{})
		if err != nil {
			t.Fatalf("an unproducible output reference must not fail plan: %v", err)
		}
		if len(plan.Unchanged) != 1 {
			t.Fatalf("expected the workflow to plan as unchanged, got %+v", plan)
		}
		if len(plan.Warnings) != 1 {
			t.Fatalf("expected exactly one advisory, got %v", plan.Warnings)
		}
		for _, want := range []string{"annotate", "describe", "output_filename"} {
			if !strings.Contains(plan.Warnings[0], want) {
				t.Errorf("advisory missing %q; got: %s", want, plan.Warnings[0])
			}
		}
	})
}

func TestPlanWorkflows_ConnectorNodeDoesNotSilenceTheWorkflow(t *testing.T) {
	connectorOnly := map[string]any{
		"id": "push", "type": "tool", "connector": "traffic-db",
		"depends_on": []any{"fetch"},
	}
	bad := fetchNode(map[string]any{
		"url":             "https://example.com/frame.jpg",
		"output_filename": "frame.jpg",
	})

	_, err := planOne(t, "annotate", toolNodeWorkflow(builtinWebRequest(), bad, connectorOnly))
	if err == nil {
		t.Fatal("a connector-bound node must not suppress the check on a sibling node")
	}
	for _, want := range []string{"fetch", "output_filename"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error missing %q; got: %s", want, err.Error())
		}
	}
	if strings.Contains(err.Error(), "push") {
		t.Errorf("connector-bound node was graded; got: %s", err.Error())
	}
}

func TestPlanWorkflows_ConnectorNodeArgumentsAreNotGraded(t *testing.T) {
	plan, err := planOne(t, "pusher", toolNodeWorkflow(builtinWebRequest(),
		fetchNode(map[string]any{"url": "https://example.com/frame.jpg"}),
		map[string]any{
			"id": "push", "type": "tool", "connector": "traffic-db",
			"depends_on": []any{"fetch"},
			"input":      map[string]any{"anything_at_all": "x"},
		}))
	if err != nil {
		t.Fatalf("a connector node's arguments must not fail plan: %v", err)
	}
	if len(plan.Warnings) != 0 {
		t.Errorf("expected no advisory for a connector-bound node, got %v", plan.Warnings)
	}
}

func TestPlanWorkflows_MCPConnectorToolSelectorPlans(t *testing.T) {
	plan, err := planOne(t, "crm-lookup", toolNodeWorkflow(builtinWebRequest(),
		fetchNode(map[string]any{"url": "https://example.com/frame.jpg"}),
		map[string]any{
			"id": "find", "type": "tool", "connector": "acme-crm", "tool": "find_customer",
			"depends_on": []any{"fetch"},
			"input":      map[string]any{"company": "Globex"},
		}))
	if err != nil {
		t.Fatalf("a connector-plus-tool node must not fail plan: %v", err)
	}
	if len(plan.Warnings) != 0 {
		t.Errorf("expected no advisory for a connector-bound node, got %v", plan.Warnings)
	}
}

func TestPlanWorkflows_FatalOnlyWhenTheWorkflowIsMoving(t *testing.T) {
	badSpec := func() map[string]any {
		return toolNodeWorkflow(builtinWebRequest(),
			fetchNode(map[string]any{
				"url":             "https://example.com/frame.jpg",
				"output_filename": "frame.jpg",
			}))
	}
	desired := []resolve.ResolvedResource{resolvedWorkflow("annotate", badSpec())}

	t.Run("create is fatal", func(t *testing.T) {
		if _, err := planWorkflows(desired, nil, translate.WorkflowHooks{}); err == nil {
			t.Fatal("a new workflow carrying the mistake must fail the plan")
		}
	})

	t.Run("update is fatal", func(t *testing.T) {
		prior := actualWorkflow("id-1", "annotate", badSpec())
		prior.Description = "drifted server-side"
		_, err := planWorkflows(desired, []wire.WorkflowResponse{prior}, translate.WorkflowHooks{})
		if err == nil {
			t.Fatal("a workflow being updated must still fail the plan")
		}
	})

	t.Run("unchanged only warns", func(t *testing.T) {
		plan, err := planWorkflows(desired, []wire.WorkflowResponse{
			actualWorkflow("id-1", "annotate", badSpec()),
		}, translate.WorkflowHooks{})
		if err != nil {
			t.Fatalf("an unchanged workflow must not fail the plan: %v", err)
		}
		if len(plan.Warnings) != 1 {
			t.Fatalf("expected exactly one advisory, got %v", plan.Warnings)
		}
	})

	t.Run("one stale workflow does not block a sibling create", func(t *testing.T) {
		stale := resolvedWorkflow("annotate", badSpec())
		clean := resolvedWorkflow("fresh", toolNodeWorkflow(builtinWebRequest(),
			fetchNode(map[string]any{"url": "https://example.com/ok.jpg"})))
		plan, err := planWorkflows(
			[]resolve.ResolvedResource{stale, clean},
			[]wire.WorkflowResponse{actualWorkflow("id-1", "annotate", badSpec())},
			translate.WorkflowHooks{})
		if err != nil {
			t.Fatalf("a stale sibling must not block the create: %v", err)
		}
		if len(plan.Creates) != 1 || plan.Creates[0].Name != "fresh" {
			t.Fatalf("expected the clean workflow to still plan as a create, got %+v", plan)
		}
	})
}

func TestPlanWorkflows_RefusesToolNodeWithoutToolSource(t *testing.T) {
	undeclared := func() map[string]any {
		return toolNodeWorkflow(nil, map[string]any{"id": "fetch", "type": "tool", "tool_name": "web_request"})
	}
	desired := []resolve.ResolvedResource{resolvedWorkflow("stranded", undeclared())}

	t.Run("create is fatal", func(t *testing.T) {
		_, err := planWorkflows(desired, nil, translate.WorkflowHooks{})
		if err == nil || !strings.Contains(err.Error(), `tool node "fetch"`) {
			t.Fatalf("a tool node with no declared source fails every run; the plan must refuse it, got %v", err)
		}
	})

	t.Run("the permissive flag does not demote it", func(t *testing.T) {
		t.Setenv("SAM_FEATURE_WORKFLOW_TOOL_VALIDATION_PERMISSIVE", "true")
		_, err := planOne(t, "stranded", toolNodeWorkflow(nil,
			fetchNode(map[string]any{"url": "https://example.com/x", "output_filename": "x.jpg"})))
		if err == nil {
			t.Fatal("the flag relaxes argument checking only; a missing tool set fails the node regardless")
		}
		if !strings.Contains(err.Error(), `tool node "fetch"`) {
			t.Errorf("error does not name the stranded node; got: %s", err)
		}
		if strings.Contains(err.Error(), "output_filename") {
			t.Errorf("the flag must demote the argument problem to an advisory, yet the error still names it: %s", err)
		}
	})

	t.Run("unchanged only warns", func(t *testing.T) {
		plan, err := planWorkflows(desired, []wire.WorkflowResponse{
			actualWorkflow("id-1", "stranded", undeclared()),
		}, translate.WorkflowHooks{})
		if err != nil {
			t.Fatalf("an unchanged workflow must not fail the plan: %v", err)
		}
		if len(plan.Warnings) != 1 || !strings.Contains(plan.Warnings[0], "not changing this workflow") {
			t.Fatalf("expected one demoted advisory, got %v", plan.Warnings)
		}
		if !strings.Contains(plan.Warnings[0], `tool node "fetch"`) {
			t.Errorf("the advisory does not name the stranded node; got: %s", plan.Warnings[0])
		}
	})
}

func TestPlanWorkflows_ReportsSourceAndArgumentFatalsTogether(t *testing.T) {
	_, err := planOne(t, "stranded", toolNodeWorkflow(nil,
		map[string]any{"id": "clock", "type": "tool", "tool_name": "get_current_time"},
		fetchNode(map[string]any{
			"url":             "https://example.com/frame.jpg",
			"output_filename": "frame.jpg",
		})))
	if err == nil {
		t.Fatal("expected the plan to fail, got nil")
	}
	for _, want := range []string{
		`tool node "clock"`, `tool node "fetch"`, "output_filename",
		"SAM_FEATURE_WORKFLOW_TOOL_VALIDATION_PERMISSIVE",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error missing %q; got: %s", want, err.Error())
		}
	}
}

func TestPlanWorkflows_UnparseableBodyStillRefusesMissingToolSource(t *testing.T) {
	spec := map[string]any{
		"version": "1.0.0",
		"nodes": []any{
			map[string]any{"id": "fetch", "type": "tool", "tool_name": "web_request"},
			map[string]any{"id": "broken", "type": "tool", "depends_on": []any{"fetch"}},
		},
		"output_mapping": map[string]any{"result": "{{fetch.output.filename}}"},
	}
	_, err := planOne(t, "stranded", spec)
	if err == nil || !strings.Contains(err.Error(), `tool node "fetch"`) {
		t.Fatalf("a missing tool source must fail the plan even when the body does not parse, got %v", err)
	}
}

func TestPlanWorkflows_RefusesRestatedPlatformConnectorEntry(t *testing.T) {
	restated := func() map[string]any {
		spec := toolNodeWorkflow(nil, map[string]any{
			"id": "query", "type": "tool", "connector": "warehouse",
			"input": map[string]any{"query": "SELECT 1"},
		})
		spec["connectors"] = []any{map[string]any{"name": "warehouse", "type": "sql", "subtype": "mysql"}}
		return spec
	}
	desired := []resolve.ResolvedResource{resolvedWorkflow("restated", restated())}

	t.Run("create is fatal", func(t *testing.T) {
		_, err := planWorkflows(desired, nil, translate.WorkflowHooks{})
		if err == nil {
			t.Fatal("plan accepted a restated platform connector that save refuses")
		}
		for _, want := range []string{"restated", `"warehouse"`, `tool node "query"`} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error missing %q; got: %s", want, err.Error())
			}
		}
	})

	t.Run("unchanged only warns", func(t *testing.T) {
		plan, err := planWorkflows(desired, []wire.WorkflowResponse{
			actualWorkflow("id-1", "restated", restated()),
		}, translate.WorkflowHooks{})
		if err != nil {
			t.Fatalf("an unchanged workflow must not fail the plan: %v", err)
		}
		found := false
		for _, w := range plan.Warnings {
			if strings.Contains(w, `"warehouse"`) {
				found = true
			}
		}
		if !found {
			t.Fatalf("expected an advisory naming the restated entry, got %v", plan.Warnings)
		}
	})
}

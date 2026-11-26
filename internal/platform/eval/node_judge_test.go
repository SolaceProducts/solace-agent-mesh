// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eval

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func judgeWithTemplate(template string) *store.EvalEvaluator {
	return &store.EvalEvaluator{Name: "judge", Type: evaluatorTypeLLMJudge, PromptTemplate: &template}
}

func TestRenderJudgeTemplateFillsEachPlaceholderOnce(t *testing.T) {
	expected := "the ${1} answer"
	in := ScoreInputs{
		Evaluator: judgeWithTemplate("P={{Prompt}} R={{Response}} E={{Expected Response}} O={{Node Output: classify}} I={{Node Input: classify}}"),
		Prompt:    "asks {{Response}} for $1",
		Response:  "says $1 and {{Expected Response}}",
		Expected:  &expected,
		ObservedPath: observedPath{
			outputs: map[string]string{"classify": `{"note":"{{Prompt}} $2"}`},
			inputs:  map[string]string{"classify": `{"order_id":"O-1"}`},
		},
		WorkflowTrajectory: true,
	}

	want := `P=asks {{Response}} for $1 R=says $1 and {{Expected Response}} E=the ${1} answer O={"note":"{{Prompt}} $2"} I={"order_id":"O-1"}`
	if got := renderJudgeTemplate(in); got != want {
		t.Errorf("rendered\n%s\nwant\n%s", got, want)
	}
}

func TestRenderJudgeTemplateNodePlaceholders(t *testing.T) {
	template := "{{Node Output: classify}} | {{Node Input: classify}} | {{Node Output: enrich ▸ validate}}"
	cases := []struct {
		name string
		in   ScoreInputs
		want string
	}{
		{"an agent trial leaves them as written", ScoreInputs{ObservedPath: observedPath{outputs: map[string]string{"classify": "x"}}},
			template},
		{"a node no captured event names says so", ScoreInputs{WorkflowTrajectory: true, ObservedPath: observedPath{sawOutcome: true}},
			`[No node "classify" reported in this trial: it did not run, is not a node of this workflow, or runs inside a map or loop, which reports only as a whole; a nested sub-workflow's node is named as outer ▸ inner] | ` +
				`[No node "classify" reported in this trial: it did not run, is not a node of this workflow, or runs inside a map or loop, which reports only as a whole; a nested sub-workflow's node is named as outer ▸ inner] | ` +
				`[No node "enrich ▸ validate" reported in this trial: it did not run, is not a node of this workflow, or runs inside a map or loop, which reports only as a whole; a nested sub-workflow's node is named as outer ▸ inner]`},
		{"a capture cut short says so", ScoreInputs{WorkflowTrajectory: true, ObservedPath: observedPath{truncated: true}},
			`[Node "classify": the trial's events stopped being captured before it reported its output] | ` +
				`[Node "classify": the trial's events stopped being captured before it reported its input] | ` +
				`[Node "enrich ▸ validate": the trial's events stopped being captured before it reported its output]`},
		{"a failed node's value says it failed", ScoreInputs{WorkflowTrajectory: true, ObservedPath: observedPath{
			lastStatus: map[string]string{"classify": "error"}, outputs: map[string]string{"classify": "out"}, inputs: map[string]string{"classify": "in"},
			sawOutcome: true}},
			`[Node "classify" failed in this trial. Its recorded output:] out | [Node "classify" failed in this trial. Its recorded input:] in | ` +
				`[No node "enrich ▸ validate" reported in this trial: it did not run, is not a node of this workflow, or runs inside a map or loop, which reports only as a whole; a nested sub-workflow's node is named as outer ▸ inner]`},
		{"an id is matched as written", ScoreInputs{WorkflowTrajectory: true, ObservedPath: observedPath{sawOutcome: true,
			outputs: map[string]string{"Classify": "upper", "classify": "lower", "enrich ▸ validate": "inner"}, inputs: map[string]string{"classify": "in"}}},
			"lower | in | inner"},
		{"a nested node is named under its owner", ScoreInputs{WorkflowTrajectory: true,
			ObservedPath: observedPath{outputs: map[string]string{"classify": "out", "enrich ▸ validate": "inner"}, inputs: map[string]string{"classify": "in"}}},
			"out | in | inner"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.in.Evaluator = judgeWithTemplate(template)
			if got := renderJudgeTemplate(tc.in); got != tc.want {
				t.Errorf("rendered\n%s\nwant\n%s", got, tc.want)
			}
		})
	}
}

func TestNodeIDsKeepTheirCase(t *testing.T) {
	in := ScoreInputs{
		Evaluator:          judgeWithTemplate("{{Node Output: ClassifyOrder}} {{node output: classifyorder}}"),
		WorkflowTrajectory: true,
		ObservedPath:       observedPath{outputs: map[string]string{"ClassifyOrder": "upper", "classifyorder": "lower"}},
	}
	if got := renderJudgeTemplate(in); got != "upper lower" {
		t.Errorf("rendered %q, want each id read as written", got)
	}
	if got := CanonicalizeTemplatePlaceholders("{{ node output: ClassifyOrder }}"); got != "{{Node Output: ClassifyOrder}}" {
		t.Errorf("canonical %q, want the id's case kept", got)
	}
}

func TestARetriedNodeIsReadByItsLatestReport(t *testing.T) {
	result := func(status string, meta map[string]any) map[string]any {
		return workflowStatusEvent(map[string]any{"type": "workflow_node_execution_result", "node_id": "validate", "status": status, "metadata": meta},
			forwardHop(map[string]any{"agent_name": "Inner"}, "sub-enrich", "outer-task", "Outer"))
	}
	start := workflowStatusEvent(map[string]any{"type": "workflow_node_execution_start", "node_id": "enrich", "node_type": "workflow", "sub_task_id": "sub-enrich"}, nil)
	failed := result("error", map[string]any{a2a.MetadataInputPreview: "try-1"})
	succeeded := result("success", map[string]any{a2a.MetadataOutputPreview: `{"valid":true}`})
	template := "{{Node Output: enrich ▸ validate}} | {{Node Input: enrich ▸ validate}}"
	cases := []struct {
		name   string
		events []map[string]any
		want   string
	}{
		{"failed then succeeded", []map[string]any{start, failed, succeeded},
			`{"valid":true} | [Node "enrich ▸ validate" ran, but recorded no input preview; switch, map and loop nodes never record one]`},
		{"succeeded then failed", []map[string]any{start, succeeded, failed},
			`[Node "enrich ▸ validate" failed in this trial and reported no output] | [Node "enrich ▸ validate" failed in this trial. Its recorded input:] try-1`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := readObservedPath(tc.events)
			path.sawOutcome = true
			got := renderJudgeTemplate(ScoreInputs{Evaluator: judgeWithTemplate(template), WorkflowTrajectory: true, ObservedPath: path})
			if got != tc.want {
				t.Errorf("rendered\n%s\nwant\n%s", got, tc.want)
			}
		})
	}
}

func TestMissingNodeValueNoteFollowsWhatTheCaptureSaw(t *testing.T) {
	path := observedPath{
		sawOutcome: true,
		started:    map[string]bool{"lookup": true, "enrich": true, "route": true, "hang": true},
		lastStatus: map[string]string{"lookup": "error", "enrich": "success", "route": "success", "tax": "skipped"},
		outputs:    map[string]string{"route": `{"branch":"a"}`},
	}
	cases := []struct {
		node, side string
		path       observedPath
		want       string
	}{
		{"tax", "output", path, `[Node "tax" was skipped in this trial, so it has no output]`},
		{"lookup", "output", path, `[Node "lookup" failed in this trial and reported no output]`},
		{"route", "input", path, `[Node "route" ran, but recorded no input preview; switch, map and loop nodes never record one]`},
		{"tax", "input", path, `[Node "tax" was skipped in this trial, so it has no input]`},
		{"lookup", "input", path, `[Node "lookup" failed in this trial and reported no input]`},
		{"enrich", "output", path, `[Node "enrich" ran, but its result carries no output preview]`},
		{"hang", "output", path, `[Node "hang" started but reported no result in this trial]`},
		{"nope", "output", path, `[No node "nope" reported in this trial: it did not run, is not a node of this workflow, or runs inside a map or loop, which reports only as a whole; a nested sub-workflow's node is named as outer ▸ inner]`},
		{"nope", "input", observedPath{}, `[Node "nope": the workflow's result was not captured, so whether this node ran is not known]`},
		{"tax", "output", observedPath{truncated: true, lastStatus: path.lastStatus}, `[Node "tax" was skipped in this trial, so it has no output]`},
		{"lookup", "output", observedPath{truncated: true, started: path.started, lastStatus: path.lastStatus}, `[Node "lookup" failed in this trial and reported no output]`},
		{"enrich", "output", observedPath{truncated: true, started: path.started, lastStatus: path.lastStatus}, `[Node "enrich" ran, but its result carries no output preview]`},
		{"hang", "output", observedPath{started: path.started}, `[Node "hang" started but reported no result in this trial]`},
		{"hang", "output", observedPath{truncated: true, started: path.started}, `[Node "hang": the trial's events stopped being captured before it reported its output]`},
	}
	for _, tc := range cases {
		t.Run(tc.node+" "+tc.side, func(t *testing.T) {
			if got := missingNodeValueNote(&tc.path, tc.node, tc.side); got != tc.want {
				t.Errorf("note\n%s\nwant\n%s", got, tc.want)
			}
		})
	}
}

func TestObservedPathKeepsEachNodesPreviews(t *testing.T) {
	result := func(node, input, output string) map[string]any {
		return map[string]any{"type": "workflow_node_execution_result", "node_id": node, "status": "success",
			"metadata": map[string]any{a2a.MetadataInputPreview: input, a2a.MetadataOutputPreview: output}}
	}
	inner := func(data map[string]any) map[string]any {
		return workflowStatusEvent(data, forwardHop(map[string]any{"agent_name": "Inner"}, "sub-enrich", "outer-task", "Outer"))
	}
	observed := readObservedPath([]map[string]any{
		workflowStatusEvent(result("classify", "in-1", "out-1"), nil),
		workflowStatusEvent(result("classify", "in-2", "out-2"), nil),
		workflowStatusEvent(map[string]any{"type": "workflow_node_execution_start", "node_id": "enrich", "node_type": "workflow", "sub_task_id": "sub-enrich"}, nil),
		inner(result("validate", "inner-in", "inner-out")),
	})

	for node, want := range map[string][2]string{"classify": {"in-2", "out-2"}, "enrich ▸ validate": {"inner-in", "inner-out"}} {
		if observed.inputs[node] != want[0] || observed.outputs[node] != want[1] {
			t.Errorf("node %q: input %q output %q, want %q %q", node, observed.inputs[node], observed.outputs[node], want[0], want[1])
		}
	}
}

func TestReadTrialPathReadsNodeEventsOnlyWhenGraded(t *testing.T) {
	stored := `{"nodes":{"classify":"ran"}}`
	task := &pendingTask{
		example: &store.EvalExample{ID: "e1", ExpectedPath: &stored},
		messages: []map[string]any{workflowStatusEvent(map[string]any{"type": "workflow_node_execution_result", "node_id": "classify", "status": "success",
			"metadata": map[string]any{a2a.MetadataOutputPreview: "out"}}, nil)},
	}
	logger := slog.New(slog.DiscardHandler)
	cases := []struct {
		name         string
		evaluator    *store.EvalEvaluator
		wantObserved bool
		wantExpected bool
	}{
		{"a judge naming a node", judgeWithTemplate("{{Node Output: classify}} {{Response}}"), true, false},
		{"a judge naming no node", judgeWithTemplate("{{Response}}"), false, false},
		{"the Workflow Path evaluator", workflowPathEvaluator(), true, true},
		{"another heuristic", outputFactsEvaluator(), false, false},
		{"a decision evaluator escalating to a judge naming a node", escalatingDecision(t, judgeWithTemplate("{{Node Input: classify}} {{Response}}")), true, false},
		{"a decision evaluator escalating to a judge naming no node", escalatingDecision(t, judgeWithTemplate("{{Response}}")), false, false},
		{"a decision evaluator that does not escalate", escalatingDecision(t, nil), false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := readTrialPath(logger, task, []*store.EvalEvaluator{tc.evaluator})
			if got := path.observed.outputs["classify"] == "out"; got != tc.wantObserved {
				t.Errorf("node events read = %v, want %v", got, tc.wantObserved)
			}
			if got := path.expected != nil; got != tc.wantExpected {
				t.Errorf("stored path read = %v, want %v", got, tc.wantExpected)
			}
		})
	}
}

func TestAnEscalatedJudgeSeesTheNodesValues(t *testing.T) {
	cfg := baseDecisionConfig(true)
	cfg.EscalateTo["prompt_template"] = "Classify returned {{Node Output: classify}}; final {{Response}}"
	judge := &capturingJudge{}

	out := ScoreResult(t.Context(), nil, ScoreInputs{
		Evaluator: decisionEvaluatorRow(t, cfg), Prompt: "Route order O-1", Response: `{"priority":"urgent"}`,
		Decision: &fakeDecision{result: answer("partially", 0.42)}, DecisionEnabled: true,
		WorkflowTrajectory: true,
		ObservedPath:       observedPath{sawOutcome: true, outputs: map[string]string{"classify": `{"label":"billing"}`}},
	}, judge)

	if out.Details == nil || !out.Details.Escalated {
		t.Fatalf("out = %+v, want the case escalated", out)
	}
	if got, want := judge.lastUserPrompt(), `Classify returned {"label":"billing"}; final {"priority":"urgent"}`; !strings.Contains(got, want) {
		t.Errorf("escalated judge prompt lacks %q:\n%s", want, got)
	}
}

func escalatingDecision(t *testing.T, target *store.EvalEvaluator) *store.EvalEvaluator {
	t.Helper()
	cfg := decisionRunConfig{DecisionConfig: DecisionConfig{Scoring: "chosen"}}
	if target != nil {
		target.ID = "judge-1"
		cfg.EscalateToEvaluatorID = &target.ID
		cfg.EscalateTo = evaluatorSnapshotEntry(target, nil, nil)
	}
	entry := evaluatorSnapshotEntry(&store.EvalEvaluator{ID: "decision-1", Name: "decide", Type: evaluatorTypeDecision}, nil, nil)
	entry["decision_config"] = cfg
	body, err := json.Marshal(entry)
	if err != nil {
		t.Fatal(err)
	}
	var frozen map[string]any
	if err := json.Unmarshal(body, &frozen); err != nil {
		t.Fatal(err)
	}
	return evaluatorFromSnapshot(frozen)
}

func createJudge(ctx context.Context, svc *EvaluatorService, name, tmpl string) (*EvaluatorResponse, error) {
	model := "gpt-4o"
	return svc.Create(ctx, CreateEvaluatorRequest{
		Name: name, Description: "d", Type: "llm_judge", Model: &model, PromptTemplate: &tmpl,
		ChoiceScores: map[string]any{"Yes": 1.0, "No": 0.0},
	}, "alice")
}

func TestJudgeTemplateNodePlaceholderMustNameANode(t *testing.T) {
	ctx, svcs := newEvalSvcs(t)
	svcs.evaluator.SetFeatureRegistry(workflowEvalsFlag(true))
	refused := []struct{ tmpl, want string }{
		{"{{Node Output: }} secret {{Response}}", "must name a node"},
		{"{{Node Output: }} {{Node Output: a}} {{Response}}", "must name a node"},
		{"{{Response}} {{node input:}}", "must name a node"},
		{"{{Node Output node1}} {{Response}}", "a mistyped node placeholder at character 1;"},
		{"{{Response}} {{Node Outputs: x}}", "at character 14;"},
		{"{{Response}} {{node outptu: x}}", "at character 14;"},
		{"{{Node Output}} {{Response}}", "at character 1;"},
		{"{{Node Output: a{{Response}}}}", "at character 1;"},
		{"{{NodeInput: x}} {{Response}}", "at character 1;"},
		{"{{Node Inptu: x}} {{Response}}", "at character 1;"},
		{"{{Node Intput: x}} {{Response}}", "at character 1;"},
		{"{{ Node_Output: x }} {{Response}}", "at character 1;"},
		{"{{Nodes Output: x}} {{Response}}", "at character 1;"},
		{"{{Node: x}} {{Response}}", "at character 1;"},
		{"ok {{Node Output: a}} é {{Node Ouptut: secret}} {{Response}}", "at character 25;"},
		{"{{Node Output: a}} {{Node Input: }} {{Response}}", "must name a node"},
		{"{{ response }} {{Node Outputs: x}} {{Node Inputs: y}}", "at character 16;"},
		{"{{Node Output: a\nb}} {{Response}}", "at character 1;"},
		{"Grade the secret reply.", "must contain the {{Response}} placeholder"},
		{"{{Node Output: classify}} looks right", "must contain the {{Response}} placeholder"},
	}
	for _, tc := range refused {
		_, err := createJudge(ctx, svcs.evaluator, "refused", tc.tmpl)
		ve := asValidationError(t, err)
		if ve.Field != "prompt_template" || !strings.Contains(ve.Message, tc.want) || strings.Contains(ve.Message, "secret") {
			t.Errorf("create %q: %v, want refused with %q and no template text", tc.tmpl, err, tc.want)
		}
	}
	for i, tmpl := range []string{
		"{{Node}} {{Response}}", "{{Node.js}} {{Response}}", "{{node.output.x}} {{Response}}",
		"{{Node-A}} {{Response}}", "{{Node Information}} {{Response}}", "{{Node Result: x}} {{Response}}",
		"{{Node Overview}} {{Response}}",
	} {
		if _, err := createJudge(ctx, svcs.evaluator, fmt.Sprintf("plain-%d", i), tmpl); err != nil {
			t.Errorf("create %q: %v, want it saved as plain text", tmpl, err)
		}
	}

	id := mustCreateLLMJudge(ctx, t, svcs.evaluator, "node-judge-update")
	for tmpl, want := range map[string]string{
		"{{Node Output:}} {{Response}}":          "must name a node",
		"{{Node Inputs: classify}} {{Response}}": "at character 1;",
		"{{Node Output: classify}} looks right":  "must contain the {{Response}} placeholder",
		"Grade the reply.":                       "must contain the {{Response}} placeholder",
		"{{ response }} {{Node Outputs: x}}":     "at character 16;",
	} {
		_, err := svcs.evaluator.Update(ctx, id, UpdateEvaluatorRequest{PromptTemplateSet: true, PromptTemplate: &tmpl}, "bob")
		if ve := asValidationError(t, err); !strings.Contains(ve.Message, want) {
			t.Errorf("update %q: %v, want refused with %q", tmpl, err, want)
		}
	}
	named := "{{ NODE  OUTPUT :  classify }} {{Response}}"
	resp, err := svcs.evaluator.Update(ctx, id, UpdateEvaluatorRequest{PromptTemplateSet: true, PromptTemplate: &named}, "bob")
	if err != nil || resp.PromptTemplate == nil || *resp.PromptTemplate != "{{Node Output: classify}} {{Response}}" {
		t.Errorf("update = %+v, %v; want the template stored canonical", resp, err)
	}
}

func TestJudgeTemplateNodeChecksSpareUnchangedAndUngatedTemplates(t *testing.T) {
	ctx, svcs := newEvalSvcs(t)
	svcs.evaluator.SetFeatureRegistry(workflowEvalsFlag(false))
	legacy := "{{Node Outputs: x}} {{Node Output:}} {{Response}}"
	created, err := createJudge(ctx, svcs.evaluator, "legacy", legacy)
	if err != nil {
		t.Fatalf("create with workflow evaluations off: %v", err)
	}
	legacy += " Answer Yes or No."
	_, err = svcs.evaluator.Update(ctx, created.ID, UpdateEvaluatorRequest{PromptTemplateSet: true, PromptTemplate: &legacy}, "bob")
	if err != nil {
		t.Fatalf("update changing the template with workflow evaluations off: %v", err)
	}

	svcs.evaluator.SetFeatureRegistry(workflowEvalsFlag(true))
	renamed := "legacy-renamed"
	respaced := strings.Replace(legacy, "{{Response}}", "{{ response }}", 1)
	resp, err := svcs.evaluator.Update(ctx, created.ID, UpdateEvaluatorRequest{NameSet: true, Name: &renamed, PromptTemplateSet: true, PromptTemplate: &respaced}, "bob")
	if err != nil || resp.Name != renamed {
		t.Fatalf("update re-sending the template unchanged = %+v, %v; want it saved", resp, err)
	}
	edited := legacy + " Be strict."
	_, err = svcs.evaluator.Update(ctx, created.ID, UpdateEvaluatorRequest{PromptTemplateSet: true, PromptTemplate: &edited}, "bob")
	if ve := asValidationError(t, err); !strings.Contains(ve.Message, "at character 1;") {
		t.Errorf("update changing the template: %v, want it checked", err)
	}

	stored, choices := "{{ node outputs: x }} {{ response }}\n", `{"Yes": 1.0, "No": 0.0}`
	model := "gpt-4o"
	row := &store.EvalEvaluator{Name: "stored-before", Type: evaluatorTypeLLMJudge, Model: &model, PromptTemplate: &stored, ChoiceScores: &choices, CreatedBy: "alice", UpdatedBy: "alice"}
	if err = svcs.evaluators.Create(ctx, row); err != nil {
		t.Fatal(err)
	}
	resent, renamedAgain := strings.TrimSpace(stored)+" \n\n", "stored-before-renamed"
	_, err = svcs.evaluator.Update(ctx, row.ID, UpdateEvaluatorRequest{NameSet: true, Name: &renamedAgain, PromptTemplateSet: true, PromptTemplate: &resent}, "bob")
	if err != nil {
		t.Errorf("update re-sending a stored non-canonical template with other trailing whitespace: %v, want it saved", err)
	}
}

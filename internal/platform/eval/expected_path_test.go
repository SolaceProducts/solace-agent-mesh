// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eval

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func nodeStart(id string) map[string]any {
	return workflowStatusEvent(map[string]any{"type": "workflow_node_execution_start", "node_id": id}, nil)
}

func nodeResult(id, status string) map[string]any {
	return workflowStatusEvent(map[string]any{"type": "workflow_node_execution_result", "node_id": id, "status": status}, nil)
}

func toolCall(name string) map[string]any {
	return workflowStatusEvent(map[string]any{"type": "tool_invocation_start", "tool_name": name}, map[string]any{"node_id": "lookup"})
}

func workflowResult(status string) map[string]any {
	return workflowStatusEvent(map[string]any{"type": "workflow_execution_result", "status": status}, nil)
}

func captureTruncated() map[string]any {
	return map[string]any{"payload": map[string]any{"type": taskMessageBufferTruncated, "limit": maxTaskMessages}}
}

func orderTrial() []map[string]any {
	return []map[string]any{
		nodeStart("classify"), nodeResult("classify", "success"),
		nodeStart("lookup"), toolCall("crm_lookup"), nodeResult("lookup", "success"),
		nodeResult("escalate", "skipped"),
		nodeStart("each"), nodeResult("each", "success"), nodeResult("each", "error"),
		nodeStart("audit"), nodeResult("audit", "error"),
		nodeStart("notify"),
		workflowResult("success"),
	}
}

func workflowPathEvaluator() *store.EvalEvaluator {
	htype := heuristicWorkflowPath
	return &store.EvalEvaluator{Name: "Workflow Path", Type: evaluatorTypeHeuristic, HeuristicType: &htype}
}

func TestObservedPathNodeOutcomes(t *testing.T) {
	observed := readObservedPath(orderTrial())

	for node, want := range map[string]string{
		"classify": pathNodeSucceeded,
		"escalate": pathNodeSkipped,
		"each":     pathNodeFailed,
		"audit":    pathNodeFailed,
		"notify":   pathNodeRan,
		"refund":   pathNodeNotRan,
	} {
		if got := observed.nodeOutcome(node); got != want {
			t.Errorf("node %s: outcome %q, want %q", node, got, want)
		}
	}
	if observed.toolCalls["crm_lookup"] != 1 {
		t.Errorf("tool calls = %v, want crm_lookup once", observed.toolCalls)
	}
	unknown := readObservedPath([]map[string]any{nodeStart("hold"), nodeResult("hold", "waiting")})
	if got := unknown.nodeOutcome("hold"); got != pathNodeRan {
		t.Errorf("a status the path does not know: outcome %q, want %q", got, pathNodeRan)
	}
}

func TestWorkflowPathScore(t *testing.T) {
	observed := readObservedPath(orderTrial())
	cases := []struct {
		name       string
		path       *ExpectedPath
		wantScore  float64
		wantPassed bool
		wantReason []string
	}{
		{
			name: "every assertion held",
			path: &ExpectedPath{
				Nodes: map[string]string{"classify": "succeeded", "escalate": "skipped", "refund": "not_ran", "notify": "ran", "audit": "failed"},
				Tools: map[string]string{"crm_lookup": "called", "issue_refund": "not_called"},
			},
			wantScore: 1, wantPassed: true, wantReason: []string{"7 of 7 path assertions held."},
		},
		{
			name:      "a skipped node counts as not run, a failed one as run",
			path:      &ExpectedPath{Nodes: map[string]string{"escalate": "not_ran", "audit": "ran"}},
			wantScore: 1, wantPassed: true,
		},
		{
			name: "broken assertions are named",
			path: &ExpectedPath{
				Nodes: map[string]string{"audit": "succeeded", "escalate": "ran", "classify": "succeeded", "refund": "skipped"},
				Tools: map[string]string{"crm_lookup": "not_called", "issue_refund": "called"},
			},
			wantScore: 1.0 / 6, wantPassed: false,
			wantReason: []string{
				"1 of 6 path assertions held.",
				`node "audit" should succeed, but it failed`,
				`node "escalate" should run, but it was skipped`,
				`node "refund" should be skipped, but it never ran`,
				`tool "crm_lookup" should not be called, but it was called 1 time(s)`,
				`tool "issue_refund" should be called, but it was not`,
			},
		},
		{name: "an example with no path", wantScore: 0, wantPassed: false, wantReason: []string{reasonNoExpectedPath}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := ScoreResult(context.Background(), nil, ScoreInputs{Evaluator: workflowPathEvaluator(), ExpectedPath: tc.path, ObservedPath: observed, WorkflowTrajectory: true}, nil)

			if out.Score != tc.wantScore || out.Passed != tc.wantPassed || out.Errored {
				t.Errorf("score %v passed %v, want %v %v", out.Score, out.Passed, tc.wantScore, tc.wantPassed)
			}
			for _, want := range tc.wantReason {
				if out.Reasoning == nil || !strings.Contains(*out.Reasoning, want) {
					t.Errorf("reasoning %v does not contain %q", out.Reasoning, want)
				}
			}
		})
	}
}

func TestWorkflowPathScoreErrorsWhenThePathCannotBeKnown(t *testing.T) {
	absent := &ExpectedPath{Nodes: map[string]string{"refund": "not_ran"}, Tools: map[string]string{"issue_refund": "not_called"}}
	cases := []struct {
		name       string
		in         ScoreInputs
		wantReason string
	}{
		{"an agent target", ScoreInputs{ExpectedPath: absent, ObservedPath: readObservedPath(orderTrial())},
			"grades only an experiment that targets a workflow"},
		{"an unreadable stored path", ScoreInputs{ExpectedPathUnreadable: true, ObservedPath: readObservedPath(orderTrial()), WorkflowTrajectory: true},
			"stored expected path cannot be read"},
		{"a capture cut short", ScoreInputs{ExpectedPath: absent, ObservedPath: readObservedPath(append(orderTrial(), captureTruncated())), WorkflowTrajectory: true},
			"stopped before the workflow finished"},
		{"no workflow result captured", ScoreInputs{ExpectedPath: absent, ObservedPath: readObservedPath(orderTrial()[:4]), WorkflowTrajectory: true},
			"workflow's result was not captured"},
		{"no events at all", ScoreInputs{ExpectedPath: absent, ObservedPath: readObservedPath(nil), WorkflowTrajectory: true},
			"workflow's result was not captured"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.in.Evaluator = workflowPathEvaluator()
			out := ScoreResult(context.Background(), nil, tc.in, nil)

			if !out.Errored || out.Passed || out.Reasoning == nil || !strings.Contains(*out.Reasoning, tc.wantReason) {
				t.Errorf("errored %v passed %v reasoning %v, want errored naming %q", out.Errored, out.Passed, out.Reasoning, tc.wantReason)
			}
		})
	}
}

func TestObservedPathNamesNestedNodesUnderTheirNode(t *testing.T) {
	inner := func(data map[string]any) map[string]any {
		own := map[string]any{"agent_name": "Inner", "workflow_name": "Inner"}
		return workflowStatusEvent(data, forwardHop(own, "sub-enrich", "outer-task", "Outer"))
	}
	messages := []map[string]any{
		workflowStatusEvent(map[string]any{"type": "workflow_node_execution_start", "node_id": "enrich", "node_type": "workflow", "sub_task_id": "sub-enrich"}, nil),
		inner(map[string]any{"type": "workflow_node_execution_start", "node_id": "validate", "node_type": "tool", "sub_task_id": "call-1"}),
		inner(map[string]any{"type": "tool_invocation_start", "tool_name": "fetch"}),
		inner(map[string]any{"type": "workflow_node_execution_result", "node_id": "validate", "status": "success"}),
		inner(map[string]any{"type": "workflow_execution_result", "status": "success"}),
		workflowStatusEvent(map[string]any{"type": "workflow_node_execution_result", "node_id": "enrich", "status": "success"}, nil),
		nodeResult("validate", "skipped"),
	}

	observed := readObservedPath(messages)

	for node, want := range map[string]string{
		"enrich":            pathNodeSucceeded,
		"validate":          pathNodeSkipped,
		"enrich ▸ validate": pathNodeSucceeded,
	} {
		if got := observed.nodeOutcome(node); got != want {
			t.Errorf("node %q: outcome %q, want %q", node, got, want)
		}
	}
	if observed.toolCalls["fetch"] != 1 {
		t.Errorf("tool calls = %v, want fetch once", observed.toolCalls)
	}
	if observed.sawOutcome {
		t.Error("the sub-workflow's result was taken for the workflow's own")
	}
	if !readObservedPath(append(messages, workflowResult("error"))).sawOutcome {
		t.Error("the workflow's own failed result was not seen")
	}
}

func TestObservedPathKeepsAnUnownedForwardedEventApart(t *testing.T) {
	stray := func(data map[string]any) map[string]any {
		return workflowStatusEvent(data, forwardHop(map[string]any{"agent_name": "Inner"}, "sub-missing", "outer-task", "Outer"))
	}
	observed := readObservedPath([]map[string]any{
		stray(map[string]any{"type": "workflow_node_execution_result", "node_id": "validate", "status": "success"}),
		stray(map[string]any{"type": "workflow_execution_result", "status": "success"}),
	})

	if got := observed.nodeOutcome("validate"); got != pathNodeNotRan {
		t.Errorf("outer validate: outcome %q, want %q", got, pathNodeNotRan)
	}
	if got := observed.nodeOutcome(unattributedOwner + " ▸ validate"); got != pathNodeSucceeded {
		t.Errorf("unattributed validate: outcome %q, want %q", got, pathNodeSucceeded)
	}
	if observed.sawOutcome {
		t.Error("a forwarded result was taken for the workflow's own")
	}
}

func TestSameExpectedPath(t *testing.T) {
	base := &ExpectedPath{Nodes: map[string]string{"approve": "ran"}, Tools: map[string]string{"issue_refund": "called"}}
	cases := []struct {
		name string
		b    *ExpectedPath
		same bool
	}{
		{"equal", &ExpectedPath{Nodes: map[string]string{"approve": "ran"}, Tools: map[string]string{"issue_refund": "called"}}, true},
		{"a node changed", &ExpectedPath{Nodes: map[string]string{"approve": "failed"}, Tools: map[string]string{"issue_refund": "called"}}, false},
		{"a tool changed", &ExpectedPath{Nodes: map[string]string{"approve": "ran"}, Tools: map[string]string{"issue_refund": "not_called"}}, false},
		{"a tool dropped", &ExpectedPath{Nodes: map[string]string{"approve": "ran"}}, false},
		{"none", nil, false},
	}
	for _, tc := range cases {
		if got := SameExpectedPath(base, tc.b); got != tc.same {
			t.Errorf("%s: same = %v, want %v", tc.name, got, tc.same)
		}
	}
	if !SameExpectedPath(nil, nil) {
		t.Error("two absent paths differ")
	}
}

func TestNotRanBreaksForANodeThatRanWhateverItsResult(t *testing.T) {
	path := &ExpectedPath{Nodes: map[string]string{"audit": "not_ran", "classify": "not_ran"}}
	out := ScoreResult(context.Background(), nil, ScoreInputs{Evaluator: workflowPathEvaluator(), ExpectedPath: path, ObservedPath: readObservedPath(orderTrial()), WorkflowTrajectory: true}, nil)

	if out.Score != 0 || out.Passed || out.Reasoning == nil ||
		!strings.Contains(*out.Reasoning, `node "audit" should not run, but it failed`) ||
		!strings.Contains(*out.Reasoning, `node "classify" should not run, but it succeeded`) {
		t.Errorf("score %v passed %v reasoning %v, want 0 naming both nodes", out.Score, out.Passed, out.Reasoning)
	}
}

func TestExpectedPathCellNullIsNoPath(t *testing.T) {
	for _, cell := range []string{"", "  ", "null", " null "} {
		if path, err := ParseExpectedPathCell(cell); path != nil || err != nil {
			t.Errorf("cell %q = %+v, %v; want no path", cell, path, err)
		}
	}
}

func TestExpectedPathHoldsAtMostAHundredAssertions(t *testing.T) {
	path := func(nodes, tools int) *ExpectedPath {
		out := &ExpectedPath{Nodes: map[string]string{}, Tools: map[string]string{}}
		for i := range nodes {
			out.Nodes[fmt.Sprintf("n%03d", i)] = pathNodeRan
		}
		for i := range tools {
			out.Tools[fmt.Sprintf("t%03d", i)] = pathToolCalled
		}
		return out
	}
	for _, tc := range []struct {
		nodes, tools int
		ok           bool
	}{{100, 0, true}, {101, 0, false}, {50, 50, true}, {50, 51, false}, {0, 100, true}, {0, 101, false}} {
		if err := validateExpectedPath(path(tc.nodes, tc.tools)); (err == nil) != tc.ok {
			t.Errorf("%d nodes and %d tools: err = %v, want accepted %v", tc.nodes, tc.tools, err, tc.ok)
		}
	}
	bad := map[string]string{}
	for i := range 5000 {
		bad[fmt.Sprintf("n%04d", i)] = "done"
	}
	var verr *ValidationError
	if err := validateExpectedPath(&ExpectedPath{Nodes: bad}); !errors.As(err, &verr) || len(verr.Details) != 1 || len(verr.Details["expected_path"]) != 1 {
		t.Errorf("5000 bad assertions reported %v; want only the count refused, so the error does not grow with the request", err)
	}
}

func TestDecodeExpectedPathRefusesAnUnreadableValue(t *testing.T) {
	want := &ExpectedPath{Nodes: map[string]string{"a": "ran"}}
	for _, raw := range []string{`not json`, `{"nodes":{"a":"done"}}`, `{"nodes":{"a":"ran"}} {}`, `{"nodes":{"a":"ran"}}}`, `{"nodes":{"a":"ran"}}]`, `{"node":{"a":"ran"}}`, `{}`} {
		t.Run(raw, func(t *testing.T) {
			if path, err := decodeExpectedPath(&raw); err == nil {
				t.Errorf("decoded %+v, want an error", path)
			}
			if sameExpectedPath(&raw, nil) || sameExpectedPath(&raw, want) {
				t.Error("an unreadable stored path compared equal")
			}
			if shownExpectedPath(&raw) != nil {
				t.Error("an unreadable stored path was shown")
			}
		})
	}
	stored := `{"nodes":{"a":"ran"}}`
	if path, err := decodeExpectedPath(&stored); err != nil || !SameExpectedPath(path, want) || !sameExpectedPath(&stored, want) {
		t.Errorf("a valid stored path: %+v, %v", path, err)
	}
	if path, err := decodeExpectedPath(nil); path != nil || err != nil {
		t.Errorf("no stored path: %+v, %v", path, err)
	}
}

func TestValidateExpectedPath(t *testing.T) {
	if err := validateExpectedPath(&ExpectedPath{Nodes: map[string]string{"a": "succeeded"}, Tools: map[string]string{"t": "called"}}); err != nil {
		t.Fatalf("a valid path: %v", err)
	}
	err := validateExpectedPath(&ExpectedPath{Nodes: map[string]string{"a": "done", " ": "ran"}, Tools: map[string]string{"t": "maybe"}})
	var verr *ValidationError
	if !errors.As(err, &verr) {
		t.Fatalf("err = %v, want a ValidationError", err)
	}
	want := map[string][]string{
		"expected_path.nodes": {"a name must not be blank", `"a": "done" is not one of ran, not_ran, succeeded, failed, skipped`},
		"expected_path.tools": {`"t": "maybe" is not one of called, not_called`},
	}
	if !maps.EqualFunc(verr.Details, want, slices.Equal[[]string]) {
		t.Errorf("details = %v, want %v (names in the message, never in the field, so a v2 client sees them as typed)", verr.Details, want)
	}
	if err := validateExpectedPath(&ExpectedPath{}); err == nil {
		t.Error("a path with no assertions was accepted")
	}
}

func TestParseExpectedPathCell(t *testing.T) {
	path, err := ParseExpectedPathCell(`{"nodes":{"approve":"ran"},"tools":{"refund":"not_called"}}`)
	if err != nil || path.Nodes["approve"] != "ran" || path.Tools["refund"] != "not_called" {
		t.Fatalf("path = %+v, %v", path, err)
	}
	if path, err := ParseExpectedPathCell("  "); path != nil || err != nil {
		t.Errorf("blank cell = %+v, %v; want no path", path, err)
	}
	for _, cell := range []string{`{"node":{"approve":"ran"}}`, `{"nodes":{"approve":"done"}}`, `[1]`} {
		if _, err := ParseExpectedPathCell(cell); err == nil {
			t.Errorf("cell %s was accepted", cell)
		}
	}
}

func TestExampleExpectedPathRoundTrips(t *testing.T) {
	db := store.TestDB(t)
	ctx := t.Context()
	datasets := NewDatasetService(store.NewEvalDatasetRepo(db), store.NewEvalExampleRepo(db), store.NewEvalRunRepo(db), nil)
	examples := NewExampleService(store.NewEvalExampleRepo(db), store.NewEvalDatasetRepo(db), nil)
	datasets.SetFeatureRegistry(workflowEvalsFlag(true))
	examples.SetFeatureRegistry(workflowEvalsFlag(true))
	ds, dsErr := datasets.Create(ctx, CreateDatasetRequest{Name: "orders"}, "u")
	if dsErr != nil {
		t.Fatalf("create dataset: %v", dsErr)
	}
	path := &ExpectedPath{Nodes: map[string]string{"approve": "ran"}}

	created, err := examples.Create(ctx, ds.ID, CreateExampleRequest{Prompt: "refund O-1", ExpectedPath: path})
	if err != nil || created.ExpectedPath == nil || created.ExpectedPath.Nodes["approve"] != "ran" {
		t.Fatalf("create = %+v, %v; want the path back", created, err)
	}
	if _, badErr := examples.Create(ctx, ds.ID, CreateExampleRequest{Prompt: "x", ExpectedPath: &ExpectedPath{Nodes: map[string]string{"approve": "done"}}}); badErr == nil {
		t.Error("an invalid expectation was stored")
	}

	stored := func(step string, want *ExpectedPath) {
		t.Helper()
		got, getErr := examples.Get(ctx, created.ID)
		if getErr != nil || !SameExpectedPath(got.ExpectedPath, want) {
			t.Errorf("%s: stored path %+v, %v; want %+v", step, got, getErr, want)
		}
	}
	kept, err := examples.Update(ctx, created.ID, UpdateExampleRequest{Prompt: new("refund O-2")})
	if err != nil || kept.ExpectedPath == nil {
		t.Fatalf("an update that leaves the path out cleared it: %+v, %v", kept, err)
	}
	stored("an update leaving it out", path)
	replaced := &ExpectedPath{Tools: map[string]string{"issue_refund": "not_called"}}
	if _, replaceErr := examples.Update(ctx, created.ID, UpdateExampleRequest{ExpectedPath: replaced, ExpectedPathSet: true}); replaceErr != nil {
		t.Fatalf("replace the path: %v", replaceErr)
	}
	stored("a replacing update", replaced)
	cleared, err := examples.Update(ctx, created.ID, UpdateExampleRequest{ExpectedPathSet: true})
	if err != nil || cleared.ExpectedPath != nil {
		t.Errorf("an explicit null left %+v, %v", cleared, err)
	}
	stored("a clearing update", nil)

	csvText := "prompt,expected_path\n" + `"refund O-3","{""tools"":{""issue_refund"":""called""}}"` + "\n"
	if _, importErr := datasets.ImportExamples(ctx, ds.ID, csvText, "text/csv", "u"); importErr != nil {
		t.Fatalf("import: %v", importErr)
	}
	exported, err := datasets.ExportExamplesCSV(ctx, ds.ID)
	if err != nil || !strings.Contains(exported, "expected_path") || !strings.Contains(exported, `""issue_refund"":""called""`) {
		t.Errorf("export = %q, %v; want the expected_path column", exported, err)
	}
	bad := "prompt,expected_path\n" + `"x","{""nodes"":{""a"":""done""}}"` + "\n"
	if _, err := datasets.ImportExamples(ctx, ds.ID, bad, "text/csv", "u"); err == nil || !strings.Contains(err.Error(), "row 2") {
		t.Errorf("a bad cell imported: %v", err)
	}
}

func TestExpectedPathIsRefusedWhileWorkflowEvalsIsOff(t *testing.T) {
	db := store.TestDB(t)
	ctx := t.Context()
	datasets := NewDatasetService(store.NewEvalDatasetRepo(db), store.NewEvalExampleRepo(db), store.NewEvalRunRepo(db), nil)
	examples := NewExampleService(store.NewEvalExampleRepo(db), store.NewEvalDatasetRepo(db), nil)
	datasets.SetFeatureRegistry(workflowEvalsFlag(false))
	examples.SetFeatureRegistry(workflowEvalsFlag(false))
	ds, err := datasets.Create(ctx, CreateDatasetRequest{Name: "orders"}, "u")
	if err != nil {
		t.Fatalf("create dataset: %v", err)
	}
	path := &ExpectedPath{Nodes: map[string]string{"approve": "ran"}}
	refused := func(what string, err error) {
		t.Helper()
		if err == nil || !strings.Contains(err.Error(), "workflow evaluations are turned off") {
			t.Errorf("%s: err = %v, want the workflow_evals refusal", what, err)
		}
	}

	_, err = examples.Create(ctx, ds.ID, CreateExampleRequest{Prompt: "refund O-1", ExpectedPath: path})
	refused("create", err)
	plain, err := examples.Create(ctx, ds.ID, CreateExampleRequest{Prompt: "refund O-1"})
	if err != nil {
		t.Fatalf("create without a path: %v", err)
	}
	_, err = examples.Update(ctx, plain.ID, UpdateExampleRequest{ExpectedPath: path, ExpectedPathSet: true})
	refused("update", err)
	if _, clearErr := examples.Update(ctx, plain.ID, UpdateExampleRequest{ExpectedPathSet: true}); clearErr != nil {
		t.Errorf("clearing a path: %v", clearErr)
	}
	_, err = datasets.ImportExamples(ctx, ds.ID, "prompt,expected_path\n"+`"x","{""nodes"":{""a"":""ran""}}"`+"\n", "text/csv", "u")
	refused("csv import", err)
	_, err = datasets.ImportExamples(ctx, ds.ID, `[{"prompt":"x","expectedPath":{"nodes":{"a":"ran"}}}]`, "application/json", "u")
	refused("json import", err)
	_, err = datasets.ImportExamplesStrict(ctx, ds.ID, ImportFormatJSON, []byte(`[{"prompt":"x","expected_path":{"nodes":{"a":"ran"}}}]`))
	refused("strict json import", err)
	_, err = datasets.ReconcileExamples(ctx, ds.ID, []DesiredExample{{SequenceNumber: 1, Prompt: "x", ExpectedPath: path}}, "u")
	refused("reconcile", err)
}

func TestExpectedPathReconcilesAndExports(t *testing.T) {
	stored := `{"nodes":{"approve":"ran"}}`
	actual := []*store.EvalExample{{ID: "e1", SequenceNumber: 1, Prompt: "p", ExpectedPath: &stored}}
	same := DiffExamples(actual, []DesiredExample{{SequenceNumber: 1, Prompt: "p", ExpectedPath: &ExpectedPath{Nodes: map[string]string{"approve": "ran"}}}})
	if same.Updates()+len(same.InsertOps)+same.Deletes() != 0 {
		t.Errorf("an equal path planned %+v, want nothing", same)
	}
	changed := DiffExamples(actual, []DesiredExample{{SequenceNumber: 1, Prompt: "p", ExpectedPath: &ExpectedPath{Nodes: map[string]string{"approve": "failed"}}}})
	if changed.Updates() != 1 {
		t.Errorf("a changed path planned %+v, want one update", changed)
	}
	storedTools := `{"tools":{"issue_refund":"called"}}`
	toolChanged := DiffExamples([]*store.EvalExample{{ID: "e1", SequenceNumber: 1, Prompt: "p", ExpectedPath: &storedTools}},
		[]DesiredExample{{SequenceNumber: 1, Prompt: "p", ExpectedPath: &ExpectedPath{Tools: map[string]string{"issue_refund": "not_called"}}}})
	if toolChanged.Updates() != 1 {
		t.Errorf("a changed tool assertion planned %+v, want one update", toolChanged)
	}

	db := store.TestDB(t)
	ctx := t.Context()
	datasets := NewDatasetService(store.NewEvalDatasetRepo(db), store.NewEvalExampleRepo(db), store.NewEvalRunRepo(db), nil)
	datasets.SetFeatureRegistry(workflowEvalsFlag(true))
	ds, err := datasets.Create(ctx, CreateDatasetRequest{Name: "orders"}, "u")
	if err != nil {
		t.Fatalf("create dataset: %v", err)
	}
	if _, importErr := datasets.ImportExamples(ctx, ds.ID, `[{"prompt":"refund O-1","expectedPath":{"tools":{"issue_refund":"called"}}}]`, "application/json", "u"); importErr != nil {
		t.Fatalf("import: %v", importErr)
	}
	exported, err := datasets.ExportExamplesJSON(ctx, ds.ID)
	if err != nil || len(exported) != 1 || exported[0].ExpectedPath == nil || exported[0].ExpectedPath.Tools["issue_refund"] != "called" {
		t.Errorf("export = %+v, %v; want the path", exported, err)
	}
}

func TestUpdateExampleRequestSendsAClearedPathAsNull(t *testing.T) {
	body, err := json.Marshal(UpdateExampleRequest{ExpectedPathSet: true})
	if err != nil || string(body) != `{"expectedPath":null}` {
		t.Errorf("body = %s, %v; want the path cleared with null", body, err)
	}
	body, err = json.Marshal(UpdateExampleRequest{Prompt: new("p")})
	if err != nil || strings.Contains(string(body), "expectedPath") {
		t.Errorf("body = %s, %v; want no expectedPath when it is left alone", body, err)
	}
}

func TestBuildConfigSnapshotCarriesTheExpectedPath(t *testing.T) {
	stored := `{"nodes":{"approve":"ran"}}`
	examples := []*store.EvalExample{{ID: "a", SequenceNumber: 1, ExpectedPath: &stored}, {ID: "b", SequenceNumber: 2}}
	encoded, err := json.Marshal(buildConfigSnapshot(&store.EvalExperiment{ID: "e"}, "d", "orders", examples, nil))
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Dataset struct {
			Examples []map[string]json.RawMessage `json:"examples"`
		} `json:"dataset"`
	}
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if got := string(decoded.Dataset.Examples[0]["expected_path"]); got != stored {
		t.Errorf("expected_path = %s, want %s", got, stored)
	}
	if got, ok := decoded.Dataset.Examples[1]["expected_path"]; !ok || string(got) != "null" {
		t.Errorf("no-path example's expected_path = %s (present %v), want present and null", got, ok)
	}
}

func TestImportedPathsAreReadStrictly(t *testing.T) {
	const mixed = `{"nodes":{"approve":"ran"},"tool":{"issue_refund":"called"}}`
	if _, err := ParseExpectedPathCell(mixed); err == nil {
		t.Error("config-apply cell: a misspelt key was accepted")
	}
	if _, err := ParseExpectedPathCell(`{"nodes":{"approve":"ran"}}}`); err == nil {
		t.Error("config-apply cell: a stray closing brace was accepted")
	}
	if _, err := encodeExpectedPathCell(mixed); err == nil {
		t.Error("CSV cell: a misspelt key was accepted")
	}
	if _, err := importedExpectedPath(json.RawMessage(mixed)); err == nil {
		t.Error("JSON value: a misspelt key was accepted")
	}
	for _, none := range []string{"", "null", " null "} {
		if got, err := importedExpectedPath(json.RawMessage(none)); got != nil || err != nil {
			t.Errorf("JSON value %q = %v, %v; want no path", none, got, err)
		}
	}

	db := store.TestDB(t)
	ctx := t.Context()
	datasets := NewDatasetService(store.NewEvalDatasetRepo(db), store.NewEvalExampleRepo(db), store.NewEvalRunRepo(db), nil)
	datasets.SetFeatureRegistry(workflowEvalsFlag(true))
	examples := NewExampleService(store.NewEvalExampleRepo(db), store.NewEvalDatasetRepo(db), nil)
	ds, err := datasets.Create(ctx, CreateDatasetRequest{Name: "legacy"}, "u")
	if err != nil {
		t.Fatalf("create dataset: %v", err)
	}
	lenient := `[{"prompt":"lenient","expected_path":{"nodes":{"approve":"ran"}}},{"prompt":"none","expectedPath":null}]`
	if _, importErr := datasets.ImportExamples(ctx, ds.ID, lenient, "application/json", "u"); importErr != nil {
		t.Fatalf("lenient import: %v", importErr)
	}
	strict := `[{"prompt":"strict","expected_path":{"nodes":{"approve":"ran"}}},{"prompt":"none","expectedPath":null}]`
	if _, importErr := datasets.ImportExamplesStrict(ctx, ds.ID, ImportFormatJSON, []byte(strict)); importErr != nil {
		t.Fatalf("strict import: %v", importErr)
	}
	misspelt := `[{"prompt":"x","expectedPath":` + mixed + `}]`
	if _, importErr := datasets.ImportExamples(ctx, ds.ID, misspelt, "application/json", "u"); importErr == nil {
		t.Error("lenient import: a misspelt key was accepted")
	}
	if _, importErr := datasets.ImportExamplesStrict(ctx, ds.ID, ImportFormatJSON, []byte(misspelt)); importErr == nil {
		t.Error("strict import: a misspelt key was accepted")
	}

	stored, err := examples.List(ctx, ds.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(stored) != 4 {
		t.Fatalf("stored %d examples, want 4", len(stored))
	}
	approve := &ExpectedPath{Nodes: map[string]string{"approve": "ran"}}
	want := map[string]*ExpectedPath{"lenient": approve, "strict": approve, "none": nil}
	for _, ex := range stored {
		if !SameExpectedPath(ex.ExpectedPath, want[ex.Prompt]) {
			t.Errorf("example %q: path %+v, want %+v", ex.Prompt, ex.ExpectedPath, want[ex.Prompt])
		}
	}
}

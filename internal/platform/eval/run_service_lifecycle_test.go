// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eval

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/eval/storage"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func TestRunServiceGetAndListAndDelete(t *testing.T) {
	f := newRunFixture(t, 1)
	runs, err := f.svc.TriggerRuns(f.ctx, f.experimentID, "alice", "", "manual")
	if err != nil {
		t.Fatalf("trigger: %v", err)
	}
	runID := runs[0].ID

	got, err := f.svc.GetRun(f.ctx, runID)
	if err != nil || got.ID != runID {
		t.Fatalf("get run: got=%+v err=%v", got, err)
	}

	listed, err := f.svc.ListRuns(f.ctx, f.experimentID)
	if err != nil || len(listed) != 1 {
		t.Fatalf("list runs: %d %v", len(listed), err)
	}

	if _, err := f.svc.GetRun(f.ctx, idgen.New()); !errors.As(err, new(*NotFoundError)) {
		t.Fatalf("expected NotFoundError on missing run, got %v", err)
	}
	if _, err := f.svc.ListRuns(f.ctx, idgen.New()); !errors.As(err, new(*NotFoundError)) {
		t.Fatalf("expected NotFoundError on missing experiment, got %v", err)
	}

	if err := f.svc.DeleteRun(f.ctx, runID); !isRunActiveRefusal(err) {
		t.Fatalf("delete of a pending run = %v, want RUN_IS_ACTIVE", err)
	}
	if _, err := f.svc.CancelRun(f.ctx, runID); err != nil {
		t.Fatalf("cancel run: %v", err)
	}
	if err := f.svc.DeleteRun(f.ctx, runID); err != nil {
		t.Fatalf("delete run: %v", err)
	}
	if _, err := f.svc.GetRun(f.ctx, runID); err == nil {
		t.Fatalf("expected error after delete")
	}
}

func TestRunServiceGetRunResults(t *testing.T) {
	f := newRunFixture(t, 1)
	runs, err := f.svc.TriggerRuns(f.ctx, f.experimentID, "alice", "", "manual")
	if err != nil {
		t.Fatalf("trigger: %v", err)
	}
	runID := runs[0].ID
	results, err := f.svc.GetRunResults(f.ctx, runID)
	if err != nil {
		t.Fatalf("get results: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("expected empty results pre-execution, got %d", len(results))
	}
	if _, err := f.svc.GetRunResults(f.ctx, idgen.New()); !errors.As(err, new(*NotFoundError)) {
		t.Fatalf("expected NotFoundError, got %v", err)
	}
}

func TestRunServiceRunGroupsCarryTheirTargetType(t *testing.T) {
	f := newRunFixture(t, 1)
	if _, err := f.svc.TriggerRuns(f.ctx, f.experimentID, "alice", "", "manual"); err != nil {
		t.Fatalf("trigger: %v", err)
	}
	flow := &store.EvalExperiment{Name: "order-flow", TargetAgent: "order_flow", TargetType: store.EvalTargetTypeWorkflow,
		Models: "[]", RunsPerExample: 1, MaxWorkers: 1, DatasetID: &f.datasetID, CreatedBy: "alice", UpdatedBy: "alice"}
	if err := f.expRepo.Create(f.ctx, flow, []string{f.evaluatorID}); err != nil {
		t.Fatalf("create workflow experiment: %v", err)
	}
	if err := f.runRepo.Create(f.ctx, &store.EvalRun{ExperimentID: flow.ID, RunGroupID: idgen.New(), Status: StatusCompleted, StartedTime: 100, StartedBy: "alice"}); err != nil {
		t.Fatalf("create workflow run: %v", err)
	}

	page, err := f.svc.ListRunGroupsPaginated(f.ctx, 1, 10, store.RunGroupFilter{})
	if err != nil {
		t.Fatalf("list groups: %v", err)
	}
	got := map[string]string{}
	for _, g := range page.Data {
		got[g.ExperimentID] = g.TargetType
	}
	if got[f.experimentID] != store.EvalTargetTypeAgent || got[flow.ID] != store.EvalTargetTypeWorkflow {
		t.Errorf("target types by experiment = %v, want the agent's agent and the workflow's workflow", got)
	}
}

func TestRunServiceRunGroupOps(t *testing.T) {
	f := newRunFixture(t, 1)
	runs, err := f.svc.TriggerRuns(f.ctx, f.experimentID, "alice", "named-run", "manual")
	if err != nil {
		t.Fatalf("trigger: %v", err)
	}
	groupID := runs[0].RunGroupID

	groupRuns, err := f.svc.ListRunsInGroup(f.ctx, groupID)
	if err != nil || len(groupRuns) != 1 {
		t.Fatalf("list runs in group: %d %v", len(groupRuns), err)
	}
	if _, lerr := f.svc.ListResultsInGroup(f.ctx, groupID); lerr != nil {
		t.Fatalf("list results in group: %v", lerr)
	}

	page, err := f.svc.ListRunGroupsPaginated(f.ctx, 1, 10, store.RunGroupFilter{})
	if err != nil {
		t.Fatalf("list groups paginated: %v", err)
	}
	if len(page.Data) != 1 {
		t.Fatalf("page data = %d, want 1", len(page.Data))
	}
	if page.Data[0].RunGroupID != groupID {
		t.Fatalf("group id mismatch: %s vs %s", page.Data[0].RunGroupID, groupID)
	}

	filtered, err := f.svc.ListRunGroupsPaginated(f.ctx, 1, 10, store.RunGroupFilter{Name: "named"})
	if err != nil || len(filtered.Data) != 1 {
		t.Fatalf("filter by name: %d %v", len(filtered.Data), err)
	}
	missed, err := f.svc.ListRunGroupsPaginated(f.ctx, 1, 10, store.RunGroupFilter{Name: "no-match"})
	if err != nil || len(missed.Data) != 0 {
		t.Fatalf("filter no-match: %d %v", len(missed.Data), err)
	}

	if err = f.svc.DeleteRunGroup(f.ctx, groupID); !isRunActiveRefusal(err) {
		t.Fatalf("delete of a group with a pending run = %v, want RUN_IS_ACTIVE", err)
	}
	inGroup, err := f.svc.ListRunsInGroup(f.ctx, groupID)
	if err != nil {
		t.Fatalf("list group: %v", err)
	}
	for _, r := range inGroup {
		if _, err := f.svc.CancelRun(f.ctx, r.ID); err != nil {
			t.Fatalf("cancel run: %v", err)
		}
	}
	if err := f.svc.DeleteRunGroup(f.ctx, groupID); err != nil {
		t.Fatalf("delete group: %v", err)
	}
	if _, err := f.svc.ListRunsInGroup(f.ctx, groupID); !errors.As(err, new(*NotFoundError)) {
		t.Fatalf("expected NotFoundError after delete, got %v", err)
	}
	if err := f.svc.DeleteRunGroup(f.ctx, idgen.New()); !errors.As(err, new(*NotFoundError)) {
		t.Fatalf("expected NotFoundError, got %v", err)
	}
}

func TestRunServiceListDistinctTargetAgents(t *testing.T) {
	f := newRunFixture(t, 1)
	if _, err := f.svc.TriggerRuns(f.ctx, f.experimentID, "alice", "", "manual"); err != nil {
		t.Fatalf("trigger: %v", err)
	}
	agents, err := f.svc.ListDistinctTargetAgents(f.ctx)
	if err != nil {
		t.Fatalf("list agents: %v", err)
	}
	if len(agents) != 1 || agents[0] != "Orchestrator" {
		t.Fatalf("agents = %v", agents)
	}
}

func TestRunServiceExecutionDataAndTaskEvents(t *testing.T) {
	f := newRunFixture(t, 1)
	runs, err := f.svc.TriggerRuns(f.ctx, f.experimentID, "alice", "", "manual")
	if err != nil {
		t.Fatalf("trigger: %v", err)
	}
	prompt := "p"
	resultRow := &store.EvalRunResult{RunID: runs[0].ID, ExampleID: f.exampleIDs[0], TrialIndex: 0, InputPrompt: &prompt}
	if cerr := f.resultRepo.Create(f.ctx, resultRow); cerr != nil {
		t.Fatalf("create result: %v", cerr)
	}

	if _, gerr := f.svc.GetExecutionData(f.ctx, resultRow.ID); !errors.As(gerr, new(*NotFoundError)) {
		t.Fatalf("expected NotFoundError without storage, got %v", gerr)
	}
	if _, gerr := f.svc.GetTaskEvents(f.ctx, resultRow.ID); !errors.As(gerr, new(*NotFoundError)) {
		t.Fatalf("expected NotFoundError without storage, got %v", gerr)
	}

	mem := storage.NewMemory()
	storageSvc := NewStorageService(mem, "ns")
	svcWithStorage := NewRunService(RunServiceParams{
		Runs: f.runRepo, Results: f.resultRepo, Experiments: f.expRepo,
		Examples: f.exampleRepo, Evaluators: f.evRepo, Storage: storageSvc,
	})

	key, err := storageSvc.UploadExecutionData(runs[0].ID, f.exampleIDs[0], 0, map[string]any{"k": 1})
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	if serr := f.resultRepo.SetExecutionDataKey(f.ctx, resultRow.ID, key); serr != nil {
		t.Fatalf("set key: %v", serr)
	}
	got, err := svcWithStorage.GetExecutionData(f.ctx, resultRow.ID)
	if err != nil {
		t.Fatalf("get exec: %v", err)
	}
	if got["k"] != float64(1) {
		t.Fatalf("got = %v", got)
	}

	eventsKey, err := storageSvc.UploadTaskEvents(runs[0].ID, f.exampleIDs[0], 0, []map[string]any{{"e": "x"}})
	if err != nil {
		t.Fatalf("upload events: %v", err)
	}
	if serr := f.resultRepo.SetTaskEventsKey(f.ctx, resultRow.ID, eventsKey); serr != nil {
		t.Fatalf("set events key: %v", serr)
	}
	events, err := svcWithStorage.GetTaskEvents(f.ctx, resultRow.ID)
	if err != nil {
		t.Fatalf("get events: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("events = %v", events)
	}
}

func TestRunServiceExportJSON(t *testing.T) {
	f := newRunFixture(t, 1)
	runs, err := f.svc.TriggerRuns(f.ctx, f.experimentID, "alice", "", "manual")
	if err != nil {
		t.Fatalf("trigger: %v", err)
	}
	prompt := "the answer"
	resp := "42"
	resultRow := &store.EvalRunResult{RunID: runs[0].ID, ExampleID: f.exampleIDs[0], TrialIndex: 0, InputPrompt: &prompt, AgentResponse: &resp}
	if cerr := f.resultRepo.Create(f.ctx, resultRow); cerr != nil {
		t.Fatalf("create result: %v", cerr)
	}
	out, err := f.svc.ExportRunResultsJSON(f.ctx, runs[0].ID)
	if err != nil {
		t.Fatalf("export json: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("exported %d rows", len(out))
	}
	if out[0]["agent_response"].(*string) != &resp && *(out[0]["agent_response"].(*string)) != "42" {
		if got := *(out[0]["agent_response"].(*string)); got != "42" {
			t.Fatalf("agent_response = %q", got)
		}
	}
}

func TestRunServiceCSVInjectionDefense(t *testing.T) {
	f := newRunFixture(t, 1)
	runs, err := f.svc.TriggerRuns(f.ctx, f.experimentID, "alice", "", "manual")
	if err != nil {
		t.Fatalf("trigger: %v", err)
	}
	prompt := "=cmd|'/c calc'!A1"
	resp := "@harmless"
	resultRow := &store.EvalRunResult{RunID: runs[0].ID, ExampleID: f.exampleIDs[0], TrialIndex: 0, InputPrompt: &prompt, AgentResponse: &resp}
	if cerr := f.resultRepo.Create(f.ctx, resultRow); cerr != nil {
		t.Fatalf("create result: %v", cerr)
	}
	csvOut, err := f.svc.ExportRunResultsCSV(f.ctx, runs[0].ID)
	if err != nil {
		t.Fatalf("export csv: %v", err)
	}
	if !strings.Contains(csvOut, "'=cmd") {
		t.Fatalf("expected '=cmd prefix-armoured, got: %s", csvOut)
	}
	if !strings.Contains(csvOut, "'@harmless") {
		t.Fatalf("expected '@harmless prefix-armoured, got: %s", csvOut)
	}
}

func TestRunServiceExtractResponseStates(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name        string
		payload     map[string]any
		wantText    string
		wantFinal   bool
		wantAgentEr bool
	}{
		{
			name: "completed status",
			payload: map[string]any{
				"result": map[string]any{
					"status": map[string]any{
						"state":   "completed",
						"message": map[string]any{"parts": []any{map[string]any{"kind": "text", "text": "ok"}}},
					},
				},
			},
			wantText: "ok", wantFinal: true,
		},
		{
			name: "final-flag false → not final",
			payload: map[string]any{
				"result": map[string]any{
					"final": false,
				},
			},
			wantText: "", wantFinal: false,
		},
		{
			name: "failed status flagged as agent error",
			payload: map[string]any{
				"result": map[string]any{
					"status": map[string]any{
						"state":   "failed",
						"message": map[string]any{"parts": []any{map[string]any{"kind": "text", "text": "boom"}}},
					},
				},
			},
			wantText: "boom", wantFinal: true, wantAgentEr: true,
		},
		{
			name:        "top-level error",
			payload:     map[string]any{"error": map[string]any{"message": "nope"}},
			wantText:    "nope",
			wantFinal:   true,
			wantAgentEr: true,
		},
		{
			name: "bare parts result",
			payload: map[string]any{
				"result": map[string]any{
					"parts": []any{map[string]any{"kind": "text", "text": "bare"}},
				},
			},
			wantText: "bare", wantFinal: true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ext := extractResponse(c.payload)
			if ext.text != c.wantText || ext.isFinal != c.wantFinal || ext.isAgentError != c.wantAgentEr {
				t.Fatalf("got (%q, %v, %v) want (%q, %v, %v)", ext.text, ext.isFinal, ext.isAgentError, c.wantText, c.wantFinal, c.wantAgentEr)
			}
		})
	}
}

func TestRunServiceCancelRunMissing(t *testing.T) {
	f := newRunFixture(t, 1)
	if _, err := f.svc.CancelRun(f.ctx, idgen.New()); !errors.As(err, new(*NotFoundError)) {
		t.Fatalf("expected NotFoundError, got %v", err)
	}
}

func TestSeedFailsFastOnRepoError(t *testing.T) {
	db := store.TestDB(t)
	repo := store.NewEvalEvaluatorRepo(db)
	if err := db.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if err := SeedBuiltinEvaluators(context.Background(), repo, nil); err == nil {
		t.Fatalf("expected error from closed DB")
	}
}

func isRunActiveRefusal(err error) bool {
	var brv *BusinessRuleViolationError
	return errors.As(err, &brv) && brv.Code == "RUN_IS_ACTIVE"
}

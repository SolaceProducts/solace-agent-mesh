// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eval

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

type runServiceFixture struct {
	ctx          context.Context
	svc          *RunService
	expRepo      *store.EvalExperimentRepo
	runRepo      *store.EvalRunRepo
	resultRepo   *store.EvalRunResultRepo
	scoreRepo    *store.EvalRunScoreRepo
	exampleRepo  *store.EvalExampleRepo
	datasetRepo  *store.EvalDatasetRepo
	evRepo       *store.EvalEvaluatorRepo
	experimentID string
	datasetID    string
	evaluatorID  string
	exampleIDs   []string
}

func newRunFixture(t *testing.T, exampleCount int) *runServiceFixture {
	t.Helper()
	db := store.TestDB(t)
	datasetRepo := store.NewEvalDatasetRepo(db)
	exampleRepo := store.NewEvalExampleRepo(db)
	expRepo := store.NewEvalExperimentRepo(db)
	evRepo := store.NewEvalEvaluatorRepo(db)
	runRepo := store.NewEvalRunRepo(db)
	resultRepo := store.NewEvalRunResultRepo(db)
	scoreRepo := store.NewEvalRunScoreRepo(db)

	ctx := context.Background()
	ds := &store.EvalDataset{Name: "fixture", CreatedBy: "alice", UpdatedBy: "alice"}
	if err := datasetRepo.Create(ctx, ds); err != nil {
		t.Fatalf("create dataset: %v", err)
	}
	exampleIDs := make([]string, 0, exampleCount)
	for i := range exampleCount {
		ex := &store.EvalExample{
			DatasetID:      ds.ID,
			SequenceNumber: i + 1,
			Prompt:         "Prompt " + strings.Repeat("X", i+1),
		}
		if err := exampleRepo.Create(ctx, ex); err != nil {
			t.Fatalf("create example: %v", err)
		}
		exampleIDs = append(exampleIDs, ex.ID)
	}
	htype := "valid_json"
	ev := &store.EvalEvaluator{
		Name:          "valid-json",
		Type:          "heuristic",
		HeuristicType: &htype,
		IsDefault:     true,
		CreatedBy:     "system",
		UpdatedBy:     "system",
	}
	if err := evRepo.Create(ctx, ev); err != nil {
		t.Fatalf("create evaluator: %v", err)
	}
	exp := &store.EvalExperiment{
		Name:           "fixture-exp",
		TargetAgent:    "Orchestrator",
		Models:         "[]",
		RunsPerExample: 1,
		MaxWorkers:     1,
		DatasetID:      &ds.ID,
		CreatedBy:      "alice",
		UpdatedBy:      "alice",
	}
	if err := expRepo.Create(ctx, exp, []string{ev.ID}); err != nil {
		t.Fatalf("create experiment: %v", err)
	}
	svc := NewRunService(RunServiceParams{
		Runs:        runRepo,
		Results:     resultRepo,
		Experiments: expRepo,
		Examples:    exampleRepo,
		Evaluators:  evRepo,
	})
	return &runServiceFixture{
		ctx:          ctx,
		svc:          svc,
		expRepo:      expRepo,
		runRepo:      runRepo,
		resultRepo:   resultRepo,
		scoreRepo:    scoreRepo,
		exampleRepo:  exampleRepo,
		datasetRepo:  datasetRepo,
		evRepo:       evRepo,
		experimentID: exp.ID,
		datasetID:    ds.ID,
		evaluatorID:  ev.ID,
		exampleIDs:   exampleIDs,
	}
}

func TestTriggerRunsCreatesPendingRun(t *testing.T) {
	f := newRunFixture(t, 3)
	runs, err := f.svc.TriggerRuns(f.ctx, f.experimentID, "alice", "first run", "manual")
	if err != nil {
		t.Fatalf("trigger: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("expected 1 run with no models configured, got %d", len(runs))
	}
	run := runs[0]
	if run.Status != StatusPending {
		t.Fatalf("status = %q, want pending", run.Status)
	}
	if run.ProgressTotal != len(f.exampleIDs) {
		t.Fatalf("progress total = %d, want %d", run.ProgressTotal, len(f.exampleIDs))
	}
	if run.RunName == nil || *run.RunName != "first run" {
		t.Fatalf("run name = %v", run.RunName)
	}
	if run.ConfigSnapshot == nil {
		t.Fatalf("config snapshot must be set")
	}
	var snapshot map[string]any
	if err := json.Unmarshal([]byte(*run.ConfigSnapshot), &snapshot); err != nil {
		t.Fatalf("snapshot json: %v", err)
	}
	dataset := snapshot["dataset"].(map[string]any)
	if int(dataset["example_count"].(float64)) != len(f.exampleIDs) {
		t.Fatalf("snapshot example_count = %v", dataset["example_count"])
	}
}

func TestTriggerRunsDefaultsRunNameToExperimentName(t *testing.T) {
	f := newRunFixture(t, 1)
	runs, err := f.svc.TriggerRuns(f.ctx, f.experimentID, "alice", "  ", "manual")
	if err != nil {
		t.Fatalf("trigger: %v", err)
	}
	if runs[0].RunName == nil || *runs[0].RunName != "fixture-exp" {
		t.Fatalf("run name = %v, want fixture-exp", runs[0].RunName)
	}
}

func TestTriggerRunsEmptyDatasetRejected(t *testing.T) {
	f := newRunFixture(t, 0)
	_, err := f.svc.TriggerRuns(f.ctx, f.experimentID, "alice", "", "manual")
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("expected ValidationError, got %T: %v", err, err)
	}
}

func TestTriggerRunsZeroEvaluatorsRejected(t *testing.T) {
	f := newRunFixture(t, 1)
	if err := f.evRepo.Delete(f.ctx, f.evaluatorID); err != nil {
		t.Fatalf("delete evaluator: %v", err)
	}
	_, err := f.svc.TriggerRuns(f.ctx, f.experimentID, "alice", "", "manual")
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("expected ValidationError, got %T: %v", err, err)
	}
	if got := ve.Details["evaluatorIds"]; len(got) == 0 {
		t.Fatalf("validation details missing evaluatorIds: %+v", ve.Details)
	}
}

func TestEvaluatorDeleteCascadesJunction(t *testing.T) {
	f := newRunFixture(t, 1)
	idsBefore, err := f.expRepo.GetEvaluatorIDs(f.ctx, f.experimentID)
	if err != nil {
		t.Fatalf("get evaluator ids: %v", err)
	}
	if len(idsBefore) != 1 {
		t.Fatalf("junction rows before delete = %d, want 1", len(idsBefore))
	}
	if err = f.evRepo.Delete(f.ctx, f.evaluatorID); err != nil {
		t.Fatalf("delete attached evaluator: %v", err)
	}
	idsAfter, err := f.expRepo.GetEvaluatorIDs(f.ctx, f.experimentID)
	if err != nil {
		t.Fatalf("get evaluator ids: %v", err)
	}
	if len(idsAfter) != 0 {
		t.Fatalf("junction rows after delete = %d, want 0", len(idsAfter))
	}
}

func TestTriggerRunsPerModel(t *testing.T) {
	f := newRunFixture(t, 1)
	models, _ := json.Marshal([]map[string]any{
		{"modelConfigId": "alpha"},
		{"modelConfigId": "beta"},
	})
	modelsJSON := string(models)
	if _, err := f.expRepo.Update(f.ctx, f.experimentID, store.EvalExperimentUpdate{
		UpdatedBy: "alice",
		Models:    &modelsJSON,
	}); err != nil {
		t.Fatalf("update experiment models: %v", err)
	}
	runs, err := f.svc.TriggerRuns(f.ctx, f.experimentID, "alice", "", "manual")
	if err != nil {
		t.Fatalf("trigger: %v", err)
	}
	if len(runs) != 2 {
		t.Fatalf("expected one run per model, got %d", len(runs))
	}
	gotModels := map[string]bool{}
	for _, r := range runs {
		gotModels[r.Model] = true
		if r.RunGroupID != runs[0].RunGroupID {
			t.Fatalf("runs in same trigger must share run_group_id")
		}
	}
	if !gotModels["alpha"] || !gotModels["beta"] {
		t.Fatalf("expected runs for alpha and beta, got %v", gotModels)
	}
}

func TestCancelRunRejectsTerminal(t *testing.T) {
	f := newRunFixture(t, 1)
	runs, err := f.svc.TriggerRuns(f.ctx, f.experimentID, "alice", "", "manual")
	if err != nil {
		t.Fatalf("trigger: %v", err)
	}
	runID := runs[0].ID
	if _, uerr := f.runRepo.UpdateStatus(f.ctx, runID, StatusCompleted); uerr != nil {
		t.Fatalf("update status: %v", uerr)
	}
	_, err = f.svc.CancelRun(f.ctx, runID)
	var br *BusinessRuleViolationError
	if !errors.As(err, &br) {
		t.Fatalf("expected BusinessRuleViolationError, got %T: %v", err, err)
	}
}

func TestCancelRunTransitionsToCancelled(t *testing.T) {
	f := newRunFixture(t, 1)
	runs, err := f.svc.TriggerRuns(f.ctx, f.experimentID, "alice", "", "manual")
	if err != nil {
		t.Fatalf("trigger: %v", err)
	}
	resp, err := f.svc.CancelRun(f.ctx, runs[0].ID)
	if err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if resp.Status != StatusCancelled {
		t.Fatalf("status = %q after cancel", resp.Status)
	}
}

func TestRunGroupNotFound(t *testing.T) {
	f := newRunFixture(t, 1)
	_, err := f.svc.ListRunsInGroup(f.ctx, idgen.New())
	var nf *NotFoundError
	if !errors.As(err, &nf) {
		t.Fatalf("expected NotFoundError, got %T: %v", err, err)
	}
}

func TestExportRunResultsCSV(t *testing.T) {
	f := newRunFixture(t, 1)
	runs, err := f.svc.TriggerRuns(f.ctx, f.experimentID, "alice", "", "manual")
	if err != nil {
		t.Fatalf("trigger: %v", err)
	}
	runID := runs[0].ID
	prompt := "what is 2+2?"
	resp := "4"
	dur := 1.25
	resultRow := &store.EvalRunResult{
		RunID:           runID,
		ExampleID:       f.exampleIDs[0],
		TrialIndex:      0,
		InputPrompt:     &prompt,
		AgentResponse:   &resp,
		DurationSeconds: &dur,
	}
	if cerr := f.resultRepo.Create(f.ctx, resultRow); cerr != nil {
		t.Fatalf("create result: %v", cerr)
	}
	passed := true
	score := &store.EvalRunScore{
		RunResultID: resultRow.ID,
		EvaluatorID: f.evaluatorID,
		Score:       1.0,
		Passed:      &passed,
	}
	if cerr := f.scoreRepo.Create(f.ctx, score); cerr != nil {
		t.Fatalf("create score: %v", cerr)
	}
	csvOut, err := f.svc.ExportRunResultsCSV(f.ctx, runID)
	if err != nil {
		t.Fatalf("export csv: %v", err)
	}
	if !strings.Contains(csvOut, "score_valid-json") {
		t.Fatalf("expected score_valid-json column, got: %s", csvOut)
	}
	if !strings.Contains(csvOut, "what is 2+2?") {
		t.Fatalf("expected prompt in CSV: %s", csvOut)
	}
	if !strings.Contains(csvOut, "true") {
		t.Fatalf("expected passed=true in CSV: %s", csvOut)
	}
}

func TestExportRunResultsCSVCarriesTheInputArtifact(t *testing.T) {
	f := newRunFixture(t, 1)
	runs, err := f.svc.TriggerRuns(f.ctx, f.experimentID, "alice", "", "manual")
	if err != nil {
		t.Fatalf("trigger: %v", err)
	}
	runID := runs[0].ID

	meta := `{"filename":"config.yaml","version":2,"content_hash":"h"}`
	expectedMeta := `{"filename":"reference.md","version":0,"content_hash":"e"}`
	withFile := &store.EvalRunResult{
		RunID: runID, ExampleID: f.exampleIDs[0], TrialIndex: 0,
		InputArtifactMetadata: &meta, ExpectedArtifactMetadata: &expectedMeta,
	}
	if cerr := f.resultRepo.Create(f.ctx, withFile); cerr != nil {
		t.Fatalf("create result: %v", cerr)
	}
	textOnly := &store.EvalRunResult{RunID: runID, ExampleID: f.exampleIDs[0], TrialIndex: 1}
	if cerr := f.resultRepo.Create(f.ctx, textOnly); cerr != nil {
		t.Fatalf("create text-only result: %v", cerr)
	}

	csvOut, err := f.svc.ExportRunResultsCSV(f.ctx, runID)
	if err != nil {
		t.Fatalf("export csv: %v", err)
	}
	records, err := csv.NewReader(strings.NewReader(csvOut)).ReadAll()
	if err != nil {
		t.Fatalf("parse csv: %v", err)
	}
	if len(records) != 3 {
		t.Fatalf("rows = %d, want a header plus two results", len(records))
	}
	header := records[0]
	idx := slices.Index(header, "input_artifact")
	if idx == -1 {
		t.Fatalf("header %v has no input_artifact column", header)
	}
	if idx != slices.Index(header, "error_message")+1 {
		t.Errorf("input_artifact at %d, want it immediately after error_message in %v", idx, header)
	}
	expIdx := slices.Index(header, "expected_artifact")
	if expIdx != idx+1 {
		t.Fatalf("expected_artifact at %d, want it immediately after input_artifact in %v", expIdx, header)
	}
	for _, col := range header[expIdx+1:] {
		if !strings.HasPrefix(col, "score_") && !strings.HasPrefix(col, "passed_") {
			t.Errorf("column %q follows expected_artifact; the score_*/passed_* tail must stay contiguous", col)
		}
	}

	cells := map[string]bool{records[1][idx]: true, records[2][idx]: true}
	if !cells["config.yaml@2"] {
		t.Errorf("input_artifact cells = %v, want one reading config.yaml@2", cells)
	}
	if !cells[""] {
		t.Errorf("input_artifact cells = %v, want the text-only trial empty", cells)
	}
	expCells := map[string]bool{records[1][expIdx]: true, records[2][expIdx]: true}
	if !expCells["reference.md@0"] || !expCells[""] {
		t.Errorf("expected_artifact cells = %v, want reference.md@0 and an empty cell", expCells)
	}
}

func TestExportRunResultsJSONCarriesTheInputArtifact(t *testing.T) {
	f := newRunFixture(t, 1)
	runs, err := f.svc.TriggerRuns(f.ctx, f.experimentID, "alice", "", "manual")
	if err != nil {
		t.Fatalf("trigger: %v", err)
	}
	runID := runs[0].ID

	meta := `{"filename":"config.yaml","version":2,"content_hash":"h"}`
	expectedMeta := `{"filename":"reference.md","version":0,"content_hash":"r"}`
	if cerr := f.resultRepo.Create(f.ctx, &store.EvalRunResult{
		RunID: runID, ExampleID: f.exampleIDs[0], TrialIndex: 0,
		InputArtifactMetadata:    &meta,
		ExpectedArtifactMetadata: &expectedMeta,
	}); cerr != nil {
		t.Fatalf("create result: %v", cerr)
	}
	if cerr := f.resultRepo.Create(f.ctx, &store.EvalRunResult{
		RunID: runID, ExampleID: f.exampleIDs[0], TrialIndex: 1,
	}); cerr != nil {
		t.Fatalf("create text-only result: %v", cerr)
	}

	out, err := f.svc.ExportRunResultsJSON(f.ctx, runID)
	if err != nil {
		t.Fatalf("export json: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("rows = %d, want a result with files and a text-only one", len(out))
	}

	counts := map[string]*struct{ named, empty int }{
		"input_artifact":    {},
		"expected_artifact": {},
	}
	wantFile := map[string]struct {
		filename string
		version  int
	}{
		"input_artifact":    {"config.yaml", 2},
		"expected_artifact": {"reference.md", 0},
	}
	for _, dict := range out {
		for key, c := range counts {
			v, ok := dict[key]
			if !ok {
				t.Fatalf("row %v has no %s key", dict, key)
			}
			rec, isRecord := v.(*exampleArtifactRecord)
			if !isRecord {
				t.Fatalf("%s = %T, want *exampleArtifactRecord", key, v)
			}
			want := wantFile[key]
			switch {
			case rec == nil:
				c.empty++
			case rec.Filename == want.filename && rec.Version == want.version:
				c.named++
			default:
				t.Errorf("%s = %+v, want %s@%d or nil", key, rec, want.filename, want.version)
			}
		}
	}
	for key, c := range counts {
		if c.named != 1 || c.empty != 1 {
			t.Errorf("%s values: %d named, %d null; want one of each", key, c.named, c.empty)
		}
	}
}

func TestTriggerRunsHydratesAgentAndExperimentSnapshots(t *testing.T) {
	f := newRunFixture(t, 1)

	models := `[{"modelConfigId":"general","weight":1}]`
	if _, err := f.expRepo.Update(f.ctx, f.experimentID, store.EvalExperimentUpdate{
		Models:    &models,
		UpdatedBy: "test",
	}); err != nil {
		t.Fatalf("update experiment.models: %v", err)
	}

	card := &a2a.AgentCard{
		Name:               "Orchestrator",
		Description:        "test orchestrator",
		DefaultInputModes:  []string{"text"},
		DefaultOutputModes: []string{"text"},
	}
	registry := &fakeRegistry{cards: map[string]*a2a.AgentCard{"Orchestrator": card}}

	resolver := func(id string) (map[string]any, map[string]any) {
		if id != "general" {
			return nil, nil
		}
		return map[string]any{"model": "gpt-4o"},
			map[string]any{"alias": "general", "modelName": "gpt-4o"}
	}

	svc := NewRunService(RunServiceParams{
		Runs:          f.runRepo,
		Results:       f.resultRepo,
		Experiments:   f.expRepo,
		Examples:      f.exampleRepo,
		Evaluators:    f.evRepo,
		AgentRegistry: registry,
		ModelResolver: resolver,
	})

	runs, err := svc.TriggerRuns(f.ctx, f.experimentID, "alice", "", "manual")
	if err != nil {
		t.Fatalf("trigger: %v", err)
	}
	if len(runs) == 0 || runs[0].ConfigSnapshot == nil {
		t.Fatalf("snapshots not populated: %+v", runs)
	}

	var cfg map[string]any
	if err := json.Unmarshal([]byte(*runs[0].ConfigSnapshot), &cfg); err != nil {
		t.Fatalf("config snapshot json: %v", err)
	}

	agent, _ := cfg["agent"].(map[string]any)
	if agent == nil {
		t.Fatalf("config_snapshot.agent is nil; want hydrated. cfg=%v", cfg)
	}
	if agent["origin"] != "discovered" {
		t.Errorf("agent.origin = %v, want 'discovered' (registry path)", agent["origin"])
	}
	if agent["name"] != "Orchestrator" {
		t.Errorf("agent.name = %v", agent["name"])
	}

	exp, _ := cfg["experiment"].(map[string]any)
	rawModels, _ := exp["models"].([]any)
	if len(rawModels) != 1 {
		t.Fatalf("experiment.models = %v, want 1 entry", rawModels)
	}
	m0, _ := rawModels[0].(map[string]any)
	if m0["resolvedModel"] != "gpt-4o" {
		t.Errorf("models[0].resolvedModel = %v, want gpt-4o", m0["resolvedModel"])
	}
	mc, _ := m0["modelConfig"].(map[string]any)
	if mc == nil || mc["modelName"] != "gpt-4o" {
		t.Errorf("models[0].modelConfig = %v", m0["modelConfig"])
	}
}

func TestTriggerRunsCopiesRunAs(t *testing.T) {
	f := newRunFixture(t, 1)
	runAs := "system:eval"
	if _, err := f.expRepo.Update(f.ctx, f.experimentID, store.EvalExperimentUpdate{RunAs: &runAs, UpdatedBy: "alice"}); err != nil {
		t.Fatalf("set run_as: %v", err)
	}

	runs, err := f.svc.TriggerRuns(f.ctx, f.experimentID, "alice", "", "manual")
	if err != nil {
		t.Fatalf("trigger: %v", err)
	}
	for _, run := range runs {
		if run.RunAs == nil || *run.RunAs != runAs {
			t.Errorf("run run_as = %v, want %q", run.RunAs, runAs)
		}
	}

	var snapshot map[string]any
	if err := json.Unmarshal([]byte(*runs[0].ConfigSnapshot), &snapshot); err != nil {
		t.Fatalf("snapshot json: %v", err)
	}
	expDict := snapshot["experiment"].(map[string]any)
	if expDict["run_as"] != runAs {
		t.Errorf("snapshot run_as = %v, want %q", expDict["run_as"], runAs)
	}
}

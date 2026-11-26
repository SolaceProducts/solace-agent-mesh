// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eval

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func tokenUsageResult() map[string]any {
	return map[string]any{
		"status": map[string]any{
			"state":   "completed",
			"message": map[string]any{"parts": []any{map[string]any{"text": "done"}}},
		},
		"metadata": map[string]any{
			"token_usage": map[string]any{
				"total_input_tokens":        float64(100),
				"total_output_tokens":       float64(40),
				"total_cached_input_tokens": float64(20),
				"total_reasoning_tokens":    float64(15),
				"total_tokens":              float64(140),
			},
		},
	}
}

func wantPtr(t *testing.T, got *int64, want int64, field string) {
	t.Helper()
	if got == nil {
		t.Errorf("%s = nil, want %d", field, want)
		return
	}
	if *got != want {
		t.Errorf("%s = %d, want %d", field, *got, want)
	}
}

func TestExtractEvalTokenUsage(t *testing.T) {
	got := extractEvalTokenUsage(tokenUsageResult())
	if got == nil {
		t.Fatalf("expected non-nil token usage")
	}
	wantPtr(t, got.Input, 100, "input")
	wantPtr(t, got.Output, 40, "output")
	wantPtr(t, got.Cached, 20, "cached")
	wantPtr(t, got.Reasoning, 15, "reasoning")
	wantPtr(t, got.Total, 140, "total")

	preUpgradeBlock := map[string]any{"metadata": map[string]any{"token_usage": map[string]any{
		"total_input_tokens":  float64(50),
		"total_output_tokens": float64(10),
	}}}
	pu := extractEvalTokenUsage(preUpgradeBlock)
	if pu == nil {
		t.Fatalf("expected non-nil for a partial block")
	}
	wantPtr(t, pu.Input, 50, "partial.input")
	if pu.Reasoning != nil {
		t.Errorf("partial.reasoning = %d, want nil (absent key must not fabricate 0)", *pu.Reasoning)
	}
	if pu.Cached != nil || pu.Total != nil {
		t.Errorf("partial cached/total should be nil when absent, got %v/%v", pu.Cached, pu.Total)
	}

	if tu := extractEvalTokenUsage(map[string]any{"status": map[string]any{"state": "completed"}}); tu != nil {
		t.Errorf("expected nil when metadata absent, got %+v", tu)
	}
	if tu := extractEvalTokenUsage(map[string]any{"metadata": map[string]any{}}); tu != nil {
		t.Errorf("expected nil when token_usage absent, got %+v", tu)
	}
	empty := map[string]any{"metadata": map[string]any{"token_usage": map[string]any{"unrelated": float64(1)}}}
	if tu := extractEvalTokenUsage(empty); tu != nil {
		t.Errorf("expected nil when no known keys present, got %+v", tu)
	}
}

func TestExtractResponse_CapturesTokenUsage(t *testing.T) {
	payload := map[string]any{"result": tokenUsageResult()}
	ext := extractResponse(payload)
	if !ext.isFinal {
		t.Fatalf("expected final response")
	}
	if ext.tokenUsage == nil {
		t.Fatalf("expected token usage on extractedResponse")
	}
	wantPtr(t, ext.tokenUsage.Reasoning, 15, "reasoning")
	wantPtr(t, ext.tokenUsage.Cached, 20, "cached")

	noUsage := map[string]any{"result": map[string]any{
		"status": map[string]any{"state": "completed", "message": map[string]any{"parts": []any{}}},
	}}
	if ext := extractResponse(noUsage); ext.tokenUsage != nil {
		t.Errorf("expected nil tokenUsage when absent, got %+v", ext.tokenUsage)
	}
}

func TestTokenUsageFromRow(t *testing.T) {
	row := &store.EvalRunResult{
		InputTokens:       new(int64(100)),
		OutputTokens:      new(int64(40)),
		CachedInputTokens: new(int64(20)),
		ReasoningTokens:   new(int64(15)),
		TotalTokens:       new(int64(140)),
	}
	tu := tokenUsageFromRow(row)
	if tu == nil {
		t.Fatalf("expected non-nil TokenUsage")
	}
	wantPtr(t, tu.InputTokens, 100, "input")
	wantPtr(t, tu.OutputTokens, 40, "output")
	wantPtr(t, tu.CachedInputTokens, 20, "cached")
	wantPtr(t, tu.ReasoningTokens, 15, "reasoning")
	wantPtr(t, tu.TotalTokens, 140, "total")

	partial := tokenUsageFromRow(&store.EvalRunResult{InputTokens: new(int64(50)), OutputTokens: new(int64(10))})
	if partial == nil {
		t.Fatalf("expected non-nil for a partial row")
	}
	wantPtr(t, partial.InputTokens, 50, "partial.input")
	if partial.ReasoningTokens != nil {
		t.Errorf("partial.reasoning = %d, want nil (absent column must not fabricate 0)", *partial.ReasoningTokens)
	}

	zero := &store.EvalRunResult{
		InputTokens:       new(int64(0)),
		OutputTokens:      new(int64(0)),
		CachedInputTokens: new(int64(0)),
		ReasoningTokens:   new(int64(0)),
		TotalTokens:       new(int64(0)),
	}
	z := tokenUsageFromRow(zero)
	if z == nil {
		t.Fatalf("a row with present zero columns must produce a non-nil (real 0) TokenUsage")
	}
	wantPtr(t, z.InputTokens, 0, "zero.input")

	if tu := tokenUsageFromRow(&store.EvalRunResult{}); tu != nil {
		t.Errorf("expected nil TokenUsage for a row with no token columns, got %+v", tu)
	}
}

func TestTokenUsageJSONShape(t *testing.T) {
	raw, err := json.Marshal(&TokenUsage{InputTokens: new(int64(0)), OutputTokens: new(int64(7))})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	v, present := got["inputTokens"]
	if !present {
		t.Errorf("inputTokens key missing — a real 0 must serialize as 0, not be omitted: %s", raw)
	} else if v != float64(0) {
		t.Errorf("inputTokens = %v, want 0", v)
	}
	if v, present := got["reasoningTokens"]; !present || v != nil {
		t.Errorf("reasoningTokens = %v (present=%v), want an explicit null for unknown: %s", v, present, raw)
	}
}

func TestSummarizeModelTokens(t *testing.T) {
	results := []RunResultResponse{
		{TokenUsage: &TokenUsage{InputTokens: new(int64(100)), OutputTokens: new(int64(40)), CachedInputTokens: new(int64(10)), ReasoningTokens: new(int64(4)), TotalTokens: new(int64(140))}},
		{TokenUsage: &TokenUsage{InputTokens: new(int64(200)), OutputTokens: new(int64(60)), CachedInputTokens: new(int64(30)), ReasoningTokens: new(int64(8)), TotalTokens: new(int64(260))}},
		{TokenUsage: nil},
	}
	sum := summarizeModelTokens(results)
	if sum.UsageResultCount != 2 {
		t.Errorf("UsageResultCount = %d, want 2 (only results with usage)", sum.UsageResultCount)
	}
	wantMean := func(got *float64, want float64, field string) {
		t.Helper()
		if got == nil {
			t.Errorf("%s = nil, want %v", field, want)
			return
		}
		if *got != want {
			t.Errorf("%s = %v, want %v", field, *got, want)
		}
	}
	wantMean(sum.MeanInputTokens, 150, "MeanInputTokens")
	wantMean(sum.MeanOutputTokens, 50, "MeanOutputTokens")
	wantMean(sum.MeanCachedInputTokens, 20, "MeanCachedInputTokens")
	wantMean(sum.MeanReasoningTokens, 6, "MeanReasoningTokens")
	wantMean(sum.MeanTotalTokens, 200, "MeanTotalTokens")

	partial := []RunResultResponse{
		{TokenUsage: &TokenUsage{InputTokens: new(int64(100)), ReasoningTokens: new(int64(10))}},
		{TokenUsage: &TokenUsage{InputTokens: new(int64(300))}},
	}
	ps := summarizeModelTokens(partial)
	if ps.UsageResultCount != 2 {
		t.Errorf("partial UsageResultCount = %d, want 2", ps.UsageResultCount)
	}
	wantMean(ps.MeanInputTokens, 200, "partial MeanInputTokens")
	wantMean(ps.MeanReasoningTokens, 10, "partial MeanReasoningTokens")
	if ps.MeanOutputTokens != nil {
		t.Errorf("partial MeanOutputTokens = %v, want nil (no result reported output)", *ps.MeanOutputTokens)
	}

	empty := summarizeModelTokens([]RunResultResponse{{TokenUsage: nil}})
	if empty.UsageResultCount != 0 || empty.MeanInputTokens != nil {
		t.Errorf("empty summary = %+v, want count 0 and nil means", empty)
	}
}

func TestHandleResponse_PersistsTokenUsage(t *testing.T) {
	db := store.TestDB(t)
	datasetRepo := store.NewEvalDatasetRepo(db)
	exampleRepo := store.NewEvalExampleRepo(db)
	expRepo := store.NewEvalExperimentRepo(db)
	runRepo := store.NewEvalRunRepo(db)
	resultRepo := store.NewEvalRunResultRepo(db)
	scoreRepo := store.NewEvalRunScoreRepo(db)

	ctx := t.Context()
	ds := &store.EvalDataset{Name: "harvest", CreatedBy: "a", UpdatedBy: "a"}
	if err := datasetRepo.Create(ctx, ds); err != nil {
		t.Fatalf("create dataset: %v", err)
	}
	ex := &store.EvalExample{DatasetID: ds.ID, SequenceNumber: 1, Prompt: "hi"}
	if err := exampleRepo.Create(ctx, ex); err != nil {
		t.Fatalf("create example: %v", err)
	}
	exp := &store.EvalExperiment{
		Name: "harvest-exp", TargetAgent: "Orchestrator", Models: "[]",
		RunsPerExample: 1, MaxWorkers: 1, DatasetID: &ds.ID,
		CreatedBy: "a", UpdatedBy: "a",
	}
	if err := expRepo.Create(ctx, exp, nil); err != nil {
		t.Fatalf("create experiment: %v", err)
	}
	run := &store.EvalRun{ExperimentID: exp.ID, Status: StatusRunning, ProgressTotal: 1, Source: "manual", StartedBy: "a"}
	if err := runRepo.Create(ctx, run); err != nil {
		t.Fatalf("create run: %v", err)
	}

	svc := NewExecutionService(ExecutionServiceParams{
		Namespace: "ns", Runs: runRepo, Results: resultRepo, Scores: scoreRepo,
		Experiments: expRepo, Examples: exampleRepo,
	})

	taskID := "task-harvest-1"
	task := &pendingTask{exampleID: ex.ID, trialIndex: 0, example: ex, startedAt: time.Now()}
	state := &runState{
		pending: map[string]*pendingTask{taskID: task},
	}

	payload := map[string]any{"result": map[string]any{
		"final":  true,
		"status": map[string]any{"state": "completed", "message": map[string]any{"parts": []any{map[string]any{"text": "ok"}}}},
		"metadata": map[string]any{"token_usage": map[string]any{
			"total_input_tokens":        float64(120),
			"total_output_tokens":       float64(30),
			"total_cached_input_tokens": float64(10),
			"total_reasoning_tokens":    float64(9),
			"total_tokens":              float64(150),
		}},
	}}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	svc.handleResponse(ctx, run.ID, exp, "Orchestrator", nil, BrokerResponse{Topic: "ns/status/" + taskID, Payload: raw}, state)
	state.scoreWG.Wait()

	results, err := resultRepo.ListByRun(ctx, run.ID)
	if err != nil {
		t.Fatalf("list results: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("results = %d, want 1", len(results))
	}
	r := results[0]
	wantPtr(t, r.InputTokens, 120, "persisted input")
	wantPtr(t, r.OutputTokens, 30, "persisted output")
	wantPtr(t, r.CachedInputTokens, 10, "persisted cached")
	wantPtr(t, r.ReasoningTokens, 9, "persisted reasoning")
	wantPtr(t, r.TotalTokens, 150, "persisted total")
}

func TestGetRunResultsForRun_SurfacesTokenUsage(t *testing.T) {
	f := newRunFixture(t, 2)
	runs, err := f.svc.TriggerRuns(f.ctx, f.experimentID, "alice", "", "manual")
	if err != nil {
		t.Fatalf("trigger: %v", err)
	}
	runID := runs[0].ID

	withTokens := &store.EvalRunResult{
		RunID:             runID,
		ExampleID:         f.exampleIDs[0],
		TrialIndex:        0,
		InputTokens:       new(int64(100)),
		OutputTokens:      new(int64(40)),
		CachedInputTokens: new(int64(20)),
		ReasoningTokens:   new(int64(15)),
		TotalTokens:       new(int64(140)),
	}
	noTokens := &store.EvalRunResult{
		RunID:      runID,
		ExampleID:  f.exampleIDs[1],
		TrialIndex: 0,
	}
	for _, r := range []*store.EvalRunResult{withTokens, noTokens} {
		if cerr := f.resultRepo.Create(f.ctx, r); cerr != nil {
			t.Fatalf("create result: %v", cerr)
		}
	}

	results, err := f.svc.GetRunResultsForRun(f.ctx, runID)
	if err != nil {
		t.Fatalf("GetRunResultsForRun: %v", err)
	}
	byExample := make(map[string]RunResultResponse, len(results))
	for _, r := range results {
		byExample[r.ExampleID] = r
	}

	got := byExample[f.exampleIDs[0]]
	if got.TokenUsage == nil {
		t.Fatalf("expected TokenUsage on the row with tokens, got nil")
	}
	wantPtr(t, got.TokenUsage.InputTokens, 100, "input")
	wantPtr(t, got.TokenUsage.OutputTokens, 40, "output")
	wantPtr(t, got.TokenUsage.CachedInputTokens, 20, "cached")
	wantPtr(t, got.TokenUsage.ReasoningTokens, 15, "reasoning")
	wantPtr(t, got.TokenUsage.TotalTokens, 140, "total")
	if byExample[f.exampleIDs[1]].TokenUsage != nil {
		t.Errorf("expected nil TokenUsage on the row without tokens, got %+v", byExample[f.exampleIDs[1]].TokenUsage)
	}
}

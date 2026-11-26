// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eval

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func TestDefaultExecutionConfig(t *testing.T) {
	t.Parallel()
	cfg := DefaultExecutionConfig()
	if cfg.ExampleBudget != 600*time.Second {
		t.Fatalf("ExampleBudget = %v", cfg.ExampleBudget)
	}
	if cfg.WatchdogInterval != 120*time.Second {
		t.Fatalf("WatchdogInterval = %v", cfg.WatchdogInterval)
	}
	if cfg.ScoringConcurrency != 8 {
		t.Fatalf("ScoringConcurrency = %v", cfg.ScoringConcurrency)
	}
	if cfg.DBOpTimeout != 300*time.Second {
		t.Fatalf("DBOpTimeout = %v", cfg.DBOpTimeout)
	}
	if cfg.EvaluateConcurrency != 64 {
		t.Fatalf("EvaluateConcurrency = %v", cfg.EvaluateConcurrency)
	}
}

func TestLoadExecutionConfigFromEnv(t *testing.T) {
	t.Run("all overrides applied", func(t *testing.T) {
		t.Setenv("EVAL_EXAMPLE_BUDGET_SECONDS", "60")
		t.Setenv("EVAL_WATCHDOG_INTERVAL_SECONDS", "5")
		t.Setenv("EVAL_SCORING_THREADS", "4")
		t.Setenv("EVAL_EVALUATE_CONCURRENCY", "32")
		t.Setenv("EVAL_DB_OP_TIMEOUT_SECONDS", "12")
		t.Setenv("EVAL_LIVENESS_POLL_SECONDS", "7")
		cfg := LoadExecutionConfigFromEnv(nil)
		if cfg.ExampleBudget != 60*time.Second {
			t.Errorf("ExampleBudget = %v", cfg.ExampleBudget)
		}
		if cfg.WatchdogInterval != 5*time.Second {
			t.Errorf("WatchdogInterval = %v", cfg.WatchdogInterval)
		}
		if cfg.ScoringConcurrency != 4 {
			t.Errorf("ScoringConcurrency = %v", cfg.ScoringConcurrency)
		}
		if cfg.EvaluateConcurrency != 32 {
			t.Errorf("EvaluateConcurrency = %v", cfg.EvaluateConcurrency)
		}
		if cfg.DBOpTimeout != 12*time.Second {
			t.Errorf("DBOpTimeout = %v", cfg.DBOpTimeout)
		}
		if cfg.LivenessInterval != 7*time.Second {
			t.Errorf("LivenessInterval = %v", cfg.LivenessInterval)
		}
	})

	t.Run("invalid values fall back to defaults", func(t *testing.T) {
		t.Setenv("EVAL_EXAMPLE_BUDGET_SECONDS", "-1")
		t.Setenv("EVAL_WATCHDOG_INTERVAL_SECONDS", "0")
		t.Setenv("EVAL_SCORING_THREADS", "abc")
		t.Setenv("EVAL_EVALUATE_CONCURRENCY", "0")
		t.Setenv("EVAL_DB_OP_TIMEOUT_SECONDS", "not-a-number")
		t.Setenv("EVAL_LIVENESS_POLL_SECONDS", "-5")
		cfg := LoadExecutionConfigFromEnv(nil)
		def := DefaultExecutionConfig()
		if cfg.ExampleBudget != def.ExampleBudget {
			t.Errorf("ExampleBudget = %v, want default %v", cfg.ExampleBudget, def.ExampleBudget)
		}
		if cfg.WatchdogInterval != def.WatchdogInterval {
			t.Errorf("WatchdogInterval = %v, want default %v", cfg.WatchdogInterval, def.WatchdogInterval)
		}
		if cfg.ScoringConcurrency != def.ScoringConcurrency {
			t.Errorf("ScoringConcurrency = %v, want default %v", cfg.ScoringConcurrency, def.ScoringConcurrency)
		}
		if cfg.EvaluateConcurrency != def.EvaluateConcurrency {
			t.Errorf("EvaluateConcurrency = %v, want default %v", cfg.EvaluateConcurrency, def.EvaluateConcurrency)
		}
		if cfg.DBOpTimeout != def.DBOpTimeout {
			t.Errorf("DBOpTimeout = %v, want default %v", cfg.DBOpTimeout, def.DBOpTimeout)
		}
		if cfg.LivenessInterval != def.LivenessInterval {
			t.Errorf("LivenessInterval = %v, want default %v", cfg.LivenessInterval, def.LivenessInterval)
		}
	})

	t.Run("empty env returns defaults", func(t *testing.T) {
		t.Setenv("EVAL_EXAMPLE_BUDGET_SECONDS", "")
		t.Setenv("EVAL_WATCHDOG_INTERVAL_SECONDS", "")
		t.Setenv("EVAL_SCORING_THREADS", "")
		cfg := LoadExecutionConfigFromEnv(nil)
		def := DefaultExecutionConfig()
		if cfg != def {
			t.Errorf("got %+v, want default %+v", cfg, def)
		}
	})
}

func TestFloatSecondsPtr(t *testing.T) {
	t.Parallel()
	got := floatSecondsPtr(2 * time.Second)
	if got == nil || *got != 2.0 {
		t.Fatalf("got = %v", got)
	}
}

func TestWrapEvalErr(t *testing.T) {
	t.Parallel()
	base := errors.New("inner")
	wrapped := wrapEvalErr("op", base)
	if !errors.Is(wrapped, base) {
		t.Fatalf("expected wrapped error to wrap base")
	}
	if wrapped.Error() != "op: inner" {
		t.Fatalf("got = %q", wrapped.Error())
	}
}

func TestEvaluatorFromSnapshotMinimum(t *testing.T) {
	t.Parallel()
	if got := evaluatorFromSnapshot(map[string]any{"name": "x"}); got != nil {
		t.Fatalf("missing id should return nil, got %+v", got)
	}
	if got := evaluatorFromSnapshot(map[string]any{"id": "x"}); got != nil {
		t.Fatalf("missing name should return nil, got %+v", got)
	}
	got := evaluatorFromSnapshot(map[string]any{
		"id":              "abc",
		"name":            "Factuality",
		"type":            "llm_judge",
		"heuristic_type":  "valid_json",
		"model":           "gpt-4o",
		"prompt_template": "User: {{Prompt}} → {{Response}}",
		"choice_scores":   map[string]any{"Yes": 1.0, "No": 0.0},
	})
	if got == nil {
		t.Fatalf("expected snapshot to parse")
	}
	if got.ID != "abc" || got.Name != "Factuality" || got.Type != "llm_judge" {
		t.Fatalf("missing primary fields: %+v", got)
	}
	if got.HeuristicType == nil || *got.HeuristicType != "valid_json" {
		t.Fatalf("heuristic_type = %v", got.HeuristicType)
	}
	if got.Model == nil || *got.Model != "gpt-4o" {
		t.Fatalf("model = %v", got.Model)
	}
	if got.ChoiceScores == nil {
		t.Fatalf("choice_scores must be set")
	}
}

func TestEvaluatorFromSnapshotPrefersModelConfigRef(t *testing.T) {
	t.Parallel()
	got := evaluatorFromSnapshot(map[string]any{
		"id":                  "abc",
		"name":                "Factuality",
		"type":                "llm_judge",
		"model":               "205a20eb-93f5-48ec-9629-6d470912a964",
		"resolved_model_name": "claude-sonnet-4-6",
	})
	if got == nil {
		t.Fatalf("expected snapshot to parse")
	}
	if got.Model == nil || *got.Model != "205a20eb-93f5-48ec-9629-6d470912a964" {
		t.Fatalf("model = %v, want the model-config reference", got.Model)
	}
}

func TestEvaluatorFromSnapshotFallsBackToModelWhenResolvedAbsent(t *testing.T) {
	t.Parallel()
	got := evaluatorFromSnapshot(map[string]any{
		"id":    "abc",
		"name":  "Factuality",
		"type":  "llm_judge",
		"model": "gpt-4o",
	})
	if got == nil {
		t.Fatalf("expected snapshot to parse")
	}
	if got.Model == nil || *got.Model != "gpt-4o" {
		t.Fatalf("model = %v, want gpt-4o (fallback)", got.Model)
	}
}

func TestEvaluatorFromSnapshotFallsBackWhenResolvedEmpty(t *testing.T) {
	t.Parallel()
	got := evaluatorFromSnapshot(map[string]any{
		"id":                  "abc",
		"name":                "Factuality",
		"type":                "llm_judge",
		"model":               "gpt-4o",
		"resolved_model_name": "",
	})
	if got == nil {
		t.Fatalf("expected snapshot to parse")
	}
	if got.Model == nil || *got.Model != "gpt-4o" {
		t.Fatalf("model = %v, want gpt-4o (fallback on empty resolved_model_name)", got.Model)
	}
}

func TestEvaluatorFromSnapshotUsesResolvedWhenModelAbsent(t *testing.T) {
	t.Parallel()
	got := evaluatorFromSnapshot(map[string]any{
		"id":                  "abc",
		"name":                "Factuality",
		"type":                "llm_judge",
		"resolved_model_name": "gpt-4o",
	})
	if got == nil {
		t.Fatalf("expected snapshot to parse")
	}
	if got.Model == nil || *got.Model != "gpt-4o" {
		t.Fatalf("model = %v, want gpt-4o (resolved_model_name used when model absent)", got.Model)
	}
}

func TestSnapshotModelFallback(t *testing.T) {
	t.Parallel()
	discard := slog.New(slog.DiscardHandler)

	if got := snapshotModelFallback(discard, "run", nil); got != nil {
		t.Fatalf("nil snapshot: got %v, want nil", got)
	}
	if got := snapshotModelFallback(discard, "run", ptr("")); got != nil {
		t.Fatalf("empty snapshot: got %v, want nil", got)
	}

	noResolved := `[{"model":"qwen-cfg"},{"model":"gpt-cfg"}]`
	if got := snapshotModelFallback(discard, "run", ptr(noResolved)); got != nil {
		t.Fatalf("no resolved names: got %v, want nil", got)
	}

	valid := `[{"model":"qwen-cfg","resolved_model_name":"openai/qwen3"},` +
		`{"model":"gpt-cfg","resolved_model_name":"gpt-4o"},` +
		`{"model":"jev","type":"decision","decision_config":{"escalate_to":{"model":"judge-cfg","resolved_model_name":"claude-sonnet"}}},` +
		`{"resolved_model_name":"orphan"},{"model":"no-resolved"}]`
	got := snapshotModelFallback(discard, "run", ptr(valid))
	want := map[string]string{"qwen-cfg": "openai/qwen3", "gpt-cfg": "gpt-4o", "judge-cfg": "claude-sonnet"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("got[%q] = %q, want %q", k, got[k], v)
		}
	}

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
	if got := snapshotModelFallback(logger, "run-xyz", ptr(`{not valid json`)); got != nil {
		t.Fatalf("malformed JSON: got %v, want nil", got)
	}
	if !strings.Contains(buf.String(), "snapshot unparseable") {
		t.Fatalf("expected Warn log for unparseable snapshot, got %q", buf.String())
	}
	if !strings.Contains(buf.String(), "run-xyz") {
		t.Fatalf("expected runID in log, got %q", buf.String())
	}
}

type stubLLMClient struct {
	resp *llm.ChatResponse
	err  error
}

func (s *stubLLMClient) ChatCompletion(_ context.Context, _ *llm.ChatRequest) (*llm.ChatResponse, error) {
	return s.resp, s.err
}

func (s *stubLLMClient) ChatCompletionStream(_ context.Context, _ *llm.ChatRequest) (<-chan llm.StreamChunk, error) {
	return nil, errors.New("not implemented")
}

func TestLLMJudgeAdapter(t *testing.T) {
	t.Parallel()
	stub := &stubLLMClient{resp: &llm.ChatResponse{Content: "hi"}}
	adapter := NewLLMJudgeAdapter(stub)
	got, err := adapter.JudgeScore(context.Background(), "model", "sys", "user", nil)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != "hi" {
		t.Fatalf("got = %q", got)
	}

	errStub := &stubLLMClient{err: errors.New("boom")}
	if _, err := NewLLMJudgeAdapter(errStub).JudgeScore(context.Background(), "m", "s", "u", nil); err == nil {
		t.Fatalf("expected error pass-through")
	}

	nilStub := &stubLLMClient{}
	if _, err := NewLLMJudgeAdapter(nilStub).JudgeScore(context.Background(), "m", "s", "u", nil); err == nil {
		t.Fatalf("expected error on nil response")
	}
}

type tempAwareLLMClient struct {
	failWhileTempSet error
	mu               sync.Mutex
	sawTemp          []bool
}

func (c *tempAwareLLMClient) ChatCompletion(_ context.Context, req *llm.ChatRequest) (*llm.ChatResponse, error) {
	c.mu.Lock()
	c.sawTemp = append(c.sawTemp, req.Temperature != nil)
	c.mu.Unlock()
	if req.Temperature != nil && c.failWhileTempSet != nil {
		return nil, c.failWhileTempSet
	}
	return &llm.ChatResponse{Content: "graded"}, nil
}

func (c *tempAwareLLMClient) ChatCompletionStream(_ context.Context, _ *llm.ChatRequest) (<-chan llm.StreamChunk, error) {
	return nil, errors.New("not implemented")
}

func TestJudgeChat_RetriesWithoutTemperatureOnUnsupported(t *testing.T) {
	t.Parallel()
	stub := &tempAwareLLMClient{
		failWhileTempSet: &llm.Error{
			Message:    "`temperature` is deprecated for this model.",
			StatusCode: 400,
			Provider:   "anthropic",
		},
	}
	got, err := NewLLMJudgeAdapter(stub).JudgeScore(context.Background(), "claude-opus-4-8", "sys", "user", nil)
	if err != nil {
		t.Fatalf("expected retry without temperature to succeed, got err: %v", err)
	}
	if got != "graded" {
		t.Fatalf("content = %q, want %q", got, "graded")
	}
	if len(stub.sawTemp) != 2 {
		t.Fatalf("calls = %d, want 2 (initial + retry)", len(stub.sawTemp))
	}
	if !stub.sawTemp[0] {
		t.Fatalf("first call should carry temperature")
	}
	if stub.sawTemp[1] {
		t.Fatalf("retry call should omit temperature")
	}
}

func TestJudgeChat_NoRetryOnUnrelatedError(t *testing.T) {
	t.Parallel()
	stub := &tempAwareLLMClient{
		failWhileTempSet: &llm.Error{Message: "invalid model name", StatusCode: 400},
	}
	if _, err := NewLLMJudgeAdapter(stub).JudgeScore(context.Background(), "m", "s", "u", nil); err == nil {
		t.Fatalf("expected the unrelated 400 to be surfaced")
	}
	if len(stub.sawTemp) != 1 {
		t.Fatalf("calls = %d, want 1 (no retry)", len(stub.sawTemp))
	}
}

func temperatureUnsupportedStub() *tempAwareLLMClient {
	return &tempAwareLLMClient{
		failWhileTempSet: &llm.Error{
			Message:    "`temperature` is deprecated for this model.",
			StatusCode: 400,
			Provider:   "anthropic",
		},
	}
}

func TestRunJudgeClient_CachesTemperatureUnsupportedPerRun(t *testing.T) {
	t.Parallel()
	stub := temperatureUnsupportedStub()
	rjc := newRunJudgeClient(NewLLMJudgeAdapter(stub), nil, nil, 0)
	defer rjc.Close()

	if _, err := rjc.JudgeScore(context.Background(), "claude-opus-4-8", "sys", "user", nil); err != nil {
		t.Fatalf("first grade: %v", err)
	}
	if len(stub.sawTemp) != 2 {
		t.Fatalf("first call issued %d requests, want 2 (probe + retry)", len(stub.sawTemp))
	}

	if _, err := rjc.JudgeScore(context.Background(), "claude-opus-4-8", "sys", "user", nil); err != nil {
		t.Fatalf("second grade: %v", err)
	}
	if len(stub.sawTemp) != 3 {
		t.Fatalf("after second call total requests = %d, want 3 (no second probe)", len(stub.sawTemp))
	}
	if stub.sawTemp[2] {
		t.Fatalf("second call should skip the temperature probe")
	}
}

func TestRunJudgeClient_ReprobesTemperatureOnFreshRun(t *testing.T) {
	t.Parallel()
	stub1 := temperatureUnsupportedStub()
	rjc1 := newRunJudgeClient(NewLLMJudgeAdapter(stub1), nil, nil, 0)
	defer rjc1.Close()
	if _, err := rjc1.JudgeScore(context.Background(), "claude-opus-4-8", "sys", "user", nil); err != nil {
		t.Fatalf("run 1 grade: %v", err)
	}

	stub2 := temperatureUnsupportedStub()
	rjc2 := newRunJudgeClient(NewLLMJudgeAdapter(stub2), nil, nil, 0)
	defer rjc2.Close()
	if _, err := rjc2.JudgeScore(context.Background(), "claude-opus-4-8", "sys", "user", nil); err != nil {
		t.Fatalf("run 2 grade: %v", err)
	}
	if len(stub2.sawTemp) != 2 {
		t.Fatalf("fresh run issued %d requests, want 2 (probe + retry)", len(stub2.sawTemp))
	}
	if !stub2.sawTemp[0] {
		t.Fatalf("fresh run's first request should carry temperature (re-probe)")
	}
}

func TestRunJudgeClient_UnrelatedErrorDoesNotCacheSkip(t *testing.T) {
	t.Parallel()
	stub := &tempAwareLLMClient{
		failWhileTempSet: &llm.Error{Message: "invalid model name", StatusCode: 400},
	}
	rjc := newRunJudgeClient(NewLLMJudgeAdapter(stub), nil, nil, 0)
	defer rjc.Close()

	if _, err := rjc.JudgeScore(context.Background(), "some-model", "sys", "user", nil); err == nil {
		t.Fatalf("expected the unrelated 400 to surface")
	}
	if _, err := rjc.JudgeScore(context.Background(), "some-model", "sys", "user", nil); err == nil {
		t.Fatalf("expected the unrelated 400 to surface on the second call too")
	}
	if len(stub.sawTemp) != 2 {
		t.Fatalf("calls = %d, want 2 (each probes with temperature)", len(stub.sawTemp))
	}
	for i, sawTemp := range stub.sawTemp {
		if !sawTemp {
			t.Fatalf("call %d skipped temperature; an unrelated error must not cache a skip", i)
		}
	}
}

func TestRunJudgeClient_ConcurrentCallsBoundTemperatureProbes(t *testing.T) {
	t.Parallel()
	stub := temperatureUnsupportedStub()
	rjc := newRunJudgeClient(NewLLMJudgeAdapter(stub), nil, nil, 0)
	defer rjc.Close()

	const callers = 16
	var wg sync.WaitGroup
	errs := make(chan error, callers)
	for range callers {
		wg.Go(func() {
			if _, err := rjc.JudgeScore(context.Background(), "claude-opus-4-8", "sys", "user", nil); err != nil {
				errs <- err
			}
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent grade: %v", err)
	}

	probes := 0
	for _, sawTemp := range stub.sawTemp {
		if sawTemp {
			probes++
		}
	}
	if probes < 1 || probes > callers {
		t.Fatalf("probes = %d, want between 1 and %d (at most one wasted probe per first-wave caller)", probes, callers)
	}

	if _, err := rjc.JudgeScore(context.Background(), "claude-opus-4-8", "sys", "user", nil); err != nil {
		t.Fatalf("post-wave grade: %v", err)
	}
	if stub.sawTemp[len(stub.sawTemp)-1] {
		t.Fatal("call after the concurrent wave settled should skip the temperature probe")
	}
}

func TestFailRun(t *testing.T) {
	db := store.TestDB(t)
	runRepo := store.NewEvalRunRepo(db)
	expRepo := store.NewEvalExperimentRepo(db)
	dsRepo := store.NewEvalDatasetRepo(db)
	exRepo := store.NewEvalExampleRepo(db)
	evRepo := store.NewEvalEvaluatorRepo(db)

	ctx := context.Background()
	ds := &store.EvalDataset{Name: "fail-fixture", CreatedBy: "alice", UpdatedBy: "alice"}
	if err := dsRepo.Create(ctx, ds); err != nil {
		t.Fatalf("create dataset: %v", err)
	}
	if err := exRepo.Create(ctx, &store.EvalExample{DatasetID: ds.ID, SequenceNumber: 1, Prompt: "p"}); err != nil {
		t.Fatalf("create example: %v", err)
	}
	htype := "valid_json"
	ev := &store.EvalEvaluator{Name: "vj", Type: "heuristic", HeuristicType: &htype, IsDefault: true, CreatedBy: "system", UpdatedBy: "system"}
	if err := evRepo.Create(ctx, ev); err != nil {
		t.Fatalf("create evaluator: %v", err)
	}
	exp := &store.EvalExperiment{
		Name: "exp", TargetAgent: "Orchestrator", Models: "[]",
		RunsPerExample: 1, MaxWorkers: 1, DatasetID: &ds.ID,
		CreatedBy: "alice", UpdatedBy: "alice",
	}
	if err := expRepo.Create(ctx, exp, []string{ev.ID}); err != nil {
		t.Fatalf("create experiment: %v", err)
	}
	run := &store.EvalRun{
		ExperimentID:  exp.ID,
		Status:        StatusPending,
		ProgressTotal: 1,
		Source:        "manual",
		StartedBy:     "alice",
	}
	if err := runRepo.Create(ctx, run); err != nil {
		t.Fatalf("create run: %v", err)
	}

	svc := NewExecutionService(ExecutionServiceParams{
		Namespace:   "ns",
		Runs:        runRepo,
		Results:     store.NewEvalRunResultRepo(db),
		Scores:      store.NewEvalRunScoreRepo(db),
		Experiments: expRepo,
		Examples:    exRepo,
	})
	if err := svc.failRun(ctx, run.ID, "boom", nil); err != nil {
		t.Fatalf("failRun: %v", err)
	}
	got, err := runRepo.GetByID(ctx, run.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status != StatusFailed {
		t.Fatalf("status = %q, want failed", got.Status)
	}
	if got.ErrorMessage == nil || *got.ErrorMessage != "boom" {
		t.Fatalf("error message = %v", got.ErrorMessage)
	}
}

func TestDBExecDeadline(t *testing.T) {
	t.Parallel()
	t.Run("deadline fires on long op", func(t *testing.T) {
		svc := &ExecutionService{cfg: ExecutionConfig{DBOpTimeout: 25 * time.Millisecond}}
		err := svc.dbExec(t.Context(), func(ctx context.Context) error {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(200 * time.Millisecond):
				return nil
			}
		})
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("err = %v, want DeadlineExceeded", err)
		}
	})

	t.Run("zero timeout passes through", func(t *testing.T) {
		svc := &ExecutionService{cfg: ExecutionConfig{}}
		called := false
		err := svc.dbExec(t.Context(), func(_ context.Context) error {
			called = true
			return nil
		})
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if !called {
			t.Fatal("closure not called when DBOpTimeout=0")
		}
	})
}

func TestAcquireReleaseEvaluateSlot(t *testing.T) {
	t.Run("cap blocks the over-cap acquirer", func(t *testing.T) {
		svc := NewExecutionService(ExecutionServiceParams{
			Namespace: "ns",
			Config:    ExecutionConfig{EvaluateConcurrency: 2, ExampleBudget: time.Hour, WatchdogInterval: time.Hour},
		})
		ctx := t.Context()
		if !svc.acquireEvaluateSlot(ctx) {
			t.Fatal("first acquire failed")
		}
		if !svc.acquireEvaluateSlot(ctx) {
			t.Fatal("second acquire failed")
		}
		bctx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
		defer cancel()
		if svc.acquireEvaluateSlot(bctx) {
			t.Fatal("third acquire should have blocked under cap=2")
		}
		svc.releaseEvaluateSlot()
		if !svc.acquireEvaluateSlot(ctx) {
			t.Fatal("third acquire failed after release")
		}
		svc.releaseEvaluateSlot()
		svc.releaseEvaluateSlot()
	})

	t.Run("zero cap disables the gate", func(t *testing.T) {
		svc := NewExecutionService(ExecutionServiceParams{
			Namespace: "ns",
			Config:    ExecutionConfig{EvaluateConcurrency: 0, ExampleBudget: time.Hour, WatchdogInterval: time.Hour},
		})
		ctx := t.Context()
		for i := range 100 {
			if !svc.acquireEvaluateSlot(ctx) {
				t.Fatalf("acquire %d failed with cap=0", i)
			}
		}
	})
}

// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eval

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/budget/budgettest"
	"github.com/SolaceProducts/solace-agent-mesh/internal/decision"
	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
	"github.com/SolaceProducts/solace-agent-mesh/internal/spend"
	"github.com/SolaceProducts/solace-agent-mesh/internal/taxonomy"
	"github.com/SolaceProducts/solace-agent-mesh/internal/testing/mockllm"
)

func startDecisionMock(t *testing.T) string {
	t.Helper()
	sc, err := mockllm.ParseScenario([]byte(`{"id":"d","responses":[{"choices":[{"message":{"content":"ok"}}]}]}`))
	if err != nil {
		t.Fatalf("parse scenario: %v", err)
	}
	srv := mockllm.NewMockLLMServer("127.0.0.1:0", 0, 0, slog.Default())
	srv.LoadScenarios(sc)
	if err := srv.Start(); err != nil {
		t.Fatalf("start mock llm: %v", err)
	}
	t.Cleanup(srv.Stop)
	return "http://" + srv.Addr()
}

type staticDecisionResolver struct{ raw map[string]any }

func (s staticDecisionResolver) ResolveDecision(context.Context, string) (map[string]any, error) {
	return s.raw, nil
}

func countingProxy(t *testing.T, target string) (string, *atomic.Int64) {
	t.Helper()
	u, err := url.Parse(target)
	if err != nil {
		t.Fatalf("parse proxy target: %v", err)
	}
	rp := httputil.NewSingleHostReverseProxy(u)
	hits := &atomic.Int64{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		rp.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv.URL, hits
}

type decisionRunFixture struct {
	db               *store.DB
	runRepo          *store.EvalRunRepo
	resultRepo       *store.EvalRunResultRepo
	runID            string
	decisionID       string
	judge            *judgeProbeServer
	decisionAPIBase  string
	decisionHits     *atomic.Int64
	decisionConfigID string
	judgeConfigID    string
	reports          chan reportedRun
}

type reportedRun struct {
	outcome    string
	targetType string
	shape      *platpkg.EvalRunShape
	decision   *platpkg.EvalDecisionFacts
}

type channelRunReporter chan reportedRun

func (c channelRunReporter) EvalRun(_ context.Context, outcome, _, targetType string, shape *platpkg.EvalRunShape, decision *platpkg.EvalDecisionFacts) {
	select {
	case c <- reportedRun{outcome: outcome, targetType: targetType, shape: shape, decision: decision}:
	default:
	}
}

func (fx decisionRunFixture) awaitReport(t *testing.T) reportedRun {
	t.Helper()
	select {
	case r := <-fx.reports:
		return r
	case <-time.After(5 * time.Second):
		t.Fatal("the run reported no eval:run")
		return reportedRun{}
	}
}

func assertDecisionFacts(t *testing.T, got *platpkg.EvalDecisionFacts, want platpkg.EvalDecisionFacts) {
	t.Helper()
	if got == nil {
		t.Fatal("eval:run carried no decision facts for a run that scored")
	}
	if got.Evaluators != want.Evaluators || got.Decided != want.Decided ||
		got.Escalated != want.Escalated || got.Errored != want.Errored {
		t.Errorf("decision facts = %+v, want %+v", *got, want)
	}
	for _, reason := range taxonomy.EscalationReasons {
		if got.EscalatedBy[reason] != want.EscalatedBy[reason] {
			t.Errorf("escalated by %s = %d, want %d", reason, got.EscalatedBy[reason], want.EscalatedBy[reason])
		}
	}
}

type decisionScenarioOptions struct {
	logger           *slog.Logger
	decisionOffAtRun bool
	alsoJudge        bool
	maxWorkers       int
	runsPerExample   int
	recordSpend      bool
}

func runDecisionScenario(t *testing.T, apiKey string, prompts map[string]string) decisionRunFixture {
	t.Helper()
	return runDecisionScenarioWith(t, apiKey, prompts, decisionScenarioOptions{})
}

func runDecisionScenarioLogged(t *testing.T, apiKey string, prompts map[string]string, logger *slog.Logger) decisionRunFixture {
	t.Helper()
	return runDecisionScenarioWith(t, apiKey, prompts, decisionScenarioOptions{logger: logger})
}

func runDecisionScenarioWith(t *testing.T, apiKey string, prompts map[string]string, opts decisionScenarioOptions) decisionRunFixture {
	t.Helper()
	db := store.TestDB(t)
	datasetRepo := store.NewEvalDatasetRepo(db)
	exampleRepo := store.NewEvalExampleRepo(db)
	expRepo := store.NewEvalExperimentRepo(db)
	evRepo := store.NewEvalEvaluatorRepo(db)
	runRepo := store.NewEvalRunRepo(db)
	resultRepo := store.NewEvalRunResultRepo(db)
	scoreRepo := store.NewEvalRunScoreRepo(db)
	ctx := t.Context()

	ds := &store.EvalDataset{Name: "fixture", CreatedBy: "a", UpdatedBy: "a"}
	if err := datasetRepo.Create(ctx, ds); err != nil {
		t.Fatalf("create dataset: %v", err)
	}
	seq := 1
	for prompt := range prompts {
		if err := exampleRepo.Create(ctx, &store.EvalExample{DatasetID: ds.ID, SequenceNumber: seq, Prompt: prompt}); err != nil {
			t.Fatalf("create example: %v", err)
		}
		seq++
	}

	evaluators := NewEvaluatorService(evRepo, nil)
	evaluators.SetFeatureRegistry(decisionFlag(true))
	judge, err := evaluators.Create(ctx, CreateEvaluatorRequest{
		Name: "quality", Description: "judge", Model: new("judge-cfg"),
		PromptTemplate: new("Grade {{Response}}"), ChoiceScores: map[string]any{"Yes": 1.0, "No": 0.0},
	}, "a")
	if err != nil {
		t.Fatalf("create judge: %v", err)
	}
	dec, err := evaluators.Create(ctx, CreateEvaluatorRequest{
		Name: "answers", Description: "Did it answer?", Type: "decision", Model: new("jev"),
		Question: &EvaluatorQuestion{Type: "choice", Instructions: "Does the response answer the prompt?",
			Criteria: []EvaluatorCriterion{{Key: "answered"}, {Key: "missed"}}},
		ChoiceScores:          map[string]any{"answered": 1.0, "missed": 0.0},
		MinConfidence:         new(0.8),
		EscalateToEvaluatorID: &judge.ID,
	}, "a")
	if err != nil {
		t.Fatalf("create decision evaluator: %v", err)
	}
	exp := &store.EvalExperiment{
		Name: "exp", TargetAgent: "Orchestrator", Models: "[]",
		RunsPerExample: max(opts.runsPerExample, 1), MaxWorkers: max(opts.maxWorkers, 1), DatasetID: &ds.ID, CreatedBy: "a", UpdatedBy: "a",
	}
	evaluatorIDs := []string{dec.ID}
	if opts.alsoJudge {
		evaluatorIDs = append(evaluatorIDs, judge.ID)
	}
	if err = expRepo.Create(ctx, exp, evaluatorIDs); err != nil {
		t.Fatalf("create experiment: %v", err)
	}

	br := broker.NewDevBroker()
	if err = br.Connect(ctx); err != nil {
		t.Fatalf("connect broker: %v", err)
	}
	t.Cleanup(func() { _ = br.Disconnect(ctx) })

	decisionConfigID, judgeConfigID := "jev-cfg", "cfg1"
	var publisher *spend.Publisher
	if opts.recordSpend {
		decisionConfigID, judgeConfigID = pricedModel(t, db, "jev"), pricedModel(t, db, "judge-cfg")
		publisher = fileSpendInto(t, db, br)
	}

	decisionBase, decisionHits := countingProxy(t, startDecisionMock(t))
	decisionRaw := map[string]any{
		"model": "jev-1.13.0", "api_base": decisionBase, "api_key": apiKey,
		"model_type": "decision", "decision_provider": decision.ProviderTypeSafe,
	}
	judgeProvider := newJudgeProbeServer(t, `{"choice":"Yes","reasoning":"PROVIDER"}`)
	reports := make(chan reportedRun, 4)
	namespace := "test-ns"
	trigger := NewTriggerPublisher(br, namespace)
	storageSvc := NewStorageService(nil, namespace)
	modelResolver := func(ref string) (map[string]any, map[string]any) {
		switch ref {
		case "jev":
			return decisionRaw, map[string]any{"id": decisionConfigID, "alias": "jev"}
		case "judge-cfg":
			return map[string]any{"model": "openai/qwen3"}, map[string]any{"id": judgeConfigID, "alias": "judge-cfg"}
		}
		return nil, nil
	}
	runSvc := NewRunService(RunServiceParams{
		Runs: runRepo, Results: resultRepo, Experiments: expRepo,
		Examples: exampleRepo, Evaluators: evRepo,
		Trigger: trigger, Storage: storageSvc, ModelResolver: modelResolver,
		DecisionModelLookup: func(_ context.Context, ref string) (map[string]any, map[string]any, error) {
			raw, snap := modelResolver(ref)
			return raw, snap, nil
		},
	})
	exec := NewExecutionService(ExecutionServiceParams{
		Namespace: namespace, Broker: br, BrokerCli: NewBrokerClient(br, namespace, nil, nil), Storage: storageSvc,
		Runs: runRepo, Results: resultRepo, Scores: scoreRepo,
		Experiments: expRepo, Examples: exampleRepo,
		JudgeResolver: &fakeJudgeResolver{
			want: "judge-cfg", configID: judgeConfigID, dedicated: true, raw: providerRaw(judgeProvider.srv.URL),
		},
		SpendPublisher:   publisher,
		BudgetGate:       budgettest.NewGate(),
		DecisionResolver: staticDecisionResolver{raw: decisionRaw},
		FeatureRegistry:  decisionFlag(!opts.decisionOffAtRun),
		Analytics:        channelRunReporter(reports),
		Logger:           opts.logger,
		Config: ExecutionConfig{
			ExampleBudget: 10 * time.Second, WatchdogInterval: 500 * time.Millisecond, ScoringConcurrency: 2,
		},
	})
	agent := newFakeAgent(t, br, namespace, "Orchestrator", prompts)
	t.Cleanup(agent.Stop)
	if err = exec.Start(ctx); err != nil {
		t.Fatalf("start exec: %v", err)
	}
	t.Cleanup(exec.Stop)

	runs, err := runSvc.TriggerRuns(ctx, exp.ID, "a", "decision", "manual")
	if err != nil {
		t.Fatalf("trigger: %v", err)
	}
	fx := decisionRunFixture{
		db: db, runRepo: runRepo, resultRepo: resultRepo, runID: runs[0].ID, decisionID: dec.ID, judge: judgeProvider,
		decisionAPIBase: decisionBase, decisionHits: decisionHits, reports: reports,
		decisionConfigID: decisionConfigID, judgeConfigID: judgeConfigID,
	}
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		row, err := runRepo.GetByID(ctx, fx.runID)
		if err != nil {
			t.Fatalf("get run: %v", err)
		}
		if IsTerminalStatus(row.Status) {
			return fx
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("run did not reach a terminal state within deadline")
	return fx
}

func scoreDetailsByPrompt(t *testing.T, fx decisionRunFixture) map[string]*store.EvalRunScore {
	t.Helper()
	results, err := fx.resultRepo.ListByRun(t.Context(), fx.runID)
	if err != nil {
		t.Fatalf("list results: %v", err)
	}
	out := map[string]*store.EvalRunScore{}
	for _, r := range results {
		prompt := derefOrEmpty(r.InputPrompt)
		if len(r.Scores) != 1 {
			t.Fatalf("result for %q has %d scores, want 1", prompt, len(r.Scores))
		}
		out[prompt] = r.Scores[0]
	}
	return out
}

func decodeDetails(t *testing.T, s *store.EvalRunScore) ScoreDetails {
	t.Helper()
	if s.Details == nil {
		t.Fatalf("score %s has no details", s.ID)
	}
	var d ScoreDetails
	if err := json.Unmarshal([]byte(*s.Details), &d); err != nil {
		t.Fatalf("decode details: %v", err)
	}
	return d
}

func TestExecutionServiceDecisionEvaluator(t *testing.T) {
	const (
		confident = "ping"
		uncertain = "ping " + mockllm.LowConfidenceMarker
	)
	fx := runDecisionScenario(t, "test-key", map[string]string{confident: "pong", uncertain: "pong?"})

	row, err := fx.runRepo.GetByID(t.Context(), fx.runID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if row.Status != StatusCompleted {
		t.Fatalf("status = %q, want completed (err=%v)", row.Status, derefOrEmpty(row.ErrorMessage))
	}
	if fx.decisionHits.Load() == 0 {
		t.Fatal("no request reached the decision API")
	}
	if got := frozenDecisionAPIBase(t, row, fx.decisionID); got != fx.decisionAPIBase {
		t.Fatalf("snapshot resolved_model.api_base = %q, want %q", got, fx.decisionAPIBase)
	}
	scores := scoreDetailsByPrompt(t, fx)

	decided := decodeDetails(t, scores[confident])
	if decided.DecidedBy != "decision" || decided.Escalated || decided.Answer == nil || optionalFloat(decided.Answer.Confidence) != 1.0 {
		t.Fatalf("confident case details = %+v", decided)
	}
	if scores[confident].Choice == nil || *scores[confident].Choice != "answered" || scores[confident].Score != 1 {
		t.Fatalf("confident case score = %v choice = %v", scores[confident].Score, scores[confident].Choice)
	}

	escalated := decodeDetails(t, scores[uncertain])
	if !escalated.Escalated || escalated.EscalationReason != "low_confidence" || escalated.DecidedBy != "llm_judge" ||
		escalated.Answer == nil || optionalFloat(escalated.Answer.Confidence) != mockllm.LowChoiceConfidence {
		t.Fatalf("uncertain case details = %+v", escalated)
	}
	if u := escalated.Usage; u == nil || u.LLMJudge == nil || u.LLMJudge.InputTokens != 1 || u.LLMJudge.OutputTokens != 1 {
		t.Fatalf("escalated case usage = %+v, want the LLM judge's 1/1 tokens", escalated.Usage)
	}
	if fx.judge.hits.Load() != 1 || scores[uncertain].Score != 1 {
		t.Fatalf("judge hits = %d, escalated score = %v; want 1 and the judge's 1.0", fx.judge.hits.Load(), scores[uncertain].Score)
	}

	if row.EvaluatorStats == nil {
		t.Fatal("run has no evaluator_stats")
	}
	var stats map[string]EvaluatorRunStats
	if err := json.Unmarshal([]byte(*row.EvaluatorStats), &stats); err != nil {
		t.Fatalf("decode evaluator_stats: %v", err)
	}
	st := stats[fx.decisionID]
	if st.Scored != 2 || st.Escalated != 1 || st.WithConfidence != 2 ||
		st.EscalatedPercent == nil || *st.EscalatedPercent != 50 || st.MeanConfidence == nil ||
		math.Abs(*st.MeanConfidence-(1+mockllm.LowChoiceConfidence)/2) > 1e-9 {
		t.Fatalf("stats = %+v", st)
	}
	if j := st.JudgeUsage.LLMJudge; j == nil || j.InputTokens != 1 || j.OutputTokens != 1 {
		t.Fatalf("stats judgeUsage.llmJudge = %+v, want the one escalation's 1/1 tokens", j)
	}

	report := fx.awaitReport(t)
	if report.outcome != taxonomy.OutcomeSuccess {
		t.Errorf("eval:run outcome = %q, want success", report.outcome)
	}
	assertDecisionFacts(t, report.decision, platpkg.EvalDecisionFacts{
		Evaluators: 1, Decided: 1, Escalated: 1,
		EscalatedBy: map[string]int{taxonomy.EscalationReasonLowConfidence: 1},
	})
	if report.targetType != taxonomy.EvalTargetTypeAgent {
		t.Errorf("eval:run target type = %q, want agent", report.targetType)
	}
	if shape := report.shape; shape == nil || !slices.Equal(shape.EvaluatorTypes, []string{taxonomy.EvaluatorTypeDecision}) ||
		shape.MultiTrial || len(shape.TrialFailureKinds) != 0 {
		t.Errorf("eval:run shape = %+v, want one decision evaluator, one trial per example and no early end", shape)
	}
}

func TestRunReportsSeveralTrialsAndEveryEvaluatorType(t *testing.T) {
	fx := runDecisionScenarioWith(t, "test-key", map[string]string{"ping": "pong"},
		decisionScenarioOptions{alsoJudge: true, runsPerExample: 2})

	shape := fx.awaitReport(t).shape
	if shape == nil {
		t.Fatal("eval:run carried no shape")
	}
	if !shape.MultiTrial {
		t.Error("eval:run reported one trial per example, want several")
	}
	if want := []string{taxonomy.EvaluatorTypeDecision, taxonomy.EvaluatorTypeLLMJudge}; !slices.Equal(shape.EvaluatorTypes, want) {
		t.Errorf("evaluator types = %v, want %v", shape.EvaluatorTypes, want)
	}
}

func TestDecisionEvaluatorBooksItsSpendToTheEvalSystemUser(t *testing.T) {
	const (
		confident = "ping"
		uncertain = "ping " + mockllm.LowConfidenceMarker
	)
	fx := runDecisionScenarioWith(t, "test-key", map[string]string{confident: "pong", uncertain: "pong?"},
		decisionScenarioOptions{recordSpend: true})

	byConfig := map[string]int{}
	for _, row := range judgeRowsIn(t, fx.db, 3) {
		if row.UserID == nil || *row.UserID != rbac.EvalSystemUserID || row.TaskID == nil {
			t.Errorf("row booked to user %v under task %v, want the eval system user under the trial's task", row.UserID, row.TaskID)
		}
		if row.ModelConfigID == nil {
			t.Fatalf("row %+v names no model configuration", row)
		}
		byConfig[*row.ModelConfigID]++
	}
	if byConfig[fx.decisionConfigID] != 2 || byConfig[fx.judgeConfigID] != 1 {
		t.Errorf("rows per model configuration = %v, want 2 for the decision model %s and 1 for the escalation judge %s",
			byConfig, fx.decisionConfigID, fx.judgeConfigID)
	}
}

func frozenDecisionAPIBase(t *testing.T, row *store.EvalRun, evaluatorID string) string {
	t.Helper()
	if row.EvaluatorSnapshot == nil {
		t.Fatal("run has no evaluator_snapshot")
	}
	var entries []struct {
		ID             string             `json:"id"`
		DecisionConfig *decisionRunConfig `json:"decision_config"`
	}
	if err := json.Unmarshal([]byte(*row.EvaluatorSnapshot), &entries); err != nil {
		t.Fatalf("decode evaluator_snapshot: %v", err)
	}
	for _, e := range entries {
		if e.ID != evaluatorID {
			continue
		}
		if e.DecisionConfig == nil || e.DecisionConfig.ResolvedModel == nil {
			t.Fatalf("evaluator %s snapshot has no decision_config.resolved_model", evaluatorID)
		}
		return e.DecisionConfig.ResolvedModel.APIBase
	}
	t.Fatalf("evaluator %s missing from evaluator_snapshot", evaluatorID)
	return ""
}

func TestExecutionServiceDecisionEvaluatorFlagOffAtRun(t *testing.T) {
	fx := runDecisionScenarioWith(t, "test-key", map[string]string{"ping": "pong"},
		decisionScenarioOptions{decisionOffAtRun: true})

	row, err := fx.runRepo.GetByID(t.Context(), fx.runID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if row.Status != StatusCompletedWithWarnings {
		t.Fatalf("status = %q, want completed_with_warnings (err=%v)", row.Status, derefOrEmpty(row.ErrorMessage))
	}
	score := scoreDetailsByPrompt(t, fx)["ping"]
	if score == nil || score.Reasoning == nil || !strings.Contains(*score.Reasoning, "SAM_FEATURE_EVAL_DECISION_EVALUATOR") {
		t.Fatalf("score = %+v, want reasoning naming SAM_FEATURE_EVAL_DECISION_EVALUATOR", score)
	}
	if n := fx.decisionHits.Load(); n != 0 {
		t.Fatalf("decision API got %d requests with the flag off, want 0", n)
	}
	if fx.judge.hits.Load() != 0 {
		t.Fatal("a disabled decision evaluator must not escalate")
	}
}

func TestExecutionServiceDecisionEvaluatorRejectedKey(t *testing.T) {
	fx := runDecisionScenario(t, mockllm.RejectedDecisionAPIKey, map[string]string{"ping": "pong"})

	row, err := fx.runRepo.GetByID(t.Context(), fx.runID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if row.Status != StatusCompletedWithWarnings {
		t.Fatalf("status = %q, want completed_with_warnings", row.Status)
	}
	if fx.judge.hits.Load() != 0 {
		t.Fatal("an unauthorized decision model must not escalate")
	}
	d := decodeDetails(t, scoreDetailsByPrompt(t, fx)["ping"])
	if d.Escalated || d.ProviderError != string(decision.FailureUnauthorized) {
		t.Fatalf("details = %+v", d)
	}
	if row.EvaluatorStats != nil {
		t.Fatalf("evaluator_stats = %s, want none for a run whose decision scores all errored", *row.EvaluatorStats)
	}
	assertDecisionFacts(t, fx.awaitReport(t).decision, platpkg.EvalDecisionFacts{Evaluators: 1, Errored: 1})
}

func TestDecisionRunReportCountsOnlyDecisionModelCases(t *testing.T) {
	prompts := map[string]string{
		"ping one":   "pong",
		"ping two":   "pong",
		"ping three": "pong",
		"ping four " + mockllm.LowConfidenceMarker: "pong?",
		"ping five " + mockllm.LowConfidenceMarker: "pong?",
		"ping six": fakeAgentFails,
	}
	fx := runDecisionScenarioWith(t, "test-key", prompts, decisionScenarioOptions{alsoJudge: true, maxWorkers: 3})

	assertDecisionFacts(t, fx.awaitReport(t).decision, platpkg.EvalDecisionFacts{
		Evaluators: 1, Decided: 3, Escalated: 2,
		EscalatedBy: map[string]int{taxonomy.EscalationReasonLowConfidence: 2},
	})
}

func TestDecisionEvaluatorLogsOmitCaseContent(t *testing.T) {
	const marker = "CANARY-eval-decision-state-must-not-reach-logs"
	cases := map[string]struct {
		apiKey      string
		prompts     map[string]string
		mustContain string
	}{
		"decided and escalated": {
			apiKey: "test-key",
			prompts: map[string]string{
				marker + " ping": marker + " pong",
				marker + " ping " + mockllm.LowConfidenceMarker: marker + " pong?",
			},
			mustContain: "eval: decision evaluator answered",
		},
		"provider failure": {
			apiKey:      mockllm.RejectedDecisionAPIKey,
			prompts:     map[string]string{marker + " ping": marker + " pong"},
			mustContain: "eval: decision evaluator call failed",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			var buf syncBuffer
			logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
			runDecisionScenarioLogged(t, tc.apiKey, tc.prompts, logger)
			logs := buf.String()
			if !strings.Contains(logs, tc.mustContain) {
				t.Fatalf("the decision path logged no %q line; the canary proves nothing", tc.mustContain)
			}
			for line := range strings.Lines(logs) {
				if strings.Contains(line, tc.mustContain) && !strings.Contains(line, "runID=") {
					t.Fatalf("decision log line has no runID: %s", line)
				}
			}
			if strings.Contains(logs, marker) {
				t.Fatal("case content reached the logs")
			}
		})
	}
}

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

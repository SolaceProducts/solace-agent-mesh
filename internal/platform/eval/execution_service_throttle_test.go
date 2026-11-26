// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eval

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

type countingAgent struct {
	t           *testing.T
	br          broker.Broker
	queue       broker.Queue
	delay       time.Duration
	cancel      context.CancelFunc
	wg          sync.WaitGroup
	inFlight    atomic.Int32
	maxInFlight atomic.Int32
	ignoreFirst int32
	seen        atomic.Int32
	slowFor     time.Duration
	slowNth     int32
	slowAll     bool
	streamEvery time.Duration
}

func newCountingAgent(t *testing.T, br broker.Broker, namespace, agentName string, delay time.Duration) *countingAgent {
	t.Helper()
	q, err := br.CreateDirectSubscription()
	if err != nil {
		t.Fatalf("agent direct sub: %v", err)
	}
	if err := br.Subscribe(q, a2a.AgentRequestTopic(namespace, agentName)); err != nil {
		t.Fatalf("agent subscribe: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	a := &countingAgent{t: t, br: br, queue: q, delay: delay, cancel: cancel}
	a.wg.Add(1)
	go a.run(ctx)
	return a
}

func (a *countingAgent) Stop() {
	a.cancel()
	a.wg.Wait()
}

func (a *countingAgent) run(ctx context.Context) {
	defer a.wg.Done()
	for {
		msg, err := a.br.Receive(ctx, a.queue)
		if err != nil || msg == nil {
			return
		}
		a.wg.Add(1)
		go a.handle(ctx, msg)
	}
}

func (a *countingAgent) hold(ctx context.Context, msg *broker.Message, requestID string) {
	until := time.Now().Add(a.slowFor)
	for time.Now().Before(until) {
		step := time.Until(until)
		if a.streamEvery > 0 {
			step = min(step, a.streamEvery)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(step):
		}
		if a.streamEvery > 0 && msg.ReplyTo != "" && time.Now().Before(until) {
			update, _ := json.Marshal(map[string]any{
				"jsonrpc": "2.0", "id": requestID,
				"result": map[string]any{"final": false, "status": map[string]any{"state": "working"}},
			})
			_ = a.br.Publish(ctx, msg.ReplyTo, &broker.Message{Topic: msg.ReplyTo, Payload: update})
		}
	}
}

func (a *countingAgent) handle(ctx context.Context, msg *broker.Message) {
	defer a.wg.Done()
	var req struct {
		ID     string `json:"id"`
		Method string `json:"method"`
	}
	_ = json.Unmarshal(msg.Payload, &req)
	if req.Method == "tasks/cancel" {
		return
	}
	seq := a.seen.Add(1)
	if seq <= a.ignoreFirst {
		return
	}

	n := a.inFlight.Add(1)
	for {
		m := a.maxInFlight.Load()
		if n <= m || a.maxInFlight.CompareAndSwap(m, n) {
			break
		}
	}
	if a.slowFor > 0 && (a.slowAll || seq == max(a.slowNth, 1)) {
		a.hold(ctx, msg, req.ID)
	}
	time.Sleep(a.delay)
	a.inFlight.Add(-1)

	if msg.ReplyTo == "" {
		return
	}
	response := map[string]any{
		"jsonrpc": "2.0",
		"id":      req.ID,
		"result": map[string]any{
			"final": true,
			"status": map[string]any{
				"state": "completed",
				"message": map[string]any{
					"parts": []map[string]any{{"kind": "text", "text": `{"ok":true}`}},
				},
			},
		},
	}
	body, err := json.Marshal(response)
	if err != nil {
		return
	}
	_ = a.br.Publish(ctx, msg.ReplyTo, &broker.Message{Topic: msg.ReplyTo, Payload: body})
}

type slowJudge struct{ delay time.Duration }

func (j *slowJudge) JudgeScore(ctx context.Context, _, _, _ string, _ *llm.SpendAttribution) (string, error) {
	select {
	case <-time.After(j.delay):
	case <-ctx.Done():
		return "", ctx.Err()
	}
	return `{"choice":"A","reasoning":"ok"}`, nil
}

func TestExecutionServiceHonoursMaxWorkers(t *testing.T) {
	const numExamples, maxWorkers = 8, 2

	db := store.TestDB(t)
	datasetRepo := store.NewEvalDatasetRepo(db)
	exampleRepo := store.NewEvalExampleRepo(db)
	expRepo := store.NewEvalExperimentRepo(db)
	evRepo := store.NewEvalEvaluatorRepo(db)
	runRepo := store.NewEvalRunRepo(db)
	resultRepo := store.NewEvalRunResultRepo(db)

	ctx := context.Background()
	ds := &store.EvalDataset{Name: "throttle-fixture", CreatedBy: "alice", UpdatedBy: "alice"}
	if err := datasetRepo.Create(ctx, ds); err != nil {
		t.Fatalf("create dataset: %v", err)
	}
	for i := range numExamples {
		if err := exampleRepo.Create(ctx, &store.EvalExample{DatasetID: ds.ID, SequenceNumber: i + 1, Prompt: "prompt"}); err != nil {
			t.Fatalf("create example: %v", err)
		}
	}
	heuristic := "valid_json"
	ev := &store.EvalEvaluator{Name: "valid-json", Type: "heuristic", HeuristicType: &heuristic, IsDefault: true, CreatedBy: "system", UpdatedBy: "system"}
	if err := evRepo.Create(ctx, ev); err != nil {
		t.Fatalf("create evaluator: %v", err)
	}
	exp := &store.EvalExperiment{
		Name: "throttle-exp", TargetAgent: "Orchestrator", Models: "[]",
		RunsPerExample: 1, MaxWorkers: maxWorkers, DatasetID: &ds.ID,
		CreatedBy: "alice", UpdatedBy: "alice",
	}
	if err := expRepo.Create(ctx, exp, []string{ev.ID}); err != nil {
		t.Fatalf("create experiment: %v", err)
	}

	br := broker.NewDevBroker()
	if err := br.Connect(ctx); err != nil {
		t.Fatalf("connect broker: %v", err)
	}
	t.Cleanup(func() { _ = br.Disconnect(ctx) })
	namespace := "test-ns"
	storageSvc := NewStorageService(nil, namespace)
	runSvc := NewRunService(RunServiceParams{
		Runs: runRepo, Results: resultRepo, Experiments: expRepo,
		Examples: exampleRepo, Evaluators: evRepo,
		Trigger: NewTriggerPublisher(br, namespace), Storage: storageSvc,
	})
	exec := NewExecutionService(ExecutionServiceParams{
		Namespace: namespace, Broker: br, BrokerCli: NewBrokerClient(br, namespace, nil, nil), Storage: storageSvc,
		Runs: runRepo, Results: resultRepo, Scores: store.NewEvalRunScoreRepo(db),
		Experiments: expRepo, Examples: exampleRepo,
		Config: ExecutionConfig{ExampleBudget: 5 * time.Second, WatchdogInterval: 500 * time.Millisecond},
	})
	agent := newCountingAgent(t, br, namespace, "Orchestrator", 80*time.Millisecond)
	t.Cleanup(agent.Stop)
	if err := exec.Start(ctx); err != nil {
		t.Fatalf("start exec: %v", err)
	}
	t.Cleanup(exec.Stop)

	runs, err := runSvc.TriggerRuns(ctx, exp.ID, "alice", "throttle", "manual")
	if err != nil {
		t.Fatalf("trigger: %v", err)
	}
	runID := runs[0].ID

	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		row, gerr := runRepo.GetByID(ctx, runID)
		if gerr != nil {
			t.Fatalf("get run: %v", gerr)
		}
		if !IsTerminalStatus(row.Status) {
			time.Sleep(50 * time.Millisecond)
			continue
		}
		if row.Status != StatusCompleted {
			t.Fatalf("final status = %q, want completed (err=%v)", row.Status, derefOrEmpty(row.ErrorMessage))
		}
		results, lerr := resultRepo.ListByRun(ctx, runID)
		if lerr != nil {
			t.Fatalf("list results: %v", lerr)
		}
		if len(results) != numExamples {
			t.Fatalf("results = %d, want every example run", len(results))
		}
		if peak := agent.maxInFlight.Load(); peak != maxWorkers {
			t.Fatalf("peak in-flight trials = %d, want exactly maxWorkers (%d)", peak, maxWorkers)
		}
		return
	}
	t.Fatalf("run did not reach a terminal state within deadline")
}

func TestExecutionServiceAbandonsOnlyASilentTrialThatBlocksOthers(t *testing.T) {
	cases := []struct {
		name          string
		run           budgetRun
		agent         func(a *countingAgent)
		wantStatus    string
		wantAbandoned int
		wantPeak      int32
	}{
		{"silent trial blocking the queue", budgetRun{maxWorkers: 1}, func(a *countingAgent) { a.ignoreFirst = 1 }, StatusCompletedWithWarnings, 1, 0},
		{"slow trial with nothing queued", budgetRun{maxWorkers: 4}, func(a *countingAgent) { a.slowFor = 2500 * time.Millisecond }, StatusCompleted, 0, 0},
		{"streaming trial blocking the queue", budgetRun{maxWorkers: 1}, func(a *countingAgent) {
			a.slowFor = 2500 * time.Millisecond
			a.streamEvery = 300 * time.Millisecond
		}, StatusCompleted, 0, 0},
		{"quiet trial within its budget while the queue waits", budgetRun{maxWorkers: 1, examples: 2, budget: 4 * time.Second}, func(a *countingAgent) {
			a.slowFor = 3500 * time.Millisecond
		}, StatusCompleted, 0, 0},
		{"silent trial beside streaming peers", budgetRun{maxWorkers: 2}, func(a *countingAgent) {
			a.ignoreFirst = 1
			a.slowFor = 2500 * time.Millisecond
			a.slowAll = true
			a.streamEvery = 300 * time.Millisecond
		}, StatusCompletedWithWarnings, 1, 0},
		{"two silent trials reaped together", budgetRun{maxWorkers: 2, examples: 6, budget: 1500 * time.Millisecond}, func(a *countingAgent) {
			a.ignoreFirst = 2
			a.slowFor = 150 * time.Millisecond
			a.slowAll = true
		}, StatusCompletedWithWarnings, 2, 2},
		{"silent last trial after the publisher waited", budgetRun{maxWorkers: 1}, func(a *countingAgent) {
			a.slowFor = 2500 * time.Millisecond
			a.slowNth = defaultBudgetExamples
		}, StatusCompleted, 0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			examples := tc.run.exampleCount()
			got := runBudgetFixture(t, tc.run, tc.agent)
			if got.status != tc.wantStatus || got.abandoned != tc.wantAbandoned || got.answered != examples-tc.wantAbandoned {
				t.Fatalf("status %q, %d abandoned, %d answered; want %q, %d abandoned, the rest answered",
					got.status, got.abandoned, got.answered, tc.wantStatus, tc.wantAbandoned)
			}
			if got.progress != examples {
				t.Errorf("progress = %d, want %d: an abandoned trial still counts as done", got.progress, examples)
			}
			if tc.wantPeak > 0 && got.peak != tc.wantPeak {
				t.Errorf("peak in-flight answered trials = %d, want %d", got.peak, tc.wantPeak)
			}
		})
	}
}

const defaultBudgetExamples = 4

type budgetRun struct {
	maxWorkers int
	examples   int
	budget     time.Duration
}

func (r budgetRun) exampleCount() int {
	if r.examples > 0 {
		return r.examples
	}
	return defaultBudgetExamples
}

type budgetOutcome struct {
	status              string
	abandoned, answered int
	progress            int
	peak                int32
}

func runBudgetFixture(t *testing.T, run budgetRun, configure func(a *countingAgent)) budgetOutcome {
	t.Helper()
	maxWorkers, examples, budget := run.maxWorkers, run.exampleCount(), run.budget
	if budget == 0 {
		budget = time.Second
	}
	db := store.TestDB(t)
	datasetRepo := store.NewEvalDatasetRepo(db)
	exampleRepo := store.NewEvalExampleRepo(db)
	expRepo := store.NewEvalExperimentRepo(db)
	evRepo := store.NewEvalEvaluatorRepo(db)
	runRepo := store.NewEvalRunRepo(db)
	resultRepo := store.NewEvalRunResultRepo(db)

	ctx := t.Context()
	ds := &store.EvalDataset{Name: "budget-fixture", CreatedBy: "alice", UpdatedBy: "alice"}
	if err := datasetRepo.Create(ctx, ds); err != nil {
		t.Fatalf("create dataset: %v", err)
	}
	for i := range examples {
		if err := exampleRepo.Create(ctx, &store.EvalExample{DatasetID: ds.ID, SequenceNumber: i + 1, Prompt: "prompt"}); err != nil {
			t.Fatalf("create example: %v", err)
		}
	}
	heuristic := "valid_json"
	ev := &store.EvalEvaluator{Name: "valid-json", Type: "heuristic", HeuristicType: &heuristic, IsDefault: true, CreatedBy: "system", UpdatedBy: "system"}
	if err := evRepo.Create(ctx, ev); err != nil {
		t.Fatalf("create evaluator: %v", err)
	}
	exp := &store.EvalExperiment{
		Name: "budget-exp", TargetAgent: "Orchestrator", Models: "[]",
		RunsPerExample: 1, MaxWorkers: maxWorkers, DatasetID: &ds.ID,
		CreatedBy: "alice", UpdatedBy: "alice",
	}
	if err := expRepo.Create(ctx, exp, []string{ev.ID}); err != nil {
		t.Fatalf("create experiment: %v", err)
	}

	br := broker.NewDevBroker()
	if err := br.Connect(ctx); err != nil {
		t.Fatalf("connect broker: %v", err)
	}
	t.Cleanup(func() { _ = br.Disconnect(context.Background()) })
	namespace := "test-ns"
	storageSvc := NewStorageService(nil, namespace)
	runSvc := NewRunService(RunServiceParams{
		Runs: runRepo, Results: resultRepo, Experiments: expRepo,
		Examples: exampleRepo, Evaluators: evRepo,
		Trigger: NewTriggerPublisher(br, namespace), Storage: storageSvc,
	})
	exec := NewExecutionService(ExecutionServiceParams{
		Namespace: namespace, Broker: br, BrokerCli: NewBrokerClient(br, namespace, nil, nil), Storage: storageSvc,
		Runs: runRepo, Results: resultRepo, Scores: store.NewEvalRunScoreRepo(db),
		Experiments: expRepo, Examples: exampleRepo,
		Config: ExecutionConfig{ExampleBudget: budget, WatchdogInterval: time.Minute},
	})
	agent := newCountingAgent(t, br, namespace, "Orchestrator", 10*time.Millisecond)
	configure(agent)
	t.Cleanup(agent.Stop)
	if err := exec.Start(ctx); err != nil {
		t.Fatalf("start exec: %v", err)
	}
	t.Cleanup(exec.Stop)

	runs, err := runSvc.TriggerRuns(ctx, exp.ID, "alice", "budget", "manual")
	if err != nil {
		t.Fatalf("trigger: %v", err)
	}
	runID := runs[0].ID

	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		row, gerr := runRepo.GetByID(ctx, runID)
		if gerr != nil {
			t.Fatalf("get run: %v", gerr)
		}
		if !IsTerminalStatus(row.Status) {
			time.Sleep(50 * time.Millisecond)
			continue
		}
		results, lerr := resultRepo.ListByRun(ctx, runID)
		if lerr != nil {
			t.Fatalf("list results: %v", lerr)
		}
		out := budgetOutcome{status: row.Status, progress: row.ProgressCurrent, peak: agent.maxInFlight.Load()}
		for _, r := range results {
			switch {
			case r.ErrorMessage == nil:
				out.answered++
			case *r.ErrorMessage == msgTrialSilentWhileBlocking:
				out.abandoned++
			default:
				t.Errorf("unexpected result error: %s", *r.ErrorMessage)
			}
		}
		return out
	}
	t.Fatalf("run did not reach a terminal state within deadline")
	return budgetOutcome{}
}

func TestExecutionServiceConcurrentScoringNoDrop(t *testing.T) {
	const numExamples = 12

	db := store.TestDB(t)
	datasetRepo := store.NewEvalDatasetRepo(db)
	exampleRepo := store.NewEvalExampleRepo(db)
	expRepo := store.NewEvalExperimentRepo(db)
	evRepo := store.NewEvalEvaluatorRepo(db)
	runRepo := store.NewEvalRunRepo(db)
	resultRepo := store.NewEvalRunResultRepo(db)
	scoreRepo := store.NewEvalRunScoreRepo(db)

	ctx := context.Background()
	ds := &store.EvalDataset{Name: "nodrop-fixture", CreatedBy: "alice", UpdatedBy: "alice"}
	if err := datasetRepo.Create(ctx, ds); err != nil {
		t.Fatalf("create dataset: %v", err)
	}
	for i := range numExamples {
		ex := &store.EvalExample{DatasetID: ds.ID, SequenceNumber: i + 1, Prompt: "prompt"}
		if err := exampleRepo.Create(ctx, ex); err != nil {
			t.Fatalf("create example: %v", err)
		}
	}
	cs := `{"Yes": 1.0, "No": 0.0}`
	model := "judge-model"
	tmpl := "User: {{Prompt}}\nResponse: {{Response}}"
	ev := &store.EvalEvaluator{
		Name: "slow-judge", Type: "llm_judge", Model: &model, PromptTemplate: &tmpl, ChoiceScores: &cs,
		IsDefault: true, CreatedBy: "system", UpdatedBy: "system",
	}
	if err := evRepo.Create(ctx, ev); err != nil {
		t.Fatalf("create evaluator: %v", err)
	}
	exp := &store.EvalExperiment{
		Name: "nodrop-exp", TargetAgent: "Orchestrator", Models: "[]",
		RunsPerExample: 1, MaxWorkers: numExamples, DatasetID: &ds.ID,
		CreatedBy: "alice", UpdatedBy: "alice",
	}
	if err := expRepo.Create(ctx, exp, []string{ev.ID}); err != nil {
		t.Fatalf("create experiment: %v", err)
	}

	br := broker.NewDevBroker()
	if err := br.Connect(ctx); err != nil {
		t.Fatalf("connect broker: %v", err)
	}
	t.Cleanup(func() { _ = br.Disconnect(ctx) })

	namespace := "test-ns"
	trigger := NewTriggerPublisher(br, namespace)
	storageSvc := NewStorageService(nil, namespace)
	runSvc := NewRunService(RunServiceParams{
		Runs: runRepo, Results: resultRepo, Experiments: expRepo,
		Examples: exampleRepo, Evaluators: evRepo,
		Trigger: trigger, Storage: storageSvc,
	})
	brokerCli := NewBrokerClient(br, namespace, nil, nil)
	exec := NewExecutionService(ExecutionServiceParams{
		Namespace: namespace, Broker: br, BrokerCli: brokerCli, Storage: storageSvc,
		Runs: runRepo, Results: resultRepo, Scores: scoreRepo,
		Experiments: expRepo, Examples: exampleRepo,
		LLMClient: &slowJudge{delay: 120 * time.Millisecond},
		Config: ExecutionConfig{
			ExampleBudget:    3 * time.Second,
			WatchdogInterval: 500 * time.Millisecond,
		},
	})

	agent := newCountingAgent(t, br, namespace, "Orchestrator", 0)
	t.Cleanup(agent.Stop)

	if err := exec.Start(ctx); err != nil {
		t.Fatalf("start exec: %v", err)
	}
	t.Cleanup(exec.Stop)

	runs, err := runSvc.TriggerRuns(ctx, exp.ID, "alice", "nodrop", "manual")
	if err != nil {
		t.Fatalf("trigger: %v", err)
	}
	runID := runs[0].ID

	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		row, gerr := runRepo.GetByID(ctx, runID)
		if gerr != nil {
			t.Fatalf("get run: %v", gerr)
		}
		if IsTerminalStatus(row.Status) {
			if row.Status != StatusCompleted {
				t.Fatalf("final status = %q, want completed (err=%v)", row.Status, derefOrEmpty(row.ErrorMessage))
			}
			results, lerr := resultRepo.ListByRun(ctx, runID)
			if lerr != nil {
				t.Fatalf("list results: %v", lerr)
			}
			if len(results) != numExamples {
				t.Fatalf("results = %d, want %d", len(results), numExamples)
			}
			for _, r := range results {
				if r.ErrorMessage != nil {
					t.Fatalf("example %s errored (dropped response?): %s", r.ExampleID, *r.ErrorMessage)
				}
				if len(r.Scores) != 1 {
					t.Fatalf("example %s has %d scores, want 1", r.ExampleID, len(r.Scores))
				}
			}
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("run did not reach a terminal state within deadline")
}

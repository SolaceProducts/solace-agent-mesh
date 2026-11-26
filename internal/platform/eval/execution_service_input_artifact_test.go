// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eval

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	artifactmem "github.com/SolaceProducts/solace-agent-mesh/internal/artifact/memory"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func TestAnyEvaluatorReadsArtifacts(t *testing.T) {
	t.Parallel()
	heuristic := &store.EvalEvaluator{Type: "heuristic"}
	judge := &store.EvalEvaluator{Type: "llm_judge"}
	if anyEvaluatorReadsArtifacts([]*store.EvalEvaluator{heuristic, nil}) {
		t.Fatal("a heuristic-only snapshot must not trigger example-file loads")
	}
	if !anyEvaluatorReadsArtifacts([]*store.EvalEvaluator{heuristic, judge}) {
		t.Fatal("a snapshot with an llm_judge evaluator must trigger example-file loads")
	}
}

const (
	exampleFileFixtureInputBody    = "region,revenue\nemea,120\n"
	exampleFileFixtureExpectedBody = "emea grew to 120\n"
)

func driveExampleFileRun(t *testing.T, ev *store.EvalEvaluator) (*capturingJudge, *countingArtifactService) {
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
	ex := &store.EvalExample{DatasetID: ds.ID, SequenceNumber: 1, Prompt: "summarise the attached file"}
	if err := exampleRepo.Create(ctx, ex); err != nil {
		t.Fatalf("create example: %v", err)
	}

	counting := &countingArtifactService{Service: artifactmem.New()}
	scoped := &artifact.ScopedService{Inner: counting, Scope: "namespace", Namespace: "test-ns"}
	uploader := NewExampleArtifactService(ExampleArtifactServiceParams{
		Examples: exampleRepo, ArtifactRows: store.NewEvalExampleArtifactRepo(db),
		Artifacts: scoped, Config: DefaultExampleArtifactConfig(),
	})
	if _, uploadErr := uploader.Set(ctx, ex.ID, store.EvalArtifactRoleInput, "revenue.csv", "text/csv", []byte(exampleFileFixtureInputBody), ""); uploadErr != nil {
		t.Fatalf("attach input file: %v", uploadErr)
	}
	if _, uploadErr := uploader.Set(ctx, ex.ID, store.EvalArtifactRoleExpected, "reference.md", "text/markdown", []byte(exampleFileFixtureExpectedBody), ""); uploadErr != nil {
		t.Fatalf("attach expected file: %v", uploadErr)
	}

	if err := evRepo.Create(ctx, ev); err != nil {
		t.Fatalf("create evaluator: %v", err)
	}
	exp := &store.EvalExperiment{
		Name: "fixture-exp", TargetAgent: "Orchestrator", Models: "[]",
		RunsPerExample: 1, MaxWorkers: 1, DatasetID: &ds.ID,
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

	judge := &capturingJudge{}
	exec := NewExecutionService(ExecutionServiceParams{
		Namespace: namespace, Broker: br, BrokerCli: NewBrokerClient(br, namespace, nil, nil),
		Storage: storageSvc, Artifacts: scoped,
		Runs: runRepo, Results: resultRepo, Scores: scoreRepo,
		Experiments: expRepo, Examples: exampleRepo,
		LLMClient: judge,
		Config: ExecutionConfig{
			ExampleBudget:    10 * time.Second,
			WatchdogInterval: 500 * time.Millisecond, ScoringConcurrency: 1,
		},
	})

	agent := newTranscriptFakeAgent(t, br, namespace, "Orchestrator")
	t.Cleanup(agent.Stop)
	if err := exec.Start(ctx); err != nil {
		t.Fatalf("start exec: %v", err)
	}
	t.Cleanup(exec.Stop)

	runs, triggerErr := runSvc.TriggerRuns(ctx, exp.ID, "alice", "input-file", "manual")
	if triggerErr != nil {
		t.Fatalf("trigger: %v", triggerErr)
	}
	runID := runs[0].ID

	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		row, gerr := runRepo.GetByID(ctx, runID)
		if gerr != nil {
			t.Fatalf("get run: %v", gerr)
		}
		if IsTerminalStatus(row.Status) {
			return judge, counting
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("run %s never reached a terminal state", runID)
	return nil, nil
}

func TestExecutionServiceFeedsInputFileToJudge(t *testing.T) {
	model := "judge-model"
	tmpl := "User input: {{Prompt}}\nAgent response: {{Response}}"
	cs := `{"Correct": 1.0, "Incorrect": 0.0}`
	judge, _ := driveExampleFileRun(t, &store.EvalEvaluator{
		Name: "routing", Type: "llm_judge", Model: &model,
		PromptTemplate: &tmpl, ChoiceScores: &cs,
		IsDefault: true, CreatedBy: "system", UpdatedBy: "system",
	})

	prompt := judge.lastUserPrompt()
	if prompt == "" {
		t.Fatalf("judge received no user prompt")
	}
	inputAt := strings.Index(prompt, inputArtifactsHeader)
	if inputAt < 0 {
		t.Fatalf("judge prompt missing the input-file section:\n%s", prompt)
	}
	if !strings.Contains(prompt, exampleFileFixtureInputBody) {
		t.Fatalf("judge prompt missing the input file's content:\n%s", prompt)
	}
	if !strings.Contains(prompt, "revenue.csv (text/csv)") {
		t.Fatalf("judge prompt missing the input file's name and type:\n%s", prompt)
	}
	expectedAt := strings.Index(prompt, expectedArtifactsHeader)
	if expectedAt < 0 {
		t.Fatalf("judge prompt missing the expected-file section:\n%s", prompt)
	}
	if inputAt >= expectedAt {
		t.Fatalf("expected section must follow the input section (input=%d expected=%d):\n%s", inputAt, expectedAt, prompt)
	}
	if !strings.Contains(prompt, exampleFileFixtureExpectedBody) {
		t.Fatalf("judge prompt missing the expected file's content:\n%s", prompt)
	}
	if !strings.Contains(judge.systemPrompt, inputArtifactsHeader) {
		t.Fatalf("system prompt does not name the input-file heading:\n%s", judge.systemPrompt)
	}
	if !strings.Contains(judge.systemPrompt, expectedArtifactsHeader) {
		t.Fatalf("system prompt does not name the expected-file heading:\n%s", judge.systemPrompt)
	}
}

func TestExecutionServiceHeuristicOnlyRunLoadsNoExampleFiles(t *testing.T) {
	htype := "levenshtein"
	judge, counting := driveExampleFileRun(t, &store.EvalEvaluator{
		Name: "distance", Type: "heuristic", HeuristicType: &htype,
		IsDefault: true, CreatedBy: "system", UpdatedBy: "system",
	})

	if n := counting.loads.Load(); n != 0 {
		t.Fatalf("a heuristic-only run read example files %d times, want 0", n)
	}
	if judge.lastUserPrompt() != "" {
		t.Fatalf("a heuristic-only run called the LLM judge:\n%s", judge.lastUserPrompt())
	}
}

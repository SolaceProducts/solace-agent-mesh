// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eval

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact/filesystem"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact/memory"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/eval/storage"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func TestFileDeletes_DeletesEveryRecordedFile(t *testing.T) {
	ctx := t.Context()
	storageSvc := NewStorageService(storage.NewMemory(), "ns")
	scoped := &artifact.ScopedService{Inner: memory.New(), Scope: "namespace", Namespace: "ns"}
	files := NewFileDeletes(storageSvc, scoped, nil)

	if _, err := storageSvc.UploadExecutionData("run-1", "ex-1", 0, map[string]any{"a": 1}); err != nil {
		t.Fatal(err)
	}
	save := func(session, name string) artifact.Locator {
		loc := scoped.Resolve("", EvalRunnerUserID, session, name)
		if _, err := scoped.Inner.Save(ctx, loc, []byte("x"), "text/plain"); err != nil {
			t.Fatal(err)
		}
		return loc
	}
	locs := []artifact.Locator{
		save(evalRunSnapshotSessionID("run-1"), "report.md"),
		save(evalArtifactSnapshotSessionID("run-0", "ex-0", 2), "old.md"),
		save("eval-example-ex-1-input-u1", "in.txt"),
	}
	event, err := fileDeleteEvent(store.EvalFileDeletes{
		RunIDs:         []string{"run-1"},
		TrialSnapshots: []store.EvalTrialSnapshot{{RunID: "run-0", ExampleID: "ex-0", TrialIndex: 2, ArtifactsMetadata: `[{"filename":"old.md","version":0}]`}},
		ExampleFiles:   []store.EvalExampleFile{{SessionID: "eval-example-ex-1-input-u1", Filename: "in.txt"}},
	})
	if err != nil || event == nil {
		t.Fatalf("event = %v, %v", event, err)
	}
	if err := files.PublishOutboxEvent(ctx, *event); err != nil {
		t.Fatalf("publish: %v", err)
	}
	for _, loc := range locs {
		if got, _, _ := scoped.Inner.Load(ctx, loc, nil); got != nil {
			t.Errorf("%s/%s survived the delete", loc.SessionID, loc.Filename)
		}
	}
	if n, err := storageSvc.DeleteRunData(t.Context(), "run-1"); err != nil || n != 0 {
		t.Errorf("run data left = %d, %v; want none", n, err)
	}
	if err := files.PublishOutboxEvent(ctx, *event); err != nil {
		t.Errorf("a second delete of the same files = %v, want success", err)
	}
}

func TestFileDeletes_KeepsTheEvalRunnerUsersOwnFiles(t *testing.T) {
	ctx := t.Context()
	scoped := &artifact.ScopedService{Inner: filesystem.New(t.TempDir()), Scope: "namespace", Namespace: "ns"}
	files := NewFileDeletes(NewStorageService(nil, "ns"), scoped, nil)
	session := evalRunSnapshotSessionID("run-1")
	own := scoped.Resolve("", EvalRunnerUserID, session, "report.md")
	shared := scoped.Resolve("", EvalRunnerUserID, session, "user:data.csv")
	for _, loc := range []artifact.Locator{own, shared} {
		if _, err := scoped.Inner.Save(ctx, loc, []byte("x"), "text/plain"); err != nil {
			t.Fatal(err)
		}
	}
	event, err := fileDeleteEvent(store.EvalFileDeletes{RunIDs: []string{"run-1"}})
	if err != nil || event == nil {
		t.Fatalf("event = %v, %v", event, err)
	}
	if err := files.PublishOutboxEvent(ctx, *event); err != nil {
		t.Fatalf("publish: %v", err)
	}
	if got, _, _ := scoped.Inner.Load(ctx, own, nil); got != nil {
		t.Error("the run's snapshot file survived the delete")
	}
	if got, _, _ := scoped.Inner.Load(ctx, shared, nil); got == nil {
		t.Error("the delete removed a file of the eval-runner user outside the run's session")
	}
}

func TestLegacySnapshotSession(t *testing.T) {
	recorded := store.EvalTrialSnapshot{RunID: "r", ExampleID: "e", ArtifactsMetadata: `[{"filename":"a","version":3,"artifact_session_id":"eval-run-r"}]`}
	if got := legacySnapshotSession(recorded); got != "" {
		t.Errorf("a row naming its session = %q, want none: its files go with the run", got)
	}
	legacy := store.EvalTrialSnapshot{RunID: "r", ExampleID: "e", TrialIndex: 1, ArtifactsMetadata: `[{"filename":"a","version":0}]`}
	if got := legacySnapshotSession(legacy); got != evalArtifactSnapshotSessionID("r", "e", 1) {
		t.Errorf("a row from before per-run sessions = %q, want its per-trial session", got)
	}
}

type takeoverFixture struct {
	db        *store.DB
	svc       *ExecutionService
	runs      *store.EvalRunRepo
	results   *store.EvalRunResultRepo
	exp       *store.EvalExperiment
	examples  []*store.EvalExample
	evaluator *store.EvalEvaluator
}

func newTakeoverFixture(t *testing.T) *takeoverFixture {
	t.Helper()
	ctx := t.Context()
	db := store.TestDB(t)
	dataset := &store.EvalDataset{Name: "takeover", CreatedBy: "u", UpdatedBy: "u"}
	if err := store.NewEvalDatasetRepo(db).Create(ctx, dataset); err != nil {
		t.Fatal(err)
	}
	f := &takeoverFixture{db: db, runs: store.NewEvalRunRepo(db), results: store.NewEvalRunResultRepo(db)}
	for i := range 3 {
		ex := &store.EvalExample{DatasetID: dataset.ID, SequenceNumber: i + 1, Prompt: "p"}
		if err := store.NewEvalExampleRepo(db).Create(ctx, ex); err != nil {
			t.Fatal(err)
		}
		f.examples = append(f.examples, ex)
	}
	f.exp = &store.EvalExperiment{Name: "takeover", DatasetID: &dataset.ID, TargetAgent: "agent", Models: "[]", RunsPerExample: 1, CreatedBy: "u", UpdatedBy: "u"}
	if err := store.NewEvalExperimentRepo(db).Create(ctx, f.exp, nil); err != nil {
		t.Fatal(err)
	}
	tmpl, choices := "grade it", `{"Yes": 1.0, "No": 0.0}`
	f.evaluator = &store.EvalEvaluator{Name: "judge", Type: "llm_judge", PromptTemplate: &tmpl, ChoiceScores: &choices, CreatedBy: "u", UpdatedBy: "u"}
	if err := store.NewEvalEvaluatorRepo(db).Create(ctx, f.evaluator); err != nil {
		t.Fatal(err)
	}
	f.svc = NewExecutionService(ExecutionServiceParams{
		Runs: f.runs, Results: f.results, Scores: store.NewEvalRunScoreRepo(db),
		Experiments: store.NewEvalExperimentRepo(db), Examples: store.NewEvalExampleRepo(db),
	})
	return f
}

func (f *takeoverFixture) runningRun(t *testing.T) *store.EvalRun {
	t.Helper()
	ctx := t.Context()
	snapshot, err := json.Marshal(map[string]any{"dataset": map[string]any{"examples": []map[string]any{{"id": f.examples[0].ID}, {"id": f.examples[1].ID}}}})
	if err != nil {
		t.Fatal(err)
	}
	config := string(snapshot)
	evaluators, err := json.Marshal([]map[string]any{{"id": f.evaluator.ID, "name": f.evaluator.Name, "type": f.evaluator.Type}})
	if err != nil {
		t.Fatal(err)
	}
	evaluatorSnapshot := string(evaluators)
	run := &store.EvalRun{ExperimentID: f.exp.ID, Model: "m", StartedBy: "u", ConfigSnapshot: &config, EvaluatorSnapshot: &evaluatorSnapshot}
	if err := f.runs.Create(ctx, run); err != nil {
		t.Fatal(err)
	}
	if ok, err := f.runs.SetRunning(ctx, run.ID, 2); err != nil || !ok {
		t.Fatalf("set running: %v %v", ok, err)
	}
	saved := &store.EvalRunResult{RunID: run.ID, ExampleID: f.examples[0].ID}
	half := &store.EvalRunResult{RunID: run.ID, ExampleID: f.examples[1].ID}
	if err := f.results.BulkCreate(ctx, []*store.EvalRunResult{saved, half}); err != nil {
		t.Fatal(err)
	}
	if err := store.NewEvalRunScoreRepo(f.db).Create(ctx, &store.EvalRunScore{RunResultID: saved.ID, EvaluatorID: f.evaluator.ID, Score: 1}); err != nil {
		t.Fatal(err)
	}
	if err := f.runs.UpdateProgress(ctx, run.ID, 2); err != nil {
		t.Fatal(err)
	}
	return run
}

func TestTakeover_ResumesOnlyTheTrialsWithNoSavedResult(t *testing.T) {
	f := newTakeoverFixture(t)
	ctx := t.Context()
	run := f.runningRun(t)
	got, _ := f.runs.GetByID(ctx, run.ID)

	gotRun, exp, _, exampleIDs, claimed, err := f.svc.takeoverSetup(ctx, got)
	if err != nil || !claimed || gotRun.ID != run.ID || exp.ID != f.exp.ID {
		t.Fatalf("takeoverSetup = %v, %v", claimed, err)
	}
	if !slices.Equal(exampleIDs, []string{f.examples[0].ID, f.examples[1].ID}) {
		t.Errorf("examples = %v, want the two in the run's snapshot, not the one added since", exampleIDs)
	}
	remaining, saved, err := f.svc.resumeWork(ctx, run.ID, []*store.EvalEvaluator{f.evaluator}, buildAllWork(exampleIDs, 1))
	if err != nil {
		t.Fatal(err)
	}
	if saved != 1 || len(remaining) != 1 || remaining[0].exampleID != f.examples[1].ID {
		t.Errorf("resume = %v saved %d, want the half-saved trial sent again and one saved", remaining, saved)
	}
	after, _ := f.runs.GetByID(ctx, run.ID)
	if after.ProgressCurrent != 1 {
		t.Errorf("progress = %d, want it reset to the one saved trial", after.ProgressCurrent)
	}
}

func TestTakeover_TheTakeoverThatReachesTheCapStillRuns(t *testing.T) {
	f := newTakeoverFixture(t)
	ctx := t.Context()
	run := f.runningRun(t)
	if _, err := f.db.ExecContext(ctx, `UPDATE eval_runs SET takeovers = ? WHERE id = ?`, maxTakeoversWithoutProgress-1, run.ID); err != nil {
		t.Fatal(err)
	}
	if !f.svc.claimTakeover(ctx, run.ID) {
		t.Fatal("the takeover that reaches the cap was refused")
	}
	got, _ := f.runs.GetByID(ctx, run.ID)
	if got.Status != StatusRunning || got.Takeovers != maxTakeoversWithoutProgress {
		t.Errorf("run = %s after %d takeovers, want running after %d", got.Status, got.Takeovers, maxTakeoversWithoutProgress)
	}
}

func TestTakeover_FailsARunPastTheCap(t *testing.T) {
	f := newTakeoverFixture(t)
	ctx := t.Context()
	run := f.runningRun(t)
	if _, err := f.db.ExecContext(ctx, `UPDATE eval_runs SET takeovers = ? WHERE id = ?`, maxTakeoversWithoutProgress, run.ID); err != nil {
		t.Fatal(err)
	}
	f.svc.takeOverRuns(ctx)
	got, _ := f.runs.GetByID(ctx, run.ID)
	if got.Status != StatusFailed || got.ErrorMessage == nil || *got.ErrorMessage != msgRunTakeoverCap {
		t.Errorf("run = %s %v, want failed with the takeover cap message", got.Status, got.ErrorMessage)
	}
}

func TestTakeover_BindStateDrivesTheReconcile(t *testing.T) {
	svc := NewExecutionService(ExecutionServiceParams{})
	svc.onTriggerBindState(broker.BindActive)
	if !svc.triggerActive.Load() {
		t.Fatal("an active bind left the service standby")
	}
	select {
	case <-svc.reconcileNow:
	default:
		t.Fatal("an active bind did not ask for a reconcile")
	}
	svc.onTriggerBindState(broker.BindActive)
	select {
	case <-svc.reconcileNow:
		t.Fatal("a repeated active report asked for another reconcile")
	default:
	}

	ctx, cancel := context.WithCancel(t.Context())
	svc.activeRuns["r1"] = cancel
	svc.onTriggerBindState(broker.BindStandby)
	svc.standDown()
	if ctx.Err() == nil {
		t.Error("standing down left a coordinator running")
	}
	if svc.consumeCause("r1") != causeShutdown {
		t.Error("standing down did not mark the run as left for a takeover")
	}
}

func TestCancelWithCause_RecordsOnlyForARunHeldHere(t *testing.T) {
	svc := NewExecutionService(ExecutionServiceParams{})
	svc.cancelWithCause("elsewhere", causeUserCancel)
	if len(svc.terminationCauses) != 0 {
		t.Errorf("causes = %v, want none for a run this GWE does not hold", svc.terminationCauses)
	}
}

func TestTakeover_ARedeliveredTriggerTakesTheRunOver(t *testing.T) {
	f := startLivenessRun(t, true, time.Nanosecond)
	waitFor(t, func() bool {
		got, _ := f.runs.GetByID(f.ctx, f.runID)
		return got.Status == StatusRunning
	}, "the first GWE to claim the run")
	f.exec.Stop()

	params := f.params
	params.BrokerCli = NewBrokerClient(params.Broker, params.Namespace, nil, nil)
	if err := params.BrokerCli.Start(f.ctx); err != nil {
		t.Fatal(err)
	}
	next := NewExecutionService(params)
	next.mu.Lock()
	next.started = true
	next.mu.Unlock()
	t.Cleanup(next.Stop)
	next.spawnCoordinator(f.runID)

	waitFor(t, func() bool {
		got, _ := f.runs.GetByID(f.ctx, f.runID)
		return got.Takeovers == 1
	}, "the takeover to be counted")
	time.Sleep(200 * time.Millisecond)
	if !next.runIsActive(f.runID) {
		t.Fatal("the next GWE dropped the run instead of running it")
	}
}

func waitFor(t *testing.T, done func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !done() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestReconcileWithin_EndsAHungDatabaseCallAtTheLimit(t *testing.T) {
	f := newTakeoverFixture(t)
	held, err := f.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = held.Close() })
	if f.db.Stats().MaxOpenConnections != 1 {
		t.Skip("needs a single-connection database to hold")
	}
	start := time.Now()
	f.svc.reconcileWithin(t.Context(), 200*time.Millisecond)
	if took := time.Since(start); took > 2*time.Second {
		t.Fatalf("reconcile took %s with its connection held, want it to end near the 200ms limit", took)
	}
}

func TestClaimLostTriggers_StartsOnlyOverduePendingRunsNotRunningHere(t *testing.T) {
	f := startLivenessRun(t, true, time.Nanosecond)
	first, err := f.runs.GetByID(f.ctx, f.runID)
	if err != nil {
		t.Fatal(err)
	}
	pending := func(age time.Duration) *store.EvalRun {
		run := &store.EvalRun{ExperimentID: first.ExperimentID, Model: "m", StartedBy: "u", StartedTime: time.Now().Add(-age).UnixMilli()}
		if err := f.runs.Create(f.ctx, run); err != nil {
			t.Fatal(err)
		}
		return run
	}
	overdue, young, startingHere := pending(time.Minute), pending(0), pending(time.Minute)
	f.exec.mu.Lock()
	f.exec.activeRuns[startingHere.ID] = func() {}
	f.exec.mu.Unlock()

	f.exec.claimLostTriggers(f.ctx)

	waitFor(t, func() bool {
		got, _ := f.runs.GetByID(f.ctx, overdue.ID)
		return got.Status == StatusRunning
	}, "the overdue pending run to start")
	for name, id := range map[string]string{"young": young.ID, "starting here": startingHere.ID} {
		if got, _ := f.runs.GetByID(f.ctx, id); got.Status != StatusPending {
			t.Errorf("%s run status = %s, want pending", name, got.Status)
		}
	}
}

type countingGenerations struct{ runs atomic.Int32 }

func (g *countingGenerations) RunGeneration(context.Context, string, bool) { g.runs.Add(1) }
func (g *countingGenerations) GenerationsToTakeOver(context.Context) []string {
	return nil
}

func TestExecutionService_AStoppedServiceStartsNoWork(t *testing.T) {
	generations := &countingGenerations{}
	svc := NewExecutionService(ExecutionServiceParams{Generations: generations})
	svc.spawnCoordinator("run-1")
	svc.runGeneration("gen-1", true)
	svc.wg.Wait()
	if svc.runIsActive("run-1") || generations.runs.Load() != 0 {
		t.Fatalf("run active = %v, generations run = %d; want neither on a stopped service", svc.runIsActive("run-1"), generations.runs.Load())
	}
}

func TestReconcile_StopsCoordinatorOfCancelledRunWhoseCancelEventNeverArrived(t *testing.T) {
	f := newTakeoverFixture(t)
	ctx := t.Context()
	cancelled, running := f.runningRun(t), f.runningRun(t)
	if _, ok, err := f.runs.CancelActive(ctx, cancelled.ID); err != nil || !ok {
		t.Fatalf("cancel: %v %v", ok, err)
	}
	cancelledCtx, stopCancelled := context.WithCancel(ctx)
	runningCtx, stopRunning := context.WithCancel(ctx)
	defer stopRunning()
	f.svc.activeRuns[cancelled.ID] = stopCancelled
	f.svc.activeRuns[running.ID] = stopRunning

	f.svc.reconcile(ctx)
	if cancelledCtx.Err() == nil || f.svc.consumeCause(cancelled.ID) != causeUserCancel {
		t.Error("the cancelled run's coordinator was not stopped as a user cancel")
	}
	if runningCtx.Err() != nil {
		t.Error("a running run's coordinator was stopped")
	}
}

func (f *takeoverFixture) startGWE(t *testing.T, client broker.Broker) *ExecutionService {
	t.Helper()
	svc := NewExecutionService(ExecutionServiceParams{
		Namespace: "takeover-ns", Broker: client, BrokerCli: NewBrokerClient(client, "takeover-ns", nil, nil),
		Runs: f.runs, Results: f.results, Scores: store.NewEvalRunScoreRepo(f.db),
		Experiments: store.NewEvalExperimentRepo(f.db), Examples: store.NewEvalExampleRepo(f.db),
		Config: ExecutionConfig{ExampleBudget: time.Hour, WatchdogInterval: 20 * time.Millisecond},
	})
	if err := svc.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(svc.Stop)
	return svc
}

func TestTakeover_TheActiveGWEResumesALeftRunAndTheStandbyTakesOverWhenItStops(t *testing.T) {
	f := newTakeoverFixture(t)
	ctx := t.Context()
	brk := broker.NewDevBroker()
	if err := brk.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })
	agentTasks, err := brk.CreateDirectSubscription()
	if err != nil {
		t.Fatal(err)
	}
	if err := brk.Subscribe(agentTasks, a2a.AgentRequestTopic("takeover-ns", f.exp.TargetAgent)); err != nil {
		t.Fatal(err)
	}

	run := f.runningRun(t)
	first := f.startGWE(t, brk.NewClient())
	waitFor(t, first.triggerActive.Load, "the first GWE's trigger bind to be active")
	second := f.startGWE(t, brk.NewClient())

	waitFor(t, func() bool { return first.runIsActive(run.ID) }, "the active GWE to take the run over")
	firstTrial, cancelFirst := context.WithTimeout(ctx, 5*time.Second)
	defer cancelFirst()
	received, rerr := brk.Receive(firstTrial, agentTasks)
	if rerr != nil || received == nil {
		t.Fatalf("no trial reached the agent: %v", rerr)
	}
	quiet, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
	defer cancel()
	if extra, err := brk.Receive(quiet, agentTasks); err == nil {
		t.Fatalf("a second trial %s was sent, want only the trial with no saved result", extra.Topic)
	}
	if got, _ := f.runs.GetByID(ctx, run.ID); got.Takeovers != 1 || second.runIsActive(run.ID) {
		t.Fatalf("takeovers = %d, standby running it = %v; want one takeover by the active GWE only", got.Takeovers, second.runIsActive(run.ID))
	}

	first.Stop()
	waitFor(t, func() bool { return second.runIsActive(run.ID) }, "the standby GWE to take the run over")
	if got, _ := f.runs.GetByID(ctx, run.ID); got.Takeovers != 2 {
		t.Errorf("takeovers = %d, want 2", got.Takeovers)
	}
}

func TestTakeover_ASetupErrorLeavesTheRunRunning(t *testing.T) {
	f := newTakeoverFixture(t)
	run := f.runningRun(t)
	run.Status = StatusRunning
	if err := f.db.Close(); err != nil {
		t.Fatal(err)
	}
	_, _, _, _, _, err := f.svc.takeoverSetup(t.Context(), run)
	var retried *runLeftRunningError
	if !errors.As(err, &retried) || err.Error() == "" || errors.Unwrap(err) == nil {
		t.Fatalf("takeoverSetup = %v, want an error that leaves the run running", err)
	}
}

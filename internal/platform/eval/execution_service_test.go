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
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	artifactmem "github.com/SolaceProducts/solace-agent-mesh/internal/artifact/memory"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/control"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	evalstorage "github.com/SolaceProducts/solace-agent-mesh/internal/platform/eval/storage"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

const ciSafeExampleBudget = 10 * time.Second

type fakeAgent struct {
	t           *testing.T
	br          broker.Broker
	queue       broker.Queue
	requestTop  string
	responseFor map[string]string
	onRequest   func(*broker.Message)
	wg          sync.WaitGroup
	cancel      context.CancelFunc
}

const fakeAgentFails = "<fake agent fails>"

func newFakeAgent(t *testing.T, br broker.Broker, namespace, agentName string, responseFor map[string]string) *fakeAgent {
	return newObservedFakeAgent(t, br, namespace, agentName, responseFor, nil)
}

func newObservedFakeAgent(t *testing.T, br broker.Broker, namespace, agentName string, responseFor map[string]string, onRequest func(*broker.Message)) *fakeAgent {
	t.Helper()
	q, err := br.CreateDirectSubscription()
	if err != nil {
		t.Fatalf("agent direct sub: %v", err)
	}
	topic := a2a.AgentRequestTopic(namespace, agentName)
	if err := br.Subscribe(q, topic); err != nil {
		t.Fatalf("agent subscribe: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	a := &fakeAgent{
		t:           t,
		br:          br,
		queue:       q,
		requestTop:  topic,
		responseFor: responseFor,
		onRequest:   onRequest,
		cancel:      cancel,
	}
	a.wg.Add(1)
	go a.run(ctx)
	return a
}

func (a *fakeAgent) Stop() {
	a.cancel()
	a.wg.Wait()
}

func (a *fakeAgent) run(ctx context.Context) {
	defer a.wg.Done()
	for {
		msg, err := a.br.Receive(ctx, a.queue)
		if err != nil || msg == nil {
			return
		}
		if a.onRequest != nil {
			a.onRequest(msg)
		}
		var req struct {
			ID     string `json:"id"`
			Params struct {
				Message struct {
					Parts []struct {
						Text string `json:"text"`
					} `json:"parts"`
					TaskID string `json:"taskId"`
				} `json:"message"`
			} `json:"params"`
		}
		if uerr := json.Unmarshal(msg.Payload, &req); uerr != nil {
			continue
		}
		var prompt string
		if len(req.Params.Message.Parts) > 0 {
			prompt = req.Params.Message.Parts[0].Text
		}
		responseText := a.responseFor[prompt]
		if responseText == "" {
			responseText = "DEFAULT"
		}
		response := map[string]any{
			"jsonrpc": "2.0",
			"id":      req.ID,
			"result": map[string]any{
				"final": true,
				"status": map[string]any{
					"state": "completed",
					"message": map[string]any{
						"parts": []map[string]any{{"kind": "text", "text": responseText}},
					},
				},
			},
		}
		if responseText == fakeAgentFails {
			response = map[string]any{"jsonrpc": "2.0", "id": req.ID, "error": map[string]any{"message": "agent failed"}}
		}
		body, err := json.Marshal(response)
		if err != nil {
			continue
		}
		reply := msg.ReplyTo
		if reply == "" {
			continue
		}
		out := &broker.Message{Topic: reply, Payload: body}
		_ = a.br.Publish(ctx, reply, out)
	}
}

func TestExecutionServiceFullRunLifecycle(t *testing.T) {
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
	prompt1 := `produce JSON: {"k":1}`
	prompt2 := `produce JSON: {"k":2}`
	for i, p := range []string{prompt1, prompt2} {
		ex := &store.EvalExample{DatasetID: ds.ID, SequenceNumber: i + 1, Prompt: p}
		if err := exampleRepo.Create(ctx, ex); err != nil {
			t.Fatalf("create example: %v", err)
		}
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
		Namespace:   namespace,
		Broker:      br,
		BrokerCli:   brokerCli,
		Storage:     storageSvc,
		Runs:        runRepo,
		Results:     resultRepo,
		Scores:      scoreRepo,
		Experiments: expRepo,
		Examples:    exampleRepo,
		Config: ExecutionConfig{
			ExampleBudget:    ciSafeExampleBudget,
			WatchdogInterval: 500 * time.Millisecond,
		},
	})

	agent := newFakeAgent(t, br, namespace, "Orchestrator", map[string]string{
		prompt1: `{"k":1}`,
		prompt2: `{"k":2}`,
	})
	t.Cleanup(agent.Stop)

	if err := exec.Start(ctx); err != nil {
		t.Fatalf("start exec: %v", err)
	}
	t.Cleanup(exec.Stop)

	runs, err := runSvc.TriggerRuns(ctx, exp.ID, "alice", "lifecycle", "manual")
	if err != nil {
		t.Fatalf("trigger: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("expected 1 run, got %d", len(runs))
	}
	runID := runs[0].ID

	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		row, err := runRepo.GetByID(ctx, runID)
		if err != nil {
			t.Fatalf("get run: %v", err)
		}
		if IsTerminalStatus(row.Status) {
			if row.Status != StatusCompleted {
				t.Fatalf("final status = %q, want completed (error_message=%v)", row.Status, derefOrEmpty(row.ErrorMessage))
			}
			results, err := resultRepo.ListByRun(ctx, runID)
			if err != nil {
				t.Fatalf("list results: %v", err)
			}
			if len(results) != 2 {
				t.Fatalf("results = %d, want 2", len(results))
			}
			for _, r := range results {
				if r.AgentResponse == nil || !strings.HasPrefix(strings.TrimSpace(*r.AgentResponse), "{") {
					t.Fatalf("agent response = %v", r.AgentResponse)
				}
				if len(r.Scores) != 1 {
					t.Fatalf("scores = %d, want 1", len(r.Scores))
				}
				if r.Scores[0].Score != 1.0 {
					t.Fatalf("score = %v, want 1.0", r.Scores[0].Score)
				}
			}
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("run did not reach a terminal state within deadline")
}

func TestExecutionServiceWatchdogFinalizesUnresponsiveRun(t *testing.T) {
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
	ex := &store.EvalExample{DatasetID: ds.ID, SequenceNumber: 1, Prompt: "no reply"}
	if err := exampleRepo.Create(ctx, ex); err != nil {
		t.Fatalf("create example: %v", err)
	}
	htype := "valid_json"
	ev := &store.EvalEvaluator{Name: "vj", Type: "heuristic", HeuristicType: &htype, IsDefault: true, CreatedBy: "system", UpdatedBy: "system"}
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
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = br.Disconnect(ctx) })
	namespace := "test-ns"
	brokerCli := NewBrokerClient(br, namespace, nil, nil)
	exec := NewExecutionService(ExecutionServiceParams{
		Namespace:   namespace,
		Broker:      br,
		BrokerCli:   brokerCli,
		Runs:        runRepo,
		Results:     resultRepo,
		Scores:      scoreRepo,
		Experiments: expRepo,
		Examples:    exampleRepo,
		Config: ExecutionConfig{
			ExampleBudget:    300 * time.Millisecond,
			WatchdogInterval: 200 * time.Millisecond,
		},
	})
	if err := exec.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(exec.Stop)

	trigger := NewTriggerPublisher(br, namespace)
	runSvc := NewRunService(RunServiceParams{
		Runs: runRepo, Results: resultRepo, Experiments: expRepo,
		Examples: exampleRepo, Evaluators: evRepo, Trigger: trigger,
	})
	runs, err := runSvc.TriggerRuns(ctx, exp.ID, "alice", "", "manual")
	if err != nil {
		t.Fatalf("trigger: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		row, err := runRepo.GetByID(ctx, runs[0].ID)
		if err != nil {
			t.Fatalf("get run: %v", err)
		}
		if IsTerminalStatus(row.Status) {
			if row.Status != StatusFailed {
				t.Fatalf("status = %q, want failed (no successful examples)", row.Status)
			}
			results, lerr := resultRepo.ListByRun(ctx, runs[0].ID)
			if lerr != nil {
				t.Fatalf("list results: %v", lerr)
			}
			if len(results) != 1 {
				t.Fatalf("attribution rows = %d, want 1", len(results))
			}
			if results[0].ErrorMessage == nil || !strings.Contains(*results[0].ErrorMessage, "Run exceeded maximum duration") {
				t.Errorf("error_message = %v, want 'Run exceeded maximum duration.'", results[0].ErrorMessage)
			}
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("run did not reach terminal state")
}

func derefOrEmpty(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

type stubResolver struct {
	in  string
	out string
	err error
}

func (s *stubResolver) ResolveAgentRoutingName(_ context.Context, name string) (string, error) {
	if s.err != nil {
		return "", s.err
	}
	if name != s.in {
		return name, nil
	}
	return s.out, nil
}

func TestExecutionServiceUsesResolvedAgentSlug(t *testing.T) {
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
	prompt := `produce JSON: {"k":1}`
	if err := exampleRepo.Create(ctx, &store.EvalExample{
		DatasetID: ds.ID, SequenceNumber: 1, Prompt: prompt,
	}); err != nil {
		t.Fatalf("create example: %v", err)
	}
	htype := "valid_json"
	ev := &store.EvalEvaluator{
		Name: "valid-json", Type: "heuristic", HeuristicType: &htype,
		IsDefault: true, CreatedBy: "system", UpdatedBy: "system",
	}
	if err := evRepo.Create(ctx, ev); err != nil {
		t.Fatalf("create evaluator: %v", err)
	}
	exp := &store.EvalExperiment{
		Name:           "slug-exp",
		TargetAgent:    "PlatformAgent",
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
	const slug = "agent_abc123"
	exec := NewExecutionService(ExecutionServiceParams{
		Namespace:     namespace,
		Broker:        br,
		BrokerCli:     brokerCli,
		Storage:       storageSvc,
		Runs:          runRepo,
		Results:       resultRepo,
		Scores:        scoreRepo,
		Experiments:   expRepo,
		Examples:      exampleRepo,
		AgentResolver: &stubResolver{in: "PlatformAgent", out: slug},
		Config: ExecutionConfig{
			ExampleBudget:    ciSafeExampleBudget,
			WatchdogInterval: 500 * time.Millisecond,
		},
	})

	agent := newFakeAgent(t, br, namespace, slug, map[string]string{prompt: `{"k":1}`})
	t.Cleanup(agent.Stop)

	if err := exec.Start(ctx); err != nil {
		t.Fatalf("start exec: %v", err)
	}
	t.Cleanup(exec.Stop)

	runs, err := runSvc.TriggerRuns(ctx, exp.ID, "alice", "slug-test", "manual")
	if err != nil {
		t.Fatalf("trigger: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("expected 1 run, got %d", len(runs))
	}
	runID := runs[0].ID

	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		row, err := runRepo.GetByID(ctx, runID)
		if err != nil {
			t.Fatalf("get run: %v", err)
		}
		if IsTerminalStatus(row.Status) {
			if row.Status != StatusCompleted {
				t.Fatalf("final status = %q, want completed (error_message=%v) — resolver slug likely not threaded through",
					row.Status, derefOrEmpty(row.ErrorMessage))
			}
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("run did not reach terminal state — slug topic %q never received the task", slug)
}

type stubAgentRegistry struct {
	mu        sync.Mutex
	present   bool
	knownName string
}

func (s *stubAgentRegistry) Get(name string) *a2a.AgentCard {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.present {
		return nil
	}
	if s.knownName != "" && name != s.knownName {
		return nil
	}
	return &a2a.AgentCard{Name: name}
}

func (s *stubAgentRegistry) setPresent(p bool) {
	s.mu.Lock()
	s.present = p
	s.mu.Unlock()
}

type livenessFixture struct {
	ctx      context.Context
	runs     *store.EvalRunRepo
	results  *store.EvalRunResultRepo
	registry *stubAgentRegistry
	runID    string
	exec     *ExecutionService
	params   ExecutionServiceParams
}

func startLivenessRun(t *testing.T, present bool, warmup time.Duration, lookup ...func(context.Context, string) control.Target) livenessFixture {
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
	ds := &store.EvalDataset{Name: "liveness-fixture", CreatedBy: "alice", UpdatedBy: "alice"}
	if err := datasetRepo.Create(ctx, ds); err != nil {
		t.Fatalf("create dataset: %v", err)
	}
	ex := &store.EvalExample{DatasetID: ds.ID, SequenceNumber: 1, Prompt: "no reply"}
	if err := exampleRepo.Create(ctx, ex); err != nil {
		t.Fatalf("create example: %v", err)
	}
	htype := "valid_json"
	ev := &store.EvalEvaluator{Name: "vj", Type: "heuristic", HeuristicType: &htype, IsDefault: true, CreatedBy: "system", UpdatedBy: "system"}
	if err := evRepo.Create(ctx, ev); err != nil {
		t.Fatalf("create evaluator: %v", err)
	}
	exp := &store.EvalExperiment{
		Name: "liveness-exp", TargetAgent: "GhostAgent", Models: "[]",
		RunsPerExample: 1, MaxWorkers: 1, DatasetID: &ds.ID,
		CreatedBy: "alice", UpdatedBy: "alice",
	}
	if err := expRepo.Create(ctx, exp, []string{ev.ID}); err != nil {
		t.Fatalf("create experiment: %v", err)
	}

	br := broker.NewDevBroker()
	if err := br.Connect(ctx); err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = br.Disconnect(ctx) })

	registry := &stubAgentRegistry{present: present}
	namespace := "test-ns"
	brokerCli := NewBrokerClient(br, namespace, nil, nil)
	params := ExecutionServiceParams{
		Namespace:     namespace,
		Broker:        br,
		BrokerCli:     brokerCli,
		Runs:          runRepo,
		Results:       resultRepo,
		Scores:        scoreRepo,
		Experiments:   expRepo,
		Examples:      exampleRepo,
		AgentRegistry: registry,
		Config: ExecutionConfig{
			ExampleBudget:    time.Hour,
			WatchdogInterval: time.Hour,
			LivenessInterval: 100 * time.Millisecond,
			DiscoveryWarmup:  warmup,
		},
	}
	if len(lookup) > 0 {
		params.TargetLookup = lookup[0]
	}
	exec := NewExecutionService(params)
	if err := exec.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(exec.Stop)

	trigger := NewTriggerPublisher(br, namespace)
	runSvc := NewRunService(RunServiceParams{
		Runs: runRepo, Results: resultRepo, Experiments: expRepo,
		Examples: exampleRepo, Evaluators: evRepo, Trigger: trigger,
	})
	runs, err := runSvc.TriggerRuns(ctx, exp.ID, "alice", "", "manual")
	if err != nil {
		t.Fatalf("trigger: %v", err)
	}
	runID := runs[0].ID

	return livenessFixture{ctx: ctx, runs: runRepo, results: resultRepo, registry: registry, runID: runID, exec: exec, params: params}
}

func TestExecutionServiceAgentLivenessFinalizesRun(t *testing.T) {
	f := startLivenessRun(t, true, time.Nanosecond)

	time.Sleep(300 * time.Millisecond)
	f.registry.setPresent(false)

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		row, err := f.runs.GetByID(f.ctx, f.runID)
		if err != nil {
			t.Fatalf("get run: %v", err)
		}
		if IsTerminalStatus(row.Status) {
			if row.Status != StatusFailed {
				t.Fatalf("status = %q, want failed (no successful examples)", row.Status)
			}
			results, err := f.results.ListByRun(f.ctx, f.runID)
			if err != nil {
				t.Fatalf("list results: %v", err)
			}
			if len(results) != 1 {
				t.Fatalf("results = %d, want 1 attribution row", len(results))
			}
			if results[0].ErrorMessage == nil || !strings.Contains(*results[0].ErrorMessage, "Agent unavailable") {
				t.Fatalf("error_message = %v, want 'Agent unavailable: ...'", results[0].ErrorMessage)
			}
			if !strings.Contains(*results[0].ErrorMessage, "GhostAgent") {
				t.Fatalf("error_message = %q, want mention of GhostAgent", *results[0].ErrorMessage)
			}
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("run did not reach terminal state after agent removal")
}

func TestExecutionServiceAgentLivenessKeepsRunWhileAgentDeployed(t *testing.T) {
	var state atomic.Int32
	state.Store(int32(control.TargetDeployed))
	f := startLivenessRun(t, false, time.Nanosecond, func(context.Context, string) control.Target {
		return control.Target{State: control.TargetState(state.Load())}
	})

	time.Sleep(600 * time.Millisecond)
	row, err := f.runs.GetByID(f.ctx, f.runID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if IsTerminalStatus(row.Status) {
		t.Fatalf("status = %q while the agent is deployed, want the run still running", row.Status)
	}

	state.Store(int32(control.TargetNotDeployed))
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if row, err = f.runs.GetByID(f.ctx, f.runID); err != nil {
			t.Fatalf("get run: %v", err)
		}
		if row.Status == StatusFailed {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("status = %q after the agent was undeployed, want failed", row.Status)
}

func TestExecutionServiceAgentLivenessEndsRunForUnknownAgent(t *testing.T) {
	f := startLivenessRun(t, false, time.Nanosecond, func(context.Context, string) control.Target {
		return control.Target{State: control.TargetUnknown}
	})
	deadline := time.Now().Add(3 * time.Second)
	var status string
	for time.Now().Before(deadline) {
		row, err := f.runs.GetByID(f.ctx, f.runID)
		if err != nil {
			t.Fatalf("get run: %v", err)
		}
		if status = row.Status; status == StatusFailed {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("status = %q for an agent the platform does not know, want failed", status)
}

func TestExecutionServiceAgentLivenessWaitsOutDiscoveryWarmup(t *testing.T) {
	f := startLivenessRun(t, false, time.Second)

	time.Sleep(600 * time.Millisecond)
	row, err := f.runs.GetByID(f.ctx, f.runID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if IsTerminalStatus(row.Status) {
		t.Fatalf("status = %q inside the discovery warm-up, want the run still running", row.Status)
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if row, err = f.runs.GetByID(f.ctx, f.runID); err != nil {
			t.Fatalf("get run: %v", err)
		}
		if row.Status == StatusFailed {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("status = %q after the discovery warm-up, want failed", row.Status)
}

func TestExecutionServiceLivenessUsesRoutingName(t *testing.T) {
	db := store.TestDB(t)
	datasetRepo := store.NewEvalDatasetRepo(db)
	exampleRepo := store.NewEvalExampleRepo(db)
	expRepo := store.NewEvalExperimentRepo(db)
	evRepo := store.NewEvalEvaluatorRepo(db)
	runRepo := store.NewEvalRunRepo(db)
	resultRepo := store.NewEvalRunResultRepo(db)
	scoreRepo := store.NewEvalRunScoreRepo(db)

	ctx := t.Context()
	ds := &store.EvalDataset{Name: "routing-name-fixture", CreatedBy: "alice", UpdatedBy: "alice"}
	if err := datasetRepo.Create(ctx, ds); err != nil {
		t.Fatalf("create dataset: %v", err)
	}
	ex := &store.EvalExample{DatasetID: ds.ID, SequenceNumber: 1, Prompt: "p"}
	if err := exampleRepo.Create(ctx, ex); err != nil {
		t.Fatalf("create example: %v", err)
	}
	htype := "valid_json"
	ev := &store.EvalEvaluator{Name: "vj", Type: "heuristic", HeuristicType: &htype, IsDefault: true, CreatedBy: "system", UpdatedBy: "system"}
	if err := evRepo.Create(ctx, ev); err != nil {
		t.Fatalf("create evaluator: %v", err)
	}
	exp := &store.EvalExperiment{
		Name: "routing-name-exp", TargetAgent: "Orchestrator", Models: "[]",
		RunsPerExample: 1, MaxWorkers: 1, DatasetID: &ds.ID,
		CreatedBy: "alice", UpdatedBy: "alice",
	}
	if err := expRepo.Create(ctx, exp, []string{ev.ID}); err != nil {
		t.Fatalf("create experiment: %v", err)
	}

	br := broker.NewDevBroker()
	if err := br.Connect(ctx); err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = br.Disconnect(ctx) })

	const slug = "agent_019e89b1_c647_7b85_9b7c_737591e865cd"
	registry := &stubAgentRegistry{present: true, knownName: slug}
	namespace := "test-ns"
	brokerCli := NewBrokerClient(br, namespace, nil, nil)
	exec := NewExecutionService(ExecutionServiceParams{
		Namespace:     namespace,
		Broker:        br,
		BrokerCli:     brokerCli,
		Runs:          runRepo,
		Results:       resultRepo,
		Scores:        scoreRepo,
		Experiments:   expRepo,
		Examples:      exampleRepo,
		AgentRegistry: registry,
		AgentResolver: &stubResolver{in: "Orchestrator", out: slug},
		Config: ExecutionConfig{
			ExampleBudget:    time.Hour,
			WatchdogInterval: time.Hour,
			LivenessInterval: 50 * time.Millisecond,
		},
	})
	if err := exec.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(exec.Stop)

	trigger := NewTriggerPublisher(br, namespace)
	runSvc := NewRunService(RunServiceParams{
		Runs: runRepo, Results: resultRepo, Experiments: expRepo,
		Examples: exampleRepo, Evaluators: evRepo, Trigger: trigger,
	})
	runs, err := runSvc.TriggerRuns(ctx, exp.ID, "alice", "", "manual")
	if err != nil {
		t.Fatalf("trigger: %v", err)
	}
	runID := runs[0].ID

	time.Sleep(500 * time.Millisecond)

	row, err := runRepo.GetByID(ctx, runID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if IsTerminalStatus(row.Status) {
		msg := ""
		if row.ErrorMessage != nil {
			msg = *row.ErrorMessage
		}
		t.Fatalf("run finalized prematurely: status=%q errorMessage=%q (liveness was polling by display name, not routing name)", row.Status, msg)
	}
}

type slowStubLLM struct {
	delay time.Duration
}

func (s *slowStubLLM) JudgeScore(_ context.Context, _ string, _, _ string, _ *llm.SpendAttribution) (string, error) {
	time.Sleep(s.delay)
	return `{"choice": "Yes", "reasoning": "ok"}`, nil
}

func assertParallelDuration(t *testing.T, cap time.Duration, fn func()) {
	t.Helper()
	start := time.Now()
	fn()
	elapsed := time.Since(start)
	if elapsed > cap {
		t.Fatalf("expected parallel execution under %s, took %s", cap, elapsed)
	}
}

func TestEvaluateAndSaveScoresEvaluatorsInParallel(t *testing.T) {
	db := store.TestDB(t)
	datasetRepo := store.NewEvalDatasetRepo(db)
	exampleRepo := store.NewEvalExampleRepo(db)
	expRepo := store.NewEvalExperimentRepo(db)
	runRepo := store.NewEvalRunRepo(db)
	resultRepo := store.NewEvalRunResultRepo(db)
	scoreRepo := store.NewEvalRunScoreRepo(db)

	ctx := t.Context()
	ds := &store.EvalDataset{Name: "parallel-fixture", CreatedBy: "alice", UpdatedBy: "alice"}
	if err := datasetRepo.Create(ctx, ds); err != nil {
		t.Fatalf("create dataset: %v", err)
	}
	ex := &store.EvalExample{DatasetID: ds.ID, SequenceNumber: 1, Prompt: "p"}
	if err := exampleRepo.Create(ctx, ex); err != nil {
		t.Fatalf("create example: %v", err)
	}
	exp := &store.EvalExperiment{
		Name: "parallel-exp", TargetAgent: "Orchestrator", Models: "[]",
		RunsPerExample: 1, MaxWorkers: 1, DatasetID: &ds.ID,
		CreatedBy: "alice", UpdatedBy: "alice",
	}
	if err := expRepo.Create(ctx, exp, nil); err != nil {
		t.Fatalf("create exp: %v", err)
	}
	run := &store.EvalRun{
		ExperimentID:  exp.ID,
		Status:        StatusRunning,
		ProgressTotal: 1,
		Source:        "manual",
		StartedBy:     "alice",
	}
	if err := runRepo.Create(ctx, run); err != nil {
		t.Fatalf("create run: %v", err)
	}

	model := "stub-model"
	tmpl := "Judge {{Prompt}} → {{Response}}"
	choiceScores := `{"Yes": 1.0, "No": 0.0}`
	mkEv := func(name string) *store.EvalEvaluator {
		return &store.EvalEvaluator{
			ID:             idgen.New(),
			Name:           name,
			Type:           "llm_judge",
			Model:          &model,
			PromptTemplate: &tmpl,
			ChoiceScores:   &choiceScores,
		}
	}
	evaluatorSnapshot := []*store.EvalEvaluator{mkEv("e1"), mkEv("e2"), mkEv("e3")}

	svc := NewExecutionService(ExecutionServiceParams{
		Namespace:   "ns",
		Runs:        runRepo,
		Results:     resultRepo,
		Scores:      scoreRepo,
		Experiments: expRepo,
		Examples:    exampleRepo,
		LLMClient:   &slowStubLLM{delay: 200 * time.Millisecond},
	})

	task := &pendingTask{
		exampleID:  ex.ID,
		trialIndex: 0,
		example:    ex,
		startedAt:  time.Now(),
	}
	state := &runState{
		pending: map[string]*pendingTask{},
	}
	respText := "the answer"

	assertParallelDuration(t, 450*time.Millisecond, func() {
		svc.evaluateAndSave(ctx, evaluateAndSaveArgs{
			RunID:             run.ID,
			Experiment:        exp,
			EvaluatorSnapshot: evaluatorSnapshot,
			Task:              task,
			ResponseText:      &respText,
			Duration:          0,
			State:             state,
		})
	})

	results, err := resultRepo.ListByRun(ctx, run.ID)
	if err != nil {
		t.Fatalf("list results: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("results = %d, want 1", len(results))
	}
	if len(results[0].Scores) != 3 {
		t.Fatalf("scores = %d, want 3", len(results[0].Scores))
	}
}

func TestExecutionServiceCancelPropagatesViaBrokerEvent(t *testing.T) {
	db := store.TestDB(t)
	datasetRepo := store.NewEvalDatasetRepo(db)
	exampleRepo := store.NewEvalExampleRepo(db)
	expRepo := store.NewEvalExperimentRepo(db)
	evRepo := store.NewEvalEvaluatorRepo(db)
	runRepo := store.NewEvalRunRepo(db)
	resultRepo := store.NewEvalRunResultRepo(db)
	scoreRepo := store.NewEvalRunScoreRepo(db)

	ctx := t.Context()
	ds := &store.EvalDataset{Name: "cancel-fixture", CreatedBy: "alice", UpdatedBy: "alice"}
	if err := datasetRepo.Create(ctx, ds); err != nil {
		t.Fatalf("create dataset: %v", err)
	}
	cancelFile := "config.yaml"
	var fileExampleID string
	for i := 0; i < 2; i++ {
		ex := &store.EvalExample{
			DatasetID: ds.ID, SequenceNumber: i + 1,
			Prompt: fmt.Sprintf("prompt-%d", i),
		}
		if err := exampleRepo.Create(ctx, ex); err != nil {
			t.Fatalf("create example %d: %v", i, err)
		}
		if i == 0 {
			fileExampleID = ex.ID
			if err := store.NewEvalExampleArtifactRepo(db).Upsert(ctx, &store.EvalExampleArtifact{
				ExampleID: ex.ID, Role: store.EvalArtifactRoleInput,
				Filename: cancelFile, MIMEType: "application/x-yaml", SizeBytes: 10,
				Version: 0, SessionID: evalExampleArtifactSessionID(ex.ID, store.EvalArtifactRoleInput, "u"),
				ContentHash: "abc",
			}, nil); err != nil {
				t.Fatalf("attach input file: %v", err)
			}
		}
	}
	htype := "valid_json"
	ev := &store.EvalEvaluator{Name: "vj", Type: "heuristic", HeuristicType: &htype, IsDefault: true, CreatedBy: "system", UpdatedBy: "system"}
	if err := evRepo.Create(ctx, ev); err != nil {
		t.Fatalf("create evaluator: %v", err)
	}
	exp := &store.EvalExperiment{
		Name: "cancel-exp", TargetAgent: "SilentAgent", Models: "[]",
		RunsPerExample: 1, MaxWorkers: 2, DatasetID: &ds.ID,
		CreatedBy: "alice", UpdatedBy: "alice",
	}
	if err := expRepo.Create(ctx, exp, []string{ev.ID}); err != nil {
		t.Fatalf("create experiment: %v", err)
	}

	br := broker.NewDevBroker()
	if err := br.Connect(ctx); err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = br.Disconnect(ctx) })

	namespace := "test-ns"
	brokerCli := NewBrokerClient(br, namespace, nil, nil)
	exec := NewExecutionService(ExecutionServiceParams{
		Namespace: namespace,
		Broker:    br,
		BrokerCli: brokerCli,
		Artifacts: &artifact.ScopedService{
			Inner: artifactmem.New(), Scope: "namespace", Namespace: namespace,
		},
		Runs:        runRepo,
		Results:     resultRepo,
		Scores:      scoreRepo,
		Experiments: expRepo,
		Examples:    exampleRepo,
		Config: ExecutionConfig{
			ExampleBudget:    time.Hour,
			WatchdogInterval: time.Hour,
		},
	})
	if err := exec.Start(ctx); err != nil {
		t.Fatalf("start exec: %v", err)
	}
	t.Cleanup(exec.Stop)

	taskQueue, tqErr := br.CreateDirectSubscription()
	if tqErr != nil {
		t.Fatalf("task observer subscription: %v", tqErr)
	}
	if serr := br.Subscribe(taskQueue, a2a.AgentRequestTopic(namespace, exp.TargetAgent)); serr != nil {
		t.Fatalf("task observer subscribe: %v", serr)
	}

	trigger := NewTriggerPublisher(br, namespace)
	runSvc := NewRunService(RunServiceParams{
		Runs: runRepo, Results: resultRepo, Experiments: expRepo,
		Examples: exampleRepo, Evaluators: evRepo, Trigger: trigger,
	})
	runs, err := runSvc.TriggerRuns(ctx, exp.ID, "alice", "cancel-test", "manual")
	if err != nil {
		t.Fatalf("trigger: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("expected 1 run, got %d", len(runs))
	}
	runID := runs[0].ID

	recvCtx, recvCancel := context.WithTimeout(ctx, 60*time.Second)
	firstTask, recvErr := br.Receive(recvCtx, taskQueue)
	recvCancel()
	if recvErr != nil || firstTask == nil {
		t.Fatalf("no task reached %s within 60s of trigger: %v",
			a2a.AgentRequestTopic(namespace, exp.TargetAgent), recvErr)
	}

	cancelFiredAt := time.Now()
	if _, cerr := runSvc.CancelRun(ctx, runID); cerr != nil {
		t.Fatalf("cancel: %v", cerr)
	}

	var statusElapsed time.Duration
	statusObserved := false
	statusDeadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(statusDeadline) {
		row, err := runRepo.GetByID(ctx, runID)
		if err != nil {
			t.Fatalf("get run: %v", err)
		}
		if IsTerminalStatus(row.Status) {
			statusElapsed = time.Since(cancelFiredAt)
			if statusElapsed > 3*time.Second {
				t.Fatalf("cancel took %s — push-driven path should land in <3s", statusElapsed)
			}
			if row.Status != StatusCancelled {
				t.Fatalf("status = %q, want cancelled (user-cancel keeps the DB-set status)", row.Status)
			}
			statusObserved = true
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !statusObserved {
		t.Fatalf("run did not reach terminal state within 5s of cancel")
	}

	var results []*store.EvalRunResultWithScores
	rowsDeadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(rowsDeadline) {
		var lerr error
		results, lerr = resultRepo.ListByRun(ctx, runID)
		if lerr != nil {
			t.Fatalf("list results: %v", lerr)
		}
		if len(results) == 2 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if len(results) != 2 {
		t.Fatalf("attribution rows = %d, want 2 (one per pending task); status_latency=%s", len(results), statusElapsed)
	}
	for _, r := range results {
		if r.ErrorMessage == nil || !strings.Contains(*r.ErrorMessage, "Run cancelled") {
			t.Errorf("error_message = %v, want 'Run cancelled before task completed'", r.ErrorMessage)
		}
		if r.ExampleID != fileExampleID {
			continue
		}
		if rec := artifactFromMetadata(r.InputArtifactMetadata, r.ID, nil); rec == nil || rec.Filename != cancelFile {
			t.Errorf("cancelled row input artifact = %+v, want it to name %s", rec, cancelFile)
		}
	}
}

func TestCancelOfAThrottledRunAttributesUndispatchedTrials(t *testing.T) {
	const numExamples = 3
	db := store.TestDB(t)
	datasetRepo := store.NewEvalDatasetRepo(db)
	exampleRepo := store.NewEvalExampleRepo(db)
	expRepo := store.NewEvalExperimentRepo(db)
	evRepo := store.NewEvalEvaluatorRepo(db)
	runRepo := store.NewEvalRunRepo(db)
	resultRepo := store.NewEvalRunResultRepo(db)

	ctx := t.Context()
	ds := &store.EvalDataset{Name: "throttled-cancel", CreatedBy: "alice", UpdatedBy: "alice"}
	if err := datasetRepo.Create(ctx, ds); err != nil {
		t.Fatalf("create dataset: %v", err)
	}
	for i := range numExamples {
		if err := exampleRepo.Create(ctx, &store.EvalExample{DatasetID: ds.ID, SequenceNumber: i + 1, Prompt: fmt.Sprintf("prompt-%d", i)}); err != nil {
			t.Fatalf("create example %d: %v", i, err)
		}
	}
	htype := "valid_json"
	ev := &store.EvalEvaluator{Name: "vj", Type: "heuristic", HeuristicType: &htype, IsDefault: true, CreatedBy: "system", UpdatedBy: "system"}
	if err := evRepo.Create(ctx, ev); err != nil {
		t.Fatalf("create evaluator: %v", err)
	}
	exp := &store.EvalExperiment{
		Name: "throttled-cancel-exp", TargetAgent: "SilentAgent", Models: "[]",
		RunsPerExample: 1, MaxWorkers: 1, DatasetID: &ds.ID,
		CreatedBy: "alice", UpdatedBy: "alice",
	}
	if err := expRepo.Create(ctx, exp, []string{ev.ID}); err != nil {
		t.Fatalf("create experiment: %v", err)
	}

	br := broker.NewDevBroker()
	if err := br.Connect(ctx); err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = br.Disconnect(context.Background()) })
	namespace := "test-ns"
	exec := NewExecutionService(ExecutionServiceParams{
		Namespace: namespace, Broker: br, BrokerCli: NewBrokerClient(br, namespace, nil, nil),
		Runs: runRepo, Results: resultRepo, Scores: store.NewEvalRunScoreRepo(db),
		Experiments: expRepo, Examples: exampleRepo,
		Config: ExecutionConfig{ExampleBudget: time.Hour, WatchdogInterval: time.Hour},
	})
	if err := exec.Start(ctx); err != nil {
		t.Fatalf("start exec: %v", err)
	}
	t.Cleanup(exec.Stop)

	taskQueue, err := br.CreateDirectSubscription()
	if err != nil {
		t.Fatalf("task observer subscription: %v", err)
	}
	if subErr := br.Subscribe(taskQueue, a2a.AgentRequestTopic(namespace, exp.TargetAgent)); subErr != nil {
		t.Fatalf("task observer subscribe: %v", subErr)
	}

	runSvc := NewRunService(RunServiceParams{
		Runs: runRepo, Results: resultRepo, Experiments: expRepo,
		Examples: exampleRepo, Evaluators: evRepo, Trigger: NewTriggerPublisher(br, namespace),
	})
	runs, err := runSvc.TriggerRuns(ctx, exp.ID, "alice", "throttled-cancel", "manual")
	if err != nil {
		t.Fatalf("trigger: %v", err)
	}
	runID := runs[0].ID

	recvCtx, recvCancel := context.WithTimeout(ctx, 60*time.Second)
	first, recvErr := br.Receive(recvCtx, taskQueue)
	recvCancel()
	if recvErr != nil || first == nil {
		t.Fatalf("no task reached the agent: %v", recvErr)
	}
	if _, cancelErr := runSvc.CancelRun(ctx, runID); cancelErr != nil {
		t.Fatalf("cancel: %v", cancelErr)
	}

	var results []*store.EvalRunResultWithScores
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if results, err = resultRepo.ListByRun(ctx, runID); err != nil {
			t.Fatalf("list results: %v", err)
		}
		if len(results) == numExamples {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if len(results) != numExamples {
		t.Fatalf("attribution rows = %d, want %d: the in-flight trial and the two never dispatched", len(results), numExamples)
	}
	for _, r := range results {
		if r.ErrorMessage == nil || !strings.Contains(*r.ErrorMessage, "Run cancelled") {
			t.Errorf("error_message = %v, want the cancel attribution", r.ErrorMessage)
		}
	}
	row, err := runRepo.GetByID(ctx, runID)
	if err != nil || row.Status != StatusCancelled {
		t.Fatalf("run status = %v (%v), want cancelled", row, err)
	}
	for {
		nextCtx, nextCancel := context.WithTimeout(ctx, 300*time.Millisecond)
		msg, nextErr := br.Receive(nextCtx, taskQueue)
		nextCancel()
		if errors.Is(nextErr, context.DeadlineExceeded) {
			break
		}
		if nextErr != nil {
			t.Fatalf("receive on the agent queue: %v", nextErr)
		}
		var req struct {
			Method string `json:"method"`
		}
		_ = json.Unmarshal(msg.Payload, &req)
		if req.Method != "tasks/cancel" {
			t.Errorf("a second trial (%s) reached the agent; maxWorkers 1 should have held it back", req.Method)
		}
	}
}

func TestWriteUndispatchedAttributionsBackFillsOnlyMissingRows(t *testing.T) {
	db := store.TestDB(t)
	datasetRepo := store.NewEvalDatasetRepo(db)
	exampleRepo := store.NewEvalExampleRepo(db)
	expRepo := store.NewEvalExperimentRepo(db)
	evRepo := store.NewEvalEvaluatorRepo(db)
	runRepo := store.NewEvalRunRepo(db)
	resultRepo := store.NewEvalRunResultRepo(db)

	ctx := t.Context()
	ds := &store.EvalDataset{Name: "backfill-fixture", CreatedBy: "alice", UpdatedBy: "alice"}
	if err := datasetRepo.Create(ctx, ds); err != nil {
		t.Fatalf("create dataset: %v", err)
	}
	backfillFile := "config.yaml"
	exA := &store.EvalExample{DatasetID: ds.ID, SequenceNumber: 1, Prompt: "prompt-A"}
	exB := &store.EvalExample{DatasetID: ds.ID, SequenceNumber: 2, Prompt: "prompt-B"}
	if err := exampleRepo.Create(ctx, exA); err != nil {
		t.Fatalf("create exA: %v", err)
	}
	if err := exampleRepo.Create(ctx, exB); err != nil {
		t.Fatalf("create exB: %v", err)
	}
	if err := store.NewEvalExampleArtifactRepo(db).Upsert(ctx, &store.EvalExampleArtifact{
		ExampleID: exB.ID, Role: store.EvalArtifactRoleInput,
		Filename: backfillFile, MIMEType: "application/x-yaml", SizeBytes: 10,
		Version: 0, SessionID: evalExampleArtifactSessionID(exB.ID, store.EvalArtifactRoleInput, "u"),
		ContentHash: "abc",
	}, nil); err != nil {
		t.Fatalf("attach input file: %v", err)
	}
	exB, reloadErr := exampleRepo.GetByID(ctx, exB.ID)
	if reloadErr != nil {
		t.Fatalf("reload exB: %v", reloadErr)
	}
	htype := "valid_json"
	ev := &store.EvalEvaluator{Name: "vj", Type: "heuristic", HeuristicType: &htype, IsDefault: true, CreatedBy: "system", UpdatedBy: "system"}
	if err := evRepo.Create(ctx, ev); err != nil {
		t.Fatalf("create evaluator: %v", err)
	}
	exp := &store.EvalExperiment{
		Name: "backfill-exp", TargetAgent: "AnyAgent", Models: "[]",
		RunsPerExample: 1, MaxWorkers: 1, DatasetID: &ds.ID,
		CreatedBy: "alice", UpdatedBy: "alice",
	}
	if err := expRepo.Create(ctx, exp, []string{ev.ID}); err != nil {
		t.Fatalf("create experiment: %v", err)
	}
	run := &store.EvalRun{ExperimentID: exp.ID, Status: StatusRunning, StartedBy: "alice"}
	if err := runRepo.Create(ctx, run); err != nil {
		t.Fatalf("create run: %v", err)
	}
	existingMsg := "Run cancelled before task completed"
	if err := resultRepo.Create(ctx, &store.EvalRunResult{
		RunID: run.ID, ExampleID: exA.ID, TrialIndex: 0,
		InputPrompt: &exA.Prompt, ErrorMessage: &existingMsg,
	}); err != nil {
		t.Fatalf("seed existing result: %v", err)
	}

	svc := NewExecutionService(ExecutionServiceParams{
		Namespace: "test-ns",
		Runs:      runRepo,
		Results:   resultRepo,
	})
	state := &runState{
		pending: map[string]*pendingTask{},
		allWork: []workItem{
			{exampleID: exA.ID, trialIndex: 0},
			{exampleID: exB.ID, trialIndex: 0},
		},
		examplesByID: map[string]*store.EvalExample{exA.ID: exA, exB.ID: exB},
	}
	svc.writeUndispatchedAttributions(ctx, run.ID, exp, state, trialAttribution{message: existingMsg})

	rows, err := resultRepo.ListByRun(ctx, run.ID)
	if err != nil {
		t.Fatalf("list results: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2 (one back-filled + one pre-existing)", len(rows))
	}
	byEx := map[string]*store.EvalRunResult{}
	for _, r := range rows {
		byEx[r.ExampleID] = r.EvalRunResult
	}
	if _, ok := byEx[exA.ID]; !ok {
		t.Errorf("pre-existing row for exA missing")
	}
	rb, ok := byEx[exB.ID]
	if !ok {
		t.Fatalf("back-fill row for exB missing")
	}
	if rb.ErrorMessage == nil || *rb.ErrorMessage != existingMsg {
		t.Errorf("back-fill error_message = %v, want %q", rb.ErrorMessage, existingMsg)
	}
	if rb.InputPrompt == nil || *rb.InputPrompt != exB.Prompt {
		t.Errorf("back-fill input_prompt = %v, want %q", rb.InputPrompt, exB.Prompt)
	}
	if rb.TargetAgent == nil || *rb.TargetAgent != exp.TargetAgent {
		t.Errorf("back-fill target_agent = %v, want %q", rb.TargetAgent, exp.TargetAgent)
	}
	if rec := artifactFromMetadata(rb.InputArtifactMetadata, rb.ID, nil); rec == nil || rec.Filename != backfillFile {
		t.Errorf("back-fill input artifact = %+v, want it to name %s", rec, backfillFile)
	}
}

func TestLivenessLoopExitsEarlyWithoutRegistry(t *testing.T) {
	t.Parallel()
	svc := NewExecutionService(ExecutionServiceParams{Namespace: "ns"})
	done := make(chan struct{})
	go func() {
		svc.livenessLoop(t.Context(), "run-x", "AgentX", "AgentX")
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("livenessLoop did not return immediately when agentRegistry is nil")
	}
}

func TestUploadTaskArtifactsRoundTripsThroughStorage(t *testing.T) {
	db := store.TestDB(t)
	datasetRepo := store.NewEvalDatasetRepo(db)
	exampleRepo := store.NewEvalExampleRepo(db)
	expRepo := store.NewEvalExperimentRepo(db)
	evRepo := store.NewEvalEvaluatorRepo(db)
	runRepo := store.NewEvalRunRepo(db)
	resultRepo := store.NewEvalRunResultRepo(db)

	ctx := t.Context()
	ds := &store.EvalDataset{Name: "uploadfix", CreatedBy: "alice", UpdatedBy: "alice"}
	if err := datasetRepo.Create(ctx, ds); err != nil {
		t.Fatalf("dataset: %v", err)
	}
	ex := &store.EvalExample{DatasetID: ds.ID, SequenceNumber: 1, Prompt: "p"}
	if err := exampleRepo.Create(ctx, ex); err != nil {
		t.Fatalf("example: %v", err)
	}
	htype := "valid_json"
	ev := &store.EvalEvaluator{Name: "vj", Type: "heuristic", HeuristicType: &htype, IsDefault: true, CreatedBy: "system", UpdatedBy: "system"}
	if err := evRepo.Create(ctx, ev); err != nil {
		t.Fatalf("evaluator: %v", err)
	}
	exp := &store.EvalExperiment{
		Name: "uploadfix-exp", TargetAgent: "AnyAgent", Models: "[]",
		RunsPerExample: 1, MaxWorkers: 1, DatasetID: &ds.ID,
		CreatedBy: "alice", UpdatedBy: "alice",
	}
	if err := expRepo.Create(ctx, exp, []string{ev.ID}); err != nil {
		t.Fatalf("experiment: %v", err)
	}
	run := &store.EvalRun{ExperimentID: exp.ID, Status: StatusRunning, StartedBy: "alice"}
	if err := runRepo.Create(ctx, run); err != nil {
		t.Fatalf("run: %v", err)
	}
	resultRow := &store.EvalRunResult{RunID: run.ID, ExampleID: ex.ID, TrialIndex: 0, InputPrompt: &ex.Prompt}
	if err := resultRepo.Create(ctx, resultRow); err != nil {
		t.Fatalf("result: %v", err)
	}

	mem := evalstorage.NewMemory()
	storageSvc := NewStorageService(mem, "ns")
	svc := NewExecutionService(ExecutionServiceParams{
		Namespace: "ns",
		Runs:      runRepo,
		Results:   resultRepo,
		Storage:   storageSvc,
	})

	task := &pendingTask{
		taskID:     "task-1",
		exampleID:  ex.ID,
		trialIndex: 0,
		example:    ex,
		messages: []map[string]any{
			{"topic": "ns/a2a/v1/.../response/run/task", "wall_time": "2026-05-29T14:00:00Z",
				"payload": makeStatusMessage(map[string]any{"type": "tool_invocation_start", "tool": "search"})},
			{"topic": "ns/a2a/v1/.../response/run/task", "wall_time": "2026-05-29T14:00:01Z",
				"payload": makeStatusMessage(map[string]any{"type": "raw_token_stream", "text": "hello"})},
		},
	}
	resp := "the agent response"
	svc.uploadTaskArtifacts(run.ID, resultRow, task, &resp, 1.25)

	got := resultRow
	if got.ExecutionDataKey == nil || *got.ExecutionDataKey == "" {
		t.Fatalf("execution_data_key not set; got=%v", got.ExecutionDataKey)
	}
	if got.TaskEventsKey == nil || *got.TaskEventsKey == "" {
		t.Fatalf("task_events_key not set; got=%v", got.TaskEventsKey)
	}

	exec, err := storageSvc.GetExecutionData(*got.ExecutionDataKey)
	if err != nil {
		t.Fatalf("get exec data: %v", err)
	}
	if exec["run_id"] != run.ID {
		t.Errorf("exec run_id = %v, want %v", exec["run_id"], run.ID)
	}
	if exec["agent_response"] != resp {
		t.Errorf("exec agent_response = %v, want %q", exec["agent_response"], resp)
	}

	events, err := storageSvc.GetTaskEvents(*got.TaskEventsKey)
	if err != nil {
		t.Fatalf("get task events: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("events = %d, want 2 (every message mapped)", len(events))
	}
	if events[0]["event_type"] != "a2a_message" {
		t.Errorf("event event_type = %v, want a2a_message", events[0]["event_type"])
	}
	if _, ok := events[0]["full_payload"].(map[string]any); !ok {
		t.Errorf("event missing full_payload (FE visualizer reads it)")
	}
	if events[0]["timestamp"] != "2026-05-29T14:00:00Z" {
		t.Errorf("event timestamp = %v, want captured wall_time", events[0]["timestamp"])
	}
}

func TestUploadTaskArtifactsSkipsWhenNoStorage(t *testing.T) {
	db := store.TestDB(t)
	resultRepo := store.NewEvalRunResultRepo(db)
	svc := NewExecutionService(ExecutionServiceParams{Namespace: "ns", Results: resultRepo})

	task := &pendingTask{
		taskID:     "task-1",
		exampleID:  idgen.New(),
		trialIndex: 0,
		messages:   []map[string]any{{"payload": makeStatusMessage(map[string]any{"type": "tool_invocation_start"})}},
	}
	svc.uploadTaskArtifacts("run-x", &store.EvalRunResult{ID: idgen.New()}, task, nil, 0)
}

func TestUploadTaskArtifactsCapturesEventsForNoToolResponse(t *testing.T) {
	db := store.TestDB(t)
	datasetRepo := store.NewEvalDatasetRepo(db)
	exampleRepo := store.NewEvalExampleRepo(db)
	expRepo := store.NewEvalExperimentRepo(db)
	evRepo := store.NewEvalEvaluatorRepo(db)
	runRepo := store.NewEvalRunRepo(db)
	resultRepo := store.NewEvalRunResultRepo(db)
	ctx := t.Context()

	ds := &store.EvalDataset{Name: "noevents", CreatedBy: "u", UpdatedBy: "u"}
	if err := datasetRepo.Create(ctx, ds); err != nil {
		t.Fatalf("dataset: %v", err)
	}
	ex := &store.EvalExample{DatasetID: ds.ID, SequenceNumber: 1, Prompt: "p"}
	if err := exampleRepo.Create(ctx, ex); err != nil {
		t.Fatalf("example: %v", err)
	}
	htype := "valid_json"
	ev := &store.EvalEvaluator{Name: "vj", Type: "heuristic", HeuristicType: &htype, IsDefault: true, CreatedBy: "system", UpdatedBy: "system"}
	if err := evRepo.Create(ctx, ev); err != nil {
		t.Fatalf("evaluator: %v", err)
	}
	exp := &store.EvalExperiment{
		Name: "noevents-exp", TargetAgent: "AnyAgent", Models: "[]",
		RunsPerExample: 1, MaxWorkers: 1, DatasetID: &ds.ID,
		CreatedBy: "u", UpdatedBy: "u",
	}
	if err := expRepo.Create(ctx, exp, []string{ev.ID}); err != nil {
		t.Fatalf("experiment: %v", err)
	}
	run := &store.EvalRun{ExperimentID: exp.ID, Status: StatusRunning, StartedBy: "u"}
	if err := runRepo.Create(ctx, run); err != nil {
		t.Fatalf("run: %v", err)
	}
	resultRow := &store.EvalRunResult{RunID: run.ID, ExampleID: ex.ID, TrialIndex: 0}
	if err := resultRepo.Create(ctx, resultRow); err != nil {
		t.Fatalf("result: %v", err)
	}

	mem := evalstorage.NewMemory()
	storageSvc := NewStorageService(mem, "ns")
	svc := NewExecutionService(ExecutionServiceParams{Namespace: "ns", Runs: runRepo, Results: resultRepo, Storage: storageSvc})

	task := &pendingTask{
		taskID:     "task-1",
		exampleID:  ex.ID,
		trialIndex: 0,
		example:    ex,
		messages: []map[string]any{
			{"topic": "ns/a2a/v1/.../response/run/task", "wall_time": "2026-05-29T14:00:00Z",
				"payload": makeStatusMessage(map[string]any{"type": "llm_response"})},
		},
	}
	svc.uploadTaskArtifacts(run.ID, resultRow, task, nil, 0)

	got := resultRow
	if got.ExecutionDataKey == nil || *got.ExecutionDataKey == "" {
		t.Errorf("execution_data_key must be set")
	}
	if got.TaskEventsKey == nil || *got.TaskEventsKey == "" {
		t.Fatalf("task_events_key must be set for a no-tool response; got=%v", got.TaskEventsKey)
	}
	events, err := storageSvc.GetTaskEvents(*got.TaskEventsKey)
	if err != nil {
		t.Fatalf("get task events: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("events = %d, want 1", len(events))
	}
	if events[0]["event_type"] != "a2a_message" {
		t.Errorf("event event_type = %v, want a2a_message", events[0]["event_type"])
	}
}

func TestWriteUndispatchedAttributionsHandlesEmptyAllWork(t *testing.T) {
	db := store.TestDB(t)
	resultRepo := store.NewEvalRunResultRepo(db)
	svc := NewExecutionService(ExecutionServiceParams{
		Namespace: "test-ns",
		Results:   resultRepo,
	})
	state := &runState{
		pending:      map[string]*pendingTask{},
		examplesByID: map[string]*store.EvalExample{},
	}
	exp := &store.EvalExperiment{TargetAgent: "X"}
	runID := idgen.New()
	svc.writeUndispatchedAttributions(t.Context(), runID, exp, state, trialAttribution{message: "msg"})
	rows, err := resultRepo.ListByRun(t.Context(), runID)
	if err != nil {
		t.Fatalf("list results: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("rows = %d, want 0", len(rows))
	}
}

func TestExecutionServiceSkipsExampleWithUnusableInputFile(t *testing.T) {
	db := store.TestDB(t)
	datasetRepo := store.NewEvalDatasetRepo(db)
	exampleRepo := store.NewEvalExampleRepo(db)
	expRepo := store.NewEvalExperimentRepo(db)
	evRepo := store.NewEvalEvaluatorRepo(db)
	runRepo := store.NewEvalRunRepo(db)
	resultRepo := store.NewEvalRunResultRepo(db)
	scoreRepo := store.NewEvalRunScoreRepo(db)

	ctx := context.Background()
	ds := &store.EvalDataset{Name: "mixed", CreatedBy: "alice", UpdatedBy: "alice"}
	if err := datasetRepo.Create(ctx, ds); err != nil {
		t.Fatalf("create dataset: %v", err)
	}
	const textPrompt = `produce JSON: {"k":1}`
	textOnly := &store.EvalExample{DatasetID: ds.ID, SequenceNumber: 1, Prompt: textPrompt}
	if err := exampleRepo.Create(ctx, textOnly); err != nil {
		t.Fatalf("create text example: %v", err)
	}
	withFile := &store.EvalExample{DatasetID: ds.ID, SequenceNumber: 2, Prompt: "review the attached"}
	if err := exampleRepo.Create(ctx, withFile); err != nil {
		t.Fatalf("create file example: %v", err)
	}
	if err := store.NewEvalExampleArtifactRepo(db).Upsert(ctx, &store.EvalExampleArtifact{
		ExampleID: withFile.ID, Role: store.EvalArtifactRoleInput,
		Filename: "config.yaml", MIMEType: "application/x-yaml", SizeBytes: 10,
		Version: 0, SessionID: evalExampleArtifactSessionID(withFile.ID, store.EvalArtifactRoleInput, "u"),
		ContentHash: "hash",
	}, nil); err != nil {
		t.Fatalf("set artifact: %v", err)
	}

	htype := "valid_json"
	ev := &store.EvalEvaluator{
		Name: "valid-json", Type: "heuristic", HeuristicType: &htype,
		IsDefault: true, CreatedBy: "system", UpdatedBy: "system",
	}
	if err := evRepo.Create(ctx, ev); err != nil {
		t.Fatalf("create evaluator: %v", err)
	}
	exp := &store.EvalExperiment{
		Name: "mixed-exp", TargetAgent: "Orchestrator", Models: "[]",
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
	exec := NewExecutionService(ExecutionServiceParams{
		Namespace: namespace, Broker: br, BrokerCli: NewBrokerClient(br, namespace, nil, nil),
		Storage: storageSvc, Runs: runRepo, Results: resultRepo, Scores: scoreRepo,
		Experiments: expRepo, Examples: exampleRepo,
		Config: ExecutionConfig{ExampleBudget: 10 * time.Second, WatchdogInterval: 500 * time.Millisecond},
	})

	agent := newFakeAgent(t, br, namespace, "Orchestrator", map[string]string{textPrompt: `{"k":1}`})
	t.Cleanup(agent.Stop)
	if err := exec.Start(ctx); err != nil {
		t.Fatalf("start exec: %v", err)
	}
	t.Cleanup(exec.Stop)

	runs, err := runSvc.TriggerRuns(ctx, exp.ID, "alice", "mixed", "manual")
	if err != nil {
		t.Fatalf("trigger: %v", err)
	}
	runID := runs[0].ID

	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		row, err := runRepo.GetByID(ctx, runID)
		if err != nil {
			t.Fatalf("get run: %v", err)
		}
		if !IsTerminalStatus(row.Status) {
			time.Sleep(50 * time.Millisecond)
			continue
		}
		if row.Status != StatusCompletedWithWarnings {
			t.Fatalf("final status = %q, want completed_with_warnings (error_message=%v)",
				row.Status, derefOrEmpty(row.ErrorMessage))
		}
		results, err := resultRepo.ListByRun(ctx, runID)
		if err != nil {
			t.Fatalf("list results: %v", err)
		}
		if len(results) != 2 {
			t.Fatalf("results = %d, want one per example", len(results))
		}
		byExample := make(map[string]*store.EvalRunResultWithScores, len(results))
		for _, r := range results {
			byExample[r.ExampleID] = r
		}
		good := byExample[textOnly.ID]
		if good == nil || good.AgentResponse == nil || !strings.Contains(*good.AgentResponse, `"k":1`) {
			t.Errorf("text-only example result = %+v, want the agent's answer", good)
		}
		if good != nil && good.ErrorMessage != nil {
			t.Errorf("text-only example errorMessage = %q, want none", *good.ErrorMessage)
		}
		skipped := byExample[withFile.ID]
		if skipped == nil || skipped.ErrorMessage == nil {
			t.Fatalf("file example result = %+v, want an attribution row with an error", skipped)
		}
		if !strings.Contains(*skipped.ErrorMessage, "artifact_service") {
			t.Errorf("errorMessage = %q, want it to name the missing artifact_service", *skipped.ErrorMessage)
		}
		if skipped.AgentResponse != nil {
			t.Errorf("file example agentResponse = %q, want none — it was never published", *skipped.AgentResponse)
		}
		if skipped.InputArtifactMetadata == nil {
			t.Error("file example inputArtifactMetadata = nil, want the file the trial was denied")
		} else if rec := artifactFromMetadata(skipped.InputArtifactMetadata, skipped.ID, nil); rec == nil || rec.Filename != "config.yaml" {
			t.Errorf("inputArtifactMetadata = %q, want it to name config.yaml", *skipped.InputArtifactMetadata)
		}
		if good != nil && good.InputArtifactMetadata != nil {
			t.Errorf("text-only example inputArtifactMetadata = %q, want nil", *good.InputArtifactMetadata)
		}
		return
	}
	t.Fatalf("run did not reach a terminal state within deadline — the skip path likely never advanced completed")
}

func TestExampleInputFile(t *testing.T) {
	t.Parallel()

	const filename, mime = "config.yaml", "application/x-yaml"
	withFile := func() *store.EvalExample {
		return &store.EvalExample{
			ID: "abc", Prompt: "p",
			Artifacts: []*store.EvalExampleArtifact{{
				ExampleID: "abc", Role: store.EvalArtifactRoleInput,
				Filename: filename, MIMEType: mime, Version: 0,
				SessionID: "eval-example-abc",
			}},
		}
	}

	t.Run("no file", func(t *testing.T) {
		svc := &ExecutionService{}
		att, err := svc.exampleInputFile(&store.EvalExample{ID: "abc", Prompt: "p"})
		if err != nil || att != nil {
			t.Fatalf("att=%v err=%v, want (nil, nil)", att, err)
		}
	})

	t.Run("namespace scope builds the URI", func(t *testing.T) {
		svc := &ExecutionService{artifacts: &artifact.ScopedService{
			Inner: artifactmem.New(), Scope: "namespace", Namespace: "ns",
		}}
		att, err := svc.exampleInputFile(withFile())
		if err != nil {
			t.Fatalf("exampleInputFile: %v", err)
		}
		const want = "artifact://ns/eval-runner/eval-example-abc/config.yaml?version=0"
		if att.URI != want {
			t.Errorf("uri = %q, want %q", att.URI, want)
		}
		if att.Filename != filename || att.MIMEType != mime {
			t.Errorf("att = %+v, want the row's filename and mime type", att)
		}
	})

	t.Run("no artifact service", func(t *testing.T) {
		svc := &ExecutionService{}
		if _, err := svc.exampleInputFile(withFile()); err == nil ||
			!strings.Contains(err.Error(), "no artifact_service") {
			t.Errorf("err = %v, want it to name the missing artifact_service", err)
		}
	})

	t.Run("agent scope", func(t *testing.T) {
		svc := &ExecutionService{artifacts: &artifact.ScopedService{Inner: artifactmem.New(), Scope: "agent"}}
		if _, err := svc.exampleInputFile(withFile()); err == nil ||
			!strings.Contains(err.Error(), "not namespace") {
			t.Errorf("err = %v, want it to name the wrong scope", err)
		}
	})

	t.Run("hierarchical namespace", func(t *testing.T) {
		svc := &ExecutionService{artifacts: &artifact.ScopedService{
			Inner: artifactmem.New(), Scope: "namespace", Namespace: "myorg/prod",
		}}
		if _, err := svc.exampleInputFile(withFile()); err == nil ||
			!strings.Contains(err.Error(), "cannot address it") {
			t.Errorf("err = %v, want it to name the unaddressable namespace", err)
		}
	})

	t.Run("an expected-only example delivers nothing", func(t *testing.T) {
		svc := &ExecutionService{artifacts: &artifact.ScopedService{
			Inner: artifactmem.New(), Scope: "namespace", Namespace: "ns",
		}}
		row := &store.EvalExample{
			ID: "abc", Prompt: "p",
			Artifacts: []*store.EvalExampleArtifact{{
				ExampleID: "abc", Role: store.EvalArtifactRoleExpected,
				Filename: "reference.md", MIMEType: "text/markdown", Version: 0,
				SessionID: "eval-example-abc-expected",
			}},
		}
		att, err := svc.exampleInputFile(row)
		if err != nil || att != nil {
			t.Fatalf("att=%v err=%v, want (nil, nil) — the expected file is never delivered", att, err)
		}
	})
}

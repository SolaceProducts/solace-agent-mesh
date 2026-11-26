// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eval

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	artifactmem "github.com/SolaceProducts/solace-agent-mesh/internal/artifact/memory"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

type artifactFakeAgent struct {
	t            *testing.T
	br           broker.Broker
	queue        broker.Queue
	arts         *artifact.ScopedService
	agentName    string
	artifactName string
	artifactBody string
	wg           sync.WaitGroup
	cancel       context.CancelFunc
}

func newArtifactFakeAgent(t *testing.T, br broker.Broker, arts *artifact.ScopedService, namespace, agentName, artifactName, artifactBody string) *artifactFakeAgent {
	t.Helper()
	q, err := br.CreateDirectSubscription()
	if err != nil {
		t.Fatalf("agent direct sub: %v", err)
	}
	if err := br.Subscribe(q, a2a.AgentRequestTopic(namespace, agentName)); err != nil {
		t.Fatalf("agent subscribe: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	a := &artifactFakeAgent{
		t: t, br: br, queue: q, arts: arts, agentName: agentName,
		artifactName: artifactName, artifactBody: artifactBody, cancel: cancel,
	}
	a.wg.Add(1)
	go a.run(ctx)
	return a
}

func (a *artifactFakeAgent) Stop() {
	a.cancel()
	a.wg.Wait()
}

func (a *artifactFakeAgent) run(ctx context.Context) {
	defer a.wg.Done()
	for {
		msg, err := a.br.Receive(ctx, a.queue)
		if err != nil || msg == nil {
			return
		}
		var req struct {
			ID     string `json:"id"`
			Params struct {
				Message struct {
					TaskID string `json:"taskId"`
				} `json:"message"`
			} `json:"params"`
		}
		if uerr := json.Unmarshal(msg.Payload, &req); uerr != nil {
			continue
		}
		taskID := req.Params.Message.TaskID
		loc := a.arts.Resolve(a.agentName, msg.UserProperties[a2a.PropUserID], taskID, a.artifactName)
		if _, serr := a.arts.Inner.Save(ctx, loc, []byte(a.artifactBody), "text/markdown"); serr != nil {
			a.t.Errorf("agent save artifact: %v", serr)
			continue
		}
		responseText := "Here is your calendar:\n«artifact_content:" + a.artifactName + "»"
		response := map[string]any{
			"jsonrpc": "2.0",
			"id":      req.ID,
			"result": map[string]any{
				"final": true,
				"status": map[string]any{
					"state": "completed",
					"message": map[string]any{
						"contextId": taskID,
						"parts":     []map[string]any{{"kind": "text", "text": responseText}},
					},
				},
			},
		}
		body, err := json.Marshal(response)
		if err != nil {
			continue
		}
		if msg.ReplyTo == "" {
			continue
		}
		_ = a.br.Publish(ctx, msg.ReplyTo, &broker.Message{Topic: msg.ReplyTo, Payload: body})
	}
}

func runEmbedResolutionFixture(t *testing.T, artifactName, artifactBody, runAs string) *store.EvalRunResultWithScores {
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
	ex := &store.EvalExample{DatasetID: ds.ID, SequenceNumber: 1, Prompt: "render my calendar"}
	if err := exampleRepo.Create(ctx, ex); err != nil {
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
		Name: "fixture-exp", TargetAgent: "Orchestrator", Models: "[]",
		RunsPerExample: 1, MaxWorkers: 1, DatasetID: &ds.ID,
		CreatedBy: "alice", UpdatedBy: "alice",
	}
	if runAs != "" {
		exp.RunAs = &runAs
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
	arts := &artifact.ScopedService{Inner: artifactmem.New(), Scope: "namespace", Namespace: namespace}

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
		Artifacts: arts,
		Config: ExecutionConfig{
			ExampleBudget:    10 * time.Second,
			WatchdogInterval: 500 * time.Millisecond,
		},
	})

	agent := newArtifactFakeAgent(t, br, arts, namespace, "Orchestrator", artifactName, artifactBody)
	t.Cleanup(agent.Stop)

	if err := exec.Start(ctx); err != nil {
		t.Fatalf("start exec: %v", err)
	}
	t.Cleanup(exec.Stop)

	runs, err := runSvc.TriggerRuns(ctx, exp.ID, "alice", "embed", "manual")
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
		if IsTerminalStatus(row.Status) {
			results, err := resultRepo.ListByRun(ctx, runID)
			if err != nil {
				t.Fatalf("list results: %v", err)
			}
			if len(results) != 1 {
				t.Fatalf("results = %d, want 1", len(results))
			}
			return results[0]
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("run did not reach a terminal state within deadline")
	return nil
}

func TestExecutionServiceResolvesArtifactContentEmbed(t *testing.T) {
	body := "# August 2026\n\n| Mon | Tue | Wed |\n| 1 | 2 | 3 |"
	res := runEmbedResolutionFixture(t, "calendar_2026_08.md", body, "")
	if res.AgentResponse == nil {
		t.Fatalf("agent response is nil")
	}
	got := *res.AgentResponse
	if strings.Contains(got, "«artifact_content:") {
		t.Fatalf("response still contains raw embed directive: %q", got)
	}
	if !strings.Contains(got, "# August 2026") || !strings.Contains(got, "| 1 | 2 | 3 |") {
		t.Fatalf("response missing resolved artifact content: %q", got)
	}
}

func TestSnapshotProducedArtifactsUsesRunPrincipal(t *testing.T) {
	ctx := context.Background()
	namespace := "test-ns"
	arts := &artifact.ScopedService{Inner: artifactmem.New(), Scope: "namespace", Namespace: namespace}
	exec := NewExecutionService(ExecutionServiceParams{Namespace: namespace, Artifacts: arts})

	const principal = "system:qa-bot"
	task := &pendingTask{taskID: "task-qa", agentName: "Orchestrator", principal: principal}
	if task.userScope() != principal {
		t.Fatalf("userScope() = %q, want %q", task.userScope(), principal)
	}
	body := []byte("# QA report\nverdict: ok")
	if _, err := arts.Inner.Save(ctx, arts.Resolve(task.agentName, principal, task.taskID, "report.md"), body, "text/markdown"); err != nil {
		t.Fatalf("seed artifact under %s: %v", principal, err)
	}

	blocks := exec.snapshotProducedArtifacts(ctx, task)
	if len(blocks) != 1 {
		t.Fatalf("blocks = %d, want 1 (the principal-scoped artifact must be found)", len(blocks))
	}
	text, ok := blocks[0].(ArtifactJudgeText)
	if !ok {
		t.Fatalf("blocks[0] = %T, want ArtifactJudgeText", blocks[0])
	}
	if text.Text != string(body) {
		t.Errorf("inlined text = %q, want %q", text.Text, body)
	}

	defaulted := &pendingTask{taskID: task.taskID, agentName: task.agentName, principal: rbac.EvalSystemUserSub}
	if blocks := exec.snapshotProducedArtifacts(ctx, defaulted); len(blocks) != 0 {
		t.Errorf("default-scoped read found %d blocks; the principal scopes must be distinct", len(blocks))
	}
}

func TestSnapshotProducedArtifacts(t *testing.T) {
	ctx := context.Background()
	namespace := "test-ns"
	arts := &artifact.ScopedService{Inner: artifactmem.New(), Scope: "namespace", Namespace: namespace}
	exec := NewExecutionService(ExecutionServiceParams{Namespace: namespace, Artifacts: arts})

	task := &pendingTask{taskID: "task-1", agentName: "Orchestrator", principal: rbac.EvalSystemUserSub}
	save := func(name, mime string, body []byte) {
		loc := arts.Resolve(task.agentName, task.userScope(), task.taskID, name)
		if _, err := arts.Inner.Save(ctx, loc, body, mime); err != nil {
			t.Fatalf("save %s: %v", name, err)
		}
	}
	save("notes.md", "text/markdown", []byte("# Notes\nhello"))
	save("logo.png", "image/png", []byte{0x89, 0x50, 0x4e, 0x47})

	blocks := exec.snapshotProducedArtifacts(ctx, task)
	if len(blocks) != 2 {
		t.Fatalf("blocks = %d, want 2", len(blocks))
	}
	byName := map[string]ArtifactBlock{}
	for _, b := range blocks {
		switch v := b.(type) {
		case ArtifactJudgeText:
			byName[v.Filename] = b
		case ArtifactPlaceholder:
			byName[v.Filename] = b
		}
	}
	txt, ok := byName["notes.md"].(ArtifactJudgeText)
	if !ok {
		t.Fatalf("notes.md = %T, want ArtifactJudgeText", byName["notes.md"])
	}
	if !strings.Contains(txt.Text, "# Notes") {
		t.Fatalf("notes.md text = %q, want decoded content", txt.Text)
	}
	if _, ok := byName["logo.png"].(ArtifactPlaceholder); !ok {
		t.Fatalf("logo.png = %T, want ArtifactPlaceholder", byName["logo.png"])
	}
}

func TestExecutionServiceEmbedSizeCap(t *testing.T) {
	body := strings.Repeat("Z", evalEmbedMaxArtifactBytes+4096)
	res := runEmbedResolutionFixture(t, "huge.md", body, "")
	if res.AgentResponse == nil {
		t.Fatalf("agent response is nil")
	}
	got := *res.AgentResponse
	if strings.Contains(got, "«artifact_content:") {
		t.Fatalf("response still contains raw embed directive: %q", got[:min(200, len(got))])
	}
	if strings.Contains(got, body) {
		t.Fatalf("oversized artifact was inlined whole instead of truncated")
	}
	if !strings.Contains(got, "Truncated") {
		t.Fatalf("expected a truncation marker, got: %q", got[:min(200, len(got))])
	}
}

func TestEmbedResolutionUsesRunPrincipal(t *testing.T) {
	body := "# QA calendar\n\n| Mon | Tue |\n| 4 | 5 |"
	res := runEmbedResolutionFixture(t, "qa_calendar.md", body, "system:qa-bot")
	if res.AgentResponse == nil {
		t.Fatalf("agent response is nil")
	}
	got := *res.AgentResponse
	if strings.Contains(got, "«artifact_content:") {
		t.Fatalf("embed unresolved under a non-default run_as: %q", got)
	}
	if !strings.Contains(got, "# QA calendar") {
		t.Fatalf("response missing artifact content written under the run's own principal: %q", got)
	}
}

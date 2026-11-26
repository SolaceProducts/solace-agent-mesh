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

	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

type capturingJudge struct {
	mu           sync.Mutex
	systemPrompt string
	userPrompts  []string
}

func (c *capturingJudge) JudgeScore(_ context.Context, _, system, user string, _ *llm.SpendAttribution) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.systemPrompt = system
	c.userPrompts = append(c.userPrompts, user)
	return `{"choice": "A", "reasoning": "ok"}`, nil
}

func (c *capturingJudge) lastUserPrompt() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.userPrompts) == 0 {
		return ""
	}
	return c.userPrompts[len(c.userPrompts)-1]
}

type transcriptFakeAgent struct {
	t         *testing.T
	br        broker.Broker
	queue     broker.Queue
	agentName string
	wg        sync.WaitGroup
	cancel    context.CancelFunc
}

func newTranscriptFakeAgent(t *testing.T, br broker.Broker, namespace, agentName string) *transcriptFakeAgent {
	t.Helper()
	q, err := br.CreateDirectSubscription()
	if err != nil {
		t.Fatalf("agent direct sub: %v", err)
	}
	if err := br.Subscribe(q, a2a.AgentRequestTopic(namespace, agentName)); err != nil {
		t.Fatalf("agent subscribe: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	a := &transcriptFakeAgent{t: t, br: br, queue: q, agentName: agentName, cancel: cancel}
	a.wg.Add(1)
	go a.run(ctx)
	return a
}

func (a *transcriptFakeAgent) Stop() {
	a.cancel()
	a.wg.Wait()
}

func (a *transcriptFakeAgent) run(ctx context.Context) {
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
		reply := msg.ReplyTo
		if reply == "" {
			continue
		}
		taskID := req.Params.Message.TaskID

		emit := func(parts []map[string]any, final bool, state string) {
			anyParts := make([]any, len(parts))
			for i, p := range parts {
				anyParts[i] = p
			}
			payload := map[string]any{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"result": map[string]any{
					"final": final,
					"status": map[string]any{
						"state": state,
						"message": map[string]any{
							"contextId": taskID,
							"parts":     anyParts,
						},
					},
				},
			}
			body, merr := json.Marshal(payload)
			if merr != nil {
				a.t.Errorf("marshal status: %v", merr)
				return
			}
			_ = a.br.Publish(ctx, reply, &broker.Message{Topic: reply, Payload: body})
		}

		emit([]map[string]any{textPart("Let me compute that. ")}, false, "working")
		emit([]map[string]any{llmInvocationPart("date_math", "secret_unused_tool")}, false, "working")
		emit([]map[string]any{toolCallPart("date_math", map[string]any{
			"operation": "add", "base_date": "2024-01-01", "days": 30,
		})}, false, "working")
		emit([]map[string]any{toolResultPart("date_math", map[string]any{
			"status": "success", "result": "2024-01-31",
		})}, false, "working")
		emit([]map[string]any{textPart("The date is 2024-01-31.")}, false, "working")
		emit([]map[string]any{textPart("The date is 2024-01-31.")}, true, "completed")
	}
}

func TestExecutionServiceFeedsTranscriptToJudge(t *testing.T) {
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
	ex := &store.EvalExample{DatasetID: ds.ID, SequenceNumber: 1, Prompt: "what is 30 days after 2024-01-01?"}
	if err := exampleRepo.Create(ctx, ex); err != nil {
		t.Fatalf("create example: %v", err)
	}

	model := "judge-model"
	tmpl := "User input: {{Prompt}}\nAgent response: {{Response}}"
	cs := `{"Correct": 1.0, "Incorrect": 0.0}`
	ev := &store.EvalEvaluator{
		Name: "routing", Type: "llm_judge", Model: &model,
		PromptTemplate: &tmpl, ChoiceScores: &cs,
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

	judge := &capturingJudge{}
	brokerCli := NewBrokerClient(br, namespace, nil, nil)
	exec := NewExecutionService(ExecutionServiceParams{
		Namespace: namespace, Broker: br, BrokerCli: brokerCli, Storage: storageSvc,
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

	runs, err := runSvc.TriggerRuns(ctx, exp.ID, "alice", "transcript", "manual")
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
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	prompt := judge.lastUserPrompt()
	if prompt == "" {
		t.Fatalf("judge received no user prompt")
	}
	if !strings.Contains(prompt, "--- Agent transcript[") {
		t.Fatalf("judge prompt missing transcript block:\n%s", prompt)
	}
	if !strings.Contains(prompt, "[tool call] date_math(") {
		t.Fatalf("judge prompt missing tool call:\n%s", prompt)
	}
	for _, want := range []string{`"operation":"add"`, `"base_date":"2024-01-01"`} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("judge prompt missing tool param %q:\n%s", want, prompt)
		}
	}
	if !strings.Contains(prompt, "[tool result] date_math → ") || !strings.Contains(prompt, `"result":"2024-01-31"`) {
		t.Fatalf("judge prompt missing tool result:\n%s", prompt)
	}
	if strings.Contains(prompt, "secret_unused_tool") {
		t.Fatalf("judge prompt leaked an uncalled tool definition:\n%s", prompt)
	}
	if !strings.Contains(judge.systemPrompt, "execution transcript") {
		t.Fatalf("system prompt does not describe the transcript:\n%s", judge.systemPrompt)
	}
}

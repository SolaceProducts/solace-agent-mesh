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
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

const hilNeverReachesExampleBudget = 5 * time.Minute

type hilFakeAgent struct {
	br         broker.Broker
	queue      broker.Queue
	cancel     context.CancelFunc
	wg         sync.WaitGroup
	mu         sync.Mutex
	cancelled  []string
	lastTaskID string
}

func newHILFakeAgent(t *testing.T, br broker.Broker, namespace, agentName string) *hilFakeAgent {
	t.Helper()
	q, err := br.CreateDirectSubscription()
	if err != nil {
		t.Fatalf("agent direct sub: %v", err)
	}
	if err := br.Subscribe(q, a2a.AgentRequestTopic(namespace, agentName)); err != nil {
		t.Fatalf("agent subscribe: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	a := &hilFakeAgent{br: br, queue: q, cancel: cancel}
	a.wg.Add(1)
	go a.run(ctx)
	return a
}

func (a *hilFakeAgent) Stop() {
	a.cancel()
	a.wg.Wait()
}

func (a *hilFakeAgent) cancelledTasks() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.cancelled...)
}

func (a *hilFakeAgent) run(ctx context.Context) {
	defer a.wg.Done()
	for {
		msg, err := a.br.Receive(ctx, a.queue)
		if err != nil || msg == nil {
			return
		}
		var req struct {
			ID     string         `json:"id"`
			Method string         `json:"method"`
			Params map[string]any `json:"params"`
		}
		if uerr := json.Unmarshal(msg.Payload, &req); uerr != nil {
			continue
		}
		if req.Method == "tasks/cancel" {
			id, _ := req.Params["id"].(string)
			a.mu.Lock()
			a.cancelled = append(a.cancelled, id)
			a.mu.Unlock()
			continue
		}
		message, _ := req.Params["message"].(map[string]any)
		taskID, _ := message["taskId"].(string)
		a.mu.Lock()
		a.lastTaskID = taskID
		a.mu.Unlock()
		reply := msg.ReplyTo
		if reply == "" {
			continue
		}
		out := &broker.Message{Topic: reply, Payload: hilInputRequiredPayload(req.ID, taskID)}
		_ = a.br.Publish(ctx, reply, out)
	}
}

func inputRequiredEnvelope(rpcID, taskID string, dataPart map[string]any) []byte {
	body, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      rpcID,
		"result": map[string]any{
			"taskId":    taskID,
			"contextId": taskID,
			"kind":      "status-update",
			"final":     false,
			"status": map[string]any{
				"state":   string(a2a.TaskStateInputNeeded),
				"message": map[string]any{"parts": []any{dataPart}},
			},
		},
	})
	return body
}

func hilInputRequiredPayload(rpcID, taskID string) []byte {
	surface := map[string]any{
		"components": []any{
			map[string]any{"id": "q0-label", "component": "Text", "text": map[string]any{"literalString": "Which deployment variant?"}},
			map[string]any{"id": "q0-picker", "component": "ChoicePicker", "options": []any{
				map[string]any{"label": map[string]any{"literalString": "Cloud"}, "value": "Cloud"},
				map[string]any{"label": map[string]any{"literalString": "Software"}, "value": "Software"},
				map[string]any{"label": map[string]any{"literalString": "Other"}, "value": "__other__"},
			}},
			map[string]any{"id": "submit-text", "component": "Text", "text": map[string]any{"literalString": "Submit"}},
		},
	}
	dataPart := map[string]any{
		"kind": "data",
		"data": map[string]any{
			"type":    "user_input_request",
			"source":  "ask_user_question",
			"surface": surface,
		},
	}
	return inputRequiredEnvelope(rpcID, taskID, dataPart)
}

func TestExecutionServiceInputRequiredTerminal(t *testing.T) {
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
	prompt := "Is the broker highly available?"
	if err := exampleRepo.Create(ctx, &store.EvalExample{DatasetID: ds.ID, SequenceNumber: 1, Prompt: prompt}); err != nil {
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
		t.Fatalf("connect broker: %v", err)
	}
	t.Cleanup(func() { _ = br.Disconnect(ctx) })

	namespace := "test-ns"
	trigger := NewTriggerPublisher(br, namespace)
	storageSvc := NewStorageService(nil, namespace)
	runSvc := NewRunService(RunServiceParams{
		Runs: runRepo, Results: resultRepo, Experiments: expRepo,
		Examples: exampleRepo, Evaluators: evRepo, Trigger: trigger, Storage: storageSvc,
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
			ExampleBudget:    hilNeverReachesExampleBudget,
			WatchdogInterval: time.Hour,
		},
	})

	agent := newHILFakeAgent(t, br, namespace, "Orchestrator")
	t.Cleanup(agent.Stop)

	if err := exec.Start(ctx); err != nil {
		t.Fatalf("start exec: %v", err)
	}
	t.Cleanup(exec.Stop)

	runs, err := runSvc.TriggerRuns(ctx, exp.ID, "alice", "hil", "manual")
	if err != nil {
		t.Fatalf("trigger: %v", err)
	}
	runID := runs[0].ID

	deadline := time.Now().Add(8 * time.Second)
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
			t.Fatalf("final status = %q, want completed (HIL is not an error)", row.Status)
		}
		results, lerr := resultRepo.ListByRun(ctx, runID)
		if lerr != nil {
			t.Fatalf("list results: %v", lerr)
		}
		if len(results) != 1 {
			t.Fatalf("results = %d, want 1", len(results))
		}
		r := results[0]
		if r.ErrorMessage != nil {
			t.Fatalf("ErrorMessage = %q, want nil (input-required must not be a timeout/error)", *r.ErrorMessage)
		}
		if r.AgentResponse == nil {
			t.Fatal("AgentResponse is nil; want captured question")
		}
		resp := *r.AgentResponse
		if !strings.Contains(resp, "input-required") {
			t.Errorf("AgentResponse missing input-required marker: %q", resp)
		}
		if !strings.Contains(resp, "Which deployment variant?") {
			t.Errorf("AgentResponse missing question text: %q", resp)
		}
		if !strings.Contains(resp, "Cloud") || !strings.Contains(resp, "Software") {
			t.Errorf("AgentResponse missing options: %q", resp)
		}
		agent.mu.Lock()
		taskID := agent.lastTaskID
		agent.mu.Unlock()
		cancelledOK := false
		for _, c := range agent.cancelledTasks() {
			if c == taskID && taskID != "" {
				cancelledOK = true
			}
		}
		if !cancelledOK {
			t.Errorf("no tasks/cancel published for HIL task %q; cancelled=%v", taskID, agent.cancelledTasks())
		}
		return
	}
	t.Fatalf("run did not finalize within 8s — HIL was likely waited out as a timeout")
}

func TestExtractResponseInputRequired(t *testing.T) {
	t.Parallel()
	var payload map[string]any
	if err := json.Unmarshal(hilInputRequiredPayload("rpc-1", "task-1"), &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	ext := extractResponse(payload)
	if !ext.isFinal {
		t.Fatal("input-required must be terminal (isFinal=true)")
	}
	if !ext.inputRequired {
		t.Fatal("inputRequired flag not set")
	}
	if ext.isAgentError {
		t.Error("input-required must not be flagged as an agent error")
	}
	if !strings.Contains(ext.text, "Which deployment variant?") {
		t.Errorf("captured text missing question: %q", ext.text)
	}
	if !strings.Contains(ext.text, "Cloud") || !strings.Contains(ext.text, "Software") {
		t.Errorf("captured text missing options: %q", ext.text)
	}
	if strings.Contains(ext.text, "__other__") || strings.Contains(ext.text, "Submit") {
		t.Errorf("captured text leaked UI scaffolding: %q", ext.text)
	}
}

func TestExtractResponseNonTerminalStates(t *testing.T) {
	t.Parallel()
	working := map[string]any{"result": map[string]any{"status": map[string]any{"state": "working"}}}
	if ext := extractResponse(working); ext.isFinal {
		t.Error("working status should be non-final")
	}
	bare := map[string]any{"result": map[string]any{}}
	if ext := extractResponse(bare); ext.isFinal {
		t.Error("bare result should be non-final")
	}
}

func TestExtractInputRequiredTextBranches(t *testing.T) {
	t.Parallel()

	if got := extractInputRequiredText(nil); got != inputRequiredFallback {
		t.Errorf("nil message: got %q, want fallback", got)
	}

	if got := extractInputRequiredText(map[string]any{"parts": []any{}}); got != inputRequiredFallback {
		t.Errorf("empty parts: got %q, want fallback", got)
	}

	textMsg := map[string]any{"parts": []any{
		"not-a-map",
		map[string]any{"kind": "text", "text": "please clarify"},
	}}
	if got := extractInputRequiredText(textMsg); got != "please clarify" {
		t.Errorf("text part: got %q, want 'please clarify'", got)
	}

	authMsg := map[string]any{"parts": []any{
		map[string]any{"root": map[string]any{
			"kind": "data",
			"data": map[string]any{
				"type":              "authentication_required",
				"auth_uri":          leakAuthURI,
				"code_verifier":     leakVerifier,
				"credential_key":    "atlassian:eval-runner",
				"tool_display_name": "Atlassian",
			},
		}},
	}}
	got := extractInputRequiredText(authMsg)
	if want := inputRequiredFallback + " (type=authentication_required, service=Atlassian)"; got != want {
		t.Errorf("auth data summary:\n got %q\nwant %q", got, want)
	}
	for _, secret := range leakSecrets {
		if strings.Contains(got, secret) {
			t.Errorf("summary leaked %q: %q", secret, got)
		}
	}

	noService := map[string]any{"parts": []any{
		map[string]any{"kind": "data", "data": map[string]any{
			"type":          "authentication_required",
			"code_verifier": leakVerifier,
		}},
	}}
	if got, want := extractInputRequiredText(noService), inputRequiredFallback+" (type=authentication_required)"; got != want {
		t.Errorf("absent service name:\n got %q\nwant %q", got, want)
	}

	sourceOnly := map[string]any{"parts": []any{
		map[string]any{"kind": "data", "data": map[string]any{
			"type":   "user_input_request",
			"source": "ask_user_question",
		}},
	}}
	if got, want := extractInputRequiredText(sourceOnly), inputRequiredFallback+" (type=user_input_request, source=ask_user_question)"; got != want {
		t.Errorf("surfaceless user_input_request:\n got %q\nwant %q", got, want)
	}

	boundText := map[string]any{"parts": []any{
		map[string]any{"kind": "data", "data": map[string]any{
			"type":   "user_input_request",
			"source": "ask_user_question",
			"surface": map[string]any{"components": []any{
				map[string]any{"id": "q", "component": "Text", "text": map[string]any{"path": "/prompt"}},
			}, "dataModel": map[string]any{"prompt": "Which region?"}},
		}},
	}}
	echoed := extractInputRequiredText(boundText)
	if !strings.HasPrefix(echoed, inputRequiredFallback+" (type=user_input_request, source=ask_user_question)\nSurface: ") {
		t.Errorf("unparsed surface lost its summary prefix: %q", echoed)
	}
	if !strings.Contains(echoed, "Which region?") {
		t.Errorf("unparsed surface dropped the question text: %q", echoed)
	}

	authWithSurface := map[string]any{"parts": []any{
		map[string]any{"kind": "data", "data": map[string]any{
			"type":    "authentication_required",
			"surface": map[string]any{"components": []any{map[string]any{"id": "x", "component": "Unknown"}}},
		}},
	}}
	if got, want := extractInputRequiredText(authWithSurface), inputRequiredFallback+" (type=authentication_required)"; got != want {
		t.Errorf("authentication_required:\n got %q\nwant %q", got, want)
	}

	typeless := map[string]any{"parts": []any{
		map[string]any{"kind": "data", "data": map[string]any{"code_verifier": leakVerifier}},
	}}
	if got := extractInputRequiredText(typeless); got != inputRequiredFallback {
		t.Errorf("typeless data part: got %q, want bare fallback", got)
	}
}

func TestExtractSurfaceQAEdgeCases(t *testing.T) {
	t.Parallel()

	if q, o := extractSurfaceQA("not-a-map"); q != nil || o != nil {
		t.Errorf("non-map surface: got (%v, %v), want (nil, nil)", q, o)
	}

	surface := map[string]any{"components": []any{
		"not-a-map",
		map[string]any{"id": "submit-text", "component": "Text", "text": map[string]any{"literalString": "Submit"}},
		map[string]any{"id": "q0", "component": "Text", "text": map[string]any{"literalString": "Pick one"}},
		map[string]any{"component": "ChoicePicker", "options": []any{
			"not-a-map",
			map[string]any{"value": "A"},
			map[string]any{"value": "__other__"},
		}},
	}}
	q, o := extractSurfaceQA(surface)
	if len(q) != 1 || q[0] != "Pick one" {
		t.Errorf("questions = %v, want [Pick one] (Submit must be skipped)", q)
	}
	if len(o) != 1 || o[0] != "A" {
		t.Errorf("options = %v, want [A] (__other__ must be skipped)", o)
	}

	if got := literalStringValue("not-a-map"); got != "" {
		t.Errorf("literalStringValue(non-map) = %q, want empty", got)
	}
}

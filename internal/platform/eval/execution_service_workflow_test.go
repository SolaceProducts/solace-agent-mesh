// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eval

import (
	"cmp"
	"context"
	"encoding/json"
	"log/slog"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
	"github.com/SolaceProducts/solace-agent-mesh/internal/taxonomy"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/toolinit"
	"github.com/SolaceProducts/solace-agent-mesh/internal/workflow"
)

const evalWorkflowName = "order_flow"

const evalWorkflowInstance = "workflow_0001"

type classifyOrderTool struct{}

func (classifyOrderTool) Name() string                                      { return "classify_order" }
func (classifyOrderTool) Description() string                               { return "classifies an order" }
func (classifyOrderTool) Instructions() string                              { return "" }
func (classifyOrderTool) IsLongRunning() bool                               { return false }
func (classifyOrderTool) RequiredScopes() []string                          { return nil }
func (classifyOrderTool) RawStringArgs() []string                           { return nil }
func (classifyOrderTool) ArtifactParams() map[string]tool.ArtifactParamInfo { return nil }
func (classifyOrderTool) ContextParam() string                              { return "" }
func (classifyOrderTool) Parameters() *tool.JSONSchema                      { return &tool.JSONSchema{Type: "object"} }

func (classifyOrderTool) Execute(ctx context.Context, args map[string]any, toolCtx *tool.Context) (*tool.Result, error) {
	switch args["order_id"] {
	case "FAIL":
		return tool.Error("unsupported currency"), nil
	case "AUTH":
		if toolCtx == nil || toolCtx.SendSignal == nil {
			return tool.AuthPromptChannelMissing(), nil
		}
		return tool.AuthPromptUndelivered(toolCtx.SendSignal(&a2a.AuthenticationRequiredData{CredentialKey: "orders_api"})), nil
	case "ASK":
		return &tool.Result{Status: tool.StatusPending, Data: map[string]any{"hil_request_id": "req-ask"}}, nil
	case "SLOW":
		select {
		case <-time.After(10 * time.Second):
		case <-ctx.Done():
		}
		return tool.Error("stopped"), nil
	}
	return tool.OK("classified", tool.WithData(map[string]any{"priority": "urgent"})), nil
}

func orderFlowInputSchema() map[string]any {
	return map[string]any{
		"type":       "object",
		"required":   []any{"order_id"},
		"properties": map[string]any{"order_id": map[string]any{"type": "string"}},
	}
}

func startOrderFlow(t *testing.T, br broker.Broker, namespace string, configure func(*workflow.WorkflowConfig)) {
	t.Helper()
	cfg := &workflow.WorkflowConfig{
		Name:            evalWorkflowInstance,
		Namespace:       namespace,
		FailFast:        true,
		WorkflowTimeout: 1500 * time.Millisecond,
		InputSchema:     orderFlowInputSchema(),
		OutputMapping:   map[string]any{"priority": "{{ classify.output.priority }}"},
		Tools:           []any{map[string]any{"tool_type": "builtin", "tool_name": "classify_order"}},
		Nodes: []*workflow.NodeConfig{{
			ID: "classify", Type: "tool", ToolName: "classify_order",
			Input: map[string]any{"order_id": "{{ workflow.input.order_id }}"},
		}},
	}
	if configure != nil {
		configure(cfg)
	}
	w := workflow.NewWorkflowInstance(cfg)
	w.SetToolInitOptions(toolinit.Options{
		AdditionalBuiltin: func(name string, _ map[string]any) tool.BuiltinTool {
			if name == "classify_order" {
				return classifyOrderTool{}
			}
			return nil
		},
	})
	svc := runtime.NewRuntime(br, nil, runtime.WithLogger(slog.New(slog.DiscardHandler))).Services()
	if err := w.Init(t.Context(), nil, svc); err != nil {
		t.Fatalf("init workflow: %v", err)
	}
	if err := w.Start(t.Context()); err != nil {
		t.Fatalf("start workflow: %v", err)
	}
	t.Cleanup(func() { _ = w.Stop(context.Background()) })
}

type orderFlowRun struct {
	orders        []any
	prompt        string
	configure     func(*workflow.WorkflowConfig)
	judge         LLMJudgeClient
	judgeTemplate string
	startAgents   func(br broker.Broker, namespace string)
	expectedPath  *ExpectedPath
	storedPath    *string
	expectedFacts *ExpectedFacts
	reports       chan reportedRun
}

func awaitRunReport(t *testing.T, reports chan reportedRun) reportedRun {
	t.Helper()
	select {
	case report := <-reports:
		return report
	case <-time.After(5 * time.Second):
		t.Fatal("the run reported no eval:run")
		return reportedRun{}
	}
}

func runOrderFlowExperiment(t *testing.T, opts orderFlowRun) (*store.EvalRun, string, []*store.EvalRunResultWithScores) {
	t.Helper()
	orders, configure := opts.orders, opts.configure
	db := store.TestDB(t)
	datasetRepo := store.NewEvalDatasetRepo(db)
	exampleRepo := store.NewEvalExampleRepo(db)
	expRepo := store.NewEvalExperimentRepo(db)
	evRepo := store.NewEvalEvaluatorRepo(db)
	runRepo := store.NewEvalRunRepo(db)
	resultRepo := store.NewEvalRunResultRepo(db)
	ctx := context.Background()
	expectedPath, pathErr := encodeExpectedPath(opts.expectedPath)
	if pathErr != nil {
		t.Fatal(pathErr)
	}
	if opts.storedPath != nil {
		expectedPath = opts.storedPath
	}
	expectedFacts, factsErr := encodeExpectedFacts(opts.expectedFacts)
	if factsErr != nil {
		t.Fatal(factsErr)
	}

	ds := &store.EvalDataset{Name: "orders", CreatedBy: "alice", UpdatedBy: "alice"}
	if err := datasetRepo.Create(ctx, ds); err != nil {
		t.Fatalf("create dataset: %v", err)
	}
	for i, order := range orders {
		raw, err := json.Marshal(map[string]any{"order_id": order})
		if err != nil {
			t.Fatal(err)
		}
		input, err := encodeInputData(raw)
		if err != nil {
			t.Fatal(err)
		}
		if err := exampleRepo.Create(ctx, &store.EvalExample{DatasetID: ds.ID, SequenceNumber: i + 1, Prompt: opts.prompt, InputData: input, ExpectedPath: expectedPath, ExpectedFacts: expectedFacts}); err != nil {
			t.Fatalf("create example: %v", err)
		}
	}
	heuristic := "valid_json"
	ev := &store.EvalEvaluator{Name: "valid-json", Type: "heuristic", HeuristicType: &heuristic, IsDefault: true, CreatedBy: "system", UpdatedBy: "system"}
	if err := evRepo.Create(ctx, ev); err != nil {
		t.Fatalf("create evaluator: %v", err)
	}
	evaluatorIDs := []string{ev.ID}
	if opts.judge != nil {
		model, tmpl, choices := "judge-model", cmp.Or(opts.judgeTemplate, "Request: {{Prompt}}\nOutput: {{Response}}"), `{"Yes": 1.0, "No": 0.0}`
		judgeEv := &store.EvalEvaluator{Name: "judge", Type: "llm_judge", Model: &model, PromptTemplate: &tmpl, ChoiceScores: &choices, CreatedBy: "system", UpdatedBy: "system"}
		if err := evRepo.Create(ctx, judgeEv); err != nil {
			t.Fatalf("create judge evaluator: %v", err)
		}
		evaluatorIDs = append(evaluatorIDs, judgeEv.ID)
	}
	if opts.expectedPath != nil {
		pathEv := workflowPathEvaluator()
		pathEv.CreatedBy, pathEv.UpdatedBy = "system", "system"
		if err := evRepo.Create(ctx, pathEv); err != nil {
			t.Fatalf("create path evaluator: %v", err)
		}
		evaluatorIDs = append(evaluatorIDs, pathEv.ID)
	}
	if opts.expectedFacts != nil {
		factsEv := outputFactsEvaluator()
		factsEv.CreatedBy, factsEv.UpdatedBy = "system", "system"
		if err := evRepo.Create(ctx, factsEv); err != nil {
			t.Fatalf("create facts evaluator: %v", err)
		}
		evaluatorIDs = append(evaluatorIDs, factsEv.ID)
	}
	exp := &store.EvalExperiment{
		Name: "order-flow-regression", TargetAgent: evalWorkflowName, TargetType: store.EvalTargetTypeWorkflow,
		Models: "[]", RunsPerExample: 1, MaxWorkers: 2, DatasetID: &ds.ID, CreatedBy: "alice", UpdatedBy: "alice",
	}
	if err := expRepo.Create(ctx, exp, evaluatorIDs); err != nil {
		t.Fatalf("create experiment: %v", err)
	}

	br := broker.NewDevBroker()
	if err := br.Connect(ctx); err != nil {
		t.Fatalf("connect broker: %v", err)
	}
	t.Cleanup(func() { _ = br.Disconnect(ctx) })
	namespace := "test-ns"
	startOrderFlow(t, br, namespace, configure)
	if opts.startAgents != nil {
		opts.startAgents(br, namespace)
	}

	storageSvc := NewStorageService(nil, namespace)
	runSvc := NewRunService(RunServiceParams{
		Runs: runRepo, Results: resultRepo, Experiments: expRepo,
		Examples: exampleRepo, Evaluators: evRepo,
		Trigger: NewTriggerPublisher(br, namespace), Storage: storageSvc,
		FeatureRegistry: workflowEvalsFlag(true),
		WorkflowTargets: NewWorkflowTargetValidator(
			fakeWorkflows{evalWorkflowName: {InstanceName: evalWorkflowInstance, InputSchema: orderFlowInputSchema()}},
			fakeInvoke{rbac.EvalSystemUserSub: true}, exampleRepo, nil),
	})
	params := ExecutionServiceParams{
		Namespace: namespace, Broker: br, BrokerCli: NewBrokerClient(br, namespace, nil, nil), Storage: storageSvc,
		Runs: runRepo, Results: resultRepo, Scores: store.NewEvalRunScoreRepo(db),
		Experiments: expRepo, Examples: exampleRepo, FeatureRegistry: workflowEvalsFlag(true), LLMClient: opts.judge,
		Config: ExecutionConfig{ExampleBudget: 20 * time.Second, WatchdogInterval: time.Second},
	}
	if opts.reports != nil {
		params.Analytics = channelRunReporter(opts.reports)
	}
	exec := NewExecutionService(params)
	if err := exec.Start(ctx); err != nil {
		t.Fatalf("start exec: %v", err)
	}
	t.Cleanup(exec.Stop)

	runs, err := runSvc.TriggerRuns(ctx, exp.ID, "alice", "workflow", "manual")
	if err != nil {
		t.Fatalf("trigger: %v", err)
	}
	run := waitForTerminalRun(t, runRepo, runs[0].ID)
	results, err := resultRepo.ListByRun(ctx, runs[0].ID)
	if err != nil {
		t.Fatalf("list results: %v", err)
	}
	return run, ev.ID, results
}

func TestWorkflowExperimentRunsEndToEnd(t *testing.T) {
	orders := []any{"O-1", "FAIL", "ASK", "SLOW", 42}
	judge := &capturingJudge{}
	reports := make(chan reportedRun, 4)
	run, evID, results := runOrderFlowExperiment(t, orderFlowRun{orders: orders, judge: judge, reports: reports})

	if run.Status != StatusCompletedWithWarnings {
		t.Errorf("run status = %q, want completed_with_warnings (one graded trial, four ended early)", run.Status)
	}
	var summary map[string]float64
	if run.SummaryScores == nil || json.Unmarshal([]byte(*run.SummaryScores), &summary) != nil || summary[evID] != 1 {
		t.Errorf("summary scores = %v, want only the completed trial counted, scoring 1", derefString(run.SummaryScores))
	}

	if len(results) != len(orders) {
		t.Fatalf("results = %d, want one per example", len(results))
	}
	wantKind := map[string]string{"O-1": "", "FAIL": FailureKindNodeFailed, "ASK": FailureKindPausedForInput, "SLOW": FailureKindTimeout, "42": FailureKindInvalidInput}
	for _, r := range results {
		order := strings.Trim(strings.TrimPrefix(strings.TrimSuffix(derefString(r.InputPrompt), "}"), `{"order_id":`), `"`)
		want, known := wantKind[order]
		if !known {
			t.Errorf("unexpected trial prompt %q", derefString(r.InputPrompt))
			continue
		}
		if got := derefString(r.FailureKind); got != want {
			t.Errorf("%s: failure kind = %q (%s), want %q", order, got, derefString(r.ErrorMessage), want)
		}
		if derefString(r.TargetType) != store.EvalTargetTypeWorkflow {
			t.Errorf("%s: target type = %q", order, derefString(r.TargetType))
		}
		if want != "" {
			if len(r.Scores) != 0 {
				t.Errorf("%s ended early yet was scored: %d scores", order, len(r.Scores))
			}
			b := decodeTokenBreakdown(r.TokenBreakdown)
			switch {
			case want == FailureKindInvalidInput && b != nil:
				t.Errorf("%s was never sent yet has a token breakdown %+v", order, b)
			case want != FailureKindInvalidInput && (b == nil || !b.Incomplete):
				t.Errorf("%s: token breakdown = %+v, want an incomplete one: its node may still be calling models", order, b)
			}
			continue
		}
		if derefString(r.AgentResponse) != `{"priority":"urgent"}` {
			t.Errorf("%s: graded response = %q, want the workflow's output", order, derefString(r.AgentResponse))
		}
		if len(r.Scores) != 2 {
			t.Errorf("%s: scores = %+v, want one per evaluator", order, r.Scores)
		}
		if b := decodeTokenBreakdown(r.TokenBreakdown); b == nil || b.TraceID == "" || b.Incomplete || len(b.ByModel) != 0 {
			t.Errorf("%s: token breakdown = %+v, want a complete empty one with the trial's trace", order, b)
		}
	}
	if refused := findResult(results, `42`); refused == nil || !strings.HasPrefix(derefString(refused.ErrorMessage), "The example's input does not fit the workflow's input schema") {
		t.Errorf("the misfit example was not refused before it was sent: %v", refused)
	}
	if failed := findResult(results, `"FAIL"`); failed == nil || !strings.Contains(derefString(failed.ErrorMessage), "unsupported currency") {
		t.Errorf("the failed node's error did not reach the result: %v", failed)
	}

	report := awaitRunReport(t, reports)
	if report.targetType != taxonomy.EvalTargetTypeWorkflow {
		t.Errorf("eval:run target type = %q, want workflow", report.targetType)
	}
	wantShape := platpkg.EvalRunShape{
		EvaluatorTypes: []string{taxonomy.EvaluatorTypeHeuristic, taxonomy.EvaluatorTypeLLMJudge},
		TrialFailureKinds: []string{
			FailureKindInvalidInput, FailureKindNodeFailed, FailureKindPausedForInput, FailureKindTimeout,
		},
	}
	if report.shape == nil || !slices.Equal(report.shape.EvaluatorTypes, wantShape.EvaluatorTypes) ||
		report.shape.MultiTrial != wantShape.MultiTrial || !slices.Equal(report.shape.TrialFailureKinds, wantShape.TrialFailureKinds) ||
		report.shape.TrialFailureKindsUnknown {
		t.Errorf("eval:run shape = %+v, want %+v: the four trials that ended early, each under its kind", report.shape, wantShape)
	}

	if !strings.Contains(judge.systemPrompt, "WORKFLOW's final output") {
		t.Errorf("the judge was not told it grades a workflow: %.200s", judge.systemPrompt)
	}
	prompt := judge.lastUserPrompt()
	for _, want := range []string{
		`Request: {"order_id":"O-1"}`,
		`Output: {"priority":"urgent"}`,
		"Workflow execution trajectory",
		"[workflow] started",
		"[node classify · tool classify_order] started",
		"[node classify → tool call] classify_order(",
		"[node classify → tool result] classify_order",
		`[node classify] success — input: {"order_id":"O-1"}; output: `,
		"[workflow] success",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("judge prompt lacks %q:\n%s", want, prompt)
		}
	}
}

func findResult(results []*store.EvalRunResultWithScores, promptFragment string) *store.EvalRunResultWithScores {
	for _, r := range results {
		if strings.Contains(derefString(r.InputPrompt), promptFragment) {
			return r
		}
	}
	return nil
}

func TestAWorkflowThatLostABranchIsGradedAndFlagged(t *testing.T) {
	reports := make(chan reportedRun, 4)
	run, evID, results := runOrderFlowExperiment(t, orderFlowRun{orders: []any{"O-1"}, reports: reports, configure: func(cfg *workflow.WorkflowConfig) {
		cfg.FailFast = false
		cfg.Nodes = append(cfg.Nodes, &workflow.NodeConfig{
			ID: "audit", Type: "tool", ToolName: "classify_order",
			Input: map[string]any{"order_id": "FAIL"},
		})
	}})

	if run.Status != StatusCompletedWithWarnings {
		t.Errorf("run status = %q, want completed_with_warnings", run.Status)
	}
	if len(results) != 1 {
		t.Fatalf("results = %d, want one", len(results))
	}
	r := results[0]
	if derefString(r.FailureKind) != FailureKindPartialFailure || r.ErrorMessage != nil {
		t.Errorf("failure kind %q, error %q; want partial_failure and no error message", derefString(r.FailureKind), derefString(r.ErrorMessage))
	}
	if derefString(r.AgentResponse) != `{"priority":"urgent"}` {
		t.Errorf("graded response = %q, want the surviving node's output", derefString(r.AgentResponse))
	}
	if len(r.Scores) != 1 || r.Scores[0].Passed == nil || !*r.Scores[0].Passed {
		t.Errorf("scores = %+v, want the trial graded", r.Scores)
	}
	var summary map[string]float64
	if run.SummaryScores == nil || json.Unmarshal([]byte(*run.SummaryScores), &summary) != nil || summary[evID] != 1 {
		t.Errorf("summary scores = %v, want the flagged trial counted", derefString(run.SummaryScores))
	}
	if shape := awaitRunReport(t, reports).shape; shape == nil || len(shape.TrialFailureKinds) != 0 || shape.TrialFailureKindsUnknown {
		t.Errorf("eval:run shape = %+v, want no early end and every end known: the trial was graded", shape)
	}
}

func TestAWorkflowTrialAtAToolNodesSignInIsAuthRequired(t *testing.T) {
	cases := []struct {
		name      string
		orders    []any
		configure func(*workflow.WorkflowConfig)
		wantNode  string
	}{
		{"the workflow stops", []any{"AUTH"}, nil, "classify"},
		{"the workflow runs on without the branch", []any{"O-1"}, func(cfg *workflow.WorkflowConfig) {
			cfg.FailFast = false
			cfg.Nodes = append(cfg.Nodes, &workflow.NodeConfig{
				ID: "lookup", Type: "tool", ToolName: "classify_order",
				Input: map[string]any{"order_id": "AUTH"},
			})
		}, "lookup"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reports := make(chan reportedRun, 4)
			run, _, results := runOrderFlowExperiment(t, orderFlowRun{orders: tc.orders, configure: tc.configure, reports: reports})

			if run.Status != StatusFailed || run.SummaryScores != nil && *run.SummaryScores != "{}" {
				t.Errorf("run status %q, summary scores %s; want failed with nothing scored, its only trial walled", run.Status, derefString(run.SummaryScores))
			}
			if len(results) != 1 {
				t.Fatalf("results = %d, want one", len(results))
			}
			r := results[0]
			if derefString(r.FailureKind) != FailureKindAuthRequired || len(r.Scores) != 0 {
				t.Errorf("failure kind %q with %d scores (%s), want auth_required and unscored", derefString(r.FailureKind), len(r.Scores), derefString(r.ErrorMessage))
			}
			if msg := derefString(r.ErrorMessage); !strings.Contains(msg, `tool "classify_order" in node "`+tc.wantNode+`"`) || !strings.Contains(msg, "Connect tools on the experiment") {
				t.Errorf("message = %q, want the tool and node to connect", msg)
			}
			if shape := awaitRunReport(t, reports).shape; shape == nil || !slices.Equal(shape.TrialFailureKinds, []string{FailureKindAuthRequired}) || shape.TrialFailureKindsUnknown {
				t.Errorf("eval:run shape = %+v, want the trial's early end reported as auth_required", shape)
			}
		})
	}
}

func TestTheJudgeReadsWhatAWorkflowTrialWasSent(t *testing.T) {
	judge := &capturingJudge{}

	_, _, results := runOrderFlowExperiment(t, orderFlowRun{orders: []any{"O-1"}, prompt: "Classify this order", judge: judge})

	want := "Classify this order\n\nInput: {\"order_id\":\"O-1\"}"
	if len(results) != 1 || derefString(results[0].InputPrompt) != want {
		t.Fatalf("recorded prompt = %q, want %q", derefString(results[0].InputPrompt), want)
	}
	if got := judge.lastUserPrompt(); !strings.Contains(got, "Request: "+want+"\n") {
		t.Errorf("judge prompt = %q, want it to carry %q", got, want)
	}
}

func TestWorkflowJudgeGradesOneNode(t *testing.T) {
	judge := &capturingJudge{}

	runOrderFlowExperiment(t, orderFlowRun{orders: []any{"O-1"}, judge: judge, configure: func(cfg *workflow.WorkflowConfig) {
		cfg.FailFast = false
		cfg.Nodes = append(cfg.Nodes, &workflow.NodeConfig{
			ID: "audit", Type: "tool", ToolName: "classify_order",
			Input: map[string]any{"order_id": "FAIL"},
		})
	}, judgeTemplate: "The classify node returned {{Node Output: classify}} for {{node input:classify}}. " +
		"Audit took {{Node Input: audit}} and returned {{Node Output: audit}}; a missing node reads {{Node Output: nope}}. Final: {{Response}}"})

	prompt := judge.lastUserPrompt()
	for _, want := range []string{
		`The classify node returned {"message":"classified","priority":"urgent","status":"success"} for {"order_id":"O-1"}`,
		`Audit took [Node "audit" failed in this trial. Its recorded input:] {"order_id":"FAIL"} and returned [Node "audit" failed in this trial and reported no output]`,
		`a missing node reads [No node "nope" reported in this trial: it did not run, is not a node of this workflow, or runs inside a map or loop, which reports only as a whole; a nested sub-workflow's node is named as outer ▸ inner]`,
		`Final: {"priority":"urgent"}`,
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("judge prompt lacks %q:\n%s", want, prompt)
		}
	}
}

func startWallingNodeAgent(t *testing.T, br broker.Broker, namespace, agentName string) {
	t.Helper()
	q, err := br.CreateDirectSubscription()
	if err != nil {
		t.Fatalf("agent direct sub: %v", err)
	}
	if err := br.Subscribe(q, a2a.AgentRequestTopic(namespace, agentName)); err != nil {
		t.Fatalf("agent subscribe: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	t.Cleanup(func() {
		cancel()
		<-done
	})
	go func() {
		defer close(done)
		for {
			msg, err := br.Receive(ctx, q)
			if err != nil || msg == nil {
				return
			}
			var req struct {
				Method string         `json:"method"`
				Params map[string]any `json:"params"`
			}
			if json.Unmarshal(msg.Payload, &req) != nil || req.Method == "tasks/cancel" {
				continue
			}
			message, _ := req.Params["message"].(map[string]any)
			taskID, _ := message["taskId"].(string)
			if topic := msg.UserProperties[a2a.PropA2AStatusTopic]; topic != "" {
				_ = br.Publish(ctx, topic, &broker.Message{Topic: topic, Payload: authRequiredPayload(t, taskID)})
			}
		}
	}()
}

func TestAWorkflowTrialAtANodeAgentsSignInWallNamesTheNodeAgent(t *testing.T) {
	const nodeAgent = "Orchestrator"
	_, _, results := runOrderFlowExperiment(t, orderFlowRun{
		orders: []any{"O-1"},
		configure: func(cfg *workflow.WorkflowConfig) {
			cfg.Tools = nil
			cfg.Nodes = []*workflow.NodeConfig{{
				ID: "classify", Type: "agent", AgentName: nodeAgent,
				Input: map[string]any{"order_id": "{{ workflow.input.order_id }}"},
			}}
		},
		startAgents: func(br broker.Broker, namespace string) { startWallingNodeAgent(t, br, namespace, nodeAgent) },
	})

	if len(results) != 1 {
		t.Fatalf("results = %d, want one", len(results))
	}
	r := results[0]
	if derefString(r.FailureKind) != FailureKindAuthRequired {
		t.Fatalf("failure kind = %q (%s), want auth_required", derefString(r.FailureKind), derefString(r.ErrorMessage))
	}
	if msg := derefString(r.ErrorMessage); !strings.HasPrefix(msg, nodeAgent+" requires authorization") || strings.Contains(msg, evalWorkflowInstance) {
		t.Errorf("error message = %q, want it to name the node agent, not the workflow", msg)
	}
}

func TestWorkflowExperimentErrorsAnUnreadableExpectedPath(t *testing.T) {
	unreadable := `{"nodes":{"classify":"done"}}`
	run, _, results := runOrderFlowExperiment(t, orderFlowRun{
		orders:       []any{"O-1"},
		expectedPath: &ExpectedPath{Nodes: map[string]string{"classify": "succeeded"}},
		storedPath:   &unreadable,
	})

	if len(results) != 1 {
		t.Fatalf("results = %d, want one", len(results))
	}
	var pathScore *store.EvalRunScore
	for _, sc := range results[0].Scores {
		if strings.Contains(derefString(sc.Reasoning), "expected path cannot be read") {
			pathScore = sc
		}
	}
	if pathScore == nil {
		t.Fatalf("no errored Workflow Path score among %d scores", len(results[0].Scores))
	}
	if run.Status != StatusCompletedWithWarnings {
		t.Errorf("run status = %q, want %q", run.Status, StatusCompletedWithWarnings)
	}
}

func TestWorkflowExperimentGradesAMapByItsOwnNode(t *testing.T) {
	withMap := func(cfg *workflow.WorkflowConfig) {
		cfg.Nodes = append(cfg.Nodes,
			&workflow.NodeConfig{ID: "each", Type: "map", TemplateNode: "check_each", ItemsExpression: []any{"O-2", "O-3"}, DependsOn: []string{"classify"}},
			&workflow.NodeConfig{ID: "check_each", Type: "tool", ToolName: "classify_order",
				Input: map[string]any{"order_id": "{{ _map_item.output }}"}, DependsOn: []string{"each"}},
		)
	}
	path := &ExpectedPath{
		Nodes: map[string]string{"each": "succeeded", "check_each": "ran"},
		Tools: map[string]string{"classify_order": "called"},
	}
	_, _, results := runOrderFlowExperiment(t, orderFlowRun{orders: []any{"O-1"}, configure: withMap, expectedPath: path})

	if len(results) != 1 {
		t.Fatalf("results = %d, want one", len(results))
	}
	var pathScore *store.EvalRunScore
	for _, sc := range results[0].Scores {
		if strings.Contains(derefString(sc.Reasoning), "path assertions held") {
			pathScore = sc
		}
	}
	if pathScore == nil {
		t.Fatalf("no Workflow Path score among %d scores (%s)", len(results[0].Scores), derefString(results[0].ErrorMessage))
	}
	const want = `node "check_each" should run, but it never ran`
	if reason := derefString(pathScore.Reasoning); !strings.HasPrefix(reason, "2 of 3 path assertions held.") || !strings.Contains(reason, want) {
		t.Errorf("reasoning %q, want 2 of 3 held and %q", reason, want)
	}
}

func TestWorkflowExperimentGradesTheExpectedPath(t *testing.T) {
	cases := []struct {
		name       string
		path       *ExpectedPath
		wantScore  float64
		wantReason string
	}{
		{
			name: "the path the workflow took",
			path: &ExpectedPath{
				Nodes: map[string]string{"classify": "succeeded", "audit": "failed", "escalate": "not_ran"},
				Tools: map[string]string{"classify_order": "called", "issue_refund": "not_called"},
			},
			wantScore:  1,
			wantReason: "5 of 5 path assertions held.",
		},
		{
			name: "a path it did not take",
			path: &ExpectedPath{
				Nodes: map[string]string{"classify": "succeeded", "audit": "succeeded"},
				Tools: map[string]string{"classify_order": "not_called"},
			},
			wantScore:  1.0 / 3,
			wantReason: `node "audit" should succeed, but it failed; tool "classify_order" should not be called, but it was called 2 time(s)`,
		},
	}
	lostAudit := func(cfg *workflow.WorkflowConfig) {
		cfg.FailFast = false
		cfg.Nodes = append(cfg.Nodes, &workflow.NodeConfig{
			ID: "audit", Type: "tool", ToolName: "classify_order",
			Input: map[string]any{"order_id": "FAIL"},
		})
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, results := runOrderFlowExperiment(t, orderFlowRun{orders: []any{"O-1"}, configure: lostAudit, expectedPath: tc.path})

			if len(results) != 1 {
				t.Fatalf("results = %d, want one", len(results))
			}
			var pathScore *store.EvalRunScore
			for _, sc := range results[0].Scores {
				if strings.Contains(derefString(sc.Reasoning), "path assertions held") {
					pathScore = sc
				}
			}
			if pathScore == nil {
				t.Fatalf("no Workflow Path score among %d scores", len(results[0].Scores))
			}
			if pathScore.Score != tc.wantScore || !strings.Contains(derefString(pathScore.Reasoning), tc.wantReason) {
				t.Errorf("score %v (%s), want %v containing %q", pathScore.Score, derefString(pathScore.Reasoning), tc.wantScore, tc.wantReason)
			}
		})
	}
}

func TestWorkflowExperimentGradesTheExpectedFacts(t *testing.T) {
	cases := []struct {
		name       string
		facts      *ExpectedFacts
		wantScore  float64
		wantReason string
	}{
		{"the output says what it must", &ExpectedFacts{Required: []string{"Urgent"}, Forbidden: []string{"standard"}}, 1, "2 of 2 facts held."},
		{"the output says what it must not", &ExpectedFacts{Required: []string{"standard"}, Forbidden: []string{"urgent"}}, 0,
			`0 of 2 facts held. Missing: "standard". Present but forbidden: "urgent".`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, results := runOrderFlowExperiment(t, orderFlowRun{orders: []any{"O-1"}, expectedFacts: tc.facts})

			if len(results) != 1 {
				t.Fatalf("results = %d, want one", len(results))
			}
			var factsScore *store.EvalRunScore
			for _, sc := range results[0].Scores {
				if strings.Contains(derefString(sc.Reasoning), "facts held") {
					factsScore = sc
				}
			}
			if factsScore == nil {
				t.Fatalf("no Output Facts score among %d scores", len(results[0].Scores))
			}
			if factsScore.Score != tc.wantScore || derefString(factsScore.Reasoning) != tc.wantReason {
				t.Errorf("score %v (%s), want %v (%s)", factsScore.Score, derefString(factsScore.Reasoning), tc.wantScore, tc.wantReason)
			}
		})
	}
}

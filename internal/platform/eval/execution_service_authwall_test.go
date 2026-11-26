// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eval

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/budget"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	evalstorage "github.com/SolaceProducts/solace-agent-mesh/internal/platform/eval/storage"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

type authWallFakeAgent struct {
	t                 *testing.T
	br                broker.Broker
	queue             broker.Queue
	wallPrompts       map[string]bool
	forwardedFromPeer string
	silent            bool
	cancel            context.CancelFunc
	wg                sync.WaitGroup
	mu                sync.Mutex
	cancelled         []string
	cancelProps       []map[string]string
	walledTasks       []string
}

func newAuthWallFakeAgent(t *testing.T, br broker.Broker, namespace, agentName string, wallPrompts map[string]bool) *authWallFakeAgent {
	return newAuthWallFakeAgentOpts(t, br, namespace, agentName, wallPrompts, "", false)
}

func newSilentFakeAgent(t *testing.T, br broker.Broker, namespace, agentName string) *authWallFakeAgent {
	return newAuthWallFakeAgentOpts(t, br, namespace, agentName, nil, "", true)
}

func newAuthWallFakeAgentOpts(t *testing.T, br broker.Broker, namespace, agentName string, wallPrompts map[string]bool, forwardedFromPeer string, silent bool) *authWallFakeAgent {
	t.Helper()
	q, err := br.CreateDirectSubscription()
	if err != nil {
		t.Fatalf("agent direct sub: %v", err)
	}
	if err := br.Subscribe(q, a2a.AgentRequestTopic(namespace, agentName)); err != nil {
		t.Fatalf("agent subscribe: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	a := &authWallFakeAgent{
		t: t, br: br, queue: q, wallPrompts: wallPrompts,
		forwardedFromPeer: forwardedFromPeer, silent: silent, cancel: cancel,
	}
	a.wg.Add(1)
	go a.run(ctx)
	return a
}

func (a *authWallFakeAgent) Stop() {
	a.cancel()
	a.wg.Wait()
}

func (a *authWallFakeAgent) cancelledTasks() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.cancelled...)
}

func (a *authWallFakeAgent) cancelUserProps() []map[string]string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]map[string]string(nil), a.cancelProps...)
}

func (a *authWallFakeAgent) walled() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.walledTasks...)
}

func (a *authWallFakeAgent) run(ctx context.Context) {
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
			a.cancelProps = append(a.cancelProps, msg.UserProperties)
			a.mu.Unlock()
			continue
		}
		message, _ := req.Params["message"].(map[string]any)
		taskID, _ := message["taskId"].(string)
		var prompt string
		if parts, ok := message["parts"].([]any); ok && len(parts) > 0 {
			if pm, ok := parts[0].(map[string]any); ok {
				prompt, _ = pm["text"].(string)
			}
		}
		if msg.ReplyTo == "" {
			continue
		}
		if a.silent {
			a.mu.Lock()
			a.walledTasks = append(a.walledTasks, taskID)
			a.mu.Unlock()
			continue
		}
		var body []byte
		if a.wallPrompts[prompt] {
			a.mu.Lock()
			a.walledTasks = append(a.walledTasks, taskID)
			a.mu.Unlock()
			body = authRequiredPayload(a.t, taskID)
			if a.forwardedFromPeer != "" {
				body = forwardAuthWallFromPeer(a.t, body, a.forwardedFromPeer)
			}
		} else {
			body, _ = json.Marshal(map[string]any{
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
			})
		}
		_ = a.br.Publish(ctx, msg.ReplyTo, &broker.Message{Topic: msg.ReplyTo, Payload: body})
	}
}

func forwardAuthWallFromPeer(t *testing.T, body []byte, peer string) []byte {
	t.Helper()
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("unmarshal auth wall: %v", err)
	}
	message := payload["result"].(map[string]any)["status"].(map[string]any)["message"].(map[string]any)
	message["metadata"] = map[string]any{"forwarded_from_peer": peer}
	out, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal forwarded auth wall: %v", err)
	}
	return out
}

type evalRunFixture struct {
	runRepo    *store.EvalRunRepo
	resultRepo *store.EvalRunResultRepo
	storageSvc *StorageService
	runSvc     *RunService
	runID      string
}

type evalRunOpts struct {
	prompts        []string
	runAs          *string
	signer         TaskAuthSigner
	registry       agentRegistrySource
	startAgent     func(br broker.Broker, namespace string)
	systemUsers    map[string]string
	budgetGate     *budget.Gate
	budgetAssigned []string
	llmJudge       bool
	maxWorkers     int
}

func startEvalRun(t *testing.T, opts evalRunOpts) evalRunFixture {
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
	ds := &store.EvalDataset{Name: "lifecycle", CreatedBy: "alice", UpdatedBy: "alice"}
	if err := datasetRepo.Create(ctx, ds); err != nil {
		t.Fatalf("create dataset: %v", err)
	}
	for i, prompt := range opts.prompts {
		if err := exampleRepo.Create(ctx, &store.EvalExample{DatasetID: ds.ID, SequenceNumber: i + 1, Prompt: prompt}); err != nil {
			t.Fatalf("create example: %v", err)
		}
	}
	htype := "valid_json"
	ev := &store.EvalEvaluator{Name: "vj", Type: "heuristic", HeuristicType: &htype, IsDefault: true, CreatedBy: "system", UpdatedBy: "system"}
	if opts.llmJudge {
		prompt, choices := "Grade {{output}}", `{"A": 1, "B": 0}`
		ev = &store.EvalEvaluator{Name: "judge", Type: evaluatorTypeLLMJudge, PromptTemplate: &prompt, ChoiceScores: &choices, IsDefault: true, CreatedBy: "system", UpdatedBy: "system"}
	}
	if err := evRepo.Create(ctx, ev); err != nil {
		t.Fatalf("create evaluator: %v", err)
	}
	exp := &store.EvalExperiment{
		Name: "lifecycle-exp", TargetAgent: "Orchestrator", Models: "[]",
		RunsPerExample: 1, MaxWorkers: max(opts.maxWorkers, 1), DatasetID: &ds.ID,
		RunAs:     opts.runAs,
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
	storageSvc := NewStorageService(evalstorage.NewMemory(), namespace)
	runSvc := NewRunService(RunServiceParams{
		Runs: runRepo, Results: resultRepo, Experiments: expRepo,
		Examples: exampleRepo, Evaluators: evRepo, Trigger: trigger, Storage: storageSvc,
	})

	users := store.NewUsersRepo(db)
	for sub, id := range opts.systemUsers {
		if err := users.UpsertUser(ctx, &store.User{ID: id, Issuer: rbac.SystemUserIssuer, Sub: sub, ProvisionedVia: rbac.ProvisionedViaSystem, CreatedBy: "test", UpdatedBy: "test"}, nil); err != nil {
			t.Fatalf("seed system user: %v", err)
		}
	}

	for _, userID := range opts.budgetAssigned {
		assignOwnBudget(t, db, userID)
	}

	brokerCli := NewBrokerClient(br, namespace, opts.signer, nil)
	exec := NewExecutionService(ExecutionServiceParams{
		SystemUsers:       users,
		BudgetAssignments: store.NewBudgetAssignmentsRepo(db),
		Namespace:         namespace, Broker: br, BrokerCli: brokerCli, Storage: storageSvc,
		Runs: runRepo, Results: resultRepo, Scores: scoreRepo,
		Experiments: expRepo, Examples: exampleRepo,
		AgentRegistry: opts.registry,
		BudgetGate:    opts.budgetGate,
		Config: ExecutionConfig{
			ExampleBudget: 5 * time.Minute, WatchdogInterval: time.Hour,
			LivenessInterval: time.Hour,
		},
	})

	opts.startAgent(br, namespace)

	if err := exec.Start(ctx); err != nil {
		t.Fatalf("start exec: %v", err)
	}
	t.Cleanup(exec.Stop)

	runs, err := runSvc.TriggerRuns(ctx, exp.ID, "alice", "lifecycle", "manual")
	if err != nil {
		t.Fatalf("trigger: %v", err)
	}
	return evalRunFixture{runRepo: runRepo, resultRepo: resultRepo, storageSvc: storageSvc, runSvc: runSvc, runID: runs[0].ID}
}

type authWallRunFixture struct {
	evalRunFixture
	agent *authWallFakeAgent
}

func startAuthWallRun(t *testing.T, prompts map[string]bool, runAs *string) authWallRunFixture {
	t.Helper()
	return startAuthWallRunWithSigner(t, prompts, runAs, nil)
}

func startAuthWallRunWithSigner(t *testing.T, prompts map[string]bool, runAs *string, signer TaskAuthSigner) authWallRunFixture {
	t.Helper()
	all := make([]string, 0, len(prompts))
	wallPrompts := map[string]bool{}
	for prompt, walled := range prompts {
		all = append(all, prompt)
		if walled {
			wallPrompts[prompt] = true
		}
	}
	var agent *authWallFakeAgent
	f := startEvalRun(t, evalRunOpts{
		prompts: all,
		runAs:   runAs,
		signer:  signer,
		startAgent: func(br broker.Broker, namespace string) {
			agent = newAuthWallFakeAgent(t, br, namespace, "Orchestrator", wallPrompts)
			t.Cleanup(agent.Stop)
		},
	})
	return authWallRunFixture{evalRunFixture: f, agent: agent}
}

func waitForTerminalRun(t *testing.T, runRepo *store.EvalRunRepo, runID string) *store.EvalRun {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		row, err := runRepo.GetByID(t.Context(), runID)
		if err != nil {
			t.Fatalf("get run: %v", err)
		}
		if IsTerminalStatus(row.Status) {
			return row
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("run did not reach a terminal state within deadline")
	return nil
}

func waitForRunResults(t *testing.T, f evalRunFixture, want int) []*store.EvalRunResultWithScores {
	t.Helper()
	waitForTerminalRun(t, f.runRepo, f.runID)
	deadline := time.Now().Add(10 * time.Second)
	var results []*store.EvalRunResultWithScores
	for time.Now().Before(deadline) {
		var err error
		results, err = f.resultRepo.ListByRun(t.Context(), f.runID)
		if err != nil {
			t.Fatalf("list results: %v", err)
		}
		if len(results) >= want {
			return results
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("results = %d, want %d — a run that reports progress_total %d must not leave trials with no row at all", len(results), want, want)
	return nil
}

func TestExecutionServiceAuthWallClassifiesTrial(t *testing.T) {
	f := startAuthWallRun(t, map[string]bool{"list my jira tickets": true}, nil)
	run := waitForTerminalRun(t, f.runRepo, f.runID)
	if run.Status != StatusFailed {
		t.Errorf("run status = %q, want failed (every trial was auth-blocked)", run.Status)
	}

	ctx := t.Context()
	results, err := f.resultRepo.ListByRun(ctx, f.runID)
	if err != nil {
		t.Fatalf("list results: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("results = %d, want 1", len(results))
	}
	r := results[0]
	if r.FailureKind == nil || *r.FailureKind != FailureKindAuthRequired {
		t.Errorf("FailureKind = %v, want %q", r.FailureKind, FailureKindAuthRequired)
	}
	if r.AgentResponse != nil {
		t.Errorf("AgentResponse = %q, want nil", *r.AgentResponse)
	}
	if r.ErrorMessage == nil {
		t.Fatal("ErrorMessage is nil; want the auth-required attribution")
	}
	if !strings.Contains(*r.ErrorMessage, "Atlassian") {
		t.Errorf("ErrorMessage = %q, want it to name the tool", *r.ErrorMessage)
	}
	if !strings.Contains(*r.ErrorMessage, "Connect tools on the experiment") {
		t.Errorf("ErrorMessage = %q, want re-run guidance", *r.ErrorMessage)
	}
	assertNoSecrets(t, "error_message", *r.ErrorMessage)
	if len(r.Scores) != 0 {
		t.Errorf("scores = %d, want 0 (auth-walled trials are never scored)", len(r.Scores))
	}

	stored, err := f.resultRepo.GetByID(ctx, r.ID)
	if err != nil {
		t.Fatalf("get result: %v", err)
	}
	if stored.ExecutionDataKey != nil && *stored.ExecutionDataKey != "" {
		execData, gerr := f.storageSvc.GetExecutionData(*stored.ExecutionDataKey)
		if gerr != nil {
			t.Fatalf("get execution data: %v", gerr)
		}
		assertNoSecrets(t, "executionData blob", mustJSON(t, execData))
	}

	walled := f.agent.walled()
	if len(walled) != 1 {
		t.Fatalf("walled tasks = %v, want exactly 1", walled)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, c := range f.agent.cancelledTasks() {
			if c == walled[0] {
				return
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Errorf("no tasks/cancel published for auth-walled task %q; cancelled=%v", walled[0], f.agent.cancelledTasks())
}

func TestExecutionServiceAuthWallPartialRunWarns(t *testing.T) {
	f := startAuthWallRun(t, map[string]bool{
		"list my jira tickets": true,
		"what is 2+2?":         false,
	}, nil)
	run := waitForTerminalRun(t, f.runRepo, f.runID)
	if run.Status != StatusCompletedWithWarnings {
		t.Errorf("run status = %q, want completed_with_warnings (one trial blocked, one completed)", run.Status)
	}

	results, err := f.resultRepo.ListByRun(t.Context(), f.runID)
	if err != nil {
		t.Fatalf("list results: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("results = %d, want 2", len(results))
	}
	var walledRow, okRow *store.EvalRunResultWithScores
	for _, r := range results {
		if r.FailureKind != nil && *r.FailureKind == FailureKindAuthRequired {
			walledRow = r
		} else {
			okRow = r
		}
	}
	if walledRow == nil || okRow == nil {
		t.Fatalf("want one auth_required row and one normal row, got %+v", results)
	}
	if len(walledRow.Scores) != 0 {
		t.Errorf("walled trial scores = %d, want 0", len(walledRow.Scores))
	}
	if okRow.AgentResponse == nil || len(okRow.Scores) == 0 {
		t.Errorf("completed trial should be scored normally: response=%v scores=%d", okRow.AgentResponse, len(okRow.Scores))
	}
	if okRow.FailureKind != nil {
		t.Errorf("completed trial FailureKind = %q, want nil", *okRow.FailureKind)
	}
}

func TestExtractResponseAuthRequiredForwardedFromPeer(t *testing.T) {
	t.Parallel()
	var payload map[string]any
	if err := json.Unmarshal(authRequiredPayload(t, "task-1"), &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	direct := extractResponse(payload)
	if direct.authRequired == nil {
		t.Fatal("authRequired signal not detected on the un-forwarded event")
	}
	if direct.authPeer != "" {
		t.Errorf("authPeer = %q on an un-forwarded event, want empty", direct.authPeer)
	}

	message := payload["result"].(map[string]any)["status"].(map[string]any)["message"].(map[string]any)
	message["metadata"] = map[string]any{"forwarded_from_peer": "JiraAgent"}
	forwarded := extractResponse(payload)
	if forwarded.authRequired == nil {
		t.Fatal("authRequired signal not detected on the forwarded event")
	}
	if forwarded.authPeer != "JiraAgent" {
		t.Fatalf("authPeer = %q, want JiraAgent", forwarded.authPeer)
	}

	msg := authRequiredErrorMessage(forwarded.authRequired, forwarded.authPeer)
	want := "JiraAgent requires authorization for Atlassian (atlassian:system-eval) — connect tools on the experiment and re-run."
	if msg != want {
		t.Errorf("peer error message = %q, want %q", msg, want)
	}
	assertNoSecrets(t, "peer error message", msg)
}

func TestPeerDisplayName(t *testing.T) {
	t.Parallel()
	const slug = "agent_019e1876_5f14_7f40_b463_b564c754128c"
	registry := &fakeRegistry{cards: map[string]*a2a.AgentCard{
		slug: {Name: slug, Capabilities: a2a.AgentCapabilities{
			Extensions: []a2a.AgentExtension{{
				URI:    a2a.ExtensionURIDisplayName,
				Params: map[string]any{"display_name": "Jira Agent"},
			}},
		}},
		"PlainNamed": {Name: "PlainNamed"},
		"BlankLabels": {Name: "BlankLabels", Capabilities: a2a.AgentCapabilities{
			Extensions: []a2a.AgentExtension{{
				URI:    a2a.ExtensionURIDisplayName,
				Params: map[string]any{"display_name": ""},
			}},
		}},
	}}

	cases := []struct {
		name     string
		registry agentRegistrySource
		peer     string
		want     string
	}{
		{"deployed slug renders its display name", registry, slug, "Jira Agent"},
		{"card without the extension keeps its name", registry, "PlainNamed", "PlainNamed"},
		{"blank display name falls back to the routing name", registry, "BlankLabels", "BlankLabels"},
		{"unknown peer keeps its routing name", registry, "agent_deleted_since", "agent_deleted_since"},
		{"no registry keeps the routing name", nil, slug, slug},
		{"un-forwarded wall stays unattributed", registry, "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			svc := &ExecutionService{agentRegistry: tc.registry}
			if got := svc.peerDisplayName(tc.peer); got != tc.want {
				t.Errorf("peerDisplayName(%q) = %q, want %q", tc.peer, got, tc.want)
			}
		})
	}
}

func TestExecutionServiceAuthWallNamesTheDelegatedPeerByDisplayName(t *testing.T) {
	const prompt = "list my jira tickets"
	const slug = "agent_019e1876_5f14_7f40_b463_b564c754128c"
	registry := &fakeRegistry{cards: map[string]*a2a.AgentCard{
		slug: {Name: slug, Capabilities: a2a.AgentCapabilities{
			Extensions: []a2a.AgentExtension{{
				URI:    a2a.ExtensionURIDisplayName,
				Params: map[string]any{"display_name": "Jira Agent"},
			}},
		}},
	}}

	f := startEvalRun(t, evalRunOpts{
		prompts:  []string{prompt},
		registry: registry,
		startAgent: func(br broker.Broker, namespace string) {
			agent := newAuthWallFakeAgentOpts(t, br, namespace, "Orchestrator",
				map[string]bool{prompt: true}, slug, false)
			t.Cleanup(agent.Stop)
		},
	})

	results := waitForRunResults(t, f, 1)
	r := results[0]
	if r.FailureKind == nil || *r.FailureKind != FailureKindAuthRequired {
		t.Fatalf("FailureKind = %v, want %q", r.FailureKind, FailureKindAuthRequired)
	}
	if r.ErrorMessage == nil {
		t.Fatal("ErrorMessage is nil; want the delegated auth-required attribution")
	}
	if !strings.Contains(*r.ErrorMessage, "Jira Agent") {
		t.Errorf("ErrorMessage = %q, want it to name the peer's display name", *r.ErrorMessage)
	}
	if strings.Contains(*r.ErrorMessage, slug) {
		t.Errorf("ErrorMessage = %q, want the broker routing slug resolved away", *r.ErrorMessage)
	}
}

func TestExecutionServiceAuthWallUnderExplicitRunAs(t *testing.T) {
	const principal = "system:qa-bot"
	signer := &stubTaskSigner{token: "signed.jwt"}
	f := startAuthWallRunWithSigner(t, map[string]bool{"list my jira tickets": true}, strPtr(principal), signer)

	if run := waitForTerminalRun(t, f.runRepo, f.runID); run.Status != StatusFailed {
		t.Errorf("run status = %q, want failed", run.Status)
	}

	results, err := f.resultRepo.ListByRun(t.Context(), f.runID)
	if err != nil {
		t.Fatalf("list results: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("results = %d, want 1", len(results))
	}
	row := results[0]
	if row.FailureKind == nil || *row.FailureKind != FailureKindAuthRequired {
		t.Errorf("failureKind = %v, want %q", row.FailureKind, FailureKindAuthRequired)
	}
	if row.AgentResponse != nil {
		t.Errorf("agentResponse = %v, want nil", row.AgentResponse)
	}

	if signer.principals()[0] != principal {
		t.Errorf("first SignTask principal = %q, want %q", signer.principals()[0], principal)
	}
	props := f.agent.cancelUserProps()
	if len(props) == 0 {
		t.Fatal("no tasks/cancel reached the agent")
	}
	if got := props[0][a2a.PropUserID]; got != principal {
		t.Errorf("cancel userId = %q, want %q", got, principal)
	}
}

func TestFailureKindAuthRequiredWireValue(t *testing.T) {
	t.Parallel()
	if FailureKindAuthRequired != "auth_required" {
		t.Errorf("FailureKindAuthRequired = %q, want %q — existing rows and the frontend read this exact string",
			FailureKindAuthRequired, "auth_required")
	}
}

func waitForPendingTasks(t *testing.T, agent *authWallFakeAgent, n int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if len(agent.walled()) >= n {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("agent accepted %d task requests within the deadline, want %d", len(agent.walled()), n)
}

func TestCancelFinalizeStampsRunPrincipalOnStragglerCancel(t *testing.T) {
	const principal = "system:qa-bot"
	runAs := principal
	var agent *authWallFakeAgent
	f := startEvalRun(t, evalRunOpts{
		prompts: []string{"never answered"},
		runAs:   &runAs,
		startAgent: func(br broker.Broker, namespace string) {
			agent = newSilentFakeAgent(t, br, namespace, "Orchestrator")
			t.Cleanup(agent.Stop)
		},
	})

	waitForPendingTasks(t, agent, 1)
	if _, err := f.runSvc.CancelRun(t.Context(), f.runID); err != nil {
		t.Fatalf("cancel run: %v", err)
	}
	waitForTerminalRun(t, f.runRepo, f.runID)

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && len(agent.cancelUserProps()) == 0 {
		time.Sleep(20 * time.Millisecond)
	}
	props := agent.cancelUserProps()
	if len(props) == 0 {
		t.Fatal("no tasks/cancel reached the agent for the in-flight task")
	}
	for i, p := range props {
		if got := p[a2a.PropUserID]; got != principal {
			t.Errorf("cancel[%d] userId = %q, want %q — the agent authorizes tasks/cancel on this value", i, got, principal)
		}
	}
}

type refuseAfterFirstSigner struct {
	mu    sync.Mutex
	calls int
}

func (s *refuseAfterFirstSigner) SignTask(context.Context, TaskPrincipal, string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	if s.calls == 1 {
		return "token-1", nil
	}
	return "", errors.New("eval run_as principal is not authorized")
}

func (s *refuseAfterFirstSigner) SignTeardown(ctx context.Context, principal, taskID string) (string, error) {
	return s.SignTask(ctx, TaskPrincipal{Subject: principal}, taskID)
}

func (s *refuseAfterFirstSigner) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func TestPublishFailureFinalizesInFlightTrials(t *testing.T) {
	var agent *authWallFakeAgent
	signer := &refuseAfterFirstSigner{}
	f := startEvalRun(t, evalRunOpts{
		prompts:    []string{"first", "second", "third"},
		signer:     signer,
		maxWorkers: 3,
		startAgent: func(br broker.Broker, namespace string) {
			agent = newSilentFakeAgent(t, br, namespace, "Orchestrator")
			t.Cleanup(agent.Stop)
		},
	})

	results := waitForRunResults(t, f, 3)
	if run, err := f.runRepo.GetByID(t.Context(), f.runID); err != nil {
		t.Fatalf("get run: %v", err)
	} else {
		if run.Status != StatusFailed {
			t.Errorf("run status = %q, want failed", run.Status)
		}
		if got := derefOrEmpty(run.ErrorMessage); got != msgRunPublishFailed {
			t.Errorf("run error = %q, want %q and not the publisher's error text", got, msgRunPublishFailed)
		}
	}
	if len(results) != 3 {
		t.Fatalf("results = %d, want exactly 3", len(results))
	}
	for _, r := range results {
		if r.ErrorMessage == nil || !strings.Contains(*r.ErrorMessage, "Run failed") {
			t.Errorf("trial %s/%d error = %v, want the publish-failure attribution", r.ExampleID, r.TrialIndex, r.ErrorMessage)
		}
	}

	if got := signer.callCount(); got < 3 {
		t.Errorf("signer calls = %d, want >= 3 (two publishes + a straggler cancel) — the in-flight task was never cancelled", got)
	}
	if len(agent.cancelledTasks()) != 0 {
		t.Errorf("cancels delivered = %v, want none: the refusal that failed the publish also fails the cancel signature", agent.cancelledTasks())
	}
}

func assignOwnBudget(t *testing.T, db *store.DB, userID string) {
	t.Helper()
	budgetID, now := idgen.New(), time.Now().UnixMilli()
	if _, err := db.ExecContext(t.Context(),
		`INSERT INTO budgets (id, name, name_lower, subject_type, amount, is_default, created_by, updated_by, created_time, updated_time)
		 VALUES (?, ?, ?, 'user', 100, 0, 'admin', 'admin', ?, ?)`,
		budgetID, "own "+budgetID, "own "+budgetID, now, now); err != nil {
		t.Fatalf("create a budget: %v", err)
	}
	if _, err := db.ExecContext(t.Context(),
		`INSERT INTO budget_assignments (id, subject_type, subject_ref, budget_id, created_by, updated_by, created_time, updated_time)
		 VALUES (?, 'user', ?, ?, 'admin', 'admin', ?, ?)`,
		idgen.New(), userID, budgetID, now, now); err != nil {
		t.Fatalf("assign a budget: %v", err)
	}
}

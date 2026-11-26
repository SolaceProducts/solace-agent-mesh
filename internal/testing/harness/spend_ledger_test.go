// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package harness

import (
	"cmp"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/budget"
	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/httputil"
	platstore "github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/pricing"
)

const (
	governorFlushInterval = 500 * time.Millisecond
	pastOneGovernorFlush  = governorFlushInterval + 100*time.Millisecond
)

type ledgerRow struct {
	UserID              *string `db:"user_id"`
	UserSub             *string `db:"user_sub"`
	AgentID             *string `db:"agent_id"`
	AgentName           *string `db:"agent_name"`
	WorkflowID          *string `db:"workflow_id"`
	WorkflowName        *string `db:"workflow_name"`
	RootKind            *string `db:"root_kind"`
	RootID              *string `db:"root_id"`
	RootName            *string `db:"root_name"`
	RootNodeID          *string `db:"root_node_id"`
	EntrypointID        *string `db:"entrypoint_id"`
	EntrypointName      *string `db:"entrypoint_name"`
	Channel             *string `db:"channel"`
	TriggerKind         *string `db:"trigger_kind"`
	TriggerID           *string `db:"trigger_id"`
	Surface             string  `db:"surface"`
	ModelConfigID       *string `db:"model_config_id"`
	ModelName           *string `db:"model_name"`
	TaskID              *string `db:"task_id"`
	TraceID             *string `db:"trace_id"`
	UncachedInputTokens int64   `db:"uncached_input_tokens"`
	OutputTokens        int64   `db:"output_tokens"`
	CacheRead           int64   `db:"cache_read_tokens"`
	CacheWrite          int64   `db:"cache_write_tokens"`
	UncachedInputCost   *int64  `db:"uncached_input_cost"`
	OutputCost          *int64  `db:"output_cost"`
	CacheReadCost       *int64  `db:"cache_read_cost"`
	CacheWriteCost      *int64  `db:"cache_write_cost"`
	TotalCost           *int64  `db:"total_cost"`

	DetailsID         *string `db:"details_id"`
	ModelKey          *string `db:"model_key"`
	SentModelName     *string `db:"sent_model_name"`
	ResolvedModelName *string `db:"resolved_model_name"`
	ResolvedProvider  *string `db:"resolved_model_provider"`
	ToolName          *string `db:"tool_name"`
	WorkflowNodeID    *string `db:"workflow_node_id"`
	ParentTaskID      *string `db:"parent_task_id"`
	SessionID         *string `db:"session_id"`
}

func (r ledgerRow) tokens() buckets {
	return buckets{r.UncachedInputTokens, r.OutputTokens, r.CacheRead, r.CacheWrite}
}

const ledgerSelect = `SELECT l.user_id, l.user_sub, l.agent_id, l.agent_name, l.workflow_id, l.workflow_name,
	l.root_kind, l.root_id, l.root_name, l.root_node_id, l.entrypoint_id, l.entrypoint_name, l.channel, l.trigger_kind, l.trigger_id, l.surface,
	l.model_config_id, l.model_name, l.task_id, l.trace_id,
	l.uncached_input_tokens, l.output_tokens, l.cache_read_tokens, l.cache_write_tokens,
	l.uncached_input_cost, l.output_cost, l.cache_read_cost, l.cache_write_cost, l.total_cost,
	CAST(d.id AS TEXT) AS details_id, d.model_key, d.sent_model_name, d.resolved_model_name, d.resolved_model_provider,
	d.tool_name, d.workflow_node_id, d.parent_task_id, d.session_id
	FROM token_spend_ledger l LEFT JOIN token_spend_ledger_details d ON d.id = l.id`

const providerModelName = "provider-ledger-model"

func deref(p *string) string {
	if p == nil {
		return "<null>"
	}
	return *p
}

func mustAmount(t *testing.T, text string) pricing.Amount {
	t.Helper()
	a, err := pricing.ParseAmount(text)
	if err != nil {
		t.Fatalf("ParseAmount(%q): %v", text, err)
	}
	return a
}

func seedPricedModel(t *testing.T, db *platstore.DB, alias string) platstore.ModelConfiguration {
	t.Helper()
	cacheRead, cacheWrite := mustAmount(t, "0.3"), mustAmount(t, "3.75")
	return createPricedModel(t, db, platstore.ModelConfiguration{Alias: alias, Provider: "openai", ModelName: providerModelName},
		pricing.ModelPrice{Input: mustAmount(t, "3"), Output: mustAmount(t, "15"), CacheRead: &cacheRead, CacheWrite: &cacheWrite})
}

func createPricedModel(t *testing.T, db *platstore.DB, mc platstore.ModelConfiguration, price pricing.ModelPrice) platstore.ModelConfiguration {
	t.Helper()
	stored := pricing.Store(&price)
	mc.ModelAuthType, mc.ModelAuthConfig, mc.ModelParams, mc.CreatedBy, mc.UpdatedBy = "apikey", `{}`, `{}`, "test", "test"
	mc.PriceInput, mc.PriceOutput, mc.PriceCacheRead, mc.PriceCacheWrite = stored.Input, stored.Output, stored.CacheRead, stored.CacheWrite
	if err := platstore.NewModelConfigRepo(db).Create(t.Context(), &mc); err != nil {
		t.Fatalf("create model %s: %v", mc.Alias, err)
	}
	return mc
}

func waitForLedgerRows(t *testing.T, db *platstore.DB, want int) []ledgerRow {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		rows, err := dbutil.QueryRows[ledgerRow](t.Context(), db, ledgerSelect)
		if err != nil {
			t.Fatalf("read ledger: %v", err)
		}
		if (len(rows) >= want && allDetailed(rows)) || time.Now().After(deadline) {
			return rows
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func allDetailed(rows []ledgerRow) bool {
	for _, row := range rows {
		if row.DetailsID == nil {
			return false
		}
	}
	return true
}

type buckets struct{ input, output, cacheRead, cacheWrite int64 }

type ledgerCall struct {
	agent, workflow, node  string
	rootKind, root         string
	rootNode               string
	triggerKind, triggerID string
	channel                string
	delegated              bool
	tokens                 buckets
	costs                  buckets
}

func ptrEquals(got *string, want string) bool {
	if want == "" {
		return got == nil
	}
	return got != nil && *got == want
}

func pairIs(id, name *string, wantID, wantName string) bool {
	return ptrEquals(id, wantID) && ptrEquals(name, wantName)
}

func byName(id, name *string, want string) bool {
	return pairIs(id, name, "", want)
}

var stampedOrigin = a2a.Origin{
	UserUUID:       "0192f0c4-6a4e-7b1e-9d3c-5f2a8b7e1c40",
	EntrypointUUID: "0192f0c4-6a4e-7b1e-9d3c-5f2a8b7e1c41",
}

const signedUserRow = "0192f0c4-6a4e-7b1e-9d3c-5f2a8b7e1c42"

type ledgerSender struct {
	origin   a2a.Origin
	verified *EnterpriseTestConfig
}

func forEachOrigin(t *testing.T, run func(t *testing.T, sender ledgerSender)) {
	t.Run("platform ids stamped", func(t *testing.T) { run(t, ledgerSender{origin: stampedOrigin}) })
	t.Run("no platform ids", func(t *testing.T) { run(t, ledgerSender{}) })
}

func forEachSender(t *testing.T, run func(t *testing.T, sender ledgerSender)) {
	forEachOrigin(t, run)
	t.Run("signed user row", func(t *testing.T) {
		run(t, ledgerSender{origin: stampedOrigin, verified: &EnterpriseTestConfig{UserScopes: []string{"*"}, PlatformUserID: signedUserRow}})
	})
}

func runLedgeredScenario(t *testing.T, scenarioPath string, sender ledgerSender, ledger *SpendLedger, beforeRun func()) (traceID, gatewayTask string) {
	t.Helper()
	scenario, err := LoadScenario(scenarioPath)
	if err != nil {
		t.Fatalf("load scenario: %v", err)
	}
	scenario.GatewayInput.UserUUID = sender.origin.UserUUID
	scenario.GatewayInput.EntrypointUUID = sender.origin.EntrypointUUID
	scenario.GatewayInput.Enterprise = sender.verified
	h := New(t, scenario)
	t.Cleanup(h.Cleanup)
	h.SpendLedger = ledger
	if err = h.Setup(t.Context()); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if beforeRun != nil {
		beforeRun()
	}
	collected, err := h.Run(t.Context())
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	h.AssertEvents(t, collected, scenario.ExpectedGatewayOutput)
	traces := h.gateway.(*TestGateway).TraceIDs()
	if len(traces) != 1 {
		t.Fatalf("gateway minted %d traces, want the run's one", len(traces))
	}
	return traces[0], h.gateway.(*TestGateway).TaskIDs()[0]
}

const ledgerScenarioUser = "spend_user@example.com"

func wantOrigin(sender ledgerSender, delegated bool) (userID, userSub, entrypointID, entrypointName string) {
	userID, entrypointID = sender.origin.UserUUID, sender.origin.EntrypointUUID
	if sender.verified != nil && !delegated {
		userID = sender.verified.PlatformUserID
	}
	if userID == "" {
		userSub = ledgerScenarioUser
	}
	if entrypointID == "" {
		entrypointName = "test-gw"
	}
	return userID, userSub, entrypointID, entrypointName
}

func runLedgered(t *testing.T, scenarioPath string, sender ledgerSender, calls map[int64]ledgerCall) (rows []ledgerRow, gatewayTask string) {
	t.Helper()
	return runLedgeredAfter(t, scenarioPath, sender, calls, nil)
}

func runLedgeredAfter(t *testing.T, scenarioPath string, sender ledgerSender, calls map[int64]ledgerCall,
	beforeRun func(t *testing.T, db *platstore.DB, model platstore.ModelConfiguration)) (rows []ledgerRow, gatewayTask string) {
	t.Helper()
	db := platstore.TestDB(t)
	model := seedPricedModel(t, db, "priced-model")
	ledger := &SpendLedger{
		DB: db, ModelAlias: model.Alias, ModelName: model.ModelName, ModelConfigID: model.ID, FlushInterval: governorFlushInterval,
	}
	var hook func()
	if beforeRun != nil {
		hook = func() { beforeRun(t, db, model) }
	}
	traceID, gatewayTask := runLedgeredScenario(t, scenarioPath, sender, ledger, hook)
	rows = waitForLedgerRows(t, db, len(calls))
	if len(rows) != len(calls) {
		t.Fatalf("ledger holds %d rows, want one per physical call (%d)", len(rows), len(calls))
	}
	seen := map[int64]int{}
	for _, row := range rows {
		want, ok := calls[row.OutputTokens]
		if !ok {
			t.Fatalf("unexpected row: %+v", row)
		}
		seen[row.OutputTokens]++
		if wantChannel := cmp.Or(want.channel, httputil.SurfaceWebUI); !ptrEquals(row.Channel, wantChannel) {
			t.Errorf("row of the %d-token call channel = %v, want every row of a run to carry the one it entered through (%s)", row.OutputTokens, deref(row.Channel), wantChannel)
		}
		if !ptrEquals(row.RootNodeID, want.rootNode) {
			t.Errorf("row of the %d-token call root node = %s, want %q", row.OutputTokens, deref(row.RootNodeID), want.rootNode)
		}
		wantUserID, wantUserSub, wantEntrypointID, wantEntrypointName := wantOrigin(sender, want.delegated)
		if !pairIs(row.UserID, row.UserSub, wantUserID, wantUserSub) ||
			!pairIs(row.EntrypointID, row.EntrypointName, wantEntrypointID, wantEntrypointName) ||
			row.Surface != "agent" || !byName(row.AgentID, row.AgentName, want.agent) ||
			!ptrEquals(row.RootKind, want.rootKind) || !byName(row.RootID, row.RootName, want.root) ||
			!ptrEquals(row.TriggerKind, want.triggerKind) || !ptrEquals(row.TriggerID, want.triggerID) ||
			!byName(row.WorkflowID, row.WorkflowName, want.workflow) {
			t.Errorf("row of the %d-token call misattributed: user=%s/%s agent=%s/%s root=%s/%s/%s trigger=%s/%s workflow=%s/%s entrypoint=%s/%s",
				row.OutputTokens, deref(row.UserID), deref(row.UserSub), deref(row.AgentID), deref(row.AgentName),
				deref(row.RootKind), deref(row.RootID), deref(row.RootName), deref(row.TriggerKind), deref(row.TriggerID),
				deref(row.WorkflowID), deref(row.WorkflowName), deref(row.EntrypointID), deref(row.EntrypointName))
		}
		if !ptrEquals(row.ModelConfigID, model.ID) || row.ModelName != nil {
			t.Errorf("row of the %d-token call model = %s/%s, want configuration %s and no fallback name",
				row.OutputTokens, deref(row.ModelConfigID), deref(row.ModelName), model.ID)
		}
		if !ptrEquals(row.TraceID, traceID) {
			t.Errorf("row of the %d-token call trace = %s, want the gateway's trace %s", row.OutputTokens, deref(row.TraceID), traceID)
		}
		assertLedgerDetails(t, row, want, model, deref(rows[0].SessionID))
		gotTokens := row.tokens()
		if gotTokens != want.tokens {
			t.Errorf("row of the %d-token call counts %+v tokens, want %+v", row.OutputTokens, gotTokens, want.tokens)
		}
		gotCosts := [4]*int64{row.UncachedInputCost, row.OutputCost, row.CacheReadCost, row.CacheWriteCost}
		for i, wantCost := range [4]int64{want.costs.input, want.costs.output, want.costs.cacheRead, want.costs.cacheWrite} {
			if gotCosts[i] == nil || *gotCosts[i] != wantCost {
				t.Errorf("row of the %d-token call bucket %d cost = %v, want %d", row.OutputTokens, i, gotCosts[i], wantCost)
			}
		}
		wantTotal := want.costs.input + want.costs.output + want.costs.cacheRead + want.costs.cacheWrite
		if row.TotalCost == nil || *row.TotalCost != wantTotal {
			t.Errorf("row of the %d-token call total cost = %v, want the sum of its buckets %d", row.OutputTokens, row.TotalCost, wantTotal)
		}
	}
	for output := range calls {
		if seen[output] != 1 {
			t.Errorf("the %d-token call is in the ledger %d times, want once", output, seen[output])
		}
	}

	time.Sleep(pastOneGovernorFlush)
	if later, err := dbutil.QueryRows[ledgerRow](t.Context(), db, ledgerSelect); err != nil || len(later) != len(calls) {
		t.Errorf("ledger holds %d rows (err %v) a flush after the expected ones arrived, want %d", len(later), err, len(calls))
	}
	assertBudgetPeriodsMatchLedger(t, db, rows)
	return rows, gatewayTask
}

func assertBudgetPeriodsMatchLedger(t *testing.T, db *platstore.DB, rows []ledgerRow) {
	t.Helper()
	times, err := dbutil.QueryVals[int64](t.Context(), db, `SELECT completed_time FROM token_spend_ledger`)
	if err != nil {
		t.Fatalf("read ledger times: %v", err)
	}
	repo := platstore.NewSpendLedgerRepo(db)
	var meshCost int64
	checked := map[int64]bool{}
	for _, completed := range times {
		period := budget.PeriodStart(time.UnixMilli(completed)).UnixMilli()
		if checked[period] {
			continue
		}
		checked[period] = true
		drift, driftErr := repo.PeriodDrift(t.Context(), period, time.Minute)
		if driftErr != nil {
			t.Fatalf("budget period drift: %v", driftErr)
		}
		if len(drift) != 0 {
			t.Errorf("budget period rows disagree with the ledger the governor filed: %+v", drift)
		}
		cost, costErr := repo.PeriodCost(t.Context(), platstore.BudgetPeriodMeshSubject, period)
		if costErr != nil {
			t.Fatalf("mesh period cost: %v", costErr)
		}
		meshCost += cost
	}
	var filed int64
	for _, row := range rows {
		if row.TotalCost != nil {
			filed += *row.TotalCost
		}
	}
	if meshCost != filed || filed == 0 {
		t.Errorf("mesh period cost = %d, want the run's priced calls, %d", meshCost, filed)
	}
}

func assertLedgerDetails(t *testing.T, row ledgerRow, want ledgerCall, model platstore.ModelConfiguration, session string) {
	t.Helper()
	if row.DetailsID == nil {
		t.Errorf("row of the %d-token call has no details", row.OutputTokens)
		return
	}
	if !ptrEquals(row.ModelKey, model.Alias) || !ptrEquals(row.SentModelName, model.Provider+"/"+model.ModelName) ||
		!ptrEquals(row.ResolvedModelName, model.ModelName) || !ptrEquals(row.ResolvedProvider, model.Provider) {
		t.Errorf("row of the %d-token call model details = key %s sent %s resolved %s/%s, want %s, %s, %s/%s",
			row.OutputTokens, deref(row.ModelKey), deref(row.SentModelName), deref(row.ResolvedModelName), deref(row.ResolvedProvider),
			model.Alias, model.Provider+"/"+model.ModelName, model.ModelName, model.Provider)
	}
	if !ptrEquals(row.ToolName, "") || !ptrEquals(row.WorkflowNodeID, want.node) {
		t.Errorf("row of the %d-token call details tool=%s node=%s, want no tool and node %q",
			row.OutputTokens, deref(row.ToolName), deref(row.WorkflowNodeID), want.node)
	}
	if (row.ParentTaskID != nil) != want.delegated {
		t.Errorf("row of the %d-token call parent task = %s, want one only when delegated (%t)", row.OutputTokens, deref(row.ParentTaskID), want.delegated)
	}
	if row.SessionID == nil || *row.SessionID != session {
		t.Errorf("row of the %d-token call session = %s, want one session for the run (%s)", row.OutputTokens, deref(row.SessionID), session)
	}
}

func taskOf(t *testing.T, rows []ledgerRow, agent string) string {
	t.Helper()
	tasks := map[string]bool{}
	for _, row := range rows {
		if deref(row.AgentName) == agent {
			tasks[deref(row.TaskID)] = true
		}
	}
	if len(tasks) != 1 {
		t.Fatalf("%s calls span %d tasks, want 1", agent, len(tasks))
	}
	for task := range tasks {
		return task
	}
	return ""
}

func delegationCalls() map[int64]ledgerCall {
	agentRoot := ledgerCall{rootKind: "agent", root: "TestAgent"}
	root, peer := agentRoot, agentRoot
	root.agent, root.triggerKind = "TestAgent", "eval"
	peer.agent, peer.triggerKind, peer.triggerID, peer.delegated = "TestPeerAgentB", "agent", "TestAgent", true
	root.channel, peer.channel = "slack", "slack"
	with := func(c ledgerCall, tokens, costs buckets) ledgerCall { c.tokens, c.costs = tokens, costs; return c }
	return map[int64]ledgerCall{
		50: with(root, buckets{400, 50, 400, 200}, buckets{12_000_000, 7_500_000, 1_200_000, 7_500_000}),
		20: with(peer, buckets{400, 20, 60, 40}, buckets{12_000_000, 3_000_000, 180_000, 1_500_000}),
		10: with(peer, buckets{220, 10, 300, 0}, buckets{6_600_000, 1_500_000, 900_000, 0}),
		30: with(root, buckets{300, 30, 800, 100}, buckets{9_000_000, 4_500_000, 2_400_000, 3_750_000}),
	}
}

func TestLedger_DelegatedTaskIsLedgeredOncePerPhysicalLLMCall(t *testing.T) {
	forEachSender(t, func(t *testing.T, sender ledgerSender) {
		rows, _ := runLedgered(t, "testdata/spend_delegation.yaml", sender, delegationCalls())

		rootTask := taskOf(t, rows, "TestAgent")
		if rootTask == taskOf(t, rows, "TestPeerAgentB") {
			t.Error("the delegated task shares the root's task id")
		}
		for _, row := range rows {
			if deref(row.AgentName) == "TestPeerAgentB" && deref(row.ParentTaskID) != rootTask {
				t.Errorf("delegated row parent task = %s, want the root's task %s", deref(row.ParentTaskID), rootTask)
			}
		}
	})
}

func TestLedger_WorkflowNodeCallsAreLedgeredAgainstTheWorkflow(t *testing.T) {
	node := func(agent, nodeID string, tokens, costs buckets) ledgerCall {
		return ledgerCall{
			agent: agent, workflow: "SimpleTestWorkflow", node: nodeID,
			rootKind: "workflow", root: "SimpleTestWorkflow", rootNode: nodeID,
			triggerKind: "workflow", triggerID: "SimpleTestWorkflow", delegated: true,
			tokens: tokens, costs: costs,
		}
	}

	forEachOrigin(t, func(t *testing.T, sender ledgerSender) {
		rows, workflowTask := runLedgered(t, "testdata/spend_workflow.yaml", sender, map[int64]ledgerCall{
			50: node("TestPeerAgentA", "step_1", buckets{400, 50, 400, 200}, buckets{12_000_000, 7_500_000, 1_200_000, 7_500_000}),
			20: node("TestPeerAgentA", "step_1", buckets{400, 20, 60, 40}, buckets{12_000_000, 3_000_000, 180_000, 1_500_000}),
			10: node("TestPeerAgentB", "step_2", buckets{220, 10, 300, 0}, buckets{6_600_000, 1_500_000, 900_000, 0}),
			30: node("TestPeerAgentB", "step_2", buckets{300, 30, 800, 100}, buckets{9_000_000, 4_500_000, 2_400_000, 3_750_000}),
		})

		for _, row := range rows {
			if deref(row.ParentTaskID) != workflowTask {
				t.Errorf("row of the %d-token call parent task = %s, want the workflow's task %s", row.OutputTokens, deref(row.ParentTaskID), workflowTask)
			}
		}
	})
}

func TestLedger_ACallIsPricedByTheConfigurationThatServedItAfterItsAliasIsRenamedAndReused(t *testing.T) {
	runLedgeredAfter(t, "testdata/spend_delegation.yaml", ledgerSender{}, delegationCalls(),
		func(t *testing.T, db *platstore.DB, served platstore.ModelConfiguration) {
			repo := platstore.NewModelConfigRepo(db)
			served.Alias = "priced-model-renamed"
			if err := repo.Update(t.Context(), &served); err != nil {
				t.Fatalf("rename alias: %v", err)
			}
			free := pricing.Store(&pricing.ModelPrice{})
			reusing := platstore.ModelConfiguration{
				Alias: "priced-model", Provider: "openai", ModelName: "reusing-model", ModelAuthType: "apikey",
				ModelAuthConfig: `{}`, ModelParams: `{}`, CreatedBy: "test", UpdatedBy: "test",
				PriceInput: free.Input, PriceOutput: free.Output,
			}
			if err := repo.Create(t.Context(), &reusing); err != nil {
				t.Fatalf("reuse alias: %v", err)
			}
		})
}

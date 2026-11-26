// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package harness

import (
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/httputil"
	platstore "github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/pricing"
)

const (
	decisionModelName     = "jev-1.13.0"
	sentDecisionModelName = "typesafe/" + decisionModelName
)

var primedDecisionUsage = buckets{input: 42, output: 7}

func seedPricedDecisionModel(t *testing.T, db *platstore.DB) platstore.ModelConfiguration {
	t.Helper()
	return createPricedModel(t, db, platstore.ModelConfiguration{
		Alias: "priced-decision-model", Provider: "typesafe", ModelName: decisionModelName, ModelType: platstore.ModelTypeDecision,
	}, pricing.ModelPrice{Input: mustAmount(t, "2"), Output: mustAmount(t, "8")})
}

func runDecisionLedgered(t *testing.T, sender ledgerSender, decisionModel *platstore.ModelConfiguration) (decisionRow ledgerRow, agentRows []ledgerRow, traceID string) {
	t.Helper()
	db := platstore.TestDB(t)
	model := seedPricedModel(t, db, "priced-model")
	ledger := &SpendLedger{DB: db, ModelAlias: model.Alias, ModelName: model.ModelName, FlushInterval: governorFlushInterval}
	if decisionModel != nil {
		*decisionModel = seedPricedDecisionModel(t, db)
		ledger.DecisionAlias, ledger.DecisionConfigID, ledger.DecisionModelName = decisionModel.Alias, decisionModel.ID, decisionModel.ModelName
	}
	traceID, _ = runLedgeredScenario(t, "testdata/spend_decision.yaml", sender, ledger, nil)

	rows := waitForLedgerRows(t, db, 3)
	var decisionRows []ledgerRow
	for _, row := range rows {
		if row.Surface == "decision" {
			decisionRows = append(decisionRows, row)
		} else {
			agentRows = append(agentRows, row)
		}
	}
	if len(decisionRows) != 1 || len(agentRows) != 2 {
		t.Fatalf("ledger holds %d decision and %d agent rows, want 1 and 2", len(decisionRows), len(agentRows))
	}
	time.Sleep(pastOneGovernorFlush)
	if later, err := dbutil.QueryRows[ledgerRow](t.Context(), db, ledgerSelect); err != nil || len(later) != len(rows) {
		t.Errorf("ledger holds %d rows (err %v) a flush after the expected ones arrived, want %d", len(later), err, len(rows))
	}
	return decisionRows[0], agentRows, traceID
}

func assertDecisionRowAttribution(t *testing.T, row ledgerRow, agentRows []ledgerRow, sender ledgerSender, traceID string) {
	t.Helper()
	wantUserID, wantUserSub, wantEntrypointID, wantEntrypointName := wantOrigin(sender, true)
	if !pairIs(row.UserID, row.UserSub, wantUserID, wantUserSub) ||
		!pairIs(row.EntrypointID, row.EntrypointName, wantEntrypointID, wantEntrypointName) ||
		!pairIs(row.AgentID, row.AgentName, "", "") ||
		!byName(row.WorkflowID, row.WorkflowName, "DecisionRoutingWorkflow") ||
		!ptrEquals(row.RootKind, "workflow") || !byName(row.RootID, row.RootName, "DecisionRoutingWorkflow") ||
		!ptrEquals(row.TriggerKind, "chat") || !ptrEquals(row.TriggerID, "") ||
		!ptrEquals(row.Channel, httputil.SurfaceWebUI) || !ptrEquals(row.TraceID, traceID) {
		t.Errorf("decision row misattributed: user=%s/%s entrypoint=%s/%s agent=%s/%s workflow=%s/%s root=%s/%s/%s trigger=%s/%s channel=%s trace=%s",
			deref(row.UserID), deref(row.UserSub), deref(row.EntrypointID), deref(row.EntrypointName), deref(row.AgentID), deref(row.AgentName),
			deref(row.WorkflowID), deref(row.WorkflowName), deref(row.RootKind), deref(row.RootID), deref(row.RootName),
			deref(row.TriggerKind), deref(row.TriggerID), deref(row.Channel), deref(row.TraceID))
	}
	if !ptrEquals(row.WorkflowNodeID, "triage") || !ptrEquals(row.RootNodeID, "triage") || row.ParentTaskID != nil {
		t.Errorf("decision row details node=%s rootNode=%s parent=%s, want the triage node of an undelegated workflow task",
			deref(row.WorkflowNodeID), deref(row.RootNodeID), deref(row.ParentTaskID))
	}
	for _, agentRow := range agentRows {
		if row.TaskID == nil || !ptrEquals(agentRow.ParentTaskID, *row.TaskID) {
			t.Errorf("decision row task = %s, want the workflow task the branch's calls name as parent (%s)", deref(row.TaskID), deref(agentRow.ParentTaskID))
		}
		if row.SessionID == nil || !ptrEquals(agentRow.SessionID, *row.SessionID) {
			t.Errorf("decision row session = %s, want the run's session %s", deref(row.SessionID), deref(agentRow.SessionID))
		}
	}
	got := row.tokens()
	if want := primedDecisionUsage; got != want {
		t.Errorf("decision row counts %+v tokens, want the provider's two buckets %+v", got, want)
	}
	if !ptrEquals(row.SentModelName, sentDecisionModelName) {
		t.Errorf("decision row sent model = %s, want %s", deref(row.SentModelName), sentDecisionModelName)
	}
}

func TestLedger_DecisionCallOfARegisteredModelIsLedgeredAndPricedAtItsNode(t *testing.T) {
	forEachOrigin(t, func(t *testing.T, sender ledgerSender) {
		var model platstore.ModelConfiguration
		row, agentRows, traceID := runDecisionLedgered(t, sender, &model)

		assertDecisionRowAttribution(t, row, agentRows, sender, traceID)
		if !ptrEquals(row.ModelConfigID, model.ID) || row.ModelName != nil || !ptrEquals(row.ModelKey, model.Alias) {
			t.Errorf("decision row model = %s/%s key %s, want configuration %s by its alias %s",
				deref(row.ModelConfigID), deref(row.ModelName), deref(row.ModelKey), model.ID, model.Alias)
		}
		gotCosts := [4]*int64{row.UncachedInputCost, row.OutputCost, row.CacheReadCost, row.CacheWriteCost}
		for i, want := range [4]int64{840_000, 560_000, 0, 0} {
			if gotCosts[i] == nil || *gotCosts[i] != want {
				t.Errorf("decision row bucket %d cost = %v, want %d", i, gotCosts[i], want)
			}
		}
		if row.TotalCost == nil || *row.TotalCost != 1_400_000 {
			t.Errorf("decision row total cost = %v, want 1400000", row.TotalCost)
		}
	})
}

func TestLedger_DecisionCallOfAnInlineEndpointIsLedgeredUnpriced(t *testing.T) {
	sender := ledgerSender{origin: stampedOrigin}
	row, agentRows, traceID := runDecisionLedgered(t, sender, nil)

	assertDecisionRowAttribution(t, row, agentRows, sender, traceID)
	if row.ModelConfigID != nil || !ptrEquals(row.ModelName, sentDecisionModelName) || row.ModelKey != nil || row.TotalCost != nil {
		t.Errorf("decision row model = %s/%s key %s cost %v, want the unpriced provider model %s",
			deref(row.ModelConfigID), deref(row.ModelName), deref(row.ModelKey), row.TotalCost, sentDecisionModelName)
	}
}

func TestLedger_DecisionCallOfAWorkflowAnAgentDelegatedBooksTheChainsEntrypoint(t *testing.T) {
	forEachOrigin(t, func(t *testing.T, sender ledgerSender) {
		db := platstore.TestDB(t)
		model := seedPricedModel(t, db, "priced-model")
		runLedgeredScenario(t, "testdata/spend_decision_delegated.yaml", sender,
			&SpendLedger{DB: db, ModelAlias: model.Alias, ModelName: model.ModelName, FlushInterval: governorFlushInterval}, nil)

		rows := waitForLedgerRows(t, db, 5)
		var decisionRows []ledgerRow
		for _, row := range rows {
			if row.Surface == "decision" {
				decisionRows = append(decisionRows, row)
			}
		}
		if len(rows) != 5 || len(decisionRows) != 1 {
			t.Fatalf("ledger holds %d rows with %d decision rows, want the agent's 2, the branch's 2 and 1 decision row", len(rows), len(decisionRows))
		}
		row := decisionRows[0]
		_, _, wantEntrypointID, wantEntrypointName := wantOrigin(sender, true)
		if !pairIs(row.EntrypointID, row.EntrypointName, wantEntrypointID, wantEntrypointName) ||
			!ptrEquals(row.TriggerKind, "agent") || !ptrEquals(row.TriggerID, "TestAgent") {
			t.Errorf("decision row entrypoint=%s/%s trigger=%s/%s, want entrypoint %s/%s triggered by TestAgent",
				deref(row.EntrypointID), deref(row.EntrypointName), deref(row.TriggerKind), deref(row.TriggerID),
				wantEntrypointID, wantEntrypointName)
		}
	})
}

// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package harness

import (
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/budget"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	admin "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	platstore "github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/spend"
)

const budgetSignalWait = 10 * time.Second

func waitForGate(t *testing.T, gate *budget.Gate, user, what string, done func(error) bool) {
	t.Helper()
	deadline := time.After(budgetSignalWait)
	for {
		changed := gate.Changed()
		if done(gate.Check(user)) {
			return
		}
		select {
		case <-changed:
		case <-deadline:
			t.Fatalf("%s: the gate still answers %v", what, gate.Check(user))
		}
	}
}

func isAdmitted(err error) bool { return err == nil }

func isExhausted(err error) bool {
	_, ok := errors.AsType[*budget.ExhaustedError](err)
	return ok
}

func spendOverTheMeshLimit(t *testing.T, db *platstore.DB) {
	t.Helper()
	if _, err := db.ExecContext(t.Context(), `UPDATE budgets SET amount = 10 WHERE subject_type = 'mesh'`); err != nil {
		t.Fatalf("limit the mesh: %v", err)
	}
	entry := platstore.SpendLedgerEntry{
		ID: idgen.New(), CompletedTime: time.Now().UnixMilli(), UserSub: new("spend_user@example.com"),
		Surface: "agent", ModelName: new(providerModelName), OutputTokens: 1, Cost: &spend.Cost{Output: 10},
	}
	if _, err := platstore.NewSpendLedgerRepo(db).Insert(t.Context(), []platstore.SpendLedgerEntry{entry}); err != nil {
		t.Fatalf("file spend: %v", err)
	}
}

func runWithTheMeshSpent(t *testing.T, scenarioPath string) (*TestHarness, *TestScenario, []CollectedEvent) {
	t.Helper()
	return runWithTheMeshSpentIn(t, scenarioPath, "", false)
}

func runWithTheMeshSpentIn(t *testing.T, scenarioPath, gatewayMode string, gatewayOnly bool) (*TestHarness, *TestScenario, []CollectedEvent) {
	t.Helper()
	db := platstore.TestDB(t)
	model := seedPricedModel(t, db, "priced-model")
	spendOverTheMeshLimit(t, db)
	scenario, err := LoadScenario(scenarioPath)
	if err != nil {
		t.Fatalf("load scenario: %v", err)
	}
	h := New(t, scenario)
	t.Cleanup(h.Cleanup)
	if gatewayMode != "" {
		h.GatewayMode = gatewayMode
	}
	h.SpendLedger = &SpendLedger{DB: db, ModelAlias: model.Alias, ModelName: model.ModelName, FlushInterval: governorFlushInterval,
		EnforceBudget: true, GateGatewayOnly: gatewayOnly}
	if err = h.Setup(t.Context()); err != nil {
		t.Fatalf("setup: %v", err)
	}
	waitForGate(t, h.SpendLedger.gate, "", "the monitor's broadcast never blocked the harness's gate", isExhausted)

	collected, err := h.Run(t.Context())
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	return h, scenario, collected
}

func TestBudget_ASpentMeshStopsTheAgentBeforeItsModelAndTheGatewaySeesWhy(t *testing.T) {
	h, scenario, collected := runWithTheMeshSpent(t, "testdata/spend_budget_stop.yaml")

	h.AssertEvents(t, collected, scenario.ExpectedGatewayOutput)
	if requests := h.llmClient.Recorder().Requests(); len(requests) != 0 {
		t.Errorf("the model received %d requests while the mesh budget was spent, want 0", len(requests))
	}
}

func TestBudget_ASpentMeshFailsADecisionNodeBeforeItsProvider(t *testing.T) {
	h, scenario, collected := runWithTheMeshSpent(t, "testdata/spend_budget_decision_stop.yaml")

	h.AssertEvents(t, collected, scenario.ExpectedGatewayOutput)
	if served := h.decisionServer.servedCount(); served != 0 {
		t.Errorf("the decision provider served %d requests while the mesh budget was spent, want 0", served)
	}
	if requests := h.llmClient.Recorder().Requests(); len(requests) != 0 {
		t.Errorf("a branch agent's model received %d requests after the decision node failed, want 0", len(requests))
	}
}

func TestBudget_ASpentMeshIsRefusedAtTheGatewayBeforeAnyAgentRuns(t *testing.T) {
	h, _, collected := runWithTheMeshSpentIn(t, "testdata/spend_budget_gateway_refusal.yaml", "event-mesh", true)

	if len(collected) != 0 {
		t.Errorf("the gateway published %d results for a refused event, want none", len(collected))
	}
	if requests := h.llmClient.Recorder().Requests(); len(requests) != 0 {
		t.Errorf("the ungated agent's model received %d requests, so the gateway admitted the event", len(requests))
	}
}

func TestBudget_TheGovernorsSignalBlocksTheMeshAndRaisingTheLimitUnblocksIt(t *testing.T) {
	db := platstore.TestDB(t)
	model := seedPricedModel(t, db, "priced-model")
	if _, err := db.ExecContext(t.Context(), `UPDATE budgets SET amount = 1 WHERE subject_type = 'mesh'`); err != nil {
		t.Fatalf("limit the mesh: %v", err)
	}
	scenario, err := LoadScenario("testdata/spend_budget_stop.yaml")
	if err != nil {
		t.Fatalf("load scenario: %v", err)
	}
	h := New(t, scenario)
	t.Cleanup(h.Cleanup)
	h.SpendLedger = &SpendLedger{DB: db, ModelAlias: model.Alias, ModelName: model.ModelName, FlushInterval: governorFlushInterval,
		EnforceBudget: true}
	if err = h.Setup(t.Context()); err != nil {
		t.Fatalf("setup: %v", err)
	}
	gate := h.SpendLedger.gate
	if err := gate.Check(""); err != nil {
		t.Fatalf("the mesh was refused before any spend: %v", err)
	}

	if _, err := h.Run(t.Context()); err != nil {
		t.Fatalf("run: %v", err)
	}
	if requests := h.llmClient.Recorder().Requests(); len(requests) != 1 {
		t.Fatalf("the model received %d requests, want the one call that crosses the limit", len(requests))
	}
	waitForGate(t, gate, "", "filing the crossing call did not block the mesh", isExhausted)

	var meshID string
	if err := db.QueryRowContext(t.Context(), `SELECT CAST(id AS TEXT) FROM budgets WHERE subject_type = 'mesh'`).Scan(&meshID); err != nil {
		t.Fatalf("find the mesh budget: %v", err)
	}
	logger := slog.New(slog.DiscardHandler)
	budgets := admin.NewBudgetService(platstore.NewBudgetsRepo(db), admin.NewBudgetsChangedSignal(h.broker, "test-ns", logger), logger)
	if _, err := budgets.Update(t.Context(), meshID, admin.UpdateBudgetRequest{Amount: new("1000"), AmountSet: true}, "admin"); err != nil {
		t.Fatalf("raise the mesh limit: %v", err)
	}
	waitForGate(t, gate, "", "raising the limit did not unblock the mesh", isAdmitted)
}

func TestBudget_AUsersOwnSpendBlocksOnlyThemUntilAnAdminRaisesTheirLimit(t *testing.T) {
	for name, tc := range map[string]struct {
		withRow bool
		raise   func(t *testing.T, admins budgetAdmins, user GatewayInput)
	}{
		"a user known only by email, raised by a pending assignment": {
			raise: func(t *testing.T, admins budgetAdmins, user GatewayInput) {
				generous, err := admins.budgets.Create(t.Context(), admin.CreateBudgetRequest{
					Name: "Generous", SubjectType: platstore.BudgetSubjectUser, Amount: new("1000"), AmountSet: true}, "admin")
				if err != nil {
					t.Fatalf("create a budget: %v", err)
				}
				if _, err := admins.assignments.Create(t.Context(), admin.CreateBudgetAssignmentRequest{
					SubjectType: platstore.BudgetSubjectUser, BudgetID: generous.ID,
					Identity: &admin.BudgetAssignmentIdentity{Type: platstore.BudgetIdentityEmail, Value: user.UserIdentity},
				}, "admin"); err != nil {
					t.Fatalf("assign the budget to the email: %v", err)
				}
			},
		},
		"a user with a platform row, raised by a top-up": {
			withRow: true,
			raise: func(t *testing.T, admins budgetAdmins, user GatewayInput) {
				if _, _, err := admins.topUps.Grant(t.Context(), admin.CreateBudgetTopUpRequest{
					SubjectType: platstore.BudgetSubjectUser, SubjectID: user.UserUUID, Amount: "1000"}, "admin"); err != nil {
					t.Fatalf("top the user up: %v", err)
				}
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			db := platstore.TestDB(t)
			model := seedPricedModel(t, db, "priced-model")
			if _, err := db.ExecContext(t.Context(), `UPDATE budgets SET amount = 1 WHERE id = ?`, platstore.SeededDefaultUserBudgetID); err != nil {
				t.Fatalf("limit the user default: %v", err)
			}
			spender := GatewayInput{TargetAgentName: "TestAgent", UserIdentity: "spender@example.com"}
			if tc.withRow {
				spender.UserUUID = seedPlatformUser(t, db, spender.UserIdentity)
			}
			spenderKey := budget.UserKey(spender.UserUUID, spender.UserIdentity)
			other := GatewayInput{TargetAgentName: "TestAgent", UserIdentity: "other@example.com"}

			scenario, err := LoadScenario("testdata/spend_user_budget.yaml")
			if err != nil {
				t.Fatalf("load scenario: %v", err)
			}
			h := New(t, scenario)
			t.Cleanup(h.Cleanup)
			h.SpendLedger = &SpendLedger{DB: db, ModelAlias: model.Alias, ModelName: model.ModelName, FlushInterval: governorFlushInterval,
				EnforceBudget: true}
			if err = h.Setup(t.Context()); err != nil {
				t.Fatalf("setup: %v", err)
			}
			gate := h.SpendLedger.gate
			waitForBlock(t, gate, false, "a user was refused before anyone spent")

			if text := sendAs(t, h, spender); strings.Contains(text, "usage budget is used up") {
				t.Fatalf("the spender's first task was refused: %.300s", text)
			}
			waitForGate(t, gate, spenderKey, "filing the spender's call did not block them", isExhausted)

			if text := sendAs(t, h, spender); !strings.Contains(text, "your usage budget is used up until") {
				t.Errorf("the spender's next task = %.300s, want it stopped on their own budget", text)
			}
			if text := sendAs(t, h, other); !strings.Contains(text, "Another user's answer.") {
				t.Errorf("another user's task = %.300s, want it served", text)
			}

			tc.raise(t, newBudgetAdmins(h, db), spender)
			waitForGate(t, gate, spenderKey, "raising the spender's limit did not lift their block", isAdmitted)
			if text := sendAs(t, h, spender); !strings.Contains(text, "The answer once the user's limit was raised.") {
				t.Errorf("the spender's task after the raise = %.300s, want it served", text)
			}
			if requests := h.llmClient.Recorder().Requests(); len(requests) != 3 {
				t.Errorf("the model received %d requests, want 3: the refused task must never reach it", len(requests))
			}
		})
	}
}

func waitForBlock(t *testing.T, gate *budget.Gate, blocked bool, what string) {
	t.Helper()
	deadline := time.After(budgetSignalWait)
	for {
		changed := gate.Changed()
		if gate.HoldsBlock() == blocked {
			return
		}
		select {
		case <-changed:
		case <-deadline:
			t.Fatalf("%s: the gate's block is still %v", what, !blocked)
		}
	}
}

func sendAs(t *testing.T, h *TestHarness, input GatewayInput) string {
	t.Helper()
	gw, ok := h.gateway.(*TestGateway)
	if !ok {
		t.Fatalf("gateway is %T, want the broker-level TestGateway", h.gateway)
	}
	input.Prompt = &PromptInput{Parts: []InputPart{{Type: "text", Text: "Summarize the report."}}}
	if _, err := gw.SendRequest(t.Context(), input.TargetAgentName, &input); err != nil {
		t.Fatalf("send as %s: %v", input.UserIdentity, err)
	}
	events, err := gw.CollectEvents(t.Context(), h.requestTimeout())
	if err != nil {
		t.Fatalf("collect for %s: %v", input.UserIdentity, err)
	}
	gw.ResetForNextRequest()
	var text strings.Builder
	for _, e := range events {
		text.Write(e.Payload)
	}
	return text.String()
}

type budgetAdmins struct {
	budgets     *admin.BudgetService
	assignments *admin.BudgetAssignmentService
	topUps      *admin.BudgetTopUpService
}

func newBudgetAdmins(h *TestHarness, db *platstore.DB) budgetAdmins {
	logger := slog.New(slog.DiscardHandler)
	changed := admin.NewBudgetsChangedSignal(h.broker, "test-ns", logger)
	budgets, topUps := platstore.NewBudgetsRepo(db), platstore.NewBudgetTopUpsRepo(db)
	return budgetAdmins{
		budgets:     admin.NewBudgetService(budgets, changed, logger),
		assignments: admin.NewBudgetAssignmentService(platstore.NewBudgetAssignmentsRepo(db), budgets, platstore.NewUsersRepo(db), topUps, changed, logger),
		topUps:      admin.NewBudgetTopUpService(topUps, changed, logger),
	}
}

func seedPlatformUser(t *testing.T, db *platstore.DB, email string) string {
	t.Helper()
	id := idgen.New()
	if _, err := db.ExecContext(t.Context(),
		`INSERT INTO users (id, issuer, sub, email, source, provisioned_via, active, created_by, updated_by, created_time, updated_time)
		 VALUES (?, 'https://idp.example.com', ?, ?, '{}', 'jit', 1, 'test', 'test', 1, 1)`, id, email, email); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	return id
}

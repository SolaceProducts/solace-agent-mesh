// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/bootstrap"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/budget"
	"github.com/SolaceProducts/solace-agent-mesh/internal/budget/budgettest"
	"github.com/SolaceProducts/solace-agent-mesh/internal/budgetwatch"
	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
	"github.com/SolaceProducts/solace-agent-mesh/internal/control"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
	"github.com/SolaceProducts/solace-agent-mesh/internal/spend"
)

const budgetStateTestNamespace = "budget-state-control-ns"

func startBudgetStateControlPlane(t *testing.T, dbURL string) (*control.Client, broker.Broker) {
	t.Helper()
	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatalf("connect broker: %v", err)
	}
	rt := runtime.NewRuntime(brk, config.FromMap(nil))
	core := platpkg.NewPlatformCore(map[string]any{
		"namespace":    budgetStateTestNamespace,
		"database_url": dbURL,
	}, platpkg.ExtensionPoints{})
	core.SetServices(rt.Services())
	if err := core.Init(t.Context(), nil); err != nil {
		t.Fatalf("init platform core: %v", err)
	}
	rt.Register("platform", &stubPlatformComponent{mp: &ManageablePlatform{PlatformCore: core, deps: &RouteDeps{}, logger: discardLogger()}})
	rt.Register("control", control.NewService(rt, budgetStateTestNamespace))
	if err := rt.Init(t.Context()); err != nil {
		t.Fatalf("init runtime: %v", err)
	}
	if err := rt.Start(t.Context()); err != nil {
		t.Fatalf("start runtime: %v", err)
	}
	t.Cleanup(func() {
		_ = rt.Stop(context.Background())
		_ = brk.Disconnect(context.Background())
	})
	return control.NewClient(brk, budgetStateTestNamespace, control.WithTimeout(5*time.Second)), brk
}

func TestBudgetStateControl_AnEnforcementPointPullsTheCurrentState(t *testing.T) {
	t.Parallel()
	dbURL := entTestTemplate.Clone(t)
	spendTheMeshBudget(t, dbURL)
	client, _ := startBudgetStateControlPlane(t, dbURL)

	raw, err := client.Custom(t.Context(), "platform", "get", "budget-state", nil)
	if err != nil {
		t.Fatalf("pull budget state: %v", err)
	}
	var state budget.EnforcementState
	if err := json.Unmarshal(raw, &state); err != nil {
		t.Fatalf("decode budget state %s: %v", raw, err)
	}
	if !state.BudgetsInUse || !state.MeshBlocked || !state.PeriodEnd.Equal(budget.PeriodEnd(time.Now())) {
		t.Errorf("pulled state = %+v, want the spent mesh blocked until the period ends", state)
	}
}

func TestBudgetStateControl_RequiresTheBudgetReadScope(t *testing.T) {
	t.Parallel()
	mp := testManageablePlatform()
	identity := &auth.Identity{Scopes: []string{rbac.ScopeWebuiSettingsRead}}
	if _, err := mp.HandleManagement(t.Context(), "get", []string{"budget-state"}, nil, identity); err == nil {
		t.Error("an identity without the budget read scope pulled the budget state")
	}
}

func spendTheMeshBudget(t *testing.T, dbURL string) {
	t.Helper()
	db, err := store.Open(dbURL, nil)
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, execErr := db.ExecContext(t.Context(), `UPDATE budgets SET amount = 10 WHERE subject_type = 'mesh'`); execErr != nil {
		t.Fatalf("limit the mesh: %v", execErr)
	}
	entry := store.SpendLedgerEntry{
		ID: idgen.New(), CompletedTime: time.Now().UnixMilli(), UserSub: new("u"),
		Surface: "agent", ModelName: new("m"), OutputTokens: 1, Cost: &spend.Cost{Output: 10},
	}
	if _, insertErr := store.NewSpendLedgerRepo(db).Insert(t.Context(), []store.SpendLedgerEntry{entry}); insertErr != nil {
		t.Fatalf("file spend: %v", insertErr)
	}
}

func watchedGate(t *testing.T, brk broker.Broker) *budget.Gate {
	t.Helper()
	gate := budgettest.UnconfirmedGate(t)
	w := budgetwatch.New(gate, bootstrap.NewPlatformBudgetStateFetch(brk, budgetStateTestNamespace), brk, budgetStateTestNamespace, discardLogger())
	if err := w.Start(t.Context()); err != nil {
		t.Fatalf("start watcher: %v", err)
	}
	t.Cleanup(func() { _ = w.Stop(context.Background()) }) //nolint:contextcheck
	return gate
}

func eventually(t *testing.T, cond func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(20 * time.Millisecond)
	}
	return true
}

func TestBudgetStateControl_ARestartedGatewayLearnsTheMeshIsSpentFromThePull(t *testing.T) {
	t.Parallel()
	dbURL := entTestTemplate.Clone(t)
	spendTheMeshBudget(t, dbURL)
	_, brk := startBudgetStateControlPlane(t, dbURL)

	gate := watchedGate(t, brk)

	if !eventually(t, func() bool { return errors.Is(gate.Check(""), budget.ErrExhausted) }) {
		t.Errorf("gate = %v, want the pulled block", gate.Check(""))
	}
}

// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package runtime

import (
	"context"
	"log/slog"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/budget"
	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
)

type stubComponent struct{ id int }

func (stubComponent) Init(context.Context, config.Config) error { return nil }
func (stubComponent) Start(context.Context) error               { return nil }
func (stubComponent) Stop(context.Context) error                { return nil }
func (stubComponent) Health() error                             { return nil }

func TestNewRuntime_HasAGateThatAdmitsWithoutBudgetEnforcement(t *testing.T) {
	gate := NewRuntime(nil, nil).Services().BudgetGate()
	if gate == nil {
		t.Fatal("a runtime built without budget enforcement has no gate")
	}
	if err := gate.Check("user-1"); err != nil {
		t.Errorf("Check = %v, want a gate that has heard nothing to admit", err)
	}
}

func TestWithBudgetEnforcement_RegistersTheWatcherAheadOfComponentsRegisteredLater(t *testing.T) {
	gate := budget.NewGate(slog.New(slog.DiscardHandler))
	watcher := stubComponent{}
	rt := NewRuntime(nil, nil, WithBudgetEnforcement(gate, watcher))
	rt.Register("first-registered", stubComponent{})

	if got := rt.Services().BudgetGate(); got != gate {
		t.Errorf("BudgetGate = %p, want the gate the option gave", got)
	}
	components := rt.Components()
	if len(components) != 2 || components[0].Name != "budget-watch" {
		t.Fatalf("components = %+v, want budget-watch first", components)
	}
}

func TestWithBudgetEnforcement_ALaterOneReplacesAnEarlierOne(t *testing.T) {
	g1, g2 := budget.NewGate(slog.New(slog.DiscardHandler)), budget.NewGate(slog.New(slog.DiscardHandler))
	w1, w2 := stubComponent{id: 1}, stubComponent{id: 2}
	rt := NewRuntime(nil, nil, WithBudgetEnforcement(g1, w1), WithBudgetEnforcement(g2, w2))

	if got := rt.Services().BudgetGate(); got != g2 {
		t.Errorf("BudgetGate = %p, want the later gate %p", got, g2)
	}
	components := rt.Components()
	if len(components) != 1 || components[0].Name != "budget-watch" || components[0].Component != Component(w2) {
		t.Fatalf("components = %+v, want only the later watcher as budget-watch", components)
	}
}

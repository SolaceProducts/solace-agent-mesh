// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package bootstrap

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/budget"
	"github.com/SolaceProducts/solace-agent-mesh/internal/budgetwatch"
	"github.com/SolaceProducts/solace-agent-mesh/internal/control"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

const budgetStateControlTimeout = 5 * time.Second

func BudgetEnforcement(brk broker.Broker, namespace string, logger *slog.Logger) runtime.Option {
	gate := budget.NewGate(logger)
	var fetch budgetwatch.Fetch
	if brk != nil {
		fetch = NewPlatformBudgetStateFetch(brk, namespace)
	}
	return runtime.WithBudgetEnforcement(gate, budgetwatch.New(gate, fetch, brk, namespace, logger))
}

func NewPlatformBudgetStateFetch(brk broker.Broker, namespace string) budgetwatch.Fetch {
	client := control.NewClient(brk, namespace, control.WithTimeout(budgetStateControlTimeout))
	return func(ctx context.Context) (budget.EnforcementState, error) {
		raw, err := client.Custom(ctx, platformControlApp, platformControlGetMethod, "budget-state", nil)
		if err != nil {
			return budget.EnforcementState{}, fmt.Errorf("platform budget-state lookup: %w", err)
		}
		state, err := budget.DecodeState(raw)
		if err != nil {
			return budget.EnforcementState{}, fmt.Errorf("platform budget-state response: %w", err)
		}
		return state, nil
	}
}

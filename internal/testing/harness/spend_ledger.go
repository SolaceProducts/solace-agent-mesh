// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package harness

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/agent"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/budget"
	"github.com/SolaceProducts/solace-agent-mesh/internal/budgetwatch"
	"github.com/SolaceProducts/solace-agent-mesh/internal/leader"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/budgetstate"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/governor"
	platstore "github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samapp"
	"github.com/SolaceProducts/solace-agent-mesh/internal/spend"
	"github.com/SolaceProducts/solace-agent-mesh/internal/workflow"
)

const spendLedgerStopTimeout = 10 * time.Second

type SpendLedger struct {
	DB                *platstore.DB
	ModelAlias        string
	ModelName         string
	DecisionAlias     string
	DecisionConfigID  string
	DecisionModelName string
	ModelConfigID     string
	FlushInterval     time.Duration
	EnforceBudget     bool
	GateGatewayOnly   bool

	logger      *slog.Logger
	publisher   *spend.Publisher
	governor    *governor.Governor
	decisionURL string
	gate        *budget.Gate
	watcher     *budgetwatch.Watcher
	stopMonitor context.CancelFunc
	monitorDone chan struct{}
}

const budgetMonitorRetry = 100 * time.Millisecond

func (l *SpendLedger) start(ctx context.Context, brk broker.Broker, namespace string, logger *slog.Logger) error {
	if l == nil {
		return nil
	}
	l.logger = logger
	l.governor = governor.New(governor.Config{
		Namespace:     namespace,
		Broker:        brk,
		Ledger:        platstore.NewSpendLedgerRepo(l.DB),
		Prices:        governor.NewModelConfigPrices(platstore.NewModelConfigRepo(l.DB), logger),
		Logger:        logger,
		FlushInterval: l.FlushInterval,
	})
	if err := l.governor.Start(ctx); err != nil {
		return err
	}
	l.publisher = spend.NewPublisher(brk, namespace, logger)
	if l.EnforceBudget {
		return l.startBudgetEnforcement(ctx, brk, namespace)
	}
	return nil
}

func (l *SpendLedger) startBudgetEnforcement(ctx context.Context, brk broker.Broker, namespace string) error {
	l.gate = budget.NewGate(l.logger)
	l.watcher = budgetwatch.New(l.gate, nil, brk, namespace, l.logger)
	if err := l.watcher.Start(ctx); err != nil {
		return err
	}
	job := budgetstate.MonitorJob(budgetstate.NewStore(l.DB), brk, namespace, l.logger, budgetstate.Alerting{})
	monitorCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	l.stopMonitor, l.monitorDone = cancel, make(chan struct{})
	go func() {
		defer close(l.monitorDone)
		for {
			err := job.Run(monitorCtx, leader.Term{Number: 1, Holder: "harness"})
			if monitorCtx.Err() != nil {
				return
			}
			l.logger.Warn("budget monitor run failed", "error", err)
			select {
			case <-monitorCtx.Done():
				return
			case <-time.After(budgetMonitorRetry):
			}
		}
	}()
	return nil
}

func (l *SpendLedger) bindings(client llm.Client) runtime.ModelBindings {
	if l == nil {
		return nil
	}
	return aliasBinding{alias: l.ModelAlias, modelName: l.ModelName, configID: l.ModelConfigID, client: client, decisionAlias: l.DecisionAlias, decisionConfigID: l.DecisionConfigID, decisionModelName: l.DecisionModelName, decisionURL: l.decisionURL}
}

func (l *SpendLedger) routeDecisionsTo(url string) {
	if l != nil {
		l.decisionURL = url
	}
}

func (l *SpendLedger) configureDecision(provider *workflow.DecisionProviderConfig) {
	if l != nil && l.DecisionAlias != "" {
		*provider = workflow.DecisionProviderConfig{ModelAlias: l.DecisionAlias}
	}
}

func (l *SpendLedger) attachWorkflow(w *workflow.WorkflowInstance) {
	if l != nil {
		w.SetSpendPublisher(l.publisher)
	}
}

func (l *SpendLedger) agentGate() *budget.Gate {
	if l == nil {
		return budget.NewGate(slog.New(slog.DiscardHandler))
	}
	if l.gate == nil || l.GateGatewayOnly {
		return budget.NewGate(l.logger)
	}
	return l.gate
}

func (l *SpendLedger) gatewayGate() *budget.Gate {
	if l == nil || !l.GateGatewayOnly {
		return nil
	}
	return l.gate
}

func (l *SpendLedger) configure(cfg *agent.AgentConfig) {
	if l != nil {
		cfg.Model = ""
		cfg.ModelAlias = l.ModelAlias
	}
}

func (l *SpendLedger) attach(ag *agent.Agent) {
	if l != nil {
		ag.SetSpendPublisher(l.publisher)
	}
}

func (l *SpendLedger) stop(ctx context.Context) {
	if l == nil || l.governor == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), spendLedgerStopTimeout)
	defer cancel()
	if l.stopMonitor != nil {
		l.stopMonitor()
		<-l.monitorDone
		if err := l.watcher.Stop(ctx); err != nil {
			l.logger.Warn("budget watcher did not stop cleanly", "error", err)
		}
	}
	if err := l.publisher.Close(ctx); err != nil {
		l.logger.Warn("spend ledger publisher did not close cleanly", "error", err)
	}
	if err := l.governor.Stop(ctx); err != nil {
		l.logger.Warn("spend ledger governor did not stop cleanly", "error", err)
	}
}

type aliasBinding struct {
	alias, modelName, configID                                      string
	client                                                          llm.Client
	decisionAlias, decisionConfigID, decisionModelName, decisionURL string
}

func (b aliasBinding) ModelClient(alias string) (llm.Client, error) {
	if alias != b.alias {
		return nil, fmt.Errorf("no binding for alias %q", alias)
	}
	return b.client, nil
}

func (aliasBinding) GrantTokens() llm.GrantTokens { return nil }

func (b aliasBinding) ResolveOrFetch(_ context.Context, alias string) (llm.Client, error) {
	return b.ModelClient(alias)
}

func (b aliasBinding) ResolveRawOrFetch(_ context.Context, alias string) (map[string]any, error) {
	if alias != "" && alias == b.decisionAlias {
		return map[string]any{
			samapp.ModelTypeKey: samapp.DecisionModelType, "api_base": b.decisionURL, "api_key": "test-key",
			samapp.ModelConfigIDKey: b.decisionConfigID, "model": b.decisionModelName,
		}, nil
	}
	if alias != b.alias {
		return nil, fmt.Errorf("no binding for alias %q", alias)
	}
	return map[string]any{"model": b.modelName, samapp.ModelConfigIDKey: b.configID}, nil
}

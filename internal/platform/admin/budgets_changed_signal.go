// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"log/slog"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
)

const budgetsChangedPublishTimeout = 5 * time.Second

type BudgetsChangedSignal struct {
	broker    broker.Broker
	namespace string
	logger    *slog.Logger
}

func NewBudgetsChangedSignal(brk broker.Broker, namespace string, logger *slog.Logger) *BudgetsChangedSignal {
	if brk == nil {
		return nil
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &BudgetsChangedSignal{broker: brk, namespace: namespace, logger: logger.With("logger_name", "budgets-changed")}
}

func (s *BudgetsChangedSignal) send(ctx context.Context) {
	if s == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), budgetsChangedPublishTimeout)
	defer cancel()
	topic := a2a.BudgetsChangedTopic(s.namespace)
	if err := s.broker.Publish(ctx, topic, &broker.Message{Topic: topic}); err != nil {
		s.logger.Warn("budgets changed signal not published; the budget state catches up at its next heartbeat", "error", err)
		return
	}
	s.logger.Debug("budgets changed signal published")
}

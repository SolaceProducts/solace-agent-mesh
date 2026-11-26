// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eventmesh

import (
	"log/slog"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/sacexpr"
)

const (
	metricEventFilterEvaluations = "entrypoint.event_filter.evaluations"

	filterOutcomeMatch   = "match"
	filterOutcomeNoMatch = "no_match"
	filterOutcomeError   = "error"
)

func (a *EventMeshAdapter) admitsEvent(handler *EventHandlerConfig, exprCtx *sacexpr.Context, msg *broker.Message, logger *slog.Logger) bool {
	if handler.EventFilter == nil {
		return true
	}
	matched, err := handler.EventFilter.Match(exprCtx)
	switch {
	case err != nil:
		a.countFilterOutcome(handler, filterOutcomeError)
		logger.Warn("event_filter could not be evaluated; delivering the event", "messageID", msg.ID, "error", err)
		return true
	case matched:
		a.countFilterOutcome(handler, filterOutcomeMatch)
		return true
	default:
		a.countFilterOutcome(handler, filterOutcomeNoMatch)
		logger.Debug("event dropped by event_filter", "messageID", msg.ID)
		return false
	}
}

func (a *EventMeshAdapter) countFilterOutcome(handler *EventHandlerConfig, outcome string) {
	svc := a.core.Services()
	if svc == nil {
		return
	}
	svc.Metrics().Counter(metricEventFilterEvaluations, 1,
		"entrypoint.name", a.core.GatewayID(),
		"event_rule.name", handler.Name,
		"outcome", outcome,
	)
}

// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package harness

import (
	"context"
	"log/slog"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
)

const (
	activityLogQueueSize = 600
	activityLogGatewayID = "test-gw-activity"
)

type ActivityLog struct {
	DB *store.DB

	svc *entrypoint.TaskLoggerService
}

func (l *ActivityLog) start(ctx context.Context, brk broker.Broker, namespace string, logger *slog.Logger) error {
	if l == nil {
		return nil
	}
	cfg := entrypoint.ParseTaskLoggerConfig(map[string]any{"enabled": true}, activityLogQueueSize)
	l.svc = entrypoint.NewTaskLoggerService(cfg, store.NewTaskRepo(l.DB), store.NewTaskEventRepo(l.DB),
		brk, namespace, activityLogGatewayID, logger)
	return l.svc.Start(ctx)
}

func (l *ActivityLog) stop() {
	if l == nil || l.svc == nil {
		return
	}
	l.svc.Stop()
}

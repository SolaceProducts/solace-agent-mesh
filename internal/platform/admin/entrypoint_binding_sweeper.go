// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/leader"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

const (
	bindingRetention     = 90 * 24 * time.Hour
	bindingSweepInterval = 6 * time.Hour
)

func entrypointBindingSweepJob(bindings *store.EntrypointIdentityBindingsRepo, logger *slog.Logger) leader.Job {
	return leader.Job{
		Name:  leader.JobPlatformEntrypointBindingSweep,
		Every: bindingSweepInterval,
		Run: func(ctx context.Context, _ leader.Term) error {
			removed, err := bindings.DeleteStale(ctx, time.Now().Add(-bindingRetention).UnixMilli())
			if err != nil {
				return fmt.Errorf("delete stale entrypoint identity bindings: %w", err)
			}
			if removed > 0 {
				logger.Debug("reaped stale entrypoint identity bindings", "count", removed)
			}
			return nil
		},
	}
}

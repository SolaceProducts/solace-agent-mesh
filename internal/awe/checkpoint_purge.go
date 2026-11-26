// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package awe

import (
	"context"
	"time"
)

const (
	checkpointPurgeEvery   = time.Hour
	abandonedCheckpointAge = 24 * time.Hour
	checkpointPurgeBatch   = 500
)

func (e *Executor) checkpointPurgeLoop(ctx context.Context) {
	ticker := time.NewTicker(checkpointPurgeEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			e.purgeAbandonedCheckpoints(ctx)
		}
	}
}

func (e *Executor) purgeAbandonedCheckpoints(ctx context.Context) {
	if e.svc == nil || e.svc.Sessions() == nil {
		return
	}
	cutoff := time.Now().Add(-abandonedCheckpointAge)
	total := 0
	for ctx.Err() == nil {
		removed, err := e.svc.Sessions().PurgeAbandonedCheckpoints(ctx, "", cutoff, checkpointPurgeBatch)
		total += removed
		if err != nil {
			e.logger.Warn("failed to remove long-abandoned checkpoints; retrying in an hour", "error", err)
			break
		}
		if removed < checkpointPurgeBatch {
			break
		}
	}
	if total > 0 {
		e.logger.Warn("removed checkpoints no process took over for a day", "checkpoints", total)
	}
}

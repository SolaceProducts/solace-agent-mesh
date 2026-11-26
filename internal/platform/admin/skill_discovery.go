// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/leader"
)

type SkillDiscoveryChecker struct {
	svc            *SkillService
	checkInterval  time.Duration
	pendingTimeout time.Duration
}

func NewSkillDiscoveryChecker(svc *SkillService) *SkillDiscoveryChecker {
	return &SkillDiscoveryChecker{
		svc:            svc,
		checkInterval:  envDuration(envDiscoveryCheckIntervalSec, defaultDiscoveryCheckInterval),
		pendingTimeout: envDuration(envDiscoveryPendingTimeoutSec, defaultDiscoveryPendingTimeout),
	}
}

func (c *SkillDiscoveryChecker) LeaderJob() leader.Job {
	return leader.Job{
		Name:  leader.JobPlatformSkillDiscoveryExpiry,
		Every: c.checkInterval,
		Run: func(ctx context.Context, _ leader.Term) error {
			return c.svc.FailStalePending(ctx, time.Now().Add(-c.pendingTimeout).UnixMilli())
		},
	}
}

// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"os"
	"strconv"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/leader"
)

const (
	defaultDiscoveryCheckInterval  = 60 * time.Second
	defaultDiscoveryPendingTimeout = 2 * time.Minute
	envDiscoveryCheckIntervalSec   = "TOOLSET_DISCOVERY_CHECK_INTERVAL_SEC"
	envDiscoveryPendingTimeoutSec  = "TOOLSET_DISCOVERY_PENDING_TIMEOUT_SEC"
)

type ToolsetDiscoveryChecker struct {
	svc            *ToolsetService
	checkInterval  time.Duration
	pendingTimeout time.Duration
}

func NewToolsetDiscoveryChecker(svc *ToolsetService) *ToolsetDiscoveryChecker {
	return &ToolsetDiscoveryChecker{
		svc:            svc,
		checkInterval:  envDuration(envDiscoveryCheckIntervalSec, defaultDiscoveryCheckInterval),
		pendingTimeout: envDuration(envDiscoveryPendingTimeoutSec, defaultDiscoveryPendingTimeout),
	}
}

func envDuration(name string, fallback time.Duration) time.Duration {
	v := os.Getenv(name)
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return fallback
	}
	return time.Duration(n) * time.Second
}

func (c *ToolsetDiscoveryChecker) LeaderJob() leader.Job {
	return leader.Job{
		Name:  leader.JobPlatformToolsetDiscoveryExpiry,
		Every: c.checkInterval,
		Run: func(ctx context.Context, _ leader.Term) error {
			return c.svc.FailStalePending(ctx, time.Now().Add(-c.pendingTimeout).UnixMilli())
		},
	}
}

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

const testAgentPurgeInterval = 30 * time.Minute

func testAgentPurgeJob(svc *TestAgentService) leader.Job {
	return leader.Job{
		Name:  leader.JobPlatformTestAgentPurge,
		Every: testAgentPurgeInterval,
		Run: func(ctx context.Context, _ leader.Term) error {
			_, err := svc.PurgeExpired(ctx)
			return err
		},
	}
}

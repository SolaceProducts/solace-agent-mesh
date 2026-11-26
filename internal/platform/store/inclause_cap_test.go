// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
)

func TestInClauseCap_PropagatedByPlatformRepos(t *testing.T) {
	db := TestDB(t)
	ctx := context.Background()

	tooMany := make([]string, dbutil.InClauseCap+1)
	for i := range tooMany {
		tooMany[i] = fmt.Sprintf("id-%d", i)
	}

	t.Run("agent_repo_get_deployment_statuses", func(t *testing.T) {
		repo := NewAgentRepo(db)
		_, err := repo.GetDeploymentStatuses(ctx, tooMany)
		if !errors.Is(err, dbutil.ErrTooManyValues) {
			t.Fatalf("err = %v, want errors.Is(err, ErrTooManyValues)", err)
		}
	})

	t.Run("agent_deployment_repo_get_latest_successful", func(t *testing.T) {
		repo := NewAgentDeploymentRepo(db)
		_, err := repo.GetLatestSuccessfulDeploys(ctx, tooMany)
		if !errors.Is(err, dbutil.ErrTooManyValues) {
			t.Fatalf("err = %v, want errors.Is(err, ErrTooManyValues)", err)
		}
	})

	t.Run("connector_repo_get_agent_counts", func(t *testing.T) {
		repo := NewConnectorRepo(db)
		_, err := repo.GetAgentCounts(ctx, tooMany)
		if !errors.Is(err, dbutil.ErrTooManyValues) {
			t.Fatalf("err = %v, want errors.Is(err, ErrTooManyValues)", err)
		}
	})

	t.Run("workflow_repo_get_deployment_statuses", func(t *testing.T) {
		repo := NewWorkflowRepo(db)
		_, err := repo.GetDeploymentStatuses(ctx, tooMany)
		if !errors.Is(err, dbutil.ErrTooManyValues) {
			t.Fatalf("err = %v, want errors.Is(err, ErrTooManyValues)", err)
		}
	})
}

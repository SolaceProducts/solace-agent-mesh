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

func TestInClauseCap_PropagatedByGatewayRepos(t *testing.T) {
	db := TestDB(t)
	ctx := context.Background()

	tooMany := make([]string, dbutil.InClauseCap+1)
	for i := range tooMany {
		tooMany[i] = fmt.Sprintf("id-%d", i)
	}

	t.Run("task_repo_get_by_ids", func(t *testing.T) {
		repo := NewTaskRepo(db)
		_, err := repo.GetByIDs(ctx, tooMany)
		if !errors.Is(err, dbutil.ErrTooManyValues) {
			t.Fatalf("err = %v, want errors.Is(err, ErrTooManyValues)", err)
		}
	})

	t.Run("task_repo_has_active_background_task_batch", func(t *testing.T) {
		repo := NewTaskRepo(db)
		_, err := repo.HasActiveBackgroundTaskBatch(ctx, tooMany)
		if !errors.Is(err, dbutil.ErrTooManyValues) {
			t.Fatalf("err = %v, want errors.Is(err, ErrTooManyValues)", err)
		}
	})

	t.Run("project_repo_get_by_ids", func(t *testing.T) {
		repo := NewProjectRepo(db)
		_, err := repo.GetByIDs(ctx, tooMany)
		if !errors.Is(err, dbutil.ErrTooManyValues) {
			t.Fatalf("err = %v, want errors.Is(err, ErrTooManyValues)", err)
		}
	})

	t.Run("prompt_repo_get_by_ids", func(t *testing.T) {
		repo := NewPromptRepo(db)
		_, err := repo.GetByIDs(ctx, tooMany)
		if !errors.Is(err, dbutil.ErrTooManyValues) {
			t.Fatalf("err = %v, want errors.Is(err, ErrTooManyValues)", err)
		}
	})

	t.Run("chat_task_repo_list_by_session_ids", func(t *testing.T) {
		repo := NewChatTaskRepo(db)
		_, err := repo.ListBySessionIDs(ctx, tooMany)
		if !errors.Is(err, dbutil.ErrTooManyValues) {
			t.Fatalf("err = %v, want errors.Is(err, ErrTooManyValues)", err)
		}
	})

	t.Run("shared_link_user_repo_batch_remove", func(t *testing.T) {
		repo := NewSharedLinkUserRepo(db)
		_, err := repo.BatchRemove(ctx, "share-1", tooMany)
		if !errors.Is(err, dbutil.ErrTooManyValues) {
			t.Fatalf("err = %v, want errors.Is(err, ErrTooManyValues)", err)
		}
	})

	t.Run("feedback_repo_search_cross_user_agent_reach", func(t *testing.T) {
		repo := NewFeedbackRepo(db)
		vis := FeedbackVisibility{Agents: SubjectScope{IDs: tooMany}}
		_, _, err := repo.SearchCrossUser(ctx, vis, CrossUserFeedbackFilter{}, 50, 0)
		if !errors.Is(err, dbutil.ErrTooManyValues) {
			t.Fatalf("err = %v, want errors.Is(err, ErrTooManyValues)", err)
		}
	})

	t.Run("feedback_repo_search_cross_user_workflow_reach", func(t *testing.T) {
		repo := NewFeedbackRepo(db)
		vis := FeedbackVisibility{Workflows: SubjectScope{IDs: tooMany}}
		_, _, err := repo.SearchCrossUser(ctx, vis, CrossUserFeedbackFilter{}, 50, 0)
		if !errors.Is(err, dbutil.ErrTooManyValues) {
			t.Fatalf("err = %v, want errors.Is(err, ErrTooManyValues)", err)
		}
	})

	t.Run("feedback_repo_search_cross_user_agent_filter", func(t *testing.T) {
		repo := NewFeedbackRepo(db)
		vis := FeedbackVisibility{Agents: SubjectScope{All: true}, Workflows: SubjectScope{All: true}}
		_, _, err := repo.SearchCrossUser(ctx, vis, CrossUserFeedbackFilter{AgentIDs: tooMany}, 50, 0)
		if !errors.Is(err, dbutil.ErrTooManyValues) {
			t.Fatalf("err = %v, want errors.Is(err, ErrTooManyValues)", err)
		}
	})

	t.Run("feedback_repo_search_cross_user_workflow_filter", func(t *testing.T) {
		repo := NewFeedbackRepo(db)
		vis := FeedbackVisibility{Agents: SubjectScope{All: true}, Workflows: SubjectScope{All: true}}
		_, _, err := repo.SearchCrossUser(ctx, vis, CrossUserFeedbackFilter{WorkflowIDs: tooMany}, 50, 0)
		if !errors.Is(err, dbutil.ErrTooManyValues) {
			t.Fatalf("err = %v, want errors.Is(err, ErrTooManyValues)", err)
		}
	})
}

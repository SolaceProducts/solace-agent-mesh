// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"context"
	"errors"
	"fmt"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
)

type FeedbackSubject struct {
	ID   string
	Type string
}

var (
	ErrFeedbackTaskNotFound     = errors.New("feedback: task not found")
	ErrFeedbackTaskUnclassified = errors.New("feedback: task has no classified handler to attribute")
)

func DeriveFeedbackSubject(task *store.Task) (FeedbackSubject, bool) {
	if task == nil || task.HandlerName == nil || *task.HandlerName == "" || task.HandlerType == nil {
		return FeedbackSubject{}, false
	}
	id := *task.HandlerName
	if instanceID, _, isInstance := a2a.IDFromInstanceName(id); isInstance {
		id = instanceID
	}
	switch *task.HandlerType {
	case store.TaskHandlerTypeAgent:
		return FeedbackSubject{ID: id, Type: store.SubjectTypeAgent}, true
	case store.TaskHandlerTypeWorkflow:
		return FeedbackSubject{ID: id, Type: store.SubjectTypeWorkflow}, true
	default:
		return FeedbackSubject{}, false
	}
}

func FeedbackSubjectForTask(ctx context.Context, tasks *store.TaskRepo, taskID string) (FeedbackSubject, *store.Task, error) {
	task, err := tasks.GetByID(ctx, taskID)
	if err != nil {
		return FeedbackSubject{}, nil, fmt.Errorf("feedback task lookup: %w", err)
	}
	if task == nil {
		return FeedbackSubject{}, nil, ErrFeedbackTaskNotFound
	}
	subject, ok := DeriveFeedbackSubject(task)
	if !ok {
		return FeedbackSubject{}, task, ErrFeedbackTaskUnclassified
	}
	return subject, task, nil
}

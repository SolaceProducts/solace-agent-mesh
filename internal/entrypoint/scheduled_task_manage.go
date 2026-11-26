// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
)

var ErrTaskNameConflict = errors.New("a scheduled task with this name already exists")

func (s *ScheduledTaskService) ListTasks(ctx context.Context, userID, ownership string, enabledOnly bool, limit, offset int) ([]*store.ScheduledTask, int, error) {
	namespace := s.core.Config().Namespace
	if ownership == "mine" {
		tasks, err := s.repo.FindByNamespace(ctx, namespace, &userID, enabledOnly, limit, offset)
		if err != nil {
			return nil, 0, err
		}
		total, err := s.repo.CountByNamespace(ctx, namespace, &userID, enabledOnly)
		if err != nil {
			return nil, 0, err
		}
		return tasks, total, nil
	}
	tasks, err := s.repo.FindByNamespaceWithShared(ctx, namespace, userID, enabledOnly, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	total, err := s.repo.CountByNamespaceWithShared(ctx, namespace, userID, enabledOnly)
	if err != nil {
		return nil, 0, err
	}
	return tasks, total, nil
}

func (s *ScheduledTaskService) ApplyUpdate(ctx context.Context, taskID string, fields map[string]any, scheduleChanged bool) (*store.ScheduledTask, error) {
	current, err := s.repo.GetByID(ctx, taskID)
	if err != nil {
		return nil, fmt.Errorf("load scheduled task before update: %w", err)
	}
	if current != nil {
		after := withScheduleFields(*current, fields)
		enabling := after.Enabled && !current.Enabled
		if enabling {
			fields["consecutive_failure_count"] = 0
		}
		if after.Enabled && (scheduleChanged || enabling) {
			next, nextErr := FirstRunAt(&after, time.Now(), false)
			if nextErr != nil {
				return nil, nextErr
			}
			fields["next_run_at"] = next
		}
	}
	if err = s.repo.Update(ctx, taskID, fields); err != nil {
		if dbutil.IsUniqueViolation(err) {
			return nil, ErrTaskNameConflict
		}
		return nil, fmt.Errorf("update scheduled task: %w", err)
	}

	updated, err := s.repo.GetByID(ctx, taskID)
	if err != nil {
		return nil, fmt.Errorf("reload scheduled task after update: %w", err)
	}
	if updated == nil {
		return nil, fmt.Errorf("scheduled task %q disappeared after update", taskID)
	}
	return updated, nil
}

func withScheduleFields(task store.ScheduledTask, fields map[string]any) store.ScheduledTask {
	if v, ok := fields["schedule_type"].(string); ok {
		task.ScheduleType = v
	}
	if v, ok := fields["schedule_expression"].(string); ok {
		task.ScheduleExpression = v
	}
	if v, ok := fields["timezone"].(string); ok {
		task.Timezone = v
	}
	if v, ok := fields["enabled"].(int); ok {
		task.Enabled = v == dbutil.BoolToInt(true)
	}
	return task
}

func (s *ScheduledTaskService) DeleteTask(ctx context.Context, taskID, deletedBy string) error {
	if err := s.repo.SoftDelete(ctx, taskID, deletedBy); err != nil {
		return fmt.Errorf("soft delete scheduled task: %w", err)
	}
	return nil
}

func (s *ScheduledTaskService) EnableTask(ctx context.Context, taskID string) (*store.ScheduledTask, error) {
	fields := map[string]any{
		"enabled":                   dbutil.BoolToInt(true),
		"consecutive_failure_count": 0,
	}
	current, err := s.repo.GetByID(ctx, taskID)
	if err != nil {
		return nil, fmt.Errorf("load scheduled task before enable: %w", err)
	}
	if current != nil {
		current.Enabled = true
		next, nextErr := FirstRunAt(current, time.Now(), false)
		if nextErr != nil {
			return nil, nextErr
		}
		fields["next_run_at"] = next
	}
	if err = s.repo.Update(ctx, taskID, fields); err != nil {
		return nil, fmt.Errorf("enable scheduled task: %w", err)
	}
	task, err := s.repo.GetByID(ctx, taskID)
	if err != nil {
		return nil, fmt.Errorf("reload scheduled task after enable: %w", err)
	}
	if task == nil {
		return nil, fmt.Errorf("scheduled task %q disappeared after enable", taskID)
	}
	return task, nil
}

func (s *ScheduledTaskService) DisableTask(ctx context.Context, taskID string) error {
	if err := s.repo.SetEnabled(ctx, taskID, false); err != nil {
		return fmt.Errorf("disable scheduled task: %w", err)
	}
	return nil
}

// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package session

import (
	"context"
	"time"
)

type memRunningTask struct {
	RunningTask
	ownerID string
	until   time.Time
}

func (s *MemoryStore) RegisterRunningTask(_ context.Context, task RunningTask, ownerID string, until time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.runningTasks[task.TaskID] = memRunningTask{RunningTask: task, ownerID: ownerID, until: until}
	return nil
}

func (s *MemoryStore) RenewRunningTasks(_ context.Context, ownerID string, tasks []RunningTask, until time.Time) (int, error) {
	if ownerID == "" {
		return 0, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	renewed := 0
	for _, t := range tasks {
		row, ok := s.runningTasks[t.TaskID]
		if ok && row.ownerID != ownerID {
			continue
		}
		if !ok {
			row = memRunningTask{RunningTask: t, ownerID: ownerID}
		}
		row.until = until
		s.runningTasks[t.TaskID] = row
		renewed++
	}
	return renewed, nil
}

func (s *MemoryStore) DeleteLapsedRunningTasks(_ context.Context, appName string, lapsedBefore time.Time) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	removed := 0
	for id, row := range s.runningTasks {
		if row.AppName == appName && row.until.Before(lapsedBefore) {
			delete(s.runningTasks, id)
			removed++
		}
	}
	return removed, nil
}

func (s *MemoryStore) CheckpointedTasks(_ context.Context, sessionID string, taskIDs []string) ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var found []string
	for _, id := range taskIDs {
		if cp, ok := s.checkpoints[id]; ok && cp.SessionID == sessionID {
			found = append(found, id)
		}
	}
	return found, nil
}

func (s *MemoryStore) deleteSessionRunningTasksLocked(appName, userID, sessionID string) {
	for id, row := range s.runningTasks {
		if row.AppName == appName && row.UserID == userID && row.SessionID == sessionID {
			delete(s.runningTasks, id)
		}
	}
}

func (s *MemoryStore) RemoveRunningTask(_ context.Context, taskID, ownerID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if row, ok := s.runningTasks[taskID]; ok && row.ownerID == ownerID {
		delete(s.runningTasks, taskID)
	}
	return nil
}

func (s *MemoryStore) ListRunningTasks(_ context.Context, appName, userID, sessionID string, liveAfter time.Time) ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var taskIDs []string
	for id, row := range s.runningTasks {
		if row.AppName == appName && row.UserID == userID && row.SessionID == sessionID && row.until.After(liveAfter) {
			taskIDs = append(taskIDs, id)
		}
	}
	return taskIDs, nil
}

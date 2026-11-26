// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package proxy

import (
	"sync"
	"time"
)

type TaskStore struct {
	mu           sync.Mutex
	tasks        map[string]*TaskContext
	byDownstream map[string]*TaskContext
}

func NewTaskStore() *TaskStore {
	return &TaskStore{
		tasks:        make(map[string]*TaskContext),
		byDownstream: make(map[string]*TaskContext),
	}
}

func (s *TaskStore) Add(tc *TaskContext) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tasks[tc.TaskID] = tc
	if tc.DownstreamTaskID != "" {
		s.byDownstream[tc.DownstreamTaskID] = tc
	}
}

func (s *TaskStore) Get(samTaskID string) *TaskContext {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.tasks[samTaskID]
}

func (s *TaskStore) GetByDownstreamID(dsID string) *TaskContext {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.byDownstream[dsID]
}

func (s *TaskStore) IndexDownstreamID(samTaskID, dsID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if tc, ok := s.tasks[samTaskID]; ok {
		s.byDownstream[dsID] = tc
	}
}

func (s *TaskStore) Remove(samTaskID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if tc, ok := s.tasks[samTaskID]; ok {
		if tc.DownstreamTaskID != "" {
			delete(s.byDownstream, tc.DownstreamTaskID)
		}
		delete(s.tasks, samTaskID)
	}
}

func (s *TaskStore) CleanupStale(ttl time.Duration) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	cutoff := time.Now().Add(-ttl)
	removed := 0
	for id, tc := range s.tasks {
		if tc.CreatedAt.Before(cutoff) {
			if tc.DownstreamTaskID != "" {
				delete(s.byDownstream, tc.DownstreamTaskID)
			}
			delete(s.tasks, id)
			removed++
		}
	}
	return removed
}

func (s *TaskStore) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.tasks)
}

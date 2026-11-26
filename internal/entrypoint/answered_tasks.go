// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"sync"
	"time"
)

const answeredTaskMemory = time.Duration(defaultTaskHardTimeoutSeconds) * time.Second

type answeredTask struct {
	id string
	at time.Time
}

type answeredTasks struct {
	memory time.Duration
	mu     sync.Mutex
	ids    map[string]struct{}
	order  []answeredTask
}

func newAnsweredTasks(memory time.Duration) *answeredTasks {
	return &answeredTasks{memory: memory, ids: make(map[string]struct{})}
}

func (a *answeredTasks) setMemory(memory time.Duration) {
	if memory <= 0 {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.memory = memory
}

func (a *answeredTasks) add(taskID string) {
	now := time.Now()
	a.mu.Lock()
	defer a.mu.Unlock()
	a.forgetExpiredLocked(now)
	if _, ok := a.ids[taskID]; ok {
		return
	}
	a.ids[taskID] = struct{}{}
	a.order = append(a.order, answeredTask{id: taskID, at: now})
}

func (a *answeredTasks) has(taskID string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.forgetExpiredLocked(time.Now())
	_, ok := a.ids[taskID]
	return ok
}

func (a *answeredTasks) forgetExpiredLocked(now time.Time) {
	expired := 0
	for expired < len(a.order) && now.Sub(a.order[expired].at) >= a.memory {
		delete(a.ids, a.order[expired].id)
		expired++
	}
	a.order = a.order[expired:]
}

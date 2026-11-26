// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eventlog

import "sync"

const notifyShardCount = 64

type notifyRegistry struct {
	shards [notifyShardCount]notifyShard
}

type notifyShard struct {
	mu      sync.Mutex
	waiters map[string]map[chan struct{}]struct{}
}

func newNotifyRegistry() *notifyRegistry {
	r := &notifyRegistry{}
	for i := range r.shards {
		r.shards[i].waiters = make(map[string]map[chan struct{}]struct{})
	}
	return r
}

func (r *notifyRegistry) shardFor(taskID string) *notifyShard {
	return &r.shards[hashTaskID(taskID)%notifyShardCount]
}

func (r *notifyRegistry) register(taskID string) chan struct{} {
	ch := make(chan struct{}, 1)
	sh := r.shardFor(taskID)
	sh.mu.Lock()
	set := sh.waiters[taskID]
	if set == nil {
		set = make(map[chan struct{}]struct{})
		sh.waiters[taskID] = set
	}
	set[ch] = struct{}{}
	sh.mu.Unlock()
	return ch
}

func (r *notifyRegistry) unregister(taskID string, ch chan struct{}) {
	sh := r.shardFor(taskID)
	sh.mu.Lock()
	if set, ok := sh.waiters[taskID]; ok {
		delete(set, ch)
		if len(set) == 0 {
			delete(sh.waiters, taskID)
		}
	}
	sh.mu.Unlock()
}

func (r *notifyRegistry) signal(taskID string) {
	sh := r.shardFor(taskID)
	sh.mu.Lock()
	for ch := range sh.waiters[taskID] {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
	sh.mu.Unlock()
}

func hashTaskID(s string) uint64 {
	const (
		offset = uint64(1469598103934665603)
		prime  = uint64(1099511628211)
	)
	h := offset
	for i := range len(s) {
		h ^= uint64(s[i])
		h *= prime
	}
	return h
}

func (r *notifyRegistry) hasWaiters(taskID string) bool {
	sh := r.shardFor(taskID)
	sh.mu.Lock()
	defer sh.mu.Unlock()
	return len(sh.waiters[taskID]) > 0
}

func (r *notifyRegistry) tasks() []string {
	var out []string
	for i := range r.shards {
		sh := &r.shards[i]
		sh.mu.Lock()
		for taskID := range sh.waiters {
			out = append(out, taskID)
		}
		sh.mu.Unlock()
	}
	return out
}

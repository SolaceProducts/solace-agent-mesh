// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package governor

import "sync"

type recentIDs struct {
	mu   sync.Mutex
	ring []string
	next int
	set  map[string]struct{}
}

func newRecentIDs(capacity int) *recentIDs {
	return &recentIDs{ring: make([]string, capacity), set: make(map[string]struct{}, capacity)}
}

func (r *recentIDs) add(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.set[id]; ok {
		return
	}
	delete(r.set, r.ring[r.next])
	r.ring[r.next] = id
	r.set[id] = struct{}{}
	r.next = (r.next + 1) % len(r.ring)
}

func (r *recentIDs) contains(id string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.set[id]
	return ok
}

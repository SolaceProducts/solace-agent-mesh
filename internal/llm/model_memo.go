// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package llm

import "sync"

type modelMemo struct {
	mu   sync.Mutex
	keys map[string]struct{}
}

func (m *modelMemo) has(key string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.keys[key]
	return ok
}

func (m *modelMemo) add(key string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.keys[key]; ok {
		return false
	}
	if m.keys == nil {
		m.keys = make(map[string]struct{}, 1)
	}
	m.keys[key] = struct{}{}
	return true
}

func (m *modelMemo) remove(key string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.keys, key)
}

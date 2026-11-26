// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package mcpauth

func mem(h *Handler) *memoryStore { return h.store.(*memoryStore) }

func (m *memoryStore) statesLen() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.states)
}

func (m *memoryStore) codesLen() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.codes)
}

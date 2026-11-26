// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package runtime

import "sync"

type MapKVStore struct {
	mu   sync.RWMutex
	data map[string]any
}

func NewMapKVStore() *MapKVStore {
	return &MapKVStore{data: make(map[string]any)}
}

func (s *MapKVStore) Get(key string) (any, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.data[key]
	return v, ok
}

func (s *MapKVStore) Set(key string, value any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data[key] = value
}

func (s *MapKVStore) Delete(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.data, key)
}

var _ KVStore = (*MapKVStore)(nil)

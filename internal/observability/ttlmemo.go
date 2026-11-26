// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package observability

import (
	"sync"
	"time"
)

type TTLMemo[T any] struct {
	ttl     time.Duration
	compute func() (T, error)

	now func() time.Time

	mu     sync.Mutex
	cached T
	last   time.Time
	primed bool
}

func NewTTLMemo[T any](ttl time.Duration, compute func() (T, error)) *TTLMemo[T] {
	return &TTLMemo[T]{
		ttl:     ttl,
		compute: compute,
		now:     time.Now,
	}
}

func (m *TTLMemo[T]) Value() (T, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := m.now()
	if m.primed && m.ttl > 0 && now.Sub(m.last) < m.ttl {
		return m.cached, nil
	}

	v, err := m.compute()
	if err != nil {
		return m.cached, err
	}
	m.cached = v
	m.last = now
	m.primed = true
	return v, nil
}

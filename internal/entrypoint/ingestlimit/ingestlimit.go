// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package ingestlimit

import (
	"context"
	"errors"
	"sync"

	"golang.org/x/sync/semaphore"
)

const InboundCapacity = 125 << 20

var inbound = New(InboundCapacity)

func Inbound() *Budget { return inbound }

var ErrOverCapacity = errors.New("ingestlimit: reservation exceeds the budget's capacity")

type Budget struct {
	capacity int64
	sem      *semaphore.Weighted
}

func New(capacity int64) *Budget {
	return &Budget{capacity: capacity, sem: semaphore.NewWeighted(capacity)}
}

func (b *Budget) Capacity() int64 { return b.capacity }

func (b *Budget) Acquire(ctx context.Context, n int64) (*Hold, error) {
	n = max(n, 0)
	if n > b.capacity {
		return nil, ErrOverCapacity
	}
	if err := b.sem.Acquire(ctx, n); err != nil {
		return nil, err
	}
	return &Hold{budget: b, held: n}, nil
}

type Hold struct {
	budget *Budget

	mu   sync.Mutex
	held int64
}

func (h *Hold) Shrink(n int64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	n = max(n, 0)
	if n >= h.held {
		return
	}
	h.budget.sem.Release(h.held - n)
	h.held = n
}

func (h *Hold) Release() { h.Shrink(0) }

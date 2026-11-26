// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package whatsapp

import (
	"context"
	"sync"
)

const maxConcurrentMediaTransfers = 2

type mediaSemaphore struct {
	once  sync.Once
	slots chan struct{}
}

func (s *mediaSemaphore) channel() chan struct{} {
	s.once.Do(func() { s.slots = make(chan struct{}, maxConcurrentMediaTransfers) })
	return s.slots
}

func (s *mediaSemaphore) acquire(ctx context.Context) bool {
	select {
	case s.channel() <- struct{}{}:
		return true
	case <-ctx.Done():
		return false
	}
}

func (s *mediaSemaphore) release() {
	select {
	case <-s.channel():
	default:
	}
}

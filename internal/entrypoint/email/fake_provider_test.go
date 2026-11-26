// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package email

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"
)

type FakeProvider struct {
	mu sync.Mutex

	pending    []RawMessage
	processed  map[ProviderUID]struct{}
	connected  bool
	lastAuthAt time.Time

	IdleSupported bool
	IdleEvents    chan IdleEvent
	IdleDone      chan struct{}
	idleStopped   chan struct{}
	idleMu        sync.Mutex
	idleActive    bool

	IdleStartError error

	StopIdleError error

	StopIdleCount atomic.Int32

	MarkProcessedDuringIdle atomic.Int32

	MarkProcessedBlock chan struct{}

	MarkProcessedError error

	MarkProcessedErrorRemaining int

	ConnectErrorRemaining int
	ConnectError          error
}

func NewFakeProvider(msgs []RawMessage) *FakeProvider {
	pending := make([]RawMessage, len(msgs))
	copy(pending, msgs)
	return &FakeProvider{
		pending:   pending,
		processed: make(map[ProviderUID]struct{}),
	}
}

func (f *FakeProvider) Connect(_ context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ConnectErrorRemaining > 0 {
		f.ConnectErrorRemaining--
		err := f.ConnectError
		if err == nil {
			err = errors.New("fake: simulated connect failure")
		}
		return err
	}
	f.connected = true
	f.lastAuthAt = time.Now()
	return nil
}

func (f *FakeProvider) Disconnect(_ context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.connected = false
	return nil
}

func (f *FakeProvider) IsConnected() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.connected
}

func (f *FakeProvider) LastSuccessfulAuthAt() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lastAuthAt
}

func (f *FakeProvider) FetchNew(_ context.Context, limit int) ([]RawMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if limit <= 0 {
		return nil, nil
	}
	out := make([]RawMessage, 0, min(limit, len(f.pending)))
	for _, m := range f.pending {
		if _, done := f.processed[m.UID]; done {
			continue
		}
		out = append(out, m)
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (f *FakeProvider) MarkProcessed(_ context.Context, uid ProviderUID) error {
	f.idleMu.Lock()
	idleActive := f.idleActive
	f.idleMu.Unlock()
	if idleActive {
		f.MarkProcessedDuringIdle.Add(1)
	}

	if f.MarkProcessedBlock != nil {
		<-f.MarkProcessedBlock
	}

	if f.MarkProcessedError != nil {
		return f.MarkProcessedError
	}

	f.mu.Lock()
	if f.MarkProcessedErrorRemaining > 0 {
		f.MarkProcessedErrorRemaining--
		f.mu.Unlock()
		return errors.New("fake: simulated STORE failure")
	}
	defer f.mu.Unlock()
	f.processed[uid] = struct{}{}
	kept := make([]RawMessage, 0, len(f.pending))
	for _, m := range f.pending {
		if m.UID != uid {
			kept = append(kept, m)
		}
	}
	f.pending = kept
	return nil
}

func (f *FakeProvider) WasProcessed(uid ProviderUID) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.processed[uid]
	return ok
}

func (f *FakeProvider) SupportsIdle() bool {
	f.mu.Lock()
	connected := f.connected
	f.mu.Unlock()
	return f.IdleSupported && connected
}

func (f *FakeProvider) SimulateSessionDrop() {
	f.mu.Lock()
	f.connected = false
	f.mu.Unlock()

	f.idleMu.Lock()
	defer f.idleMu.Unlock()
	if f.IdleDone != nil {
		close(f.IdleDone)
		f.IdleDone = nil
	}
	f.IdleEvents = nil
	f.idleActive = false
}

func (f *FakeProvider) Idle(_ context.Context) (<-chan IdleEvent, <-chan struct{}, error) {
	f.idleMu.Lock()
	defer f.idleMu.Unlock()
	if f.IdleStartError != nil {
		return nil, nil, f.IdleStartError
	}
	if f.idleActive {
		return nil, nil, errors.New("fake: idle already running")
	}
	f.idleActive = true
	f.idleStopped = make(chan struct{})
	if f.IdleEvents == nil {
		f.IdleEvents = make(chan IdleEvent, 8)
	}
	if f.IdleDone == nil {
		f.IdleDone = make(chan struct{})
	}
	return f.IdleEvents, f.IdleDone, nil
}

func (f *FakeProvider) StopIdle(_ context.Context) error {
	f.idleMu.Lock()
	defer f.idleMu.Unlock()
	if f.StopIdleError != nil {
		return f.StopIdleError
	}
	if !f.idleActive {
		return nil
	}
	f.idleActive = false
	f.StopIdleCount.Add(1)
	close(f.idleStopped)
	return nil
}

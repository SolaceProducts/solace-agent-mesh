// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"sync"
	"time"
)

const lastUsedWriteThrottle = 5 * time.Minute

const lastUsedSweepInterval = time.Hour

const lastUsedRetryAfter = 30 * time.Second

const lastUsedStaleAge = 24 * time.Hour

type lastUsedThrottle struct {
	interval time.Duration
	staleAge time.Duration

	mu        sync.Mutex
	lastWrite map[string]int64
	retryAt   map[string]int64
	lastSweep int64
}

func newLastUsedThrottle(interval, staleAge time.Duration) *lastUsedThrottle {
	return &lastUsedThrottle{
		interval:  interval,
		staleAge:  staleAge,
		lastWrite: make(map[string]int64),
		retryAt:   make(map[string]int64),
	}
}

func (t *lastUsedThrottle) due(keyID string, nowMS int64) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.sweepLocked(nowMS)
	if retry, failed := t.retryAt[keyID]; failed {
		if nowMS < retry && retry-nowMS <= lastUsedRetryAfter.Milliseconds() {
			return false
		}
		delete(t.retryAt, keyID)
	}
	last, seen := t.lastWrite[keyID]
	return !seen || nowMS < last || nowMS-last >= t.interval.Milliseconds()
}

func (t *lastUsedThrottle) record(keyID string, nowMS int64) {
	t.mu.Lock()
	t.lastWrite[keyID] = nowMS
	delete(t.retryAt, keyID)
	t.mu.Unlock()
}

func (t *lastUsedThrottle) fail(keyID string, nowMS int64) {
	t.mu.Lock()
	t.retryAt[keyID] = nowMS + lastUsedRetryAfter.Milliseconds()
	t.mu.Unlock()
}

func (t *lastUsedThrottle) sweepLocked(nowMS int64) {
	if nowMS >= t.lastSweep && nowMS-t.lastSweep < lastUsedSweepInterval.Milliseconds() {
		return
	}
	t.lastSweep = nowMS
	cutoff := t.staleAge.Milliseconds()
	for id, written := range t.lastWrite {
		if nowMS-written >= cutoff {
			delete(t.lastWrite, id)
		}
	}
	for id, retry := range t.retryAt {
		if nowMS >= retry {
			delete(t.retryAt, id)
		}
	}
}

type lastUsedToucher interface {
	TouchLastUsed(ctx context.Context, id string, at int64) error
}

func (a *APIKeyAuthority) stampLastUsed(ctx context.Context, keyID string) {
	nowMS := a.now().UnixMilli()
	if !a.lastUsed.due(keyID, nowMS) {
		return
	}
	if err := a.stamps.TouchLastUsed(ctx, keyID, nowMS); err != nil {
		a.lastUsed.fail(keyID, nowMS)
		a.logger.Warn("api key last-used stamp failed; retrying after a delay",
			"credentialID", keyID, "retryAfterSeconds", int(lastUsedRetryAfter.Seconds()), "error", err)
		return
	}
	a.lastUsed.record(keyID, nowMS)
}

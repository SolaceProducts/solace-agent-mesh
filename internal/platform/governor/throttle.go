// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package governor

import (
	"sync"
	"time"
)

const maxThrottledKeys = 256

const overflowKey = "\x00overflow"

type logThrottle struct {
	interval time.Duration

	mu         sync.Mutex
	last       map[string]time.Time
	suppressed map[string]int
}

func newLogThrottle(interval time.Duration) *logThrottle {
	return &logThrottle{interval: interval, last: make(map[string]time.Time), suppressed: make(map[string]int)}
}

func (t *logThrottle) allow(key string, now time.Time) bool {
	ok, _ := t.admit(key, now)
	return ok
}

func (t *logThrottle) admit(key string, now time.Time) (ok bool, suppressed int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, seen := t.last[key]; !seen && len(t.last) >= maxThrottledKeys {
		t.forgetLoggedAtOrBefore(now.Add(-t.interval))
		if len(t.last) >= maxThrottledKeys {
			key = overflowKey
		}
	}
	if last, seen := t.last[key]; seen && now.Sub(last) < t.interval {
		t.suppressed[key]++
		return false, 0
	}
	t.last[key] = now
	suppressed = t.suppressed[key]
	delete(t.suppressed, key)
	return true, suppressed
}

func (t *logThrottle) forgetLoggedAtOrBefore(cutoff time.Time) {
	for key, last := range t.last {
		if !last.After(cutoff) {
			delete(t.last, key)
			delete(t.suppressed, key)
		}
	}
}

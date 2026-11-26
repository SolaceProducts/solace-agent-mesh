// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package spend

import (
	"sync"
	"time"
)

type WarnLimiter struct {
	interval time.Duration
	now      func() time.Time

	mu   sync.Mutex
	keys map[string]*limitedWarning
}

type limitedWarning struct {
	warnedAt   time.Time
	suppressed int
}

func NewWarnLimiter(interval time.Duration, now func() time.Time) *WarnLimiter {
	return &WarnLimiter{interval: interval, now: now, keys: make(map[string]*limitedWarning)}
}

func (l *WarnLimiter) Admit(key string) (suppressed int, warn bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	w, seen := l.keys[key]
	if !seen {
		l.keys[key] = &limitedWarning{warnedAt: now}
		return 0, true
	}
	if now.Sub(w.warnedAt) < l.interval {
		w.suppressed++
		return 0, false
	}
	suppressed = w.suppressed
	*w = limitedWarning{warnedAt: now}
	return suppressed, true
}

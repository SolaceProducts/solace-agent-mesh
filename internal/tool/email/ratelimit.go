// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package email

import (
	"sync"
	"time"
)

type RateLimiter struct {
	mu           sync.Mutex
	limits       RateLimitConfig
	minuteWindow *slidingWindow
	hourWindow   *slidingWindow
}

func NewRateLimiter(limits RateLimitConfig) *RateLimiter {
	return &RateLimiter{
		limits:       limits,
		minuteWindow: newSlidingWindow(time.Minute),
		hourWindow:   newSlidingWindow(time.Hour),
	}
}

func (r *RateLimiter) Allow() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	now := time.Now()

	if r.limits.PerMinute > 0 {
		count := r.minuteWindow.count(now)
		if count >= r.limits.PerMinute {
			return &ThrottleError{
				Limit:      r.limits.PerMinute,
				Window:     "minute",
				RetryAfter: r.minuteWindow.timeUntilReset(now),
			}
		}
	}

	if r.limits.PerHour > 0 {
		count := r.hourWindow.count(now)
		if count >= r.limits.PerHour {
			return &ThrottleError{
				Limit:      r.limits.PerHour,
				Window:     "hour",
				RetryAfter: r.hourWindow.timeUntilReset(now),
			}
		}
	}

	r.minuteWindow.record(now)
	r.hourWindow.record(now)

	return nil
}

func (r *RateLimiter) Reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.minuteWindow = newSlidingWindow(time.Minute)
	r.hourWindow = newSlidingWindow(time.Hour)
}

type slidingWindow struct {
	windowSize time.Duration
	timestamps []time.Time
}

func newSlidingWindow(windowSize time.Duration) *slidingWindow {
	return &slidingWindow{
		windowSize: windowSize,
		timestamps: make([]time.Time, 0),
	}
}

func (w *slidingWindow) count(now time.Time) int {
	w.prune(now)
	return len(w.timestamps)
}

func (w *slidingWindow) record(now time.Time) {
	w.prune(now)
	w.timestamps = append(w.timestamps, now)
}

func (w *slidingWindow) prune(now time.Time) {
	cutoff := now.Add(-w.windowSize)
	newStart := 0
	for i, ts := range w.timestamps {
		if ts.After(cutoff) {
			newStart = i
			break
		}
		newStart = i + 1
	}
	if newStart > 0 {
		w.timestamps = w.timestamps[newStart:]
	}
}

func (w *slidingWindow) timeUntilReset(now time.Time) time.Duration {
	if len(w.timestamps) == 0 {
		return 0
	}
	oldest := w.timestamps[0]
	expiry := oldest.Add(w.windowSize)
	if expiry.Before(now) {
		return 0
	}
	return expiry.Sub(now)
}

// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package email

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRateLimiter_Allow(t *testing.T) {
	t.Run("allows within limits", func(t *testing.T) {
		rl := NewRateLimiter(RateLimitConfig{PerMinute: 5, PerHour: 100})

		for i := range 5 {
			err := rl.Allow()
			assert.NoError(t, err, "request %d should be allowed", i+1)
		}
	})

	t.Run("rejects when minute limit exceeded", func(t *testing.T) {
		rl := NewRateLimiter(RateLimitConfig{PerMinute: 3, PerHour: 100})

		for range 3 {
			err := rl.Allow()
			require.NoError(t, err)
		}

		err := rl.Allow()
		require.Error(t, err)
		var throttleErr *ThrottleError
		require.ErrorAs(t, err, &throttleErr)
		assert.Equal(t, "minute", throttleErr.Window)
		assert.Equal(t, 3, throttleErr.Limit)
	})

	t.Run("rejects when hour limit exceeded", func(t *testing.T) {
		rl := NewRateLimiter(RateLimitConfig{PerMinute: 100, PerHour: 3})

		for range 3 {
			err := rl.Allow()
			require.NoError(t, err)
		}

		err := rl.Allow()
		require.Error(t, err)
		var throttleErr *ThrottleError
		require.ErrorAs(t, err, &throttleErr)
		assert.Equal(t, "hour", throttleErr.Window)
		assert.Equal(t, 3, throttleErr.Limit)
	})

	t.Run("no limits when zero", func(t *testing.T) {
		rl := NewRateLimiter(RateLimitConfig{PerMinute: 0, PerHour: 0})

		for range 100 {
			err := rl.Allow()
			assert.NoError(t, err)
		}
	})
}

func TestRateLimiter_Reset(t *testing.T) {
	rl := NewRateLimiter(RateLimitConfig{PerMinute: 2, PerHour: 100})

	_ = rl.Allow()
	_ = rl.Allow()

	err := rl.Allow()
	require.Error(t, err)

	rl.Reset()

	err = rl.Allow()
	assert.NoError(t, err)
}

func TestSlidingWindow_Pruning(t *testing.T) {
	w := newSlidingWindow(100 * time.Millisecond)

	now := time.Now()

	w.record(now)
	assert.Equal(t, 1, w.count(now))

	time.Sleep(150 * time.Millisecond)

	later := time.Now()
	assert.Equal(t, 0, w.count(later))
}

func TestSlidingWindow_TimeUntilReset(t *testing.T) {
	w := newSlidingWindow(time.Second)

	now := time.Now()
	w.record(now)

	dur := w.timeUntilReset(now)
	assert.InDelta(t, float64(time.Second), float64(dur), float64(50*time.Millisecond))

	assert.Equal(t, time.Duration(0), w.timeUntilReset(now.Add(2*time.Second)))
}

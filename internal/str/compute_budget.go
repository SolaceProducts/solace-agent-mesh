// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import (
	"bufio"
	"bytes"
	"context"
	"strconv"
	"strings"
	"sync"
	"time"
)

type computeBudget struct {
	mu      sync.Mutex
	cond    *sync.Cond
	limit   int
	held    int
	waiting int
}

func newComputeBudget(limit int) *computeBudget {
	b := &computeBudget{limit: max(limit, 1)}
	b.cond = sync.NewCond(&b.mu)
	return b
}

func (b *computeBudget) acquire(ctx context.Context) error {
	stop := context.AfterFunc(ctx, func() {
		b.mu.Lock()
		b.cond.Broadcast()
		b.mu.Unlock()
	})
	defer stop()

	b.mu.Lock()
	defer b.mu.Unlock()
	for b.held >= b.limit {
		if err := ctx.Err(); err != nil {
			return err
		}
		b.waiting++
		b.cond.Wait()
		b.waiting--
	}
	b.held++
	return nil
}

func (b *computeBudget) release() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.held > 0 {
		b.held--
	}
	b.cond.Signal()
}

func (b *computeBudget) setLimit(n int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.limit = max(n, 1)
	b.cond.Broadcast()
}

func (b *computeBudget) snapshot() (limit, held, waiting int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.limit, b.held, b.waiting
}

type cpuThrottleSample struct {
	periods   uint64
	throttled uint64
}

func parseCPUStat(contents []byte) (cpuThrottleSample, bool) {
	var s cpuThrottleSample
	found := false
	sc := bufio.NewScanner(bytes.NewReader(contents))
	for sc.Scan() {
		key, val, ok := strings.Cut(strings.TrimSpace(sc.Text()), " ")
		if !ok {
			continue
		}
		n, err := strconv.ParseUint(strings.TrimSpace(val), 10, 64)
		if err != nil {
			continue
		}
		switch key {
		case "nr_periods":
			s.periods = n
			found = true
		case "nr_throttled":
			s.throttled = n
		}
	}
	return s, found
}

func throttledRatio(prev, cur cpuThrottleSample) float64 {
	periods := cur.periods - prev.periods
	if cur.periods < prev.periods || periods == 0 {
		return 0
	}
	throttled := cur.throttled - prev.throttled
	if cur.throttled < prev.throttled {
		return 0
	}
	return float64(throttled) / float64(periods)
}

const (
	adaptiveComputeTick = 500 * time.Millisecond

	throttleGrowBelow = 0.05

	throttleShrinkAbove = 0.25

	calmTicksToDouble = 2

	shrinkAfterThrottledTicks = 2
)

func nextComputeBudget(cur, floor, ceil, waiting int, throttled float64, sustainedThrottle, calm bool) int {
	floor = max(floor, 1)
	ceil = max(ceil, floor)
	cur = min(max(cur, floor), ceil)
	switch {
	case sustainedThrottle:
		return max(floor, cur-max(1, cur/4))
	case waiting > 0 && calm:
		return min(ceil, cur+min(waiting, cur))
	case waiting > 0 && throttled < throttleGrowBelow:
		return min(ceil, cur+1)
	}
	return cur
}

// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package trust

import (
	"sync"
	"time"
)

type JTICache interface {
	MarkOnceWithTTL(jti string, ttl time.Duration) bool
}

type inMemoryJTICache struct {
	mu         sync.Mutex
	seen       map[string]time.Time
	maxEntries int
	now        func() time.Time
}

func NewInMemoryJTICache(maxEntries int) JTICache {
	if maxEntries <= 0 {
		maxEntries = 65536
	}
	return &inMemoryJTICache{
		seen:       make(map[string]time.Time, maxEntries),
		maxEntries: maxEntries,
		now:        time.Now,
	}
}

func (c *inMemoryJTICache) MarkOnceWithTTL(jti string, ttl time.Duration) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	if len(c.seen) >= c.maxEntries {
		for k, exp := range c.seen {
			if !exp.After(now) {
				delete(c.seen, k)
			}
		}
	}
	if exp, ok := c.seen[jti]; ok && exp.After(now) {
		return false
	}
	c.seen[jti] = now.Add(ttl)
	return true
}

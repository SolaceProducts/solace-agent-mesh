// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package identity

import (
	"context"
	"errors"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
)

type cacheEntry[V any] struct {
	value    V
	expireAt time.Time
}

type ttlCache[V any] struct {
	ttl           time.Duration
	coldWait      time.Duration
	sf            singleflight.Group
	mu            sync.RWMutex
	data          map[string]cacheEntry[V]
	coldBarrierMu sync.Mutex
	coldBarriers  map[string]chan struct{}
	coldErrors    map[string]error
}

func newTTLCache[V any](ttl, coldWait time.Duration) *ttlCache[V] {
	return &ttlCache[V]{
		ttl:          ttl,
		coldWait:     coldWait,
		data:         map[string]cacheEntry[V]{},
		coldBarriers: map[string]chan struct{}{},
		coldErrors:   map[string]error{},
	}
}

var errColdCacheTimeout = errors.New("cold cache wait exceeded")

func (c *ttlCache[V]) get(key string) (V, bool) {
	c.mu.RLock()
	entry, ok := c.data[key]
	c.mu.RUnlock()
	if !ok {
		var zero V
		return zero, false
	}
	if time.Now().After(entry.expireAt) {
		c.mu.Lock()
		if cur, ok := c.data[key]; ok && time.Now().After(cur.expireAt) {
			delete(c.data, key)
		}
		c.mu.Unlock()
		var zero V
		return zero, false
	}
	return entry.value, true
}

func (c *ttlCache[V]) set(key string, value V) {
	c.mu.Lock()
	c.data[key] = cacheEntry[V]{value: value, expireAt: time.Now().Add(c.ttl)}
	c.mu.Unlock()
}

func (c *ttlCache[V]) getOrLoad(ctx context.Context, key string, loader func(context.Context) (V, error)) (V, error) {
	if v, ok := c.get(key); ok {
		return v, nil
	}
	res, err, _ := c.sf.Do(key, func() (any, error) {
		if v, ok := c.get(key); ok {
			return v, nil
		}
		v, err := loader(ctx)
		if err != nil {
			return v, err
		}
		c.set(key, v)
		return v, nil
	})
	if err != nil {
		var zero V
		return zero, err
	}
	return res.(V), nil
}

func (c *ttlCache[V]) getOrLoadCold(ctx context.Context, key string, loader func(context.Context) (V, error)) (V, error) {
	if v, ok := c.get(key); ok {
		return v, nil
	}
	if c.coldWait <= 0 {
		return c.getOrLoad(ctx, key, loader)
	}

	c.coldBarrierMu.Lock()
	barrier, exists := c.coldBarriers[key]
	if !exists {
		barrier = make(chan struct{})
		c.coldBarriers[key] = barrier
	}
	c.coldBarrierMu.Unlock()

	if !exists {
		go func() {
			_, _, _ = c.sf.Do(key, func() (any, error) {
				if v, ok := c.get(key); ok {
					c.recordColdResult(key, nil)
					return v, nil
				}
				v, err := loader(context.Background())
				if err == nil {
					c.set(key, v)
				}
				c.recordColdResult(key, err)
				return v, err
			})
		}()
	}

	timer := time.NewTimer(c.coldWait)
	defer timer.Stop()
	select {
	case <-barrier:
		if v, ok := c.get(key); ok {
			return v, nil
		}
		if err := c.peekColdError(key); err != nil {
			var zero V
			return zero, err
		}
		var zero V
		return zero, errColdCacheTimeout
	case <-timer.C:
		var zero V
		return zero, errColdCacheTimeout
	case <-ctx.Done():
		var zero V
		return zero, ctx.Err()
	}
}

func (c *ttlCache[V]) recordColdResult(key string, err error) {
	c.coldBarrierMu.Lock()
	barrier, ok := c.coldBarriers[key]
	if ok {
		delete(c.coldBarriers, key)
	}
	if err != nil {
		c.coldErrors[key] = err
	} else {
		delete(c.coldErrors, key)
	}
	c.coldBarrierMu.Unlock()
	if ok {
		close(barrier)
	}
}

func (c *ttlCache[V]) peekColdError(key string) error {
	c.coldBarrierMu.Lock()
	err := c.coldErrors[key]
	c.coldBarrierMu.Unlock()
	return err
}

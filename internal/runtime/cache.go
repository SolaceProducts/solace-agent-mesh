// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package runtime

import (
	"sync"
	"time"
)

func WithTTL(d time.Duration) CacheOption {
	return func(cfg *cacheEntryConfig) {
		cfg.ttl = d
	}
}

func WithOnExpiry(fn func(key string, value any)) CacheOption {
	return func(cfg *cacheEntryConfig) {
		cfg.onExpiry = fn
	}
}

type MapCache struct {
	mu      sync.RWMutex
	entries map[string]*cacheEntry
}

type cacheEntry struct {
	value     any
	expiresAt time.Time
	timer     *time.Timer
}

func NewMapCache() *MapCache {
	return &MapCache{entries: make(map[string]*cacheEntry)}
}

func (c *MapCache) Get(key string) (any, bool) {
	c.mu.RLock()
	e, ok := c.entries[key]
	if !ok {
		c.mu.RUnlock()
		return nil, false
	}
	if !e.expiresAt.IsZero() && time.Now().After(e.expiresAt) {
		c.mu.RUnlock()
		return nil, false
	}
	v := e.value
	c.mu.RUnlock()
	return v, true
}

func (c *MapCache) armExpiry(key string, entry *cacheEntry, cfg *cacheEntryConfig) {
	if cfg.ttl <= 0 {
		return
	}
	entry.expiresAt = time.Now().Add(cfg.ttl)
	entry.timer = time.AfterFunc(cfg.ttl, func() {
		c.mu.Lock()
		current, exists := c.entries[key]
		live := exists && current == entry
		if live {
			delete(c.entries, key)
		}
		c.mu.Unlock()
		if live && cfg.onExpiry != nil {
			cfg.onExpiry(key, entry.value)
		}
	})
}

func (c *MapCache) Set(key string, value any, opts ...CacheOption) {
	cfg := &cacheEntryConfig{}
	for _, opt := range opts {
		opt(cfg)
	}

	c.mu.Lock()
	if old, exists := c.entries[key]; exists && old.timer != nil {
		old.timer.Stop()
	}

	entry := &cacheEntry{value: value}

	c.armExpiry(key, entry, cfg)

	c.entries[key] = entry
	c.mu.Unlock()
}

func (c *MapCache) Delete(key string) {
	c.mu.Lock()
	if e, exists := c.entries[key]; exists {
		if e.timer != nil {
			e.timer.Stop()
		}
		delete(c.entries, key)
	}
	c.mu.Unlock()
}

func (c *MapCache) Clear() {
	c.mu.Lock()
	for _, e := range c.entries {
		if e.timer != nil {
			e.timer.Stop()
		}
	}
	c.entries = make(map[string]*cacheEntry)
	c.mu.Unlock()
}

func (c *MapCache) GetAndDelete(key string) (any, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[key]
	if !ok {
		return nil, false
	}
	if !e.expiresAt.IsZero() && time.Now().After(e.expiresAt) {
		return nil, false
	}
	if e.timer != nil {
		e.timer.Stop()
	}
	delete(c.entries, key)
	return e.value, true
}

func (c *MapCache) DeleteIf(key string, pred func(value any) bool) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[key]
	if !ok {
		return false
	}
	if !e.expiresAt.IsZero() && time.Now().After(e.expiresAt) {
		return false
	}
	if !pred(e.value) {
		return false
	}
	if e.timer != nil {
		e.timer.Stop()
	}
	delete(c.entries, key)
	return true
}

func (c *MapCache) Swap(key string, value any, opts ...CacheOption) (any, bool) {
	cfg := &cacheEntryConfig{}
	for _, opt := range opts {
		opt(cfg)
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	var prev any
	var had bool
	if old, exists := c.entries[key]; exists {
		if old.timer != nil {
			old.timer.Stop()
		}
		prev, had = old.value, true
	}

	entry := &cacheEntry{value: value}
	c.armExpiry(key, entry, cfg)
	c.entries[key] = entry
	return prev, had
}

func (c *MapCache) SetIfAbsent(key string, value any, opts ...CacheOption) bool {
	cfg := &cacheEntryConfig{}
	for _, opt := range opts {
		opt(cfg)
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if e, exists := c.entries[key]; exists {
		if e.expiresAt.IsZero() || !time.Now().After(e.expiresAt) {
			return false
		}
		if e.timer != nil {
			e.timer.Stop()
		}
	}

	entry := &cacheEntry{value: value}
	c.armExpiry(key, entry, cfg)

	c.entries[key] = entry
	return true
}

var _ Cache = (*MapCache)(nil)

// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package session

import (
	"container/list"
	"slices"
	"sync"
)

const (
	defaultMsgCacheEntries = 2048
	defaultMsgCacheBytes   = 256 << 20
)

type msgCacheEntry struct {
	key    msgKey
	msgs   []Message
	maxSeq int64
	bytes  int
}

type msgCache struct {
	mu         sync.Mutex
	ll         *list.List
	entries    map[msgKey]*list.Element
	maxEntries int
	maxBytes   int
	curBytes   int

	hits   uint64
	misses uint64
}

func newMsgCache(maxEntries, maxBytes int) *msgCache {
	return &msgCache{
		ll:         list.New(),
		entries:    make(map[msgKey]*list.Element),
		maxEntries: maxEntries,
		maxBytes:   maxBytes,
	}
}

func (c *msgCache) get(key msgKey, lastSeq int64) ([]Message, bool) {
	if c == nil {
		return nil, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.entries[key]
	if !ok {
		c.misses++
		return nil, false
	}
	e := el.Value.(*msgCacheEntry)
	if e.maxSeq != lastSeq {
		c.misses++
		c.removeElement(el)
		return nil, false
	}
	c.hits++
	c.ll.MoveToFront(el)
	return slices.Clone(e.msgs), true
}

func (c *msgCache) put(key msgKey, msgs []Message, lastSeq int64) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.entries[key]; ok {
		c.removeElement(el)
	}
	e := &msgCacheEntry{
		key:    key,
		msgs:   slices.Clone(msgs),
		maxSeq: lastSeq,
		bytes:  messagesBytes(msgs),
	}
	c.entries[key] = c.ll.PushFront(e)
	c.curBytes += e.bytes
	c.evictLocked()
}

func (c *msgCache) appendOrEvict(key msgKey, msgs []Message, base int64) {
	if c == nil || len(msgs) == 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.entries[key]
	if !ok {
		return
	}
	e := el.Value.(*msgCacheEntry)
	if e.maxSeq != base {
		c.removeElement(el)
		return
	}
	e.msgs = append(e.msgs, slices.Clone(msgs)...)
	delta := messagesBytes(msgs)
	e.bytes += delta
	c.curBytes += delta
	e.maxSeq = base + int64(len(msgs))
	c.ll.MoveToFront(el)
	c.evictLocked()
}

func (c *msgCache) evict(key msgKey) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.entries[key]; ok {
		c.removeElement(el)
	}
}

func (c *msgCache) stats() (hits, misses uint64) {
	if c == nil {
		return 0, 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.hits, c.misses
}

func (c *msgCache) evictLocked() {
	for c.ll.Len() > c.maxEntries || c.curBytes > c.maxBytes {
		back := c.ll.Back()
		if back == nil {
			return
		}
		c.removeElement(back)
	}
}

func (c *msgCache) removeElement(el *list.Element) {
	e := el.Value.(*msgCacheEntry)
	c.ll.Remove(el)
	delete(c.entries, e.key)
	c.curBytes -= e.bytes
}

func messagesBytes(msgs []Message) int {
	total := 0
	for i := range msgs {
		total += len(msgs[i].Content) + len(msgs[i].Reasoning) + len(msgs[i].ToolCallID) + len(msgs[i].ToolName) + 64
		for j := range msgs[i].ToolCalls {
			tc := msgs[i].ToolCalls[j]
			total += len(tc.ID) + len(tc.Name) + len(tc.Arguments)
		}
	}
	return total
}

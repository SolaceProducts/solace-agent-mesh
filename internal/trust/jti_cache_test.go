// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package trust

import (
	"sync"
	"testing"
	"time"
)

func TestJTICache_FirstSightingMarks(t *testing.T) {
	c := NewInMemoryJTICache(8)
	if !c.MarkOnceWithTTL("jti-1", time.Minute) {
		t.Fatal("first sighting must return true")
	}
}

func TestJTICache_SecondSightingRejects(t *testing.T) {
	c := NewInMemoryJTICache(8)
	c.MarkOnceWithTTL("jti-1", time.Minute)
	if c.MarkOnceWithTTL("jti-1", time.Minute) {
		t.Fatal("second sighting must return false (replay)")
	}
}

func TestJTICache_ExpiredEntryAccepted(t *testing.T) {
	cache := &inMemoryJTICache{
		seen:       make(map[string]time.Time),
		maxEntries: 8,
		now:        func() time.Time { return time.Unix(0, 0) },
	}
	if !cache.MarkOnceWithTTL("jti-1", time.Second) {
		t.Fatal("first sighting must return true")
	}
	cache.now = func() time.Time { return time.Unix(10, 0) }
	if !cache.MarkOnceWithTTL("jti-1", time.Second) {
		t.Fatal("after expiry, jti can be marked again")
	}
}

func TestJTICache_ConcurrentSafe(t *testing.T) {
	c := NewInMemoryJTICache(1024)
	var wg sync.WaitGroup
	for i := range 100 {
		wg.Go(func() {
			jti := jtiAt(i)
			if !c.MarkOnceWithTTL(jti, time.Minute) {
				t.Errorf("distinct jti %s rejected on first sighting", jti)
			}
		})
	}
	wg.Wait()
}

func jtiAt(i int) string {
	return "jti-" + string(rune('a'+i%26)) + string(rune('a'+(i/26)%26))
}

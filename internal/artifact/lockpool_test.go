// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package artifact

import (
	"sync"
	"testing"
)

func TestFnv1a_Deterministic(t *testing.T) {
	for _, key := range []string{"", "a", "artifact/user1/sess1/file.txt", "hello world"} {
		h1 := fnv1a(key)
		h2 := fnv1a(key)
		if h1 != h2 {
			t.Errorf("fnv1a(%q) not deterministic: %d != %d", key, h1, h2)
		}
	}
}

func TestFnv1a_Distribution(t *testing.T) {
	used := make(map[uint32]bool)
	for i := 0; i < 1000; i++ {
		key := "key-" + string(rune('A'+i%26)) + "-" + string(rune('0'+i%10))
		shard := fnv1a(key) % lockPoolShards
		used[shard] = true
	}
	if len(used) < 50 {
		t.Errorf("poor distribution: only %d of %d shards used", len(used), lockPoolShards)
	}
}

func TestFnv1a_DifferentKeys(t *testing.T) {
	if fnv1a("alpha") == fnv1a("beta") {
		t.Error("fnv1a(alpha) == fnv1a(beta), expected different hashes")
	}
}

func TestLockPool_SameKeySameShard(t *testing.T) {
	pool := NewLockPool()

	pool.Lock("mykey")
	pool.Unlock("mykey")
}

func TestLockPool_DifferentKeysConcurrent(t *testing.T) {
	pool := NewLockPool()
	const goroutines = 100

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func(n int) {
			defer wg.Done()
			key := string(rune('A' + n%26))
			for j := 0; j < 100; j++ {
				pool.Lock(key)
				pool.Unlock(key)
			}
		}(i)
	}
	wg.Wait()
}

func TestLockPool_MutualExclusion(t *testing.T) {
	pool := NewLockPool()
	key := "shared-counter"
	counter := 0
	const goroutines = 50
	const iterations = 100

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				pool.Lock(key)
				counter++
				pool.Unlock(key)
			}
		}()
	}
	wg.Wait()

	if counter != goroutines*iterations {
		t.Errorf("counter = %d, want %d (race detected)", counter, goroutines*iterations)
	}
}

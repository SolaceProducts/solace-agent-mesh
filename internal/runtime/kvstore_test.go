// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package runtime

import (
	"sync"
	"testing"
)

func TestKVStoreBasic(t *testing.T) {
	kv := NewMapKVStore()

	if _, ok := kv.Get("missing"); ok {
		t.Error("should not find missing key")
	}

	kv.Set("key1", "value1")
	v, ok := kv.Get("key1")
	if !ok || v != "value1" {
		t.Errorf("get = %v, %v", v, ok)
	}

	kv.Set("key1", 42)
	v, ok = kv.Get("key1")
	if !ok || v != 42 {
		t.Errorf("get = %v, %v", v, ok)
	}

	kv.Delete("key1")
	if _, ok := kv.Get("key1"); ok {
		t.Error("should be deleted")
	}

	kv.Delete("nonexistent")
}

func TestKVStoreConcurrent(t *testing.T) {
	kv := NewMapKVStore()
	const n = 100
	var wg sync.WaitGroup
	wg.Add(n * 3)

	for range n {
		key := "key"
		go func() { defer wg.Done(); kv.Set(key, "val") }()
		go func() { defer wg.Done(); kv.Get(key) }()
		go func() { defer wg.Done(); kv.Delete(key) }()
	}
	wg.Wait()
}

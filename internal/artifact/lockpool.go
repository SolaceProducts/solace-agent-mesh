// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package artifact

import "sync"

const lockPoolShards = 256

type LockPool struct {
	shards [lockPoolShards]sync.Mutex
}

func NewLockPool() *LockPool {
	return &LockPool{}
}

func (p *LockPool) Lock(key string) {
	p.shards[fnv1a(key)%lockPoolShards].Lock()
}

func (p *LockPool) Unlock(key string) {
	p.shards[fnv1a(key)%lockPoolShards].Unlock()
}

func fnv1a(s string) uint32 {
	h := uint32(2166136261)
	for i := 0; i < len(s); i++ {
		h ^= uint32(s[i])
		h *= 16777619
	}
	return h
}

// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package auth

import (
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/identity"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

type ProfileCache struct {
	cache *runtime.MapCache
	ttl   time.Duration
}

type CachedProfile struct {
	Profile          *identity.Profile
	EnrichmentStatus identity.EnrichmentStatus
	EnrichmentReason string
}

func NewProfileCache(ttl time.Duration) *ProfileCache {
	return &ProfileCache{
		cache: runtime.NewMapCache(),
		ttl:   ttl,
	}
}

func (c *ProfileCache) Put(userID string, entry CachedProfile) {
	if c == nil || c.cache == nil || userID == "" {
		return
	}
	c.cache.Set(userID, entry, runtime.WithTTL(c.ttl))
}

func (c *ProfileCache) Get(userID string) (CachedProfile, bool) {
	if c == nil || c.cache == nil || userID == "" {
		return CachedProfile{}, false
	}
	v, ok := c.cache.Get(userID)
	if !ok {
		return CachedProfile{}, false
	}
	entry, ok := v.(CachedProfile)
	if !ok {
		return CachedProfile{}, false
	}
	return entry, true
}

func (c *ProfileCache) Invalidate(userID string) {
	if c == nil || c.cache == nil || userID == "" {
		return
	}
	c.cache.Delete(userID)
}

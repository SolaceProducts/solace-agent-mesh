// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package auth

import (
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/identity"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

func TestProfileCache_PutGet(t *testing.T) {
	c := NewProfileCache(time.Hour)
	entry := CachedProfile{
		Profile:          &identity.Profile{ID: "u-1", DisplayName: "Alice"},
		EnrichmentStatus: identity.EnrichmentOK,
	}
	c.Put("alice", entry)

	got, ok := c.Get("alice")
	if !ok {
		t.Fatal("expected cache hit after Put")
	}
	if got.Profile.ID != "u-1" || got.EnrichmentStatus != identity.EnrichmentOK {
		t.Errorf("got = %+v", got)
	}
}

func TestProfileCache_GetMissing(t *testing.T) {
	c := NewProfileCache(time.Hour)
	if _, ok := c.Get("nobody"); ok {
		t.Error("expected miss for empty cache")
	}
}

func TestProfileCache_EmptyUserIDIsNoop(t *testing.T) {
	c := NewProfileCache(time.Hour)
	c.Put("", CachedProfile{EnrichmentStatus: identity.EnrichmentOK})
	if _, ok := c.Get(""); ok {
		t.Error("empty userID must not yield a cache entry")
	}
	c.Invalidate("")
}

func TestProfileCache_Invalidate(t *testing.T) {
	c := NewProfileCache(time.Hour)
	c.Put("alice", CachedProfile{EnrichmentStatus: identity.EnrichmentOK})
	c.Invalidate("alice")
	if _, ok := c.Get("alice"); ok {
		t.Error("expected miss after Invalidate")
	}
}

func TestProfileCache_NilReceiverIsSafe(t *testing.T) {
	var c *ProfileCache
	c.Put("alice", CachedProfile{})
	if _, ok := c.Get("alice"); ok {
		t.Error("nil receiver Get must return miss")
	}
	c.Invalidate("alice")
}

func TestProfileCache_ZeroValueIsSafe(t *testing.T) {
	c := &ProfileCache{}
	c.Put("alice", CachedProfile{})
	if _, ok := c.Get("alice"); ok {
		t.Error("zero-value Get must return miss")
	}
	c.Invalidate("alice")
}

func TestProfileCache_WrongValueTypeReturnsMiss(t *testing.T) {
	c := NewProfileCache(time.Hour)
	c.cache.Set("alice", "not a CachedProfile", runtime.WithTTL(time.Hour))
	if _, ok := c.Get("alice"); ok {
		t.Error("Get must return miss when stored value has wrong type")
	}
}

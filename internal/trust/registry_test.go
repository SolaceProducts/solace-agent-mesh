// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package trust

import (
	"testing"
	"time"
)

func TestRegistryAddAndGet(t *testing.T) {
	t.Parallel()
	r := NewRegistry()

	card := &TrustCard{
		ComponentType: "gateway",
		ComponentID:   "gw-1",
		Namespace:     "test-ns",
		JWKS:          map[string]any{"keys": []any{}},
		IssuedAt:      time.Now().UnixMilli(),
		ExpiresAt:     time.Now().Add(24 * time.Hour).UnixMilli(),
		Version:       "1.0",
	}
	r.AddOrUpdate(card)

	got := r.GetTrustCard("gw-1")
	if got == nil {
		t.Fatal("expected trust card, got nil")
	}
	if got.ComponentID != "gw-1" {
		t.Errorf("ComponentID = %q, want %q", got.ComponentID, "gw-1")
	}

	got.ComponentID = "modified"
	original := r.GetTrustCard("gw-1")
	if original.ComponentID != "gw-1" {
		t.Errorf("registry card was mutated: got %q", original.ComponentID)
	}
}

func TestRegistryGetNotFound(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	if got := r.GetTrustCard("nonexistent"); got != nil {
		t.Errorf("expected nil, got %+v", got)
	}
}

func TestRegistryGetJWKS(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	jwks := map[string]any{"keys": []any{"key1"}}
	r.AddOrUpdate(&TrustCard{
		ComponentID: "gw-1",
		JWKS:        jwks,
		ExpiresAt:   time.Now().Add(time.Hour).UnixMilli(),
	})

	got, ok := r.GetJWKS("gw-1")
	if !ok {
		t.Fatal("expected JWKS, got not-found")
	}
	if got == nil {
		t.Fatal("expected non-nil JWKS")
	}

	_, ok = r.GetJWKS("nonexistent")
	if ok {
		t.Error("expected not-found for nonexistent component")
	}
}

func TestRegistryGetComponentType(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	r.AddOrUpdate(&TrustCard{
		ComponentType: "agent",
		ComponentID:   "agent-1",
		ExpiresAt:     time.Now().Add(time.Hour).UnixMilli(),
	})

	ct, ok := r.GetComponentType("agent-1")
	if !ok || ct != "agent" {
		t.Errorf("GetComponentType = (%q, %v), want (\"agent\", true)", ct, ok)
	}

	_, ok = r.GetComponentType("nonexistent")
	if ok {
		t.Error("expected not-found for nonexistent component")
	}
}

func TestRegistryListComponents(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	r.AddOrUpdate(&TrustCard{ComponentID: "gw-1", ExpiresAt: time.Now().Add(time.Hour).UnixMilli()})
	r.AddOrUpdate(&TrustCard{ComponentID: "agent-1", ExpiresAt: time.Now().Add(time.Hour).UnixMilli()})

	ids := r.ListComponents()
	if len(ids) != 2 {
		t.Fatalf("expected 2 components, got %d", len(ids))
	}
	found := map[string]bool{}
	for _, id := range ids {
		found[id] = true
	}
	if !found["gw-1"] || !found["agent-1"] {
		t.Errorf("unexpected component list: %v", ids)
	}
}

func TestRegistryCountByType(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	r.AddOrUpdate(&TrustCard{ComponentType: "gateway", ComponentID: "gw-1", ExpiresAt: time.Now().Add(time.Hour).UnixMilli()})
	r.AddOrUpdate(&TrustCard{ComponentType: "gateway", ComponentID: "gw-2", ExpiresAt: time.Now().Add(time.Hour).UnixMilli()})
	r.AddOrUpdate(&TrustCard{ComponentType: "agent", ComponentID: "agent-1", ExpiresAt: time.Now().Add(time.Hour).UnixMilli()})

	if got := r.CountByType("gateway"); got != 2 {
		t.Errorf("CountByType(gateway) = %d, want 2", got)
	}
	if got := r.CountByType("agent"); got != 1 {
		t.Errorf("CountByType(agent) = %d, want 1", got)
	}
	if got := r.CountByType("nonexistent"); got != 0 {
		t.Errorf("CountByType(nonexistent) = %d, want 0", got)
	}
}

func TestRegistryRemoveExpired(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	r.AddOrUpdate(&TrustCard{
		ComponentID: "expired",
		ExpiresAt:   time.Now().Add(-time.Hour).UnixMilli(),
	})
	r.AddOrUpdate(&TrustCard{
		ComponentID: "valid",
		ExpiresAt:   time.Now().Add(time.Hour).UnixMilli(),
	})

	removed := r.RemoveExpired()
	if removed != 1 {
		t.Errorf("RemoveExpired = %d, want 1", removed)
	}
	if r.GetTrustCard("expired") != nil {
		t.Error("expired card should have been removed")
	}
	if r.GetTrustCard("valid") == nil {
		t.Error("valid card should still exist")
	}
}

func TestRegistryClear(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	r.AddOrUpdate(&TrustCard{ComponentID: "gw-1", ExpiresAt: time.Now().Add(time.Hour).UnixMilli()})
	r.Clear()
	if got := r.ListComponents(); len(got) != 0 {
		t.Errorf("expected empty registry after Clear, got %v", got)
	}
}

func TestRegistryUpdate(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	r.AddOrUpdate(&TrustCard{
		ComponentID: "gw-1",
		Namespace:   "old-ns",
		ExpiresAt:   time.Now().Add(time.Hour).UnixMilli(),
	})
	r.AddOrUpdate(&TrustCard{
		ComponentID: "gw-1",
		Namespace:   "new-ns",
		ExpiresAt:   time.Now().Add(2 * time.Hour).UnixMilli(),
	})

	got := r.GetTrustCard("gw-1")
	if got.Namespace != "new-ns" {
		t.Errorf("Namespace = %q, want %q", got.Namespace, "new-ns")
	}
}

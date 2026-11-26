// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package discovery

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
)

type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time { return c.t }

func (c *fakeClock) advance(d time.Duration) { c.t = c.t.Add(d) }

func newClockedRegistry() (*Registry, *fakeClock) {
	clock := &fakeClock{t: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	r := NewRegistry()
	r.now = clock.now
	return r, clock
}

func instanceIDs(r *Registry, name string) []string {
	var ids []string
	for _, inst := range r.Instances(name) {
		ids = append(ids, inst.ID)
	}
	return ids
}

func TestRegistry_ReplicaRemovalKeepsName(t *testing.T) {
	r := NewRegistry()
	card := a2a.AgentCard{Name: "agent-x"}
	r.Observe(card, "A")
	r.Observe(card, "B")

	if r.Remove("agent-x", "A").Gone {
		t.Fatal("removal from replica A reported the name gone while replica B still publishes it")
	}
	if r.Get("agent-x") == nil {
		t.Fatal("agent-x evicted although replica B still publishes it")
	}
	if got := instanceIDs(r, "agent-x"); !slices.Equal(got, []string{"B"}) {
		t.Errorf("instances = %v, want [B]", got)
	}

	if !r.Remove("agent-x", "B").Gone {
		t.Fatal("removal from the last replica should report the name gone")
	}
	if r.Get("agent-x") != nil {
		t.Fatal("agent-x should be gone after its last replica's removal")
	}
}

func TestRegistry_RollingRestart(t *testing.T) {
	r := NewRegistry()
	card := a2a.AgentCard{Name: "peer_x"}

	r.Observe(card, "A")
	r.Observe(card, "B")

	if r.Remove("peer_x", "A").Gone {
		t.Fatal("removal from the terminating pod must not remove the peer")
	}
	if r.Get("peer_x") == nil {
		t.Fatal("peer should still be present after the old pod's removal")
	}
	if !r.Remove("peer_x", "B").Gone {
		t.Fatal("removal from the current pod should remove the peer")
	}
}

func TestRegistry_RemoveLegacy(t *testing.T) {
	t.Run("removal without id from a legacy publisher", func(t *testing.T) {
		r := NewRegistry()
		r.Observe(a2a.AgentCard{Name: "a"}, "")
		if !r.Remove("a", "").Gone {
			t.Fatal("removal with an empty id should be honored for a legacy publisher")
		}
	})
	t.Run("removal without id spares instances with ids", func(t *testing.T) {
		r := NewRegistry()
		r.Observe(a2a.AgentCard{Name: "a"}, "A")
		r.Observe(a2a.AgentCard{Name: "a"}, "")
		rm := r.Remove("a", "")
		if rm.Gone || !rm.Applied || rm.Remaining != 1 {
			t.Fatalf("removal = %+v, want only the legacy instance dropped", rm)
		}
		if rm := r.Remove("a", ""); rm.Applied {
			t.Fatalf("second id-less removal = %+v, want ignored", rm)
		}
		if got := instanceIDs(r, "a"); !slices.Equal(got, []string{"A"}) {
			t.Errorf("instances = %v, want [A]", got)
		}
	})
	t.Run("stored card without id", func(t *testing.T) {
		r := NewRegistry()
		r.Observe(a2a.AgentCard{Name: "a"}, "")
		if !r.Remove("a", "B").Gone {
			t.Fatal("removal should be honored when only a legacy publisher registered the name")
		}
	})
	t.Run("unknown instance is ignored", func(t *testing.T) {
		r := NewRegistry()
		r.Observe(a2a.AgentCard{Name: "a"}, "A")
		if r.Remove("a", "Z").Gone {
			t.Fatal("removal from an instance that never published should be ignored")
		}
		if r.Get("a") == nil {
			t.Fatal("a should still be registered")
		}
	})
	t.Run("unknown name", func(t *testing.T) {
		r := NewRegistry()
		if r.Remove("missing", "A").Gone {
			t.Fatal("removing an unknown name should report false")
		}
	})
}

func TestRegistry_ExpiresPerInstance(t *testing.T) {
	r, clock := newClockedRegistry()
	card := a2a.AgentCard{Name: "agent-x"}
	r.Observe(card, "A")
	clock.advance(2 * time.Minute)
	r.Observe(card, "B")

	if gone := r.ExpireOlderThan(clock.t.Add(-time.Minute)); len(gone) != 0 {
		t.Fatalf("gone = %v, want none: replica B is still publishing", gone)
	}
	if got := instanceIDs(r, "agent-x"); !slices.Equal(got, []string{"B"}) {
		t.Errorf("instances = %v, want [B]", got)
	}

	clock.advance(2 * time.Minute)
	if gone := r.ExpireOlderThan(clock.t.Add(-time.Minute)); !slices.Equal(gone, []string{"agent-x"}) {
		t.Fatalf("gone = %v, want [agent-x]", gone)
	}
	if r.Count() != 0 {
		t.Errorf("Count = %d, want 0", r.Count())
	}
}

func TestRegistry_VersionIgnoresHeartbeats(t *testing.T) {
	r := NewRegistry()
	card := a2a.AgentCard{Name: "a", Description: "v1"}

	if got := r.Observe(card, "A"); got != Added {
		t.Fatalf("first card = %v, want Added", got)
	}
	v := r.Version()

	if got := r.Observe(card, "A"); got != Refreshed {
		t.Fatalf("identical heartbeat = %v, want Refreshed", got)
	}
	if got := r.Observe(card, "B"); got != Refreshed {
		t.Fatalf("identical card from a second replica = %v, want Refreshed", got)
	}
	if r.Version() != v {
		t.Fatalf("version moved from %d to %d on identical heartbeats", v, r.Version())
	}

	r.Remove("a", "A")
	if r.Version() != v {
		t.Fatalf("version moved on a replica removal that left the card unchanged")
	}

	card.Description = "v2"
	if got := r.Observe(card, "B"); got != Updated {
		t.Fatalf("changed card = %v, want Updated", got)
	}
	if r.Version() <= v {
		t.Fatal("version should increase when the card changes")
	}
	v = r.Version()

	r.Remove("a", "B")
	if r.Version() <= v {
		t.Fatal("version should increase when the name goes")
	}
}

func TestRegistry_SharedCardSurvivesReplicaHeartbeats(t *testing.T) {
	r := NewRegistry()
	card := a2a.AgentCard{Name: "a"}
	r.Observe(card, "A")
	first := r.Lookup("a")
	r.Observe(card, "B")
	r.Observe(card, "A")
	if r.Lookup("a") != first {
		t.Fatal("identical heartbeats replaced the shared card; consumers detect changes by pointer")
	}
}

func TestRegistry_DifferingReplicaCardsDoNotChurn(t *testing.T) {
	r, clock := newClockedRegistry()
	r.Observe(a2a.AgentCard{Name: "a", Description: "healthy"}, "A")
	clock.advance(time.Second)
	r.Observe(a2a.AgentCard{Name: "a", Description: "degraded"}, "B")
	v := r.Version()

	for range 5 {
		clock.advance(time.Second)
		if got := r.Observe(a2a.AgentCard{Name: "a", Description: "healthy"}, "A"); got != Refreshed {
			t.Fatalf("replica A heartbeat = %v, want Refreshed", got)
		}
		if got := r.Observe(a2a.AgentCard{Name: "a", Description: "degraded"}, "B"); got != Refreshed {
			t.Fatalf("replica B heartbeat = %v, want Refreshed", got)
		}
	}
	if r.Version() != v {
		t.Fatalf("version moved from %d to %d while replicas repeated their own cards", v, r.Version())
	}
	if got := r.Get("a").Description; got != "degraded" {
		t.Fatalf("Get = %q, want the card that changed most recently", got)
	}
}

func TestRegistry_StaleInstanceStopsSpeakingForTheName(t *testing.T) {
	r, clock := newClockedRegistry()
	r.Observe(a2a.AgentCard{Name: "a", Description: "live"}, "A")
	clock.advance(time.Second)
	r.Observe(a2a.AgentCard{Name: "a", Description: "dead"}, "B")

	clock.advance(time.Minute)
	r.Observe(a2a.AgentCard{Name: "a", Description: "live"}, "A")
	cutoff := clock.t.Add(-30 * time.Second)

	if got := r.LookupSince("a", cutoff); got == nil || got.Description != "live" {
		t.Fatalf("LookupSince = %+v, want the card of the replica still publishing", got)
	}
	var seen []string
	r.EachSince(cutoff, func(card *a2a.AgentCard, fresh bool) {
		if fresh {
			seen = append(seen, card.Description)
		}
	})
	if !slices.Equal(seen, []string{"live"}) {
		t.Fatalf("EachSince fresh cards = %v, want [live]", seen)
	}

	if got := r.LookupSince("a", clock.t.Add(time.Second)); got != nil {
		t.Fatalf("LookupSince past every instance = %+v, want nil", got)
	}
	r.EachSince(clock.t.Add(time.Second), func(card *a2a.AgentCard, fresh bool) {
		if fresh {
			t.Fatalf("EachSince reported %q fresh although no instance published since the cutoff", card.Name)
		}
	})
}

func TestRegistry_CapsInstancesPerName(t *testing.T) {
	r, clock := newClockedRegistry()
	for i := range maxInstancesPerName + 5 {
		clock.advance(time.Second)
		r.Observe(a2a.AgentCard{Name: "a", Description: fmt.Sprint(i)}, fmt.Sprintf("id-%03d", i))
	}
	ids := instanceIDs(r, "a")
	if len(ids) != maxInstancesPerName {
		t.Fatalf("instances = %d, want the cap %d", len(ids), maxInstancesPerName)
	}
	if slices.Contains(ids, "id-000") || !slices.Contains(ids, "id-005") {
		t.Errorf("instances = %v, want the five seen longest ago dropped", ids)
	}
	if got := r.Get("a").Description; got != fmt.Sprint(maxInstancesPerName+4) {
		t.Errorf("Get = %q, want the instance that published last", got)
	}
}

func TestRegistry_RemovalResolvesToRemainingInstance(t *testing.T) {
	r, clock := newClockedRegistry()
	r.Observe(a2a.AgentCard{Name: "a", Description: "old"}, "A")
	clock.advance(time.Second)
	r.Observe(a2a.AgentCard{Name: "a", Description: "new"}, "B")
	if got := r.Get("a").Description; got != "new" {
		t.Fatalf("Get = %q, want the most recent card", got)
	}
	v := r.Version()

	r.Remove("a", "B")
	if got := r.Get("a").Description; got != "old" {
		t.Fatalf("Get after B left = %q, want replica A's card", got)
	}
	if r.Version() <= v {
		t.Fatal("version should increase when the resolved card changes")
	}
}

func TestRegistry_GetReturnsCopy(t *testing.T) {
	r := NewRegistry()
	r.Observe(a2a.AgentCard{Name: "a", Description: "v1"}, "")
	r.Get("a").Description = "mutated"
	if got := r.Lookup("a").Description; got != "v1" {
		t.Fatalf("stored card = %q, want v1: Get must return a copy", got)
	}
}

func TestRegistry_NamesAndListSorted(t *testing.T) {
	r := NewRegistry()
	for _, n := range []string{"c", "a", "b"} {
		r.Observe(a2a.AgentCard{Name: n}, "")
	}
	if got := r.Names(); !slices.Equal(got, []string{"a", "b", "c"}) {
		t.Errorf("Names = %v", got)
	}
	var listed []string
	for _, c := range r.List() {
		listed = append(listed, c.Name)
	}
	if !slices.Equal(listed, []string{"a", "b", "c"}) {
		t.Errorf("List = %v", listed)
	}
}

func TestApply(t *testing.T) {
	r := NewRegistry()
	parse := func(payload string, props map[string]string) Message {
		t.Helper()
		m, err := ParseMessage([]byte(payload), props)
		if err != nil {
			t.Fatalf("ParseMessage: %v", err)
		}
		return m
	}

	if ev := r.Apply(parse(`{"name":"a"}`, map[string]string{"instance_id": "A"})); ev.Change != Added || ev.Name != "a" {
		t.Fatalf("card event = %+v, want Added a", ev)
	}
	r.Apply(parse(`{"name":"a"}`, map[string]string{"instance_id": "B"}))

	ev := r.Apply(parse(`{"name":"a"}`, map[string]string{"instance_id": "A", "removed": "true"}))
	if ev.Removed || ev.Ignored || ev.Remaining != 1 {
		t.Fatalf("replica removal event = %+v, want applied with 1 remaining", ev)
	}
	ev = r.Apply(parse(`{"name":"a"}`, map[string]string{"instance_id": "Z", "removed": "true"}))
	if ev.Removed || !ev.Ignored {
		t.Fatalf("unknown instance removal event = %+v, want ignored", ev)
	}
	ev = r.Apply(parse(`{"name":"a"}`, map[string]string{"instance_id": "B", "removed": "true"}))
	if !ev.Removed {
		t.Fatalf("last replica removal event = %+v, want removed", ev)
	}

	if ev := r.Apply(parse(`{"description":"no name"}`, nil)); !ev.Ignored || r.Count() != 0 {
		t.Fatalf("nameless card event = %+v, count %d; want ignored and nothing stored", ev, r.Count())
	}
}

func TestParseMessage_RejectsOversizedInput(t *testing.T) {
	big := []byte(`{"name":"a","description":"` + strings.Repeat("x", maxCardBytes) + `"}`)
	if _, err := ParseMessage(big, nil); err == nil {
		t.Fatal("want an error for a card over the size limit")
	}
	if _, err := ParseMessage([]byte(`{"name":"a"}`), map[string]string{"instance_id": strings.Repeat("i", maxInstanceIDSize+1)}); err == nil {
		t.Fatal("want an error for an instance_id over the size limit")
	}
	if _, err := ParseMessage([]byte(`{"name":"a"}`), map[string]string{"instance_id": strings.Repeat("i", maxInstanceIDSize)}); err != nil {
		t.Fatalf("an instance_id at the limit should parse: %v", err)
	}
}

func TestParseMessage_RejectsMalformedCard(t *testing.T) {
	if _, err := ParseMessage([]byte(`{"name":"a","capabilities":"broken"}`), nil); err == nil {
		t.Fatal("want an error for a card whose capabilities is not an object")
	}
}

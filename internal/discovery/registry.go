// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package discovery

import (
	"cmp"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
)

const maxInstancesPerName = 64

type Change int

const (
	Added Change = iota + 1
	Updated
	Refreshed
)

type Instance struct {
	ID       string
	Card     a2a.AgentCard
	LastSeen time.Time
}

type instance struct {
	card      *a2a.AgentCard
	sum       [sha256.Size]byte
	lastSeen  time.Time
	changedAt time.Time
}

type entry struct {
	instances map[string]*instance
	current   string
}

func (e *entry) resolved() *instance { return e.instances[e.current] }

func (e *entry) resolvedSince(cutoff time.Time) *instance {
	if cur := e.resolved(); !cur.lastSeen.Before(cutoff) {
		return cur
	}
	_, inst := e.newestChange(cutoff)
	return inst
}

func (e *entry) newestChange(cutoff time.Time) (string, *instance) {
	var bestID string
	var best *instance
	for id, inst := range e.instances {
		if inst.lastSeen.Before(cutoff) {
			continue
		}
		if best == nil || inst.changedAt.After(best.changedAt) || (inst.changedAt.Equal(best.changedAt) && id < bestID) {
			bestID, best = id, inst
		}
	}
	return bestID, best
}

func (e *entry) legacyOnly() bool {
	_, ok := e.instances[""]
	return ok && len(e.instances) == 1
}

type Registry struct {
	now func() time.Time

	mu      sync.RWMutex
	entries map[string]*entry
	version uint64
	seen    uint64
}

func NewRegistry() *Registry {
	return &Registry{now: time.Now, entries: make(map[string]*entry)}
}

func (r *Registry) Version() uint64 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.version
}

func (r *Registry) Seen() uint64 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.seen
}

func (r *Registry) Observe(card a2a.AgentCard, instanceID string) Change {
	raw, err := json.Marshal(card)
	if err != nil {
		raw = fmt.Appendf(nil, "%+v", card)
	}
	return r.observe(&card, raw, instanceID, r.now(), false)
}

func (r *Registry) ObserveSeen(card a2a.AgentCard, instanceID string, lastSeen time.Time) Change {
	raw, err := json.Marshal(card)
	if err != nil {
		raw = fmt.Appendf(nil, "%+v", card)
	}
	return r.observe(&card, raw, instanceID, lastSeen, true)
}

func (r *Registry) observe(card *a2a.AgentCard, raw []byte, instanceID string, seenAt time.Time, reported bool) Change {
	sum := sha256.Sum256(raw)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seen++
	e, exists := r.entries[card.Name]
	if !exists {
		e = &entry{instances: make(map[string]*instance), current: instanceID}
		e.instances[instanceID] = &instance{card: card, sum: sum, lastSeen: seenAt, changedAt: seenAt}
		r.entries[card.Name] = e
		r.version++
		return Added
	}
	if inst, ok := e.instances[instanceID]; ok && inst.sum == sum {
		if seenAt.After(inst.lastSeen) {
			inst.lastSeen = seenAt
		}
		return Refreshed
	}
	if inst, ok := e.instances[instanceID]; ok && reported && seenAt.Before(inst.changedAt) {
		return Refreshed
	}
	prev := e.resolved()
	inst := &instance{card: card, sum: sum, lastSeen: seenAt, changedAt: seenAt}
	for _, other := range e.instances {
		if other.sum == sum {
			inst.card = other.card
			break
		}
	}
	e.instances[instanceID] = inst
	if !seenAt.Before(prev.changedAt) {
		e.current = instanceID
	}
	r.capLocked(e)
	if e.resolved().card == prev.card {
		return Refreshed
	}
	r.version++
	return Updated
}

func (r *Registry) capLocked(e *entry) {
	for len(e.instances) > maxInstancesPerName {
		oldestID := ""
		var oldest *instance
		for id, inst := range e.instances {
			if id != e.current && (oldest == nil || inst.lastSeen.Before(oldest.lastSeen)) {
				oldestID, oldest = id, inst
			}
		}
		delete(e.instances, oldestID)
	}
}

type Removal struct {
	Gone      bool
	Applied   bool
	Remaining int
}

func (r *Registry) Remove(name, instanceID string) Removal {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.entries[name]
	if !ok {
		return Removal{}
	}
	prevCard := e.resolved().card
	_, known := e.instances[instanceID]
	switch {
	case known:
		delete(e.instances, instanceID)
	case instanceID != "" && e.legacyOnly():
		delete(e.instances, "")
	default:
		return Removal{Remaining: len(e.instances)}
	}
	if len(e.instances) == 0 {
		delete(r.entries, name)
		r.version++
		return Removal{Gone: true, Applied: true}
	}
	r.resolveLocked(e, prevCard)
	return Removal{Applied: true, Remaining: len(e.instances)}
}

func (r *Registry) Delete(name string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.entries[name]; !ok {
		return false
	}
	delete(r.entries, name)
	r.version++
	return true
}

func (r *Registry) resolveLocked(e *entry, prevCard *a2a.AgentCard) {
	if _, ok := e.instances[e.current]; !ok {
		e.current, _ = e.newestChange(time.Time{})
	}
	if e.resolved().card != prevCard {
		r.version++
	}
}

func (r *Registry) ExpireOlderThan(cutoff time.Time) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var gone []string
	for name, e := range r.entries {
		prevCard := e.resolved().card
		for id, inst := range e.instances {
			if inst.lastSeen.Before(cutoff) {
				delete(e.instances, id)
			}
		}
		if len(e.instances) == 0 {
			delete(r.entries, name)
			gone = append(gone, name)
			continue
		}
		r.resolveLocked(e, prevCard)
	}
	if len(gone) > 0 {
		r.version++
	}
	return gone
}

func (r *Registry) Get(name string) *a2a.AgentCard {
	r.mu.RLock()
	defer r.mu.RUnlock()
	e, ok := r.entries[name]
	if !ok {
		return nil
	}
	card := *e.resolved().card
	return &card
}

func (r *Registry) Lookup(name string) *a2a.AgentCard {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if e, ok := r.entries[name]; ok {
		return e.resolved().card
	}
	return nil
}

func (r *Registry) LookupSince(name string, cutoff time.Time) *a2a.AgentCard {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if e, ok := r.entries[name]; ok {
		if inst := e.resolvedSince(cutoff); inst != nil {
			return inst.card
		}
	}
	return nil
}

func (r *Registry) Instances(name string) []Instance {
	r.mu.RLock()
	defer r.mu.RUnlock()
	e, ok := r.entries[name]
	if !ok {
		return nil
	}
	out := make([]Instance, 0, len(e.instances))
	for id, inst := range e.instances {
		out = append(out, Instance{ID: id, Card: *inst.card, LastSeen: inst.lastSeen})
	}
	slices.SortFunc(out, func(a, b Instance) int { return cmp.Compare(a.ID, b.ID) })
	return out
}

func (r *Registry) Each(fn func(card *a2a.AgentCard)) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, e := range r.entries {
		fn(e.resolved().card)
	}
}

func (r *Registry) EachSince(cutoff time.Time, fn func(card *a2a.AgentCard, fresh bool)) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, e := range r.entries {
		if inst := e.resolvedSince(cutoff); inst != nil {
			fn(inst.card, true)
		} else {
			fn(e.resolved().card, false)
		}
	}
}

func (r *Registry) EachInstance(fn func(card *a2a.AgentCard, instanceID string, lastSeen time.Time)) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, e := range r.entries {
		for id, inst := range e.instances {
			fn(inst.card, id, inst.lastSeen)
		}
	}
}

func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.entries))
	for name := range r.entries {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

func (r *Registry) List() []a2a.AgentCard {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.entries))
	for name := range r.entries {
		names = append(names, name)
	}
	slices.Sort(names)
	cards := make([]a2a.AgentCard, 0, len(names))
	for _, name := range names {
		cards = append(cards, *r.entries[name].resolved().card)
	}
	return cards
}

func (r *Registry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.entries)
}

func (r *Registry) Clear() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.entries) > 0 {
		r.version++
	}
	r.entries = make(map[string]*entry)
}

type Message struct {
	Card       *a2a.AgentCard
	Raw        []byte
	InstanceID string
	Removed    bool
}

const ProcessIDProperty = "sam_process_id"

const (
	maxCardBytes      = 1 << 20
	maxInstanceIDSize = 128
)

func ParseMessage(payload []byte, userProperties map[string]string) (Message, error) {
	if len(payload) > maxCardBytes {
		return Message{}, fmt.Errorf("card is %d bytes, over the %d byte limit", len(payload), maxCardBytes)
	}
	if n := len(userProperties["instance_id"]); n > maxInstanceIDSize {
		return Message{}, fmt.Errorf("instance_id is %d bytes, over the %d byte limit", n, maxInstanceIDSize)
	}
	var card a2a.AgentCard
	if err := json.Unmarshal(payload, &card); err != nil {
		return Message{}, fmt.Errorf("decode card: %w", err)
	}
	return Message{
		Card:       &card,
		Raw:        payload,
		InstanceID: userProperties["instance_id"],
		Removed:    userProperties["removed"] == "true",
	}, nil
}

type Event struct {
	Name      string
	Change    Change
	Removed   bool
	Remaining int
	Ignored   bool
}

func (r *Registry) Apply(m Message) Event {
	name := m.Card.Name
	if name == "" {
		return Event{Ignored: true}
	}
	if m.Removed {
		rm := r.Remove(name, m.InstanceID)
		return Event{Name: name, Removed: rm.Gone, Remaining: rm.Remaining, Ignored: !rm.Applied}
	}
	return Event{Name: name, Change: r.observe(m.Card, m.Raw, m.InstanceID, r.now(), false)}
}

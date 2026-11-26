// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package trust

import "sync"

type Registry struct {
	mu    sync.RWMutex
	cards map[string]*TrustCard
}

func NewRegistry() *Registry {
	return &Registry{cards: make(map[string]*TrustCard)}
}

func (r *Registry) AddOrUpdate(card *TrustCard) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cards[card.ComponentID] = card
}

func (r *Registry) GetTrustCard(componentID string) *TrustCard {
	r.mu.RLock()
	defer r.mu.RUnlock()
	card, ok := r.cards[componentID]
	if !ok {
		return nil
	}
	cp := *card
	return &cp
}

func (r *Registry) GetJWKS(componentID string) (map[string]any, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	card, ok := r.cards[componentID]
	if !ok {
		return nil, false
	}
	return card.JWKS, true
}

func (r *Registry) GetComponentType(componentID string) (string, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	card, ok := r.cards[componentID]
	if !ok {
		return "", false
	}
	return card.ComponentType, true
}

func (r *Registry) ListComponents() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	ids := make([]string, 0, len(r.cards))
	for id := range r.cards {
		ids = append(ids, id)
	}
	return ids
}

func (r *Registry) CountByType(componentType string) int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	count := 0
	for _, card := range r.cards {
		if card.ComponentType == componentType {
			count++
		}
	}
	return count
}

func (r *Registry) ByType(componentType string) []*TrustCard {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var cards []*TrustCard
	for _, card := range r.cards {
		if card.ComponentType == componentType {
			cp := *card
			cards = append(cards, &cp)
		}
	}
	return cards
}

func (r *Registry) Remove(componentID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.cards, componentID)
}

func (r *Registry) RemoveExpired() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	removed := 0
	for id, card := range r.cards {
		if card.IsExpired() {
			delete(r.cards, id)
			removed++
		}
	}
	return removed
}

func (r *Registry) Clear() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cards = make(map[string]*TrustCard)
}

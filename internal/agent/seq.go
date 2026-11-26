// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"sync"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
)

type EventSeqGen struct {
	mu sync.Mutex
	m  map[string]uint64
}

func NewEventSeqGen() *EventSeqGen {
	return &EventSeqGen{m: make(map[string]uint64)}
}

func (g *EventSeqGen) Next(taskID string) uint64 {
	if g == nil {
		return 0
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.m[taskID]++
	return g.m[taskID]
}

func (g *EventSeqGen) Stamp(m map[string]string, publisherID, taskID string) {
	if g == nil || publisherID == "" {
		return
	}
	a2a.AddEventSeq(m, publisherID, g.Next(taskID))
}

func (g *EventSeqGen) Drop(taskID string) {
	if g == nil {
		return
	}
	g.mu.Lock()
	delete(g.m, taskID)
	g.mu.Unlock()
}

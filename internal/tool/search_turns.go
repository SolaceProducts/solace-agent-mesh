// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tool

import (
	"encoding/json"
	"sync"
	"sync/atomic"

	"github.com/SolaceProducts/solace-agent-mesh/internal/tool/searchturn"
)

type SearchTurns struct {
	counters sync.Map

	mu   sync.Mutex
	held map[string]int
}

func (s *SearchTurns) Hold(callID string, turn int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.held == nil {
		s.held = make(map[string]int)
	}
	s.held[callID] = turn
}

func (s *SearchTurns) TakeHeld(callID string) (int, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	turn, ok := s.held[callID]
	delete(s.held, callID)
	return turn, ok
}

const WebSearchTurnKey = searchturn.WebSearch

func SearchTurnKey(toolName string) string {
	return searchturn.Key(toolName)
}

func NumbersSearches(toolName string) bool {
	return SearchTurnKey(toolName) == WebSearchTurnKey
}

func (s *SearchTurns) Next(toolName string) int {
	return int(s.counter(toolName).Add(1) - 1)
}

func (s *SearchTurns) Seed(toolName string, n int) {
	s.seed(toolName, int64(n))
}

func (s *SearchTurns) seed(toolName string, n int64) {
	c := s.counter(toolName)
	for {
		cur := c.Load()
		if cur >= n || c.CompareAndSwap(cur, n) {
			return
		}
	}
}

func (s *SearchTurns) counter(toolName string) *atomic.Int64 {
	c, _ := s.counters.LoadOrStore(SearchTurnKey(toolName), &atomic.Int64{})
	return c.(*atomic.Int64)
}

func (s *SearchTurns) MarshalJSON() ([]byte, error) {
	counts := map[string]int64{}
	s.counters.Range(func(key, c any) bool {
		counts[key.(string)] = c.(*atomic.Int64).Load()
		return true
	})
	return json.Marshal(counts)
}

func (s *SearchTurns) UnmarshalJSON(data []byte) error {
	var counts map[string]int64
	if err := json.Unmarshal(data, &counts); err != nil {
		return err
	}
	for key, n := range counts {
		s.seed(key, n)
	}
	return nil
}

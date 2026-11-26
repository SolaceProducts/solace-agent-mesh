// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tool

import (
	"encoding/json"
	"testing"
)

func TestSearchTurnsNumberPerToolAndContinueAfterASeed(t *testing.T) {
	var s SearchTurns
	if got := s.Next("web"); got != 0 {
		t.Fatalf("first web turn = %d, want 0", got)
	}
	if got := s.Next("index"); got != 0 {
		t.Fatalf("first index turn = %d, want 0: tools number separately", got)
	}
	s.Seed("web", 5)
	if got := s.Next("web"); got != 5 {
		t.Fatalf("web turn after a seed of 5 = %d, want 5", got)
	}
	s.Seed("web", 2)
	if got := s.Next("web"); got != 6 {
		t.Fatalf("a lower seed moved the counter back: got %d, want 6", got)
	}
}

func TestWebSearchToolsShareOneCounter(t *testing.T) {
	var s SearchTurns
	s.Seed(SearchTurnKey("web_search_google"), 2)
	got := []int{s.Next("web_search_ab12cd34"), s.Next("web_search_google"), s.Next("web_search")}
	if got[0] != 2 || got[1] != 3 || got[2] != 4 {
		t.Fatalf("turns = %v, want [2 3 4]: web search tools must not repeat a citation turn", got)
	}
	if got := s.Next("web_searcher"); got != 0 {
		t.Fatalf("web_searcher turn = %d, want 0: only the web_search name and its connector names share", got)
	}
}

func TestSkillNamespacedWebSearchToolsShareTheWebCounter(t *testing.T) {
	for _, name := range []string{"my-skill:web_search_x", "my-skill:web_search_google", "my-skill:web_search"} {
		if got := SearchTurnKey(name); got != WebSearchTurnKey {
			t.Errorf("SearchTurnKey(%q) = %q, want %q", name, got, WebSearchTurnKey)
		}
	}
	var s SearchTurns
	s.Seed(SearchTurnKey("my-skill:web_search_x"), 3)
	if got := s.Next("web_search_google"); got != 3 {
		t.Errorf("web_search_google turn after seeding a namespaced alias = %d, want 3", got)
	}
	for _, name := range []string{"my-skill:web_searcher", "my-skill:index_search", "web_searcher"} {
		if got := SearchTurnKey(name); got != name {
			t.Errorf("SearchTurnKey(%q) = %q, want the name itself", name, got)
		}
	}
}

func TestSearchTurnsSurviveAJSONRoundTrip(t *testing.T) {
	var s SearchTurns
	s.Next("web_search_google")
	s.Next("web_search_x")
	s.Next("kb_search")
	data, err := json.Marshal(&s)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var restored SearchTurns
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatalf("unmarshal %s: %v", data, err)
	}
	if got := restored.Next("web_search_y"); got != 2 {
		t.Errorf("web search turn after a round trip = %d, want 2", got)
	}
	if got := restored.Next("kb_search"); got != 1 {
		t.Errorf("kb_search turn after a round trip = %d, want 1", got)
	}
	if got := restored.Next("never_searched"); got != 0 {
		t.Errorf("an unrecorded tool's first turn = %d, want 0", got)
	}
}

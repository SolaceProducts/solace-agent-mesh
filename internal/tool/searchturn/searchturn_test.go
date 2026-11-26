// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package searchturn

import "testing"

func TestKeyNumbersEveryWebSearchToolOnOneCounter(t *testing.T) {
	for _, tc := range []struct{ name, want string }{
		{"web_search", WebSearch},
		{"web_search_google", WebSearch},
		{"web_search_ab12cd34", WebSearch},
		{"my-skill:web_search_x", WebSearch},
		{"web_searcher", "web_searcher"},
		{"tavily_search", "tavily_search"},
		{"index_search", "index_search"},
	} {
		if got := Key(tc.name); got != tc.want {
			t.Errorf("Key(%q) = %q, want %q", tc.name, got, tc.want)
		}
	}
}

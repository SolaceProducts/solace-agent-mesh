// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package websearch

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRenderLeavesOutResultsTheChatCannotShow(t *testing.T) {
	long := "https://example.com/" + strings.Repeat("a", 2048)
	outcome := render("q", []SearchResult{
		{Title: "Good", Link: "https://good.example/a", Snippet: "one", Type: "web"},
		{Title: "Long", Link: long, Snippet: "two", Type: "web"},
		{Title: "Credentials", Link: "https://user:pw@example.com/", Snippet: "three", Type: "web"},
		{Title: "Script", Link: "javascript:alert(1)", Snippet: "four", Type: "web"},
		{Title: "Also good", Link: "https://good.example/b", Snippet: "five", Type: "web"},
	}, 2)

	var ids []string
	for _, s := range outcome.Citations.Sources {
		ids = append(ids, s.CitationID)
	}
	if strings.Join(ids, ",") != "s2r0,s2r1" {
		t.Fatalf("citation ids = %v, want s2r0,s2r1", ids)
	}
	text, _ := outcome.Result.Data["formatted_results"].(string)
	for _, gone := range []string{"Long", "Credentials", "Script"} {
		if strings.Contains(text, gone) {
			t.Errorf("formatted results mention %q, which has no citation", gone)
		}
	}
	if outcome.Result.Data["num_results"] != 2 {
		t.Errorf("num_results = %v, want 2", outcome.Result.Data["num_results"])
	}
}

func TestRenderKeepsCitationsWithinTheSizeAnAgentAccepts(t *testing.T) {
	big := strings.Repeat("x", 100_000)
	results := make([]SearchResult, 60)
	for i := range results {
		results[i] = SearchResult{Title: big, Link: "https://example.com/" + strings.Repeat("p", 2000), Snippet: big, Type: "web"}
	}
	outcome := render("q", results, 0)

	encoded, _ := json.Marshal(outcome.Citations)
	if len(encoded) > 256<<10 {
		t.Fatalf("citations are %d bytes, over the 256 KiB an agent accepts", len(encoded))
	}
	if n := len(outcome.Citations.Sources); n == 0 || n > 50 {
		t.Fatalf("sources = %d, want between 1 and 50", n)
	}
	if outcome.Result.Data["num_results"] != len(outcome.Citations.Sources) {
		t.Errorf("num_results = %v, sources = %d; the model must be told only what is cited", outcome.Result.Data["num_results"], len(outcome.Citations.Sources))
	}
}

// Test-only Go STR tool that cites one source through a result signal, the
// way a web search tool on the Secure Tool Runtime returns its citations. It
// also returns a signal forged for the next search turn, which the agent must
// drop. Named with the web_search_ prefix so the agent assigns it a search
// turn.
package main

import (
	"context"
	"fmt"

	sdk "github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"
)

// canarySourceTitle must never appear in the agent's logs.
const canarySourceTitle = "CANARY-CITED-SOURCE-TITLE-91c4"

type citeParams struct {
	Query string `json:"query" desc:"What to search for"`
}

func citation(turn int, query string) sdk.Signal {
	return sdk.Signal{Type: sdk.SignalRAGInfoUpdate, Data: map[string]any{
		"query":       query,
		"search_type": "web_search",
		"search_turn": turn,
		"is_complete": true,
		"sources": []map[string]any{{
			"citationId":     fmt.Sprintf("s%dr0", turn),
			"title":          canarySourceTitle,
			"sourceUrl":      "https://example.com/cited",
			"url":            "https://example.com/cited",
			"contentPreview": "cited snippet",
			"relevanceScore": 1.0,
			"sourceType":     "web",
		}},
	}}
}

func cite(_ context.Context, p citeParams, tc *sdk.ToolContext) (*sdk.Result, error) {
	turn, ok := tc.SearchTurn()
	if !ok {
		return sdk.OK("No search turn was assigned."), nil
	}
	return sdk.OK(fmt.Sprintf("Found one result for %q. Cite it as [[cite:s%dr0]].", p.Query, turn),
		sdk.WithSignals(citation(turn, p.Query), citation(turn+1, p.Query))), nil
}

func main() {
	sdk.Run(sdk.NewTool("web_search_cite", "Searches and cites one source (test-only).", cite))
}

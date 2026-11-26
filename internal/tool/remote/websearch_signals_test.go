// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package remote

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"
	"github.com/SolaceProducts/solace-agent-mesh/tools/go/web_search/websearchtool"
)

func TestTheWebSearchToolsCitationsPassTheAgentsSignalChecks(t *testing.T) {
	t.Parallel()
	tavily := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results": [{"title": "Page", "url": "https://one.example/a", "content": "text"}],
			"images": [{"url": "https://img.example/1.png", "description": "a diagram"}]}`))
	}))
	t.Cleanup(tavily.Close)

	turn := 4
	wire, err := samtoolsdk.InvokeInProcess(t.Context(), websearchtool.Tools(), "web_search",
		map[string]any{"query": "diagram", "search_type": "image"},
		map[string]any{"tool_name": "web_search_1dec0538", "provider": "tavily", "api_key": "K", "api_base": tavily.URL},
		samtoolsdk.InProcessBindings{SearchTurn: &turn})
	if err != nil {
		t.Fatal(err)
	}
	raw := wire.(map[string]any)
	signals, _ := takeResultSignals(raw).([]any)
	if len(signals) != 1 {
		t.Fatalf("result = %v, want one signal", raw)
	}
	got, err := decodeResultSignal(signals[0], &turn)
	if err != nil {
		t.Fatalf("the agent refused the web_search tool's citations: %v", err)
	}
	update := got.(a2a.RAGInfoUpdateData)
	if len(update.Sources) == 0 {
		t.Fatal("no sources")
	}
	for _, s := range update.Sources {
		if _, kept := s.Metadata["favicon"]; kept {
			t.Errorf("%s kept its favicon", s.CitationID)
		}
	}
}

func TestOneBadResultDoesNotCostTheSearchItsCitations(t *testing.T) {
	t.Parallel()
	long := "https://track.example/" + strings.Repeat("x", 2100)
	tavily := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results": [
			{"title": "Good", "url": "https://one.example/a", "content": "text"},
			{"title": "Tracked", "url": "` + long + `", "content": "text"},
			{"title": "Credentials", "url": "https://user:pw@two.example/b", "content": "text"},
			{"title": "Also good", "url": "https://three.example/c", "content": "text"}]}`))
	}))
	t.Cleanup(tavily.Close)

	turn := 1
	wire, err := samtoolsdk.InvokeInProcess(t.Context(), websearchtool.Tools(), "web_search",
		map[string]any{"query": "q"},
		map[string]any{"tool_name": "web_search_1dec0538", "provider": "tavily", "api_key": "K", "api_base": tavily.URL},
		samtoolsdk.InProcessBindings{SearchTurn: &turn})
	if err != nil {
		t.Fatal(err)
	}
	signals, _ := takeResultSignals(wire.(map[string]any)).([]any)
	if len(signals) != 1 {
		t.Fatalf("signals = %v, want one", signals)
	}
	got, err := decodeResultSignal(signals[0], &turn)
	if err != nil {
		t.Fatalf("the agent refused the search's citations: %v", err)
	}
	if n := len(got.(a2a.RAGInfoUpdateData).Sources); n != 2 {
		t.Fatalf("sources = %d, want the 2 the chat can show", n)
	}
}

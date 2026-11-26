// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"reflect"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
)

func dataMessage(data map[string]any) *a2a.Message {
	return &a2a.Message{Parts: []a2a.Part{{Kind: "data", Data: data}}}
}

func TestCitationSourcesFromMessage_WebSearchSignal(t *testing.T) {
	msg := dataMessage(map[string]any{
		"type":        "rag_info_update",
		"search_type": "web_search",
		"sources": []any{
			map[string]any{"citationId": "s0r0", "sourceUrl": "https://www.cnn.com/markets", "url": "https://other.example/", "title": "CNN Markets"},
			map[string]any{"citationId": "s0r1", "url": "https://example.com/a"},
			map[string]any{"citationId": "s0r2", "metadata": map[string]any{"link": "https://example.org/b", "title": "From metadata"}},
			map[string]any{"title": "no id, so nothing can cite it"},
		},
	})

	got := CitationSourcesFromMessage(msg)

	want := []CitationSource{
		{ID: "s0r0", URL: "https://www.cnn.com/markets", Title: "CNN Markets"},
		{ID: "s0r1", URL: "https://example.com/a"},
		{ID: "s0r2", URL: "https://example.org/b", Title: "From metadata"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("sources = %+v, want %+v", got, want)
	}
}

func TestCitationSourcesFromMessage_ToolResultEnvelope(t *testing.T) {
	msg := dataMessage(map[string]any{
		"type": "tool_result",
		"result_data": map[string]any{"rag_metadata": map[string]any{
			"searchType": "document_search",
			"sources":    []any{map[string]any{"citationId": "idx0r0", "filename": "guide.pdf", "title": "Guide"}},
		}},
	})

	got := CitationSourcesFromMessage(msg)

	if want := []CitationSource{{ID: "idx0r0", Title: "Guide"}}; !reflect.DeepEqual(got, want) {
		t.Errorf("sources = %+v, want %+v", got, want)
	}
}

func TestCitationSourcesFromMessage_NoValueIsNotAnAddress(t *testing.T) {
	msg := dataMessage(map[string]any{
		"type": "rag_info_update",
		"sources": []any{
			map[string]any{"citationId": "research0", "sourceUrl": "N/A", "title": "n/a"},
			map[string]any{"citationId": "research1", "sourceUrl": "N/A", "url": "https://example.com/r"},
		},
	})

	got := CitationSourcesFromMessage(msg)

	want := []CitationSource{{ID: "research0"}, {ID: "research1", URL: "https://example.com/r"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("sources = %+v, want %+v", got, want)
	}
}

func TestCitationSourcesFromMessage_EmptyValuesFallThrough(t *testing.T) {
	msg := dataMessage(map[string]any{
		"type": "rag_info_update",
		"sources": []any{
			map[string]any{"citationId": "s0r0", "sourceUrl": "", "url": "https://example.com/a", "title": "", "metadata": map[string]any{"title": "From metadata"}},
			map[string]any{"citationId": "s0r1", "sourceUrl": " N/A ", "metadata": map[string]any{"link": "https://example.com/b"}},
			map[string]any{"citationId": "s0r2", "url": "https://example.com/c", "metadata": map[string]any{"link": "https://example.org/other"}},
		},
	})

	got := CitationSourcesFromMessage(msg)

	want := []CitationSource{
		{ID: "s0r0", URL: "https://example.com/a", Title: "From metadata"},
		{ID: "s0r1", URL: "https://example.com/b"},
		{ID: "s0r2", URL: "https://example.com/c"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("sources = %+v, want %+v", got, want)
	}
}

func TestCitationSourcesFromMessage_NothingToReport(t *testing.T) {
	for name, msg := range map[string]*a2a.Message{
		"no message":   nil,
		"text only":    {Parts: []a2a.Part{{Kind: "text", Text: "hello"}}},
		"other data":   dataMessage(map[string]any{"type": "agent_progress_update", "status_text": "working"}),
		"empty search": dataMessage(map[string]any{"type": "rag_info_update", "sources": []any{}}),
	} {
		if got := CitationSourcesFromMessage(msg); len(got) != 0 {
			t.Errorf("%s: sources = %+v, want none", name, got)
		}
	}
}

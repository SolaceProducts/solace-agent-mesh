// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"strconv"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/indexing"
	"github.com/SolaceProducts/solace-agent-mesh/internal/indexing/convert"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func TestFormatLocationString(t *testing.T) {
	tests := []struct {
		location     string
		citationType string
		want         string
	}{
		{"physical_page_3", "page", "Page 3"},
		{"physical_paragraph_5", "paragraph", "Paragraph 5"},
		{"physical_slide_2", "slide", "Slide 2"},
		{"lines_1_50", "line_range", "Lines 1_50"},
		{"unknown", "page", "unknown"},
		{"", "page", ""},
	}
	for _, tc := range tests {
		got := formatLocationString(tc.location, tc.citationType)
		if got != tc.want {
			t.Errorf("formatLocationString(%q, %q) = %q, want %q", tc.location, tc.citationType, got, tc.want)
		}
	}
}

func TestFormatLocationRange(t *testing.T) {
	chunk := indexing.ManifestChunk{
		CitationType: "page",
		CitationMap: []convert.CitationRange{
			{Location: "physical_page_3", CharStart: 0, CharEnd: 1000},
		},
	}
	if got := formatLocationRange(chunk); got != "Page 3" {
		t.Errorf("single page: got %q", got)
	}

	chunk.CitationMap = []convert.CitationRange{
		{Location: "physical_page_3", CharStart: 0, CharEnd: 1000},
		{Location: "physical_page_4", CharStart: 1000, CharEnd: 2000},
		{Location: "physical_page_5", CharStart: 2000, CharEnd: 3000},
	}
	if got := formatLocationRange(chunk); got != "Pages 3-5" {
		t.Errorf("multiple pages: got %q", got)
	}

	chunk.CitationMap = nil
	if got := formatLocationRange(chunk); got != "Unknown location" {
		t.Errorf("empty: got %q", got)
	}
}

func TestCitationIDSequencing(t *testing.T) {
	builder := indexing.NewIndexBuilder(2000, 500)
	builder.AddDocument(indexing.Document{
		Filename: "doc1.txt",
		Version:  1,
		Conversion: &convert.Result{
			Text:         "machine learning artificial intelligence neural networks deep learning",
			CitationType: "line_range",
			Citations:    []convert.CitationRange{{Location: "lines_1_1", CharStart: 0, CharEnd: 70}},
		},
	})
	builder.AddDocument(indexing.Document{
		Filename: "doc2.txt",
		Version:  1,
		Conversion: &convert.Result{
			Text:         "quantum computing physics research laboratory experiments",
			CitationType: "line_range",
			Citations:    []convert.CitationRange{{Location: "lines_1_1", CharStart: 0, CharEnd: 57}},
		},
	})

	built, err := builder.Build("test")
	if err != nil {
		t.Fatal(err)
	}

	zipData, err := indexing.SerializeToZIP(built)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := indexing.DeserializeFromZIP(zipData)
	if err != nil {
		t.Fatal(err)
	}

	results := loaded.Index.Search([]string{"machine", "learning"}, 5)
	if len(results) == 0 {
		t.Fatal("no results")
	}
	if results[0].DocIndex != 0 {
		t.Errorf("expected doc 0 first, got %d", results[0].DocIndex)
	}

	searchTurn := 0
	var formatted []idxSearchResult
	for i, r := range results {
		if r.Score <= 0 {
			continue
		}
		chunk := loaded.Manifest.Chunks[r.DocIndex]
		formatted = append(formatted, idxSearchResult{
			CitationID:    "idx0r" + strconv.Itoa(i),
			ChunkText:     chunk.ChunkText,
			SourceFile:    chunk.Filename,
			Score:         r.Score,
			LocationRange: formatLocationRange(chunk),
		})
	}
	_ = searchTurn

	if len(formatted) > 0 && formatted[0].CitationID != "idx0r0" {
		t.Errorf("first citation: got %q, want 'idx0r0'", formatted[0].CitationID)
	}
}

func TestFormatResultsForLLM(t *testing.T) {
	results := []idxSearchResult{
		{
			CitationID:     "idx0r0",
			ChunkText:      "Machine learning is great.",
			SourceFile:     "report.pdf",
			Score:          1.5,
			RelevanceScore: 1.0,
			LocationRange:  "Page 3",
		},
		{
			CitationID:     "idx0r1",
			ChunkText:      "Deep learning advances.",
			SourceFile:     "paper.docx",
			Score:          0.8,
			RelevanceScore: 0.5,
			LocationRange:  "Paragraph 5",
		},
	}

	output := formatResultsForLLM("machine learning", 0, results, []string{"idx0r0", "idx0r1"})

	checks := []string{
		"=== DOCUMENT SEARCH RESULTS (Turn 0) ===",
		"Query: machine learning",
		"Valid citation IDs: idx0r0, idx0r1",
		"Total results: 2",
		"[[cite:idx0r0]]",
		"[[cite:idx0r1]]",
		"SOURCE FILE: report.pdf",
		"SOURCE FILE: paper.docx",
		"LOCATION: Page 3",
		"LOCATION: Paragraph 5",
		"RELEVANCE SCORE: 100.0 (0-100)",
		"RELEVANCE SCORE: 50.0 (0-100)",
		"IMPORTANT CITATION RULES:",
	}
	for _, s := range checks {
		if !strings.Contains(output, s) {
			t.Errorf("output missing %q", s)
		}
	}
}

func TestIndexSearch_TurnCounterIncrementsPerTask(t *testing.T) {
	tool := &IndexSearchTool{}

	if got := tool.nextTurn("task-A"); got != 0 {
		t.Errorf("first call for task-A: got %d, want 0", got)
	}
	if got := tool.nextTurn("task-A"); got != 1 {
		t.Errorf("second call for task-A: got %d, want 1", got)
	}
	if got := tool.nextTurn("task-A"); got != 2 {
		t.Errorf("third call for task-A: got %d, want 2", got)
	}

	if got := tool.nextTurn("task-B"); got != 0 {
		t.Errorf("first call for task-B: got %d, want 0", got)
	}
	if got := tool.nextTurn("task-B"); got != 1 {
		t.Errorf("second call for task-B: got %d, want 1", got)
	}

	if got := tool.nextTurn("task-A"); got != 3 {
		t.Errorf("fourth call for task-A: got %d, want 3", got)
	}
}

func TestIndexSearch_ReturnsRAGMetadata(t *testing.T) {
	builder := indexing.NewIndexBuilder(2000, 500)
	builder.AddDocument(indexing.Document{
		Filename: "whitepaper.txt",
		Version:  1,
		Conversion: &convert.Result{
			Text:         "revenue grew forty two percent year over year",
			CitationType: "line_range",
			Citations:    []convert.CitationRange{{Location: "lines_1_1", CharStart: 0, CharEnd: 47}},
		},
	})
	built, err := builder.Build("test")
	if err != nil {
		t.Fatal(err)
	}
	zipData, err := indexing.SerializeToZIP(built)
	if err != nil {
		t.Fatal(err)
	}

	svc := newMockArtifactService()
	toolCtx := newTestToolCtx(svc)
	toolCtx.ProjectID = "proj-123"
	toolCtx.ParentTaskID = "task-xyz"
	ctx := t.Context()

	loc := toolCtx.ArtifactService.Resolve("test-agent", "user-1", "session-1", "project_bm25_index.zip")
	if _, saveErr := svc.Save(ctx, loc, zipData, "application/zip"); saveErr != nil {
		t.Fatalf("save index: %v", saveErr)
	}

	idx := &IndexSearchTool{}
	res, err := idx.Execute(ctx, map[string]any{"query": "revenue"}, toolCtx)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Status != tool.StatusSuccess {
		t.Fatalf("status = %q, message = %q", res.Status, res.Message)
	}

	rag, ok := res.Data["rag_metadata"].(map[string]any)
	if !ok {
		t.Fatalf("rag_metadata missing or wrong type: %T", res.Data["rag_metadata"])
	}
	if st, _ := rag["searchType"].(string); st != "document_search" {
		t.Errorf("searchType = %q, want %q", st, "document_search")
	}
	if q, _ := rag["query"].(string); q != "revenue" {
		t.Errorf("query = %q, want %q", q, "revenue")
	}
	if tid, _ := rag["taskId"].(string); tid != "task-xyz" {
		t.Errorf("taskId = %q, want %q", tid, "task-xyz")
	}
	if turn, isInt := rag["turnNumber"].(int); !isInt || turn != 0 {
		t.Errorf("turnNumber = %v (%T), want int 0", rag["turnNumber"], rag["turnNumber"])
	}
	sources, ok := rag["sources"].([]a2a.RAGSource)
	if !ok {
		t.Fatalf("sources type = %T, want []a2a.RAGSource", rag["sources"])
	}
	if len(sources) == 0 {
		t.Fatal("expected at least one RAG source")
	}
	if sources[0].CitationID == "" {
		t.Error("source[0].CitationID is empty")
	}
	if sources[0].SourceType != "document" {
		t.Errorf("source[0].SourceType = %q, want %q", sources[0].SourceType, "document")
	}
}

func TestIndexSearch_NoIndexYet(t *testing.T) {
	svc := newMockArtifactService()
	toolCtx := newTestToolCtx(svc)
	toolCtx.ProjectID = "fresh-project"
	toolCtx.ParentTaskID = "task-fresh"
	ctx := t.Context()

	idx := &IndexSearchTool{}
	res, err := idx.Execute(ctx, map[string]any{"query": "anything"}, toolCtx)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Status != tool.StatusError {
		t.Fatalf("expected error status for missing index, got %q", res.Status)
	}
	if got := res.Message; got == "" || got == "Failed to load document index: open zip: zip: not a valid zip file" {
		t.Errorf("unexpected error message %q", got)
	}
	if got := res.Message; !strings.Contains(got, "No document index found") {
		t.Errorf("expected 'No document index found' message, got %q", got)
	}
}

func TestIndexSearch_ZeroResultEnvelope(t *testing.T) {
	builder := indexing.NewIndexBuilder(2000, 500)
	builder.AddDocument(indexing.Document{
		Filename: "doc.txt",
		Version:  1,
		Conversion: &convert.Result{
			Text:         "completely unrelated content about cooking",
			CitationType: "line_range",
			Citations:    []convert.CitationRange{{Location: "lines_1_1", CharStart: 0, CharEnd: 42}},
		},
	})
	built, err := builder.Build("test")
	if err != nil {
		t.Fatal(err)
	}
	zipData, err := indexing.SerializeToZIP(built)
	if err != nil {
		t.Fatal(err)
	}

	svc := newMockArtifactService()
	toolCtx := newTestToolCtx(svc)
	toolCtx.ProjectID = "proj-empty"
	toolCtx.ParentTaskID = "task-empty"
	ctx := t.Context()

	loc := toolCtx.ArtifactService.Resolve("test-agent", "user-1", "session-1", "project_bm25_index.zip")
	if _, saveErr := svc.Save(ctx, loc, zipData, "application/zip"); saveErr != nil {
		t.Fatalf("save index: %v", saveErr)
	}

	idx := &IndexSearchTool{}

	assertEmptyEnvelope := func(t *testing.T, query string) {
		t.Helper()
		res, err := idx.Execute(ctx, map[string]any{"query": query}, toolCtx)
		if err != nil {
			t.Fatalf("Execute: %v", err)
		}
		if res.Status != tool.StatusSuccess {
			t.Fatalf("status = %q, message = %q", res.Status, res.Message)
		}
		rag, ok := res.Data["rag_metadata"].(map[string]any)
		if !ok {
			t.Fatalf("rag_metadata missing or wrong type: %T", res.Data["rag_metadata"])
		}
		if st, _ := rag["searchType"].(string); st != "document_search" {
			t.Errorf("searchType = %q, want %q", st, "document_search")
		}
		sources, ok := rag["sources"].([]a2a.RAGSource)
		if !ok {
			t.Fatalf("sources type = %T, want []a2a.RAGSource", rag["sources"])
		}
		if len(sources) != 0 {
			t.Errorf("sources = %v, want empty slice", sources)
		}
		if n, _ := res.Data["num_results"].(int); n != 0 {
			t.Errorf("num_results = %d, want 0", n)
		}
	}

	t.Run("empty_tokens", func(t *testing.T) {
		assertEmptyEnvelope(t, "!!!???")
	})

	t.Run("zero_filtered_results", func(t *testing.T) {
		assertEmptyEnvelope(t, "quantum cryptography blockchain")
	})
}

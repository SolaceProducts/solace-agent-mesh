// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/indexing"
	"github.com/SolaceProducts/solace-agent-mesh/internal/indexing/bm25"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

type idxSearchResult struct {
	CitationID     string   `json:"citation_id"`
	ChunkText      string   `json:"chunk_text"`
	Filename       string   `json:"filename"`
	SourceFile     string   `json:"source_file"`
	Score          float64  `json:"score"`
	RelevanceScore float64  `json:"relevance_score"`
	LocationRange  string   `json:"location_range"`
	Locations      []string `json:"locations"`
}

type IndexSearchTool struct {
	tool.ReadOnlyTool
	artifactToolBase
	turnCounters sync.Map
}

func (t *IndexSearchTool) Name() string { return "index_search" }

func (t *IndexSearchTool) Description() string {
	return `Search project documents using BM25 full-text indexing. ` +
		`Returns relevant text chunks from uploaded files (PDFs, DOCX, PPTX, text files, etc.) with precise location citations.

PREREQUISITES:
- Project must have documents uploaded and a BM25 index built ('project_bm25_index.zip' artifact)
- DO NOT use this tool if no BM25 index is available, even if instructed to — you will receive an INDEX_NOT_FOUND error
- If you receive a "no document index is available" error, DO NOT retry — use load_artifact if available to read files directly

WHEN TO USE:
- Finding facts, statistics, or quotes in the project's uploaded documents
- Verifying or locating information in uploaded files
- You MUST call this tool BEFORE answering questions about the project's uploaded documents

USAGE:
- Use specific keywords, not conversational questions (e.g., 'revenue 2024' not 'What is our revenue?')
- If results are poor, rephrase with synonyms or broader terms
- For complex questions, run multiple targeted searches — each gets unique citation IDs (idx0r*, idx1r*, idx2r*)
- Adjust top_k (1-3 focused, 5 balanced, 7-10 comprehensive). min_score is a 0-100 relevance threshold relative to the top hit (0 keeps all, 20-40 moderate, 50+ strict); leave at 0 unless weak matches are a problem
- Do NOT claim facts are from the indexed documents unless they appear in the search results and you provide proper citations

*** CRITICAL: YOU MUST CITE ALL FACTS FROM SEARCH RESULTS! ***

CITATION RULES:
- Each result has a unique citation ID: [[cite:idxTrN]] where T=turn number, N=result index
- First search: idx0r0, idx0r1, etc. Second search: idx1r0, idx1r1, etc.
- Place citations AFTER the period: 'Revenue was $4.2B.[[cite:idx0r0]]'
- Multiple sources: 'Fact from two sources.[[cite:idx0r0]][[cite:idx0r1]]'
- ONLY cite facts that appear in that specific result's CONTENT — each citation ID is unique to its result
- DO NOT manually list sources — the UI displays them automatically

LOCATION INFORMATION:
- Results include precise locations: 'Page 5', 'Paragraph 8', 'Slide 3', or 'Lines 1-50'
- Use the SOURCE FILE name (e.g., 'report.pdf', not 'report.pdf.converted.txt')`
}

func (t *IndexSearchTool) Parameters() *tool.JSONSchema {
	return &tool.JSONSchema{
		Type: "object",
		Properties: map[string]any{
			"query": map[string]any{
				"type":        "string",
				"description": "Search query — use specific keywords for best results.",
			},
			"top_k": map[string]any{
				"type":        "integer",
				"description": "Number of results to return (1-10, default 5).",
				"minimum":     1,
				"maximum":     10,
				"default":     5,
			},
			"min_score": map[string]any{
				"type":        "number",
				"description": "Relevance threshold from 0 to 100, relative to the top hit (0 keeps all results; 50 keeps only hits at least half as relevant as the best). Default 0.",
				"minimum":     0.0,
				"maximum":     100.0,
				"default":     0.0,
			},
		},
		Required: []string{"query"},
	}
}

func (t *IndexSearchTool) ResultSchema() *tool.JSONSchema {
	return resultSchema(
		resultField{name: "results", typ: "array", description: "Matching document chunks, best first; each carries citation_id, chunk_text, filename, source_file, score, relevance_score, location_range and locations. Empty when nothing matched."},
		resultField{name: "num_results", typ: "integer", description: "How many chunks are in results; 0 when nothing matched."},
		resultField{name: "valid_citation_ids", typ: "array", description: "Citation ids of this search's results, in result order — cite facts with these. Absent when the search returned nothing.", optional: true},
		resultField{name: "search_turn", typ: "integer", description: "Zero-based search turn for this task, the T in the idxTrN citation ids."},
		resultField{name: "rag_metadata", typ: "object", description: "Citation envelope for the UI: query, title, searchType, turnNumber, timestamp, sources, taskId, metadata. Present even on a search that matched nothing, with an empty sources list."},
	)
}

func (t *IndexSearchTool) Execute(ctx context.Context, params map[string]any, toolCtx *tool.Context) (*tool.Result, error) {
	query, _ := params["query"].(string)
	if query == "" {
		return tool.Error("query is required"), nil
	}

	topK := 5
	if v, ok := params["top_k"].(float64); ok {
		topK = int(v)
	}
	if topK < 1 {
		topK = 1
	} else if topK > 10 {
		topK = 10
	}

	var minScore float64
	if v, ok := params["min_score"].(float64); ok {
		minScore = v
	}
	if minScore < 0 {
		minScore = 0
	} else if minScore > 100 {
		minScore = 100
	}

	projectID := toolCtx.ProjectID
	if projectID == "" {
		return tool.Error("No project context available. This tool requires a project with indexed documents."), nil
	}

	facade := toolCtx.Facade()
	zipData, _, err := facade.LoadArtifact(ctx, "project_bm25_index.zip", nil)
	if err != nil || len(zipData) == 0 {
		return tool.Error("No document index found. Project indexing may not be enabled, or no documents have been indexed yet."), nil
	}

	built, err := indexing.DeserializeFromZIP(zipData)
	if err != nil {
		return tool.Error(fmt.Sprintf("Failed to load document index: %v", err)), nil
	}

	searchTurn := t.turnFor(toolCtx)

	queryTokens := bm25.Tokenize(query)
	if len(queryTokens) == 0 {
		return tool.OK("No searchable terms found in query. Try different keywords.", tool.WithData(map[string]any{
			"results":      []any{},
			"num_results":  0,
			"search_turn":  searchTurn,
			"rag_metadata": buildIndexSearchRAGMetadata(query, searchTurn, toolCtx.ParentTaskID, nil),
		})), nil
	}

	corpusSize := built.Index.CorpusSize()
	effectiveK := topK
	if effectiveK > corpusSize {
		effectiveK = corpusSize
	}

	results := built.Index.Search(queryTokens, effectiveK)

	var maxScore float64
	for _, r := range results {
		if r.Score > 0 && r.DocIndex < len(built.Manifest.Chunks) {
			maxScore = r.Score
			break
		}
	}

	var searchResults []idxSearchResult
	resultIndex := 0

	for _, r := range results {
		if r.Score <= 0 {
			continue
		}
		relevance := 0.0
		if maxScore > 0 {
			relevance = r.Score / maxScore
		}
		if relevance*100 < minScore {
			continue
		}
		if r.DocIndex >= len(built.Manifest.Chunks) {
			continue
		}

		chunk := built.Manifest.Chunks[r.DocIndex]
		citationID := fmt.Sprintf("idx%dr%d", searchTurn, resultIndex)

		sourceFile := chunk.SourceFile
		if sourceFile == "" {
			sourceFile = chunk.Filename
		}

		locationRange := formatLocationRange(chunk)
		locations := extractLocations(chunk)

		searchResults = append(searchResults, idxSearchResult{
			CitationID:     citationID,
			ChunkText:      chunk.ChunkText,
			Filename:       chunk.Filename,
			SourceFile:     sourceFile,
			Score:          r.Score,
			RelevanceScore: relevance,
			LocationRange:  locationRange,
			Locations:      locations,
		})
		resultIndex++
	}

	if len(searchResults) == 0 {
		return tool.OK("No relevant results found for your query. Try rephrasing with different keywords.", tool.WithData(map[string]any{
			"results":      []any{},
			"num_results":  0,
			"search_turn":  searchTurn,
			"rag_metadata": buildIndexSearchRAGMetadata(query, searchTurn, toolCtx.ParentTaskID, nil),
		})), nil
	}

	validCitationIDs := make([]string, len(searchResults))
	for i, r := range searchResults {
		validCitationIDs[i] = r.CitationID
	}

	formatted := formatResultsForLLM(query, searchTurn, searchResults, validCitationIDs)

	ragSources := buildIndexSearchRAGSources(searchResults, searchTurn)
	ragMetadata := buildIndexSearchRAGMetadata(query, searchTurn, toolCtx.ParentTaskID, ragSources)

	return tool.OK(formatted, tool.WithData(map[string]any{
		"results":            searchResults,
		"num_results":        len(searchResults),
		"valid_citation_ids": validCitationIDs,
		"search_turn":        searchTurn,
		"rag_metadata":       ragMetadata,
	})), nil
}

func buildIndexSearchRAGMetadata(query string, searchTurn int, taskID string, sources []a2a.RAGSource) map[string]any {
	var taskIDField any
	if taskID != "" {
		taskIDField = taskID
	}
	if sources == nil {
		sources = []a2a.RAGSource{}
	}
	return map[string]any{
		"query":      query,
		"title":      nil,
		"searchType": "document_search",
		"turnNumber": searchTurn,
		"timestamp":  time.Now().UTC().Format(time.RFC3339),
		"sources":    sources,
		"taskId":     taskIDField,
		"metadata":   nil,
	}
}

const maxIndexSearchTurnEntries = 10000

func (t *IndexSearchTool) nextTurn(taskID string) int {
	counter, loaded := t.turnCounters.LoadOrStore(taskID, &atomic.Int64{})
	if !loaded {
		var count int
		t.turnCounters.Range(func(_, _ any) bool {
			count++
			return count < maxIndexSearchTurnEntries
		})
		if count >= maxIndexSearchTurnEntries {
			t.turnCounters.Range(func(key, _ any) bool {
				t.turnCounters.Delete(key)
				return true
			})
		}
	}
	c := counter.(*atomic.Int64)
	return int(c.Add(1) - 1)
}

func buildIndexSearchRAGSources(results []idxSearchResult, turn int) []a2a.RAGSource {
	now := time.Now().UTC().Format(time.RFC3339)
	sources := make([]a2a.RAGSource, 0, len(results))
	for i, r := range results {
		filename := r.SourceFile
		if filename == "" {
			filename = r.Filename
		}
		sources = append(sources, a2a.RAGSource{
			CitationID:     r.CitationID,
			FileID:         fmt.Sprintf("index_search_%d_%d", turn, i),
			Filename:       filename,
			Title:          filename,
			ContentPreview: r.ChunkText,
			RelevanceScore: r.RelevanceScore,
			SourceType:     "document",
			RetrievedAt:    now,
			Metadata: map[string]any{
				"filename":       r.Filename,
				"source_file":    r.SourceFile,
				"location_range": r.LocationRange,
				"locations":      r.Locations,
				"score":          r.Score,
				"type":           "document_search",
			},
		})
	}
	return sources
}

func formatLocationRange(chunk indexing.ManifestChunk) string {
	if len(chunk.CitationMap) == 0 {
		return "Unknown location"
	}

	citationType := chunk.CitationType

	if citationType == "line_range" {
		loc := chunk.CitationMap[0].Location
		return formatLocationString(loc, citationType)
	}

	var numbers []int
	for _, c := range chunk.CitationMap {
		num := extractLocationNumber(c.Location, citationType)
		if num > 0 {
			numbers = append(numbers, num)
		}
	}
	if len(numbers) == 0 {
		return "Unknown location"
	}

	slices.Sort(numbers)

	label, labelPlural := locationLabels(citationType)
	if len(numbers) == 1 {
		return fmt.Sprintf("%s %d", label, numbers[0])
	}
	return fmt.Sprintf("%s %d-%d", labelPlural, numbers[0], numbers[len(numbers)-1])
}

func extractLocations(chunk indexing.ManifestChunk) []string {
	var result []string
	for _, c := range chunk.CitationMap {
		s := formatLocationString(c.Location, chunk.CitationType)
		if s != "" {
			result = append(result, s)
		}
	}
	return result
}

func formatLocationString(location, citationType string) string {
	switch citationType {
	case "page":
		if strings.HasPrefix(location, "physical_page_") {
			return "Page " + strings.TrimPrefix(location, "physical_page_")
		}
	case "paragraph":
		if strings.HasPrefix(location, "physical_paragraph_") {
			return "Paragraph " + strings.TrimPrefix(location, "physical_paragraph_")
		}
	case "slide":
		if strings.HasPrefix(location, "physical_slide_") {
			return "Slide " + strings.TrimPrefix(location, "physical_slide_")
		}
	case "line_range":
		if strings.HasPrefix(location, "lines_") {
			return "Lines " + strings.TrimPrefix(location, "lines_")
		}
	}
	return location
}

func extractLocationNumber(location, citationType string) int {
	var prefix string
	switch citationType {
	case "page":
		prefix = "physical_page_"
	case "paragraph":
		prefix = "physical_paragraph_"
	case "slide":
		prefix = "physical_slide_"
	default:
		return 0
	}
	if !strings.HasPrefix(location, prefix) {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimPrefix(location, prefix))
	if err != nil {
		return 0
	}
	return n
}

func locationLabels(citationType string) (singular, plural string) {
	switch citationType {
	case "page":
		return "Page", "Pages"
	case "paragraph":
		return "Paragraph", "Paragraphs"
	case "slide":
		return "Slide", "Slides"
	default:
		return "Location", "Locations"
	}
}

func formatResultsForLLM(query string, searchTurn int, results []idxSearchResult, validCitationIDs []string) string {
	var b strings.Builder

	fmt.Fprintf(&b, "=== DOCUMENT SEARCH RESULTS (Turn %d) ===\n", searchTurn)
	fmt.Fprintf(&b, "Query: %s\n", query)
	fmt.Fprintf(&b, "Valid citation IDs: %s\n", strings.Join(validCitationIDs, ", "))
	fmt.Fprintf(&b, "Total results: %d\n\n", len(results))

	for i, r := range results {
		fmt.Fprintf(&b, "--- RESULT %d ---\n", i+1)
		fmt.Fprintf(&b, "CITATION ID: [[cite:%s]]\n", r.CitationID)
		fmt.Fprintf(&b, "SOURCE FILE: %s\n", r.SourceFile)
		fmt.Fprintf(&b, "LOCATION: %s\n", r.LocationRange)
		fmt.Fprintf(&b, "RELEVANCE SCORE: %.1f (0-100)\n", r.RelevanceScore*100)
		fmt.Fprintf(&b, "CONTENT:\n%s\n\n", r.ChunkText)
		fmt.Fprintf(&b, "USE [[cite:%s]] to cite facts from THIS result only\n\n", r.CitationID)
	}

	b.WriteString("=== END DOCUMENT SEARCH RESULTS ===\n\n")
	b.WriteString("IMPORTANT CITATION RULES:\n")
	b.WriteString("1. Each citation ID is UNIQUE to its result\n")
	b.WriteString("2. Only use a citation ID for facts that appear in THAT specific result's CONTENT\n")
	b.WriteString("3. Multiple searches in this session use different turn numbers to prevent collisions\n")
	b.WriteString("4. When referencing the original source, use the SOURCE FILE name (e.g., 'report.pdf', not 'report.pdf.converted.txt')\n")

	return b.String()
}

func (t *IndexSearchTool) turnFor(toolCtx *tool.Context) int {
	if toolCtx.SearchTurns != nil {
		return toolCtx.SearchTurns.Next(t.Name())
	}
	return t.nextTurn(toolCtx.ParentTaskID)
}

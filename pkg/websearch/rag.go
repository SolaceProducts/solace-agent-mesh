// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package websearch

import (
	"fmt"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
)

func formatCitationID(turn, index int) string {
	return fmt.Sprintf("s%dr%d", turn, index)
}

func formatImageCitationID(turn, index int) string {
	return fmt.Sprintf("img%dr%d", turn, index)
}

func buildRAGSources(results []SearchResult, turn int) []a2a.RAGSource {
	now := time.Now().UTC().Format(time.RFC3339)
	sources := make([]a2a.RAGSource, 0, len(results))
	for i, r := range results {
		citationID := formatCitationID(turn, i)
		title := citationTitle(r.Title)
		filename := citationTitle(r.Attribution)
		if filename == "" {
			filename = title
		}
		favicon := ""
		if r.Link != "" {
			favicon = fmt.Sprintf("https://www.google.com/s2/favicons?domain=%s&sz=32", r.Link)
		}
		sources = append(sources, a2a.RAGSource{
			CitationID:     citationID,
			FileID:         fmt.Sprintf("web_search_%d_%d", turn, i),
			Filename:       filename,
			Title:          title,
			SourceURL:      r.Link,
			URL:            r.Link,
			ContentPreview: citationPreview(r.Snippet),
			RelevanceScore: 1.0,
			SourceType:     "web",
			RetrievedAt:    now,
			Metadata: map[string]any{
				"title":   title,
				"link":    r.Link,
				"type":    "web_search",
				"favicon": favicon,
			},
		})
	}
	return sources
}

func buildImageRAGSources(results []SearchResult, turn int) []a2a.RAGSource {
	now := time.Now().UTC().Format(time.RFC3339)
	sources := make([]a2a.RAGSource, 0, len(results))
	for i, r := range results {
		citationID := formatImageCitationID(turn, i)
		title := citationTitle(r.Title)
		filename := title
		if filename == "" {
			filename = fmt.Sprintf("Image %d", i+1)
		}
		sources = append(sources, a2a.RAGSource{
			CitationID:     citationID,
			FileID:         fmt.Sprintf("web_search_image_%d_%d", turn, i),
			Filename:       filename,
			Title:          title,
			SourceURL:      r.Link,
			URL:            r.Link,
			ContentPreview: title,
			RelevanceScore: 1.0,
			SourceType:     "image",
			RetrievedAt:    now,
			Metadata: map[string]any{
				"title":    title,
				"link":     r.Link,
				"imageUrl": r.ImageURL,
				"type":     "image",
			},
		})
	}
	return sources
}

func formatImageResultsText(results []SearchResult, turn int, query string) string {
	if len(results) == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "=== IMAGE RESULTS (Turn %d) ===\n", turn)
	fmt.Fprintf(&b, "Query: %s\n", query)
	fmt.Fprintf(&b, "%d image(s) found and shown to the user automatically.\n\n", len(results))
	for i, r := range results {
		fmt.Fprintf(&b, "--- IMAGE %d ---\n", i+1)
		fmt.Fprintf(&b, "TITLE: %s\n", r.Title)
		fmt.Fprintf(&b, "PAGE URL: %s\n\n", r.Link)
	}
	b.WriteString("=== END IMAGE RESULTS ===\n\n")
	b.WriteString("The user can already see these images, so do NOT cite them and do not emit citation markers for them. The page URLs above are context for you; quote one only if the user asks where an image came from.\n")
	return b.String()
}

func formatCombinedResultsText(organic, images []SearchResult, turn int, query string) string {
	if len(organic) == 0 && len(images) == 0 {
		return fmt.Sprintf("No results found for query: %s", query)
	}
	var parts []string
	if len(organic) > 0 {
		parts = append(parts, formatSearchResultsText(organic, turn, query))
	}
	if len(images) > 0 {
		parts = append(parts, formatImageResultsText(images, turn, query))
	}
	return strings.Join(parts, "\n")
}

func formatSearchResultsText(results []SearchResult, turn int, query string) string {
	if len(results) == 0 {
		return fmt.Sprintf("No results found for query: %s", query)
	}

	var b strings.Builder

	citationIDs := make([]string, len(results))
	for i := range results {
		citationIDs[i] = formatCitationID(turn, i)
	}

	fmt.Fprintf(&b, "=== SEARCH RESULTS (Turn %d) ===\n", turn)
	fmt.Fprintf(&b, "Query: %s\n", query)
	fmt.Fprintf(&b, "Valid citation IDs: %s\n\n", strings.Join(citationIDs, ", "))

	for i, r := range results {
		citeID := formatCitationID(turn, i)
		fmt.Fprintf(&b, "--- RESULT %d ---\n", i+1)
		fmt.Fprintf(&b, "CITATION ID: [[cite:%s]]\n", citeID)
		fmt.Fprintf(&b, "TITLE: %s\n", r.Title)
		fmt.Fprintf(&b, "URL: %s\n", r.Link)
		fmt.Fprintf(&b, "CONTENT: %s\n", r.Snippet)
		fmt.Fprintf(&b, "USE [[cite:%s]] to cite facts from THIS result only\n\n", citeID)
	}

	b.WriteString("=== END SEARCH RESULTS ===\n\n")
	b.WriteString("IMPORTANT: Each citation ID is UNIQUE to its result.\n")
	b.WriteString("Only use a citation ID for facts that appear in THAT specific result's CONTENT.\n")

	return b.String()
}

const (
	maxCitedSources         = 50
	maxCitationURLLength    = 2048
	maxCitationPreviewBytes = 2000
	maxCitationTitleBytes   = 500
	maxCitationBytes        = 240 << 10
)

func citableResults(results []SearchResult) []SearchResult {
	kept := make([]SearchResult, 0, min(len(results), maxCitedSources))
	for _, r := range results {
		if len(kept) == maxCitedSources {
			break
		}
		if citableURL(r.Link) && (r.Type != searchTypeImage || citableURL(r.ImageURL)) {
			kept = append(kept, r)
		}
	}
	return kept
}

func citableURL(raw string) bool {
	if raw == "" {
		return true
	}
	if len(raw) > maxCitationURLLength {
		return false
	}
	u, err := url.Parse(raw)
	return err == nil && u.Host != "" && u.User == nil && (u.Scheme == "http" || u.Scheme == "https")
}

func citationPreview(s string) string { return boundBytes(s, maxCitationPreviewBytes) }

func citationTitle(s string) string { return boundBytes(s, maxCitationTitleBytes) }

func boundBytes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}

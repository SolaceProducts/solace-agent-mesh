// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
)

type CitationSource struct {
	ID    string
	URL   string
	Title string
}

func CitationSourcesFromMessage(msg *a2a.Message) []CitationSource {
	var out []CitationSource
	for _, envelope := range extractRAGFromParts(msg) {
		sources, _ := envelope["sources"].([]any)
		for _, raw := range sources {
			source, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			id, _ := source["citationId"].(string)
			if id == "" {
				continue
			}
			metadata, _ := source["metadata"].(map[string]any)
			out = append(out, CitationSource{
				ID:    id,
				URL:   firstString(source["sourceUrl"], source["url"], metadata["link"]),
				Title: firstString(source["title"], metadata["title"]),
			})
		}
	}
	return out
}

const noValue = "N/A"

func firstString(values ...any) string {
	for _, v := range values {
		if s, ok := v.(string); ok && s != "" && !strings.EqualFold(strings.TrimSpace(s), noValue) {
			return s
		}
	}
	return ""
}

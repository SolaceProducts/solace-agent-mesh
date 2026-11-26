// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package convert

import (
	"fmt"
	"strings"
)

const LinesPerCitationChunk = 50

func ConvertText(content []byte) (*Result, error) {
	text := string(content)
	if text == "" {
		return &Result{
			Text:         "",
			CitationType: "line_range",
			Citations:    nil,
		}, nil
	}

	lines := strings.Split(text, "\n")
	var citations []CitationRange
	charPos := 0

	for startIdx := 0; startIdx < len(lines); startIdx += LinesPerCitationChunk {
		endIdx := startIdx + LinesPerCitationChunk
		if endIdx > len(lines) {
			endIdx = len(lines)
		}

		chunkLines := lines[startIdx:endIdx]
		chunkText := strings.Join(chunkLines, "\n")

		charStart := charPos
		charEnd := charPos + len(chunkText)

		citations = append(citations, CitationRange{
			Location:  fmt.Sprintf("lines_%d_%d", startIdx+1, endIdx),
			CharStart: charStart,
			CharEnd:   charEnd,
		})

		if endIdx < len(lines) {
			charPos = charEnd + 1
		} else {
			charPos = charEnd
		}
	}

	return &Result{
		Text:         text,
		CitationType: "line_range",
		Citations:    citations,
	}, nil
}

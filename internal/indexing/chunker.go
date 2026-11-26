// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package indexing

const (
	ChunkSizeChars = 2000
	OverlapChars   = 500
)

type Chunk struct {
	Text  string
	Start int
	End   int
}

func ChunkText(text string, chunkSize, overlap int) []Chunk {
	if text == "" {
		return nil
	}

	textLen := len(text)
	var chunks []Chunk
	start := 0

	for start < textLen {
		end := start + chunkSize
		if end > textLen {
			end = textLen
		}

		chunkText := text[start:end]

		if hasNonWhitespace(chunkText) {
			chunks = append(chunks, Chunk{
				Text:  chunkText,
				Start: start,
				End:   end,
			})
		}

		if end == textLen {
			break
		}

		start = end - overlap
	}

	return chunks
}

func hasNonWhitespace(s string) bool {
	for _, c := range s {
		if c != ' ' && c != '\t' && c != '\n' && c != '\r' {
			return true
		}
	}
	return false
}

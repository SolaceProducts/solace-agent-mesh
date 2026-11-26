// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package indexing

import (
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/indexing/bm25"
	"github.com/SolaceProducts/solace-agent-mesh/internal/indexing/convert"
)

type Document struct {
	Filename          string
	SourceFile        string
	Version           int
	SourceFileVersion int
	Conversion        *convert.Result
}

type BuiltIndex struct {
	Index    *bm25.Index
	Manifest *Manifest
	Sources  Sources
}

type IndexBuilder struct {
	docs      []Document
	chunkSize int
	overlap   int
}

func NewIndexBuilder(chunkSize, overlap int) *IndexBuilder {
	return &IndexBuilder{
		chunkSize: chunkSize,
		overlap:   overlap,
	}
}

func (b *IndexBuilder) AddDocument(doc Document) {
	b.docs = append(b.docs, doc)
}

func (b *IndexBuilder) Build(projectID string) (*BuiltIndex, error) {
	var allChunks []ManifestChunk
	var corpus [][]string

	corpusIndex := 0
	for docID, doc := range b.docs {
		text := doc.Conversion.Text
		chunks := ChunkText(text, b.chunkSize, b.overlap)

		for chunkID, chunk := range chunks {
			mc := ManifestChunk{
				CorpusIndex: corpusIndex,
				DocID:       docID,
				Filename:    doc.Filename,
				Version:     doc.Version,
				ChunkID:     chunkID,
				ChunkStart:  chunk.Start,
				ChunkEnd:    chunk.End,
				ChunkText:   chunk.Text,
			}

			if doc.Conversion != nil && len(doc.Conversion.Citations) > 0 {
				mc.CitationType = doc.Conversion.CitationType
				if doc.SourceFile != "" {
					mc.SourceFile = doc.SourceFile
					mc.SourceFileVersion = doc.SourceFileVersion
				}
				mc.CitationMap = overlapCitations(doc.Conversion.Citations, chunk.Start, chunk.End)
			} else {
				mc.CitationType = "text_file"
				mc.CitationMap = []convert.CitationRange{}
			}

			allChunks = append(allChunks, mc)

			tokens := bm25.Tokenize(chunk.Text)
			corpus = append(corpus, tokens)
			corpusIndex++
		}
	}

	idx := bm25.NewIndex(corpus)

	manifest := &Manifest{
		SchemaVersion: "1.0",
		CreatedAt:     time.Now().UTC().Format(time.RFC3339),
		ProjectID:     projectID,
		FileCount:     len(b.docs),
		ChunkCount:    len(allChunks),
		ChunkSize:     b.chunkSize,
		Overlap:       b.overlap,
		Chunks:        allChunks,
	}

	return &BuiltIndex{
		Index:    idx,
		Manifest: manifest,
	}, nil
}

func overlapCitations(citations []convert.CitationRange, chunkStart, chunkEnd int) []convert.CitationRange {
	var result []convert.CitationRange
	for _, c := range citations {
		if c.CharEnd > chunkStart && c.CharStart < chunkEnd {
			result = append(result, c)
		}
	}
	if result == nil {
		result = []convert.CitationRange{}
	}
	return result
}

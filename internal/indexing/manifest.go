// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package indexing

import "github.com/SolaceProducts/solace-agent-mesh/internal/indexing/convert"

type Manifest struct {
	SchemaVersion string          `json:"schema_version"`
	CreatedAt     string          `json:"created_at"`
	ProjectID     string          `json:"project_id"`
	FileCount     int             `json:"file_count"`
	ChunkCount    int             `json:"chunk_count"`
	ChunkSize     int             `json:"chunk_size"`
	Overlap       int             `json:"overlap"`
	Chunks        []ManifestChunk `json:"chunks"`
}

type ManifestChunk struct {
	CorpusIndex       int                     `json:"corpus_index"`
	DocID             int                     `json:"doc_id"`
	Filename          string                  `json:"filename"`
	Version           int                     `json:"version"`
	ChunkID           int                     `json:"chunk_id"`
	ChunkStart        int                     `json:"chunk_start"`
	ChunkEnd          int                     `json:"chunk_end"`
	ChunkText         string                  `json:"chunk_text"`
	CitationType      string                  `json:"citation_type"`
	SourceFile        string                  `json:"source_file,omitempty"`
	SourceFileVersion int                     `json:"source_file_version,omitempty"`
	CitationMap       []convert.CitationRange `json:"citation_map"`
}

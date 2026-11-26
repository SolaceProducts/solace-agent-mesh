// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package indexing

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io"

	"github.com/SolaceProducts/solace-agent-mesh/internal/indexing/bm25"
)

const (
	maxUncompressedSize = 500 << 20
	maxFiles            = 1000
	maxCompressionRatio = 100
)

func SerializeToZIP(built *BuiltIndex) ([]byte, error) {
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)

	manifestJSON, err := json.MarshalIndent(built.Manifest, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal manifest: %w", err)
	}
	f, err := w.Create("manifest.json")
	if err != nil {
		return nil, fmt.Errorf("create manifest entry: %w", err)
	}
	if _, writeErr := f.Write(manifestJSON); writeErr != nil {
		return nil, fmt.Errorf("write manifest: %w", err)
	}

	indexJSON, err := json.Marshal(built.Index)
	if err != nil {
		return nil, fmt.Errorf("marshal index: %w", err)
	}
	f, err = w.Create("index/index.json")
	if err != nil {
		return nil, fmt.Errorf("create index entry: %w", err)
	}
	if _, err = f.Write(indexJSON); err != nil {
		return nil, fmt.Errorf("write index: %w", err)
	}

	sources := built.Sources
	if sources.Files == nil {
		sources.Files = []SourceFile{}
	}
	sourcesJSON, err := json.Marshal(sources)
	if err != nil {
		return nil, fmt.Errorf("marshal sources: %w", err)
	}
	f, err = w.Create(sourcesEntryName)
	if err != nil {
		return nil, fmt.Errorf("create sources entry: %w", err)
	}
	if _, err = f.Write(sourcesJSON); err != nil {
		return nil, fmt.Errorf("write sources: %w", err)
	}

	if err := w.Close(); err != nil {
		return nil, fmt.Errorf("close zip: %w", err)
	}

	return buf.Bytes(), nil
}

func DeserializeFromZIP(data []byte) (*BuiltIndex, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("open zip: %w", err)
	}

	if len(zr.File) > maxFiles {
		return nil, fmt.Errorf("zip contains %d files (max %d)", len(zr.File), maxFiles)
	}

	var manifest Manifest
	var idx bm25.Index
	gotManifest, gotIndex := false, false

	for _, f := range zr.File {
		if f.CompressedSize64 > 0 && f.UncompressedSize64/f.CompressedSize64 > maxCompressionRatio {
			return nil, fmt.Errorf("suspicious compression ratio for %s", f.Name)
		}
		if f.UncompressedSize64 > maxUncompressedSize {
			return nil, fmt.Errorf("file %s too large (%d bytes)", f.Name, f.UncompressedSize64)
		}

		rc, err := f.Open()
		if err != nil {
			return nil, fmt.Errorf("open %s: %w", f.Name, err)
		}

		lr := io.LimitReader(rc, int64(maxUncompressedSize)+1)
		content, err := io.ReadAll(lr)
		_ = rc.Close()
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", f.Name, err)
		}
		if len(content) > int(maxUncompressedSize) {
			return nil, fmt.Errorf("file %s exceeds max size", f.Name)
		}

		switch f.Name {
		case "manifest.json":
			if err := json.Unmarshal(content, &manifest); err != nil {
				return nil, fmt.Errorf("parse manifest: %w", err)
			}
			gotManifest = true
		case "index/index.json":
			if err := json.Unmarshal(content, &idx); err != nil {
				return nil, fmt.Errorf("parse index: %w", err)
			}
			gotIndex = true
		}
	}

	if !gotManifest {
		return nil, fmt.Errorf("manifest.json not found in zip")
	}
	if !gotIndex {
		return nil, fmt.Errorf("index/index.json not found in zip")
	}

	return &BuiltIndex{
		Index:    &idx,
		Manifest: &manifest,
	}, nil
}

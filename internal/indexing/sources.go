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
	"slices"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
)

const sourcesEntryName = "sources.json"

const maxSourcesEntrySize = 4 << 20

type SourceFile struct {
	Filename string `json:"filename"`
	Version  int    `json:"version"`
}

type Sources struct {
	Files        []SourceFile `json:"files"`
	IndexedFiles int          `json:"indexed_files"`
}

func IsIndexSource(filename string) bool {
	if artifact.IsInternalArtifact(filename) {
		return false
	}
	_, userScoped := artifact.ResolveFilename(filename)
	return !userScoped
}

func ProjectSourceFiles(versionsByName map[string][]int) []SourceFile {
	files := make([]SourceFile, 0, len(versionsByName))
	for name, versions := range versionsByName {
		if len(versions) == 0 || !IsIndexSource(name) {
			continue
		}
		files = append(files, SourceFile{Filename: name, Version: versions[len(versions)-1]})
	}
	slices.SortFunc(files, func(a, b SourceFile) int { return strings.Compare(a.Filename, b.Filename) })
	return files
}

func SameSourceFiles(a, b []SourceFile) bool {
	return slices.Equal(a, b)
}

func ReadSources(zipData []byte) (sources Sources, found bool, err error) {
	zr, err := zip.NewReader(bytes.NewReader(zipData), int64(len(zipData)))
	if err != nil {
		return Sources{}, false, fmt.Errorf("open index zip: %w", err)
	}
	for _, f := range zr.File {
		if f.Name != sourcesEntryName {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return Sources{}, false, fmt.Errorf("open %s: %w", sourcesEntryName, err)
		}
		content, err := io.ReadAll(io.LimitReader(rc, maxSourcesEntrySize+1))
		_ = rc.Close()
		if err != nil {
			return Sources{}, false, fmt.Errorf("read %s: %w", sourcesEntryName, err)
		}
		if len(content) > maxSourcesEntrySize {
			return Sources{}, false, fmt.Errorf("%s exceeds max size", sourcesEntryName)
		}
		if err := json.Unmarshal(content, &sources); err != nil {
			return Sources{}, false, fmt.Errorf("parse %s: %w", sourcesEntryName, err)
		}
		return sources, true, nil
	}
	return Sources{}, false, nil
}

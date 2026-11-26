// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package convert

import (
	"fmt"
	"path/filepath"
	"strings"
)

func Convert(filename string, content []byte, mimeType string) (*Result, error) {
	ext := strings.ToLower(filepath.Ext(filename))

	switch {
	case ext == ".pdf" || mimeType == "application/pdf":
		return ConvertPDF(content)
	case ext == ".docx" || mimeType == "application/vnd.openxmlformats-officedocument.wordprocessingml.document":
		return ConvertDOCX(content)
	case ext == ".pptx" || mimeType == "application/vnd.openxmlformats-officedocument.presentationml.presentation":
		return ConvertPPTX(content)
	default:
		return ConvertText(content)
	}
}

func ShouldConvert(filename string, mimeType string) bool {
	ext := strings.ToLower(filepath.Ext(filename))
	switch ext {
	case ".pdf", ".docx", ".pptx":
		return true
	}
	switch mimeType {
	case "application/pdf",
		"application/vnd.openxmlformats-officedocument.wordprocessingml.document",
		"application/vnd.openxmlformats-officedocument.presentationml.presentation":
		return true
	}
	return false
}

var textSubtypes = map[string]bool{
	"json": true, "xml": true, "yaml": true, "x-yaml": true, "yml": true,
	"csv": true, "javascript": true, "ecmascript": true, "xhtml+xml": true,
	"svg+xml": true, "atom+xml": true, "rss+xml": true, "sparql-query": true,
	"sparql-update": true, "sql": true, "graphql": true, "markdown": true,
	"html": true, "rtf": true, "sgml": true,
}

var textPlusSuffixes = map[string]bool{
	"json": true, "xml": true, "yaml": true, "csv": true, "svg": true, "xhtml": true,
}

func IsTextFile(mimeType string) bool {
	m := strings.ToLower(strings.TrimSpace(mimeType))
	if m == "" {
		return false
	}
	if i := strings.IndexByte(m, ';'); i >= 0 {
		m = strings.TrimSpace(m[:i])
	}
	primary, subtype, _ := strings.Cut(m, "/")
	if primary == "text" || textSubtypes[subtype] {
		return true
	}
	if i := strings.LastIndexByte(subtype, '+'); i >= 0 {
		return textPlusSuffixes[subtype[i+1:]]
	}
	return false
}

func ConvertedFilename(sourceFilename string) string {
	return fmt.Sprintf("%s.converted.txt", sourceFilename)
}

// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package artifact

import (
	"mime"
	"net/http"
	"path/filepath"
	"strings"
)

const (
	mimeTextPlain = "text/plain"
	mimeTextCSV   = "text/csv"
	mimeJSON      = "application/json"
)

func DetectMIMEType(filename string, content []byte) string {
	ext := strings.ToLower(filepath.Ext(filename))
	switch ext {
	case ".csv":
		return mimeTextCSV
	case ".json":
		return mimeJSON
	case ".yaml", ".yml", ".stim":
		return "application/x-yaml"
	case ".xml":
		return "application/xml"
	case ".html", ".htm":
		return "text/html"
	case ".txt":
		return mimeTextPlain
	case ".md":
		return "text/markdown"
	case ".py":
		return "text/x-python"
	case ".go":
		return "text/x-go"
	case ".js":
		return "text/javascript"
	case ".ts":
		return "text/typescript"
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".svg":
		return "image/svg+xml"
	case ".pdf":
		return "application/pdf"
	}

	if byExt := mime.TypeByExtension(ext); byExt != "" {
		return byExt
	}

	if len(content) > 0 {
		return http.DetectContentType(content)
	}
	return "application/octet-stream"
}

func IsGenericMIME(mimeType string) bool {
	base, _, _ := strings.Cut(mimeType, ";")
	switch strings.TrimSpace(base) {
	case "", "application/octet-stream", mimeTextPlain:
		return true
	}
	return false
}

func ResolveDataMIME(filename, storedMIME string, content []byte) string {
	if !IsGenericMIME(storedMIME) {
		return storedMIME
	}
	if derived := DetectMIMEType(filename, content); !IsGenericMIME(derived) {
		return derived
	}
	return storedMIME
}

func IsStructuredMIME(mimeType string) bool {
	return IsCSVMIME(mimeType) || IsJSONMIME(mimeType) || IsYAMLMIME(mimeType)
}

func baseMIME(mimeType string) string {
	base, _, _ := strings.Cut(mimeType, ";")
	return strings.ToLower(strings.TrimSpace(base))
}

func IsJSONMIME(mimeType string) bool {
	base := baseMIME(mimeType)
	return base == mimeJSON || base == "text/json" || strings.HasSuffix(base, "+json")
}

func IsYAMLMIME(mimeType string) bool {
	base := baseMIME(mimeType)
	switch base {
	case "application/yaml", "text/yaml", "application/x-yaml", "text/x-yaml":
		return true
	}
	return strings.HasSuffix(base, "+yaml")
}

func IsCSVMIME(mimeType string) bool {
	base := baseMIME(mimeType)
	return base == mimeTextCSV || base == "application/csv"
}

func InlineContentType(mimeType string) (string, bool) {
	mediaType, params, err := mime.ParseMediaType(mimeType)
	if err != nil || !isInlineSafeMediaType(mediaType) {
		return "", false
	}
	contentType := mime.FormatMediaType(mediaType, params)
	return contentType, contentType != ""
}

func isInlineSafeMediaType(mediaType string) bool {
	switch mediaType {
	case "image/png", "image/jpeg", "image/gif", "image/webp", "image/bmp", "image/avif",
		"application/pdf", mimeTextPlain, mimeTextCSV, "text/markdown", mimeJSON:
		return true
	}
	return strings.HasPrefix(mediaType, "audio/") || strings.HasPrefix(mediaType, "video/")
}

const SAMConfigMIMEPrefix = "application/vnd.sam-"

func IsSAMConfigMIME(mimeType string) bool {
	base, _, _ := strings.Cut(mimeType, ";")
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(base)), SAMConfigMIMEPrefix)
}

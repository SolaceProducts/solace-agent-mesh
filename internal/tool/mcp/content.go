// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package mcp

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"strings"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

const maxRawErrorTextBytes = 4096

type ContentType string

const (
	ContentTypeText     ContentType = "text"
	ContentTypeImage    ContentType = "image"
	ContentTypeAudio    ContentType = "audio"
	ContentTypeResource ContentType = "resource"
)

type TextFormat string

const (
	TextFormatJSON     TextFormat = "json"
	TextFormatCSV      TextFormat = "csv"
	TextFormatYAML     TextFormat = "yaml"
	TextFormatMarkdown TextFormat = "markdown"
	TextFormatPlain    TextFormat = "plain"
)

type ContentItem struct {
	Type     ContentType
	Text     string
	Data     []byte
	MIMEType string
	Filename string
	URI      string
	Metadata map[string]string
}

func extractContentItems(responseText string, toolName string) []ContentItem {
	var contentArray []map[string]any
	if err := json.Unmarshal([]byte(responseText), &contentArray); err == nil && len(contentArray) > 0 {
		if _, hasType := contentArray[0]["type"]; hasType {
			return parseContentArray(contentArray, toolName)
		}
	}

	if items := parseBifrostFlatFormat(responseText, toolName); items != nil {
		return items
	}

	responseText = unescapeNewlines(responseText)
	format, mimeType := DetectTextFormat(responseText)
	responseText = normalizeForFormat(responseText, format)
	ext := FormatExtension(format)
	return []ContentItem{{
		Type:     ContentTypeText,
		Text:     responseText,
		MIMEType: mimeType,
		Filename: generateFilename(toolName, filenameTypeLabel(format), ext, 0),
		Metadata: map[string]string{"detected_format": string(format)},
	}}
}

func convertCallToolResult(result *rawCallToolResult, toolName string) []ContentItem {
	if result == nil || len(result.Content) == 0 {
		return nil
	}

	var items []ContentItem
	for i, raw := range result.Content {
		var header struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(raw, &header); err != nil {
			continue
		}

		switch header.Type {
		case "text":
			var tc struct {
				Text string `json:"text"`
			}
			if err := json.Unmarshal(raw, &tc); err != nil {
				continue
			}
			text := unescapeNewlines(tc.Text)
			format, mimeType := DetectTextFormat(text)
			text = normalizeForFormat(text, format)
			ext := FormatExtension(format)
			items = append(items, ContentItem{
				Type:     ContentTypeText,
				Text:     text,
				MIMEType: mimeType,
				Filename: generateFilename(toolName, filenameTypeLabel(format), ext, i),
				Metadata: map[string]string{"detected_format": string(format)},
			})

		case "image":
			var ic struct {
				Data     string `json:"data"`
				MIMEType string `json:"mimeType"`
			}
			if err := json.Unmarshal(raw, &ic); err != nil {
				continue
			}
			decoded, err := decodeBase64Flexible(ic.Data)
			if err != nil {
				continue
			}
			ext := mimeExtension(ic.MIMEType)
			items = append(items, ContentItem{
				Type:     ContentTypeImage,
				Data:     decoded,
				MIMEType: ic.MIMEType,
				Filename: generateFilename(toolName, string(ContentTypeImage), ext, i),
			})

		case "audio":
			var ac struct {
				Data     string `json:"data"`
				MIMEType string `json:"mimeType"`
			}
			if err := json.Unmarshal(raw, &ac); err != nil {
				continue
			}
			decoded, err := decodeBase64Flexible(ac.Data)
			if err != nil {
				continue
			}
			ext := mimeExtension(ac.MIMEType)
			items = append(items, ContentItem{
				Type:     ContentTypeAudio,
				Data:     decoded,
				MIMEType: ac.MIMEType,
				Filename: generateFilename(toolName, string(ContentTypeAudio), ext, i),
			})

		case "resource":
			var rc struct {
				Resource struct {
					URI      string `json:"uri"`
					MIMEType string `json:"mimeType"`
					Text     string `json:"text"`
					Blob     string `json:"blob"`
				} `json:"resource"`
			}
			if err := json.Unmarshal(raw, &rc); err != nil {
				continue
			}
			res := rc.Resource

			ci := ContentItem{
				Type:     ContentTypeResource,
				URI:      res.URI,
				MIMEType: res.MIMEType,
			}

			if res.Text != "" {
				ci.Text = res.Text
				if ci.MIMEType == "" {
					ci.MIMEType = "text/plain"
				}
				filename := extractFilenameFromURI(res.URI)
				if filename != "" {
					ci.Filename = fmt.Sprintf("%s_resource_%d_%s", sanitizeName(toolName), i, filename)
				} else {
					ci.Filename = generateFilename(toolName, string(ContentTypeResource), ".txt", i)
				}
			} else if res.Blob != "" {
				if ci.MIMEType == "" {
					ci.MIMEType = "application/octet-stream"
				}
				if decoded, err := decodeBase64Flexible(res.Blob); err == nil {
					ci.Data = decoded
				}
				filename := extractFilenameFromURI(res.URI)
				if filename != "" {
					ci.Filename = fmt.Sprintf("%s_resource_%d_%s", sanitizeName(toolName), i, filename)
				} else {
					ext := mimeExtension(ci.MIMEType)
					ci.Filename = generateFilename(toolName, string(ContentTypeResource), ext, i)
				}
			} else {
				if ci.MIMEType == "" {
					ci.MIMEType = "application/octet-stream"
				}
				filename := extractFilenameFromURI(res.URI)
				if filename != "" {
					ci.Filename = fmt.Sprintf("%s_resource_%d_%s", sanitizeName(toolName), i, filename)
				} else {
					ci.Filename = generateFilename(toolName, string(ContentTypeResource), ".bin", i)
				}
			}
			items = append(items, ci)

		case "resource_link":
			var rl struct {
				URI      string `json:"uri"`
				MIMEType string `json:"mimeType"`
			}
			if err := json.Unmarshal(raw, &rl); err != nil {
				continue
			}
			filename := extractFilenameFromURI(rl.URI)
			if filename == "" {
				filename = generateFilename(toolName, string(ContentTypeResource), ".bin", i)
			} else {
				filename = fmt.Sprintf("%s_resource_%d_%s", sanitizeName(toolName), i, filename)
			}
			items = append(items, ContentItem{
				Type:     ContentTypeResource,
				URI:      rl.URI,
				MIMEType: rl.MIMEType,
				Filename: filename,
			})
		}
	}

	return items
}

func extractErrorText(result *rawCallToolResult) string {
	var parts []string
	for _, raw := range result.Content {
		var tc struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if err := json.Unmarshal(raw, &tc); err == nil && tc.Type == "text" {
			parts = append(parts, tc.Text)
		}
	}
	if len(parts) > 0 {
		return strings.Join(parts, "\n")
	}
	for _, raw := range result.Content {
		if s := strings.TrimSpace(string(raw)); s != "" {
			parts = append(parts, s)
		}
	}
	if len(parts) == 0 {
		return "MCP tool returned an error"
	}
	return truncateRawError(strings.Join(parts, "\n"))
}

func truncateRawError(s string) string {
	if len(s) <= maxRawErrorTextBytes {
		return s
	}
	b := s[:maxRawErrorTextBytes]
	for len(b) > 0 && !utf8.ValidString(b) {
		b = b[:len(b)-1]
	}
	return b + "… (truncated)"
}

var bifrostResourcePattern = regexp.MustCompile(`\[Embedded Resource Response: ([^\]]*)\]`)

var bifrostAnyMediaPattern = regexp.MustCompile(`\[(Image|Audio) Response: ([A-Za-z0-9+/=]+), MIME: ([^\]]+)\]`)

func parseBifrostFlatFormat(text string, toolName string) []ContentItem {
	hasMedia := bifrostAnyMediaPattern.MatchString(text)
	hasResource := bifrostResourcePattern.MatchString(text)
	if !hasMedia && !hasResource {
		return nil
	}

	matches := bifrostAnyMediaPattern.FindAllStringSubmatchIndex(text, -1)

	resourceMatches := bifrostResourcePattern.FindAllStringSubmatchIndex(text, -1)

	type marker struct {
		start, end int
		kind       string
		b64Data    string
		mimeType   string
	}

	var markers []marker
	for _, match := range matches {
		kind := strings.ToLower(text[match[2]:match[3]])
		markers = append(markers, marker{
			start:    match[0],
			end:      match[1],
			kind:     kind,
			b64Data:  text[match[4]:match[5]],
			mimeType: text[match[6]:match[7]],
		})
	}
	for _, match := range resourceMatches {
		markers = append(markers, marker{
			start: match[0],
			end:   match[1],
			kind:  "resource",
		})
	}

	for i := 1; i < len(markers); i++ {
		for j := i; j > 0 && markers[j].start < markers[j-1].start; j-- {
			markers[j], markers[j-1] = markers[j-1], markers[j]
		}
	}

	var items []ContentItem
	idx := 0
	itemNum := 0

	for _, m := range markers {
		if m.start > idx {
			preceding := strings.TrimSpace(text[idx:m.start])
			if preceding != "" {
				preceding = unescapeNewlines(preceding)
				format, mimeType := DetectTextFormat(preceding)
				preceding = normalizeForFormat(preceding, format)
				ext := FormatExtension(format)
				items = append(items, ContentItem{
					Type:     ContentTypeText,
					Text:     preceding,
					MIMEType: mimeType,
					Filename: generateFilename(toolName, filenameTypeLabel(format), ext, itemNum),
					Metadata: map[string]string{"detected_format": string(format)},
				})
				itemNum++
			}
		}

		switch m.kind {
		case "image":
			decoded, err := decodeBase64Flexible(m.b64Data)
			if err == nil {
				ext := mimeExtension(m.mimeType)
				items = append(items, ContentItem{
					Type:     ContentTypeImage,
					Data:     decoded,
					MIMEType: m.mimeType,
					Filename: generateFilename(toolName, string(ContentTypeImage), ext, itemNum),
				})
				itemNum++
			}
		case "audio":
			decoded, err := decodeBase64Flexible(m.b64Data)
			if err == nil {
				ext := mimeExtension(m.mimeType)
				items = append(items, ContentItem{
					Type:     ContentTypeAudio,
					Data:     decoded,
					MIMEType: m.mimeType,
					Filename: generateFilename(toolName, string(ContentTypeAudio), ext, itemNum),
				})
				itemNum++
			}
		case "resource":
			items = append(items, ContentItem{
				Type:     ContentTypeResource,
				MIMEType: "application/octet-stream",
				Filename: generateFilename(toolName, string(ContentTypeResource), ".bin", itemNum),
				URI:      "",
			})
			itemNum++
		}

		idx = m.end
	}

	if idx < len(text) {
		trailing := strings.TrimSpace(text[idx:])
		if trailing != "" {
			trailing = unescapeNewlines(trailing)
			format, mimeType := DetectTextFormat(trailing)
			trailing = normalizeForFormat(trailing, format)
			ext := FormatExtension(format)
			items = append(items, ContentItem{
				Type:     ContentTypeText,
				Text:     trailing,
				MIMEType: mimeType,
				Filename: generateFilename(toolName, filenameTypeLabel(format), ext, itemNum),
				Metadata: map[string]string{"detected_format": string(format)},
			})
		}
	}

	return items
}

func parseContentArray(items []map[string]any, toolName string) []ContentItem {
	var result []ContentItem

	for i, item := range items {
		contentType, _ := item["type"].(string)

		switch contentType {
		case "text":
			text, _ := item["text"].(string)
			format, mimeType := DetectTextFormat(text)
			text = normalizeForFormat(text, format)
			ext := FormatExtension(format)
			result = append(result, ContentItem{
				Type:     ContentTypeText,
				Text:     text,
				MIMEType: mimeType,
				Filename: generateFilename(toolName, filenameTypeLabel(format), ext, i),
				Metadata: map[string]string{"detected_format": string(format)},
			})

		case "image":
			data, _ := item["data"].(string)
			mimeType, _ := item["mimeType"].(string)
			if mimeType == "" {
				mimeType = "image/png"
			}
			decoded, err := decodeBase64Flexible(data)
			if err != nil {
				continue
			}
			ext := mimeExtension(mimeType)
			result = append(result, ContentItem{
				Type:     ContentTypeImage,
				Data:     decoded,
				MIMEType: mimeType,
				Filename: generateFilename(toolName, string(ContentTypeImage), ext, i),
			})

		case "audio":
			data, _ := item["data"].(string)
			mimeType, _ := item["mimeType"].(string)
			if mimeType == "" {
				mimeType = "audio/wav"
			}
			decoded, err := decodeBase64Flexible(data)
			if err != nil {
				continue
			}
			ext := mimeExtension(mimeType)
			result = append(result, ContentItem{
				Type:     ContentTypeAudio,
				Data:     decoded,
				MIMEType: mimeType,
				Filename: generateFilename(toolName, string(ContentTypeAudio), ext, i),
			})

		case "resource":
			resource, _ := item["resource"].(map[string]any)
			if resource == nil {
				continue
			}
			uri, _ := resource["uri"].(string)
			mimeType, _ := resource["mimeType"].(string)
			filename := extractFilenameFromURI(uri)
			if filename == "" {
				filename = generateFilename(toolName, string(ContentTypeResource), ".bin", i)
			}

			ci := ContentItem{
				Type:     ContentTypeResource,
				MIMEType: mimeType,
				Filename: filename,
				URI:      uri,
			}

			if text, ok := resource["text"].(string); ok {
				ci.Text = text
				if mimeType == "" {
					ci.MIMEType = "text/plain"
				}
			} else if blob, ok := resource["blob"].(string); ok {
				if decoded, err := decodeBase64Flexible(blob); err == nil {
					ci.Data = decoded
				}
				if mimeType == "" {
					ci.MIMEType = "application/octet-stream"
				}
			}

			result = append(result, ci)
		}
	}

	return result
}

func normalizeForFormat(text string, format TextFormat) string {
	if format != TextFormatJSON {
		return text
	}
	if json.Valid([]byte(text)) {
		return text
	}
	sanitized, changed := escapeUnescapedJSONControlChars([]byte(text))
	if !changed || !json.Valid(sanitized) {
		return text
	}
	return string(sanitized)
}

func DetectTextFormat(text string) (TextFormat, string) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return TextFormatPlain, "text/plain"
	}

	if isJSON(trimmed) {
		return TextFormatJSON, "application/json"
	}

	if isCSV(trimmed) {
		return TextFormatCSV, "text/csv"
	}

	if isMarkdown(trimmed) {
		return TextFormatMarkdown, "text/markdown"
	}

	if isYAML(trimmed) {
		return TextFormatYAML, "application/yaml"
	}

	return TextFormatPlain, "text/plain"
}

func isJSON(s string) bool {
	if len(s) == 0 {
		return false
	}
	if s[0] != '{' && s[0] != '[' {
		return false
	}
	var v any
	return looseJSONUnmarshal([]byte(s), &v) == nil
}

func looseJSONUnmarshal(data []byte, v any) error {
	if err := json.Unmarshal(data, v); err == nil {
		return nil
	}
	sanitized, changed := escapeUnescapedJSONControlChars(data)
	if !changed {
		return json.Unmarshal(data, v)
	}
	return json.Unmarshal(sanitized, v)
}

func escapeUnescapedJSONControlChars(in []byte) ([]byte, bool) {
	var out []byte
	inString := false
	pendingEscape := false
	flushPrefix := func(i int) {
		if out == nil {
			out = make([]byte, 0, len(in)+16)
			out = append(out, in[:i]...)
		}
	}
	for i := range len(in) {
		c := in[i]
		if !inString {
			if c == '"' {
				inString = true
			}
			if out != nil {
				out = append(out, c)
			}
			continue
		}
		if pendingEscape {
			pendingEscape = false
			if c < 0x20 {
				flushPrefix(i)
				out = append(out, '\\', '\\', 'u', '0', '0', hexNibble(c>>4), hexNibble(c&0x0F))
				continue
			}
			if out != nil {
				out = append(out, c)
			}
			continue
		}
		switch {
		case c == '\\':
			pendingEscape = true
			if out != nil {
				out = append(out, c)
			}
		case c == '"':
			inString = false
			if out != nil {
				out = append(out, c)
			}
		case c < 0x20:
			flushPrefix(i)
			out = append(out, '\\', 'u', '0', '0', hexNibble(c>>4), hexNibble(c&0x0F))
		default:
			if out != nil {
				out = append(out, c)
			}
		}
	}
	if out == nil {
		return in, false
	}
	return out, true
}

func hexNibble(b byte) byte {
	if b < 10 {
		return '0' + b
	}
	return 'a' + b - 10
}

func isCSV(s string) bool {
	lines := strings.Split(s, "\n")
	if len(lines) < 2 {
		return false
	}

	firstCommas := strings.Count(lines[0], ",")
	if firstCommas == 0 {
		return false
	}

	matchCount := 0
	for _, line := range lines[1:] {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.Count(line, ",") == firstCommas {
			matchCount++
		}
	}

	return matchCount > 0 && float64(matchCount)/float64(len(lines)-1) >= 0.5
}

func isYAML(s string) bool {
	var node yaml.Node
	if err := yaml.Unmarshal([]byte(s), &node); err != nil {
		return false
	}
	if node.Kind != yaml.DocumentNode || len(node.Content) == 0 {
		return false
	}
	root := node.Content[0]
	return root.Kind == yaml.MappingNode || root.Kind == yaml.SequenceNode
}

func isMarkdown(s string) bool {
	lines := strings.Split(s, "\n")
	mdPatterns := 0
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "# ") || strings.HasPrefix(trimmed, "## ") ||
			strings.HasPrefix(trimmed, "### ") {
			mdPatterns++
		}
		if strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "* ") {
			mdPatterns++
		}
		if strings.HasPrefix(trimmed, "```") {
			mdPatterns++
		}
		if strings.Contains(trimmed, "](") && strings.Contains(trimmed, "[") {
			mdPatterns++
		}
	}
	return mdPatterns >= 2
}

func generateFilename(toolName string, typeLabel string, ext string, index int) string {
	id := shortRandomID()
	return fmt.Sprintf("%s_%s_%d_%s%s", sanitizeName(toolName), typeLabel, index, id, ext)
}

func withFreshFilenames(items []ContentItem, toolName string) []ContentItem {
	renamed := make([]ContentItem, len(items))
	for i, item := range items {
		ext := path.Ext(item.Filename)
		if len(ext) > maxFreshFilenameExtension {
			ext = ".bin"
		}
		item.Filename = generateFilename(toolName, string(item.Type), ext, i)
		renamed[i] = item
	}
	return renamed
}

const maxFreshFilenameExtension = 8

func filenameTypeLabel(format TextFormat) string {
	switch format {
	case TextFormatJSON, TextFormatCSV, TextFormatYAML, TextFormatMarkdown:
		return string(format)
	default:
		return "text"
	}
}

func shortRandomID() string {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return "00000000"
	}
	return fmt.Sprintf("%x", b)
}

func unescapeNewlines(s string) string {
	s = strings.ReplaceAll(s, "\\n", "\n")
	s = strings.ReplaceAll(s, "\\t", "\t")
	return s
}

func decodeBase64Flexible(s string) ([]byte, error) {
	if decoded, err := base64.StdEncoding.DecodeString(s); err == nil {
		return decoded, nil
	}
	trimmed := strings.TrimRight(s, "=")
	if decoded, err := base64.RawStdEncoding.DecodeString(trimmed); err == nil {
		return decoded, nil
	}
	if decoded, err := base64.URLEncoding.DecodeString(s); err == nil {
		return decoded, nil
	}
	trimmed = strings.TrimRight(s, "=")
	return base64.RawURLEncoding.DecodeString(trimmed)
}

func extractFilenameFromURI(uri string) string {
	if uri == "" {
		return ""
	}
	parsed, err := url.Parse(uri)
	if err != nil {
		return ""
	}
	base := path.Base(parsed.Path)
	if base == "" || base == "." || base == "/" {
		return ""
	}
	return base
}

func FormatExtension(format TextFormat) string {
	switch format {
	case TextFormatJSON:
		return ".json"
	case TextFormatCSV:
		return ".csv"
	case TextFormatYAML:
		return ".yaml"
	case TextFormatMarkdown:
		return ".md"
	default:
		return ".txt"
	}
}

func mimeExtension(mimeType string) string {
	switch {
	case strings.Contains(mimeType, "png"):
		return ".png"
	case strings.Contains(mimeType, "jpeg"), strings.Contains(mimeType, "jpg"):
		return ".jpg"
	case strings.Contains(mimeType, "gif"):
		return ".gif"
	case strings.Contains(mimeType, "svg"):
		return ".svg"
	case strings.Contains(mimeType, "webp"):
		return ".webp"
	case strings.Contains(mimeType, "wav"):
		return ".wav"
	case strings.Contains(mimeType, "mp3"), strings.Contains(mimeType, "mpeg"):
		return ".mp3"
	case strings.Contains(mimeType, "ogg"):
		return ".ogg"
	case strings.Contains(mimeType, "flac"):
		return ".flac"
	default:
		return ".bin"
	}
}

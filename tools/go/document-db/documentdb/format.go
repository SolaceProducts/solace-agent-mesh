// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package documentdb

import (
	"bytes"
	"crypto/rand"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

const maxQueryLenInDescription = 1000

const defaultMaxInlineResults = 10

func formatResults(docs []map[string]any, format string) (content []byte, ext string, mime string, err error) {
	normalised := strings.ToLower(strings.TrimSpace(format))
	switch normalised {
	case "yaml":
		data, err := yaml.Marshal(docs)
		if err != nil {
			return nil, "", "", fmt.Errorf("marshal yaml: %w", err)
		}
		return data, "yaml", "text/yaml", nil

	case "csv":
		data, err := marshalCSV(docs)
		if err != nil {
			return nil, "", "", err
		}
		return data, "csv", "text/csv", nil

	case "markdown":
		return []byte(marshalMarkdown(docs)), "md", "text/md", nil

	default:
		data, err := json.MarshalIndent(docs, "", "  ")
		if err != nil {
			return nil, "", "", fmt.Errorf("marshal json: %w", err)
		}
		return data, "json", "text/json", nil
	}
}

func marshalCSV(docs []map[string]any) ([]byte, error) {
	if len(docs) == 0 {
		return nil, nil
	}
	headers := sortedKeys(docs[0])
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	if err := w.Write(headers); err != nil {
		return nil, fmt.Errorf("csv header: %w", err)
	}
	row := make([]string, len(headers))
	for _, doc := range docs {
		for i, h := range headers {
			row[i] = stringify(doc[h])
		}
		if err := w.Write(row); err != nil {
			return nil, fmt.Errorf("csv row: %w", err)
		}
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return nil, fmt.Errorf("csv flush: %w", err)
	}
	return buf.Bytes(), nil
}

func marshalMarkdown(docs []map[string]any) string {
	if len(docs) == 0 {
		return "No results found."
	}
	headers := sortedKeys(docs[0])
	var buf strings.Builder
	buf.WriteString("| ")
	buf.WriteString(strings.Join(headers, " | "))
	buf.WriteString(" |\n")
	buf.WriteString("| ")
	for i := range headers {
		if i > 0 {
			buf.WriteString(" | ")
		}
		buf.WriteString("---")
	}
	buf.WriteString(" |\n")
	for _, doc := range docs {
		buf.WriteString("| ")
		for i, h := range headers {
			if i > 0 {
				buf.WriteString(" | ")
			}
			buf.WriteString(stringify(doc[h]))
		}
		buf.WriteString(" |\n")
	}
	return buf.String()
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

func stringify(v any) string {
	if v == nil {
		return ""
	}
	switch s := v.(type) {
	case string:
		return s
	default:
		return fmt.Sprintf("%v", v)
	}
}

func buildArtifactDescription(resultDescription, defaultPrefix, label, queryText string) string {
	prefix := strings.TrimSpace(resultDescription)
	if prefix == "" {
		prefix = defaultPrefix
	}
	truncated := queryText
	ellipsis := ""
	if len(queryText) > maxQueryLenInDescription {
		truncated = queryText[:maxQueryLenInDescription]
		ellipsis = "..."
	}
	return fmt.Sprintf("%s %s: %s%s", prefix, label, truncated, ellipsis)
}

func uniqueSuffix() string {
	var buf [4]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "00000000"
	}
	return hex.EncodeToString(buf[:])
}

func clampInlineContent(content string, rowCount, cap int) (inline string, previewSkipped bool) {
	if cap <= 0 {
		cap = defaultMaxInlineResults
	}
	if rowCount <= cap {
		return content, false
	}
	return "", true
}

func capFromConfig(config map[string]any) int {
	n := configInt(config, "max_inline_results", defaultMaxInlineResults)
	if n <= 0 {
		return defaultMaxInlineResults
	}
	return n
}

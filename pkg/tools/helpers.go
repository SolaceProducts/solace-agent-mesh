// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"encoding/json"
	"math"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/taxonomy"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func invalidArgument(message string) *tool.Result {
	return tool.Error(message, tool.WithErrorCode(tool.DispatchErrorCode(taxonomy.DispatchClassValidation)))
}

func layeredIntConfig(toolCtx *tool.Context, ownConfig map[string]any, key string, defaultVal int) int {
	if toolCtx != nil {
		if v, ok := positiveIntConfig(toolCtx.ToolConfig, key); ok {
			return v
		}
	}
	if v, ok := positiveIntConfig(ownConfig, key); ok {
		return v
	}
	return defaultVal
}

func positiveIntConfig(config map[string]any, key string) (int, bool) {
	switch v := config[key].(type) {
	case float64:
		if v >= 1 && v < float64(math.MaxInt) && v == math.Trunc(v) {
			return int(v), true
		}
	case int:
		if v > 0 {
			return v, true
		}
	}
	return 0, false
}

func truncatePreviewList[T any](items []T, maxRows, maxBytes int) ([]T, bool) {
	truncated := false
	if len(items) > maxRows {
		items = items[:maxRows]
		truncated = true
	}
	n, fits := fittingPrefixLen(items, maxBytes)
	if n < len(items) || !fits {
		truncated = true
	}
	return items[:n], truncated
}

func fittingPrefixLen[T any](items []T, maxBytes int) (int, bool) {
	size := len("[]")
	for i := range items {
		encoded, err := json.Marshal(&items[i])
		if err != nil {
			return max(i, 1), false
		}
		if i > 0 {
			size += len(",")
		}
		size += len(encoded)
		if size > maxBytes {
			return max(i, 1), i > 0
		}
	}
	return len(items), true
}

var sanitizeFilenameRe = regexp.MustCompile(`[<>:"/\\|?*\s]+`)

func sanitizeFilename(name string) string {
	name = filepath.Base(name)
	name = sanitizeFilenameRe.ReplaceAllString(name, "_")
	name = strings.Trim(name, "_")
	return name
}

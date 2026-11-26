// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package mcp

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	pkgtools "github.com/SolaceProducts/solace-agent-mesh/pkg/tools"
	"github.com/jmespath/go-jmespath"
	"gopkg.in/yaml.v3"
)

const (
	defaultSkeletonDepth          = 2
	defaultSkeletonMaxScalarBytes = 256
	defaultSkeletonMaxObjectKeys  = 50
	skeletonOverallMaxBytes       = 4096
	extractFieldMaxBytes          = 4096
	maxVerbatimValueBytes         = 1024
)

func buildResponseDigest(items []ContentItem, cfg HandlerConfig, toolName string) map[string]any {
	wantSkeleton := cfg.ResponseSkeleton
	wantExtract := len(cfg.ExtractFields) > 0
	if !wantSkeleton && !wantExtract {
		return nil
	}

	parsed, ok := firstStructuredValue(items)
	if !ok {
		return nil
	}
	parsed, _ = pkgtools.NullifyNonFinite(parsed)

	digest := map[string]any{}
	if wantSkeleton {
		depth := cfg.SkeletonDepth
		if depth <= 0 {
			depth = defaultSkeletonDepth
		}
		maxScalar := cfg.SkeletonMaxScalarBytes
		if maxScalar <= 0 {
			maxScalar = defaultSkeletonMaxScalarBytes
		}
		maxKeys := cfg.SkeletonMaxObjectKeys
		if maxKeys <= 0 {
			maxKeys = defaultSkeletonMaxObjectKeys
		}
		if sk := buildSkeleton(parsed, depth, maxScalar, maxKeys); sk != nil {
			digest["response_skeleton"] = sk
		} else if cfg.Logger != nil {
			cfg.Logger.Warn("mcp response skeleton too large to keep", "toolName", toolName,
				"skeletonMaxBytes", skeletonOverallMaxBytes)
		}
	}
	if wantExtract {
		if ex := extractConfiguredFields(parsed, cfg.ExtractFields); len(ex) > 0 {
			digest["extracted_fields"] = ex
		}
	}

	if len(digest) == 0 {
		return nil
	}
	return digest
}

func firstStructuredValue(items []ContentItem) (any, bool) {
	for _, item := range items {
		if item.Type != ContentTypeText || item.Text == "" {
			continue
		}
		var v any
		var err error
		if TextFormat(item.Metadata["detected_format"]) == TextFormatYAML {
			err = yaml.Unmarshal([]byte(item.Text), &v)
			v = pkgtools.NormalizeYAMLForJMESPath(v)
		} else {
			err = looseJSONUnmarshal([]byte(item.Text), &v)
		}
		if err == nil {
			return v, true
		}
	}
	return nil, false
}

func buildSkeleton(v any, maxDepth, maxScalarBytes, maxObjectKeys int) any {
	sk := skeletonValue(v, 0, maxDepth, maxScalarBytes, maxObjectKeys)
	if fits(sk) {
		return sk
	}
	if maxDepth > 1 {
		sk = skeletonValue(v, 0, 1, maxScalarBytes, maxObjectKeys)
		if fits(sk) {
			return sk
		}
	}
	return nil
}

func fits(sk any) bool {
	b, err := json.Marshal(sk)
	return err == nil && len(b) <= skeletonOverallMaxBytes
}

func skeletonValue(v any, depth, maxDepth, maxScalarBytes, maxObjectKeys int) any {
	switch x := v.(type) {
	case nil:
		return nil
	case bool:
		return x
	case float64:
		return x
	case int:
		return x
	case int64:
		return x
	case uint64:
		return x
	case json.Number:
		return x
	case string:
		return truncateScalar(x, maxScalarBytes)
	case []any:
		return fmt.Sprintf("[%d items]", len(x))
	case map[string]any:
		if depth >= maxDepth {
			return fmt.Sprintf("{%d keys}", len(x))
		}
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		truncated := 0
		if maxObjectKeys > 0 && len(keys) > maxObjectKeys {
			slices.Sort(keys)
			truncated = len(keys) - maxObjectKeys
			keys = keys[:maxObjectKeys]
		}
		out := make(map[string]any, len(keys)+1)
		for _, k := range keys {
			if s, ok := x[k].(string); ok && loopControlKey(k) {
				out[k] = wholeOrMarker(s, maxVerbatimValueBytes)
				continue
			}
			out[k] = skeletonValue(x[k], depth+1, maxDepth, maxScalarBytes, maxObjectKeys)
		}
		if truncated > 0 {
			out["_truncated_keys"] = truncated
		}
		return out
	default:
		return truncateScalar(fmt.Sprintf("%v", x), maxScalarBytes)
	}
}

func extractConfiguredFields(parsed any, fields map[string]string) map[string]any {
	out := map[string]any{}
	for name, expr := range fields {
		if expr == "" {
			continue
		}
		res, err := jmespath.Search(expr, parsed)
		res, _ = pkgtools.NullifyNonFinite(res)
		if err != nil || res == nil {
			continue
		}
		if s, ok := res.(string); ok {
			out[name] = wholeOrMarker(s, extractFieldMaxBytes)
			continue
		}
		b, marshalErr := json.Marshal(res)
		if marshalErr != nil {
			continue
		}
		if len(b) > extractFieldMaxBytes {
			out[name] = tooLargeMarker(len(b))
			continue
		}
		out[name] = res
	}
	return out
}

var pagingKeyParts = []string{"pagetoken", "pagingtoken", "cursor", "continuation", "nextlink", "nexttoken", "nextpage", "nexturl",
	"scrollid", "deltalink", "skiptoken"}

func loopControlKey(key string) bool {
	k := strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, strings.ToLower(key))
	return k == "next" || slices.ContainsFunc(pagingKeyParts, func(part string) bool { return strings.Contains(k, part) })
}

func wholeOrMarker(s string, limit int) any {
	b, err := json.Marshal(s)
	if err != nil || len(b) > limit {
		return tooLargeMarker(len(b))
	}
	return s
}

func tooLargeMarker(bytes int) string {
	return fmt.Sprintf("(value too large: %d bytes — read it from the full response)", bytes)
}

func truncateScalar(s string, maxScalarBytes int) string {
	if maxScalarBytes <= 0 || len(s) <= maxScalarBytes {
		return s
	}
	cut := maxScalarBytes
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "…(truncated)"
}

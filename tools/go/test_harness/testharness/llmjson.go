// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package testharness

import (
	"encoding/json"
	"fmt"
	"strings"
)

func extractJSON(response string, dest any) ([]byte, error) {
	candidates := jsonCandidates(response)
	if len(candidates) == 0 {
		return nil, fmt.Errorf("no JSON-shaped substring found in LLM response")
	}
	var firstErr error
	for _, c := range candidates {
		if err := json.Unmarshal([]byte(c), dest); err == nil {
			return []byte(c), nil
		} else if firstErr == nil {
			firstErr = err
		}
	}
	return nil, fmt.Errorf("could not parse LLM response as JSON: %w", firstErr)
}

func jsonCandidates(s string) []string {
	out := make([]string, 0, 3)
	if fenced := extractFencedBlock(s); fenced != "" {
		out = append(out, fenced)
	}
	if bracketed := extractFromFirstBracket(s); bracketed != "" {
		out = append(out, bracketed)
	}
	out = append(out, strings.TrimSpace(s))
	return out
}

func extractFencedBlock(s string) string {
	const fence = "```"
	start := strings.Index(s, fence)
	if start == -1 {
		return ""
	}
	rest := s[start+len(fence):]
	if nl := strings.Index(rest, "\n"); nl != -1 {
		first := strings.TrimSpace(rest[:nl])
		if first == "json" || first == "JSON" || first == "" {
			rest = rest[nl+1:]
		}
	}
	end := strings.Index(rest, fence)
	if end == -1 {
		return ""
	}
	return strings.TrimSpace(rest[:end])
}

func extractFromFirstBracket(s string) string {
	openObj := strings.Index(s, "{")
	openArr := strings.Index(s, "[")
	var first int
	switch {
	case openObj == -1:
		first = openArr
	case openArr == -1:
		first = openObj
	default:
		if openObj < openArr {
			first = openObj
		} else {
			first = openArr
		}
	}
	if first == -1 {
		return ""
	}
	rest := s[first:]
	closer := matchingClose(rest)
	if closer != -1 {
		return strings.TrimSpace(rest[:closer+1])
	}
	return strings.TrimSpace(rest)
}

func matchingClose(s string) int {
	if len(s) == 0 {
		return -1
	}
	open := s[0]
	var close byte
	switch open {
	case '{':
		close = '}'
	case '[':
		close = ']'
	default:
		return -1
	}
	depth := 0
	inStr := false
	escape := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if escape {
			escape = false
			continue
		}
		if inStr {
			if c == '\\' {
				escape = true
				continue
			}
			if c == '"' {
				inStr = false
			}
			continue
		}
		switch c {
		case '"':
			inStr = true
		case open:
			depth++
		case close:
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

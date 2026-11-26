// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package whatsapp

import "strings"

func chunkText(s string, limit int) []string {
	if limit <= 0 || s == "" {
		return nil
	}
	runes := []rune(s)
	var chunks []string
	for len(runes) > limit {
		cut := lastBoundary(runes[:limit])
		if chunk := strings.TrimRight(string(runes[:cut]), " \n"); chunk != "" {
			chunks = append(chunks, chunk)
		}
		runes = trimLeadingBoundary(runes[cut:])
	}
	if tail := string(runes); tail != "" {
		chunks = append(chunks, tail)
	}
	return chunks
}

func lastBoundary(window []rune) int {
	for i := len(window) - 1; i > 0; i-- {
		if window[i] == '\n' {
			return i + 1
		}
	}
	for i := len(window) - 1; i > 0; i-- {
		if window[i] == ' ' {
			return i + 1
		}
	}
	return len(window)
}

func trimLeadingBoundary(r []rune) []rune {
	i := 0
	for i < len(r) && (r[i] == ' ' || r[i] == '\n') {
		i++
	}
	return r[i:]
}

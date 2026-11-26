// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package mermaidtool

import (
	"regexp"
	"strings"
)

func extractMetadata(source string) (title string, desc string) {
	title = extractFrontMatterTitle(source)
	if title == "" {
		title = extractTitleLine(source)
	}
	desc = extractAccDescr(source)
	return title, desc
}

var (
	frontMatterRE = regexp.MustCompile(`(?s)^\s*---\s*\n(.*?)\n---`)
	fmTitleRE     = regexp.MustCompile(`(?m)^\s*title\s*:\s*(.+?)\s*$`)
	titleLineRE   = regexp.MustCompile(`(?m)^\s*title\s+(.+?)\s*$`)
	accDescrRE    = regexp.MustCompile(`(?m)^\s*accDescr\s*:\s*(.+?)\s*$`)
	accDescrBlock = regexp.MustCompile(`(?s)accDescr\s*\{\s*(.*?)\s*\}`)
)

func extractFrontMatterTitle(source string) string {
	m := frontMatterRE.FindStringSubmatch(source)
	if m == nil {
		return ""
	}
	t := fmTitleRE.FindStringSubmatch(m[1])
	if t == nil {
		return ""
	}
	return strings.Trim(strings.TrimSpace(t[1]), `"'`)
}

func extractTitleLine(source string) string {
	for line := range strings.SplitSeq(source, "\n") {
		t := titleLineRE.FindStringSubmatch(line)
		if t != nil {
			return strings.TrimSpace(t[1])
		}
	}
	return ""
}

func extractAccDescr(source string) string {
	if m := accDescrBlock.FindStringSubmatch(source); m != nil {
		return strings.TrimSpace(m[1])
	}
	if m := accDescrRE.FindStringSubmatch(source); m != nil {
		return strings.TrimSpace(m[1])
	}
	return ""
}

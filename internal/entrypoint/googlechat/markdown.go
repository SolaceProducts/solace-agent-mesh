// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package googlechat

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

var (
	linkRE          = regexp.MustCompile(`\[([^\]]*)\]\((https?://[^)\s]+)\)`)
	boldRE          = regexp.MustCompile(`\*\*(.+?)\*\*`)
	strikeRE        = regexp.MustCompile(`~~(.+?)~~`)
	headingRE       = regexp.MustCompile(`(?m)^[ \t]*#{1,6}[ \t]+(.+?)[ \t]*#*[ \t]*$`)
	hrRE            = regexp.MustCompile(`(?m)^[ \t]*(-{3,}|\*{3,}|_{3,})[ \t]*$`)
	codeFenceLangRE = regexp.MustCompile("^```[A-Za-z0-9_+-]+\n")
	tableRowRE      = regexp.MustCompile(`^\s*\|.*\|\s*$`)
	tableRuleRE     = regexp.MustCompile(`^\s*\|?\s*:?-{3,}:?\s*(\|\s*:?-{3,}:?\s*)*\|?\s*$`)
	blankRunRE      = regexp.MustCompile(`\n[ \t]*\n(?:[ \t]*\n)+`)
)

const (
	codeFence    = "```"
	chatDivider  = "────────────"
	boldSentinel = "\x00B\x00"
)

func renderForChat(text string) string {
	if text == "" {
		return ""
	}
	var b strings.Builder
	for i, seg := range splitCodeBlocks(text) {
		if i%2 == 1 {
			b.WriteString(codeFenceLangRE.ReplaceAllString(seg, codeFence+"\n"))
			continue
		}
		b.WriteString(renderProse(seg))
	}
	return strings.TrimSpace(b.String())
}

func renderProse(s string) string {
	s = blankRunRE.ReplaceAllString(s, "\n\n")
	s = tablesToCodeBlocks(s)
	s = linkRE.ReplaceAllString(s, "<$2|$1>")
	s = boldRE.ReplaceAllString(s, boldSentinel+"$1"+boldSentinel)
	s = strikeRE.ReplaceAllString(s, "~$1~")
	s = hrRE.ReplaceAllString(s, chatDivider)
	s = headingRE.ReplaceAllString(s, boldSentinel+"$1"+boldSentinel)
	return strings.ReplaceAll(s, boldSentinel, "*")
}

func tablesToCodeBlocks(s string) string {
	lines := strings.Split(s, "\n")
	var out []string
	for i := 0; i < len(lines); {
		if !isTableStart(lines, i) {
			out = append(out, lines[i])
			i++
			continue
		}
		out = append(out, codeFence)
		for i < len(lines) && tableRowRE.MatchString(lines[i]) {
			if !tableRuleRE.MatchString(lines[i]) {
				out = append(out, strings.TrimSpace(lines[i]))
			}
			i++
		}
		out = append(out, codeFence)
	}
	return strings.Join(out, "\n")
}

func isTableStart(lines []string, i int) bool {
	return i+1 < len(lines) && tableRowRE.MatchString(lines[i]) && tableRuleRE.MatchString(lines[i+1])
}

func splitCodeBlocks(text string) []string {
	var parts []string
	for {
		start := strings.Index(text, codeFence)
		if start < 0 {
			return append(parts, text)
		}
		end := strings.Index(text[start+len(codeFence):], codeFence)
		if end < 0 {
			return append(parts, text)
		}
		end = start + len(codeFence) + end + len(codeFence)
		parts = append(parts, text[:start], text[start:end])
		text = text[end:]
	}
}

func splitForChat(text string, limit int) []string {
	var chunks []string
	for utf8.RuneCountInString(text) > limit {
		runes := []rune(text)
		cut := beforeOpenLink(runes, bestBreak(runes[:limit], max(limit-limit/6, minSplitRunes)))
		chunks = append(chunks, strings.TrimRight(string(runes[:cut]), " \n"))
		text = strings.TrimLeft(string(runes[cut:]), " \n")
	}
	if text != "" {
		chunks = append(chunks, text)
	}
	return chunks
}

func beforeOpenLink(runes []rune, cut int) int {
	reach := min(len(runes), cut+utf8.RuneCountInString(linkStart)-1)
	s := string(runes[:reach])
	start := strings.LastIndex(s, linkStart)
	if start <= 0 {
		return cut
	}
	at := utf8.RuneCountInString(s[:start])
	if cut-at > maxLinkRunes || strings.Contains(string(runes[at:cut]), ">") {
		return cut
	}
	return at
}

func bestBreak(window []rune, floor int) int {
	floor = min(floor, len(window))
	for _, sep := range []rune{'\n', ' '} {
		for i := len(window) - 1; i >= floor; i-- {
			if window[i] == sep {
				return i + 1
			}
		}
	}
	return len(window)
}

// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package pptxgen

import "strings"

const monospaceFont = "Consolas"

type inlineRun struct {
	text      string
	bold      bool
	italic    bool
	code      bool
	highlight bool
}

func parseInlineMarkdown(s string, hlEnabled bool) []inlineRun {
	return parseInline(s, false, false, false, hlEnabled)
}

func parseInline(s string, bold, italic, highlight, hlEnabled bool) []inlineRun {
	var runs []inlineRun
	var buf []byte
	flush := func() {
		if len(buf) > 0 {
			runs = append(runs, inlineRun{text: string(buf), bold: bold, italic: italic, highlight: highlight})
			buf = buf[:0]
		}
	}

	i := 0
	for i < len(s) {
		switch {
		case s[i] == '`':
			if end := strings.IndexByte(s[i+1:], '`'); end >= 0 {
				flush()
				runs = append(runs, inlineRun{text: s[i+1 : i+1+end], bold: bold, italic: italic, highlight: highlight, code: true})
				i += 1 + end + 1
				continue
			}

		case hlEnabled && !highlight && strings.HasPrefix(s[i:], "=="):
			if end := findCloser(s, i+2, "=="); end >= 0 {
				flush()
				runs = append(runs, parseInline(s[i+2:end], bold, italic, true, hlEnabled)...)
				i = end + 2
				continue
			}

		case !bold && (strings.HasPrefix(s[i:], "**") || strings.HasPrefix(s[i:], "__")):
			marker := s[i : i+2]
			if end := findCloser(s, i+2, marker); end >= 0 {
				flush()
				runs = append(runs, parseInline(s[i+2:end], true, italic, highlight, hlEnabled)...)
				i = end + len(marker)
				continue
			}

		case !italic && (s[i] == '*' || s[i] == '_') && isOpener(s, i):
			if end := findCloser(s, i+1, s[i:i+1]); end >= 0 {
				flush()
				runs = append(runs, parseInline(s[i+1:end], bold, true, highlight, hlEnabled)...)
				i = end + 1
				continue
			}

		case s[i] == '[':
			if text, n, ok := parseLink(s[i:]); ok {
				flush()
				runs = append(runs, parseInline(text, bold, italic, highlight, hlEnabled)...)
				i += n
				continue
			}
		}

		buf = append(buf, s[i])
		i++
	}
	flush()
	return runs
}

func findCloser(s string, from int, marker string) int {
	for i := from; i+len(marker) <= len(s); i++ {
		if s[i:i+len(marker)] != marker {
			continue
		}
		if i == from || isSpace(s[i-1]) {
			continue
		}
		if marker[0] == '_' && i+len(marker) < len(s) && isAlnum(s[i+len(marker)]) {
			continue
		}
		return i
	}
	return -1
}

func isOpener(s string, i int) bool {
	if i+1 >= len(s) || isSpace(s[i+1]) {
		return false
	}
	if s[i] == '_' && i > 0 && isAlnum(s[i-1]) {
		return false
	}
	return true
}

func parseLink(s string) (text string, n int, ok bool) {
	rb := strings.IndexByte(s, ']')
	if rb < 0 || rb+1 >= len(s) || s[rb+1] != '(' {
		return "", 0, false
	}
	rel := strings.IndexByte(s[rb+1:], ')')
	if rel < 0 {
		return "", 0, false
	}
	return s[1:rb], rb + 1 + rel + 1, true
}

func isSpace(b byte) bool { return b == ' ' || b == '\t' }

func isAlnum(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9')
}

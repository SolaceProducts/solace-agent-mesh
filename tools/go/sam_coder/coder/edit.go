// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package coder

import (
	"fmt"
	"strings"
	"unicode"
)

type matcher func(content, find string) []span

type span struct{ start, end int }

var matchers = []matcher{exactMatches, unicodeFoldedMatches, trimmedLineMatches}

func replaceInContent(content, oldString, newString string, replaceAll bool) (string, int, error) {
	if oldString == newString {
		return "", 0, fmt.Errorf("old_string and new_string are identical")
	}
	for _, match := range matchers {
		spans := match(content, oldString)
		if len(spans) == 0 {
			continue
		}
		if len(spans) > 1 && !replaceAll {
			return "", 0, fmt.Errorf("old_string matches %d places; add surrounding lines to make it unique, or pass replace_all", len(spans))
		}
		var b strings.Builder
		last := 0
		for _, s := range spans {
			b.WriteString(content[last:s.start])
			b.WriteString(newString)
			last = s.end
		}
		b.WriteString(content[last:])
		return b.String(), len(spans), nil
	}
	return "", 0, fmt.Errorf("old_string not found%s", missHint(content, oldString))
}

func exactMatches(content, find string) []span {
	var out []span
	for i := 0; ; {
		j := strings.Index(content[i:], find)
		if j < 0 {
			return out
		}
		out = append(out, span{i + j, i + j + len(find)})
		i += j + len(find)
	}
}

var unicodeFolds = strings.NewReplacer(
	"‘", "'", "’", "'", "“", `"`, "”", `"`,
	"–", "-", "—", "-", " ", " ", "…", "...",
)

func unicodeFoldedMatches(content, find string) []span {
	foldedFind := unicodeFolds.Replace(find)
	if foldedFind == find && unicodeFolds.Replace(content) == content {
		return nil
	}
	folded, origin := foldWithOrigin(content)
	var out []span
	for _, s := range exactMatches(folded, foldedFind) {
		out = append(out, span{origin[s.start], origin[s.end]})
	}
	return out
}

func foldWithOrigin(content string) (string, []int) {
	var b strings.Builder
	origin := make([]int, 0, len(content)+1)
	for i, r := range content {
		rep := unicodeFolds.Replace(string(r))
		for range len(rep) {
			origin = append(origin, i)
		}
		b.WriteString(rep)
	}
	origin = append(origin, len(content))
	return b.String(), origin
}

func trimmedLineMatches(content, find string) []span {
	findLines := strings.Split(strings.TrimRight(find, "\n"), "\n")
	for i := range findLines {
		findLines[i] = strings.TrimRightFunc(findLines[i], unicode.IsSpace)
	}
	lines, offsets := splitLinesWithOffsets(content)
	var out []span
	for i := 0; i+len(findLines) <= len(lines); i++ {
		ok := true
		for k, fl := range findLines {
			if strings.TrimRightFunc(lines[i+k], unicode.IsSpace) != fl {
				ok = false
				break
			}
		}
		if ok {
			end := offsets[i+len(findLines)-1] + len(lines[i+len(findLines)-1])
			out = append(out, span{offsets[i], end})
			i += len(findLines) - 1
		}
	}
	return out
}

func splitLinesWithOffsets(content string) ([]string, []int) {
	lines := strings.Split(content, "\n")
	offsets := make([]int, len(lines))
	pos := 0
	for i, l := range lines {
		offsets[i] = pos
		pos += len(l) + 1
	}
	return lines, offsets
}

func missHint(content, find string) string {
	first := strings.TrimSpace(strings.SplitN(find, "\n", 2)[0])
	if first == "" {
		return ""
	}
	lines := strings.Split(content, "\n")
	for i, l := range lines {
		if strings.Contains(l, first) {
			return fmt.Sprintf("; its first line appears at line %d, so the lines after it differ. Re-read the file and copy old_string exactly", i+1)
		}
	}
	return "; re-read the file, it may have changed"
}

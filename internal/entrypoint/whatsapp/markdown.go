// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package whatsapp

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

func renderForWhatsApp(text string) string {
	r := waRenderer{lines: strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")}
	for r.next < len(r.lines) {
		r.step()
	}
	if r.fence != "" {
		r.out = append(r.out, waCodeFence)
	}
	return strings.TrimRight(strings.Join(r.out, "\n"), "\n")
}

type waRenderer struct {
	lines       []string
	next        int
	out         []string
	fence       string
	fenceIndent int
}

func (r *waRenderer) step() {
	line := r.lines[r.next]
	r.next++
	if r.fence != "" {
		r.codeLine(line)
		return
	}
	if marker, indent, ok := openingFence(line); ok && (indent < codeIndent || r.closerFollows(marker, indent)) {
		r.fence, r.fenceIndent = marker, indent
		r.out = append(r.out, waCodeFence)
		return
	}
	if r.next < len(r.lines) && isTableStart(line, r.lines[r.next]) {
		r.table(line)
		return
	}
	r.prose(renderProseLine(line))
}

func (r *waRenderer) codeLine(line string) {
	if isClosingFence(line, r.fence, r.fenceIndent) {
		r.out = append(r.out, waCodeFence)
		r.fence = ""
		return
	}
	r.out = append(r.out, breakBacktickRuns(line))
}

func (r *waRenderer) table(header string) {
	end := tableEnd(r.lines, r.next+1)
	rows := tableRows(r.lines[r.next+1 : end])
	r.next = end
	r.prose("")
	for _, line := range renderTable(splitTableRow(header), rows) {
		r.prose(line)
	}
	r.prose("")
}

func (r *waRenderer) closerFollows(marker string, indent int) bool {
	end := min(len(r.lines), r.next+fenceLookahead)
	for _, line := range r.lines[r.next:end] {
		if isClosingFence(line, marker, indent) {
			return true
		}
	}
	return false
}

func (r *waRenderer) prose(line string) {
	if strings.TrimSpace(line) == "" {
		line = ""
	}
	if line == "" && (len(r.out) == 0 || r.out[len(r.out)-1] == "") {
		return
	}
	r.out = append(r.out, line)
}

const waCodeFence = "```"

const codeIndent = 4

const fenceLookahead = 1000

var (
	fenceOpenRE     = regexp.MustCompile("^([ \t]*)(`{3,}|~{3,})(.*)$")
	headingRE       = regexp.MustCompile(`^ {0,3}#{1,6}(?:[ \t]+(.*?))?(?:[ \t]+#+)?[ \t]*$`)
	thematicBreakRE = regexp.MustCompile(`^ {0,3}(?:(?:-[ \t]*){3,}|(?:\*[ \t]*){3,}|(?:_[ \t]*){3,})$`)
	bulletMarkerRE  = regexp.MustCompile(`^([ \t]*)[*+]([ \t]+)`)
	backtickRunRE   = regexp.MustCompile("`{3,}")
)

var (
	zeroWidthSpace      = string(rune(0x200B))
	brokenBacktickRunRE = regexp.MustCompile("`(?:" + zeroWidthSpace + "`){2,}")
)

func openingFence(line string) (marker string, indent int, ok bool) {
	m := fenceOpenRE.FindStringSubmatch(line)
	if m == nil {
		return "", 0, false
	}
	marker, info := m[2], m[3]
	if marker[0] == '`' && strings.Contains(info, "`") {
		return "", 0, false
	}
	return marker, indentWidth(m[1]), true
}

func isClosingFence(line, opening string, openingIndent int) bool {
	m := fenceOpenRE.FindStringSubmatch(line)
	return len(m) == 4 &&
		strings.HasPrefix(m[2], opening[:1]) && len(m[2]) >= len(opening) &&
		strings.TrimSpace(m[3]) == "" &&
		indentWidth(m[1]) < openingIndent+codeIndent
}

func indentWidth(ws string) int {
	width := 0
	for _, c := range ws {
		if c == '\t' {
			width += 4 - width%4
		} else {
			width++
		}
	}
	return width
}

func breakBacktickRuns(line string) string {
	return backtickRunRE.ReplaceAllStringFunc(line, func(run string) string {
		return strings.Join(strings.Split(run, ""), zeroWidthSpace)
	})
}

func rejoinBacktickRuns(s string) string {
	return brokenBacktickRunRE.ReplaceAllStringFunc(s, func(run string) string {
		return strings.ReplaceAll(run, zeroWidthSpace, "")
	})
}

func renderProseLine(line string) string {
	if thematicBreakRE.MatchString(line) {
		return ""
	}
	if m := headingRE.FindStringSubmatch(line); m != nil {
		return waBold(renderInline(m[1], true))
	}
	if m := bulletMarkerRE.FindStringSubmatchIndex(line); m != nil {
		return line[m[2]:m[3]] + "-" + line[m[4]:m[5]] + renderInline(line[m[1]:], false)
	}
	return renderInline(line, false)
}

func waBold(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	return "*" + s + "*"
}

var (
	codeSpanRE   = regexp.MustCompile("`[^`\n]+`")
	imageRE      = regexp.MustCompile(`!\[([^\]\n]*)\]\(([^)\s]+)(?:[ \t]+"[^"\n]*")?\)`)
	linkRE       = regexp.MustCompile(`\[([^\]\n]+)\]\(([^)\s]+)(?:[ \t]+"[^"\n]*")?\)`)
	autolinkRE   = regexp.MustCompile(`<((?:https?|mailto):[^>\s]+)>`)
	bareURLRE    = regexp.MustCompile(`https?://[^\s<>]+`)
	boldItalicRE = regexp.MustCompile(`\*\*\*([^\s*](?:.*?[^\s*])??)\*\*\*`)
	boldStarRE   = regexp.MustCompile(`\*\*([^\s*](?:.*?[^\s*])??)\*\*`)
	boldUnderRE  = regexp.MustCompile(`__([^\s_](?:.*?[^\s_])??)__`)
	italicStarRE = regexp.MustCompile(`\*([^\s*](?:[^*\n]*?[^\s*])??)\*`)
	strikeRE     = regexp.MustCompile(`~~([^\s~](?:[^~\n]*?[^\s~])??)~~`)
)

var (
	shelfOpen   = string(rune(0xE001))
	shelfClose  = string(rune(0xE002))
	shelfRE     = regexp.MustCompile(shelfOpen + `(\d+)` + shelfClose)
	shelfRuneRE = regexp.MustCompile("[" + shelfOpen + shelfClose + "]")
)

func renderInline(s string, asBold bool) string {
	var shelved []string
	restore := func(v string) string {
		return shelfRE.ReplaceAllStringFunc(v, func(p string) string {
			i, err := strconv.Atoi(shelfRE.FindStringSubmatch(p)[1])
			if err != nil || i >= len(shelved) {
				return p
			}
			return shelved[i]
		})
	}
	shelve := func(v string) string {
		shelved = append(shelved, restore(v))
		return shelfOpen + strconv.Itoa(len(shelved)-1) + shelfClose
	}

	s = shelfRuneRE.ReplaceAllStringFunc(s, shelve)
	s = codeSpanRE.ReplaceAllStringFunc(s, shelve)
	s = shelveLinks(s, shelve)
	s = renderEmphasis(s, asBold)
	return restore(s)
}

func shelveLinks(s string, shelve func(string) string) string {
	s = replaceSubmatches(imageRE, s, func(m []string) string {
		return labelledURL(m[1], m[2], shelve)
	})
	s = replaceSubmatches(linkRE, s, func(m []string) string {
		return labelledURL(m[1], m[2], shelve)
	})
	s = replaceSubmatches(autolinkRE, s, func(m []string) string {
		return shelve(bareAddress(m[1]))
	})
	return bareURLRE.ReplaceAllStringFunc(s, func(url string) string {
		trimmed := trimURLTrailer(url)
		return shelve(trimmed) + url[len(trimmed):]
	})
}

func renderEmphasis(s string, asBold bool) string {
	bold := func(inner string) string {
		if asBold {
			return italicize(inner)
		}
		return "*" + italicize(inner) + "*"
	}
	s = replaceSubmatches(boldItalicRE, s, func(m []string) string { return bold("_" + m[1] + "_") })
	s = replaceSubmatches(boldStarRE, s, func(m []string) string { return bold(m[1]) })
	s = replaceAtWordBoundaries(boldUnderRE, s, bold)
	if asBold {
		s = italicize(s)
	}
	return strikeRE.ReplaceAllString(s, "~${1}~")
}

func italicize(s string) string {
	return replaceAtWordBoundaries(italicStarRE, s, func(inner string) string {
		return "_" + inner + "_"
	})
}

func labelledURL(label, url string, shelve func(string) string) string {
	address := bareAddress(url)
	label = strings.TrimSpace(label)
	if label == "" || label == url || label == address {
		return shelve(address)
	}
	return label + ": " + shelve(address)
}

func bareAddress(url string) string {
	return strings.TrimPrefix(url, "mailto:")
}

func trimURLTrailer(url string) string {
	unmatchedClosers := strings.Count(url, ")") - strings.Count(url, "(")
	end := len(url)
	for end > 0 {
		switch last := url[end-1]; {
		case strings.IndexByte(".,;:!?*_~'\"", last) >= 0:
			end--
		case last == ')' && unmatchedClosers > 0:
			end--
			unmatchedClosers--
		default:
			return url[:end]
		}
	}
	return ""
}

func replaceAtWordBoundaries(re *regexp.Regexp, s string, wrap func(string) string) string {
	var b strings.Builder
	last := 0
	for _, m := range re.FindAllStringSubmatchIndex(s, -1) {
		if isWordRuneBefore(s, m[0]) || isWordRuneAt(s, m[1]) {
			continue
		}
		b.WriteString(s[last:m[0]])
		b.WriteString(wrap(s[m[2]:m[3]]))
		last = m[1]
	}
	b.WriteString(s[last:])
	return b.String()
}

func isWordRuneBefore(s string, i int) bool {
	r, _ := utf8.DecodeLastRuneInString(s[:i])
	return i > 0 && (unicode.IsLetter(r) || unicode.IsDigit(r))
}

func isWordRuneAt(s string, i int) bool {
	r, _ := utf8.DecodeRuneInString(s[i:])
	return i < len(s) && (unicode.IsLetter(r) || unicode.IsDigit(r))
}

func replaceSubmatches(re *regexp.Regexp, s string, repl func([]string) string) string {
	return re.ReplaceAllStringFunc(s, func(match string) string {
		return repl(re.FindStringSubmatch(match))
	})
}

var (
	tableSepCellRE = regexp.MustCompile(`^:?-+:?$`)
	htmlBreakRE    = regexp.MustCompile(`(?i)<br\s*/?>`)
	blockStartRE   = regexp.MustCompile("^[ \t]*(?:[-*+][ \t]|[0-9]+[.)][ \t]|#{1,6}(?:[ \t]|$)|>|```|~~~)")
)

func isTableStart(header, sep string) bool {
	if !strings.Contains(header, "|") || !strings.Contains(sep, "-") {
		return false
	}
	headerCells, sepCells := splitTableRow(header), splitTableRow(sep)
	if len(headerCells) != len(sepCells) {
		return false
	}
	for _, cell := range sepCells {
		if !tableSepCellRE.MatchString(cell) {
			return false
		}
	}
	return true
}

func tableEnd(lines []string, from int) int {
	for from < len(lines) && isTableRow(lines[from]) {
		from++
	}
	return from
}

func isTableRow(line string) bool {
	return strings.Contains(line, "|") && strings.TrimSpace(line) != "" && !blockStartRE.MatchString(line)
}

func tableRows(lines []string) [][]string {
	rows := make([][]string, 0, len(lines))
	for _, line := range lines {
		rows = append(rows, splitTableRow(line))
	}
	return rows
}

func splitTableRow(line string) []string {
	line = strings.TrimSpace(line)
	line = strings.TrimPrefix(line, "|")
	if strings.HasSuffix(line, "|") && !strings.HasSuffix(line, `\|`) {
		line = line[:len(line)-1]
	}
	var cells []string
	var cell strings.Builder
	for i := 0; i < len(line); i++ {
		switch {
		case line[i] == '\\' && i+1 < len(line) && line[i+1] == '|':
			cell.WriteByte('|')
			i++
		case line[i] == '|':
			cells = append(cells, strings.TrimSpace(cell.String()))
			cell.Reset()
		default:
			cell.WriteByte(line[i])
		}
	}
	return append(cells, strings.TrimSpace(cell.String()))
}

func renderTable(header []string, rows [][]string) []string {
	switch len(header) {
	case 1:
		return renderListTable(header, rows)
	case 2:
		return renderPairTable(header, rows)
	default:
		return renderLabelledBlocks(header, rows)
	}
}

func renderListTable(header []string, rows [][]string) []string {
	var out []string
	if title := waBold(tableCell(header, 0, true)); title != "" {
		out = append(out, title)
	}
	for _, row := range rows {
		if value := tableCell(row, 0, false); value != "" {
			out = append(out, "- "+value)
		}
	}
	return out
}

func renderPairTable(header []string, rows [][]string) []string {
	var out []string
	if title := waBold(joinNonEmpty(": ", tableCell(header, 0, true), tableCell(header, 1, true))); title != "" {
		out = append(out, title)
	}
	for _, row := range rows {
		if item := joinNonEmpty(": ", waBold(tableCell(row, 0, true)), tableCell(row, 1, false)); item != "" {
			out = append(out, "- "+item)
		}
	}
	return out
}

func renderLabelledBlocks(header []string, rows [][]string) []string {
	var out []string
	for _, row := range rows {
		block := renderLabelledBlock(header, row)
		if len(block) == 0 {
			continue
		}
		if len(out) > 0 {
			out = append(out, "")
		}
		out = append(out, block...)
	}
	return out
}

func renderLabelledBlock(header, row []string) []string {
	var block []string
	if name := waBold(tableCell(row, 0, true)); name != "" {
		block = append(block, name)
	}
	for i := 1; i < len(header); i++ {
		if value := tableCell(row, i, false); value != "" {
			block = append(block, joinNonEmpty(": ", tableCell(header, i, true), value))
		}
	}
	return block
}

func tableCell(row []string, i int, asBold bool) string {
	if i >= len(row) {
		return ""
	}
	return strings.TrimSpace(renderInline(htmlBreakRE.ReplaceAllString(row[i], " "), asBold))
}

func joinNonEmpty(sep string, parts ...string) string {
	kept := make([]string, 0, len(parts))
	for _, part := range parts {
		if part != "" {
			kept = append(kept, part)
		}
	}
	return strings.Join(kept, sep)
}

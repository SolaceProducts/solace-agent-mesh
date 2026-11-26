// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package slack

import (
	"regexp"
	"strings"
	"unicode/utf8"

	goslack "github.com/slack-go/slack"
)

const (
	sectionTextLimit = 3000
	maxMessageBlocks = 50
	tableMaxRows     = 100
	tableMaxCols     = 20
	tableCharBudget  = 10000
)

var sepCellRE = regexp.MustCompile(`^:?-{1,}:?$`)

func containsTable(text string) bool {
	if !strings.Contains(text, "|") {
		return false
	}
	lines := strings.Split(text, "\n")
	for i := 0; i+1 < len(lines); i++ {
		if isTableStart(lines[i], lines[i+1]) {
			return true
		}
	}
	return false
}

func isTableStart(header, sep string) bool {
	if !strings.Contains(header, "|") {
		return false
	}
	hCells := splitRow(header)
	sCells := splitRow(sep)
	if len(hCells) == 0 || len(hCells) != len(sCells) {
		return false
	}
	for _, c := range sCells {
		if !sepCellRE.MatchString(strings.TrimSpace(c)) {
			return false
		}
	}
	return true
}

func BuildMessageBlocks(text string) ([]goslack.Block, bool) {
	if !containsTable(text) {
		return nil, false
	}

	var blocks []goslack.Block
	charBudget := tableCharBudget
	hasTable := false

	for i, seg := range splitCodeBlocks(text) {
		if i%2 == 1 {
			blocks = append(blocks, proseSections(codeBlockLangRE.ReplaceAllString(seg, "```\n"))...)
			continue
		}
		segBlocks, segHadTable := nonCodeBlocks(seg, &charBudget)
		hasTable = hasTable || segHadTable
		blocks = append(blocks, segBlocks...)
	}

	if !hasTable || len(blocks) > maxMessageBlocks {
		return nil, false
	}
	return blocks, true
}

func nonCodeBlocks(seg string, charBudget *int) ([]goslack.Block, bool) {
	lines := strings.Split(seg, "\n")
	var blocks []goslack.Block
	var prose []string
	hasTable := false

	flushProse := func() {
		if len(prose) == 0 {
			return
		}
		blocks = append(blocks, proseSections(ConvertMarkdown(strings.Join(prose, "\n")))...)
		prose = prose[:0]
	}

	for i := 0; i < len(lines); {
		if i+1 < len(lines) && isTableStart(lines[i], lines[i+1]) {
			flushProse()
			header := splitRow(lines[i])
			body, next := collectTableBody(lines, i+2, len(header))
			blocks = append(blocks, tableBlock(header, body, charBudget))
			hasTable = true
			i = next
			continue
		}
		prose = append(prose, lines[i])
		i++
	}
	flushProse()
	return blocks, hasTable
}

func collectTableBody(lines []string, start, cols int) ([][]string, int) {
	var body [][]string
	i := start
	for i < len(lines) && strings.TrimSpace(lines[i]) != "" && strings.Contains(lines[i], "|") {
		body = append(body, normalizeRow(splitRow(lines[i]), cols))
		i++
	}
	return body, i
}

func explicitAlignColumnSettings(cols int) []goslack.ColumnSetting {
	settings := make([]goslack.ColumnSetting, cols)
	for i := range settings {
		settings[i] = goslack.ColumnSetting{Align: goslack.ColumnAlignmentLeft, IsWrapped: true}
	}
	return settings
}

func tableBlock(header []string, body [][]string, charBudget *int) goslack.Block {
	cols := len(header)
	rows := len(body) + 1
	cells := tableCellChars(header, body)

	if cols > tableMaxCols || rows > tableMaxRows || cells > *charBudget {
		return codeBlockSection(tableToCodeString(header, body))
	}
	*charBudget -= cells

	tb := goslack.NewTableBlock("")
	tb.WithColumnSettings(explicitAlignColumnSettings(cols)...)
	tb.AddRow(rowCells(header)...)
	for _, r := range body {
		tb.AddRow(rowCells(r)...)
	}
	return tb
}

func rowCells(row []string) []goslack.TableCell {
	cells := make([]goslack.TableCell, len(row))
	for i, c := range row {
		cells[i] = goslack.NewTableRichTextCell(goslack.NewRichTextSection(cellElements(c)...))
	}
	return cells
}

func cellElements(cell string) []goslack.RichTextSectionElement {
	s := strings.TrimSpace(strings.ReplaceAll(cell, `\|`, "|"))
	var elems []goslack.RichTextSectionElement
	for s != "" {
		bold := boldRE.FindStringSubmatchIndex(s)
		link := linkRE.FindStringSubmatchIndex(s)
		loc, isBold := earliestMatch(bold, link)
		if loc == nil {
			elems = append(elems, goslack.NewRichTextSectionTextElement(s, nil))
			break
		}
		if loc[0] > 0 {
			elems = append(elems, goslack.NewRichTextSectionTextElement(s[:loc[0]], nil))
		}
		if isBold {
			elems = append(elems, goslack.NewRichTextSectionTextElement(s[loc[2]:loc[3]],
				&goslack.RichTextSectionTextStyle{Bold: true}))
		} else {
			elems = append(elems, goslack.NewRichTextSectionLinkElement(s[loc[4]:loc[5]], s[loc[2]:loc[3]], nil))
		}
		s = s[loc[1]:]
	}
	if len(elems) == 0 {
		elems = append(elems, goslack.NewRichTextSectionTextElement(" ", nil))
	}
	return elems
}

func earliestMatch(bold, link []int) ([]int, bool) {
	switch {
	case bold == nil && link == nil:
		return nil, false
	case link == nil:
		return bold, true
	case bold == nil:
		return link, false
	case bold[0] <= link[0]:
		return bold, true
	default:
		return link, false
	}
}

func proseSections(text string) []goslack.Block {
	if strings.TrimSpace(text) == "" {
		return nil
	}
	var blocks []goslack.Block
	for _, chunk := range chunkOnLines(text, sectionTextLimit) {
		if strings.TrimSpace(chunk) == "" {
			continue
		}
		blocks = append(blocks, goslack.NewSectionBlock(
			goslack.NewTextBlockObject(goslack.MarkdownType, chunk, false, false), nil, nil))
	}
	return blocks
}

func codeBlockSection(code string) goslack.Block {
	const overhead = len("```\n") + len("\n```") + len("\n…(truncated)")
	body := code
	if len(body) > sectionTextLimit-overhead {
		body = body[:runeSafeCut(body, sectionTextLimit-overhead)] + "\n…(truncated)"
	}
	return goslack.NewSectionBlock(
		goslack.NewTextBlockObject(goslack.MarkdownType, "```\n"+body+"\n```", false, false), nil, nil)
}

func chunkOnLines(text string, limit int) []string {
	var chunks []string
	var cur strings.Builder
	flush := func() {
		if cur.Len() > 0 {
			chunks = append(chunks, cur.String())
			cur.Reset()
		}
	}
	for _, line := range strings.SplitAfter(text, "\n") {
		if cur.Len() > 0 && cur.Len()+len(line) > limit {
			flush()
		}
		for len(line) > limit {
			cut := runeSafeCut(line, limit)
			cur.WriteString(line[:cut])
			flush()
			line = line[cut:]
		}
		cur.WriteString(line)
	}
	flush()
	return chunks
}

func runeSafeCut(text string, limit int) int {
	if limit >= len(text) {
		return len(text)
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	if cut == 0 {
		return limit
	}
	return cut
}

func splitRow(line string) []string {
	line = strings.TrimSpace(line)
	line = strings.TrimPrefix(line, "|")
	line = strings.TrimSuffix(line, "|")
	var cells []string
	var cur strings.Builder
	for i := 0; i < len(line); i++ {
		if line[i] == '\\' && i+1 < len(line) && line[i+1] == '|' {
			cur.WriteString(`\|`)
			i++
			continue
		}
		if line[i] == '|' {
			cells = append(cells, strings.TrimSpace(cur.String()))
			cur.Reset()
			continue
		}
		cur.WriteByte(line[i])
	}
	cells = append(cells, strings.TrimSpace(cur.String()))
	return cells
}

func normalizeRow(row []string, cols int) []string {
	if len(row) == cols {
		return row
	}
	out := make([]string, cols)
	copy(out, row)
	return out
}

func tableCellChars(header []string, body [][]string) int {
	total := 0
	for _, c := range header {
		total += len(c)
	}
	for _, r := range body {
		for _, c := range r {
			total += len(c)
		}
	}
	return total
}

func tableToCodeString(header []string, body [][]string) string {
	cols := len(header)
	widths := make([]int, cols)
	rows := append([][]string{header}, body...)
	for _, r := range rows {
		for i := 0; i < cols && i < len(r); i++ {
			if w := utf8.RuneCountInString(r[i]); w > widths[i] {
				widths[i] = w
			}
		}
	}
	var b strings.Builder
	for _, r := range rows {
		for i := range cols {
			cell := ""
			if i < len(r) {
				cell = r[i]
			}
			b.WriteString(cell)
			if i < cols-1 {
				b.WriteString(strings.Repeat(" ", widths[i]-utf8.RuneCountInString(cell)+2))
			}
		}
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

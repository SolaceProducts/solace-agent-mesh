// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package render

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

const (
	SeverityError   = "error"
	SeverityWarning = "warning"
)

type Finding struct {
	Check    string `json:"check"`
	Severity string `json:"severity"`
	Bubble   int    `json:"bubble"`
	Detail   string `json:"detail"`
}

type Options struct {
	Artifacts []string
}

const (
	largeTableRows               = 20
	duplicateKeyShareNumerator   = 4
	duplicateKeyShareDenominator = 5
	minDuplicateRows             = 3
	minDuplicateParagraph        = 160
)

var (
	internalNote  = regexp.MustCompile(`(?i)note (from sam )?to the calling agent`)
	rawEmbed      = regexp.MustCompile(`«[^»\n]{0,120}»?`)
	rawTemplate   = regexp.MustCompile(`\{\{[^}]*\}\}|\{%[^%]*%\}|\[Template Error`)
	rawHTML       = regexp.MustCompile(`(?i)<(div|table|tr|td|span|script|style|iframe)\b`)
	tableLine     = regexp.MustCompile(`^\s*\|.*\|\s*$`)
	fenceLine     = regexp.MustCompile("^\\s*(```|~~~)")
	retractPhrase = regexp.MustCompile(`(?i)(my (previous|earlier) (answer|response|table) was (wrong|incorrect)|i (was wrong|made an error|made a mistake)|please disregard|ignore my (previous|earlier))`)
)

func Check(bubbles []Bubble, opts Options) []Finding {
	c := &checker{
		known:            map[string]bool{},
		haveList:         opts.Artifacts != nil,
		tablesByTask:     map[string][]tableRef{},
		paragraphsByTask: map[string]map[string]int{},
		answered:         map[string]bool{},
	}
	for _, a := range opts.Artifacts {
		c.known[stripVersion(a)] = true
	}
	for i, b := range bubbles {
		if b.Role == roleAgent {
			c.checkBubble(i, b)
		}
	}
	c.checkTasks()
	return c.out
}

type checker struct {
	out              []Finding
	known            map[string]bool
	haveList         bool
	tablesByTask     map[string][]tableRef
	paragraphsByTask map[string]map[string]int
	answered         map[string]bool
	tasks            []string
}

func (c *checker) add(check, severity string, bubble int, format string, args ...any) {
	c.out = append(c.out, Finding{Check: check, Severity: severity, Bubble: bubble, Detail: fmt.Sprintf(format, args...)})
}

func (c *checker) checkBubble(i int, b Bubble) {
	if !slices.Contains(c.tasks, b.TaskID) {
		c.tasks = append(c.tasks, b.TaskID)
	}
	if hasVisible(b.Parts) || b.InputRequest != "" || b.AuthRequest {
		c.answered[b.TaskID] = true
	}
	if b.IsWarning {
		c.add("warning_bubble", SeverityWarning, i, "warning shown to the user: %s", preview(bubbleText(b)))
	} else if b.IsError {
		c.add("error_bubble", SeverityError, i, "error shown to the user: %s", preview(bubbleText(b)))
	}
	cards := map[string]bool{}
	for _, seg := range Segments(b) {
		switch seg.Kind {
		case partArtifact, partFile:
			c.checkCardSegment(i, seg, cards)
		case partText:
			checkText(seg, b.Complete, i, c.add)
			c.checkBlocks(i, b.TaskID, seg.Blocks)
		}
	}
}

func (c *checker) checkCardSegment(i int, seg Segment, cards map[string]bool) {
	checkCard(seg, i, c.known, c.haveList, c.add)
	name := stripVersion(seg.Name)
	if name == "" {
		return
	}
	if cards[name] {
		c.add("duplicate_card", SeverityError, i, "%q is shown as two cards in one message", seg.Name)
	}
	cards[name] = true
}

func (c *checker) checkBlocks(i int, taskID string, blocks []Block) {
	for _, blk := range blocks {
		switch blk.Kind {
		case blockTable:
			c.tablesByTask[taskID] = append(c.tablesByTask[taskID], tableRef{bubble: i, block: blk})
			if blk.Rows > largeTableRows {
				c.add("large_table", SeverityWarning, i, "a %d-row table is in the answer text; fine for a rendered template, a problem if the model typed it", blk.Rows)
			}
		case blockParagraph:
			c.checkParagraph(i, taskID, blk)
		}
	}
}

func (c *checker) checkParagraph(i int, taskID string, blk Block) {
	key := strings.Join(strings.Fields(blk.Text), " ")
	if len(key) < minDuplicateParagraph {
		return
	}
	if c.paragraphsByTask[taskID] == nil {
		c.paragraphsByTask[taskID] = map[string]int{}
	}
	if first, seen := c.paragraphsByTask[taskID][key]; seen {
		c.add("duplicate_text", SeverityWarning, i, "paragraph repeated from bubble %d: %s", first, preview(key))
		return
	}
	c.paragraphsByTask[taskID][key] = i
}

func (c *checker) checkTasks() {
	for _, task := range c.tasks {
		if !c.answered[task] {
			c.add("empty_answer", SeverityError, -1, "task %s finished with nothing visible in the chat", task)
		}
		c.checkRepeatedTables(c.tablesByTask[task])
	}
	if len(c.tasks) == 0 {
		c.add("empty_answer", SeverityError, -1, "no agent message at all")
	}
}

func (c *checker) checkRepeatedTables(refs []tableRef) {
	for j := 1; j < len(refs); j++ {
		for k := 0; k < j; k++ {
			if sameTable(refs[k].block, refs[j].block) {
				c.add("duplicate_table", SeverityError, refs[j].bubble, "the %d-row table from bubble %d is shown again (header %s)", refs[j].block.Rows, refs[k].bubble, strings.Join(refs[j].block.Header, " | "))
				break
			}
		}
	}
}

type tableRef struct {
	bubble int
	block  Block
}

type addFunc func(check, severity string, bubble int, format string, args ...any)

func checkCard(seg Segment, i int, known map[string]bool, haveList bool, add addFunc) {
	switch seg.Status {
	case statusFailed:
		add("artifact_failed", SeverityError, i, "artifact card %q failed", seg.Name)
	case statusInProgress:
		add("artifact_stuck", SeverityError, i, "artifact card %q is still in progress", seg.Name)
	}
	if haveList && seg.Name != "" && !known[stripVersion(seg.Name)] {
		add("missing_artifact", SeverityError, i, "card %q names an artifact the session does not have", seg.Name)
	}
}

func checkText(seg Segment, complete bool, i int, add addFunc) {
	prose, fences := splitProse(seg.Text)
	if m := internalNote.FindString(prose); m != "" {
		add("internal_note", SeverityError, i, "SAM's note to the calling agent reached the user: %q", m)
	}
	if m := rawEmbed.FindString(prose); m != "" {
		add("raw_embed", SeverityError, i, "unresolved embed or block marker shown as text: %q", m)
	}
	if m := rawTemplate.FindString(prose); m != "" {
		add("raw_template", SeverityError, i, "template syntax or template error shown as text: %q", m)
	}
	if m := rawHTML.FindString(prose); m != "" {
		add("raw_html", SeverityWarning, i, "raw HTML in the answer: %q", m)
	}
	if m := retractPhrase.FindString(prose); m != "" {
		add("retraction", SeverityWarning, i, "the answer takes back earlier output: %q", m)
	}
	if complete && fences%2 == 1 {
		add("unclosed_fence", SeverityError, i, "a code fence is never closed, so the rest of the answer renders as code")
	}
	parsed := 0
	for _, blk := range seg.Blocks {
		if blk.Kind == blockTable {
			parsed += blk.Rows + 2
		}
	}
	pipeLines := 0
	for _, line := range strings.Split(prose, "\n") {
		if tableLine.MatchString(line) {
			pipeLines++
		}
	}
	if pipeLines > parsed {
		add("malformed_table", SeverityError, i, "%d table-looking lines did not render as a table", pipeLines-parsed)
	}
}

func splitProse(text string) (string, int) {
	var sb strings.Builder
	fences := 0
	for _, line := range strings.Split(text, "\n") {
		if fenceLine.MatchString(line) {
			fences++
			continue
		}
		if fences%2 == 0 {
			sb.WriteString(line)
			sb.WriteByte('\n')
		}
	}
	return sb.String(), fences
}

func sameTable(a, b Block) bool {
	if a.Rows < minDuplicateRows || b.Rows < minDuplicateRows {
		return false
	}
	keys := map[string]bool{}
	for _, row := range a.Cells {
		if len(row) > 0 {
			keys[strings.ToLower(row[0])] = true
		}
	}
	shared := 0
	for _, row := range b.Cells {
		if len(row) > 0 && keys[strings.ToLower(row[0])] {
			shared++
		}
	}
	return shared*duplicateKeyShareDenominator >= min(a.Rows, b.Rows)*duplicateKeyShareNumerator
}

func bubbleText(b Bubble) string {
	var sb strings.Builder
	for _, p := range b.Parts {
		sb.WriteString(p.Text)
	}
	return sb.String()
}

func HasErrors(findings []Finding) bool {
	return slices.ContainsFunc(findings, func(f Finding) bool { return f.Severity == SeverityError })
}

// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package render

import (
	"fmt"
	"io"
	"strings"
)

type Report struct {
	Bubbles  []ReportBubble `json:"bubbles"`
	Findings []Finding      `json:"findings"`
}

type ReportBubble struct {
	Bubble
	Segments []Segment `json:"segments"`
}

func NewReport(bubbles []Bubble, opts Options) Report {
	r := Report{Findings: Check(bubbles, opts)}
	for _, b := range bubbles {
		r.Bubbles = append(r.Bubbles, ReportBubble{Bubble: b, Segments: Segments(b)})
	}
	if r.Findings == nil {
		r.Findings = []Finding{}
	}
	return r
}

func (r Report) WriteText(w io.Writer) {
	for i, b := range r.Bubbles {
		label := b.Role
		switch {
		case b.IsWarning:
			label += ", warning"
		case b.IsError:
			label += ", error"
		case b.InputRequest != "":
			label += ", asks the user"
		case b.AuthRequest:
			label += ", sign-in prompt"
		}
		_, _ = fmt.Fprintf(w, "[%d] %s  task %s\n", i, label, b.TaskID)
		for _, seg := range b.Segments {
			if seg.Kind != partText {
				_, _ = fmt.Fprintf(w, "    %s card %q %s\n", seg.Kind, seg.Name, seg.Status)
				continue
			}
			for _, blk := range seg.Blocks {
				_, _ = fmt.Fprintf(w, "    %s\n", describeBlock(blk))
			}
		}
	}
	_, _ = fmt.Fprintln(w)
	if len(r.Findings) == 0 {
		_, _ = fmt.Fprintln(w, "No rendering problems found.")
		return
	}
	for _, f := range r.Findings {
		where := "all"
		if f.Bubble >= 0 {
			where = fmt.Sprintf("%d", f.Bubble)
		}
		_, _ = fmt.Fprintf(w, "%-7s %-16s bubble %s: %s\n", strings.ToUpper(f.Severity), f.Check, where, f.Detail)
	}
}

func describeBlock(b Block) string {
	switch b.Kind {
	case blockTable:
		return fmt.Sprintf("table %d rows: %s", b.Rows, strings.Join(b.Header, " | "))
	case blockCode:
		return fmt.Sprintf("code %s: %s", b.Lang, b.Preview)
	default:
		return fmt.Sprintf("%s: %s", b.Kind, b.Preview)
	}
}

// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package coder

import (
	"fmt"
	"strings"
)

const (
	diffContextLines = 3
	lcsMaxCells      = 1 << 22
)

type lineOp struct {
	kind byte
	line string
}

const (
	opEqual  = ' '
	opDelete = '-'
	opInsert = '+'
)

func unifiedHunks(a, b string) (string, int, int) {
	ops := diffLines(splitLines(a), splitLines(b))
	oldBefore := make([]int, len(ops)+1)
	newBefore := make([]int, len(ops)+1)
	insertions, deletions := 0, 0
	for i, op := range ops {
		oldBefore[i+1], newBefore[i+1] = oldBefore[i], newBefore[i]
		if op.kind != opInsert {
			oldBefore[i+1]++
		}
		if op.kind != opDelete {
			newBefore[i+1]++
		}
		switch op.kind {
		case opInsert:
			insertions++
		case opDelete:
			deletions++
		}
	}
	var out strings.Builder
	for i := 0; i < len(ops); {
		if ops[i].kind == opEqual {
			i++
			continue
		}
		start, end := max(i-diffContextLines, 0), hunkEnd(ops, i)
		oldCount := oldBefore[end] - oldBefore[start]
		newCount := newBefore[end] - newBefore[start]
		fmt.Fprintf(&out, "@@ -%s +%s @@\n", hunkRange(oldBefore[start], oldCount), hunkRange(newBefore[start], newCount))
		for _, op := range ops[start:end] {
			out.WriteByte(op.kind)
			out.WriteString(op.line)
			if !strings.HasSuffix(op.line, "\n") {
				out.WriteString("\n\\ No newline at end of file\n")
			}
		}
		i = end
	}
	return out.String(), insertions, deletions
}

func hunkEnd(ops []lineOp, i int) int {
	end := i
	for {
		for end < len(ops) && ops[end].kind != opEqual {
			end++
		}
		next := end
		for next < len(ops) && ops[next].kind == opEqual {
			next++
		}
		if next == len(ops) || next-end > 2*diffContextLines {
			return min(end+diffContextLines, len(ops))
		}
		end = next
	}
}

func hunkRange(linesBefore, count int) string {
	switch count {
	case 0:
		return fmt.Sprintf("%d,0", linesBefore)
	case 1:
		return fmt.Sprintf("%d", linesBefore+1)
	}
	return fmt.Sprintf("%d,%d", linesBefore+1, count)
}

func splitLines(s string) []string {
	lines := strings.SplitAfter(s, "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func diffLines(a, b []string) []lineOp {
	head := 0
	for head < len(a) && head < len(b) && a[head] == b[head] {
		head++
	}
	tail := 0
	for tail < len(a)-head && tail < len(b)-head && a[len(a)-1-tail] == b[len(b)-1-tail] {
		tail++
	}
	ops := make([]lineOp, 0, len(a)+len(b))
	for _, l := range a[:head] {
		ops = append(ops, lineOp{opEqual, l})
	}
	ops = append(ops, lcsOps(a[head:len(a)-tail], b[head:len(b)-tail])...)
	for _, l := range a[len(a)-tail:] {
		ops = append(ops, lineOp{opEqual, l})
	}
	return ops
}

func lcsOps(a, b []string) []lineOp {
	n, m := len(a), len(b)
	ops := make([]lineOp, 0, n+m)
	if n*m > lcsMaxCells {
		for _, l := range a {
			ops = append(ops, lineOp{opDelete, l})
		}
		for _, l := range b {
			ops = append(ops, lineOp{opInsert, l})
		}
		return ops
	}
	width := m + 1
	common := make([]int32, (n+1)*width)
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				common[i*width+j] = common[(i+1)*width+j+1] + 1
			} else {
				common[i*width+j] = max(common[(i+1)*width+j], common[i*width+j+1])
			}
		}
	}
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case a[i] == b[j]:
			ops = append(ops, lineOp{opEqual, a[i]})
			i++
			j++
		case common[(i+1)*width+j] >= common[i*width+j+1]:
			ops = append(ops, lineOp{opDelete, a[i]})
			i++
		default:
			ops = append(ops, lineOp{opInsert, b[j]})
			j++
		}
	}
	for ; i < n; i++ {
		ops = append(ops, lineOp{opDelete, a[i]})
	}
	for ; j < m; j++ {
		ops = append(ops, lineOp{opInsert, b[j]})
	}
	return ops
}

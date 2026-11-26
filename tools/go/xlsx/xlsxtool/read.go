// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package xlsxtool

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	sdk "github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"
	"github.com/xuri/excelize/v2"
)

const defaultMaxCells = 2000

const searchMatchCap = 100

func readWorkbook(_ context.Context, p ReadParams, _ *sdk.ToolContext) (*sdk.Result, error) {
	if len(p.Source.Content) == 0 {
		return sdk.Error("source workbook is empty or was not provided"), nil
	}
	f, err := excelize.OpenReader(bytes.NewReader(p.Source.Content))
	if err != nil {
		return sdk.Error(fmt.Sprintf("cannot open %q as .xlsx: %v", p.Source.Filename, err)), nil
	}
	defer func() { _ = f.Close() }()

	mode := "describe"
	if p.Mode != nil && *p.Mode != "" {
		mode = *p.Mode
	}

	switch mode {
	case "describe":
		return describeWorkbook(f)
	case "values":
		return readCells(f, p, false)
	case "formulas":
		return readCells(f, p, true)
	case "search":
		return searchWorkbook(f, p)
	default:
		return sdk.Error(fmt.Sprintf("unknown mode %q (expected describe, values, formulas, or search)", mode)), nil
	}
}

func describeWorkbook(f *excelize.File) (*sdk.Result, error) {
	var b strings.Builder
	sheets := f.GetSheetList()
	fmt.Fprintf(&b, "Workbook: %d sheet(s)\n", len(sheets))
	for _, name := range sheets {
		lastCol, lastRow := usedBounds(f, name)
		dim := "A1"
		if cell, err := excelize.CoordinatesToCellName(max(lastCol, 1), max(lastRow, 1)); err == nil {
			dim = "A1:" + cell
		}
		fmt.Fprintf(&b, "\n### %s\n- dimension: %s (%d rows × %d cols)\n", name, dim, lastRow, lastCol)
		if header := headerRow(f, name, lastCol); len(header) > 0 {
			fmt.Fprintf(&b, "- header (row 1): %s\n", strings.Join(header, " | "))
		}
		if merges, _ := f.GetMergeCells(name); len(merges) > 0 {
			refs := make([]string, 0, len(merges))
			for _, m := range merges {
				refs = append(refs, m.GetStartAxis()+":"+m.GetEndAxis())
			}
			fmt.Fprintf(&b, "- merged: %s\n", strings.Join(refs, ", "))
		}
	}
	if names := f.GetDefinedName(); len(names) > 0 {
		b.WriteString("\n### Defined names\n")
		for _, dn := range names {
			fmt.Fprintf(&b, "- %s → %s\n", dn.Name, dn.RefersTo)
		}
	}
	b.WriteString("\nUse mode=values with a range/rows window, or mode=search, to read cell contents.")
	return sdk.OK(b.String()), nil
}

func headerRow(f *excelize.File, sheet string, lastCol int) []string {
	if lastCol < 1 {
		return nil
	}
	var header []string
	for col := 1; col <= lastCol; col++ {
		cell, err := excelize.CoordinatesToCellName(col, 1)
		if err != nil {
			break
		}
		v, _ := f.GetCellValue(sheet, cell)
		header = append(header, v)
	}
	return header
}

func readCells(f *excelize.File, p ReadParams, formulas bool) (*sdk.Result, error) {
	sheet := firstSheet(f, p.Sheet)
	if sheet == "" {
		return sdk.Error("workbook has no sheets"), nil
	}

	c1, c2, r1, r2, err := resolveReadRange(f, sheet, p)
	if err != nil {
		return sdk.Error(err.Error()), nil
	}

	maxCells := defaultMaxCells
	if p.MaxCells != nil && *p.MaxCells > 0 {
		maxCells = *p.MaxCells
	}
	if count := (c2 - c1 + 1) * (r2 - r1 + 1); count > maxCells {
		lastCol, lastRow := usedBounds(f, sheet)
		return sdk.OK(fmt.Sprintf(
			"Sheet %q has data through %d rows × %d cols — the requested area is %d cells, over the %d-cell cap. Narrow it with a 'range' (e.g. A1:F40), a 'rows' window (e.g. 1-50), or use mode=search to locate specific cells.",
			sheet, lastRow, lastCol, count, maxCells)), nil
	}

	grid := make([][]string, 0, r2-r1+1)
	for row := r1; row <= r2; row++ {
		rec := make([]string, 0, c2-c1+1)
		for col := c1; col <= c2; col++ {
			cell, cerr := excelize.CoordinatesToCellName(col, row)
			if cerr != nil {
				return sdk.Error(cerr.Error()), nil
			}
			var v string
			if formulas {
				v, _ = f.GetCellFormula(sheet, cell)
				if v != "" {
					v = "=" + v
				}
			} else {
				v, _ = f.GetCellValue(sheet, cell)
			}
			rec = append(rec, v)
		}
		grid = append(grid, rec)
	}

	format := "markdown"
	if p.Format != nil && *p.Format != "" {
		format = *p.Format
	}
	out, err := renderGrid(grid, format)
	if err != nil {
		return sdk.Error(err.Error()), nil
	}
	return sdk.OK(fmt.Sprintf("Sheet %q:\n\n%s", sheet, out)), nil
}

func searchWorkbook(f *excelize.File, p ReadParams) (*sdk.Result, error) {
	if p.Query == nil || *p.Query == "" {
		return sdk.Error("search mode requires 'query'"), nil
	}
	regex := p.Regex != nil && *p.Regex
	sheets := f.GetSheetList()
	if p.Sheet != nil && *p.Sheet != "" {
		sheets = []string{*p.Sheet}
	}

	var b strings.Builder
	total := 0
	truncated := false
	for _, sheet := range sheets {
		coords, err := f.SearchSheet(sheet, *p.Query, regex)
		if err != nil {
			continue
		}
		for _, coord := range coords {
			if total >= searchMatchCap {
				truncated = true
				break
			}
			val, _ := f.GetCellValue(sheet, coord)
			fmt.Fprintf(&b, "- %s!%s = %s\n", sheet, coord, val)
			total++
		}
		if truncated {
			break
		}
	}

	if total == 0 {
		return sdk.OK(fmt.Sprintf("No cells matched %q.", *p.Query)), nil
	}
	msg := fmt.Sprintf("%d match(es) for %q:\n\n%s", total, *p.Query, b.String())
	if truncated {
		msg += fmt.Sprintf("\n(stopped at %d matches — refine the query to narrow)", searchMatchCap)
	}
	return sdk.OK(msg), nil
}

func resolveReadRange(f *excelize.File, sheet string, p ReadParams) (c1, c2, r1, r2 int, err error) {
	switch {
	case p.Range != nil && *p.Range != "":
		tl, br := splitRange(*p.Range)
		c1, r1, err = excelize.CellNameToCoordinates(tl)
		if err != nil {
			return 0, 0, 0, 0, fmt.Errorf("invalid range %q: %v", *p.Range, err)
		}
		c2, r2, err = excelize.CellNameToCoordinates(br)
		if err != nil {
			return 0, 0, 0, 0, fmt.Errorf("invalid range %q: %v", *p.Range, err)
		}
		return c1, c2, r1, r2, nil
	case p.Rows != nil && *p.Rows != "":
		r1, r2, err = parseRowsSpec(*p.Rows)
		if err != nil {
			return 0, 0, 0, 0, err
		}
		lastCol, _ := usedBounds(f, sheet)
		return 1, max(lastCol, 1), r1, r2, nil
	default:
		lastCol, lastRow := usedBounds(f, sheet)
		return 1, max(lastCol, 1), 1, max(lastRow, 1), nil
	}
}

func parseRowsSpec(spec string) (start, end int, err error) {
	a, b, hasRange := strings.Cut(spec, "-")
	start, err = strconv.Atoi(strings.TrimSpace(a))
	if err != nil {
		return 0, 0, fmt.Errorf("invalid rows spec %q (expected e.g. 1-100)", spec)
	}
	end = start
	if hasRange {
		end, err = strconv.Atoi(strings.TrimSpace(b))
		if err != nil {
			return 0, 0, fmt.Errorf("invalid rows spec %q (expected e.g. 1-100)", spec)
		}
	}
	if end < start {
		start, end = end, start
	}
	return start, end, nil
}

func usedBounds(f *excelize.File, sheet string) (lastCol, lastRow int) {
	rows, err := f.GetRows(sheet)
	if err != nil {
		return 0, 0
	}
	lastRow = len(rows)
	for _, r := range rows {
		if len(r) > lastCol {
			lastCol = len(r)
		}
	}
	return lastCol, lastRow
}

func firstSheet(f *excelize.File, want *string) string {
	if want != nil && *want != "" {
		return *want
	}
	list := f.GetSheetList()
	if len(list) == 0 {
		return ""
	}
	return list[0]
}

func renderGrid(grid [][]string, format string) (string, error) {
	switch format {
	case "markdown":
		return renderMarkdown(grid), nil
	case "csv":
		return renderCSV(grid)
	case "json_rows":
		out, err := json.Marshal(grid)
		return string(out), err
	default:
		return "", fmt.Errorf("unknown format %q (expected markdown, csv, or json_rows)", format)
	}
}

func renderMarkdown(grid [][]string) string {
	if len(grid) == 0 {
		return "_(empty)_"
	}
	width := 0
	for _, r := range grid {
		if len(r) > width {
			width = len(r)
		}
	}
	var b strings.Builder
	writeRow := func(cells []string) {
		b.WriteString("|")
		for i := range width {
			v := ""
			if i < len(cells) {
				v = strings.ReplaceAll(cells[i], "|", "\\|")
			}
			fmt.Fprintf(&b, " %s |", v)
		}
		b.WriteString("\n")
	}
	writeRow(grid[0])
	b.WriteString("|")
	b.WriteString(strings.Repeat(" --- |", width))
	b.WriteString("\n")
	for _, r := range grid[1:] {
		writeRow(r)
	}
	return b.String()
}

func renderCSV(grid [][]string) (string, error) {
	var b bytes.Buffer
	w := csv.NewWriter(&b)
	if err := w.WriteAll(grid); err != nil {
		return "", err
	}
	w.Flush()
	return b.String(), w.Error()
}

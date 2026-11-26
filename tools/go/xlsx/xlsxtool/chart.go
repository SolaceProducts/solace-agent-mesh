// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package xlsxtool

import (
	"fmt"
	"strings"

	"github.com/xuri/excelize/v2"
)

func quoteSheetRef(ref string) string {
	idx := strings.LastIndex(ref, "!")
	if idx < 0 {
		return ref
	}
	sheet, cells := ref[:idx], ref[idx+1:]
	if sheet == "" || strings.HasPrefix(sheet, "'") {
		return ref
	}
	return "'" + strings.ReplaceAll(sheet, "'", "''") + "'!" + cells
}

var chartTypes = map[string]excelize.ChartType{
	"col":      excelize.Col,
	"bar":      excelize.Bar,
	"line":     excelize.Line,
	"pie":      excelize.Pie,
	"doughnut": excelize.Doughnut,
	"area":     excelize.Area,
	"scatter":  excelize.Scatter,
	"radar":    excelize.Radar,
}

func opAddChart(f *excelize.File, op Operation) error {
	sheet, err := sheetName(f, op)
	if err != nil {
		return err
	}
	if op.Anchor == nil || *op.Anchor == "" {
		return fmt.Errorf("add_chart requires 'anchor' (top-left cell, e.g. F2)")
	}
	if op.Chart == nil {
		return fmt.Errorf("add_chart requires 'chart'")
	}
	ct, ok := chartTypes[op.Chart.Type]
	if !ok {
		return fmt.Errorf("unknown chart type %q (expected col, bar, line, pie, doughnut, area, scatter, or radar)", op.Chart.Type)
	}
	if len(op.Chart.Series) == 0 {
		return fmt.Errorf("add_chart requires at least one series")
	}

	series := make([]excelize.ChartSeries, len(op.Chart.Series))
	for i, s := range op.Chart.Series {
		if strings.TrimSpace(s.Values) == "" {
			return fmt.Errorf("chart series %d requires 'values' (a range ref like Sheet1!$B$2:$B$10)", i+1)
		}
		if !strings.Contains(s.Name, "!") {
			return fmt.Errorf("chart series %d 'name' must be a cell reference to the label cell, e.g. Sheet1!$B$1 (a literal string is not supported); got %q", i+1, s.Name)
		}
		series[i] = excelize.ChartSeries{
			Name:       quoteSheetRef(s.Name),
			Categories: quoteSheetRef(s.Categories),
			Values:     quoteSheetRef(s.Values),
		}
	}

	chart := &excelize.Chart{Type: ct, Series: series}
	if op.Chart.Title != nil && *op.Chart.Title != "" {
		chart.Title = excelize.ChartTitle{Paragraph: []excelize.RichTextRun{{Text: *op.Chart.Title}}}
	}

	return f.AddChart(sheet, *op.Anchor, chart)
}

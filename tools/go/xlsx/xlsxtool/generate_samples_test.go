// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package xlsxtool

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	sdk "github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"
)

func TestGenerateSamples(t *testing.T) {
	outDir := os.Getenv("XLSX_GEN")
	if outDir == "" {
		t.Skip("set XLSX_GEN=<dir> to generate sample workbooks")
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	for _, s := range sampleWorkbooks() {
		res, err := writeWorkbook(context.Background(), s.params, &sdk.ToolContext{})
		if err != nil {
			t.Fatalf("%s: write error: %v", s.file, err)
		}
		if res.Status != sdk.StatusSuccess {
			t.Fatalf("%s: %s", s.file, res.Message)
		}
		path := filepath.Join(outDir, s.file)
		if err := os.WriteFile(path, res.DataObjects[0].Content, 0o644); err != nil {
			t.Fatalf("%s: %v", s.file, err)
		}
		t.Logf("wrote %s — %s", path, res.Message)
	}
}

type sample struct {
	file   string
	params WriteParams
}

func sampleWorkbooks() []sample {
	return []sample{
		sampleFormulas(),
		sampleDataFill(),
		sampleCharts(),
		sampleMultiSheet(),
		sampleStyles(),
		sampleBoundedFill(),
		sampleKitchenSink(),
	}
}

func art(name string, content string) sdk.Artifact {
	return sdk.Artifact{Filename: name, Content: []byte(content)}
}

func sampleFormulas() sample {
	return sample{
		file: "01_formulas.xlsx",
		params: WriteParams{
			OutputName: new("01_formulas"),
			Operations: &[]Operation{
				{Op: "rename_sheet", Sheet: new("Sheet1"), NewName: new("Budget")},
				{Op: "set_values", Sheet: new("Budget"), Anchor: new("A1"), Values: [][]any{
					{"Category", "Q1", "Q2", "Q3", "Q4", "Total", "Average", "Status"},
					{"Engineering", 120000, 135000, 128000, 150000},
					{"Marketing", 80000, 92000, 88000, 105000},
					{"Sales", 60000, 70000, 75000, 90000},
					{"Operations", 40000, 42000, 41000, 45000},
				}},
				{Op: "set_cell", Sheet: new("Budget"), Cell: new("A6"), Value: "TOTAL"},
				{Op: "set_formula_range", Sheet: new("Budget"), Range: new("F2:F5"), Formula: new("=SUM(B2:E2)")},
				{Op: "set_formula_range", Sheet: new("Budget"), Range: new("G2:G5"), Formula: new("=AVERAGE(B2:E2)")},
				{Op: "set_formula_range", Sheet: new("Budget"), Range: new("H2:H5"), Formula: new("=IF(F2>450000,\"over\",\"ok\")")},
				{Op: "set_formula_range", Sheet: new("Budget"), Range: new("B6:F6"), Formula: new("=SUM(B2:B5)")},
				{Op: "set_style", Sheet: new("Budget"), Range: new("A1:H1"), Style: &CellStyle{Bold: new(true), FontColor: new("FFFFFF"), Fill: new("305496"), Align: new("center")}},
				{Op: "set_style", Sheet: new("Budget"), Range: new("A6:H6"), Style: &CellStyle{Bold: new(true), Fill: new("D9E1F2")}},
				{Op: "set_style", Sheet: new("Budget"), Range: new("B2:F6"), Style: &CellStyle{NumberFormat: new("$#,##0")}},
				{Op: "set_style", Sheet: new("Budget"), Range: new("G2:G5"), Style: &CellStyle{NumberFormat: new("$#,##0.00")}},
				{Op: "set_col_width", Sheet: new("Budget"), Cols: new("A:H"), Width: new(14.0)},
				{Op: "freeze_panes", Sheet: new("Budget"), Cell: new("B2")},
			},
		},
	}
}

func sampleDataFill() sample {
	salesCSV := "region,month,units,revenue\n" +
		"North,Jan,120,24000\nNorth,Feb,135,27000\nNorth,Mar,150,30000\n" +
		"South,Jan,90,18000\nSouth,Feb,110,22000\nSouth,Mar,130,26000\n" +
		"West,Jan,75,15000\nWest,Feb,88,17600\nWest,Mar,95,19000\n" +
		"East,Jan,60,12000\nEast,Feb,70,14000\nEast,Mar,82,16400\n"

	peopleJSON := `[
		{"name":"Alice","dept":"Engineering","level":5,"remote":true},
		{"name":"Bob","dept":"Sales","level":3,"remote":false},
		{"name":"Carol","dept":"Marketing","level":4,"remote":true},
		{"name":"Dave","dept":"Engineering","level":2,"remote":false}
	]`

	return sample{
		file: "02_data_fill.xlsx",
		params: WriteParams{
			OutputName:  new("02_data_fill"),
			DataSources: &[]sdk.Artifact{art("sales.csv", salesCSV), art("people.json", peopleJSON)},
			Operations: &[]Operation{
				{Op: "rename_sheet", Sheet: new("Sheet1"), NewName: new("Sales")},
				{Op: "fill_from_data", Sheet: new("Sales"), Anchor: new("A1"), Source: new("sales.csv"), IncludeHeader: new(true)},
				{Op: "set_cell", Sheet: new("Sales"), Cell: new("E1"), Value: "Unit Price"},
				{Op: "set_formula_range", Sheet: new("Sales"), Range: new("E2:E13"), Formula: new("=D2/C2")},
				{Op: "set_style", Sheet: new("Sales"), Range: new("A1:E1"), Style: &CellStyle{Bold: new(true), FontColor: new("FFFFFF"), Fill: new("4472C4")}},
				{Op: "set_style", Sheet: new("Sales"), Range: new("D2:D13"), Style: &CellStyle{NumberFormat: new("$#,##0")}},
				{Op: "set_style", Sheet: new("Sales"), Range: new("E2:E13"), Style: &CellStyle{NumberFormat: new("$#,##0.00")}},
				{Op: "set_col_width", Sheet: new("Sales"), Cols: new("A:E"), Width: new(13.0)},
				{Op: "freeze_panes", Sheet: new("Sales"), Cell: new("A2")},
				{Op: "auto_filter", Sheet: new("Sales"), Range: new("A1:E13")},

				{Op: "create_sheet", Name: new("Roster")},
				{Op: "fill_from_data", Sheet: new("Roster"), Anchor: new("A1"), Source: new("people.json"), Format: new("json"),
					Columns: []string{"name", "dept", "level", "remote"}, IncludeHeader: new(true)},
				{Op: "set_style", Sheet: new("Roster"), Range: new("A1:D1"), Style: &CellStyle{Bold: new(true), Fill: new("E2EFDA"), Border: new("thin")}},
				{Op: "set_col_width", Sheet: new("Roster"), Cols: new("A:D"), Width: new(14.0)},
			},
		},
	}
}

func sampleCharts() sample {
	return sample{
		file: "03_charts.xlsx",
		params: WriteParams{
			OutputName: new("03_charts"),
			Operations: &[]Operation{
				{Op: "rename_sheet", Sheet: new("Sheet1"), NewName: new("Data")},
				{Op: "set_values", Sheet: new("Data"), Anchor: new("A1"), Values: [][]any{
					{"Quarter", "Revenue", "Cost"},
					{"Q1", 24000, 15000},
					{"Q2", 27000, 16000},
					{"Q3", 30000, 17000},
					{"Q4", 33000, 18000},
				}},
				{Op: "set_style", Sheet: new("Data"), Range: new("A1:C1"), Style: &CellStyle{Bold: new(true), Fill: new("4472C4"), FontColor: new("FFFFFF")}},
				{Op: "set_style", Sheet: new("Data"), Range: new("B2:C5"), Style: &CellStyle{NumberFormat: new("$#,##0")}},
				{Op: "add_chart", Sheet: new("Data"), Anchor: new("E1"), Chart: &ChartSpec{
					Type: "col", Title: new("Revenue vs Cost by Quarter"),
					Series: []ChartSeriesSpec{
						{Name: "Data!$B$1", Categories: "Data!$A$2:$A$5", Values: "Data!$B$2:$B$5"},
						{Name: "Data!$C$1", Categories: "Data!$A$2:$A$5", Values: "Data!$C$2:$C$5"},
					},
				}},
				{Op: "add_chart", Sheet: new("Data"), Anchor: new("E18"), Chart: &ChartSpec{
					Type: "line", Title: new("Revenue Trend"),
					Series: []ChartSeriesSpec{
						{Name: "Data!$B$1", Categories: "Data!$A$2:$A$5", Values: "Data!$B$2:$B$5"},
					},
				}},
				{Op: "add_chart", Sheet: new("Data"), Anchor: new("M1"), Chart: &ChartSpec{
					Type: "pie", Title: new("Revenue Share"),
					Series: []ChartSeriesSpec{
						{Name: "Data!$B$1", Categories: "Data!$A$2:$A$5", Values: "Data!$B$2:$B$5"},
					},
				}},
			},
		},
	}
}

func sampleMultiSheet() sample {
	ops := []Operation{
		{Op: "rename_sheet", Sheet: new("Sheet1"), NewName: new("Summary")},
	}
	quarters := []string{"Q1", "Q2", "Q3", "Q4"}
	base := []int{100, 120, 140, 160}
	for qi, q := range quarters {
		ops = append(ops,
			Operation{Op: "create_sheet", Name: new(q)},
			Operation{Op: "set_values", Sheet: new(q), Anchor: new("A1"), Values: [][]any{
				{"Product", "Units", "Revenue"},
				{"Widget", base[qi], base[qi] * 50},
				{"Gadget", base[qi] + 30, (base[qi] + 30) * 40},
				{"Gizmo", base[qi] + 10, (base[qi] + 10) * 60},
			}},
			Operation{Op: "set_cell", Sheet: new(q), Cell: new("C5"), Formula: new("=SUM(C2:C4)")},
			Operation{Op: "set_style", Sheet: new(q), Range: new("A1:C1"), Style: &CellStyle{Bold: new(true), Fill: new("4472C4"), FontColor: new("FFFFFF")}},
			Operation{Op: "set_style", Sheet: new(q), Range: new("C2:C5"), Style: &CellStyle{NumberFormat: new("$#,##0")}},
		)
	}
	ops = append(ops,
		Operation{Op: "set_values", Sheet: new("Summary"), Anchor: new("A1"), Values: [][]any{
			{"Quarter", "Total Revenue"},
			{"Q1"}, {"Q2"}, {"Q3"}, {"Q4"},
		}},
		Operation{Op: "set_cell", Sheet: new("Summary"), Cell: new("B2"), Formula: new("=Q1!C5")},
		Operation{Op: "set_cell", Sheet: new("Summary"), Cell: new("B3"), Formula: new("=Q2!C5")},
		Operation{Op: "set_cell", Sheet: new("Summary"), Cell: new("B4"), Formula: new("=Q3!C5")},
		Operation{Op: "set_cell", Sheet: new("Summary"), Cell: new("B5"), Formula: new("=Q4!C5")},
		Operation{Op: "set_cell", Sheet: new("Summary"), Cell: new("A6"), Value: "Year"},
		Operation{Op: "set_cell", Sheet: new("Summary"), Cell: new("B6"), Formula: new("=SUM(B2:B5)")},
		Operation{Op: "set_style", Sheet: new("Summary"), Range: new("A1:B1"), Style: &CellStyle{Bold: new(true), Fill: new("305496"), FontColor: new("FFFFFF")}},
		Operation{Op: "set_style", Sheet: new("Summary"), Range: new("A6:B6"), Style: &CellStyle{Bold: new(true), Fill: new("D9E1F2")}},
		Operation{Op: "set_style", Sheet: new("Summary"), Range: new("B2:B6"), Style: &CellStyle{NumberFormat: new("$#,##0")}},
		Operation{Op: "set_col_width", Sheet: new("Summary"), Cols: new("A:B"), Width: new(16.0)},
		Operation{Op: "add_chart", Sheet: new("Summary"), Anchor: new("D1"), Chart: &ChartSpec{
			Type: "col", Title: new("Revenue by Quarter"),
			Series: []ChartSeriesSpec{{Name: "Summary!$B$1", Categories: "Summary!$A$2:$A$5", Values: "Summary!$B$2:$B$5"}},
		}},
		Operation{Op: "set_defined_name", Name: new("YearTotal"), RefersTo: new("Summary!$B$6")},
	)
	return sample{file: "04_multisheet.xlsx", params: WriteParams{OutputName: new("04_multisheet"), Operations: new(ops)}}
}

func sampleStyles() sample {
	return sample{
		file: "05_styles.xlsx",
		params: WriteParams{
			OutputName: new("05_styles"),
			Operations: &[]Operation{
				{Op: "rename_sheet", Sheet: new("Sheet1"), NewName: new("Styles")},
				{Op: "merge", Sheet: new("Styles"), Range: new("A1:F1")},
				{Op: "set_cell", Sheet: new("Styles"), Cell: new("A1"), Value: "Styling Showcase"},
				{Op: "set_style", Sheet: new("Styles"), Range: new("A1:F1"), Style: &CellStyle{Bold: new(true), Size: new(16.0), FontColor: new("FFFFFF"), Fill: new("305496"), Align: new("center")}},

				{Op: "set_cell", Sheet: new("Styles"), Cell: new("A3"), Value: "Borders:"},
				{Op: "set_cell", Sheet: new("Styles"), Cell: new("B3"), Value: "thin"},
				{Op: "set_style", Sheet: new("Styles"), Range: new("B3"), Style: &CellStyle{Border: new("thin")}},
				{Op: "set_cell", Sheet: new("Styles"), Cell: new("C3"), Value: "medium"},
				{Op: "set_style", Sheet: new("Styles"), Range: new("C3"), Style: &CellStyle{Border: new("medium")}},
				{Op: "set_cell", Sheet: new("Styles"), Cell: new("D3"), Value: "thick"},
				{Op: "set_style", Sheet: new("Styles"), Range: new("D3"), Style: &CellStyle{Border: new("thick")}},
				{Op: "set_cell", Sheet: new("Styles"), Cell: new("E3"), Value: "double"},
				{Op: "set_style", Sheet: new("Styles"), Range: new("E3"), Style: &CellStyle{Border: new("double")}},
				{Op: "set_cell", Sheet: new("Styles"), Cell: new("F3"), Value: "dashed"},
				{Op: "set_style", Sheet: new("Styles"), Range: new("F3"), Style: &CellStyle{Border: new("dashed"), BorderColor: new("C00000")}},

				{Op: "set_cell", Sheet: new("Styles"), Cell: new("A5"), Value: "Number formats:"},
				{Op: "set_cell", Sheet: new("Styles"), Cell: new("B5"), Value: 1234.567},
				{Op: "set_style", Sheet: new("Styles"), Range: new("B5"), Style: &CellStyle{NumberFormat: new("#,##0.00")}},
				{Op: "set_cell", Sheet: new("Styles"), Cell: new("C5"), Value: 0.4267},
				{Op: "set_style", Sheet: new("Styles"), Range: new("C5"), Style: &CellStyle{NumberFormat: new("0.0%")}},
				{Op: "set_cell", Sheet: new("Styles"), Cell: new("D5"), Value: 98765},
				{Op: "set_style", Sheet: new("Styles"), Range: new("D5"), Style: &CellStyle{NumberFormat: new("$#,##0")}},
				{Op: "set_cell", Sheet: new("Styles"), Cell: new("E5"), Value: 45292},
				{Op: "set_style", Sheet: new("Styles"), Range: new("E5"), Style: &CellStyle{NumberFormat: new("yyyy-mm-dd")}},

				{Op: "set_cell", Sheet: new("Styles"), Cell: new("A7"), Value: "Fills + alignment:"},
				{Op: "set_cell", Sheet: new("Styles"), Cell: new("B7"), Value: "left"},
				{Op: "set_style", Sheet: new("Styles"), Range: new("B7"), Style: &CellStyle{Fill: new("FCE4D6"), Align: new("left")}},
				{Op: "set_cell", Sheet: new("Styles"), Cell: new("C7"), Value: "center"},
				{Op: "set_style", Sheet: new("Styles"), Range: new("C7"), Style: &CellStyle{Fill: new("FFF2CC"), Align: new("center")}},
				{Op: "set_cell", Sheet: new("Styles"), Cell: new("D7"), Value: "right"},
				{Op: "set_style", Sheet: new("Styles"), Range: new("D7"), Style: &CellStyle{Fill: new("E2EFDA"), Align: new("right")}},

				{Op: "set_cell", Sheet: new("Styles"), Cell: new("A9"), Value: "Wrapped text in a single cell that is long enough to need wrapping across multiple lines."},
				{Op: "set_style", Sheet: new("Styles"), Range: new("A9"), Style: &CellStyle{WrapText: new(true), Valign: new("top")}},
				{Op: "merge", Sheet: new("Styles"), Range: new("A9:C11")},
				{Op: "set_col_width", Sheet: new("Styles"), Cols: new("A:F"), Width: new(14.0)},
				{Op: "set_row_height", Sheet: new("Styles"), Row: new(9), Height: new(48.0)},
			},
		},
	}
}

func sampleBoundedFill() sample {
	csv := "item,qty\nApples,12\nPears,8\nPlums,20\n"
	return sample{
		file: "06_bounded_fill.xlsx",
		params: WriteParams{
			OutputName:  new("06_bounded_fill"),
			DataSources: &[]sdk.Artifact{art("fruit.csv", csv)},
			Operations: &[]Operation{
				{Op: "rename_sheet", Sheet: new("Sheet1"), NewName: new("Inventory")},
				{Op: "set_values", Sheet: new("Inventory"), Anchor: new("A1"), Values: [][]any{
					{"OLD", "OLD"}, {"x", 999}, {"y", 888}, {"z", 777}, {"w", 666},
				}},
				{Op: "set_cell", Sheet: new("Inventory"), Cell: new("E1"), Value: "kept (outside target)"},
				{Op: "fill_from_data", Sheet: new("Inventory"), Range: new("A1:B5"), Source: new("0")},
				{Op: "set_style", Sheet: new("Inventory"), Range: new("A1:B1"), Style: &CellStyle{Bold: new(true), Fill: new("4472C4"), FontColor: new("FFFFFF")}},
				{Op: "set_cell", Sheet: new("Inventory"), Cell: new("B6"), Formula: new("=SUM(B2:B5)")},
				{Op: "set_col_width", Sheet: new("Inventory"), Cols: new("A:E"), Width: new(16.0)},
			},
		},
	}
}

func sampleKitchenSink() sample {
	csv := "rep,region,deals,revenue\n" +
		"Ana,North,14,142000\nBen,North,9,98000\nCai,South,11,121000\n" +
		"Dee,South,7,76000\nEli,West,12,133000\nFay,West,6,61000\n"
	return sample{
		file: "07_kitchen_sink.xlsx",
		params: WriteParams{
			OutputName:  new("07_kitchen_sink"),
			DataSources: &[]sdk.Artifact{art("deals.csv", csv)},
			Operations: &[]Operation{
				{Op: "rename_sheet", Sheet: new("Sheet1"), NewName: new("Deals")},
				{Op: "fill_from_data", Sheet: new("Deals"), Anchor: new("A1"), Source: new("0"), IncludeHeader: new(true)},
				{Op: "set_cell", Sheet: new("Deals"), Cell: new("E1"), Value: "Avg Deal"},
				{Op: "set_formula_range", Sheet: new("Deals"), Range: new("E2:E7"), Formula: new("=D2/C2")},
				{Op: "set_cell", Sheet: new("Deals"), Cell: new("C8"), Formula: new("=SUM(C2:C7)")},
				{Op: "set_cell", Sheet: new("Deals"), Cell: new("D8"), Formula: new("=SUM(D2:D7)")},
				{Op: "set_cell", Sheet: new("Deals"), Cell: new("A8"), Value: "TOTAL"},
				{Op: "set_style", Sheet: new("Deals"), Range: new("A1:E1"), Style: &CellStyle{Bold: new(true), FontColor: new("FFFFFF"), Fill: new("305496"), Align: new("center")}},
				{Op: "set_style", Sheet: new("Deals"), Range: new("A8:E8"), Style: &CellStyle{Bold: new(true), Fill: new("D9E1F2"), Border: new("thin")}},
				{Op: "set_style", Sheet: new("Deals"), Range: new("D2:D8"), Style: &CellStyle{NumberFormat: new("$#,##0")}},
				{Op: "set_style", Sheet: new("Deals"), Range: new("E2:E7"), Style: &CellStyle{NumberFormat: new("$#,##0")}},
				{Op: "set_col_width", Sheet: new("Deals"), Cols: new("A:E"), Width: new(14.0)},
				{Op: "freeze_panes", Sheet: new("Deals"), Cell: new("A2")},
				{Op: "auto_filter", Sheet: new("Deals"), Range: new("A1:E7")},

				{Op: "create_sheet", Name: new("Charts")},
				{Op: "add_chart", Sheet: new("Charts"), Anchor: new("A1"), Chart: &ChartSpec{
					Type: "col", Title: new("Revenue by Rep"),
					Series: []ChartSeriesSpec{{Name: "Deals!$D$1", Categories: "Deals!$A$2:$A$7", Values: "Deals!$D$2:$D$7"}},
				}},
				{Op: "add_chart", Sheet: new("Charts"), Anchor: new("A18"), Chart: &ChartSpec{
					Type: "pie", Title: new("Deal Share by Rep"),
					Series: []ChartSeriesSpec{{Name: "Deals!$C$1", Categories: "Deals!$A$2:$A$7", Values: "Deals!$C$2:$C$7"}},
				}},
				{Op: "set_defined_name", Name: new("TotalRevenue"), RefersTo: new("Deals!$D$8")},
			},
		},
	}
}

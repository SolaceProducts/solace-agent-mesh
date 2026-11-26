// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package xlsxtool

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"slices"
	"strconv"
	"strings"
	"testing"

	sdk "github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"
	"github.com/xuri/excelize/v2"
)

func TestWriteParamsSchemaOptionality(t *testing.T) {
	schema := sdk.BuildSchema[WriteParams]()
	for _, name := range schema.Required {
		if name == "data_sources" || name == "operations" {
			t.Errorf("WriteParams marks %q required; it must be optional (create-from-scratch supplies no data_sources, and operations_artifact substitutes for operations). required=%v", name, schema.Required)
		}
	}
}

func TestOperationSchemaRequiresOnlyOp(t *testing.T) {
	got := sdk.BuildSchema[Operation]().Required
	want := []string{"op"}
	if !slices.Equal(got, want) {
		t.Errorf("required = %v, want %v", got, want)
	}
}

func hasZipEntry(t *testing.T, content []byte, name string) bool {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(content), int64(len(content)))
	if err != nil {
		t.Fatalf("open zip: %v", err)
	}
	for _, f := range zr.File {
		if f.Name == name {
			return true
		}
	}
	return false
}

func writeErr(t *testing.T, p WriteParams) string {
	t.Helper()
	res, err := writeWorkbook(context.Background(), p, &sdk.ToolContext{})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if res.Status != sdk.StatusError {
		t.Fatalf("expected error status, got %q: %s", res.Status, res.Message)
	}
	return res.Message
}

func writeOK(t *testing.T, p WriteParams) []byte {
	t.Helper()
	res, err := writeWorkbook(context.Background(), p, &sdk.ToolContext{})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if res.Status != sdk.StatusSuccess {
		t.Fatalf("expected success, got %q: %s", res.Status, res.Message)
	}
	return res.DataObjects[0].Content
}

func itoa(i int) string { return strconv.Itoa(i) }

func excelizeNewBytes(t *testing.T) []byte {
	t.Helper()
	f := excelize.NewFile()
	buf, err := f.WriteToBuffer()
	if err != nil {
		t.Fatalf("seed workbook: %v", err)
	}
	_ = f.Close()
	return buf.Bytes()
}

func outputArtifact(t *testing.T, res *sdk.Result) []byte {
	t.Helper()
	if res.Status != sdk.StatusSuccess {
		t.Fatalf("write failed: %s", res.Message)
	}
	if len(res.DataObjects) != 1 {
		t.Fatalf("expected 1 data object, got %d", len(res.DataObjects))
	}
	return res.DataObjects[0].Content
}

func openBytes(t *testing.T, content []byte) *excelize.File {
	t.Helper()
	f, err := excelize.OpenReader(bytes.NewReader(content))
	if err != nil {
		t.Fatalf("reopen workbook: %v", err)
	}
	t.Cleanup(func() { _ = f.Close() })
	return f
}

func TestCreateFillFormulaStyle(t *testing.T) {
	tc := &sdk.ToolContext{}
	csv := []byte("region,units,revenue\nNA,10,1000\nEU,20,2500\nAPAC,5,750\n")

	res, err := writeWorkbook(context.Background(), WriteParams{
		OutputName:  new("sales"),
		DataSources: &[]sdk.Artifact{{Filename: "rows.csv", Content: csv, MIMEType: "text/csv"}},
		Operations: &[]Operation{
			{Op: "rename_sheet", Sheet: new("Sheet1"), NewName: new("Q3")},
			{Op: "fill_from_data", Sheet: new("Q3"), Anchor: new("A1"), Source: new("0"), IncludeHeader: new(true)},
			{Op: "set_cell", Sheet: new("Q3"), Cell: new("D1"), Value: "avg_price"},
			{Op: "set_formula_range", Sheet: new("Q3"), Range: new("D2:D4"), Formula: new("=C2/B2")},
			{Op: "set_cell", Sheet: new("Q3"), Cell: new("C5"), Formula: new("=SUM(C2:C4)")},
			{Op: "set_style", Sheet: new("Q3"), Range: new("A1:D1"), Style: &CellStyle{Bold: new(true), Fill: new("4472C4"), FontColor: new("FFFFFF")}},
			{Op: "set_style", Sheet: new("Q3"), Range: new("D2:D4"), Style: &CellStyle{NumberFormat: new("0.00")}},
			{Op: "freeze_panes", Sheet: new("Q3"), Cell: new("A2")},
			{Op: "auto_filter", Sheet: new("Q3"), Range: new("A1:D4")},
		},
	}, tc)
	if err != nil {
		t.Fatalf("write error: %v", err)
	}
	content := outputArtifact(t, res)

	f := openBytes(t, content)

	if got := f.GetSheetList(); len(got) != 1 || got[0] != "Q3" {
		t.Fatalf("expected single sheet Q3, got %v", got)
	}

	if v, _ := f.GetCellValue("Q3", "B2"); v != "10" {
		t.Errorf("B2 = %q, want 10", v)
	}
	if v, _ := f.GetCellValue("Q3", "A3"); v != "EU" {
		t.Errorf("A3 = %q, want EU", v)
	}

	if fr, _ := f.GetCellFormula("Q3", "D2"); fr != "C2/B2" {
		t.Errorf("D2 formula = %q, want C2/B2", fr)
	}
	if fr, _ := f.GetCellFormula("Q3", "D4"); !strings.Contains(fr, "C4/B4") {
		t.Errorf("D4 formula = %q, want it to reference C4/B4", fr)
	}

	if fr, _ := f.GetCellFormula("Q3", "C5"); fr != "SUM(C2:C4)" {
		t.Errorf("C5 formula = %q, want SUM(C2:C4)", fr)
	}
}

func TestModifyExistingWorkbook(t *testing.T) {
	tc := &sdk.ToolContext{}

	seed := excelize.NewFile()
	_ = seed.SetCellValue("Sheet1", "A1", "old")
	buf, err := seed.WriteToBuffer()
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	_ = seed.Close()

	res, err := writeWorkbook(context.Background(), WriteParams{
		Source:     &sdk.Artifact{Filename: "in.xlsx", Content: buf.Bytes()},
		OutputName: new("out"),
		Operations: &[]Operation{
			{Op: "set_cell", Sheet: new("Sheet1"), Cell: new("A1"), Value: "new"},
			{Op: "set_cell", Sheet: new("Sheet1"), Cell: new("B1"), Value: float64(42)},
		},
	}, tc)
	if err != nil {
		t.Fatalf("write error: %v", err)
	}
	f := openBytes(t, outputArtifact(t, res))

	if v, _ := f.GetCellValue("Sheet1", "A1"); v != "new" {
		t.Errorf("A1 = %q, want new", v)
	}
	if v, _ := f.GetCellValue("Sheet1", "B1"); v != "42" {
		t.Errorf("B1 = %q, want 42", v)
	}
}

func TestOutputNameDefaults(t *testing.T) {
	ctx := context.Background()
	tc := &sdk.ToolContext{}
	op := Operation{Op: "set_cell", Sheet: new("Sheet1"), Cell: new("A1"), Value: "x"}

	cases := []struct {
		name string
		p    WriteParams
		want string
	}{
		{"create, no name → workbook", WriteParams{Operations: &[]Operation{op}}, "workbook.xlsx"},
		{"create, explicit name", WriteParams{OutputName: new("report"), Operations: &[]Operation{op}}, "report.xlsx"},
		{"modify defaults to source", WriteParams{Source: &sdk.Artifact{Filename: "budget.xlsx"}, Operations: &[]Operation{op}}, "budget.xlsx"},
		{"modify strips version pin", WriteParams{Source: &sdk.Artifact{Filename: "budget.xlsx:3"}, Operations: &[]Operation{op}}, "budget.xlsx"},
		{"explicit name overrides source", WriteParams{Source: &sdk.Artifact{Filename: "budget.xlsx"}, OutputName: new("budget-copy"), Operations: &[]Operation{op}}, "budget-copy.xlsx"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.p.Source != nil {
				seed := excelizeNewBytes(t)
				c.p.Source.Content = seed
			}
			res, err := writeWorkbook(ctx, c.p, tc)
			if err != nil {
				t.Fatalf("write: %v", err)
			}
			if len(res.DataObjects) != 1 {
				t.Fatalf("expected exactly 1 data object, got %d", len(res.DataObjects))
			}
			if got := res.DataObjects[0].Name; got != c.want {
				t.Errorf("output name = %q, want %q", got, c.want)
			}
		})
	}
}

func TestSingleVersionPerCall(t *testing.T) {
	ops := make([]Operation, 0, 50)
	for i := range 50 {
		cell := "A" + itoa(i+1)
		ops = append(ops, Operation{Op: "set_cell", Sheet: new("Sheet1"), Cell: &cell, Value: float64(i)})
	}
	res, err := writeWorkbook(context.Background(), WriteParams{Operations: new(ops)}, &sdk.ToolContext{})
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if len(res.DataObjects) != 1 {
		t.Fatalf("50 ops produced %d data objects, want exactly 1", len(res.DataObjects))
	}
}

func TestAddChart(t *testing.T) {
	seed := func() []Operation {
		return []Operation{
			{Op: "set_values", Sheet: new("Sheet1"), Anchor: new("A1"), Values: [][]any{
				{"Cat", "Revenue"}, {"x", 10}, {"y", 20},
			}},
		}
	}

	out := writeOK(t, WriteParams{Operations: new(append(seed(), Operation{
		Op: "add_chart", Sheet: new("Sheet1"), Anchor: new("D1"),
		Chart: &ChartSpec{Type: "col", Title: new("Rev"), Series: []ChartSeriesSpec{
			{Name: "Sheet1!$B$1", Categories: "Sheet1!$A$2:$A$3", Values: "Sheet1!$B$2:$B$3"},
		}},
	}))})
	if !hasZipEntry(t, out, "xl/charts/chart1.xml") {
		t.Error("expected a chart part in the workbook")
	}

	msg := writeErr(t, WriteParams{Operations: new(append(seed(), Operation{
		Op: "add_chart", Sheet: new("Sheet1"), Anchor: new("D1"),
		Chart: &ChartSpec{Type: "col", Series: []ChartSeriesSpec{
			{Name: "Total Revenue", Categories: "Sheet1!$A$2:$A$3", Values: "Sheet1!$B$2:$B$3"},
		}},
	}))})
	if !strings.Contains(msg, "cell reference") {
		t.Errorf("literal name error = %q, want mention of cell reference", msg)
	}

	msg = writeErr(t, WriteParams{Operations: new(append(seed(), Operation{
		Op: "add_chart", Sheet: new("Sheet1"), Anchor: new("D1"),
		Chart: &ChartSpec{Type: "col", Series: []ChartSeriesSpec{{Name: "Sheet1!$B$1", Values: ""}}},
	}))})
	if !strings.Contains(msg, "values") {
		t.Errorf("empty values error = %q, want mention of values", msg)
	}

	msg = writeErr(t, WriteParams{Operations: new(append(seed(), Operation{
		Op: "add_chart", Sheet: new("Sheet1"), Anchor: new("D1"),
		Chart: &ChartSpec{Type: "donut", Series: []ChartSeriesSpec{{Name: "Sheet1!$B$1", Values: "Sheet1!$B$2:$B$3"}}},
	}))})
	if !strings.Contains(msg, "chart type") {
		t.Errorf("bad type error = %q", msg)
	}
}

func zipEntryBytes(t *testing.T, content []byte, name string) []byte {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(content), int64(len(content)))
	if err != nil {
		t.Fatalf("open zip: %v", err)
	}
	for _, f := range zr.File {
		if f.Name == name {
			rc, err := f.Open()
			if err != nil {
				t.Fatalf("open entry %s: %v", name, err)
			}
			b, err := io.ReadAll(rc)
			_ = rc.Close()
			if err != nil {
				t.Fatalf("read entry %s: %v", name, err)
			}
			return b
		}
	}
	t.Fatalf("zip entry %q not found", name)
	return nil
}

func TestQuoteSheetRef(t *testing.T) {
	cases := []struct{ in, want string }{
		{"$B$2:$B$10", "$B$2:$B$10"},
		{"", ""},
		{"'Already Quoted'!$A$1", "'Already Quoted'!$A$1"},
		{"Summary!$A$1:$A$7", "'Summary'!$A$1:$A$7"},
		{"Dept Summary!$A$2:$A$7", "'Dept Summary'!$A$2:$A$7"},
		{"Q3 (Final)!$A$1", "'Q3 (Final)'!$A$1"},
		{"Q3!$A$1", "'Q3'!$A$1"},
		{"2024!$A$1", "'2024'!$A$1"},
		{"O'Brien!$A$1", "'O''Brien'!$A$1"},
	}
	for _, c := range cases {
		if got := quoteSheetRef(c.in); got != c.want {
			t.Errorf("quoteSheetRef(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestAddChartQuotesSpacedSheet(t *testing.T) {
	out := writeOK(t, WriteParams{Operations: new([]Operation{
		{Op: "rename_sheet", Sheet: new("Sheet1"), NewName: new("Dept Summary")},
		{Op: "set_values", Sheet: new("Dept Summary"), Anchor: new("A1"), Values: [][]any{
			{"Dept", "Revenue"}, {"Eng", 10}, {"Sales", 20},
		}},
		{Op: "add_chart", Sheet: new("Dept Summary"), Anchor: new("D1"),
			Chart: &ChartSpec{Type: "col", Series: []ChartSeriesSpec{{
				Name:       "Dept Summary!$B$1",
				Categories: "Dept Summary!$A$2:$A$3",
				Values:     "Dept Summary!$B$2:$B$3",
			}}},
		},
	})})
	xml := string(zipEntryBytes(t, out, "xl/charts/chart1.xml"))
	if !strings.Contains(xml, "&#39;Dept Summary&#39;!$B$2:$B$3") {
		t.Errorf("chart XML missing quoted sheet ref; got:\n%s", xml)
	}
	if strings.Contains(xml, "<f>Dept Summary!") {
		t.Errorf("chart XML contains unquoted spaced sheet ref (would render blank)")
	}
}

func TestRenameSheetCollision(t *testing.T) {
	msg := writeErr(t, WriteParams{Operations: &[]Operation{
		{Op: "create_sheet", Name: new("Q2")},
		{Op: "rename_sheet", Sheet: new("Sheet1"), NewName: new("Q2")},
	}})
	if !strings.Contains(msg, "already exists") {
		t.Errorf("collision error = %q", msg)
	}
}

func TestDeleteOnlySheet(t *testing.T) {
	msg := writeErr(t, WriteParams{Operations: &[]Operation{
		{Op: "delete_sheet", Name: new("Sheet1")},
	}})
	if !strings.Contains(msg, "at least one sheet") {
		t.Errorf("delete-only-sheet error = %q", msg)
	}
}

func TestCoerceScalar(t *testing.T) {
	cases := map[string]any{
		"42":        float64(42),
		"3.14":      3.14,
		"24000.50":  24000.5,
		"-5":        float64(-5),
		"0":         float64(0),
		"02134":     "02134",
		"1e3":       "1e3",
		"+15551234": "+15551234",
		"007":       "007",
		"true":      true,
		"FALSE":     false,
		"hello":     "hello",
		"":          "",
	}
	for in, want := range cases {
		if got := coerceScalar(in); got != want {
			t.Errorf("coerceScalar(%q) = %v (%T), want %v (%T)", in, got, got, want, want)
		}
	}
}

func TestBoundedReversedRange(t *testing.T) {
	csv := []byte("a,b\n1,2\n3,4\n")
	out := writeOK(t, WriteParams{
		DataSources: &[]sdk.Artifact{{Filename: "d.csv", Content: csv}},
		Operations:  &[]Operation{{Op: "fill_from_data", Sheet: new("Sheet1"), Range: new("C5:A1"), Source: new("0")}},
	})
	f := openBytes(t, out)
	if v, _ := f.GetCellValue("Sheet1", "A1"); v != "a" {
		t.Errorf("A1 = %q, want a (data anchored at normalized top-left)", v)
	}
	if v, _ := f.GetCellValue("Sheet1", "B2"); v != "2" {
		t.Errorf("B2 = %q, want 2", v)
	}
}

func TestShiftFormula(t *testing.T) {
	cases := []struct {
		in         string
		dCol, dRow int
		want       string
	}{
		{"VLOOKUP(A2,'Personal Info'!$A:$C,3,FALSE)", 0, 1, "VLOOKUP(A3,'Personal Info'!$A:$C,3,FALSE)"},
		{"VLOOKUP(A2,'Personal Info'!$A:$C,3,FALSE)", 0, 19, "VLOOKUP(A21,'Personal Info'!$A:$C,3,FALSE)"},
		{"C2/B2", 0, 1, "C3/B3"},
		{"$A$1+B2", 0, 2, "$A$1+B4"},
		{"A$1*$C5", 1, 3, "B$1*$C8"},
		{"SUM(A2:A10)", 0, 1, "SUM(A3:A11)"},
		{"A2*2", 1, 0, "B2*2"},
		{`IF(A2="B2 wins",B2,C2)`, 0, 1, `IF(A3="B2 wins",B3,C3)`},
		{"LOG10(A2)", 0, 1, "LOG10(A3)"},
		{"TRUE", 0, 1, "TRUE"},
	}
	for _, c := range cases {
		if got := shiftFormula(c.in, c.dCol, c.dRow); got != c.want {
			t.Errorf("shiftFormula(%q, %d, %d) = %q, want %q", c.in, c.dCol, c.dRow, got, c.want)
		}
	}
}

func TestSharedFormulaNotCorrupt(t *testing.T) {
	out := writeOK(t, WriteParams{Operations: &[]Operation{
		{Op: "rename_sheet", Sheet: new("Sheet1"), NewName: new("Employees")},
		{Op: "set_values", Sheet: new("Employees"), Anchor: new("A1"), Values: [][]any{
			{"ID"}, {"E001"}, {"E002"}, {"E003"},
		}},
		{Op: "create_sheet", Name: new("Personal Info"), Index: new(1)},
		{Op: "set_formula_range", Sheet: new("Employees"), Range: new("H2:H4"),
			Formula: new("=VLOOKUP(A2,'Personal Info'!$A:$C,3,FALSE)")},
	}})

	for _, sheet := range []string{"sheet1.xml", "sheet2.xml"} {
		xml := string(zipEntryBytes(t, out, "xl/worksheets/"+sheet))
		if strings.Contains(xml, `t="shared"`) {
			t.Errorf("%s contains t=\"shared\" — Excel reports this as corrupt", sheet)
		}
	}

	f := openBytes(t, out)
	for _, cell := range []string{"H2", "H3", "H4"} {
		if fr, _ := f.GetCellFormula("Employees", cell); !strings.Contains(fr, "VLOOKUP(A"+cell[1:]+",") {
			t.Errorf("%s formula = %q, want a VLOOKUP referencing A%s", cell, fr, cell[1:])
		}
	}
	xml := string(zipEntryBytes(t, out, "xl/worksheets/sheet1.xml"))
	if !strings.Contains(xml, "&#39;Personal Info&#39;!$A:$C") {
		t.Errorf("sheet1.xml lost the quoted sheet name; got:\n%s", xml)
	}
}

func TestSetValuesInterpretsFormulas(t *testing.T) {
	out := writeOK(t, WriteParams{Operations: &[]Operation{
		{Op: "set_values", Sheet: new("Sheet1"), Anchor: new("A1"), Values: [][]any{
			{"Label", "Value"},
			{"Sum", "=SUM(B1:B1)"},
			{"Cross", "='Other Sheet'!A1"},
			{"Literal", "just text"},
		}},
		{Op: "set_cell", Sheet: new("Sheet1"), Cell: new("D1"), Value: "=A1"},
	}})
	f := openBytes(t, out)

	if fr, _ := f.GetCellFormula("Sheet1", "B2"); fr != "SUM(B1:B1)" {
		t.Errorf("B2 formula = %q, want SUM(B1:B1)", fr)
	}
	if fr, _ := f.GetCellFormula("Sheet1", "D1"); fr != "A1" {
		t.Errorf("set_cell value '=A1' should be a formula; got formula %q", fr)
	}
	xml := string(zipEntryBytes(t, out, "xl/worksheets/sheet1.xml"))
	if !strings.Contains(xml, "&#39;Other Sheet&#39;!A1") {
		t.Errorf("cross-sheet formula lost its quoted sheet name; got:\n%s", xml)
	}
	if fr, _ := f.GetCellFormula("Sheet1", "B4"); fr != "" {
		t.Errorf("literal text was misread as a formula: %q", fr)
	}
	if v, _ := f.GetCellValue("Sheet1", "B4"); v != "just text" {
		t.Errorf("B4 = %q, want \"just text\"", v)
	}
}

func TestFillFromDataDoesNotInterpretFormulas(t *testing.T) {
	csv := "name,note\nAlice,=1+1\n"
	out := writeOK(t, WriteParams{
		DataSources: &[]sdk.Artifact{{Filename: "d.csv", Content: []byte(csv)}},
		Operations: &[]Operation{
			{Op: "fill_from_data", Sheet: new("Sheet1"), Anchor: new("A1"), Source: new("0")},
		},
	})
	f := openBytes(t, out)
	if fr, _ := f.GetCellFormula("Sheet1", "B2"); fr != "" {
		t.Errorf("CSV value '=1+1' must stay literal text, got formula %q", fr)
	}
	if v, _ := f.GetCellValue("Sheet1", "B2"); v != "=1+1" {
		t.Errorf("B2 = %q, want literal \"=1+1\"", v)
	}
}

func TestTemplateFormula(t *testing.T) {
	out := writeOK(t, WriteParams{Operations: &[]Operation{
		{Op: "set_values", Sheet: new("Sheet1"), Anchor: new("A1"), Values: [][]any{{1}, {2}, {3}}},
		{Op: "set_formula_range", Sheet: new("Sheet1"), Range: new("B1:B3"), Mode: new("template"), Formula: new("=A{row}*10")},
	}})
	f := openBytes(t, out)
	if fr, _ := f.GetCellFormula("Sheet1", "B1"); fr != "A1*10" {
		t.Errorf("B1 = %q, want A1*10", fr)
	}
	if fr, _ := f.GetCellFormula("Sheet1", "B3"); fr != "A3*10" {
		t.Errorf("B3 = %q, want A3*10", fr)
	}
}

func TestReadFormulasMode(t *testing.T) {
	out := writeOK(t, WriteParams{Operations: &[]Operation{
		{Op: "set_cell", Sheet: new("Sheet1"), Cell: new("A1"), Value: float64(5)},
		{Op: "set_cell", Sheet: new("Sheet1"), Cell: new("B1"), Formula: new("=A1*2")},
	}})
	res, _ := readWorkbook(context.Background(), ReadParams{
		Source: sdk.Artifact{Filename: "f.xlsx", Content: out}, Mode: new("formulas"), Sheet: new("Sheet1"), Range: new("A1:B1"),
	}, &sdk.ToolContext{})
	if !strings.Contains(res.Message, "=A1*2") {
		t.Errorf("formulas read = %q, want =A1*2", res.Message)
	}
}

func TestMergeAndDescribe(t *testing.T) {
	out := writeOK(t, WriteParams{Operations: &[]Operation{
		{Op: "set_cell", Sheet: new("Sheet1"), Cell: new("A1"), Value: "Title"},
		{Op: "merge", Sheet: new("Sheet1"), Range: new("A1:C1")},
	}})
	f := openBytes(t, out)
	merges, _ := f.GetMergeCells("Sheet1")
	if len(merges) != 1 {
		t.Fatalf("expected 1 merge, got %d", len(merges))
	}
	res, _ := readWorkbook(context.Background(), ReadParams{Source: sdk.Artifact{Filename: "m.xlsx", Content: out}}, &sdk.ToolContext{})
	if !strings.Contains(res.Message, "merged:") {
		t.Errorf("describe missing merged section: %s", res.Message)
	}
}

func TestDefinedNameAndDescribe(t *testing.T) {
	out := writeOK(t, WriteParams{Operations: &[]Operation{
		{Op: "set_cell", Sheet: new("Sheet1"), Cell: new("A1"), Value: float64(1)},
		{Op: "set_defined_name", Name: new("MyRange"), RefersTo: new("Sheet1!$A$1:$A$10")},
	}})
	res, _ := readWorkbook(context.Background(), ReadParams{Source: sdk.Artifact{Filename: "d.xlsx", Content: out}}, &sdk.ToolContext{})
	if !strings.Contains(res.Message, "MyRange") {
		t.Errorf("describe missing defined name: %s", res.Message)
	}
}

func TestOperationsArtifact(t *testing.T) {
	yaml := []byte("- op: set_cell\n  sheet: Sheet1\n  cell: A1\n  value: hello\n- op: set_cell\n  sheet: Sheet1\n  cell: B1\n  formula: =LEN(A1)\n")
	out := writeOK(t, WriteParams{
		OperationsArtifact: &sdk.Artifact{Filename: "ops.yaml", Content: yaml},
		Operations:         &[]Operation{{Op: "set_cell", Sheet: new("Sheet1"), Cell: new("A1"), Value: "ignored"}},
	})
	f := openBytes(t, out)
	if v, _ := f.GetCellValue("Sheet1", "A1"); v != "hello" {
		t.Errorf("A1 = %q, want hello (artifact ops take precedence)", v)
	}
	if fr, _ := f.GetCellFormula("Sheet1", "B1"); fr != "LEN(A1)" {
		t.Errorf("B1 formula = %q, want LEN(A1)", fr)
	}
}

func TestFillTSVAndJSONArrays(t *testing.T) {
	out := writeOK(t, WriteParams{
		DataSources: &[]sdk.Artifact{{Filename: "d.tsv", Content: []byte("a\tb\n1\t2\n")}},
		Operations:  &[]Operation{{Op: "fill_from_data", Sheet: new("Sheet1"), Anchor: new("A1"), Source: new("0")}},
	})
	f := openBytes(t, out)
	if v, _ := f.GetCellValue("Sheet1", "B2"); v != "2" {
		t.Errorf("TSV B2 = %q, want 2", v)
	}

	out = writeOK(t, WriteParams{
		DataSources: &[]sdk.Artifact{{Filename: "d.json", Content: []byte(`[["x","y"],[1,2],[3,4]]`)}},
		Operations:  &[]Operation{{Op: "fill_from_data", Sheet: new("Sheet1"), Anchor: new("A1"), Source: new("0"), Format: new("json")}},
	})
	f = openBytes(t, out)
	if v, _ := f.GetCellValue("Sheet1", "A3"); v != "3" {
		t.Errorf("JSON-arrays A3 = %q, want 3", v)
	}
}

func TestColRowSize(t *testing.T) {
	out := writeOK(t, WriteParams{Operations: &[]Operation{
		{Op: "set_cell", Sheet: new("Sheet1"), Cell: new("A1"), Value: "x"},
		{Op: "set_col_width", Sheet: new("Sheet1"), Cols: new("A:B"), Width: new(25.0)},
		{Op: "set_row_height", Sheet: new("Sheet1"), Row: new(1), Height: new(40.0)},
	}})
	f := openBytes(t, out)
	if w, _ := f.GetColWidth("Sheet1", "B"); w != 25.0 {
		t.Errorf("col B width = %v, want 25", w)
	}
	if h, _ := f.GetRowHeight("Sheet1", 1); h != 40.0 {
		t.Errorf("row 1 height = %v, want 40", h)
	}
}

func TestSearchCap(t *testing.T) {
	ops := make([]Operation, 0, 150)
	for i := range 150 {
		cell := "A" + itoa(i+1)
		ops = append(ops, Operation{Op: "set_cell", Sheet: new("Sheet1"), Cell: &cell, Value: "hit"})
	}
	out := writeOK(t, WriteParams{Operations: new(ops)})
	res, _ := readWorkbook(context.Background(), ReadParams{
		Source: sdk.Artifact{Filename: "s.xlsx", Content: out}, Mode: new("search"), Query: new("hit"),
	}, &sdk.ToolContext{})
	if !strings.Contains(res.Message, "stopped at") {
		t.Errorf("expected search cap notice, got: %s", res.Message[:min(200, len(res.Message))])
	}
}

func TestUnknownOpFailsFast(t *testing.T) {
	res, err := writeWorkbook(context.Background(), WriteParams{
		Operations: &[]Operation{{Op: "bogus"}},
	}, &sdk.ToolContext{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Status != sdk.StatusError || !strings.Contains(res.Message, "bogus") {
		t.Fatalf("expected error mentioning bogus op, got %q (%s)", res.Status, res.Message)
	}
}

func TestReadModes(t *testing.T) {
	f := excelize.NewFile()
	_ = f.SetSheetName("Sheet1", "Data")
	headers := []any{"name", "score"}
	_ = f.SetSheetRow("Data", "A1", &headers)
	rows := [][]any{{"alice", 90}, {"bob", 75}, {"carol", 88}}
	for i, r := range rows {
		cell, _ := excelize.CoordinatesToCellName(1, i+2)
		rr := r
		_ = f.SetSheetRow("Data", cell, &rr)
	}
	buf, _ := f.WriteToBuffer()
	_ = f.Close()
	src := sdk.Artifact{Filename: "data.xlsx", Content: buf.Bytes()}

	res, _ := readWorkbook(context.Background(), ReadParams{Source: src}, &sdk.ToolContext{})
	if !strings.Contains(res.Message, "Data") || !strings.Contains(res.Message, "name | score") {
		t.Errorf("describe missing sheet/header: %s", res.Message)
	}

	res, _ = readWorkbook(context.Background(), ReadParams{Source: src, Mode: new("values"), Sheet: new("Data"), Range: new("A1:B2"), Format: new("csv")}, &sdk.ToolContext{})
	if !strings.Contains(res.Message, "alice") || strings.Contains(res.Message, "carol") {
		t.Errorf("range read wrong rows: %s", res.Message)
	}

	res, _ = readWorkbook(context.Background(), ReadParams{Source: src, Mode: new("search"), Query: new("bob")}, &sdk.ToolContext{})
	if !strings.Contains(res.Message, "Data!A3") {
		t.Errorf("search did not locate bob at A3: %s", res.Message)
	}

	res, _ = readWorkbook(context.Background(), ReadParams{Source: src, Mode: new("values"), Sheet: new("Data"), MaxCells: new(1)}, &sdk.ToolContext{})
	if !strings.Contains(res.Message, "cap") {
		t.Errorf("expected cap guidance, got: %s", res.Message)
	}
}

func TestRoundTrip(t *testing.T) {
	ctx := context.Background()
	tc := &sdk.ToolContext{}

	csv := []byte("region,units,revenue\nNA,10,1000\nEU,20,2500\nAPAC,5,750\n")
	res, err := writeWorkbook(ctx, WriteParams{
		OutputName:  new("sales"),
		DataSources: &[]sdk.Artifact{{Filename: "rows.csv", Content: csv, MIMEType: "text/csv"}},
		Operations: &[]Operation{
			{Op: "rename_sheet", Sheet: new("Sheet1"), NewName: new("Q3")},
			{Op: "fill_from_data", Sheet: new("Q3"), Anchor: new("A1"), Source: new("0"), IncludeHeader: new(true)},
			{Op: "set_formula_range", Sheet: new("Q3"), Range: new("D2:D4"), Formula: new("=C2/B2")},
		},
	}, tc)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	created := sdk.Artifact{Filename: "sales.xlsx", Content: outputArtifact(t, res)}

	res, _ = readWorkbook(ctx, ReadParams{Source: created}, tc)
	if !strings.Contains(res.Message, "Q3") || !strings.Contains(res.Message, "region | units | revenue") {
		t.Fatalf("describe of created workbook missing sheet/header: %s", res.Message)
	}
	res, _ = readWorkbook(ctx, ReadParams{Source: created, Mode: new("values"), Sheet: new("Q3"), Range: new("A2:C2"), Format: new("csv")}, tc)
	if !strings.Contains(res.Message, "NA,10,1000") {
		t.Fatalf("values read of created workbook wrong: %s", res.Message)
	}

	res, _ = readWorkbook(ctx, ReadParams{Source: created, Mode: new("search"), Query: new("APAC")}, tc)
	if !strings.Contains(res.Message, "Q3!A4") {
		t.Fatalf("search of created workbook did not locate APAC at A4: %s", res.Message)
	}

	res, err = writeWorkbook(ctx, WriteParams{
		Source:     &created,
		OutputName: new("sales-v2"),
		Operations: &[]Operation{
			{Op: "set_cell", Sheet: new("Q3"), Cell: new("B3"), Value: float64(99)},
			{Op: "create_sheet", Name: new("Notes")},
			{Op: "set_cell", Sheet: new("Notes"), Cell: new("A1"), Value: "reviewed"},
		},
	}, tc)
	if err != nil {
		t.Fatalf("modify: %v", err)
	}
	modified := sdk.Artifact{Filename: "sales-v2.xlsx", Content: outputArtifact(t, res)}

	res, _ = readWorkbook(ctx, ReadParams{Source: modified, Mode: new("values"), Sheet: new("Q3"), Range: new("B3:B3")}, tc)
	if !strings.Contains(res.Message, "99") {
		t.Fatalf("modified B3 not 99: %s", res.Message)
	}
	res, _ = readWorkbook(ctx, ReadParams{Source: modified}, tc)
	if !strings.Contains(res.Message, "Notes") {
		t.Fatalf("describe of modified workbook missing new Notes sheet: %s", res.Message)
	}
	res, _ = readWorkbook(ctx, ReadParams{Source: modified, Mode: new("search"), Query: new("reviewed")}, tc)
	if !strings.Contains(res.Message, "Notes!A1") {
		t.Fatalf("search of modified workbook did not find 'reviewed' on Notes: %s", res.Message)
	}
}

func TestFillRaggedRows(t *testing.T) {
	ctx := context.Background()
	tc := &sdk.ToolContext{}
	csv := []byte("a,b,c\n1,2\n3,4,5,6\n")

	grid, err := parseDataSource(&sdk.Artifact{Filename: "r.csv", Content: csv}, "auto", nil, true)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(grid) != 3 || len(grid[0]) != 3 || len(grid[1]) != 2 || len(grid[2]) != 4 {
		t.Fatalf("ragged widths not preserved: %v", grid)
	}

	res, err := writeWorkbook(ctx, WriteParams{
		OutputName:  new("ragged"),
		DataSources: &[]sdk.Artifact{{Filename: "r.csv", Content: csv}},
		Operations:  &[]Operation{{Op: "fill_from_data", Anchor: new("A1"), Source: new("0")}},
	}, tc)
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	f := openBytes(t, outputArtifact(t, res))

	if v, _ := f.GetCellValue("Sheet1", "B2"); v != "2" {
		t.Errorf("B2 = %q, want 2", v)
	}
	if v, _ := f.GetCellValue("Sheet1", "C2"); v != "" {
		t.Errorf("C2 = %q, want empty (short row)", v)
	}
	if v, _ := f.GetCellValue("Sheet1", "D3"); v != "6" {
		t.Errorf("D3 = %q, want 6 (long row extends)", v)
	}
}

func TestFillColumnsMismatch(t *testing.T) {
	data := []byte(`[{"a":1,"b":"x","c":true},{"a":2,"b":"y","c":false}]`)

	grid, err := parseDataSource(&sdk.Artifact{Filename: "d.json", Content: data}, "json", []string{"a"}, true)
	if err != nil {
		t.Fatalf("parse subset: %v", err)
	}
	if len(grid) != 3 || len(grid[0]) != 1 || grid[0][0] != "a" {
		t.Fatalf("subset projection wrong: %v", grid)
	}

	grid, err = parseDataSource(&sdk.Artifact{Filename: "d.json", Content: data}, "json", []string{"a", "missing"}, true)
	if err != nil {
		t.Fatalf("parse missing col: %v", err)
	}
	if grid[0][1] != "missing" {
		t.Fatalf("header[1] = %v, want 'missing'", grid[0][1])
	}
	if grid[1][1] != nil {
		t.Fatalf("missing-column data cell = %v, want nil", grid[1][1])
	}

	res, err := writeWorkbook(context.Background(), WriteParams{
		OutputName:  new("proj"),
		DataSources: &[]sdk.Artifact{{Filename: "d.json", Content: data}},
		Operations: &[]Operation{
			{Op: "fill_from_data", Anchor: new("A1"), Source: new("0"), Format: new("json"), Columns: []string{"a", "missing"}},
		},
	}, &sdk.ToolContext{})
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	f := openBytes(t, outputArtifact(t, res))
	if v, _ := f.GetCellValue("Sheet1", "B2"); v != "" {
		t.Errorf("missing-column cell B2 = %q, want blank", v)
	}
	if v, _ := f.GetCellValue("Sheet1", "A2"); v != "1" {
		t.Errorf("A2 = %q, want 1", v)
	}
}

func TestFillEmptySource(t *testing.T) {
	cases := map[string]sdk.Artifact{
		"empty csv":        {Filename: "empty.csv", Content: []byte{}},
		"empty json array": {Filename: "empty.json", Content: []byte(`[]`)},
	}
	for name, art := range cases {
		t.Run(name, func(t *testing.T) {
			res, err := writeWorkbook(context.Background(), WriteParams{
				DataSources: &[]sdk.Artifact{art},
				Operations:  &[]Operation{{Op: "fill_from_data", Anchor: new("A1"), Source: new("0")}},
			}, &sdk.ToolContext{})
			if err != nil {
				t.Fatalf("unexpected error return: %v", err)
			}
			if res.Status != sdk.StatusError || !strings.Contains(res.Message, "no rows") {
				t.Fatalf("expected 'no rows' error, got %q: %s", res.Status, res.Message)
			}
		})
	}
}

func TestFillBadSourceRef(t *testing.T) {
	for _, ref := range []string{"5", "nope.csv"} {
		res, err := writeWorkbook(context.Background(), WriteParams{
			DataSources: &[]sdk.Artifact{{Filename: "d.csv", Content: []byte("a\n1\n")}},
			Operations:  &[]Operation{{Op: "fill_from_data", Anchor: new("A1"), Source: new(ref)}},
		}, &sdk.ToolContext{})
		if err != nil {
			t.Fatalf("unexpected error return: %v", err)
		}
		if res.Status != sdk.StatusError {
			t.Fatalf("ref %q: expected error, got success: %s", ref, res.Message)
		}
	}
}

func TestFillBoundedRange(t *testing.T) {
	ctx := context.Background()
	tc := &sdk.ToolContext{}
	csv := []byte("a,b\n1,2\n3,4\n")

	t.Run("fits and clears remainder", func(t *testing.T) {
		res, err := writeWorkbook(ctx, WriteParams{
			OutputName:  new("bounded"),
			DataSources: &[]sdk.Artifact{{Filename: "d.csv", Content: csv}},
			Operations: &[]Operation{
				{Op: "set_cell", Sheet: new("Sheet1"), Cell: new("C5"), Value: "stale"},
				{Op: "set_cell", Sheet: new("Sheet1"), Cell: new("E5"), Value: "keep"},
				{Op: "fill_from_data", Sheet: new("Sheet1"), Range: new("A1:C5"), Source: new("0")},
			},
		}, tc)
		if err != nil {
			t.Fatalf("write: %v", err)
		}
		f := openBytes(t, outputArtifact(t, res))
		if v, _ := f.GetCellValue("Sheet1", "B2"); v != "2" {
			t.Errorf("B2 = %q, want 2", v)
		}
		if v, _ := f.GetCellValue("Sheet1", "C5"); v != "" {
			t.Errorf("C5 = %q, want cleared (inside range, beyond data)", v)
		}
		if v, _ := f.GetCellValue("Sheet1", "E5"); v != "keep" {
			t.Errorf("E5 = %q, want preserved (outside range)", v)
		}
	})

	t.Run("row overflow errors", func(t *testing.T) {
		res, err := writeWorkbook(ctx, WriteParams{
			DataSources: &[]sdk.Artifact{{Filename: "d.csv", Content: csv}},
			Operations:  &[]Operation{{Op: "fill_from_data", Range: new("A1:B2"), Source: new("0")}},
		}, tc)
		if err != nil {
			t.Fatalf("unexpected error return: %v", err)
		}
		if res.Status != sdk.StatusError || !strings.Contains(res.Message, "rows") {
			t.Fatalf("expected row-overflow error, got %q: %s", res.Status, res.Message)
		}
	})

	t.Run("column overflow errors", func(t *testing.T) {
		res, err := writeWorkbook(ctx, WriteParams{
			DataSources: &[]sdk.Artifact{{Filename: "d.csv", Content: csv}},
			Operations:  &[]Operation{{Op: "fill_from_data", Range: new("A1:A9"), Source: new("0")}},
		}, tc)
		if err != nil {
			t.Fatalf("unexpected error return: %v", err)
		}
		if res.Status != sdk.StatusError || !strings.Contains(res.Message, "columns") {
			t.Fatalf("expected column-overflow error, got %q: %s", res.Status, res.Message)
		}
	})
}

func TestFreezePanesActivePane(t *testing.T) {
	cases := []struct {
		cell, wantPane string
	}{
		{"A2", "bottomLeft"},
		{"B1", "topRight"},
		{"B2", "bottomRight"},
	}
	for _, c := range cases {
		t.Run(c.cell, func(t *testing.T) {
			res, err := writeWorkbook(context.Background(), WriteParams{
				OutputName: new("fz"),
				Operations: &[]Operation{
					{Op: "set_cell", Sheet: new("Sheet1"), Cell: new("A1"), Value: "x"},
					{Op: "freeze_panes", Sheet: new("Sheet1"), Cell: new(c.cell)},
				},
			}, &sdk.ToolContext{})
			if err != nil {
				t.Fatalf("write: %v", err)
			}
			f := openBytes(t, outputArtifact(t, res))
			panes, err := f.GetPanes("Sheet1")
			if err != nil {
				t.Fatalf("GetPanes: %v", err)
			}
			if panes.ActivePane != c.wantPane {
				t.Errorf("freeze %s: ActivePane = %q, want %q", c.cell, panes.ActivePane, c.wantPane)
			}
			for _, sel := range panes.Selection {
				if sel.Pane != c.wantPane {
					t.Errorf("freeze %s: selection pane = %q, want %q", c.cell, sel.Pane, c.wantPane)
				}
			}
		})
	}

	res, err := writeWorkbook(context.Background(), WriteParams{
		OutputName: new("fz0"),
		Operations: &[]Operation{
			{Op: "set_cell", Sheet: new("Sheet1"), Cell: new("A1"), Value: "x"},
			{Op: "freeze_panes", Sheet: new("Sheet1"), Cell: new("A1")},
		},
	}, &sdk.ToolContext{})
	if err != nil {
		t.Fatalf("write A1: %v", err)
	}
	f := openBytes(t, outputArtifact(t, res))
	panes, _ := f.GetPanes("Sheet1")
	if panes.Freeze {
		t.Errorf("freeze A1 should be a no-op, got Freeze=true")
	}
}

func TestFillFromJSONObjects(t *testing.T) {
	jsonData := []byte(`[{"a":1,"b":"x"},{"a":2,"b":"y"}]`)
	grid, err := parseDataSource(&sdk.Artifact{Filename: "d.json", Content: jsonData}, "auto", nil, true)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(grid) != 3 {
		t.Fatalf("expected 3 rows, got %d: %v", len(grid), grid)
	}
	if grid[0][0] != "a" || grid[0][1] != "b" {
		t.Errorf("header = %v, want [a b]", grid[0])
	}
	if grid[1][1] != "x" {
		t.Errorf("row1 b = %v, want x", grid[1][1])
	}
}

func TestCreateSheetPrunesDefaultSheet(t *testing.T) {
	content := writeOK(t, WriteParams{
		OutputName: new("report"),
		Operations: &[]Operation{
			{Op: "create_sheet", Name: new("Data")},
			{Op: "set_values", Sheet: new("Data"), Anchor: new("A1"), Values: [][]any{{"x", "y"}, {1, 2}}},
			{Op: "create_sheet", Name: new("Summary")},
			{Op: "set_cell", Sheet: new("Summary"), Cell: new("A1"), Value: "total"},
		},
	})
	f := openBytes(t, content)
	if got := f.GetSheetList(); len(got) != 2 || got[0] != "Data" || got[1] != "Summary" {
		t.Fatalf("expected [Data Summary], got %v", got)
	}
}

func TestSingleCreateSheetPrunesDefaultSheet(t *testing.T) {
	content := writeOK(t, WriteParams{
		OutputName: new("report"),
		Operations: &[]Operation{
			{Op: "create_sheet", Name: new("Data")},
			{Op: "set_cell", Sheet: new("Data"), Cell: new("A1"), Value: "hi"},
		},
	})
	f := openBytes(t, content)
	if got := f.GetSheetList(); len(got) != 1 || got[0] != "Data" {
		t.Fatalf("expected [Data], got %v", got)
	}
}

func TestDefaultSheetKeptWhenUsed(t *testing.T) {
	content := writeOK(t, WriteParams{
		OutputName: new("report"),
		Operations: &[]Operation{
			{Op: "set_cell", Sheet: new("Sheet1"), Cell: new("A1"), Value: "kept"},
			{Op: "create_sheet", Name: new("Extra")},
			{Op: "set_cell", Sheet: new("Extra"), Cell: new("A1"), Value: "x"},
		},
	})
	f := openBytes(t, content)
	list := f.GetSheetList()
	if len(list) != 2 || list[0] != "Sheet1" || list[1] != "Extra" {
		t.Fatalf("expected [Sheet1 Extra], got %v", list)
	}
}

func TestDefaultSheetKeptWithNonValueContent(t *testing.T) {
	content := writeOK(t, WriteParams{
		OutputName: new("report"),
		Operations: &[]Operation{
			{Op: "create_sheet", Name: new("Data")},
			{Op: "set_values", Sheet: new("Data"), Anchor: new("A1"), Values: [][]any{{"m", "v"}, {"a", 1}, {"b", 2}}},
			{Op: "add_chart", Anchor: new("D2"), Chart: &ChartSpec{Type: "col", Series: []ChartSeriesSpec{
				{Name: "Data!$B$1", Categories: "Data!$A$2:$A$3", Values: "Data!$B$2:$B$3"},
			}}},
		},
	})
	f := openBytes(t, content)
	list := f.GetSheetList()
	if len(list) != 2 || list[0] != "Sheet1" || list[1] != "Data" {
		t.Fatalf("expected [Sheet1 Data] (chart-bearing default retained), got %v", list)
	}
}

func TestDefaultSheetKeptWhenDefinedNameRefers(t *testing.T) {
	content := writeOK(t, WriteParams{
		OutputName: new("report"),
		Operations: &[]Operation{
			{Op: "set_defined_name", Name: new("Totals"), RefersTo: new("Sheet1!$A$1")},
			{Op: "create_sheet", Name: new("Data")},
			{Op: "set_cell", Sheet: new("Data"), Cell: new("A1"), Value: "x"},
		},
	})
	f := openBytes(t, content)
	if idx, _ := f.GetSheetIndex("Sheet1"); idx < 0 {
		t.Fatalf("Sheet1 was pruned despite a defined name referencing it: %v", f.GetSheetList())
	}
}

func TestModifyKeepsEmptyFirstSheet(t *testing.T) {
	seed := excelizeNewBytes(t)
	content := writeOK(t, WriteParams{
		Source:     &sdk.Artifact{Filename: "book.xlsx", Content: seed},
		OutputName: new("book"),
		Operations: &[]Operation{
			{Op: "create_sheet", Name: new("Data")},
			{Op: "set_cell", Sheet: new("Data"), Cell: new("A1"), Value: "x"},
		},
	})
	f := openBytes(t, content)
	list := f.GetSheetList()
	if len(list) != 2 || list[0] != "Sheet1" || list[1] != "Data" {
		t.Fatalf("expected [Sheet1 Data] (modify path never prunes), got %v", list)
	}
}

func TestRenamedDefaultThenEmptyKept(t *testing.T) {
	content := writeOK(t, WriteParams{
		OutputName: new("report"),
		Operations: &[]Operation{
			{Op: "rename_sheet", Sheet: new("Sheet1"), NewName: new("Foo")},
			{Op: "create_sheet", Name: new("Bar")},
			{Op: "set_cell", Sheet: new("Bar"), Cell: new("A1"), Value: "x"},
		},
	})
	f := openBytes(t, content)
	list := f.GetSheetList()
	if len(list) != 2 || list[0] != "Foo" || list[1] != "Bar" {
		t.Fatalf("expected [Foo Bar], got %v", list)
	}
}

func TestModifyKeepsVMLContent(t *testing.T) {
	seed := excelize.NewFile()
	if _, err := seed.NewSheet("Notes"); err != nil {
		t.Fatalf("new sheet: %v", err)
	}
	if err := seed.AddComment("Notes", excelize.Comment{
		Cell: "A1", Author: "Alice",
		Paragraph: []excelize.RichTextRun{{Text: "keep me"}},
	}); err != nil {
		t.Fatalf("add comment: %v", err)
	}
	if err := seed.AddFormControl("Sheet1", excelize.FormControl{
		Cell: "B2", Type: excelize.FormControlCheckBox, Text: "Approved", Checked: true,
	}); err != nil {
		t.Fatalf("add form control: %v", err)
	}
	buf, err := seed.WriteToBuffer()
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	_ = seed.Close()

	content := writeOK(t, WriteParams{
		Source:     &sdk.Artifact{Filename: "in.xlsx", Content: buf.Bytes()},
		OutputName: new("out"),
		Operations: &[]Operation{
			{Op: "set_cell", Sheet: new("Sheet1"), Cell: new("D4"), Value: "edited"},
			{Op: "set_cell", Sheet: new("Notes"), Cell: new("B1"), Value: "edited"},
		},
	})
	f := openBytes(t, content)

	controls, err := f.GetFormControls("Sheet1")
	if err != nil {
		t.Fatalf("GetFormControls: %v", err)
	}
	if len(controls) != 1 {
		t.Fatalf("form controls = %d, want 1", len(controls))
	}
	if c := controls[0]; c.Cell != "B2" || c.Type != excelize.FormControlCheckBox || !c.Checked || c.Text != "Approved" {
		t.Errorf("form control = %+v, want checked check box %q at B2", c, "Approved")
	}

	comments, err := f.GetComments("Notes")
	if err != nil {
		t.Fatalf("GetComments: %v", err)
	}
	if len(comments) != 1 || comments[0].Cell != "A1" || comments[0].Author != "Alice" {
		t.Fatalf("comments = %+v, want one comment by Alice at A1", comments)
	}
	if len(comments[0].Paragraph) != 1 || comments[0].Paragraph[0].Text != "keep me" {
		t.Errorf("comment paragraph = %+v, want %q", comments[0].Paragraph, "keep me")
	}

	if v, _ := f.GetCellValue("Sheet1", "D4"); v != "edited" {
		t.Errorf("D4 = %q, want edited", v)
	}
}

func TestModifyKeepsSharedVMLDrawing(t *testing.T) {
	seed := excelize.NewFile()
	if err := seed.AddComment("Sheet1", excelize.Comment{
		Cell: "A1", Author: "Alice",
		Paragraph: []excelize.RichTextRun{{Text: "keep me"}},
	}); err != nil {
		t.Fatalf("add comment: %v", err)
	}
	for _, c := range []excelize.FormControl{
		{Cell: "B2", Type: excelize.FormControlCheckBox, Text: "Approved", Checked: true},
		{Cell: "B4", Type: excelize.FormControlOptionButton, Text: "Pick me"},
	} {
		if err := seed.AddFormControl("Sheet1", c); err != nil {
			t.Fatalf("add form control %s: %v", c.Cell, err)
		}
	}
	buf, err := seed.WriteToBuffer()
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	_ = seed.Close()

	content := writeOK(t, WriteParams{
		Source:     &sdk.Artifact{Filename: "in.xlsx", Content: buf.Bytes()},
		OutputName: new("out"),
		Operations: &[]Operation{
			{Op: "set_cell", Sheet: new("Sheet1"), Cell: new("D4"), Value: "edited"},
		},
	})
	f := openBytes(t, content)

	controls, err := f.GetFormControls("Sheet1")
	if err != nil {
		t.Fatalf("GetFormControls: %v", err)
	}
	if len(controls) != 2 {
		t.Fatalf("form controls = %+v, want 2", controls)
	}
	if c := controls[0]; c.Cell != "B2" || c.Type != excelize.FormControlCheckBox || !c.Checked || c.Text != "Approved" {
		t.Errorf("first control = %+v, want checked check box %q at B2", c, "Approved")
	}
	if c := controls[1]; c.Cell != "B4" || c.Type != excelize.FormControlOptionButton || c.Checked || c.Text != "Pick me" {
		t.Errorf("second control = %+v, want unchecked option button %q at B4", c, "Pick me")
	}

	comments, err := f.GetComments("Sheet1")
	if err != nil {
		t.Fatalf("GetComments: %v", err)
	}
	if len(comments) != 1 || comments[0].Cell != "A1" || comments[0].Author != "Alice" {
		t.Fatalf("comments = %+v, want one comment by Alice at A1", comments)
	}

	const shapeIDPrefix = `id="_x0000_s`
	seen := map[string]bool{}
	zr, err := zip.NewReader(bytes.NewReader(content), int64(len(content)))
	if err != nil {
		t.Fatalf("open zip: %v", err)
	}
	for _, zf := range zr.File {
		if !strings.HasPrefix(zf.Name, "xl/drawings/vmlDrawing") {
			continue
		}
		rc, err := zf.Open()
		if err != nil {
			t.Fatalf("open %s: %v", zf.Name, err)
		}
		data, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			t.Fatalf("read %s: %v", zf.Name, err)
		}
		for _, part := range strings.Split(string(data), shapeIDPrefix)[1:] {
			id, _, _ := strings.Cut(part, `"`)
			if seen[id] {
				t.Errorf("duplicate VML shape id _x0000_s%s in %s", id, zf.Name)
			}
			seen[id] = true
		}
	}
	if len(seen) != 3 {
		t.Errorf("VML shape ids = %v, want 3 distinct ids (comment + 2 controls)", seen)
	}
}

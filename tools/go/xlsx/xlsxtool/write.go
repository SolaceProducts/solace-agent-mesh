// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package xlsxtool

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"

	sdk "github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"
	"github.com/xuri/excelize/v2"
	"gopkg.in/yaml.v3"
)

const xlsxMIMEType = "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"

func writeWorkbook(_ context.Context, p WriteParams, tc *sdk.ToolContext) (*sdk.Result, error) {
	f, err := openOrNew(p.Source)
	if err != nil {
		return sdk.Error(err.Error()), nil
	}
	defer func() { _ = f.Close() }()

	freshDefaultSheet := ""
	if p.Source == nil || len(p.Source.Content) == 0 {
		if list := f.GetSheetList(); len(list) == 1 {
			freshDefaultSheet = list[0]
		}
	}
	defaultSheetUsed := false

	ops, err := resolveOperations(p)
	if err != nil {
		return sdk.Error(err.Error()), nil
	}
	if len(ops) == 0 {
		return sdk.Error("no operations provided: set 'operations' (inline) or 'operations_artifact' (a saved YAML/JSON ops array)"), nil
	}

	_ = tc.SendStatus(fmt.Sprintf("Applying %d operation(s)...", len(ops)))

	var dataSources []sdk.Artifact
	if p.DataSources != nil {
		dataSources = *p.DataSources
	}
	for i, op := range ops {
		if freshDefaultSheet != "" && !defaultSheetUsed && opTouchesSheet(f, op, freshDefaultSheet) {
			defaultSheetUsed = true
		}
		if err := applyOp(f, op, dataSources); err != nil {
			return sdk.Error(fmt.Sprintf("operation %d (%s): %v", i+1, op.Op, err)), nil
		}
	}

	if !defaultSheetUsed {
		pruneEmptyDefaultSheet(f, freshDefaultSheet)
	}

	buf, werr := f.WriteToBuffer()
	if werr != nil {
		return sdk.Error(fmt.Sprintf("failed to serialize workbook: %v", werr)), nil
	}

	filename := resolveOutputName(p)

	return sdk.OK(
		fmt.Sprintf("Wrote '%s' (%d operation(s), %d sheet(s))", filename, len(ops), len(f.GetSheetList())),
		sdk.WithDataObjects(sdk.DataObject{
			Name:        filename,
			Content:     buf.Bytes(),
			MIMEType:    xlsxMIMEType,
			Disposition: sdk.DispositionArtifact,
			Description: fmt.Sprintf("Excel workbook (%s)", strings.Join(f.GetSheetList(), ", ")),
		}),
	), nil
}

func resolveOutputName(p WriteParams) string {
	name := ""
	switch {
	case p.OutputName != nil && *p.OutputName != "":
		name = *p.OutputName
	case p.Source != nil && p.Source.Filename != "":
		name = p.Source.Filename
	default:
		name = "workbook"
	}
	if base, ver, ok := strings.Cut(name, ":"); ok && isAllDigits(ver) {
		name = base
	}
	if !strings.HasSuffix(strings.ToLower(name), ".xlsx") {
		name += ".xlsx"
	}
	return name
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func openOrNew(src *sdk.Artifact) (*excelize.File, error) {
	if src == nil || len(src.Content) == 0 {
		return excelize.NewFile(), nil
	}
	f, err := excelize.OpenReader(bytes.NewReader(src.Content))
	if err != nil {
		return nil, fmt.Errorf("cannot open source workbook %q as .xlsx: %v", src.Filename, err)
	}
	return f, nil
}

func resolveOperations(p WriteParams) ([]Operation, error) {
	if p.OperationsArtifact != nil && len(p.OperationsArtifact.Content) > 0 {
		return parseOperationsArtifact(p.OperationsArtifact)
	}
	if p.Operations != nil {
		return *p.Operations, nil
	}
	return nil, nil
}

func parseOperationsArtifact(art *sdk.Artifact) ([]Operation, error) {
	var generic any
	if err := yaml.Unmarshal(art.Content, &generic); err != nil {
		return nil, fmt.Errorf("operations artifact %q is not valid YAML or JSON: %v", art.Filename, err)
	}
	jsonBytes, err := json.Marshal(generic)
	if err != nil {
		return nil, fmt.Errorf("operations artifact %q has non-JSON-representable content: %v", art.Filename, err)
	}
	var ops []Operation
	if err := json.Unmarshal(jsonBytes, &ops); err != nil {
		return nil, fmt.Errorf("operations artifact %q does not match the expected operations-array schema: %v", art.Filename, err)
	}
	return ops, nil
}

func sheetName(f *excelize.File, op Operation) (string, error) {
	if op.Sheet != nil && *op.Sheet != "" {
		return *op.Sheet, nil
	}
	list := f.GetSheetList()
	if len(list) == 0 {
		return "", fmt.Errorf("workbook has no sheets")
	}
	return list[0], nil
}

func applyOp(f *excelize.File, op Operation, dataSources []sdk.Artifact) error {
	switch op.Op {
	case "create_sheet":
		return opCreateSheet(f, op)
	case "delete_sheet":
		return opDeleteSheet(f, op)
	case "rename_sheet":
		return opRenameSheet(f, op)
	case "set_cell":
		return opSetCell(f, op)
	case "set_values":
		return opSetValues(f, op)
	case "fill_from_data":
		return opFillFromData(f, op, dataSources)
	case "set_formula_range":
		return opSetFormulaRange(f, op)
	case "set_style":
		return opSetStyle(f, op)
	case "merge":
		return opMerge(f, op)
	case "unmerge":
		return opUnmerge(f, op)
	case "set_col_width":
		return opSetColWidth(f, op)
	case "set_row_height":
		return opSetRowHeight(f, op)
	case "freeze_panes":
		return opFreezePanes(f, op)
	case "auto_filter":
		return opAutoFilter(f, op)
	case "add_chart":
		return opAddChart(f, op)
	case "set_defined_name":
		return opSetDefinedName(f, op)
	case "":
		return fmt.Errorf("missing 'op' field")
	default:
		return fmt.Errorf("unknown op %q", op.Op)
	}
}

func opCreateSheet(f *excelize.File, op Operation) error {
	if op.Name == nil || *op.Name == "" {
		return fmt.Errorf("create_sheet requires 'name'")
	}
	if _, err := f.NewSheet(*op.Name); err != nil {
		return err
	}
	if op.Index != nil {
		list := f.GetSheetList()
		if *op.Index >= 0 && *op.Index < len(list) && list[*op.Index] != *op.Name {
			_ = f.MoveSheet(*op.Name, list[*op.Index])
		}
	}
	return nil
}

func opDeleteSheet(f *excelize.File, op Operation) error {
	if op.Name == nil || *op.Name == "" {
		return fmt.Errorf("delete_sheet requires 'name'")
	}
	if len(f.GetSheetList()) <= 1 {
		return fmt.Errorf("cannot delete %q: a workbook must keep at least one sheet", *op.Name)
	}
	return f.DeleteSheet(*op.Name)
}

func opRenameSheet(f *excelize.File, op Operation) error {
	from, err := sheetName(f, op)
	if err != nil {
		return err
	}
	if op.NewName == nil || *op.NewName == "" {
		return fmt.Errorf("rename_sheet requires 'new_name'")
	}
	if !strings.EqualFold(*op.NewName, from) {
		if idx, _ := f.GetSheetIndex(*op.NewName); idx >= 0 {
			return fmt.Errorf("cannot rename %q to %q: a sheet named %q already exists", from, *op.NewName, *op.NewName)
		}
	}
	return f.SetSheetName(from, *op.NewName)
}

func opSetCell(f *excelize.File, op Operation) error {
	sheet, err := sheetName(f, op)
	if err != nil {
		return err
	}
	if op.Cell == nil || *op.Cell == "" {
		return fmt.Errorf("set_cell requires 'cell'")
	}
	if err := ensureSheet(f, sheet); err != nil {
		return err
	}
	if op.Formula != nil && *op.Formula != "" {
		return f.SetCellFormula(sheet, *op.Cell, normalizeFormula(*op.Formula))
	}
	if op.Value == nil {
		return fmt.Errorf("set_cell requires 'value' or 'formula'")
	}
	return writeCellValue(f, sheet, *op.Cell, op.Value, true)
}

func opSetValues(f *excelize.File, op Operation) error {
	sheet, err := sheetName(f, op)
	if err != nil {
		return err
	}
	if op.Anchor == nil || *op.Anchor == "" {
		return fmt.Errorf("set_values requires 'anchor'")
	}
	if len(op.Values) == 0 {
		return fmt.Errorf("set_values requires a non-empty 'values' array")
	}
	if err := ensureSheet(f, sheet); err != nil {
		return err
	}
	return writeGrid(f, sheet, *op.Anchor, op.Values, true)
}

func opSetFormulaRange(f *excelize.File, op Operation) error {
	sheet, err := sheetName(f, op)
	if err != nil {
		return err
	}
	if op.Range == nil || *op.Range == "" {
		return fmt.Errorf("set_formula_range requires 'range'")
	}
	if op.Formula == nil || *op.Formula == "" {
		return fmt.Errorf("set_formula_range requires 'formula'")
	}
	if err := ensureSheet(f, sheet); err != nil {
		return err
	}
	mode := "shared"
	if op.Mode != nil && *op.Mode != "" {
		mode = *op.Mode
	}
	switch mode {
	case "shared":
		return setSharedFormula(f, sheet, *op.Range, normalizeFormula(*op.Formula))
	case "template":
		return setTemplateFormula(f, sheet, *op.Range, normalizeFormula(*op.Formula))
	default:
		return fmt.Errorf("unknown mode %q (expected 'shared' or 'template')", mode)
	}
}

func opSetStyle(f *excelize.File, op Operation) error {
	sheet, err := sheetName(f, op)
	if err != nil {
		return err
	}
	if op.Range == nil || *op.Range == "" {
		return fmt.Errorf("set_style requires 'range'")
	}
	if op.Style == nil {
		return fmt.Errorf("set_style requires 'style'")
	}
	if err := ensureSheet(f, sheet); err != nil {
		return err
	}
	styleID, serr := f.NewStyle(toExcelizeStyle(op.Style))
	if serr != nil {
		return fmt.Errorf("invalid style: %v", serr)
	}
	tl, br := splitRange(*op.Range)
	return f.SetCellStyle(sheet, tl, br, styleID)
}

func opMerge(f *excelize.File, op Operation) error {
	sheet, err := sheetName(f, op)
	if err != nil {
		return err
	}
	if op.Range == nil || *op.Range == "" {
		return fmt.Errorf("merge requires 'range'")
	}
	if err := ensureSheet(f, sheet); err != nil {
		return err
	}
	tl, br := splitRange(*op.Range)
	return f.MergeCell(sheet, tl, br)
}

func opUnmerge(f *excelize.File, op Operation) error {
	sheet, err := sheetName(f, op)
	if err != nil {
		return err
	}
	if op.Range == nil || *op.Range == "" {
		return fmt.Errorf("unmerge requires 'range'")
	}
	tl, br := splitRange(*op.Range)
	return f.UnmergeCell(sheet, tl, br)
}

func opSetColWidth(f *excelize.File, op Operation) error {
	sheet, err := sheetName(f, op)
	if err != nil {
		return err
	}
	if op.Cols == nil || *op.Cols == "" {
		return fmt.Errorf("set_col_width requires 'cols' (e.g. 'A' or 'A:C')")
	}
	if op.Width == nil {
		return fmt.Errorf("set_col_width requires 'width'")
	}
	if err := ensureSheet(f, sheet); err != nil {
		return err
	}
	start, end := splitColRange(*op.Cols)
	return f.SetColWidth(sheet, start, end, *op.Width)
}

func opSetRowHeight(f *excelize.File, op Operation) error {
	sheet, err := sheetName(f, op)
	if err != nil {
		return err
	}
	if op.Row == nil {
		return fmt.Errorf("set_row_height requires 'row'")
	}
	if op.Height == nil {
		return fmt.Errorf("set_row_height requires 'height'")
	}
	if err := ensureSheet(f, sheet); err != nil {
		return err
	}
	return f.SetRowHeight(sheet, *op.Row, *op.Height)
}

func opFreezePanes(f *excelize.File, op Operation) error {
	sheet, err := sheetName(f, op)
	if err != nil {
		return err
	}
	if op.Cell == nil || *op.Cell == "" {
		return fmt.Errorf("freeze_panes requires 'cell' (the top-left unfrozen cell, e.g. B2)")
	}
	if err := ensureSheet(f, sheet); err != nil {
		return err
	}
	col, row, cerr := excelize.CellNameToCoordinates(*op.Cell)
	if cerr != nil {
		return fmt.Errorf("invalid cell %q: %v", *op.Cell, cerr)
	}
	xSplit, ySplit := col-1, row-1
	var activePane string
	switch {
	case xSplit > 0 && ySplit > 0:
		activePane = "bottomRight"
	case ySplit > 0:
		activePane = "bottomLeft"
	case xSplit > 0:
		activePane = "topRight"
	default:
		return nil
	}
	return f.SetPanes(sheet, &excelize.Panes{
		Freeze:      true,
		XSplit:      xSplit,
		YSplit:      ySplit,
		TopLeftCell: *op.Cell,
		ActivePane:  activePane,
		Selection: []excelize.Selection{
			{SQRef: *op.Cell, ActiveCell: *op.Cell, Pane: activePane},
		},
	})
}

func opAutoFilter(f *excelize.File, op Operation) error {
	sheet, err := sheetName(f, op)
	if err != nil {
		return err
	}
	if op.Range == nil || *op.Range == "" {
		return fmt.Errorf("auto_filter requires 'range'")
	}
	if err := ensureSheet(f, sheet); err != nil {
		return err
	}
	return f.AutoFilter(sheet, *op.Range, []excelize.AutoFilterOptions{})
}

func opSetDefinedName(f *excelize.File, op Operation) error {
	if op.Name == nil || *op.Name == "" {
		return fmt.Errorf("set_defined_name requires 'name'")
	}
	if op.RefersTo == nil || *op.RefersTo == "" {
		return fmt.Errorf("set_defined_name requires 'refers_to'")
	}
	dn := &excelize.DefinedName{Name: *op.Name, RefersTo: *op.RefersTo}
	if op.Sheet != nil && *op.Sheet != "" {
		dn.Scope = *op.Sheet
	}
	return f.SetDefinedName(dn)
}

func pruneEmptyDefaultSheet(f *excelize.File, defaultSheet string) {
	if defaultSheet == "" {
		return
	}
	if len(f.GetSheetList()) <= 1 {
		return
	}
	if idx, _ := f.GetSheetIndex(defaultSheet); idx < 0 {
		return
	}
	rows, err := f.GetRows(defaultSheet)
	if err != nil || len(rows) > 0 {
		return
	}
	_ = f.DeleteSheet(defaultSheet)
}

func opTouchesSheet(f *excelize.File, op Operation, target string) bool {
	switch op.Op {
	case "create_sheet":
		return op.Name != nil && *op.Name == target
	case "delete_sheet":
		return false
	case "rename_sheet":
		if op.NewName != nil && *op.NewName == target {
			return true
		}
		from, err := sheetName(f, op)
		return err == nil && from == target
	case "set_defined_name":
		if op.Sheet != nil && *op.Sheet == target {
			return true
		}
		if op.RefersTo != nil {
			for _, s := range sheetRefsIn(*op.RefersTo) {
				if s == target {
					return true
				}
			}
		}
		return false
	default:
		s, err := sheetName(f, op)
		return err == nil && s == target
	}
}

func sheetRefsIn(refersTo string) []string {
	var out []string
	for part := range strings.FieldsSeq(strings.ReplaceAll(refersTo, ",", " ")) {
		if i := strings.LastIndex(part, "!"); i >= 0 {
			if name := strings.Trim(part[:i], "'"); name != "" {
				out = append(out, name)
			}
		}
	}
	return out
}

func ensureSheet(f *excelize.File, sheet string) error {
	if idx, _ := f.GetSheetIndex(sheet); idx >= 0 {
		return nil
	}
	_, err := f.NewSheet(sheet)
	return err
}

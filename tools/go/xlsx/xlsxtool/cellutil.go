// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package xlsxtool

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/xuri/excelize/v2"
)

func normalizeFormula(formula string) string {
	return strings.TrimPrefix(strings.TrimSpace(formula), "=")
}

func splitRange(rng string) (topLeft, bottomRight string) {
	if tl, br, ok := strings.Cut(rng, ":"); ok {
		return tl, br
	}
	return rng, rng
}

func splitColRange(cols string) (start, end string) {
	if s, e, ok := strings.Cut(cols, ":"); ok {
		return s, e
	}
	return cols, cols
}

func writeCellValue(f *excelize.File, sheet, cell string, v any, interpretFormulas bool) error {
	if interpretFormulas {
		if s, ok := v.(string); ok && len(s) > 1 && s[0] == '=' {
			return f.SetCellFormula(sheet, cell, normalizeFormula(s))
		}
	}
	if v == nil {
		v = ""
	}
	return f.SetCellValue(sheet, cell, v)
}

func writeGrid(f *excelize.File, sheet, anchor string, grid [][]any, interpretFormulas bool) error {
	startCol, startRow, err := excelize.CellNameToCoordinates(anchor)
	if err != nil {
		return fmt.Errorf("invalid anchor %q: %v", anchor, err)
	}
	for i, row := range grid {
		for j, v := range row {
			cell, err := excelize.CoordinatesToCellName(startCol+j, startRow+i)
			if err != nil {
				return err
			}
			if err := writeCellValue(f, sheet, cell, v, interpretFormulas); err != nil {
				return fmt.Errorf("writing cell %s: %v", cell, err)
			}
		}
	}
	return nil
}

func writeGridBounded(f *excelize.File, sheet, rng string, grid [][]any, interpretFormulas bool) error {
	tl, br := splitRange(rng)
	c1, r1, err := excelize.CellNameToCoordinates(tl)
	if err != nil {
		return fmt.Errorf("invalid range %q: %v", rng, err)
	}
	c2, r2, err := excelize.CellNameToCoordinates(br)
	if err != nil {
		return fmt.Errorf("invalid range %q: %v", rng, err)
	}
	if c2 < c1 {
		c1, c2 = c2, c1
	}
	if r2 < r1 {
		r1, r2 = r2, r1
	}
	maxRows, maxCols := r2-r1+1, c2-c1+1

	if len(grid) > maxRows {
		return fmt.Errorf("data has %d rows but target range %s allows %d — widen the range or trim the data", len(grid), rng, maxRows)
	}
	for i, row := range grid {
		if len(row) > maxCols {
			return fmt.Errorf("data row %d has %d columns but target range %s allows %d — widen the range or trim the data", i+1, len(row), rng, maxCols)
		}
	}

	if err := clearRange(f, sheet, c1, r1, c2, r2); err != nil {
		return err
	}
	anchor, aerr := excelize.CoordinatesToCellName(c1, r1)
	if aerr != nil {
		return aerr
	}
	return writeGrid(f, sheet, anchor, grid, interpretFormulas)
}

func clearRange(f *excelize.File, sheet string, c1, r1, c2, r2 int) error {
	for row := r1; row <= r2; row++ {
		for col := c1; col <= c2; col++ {
			cell, err := excelize.CoordinatesToCellName(col, row)
			if err != nil {
				return err
			}
			if err := f.SetCellValue(sheet, cell, ""); err != nil {
				return err
			}
		}
	}
	return nil
}

func setSharedFormula(f *excelize.File, sheet, rng, formula string) error {
	tl, br := splitRange(rng)
	c1, r1, err := excelize.CellNameToCoordinates(tl)
	if err != nil {
		return fmt.Errorf("invalid range start %q: %v", tl, err)
	}
	c2, r2, err := excelize.CellNameToCoordinates(br)
	if err != nil {
		return fmt.Errorf("invalid range end %q: %v", br, err)
	}
	if c2 < c1 {
		c1, c2 = c2, c1
	}
	if r2 < r1 {
		r1, r2 = r2, r1
	}
	for row := r1; row <= r2; row++ {
		for col := c1; col <= c2; col++ {
			cell, err := excelize.CoordinatesToCellName(col, row)
			if err != nil {
				return err
			}
			if err := f.SetCellFormula(sheet, cell, shiftFormula(formula, col-c1, row-r1)); err != nil {
				return fmt.Errorf("set formula at %s: %v", cell, err)
			}
		}
	}
	return nil
}

func shiftFormula(formula string, dCol, dRow int) string {
	var b strings.Builder
	n := len(formula)
	var prev byte
	for i := 0; i < n; {
		c := formula[i]
		if c == '"' || c == '\'' {
			q := c
			b.WriteByte(c)
			i++
			for i < n {
				b.WriteByte(formula[i])
				if formula[i] == q {
					i++
					break
				}
				i++
			}
			prev = q
			continue
		}
		if (c == '$' || isAlpha(c)) && !isFormulaNameByte(prev) {
			if ref, adv, ok := shiftRefAt(formula[i:], dCol, dRow); ok {
				b.WriteString(ref)
				prev = formula[i+adv-1]
				i += adv
				continue
			}
		}
		b.WriteByte(c)
		prev = c
		i++
	}
	return b.String()
}

func isAlpha(c byte) bool { return c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' }

func isFormulaNameByte(c byte) bool {
	return isAlpha(c) || c >= '0' && c <= '9' || c == '_' || c == '.'
}

func shiftRefAt(s string, dCol, dRow int) (string, int, bool) {
	i := 0
	colAbs := false
	if i < len(s) && s[i] == '$' {
		colAbs = true
		i++
	}
	colStart := i
	for i < len(s) && isAlpha(s[i]) {
		i++
	}
	if i == colStart {
		return "", 0, false
	}
	col := strings.ToUpper(s[colStart:i])
	rowAbs := false
	j := i
	if j < len(s) && s[j] == '$' {
		rowAbs = true
		j++
	}
	rowStart := j
	for j < len(s) && s[j] >= '0' && s[j] <= '9' {
		j++
	}
	if j == rowStart {
		return "", 0, false
	}
	if j < len(s) && (s[j] == '(' || isFormulaNameByte(s[j])) {
		return "", 0, false
	}
	colNum, err := excelize.ColumnNameToNumber(col)
	if err != nil {
		return "", 0, false
	}
	rowNum, err := strconv.Atoi(s[rowStart:j])
	if err != nil {
		return "", 0, false
	}
	if !colAbs {
		colNum += dCol
	}
	if !rowAbs {
		rowNum += dRow
	}
	if colNum < 1 || rowNum < 1 {
		return "", 0, false
	}
	newCol, err := excelize.ColumnNumberToName(colNum)
	if err != nil {
		return "", 0, false
	}
	var out strings.Builder
	if colAbs {
		out.WriteByte('$')
	}
	out.WriteString(newCol)
	if rowAbs {
		out.WriteByte('$')
	}
	out.WriteString(strconv.Itoa(rowNum))
	return out.String(), j, true
}

func setTemplateFormula(f *excelize.File, sheet, rng, formula string) error {
	tl, br := splitRange(rng)
	c1, r1, err := excelize.CellNameToCoordinates(tl)
	if err != nil {
		return fmt.Errorf("invalid range start %q: %v", tl, err)
	}
	c2, r2, err := excelize.CellNameToCoordinates(br)
	if err != nil {
		return fmt.Errorf("invalid range end %q: %v", br, err)
	}
	for col := c1; col <= c2; col++ {
		colName, err := excelize.ColumnNumberToName(col)
		if err != nil {
			return err
		}
		for row := r1; row <= r2; row++ {
			cell, err := excelize.CoordinatesToCellName(col, row)
			if err != nil {
				return err
			}
			expanded := strings.NewReplacer("{row}", fmt.Sprintf("%d", row), "{col}", colName).Replace(formula)
			if err := f.SetCellFormula(sheet, cell, expanded); err != nil {
				return err
			}
		}
	}
	return nil
}

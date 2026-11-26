// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package xlsxtool

import (
	sdk "github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"
)

func Tools() []*sdk.ToolDef {
	return []*sdk.ToolDef{
		sdk.NewTool("xlsx_read",
			"Read an Excel (.xlsx) workbook selectively — describe its structure, read a range or row window, read formulas, or search for cells. Defaults to mode=describe. Designed for targeted reads so a large workbook never loads wholesale into context: start with describe to orient, then pull only the range/rows you need or use search to locate cells.",
			readWorkbook,
			sdk.WithInstructions(readInstructions),
			sdk.WithEffect(sdk.EffectReadOnly),
		),
		sdk.NewTool("xlsx_write",
			"Create a new Excel (.xlsx) workbook, or modify an existing one, by applying an ordered list of openpyxl-style operations (set_cell, set_values, fill_from_data, set_formula_range, set_style, merge, freeze_panes, auto_filter, add_chart, and sheet operations). Omit 'source' to create; pass it to edit. Fill ranges directly from CSV/JSON data artifacts and stamp formulas across whole ranges without writing cells one at a time. Saves the result as a new versioned artifact.",
			writeWorkbook,
			sdk.WithInstructions(writeInstructions),
			sdk.WithTimeout(120),
			sdk.WithEffect(sdk.EffectSideEffecting),
		),
	}
}

const readInstructions = `xlsx_read reads .xlsx workbooks selectively. Workflow: call mode=describe first (sheets, dimensions, header rows, merged ranges, defined names), then read only what you need. Modes: describe | values | formulas | search. For values/formulas, scope with 'range' (A1:F40) or 'rows' (1-100); whole-sheet reads over max_cells (default 2000) return guidance to narrow instead of data. Use mode=search with a 'query' to find cells by value (set regex=true for patterns). format: markdown (default) | csv | json_rows.`

const writeInstructions = `xlsx_write applies an ordered 'operations' list to a workbook (omit 'source' to create, pass it to modify) and saves a new .xlsx artifact. Prefer fill_from_data to load bulk rows from a CSV/JSON 'data_sources' artifact instead of re-typing them, and set_formula_range to stamp a formula across a range (mode=shared adjusts relative refs automatically). For large or iterative builds, put the operations array in a saved YAML/JSON artifact and pass it as 'operations_artifact' so you can edit it in place rather than re-emitting the whole list. The full operation catalog, styling fields, and worked examples are in the excel skill's references — load the skill for details.`

// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package xlsxtool

import sdk "github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"

type WriteParams struct {
	Source             *sdk.Artifact   `json:"source" desc:"Existing .xlsx artifact to open and modify (filename, optionally name.xlsx:version). Omit to create a new workbook."`
	Operations         *[]Operation    `json:"operations" desc:"Ordered list of operations applied top-to-bottom. See the excel skill references for the full op catalog. Omit when supplying operations_artifact instead."`
	OperationsArtifact *sdk.Artifact   `json:"operations_artifact" desc:"Alternative to inline operations: filename of a YAML or JSON artifact holding the operations array. Preferred for large/iterative builds (edit in place via artifact_search_and_replace_regex). When set, takes precedence over inline operations."`
	DataSources        *[]sdk.Artifact `json:"data_sources" desc:"Data artifacts (CSV/TSV/JSON) referenced by fill_from_data operations, by 0-based index ('0') or filename. Omit when no fill_from_data operation is used."`
	OutputName         *string         `json:"output_name" desc:"Output filename. Defaults to the 'source' filename when modifying (so the edit saves as a new version of the same workbook), or 'workbook' for a fresh create. Set this only to fork a differently-named copy. .xlsx is appended automatically."`
}

type Operation struct {
	Op string `json:"op" desc:"One of: create_sheet, delete_sheet, rename_sheet, set_cell, set_values, fill_from_data, set_formula_range, set_style, merge, unmerge, set_col_width, set_row_height, freeze_panes, auto_filter, add_chart, set_defined_name."`

	Sheet   *string `json:"sheet" desc:"Target sheet name. Defaults to the first sheet when omitted."`
	Name    *string `json:"name" desc:"Sheet name (create_sheet/delete_sheet) or defined-name (set_defined_name)."`
	NewName *string `json:"new_name" desc:"New sheet name (rename_sheet)."`
	Index   *int    `json:"index" desc:"0-based sheet position (create_sheet); best-effort placement."`

	Cell    *string `json:"cell" desc:"A1 cell ref (set_cell value/formula; freeze_panes anchor)."`
	Value   any     `json:"value,omitempty" desc:"Literal cell value — string, number, or boolean (set_cell)."`
	Formula *string `json:"formula" desc:"Excel formula, e.g. =SUM(A1:A9) (set_cell, set_formula_range). Leading = optional."`

	Anchor *string `json:"anchor" desc:"Top-left A1 cell where a block or chart is placed (set_values, fill_from_data, add_chart)."`
	Values [][]any `json:"values,omitempty" desc:"2D array of literal values written row-major from anchor (set_values)."`

	Range *string `json:"range" desc:"A1 range, e.g. C2:C100 (set_formula_range, set_style, merge, unmerge, auto_filter). On fill_from_data it is an optional bounded target: data overflowing the range errors, and cells in the range not covered by the data are cleared."`
	Mode  *string `json:"mode" desc:"set_formula_range mode: 'shared' (default — relative refs auto-adjust per cell, e.g. =A2*B2 over C2:C100 fills =A3*B3 … =A100*B100) or 'template' (expand {row}/{col} tokens per cell)."`

	Source        *string  `json:"source" desc:"fill_from_data: 0-based index ('0') or filename of a data_sources artifact."`
	Format        *string  `json:"format" desc:"fill_from_data source format: 'auto' (default), 'csv', 'tsv', 'json'."`
	IncludeHeader *bool    `json:"include_header" desc:"fill_from_data: write a header row (default true)."`
	Columns       []string `json:"columns,omitempty" desc:"fill_from_data: explicit column order for JSON arrays-of-objects."`

	Style *CellStyle `json:"style" desc:"Style to apply to the range (set_style)."`

	Cols   *string  `json:"cols" desc:"Column or column range, e.g. 'A' or 'A:C' (set_col_width)."`
	Width  *float64 `json:"width" desc:"Column width in character units (set_col_width)."`
	Row    *int     `json:"row" desc:"1-based row number (set_row_height)."`
	Height *float64 `json:"height" desc:"Row height in points (set_row_height)."`

	Chart    *ChartSpec `json:"chart" desc:"Chart definition (add_chart)."`
	RefersTo *string    `json:"refers_to" desc:"Range a defined name points to, e.g. Q3!$A$1:$A$50 (set_defined_name)."`
}

type CellStyle struct {
	Bold         *bool    `json:"bold" desc:"Bold font."`
	Italic       *bool    `json:"italic" desc:"Italic font."`
	Underline    *bool    `json:"underline" desc:"Underline font."`
	Font         *string  `json:"font" desc:"Font family name, e.g. Calibri."`
	Size         *float64 `json:"size" desc:"Font size in points."`
	FontColor    *string  `json:"font_color" desc:"Font color hex without #, e.g. FFFFFF."`
	Fill         *string  `json:"fill" desc:"Background fill color hex without #, e.g. 4472C4."`
	NumberFormat *string  `json:"number_format" desc:"Excel number format code, e.g. #,##0.00 | 0% | $#,##0 | yyyy-mm-dd."`
	Align        *string  `json:"align" desc:"Horizontal alignment: left | center | right."`
	Valign       *string  `json:"valign" desc:"Vertical alignment: top | center | bottom."`
	WrapText     *bool    `json:"wrap_text" desc:"Wrap text within the cell."`
	Border       *string  `json:"border" desc:"Border on all sides: thin | medium | thick | dashed | dotted | double."`
	BorderColor  *string  `json:"border_color" desc:"Border color hex without #. Default 000000."`
}

type ChartSpec struct {
	Type   string            `json:"type" desc:"Chart type: col | bar | line | pie | doughnut | area | scatter | radar."`
	Title  *string           `json:"title" desc:"Chart title."`
	Series []ChartSeriesSpec `json:"series" desc:"One or more data series."`
}

type ChartSeriesSpec struct {
	Name       string `json:"name" desc:"Cell reference to the series label cell (usually the header), e.g. Q3!$B$1. Must be a reference, not a literal string — Excel renders the series name from this cell."`
	Categories string `json:"categories" desc:"Range ref for the category axis, e.g. Q3!$A$2:$A$13."`
	Values     string `json:"values" desc:"Range ref for the values, e.g. Q3!$B$2:$B$13."`
}

type ReadParams struct {
	Source   sdk.Artifact `json:"source" desc:"Filename of the .xlsx artifact to read (optionally name.xlsx:version)."`
	Mode     *string      `json:"mode" desc:"describe (default) | values | formulas | search."`
	Sheet    *string      `json:"sheet" desc:"Sheet to target. Omit in describe/search to cover all sheets."`
	Range    *string      `json:"range" desc:"A1 range to read in values/formulas mode, e.g. B2:F40."`
	Rows     *string      `json:"rows" desc:"Row window in values/formulas mode, e.g. 1-100 (1-based, inclusive)."`
	Query    *string      `json:"query" desc:"search mode: text (or regex) to find across cells."`
	Regex    *bool        `json:"regex" desc:"search mode: treat query as a regular expression."`
	Format   *string      `json:"format" desc:"Output rendering: markdown (default) | csv | json_rows."`
	MaxCells *int         `json:"max_cells" desc:"Safety cap on returned cells (default 2000). Over the cap, returns dimensions and guidance instead of data."`
}

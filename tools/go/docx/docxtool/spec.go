// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package docxtool

var blockFields = map[string]map[string]bool{
	"heading":           {"level": true, "text": true},
	"paragraph":         {"text": true, "runs": true, "align": true},
	"bullet_list":       {"items": true},
	"numbered_list":     {"items": true},
	"table":             {"headers": true, "rows": true, "header_bold": true, "col_widths": true},
	"image":             {"image_ref": true, "width_in": true, "height_in": true, "align": true},
	"page_break":        {},
	"table_of_contents": {"toc_title": true, "toc_levels": true},
}

var runFields = map[string]bool{
	"text": true, "bold": true, "italic": true, "underline": true, "color": true,
}

var requiredFields = map[string][]string{
	"heading":       {"level", "text"},
	"bullet_list":   {"items"},
	"numbered_list": {"items"},
	"image":         {"image_ref"},
}

type Block struct {
	Type string `json:"type" desc:"One of: heading, paragraph, bullet_list, numbered_list, table, image, page_break, table_of_contents."`

	Level int    `json:"level,omitempty" desc:"Heading level 1-4 (heading block)."`
	Text  string `json:"text,omitempty" desc:"Plain text (heading block, or paragraph block when runs is omitted)."`

	Runs  []RunSpec `json:"runs,omitempty" desc:"Rich formatted runs for a paragraph — use instead of text when the paragraph mixes bold/italic/underline/color within one line."`
	Align string    `json:"align,omitempty" desc:"Paragraph/image alignment: left | center | right | justify. Omit for left."`

	Items []string `json:"items,omitempty" desc:"List item text, one per bullet/number (bullet_list, numbered_list blocks)."`

	Headers    []string   `json:"headers,omitempty" desc:"Table header row text (table block). Omit for a table with no header row."`
	Rows       [][]string `json:"rows,omitempty" desc:"Table body rows, each an array of cell text (table block)."`
	HeaderBold *bool      `json:"header_bold,omitempty" desc:"Bold the header row (table block). Defaults to true."`
	ColWidths  []float64  `json:"col_widths,omitempty" desc:"Relative column width shares (table block), e.g. [1, 3] for a narrow label column beside a wide prose column. One entry per column. Omit to split the width evenly; a table always spans the full page width either way."`

	ImageRef string  `json:"image_ref,omitempty" desc:"0-based index into the top-level images array (image block)."`
	WidthIn  float64 `json:"width_in,omitempty" desc:"Image width in inches (image block). Defaults to 4."`
	HeightIn float64 `json:"height_in,omitempty" desc:"Image height in inches (image block). Defaults to preserving the image's aspect ratio at the resolved width."`

	TOCTitle  *string `json:"toc_title,omitempty" desc:"Heading text placed above the TOC field (table_of_contents block). Omit the field entirely (empty string) to insert the field with no preceding heading; unset defaults to \"Table of Contents\"."`
	TOCLevels string  `json:"toc_levels,omitempty" desc:"Heading levels the TOC field covers, e.g. \"1-3\" (table_of_contents block). Defaults to 1-3."`
}

type RunSpec struct {
	Text      string `json:"text" desc:"The run's text."`
	Bold      bool   `json:"bold,omitempty" desc:"Bold this run."`
	Italic    bool   `json:"italic,omitempty" desc:"Italicize this run."`
	Underline bool   `json:"underline,omitempty" desc:"Underline this run."`
	Color     string `json:"color,omitempty" desc:"6-digit hex color without '#', e.g. C0392B."`
}

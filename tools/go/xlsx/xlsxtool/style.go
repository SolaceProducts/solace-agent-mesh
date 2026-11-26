// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package xlsxtool

import "github.com/xuri/excelize/v2"

var borderStyleCodes = map[string]int{
	"thin":   1,
	"medium": 2,
	"dashed": 3,
	"dotted": 4,
	"thick":  5,
	"double": 6,
	"hair":   7,
}

func toExcelizeStyle(s *CellStyle) *excelize.Style {
	out := &excelize.Style{}

	if hasFont(s) {
		font := &excelize.Font{}
		if s.Bold != nil {
			font.Bold = *s.Bold
		}
		if s.Italic != nil {
			font.Italic = *s.Italic
		}
		if s.Underline != nil && *s.Underline {
			font.Underline = "single"
		}
		if s.Font != nil {
			font.Family = *s.Font
		}
		if s.Size != nil {
			font.Size = *s.Size
		}
		if s.FontColor != nil {
			font.Color = *s.FontColor
		}
		out.Font = font
	}

	if s.Fill != nil && *s.Fill != "" {
		out.Fill = excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{*s.Fill}}
	}

	if s.Align != nil || s.Valign != nil || (s.WrapText != nil && *s.WrapText) {
		align := &excelize.Alignment{}
		if s.Align != nil {
			align.Horizontal = *s.Align
		}
		if s.Valign != nil {
			align.Vertical = *s.Valign
		}
		if s.WrapText != nil {
			align.WrapText = *s.WrapText
		}
		out.Alignment = align
	}

	if s.NumberFormat != nil && *s.NumberFormat != "" {
		out.CustomNumFmt = s.NumberFormat
	}

	if s.Border != nil && *s.Border != "" {
		code, ok := borderStyleCodes[*s.Border]
		if ok {
			color := "000000"
			if s.BorderColor != nil && *s.BorderColor != "" {
				color = *s.BorderColor
			}
			out.Border = []excelize.Border{
				{Type: "top", Style: code, Color: color},
				{Type: "bottom", Style: code, Color: color},
				{Type: "left", Style: code, Color: color},
				{Type: "right", Style: code, Color: color},
			}
		}
	}

	return out
}

func hasFont(s *CellStyle) bool {
	return s.Bold != nil || s.Italic != nil || s.Underline != nil ||
		s.Font != nil || s.Size != nil || s.FontColor != nil
}

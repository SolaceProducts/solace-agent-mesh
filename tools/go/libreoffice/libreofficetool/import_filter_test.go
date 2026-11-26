// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package libreofficetool

import (
	"slices"
	"testing"
)

func TestImportFilterArgs(t *testing.T) {
	calc := []string{"--infilter=" + htmlCalcImportFilter}
	impress := []string{"--infilter=" + pdfImpressImportFilter}
	cases := []struct {
		input, format, filter string
		want                  []string
	}{
		{"crm.html", "xlsx", "Calc MS Excel 2007 XML", calc},
		{"crm.html", "xlsx", "", calc},
		{"crm.htm", "ods", "", calc},
		{"CRM.HTML", "csv", "", calc},
		{"crm.html", "xls", "", calc},
		{"crm.html", "pdf", "calc_pdf_Export", calc},
		{"crm.html", "xls", "MS Excel 97", calc},
		{"crm.html", "csv", "Text - txt - csv (StarCalc):44,34,76", calc},
		{"crm.html", "fods", "OpenDocument Spreadsheet Flat XML", calc},
		{"crm.html", "dbf", "dBase", calc},
		{"crm.html", "dbf", "dBase:IBM_850", calc},
		{"crm.html", "dif", "DIF", calc},
		{"crm.html", "slk", "SYLK", calc},
		{"crm.html", "csv", "Text (encoded):UTF8", nil},
		{"crm.html", "txt", "Text (encoded):DIFF", nil},
		{"crm.html", "xlsx", "writer_pdf_Export", nil},
		{"crm.html", "pdf", "", nil},
		{"crm.html", "docx", "", nil},
		{"crm.html", "odt", "MS Word 2007 XML", nil},
		{"sheet.csv", "xlsx", "", nil},
		{"report.docx", "xlsx", "", nil},
		{"input", "xlsx", "", nil},
		{"deck.pdf", "pptx", "", impress},
		{"DECK.PDF", "odp", "", impress},
		{"deck.pdf", "ppt", "", impress},
		{"deck.pdf", "pptx", "Impress MS PowerPoint 2007 XML", impress},
		{"deck.pdf", "ppt", "MS PowerPoint 97", impress},
		{"deck.pdf", "odp", "impress8", impress},
		{"deck.pdf", "pdf", "impress_pdf_Export", impress},
		{"deck.pdf", "pptx", "Impress MS PowerPoint 2007 XML:opts", impress},
		{"deck.pdf", "fodp", "OpenDocument Presentation Flat XML", impress},
		{"deck.pdf", "uop", "UOF presentation", impress},
		{"deck.pdf", "odp", "draw8", nil},
		{"deck.pdf", "pdf", "draw_pdf_Export", nil},
		{"deck.pdf", "pdf", "", nil},
		{"deck.pdf", "pptx", "writer_pdf_Export", nil},
		{"deck.pdf", "pdf", "writer_pdf_Export:impress", nil},
		{"deck.pdf", "potm", "", impress},
		{"deck.pdf", "xlsx", "", nil},
		{"deck.pdf", "docx", "", nil},
		{"deck.pdf", "html", "", nil},
		{"deck.pdf", "png", "", nil},
		{"deck.docx", "pptx", "", nil},
		{"deck.html", "pptx", "", nil},
	}
	for _, tc := range cases {
		if got := importFilterArgs(tc.input, tc.format, tc.filter); !slices.Equal(got, tc.want) {
			t.Errorf("importFilterArgs(%q, %q, %q) = %q, want %q", tc.input, tc.format, tc.filter, got, tc.want)
		}
	}
}

func TestMissingExportFilter(t *testing.T) {
	const line = "Error: no export filter for /w/out/crm.xlsx found, aborting."
	if got := missingExportFilter("Fontconfig warning\n" + line + "\nError: no export filter\n"); got != line {
		t.Errorf("missingExportFilter = %q, want %q", got, line)
	}
	if got := missingExportFilter("Fontconfig warning\nconvert /w/no export filter notes.html -> /w/out/x.pdf\n"); got != "" {
		t.Errorf("missingExportFilter = %q, want none for a filename that merely contains the phrase", got)
	}
}

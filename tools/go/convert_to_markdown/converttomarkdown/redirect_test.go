// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package converttomarkdown

import (
	"strings"
	"testing"
)

func TestPDFRefusalNamesNoSuccessorTool(t *testing.T) {
	for _, s := range []struct {
		name string
		text string
	}{
		{"error", pdfHandledElsewhereError{}.Error()},
		{"description", toolDescription},
		{"instructions", instructions},
	} {
		t.Run(s.name, func(t *testing.T) {
			for _, tool := range []string{"convert_pdf_to_markdown", "pdf_extract_text"} {
				if strings.Contains(s.text, tool) {
					t.Errorf("names %q, which is only correct where that tool is hosted; the model "+
						"is sent its own tool list and can route without being told: %q", tool, s.text)
				}
			}
		})
	}
}

func TestPDFRefusalStaysActionable(t *testing.T) {
	for _, s := range []struct {
		name string
		text string
	}{
		{"error", pdfHandledElsewhereError{}.Error()},
		{"description", toolDescription},
		{"instructions", instructions},
	} {
		t.Run(s.name, func(t *testing.T) {
			for _, tc := range []struct {
				want string
				why  string
			}{
				{"PDF input", "does not say what was refused"},
				{"reading PDFs", "does not say what kind of tool to reach for"},
				{"code sandbox", "no longer forbids parsing the bytes by hand"},
				{"could not be read", "gives no way out when no PDF tool is hosted"},
			} {
				if !strings.Contains(s.text, tc.want) {
					t.Errorf("refusal %s (missing %q): %q", tc.why, tc.want, s.text)
				}
			}
		})
	}
}

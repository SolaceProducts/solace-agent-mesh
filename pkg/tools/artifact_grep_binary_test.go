// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

const mimePDF = "application/pdf"

func TestBinaryNoteIsToolAgnostic(t *testing.T) {
	note := describeBinaryArtifacts([]binaryArtifact{{filename: "report.pdf", mimeType: mimePDF}})

	for _, want := range []string{
		"report.pdf", mimePDF, "could not be read",
		"parse the file's bytes",
		"designed to read or convert",
	} {
		if !strings.Contains(note, want) {
			t.Errorf("note is missing %q\nnote: %s", want, note)
		}
	}
	for _, name := range []string{
		"convert_pdf_to_markdown", "convert_file_to_markdown", "pdf_extract_text",
		"artifact_grep", "describe_image", "transcribe_audio", "execute_python_code",
	} {
		if strings.Contains(note, name) {
			t.Errorf("note names the tool %q, reintroducing a name that needs a registry guard"+
				"\nnote: %s", name, note)
		}
	}
}

func TestGrepBinaryArtifactReportsBinary(t *testing.T) {
	svc := newMockArtifactService()
	toolCtx := newTestToolCtx(svc)
	ctx := context.Background()

	saveTestArtifact(ctx, svc, toolCtx, "report.pdf", string(buildTestPDF("Gross domestic product")), "application/pdf")

	gt := &GrepTool{}
	result, err := gt.Execute(ctx, map[string]any{
		"pattern":  "Gross domestic product",
		"filename": "report.pdf",
	}, toolCtx)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != tool.StatusSuccess {
		t.Fatalf("status = %q", result.Status)
	}
	if strings.Contains(result.Message, "No matches found") {
		t.Errorf("binary artifact must not be reported as 'No matches found': %q", result.Message)
	}
	for _, want := range []string{"report.pdf", "binary", "application/pdf", "could not be read"} {
		if !strings.Contains(result.Message, want) {
			t.Errorf("message %q missing %q", result.Message, want)
		}
	}
	names, _ := result.Data["binary_files"].([]string)
	if len(names) != 1 || names[0] != "report.pdf" {
		t.Errorf("data[binary_files] = %v, want [report.pdf]", result.Data["binary_files"])
	}
}

func TestGrepBinaryDetectedDespiteWrongStoredMIME(t *testing.T) {
	svc := newMockArtifactService()
	toolCtx := newTestToolCtx(svc)
	ctx := context.Background()

	saveTestArtifact(ctx, svc, toolCtx, "download.bin", string(buildTestPDF("hidden")), "application/force-download")

	gt := &GrepTool{}
	result, err := gt.Execute(ctx, map[string]any{"pattern": "hidden", "filename": "download.bin"}, toolCtx)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Message, "application/pdf") {
		t.Errorf("expected the sniffed application/pdf type, got %q", result.Message)
	}
	if !strings.Contains(result.Message, "could not be read") {
		t.Errorf("expected the give-up instruction, got %q", result.Message)
	}
}

func TestGrepMixedTextAndBinary(t *testing.T) {
	svc := newMockArtifactService()
	toolCtx := newTestToolCtx(svc)
	ctx := context.Background()

	saveTestArtifact(ctx, svc, toolCtx, "notes.md", "GDP grew 4 percent\nother line\n", "text/markdown")
	saveTestArtifact(ctx, svc, toolCtx, "chart.png", "\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR GDP", "image/png")

	gt := &GrepTool{}
	result, err := gt.Execute(ctx, map[string]any{"pattern": "GDP"}, toolCtx)
	if err != nil {
		t.Fatal(err)
	}
	if result.Data["count"] != 1 {
		t.Errorf("count = %v, want 1 (only the markdown line)", result.Data["count"])
	}
	matches, _ := result.Data["matches"].(string)
	if !strings.Contains(matches, "notes.md") {
		t.Errorf("matches = %q, want the notes.md hit", matches)
	}
	if !strings.Contains(result.Message, "chart.png") || !strings.Contains(result.Message, "image/png") {
		t.Errorf("message must flag chart.png and its type: %q", result.Message)
	}
	names, _ := result.Data["binary_files"].([]string)
	if len(names) != 1 || names[0] != "chart.png" {
		t.Errorf("data[binary_files] = %v, want [chart.png]", result.Data["binary_files"])
	}
}

func TestGrepTextArtifactsUnchanged(t *testing.T) {
	svc := newMockArtifactService()
	toolCtx := newTestToolCtx(svc)
	ctx := context.Background()

	saveTestArtifact(ctx, svc, toolCtx, "log.txt", "INFO ok\nERROR boom\n", "text/plain")
	saveTestArtifact(ctx, svc, toolCtx, "data.json", "{\n  \"error\": true\n}\n", "application/json")
	saveTestArtifact(ctx, svc, toolCtx, "page.html", "<html><body>ERROR here</body></html>\n", "text/html")
	saveTestArtifact(ctx, svc, toolCtx, "latin1.txt", "caf\xe9 ERROR na\xefve\n", "text/plain")
	saveTestArtifact(ctx, svc, toolCtx, "empty.txt", "", "text/plain")

	gt := &GrepTool{}
	result, err := gt.Execute(ctx, map[string]any{"pattern": "ERROR"}, toolCtx)
	if err != nil {
		t.Fatal(err)
	}
	if result.Data["count"] != 3 {
		t.Errorf("count = %v, want 3 (log.txt, page.html, latin1.txt)", result.Data["count"])
	}
	if _, ok := result.Data["binary_files"]; ok {
		t.Errorf("no text artifact may be classified as binary; got %v", result.Data["binary_files"])
	}
	if strings.Contains(result.Message, "binary") {
		t.Errorf("message must not mention binary for an all-text search: %q", result.Message)
	}

	noMatch, err := gt.Execute(ctx, map[string]any{"pattern": "NOTHINGHERE", "filename": "log.txt"}, toolCtx)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(noMatch.Message, "No matches found") {
		t.Errorf("no-match message = %q", noMatch.Message)
	}
	if noMatch.Data != nil {
		t.Errorf("expected nil data for a text no-match, got %v", noMatch.Data)
	}
}

func TestDetectBinaryMIME(t *testing.T) {
	cases := []struct {
		name       string
		content    string
		storedMIME string
		want       string
	}{
		{"pdf signature", string(buildTestPDF("x")), "application/pdf", "application/pdf"},
		{"pdf signature, lying stored type", string(buildTestPDF("x")), "application/force-download", "application/pdf"},
		{"png signature", "\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR", "image/png", "image/png"},
		{"gzip signature", "\x1f\x8b\x08\x00\x00\x00\x00\x00", "application/gzip", "application/x-gzip"},
		{"nul bytes, unknown format", "abc\x00\x00def", "", "application/octet-stream"},
		{"nul bytes, stored type wins", "abc\x00\x00def", "application/vnd.sqlite3", "application/vnd.sqlite3"},
		{"plain text", "hello world\n", "text/plain", ""},
		{"markdown", "# Title\n\nbody\n", "text/markdown", ""},
		{"json", "{\"a\": 1}", "application/json", ""},
		{"html", "<html><body>hi</body></html>", "text/html", ""},
		{"latin-1 text", "caf\xe9 na\xefve\n", "text/plain", ""},
		{"empty", "", "text/plain", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := detectBinaryMIME([]byte(tc.content), tc.storedMIME); got != tc.want {
				t.Errorf("detectBinaryMIME(...) = %q, want %q", got, tc.want)
			}
		})
	}
}

func buildTestPDF(text string) []byte {
	stream := fmt.Sprintf("BT /F1 24 Tf 72 700 Td (%s) Tj ET\n", text)
	objs := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents 4 0 R /Resources << /Font << /F1 5 0 R >> >> >>",
		fmt.Sprintf("<< /Length %d >>\nstream\n%sendstream", len(stream), stream),
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
	}
	var buf bytes.Buffer
	buf.WriteString("%PDF-1.4\n")
	offsets := make([]int, len(objs))
	for i, body := range objs {
		offsets[i] = buf.Len()
		fmt.Fprintf(&buf, "%d 0 obj\n%s\nendobj\n", i+1, body)
	}
	xrefOffset := buf.Len()
	fmt.Fprintf(&buf, "xref\n0 %d\n", len(objs)+1)
	buf.WriteString("0000000000 65535 f \n")
	for _, off := range offsets {
		fmt.Fprintf(&buf, "%010d 00000 n \n", off)
	}
	fmt.Fprintf(&buf, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objs)+1, xrefOffset)
	return buf.Bytes()
}

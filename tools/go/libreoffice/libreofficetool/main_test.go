// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package libreofficetool

import (
	"context"
	"os"
	"strings"
	"testing"

	sdk "github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"
)

func TestTargetFormatAllowlist(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"pdf", true},
		{"docx", true},
		{"odt", true},
		{"pptx", true},
		{"xlsx", true},
		{"csv", true},
		{"html", true},
		{"txt", true},
		{"epub", true},
		{"csv:Text - txt - csv (StarCalc)", true},
		{"pdf:writer_pdf_Export:SelectPdfVersion=2", true},
		{"csv:Text - txt - csv (StarCalc):44,34,76", true},
		{"txt:Text (encoded):UTF8", true},

		{"", false},
		{"a", false},
		{"VERYLONGEXT", false},
		{"PDF", false},
		{"pdf;rm -rf /", false},
		{"pdf|evil", false},
		{"pdf$(evil)", false},
		{"../etc", false},
		{"pdf:Filter\nwith newline", false},
		{"pdf:`evil`", false},
	}

	for _, c := range cases {
		got := targetFormatPattern.MatchString(c.in)
		if got != c.want {
			t.Errorf("targetFormatPattern %q = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestExecuteConvertWithoutSoffice(t *testing.T) {
	emptyDir := t.TempDir()
	t.Setenv("PATH", emptyDir)

	t.Setenv("LIBREOFFICE_HOME", "")

	p := Params{
		InputDocument: sdk.Artifact{
			Filename: "hello.txt",
			Content:  []byte("hello"),
		},
		TargetFormat: "pdf",
	}
	res, err := executeConvert(context.Background(), p, &sdk.ToolContext{})
	if err != nil {
		t.Fatalf("executeConvert returned unexpected error: %v", err)
	}
	if res == nil {
		t.Fatal("nil result")
	}
	if res.Status != sdk.StatusError {
		t.Errorf("status = %q, want %q", res.Status, sdk.StatusError)
	}
	if !strings.Contains(res.Message, "LibreOffice is required") {
		t.Errorf("message = %q, want it to mention the graceful fallback", res.Message)
	}
}

func TestExecuteConvertValidation(t *testing.T) {
	t.Run("missing input", func(t *testing.T) {
		res, err := executeConvert(context.Background(), Params{TargetFormat: "pdf"}, &sdk.ToolContext{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res.Status != sdk.StatusError || !strings.Contains(res.Message, "input_document") {
			t.Errorf("expected error about input_document, got %+v", res)
		}
	})
	t.Run("missing target_format", func(t *testing.T) {
		res, err := executeConvert(context.Background(), Params{
			InputDocument: sdk.Artifact{Filename: "a.txt", Content: []byte("x")},
		}, &sdk.ToolContext{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res.Status != sdk.StatusError || !strings.Contains(res.Message, "target_format") {
			t.Errorf("expected error about target_format, got %+v", res)
		}
	})
	t.Run("invalid target_format", func(t *testing.T) {
		res, err := executeConvert(context.Background(), Params{
			InputDocument: sdk.Artifact{Filename: "a.txt", Content: []byte("x")},
			TargetFormat:  "pdf;evil",
		}, &sdk.ToolContext{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res.Status != sdk.StatusError || !strings.Contains(res.Message, "invalid target_format") {
			t.Errorf("expected error about invalid target_format, got %+v", res)
		}
	})
}

func TestDetectMIMEType(t *testing.T) {
	cases := map[string]string{
		"doc.pdf":    "application/pdf",
		"doc.docx":   "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
		"sheet.xlsx": "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
		"pres.pptx":  "application/vnd.openxmlformats-officedocument.presentationml.presentation",
		"plain.txt":  "text/plain",
		"noext":      "application/octet-stream",
	}
	for in, want := range cases {
		got := detectMIMEType(in)
		if !strings.HasPrefix(got, want) {
			t.Errorf("detectMIMEType(%q) = %q, want prefix %q", in, got, want)
		}
	}
}

func TestMain(m *testing.M) {
	os.Exit(m.Run())
}

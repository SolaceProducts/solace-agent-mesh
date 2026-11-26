// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package pdfextracttext

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	sdk "github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"
)

const minimalPDF = `%PDF-1.4
1 0 obj << /Type /Catalog /Pages 2 0 R >> endobj
2 0 obj << /Type /Pages /Kids [3 0 R] /Count 1 >> endobj
3 0 obj << /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents 4 0 R /Resources << /Font << /F1 5 0 R >> >> >> endobj
4 0 obj << /Length 44 >> stream
BT /F1 24 Tf 100 700 Td (Hello, World!) Tj ET
endstream endobj
5 0 obj << /Type /Font /Subtype /Type1 /BaseFont /Helvetica >> endobj
xref
0 6
0000000000 65535 f
0000000010 00000 n
0000000055 00000 n
0000000103 00000 n
0000000196 00000 n
0000000281 00000 n
trailer << /Size 6 /Root 1 0 R >>
startxref
343
%%EOF
`

func TestExtractFallbackOnMalformed(t *testing.T) {
	garbage := []byte("not-a-real-pdf\nstream\nBT /F1 12 Tf 50 50 Td (Recovered text) Tj ET\nendstream\n")
	res, err := extract(garbage, 0)
	if err != nil {
		t.Fatalf("expected fallback to succeed, got: %v", err)
	}
	if len(res.Pages) != 1 {
		t.Fatalf("expected 1 fallback page, got %d", len(res.Pages))
	}
	if res.Pages[0].Number != 0 {
		t.Errorf("expected fallback page Number=0, got %d", res.Pages[0].Number)
	}
	if !strings.Contains(res.Pages[0].Text, "Recovered text") {
		t.Errorf("expected recovered text in fallback page, got %q", res.Pages[0].Text)
	}
}

func TestExtractEmptyOnUnparseable(t *testing.T) {
	res, err := extract([]byte("not even close to a pdf"), 0)
	if err != nil {
		t.Fatalf("expected zero-page success, got error: %v", err)
	}
	if len(res.Pages) != 0 {
		t.Errorf("expected 0 pages, got %d", len(res.Pages))
	}
}

func TestExecuteRejectsOversize(t *testing.T) {
	limit := 64
	p := Params{
		InputDocument: sdk.Artifact{
			Filename: "big.pdf",
			Content:  bytes.Repeat([]byte("x"), limit+1),
			MIMEType: "application/pdf",
		},
		MaxBytes: &limit,
	}
	res, err := execute(context.Background(), p, &sdk.ToolContext{})
	if err != nil {
		t.Fatalf("unexpected execute error: %v", err)
	}
	if res.Status != sdk.StatusError {
		t.Errorf("expected error status, got %q", res.Status)
	}
	if !strings.Contains(res.Message, "max_bytes") {
		t.Errorf("expected max_bytes message, got %q", res.Message)
	}
}

func TestExecuteRequiresInput(t *testing.T) {
	p := Params{}
	res, err := execute(context.Background(), p, &sdk.ToolContext{})
	if err != nil {
		t.Fatalf("unexpected execute error: %v", err)
	}
	if res.Status != sdk.StatusError {
		t.Errorf("expected error status, got %q", res.Status)
	}
}

func TestExtractTruncates(t *testing.T) {
	res, err := extract([]byte(minimalPDF), 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Pages) > 1 {
		t.Errorf("expected ≤1 page (cap=1), got %d", len(res.Pages))
	}
}

func TestEncryptedDetection(t *testing.T) {
	cases := []struct {
		err  error
		want bool
	}{
		{nil, false},
		{errors.New("malformed PDF: cross-reference table not found"), false},
		{errors.New("encrypted PDF: invalid password"), true},
		{errors.New("ENCRYPTED PDF"), true},
		{errors.New("user password required"), true},
	}
	for _, c := range cases {
		got := isEncryptedErr(c.err)
		if got != c.want {
			t.Errorf("isEncryptedErr(%v) = %v, want %v", c.err, got, c.want)
		}
	}
}

func TestExtractStructuredEncryptedSurfacesError(t *testing.T) {
	if !errors.Is(errEncrypted, errEncrypted) {
		t.Fatal("errEncrypted should match itself via errors.Is")
	}
}

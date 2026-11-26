// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package translate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseExamplesCSV_Happy(t *testing.T) {
	body := "sequence_number,prompt,expected_response\n" +
		"1,what is 1+1?,2\n" +
		"2,who painted the sistine chapel?,Michelangelo\n" +
		"3,empty expected,\n"
	rows, err := ParseExamplesCSV([]byte(body))
	if err != nil {
		t.Fatalf("ParseExamplesCSV: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(rows))
	}
	if rows[0].SequenceNumber != 1 || rows[0].Prompt != "what is 1+1?" {
		t.Errorf("row 0 = %+v", rows[0])
	}
	if rows[0].ExpectedResponse == nil || *rows[0].ExpectedResponse != "2" {
		t.Errorf("row 0 expected_response = %v", rows[0].ExpectedResponse)
	}
	if rows[2].ExpectedResponse != nil {
		t.Errorf("row 2 expected_response should be nil, got %v", *rows[2].ExpectedResponse)
	}
}

func TestParseExamplesCSV_HeaderOnly(t *testing.T) {
	body := "sequence_number,prompt,expected_response\n"
	rows, err := ParseExamplesCSV([]byte(body))
	if err != nil {
		t.Fatalf("ParseExamplesCSV: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("expected 0 rows, got %d", len(rows))
	}
}

func TestParseExamplesCSV_BlankRowsSkipped(t *testing.T) {
	body := "sequence_number,prompt,expected_response\n" +
		"1,a,a\n" +
		"\n" +
		"2,b,b\n"
	rows, err := ParseExamplesCSV([]byte(body))
	if err != nil {
		t.Fatalf("ParseExamplesCSV: %v", err)
	}
	if len(rows) != 2 {
		t.Errorf("expected 2 rows, got %d", len(rows))
	}
}

func TestParseExamplesCSV_RejectsMissingPrompt(t *testing.T) {
	body := "sequence_number,prompt,expected_response\n1,,a\n"
	if _, err := ParseExamplesCSV([]byte(body)); err == nil || !strings.Contains(err.Error(), "prompt is required") {
		t.Errorf("expected prompt-required error, got %v", err)
	}
}

func TestParseExamplesCSV_RejectsBadSeqNumber(t *testing.T) {
	body := "sequence_number,prompt,expected_response\nabc,x,a\n"
	if _, err := ParseExamplesCSV([]byte(body)); err == nil || !strings.Contains(err.Error(), "sequence_number must be a positive integer") {
		t.Errorf("expected positive-integer error, got %v", err)
	}
}

func TestParseExamplesCSV_RejectsDuplicateSeq(t *testing.T) {
	body := "sequence_number,prompt,expected_response\n1,a,a\n1,b,b\n"
	if _, err := ParseExamplesCSV([]byte(body)); err == nil || !strings.Contains(err.Error(), "duplicate sequence_number 1") {
		t.Errorf("expected duplicate-seq error, got %v", err)
	}
}

func TestParseExamplesCSV_AcceptsCRLF(t *testing.T) {
	body := "sequence_number,prompt,expected_response\r\n1,a,A\r\n2,b,B\r\n"
	rows, err := ParseExamplesCSV([]byte(body))
	if err != nil {
		t.Fatalf("ParseExamplesCSV: %v", err)
	}
	if len(rows) != 2 {
		t.Errorf("expected 2 rows under CRLF, got %d", len(rows))
	}
}

func TestParseExamplesCSV_AcceptsQuotedComma(t *testing.T) {
	body := "sequence_number,prompt,expected_response\n1,\"a, b, c\",x\n"
	rows, err := ParseExamplesCSV([]byte(body))
	if err != nil {
		t.Fatalf("ParseExamplesCSV: %v", err)
	}
	if len(rows) != 1 || rows[0].Prompt != "a, b, c" {
		t.Errorf("quoted-comma prompt not preserved: %+v", rows)
	}
}

func TestParseExamplesCSV_RejectsUnterminatedQuote(t *testing.T) {
	body := "sequence_number,prompt,expected_response\n1,\"a,X\n"
	if _, err := ParseExamplesCSV([]byte(body)); err == nil {
		t.Errorf("expected error for unterminated quote")
	}
}

func TestParseExamplesCSV_RejectsZeroSeqNumber(t *testing.T) {
	body := "sequence_number,prompt,expected_response\n0,x,a\n"
	if _, err := ParseExamplesCSV([]byte(body)); err == nil || !strings.Contains(err.Error(), "positive integer") {
		t.Errorf("expected positive-integer error, got %v", err)
	}
}

func TestParseExamplesCSV_RejectsMissingHeader(t *testing.T) {
	if _, err := ParseExamplesCSV(nil); err == nil {
		t.Error("expected error for empty body")
	}
	body := "expected_response\nx\n"
	if _, err := ParseExamplesCSV([]byte(body)); err == nil || !strings.Contains(err.Error(), `missing required column "prompt"`) {
		t.Errorf("expected missing-prompt error, got %v", err)
	}
	body = "prompt,expected_response\nx,a\n"
	if _, err := ParseExamplesCSV([]byte(body)); err == nil || !strings.Contains(err.Error(), `missing required column "sequence_number"`) {
		t.Errorf("expected missing-sequence_number error, got %v", err)
	}
}

func TestEmitExamplesCSV_RoundTrip(t *testing.T) {
	expected := "yes"
	rows := []ExampleSpec{
		{SequenceNumber: 1, Prompt: "a", ExpectedResponse: &expected},
		{SequenceNumber: 2, Prompt: "b,c\nd", ExpectedResponse: nil},
	}
	body, err := EmitExamplesCSV(rows)
	if err != nil {
		t.Fatalf("EmitExamplesCSV: %v", err)
	}
	parsed, err := ParseExamplesCSV(body)
	if err != nil {
		t.Fatalf("re-parse: %v", err)
	}
	if len(parsed) != 2 {
		t.Fatalf("got %d rows", len(parsed))
	}
	if parsed[1].Prompt != "b,c\nd" {
		t.Errorf("CSV did not preserve embedded comma+newline: %q", parsed[1].Prompt)
	}
	if parsed[1].ExpectedResponse != nil {
		t.Errorf("nil expected lost in round trip")
	}
}

func TestResolveExamplesFile_Tristate(t *testing.T) {
	dir := t.TempDir()
	csvPath := filepath.Join(dir, "examples.csv")
	if err := os.WriteFile(csvPath, []byte("sequence_number,prompt,expected_response\n1,foo,bar\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	yamlPath := filepath.Join(dir, "ds.yaml")

	t.Run("absent", func(t *testing.T) {
		spec := map[string]any{"description": "x"}
		rows, present, err := ResolveExamplesFile(spec, yamlPath)
		if err != nil {
			t.Fatal(err)
		}
		if present || rows != nil {
			t.Errorf("absent → present=false, rows=nil; got present=%v rows=%v", present, rows)
		}
		if _, still := spec["examples_file"]; still {
			t.Error("absent path should not pop examples_file (it wasn't there)")
		}
	})

	t.Run("present-with-rows", func(t *testing.T) {
		spec := map[string]any{"examples_file": "examples.csv"}
		rows, present, err := ResolveExamplesFile(spec, yamlPath)
		if err != nil {
			t.Fatalf("ResolveExamplesFile: %v", err)
		}
		if !present || len(rows) != 1 {
			t.Errorf("got present=%v rows=%d", present, len(rows))
		}
		if _, still := spec["examples_file"]; still {
			t.Error("examples_file should be popped from spec")
		}
	})

	t.Run("empty-file", func(t *testing.T) {
		empty := filepath.Join(dir, "empty.csv")
		if err := os.WriteFile(empty, []byte("sequence_number,prompt,expected_response\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		spec := map[string]any{"examples_file": "empty.csv"}
		rows, present, err := ResolveExamplesFile(spec, yamlPath)
		if err != nil {
			t.Fatal(err)
		}
		if !present || len(rows) != 0 {
			t.Errorf("expected present=true rows=0, got present=%v rows=%d", present, len(rows))
		}
	})

	t.Run("missing-file", func(t *testing.T) {
		spec := map[string]any{"examples_file": "nonexistent.csv"}
		_, _, err := ResolveExamplesFile(spec, yamlPath)
		if err == nil || !strings.Contains(err.Error(), "read examples_file") {
			t.Errorf("expected read error, got %v", err)
		}
	})

	t.Run("non-string-value", func(t *testing.T) {
		spec := map[string]any{"examples_file": 42}
		_, _, err := ResolveExamplesFile(spec, yamlPath)
		if err == nil || !strings.Contains(err.Error(), "must be a string") {
			t.Errorf("expected type error, got %v", err)
		}
	})
}

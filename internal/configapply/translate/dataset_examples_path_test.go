// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package translate

import (
	"strings"
	"testing"
)

func TestExamplesCSV_ExpectedPathRoundTrips(t *testing.T) {
	csv := "sequence_number,prompt,expected_response,expected_path\n" +
		`1,refund O-1,,"{""nodes"":{""approve"":""ran""},""tools"":{""issue_refund"":""called""}}"` + "\n" +
		"2,status of O-2,,\n"

	rows, err := ParseExamplesCSV([]byte(csv))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if rows[0].ExpectedPath == nil || rows[0].ExpectedPath.Nodes["approve"] != "ran" || rows[0].ExpectedPath.Tools["issue_refund"] != "called" {
		t.Errorf("row 1 path = %+v", rows[0].ExpectedPath)
	}
	if rows[1].ExpectedPath != nil {
		t.Errorf("a blank cell parsed to %+v", rows[1].ExpectedPath)
	}

	emitted, err := EmitExamplesCSV(rows)
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	again, err := ParseExamplesCSV(emitted)
	if err != nil || again[0].ExpectedPath == nil || again[0].ExpectedPath.Nodes["approve"] != "ran" {
		t.Errorf("re-parse of %q = %+v, %v", emitted, again, err)
	}
}

func TestParseExamplesCSV_ExpectedPathErrors(t *testing.T) {
	for name, cell := range map[string]string{
		"an unknown expectation": `"{""nodes"":{""approve"":""done""}}"`,
		"a misspelt key":         `"{""node"":{""approve"":""ran""}}"`,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := ParseExamplesCSV([]byte("sequence_number,prompt,expected_path\n1,x," + cell + "\n"))
			if err == nil || !strings.Contains(err.Error(), "row 2") {
				t.Errorf("err = %v, want a row 2 error", err)
			}
		})
	}
}

func TestEmitExamplesCSV_NoPathColumnWithoutPaths(t *testing.T) {
	emitted, err := EmitExamplesCSV([]ExampleSpec{{SequenceNumber: 1, Prompt: "hi"}})
	if err != nil || strings.Contains(string(emitted), "expected_path") {
		t.Errorf("emitted %q, %v; want no expected_path column", emitted, err)
	}
}

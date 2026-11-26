// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package translate

import (
	"strings"
	"testing"

	evalplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/eval"
)

func TestExamplesCSV_ExpectedFactsRoundTrips(t *testing.T) {
	csv := "sequence_number,prompt,expected_response,expected_path,expected_facts\n" +
		`1,refund O-1,,"{""nodes"":{""approve"":""ran""}}","{""required"":[""O-1""],""forbidden"":[""escalated""]}"` + "\n" +
		"2,status of O-2,,,\n" +
		"3,status of O-3,,,null\n"

	rows, err := ParseExamplesCSV([]byte(csv))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	want := &evalplatform.ExpectedFacts{Required: []string{"O-1"}, Forbidden: []string{"escalated"}}
	if !evalplatform.SameExpectedFacts(rows[0].ExpectedFacts, want) || rows[1].ExpectedFacts != nil || rows[2].ExpectedFacts != nil {
		t.Errorf("facts = %+v, %+v, %+v; want %+v then none twice", rows[0].ExpectedFacts, rows[1].ExpectedFacts, rows[2].ExpectedFacts, want)
	}

	emitted, err := EmitExamplesCSV(rows)
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	if header, _, _ := strings.Cut(string(emitted), "\n"); header != "sequence_number,prompt,expected_response,expected_path,expected_facts" {
		t.Errorf("header = %q", header)
	}
	again, err := ParseExamplesCSV(emitted)
	if err != nil || !evalplatform.SameExpectedFacts(again[0].ExpectedFacts, want) || again[1].ExpectedFacts != nil {
		t.Errorf("re-parse of %q = %+v, %v", emitted, again, err)
	}

	onlyFacts, err := EmitExamplesCSV([]ExampleSpec{{SequenceNumber: 1, Prompt: "p", ExpectedFacts: want}})
	if header, _, _ := strings.Cut(string(onlyFacts), "\n"); err != nil || header != "sequence_number,prompt,expected_response,expected_facts" {
		t.Errorf("facts-only header = %q, %v; want only the facts column added", header, err)
	}
}

func TestParseExamplesCSV_ExpectedFactsErrors(t *testing.T) {
	for name, cell := range map[string]string{
		"a blank value":      `"{""required"":["" ""]}"`,
		"a value twice":      `"{""required"":[""a"",""A""]}"`,
		"a misspelt key":     `"{""require"":[""a""]}"`,
		"not a JSON object":  `refund`,
		"no values":          `"{}"`,
		"data after the end": `"{""required"":[""a""]}}"`,
	} {
		t.Run(name, func(t *testing.T) {
			csv := "sequence_number,prompt,expected_facts\n1,p," + cell + "\n"
			if _, err := ParseExamplesCSV([]byte(csv)); err == nil || !strings.Contains(err.Error(), "row 2") {
				t.Errorf("err = %v, want one naming row 2", err)
			}
		})
	}
}

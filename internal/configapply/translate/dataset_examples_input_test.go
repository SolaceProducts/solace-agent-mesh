// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package translate

import (
	"strings"
	"testing"
)

func TestParseExamplesCSV_InputData(t *testing.T) {
	body := "sequence_number,prompt,expected_response,input_data\n" +
		`1,,urgent,"{""order_id"": ""O-5003"", ""id"": 9007199254740993, ""total"": 1.0, ""note"": ""<a&b>""}"` + "\n" +
		"2,plain prompt,,\n"
	const canonical = `{"id":9007199254740993,"note":"<a&b>","order_id":"O-5003","total":1.0}`

	rows, err := ParseExamplesCSV([]byte(body))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(rows) != 2 || string(rows[0].InputData) != canonical || rows[0].Prompt != "" {
		t.Fatalf("rows = %+v, want the input-only row parsed to %s", rows, canonical)
	}
	if rows[1].InputData != nil {
		t.Errorf("a blank input_data cell = %s, want none", rows[1].InputData)
	}

	emitted, err := EmitExamplesCSV(rows)
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	if !strings.Contains(string(emitted), `"{""id"":9007199254740993,""note"":""<a&b>"",""order_id"":""O-5003"",""total"":1.0}"`) {
		t.Errorf("emitted CSV lost the exact input:\n%s", emitted)
	}
	again, err := ParseExamplesCSV(emitted)
	if err != nil || len(again) != 2 || string(again[0].InputData) != canonical || again[1].InputData != nil {
		t.Fatalf("round trip = %+v, %v\n%s", again, err, emitted)
	}
}

func TestParseExamplesCSV_InputDataErrors(t *testing.T) {
	cases := map[string]string{
		"not a JSON object":         "sequence_number,prompt,input_data\n1,p,[1]\n",
		"a null cell":               "sequence_number,prompt,input_data\n1,p,null\n",
		"neither prompt nor input":  "sequence_number,prompt,input_data\n1,,\n",
		"no prompt or input column": "sequence_number,expected_response\n1,x\n",
		"an input over the limit":   "sequence_number,input_data\n1,\"{\"\"b\"\":\"\"" + strings.Repeat("x", 256*1024) + "\"\"}\"\n",
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseExamplesCSV([]byte(body)); err == nil {
				t.Errorf("parsed %.80q", body)
			}
		})
	}
	if _, err := ParseExamplesCSV([]byte("sequence_number,input_data\n" + `1,"{""a"":1}"` + "\n")); err != nil {
		t.Errorf("an input-only CSV with no prompt column: %v", err)
	}
}

func TestEmitExamplesCSV_NoInputColumnWithoutInput(t *testing.T) {
	out, err := EmitExamplesCSV([]ExampleSpec{{SequenceNumber: 1, Prompt: "p"}})
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "sequence_number,prompt,expected_response\n1,p,\n" {
		t.Errorf("an agent dataset's pull changed:\n%s", out)
	}
}

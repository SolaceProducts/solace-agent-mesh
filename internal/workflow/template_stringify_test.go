// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import (
	"encoding/json"
	"testing"
)

func decodedOutputs(t *testing.T, raw string) map[string]map[string]any {
	t.Helper()
	var decoded map[string]any
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
		t.Fatal(err)
	}
	return map[string]map[string]any{"n": {"output": decoded}}
}

func TestInterpolatedNumbersAreNotScientificNotation(t *testing.T) {
	outputs := decodedOutputs(t, `{
		"total": 1000000,
		"big": 12345678901234,
		"justUnder": 999999,
		"small": 42,
		"ratio": 0.95,
		"neg": -2500000,
		"frac": 1234567.5
	}`)

	cases := []struct {
		name string
		in   string
		want string
	}{
		{"one million", "?limit={{n.output.total}}", "?limit=1000000"},
		{"large integer", "WHERE id > {{n.output.big}}", "WHERE id > 12345678901234"},
		{"just under the old threshold", "x={{n.output.justUnder}}", "x=999999"},
		{"small integer", "x={{n.output.small}}", "x=42"},
		{"fraction is preserved", "x={{n.output.ratio}}", "x=0.95"},
		{"negative", "x={{n.output.neg}}", "x=-2500000"},
		{"non-integer float keeps its fraction", "x={{n.output.frac}}", "x=1234567.5"},
		{"filename", "report_{{n.output.total}}.csv", "report_1000000.csv"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ResolveStringWithTemplates(tc.in, outputs); got != tc.want {
				t.Errorf("ResolveStringWithTemplates(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestInterpolatedContainersAreJSON(t *testing.T) {
	outputs := decodedOutputs(t, `{"arr": [1, 2, 3], "obj": {"a": 1}, "strs": ["x", "y"]}`)

	cases := []struct{ name, in, want string }{
		{"number array", "v={{n.output.arr}}", `v=[1,2,3]`},
		{"object", "v={{n.output.obj}}", `v={"a":1}`},
		{"string array", "v={{n.output.strs}}", `v=["x","y"]`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ResolveStringWithTemplates(tc.in, outputs); got != tc.want {
				t.Errorf("ResolveStringWithTemplates(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestInterpolatedContainersAreNotHTMLEscaped(t *testing.T) {
	outputs := decodedOutputs(t, `{
		"filters": {"q": "shoes & socks"},
		"tags": ["a&b", "<x>"],
		"cmp": {"expr": "a<b>c"}
	}`)

	cases := []struct{ name, in, want string }{
		{"ampersand in object", "https://example.com/s?f={{n.output.filters}}", `https://example.com/s?f={"q":"shoes & socks"}`},
		{"angle brackets in array", "v={{n.output.tags}}", `v=["a&b","<x>"]`},
		{"both in nested object", "v={{n.output.cmp}}", `v={"expr":"a<b>c"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ResolveStringWithTemplates(tc.in, outputs); got != tc.want {
				t.Errorf("ResolveStringWithTemplates(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestConcatUsesTheSameStringification(t *testing.T) {
	outputs := decodedOutputs(t, `{"total": 1000000, "arr": [1,2]}`)

	got, err := resolveConcat([]any{"n=", "{{n.output.total}}", ";a=", "{{n.output.arr}}"}, outputs, nil, nodeInputMode)
	if err != nil {
		t.Fatal(err)
	}
	const want = `n=1000000;a=[1,2]`
	if got != want {
		t.Errorf("resolveConcat = %q, want %q", got, want)
	}
}

func TestWholeStringAndEmbeddedAgreeOnValue(t *testing.T) {
	outputs := decodedOutputs(t, `{"total": 1000000}`)

	whole, err := ResolveValue("{{n.output.total}}", outputs)
	if err != nil {
		t.Fatal(err)
	}
	marshaled, err := json.Marshal(whole)
	if err != nil {
		t.Fatal(err)
	}
	embedded, err := ResolveValue("{{n.output.total}} ", outputs)
	if err != nil {
		t.Fatal(err)
	}
	embeddedStr, _ := embedded.(string)

	if got := string(marshaled); got != "1000000" {
		t.Errorf("whole-string form marshaled to %q, want \"1000000\"", got)
	}
	if embeddedStr != "1000000 " {
		t.Errorf("embedded form = %q, want %q — the two forms disagree on the same reference", embeddedStr, "1000000 ")
	}
}

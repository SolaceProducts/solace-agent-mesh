// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package testharness

import (
	"reflect"
	"testing"
)

func TestExtractJSON_VariousShapes(t *testing.T) {
	t.Parallel()
	type plan struct {
		SampleInput    string `json:"sampleInput"`
		ExpectedOutput string `json:"expectedOutput"`
	}
	want := plan{SampleInput: "hello", ExpectedOutput: "world"}

	cases := []struct {
		name string
		body string
	}{
		{"bare", `{"sampleInput":"hello","expectedOutput":"world"}`},
		{"fenced_json", "```json\n{\"sampleInput\":\"hello\",\"expectedOutput\":\"world\"}\n```"},
		{"fenced_no_lang", "```\n{\"sampleInput\":\"hello\",\"expectedOutput\":\"world\"}\n```"},
		{"prose_then_json", `Here's the plan you asked for: {"sampleInput":"hello","expectedOutput":"world"}`},
		{"prose_around_json", "I'll give you a plan.\n\n```\n{\"sampleInput\":\"hello\",\"expectedOutput\":\"world\"}\n```\n\nHope that helps."},
		{"trailing_prose", `{"sampleInput":"hello","expectedOutput":"world"} — let me know if you'd like another.`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var got plan
			if _, err := extractJSON(tc.body, &got); err != nil {
				t.Fatalf("extractJSON: %v\nbody: %s", err, tc.body)
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("got %+v, want %+v", got, want)
			}
		})
	}
}

func TestExtractJSON_HandlesQuotedBraces(t *testing.T) {
	t.Parallel()
	type plan struct {
		SampleInput string `json:"sampleInput"`
	}
	body := `{"sampleInput":"a }} string"}`
	var got plan
	if _, err := extractJSON(body, &got); err != nil {
		t.Fatalf("extractJSON: %v", err)
	}
	if got.SampleInput != "a }} string" {
		t.Errorf("got %q, want %q", got.SampleInput, "a }} string")
	}
}

func TestExtractJSON_NoJSON(t *testing.T) {
	t.Parallel()
	var dst map[string]any
	if _, err := extractJSON("just some prose, no json at all", &dst); err == nil {
		t.Error("expected error when no JSON is present")
	}
}

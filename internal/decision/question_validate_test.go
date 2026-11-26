// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package decision

import (
	"strings"
	"testing"
)

func TestQuestionValidate(t *testing.T) {
	two := []Criterion{{Key: "yes"}, {Key: "no"}}
	levels := func(n int) []Criterion {
		out := make([]Criterion, n)
		for i := range out {
			out[i] = Criterion{Key: string(rune('a' + i))}
		}
		return out
	}
	cases := map[string]struct {
		q       Question
		wantErr string
	}{
		"a choice question with two options is valid": {q: Question{Type: TypeChoice, Instructions: "Pick", Criteria: two}},
		"a boolean question may omit its criteria":    {q: Question{Type: TypeBoolean, Instructions: "Is it?"}},
		"a boolean question may describe both sides": {q: Question{Type: TypeBoolean, Instructions: "Is it?",
			Criteria: []Criterion{{Key: "true", Description: "yes"}, {Key: "false", Description: "no"}}}},
		"a score question takes ten levels": {q: Question{Type: TypeScore, Instructions: "Rate", Criteria: levels(10)}},

		"instructions are required":  {q: Question{Type: TypeChoice, Instructions: "  ", Criteria: two}, wantErr: "'instructions' is required"},
		"an unknown type is refused": {q: Question{Type: "rank", Instructions: "x", Criteria: two}, wantErr: "'type' must be one of"},
		"a choice question needs two options": {q: Question{Type: TypeChoice, Instructions: "x", Criteria: two[:1]},
			wantErr: "needs at least two"},
		"a score question takes at most ten levels": {q: Question{Type: TypeScore, Instructions: "x", Criteria: levels(11)},
			wantErr: "at most 10 levels"},
		"option keys are unique": {q: Question{Type: TypeChoice, Instructions: "x", Criteria: []Criterion{{Key: "a"}, {Key: "a"}}},
			wantErr: "repeats option"},
		"option keys are unique after trimming": {q: Question{Type: TypeChoice, Instructions: "x", Criteria: []Criterion{{Key: "a"}, {Key: " a"}}},
			wantErr: "repeats option"},
		"option keys are not blank": {q: Question{Type: TypeChoice, Instructions: "x", Criteria: []Criterion{{Key: "a"}, {Key: " "}}},
			wantErr: "needs an option key"},
		"a boolean question describes only true and false": {q: Question{Type: TypeBoolean, Instructions: "x",
			Criteria: []Criterion{{Key: "maybe"}}}, wantErr: "may only describe"},
		"a boolean question describes both sides or neither": {q: Question{Type: TypeBoolean, Instructions: "x",
			Criteria: []Criterion{{Key: "true", Description: "yes"}, {Key: "false"}}}, wantErr: "must describe both"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			err := tc.q.Validate()
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate() = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("Validate() = %v, want an error containing %q", err, tc.wantErr)
			}
		})
	}
}

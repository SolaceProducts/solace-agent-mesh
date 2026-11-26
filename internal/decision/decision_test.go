// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package decision

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestAnswerOutput(t *testing.T) {
	confidence := 0.8
	cases := map[string]struct {
		answer Answer
		want   map[string]any
	}{
		"boolean above half is true": {
			answer: Answer{Type: TypeBoolean, Probability: 0.7},
			want:   map[string]any{"type": "boolean", "value": true, "probability": 0.7},
		},
		"boolean below half is false": {
			answer: Answer{Type: TypeBoolean, Probability: 0.2},
			want:   map[string]any{"type": "boolean", "value": false, "probability": 0.2},
		},
		"choice without confidence emits null": {
			answer: Answer{Type: TypeChoice, Choice: "a", Probabilities: map[string]float64{"a": 1}},
			want:   map[string]any{"type": "choice", "choice": "a", "probabilities": map[string]any{"a": 1.0}, "confidence": nil},
		},
		"score": {
			answer: Answer{Type: TypeScore, Score: 0.5, Confidence: &confidence},
			want:   map[string]any{"type": "score", "score": 0.5, "legend": map[string]any(nil), "probabilities": map[string]any{}, "confidence": 0.8},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got := tc.answer.Output()
			for k, want := range tc.want {
				if !equalValue(got[k], want) {
					t.Errorf("%s = %#v, want %#v", k, got[k], want)
				}
			}
			if len(got) != len(tc.want) {
				t.Errorf("output has keys %v, want exactly %v", got, tc.want)
			}
		})
	}
}

func equalValue(a, b any) bool {
	am, aok := a.(map[string]any)
	bm, bok := b.(map[string]any)
	if aok || bok {
		if len(am) != len(bm) {
			return false
		}
		for k, v := range am {
			if bm[k] != v {
				return false
			}
		}
		return true
	}
	return a == b
}

func TestResultValidate(t *testing.T) {
	questions := []Question{
		{Key: "dept", Type: TypeChoice, Criteria: []Criterion{{Key: "billing"}, {Key: "tech"}}},
		{Key: "urgent", Type: TypeBoolean},
	}
	valid := Result{Answers: map[string]Answer{
		"dept":   {Type: TypeChoice, Choice: "tech"},
		"urgent": {Type: TypeBoolean, Probability: 0.1},
	}}
	if err := valid.Validate(questions); err != nil {
		t.Fatalf("valid result rejected: %v", err)
	}

	cases := map[string]struct {
		answers map[string]Answer
		wantErr string
	}{
		"missing answer": {
			answers: map[string]Answer{"dept": {Type: TypeChoice, Choice: "tech"}},
			wantErr: `no answer for question "urgent"`,
		},
		"wrong type": {
			answers: map[string]Answer{"dept": {Type: TypeScore}, "urgent": {Type: TypeBoolean}},
			wantErr: `answered question "dept" as "score"`,
		},
		"choice outside criteria": {
			answers: map[string]Answer{"dept": {Type: TypeChoice, Choice: "sales"}, "urgent": {Type: TypeBoolean}},
			wantErr: `chose "sales"`,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			err := Result{Answers: tc.answers}.Validate(questions)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want it to mention %q", err, tc.wantErr)
			}
			if got := Classify(err); got != FailureInvalidResponse {
				t.Errorf("Classify = %s, want %s", got, FailureInvalidResponse)
			}
		})
	}
}

func TestResultValidateRejectsOutOfRangeValues(t *testing.T) {
	questions := []Question{
		{Key: "dept", Type: TypeChoice, Criteria: []Criterion{{Key: "billing"}, {Key: "tech"}}},
		{Key: "urgent", Type: TypeBoolean},
		{Key: "severity", Type: TypeScore, Criteria: []Criterion{{Key: "low"}, {Key: "medium"}, {Key: "high"}}},
	}
	confidence := func(v float64) *float64 { return &v }
	answers := func(override map[string]Answer) map[string]Answer {
		base := map[string]Answer{
			"dept":     {Type: TypeChoice, Choice: "tech", Probabilities: map[string]float64{"billing": 0.2, "tech": 0.8}, Confidence: confidence(0.6)},
			"urgent":   {Type: TypeBoolean, Probability: 1},
			"severity": {Type: TypeScore, Score: 1.5, Probabilities: map[string]float64{"low": 0, "medium": 0.5, "high": 0.5}, Confidence: confidence(0)},
		}
		for k, v := range override {
			base[k] = v
		}
		return base
	}
	if err := (Result{Answers: answers(nil)}).Validate(questions); err != nil {
		t.Fatalf("in-range boundary values rejected: %v", err)
	}

	cases := map[string]struct {
		override map[string]Answer
		wantErr  string
	}{
		"confidence 7": {
			override: map[string]Answer{"dept": {Type: TypeChoice, Choice: "tech", Confidence: confidence(7)}},
			wantErr:  `confidence 7 for question "dept"`,
		},
		"negative confidence": {
			override: map[string]Answer{"severity": {Type: TypeScore, Score: 1, Confidence: confidence(-0.1)}},
			wantErr:  `confidence -0.1 for question "severity"`,
		},
		"noul 42": {
			override: map[string]Answer{"urgent": {Type: TypeBoolean, Probability: 42}},
			wantErr:  `probability 42 for question "urgent"`,
		},
		"option probability above one": {
			override: map[string]Answer{"dept": {Type: TypeChoice, Choice: "tech", Probabilities: map[string]float64{"tech": 1.5}}},
			wantErr:  `probability 1.5 for option "tech"`,
		},
		"negative score": {
			override: map[string]Answer{"severity": {Type: TypeScore, Score: -3}},
			wantErr:  `score -3 for question "severity"`,
		},
		"score above the top level": {
			override: map[string]Answer{"severity": {Type: TypeScore, Score: 2.5}},
			wantErr:  `score 2.5 for question "severity", outside its levels [0,2]`,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			err := Result{Answers: answers(tc.override)}.Validate(questions)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want it to mention %q", err, tc.wantErr)
			}
			if got := Classify(err); got != FailureInvalidResponse {
				t.Errorf("Classify = %s, want %s", got, FailureInvalidResponse)
			}
		})
	}
}

func TestResultValidateBoundsProviderStrings(t *testing.T) {
	questions := []Question{{Key: "dept", Type: TypeChoice, Criteria: []Criterion{{Key: "billing"}}}}
	huge := strings.Repeat("x", 500_000)

	cases := map[string]map[string]Answer{
		"choice":       {"dept": {Type: TypeChoice, Choice: huge}},
		"answer type":  {"dept": {Type: QuestionType(huge)}},
		"option key":   {"dept": {Type: TypeChoice, Choice: "billing", Probabilities: map[string]float64{huge: 2}}},
		"answered key": {huge: {Type: TypeChoice}},
	}
	for name, answers := range cases {
		t.Run(name, func(t *testing.T) {
			err := Result{Answers: answers}.Validate(questions)
			if err == nil {
				t.Fatal("Validate accepted the answer")
			}
			if n := len(err.Error()); n >= 1024 {
				t.Errorf("error is %d bytes, want well under 1 KB", n)
			}
			if !strings.Contains(err.Error(), "(500000 bytes)") {
				t.Errorf("error should report the original length: %v", err)
			}
		})
	}
}

func TestResultValidateCapsTheAnsweredKeyList(t *testing.T) {
	answers := make(map[string]Answer)
	for i := range 40 {
		answers[fmt.Sprintf("key%02d", i)] = Answer{Type: TypeBoolean}
	}
	err := Result{Answers: answers}.Validate([]Question{{Key: "missing", Type: TypeBoolean}})
	if err == nil {
		t.Fatal("Validate accepted a result without the asked question")
	}
	msg := err.Error()
	if !strings.Contains(msg, `["key00" "key01" "key02" "key03" "key04"] and 35 more`) {
		t.Errorf("answered list not capped: %v", msg)
	}
	if strings.Contains(msg, "key05") {
		t.Errorf("answered list lists more keys than the cap: %v", msg)
	}
}

func TestBoundedQuoteKeepsUTF8Whole(t *testing.T) {
	got := BoundedQuote(strings.Repeat("a", 63) + "日本")
	if !strings.HasPrefix(got, `"`+strings.Repeat("a", 63)+`"...`) {
		t.Errorf("BoundedQuote split a rune or kept too much: %s", got)
	}
	if short := BoundedQuote("tech"); short != `"tech"` {
		t.Errorf("BoundedQuote(short) = %s", short)
	}
}

func TestBoundedStringCutsOnARuneBoundary(t *testing.T) {
	cases := map[string]struct{ in, want string }{
		"short string is unchanged":        {in: "jev-1.13.0", want: "jev-1.13.0"},
		"exactly the budget is unchanged":  {in: strings.Repeat("m", 64), want: strings.Repeat("m", 64)},
		"long string is cut to the budget": {in: strings.Repeat("m", 200), want: strings.Repeat("m", 64)},
		"a rune straddling the cut is dropped whole": {
			in: strings.Repeat("a", 63) + "日本", want: strings.Repeat("a", 63),
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := BoundedString(tc.in); got != tc.want {
				t.Errorf("BoundedString = %q (%d bytes), want %q", got, len(got), tc.want)
			}
		})
	}
}

func TestClassify(t *testing.T) {
	tagged := &Error{Kind: FailureRateLimited, Err: errors.New("429")}
	cases := map[string]struct {
		err  error
		want FailureKind
	}{
		"tagged":               {err: tagged, want: FailureRateLimited},
		"wrapped tagged":       {err: fmt.Errorf("node x: %w", tagged), want: FailureRateLimited},
		"outermost tag wins":   {err: &Error{Kind: FailureConfig, Err: tagged}, want: FailureConfig},
		"tag beats a deadline": {err: &Error{Kind: FailureUnauthorized, Err: context.DeadlineExceeded}, want: FailureUnauthorized},
		"untagged deadline":    {err: fmt.Errorf("call: %w", context.DeadlineExceeded), want: FailureTimeout},
		"untagged other":       {err: errors.New("boom"), want: FailureUnreachable},
		"untagged cancel":      {err: context.Canceled, want: FailureUnreachable},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := Classify(tc.err); got != tc.want {
				t.Errorf("Classify = %s, want %s", got, tc.want)
			}
		})
	}
}

func TestErrorUnwrapsToTheCause(t *testing.T) {
	cause := errors.New("cause")
	err := error(&Error{Kind: FailureUnreachable, Err: fmt.Errorf("typesafe: %w", cause)})
	if !errors.Is(err, cause) || err.Error() != "typesafe: cause" {
		t.Errorf("err = %q, want it to read and unwrap as its cause", err)
	}
}

func TestFailureKindPermanent(t *testing.T) {
	want := map[FailureKind]bool{
		FailureUnauthorized:    true,
		FailureConfig:          true,
		FailureRateLimited:     false,
		FailureTimeout:         false,
		FailureUnreachable:     false,
		FailureInvalidResponse: false,
		FailureInputTooLarge:   false,
		FailureRefused:         false,
	}
	for kind, permanent := range want {
		if got := kind.Permanent(); got != permanent {
			t.Errorf("%s.Permanent() = %v, want %v", kind, got, permanent)
		}
	}
}

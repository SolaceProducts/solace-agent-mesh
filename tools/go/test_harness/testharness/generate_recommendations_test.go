// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package testharness

import (
	"strings"
	"testing"
)

func TestParseAndSanitizeRecommendations_DropsEmptyEntries(t *testing.T) {
	t.Parallel()
	body := `{
		"recommendations": [
			{"area":"system_prompt","finding":"vague","suggestion":"clarify role","priority":"high"},
			{"area":"tools","finding":"","suggestion":"replace tool","priority":"medium"},
			{"area":"tools","finding":"missing tool","suggestion":"","priority":"low"},
			{"area":"system_prompt","finding":"  ","suggestion":"  ","priority":"low"}
		]
	}`
	_, recs, err := parseAndSanitizeRecommendations(body)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(recs) != 1 {
		t.Fatalf("got %d recommendations, want 1 (others have empty finding/suggestion)", len(recs))
	}
	if recs[0].Finding != "vague" {
		t.Errorf("kept the wrong row: %+v", recs[0])
	}
}

func TestParseAndSanitizeRecommendations_NormalisesArea(t *testing.T) {
	t.Parallel()
	body := `{
		"recommendations": [
			{"area":"  System_Prompt  ","finding":"f1","suggestion":"s1","priority":"medium"},
			{"area":"unknown_thing","finding":"f2","suggestion":"s2","priority":"medium"},
			{"area":"WORKFLOW_LOGIC","finding":"f3","suggestion":"s3","priority":"medium"},
			{"area":"","finding":"f4","suggestion":"s4","priority":"medium"}
		]
	}`
	_, recs, err := parseAndSanitizeRecommendations(body)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	want := []string{"system_prompt", "other", "workflow_logic", "other"}
	if len(recs) != len(want) {
		t.Fatalf("got %d, want %d recs", len(recs), len(want))
	}
	for i, w := range want {
		if recs[i].Area != w {
			t.Errorf("recs[%d].Area = %q, want %q", i, recs[i].Area, w)
		}
	}
}

func TestParseAndSanitizeRecommendations_NormalisesPriority(t *testing.T) {
	t.Parallel()
	body := `{
		"recommendations": [
			{"area":"tools","finding":"f1","suggestion":"s1","priority":"  HIGH  "},
			{"area":"tools","finding":"f2","suggestion":"s2","priority":"critical"},
			{"area":"tools","finding":"f3","suggestion":"s3","priority":""},
			{"area":"tools","finding":"f4","suggestion":"s4","priority":"low"}
		]
	}`
	_, recs, err := parseAndSanitizeRecommendations(body)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	want := []string{"high", "medium", "medium", "low"}
	for i, w := range want {
		if recs[i].Priority != w {
			t.Errorf("recs[%d].Priority = %q, want %q", i, recs[i].Priority, w)
		}
	}
}

func TestParseAndSanitizeRecommendations_HandlesFencedJSON(t *testing.T) {
	t.Parallel()
	body := "Here are the recommendations:\n```json\n{\"recommendations\": [{\"area\":\"tools\",\"finding\":\"f\",\"suggestion\":\"s\",\"priority\":\"high\"}]}\n```"
	_, recs, err := parseAndSanitizeRecommendations(body)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(recs) != 1 || recs[0].Finding != "f" {
		t.Errorf("got %+v, want exactly one recommendation with finding=f", recs)
	}
}

func TestParseAndSanitizeRecommendations_EmptyArrayIsValid(t *testing.T) {
	t.Parallel()
	_, recs, err := parseAndSanitizeRecommendations(`{"recommendations": []}`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(recs) != 0 {
		t.Errorf("got %d recs, want 0", len(recs))
	}
}

func TestParseAndSanitizeRecommendations_BadJSONReturnsError(t *testing.T) {
	t.Parallel()
	_, _, err := parseAndSanitizeRecommendations("not json at all")
	if err == nil {
		t.Error("expected error for non-JSON response")
	}
}

func TestParseAndSanitizeRecommendations_ExtractsAndTrimsSummary(t *testing.T) {
	t.Parallel()
	body := `{
		"summary": "  The agent answered correctly but skipped the citation.  ",
		"recommendations": [
			{"area":"system_prompt","finding":"no citation","suggestion":"require a source line","priority":"medium"}
		]
	}`
	summary, recs, err := parseAndSanitizeRecommendations(body)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if want := "The agent answered correctly but skipped the citation."; summary != want {
		t.Errorf("summary = %q, want %q", summary, want)
	}
	if len(recs) != 1 {
		t.Fatalf("got %d recs, want 1", len(recs))
	}
}

func TestParseAndSanitizeRecommendations_MissingSummaryIsEmpty(t *testing.T) {
	t.Parallel()
	summary, _, err := parseAndSanitizeRecommendations(`{"recommendations": []}`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if summary != "" {
		t.Errorf("summary = %q, want empty", summary)
	}
}

func TestBuildRecommendationsUserPrompt_IncludesAllInputs(t *testing.T) {
	t.Parallel()
	notes := "the tone was off"
	prompt, err := buildRecommendationsUserPrompt(GenerateRecommendationsParams{
		ComponentName:  "MyAgent",
		ComponentKind:  "agent",
		SampleInput:    "hello",
		ExpectedOutput: "hi back",
		ActualOutput:   "uhh",
		UserNotes:      &notes,
	}, "agent")
	if err != nil {
		t.Fatalf("build prompt: %v", err)
	}
	for _, want := range []string{"MyAgent", "hello", "hi back", "uhh", "the tone was off"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt missing %q\nfull prompt:\n%s", want, prompt)
		}
	}
}

func TestBuildRecommendationsUserPrompt_FormatsObjectValuesAsJSON(t *testing.T) {
	t.Parallel()
	prompt, err := buildRecommendationsUserPrompt(GenerateRecommendationsParams{
		ComponentName:  "WeatherWorkflow",
		ComponentKind:  "workflow",
		SampleInput:    map[string]any{"city": "Seattle"},
		ExpectedOutput: map[string]any{"forecast": "sunny"},
		ActualOutput:   map[string]any{"forecast": "rainy"},
	}, "workflow")
	if err != nil {
		t.Fatalf("build prompt: %v", err)
	}
	for _, want := range []string{`"city": "Seattle"`, `"forecast": "sunny"`, `"forecast": "rainy"`} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt missing %q\nfull prompt:\n%s", want, prompt)
		}
	}
}

func TestBuildRecommendationsUserPrompt_UnmarshalableValueReturnsError(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		p    GenerateRecommendationsParams
		want string
	}{
		{
			"sample",
			GenerateRecommendationsParams{ComponentName: "X", SampleInput: make(chan int), ExpectedOutput: "e", ActualOutput: "a"},
			"sampleInput",
		},
		{
			"expected",
			GenerateRecommendationsParams{ComponentName: "X", SampleInput: "s", ExpectedOutput: make(chan int), ActualOutput: "a"},
			"expectedOutput",
		},
		{
			"actual",
			GenerateRecommendationsParams{ComponentName: "X", SampleInput: "s", ExpectedOutput: "e", ActualOutput: make(chan int)},
			"actualOutput",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			_, err := buildRecommendationsUserPrompt(c.p, "agent")
			if err == nil {
				t.Fatalf("expected error for unmarshalable %s", c.want)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("error %q should reference %q", err, c.want)
			}
		})
	}
}

func TestJSONStringify_StringPassthrough(t *testing.T) {
	t.Parallel()
	got, err := jsonStringify("plain text")
	if err != nil {
		t.Fatalf("jsonStringify: %v", err)
	}
	if got != "plain text" {
		t.Errorf("got %q, want %q (strings must pass through unwrapped)", got, "plain text")
	}
}

func TestJSONStringify_MarshalsNonString(t *testing.T) {
	t.Parallel()
	got, err := jsonStringify(map[string]any{"k": "v"})
	if err != nil {
		t.Fatalf("jsonStringify: %v", err)
	}
	want := "{\n  \"k\": \"v\"\n}"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestJSONStringify_UnmarshalableReturnsError(t *testing.T) {
	t.Parallel()
	_, err := jsonStringify(make(chan int))
	if err == nil {
		t.Error("expected error for channel value")
	}
}

func TestBuildRecommendationsUserPrompt_OmitsBlankUserNotes(t *testing.T) {
	t.Parallel()
	blankNotes := "   "
	prompt, err := buildRecommendationsUserPrompt(GenerateRecommendationsParams{
		ComponentName:  "MyAgent",
		ComponentKind:  "agent",
		SampleInput:    "hello",
		ExpectedOutput: "hi",
		ActualOutput:   "hi",
		UserNotes:      &blankNotes,
	}, "agent")
	if err != nil {
		t.Fatalf("build prompt: %v", err)
	}
	if strings.Contains(prompt, "User observations") {
		t.Errorf("prompt unexpectedly includes 'User observations' header for blank notes:\n%s", prompt)
	}
}

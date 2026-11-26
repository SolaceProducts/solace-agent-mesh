// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package a2a

import (
	"fmt"
	"slices"
	"strings"
	"testing"
)

func TestFormatDeclaredOutput(t *testing.T) {
	t.Parallel()

	t.Run("nothing declared adds nothing", func(t *testing.T) {
		t.Parallel()
		if got := FormatDeclaredOutput(nil); got != "" {
			t.Errorf("got %q, want empty", got)
		}
	})

	t.Run("fields come out in name order with their types", func(t *testing.T) {
		t.Parallel()
		got := FormatDeclaredOutput([]OutputField{{Name: "spanish_summary", Type: "string"}, {Name: "english_summary"}})
		want := "Declared output fields: english_summary, spanish_summary (string)."
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("a long list is cut at the cap and says exactly how many were left out", func(t *testing.T) {
		t.Parallel()
		var fields []OutputField
		for i := range 500 {
			fields = append(fields, OutputField{Name: fmt.Sprintf("field_%03d", i), Type: "string"})
		}
		got := FormatDeclaredOutput(fields)
		if len(got) > MaxDeclaredOutputBytes {
			t.Errorf("len = %d, want <= %d", len(got), MaxDeclaredOutputBytes)
		}
		if !strings.HasPrefix(got, DeclaredOutputLabel+" field_000 (string), field_001 (string)") {
			t.Errorf("unexpected start: %q", got[:60])
		}
		listed := strings.Count(got, "field_")
		if want := fmt.Sprintf(", and %d more.", 500-listed); !strings.HasSuffix(got, want) {
			t.Errorf("ends %q, want suffix %q (listed %d)", got[len(got)-30:], want, listed)
		}
	})

	t.Run("the cut is the same whatever order the fields arrive in", func(t *testing.T) {
		t.Parallel()
		var forward []OutputField
		for i := range 300 {
			forward = append(forward, OutputField{Name: fmt.Sprintf("f%03d", i)})
		}
		reversed := slices.Clone(forward)
		slices.Reverse(reversed)
		if FormatDeclaredOutput(forward) != FormatDeclaredOutput(reversed) {
			t.Error("output depends on input order")
		}
	})

	t.Run("a name too long to list whole is counted as left out, never cut short", func(t *testing.T) {
		t.Parallel()
		long := strings.Repeat("x", maxDeclaredFieldNameRunes+1)
		got := FormatDeclaredOutput([]OutputField{{Name: "a"}, {Name: long}})
		if got != "Declared output fields: a, and 1 more." {
			t.Errorf("got %q", got)
		}
		if got := FormatDeclaredOutput([]OutputField{{Name: long}}); got != "" {
			t.Errorf("a list with nothing listable must add nothing, got %q", got)
		}
	})

	t.Run("a huge number of fields still respects the cap", func(t *testing.T) {
		t.Parallel()
		for width := 1; width <= 10; width++ {
			fields := []OutputField{{Name: strings.Repeat("a", width)}}
			for i := range 120000 {
				fields = append(fields, OutputField{Name: fmt.Sprintf("b%06d", i)})
			}
			if got := FormatDeclaredOutput(fields); len(got) > MaxDeclaredOutputBytes {
				t.Errorf("first name width %d: len = %d, want <= %d", width, len(got), MaxDeclaredOutputBytes)
			}
		}
	})

	t.Run("an empty name is skipped", func(t *testing.T) {
		t.Parallel()
		if got := FormatDeclaredOutput([]OutputField{{Name: ""}, {Name: "a"}}); got != "Declared output fields: a." {
			t.Errorf("got %q", got)
		}
	})

	t.Run("limits are in characters, not bytes, and a name or type at the limit is kept", func(t *testing.T) {
		t.Parallel()
		name := strings.Repeat("\u00e9", maxDeclaredFieldNameRunes)
		typ := strings.Repeat("\u00e9", maxDeclaredFieldTypeRunes)
		got := FormatDeclaredOutput([]OutputField{{Name: name, Type: typ}})
		if want := DeclaredOutputLabel + " " + name + " (" + typ + ")."; got != want {
			t.Errorf("got %q, want %q", got, want)
		}
		wide := strings.Repeat("\U0001F600", maxDeclaredFieldNameRunes)
		wideType := strings.Repeat("\U0001F600", maxDeclaredFieldTypeRunes)
		if got := FormatDeclaredOutput([]OutputField{{Name: wide, Type: wideType}}); len(got) > MaxDeclaredOutputBytes {
			t.Errorf("a single widest entry is %d bytes, over the %d cap", len(got), MaxDeclaredOutputBytes)
		}
	})

	t.Run("an over-long type is dropped, so the first entry cannot pass the cap", func(t *testing.T) {
		t.Parallel()
		got := FormatDeclaredOutput([]OutputField{{Name: "a", Type: strings.Repeat("t", 5000)}})
		if got != "Declared output fields: a." {
			t.Errorf("got %q", got)
		}
	})
}

func TestDeclared(t *testing.T) {
	t.Parallel()
	workflowLead := fmt.Sprintf(WorkflowToolLeadFormat, "Doc Pipeline")
	peerLead := fmt.Sprintf(PeerToolLeadFormat, "Translator")
	tests := []struct {
		name string
		desc string
		want string
	}{
		{"workflow opening sentence only is nothing declared", workflowLead, ""},
		{"agent opening sentence only", peerLead, ""},
		{"empty", "", ""},
		{"the card description is what remains", workflowLead + " Summarizes then translates.", "Summarizes then translates."},
		{"declared output fields are kept after the description", workflowLead + " Does it. " + DeclaredOutputLabel + " a, b.", "Does it. " + DeclaredOutputLabel + " a, b."},
		{"declared output fields alone are a declaration", workflowLead + " " + DeclaredOutputLabel + " a.", DeclaredOutputLabel + " a."},
		{"an agent's skill names are not a declaration", peerLead + " " + ToolSkillsLabel + " translate, detect", ""},
		{"an agent's description loses only its skill names", peerLead + " Translates to Spanish. " + ToolSkillsLabel + " translate", "Translates to Spanish."},
		{"a name with an escaped quote", fmt.Sprintf(WorkflowToolLeadFormat, `The "Big" Flow`) + " Does it.", "Does it."},
		{"an unquoted copy of the opening sentence is still the opening sentence", "Invoke the DocPipeline workflow.", ""},
		{"text that is not the tool opening sentence is kept", "Translates text", "Translates text"},
		{"a multi-word name re-quoted in the copy", "Invoke the \u201cDoc Pipeline\u201d workflow. Does it.", "Does it."},
		{"a multi-word name without quotes", "Invoke the Doc Pipeline workflow. Does it.", "Does it."},
		{"the opening sentence in another case", "invoke the \"X\" WORKFLOW. Does it.", "Does it."},
		{"a workflow description mentioning Skills keeps its declared fields", workflowLead + " Ranks resumes against the job's Required Skills: Go, SQL. " + DeclaredOutputLabel + " score, summary.", "Ranks resumes against the job's Required Skills: Go, SQL. " + DeclaredOutputLabel + " score, summary."},
		{"a workflow description that starts with Skills is a declaration", workflowLead + " Skills: matching candidates to roles.", "Skills: matching candidates to roles."},
		{"an agent description mentioning Skills with no card skills is kept", peerLead + " Helps with Skills: hiring triage.", "Helps with Skills: hiring triage."},
		{"an agent description ending in the word agent is kept whole", fmt.Sprintf(PeerToolLeadFormat, "Support") + " Routes questions to the right support agent.", "Routes questions to the right support agent."},
		{"a workflow description ending in the word workflow is kept whole", fmt.Sprintf(WorkflowToolLeadFormat, "Intake") + " Runs before the review workflow.", "Runs before the review workflow."},
		{"only the final skills clause is dropped", peerLead + " Does X. Skills: a. " + ToolSkillsLabel + " b, c", "Does X. Skills: a."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := Declared(tt.desc); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

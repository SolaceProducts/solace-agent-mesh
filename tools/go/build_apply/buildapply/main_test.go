// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package buildapply

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	sdk "github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"
)

func TestBuildApplyCompleteRecord_RequiresSummary(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		p    MarkChangesAppliedParams
	}{
		{"empty", MarkChangesAppliedParams{Summary: ""}},
		{"whitespace", MarkChangesAppliedParams{Summary: "   \n\t  "}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			_, _, err := buildApplyCompleteRecord(c.p)
			if err == nil {
				t.Errorf("expected error for summary=%q", c.p.Summary)
			}
			if err != nil && !strings.Contains(err.Error(), "summary") {
				t.Errorf("error should reference summary; got %v", err)
			}
		})
	}
}

func TestBuildApplyCompleteRecord_TrimsSummary(t *testing.T) {
	t.Parallel()
	rec, _, err := buildApplyCompleteRecord(MarkChangesAppliedParams{
		Summary: "  Updated the system prompt to clarify the role.\n",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rec.Summary != "Updated the system prompt to clarify the role." {
		t.Errorf("summary not trimmed: %q", rec.Summary)
	}
}

func TestBuildApplyCompleteRecord_AcceptsEmptyChanges(t *testing.T) {
	t.Parallel()
	rec, bytes, err := buildApplyCompleteRecord(MarkChangesAppliedParams{
		Summary: "No edits needed — current config matches the test outcome.",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rec.ChangesApplied) != 0 {
		t.Errorf("expected empty ChangesApplied, got %d entries", len(rec.ChangesApplied))
	}
	if !strings.Contains(string(bytes), `"changesApplied"`) {
		t.Errorf("JSON missing changesApplied key:\n%s", bytes)
	}
}

func TestMarkChangesApplied_RejectsBlankSummary(t *testing.T) {
	t.Parallel()
	res, err := markChangesApplied(t.Context(), MarkChangesAppliedParams{Summary: "  "}, &sdk.ToolContext{})
	if err != nil {
		t.Fatalf("markChangesApplied: %v", err)
	}
	if res.Status != "error" || !strings.Contains(res.Message, "summary") {
		t.Errorf("got %+v, want error result about summary", res)
	}
}

func TestMarkChangesApplied_NoOutputDirSurfacesError(t *testing.T) {
	t.Parallel()
	res, err := markChangesApplied(t.Context(), MarkChangesAppliedParams{
		Summary:        "Tightened the Translator system prompt.",
		ChangesApplied: []ChangeApplied{{Component: "Translator", Area: "system_prompt", Change: "Added rule"}},
	}, &sdk.ToolContext{})
	if err != nil {
		t.Fatalf("markChangesApplied: %v", err)
	}
	if res.Status != "error" || !strings.Contains(res.Message, "apply_complete") {
		t.Errorf("got %+v, want error about apply_complete artifact", res)
	}
}

func TestApplyCompleteFilename_StableContract(t *testing.T) {
	t.Parallel()
	if ApplyCompleteArtifactFilename != "apply_complete.json" {
		t.Errorf("ApplyCompleteArtifactFilename = %q, want apply_complete.json", ApplyCompleteArtifactFilename)
	}
	if filepath.Ext(ApplyCompleteArtifactFilename) != ".json" {
		t.Errorf("ApplyCompleteArtifactFilename extension = %q, want .json", filepath.Ext(ApplyCompleteArtifactFilename))
	}
	if strings.ContainsAny(ApplyCompleteArtifactFilename, "/\\") {
		t.Errorf("ApplyCompleteArtifactFilename = %q must be a flat filename", ApplyCompleteArtifactFilename)
	}
	if _, err := os.Stat(filepath.Join(os.TempDir(), "nonexistent-"+ApplyCompleteArtifactFilename)); err == nil {
		t.Error("temp file should not exist")
	}
}

func TestBuildApplyCompleteRecord_PreservesPerComponentList(t *testing.T) {
	t.Parallel()
	rec, bytes, err := buildApplyCompleteRecord(MarkChangesAppliedParams{
		Summary: "Tightened the system prompts and switched the model.",
		ChangesApplied: []ChangeApplied{
			{Component: "Translator", Area: "system_prompt", Change: "Added explicit translation-only rule."},
			{Component: "Translator", Area: "model_choice", Change: "Switched to a higher-capability model."},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rec.ChangesApplied) != 2 {
		t.Fatalf("got %d changes, want 2", len(rec.ChangesApplied))
	}
	if rec.ChangesApplied[0].Component != "Translator" {
		t.Errorf("first change component = %q, want Translator", rec.ChangesApplied[0].Component)
	}

	var parsed map[string]any
	if err := json.Unmarshal(bytes, &parsed); err != nil {
		t.Fatalf("could not re-parse marshalled bytes: %v", err)
	}
	if parsed["summary"] == nil {
		t.Errorf("JSON missing 'summary' field")
	}
	changes, ok := parsed["changesApplied"].([]any)
	if !ok || len(changes) != 2 {
		t.Fatalf("changesApplied wrong shape: %T", parsed["changesApplied"])
	}
	first, _ := changes[0].(map[string]any)
	for _, k := range []string{"component", "area", "change"} {
		if first[k] == nil {
			t.Errorf("change row missing %q field; got %v", k, first)
		}
	}
}

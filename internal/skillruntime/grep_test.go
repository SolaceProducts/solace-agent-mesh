// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package skillruntime

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/strutil"
)

func TestGrepResources_Happy(t *testing.T) {
	tmp := t.TempDir()
	skillDir := buildFixtureSkill(t, tmp, "fixture")
	ctx := SkillContext{Name: "fixture", LocalPath: skillDir}

	res, err := GrepResources(ctx, GrepOptions{Pattern: "JWT"})
	if err != nil {
		t.Fatalf("GrepResources: %v", err)
	}
	if res.MatchCount != 1 {
		t.Errorf("MatchCount: got %d, want 1", res.MatchCount)
	}
	if res.Truncated {
		t.Errorf("should not be truncated")
	}
	if len(res.Matches) == 0 || res.Matches[0].File != "references/auth.md" {
		t.Errorf("first match: %+v", res.Matches)
	}
	if res.Matches[0].Line != 2 {
		t.Errorf("line should be 1-based: got %d", res.Matches[0].Line)
	}
	if !strings.Contains(res.Matches[0].Text, "JWT") {
		t.Errorf("Text field should contain match: %q", res.Matches[0].Text)
	}
}

func TestGrepResources_GrepStyleAlternation(t *testing.T) {
	skillDir := t.TempDir()
	writeFile(t, filepath.Join(skillDir, "references", "notes.md"), "alpha line\nbeta line\ncolumns a | b\nGamma line\n")
	var many strings.Builder
	for i := range MaxGrepResults + 20 {
		fmt.Fprintf(&many, "row %d\n", i)
	}
	writeFile(t, filepath.Join(skillDir, "assets", "rows.txt"), many.String())
	ctx := SkillContext{Name: "notes", LocalPath: skillDir}
	cases := map[string]struct {
		opts      GrepOptions
		matches   int
		truncated bool
		note      string
		firstText string
	}{
		"read_as_or_when_the_literal_pipe_finds_nothing": {opts: GrepOptions{Pattern: `alpha\|beta`}, matches: 2, note: strutil.GrepAlternationRetriedNote},
		"a_literal_pipe_that_matches_is_kept":            {opts: GrepOptions{Pattern: `a \| b`}, matches: 1},
		"neither_reading_matches":                        {opts: GrepOptions{Pattern: `delta\|epsilon`}, note: strutil.GrepLiteralPipeNote},
		"an_re2_or_needs_no_note":                        {opts: GrepOptions{Pattern: `alpha|beta`}, matches: 2},
		"a_plain_miss_needs_no_note":                     {opts: GrepOptions{Pattern: `delta`}},
		"the_retry_keeps_case_insensitive":               {opts: GrepOptions{Pattern: `gamma\|delta`, CaseInsensitive: true}, matches: 1, note: strutil.GrepAlternationRetriedNote},
		"the_retry_keeps_case_sensitive":                 {opts: GrepOptions{Pattern: `gamma\|delta`}, note: strutil.GrepLiteralPipeNote},
		"the_retry_keeps_context_lines": {opts: GrepOptions{Pattern: `beta\|delta`, ContextLines: 1}, matches: 1,
			note: strutil.GrepAlternationRetriedNote, firstText: "alpha line\nbeta line\ncolumns a | b"},
		"the_retry_is_capped_and_says_so": {opts: GrepOptions{Pattern: `delta\|row`}, matches: MaxGrepResults, truncated: true,
			note: strutil.GrepAlternationRetriedNote},
		"a_reading_that_does_not_compile_is_not_searched": {opts: GrepOptions{Pattern: `delta\|*`}, note: strutil.GrepLiteralPipeNote},
		"an_empty_alternative_does_not_match_every_line":  {opts: GrepOptions{Pattern: `^\|`}, note: strutil.GrepLiteralPipeNote},
		"grep_grouping_is_not_half_read":                  {opts: GrepOptions{Pattern: `\(alpha\|beta\)`}, note: strutil.GrepLiteralPipeNote},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			res, err := GrepResources(ctx, tc.opts)
			if err != nil {
				t.Fatalf("GrepResources: %v", err)
			}
			if res.MatchCount != tc.matches || res.Truncated != tc.truncated || res.Note != tc.note {
				t.Errorf("matches %d truncated %v note %q, want %d %v %q", res.MatchCount, res.Truncated, res.Note,
					tc.matches, tc.truncated, tc.note)
			}
			if tc.firstText != "" && (len(res.Matches) == 0 || res.Matches[0].Text != tc.firstText) {
				t.Errorf("first match %+v, want text %q", res.Matches, tc.firstText)
			}
			if res.Pattern != tc.opts.Pattern {
				t.Errorf("pattern reported as %q, want the caller's %q", res.Pattern, tc.opts.Pattern)
			}
			if _, hasNote := res.AsMap()["note"]; hasNote != (tc.note != "") {
				t.Errorf("note key present %v, want %v", hasNote, tc.note != "")
			}
		})
	}
}

func TestGrepResources_CaseInsensitive(t *testing.T) {
	tmp := t.TempDir()
	skillDir := buildFixtureSkill(t, tmp, "fixture")
	ctx := SkillContext{Name: "fixture", LocalPath: skillDir}

	res, err := GrepResources(ctx, GrepOptions{Pattern: "jwt"})
	if err != nil {
		t.Fatalf("GrepResources: %v", err)
	}
	if res.MatchCount != 0 {
		t.Errorf("case-sensitive 'jwt' should not match: %d", res.MatchCount)
	}

	res, err = GrepResources(ctx, GrepOptions{Pattern: "jwt", CaseInsensitive: true})
	if err != nil {
		t.Fatalf("GrepResources: %v", err)
	}
	if res.MatchCount != 1 {
		t.Errorf("case-insensitive 'jwt' should match 1: got %d", res.MatchCount)
	}
}

func TestGrepResources_Context(t *testing.T) {
	tmp := t.TempDir()
	skillDir := buildFixtureSkill(t, tmp, "fixture")
	ctx := SkillContext{Name: "fixture", LocalPath: skillDir}

	res, err := GrepResources(ctx, GrepOptions{Pattern: "Algorithm", ContextLines: 1})
	if err != nil {
		t.Fatalf("GrepResources: %v", err)
	}
	if res.MatchCount != 1 {
		t.Fatalf("MatchCount: %d", res.MatchCount)
	}
	text := res.Matches[0].Text
	if !strings.Contains(text, "Algorithm") {
		t.Errorf("text should include the match: %q", text)
	}
	if !strings.Contains(text, "JWT") || !strings.Contains(text, "Header:") {
		t.Errorf("text should include neighbours of match: %q", text)
	}
}

func TestGrepResources_ResultCap(t *testing.T) {
	tmp := t.TempDir()
	skillDir := filepath.Join(tmp, "many")
	writeFile(t, filepath.Join(skillDir, "SKILL.md"), "# many")
	var sb strings.Builder
	for i := range MaxGrepResults + 50 {
		fmt.Fprintf(&sb, "match-%d\n", i)
	}
	writeFile(t, filepath.Join(skillDir, "references", "many.txt"), sb.String())

	ctx := SkillContext{Name: "many", LocalPath: skillDir}
	res, err := GrepResources(ctx, GrepOptions{Pattern: "match-"})
	if err != nil {
		t.Fatalf("GrepResources: %v", err)
	}
	if res.MatchCount != MaxGrepResults {
		t.Errorf("MatchCount should cap at %d, got %d", MaxGrepResults, res.MatchCount)
	}
	if !res.Truncated {
		t.Errorf("Truncated should be true when cap hit")
	}
}

func TestGrepResources_InvalidPattern(t *testing.T) {
	tmp := t.TempDir()
	skillDir := buildFixtureSkill(t, tmp, "fixture")
	ctx := SkillContext{Name: "fixture", LocalPath: skillDir}

	_, err := GrepResources(ctx, GrepOptions{Pattern: "[unclosed"})
	if !errors.Is(err, ErrInvalidPattern) {
		t.Errorf("expected ErrInvalidPattern, got %v", err)
	}
}

func TestGrepResources_EmptyPattern(t *testing.T) {
	ctx := SkillContext{Name: "x", LocalPath: t.TempDir()}
	_, err := GrepResources(ctx, GrepOptions{Pattern: ""})
	if !errors.Is(err, ErrInvalidParam) {
		t.Errorf("expected ErrInvalidParam, got %v", err)
	}
}

func TestGrepResources_AsMap(t *testing.T) {
	r := GrepResult{
		SkillName: "x",
		Pattern:   "p",
		Matches: []GrepMatch{
			{File: "references/a.md", Line: 1, Text: "p found"},
			{File: "assets/b.txt", Line: 5, Text: "before\np again\nafter"},
		},
		MatchCount: 2,
	}
	m := r.AsMap()
	matches, ok := m["matches"].([]map[string]any)
	if !ok {
		t.Fatalf("matches type wrong: %T", m["matches"])
	}
	if matches[0]["text"] != "p found" {
		t.Errorf("first match text: %v", matches[0]["text"])
	}
	if _, has := matches[0]["context"]; has {
		t.Errorf("context field should no longer be emitted")
	}
	if matches[1]["text"] != "before\np again\nafter" {
		t.Errorf("second match text should carry surrounding lines: %v", matches[1]["text"])
	}
}

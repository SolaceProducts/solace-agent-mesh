// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/strutil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/taxonomy"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func TestGrepBasicMatch(t *testing.T) {
	svc := newMockArtifactService()
	toolCtx := newTestToolCtx(svc)
	ctx := context.Background()

	saveTestArtifact(ctx, svc, toolCtx, "log.txt", "INFO starting\nERROR something broke\nINFO done\n", "text/plain")

	gt := &GrepTool{}
	result, err := gt.Execute(ctx, map[string]any{
		"pattern":  "ERROR",
		"filename": "log.txt",
	}, toolCtx)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != tool.StatusSuccess {
		t.Errorf("status = %q, want %q", result.Status, tool.StatusSuccess)
	}
	if result.Data["count"] != 1 {
		t.Errorf("count = %v, want 1", result.Data["count"])
	}
	matches, _ := result.Data["matches"].(string)
	if matches != "2:ERROR something broke" {
		t.Errorf("matches = %q", matches)
	}
}

func TestGrepRegex(t *testing.T) {
	svc := newMockArtifactService()
	toolCtx := newTestToolCtx(svc)
	ctx := context.Background()

	saveTestArtifact(ctx, svc, toolCtx, "data.txt", "foo 123\nbar 456\nbaz 789\n", "text/plain")

	gt := &GrepTool{}
	result, err := gt.Execute(ctx, map[string]any{
		"pattern":  "\\d{3}",
		"filename": "data.txt",
	}, toolCtx)
	if err != nil {
		t.Fatal(err)
	}
	if result.Data["count"] != 3 {
		t.Errorf("count = %v, want 3", result.Data["count"])
	}
}

func TestGrepNoMatch(t *testing.T) {
	svc := newMockArtifactService()
	toolCtx := newTestToolCtx(svc)
	ctx := context.Background()

	saveTestArtifact(ctx, svc, toolCtx, "data.txt", "hello world\n", "text/plain")

	gt := &GrepTool{}
	result, err := gt.Execute(ctx, map[string]any{
		"pattern":  "NOTFOUND",
		"filename": "data.txt",
	}, toolCtx)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != tool.StatusSuccess {
		t.Errorf("status = %q", result.Status)
	}
	if result.Data != nil {
		t.Errorf("expected nil data for no matches, got %v", result.Data)
	}
}

func TestGrepAlternationFallback(t *testing.T) {
	cases := map[string]struct {
		pattern string
		count   any
		message string
	}{
		"read_as_or_when_the_literal_pipe_finds_nothing":  {`ERROR\|WARN`, 2, "Found 2 matching lines. " + strutil.GrepAlternationRetriedNote},
		"a_literal_pipe_that_matches_is_kept":             {`a \| b`, 1, "Found 1 matching lines."},
		"neither_reading_matches":                         {`FATAL\|PANIC`, nil, `No matches found for pattern "FATAL\\|PANIC". ` + strutil.GrepLiteralPipeNote},
		"an_empty_alternative_does_not_match_every_line":  {`^\|`, nil, `No matches found for pattern "^\\|". ` + strutil.GrepLiteralPipeNote},
		"grep_grouping_is_not_half_read":                  {`\(ERROR\|WARN\)`, nil, `No matches found for pattern "\\(ERROR\\|WARN\\)". ` + strutil.GrepLiteralPipeNote},
		"a_reading_that_does_not_compile_is_not_searched": {`FATAL\|*`, nil, `No matches found for pattern "FATAL\\|*". ` + strutil.GrepLiteralPipeNote},
		"no_grep_style_pipe_no_note":                      {`FATAL`, nil, `No matches found for pattern "FATAL".`},
		"the_or_reading_stays_case_sensitive":             {`error\|warn`, nil, `No matches found for pattern "error\\|warn". ` + strutil.GrepLiteralPipeNote},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			svc := newMockArtifactService()
			toolCtx := newTestToolCtx(svc)
			ctx := context.Background()
			saveTestArtifact(ctx, svc, toolCtx, "log.txt", "INFO start\nERROR broke\nWARN slow\ncolumns a | b\n", "text/plain")

			result, err := (&GrepTool{}).Execute(ctx, map[string]any{"pattern": tc.pattern, "filename": "log.txt"}, toolCtx)
			if err != nil {
				t.Fatal(err)
			}
			if result.Status != tool.StatusSuccess {
				t.Fatalf("status = %q, want success", result.Status)
			}
			if result.Data["count"] != tc.count {
				t.Errorf("count = %v, want %v", result.Data["count"], tc.count)
			}
			if result.Message != tc.message {
				t.Errorf("message = %q, want %q", result.Message, tc.message)
			}
		})
	}
}

type loadHookService struct {
	*mockArtifactService
	loads  int
	onLoad func(n int) error
}

func (s *loadHookService) Load(ctx context.Context, loc artifact.Locator, version *int) ([]byte, string, error) {
	s.loads++
	if err := s.onLoad(s.loads); err != nil {
		return nil, "", err
	}
	return s.mockArtifactService.Load(ctx, loc, version)
}

func TestGrepAlternationRetrySearchesTheSameArtifacts(t *testing.T) {
	ctx := context.Background()
	t.Run("a_match_only_in_the_second_file", func(t *testing.T) {
		svc := newMockArtifactService()
		toolCtx := newTestToolCtx(svc)
		saveTestArtifact(ctx, svc, toolCtx, "a.txt", "INFO start\n", "text/plain")
		saveTestArtifact(ctx, svc, toolCtx, "b.txt", "INFO\nWARN slow\n", "text/plain")
		result, err := (&GrepTool{}).Execute(ctx, map[string]any{"pattern": `ERROR\|WARN`}, toolCtx)
		if err != nil {
			t.Fatal(err)
		}
		if matches, _ := result.Data["matches"].(string); result.Data["count"] != 1 || !strings.Contains(matches, "b.txt") ||
			!strings.Contains(matches, "2:WARN slow") {
			t.Errorf("count %v matches %q, want the one match in b.txt line 2", result.Data["count"], result.Data["matches"])
		}
	})
	t.Run("a_text_file_with_a_stray_control_byte", func(t *testing.T) {
		svc := newMockArtifactService()
		toolCtx := newTestToolCtx(svc)
		saveTestArtifact(ctx, svc, toolCtx, "log.txt", "hdr\x00\nINFO\nWARN slow\n", "text/plain")
		result, err := (&GrepTool{}).Execute(ctx, map[string]any{"pattern": `ERROR\|WARN`, "filename": "log.txt"}, toolCtx)
		if err != nil {
			t.Fatal(err)
		}
		if result.Data["count"] != 1 || !strings.Contains(result.Message, strutil.GrepAlternationRetriedNote) {
			t.Errorf("count %v message %q, want the WARN line read as \"or\"", result.Data["count"], result.Message)
		}
	})
	t.Run("a_miss_on_text_with_a_stray_control_byte_keeps_the_note", func(t *testing.T) {
		svc := newMockArtifactService()
		toolCtx := newTestToolCtx(svc)
		saveTestArtifact(ctx, svc, toolCtx, "log.txt", "hdr\x00\nINFO\n", "text/plain")
		result, err := (&GrepTool{}).Execute(ctx, map[string]any{"pattern": `ERROR\|WARN`, "filename": "log.txt"}, toolCtx)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(result.Message, strutil.GrepLiteralPipeNote) {
			t.Errorf("message %q, want the note on how \\| reads", result.Message)
		}
	})
	t.Run("the_answering_pass_reports_its_own_unread_files", func(t *testing.T) {
		svc := newMockArtifactService()
		toolCtx := newTestToolCtx(svc)
		saveTestArtifact(ctx, svc, toolCtx, "r.pdf", string(buildTestPDF("report")), mimePDF)
		saveTestArtifact(ctx, svc, toolCtx, "t.txt", "INFO\nWARN slow\n", "text/plain")
		saveTestArtifact(ctx, svc, toolCtx, "u.txt", "WARN too\n", "text/plain")
		failing := &failingArtifactService{mockArtifactService: svc, failFor: map[string]bool{"u.txt": true}}
		result, err := (&GrepTool{}).Execute(ctx, map[string]any{"pattern": `ERROR\|WARN`}, newTestToolCtx(failing))
		if err != nil {
			t.Fatal(err)
		}
		skipped, _ := result.Data["skipped_files"].([]string)
		binary, _ := result.Data["binary_files"].([]string)
		if result.Data["count"] != 1 || !strings.Contains(result.Message, strutil.GrepAlternationRetriedNote) ||
			len(skipped) != 1 || skipped[0] != "u.txt" || len(binary) != 1 || binary[0] != "r.pdf" {
			t.Errorf("count %v skipped %v binary %v message %q, want 1 match, u.txt skipped, r.pdf binary",
				result.Data["count"], skipped, binary, result.Message)
		}
	})
	t.Run("a_binary_only_search_reads_once", func(t *testing.T) {
		svc := newMockArtifactService()
		saveTestArtifact(ctx, svc, newTestToolCtx(svc), "r.pdf", string(buildTestPDF("report")), mimePDF)
		hooked := &loadHookService{mockArtifactService: svc, onLoad: func(int) error { return nil }}
		if _, err := (&GrepTool{}).Execute(ctx, map[string]any{"pattern": `ERROR\|WARN`, "filename": "r.pdf"}, newTestToolCtx(hooked)); err != nil {
			t.Fatal(err)
		}
		if hooked.loads != 1 {
			t.Errorf("loads = %d, want 1: a recognised binary cannot be searched as text either way", hooked.loads)
		}
	})
	t.Run("the_pinned_version", func(t *testing.T) {
		svc := newMockArtifactService()
		toolCtx := newTestToolCtx(svc)
		saveTestArtifact(ctx, svc, toolCtx, "log.txt", "WARN old\n", "text/plain")
		saveTestArtifact(ctx, svc, toolCtx, "log.txt", "INFO new\n", "text/plain")
		for version, want := range map[float64]any{0: 1, 1: nil} {
			result, err := (&GrepTool{}).Execute(ctx, map[string]any{"pattern": `ERROR\|WARN`, "filename": "log.txt", "version": version}, toolCtx)
			if err != nil {
				t.Fatal(err)
			}
			if result.Data["count"] != want {
				t.Errorf("version %v: count %v, want %v", version, result.Data["count"], want)
			}
		}
	})
}

func TestGrepAlternationRetryThatCouldNotFinish(t *testing.T) {
	t.Run("cancelled_during_the_second_pass_is_a_stopped_search", func(t *testing.T) {
		svc := newMockArtifactService()
		saveTestArtifact(context.Background(), svc, newTestToolCtx(svc), "log.txt", "WARN slow\n", "text/plain")
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		hooked := &loadHookService{mockArtifactService: svc, onLoad: func(n int) error {
			if n == 2 {
				cancel()
				return ctx.Err()
			}
			return nil
		}}
		result, err := (&GrepTool{}).Execute(ctx, map[string]any{"pattern": `ERROR\|WARN`, "filename": "log.txt"}, newTestToolCtx(hooked))
		if err != nil {
			t.Fatal(err)
		}
		if result.Status != tool.StatusError || !strings.Contains(result.Message, "Search stopped before completing") ||
			strings.Contains(result.Message, `\|`) {
			t.Errorf("status %q message %q, want the stopped-search error and no note", result.Status, result.Message)
		}
	})
	t.Run("cancelled_during_the_first_pass_reports_that_pass", func(t *testing.T) {
		svc := newMockArtifactService()
		toolCtx := newTestToolCtx(svc)
		saveTestArtifact(context.Background(), svc, toolCtx, "a.txt", "INFO\n", "text/plain")
		saveTestArtifact(context.Background(), svc, toolCtx, "b.txt", "INFO\n", "text/plain")
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		hooked := &loadHookService{mockArtifactService: svc, onLoad: func(n int) error {
			if n == 2 {
				cancel()
				return ctx.Err()
			}
			return nil
		}}
		result, err := (&GrepTool{}).Execute(ctx, map[string]any{"pattern": `ERROR\|WARN`}, newTestToolCtx(hooked))
		if err != nil {
			t.Fatal(err)
		}
		if result.Status != tool.StatusError || !strings.Contains(result.Message, "1 of 2 artifact(s) were searched") {
			t.Errorf("status %q message %q, want the first pass's stopped-search error", result.Status, result.Message)
		}
	})
	t.Run("an_unreadable_second_pass_claims_nothing_about_the_or_reading", func(t *testing.T) {
		svc := newMockArtifactService()
		saveTestArtifact(context.Background(), svc, newTestToolCtx(svc), "log.txt", "WARN slow\n", "text/plain")
		hooked := &loadHookService{mockArtifactService: svc, onLoad: func(n int) error {
			if n == 2 {
				return errors.New("503 from object store")
			}
			return nil
		}}
		result, err := (&GrepTool{}).Execute(context.Background(), map[string]any{"pattern": `ERROR\|WARN`, "filename": "log.txt"},
			newTestToolCtx(hooked))
		if err != nil {
			t.Fatal(err)
		}
		want := `No matches found for pattern "ERROR\\|WARN". ` + strutil.GrepLiteralPipeNote
		if result.Status != tool.StatusSuccess || result.Message != want {
			t.Errorf("status %q message %q, want success with %q", result.Status, result.Message, want)
		}
	})
	t.Run("nothing_searched_needs_no_second_pass", func(t *testing.T) {
		svc := newMockArtifactService()
		saveTestArtifact(context.Background(), svc, newTestToolCtx(svc), "log.txt", "WARN slow\n", "text/plain")
		hooked := &loadHookService{mockArtifactService: svc, onLoad: func(int) error { return errors.New("503 from object store") }}
		result, err := (&GrepTool{}).Execute(context.Background(), map[string]any{"pattern": `ERROR\|WARN`, "filename": "log.txt"},
			newTestToolCtx(hooked))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(result.Message, "None of the 1 artifact(s) could be read") {
			t.Errorf("message %q, want the nothing-read error", result.Message)
		}
		if hooked.loads != 1 {
			t.Errorf("loads = %d, want 1: a pass that read nothing has nothing to read again", hooked.loads)
		}
	})
}

func TestGrepMultiFile(t *testing.T) {
	svc := newMockArtifactService()
	toolCtx := newTestToolCtx(svc)
	ctx := context.Background()

	saveTestArtifact(ctx, svc, toolCtx, "a.txt", "line one\nERROR in a\n", "text/plain")
	saveTestArtifact(ctx, svc, toolCtx, "b.txt", "all good\nERROR in b\n", "text/plain")

	gt := &GrepTool{}
	result, err := gt.Execute(ctx, map[string]any{
		"pattern": "ERROR",
	}, toolCtx)
	if err != nil {
		t.Fatal(err)
	}
	if result.Data["count"] != 2 {
		t.Errorf("count = %v, want 2", result.Data["count"])
	}
	matches, _ := result.Data["matches"].(string)
	if len(matches) == 0 {
		t.Error("expected non-empty matches")
	}
}

func TestGrepInvalidRegex(t *testing.T) {
	svc := newMockArtifactService()
	toolCtx := newTestToolCtx(svc)
	ctx := context.Background()

	saveTestArtifact(ctx, svc, toolCtx, "data.txt", "hello\n", "text/plain")

	gt := &GrepTool{}
	result, err := gt.Execute(ctx, map[string]any{
		"pattern":  "[invalid",
		"filename": "data.txt",
	}, toolCtx)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != tool.StatusError {
		t.Errorf("expected error status for invalid regex, got %q", result.Status)
	}
}

func TestGrepEmptyPattern(t *testing.T) {
	svc := newMockArtifactService()
	toolCtx := newTestToolCtx(svc)

	gt := &GrepTool{}
	result, err := gt.Execute(context.Background(), map[string]any{}, toolCtx)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != tool.StatusError {
		t.Errorf("expected error for empty pattern, got %q", result.Status)
	}
}

func TestGrepRefusedSearchCodes(t *testing.T) {
	invalidArgs := tool.DispatchErrorCode(taxonomy.DispatchClassValidation)
	tests := []struct {
		name        string
		args        map[string]any
		wantMessage string
		wantCode    string
	}{
		{"pattern missing", map[string]any{"filename": "log.txt"}, "pattern is required", invalidArgs},
		{"pattern does not compile", map[string]any{"pattern": "[invalid", "filename": "log.txt"}, "invalid regex pattern", taxonomy.ToolFailureInvalidQuery},
		{"pinned version conflicts with version", map[string]any{"pattern": "x", "filename": "log.txt:1", "version": float64(2)}, "pass one or the other", invalidArgs},
		{"named artifact does not exist", map[string]any{"pattern": "x", "filename": "missing.txt"}, "could be read", taxonomy.ErrorCodeArtifactNotFound},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc := newMockArtifactService()
			toolCtx := newTestToolCtx(svc)
			saveTestArtifact(t.Context(), svc, toolCtx, "log.txt", "INFO starting\n", "text/plain")

			result, err := (&GrepTool{}).Execute(t.Context(), tc.args, toolCtx)
			if err != nil {
				t.Fatalf("Execute returned Go error: %v", err)
			}
			if result.Status != tool.StatusError || !strings.Contains(result.Message, tc.wantMessage) {
				t.Fatalf("result = %s: %q, want an error containing %q", result.Status, result.Message, tc.wantMessage)
			}
			if result.ErrorCode != tc.wantCode {
				t.Errorf("ErrorCode = %q, want %q", result.ErrorCode, tc.wantCode)
			}
		})
	}
}

func TestArtifactGrep_LookaheadPatternNamesRE2(t *testing.T) {
	toolCtx := newTestToolCtx(newMockArtifactService())

	result, err := (&GrepTool{}).Execute(context.Background(), map[string]any{"pattern": "foo(?=bar)", "filename": "log.txt"}, toolCtx)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != tool.StatusError || !strings.Contains(result.Message, "RE2") || !strings.Contains(result.Message, "no lookahead") {
		t.Errorf("got %s %q, want an error naming RE2 and lookahead", result.Status, result.Message)
	}
}

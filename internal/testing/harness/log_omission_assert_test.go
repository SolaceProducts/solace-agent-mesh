// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package harness

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
	"unicode/utf8"
)

const (
	anchor    = "handling tool result"
	leakedTxt = "CANARY-user-content-must-not-leak"
)

func TestEvaluateLogOmissions_FailsWhenMarkerPresent(t *testing.T) {
	logs := "level=DEBUG msg=\"handling tool result\" content=\"" + leakedTxt + "\"\n"

	failures := evaluateLogOmissions(logs, []LogOmissionAssert{{
		Description:    "tool results must not be logged verbatim",
		Markers:        []string{leakedTxt},
		RequirePresent: anchor,
	}})

	if len(failures) != 1 {
		t.Fatalf("got %d failures, want 1: %v", len(failures), failures)
	}
	if !strings.Contains(failures[0], leakedTxt) {
		t.Errorf("failure does not name the marker: %s", failures[0])
	}
	if !strings.Contains(failures[0], "first offending line") {
		t.Errorf("failure does not quote the offending line: %s", failures[0])
	}
}

func TestEvaluateLogOmissions_PassesWhenMarkerAbsent(t *testing.T) {
	logs := "level=DEBUG msg=\"handling tool result\" contentLen=42\n"

	failures := evaluateLogOmissions(logs, []LogOmissionAssert{{
		Description:    "tool results must not be logged verbatim",
		Markers:        []string{leakedTxt},
		RequirePresent: anchor,
	}})
	if len(failures) != 0 {
		t.Errorf("expected no failures, got: %v", failures)
	}
}

func TestEvaluateLogOmissions_FailsWhenAnchorMissing(t *testing.T) {
	logs := "level=INFO msg=\"gateway started\"\n"

	failures := evaluateLogOmissions(logs, []LogOmissionAssert{{
		Description:    "tool results must not be logged verbatim",
		Markers:        []string{leakedTxt},
		RequirePresent: anchor,
	}})
	if len(failures) != 1 {
		t.Fatalf("got %d failures, want 1 — a scenario that stopped exercising the leak path must not pass: %v", len(failures), failures)
	}
	if !strings.Contains(failures[0], "proves nothing") {
		t.Errorf("failure does not explain why the omission is worthless: %s", failures[0])
	}
}

func TestEvaluateLogOmissions_FailsOnEmptyLogs(t *testing.T) {
	failures := evaluateLogOmissions("", []LogOmissionAssert{{
		Description:    "anything",
		Markers:        []string{leakedTxt},
		RequirePresent: anchor,
	}})
	if len(failures) != 1 {
		t.Fatalf("empty logs must fail, got %d failures", len(failures))
	}
}

func TestEvaluateLogOmissions_ReportsEveryMarker(t *testing.T) {
	logs := "msg=\"handling tool result\" a=LEAK-ONE b=LEAK-TWO\n"

	failures := evaluateLogOmissions(logs, []LogOmissionAssert{{
		Description:    "two markers, both leaked",
		Markers:        []string{"LEAK-ONE", "LEAK-TWO"},
		RequirePresent: anchor,
	}})
	if len(failures) != 2 {
		t.Errorf("got %d failures, want one per leaked marker: %v", len(failures), failures)
	}
}

func TestScenarioValidate_LogOmissionRules(t *testing.T) {
	valid := LogOmissionAssert{
		Description:    "d",
		Markers:        []string{"MARKER"},
		RequirePresent: "anchor",
	}

	for _, tc := range []struct {
		name    string
		assert  LogOmissionAssert
		wantErr string
	}{
		{"valid", valid, ""},
		{
			"no markers",
			LogOmissionAssert{Description: "d", RequirePresent: "anchor"},
			"markers must not be empty",
		},
		{
			"blank marker",
			LogOmissionAssert{Description: "d", Markers: []string{"  "}, RequirePresent: "anchor"},
			"matches every log line",
		},
		{
			"missing anchor",
			LogOmissionAssert{Description: "d", Markers: []string{"MARKER"}},
			"require_present is required",
		},
		{
			"missing description",
			LogOmissionAssert{Markers: []string{"MARKER"}, RequirePresent: "anchor"},
			"description is required",
		},
		{
			"anchor overlaps marker",
			LogOmissionAssert{Description: "d", Markers: []string{"MARKER"}, RequirePresent: "MARKER-anchor"},
			"overlaps marker",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sc := &TestScenario{AssertLogsOmit: []LogOmissionAssert{tc.assert}}
			err := sc.validateLogOmissions()
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("expected valid, got %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected an error containing %q, got nil", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error = %q, want it to contain %q", err.Error(), tc.wantErr)
			}
		})
	}
}

func TestCapturedLogs_RecordsHarnessLoggerOutputAtDebug(t *testing.T) {
	h := New(t, &TestScenario{})

	h.logger.Debug("debug level record", "marker", "CAPTURE-DEBUG")
	h.logger.Info("info level record", "marker", "CAPTURE-INFO")

	logs := h.CapturedLogs()
	if !strings.Contains(logs, "CAPTURE-INFO") {
		t.Errorf("INFO record missing from capture: %q", logs)
	}
	if !strings.Contains(logs, "CAPTURE-DEBUG") {
		t.Errorf("DEBUG record missing from capture — the harness logger is not recording at DEBUG, so canaries cannot see DEBUG-level leaks: %q", logs)
	}
}

func TestCapturedLogs_IncludesDerivedChildLoggers(t *testing.T) {
	h := New(t, &TestScenario{})

	h.logger.With("logger_name", "gateway").Warn("child logger record", "marker", "CAPTURE-CHILD")

	if !strings.Contains(h.CapturedLogs(), "CAPTURE-CHILD") {
		t.Errorf("record from a derived logger missing from capture: %q", h.CapturedLogs())
	}
}

func TestAssertLogsOmit_NoAssertionsIsNoOp(t *testing.T) {
	h := New(t, &TestScenario{})
	h.AssertLogsOmit(t)
}

func TestMarkerSurvivesTextHandler(t *testing.T) {
	render := func(marker string) string {
		var buf bytes.Buffer
		slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})).
			Info("leak", "content", "prefix "+marker+" suffix")
		return buf.String()
	}

	cases := []struct {
		name      string
		marker    string
		wantMatch bool
	}{
		{"plain", "CANARY-cross-user-content", true},
		{"with spaces", "what is the weather today", true},
		{"double quote", `say "hello" now`, false},
		{"backslash with space", `C:\Users\bob secret`, false},
		{"newline", "line one\nline two", false},
		{"tab", "a\tb", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rendered := render(tc.marker)
			gotMatch := strings.Contains(rendered, tc.marker)
			if gotMatch != tc.wantMatch {
				t.Fatalf("marker %q: rendered match = %v, want %v\n  rendered: %s",
					tc.marker, gotMatch, tc.wantMatch, rendered)
			}
			rejected := unmatchableMarkerReason(tc.marker) != ""
			if rejected == tc.wantMatch {
				t.Errorf("marker %q: handler match = %v but validator rejected = %v — the validator and the handler disagree",
					tc.marker, gotMatch, rejected)
			}
		})
	}
}

func TestValidateLogOmissions_RejectsUnmatchableMarker(t *testing.T) {
	sc := &TestScenario{AssertLogsOmit: []LogOmissionAssert{{
		Description:    "quoted excerpt",
		Markers:        []string{`the user said "hello"`},
		RequirePresent: "LLM request starting",
	}}}
	err := sc.validateLogOmissions()
	if err == nil {
		t.Fatal("a marker containing a double quote was accepted; it can never match, so the canary would be permanently green")
	}
	if !strings.Contains(err.Error(), "can never match") {
		t.Errorf("error does not explain why: %v", err)
	}
}

func TestRejectMisspelledLogOmissionKey(t *testing.T) {
	cases := []struct {
		name    string
		yaml    string
		wantErr bool
	}{
		{"correct key", "assert_logs_omit:\n  - description: d\n", false},
		{"singular log", "assert_log_omit:\n  - description: d\n", true},
		{"past tense", "assert_logs_omitted:\n  - description: d\n", true},
		{"no prefix", "logs_omit:\n  - description: d\n", true},
		{"unrelated key", "skip_reason: not applicable\n", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := rejectMisspelledLogOmissionKey([]byte(tc.yaml))
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr = %v", err, tc.wantErr)
			}
		})
	}
}

func TestFirstContextLine_TruncatesOnARuneBoundary(t *testing.T) {
	const marker = "CANARY"

	for _, tc := range []struct{ name, filler string }{
		{"cjk at the boundary", "日本語のテキスト"},
		{"emoji at the boundary", "🎉🎉🎉🎉"},
		{"accented latin at the boundary", "éééééé"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for pad := 280; pad < 305; pad++ {
				line := marker + strings.Repeat("a", pad) + tc.filler
				got := firstContextLine(line, marker)
				if !utf8.ValidString(got) {
					t.Fatalf("pad=%d produced invalid UTF-8: %q", pad, got)
				}
			}
		})
	}
}

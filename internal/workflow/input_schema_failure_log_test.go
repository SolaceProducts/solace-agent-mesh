// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/structured"
)

func TestFailureLogText(t *testing.T) {
	quoted := structured.InputValidationFailedPrefix + "'CANARY-secret' does not match pattern"

	if got := failureLogText(quoted); strings.Contains(got, "CANARY") || !strings.Contains(got, "input validation failure") {
		t.Errorf("failureLogText(input rejection) = %q, want a value-free line", got)
	}
	if got := failureLogText(`workflow "x" failed: ` + quoted); strings.Contains(got, "CANARY") {
		t.Errorf("a wrapped input rejection reached the log: %q", got)
	}
	if got := failureLogText(`node "n" failed: timeout`); got != `node "n" failed: timeout` {
		t.Errorf("an unrelated failure was rewritten: %q", got)
	}
}

func TestPeerFailure_KeepsItsTextAndMasksOnlyTheLogForm(t *testing.T) {
	cause := errors.New(structured.InputValidationFailedPrefix + "'CANARY-secret' does not match pattern")
	failure := peerFailure{cause}

	if failure.Error() != cause.Error() {
		t.Errorf("Error() = %q, want the peer's text unchanged", failure.Error())
	}
	if !errors.Is(failure, cause) {
		t.Error("errors.Is cannot see through the wrapper")
	}

	var logs bytes.Buffer
	slog.New(slog.NewTextHandler(&logs, nil)).Error("node failed", "error", error(failure))
	if strings.Contains(logs.String(), "CANARY") || !strings.Contains(logs.String(), "input validation failure") {
		t.Errorf("a log line taking the error as an attribute carried its text:\n%s", logs.String())
	}
}

func TestTruncateRunes_CutsOnRuneBoundary(t *testing.T) {
	got := truncateRunes(strings.Repeat("é", 5000), 100)

	if !utf8.ValidString(got) {
		t.Fatalf("truncated text is not valid UTF-8: %q", got)
	}
	if want := 100 + utf8.RuneCountInString("... (truncated)"); utf8.RuneCountInString(got) != want {
		t.Errorf("rune count = %d, want %d", utf8.RuneCountInString(got), want)
	}
	if short := "short"; truncateRunes(short, 100) != short {
		t.Errorf("text under the limit was changed")
	}
}

func TestRequiredInputHint_NamesAllowedTypesOfANullableField(t *testing.T) {
	schema := map[string]any{
		"properties": map[string]any{"note": map[string]any{"type": []any{"string", "null"}}},
		"required":   []any{"note"},
	}

	if got, want := requiredInputHint(schema), "Send the input as a JSON object. Required: note (string)."; got != want {
		t.Errorf("hint = %q, want %q", got, want)
	}
}

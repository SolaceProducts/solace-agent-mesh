// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"testing"
)

func newCapturingLogger() (*slog.Logger, *bytes.Buffer) {
	var buf bytes.Buffer
	return slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})), &buf
}

func TestParseConflictResponse_DoesNotLogModelContent(t *testing.T) {
	const canary = "CANARY-conflict-model-prose-must-not-be-logged"
	logger, logs := newCapturingLogger()

	got := parseConflictResponse(canary+" {not json at all}", logger)

	if !strings.Contains(logs.String(), "conflict validator non-JSON response") {
		t.Fatalf("the non-JSON path did not run, so this asserts nothing: %q", logs.String())
	}
	if strings.Contains(logs.String(), canary) {
		t.Errorf("the model's output was logged: %q", logs.String())
	}
	if !strings.Contains(logs.String(), "contentLen=") {
		t.Errorf("contentLen is missing, so an operator cannot see the model returned non-JSON: %q", logs.String())
	}
	if !strings.Contains(logs.String(), "error=") {
		t.Errorf("the parse error is missing, so nothing says WHY parsing failed: %q", logs.String())
	}
	if got == nil {
		t.Error("parseConflictResponse returned nil; the fail-open behaviour changed")
	}
}

func TestLogParseFallback_DoesNotLogModelContent(t *testing.T) {
	const canary = "CANARY-task-builder-prose-must-not-be-logged"
	logger, logs := newCapturingLogger()

	logParseFallback(logger, errors.New("unexpected end of JSON input"), "  "+canary+"  ")

	if !strings.Contains(logs.String(), "response parse failed") {
		t.Fatalf("the fallback path did not run, so this asserts nothing: %q", logs.String())
	}
	if strings.Contains(logs.String(), canary) {
		t.Errorf("the model's output was logged: %q", logs.String())
	}
	if !strings.Contains(logs.String(), "contentLen=") {
		t.Errorf("contentLen is missing, so an operator cannot see how much the model returned: %q", logs.String())
	}
	if !strings.Contains(logs.String(), "unexpected end of JSON input") {
		t.Errorf("the parse error is missing, so nothing says WHY parsing failed: %q", logs.String())
	}
}

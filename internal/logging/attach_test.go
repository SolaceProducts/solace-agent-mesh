// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package logging

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

func TestAttachHandlers_NilSafety(t *testing.T) {
	if got := AttachHandlers(nil); got != nil {
		t.Error("AttachHandlers(nil) should return nil")
	}
	base := slog.Default()
	if got := AttachHandlers(base); got != base {
		t.Error("AttachHandlers with no extras should return base unchanged")
	}
}

func TestAttachHandlers_AppendsToMultiHandler(t *testing.T) {
	var stderrBuf, fileBuf, otlpBuf bytes.Buffer
	stderr := slog.NewTextHandler(&stderrBuf, &slog.HandlerOptions{Level: slog.LevelDebug})
	file := slog.NewTextHandler(&fileBuf, &slog.HandlerOptions{Level: slog.LevelDebug})
	otlp := slog.NewTextHandler(&otlpBuf, &slog.HandlerOptions{Level: slog.LevelDebug})

	base := slog.New(NewMultiHandler(stderr, file))
	merged := AttachHandlers(base, otlp)

	merged.Info("hello-world")

	if !strings.Contains(stderrBuf.String(), "hello-world") {
		t.Errorf("stderr did not receive record: %s", stderrBuf.String())
	}
	if !strings.Contains(fileBuf.String(), "hello-world") {
		t.Errorf("file did not receive record: %s", fileBuf.String())
	}
	if !strings.Contains(otlpBuf.String(), "hello-world") {
		t.Errorf("otlp did not receive record: %s", otlpBuf.String())
	}
}

func TestAttachHandlers_WrapsSingleHandler(t *testing.T) {
	var baseBuf, extraBuf bytes.Buffer
	base := slog.New(slog.NewTextHandler(&baseBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	extra := slog.NewTextHandler(&extraBuf, &slog.HandlerOptions{Level: slog.LevelDebug})

	merged := AttachHandlers(base, extra)
	merged.Info("hello")

	if !strings.Contains(baseBuf.String(), "hello") {
		t.Errorf("base did not receive record: %s", baseBuf.String())
	}
	if !strings.Contains(extraBuf.String(), "hello") {
		t.Errorf("extra did not receive record: %s", extraBuf.String())
	}
}

func TestAttachHandlers_AdditiveWithFiltering(t *testing.T) {
	var stderrBuf, otlpBuf bytes.Buffer
	stderr := slog.NewTextHandler(&stderrBuf, &slog.HandlerOptions{Level: slog.LevelDebug})
	otlp := slog.NewTextHandler(&otlpBuf, &slog.HandlerOptions{Level: slog.LevelError})

	merged := AttachHandlers(slog.New(stderr), otlp)
	merged.Info("info-record")

	if !strings.Contains(stderrBuf.String(), "info-record") {
		t.Errorf("stderr did not receive INFO record: %s", stderrBuf.String())
	}
	if otlpBuf.Len() != 0 {
		t.Errorf("OTLP handler should have filtered INFO record: got %s", otlpBuf.String())
	}
}

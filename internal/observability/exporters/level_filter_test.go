// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package exporters

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
)

func TestLevelFromString(t *testing.T) {
	tests := []struct {
		in   string
		want slog.Level
	}{
		{"DEBUG", slog.LevelDebug},
		{"INFO", slog.LevelInfo},
		{"", slog.LevelInfo},
		{"WARNING", slog.LevelWarn},
		{"WARN", slog.LevelWarn},
		{"ERROR", slog.LevelError},
		{"CRITICAL", slog.LevelError + 4},
		{"unknown", slog.LevelInfo},
		{"  warning  ", slog.LevelWarn},
		{"debug", slog.LevelDebug},
		{"warning", slog.LevelWarn},
		{"Error", slog.LevelError},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			if got := LevelFromString(tt.in); got != tt.want {
				t.Errorf("LevelFromString(%q): got %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestLevelFilteringHandler_Enabled(t *testing.T) {
	inner := slog.NewTextHandler(&bytes.Buffer{}, &slog.HandlerOptions{Level: slog.LevelDebug})
	h := newLevelFilteringHandler(inner, slog.LevelWarn)

	cases := []struct {
		level slog.Level
		want  bool
	}{
		{slog.LevelDebug, false},
		{slog.LevelInfo, false},
		{slog.LevelWarn, true},
		{slog.LevelError, true},
	}
	for _, tt := range cases {
		t.Run(tt.level.String(), func(t *testing.T) {
			if got := h.Enabled(context.Background(), tt.level); got != tt.want {
				t.Errorf("Enabled(%v): got %v, want %v", tt.level, got, tt.want)
			}
		})
	}
}

func TestLevelFilteringHandler_HandleDropsBelowMinimum(t *testing.T) {
	var buf bytes.Buffer
	inner := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	h := newLevelFilteringHandler(inner, slog.LevelError)

	logger := slog.New(h)
	logger.Info("info-message")
	logger.Warn("warn-message")
	logger.Error("error-message")

	out := buf.String()
	if strings.Contains(out, "info-message") {
		t.Errorf("expected info to be filtered, got: %s", out)
	}
	if strings.Contains(out, "warn-message") {
		t.Errorf("expected warn to be filtered, got: %s", out)
	}
	if !strings.Contains(out, "error-message") {
		t.Errorf("expected error to pass through, got: %s", out)
	}
}

func TestLevelFilteringHandler_WithAttrsAndGroup(t *testing.T) {
	var buf bytes.Buffer
	inner := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	h := newLevelFilteringHandler(inner, slog.LevelInfo)

	withAttrs := h.WithAttrs([]slog.Attr{slog.String("svc", "auth")})
	withGroup := withAttrs.WithGroup("scope")
	logger := slog.New(withGroup)
	logger.Info("hello")

	out := buf.String()
	if !strings.Contains(out, "hello") {
		t.Errorf("expected message to be emitted, got: %s", out)
	}
	if !strings.Contains(out, "svc=auth") {
		t.Errorf("expected svc attribute, got: %s", out)
	}
}

func TestLevelFilteringHandler_LevelVarTunable(t *testing.T) {
	var buf bytes.Buffer
	inner := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	lv := new(slog.LevelVar)
	lv.Set(slog.LevelError)

	h := newLevelFilteringHandler(inner, lv)
	logger := slog.New(h)

	logger.Info("filtered")
	if buf.Len() != 0 {
		t.Errorf("expected info to be filtered while level=ERROR, got: %s", buf.String())
	}

	lv.Set(slog.LevelInfo)
	logger.Info("permitted")
	if !strings.Contains(buf.String(), "permitted") {
		t.Errorf("expected info to pass through after level=INFO, got: %s", buf.String())
	}
}

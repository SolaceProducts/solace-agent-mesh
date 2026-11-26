// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package logging

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"
)

func TestParseLogLevel(t *testing.T) {
	tests := []struct {
		input string
		want  slog.Level
	}{
		{"DEBUG", slog.LevelDebug},
		{"debug", slog.LevelDebug},
		{"INFO", slog.LevelInfo},
		{"info", slog.LevelInfo},
		{"WARNING", slog.LevelWarn},
		{"warning", slog.LevelWarn},
		{"WARN", slog.LevelWarn},
		{"ERROR", slog.LevelError},
		{"CRITICAL", slog.LevelError},
		{"", slog.LevelInfo},
		{"unknown", slog.LevelInfo},
		{"  INFO  ", slog.LevelInfo},
	}

	for _, tt := range tests {
		got := ParseLogLevel(tt.input)
		if got != tt.want {
			t.Errorf("ParseLogLevel(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestMultiHandlerEnabled(t *testing.T) {
	h1 := slog.NewTextHandler(&bytes.Buffer{}, &slog.HandlerOptions{Level: slog.LevelInfo})
	h2 := slog.NewTextHandler(&bytes.Buffer{}, &slog.HandlerOptions{Level: slog.LevelDebug})
	multi := NewMultiHandler(h1, h2)

	if !multi.Enabled(context.Background(), slog.LevelDebug) {
		t.Error("expected DEBUG to be enabled (h2 accepts it)")
	}

	if !multi.Enabled(context.Background(), slog.LevelInfo) {
		t.Error("expected INFO to be enabled")
	}
}

func TestMultiHandlerEnabledNoneMatch(t *testing.T) {
	h := slog.NewTextHandler(&bytes.Buffer{}, &slog.HandlerOptions{Level: slog.LevelError})
	multi := NewMultiHandler(h)

	if multi.Enabled(context.Background(), slog.LevelDebug) {
		t.Error("expected DEBUG to be disabled when only ERROR handler exists")
	}
}

func TestMultiHandlerHandle(t *testing.T) {
	var buf1, buf2 bytes.Buffer
	h1 := slog.NewTextHandler(&buf1, &slog.HandlerOptions{Level: slog.LevelInfo})
	h2 := slog.NewTextHandler(&buf2, &slog.HandlerOptions{Level: slog.LevelDebug})
	multi := NewMultiHandler(h1, h2)
	logger := slog.New(multi)

	logger.Info("hello")
	if buf1.Len() == 0 {
		t.Error("expected INFO message in buf1 (INFO handler)")
	}
	if buf2.Len() == 0 {
		t.Error("expected INFO message in buf2 (DEBUG handler)")
	}

	buf1.Reset()
	buf2.Reset()

	logger.Debug("debug msg")
	if buf1.Len() > 0 {
		t.Error("expected no DEBUG message in buf1 (INFO handler)")
	}
	if buf2.Len() == 0 {
		t.Error("expected DEBUG message in buf2 (DEBUG handler)")
	}
}

type failingHandler struct {
	level  slog.Level
	err    error
	called int
}

func (f *failingHandler) Enabled(_ context.Context, lvl slog.Level) bool {
	return lvl >= f.level
}

func (f *failingHandler) Handle(_ context.Context, _ slog.Record) error {
	f.called++
	return f.err
}

func (f *failingHandler) WithAttrs(_ []slog.Attr) slog.Handler { return f }
func (f *failingHandler) WithGroup(_ string) slog.Handler      { return f }

func TestMultiHandlerHandle_FailingSinkDoesNotBlockOthers(t *testing.T) {
	failing := &failingHandler{level: slog.LevelInfo, err: errors.New("write failed")}

	var buf bytes.Buffer
	good := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})

	multi := NewMultiHandler(failing, good)
	logger := slog.New(multi)

	logger.Info("must reach good sink despite failing sink")

	if failing.called != 1 {
		t.Errorf("failing handler called %d times, want 1", failing.called)
	}
	if buf.Len() == 0 {
		t.Error("second handler received nothing — first handler's error short-circuited the fan-out")
	}
	if !bytes.Contains(buf.Bytes(), []byte("must reach good sink")) {
		t.Errorf("second handler output missing message, got: %s", buf.String())
	}
}

func TestMultiHandlerHandle_CollectsErrors(t *testing.T) {
	e1 := errors.New("sink1 failed")
	e2 := errors.New("sink2 failed")
	h1 := &failingHandler{level: slog.LevelDebug, err: e1}
	h2 := &failingHandler{level: slog.LevelDebug, err: e2}

	multi := NewMultiHandler(h1, h2)

	err := multi.Handle(context.Background(), slog.NewRecord(time.Time{}, slog.LevelInfo, "msg", 0))
	if err == nil {
		t.Fatal("expected joined error, got nil")
	}
	if !errors.Is(err, e1) {
		t.Error("joined error does not wrap e1")
	}
	if !errors.Is(err, e2) {
		t.Error("joined error does not wrap e2")
	}
	if h1.called != 1 || h2.called != 1 {
		t.Errorf("both handlers should have been called once; got h1=%d h2=%d", h1.called, h2.called)
	}
}

func TestMultiHandlerWithAttrs(t *testing.T) {
	var buf bytes.Buffer
	h := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	multi := NewMultiHandler(h)

	logger := slog.New(multi.WithAttrs([]slog.Attr{slog.String("logger_name", "test")}))
	logger.Info("with attrs")

	if !bytes.Contains(buf.Bytes(), []byte("logger_name=test")) {
		t.Errorf("expected 'logger_name=test' in output, got: %s", buf.String())
	}
}

func TestMultiHandlerWithGroup(t *testing.T) {
	var buf bytes.Buffer
	h := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	multi := NewMultiHandler(h)

	logger := slog.New(multi.WithGroup("mygroup"))
	logger.Info("grouped", "key", "val")

	if !bytes.Contains(buf.Bytes(), []byte("mygroup.key=val")) {
		t.Errorf("expected 'mygroup.key=val' in output, got: %s", buf.String())
	}
}

func TestParseLogLevel_WarnsOnlyOnUnrecognizedInput(t *testing.T) {
	capture := func(level string) string {
		t.Helper()
		r, w, err := os.Pipe()
		if err != nil {
			t.Fatalf("pipe: %v", err)
		}
		orig := os.Stderr
		os.Stderr = w
		ParseLogLevel(level)
		os.Stderr = orig
		if closeErr := w.Close(); closeErr != nil {
			t.Fatalf("close pipe writer: %v", closeErr)
		}
		out, readErr := io.ReadAll(r)
		if readErr != nil {
			t.Fatalf("read pipe: %v", readErr)
		}
		return string(out)
	}

	for _, quiet := range []string{"", "  ", "DEBUG", "info", "WARN", "CRITICAL"} {
		if got := capture(quiet); got != "" {
			t.Errorf("ParseLogLevel(%q) wrote to stderr: %q — only unrecognized input should warn", quiet, got)
		}
	}

	got := capture("INFOO")
	if !strings.Contains(got, "INFOO") {
		t.Errorf("ParseLogLevel(%q) stderr = %q, want it to name the offending value", "INFOO", got)
	}
	if !strings.Contains(got, "INFO") {
		t.Errorf("ParseLogLevel(%q) stderr = %q, want it to state the level used", "INFOO", got)
	}
}

type recordingHandler struct {
	records *[]slog.Record
	attrs   []slog.Attr
}

func (h recordingHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h recordingHandler) Handle(_ context.Context, r slog.Record) error {
	for _, a := range h.attrs {
		r.AddAttrs(a)
	}
	*h.records = append(*h.records, r)
	return nil
}

func (h recordingHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return recordingHandler{records: h.records, attrs: append(append([]slog.Attr{}, h.attrs...), attrs...)}
}

func (h recordingHandler) WithGroup(string) slog.Handler { return h }

func attrText(r slog.Record) string {
	var b strings.Builder
	r.Attrs(func(a slog.Attr) bool {
		b.WriteString(a.Key + "=" + a.Value.String() + " ")
		return true
	})
	return b.String()
}

type credentialValuer struct{ apiKey string }

func (c credentialValuer) LogValue() slog.Value {
	return slog.GroupValue(slog.String("api_key", c.apiKey))
}

func TestMultiHandler_MasksSinksThatHaveNoReplaceAttr(t *testing.T) {
	const secret = "sk-live-OTLP-CANARY"

	cases := []struct {
		name string
		log  func(*slog.Logger)
	}{
		{"record attr", func(l *slog.Logger) { l.Info("boot", "api_key", secret) }},
		{"attr captured by With", func(l *slog.Logger) { l.With("api_key", secret).Info("boot") }},
		{"attr inside a group", func(l *slog.Logger) {
			l.Info("boot", slog.Group("auth", slog.String("client_secret", secret)))
		}},
		{"secret behind a LogValuer", func(l *slog.Logger) {
			l.Info("boot", slog.Any("creds", credentialValuer{secret}))
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got []slog.Record
			var buf bytes.Buffer
			logger := slog.New(NewMultiHandler(
				NewHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}, "text"),
				recordingHandler{records: &got},
			))

			tc.log(logger)

			if len(got) != 1 {
				t.Fatalf("the bridge received %d records, want 1", len(got))
			}
			if text := attrText(got[0]); strings.Contains(text, secret) {
				t.Errorf("a sink with no ReplaceAttr received the secret verbatim: %s", text)
			}
			if !strings.Contains(buf.String(), "[REDACTED]") {
				t.Errorf("the leaf handler did not mask either, so this test is not exercising masking: %q", buf.String())
			}
		})
	}
}

func TestMultiHandler_WrapIsIdempotent(t *testing.T) {
	var got []slog.Record
	base := slog.New(NewMultiHandler(recordingHandler{records: &got}))

	var buf bytes.Buffer
	merged := AttachHandlers(base, NewHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}, "text"))

	mh, ok := merged.Handler().(*MultiHandler)
	if !ok {
		t.Fatalf("AttachHandlers returned %T, want *MultiHandler", merged.Handler())
	}
	for i, h := range mh.handlers {
		inner, ok := h.(maskingHandler)
		if !ok {
			t.Fatalf("sub-handler %d is %T, not wrapped for masking", i, h)
		}
		if _, nested := inner.inner.(maskingHandler); nested {
			t.Errorf("sub-handler %d is double-wrapped; re-attaching nests a decorator per round", i)
		}
	}
}

// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package embedded

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
)

func newTestBroker(t *testing.T) broker.Broker {
	t.Helper()
	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatalf("connect dev broker: %v", err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })
	return brk
}

func publish(t *testing.T, brk broker.Broker, topic string, payload []byte) {
	t.Helper()
	if err := brk.Publish(t.Context(), topic, &broker.Message{Payload: payload}); err != nil {
		t.Fatalf("publish %q: %v", topic, err)
	}
}

func waitForLines(t *testing.T, path string, want int) []map[string]any {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		f, err := os.Open(path)
		if err == nil {
			var records []map[string]any
			sc := bufio.NewScanner(f)
			for sc.Scan() {
				var rec map[string]any
				if err := json.Unmarshal(sc.Bytes(), &rec); err == nil {
					records = append(records, rec)
				}
			}
			_ = f.Close()
			if len(records) >= want {
				return records
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %d lines in %s", want, path)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestDebugSubscriber_WritesJSONLinesFile(t *testing.T) {
	brk := newTestBroker(t)
	sinkPath := filepath.Join(t.TempDir(), "events.jsonl")

	sub := newDebugSubscriber(brk, []string{"test/a", "test/b/>"}, sinkPath, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := sub.Start(t.Context()); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { _ = sub.Stop(context.Background()) })

	publish(t, brk, "test/a", []byte(`{"hello":"world"}`))
	publish(t, brk, "test/b/nested", []byte(`plain text`))
	publish(t, brk, "other/topic", []byte(`should not match`))

	records := waitForLines(t, sinkPath, 2)

	topics := []string{
		records[0]["topic"].(string),
		records[1]["topic"].(string),
	}
	if !slices.Contains(topics, "test/a") || !slices.Contains(topics, "test/b/nested") {
		t.Fatalf("missing expected topics, got %v", topics)
	}

	for _, rec := range records {
		if _, ok := rec["payload"].(string); !ok {
			t.Errorf("expected payload to be a string, got %T", rec["payload"])
		}
		if _, ok := rec["payload_b64"]; ok {
			t.Errorf("unexpected payload_b64 for utf-8 payload")
		}
		if ts, _ := rec["ts"].(string); ts == "" {
			t.Error("expected non-empty ts")
		}
	}

	for _, rec := range records {
		if rec["topic"] == "other/topic" {
			t.Fatal("received a message on an unsubscribed topic")
		}
	}
}

func TestDebugSubscriber_NonUTF8PayloadEncodedAsBase64(t *testing.T) {
	brk := newTestBroker(t)
	sinkPath := filepath.Join(t.TempDir(), "events.jsonl")

	sub := newDebugSubscriber(brk, []string{"bin/test"}, sinkPath, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := sub.Start(t.Context()); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { _ = sub.Stop(context.Background()) })

	publish(t, brk, "bin/test", []byte{0xFF, 0xFE, 0xFD})

	records := waitForLines(t, sinkPath, 1)
	rec := records[0]
	if _, ok := rec["payload"]; ok {
		t.Errorf("non-utf8 payload should not appear under 'payload'; got %v", rec["payload"])
	}
	if _, ok := rec["payload_b64"].(string); !ok {
		t.Errorf("expected payload_b64 string, got %T", rec["payload_b64"])
	}
}

func TestDebugSubscriber_StopIsIdempotentAndFinishesLoop(t *testing.T) {
	brk := newTestBroker(t)
	sub := newDebugSubscriber(brk, []string{"x/y"}, "", slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := sub.Start(t.Context()); err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := sub.Stop(context.Background()); err != nil {
		t.Fatalf("first stop: %v", err)
	}
	if err := sub.Stop(context.Background()); err != nil {
		t.Fatalf("second stop: %v", err)
	}
}

func TestDebugSubscriber_TruncatesSinkFileOnStart(t *testing.T) {
	brk := newTestBroker(t)
	sinkPath := filepath.Join(t.TempDir(), "events.jsonl")
	if err := os.WriteFile(sinkPath, []byte("stale line\n"), 0o644); err != nil {
		t.Fatalf("seed sink: %v", err)
	}

	sub := newDebugSubscriber(brk, []string{"fresh/topic"}, sinkPath, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := sub.Start(t.Context()); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { _ = sub.Stop(context.Background()) })
	info, err := os.Stat(sinkPath)
	if err != nil {
		t.Fatalf("stat sink: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("sink mode = %o, want 600", got)
	}

	publish(t, brk, "fresh/topic", []byte(`{"v":1}`))
	records := waitForLines(t, sinkPath, 1)
	if len(records) != 1 {
		t.Fatalf("expected exactly 1 record after truncation, got %d", len(records))
	}

	raw, _ := os.ReadFile(sinkPath)
	if strings.Contains(string(raw), "stale line") {
		t.Error("expected sink file to be truncated; stale content still present")
	}
}

func TestDebugSubscriber_StartFailsOnBadSinkPath(t *testing.T) {
	brk := newTestBroker(t)
	badPath := filepath.Join(t.TempDir(), "nope", "events.jsonl")

	sub := newDebugSubscriber(brk, []string{"t"}, badPath, slog.New(slog.NewTextHandler(io.Discard, nil)))
	err := sub.Start(t.Context())
	if err == nil {
		_ = sub.Stop(context.Background())
		t.Fatal("expected Start to fail on unreachable sink path")
	}
	if !strings.Contains(err.Error(), "create sink file") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestDebugSubscriber_RejectsSymlinkSink(t *testing.T) {
	brk := newTestBroker(t)
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	if err := os.WriteFile(target, []byte("keep"), 0o600); err != nil {
		t.Fatalf("write target: %v", err)
	}
	sinkPath := filepath.Join(dir, "events.jsonl")
	if err := os.Symlink(target, sinkPath); err != nil {
		t.Fatalf("create symlink: %v", err)
	}

	sub := newDebugSubscriber(brk, []string{"t"}, sinkPath, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := sub.Start(t.Context()); err == nil {
		t.Fatal("expected Start to reject a symlink sink")
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read target: %v", err)
	}
	if string(got) != "keep" {
		t.Fatalf("symlink target changed: %q", got)
	}
}

func TestDebugSubscriber_NilLoggerUsesSlogDefault(t *testing.T) {
	brk := newTestBroker(t)

	sub := newDebugSubscriber(brk, []string{"nil/log"}, "", nil)
	if sub.logger == nil {
		t.Fatal("expected logger to default to slog.Default, got nil")
	}
	if err := sub.Init(t.Context(), nil); err != nil {
		t.Errorf("Init: %v", err)
	}
	if err := sub.Health(); err != nil {
		t.Errorf("Health: %v", err)
	}
}

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

func TestDebugSubscriber_StderrPathAndUserProperties(t *testing.T) {
	brk := newTestBroker(t)

	buf := &syncBuffer{}
	logger := slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	sub := newDebugSubscriber(brk, []string{"up/test"}, "", logger)
	if err := sub.Start(t.Context()); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { _ = sub.Stop(context.Background()) })

	msg := &broker.Message{
		Payload:        []byte(`{"k":"v"}`),
		UserProperties: map[string]string{"traceId": "abc-123"},
	}
	if err := brk.Publish(t.Context(), "up/test", msg); err != nil {
		t.Fatalf("publish: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		out := buf.String()
		if strings.Contains(out, "msg=event") && strings.Contains(out, "topic=up/test") {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for stderr event line; got: %q", out)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestDebugSubscriber_StopReturnsNilEvenIfSinkAlreadyClosed(t *testing.T) {
	brk := newTestBroker(t)
	sinkPath := filepath.Join(t.TempDir(), "events.jsonl")

	sub := newDebugSubscriber(brk, []string{"x"}, sinkPath, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := sub.Start(t.Context()); err != nil {
		t.Fatalf("start: %v", err)
	}

	_ = sub.sinkFile.Close()

	if err := sub.Stop(context.Background()); err != nil {
		t.Errorf("Stop after pre-close: %v", err)
	}
}

// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package productanalytics

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func sampleEvent() Event {
	return Event{
		EventID:       clientUUID,
		DistinctID:    "install-1",
		SessionID:     clientUUID,
		PublisherID:   "223e4567-e89b-12d3-a456-426614174000",
		Seq:           7,
		Name:          "page_view",
		OccurredAt:    time.Date(2026, 8, 6, 1, 2, 3, 400_000_000, time.UTC),
		ReceivedAt:    time.Date(2026, 8, 6, 1, 2, 4, 0, time.UTC),
		SchemaVersion: EventSchemaVersion,
		Properties:    map[string]any{"path": "/agents", "interface": "ui"},
	}
}

func decodeLines(t *testing.T, raw string) []map[string]any {
	t.Helper()
	var out []map[string]any
	scanner := bufio.NewScanner(strings.NewReader(raw))
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal(line, &rec); err != nil {
			t.Fatalf("line is not valid JSON (%q): %v", line, err)
		}
		out = append(out, rec)
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestDebugSinkWritesOneJSONLinePerEvent(t *testing.T) {
	var buf bytes.Buffer
	sink := NewDebugSink(&buf)
	if err := sink.Send(context.Background(), []Event{sampleEvent(), sampleEvent()}); err != nil {
		t.Fatal(err)
	}
	lines := decodeLines(t, buf.String())
	if len(lines) != 2 {
		t.Fatalf("wrote %d lines, want 2", len(lines))
	}
}

func TestDebugSinkEmitsTheFullEnvelopeAndProperties(t *testing.T) {
	var buf bytes.Buffer
	sink := NewDebugSink(&buf)
	if err := sink.Send(context.Background(), []Event{sampleEvent()}); err != nil {
		t.Fatal(err)
	}
	rec := decodeLines(t, buf.String())[0]

	want := map[string]any{
		"eventId":       clientUUID,
		"distinctId":    "install-1",
		"sessionId":     clientUUID,
		"publisherId":   "223e4567-e89b-12d3-a456-426614174000",
		"seq":           float64(7),
		"name":          "page_view",
		"occurredAt":    "2026-08-06T01:02:03.400Z",
		"receivedAt":    "2026-08-06T01:02:04.000Z",
		"schemaVersion": float64(EventSchemaVersion),
	}
	for key, expected := range want {
		if rec[key] != expected {
			t.Errorf("%s = %#v, want %#v", key, rec[key], expected)
		}
	}
	props, ok := rec["properties"].(map[string]any)
	if !ok {
		t.Fatalf("properties = %#v, want an object", rec["properties"])
	}
	if props["path"] != "/agents" || props["interface"] != "ui" {
		t.Errorf("properties = %#v, want the scrubbed path and interface", props)
	}
}

func TestDebugSinkOnlyEmitsWhatTheServiceScrubbed(t *testing.T) {
	var buf bytes.Buffer
	s := NewService(Options{
		Sink:     NewDebugSink(&buf),
		Consent:  allowConsent,
		Identity: fixedIdentity("install-1"),
		Config:   ServiceConfig{BatchSize: 1000},
	})
	s.Enqueue(context.Background(), SurfaceServer, []RawEvent{{
		Name:       "model:configured",
		Properties: map[string]any{"provider": "openai", "apiKey": "sk-secret", "model": "gpt-4 for john.doe@acme.com"},
	}})
	s.flush(context.Background())

	line := buf.String()
	if strings.Contains(line, "sk-secret") {
		t.Error("a property outside the allow-list reached the debug sink")
	}
	if strings.Contains(line, "john.doe@acme.com") {
		t.Error("an e-mail address survived into the debug sink")
	}
	if !strings.Contains(line, "openai") {
		t.Errorf("the approved property is missing, so the sink proves nothing: %s", line)
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("disk gone") }

func TestDebugSinkReportsAWriteFailureAsTerminal(t *testing.T) {
	sink := NewDebugSink(failingWriter{})
	err := sink.Send(context.Background(), []Event{sampleEvent()})
	if err == nil {
		t.Fatal("a write failure must surface, not be swallowed")
	}
	if IsRetryable(err) {
		t.Error("a broken debug writer cannot be fixed by a retry, so the batch must be abandoned")
	}
}

func TestDebugSinkIsSafeForConcurrentUse(t *testing.T) {
	var buf bytes.Buffer
	sink := NewDebugSink(&buf)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 25; j++ {
				if err := sink.Send(context.Background(), []Event{sampleEvent()}); err != nil {
					t.Error(err)
					return
				}
			}
		}()
	}
	wg.Wait()
	if got := len(decodeLines(t, buf.String())); got != 200 {
		t.Errorf("wrote %d lines, want 200 with no interleaving", got)
	}
}

func TestDebugSinkFromEnvWritesToTheNamedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.ndjson")
	t.Setenv(EnvDebugSinkFile, path)

	sink, err := NewDebugSinkFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if sink == nil {
		t.Fatal("the file env var should switch the debug sink on")
	}
	if sendErr := sink.Send(context.Background(), []Event{sampleEvent()}); sendErr != nil {
		t.Fatal(sendErr)
	}
	if flushErr := sink.Flush(context.Background()); flushErr != nil {
		t.Fatal(flushErr)
	}
	if closeErr := sink.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(decodeLines(t, string(data))); got != 1 {
		t.Errorf("file holds %d lines, want 1", got)
	}
}

func TestDebugSinkFromEnvIsOffByDefault(t *testing.T) {
	t.Setenv(EnvDebugSinkFile, "")
	t.Setenv(EnvDebugSink, "")
	sink, err := NewDebugSinkFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if sink != nil {
		t.Error("the debug sink must stay off unless an env var asks for it")
	}
}

func TestDebugSinkFromEnvHonoursTheOnOffVar(t *testing.T) {
	t.Setenv(EnvDebugSinkFile, "")
	t.Setenv(EnvDebugSink, "true")
	sink, err := NewDebugSinkFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if sink == nil {
		t.Fatal("the on/off env var should switch the debug sink on")
	}
	if sink.w != os.Stderr {
		t.Error("the on/off form writes to stderr, since no path was named")
	}
	if err := sink.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestDebugSinkFromEnvReportsAnUnparseableToggle(t *testing.T) {
	for _, raw := range []string{"yes", "on", "enabled", "1x"} {
		t.Run(raw, func(t *testing.T) {
			t.Setenv(EnvDebugSinkFile, "")
			t.Setenv(EnvDebugSink, raw)
			sink, err := NewDebugSinkFromEnv()
			if err == nil {
				t.Fatalf("%q must be reported, not read as off", raw)
			}
			if sink != nil {
				t.Error("no sink should be returned alongside the error")
			}
		})
	}
}

func TestDebugSinkFromEnvReportsAnUnopenableFile(t *testing.T) {
	t.Setenv(EnvDebugSinkFile, filepath.Join(t.TempDir(), "no-such-dir", "events.ndjson"))
	sink, err := NewDebugSinkFromEnv()
	if err == nil {
		t.Fatal("an unopenable path must be reported so the caller can fall back")
	}
	if sink != nil {
		t.Error("no sink should be returned alongside the error")
	}
}

func TestDebugSinkOpenErrorNamesTheEnvVarNotThePath(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(EnvDebugSinkFile, filepath.Join(dir, "no-such-dir", "events.ndjson"))

	sink, err := NewDebugSinkFromEnv()
	if err == nil {
		t.Fatal("an unopenable path must be reported")
	}
	if sink != nil {
		t.Error("no sink should be returned alongside the error")
	}
	if strings.Contains(err.Error(), dir) {
		t.Errorf("the open error carries the file path: %v", err)
	}
	if !strings.Contains(err.Error(), EnvDebugSinkFile) {
		t.Errorf("the open error should name the env var that configured the sink: %v", err)
	}
}

func TestDebugSinkIOErrorsNameTheEnvVarNotThePath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.ndjson")
	t.Setenv(EnvDebugSinkFile, path)
	sink, err := NewDebugSinkFromEnv()
	if err != nil {
		t.Fatal(err)
	}

	if closeErr := sink.w.(*os.File).Close(); closeErr != nil {
		t.Fatal(closeErr)
	}

	operations := []struct {
		name string
		run  func() error
	}{
		{"send", func() error { return sink.Send(context.Background(), []Event{sampleEvent()}) }},
		{"flush", func() error { return sink.Flush(context.Background()) }},
		{"close", sink.Close},
	}
	for _, op := range operations {
		t.Run(op.name, func(t *testing.T) {
			opErr := op.run()
			if opErr == nil {
				t.Fatal("operating on a closed file must fail, or this proves nothing")
			}
			if strings.Contains(opErr.Error(), dir) {
				t.Errorf("the error carries the file path: %v", opErr)
			}
			if !strings.Contains(opErr.Error(), EnvDebugSinkFile) {
				t.Errorf("the error should name the env var that configured the sink: %v", opErr)
			}
		})
	}
}

func TestDebugSinkSurvivesUseAfterClose(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.ndjson")
	t.Setenv(EnvDebugSinkFile, path)
	sink, err := NewDebugSinkFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if err := sink.Close(); err != nil {
		t.Fatal(err)
	}
	if err := sink.Close(); err != nil {
		t.Fatalf("second Close returned %v, want nil", err)
	}
	if err := sink.Send(context.Background(), []Event{sampleEvent()}); err != nil {
		t.Fatalf("Send after Close returned %v, want nil", err)
	}
}

func TestFlushLeavesACallerOwnedFileAlone(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "caller-owned-*.ndjson")
	if err != nil {
		t.Fatal(err)
	}
	sink := NewDebugSink(f)
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	if err := sink.Flush(context.Background()); err != nil {
		t.Errorf("Flush = %v, want nil: this sink did not open the file and must not sync it", err)
	}
}

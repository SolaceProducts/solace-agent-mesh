// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package broker

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestTraceWriter_BasicWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "trace.jsonl")
	tw, err := NewTraceWriter(path, nil, false)
	if err != nil {
		t.Fatal(err)
	}

	tw.Trace(TraceRecord{
		Timestamp:      time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		Type:           "message",
		SourceClientID: "client-1",
		Topic:          "sam/agents/test/request",
		Payload:        map[string]any{"hello": "world"},
		MatchingQueues: []string{"queue-a", "queue-b"},
	})
	tw.Trace(TraceRecord{
		Timestamp:      time.Date(2026, 1, 1, 0, 0, 1, 0, time.UTC),
		Type:           "message",
		SourceClientID: "client-2",
		Topic:          "sam/agents/test/response",
		Payload:        "plain string",
		MatchingQueues: []string{"queue-c"},
	})

	tw.Close()

	records := readTraceFile(t, path)
	if len(records) != 2 {
		t.Fatalf("expected 2 records, got %d", len(records))
	}
	if records[0].Topic != "sam/agents/test/request" {
		t.Errorf("record[0].Topic = %q, want %q", records[0].Topic, "sam/agents/test/request")
	}
	if records[0].SourceClientID != "client-1" {
		t.Errorf("record[0].SourceClientID = %q, want %q", records[0].SourceClientID, "client-1")
	}
	if len(records[0].MatchingQueues) != 2 {
		t.Errorf("record[0].MatchingQueues len = %d, want 2", len(records[0].MatchingQueues))
	}
	if records[1].Topic != "sam/agents/test/response" {
		t.Errorf("record[1].Topic = %q, want %q", records[1].Topic, "sam/agents/test/response")
	}
}

func TestTraceWriter_FilterMatchesOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "trace.jsonl")
	tw, err := NewTraceWriter(path, []string{"sam/agents/*/request"}, false)
	if err != nil {
		t.Fatal(err)
	}

	tw.Trace(TraceRecord{
		Timestamp:      time.Now(),
		Type:           "message",
		SourceClientID: "c1",
		Topic:          "sam/agents/web/request",
	})
	tw.Close()

	if !tw.matchesFilter("sam/agents/web/request") {
		t.Error("expected matchesFilter to return true for matching topic")
	}
	if tw.matchesFilter("sam/agents/web/response") {
		t.Error("expected matchesFilter to return false for non-matching topic")
	}
	if tw.matchesFilter("sam/other/topic") {
		t.Error("expected matchesFilter to return false for non-matching topic")
	}
}

func TestTraceWriter_NoFiltersMatchesAll(t *testing.T) {
	path := filepath.Join(t.TempDir(), "trace.jsonl")
	tw, err := NewTraceWriter(path, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	defer tw.Close()

	if !tw.matchesFilter("any/topic/at/all") {
		t.Error("expected matchesFilter with no filters to return true")
	}
}

func TestTraceWriter_MultipleFilters(t *testing.T) {
	path := filepath.Join(t.TempDir(), "trace.jsonl")
	tw, err := NewTraceWriter(path, []string{"sam/agents/>", "sam/gateway/>"}, false)
	if err != nil {
		t.Fatal(err)
	}
	defer tw.Close()

	if !tw.matchesFilter("sam/agents/web/request") {
		t.Error("expected match on first filter")
	}
	if !tw.matchesFilter("sam/gateway/auth/token") {
		t.Error("expected match on second filter")
	}
	if tw.matchesFilter("sam/workflow/step1") {
		t.Error("expected no match on unrelated topic")
	}
}

func TestTraceWriter_AckTracing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "trace.jsonl")
	tw, err := NewTraceWriter(path, nil, true)
	if err != nil {
		t.Fatal(err)
	}

	if !tw.TraceAcksEnabled() {
		t.Error("expected TraceAcksEnabled to be true")
	}

	tw.Trace(TraceRecord{
		Timestamp:      time.Now(),
		Type:           "ack",
		SourceClientID: "client-1",
		MessageID:      "msg-abc-123",
	})
	tw.Close()

	records := readTraceFile(t, path)
	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}
	if records[0].Type != "ack" {
		t.Errorf("record.Type = %q, want %q", records[0].Type, "ack")
	}
	if records[0].MessageID != "msg-abc-123" {
		t.Errorf("record.MessageID = %q, want %q", records[0].MessageID, "msg-abc-123")
	}
}

func TestTraceWriter_AckTracingDisabled(t *testing.T) {
	path := filepath.Join(t.TempDir(), "trace.jsonl")
	tw, err := NewTraceWriter(path, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	defer tw.Close()

	if tw.TraceAcksEnabled() {
		t.Error("expected TraceAcksEnabled to be false")
	}
}

func TestTraceWriter_Backpressure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "trace.jsonl")
	tw, err := NewTraceWriter(path, nil, false)
	if err != nil {
		t.Fatal(err)
	}

	total := traceChannelSize + 100
	done := make(chan struct{})
	go func() {
		for range total {
			tw.Trace(TraceRecord{
				Timestamp:      time.Now(),
				Type:           "message",
				SourceClientID: "stress",
				Topic:          "test/backpressure",
			})
		}
		close(done)
	}()

	<-done
	tw.Close()

	records := readTraceFile(t, path)
	if len(records) != total {
		t.Errorf("expected %d records, got %d (records were lost)", total, len(records))
	}
}

func TestTraceWriter_ServerIntegration(t *testing.T) {
	logger := testLogger()
	tracePath := filepath.Join(t.TempDir(), "trace.jsonl")

	tracer, err := NewTraceWriter(tracePath, nil, false)
	if err != nil {
		t.Fatal(err)
	}

	server := NewDevBrokerServer(nil, logger)
	server.SetTracer(tracer)
	addr, err := server.Start(":0")
	if err != nil {
		t.Fatal(err)
	}

	subscriber := NewNetworkDevBroker(addr, "sub-client", "sub-queue", logger)
	subscriber.SetInitialSubscriptions([]string{"test/>"})
	must(t, subscriber.Connect(context.Background()))

	publisher := NewNetworkDevBroker(addr, "pub-client", "pub-queue", logger)
	must(t, publisher.Connect(context.Background()))

	must(t, publisher.Publish(context.Background(), "test/hello", &Message{
		Payload:        []byte(`{"greeting":"hello"}`),
		UserProperties: map[string]string{"key": "value"},
	}))
	must(t, publisher.Publish(context.Background(), "test/world", &Message{
		Payload: []byte(`"just a string"`),
	}))

	for i := range 2 {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_, err := subscriber.Receive(ctx, nil)
		cancel()
		if err != nil {
			t.Fatalf("receive %d: %v", i, err)
		}
	}

	_ = subscriber.Disconnect(context.Background())
	_ = publisher.Disconnect(context.Background())

	_ = server.Stop()

	records := readTraceFile(t, tracePath)

	if len(records) != 2 {
		t.Fatalf("expected 2 message records, got %d", len(records))
	}

	if records[0].SourceClientID != "pub-client" {
		t.Errorf("record[0].SourceClientID = %q, want %q", records[0].SourceClientID, "pub-client")
	}
	if records[0].Topic != "test/hello" {
		t.Errorf("record[0].Topic = %q, want %q", records[0].Topic, "test/hello")
	}
	if records[0].Type != "message" {
		t.Errorf("record[0].Type = %q, want %q", records[0].Type, "message")
	}
	if len(records[0].MatchingQueues) == 0 {
		t.Error("record[0].MatchingQueues should not be empty")
	}
	if v, ok := records[0].UserProperties["key"]; !ok || v != "value" {
		t.Errorf("record[0].UserProperties[\"key\"] = %v, want \"value\"", records[0].UserProperties["key"])
	}

	if records[1].Topic != "test/world" {
		t.Errorf("record[1].Topic = %q, want %q", records[1].Topic, "test/world")
	}
}

func TestTraceWriter_ServerFilterIntegration(t *testing.T) {
	logger := testLogger()
	tracePath := filepath.Join(t.TempDir(), "trace.jsonl")

	tracer, err := NewTraceWriter(tracePath, []string{"test/traced/*"}, false)
	if err != nil {
		t.Fatal(err)
	}

	server := NewDevBrokerServer(nil, logger)
	server.SetTracer(tracer)
	addr, err := server.Start(":0")
	if err != nil {
		t.Fatal(err)
	}

	subscriber := NewNetworkDevBroker(addr, "sub", "sq", logger)
	subscriber.SetInitialSubscriptions([]string{"test/>"})
	must(t, subscriber.Connect(context.Background()))

	publisher := NewNetworkDevBroker(addr, "pub", "pq", logger)
	must(t, publisher.Connect(context.Background()))

	must(t, publisher.Publish(context.Background(), "test/traced/yes", &Message{
		Payload: []byte(`"traced"`),
	}))
	must(t, publisher.Publish(context.Background(), "test/untraced/no", &Message{
		Payload: []byte(`"not traced"`),
	}))

	for i := range 2 {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_, err := subscriber.Receive(ctx, nil)
		cancel()
		if err != nil {
			t.Fatalf("receive %d: %v", i, err)
		}
	}

	_ = subscriber.Disconnect(context.Background())
	_ = publisher.Disconnect(context.Background())
	_ = server.Stop()

	records := readTraceFile(t, tracePath)
	if len(records) != 1 {
		t.Fatalf("expected 1 traced record, got %d", len(records))
	}
	if records[0].Topic != "test/traced/yes" {
		t.Errorf("traced record topic = %q, want %q", records[0].Topic, "test/traced/yes")
	}
}

func readTraceFile(t *testing.T, path string) []TraceRecord {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()

	var records []TraceRecord
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var rec TraceRecord
		if err := json.Unmarshal(scanner.Bytes(), &rec); err != nil {
			t.Fatalf("invalid JSON line: %v", err)
		}
		records = append(records, rec)
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return records
}

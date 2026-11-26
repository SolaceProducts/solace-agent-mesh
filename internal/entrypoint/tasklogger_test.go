// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/observability"
	"github.com/google/uuid"
)

func TestTruncateRequestText(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"short ascii unchanged", "hello", "hello"},
		{"short multibyte unchanged", "héllo 世界", "héllo 世界"},
		{"empty", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := truncateRequestText(tc.in); got != tc.want {
				t.Errorf("truncateRequestText(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}

	t.Run("multibyte truncation is rune-safe and valid", func(t *testing.T) {
		in := strings.Repeat("世", maxInitialRequestTextLen+1)
		got := truncateRequestText(in)
		if !utf8.ValidString(got) {
			t.Fatalf("result is not valid UTF-8")
		}
		if n := utf8.RuneCountInString(got); n != maxInitialRequestTextLen {
			t.Errorf("rune count = %d, want %d", n, maxInitialRequestTextLen)
		}
		if utf8.ValidString(in[:maxInitialRequestTextLen]) {
			t.Errorf("test input no longer exercises the byte-split bug; pick input that splits a rune at the cap")
		}
		if strings.ContainsRune(got, '�') {
			t.Errorf("rune-safe truncation must not introduce U+FFFD: %q", got)
		}
	})

	t.Run("already-invalid input coerced to valid", func(t *testing.T) {
		in := "ab\xe2\x28cd"
		got := truncateRequestText(in)
		if !utf8.ValidString(got) {
			t.Errorf("invalid input not coerced to valid UTF-8: %q", got)
		}
	})
}

func tid(slug string) string {
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte("gateway-test:"+slug)).String() //nolint:forbidigo // deterministic test ID
}

type mockBroker struct {
	queues map[string]*mockQueue
	mu     sync.Mutex
}

func newMockBroker() *mockBroker {
	return &mockBroker{queues: make(map[string]*mockQueue)}
}

func (b *mockBroker) Connect(context.Context) error                                    { return nil }
func (b *mockBroker) Disconnect(context.Context) error                                 { return nil }
func (b *mockBroker) IsConnected() bool                                                { return true }
func (b *mockBroker) Subscribe(broker.Queue, string) error                             { return nil }
func (b *mockBroker) Unsubscribe(broker.Queue, string) error                           { return nil }
func (b *mockBroker) Publish(context.Context, string, *broker.Message) error           { return nil }
func (b *mockBroker) PublishGuaranteed(context.Context, string, *broker.Message) error { return nil }

func (b *mockBroker) CreateQueue(name string, _ broker.QueueOptions) (broker.Queue, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	q := &mockQueue{name: name, ch: make(chan *broker.Message, 100)}
	b.queues[name] = q
	return q, nil
}

func (b *mockBroker) CreateTemporaryQueue() (broker.Queue, error) { return nil, nil }
func (b *mockBroker) RequestReplyInbox(context.Context, string, *broker.Message, time.Duration) (*broker.Message, error) {
	return nil, nil
}
func (b *mockBroker) CreateDirectSubscription() (broker.Queue, error) {
	return b.CreateQueue("temp", broker.QueueOptions{})
}

func (b *mockBroker) DeleteQueue(name string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if q, ok := b.queues[name]; ok {
		close(q.ch)
		delete(b.queues, name)
	}
	return nil
}

func (b *mockBroker) DeprovisionQueue(name string) error { return b.DeleteQueue(name) }

func (b *mockBroker) Receive(ctx context.Context, q broker.Queue) (*broker.Message, error) {
	mq := q.(*mockQueue)
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case msg, ok := <-mq.ch:
		if !ok {
			return nil, ctx.Err()
		}
		return msg, nil
	}
}

func (b *mockBroker) Request(context.Context, string, *broker.Message, time.Duration) (*broker.Message, error) {
	return nil, nil
}

func (b *mockBroker) SendToQueue(name string, msg *broker.Message) {
	b.mu.Lock()
	q, ok := b.queues[name]
	b.mu.Unlock()
	if ok {
		q.ch <- msg
	}
}

type mockQueue struct {
	name string
	ch   chan *broker.Message
}

func (q *mockQueue) Name() string               { return q.name }
func (q *mockQueue) IsDirectSubscription() bool { return false }

func TestParseTaskLoggerConfig_Defaults(t *testing.T) {
	cfg := ParseTaskLoggerConfig(nil, 600)

	if cfg.Enabled {
		t.Error("expected Enabled=false by default")
	}
	if !cfg.LogStatusUpdates {
		t.Error("expected LogStatusUpdates=true by default")
	}
	if !cfg.LogArtifactEvents {
		t.Error("expected LogArtifactEvents=true by default")
	}
	if !cfg.LogFileParts {
		t.Error("expected LogFileParts=true by default")
	}
	if cfg.MaxFilePartSizeBytes != 102400 {
		t.Errorf("MaxFilePartSizeBytes=%d, want 102400", cfg.MaxFilePartSizeBytes)
	}
	if cfg.QueueSize != 600 {
		t.Errorf("QueueSize=%d, want 600", cfg.QueueSize)
	}
	if cfg.HybridBufferEnabled {
		t.Error("expected HybridBufferEnabled=false by default")
	}
	if cfg.HybridFlushThreshold != 10 {
		t.Errorf("HybridFlushThreshold=%d, want 10", cfg.HybridFlushThreshold)
	}
}

func TestParseTaskLoggerConfig_Override(t *testing.T) {
	raw := map[string]any{
		"enabled":                  true,
		"log_status_updates":       false,
		"log_artifact_events":      false,
		"log_file_parts":           false,
		"max_file_part_size_bytes": 500,
		"hybrid_buffer": map[string]any{
			"enabled":         true,
			"flush_threshold": 20,
		},
	}
	cfg := ParseTaskLoggerConfig(raw, 1000)

	if !cfg.Enabled {
		t.Error("expected Enabled=true")
	}
	if cfg.LogStatusUpdates {
		t.Error("expected LogStatusUpdates=false")
	}
	if cfg.LogArtifactEvents {
		t.Error("expected LogArtifactEvents=false")
	}
	if cfg.LogFileParts {
		t.Error("expected LogFileParts=false")
	}
	if cfg.MaxFilePartSizeBytes != 500 {
		t.Errorf("MaxFilePartSizeBytes=%d, want 500", cfg.MaxFilePartSizeBytes)
	}
	if cfg.QueueSize != 1000 {
		t.Errorf("QueueSize=%d, want 1000", cfg.QueueSize)
	}
	if !cfg.HybridBufferEnabled {
		t.Error("expected HybridBufferEnabled=true")
	}
	if cfg.HybridFlushThreshold != 20 {
		t.Errorf("HybridFlushThreshold=%d, want 20", cfg.HybridFlushThreshold)
	}
}

func TestShouldLog(t *testing.T) {
	tests := []struct {
		name      string
		cfg       TaskLoggerConfig
		eventKind string
		want      bool
	}{
		{"status allowed", TaskLoggerConfig{LogStatusUpdates: true, LogArtifactEvents: true}, "status-update", true},
		{"status filtered", TaskLoggerConfig{LogStatusUpdates: false, LogArtifactEvents: true}, "status-update", false},
		{"artifact allowed", TaskLoggerConfig{LogStatusUpdates: true, LogArtifactEvents: true}, "artifact-update", true},
		{"artifact filtered", TaskLoggerConfig{LogStatusUpdates: true, LogArtifactEvents: false}, "artifact-update", false},
		{"unknown kind allowed", TaskLoggerConfig{LogStatusUpdates: false, LogArtifactEvents: false}, "something-else", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &TaskLoggerService{cfg: &tt.cfg}
			if got := s.shouldLog(tt.eventKind); got != tt.want {
				t.Errorf("shouldLog(%q) = %v, want %v", tt.eventKind, got, tt.want)
			}
		})
	}
}

func TestSanitizePayload_StripFileParts(t *testing.T) {
	s := &TaskLoggerService{cfg: &TaskLoggerConfig{
		LogFileParts:         false,
		MaxFilePartSizeBytes: 102400,
	}}

	payload := json.RawMessage(`{"result":{"status":{"message":{"parts":[{"kind":"text","text":"hello"},{"kind":"file","file":{"bytes":"dGVzdA=="}}]}}}}`)
	sanitized := s.sanitizePayload(payload)

	var result map[string]any
	if err := json.Unmarshal(sanitized, &result); err != nil {
		t.Fatal(err)
	}

	parts := result["result"].(map[string]any)["status"].(map[string]any)["message"].(map[string]any)["parts"].([]any)
	if len(parts) != 1 {
		t.Errorf("expected 1 part (file stripped), got %d", len(parts))
	}
	if parts[0].(map[string]any)["kind"] != "text" {
		t.Error("remaining part should be text")
	}
}

func TestSanitizePayload_RedactsConversationContext(t *testing.T) {
	s := &TaskLoggerService{cfg: &TaskLoggerConfig{LogFileParts: true}}

	transcript := "--- BEGIN SLACK CONTEXT ---\n> <@U1>: secret thread chatter\n--- END SLACK CONTEXT ---"
	payload, err := json.Marshal(map[string]any{
		"params": map[string]any{
			"message": map[string]any{
				"parts":    []any{map[string]any{"kind": "text", "text": "summarize"}},
				"metadata": map[string]any{"conversation_context": transcript, "project_id": "p1"},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	sanitized := s.sanitizePayload(payload)
	if strings.Contains(string(sanitized), "secret thread chatter") || strings.Contains(string(sanitized), "BEGIN SLACK CONTEXT") {
		t.Errorf("conversation_context transcript should be redacted from the stored payload:\n%s", sanitized)
	}

	var got map[string]any
	if err := json.Unmarshal(sanitized, &got); err != nil {
		t.Fatal(err)
	}
	md := got["params"].(map[string]any)["message"].(map[string]any)["metadata"].(map[string]any)
	if md["conversation_context"] != convoContextRedaction {
		t.Errorf("conversation_context = %q, want redaction placeholder", md["conversation_context"])
	}
	if md["project_id"] != "p1" {
		t.Errorf("project_id should be preserved, got %v", md["project_id"])
	}
	if !strings.Contains(string(sanitized), "summarize") {
		t.Error("user text part should be preserved")
	}
}

func TestSanitizePayload_RedactsConversationContextInArray(t *testing.T) {
	s := &TaskLoggerService{cfg: &TaskLoggerConfig{LogFileParts: true}}

	payload, err := json.Marshal(map[string]any{"history": []any{
		map[string]any{"metadata": map[string]any{"conversation_context": "leak A"}},
		map[string]any{"metadata": map[string]any{"conversation_context": "leak B", "keep": "ok"}},
	}})
	if err != nil {
		t.Fatal(err)
	}

	out := string(s.sanitizePayload(payload))
	if strings.Contains(out, "leak A") || strings.Contains(out, "leak B") {
		t.Errorf("array-nested conversation_context not redacted:\n%s", out)
	}
	if !strings.Contains(out, "ok") {
		t.Errorf("sibling field in an array element must survive:\n%s", out)
	}
}

func TestSanitizePayload_TruncateLargeFile(t *testing.T) {
	s := &TaskLoggerService{cfg: &TaskLoggerConfig{
		LogFileParts:         true,
		MaxFilePartSizeBytes: 10,
	}}

	payload := json.RawMessage(`{"result":{"parts":[{"kind":"file","file":{"bytes":"dGVzdGluZyBsYXJnZSBjb250ZW50IGRhdGEgaGVyZQ=="}}]}}`)
	sanitized := s.sanitizePayload(payload)

	var result map[string]any
	if err := json.Unmarshal(sanitized, &result); err != nil {
		t.Fatal(err)
	}

	parts := result["result"].(map[string]any)["parts"].([]any)
	if len(parts) != 1 {
		t.Fatalf("expected 1 part, got %d", len(parts))
	}
	fileMap := parts[0].(map[string]any)["file"].(map[string]any)
	bytesVal := fileMap["bytes"].(string)
	if bytesVal == "dGVzdGluZyBsYXJnZSBjb250ZW50IGRhdGEgaGVyZQ==" {
		t.Error("expected file bytes to be truncated")
	}
	if len(bytesVal) < 10 {
		t.Error("expected truncation marker string")
	}
}

func TestSanitizePayload_SkipsWhenNoFilePartAndUnderTextCap(t *testing.T) {
	s := &TaskLoggerService{cfg: &TaskLoggerConfig{
		LogFileParts:        false,
		MaxPayloadTextBytes: 16384,
	}}

	payload := json.RawMessage(`{"result":{"status":{"message":{"parts":[{"text":"hello","kind":"text"}]}}}}`)
	sanitized := s.sanitizePayload(payload)

	if !bytes.Equal(sanitized, payload) {
		t.Errorf("expected verbatim passthrough (walk skipped), got %s", sanitized)
	}
}

func TestSanitizePayload_CapsLargeTextWithoutFilePart(t *testing.T) {
	const maxText = 64
	s := &TaskLoggerService{cfg: &TaskLoggerConfig{
		LogFileParts:        true,
		MaxPayloadTextBytes: maxText,
	}}

	big := strings.Repeat("A", 500)
	payload := json.RawMessage(`{"result":{"status":{"message":{"parts":[{"kind":"text","text":"` + big + `"}]}}}}`)
	sanitized := s.sanitizePayload(payload)

	if bytes.Equal(sanitized, payload) {
		t.Fatal("expected oversized text to be capped, got verbatim passthrough")
	}
	var result map[string]any
	if err := json.Unmarshal(sanitized, &result); err != nil {
		t.Fatal(err)
	}
	text := result["result"].(map[string]any)["status"].(map[string]any)["message"].(map[string]any)["parts"].([]any)[0].(map[string]any)["text"].(string)
	if len(text) > maxText+len("…[truncated 9999 bytes]") {
		t.Errorf("text not capped near %d bytes: got %d", maxText, len(text))
	}
	if !strings.Contains(text, "truncated") {
		t.Errorf("expected truncation marker, got %q", text)
	}
}

func TestSanitizePayload_PreservesLargeIntegers(t *testing.T) {
	const bigInt = "9007199254740993"
	s := &TaskLoggerService{cfg: &TaskLoggerConfig{
		LogFileParts:        true,
		MaxPayloadTextBytes: 64,
	}}

	payload := json.RawMessage(`{"data":{"job_id":` + bigInt + `,"note":"` + strings.Repeat("A", 500) + `"}}`)
	sanitized := s.sanitizePayload(payload)

	if !strings.Contains(string(sanitized), bigInt) {
		t.Errorf("large integer altered by the sanitize round trip: %s", sanitized)
	}
}

func TestSanitizePayload_CapStaysWithinConfiguredBound(t *testing.T) {
	const maxText = 4096
	s := &TaskLoggerService{cfg: &TaskLoggerConfig{
		LogFileParts:        true,
		MaxPayloadTextBytes: maxText,
	}}

	payload := json.RawMessage(`{"data":{"note":"` + strings.Repeat("A", 50_000) + `"}}`)
	sanitized := s.sanitizePayload(payload)

	var result map[string]any
	if err := json.Unmarshal(sanitized, &result); err != nil {
		t.Fatal(err)
	}
	note := result["data"].(map[string]any)["note"].(string)
	if len(note) > maxText {
		t.Errorf("capped string is %d bytes, over the configured %d", len(note), maxText)
	}
	if !strings.HasPrefix(note, "AAAA") || !strings.Contains(note, "truncated 4") {
		t.Errorf("expected retained content and an accurate marker, got tail %q", note[max(0, len(note)-40):])
	}
}

func TestSanitizePayload_CapsStringsInsideArrays(t *testing.T) {
	const maxText = 64
	s := &TaskLoggerService{cfg: &TaskLoggerConfig{
		LogFileParts:        true,
		MaxPayloadTextBytes: maxText,
	}}

	big := strings.Repeat("A", 500)
	payload := json.RawMessage(`{"data":{"result_data":{"rows":["` + big + `"]}}}`)
	sanitized := s.sanitizePayload(payload)

	var result map[string]any
	if err := json.Unmarshal(sanitized, &result); err != nil {
		t.Fatal(err)
	}
	rows := result["data"].(map[string]any)["result_data"].(map[string]any)["rows"].([]any)
	row := rows[0].(string)
	if len(row) > maxText+len("…[truncated 9999 bytes]") {
		t.Errorf("array element not capped near %d bytes: got %d", maxText, len(row))
	}
	if !strings.Contains(row, "truncated") {
		t.Errorf("expected truncation marker on the array element, got %q", row)
	}
}

func TestSanitizePayload_NoChangeWhenSmall(t *testing.T) {
	s := &TaskLoggerService{cfg: &TaskLoggerConfig{
		LogFileParts:         true,
		MaxFilePartSizeBytes: 102400,
	}}

	payload := json.RawMessage(`{"result":{"parts":[{"kind":"file","file":{"bytes":"dGVzdA=="}}]}}`)
	sanitized := s.sanitizePayload(payload)

	var result map[string]any
	if err := json.Unmarshal(sanitized, &result); err != nil {
		t.Fatal(err)
	}

	parts := result["result"].(map[string]any)["parts"].([]any)
	fileMap := parts[0].(map[string]any)["file"].(map[string]any)
	if fileMap["bytes"] != "dGVzdA==" {
		t.Error("small file should not be modified")
	}
}

func TestSanitizePayload_CapsLargeText(t *testing.T) {
	s := &TaskLoggerService{cfg: &TaskLoggerConfig{
		LogFileParts:         true,
		MaxFilePartSizeBytes: 102400,
		MaxPayloadTextBytes:  32,
	}}

	large := strings.Repeat("A", 5000)
	payload := json.RawMessage(`{"result":{"contextId":"ctx-1","status":{"message":{"parts":[{"kind":"text","text":"` + large + `"}]}}}}`)
	sanitized := s.sanitizePayload(payload)

	var result map[string]any
	if err := json.Unmarshal(sanitized, &result); err != nil {
		t.Fatalf("sanitized payload is not valid JSON: %v", err)
	}
	if result["result"].(map[string]any)["contextId"] != "ctx-1" {
		t.Error("short field contextId should be preserved")
	}
	text := result["result"].(map[string]any)["status"].(map[string]any)["message"].(map[string]any)["parts"].([]any)[0].(map[string]any)["text"].(string)
	if !strings.HasPrefix(text, "AAAA") {
		t.Error("capped text should keep the original prefix")
	}
	if !strings.Contains(text, "truncated") {
		t.Errorf("expected truncation marker, got %q", text)
	}
	if len(text) > s.cfg.MaxPayloadTextBytes+64 {
		t.Errorf("capped text not bounded: %d bytes (cap %d)", len(text), s.cfg.MaxPayloadTextBytes)
	}
}

func textPartPayload(t *testing.T, text string) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(map[string]any{
		"result": map[string]any{
			"status": map[string]any{"message": map[string]any{
				"parts": []any{map[string]any{"kind": "text", "text": text}},
			}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func textPartValue(t *testing.T, sanitized json.RawMessage) string {
	t.Helper()
	var result map[string]any
	if err := json.Unmarshal(sanitized, &result); err != nil {
		t.Fatalf("sanitized payload is not valid JSON: %v", err)
	}
	return result["result"].(map[string]any)["status"].(map[string]any)["message"].(map[string]any)["parts"].([]any)[0].(map[string]any)["text"].(string)
}

func TestSanitizePayload_CapsMultibyteTextSafely(t *testing.T) {
	s := &TaskLoggerService{cfg: &TaskLoggerConfig{LogFileParts: true, MaxFilePartSizeBytes: 102400, MaxPayloadTextBytes: 10}}
	text := textPartValue(t, s.sanitizePayload(textPartPayload(t, strings.Repeat("世", 100))))
	if strings.ContainsRune(text, '�') {
		t.Errorf("capped multibyte text contains a replacement char: %q", text)
	}
	prefix := strings.SplitN(text, "…", 2)[0]
	if len(prefix)%len("世") != 0 {
		t.Errorf("retained prefix split a rune: %d bytes", len(prefix))
	}
}

func TestSanitizePayload_TextCapEdgeCases(t *testing.T) {
	s := &TaskLoggerService{cfg: &TaskLoggerConfig{LogFileParts: true, MaxFilePartSizeBytes: 102400, MaxPayloadTextBytes: 5}}
	if got := textPartValue(t, s.sanitizePayload(textPartPayload(t, "12345"))); got != "12345" {
		t.Errorf("text exactly at cap should be unchanged, got %q", got)
	}

	s = &TaskLoggerService{cfg: &TaskLoggerConfig{LogFileParts: false, MaxFilePartSizeBytes: 102400, MaxPayloadTextBytes: 0}}
	big := strings.Repeat("A", 5000)
	payload := json.RawMessage(`{"result":{"parts":[{"kind":"text","text":"` + big + `"},{"kind":"file","file":{"bytes":"dGVzdA=="}}]}}`)
	var result map[string]any
	if err := json.Unmarshal(s.sanitizePayload(payload), &result); err != nil {
		t.Fatal(err)
	}
	parts := result["result"].(map[string]any)["parts"].([]any)
	if len(parts) != 1 {
		t.Errorf("file sanitization should still run with text cap disabled; parts=%d", len(parts))
	}
	if parts[0].(map[string]any)["text"].(string) != big {
		t.Error("text should be intact when cap disabled")
	}

	s = &TaskLoggerService{cfg: &TaskLoggerConfig{LogFileParts: true, MaxFilePartSizeBytes: 102400, MaxPayloadTextBytes: 16}}
	payload = json.RawMessage(`{"result":{"note":"` + strings.Repeat("x", 4000) + `"}}`)
	if err := json.Unmarshal(s.sanitizePayload(payload), &result); err != nil {
		t.Fatal(err)
	}
	if note := result["result"].(map[string]any)["note"].(string); !strings.Contains(note, "truncated") || len(note) > 16+64 {
		t.Errorf("non-text string field not capped: %d bytes", len(note))
	}
}

func TestSanitizePayload_FilePreservedWhenNoCap(t *testing.T) {
	s := &TaskLoggerService{cfg: &TaskLoggerConfig{LogFileParts: true, MaxFilePartSizeBytes: 0, MaxPayloadTextBytes: 16}}
	fileBytes := strings.Repeat("Zm9v", 2000)
	payload := json.RawMessage(`{"result":{"parts":[{"kind":"file","file":{"bytes":"` + fileBytes + `"}}]}}`)
	var result map[string]any
	if err := json.Unmarshal(s.sanitizePayload(payload), &result); err != nil {
		t.Fatal(err)
	}
	got := result["result"].(map[string]any)["parts"].([]any)[0].(map[string]any)["file"].(map[string]any)["bytes"].(string)
	if got != fileBytes {
		t.Error("file bytes must be preserved when MaxFilePartSizeBytes <= 0, even with text cap on")
	}
}

func TestParseTaskLoggerConfig_MaxPayloadTextBytes(t *testing.T) {
	if def := ParseTaskLoggerConfig(nil, 600); def.MaxPayloadTextBytes != defaultMaxPayloadTextBytes {
		t.Errorf("default MaxPayloadTextBytes = %d, want %d", def.MaxPayloadTextBytes, defaultMaxPayloadTextBytes)
	}
	if got := ParseTaskLoggerConfig(map[string]any{"max_payload_text_bytes": 4096}, 600); got.MaxPayloadTextBytes != 4096 {
		t.Errorf("override MaxPayloadTextBytes = %d, want 4096", got.MaxPayloadTextBytes)
	}
	if got := ParseTaskLoggerConfig(map[string]any{"max_payload_text_bytes": float64(8192)}, 600); got.MaxPayloadTextBytes != 8192 {
		t.Errorf("float64 override MaxPayloadTextBytes = %d, want 8192", got.MaxPayloadTextBytes)
	}
}

func TestTaskLoggerService_EventWithoutUserIsRecordedAsUnknown(t *testing.T) {
	db := store.TestDB(t)
	taskRepo := store.NewTaskRepo(db)
	svc := NewTaskLoggerService(&TaskLoggerConfig{Enabled: true, QueueSize: 100}, taskRepo, store.NewTaskEventRepo(db),
		newMockBroker(), "test-ns", "gw-test", slog.Default())
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	if err := svc.Start(ctx); err != nil {
		t.Fatal(err)
	}

	requestPayload, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": tid("task-nouser"), "method": "message/send",
		"params": map[string]any{"message": map[string]any{"parts": []any{}}},
	})
	svc.LogEvent(TaskLogEvent{
		TaskID: tid("task-nouser"), Topic: "test-ns/a2a/v1/agent/request/my-agent", Direction: "request",
		Payload: requestPayload, IsRequest: true, ExecutionMode: "foreground",
	})
	cancel()
	svc.Stop()

	task, err := taskRepo.GetByID(t.Context(), tid("task-nouser"))
	if err != nil || task == nil {
		t.Fatalf("task = %v, %v; want a row", task, err)
	}
	if task.UserID != store.TaskUserUnknown {
		t.Errorf("task.UserID = %q, want %q", task.UserID, store.TaskUserUnknown)
	}
}

func TestTaskLoggerService_EventFlow(t *testing.T) {
	db := store.TestDB(t)
	taskRepo := store.NewTaskRepo(db)
	eventRepo := store.NewTaskEventRepo(db)
	brk := newMockBroker()

	cfg := &TaskLoggerConfig{
		Enabled:              true,
		LogStatusUpdates:     true,
		LogArtifactEvents:    true,
		LogFileParts:         true,
		MaxFilePartSizeBytes: 102400,
		QueueSize:            100,
	}

	logger := slog.Default()
	svc := NewTaskLoggerService(cfg, taskRepo, eventRepo, brk, "test-ns", "gw-test", logger)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	if err := svc.Start(ctx); err != nil {
		t.Fatal(err)
	}

	requestPayload, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      tid("task-1"),
		"method":  "message/send",
		"params":  map[string]any{"message": map[string]any{"parts": []any{}}},
	})
	svc.LogEvent(TaskLogEvent{
		TaskID:             tid("task-1"),
		UserID:             "user-1",
		SessionID:          tid("sess-1"),
		Topic:              "test-ns/a2a/v1/agent/request/my-agent",
		Direction:          "request",
		Payload:            requestPayload,
		IsRequest:          true,
		InitialRequestText: "Hello agent",
		ExecutionMode:      "foreground",
	})

	statusPayload, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"result": map[string]any{
			"kind":   "status-update",
			"taskId": tid("task-1"),
		},
	})
	svc.LogEvent(TaskLogEvent{
		TaskID:    tid("task-1"),
		UserID:    "user-1",
		Topic:     "test-ns/a2a/v1/gateway/status/gw-test/task-1",
		Direction: "status",
		EventKind: "status-update",
		Payload:   statusPayload,
	})

	finalPayload, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"result": map[string]any{
			"kind": "task",
			"id":   tid("task-1"),
			"status": map[string]any{
				"state": "completed",
			},
			"metadata": map[string]any{
				"token_usage": map[string]any{
					"total_input_tokens":        100,
					"total_output_tokens":       50,
					"total_cached_input_tokens": 10,
				},
			},
		},
	})
	svc.LogEvent(TaskLogEvent{
		TaskID:      tid("task-1"),
		UserID:      "user-1",
		Topic:       "test-ns/a2a/v1/gateway/response/gw-test/task-1",
		Direction:   "response",
		Payload:     finalPayload,
		IsFinal:     true,
		FinalStatus: "completed",
		TokenUsage: &TokenUsageInfo{
			InputTokens:       100,
			OutputTokens:      50,
			CachedInputTokens: 10,
			DetailsJSON:       `{"total_input_tokens":100,"total_output_tokens":50,"total_cached_input_tokens":10}`,
		},
	})

	cancel()
	svc.Stop()

	task, err := taskRepo.GetByID(context.Background(), tid("task-1"))
	if err != nil {
		t.Fatal(err)
	}
	if task == nil {
		t.Fatal("expected task to be created")
	}
	if task.UserID != "user-1" {
		t.Errorf("task.UserID = %s, want user-1", task.UserID)
	}
	if task.ExecutionMode != "foreground" {
		t.Errorf("task.ExecutionMode = %s, want foreground", task.ExecutionMode)
	}
	if task.InitialRequestText == nil || *task.InitialRequestText != "Hello agent" {
		t.Errorf("task.InitialRequestText = %v, want 'Hello agent'", task.InitialRequestText)
	}
	if task.Status == nil || *task.Status != "completed" {
		t.Errorf("task.Status = %v, want 'completed'", task.Status)
	}
	if task.EndTime == nil {
		t.Error("task.EndTime should be set")
	}
	if task.TotalInputTokens == nil || *task.TotalInputTokens != 100 {
		t.Errorf("task.TotalInputTokens = %v, want 100", task.TotalInputTokens)
	}
	if task.TotalOutputTokens == nil || *task.TotalOutputTokens != 50 {
		t.Errorf("task.TotalOutputTokens = %v, want 50", task.TotalOutputTokens)
	}
	if task.SessionID == nil || *task.SessionID != tid("sess-1") {
		t.Errorf("task.SessionID = %v, want 'sess-1'", task.SessionID)
	}

	events, err := eventRepo.GetByTaskID(context.Background(), tid("task-1"))
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 3 {
		t.Fatalf("expected 3 events, got %d", len(events))
	}
	if events[0].Direction != "request" {
		t.Errorf("events[0].Direction = %s, want request", events[0].Direction)
	}
	if events[1].Direction != "status" {
		t.Errorf("events[1].Direction = %s, want status", events[1].Direction)
	}
	if events[2].Direction != "response" {
		t.Errorf("events[2].Direction = %s, want response", events[2].Direction)
	}
}

func TestTaskLoggerService_PlaceholderTask(t *testing.T) {
	db := store.TestDB(t)
	taskRepo := store.NewTaskRepo(db)
	eventRepo := store.NewTaskEventRepo(db)
	brk := newMockBroker()

	cfg := &TaskLoggerConfig{
		Enabled:              true,
		LogStatusUpdates:     true,
		LogArtifactEvents:    true,
		LogFileParts:         true,
		MaxFilePartSizeBytes: 102400,
		QueueSize:            100,
	}

	svc := NewTaskLoggerService(cfg, taskRepo, eventRepo, brk, "test-ns", "gw-test", slog.Default())

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	if err := svc.Start(ctx); err != nil {
		t.Fatal(err)
	}

	statusPayload, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "result": map[string]any{"kind": "status-update"}})
	svc.LogEvent(TaskLogEvent{
		TaskID:    tid("orphan-task"),
		UserID:    "user-1",
		Topic:     "test-ns/a2a/v1/gateway/status/gw-other/orphan-task",
		Direction: "status",
		EventKind: "status-update",
		Payload:   statusPayload,
	})

	cancel()
	svc.Stop()

	task, err := taskRepo.GetByID(context.Background(), tid("orphan-task"))
	if err != nil {
		t.Fatal(err)
	}
	if task == nil {
		t.Fatal("expected placeholder task to be created")
	}
	if task.InitialRequestText == nil || *task.InitialRequestText != "[Task started before logger was active]" {
		t.Errorf("unexpected InitialRequestText: %v", task.InitialRequestText)
	}
}

func TestTaskLoggerService_DuplicateRequestIsIdempotent(t *testing.T) {
	db := store.TestDB(t)
	taskRepo := store.NewTaskRepo(db)
	eventRepo := store.NewTaskEventRepo(db)
	brk := newMockBroker()

	cfg := &TaskLoggerConfig{
		Enabled:              true,
		LogStatusUpdates:     true,
		LogArtifactEvents:    true,
		LogFileParts:         true,
		MaxFilePartSizeBytes: 102400,
		QueueSize:            100,
	}

	svc := NewTaskLoggerService(cfg, taskRepo, eventRepo, brk, "test-ns", "gw-test", slog.Default())

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	if err := svc.Start(ctx); err != nil {
		t.Fatal(err)
	}

	payload, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": "message/send"})
	evt := TaskLogEvent{
		TaskID:             tid("dup-task"),
		UserID:             "user-1",
		SessionID:          tid("dup-sess"),
		Topic:              "test-ns/a2a/v1/agent/request/my-agent",
		Direction:          "request",
		Payload:            payload,
		IsRequest:          true,
		InitialRequestText: "Original request",
		ExecutionMode:      "foreground",
	}
	svc.LogEvent(evt)
	evt.InitialRequestText = "Replay should NOT overwrite this"
	svc.LogEvent(evt)

	cancel()
	svc.Stop()

	task, err := taskRepo.GetByID(context.Background(), tid("dup-task"))
	if err != nil {
		t.Fatal(err)
	}
	if task == nil {
		t.Fatal("expected single task row after duplicate request")
	}
	if task.InitialRequestText == nil || *task.InitialRequestText != "Original request" {
		t.Errorf("InitialRequestText = %v, want 'Original request' (replay must not overwrite)", task.InitialRequestText)
	}
	events, err := eventRepo.GetByTaskID(context.Background(), tid("dup-task"))
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Errorf("expected 2 task_events for the two requests, got %d", len(events))
	}
}

func TestTaskLoggerService_BrokerDispatch(t *testing.T) {
	db := store.TestDB(t)
	taskRepo := store.NewTaskRepo(db)
	eventRepo := store.NewTaskEventRepo(db)
	brk := newMockBroker()

	cfg := &TaskLoggerConfig{
		Enabled:              true,
		LogStatusUpdates:     true,
		LogArtifactEvents:    true,
		LogFileParts:         true,
		MaxFilePartSizeBytes: 102400,
		QueueSize:            100,
	}

	svc := NewTaskLoggerService(cfg, taskRepo, eventRepo, brk, "test-ns", "gw-test", slog.Default())
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	if err := svc.Start(ctx); err != nil {
		t.Fatal(err)
	}

	queueName := "test-ns/q/gdk/task_log/gw-test"

	brk.SendToQueue(queueName, &broker.Message{
		Topic:   "test-ns/a2a/v1/discovery/agentcards/some-agent",
		Payload: []byte(`{"jsonrpc":"2.0","id":"discover-1","result":{}}`),
	})
	brk.SendToQueue(queueName, &broker.Message{
		Topic:   "test-ns/a2a/v1/trust/cards/some-agent",
		Payload: []byte(`{"jsonrpc":"2.0","id":"trust-1","result":{}}`),
	})

	finalPayload, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"result": map[string]any{
			"kind":   "task",
			"id":     tid("agent-resp-task"),
			"status": map[string]any{"state": "completed"},
		},
	})
	brk.SendToQueue(queueName, &broker.Message{
		Topic:   "test-ns/a2a/v1/agent/response/my-agent/" + tid("agent-resp-task"),
		Payload: finalPayload,
	})

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		task, _ := taskRepo.GetByID(context.Background(), tid("agent-resp-task"))
		if task != nil && task.Status != nil && *task.Status == "completed" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	cancel()
	svc.Stop()

	if task, _ := taskRepo.GetByID(context.Background(), "discover-1"); task != nil {
		t.Error("discovery topic should be dropped, but task was created")
	}
	if task, _ := taskRepo.GetByID(context.Background(), "trust-1"); task != nil {
		t.Error("trust topic should be dropped, but task was created")
	}
	task, err := taskRepo.GetByID(context.Background(), tid("agent-resp-task"))
	if err != nil {
		t.Fatal(err)
	}
	if task == nil {
		t.Fatal("expected task from agent/response topic")
	}
	if task.Status == nil || *task.Status != "completed" {
		t.Errorf("agent/response task.Status = %v, want completed", task.Status)
	}
	if task.EndTime == nil {
		t.Error("agent/response should have marked task final (EndTime set)")
	}
}

func TestTaskLoggerService_AgentRequestMethods(t *testing.T) {
	tests := []struct {
		name       string
		method     string
		wantTask   bool
		wantEvents int
	}{
		{name: "message/send is logged as a task", method: "message/send", wantTask: true, wantEvents: 1},
		{name: "message/stream is logged as a task", method: "message/stream", wantTask: true, wantEvents: 1},
		{name: "tasks/cancel is still logged", method: "tasks/cancel", wantTask: true, wantEvents: 1},
		{name: "session/compact extension call is not a task", method: "session/compact"},
		{name: "request without a method is not a task", method: ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			db := store.TestDB(t)
			taskRepo := store.NewTaskRepo(db)
			eventRepo := store.NewTaskEventRepo(db)
			cfg := &TaskLoggerConfig{Enabled: true, LogStatusUpdates: true, QueueSize: 100}
			svc := NewTaskLoggerService(cfg, taskRepo, eventRepo, newMockBroker(), "test-ns", "gw-test", slog.Default())
			if err := svc.Start(t.Context()); err != nil {
				t.Fatal(err)
			}
			taskID := tid("method-request")
			payload, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": taskID, "method": tc.method,
				"params": map[string]any{"sessionId": tid("method-session")}})
			if err != nil {
				t.Fatal(err)
			}
			svc.handleBrokerMessage(&broker.Message{Topic: "test-ns/a2a/v1/agent/request/OrchestratorAgent", Payload: payload})
			svc.Stop()

			task, err := taskRepo.GetByID(t.Context(), taskID)
			if err != nil {
				t.Fatal(err)
			}
			if gotTask := task != nil; gotTask != tc.wantTask {
				t.Errorf("task row exists = %v, want %v", gotTask, tc.wantTask)
			}
			events, err := eventRepo.GetByTaskID(t.Context(), taskID)
			if err != nil {
				t.Fatal(err)
			}
			if len(events) != tc.wantEvents {
				t.Errorf("task_events = %d, want %d", len(events), tc.wantEvents)
			}
		})
	}
}

func TestTaskLoggerService_BackgroundTask(t *testing.T) {
	db := store.TestDB(t)
	taskRepo := store.NewTaskRepo(db)
	eventRepo := store.NewTaskEventRepo(db)
	brk := newMockBroker()

	cfg := &TaskLoggerConfig{
		Enabled:              true,
		LogStatusUpdates:     true,
		LogArtifactEvents:    true,
		LogFileParts:         true,
		MaxFilePartSizeBytes: 102400,
		QueueSize:            100,
	}

	svc := NewTaskLoggerService(cfg, taskRepo, eventRepo, brk, "test-ns", "gw-test", slog.Default())

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	if err := svc.Start(ctx); err != nil {
		t.Fatal(err)
	}

	maxTime := int64(7200000)
	payload, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": tid("bg-task")})
	svc.LogEvent(TaskLogEvent{
		TaskID:             tid("bg-task"),
		UserID:             "user-1",
		SessionID:          tid("sess-bg"),
		Topic:              "test-ns/a2a/v1/agent/request/my-agent",
		Direction:          "request",
		Payload:            payload,
		IsRequest:          true,
		InitialRequestText: "Background task",
		ExecutionMode:      "background",
		BackgroundEnabled:  true,
		MaxExecutionTimeMs: &maxTime,
	})

	cancel()
	svc.Stop()

	task, err := taskRepo.GetByID(context.Background(), tid("bg-task"))
	if err != nil {
		t.Fatal(err)
	}
	if task == nil {
		t.Fatal("expected task")
	}
	if task.ExecutionMode != "background" {
		t.Errorf("ExecutionMode = %s, want background", task.ExecutionMode)
	}
	if !task.BackgroundExecutionEnabled {
		t.Error("expected BackgroundExecutionEnabled=true")
	}
	if task.MaxExecutionTimeMs == nil || *task.MaxExecutionTimeMs != 7200000 {
		t.Errorf("MaxExecutionTimeMs = %v, want 7200000", task.MaxExecutionTimeMs)
	}
}

func TestTaskLoggerService_FinalizeEmitsTerminalMetricOncePerTransition(t *testing.T) {
	rec := observability.NewRecordingMetrics()

	svc, _, _, stop := makeTaskLogger(t)
	svc.metrics = rec
	taskID := tid("finalize-metric")

	requestPayload, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": taskID, "method": "message/send",
		"params": map[string]any{"message": map[string]any{
			"parts": []any{map[string]any{"kind": "text", "text": "hi"}},
		}},
	})
	svc.LogEvent(TaskLogEvent{
		TaskID: taskID, Topic: "test-ns/a2a/v1/agent/request/x", Direction: "request",
		Payload: requestPayload, IsRequest: true, ExecutionMode: "foreground",
	})

	completedPayload, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "result": map[string]any{"kind": "task"}})
	final := TaskLogEvent{
		TaskID: taskID, Topic: "test-ns/a2a/v1/gateway/response/gw-test/x",
		Direction: "response", Payload: completedPayload, IsFinal: true, FinalStatus: "completed",
	}
	svc.LogEvent(final)
	svc.LogEvent(final)

	stop()

	if got := rec.CounterSum(MetricTaskTerminal, "status", "completed"); got != 1 {
		t.Errorf("completed counter = %d, want 1 (one emission per terminal transition)", got)
	}
	if got := rec.CounterSum(MetricTaskTerminal); got != 1 {
		t.Errorf("total terminal counter = %d, want 1 (duplicate final must not re-count)", got)
	}
}

func TestTaskLoggerService_SupersedeReaperTimeoutDoesNotDoubleCount(t *testing.T) {
	rec := observability.NewRecordingMetrics()

	svc, taskRepo, _, stop := makeTaskLogger(t)
	svc.metrics = rec
	defer stop()
	ctx := context.Background()
	taskID := tid("supersede-metric")

	if err := taskRepo.Create(ctx, &store.Task{ID: taskID, UserID: "u1", StartTime: time.Now().UnixMilli(), ExecutionMode: "foreground"}); err != nil {
		t.Fatal(err)
	}
	if _, err := taskRepo.TimeoutTask(ctx, taskID); err != nil {
		t.Fatal(err)
	}

	completedPayload, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "result": map[string]any{"kind": "task"}})
	svc.LogEvent(TaskLogEvent{
		TaskID: taskID, Topic: "test-ns/a2a/v1/gateway/response/gw-test/x",
		Direction: "response", Payload: completedPayload, IsFinal: true, FinalStatus: "completed",
	})
	stop()

	if got := rec.CounterSum(MetricTaskTerminal); got != 0 {
		t.Errorf("terminal counter = %d, want 0 (finalize must not re-count a reaper-timeout supersede)", got)
	}
}

func makeTaskLogger(t *testing.T) (*TaskLoggerService, *store.TaskRepo, *store.TaskEventRepo, func()) {
	t.Helper()
	db := store.TestDB(t)
	taskRepo := store.NewTaskRepo(db)
	eventRepo := store.NewTaskEventRepo(db)
	cfg := &TaskLoggerConfig{
		Enabled:              true,
		LogStatusUpdates:     true,
		LogArtifactEvents:    true,
		LogFileParts:         true,
		MaxFilePartSizeBytes: 102400,
		QueueSize:            16,
	}
	svc := NewTaskLoggerService(cfg, taskRepo, eventRepo, newMockBroker(), "test-ns", "gw-test", slog.Default())
	if err := svc.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	return svc, taskRepo, eventRepo, func() { svc.Stop() }
}

func TestTaskLoggerService_LogEventAfterStopNoPanic(t *testing.T) {
	svc, _, _, stop := makeTaskLogger(t)
	stop()

	defer func() {
		if r := recover(); r != nil {
			t.Errorf("LogEvent after Stop panicked: %v", r)
		}
	}()

	svc.LogEvent(TaskLogEvent{
		TaskID:    tid("post-stop"),
		Direction: "error",
		IsFinal:   true,
	})
}

func TestTaskLoggerService_FinalizeIdempotentOnTerminalRow(t *testing.T) {
	svc, taskRepo, _, stop := makeTaskLogger(t)
	defer stop()

	taskID := tid("idempotent-finalize")
	requestPayload, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": "message/send"})
	svc.LogEvent(TaskLogEvent{
		TaskID: taskID, UserID: "u1", SessionID: tid("idem-sess"),
		Topic: "test-ns/a2a/v1/agent/request/x", Direction: "request",
		Payload: requestPayload, IsRequest: true, ExecutionMode: "foreground",
	})

	completedPayload, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "result": map[string]any{"kind": "task"}})
	svc.LogEvent(TaskLogEvent{
		TaskID:      taskID,
		Topic:       "test-ns/a2a/v1/gateway/response/gw-test/x",
		Direction:   "response",
		Payload:     completedPayload,
		IsFinal:     true,
		FinalStatus: "completed",
		TokenUsage: &TokenUsageInfo{
			InputTokens: 100, OutputTokens: 50, CachedInputTokens: 10,
			DetailsJSON: `{"total_input_tokens":100}`,
		},
	})

	timeoutPayload, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "error": map[string]any{"code": -32001}})
	svc.LogEvent(TaskLogEvent{
		TaskID:      taskID,
		Direction:   "error",
		Payload:     timeoutPayload,
		EventKind:   "status-update",
		IsFinal:     true,
		FinalStatus: "failed",
	})

	stop()

	task, err := taskRepo.GetByID(context.Background(), taskID)
	if err != nil || task == nil {
		t.Fatalf("task missing: %v", err)
	}
	if task.Status == nil || *task.Status != "completed" {
		t.Errorf("status = %v, want 'completed' (timeout must not overwrite)", task.Status)
	}
	if task.TotalInputTokens == nil || *task.TotalInputTokens != 100 {
		t.Errorf("TotalInputTokens = %v, want 100 (timeout must not null tokens)", task.TotalInputTokens)
	}
	if task.TotalOutputTokens == nil || *task.TotalOutputTokens != 50 {
		t.Errorf("TotalOutputTokens = %v, want 50", task.TotalOutputTokens)
	}
}

func logTestRequest(svc *TaskLoggerService, taskID string) {
	requestPayload, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": "message/send"})
	svc.LogEvent(TaskLogEvent{
		TaskID: taskID, UserID: "u1", SessionID: tid(taskID + "-sess"),
		Topic: "test-ns/a2a/v1/agent/request/x", Direction: "request",
		Payload: requestPayload, IsRequest: true, ExecutionMode: "foreground",
	})
}

func agentTokenUsage(input, output, cached int) map[string]any {
	return map[string]any{
		"total_input_tokens":        input,
		"total_output_tokens":       output,
		"total_cached_input_tokens": cached,
		"total_cache_write_tokens":  cached / 4,
		"last_input_tokens":         input / 10,
	}
}

func intValue(p *int) string {
	if p == nil {
		return "<nil>"
	}
	return strconv.Itoa(*p)
}

func assertTaskTokens(t *testing.T, task *store.Task, wantStatus string, input, output, cached int) {
	t.Helper()
	if task.Status == nil || *task.Status != wantStatus {
		t.Errorf("status = %q, want %q", stringValue(task.Status), wantStatus)
	}
	if task.TotalInputTokens == nil || *task.TotalInputTokens != input {
		t.Errorf("TotalInputTokens = %s, want %d", intValue(task.TotalInputTokens), input)
	}
	if task.TotalOutputTokens == nil || *task.TotalOutputTokens != output {
		t.Errorf("TotalOutputTokens = %s, want %d", intValue(task.TotalOutputTokens), output)
	}
	if task.TotalCachedInputTokens == nil || *task.TotalCachedInputTokens != cached {
		t.Errorf("TotalCachedInputTokens = %s, want %d", intValue(task.TotalCachedInputTokens), cached)
	}
}

func TestTaskLoggerService_ErrorResponseRecordsTokenUsage(t *testing.T) {
	for _, tc := range []struct {
		name    string
		channel string
		handle  func(*TaskLoggerService, *broker.Message, string, a2a.UserProperties)
	}{
		{"response", "response", (*TaskLoggerService).handleResponseMessage},
		{"status", "status", (*TaskLoggerService).handleStatusMessage},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, taskRepo, _, stop := makeTaskLogger(t)
			defer stop()

			taskID := tid("error-usage-" + tc.name)
			logTestRequest(svc, taskID)

			errResp := a2a.NewErrorResponse(taskID, -32603, "LLM call failed")
			errResp.Error.Data = map[string]any{"token_usage": agentTokenUsage(1200, 80, 1000)}
			payload, _ := json.Marshal(errResp)
			topic := "test-ns/a2a/v1/gateway/" + tc.channel + "/gw-test/" + taskID
			tc.handle(svc, &broker.Message{Topic: topic, Payload: payload}, topic, a2a.UserProperties{UserID: "u1"})

			stop()

			task, err := taskRepo.GetByID(t.Context(), taskID)
			if err != nil || task == nil {
				t.Fatalf("task missing: %v", err)
			}
			assertTaskTokens(t, task, "failed", 1200, 80, 1000)
			details := stringValue(task.TokenUsageDetails)
			for _, key := range []string{"total_cache_write_tokens", "last_input_tokens"} {
				if !strings.Contains(details, key) {
					t.Errorf("TokenUsageDetails = %q, want the agent's full token_usage block including %q", details, key)
				}
			}
		})
	}
}

func TestTaskLoggerService_LateUsageDoesNotOverwriteRecordedTokens(t *testing.T) {
	svc, taskRepo, _, stop := makeTaskLogger(t)
	defer stop()

	taskID := tid("late-usage-after-usage")
	logTestRequest(svc, taskID)
	svc.LogEvent(TaskLogEvent{
		TaskID:      taskID,
		Direction:   "error",
		Payload:     json.RawMessage(`{"jsonrpc":"2.0","error":{"code":-32603,"message":"LLM call failed"}}`),
		IsFinal:     true,
		FinalStatus: "failed",
		TokenUsage:  &TokenUsageInfo{InputTokens: 100, OutputTokens: 50, CachedInputTokens: 10},
	})
	svc.LogEvent(TaskLogEvent{
		TaskID:      taskID,
		Direction:   "response",
		Payload:     json.RawMessage(`{"jsonrpc":"2.0","result":{"kind":"task","status":{"state":"canceled"}}}`),
		IsFinal:     true,
		FinalStatus: "canceled",
		TokenUsage:  &TokenUsageInfo{InputTokens: 999, OutputTokens: 999, CachedInputTokens: 999},
	})

	stop()

	task, err := taskRepo.GetByID(t.Context(), taskID)
	if err != nil || task == nil {
		t.Fatalf("task missing: %v", err)
	}
	assertTaskTokens(t, task, "failed", 100, 50, 10)
}

func TestTaskLoggerService_RealCompletionSupersedesReaperTimeout(t *testing.T) {
	svc, taskRepo, _, stop := makeTaskLogger(t)
	defer stop()

	ctx := context.Background()
	taskID := tid("reaper-then-complete")

	if err := taskRepo.Create(ctx, &store.Task{ID: taskID, UserID: "u1", StartTime: time.Now().UnixMilli(), ExecutionMode: "foreground"}); err != nil {
		t.Fatal(err)
	}
	if _, err := taskRepo.TimeoutTask(ctx, taskID); err != nil {
		t.Fatal(err)
	}

	completedPayload, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "result": map[string]any{"kind": "task"}})
	svc.LogEvent(TaskLogEvent{
		TaskID:      taskID,
		Topic:       "test-ns/a2a/v1/gateway/response/gw-test/x",
		Direction:   "response",
		Payload:     completedPayload,
		IsFinal:     true,
		FinalStatus: "completed",
		TokenUsage:  &TokenUsageInfo{InputTokens: 100, OutputTokens: 50, CachedInputTokens: 10, DetailsJSON: `{"total_input_tokens":100}`},
	})

	stop()

	task, err := taskRepo.GetByID(ctx, taskID)
	if err != nil || task == nil {
		t.Fatalf("task missing: %v", err)
	}
	if task.Status == nil || *task.Status != "completed" {
		t.Errorf("status = %v, want 'completed' (real completion must supersede reaper timeout)", task.Status)
	}
	if task.TotalInputTokens == nil || *task.TotalInputTokens != 100 {
		t.Errorf("TotalInputTokens = %v, want 100 (tokens must be recorded on supersede)", task.TotalInputTokens)
	}
}

func TestTaskLoggerService_ProcessRequestEventRepoErrorPaths(t *testing.T) {
	db := store.TestDB(t)
	taskRepo := store.NewTaskRepo(db)
	eventRepo := store.NewTaskEventRepo(db)
	cfg := &TaskLoggerConfig{
		Enabled: true, LogStatusUpdates: true, LogArtifactEvents: true,
		LogFileParts: true, MaxFilePartSizeBytes: 102400, QueueSize: 16,
	}
	svc := NewTaskLoggerService(cfg, taskRepo, eventRepo, newMockBroker(), "test-ns", "gw-test", slog.Default())
	if err := svc.Start(t.Context()); err != nil {
		t.Fatal(err)
	}

	taskID := tid("repo-error")
	payload, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": "message/send"})

	svc.LogEvent(TaskLogEvent{
		TaskID: taskID, UserID: "u1", SessionID: tid("repo-err-sess"),
		Topic: "test-ns/a2a/v1/agent/request/x", Direction: "request",
		Payload: payload, IsRequest: true, ExecutionMode: "foreground",
	})

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if t2, _ := taskRepo.GetByID(context.Background(), taskID); t2 != nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	if err := db.Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}

	svc.LogEvent(TaskLogEvent{
		TaskID: taskID, UserID: "u1", SessionID: tid("repo-err-sess"),
		Topic: "test-ns/a2a/v1/agent/request/x", Direction: "request",
		Payload: payload, IsRequest: true, ExecutionMode: "foreground",
	})
	freshID := tid("repo-error-fresh")
	svc.LogEvent(TaskLogEvent{
		TaskID: freshID, UserID: "u1", SessionID: tid("repo-err-sess-2"),
		Topic: "test-ns/a2a/v1/agent/request/x", Direction: "request",
		Payload: payload, IsRequest: true, ExecutionMode: "foreground",
	})

	time.Sleep(150 * time.Millisecond)

	svc.Stop()
}

func TestTaskLogger_flushTaskEventsPerRowFallback(t *testing.T) {
	db := store.TestDB(t)
	taskRepo := store.NewTaskRepo(db)
	eventRepo := store.NewTaskEventRepo(db)
	ctx := context.Background()

	goodTask := "10000000-0000-0000-0000-00000000000a"
	if err := taskRepo.Create(ctx, &store.Task{ID: goodTask, UserID: "u1", StartTime: 1, ExecutionMode: "foreground"}); err != nil {
		t.Fatal(err)
	}

	s := &TaskLoggerService{eventRepo: eventRepo, logger: slog.Default()}
	batch := []*store.TaskEvent{
		{ID: "b0000000-0000-0000-0000-0000000000a1", TaskID: goodTask, CreatedTime: 1, Topic: "t", Direction: "status", Payload: "{}"},
		{ID: "b0000000-0000-0000-0000-0000000000a2", TaskID: "99999999-0000-0000-0000-000000000000", CreatedTime: 2, Topic: "t", Direction: "status", Payload: "{}"},
	}
	s.flushTaskEvents(ctx, batch)

	got, err := eventRepo.GetByTaskID(ctx, goodTask)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("per-row fallback should persist the valid event despite a bad sibling; got %d", len(got))
	}
}

func TestTaskLogger_FlushesEagerlyOnFinalEvent(t *testing.T) {
	db := store.TestDB(t)
	taskRepo := store.NewTaskRepo(db)
	eventRepo := store.NewTaskEventRepo(db)
	cfg := &TaskLoggerConfig{Enabled: true, LogStatusUpdates: true, QueueSize: 100}
	svc := NewTaskLoggerService(cfg, taskRepo, eventRepo, newMockBroker(), "test-ns", "gw-test", slog.Default())
	svc.batchSize = 1000
	svc.flushInterval = 10 * time.Minute

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	if err := svc.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer svc.Stop()

	taskID := tid("task-eager")
	reqPayload, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": taskID, "method": "message/send", "params": map[string]any{"message": map[string]any{"parts": []any{}}}})
	svc.LogEvent(TaskLogEvent{TaskID: taskID, UserID: "u1", SessionID: tid("s"), Topic: "test-ns/a2a/v1/agent/request/a", Direction: "request", Payload: reqPayload, IsRequest: true, ExecutionMode: "foreground"})

	finalPayload, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "result": map[string]any{"kind": "task", "id": taskID, "status": map[string]any{"state": "completed"}}})
	svc.LogEvent(TaskLogEvent{TaskID: taskID, UserID: "u1", Topic: "test-ns/a2a/v1/gateway/response/gw-test/task", Direction: "response", Payload: finalPayload, IsFinal: true, FinalStatus: "completed"})

	deadline := time.After(2 * time.Second)
	for {
		events, err := eventRepo.GetByTaskID(context.Background(), taskID)
		if err != nil {
			t.Fatal(err)
		}
		if len(events) >= 2 {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("final event was not flushed eagerly; got %d events (expected request+final flushed on IsFinal)", len(events))
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func TestTaskLogger_NoLossAcrossBatchBoundary(t *testing.T) {
	db := store.TestDB(t)
	taskRepo := store.NewTaskRepo(db)
	eventRepo := store.NewTaskEventRepo(db)
	cfg := &TaskLoggerConfig{Enabled: true, LogStatusUpdates: true, QueueSize: 100}
	svc := NewTaskLoggerService(cfg, taskRepo, eventRepo, newMockBroker(), "test-ns", "gw-test", slog.Default())
	svc.batchSize = 5
	svc.flushInterval = 10 * time.Minute

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	if err := svc.Start(ctx); err != nil {
		t.Fatal(err)
	}

	taskID := tid("task-boundary")
	reqPayload, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": taskID, "method": "message/send", "params": map[string]any{"message": map[string]any{"parts": []any{}}}})
	svc.LogEvent(TaskLogEvent{TaskID: taskID, UserID: "u1", SessionID: tid("s"), Topic: "test-ns/a2a/v1/agent/request/a", Direction: "request", Payload: reqPayload, IsRequest: true, ExecutionMode: "foreground"})

	const n = 12
	for i := range n {
		p, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "result": map[string]any{"kind": "status-update", "taskId": taskID, "seq": i}})
		svc.LogEvent(TaskLogEvent{TaskID: taskID, UserID: "u1", Topic: "test-ns/a2a/v1/gateway/status/gw-test/task", Direction: "status", EventKind: "status-update", Payload: p})
	}

	cancel()
	svc.Stop()

	events, err := eventRepo.GetByTaskID(context.Background(), taskID)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != n+1 {
		t.Fatalf("expected %d events (no loss across size-flush boundaries), got %d", n+1, len(events))
	}
	ids := make(map[string]struct{}, len(events))
	for _, e := range events {
		if _, dup := ids[e.ID]; dup {
			t.Fatalf("duplicate event id %s (row inserted twice across a flush)", e.ID)
		}
		ids[e.ID] = struct{}{}
	}
}

func TestLogEvent_ChannelFullDropsAreCounted(t *testing.T) {
	s := &TaskLoggerService{
		ch:     make(chan TaskLogEvent, 1),
		logger: slog.New(slog.DiscardHandler),
	}
	taskID := tid("channel-full-drop")

	s.LogEvent(TaskLogEvent{TaskID: taskID, Direction: "in"})
	for range 250 {
		s.LogEvent(TaskLogEvent{TaskID: taskID, Direction: "in"})
	}

	if got := s.droppedEvents.Load(); got != 250 {
		t.Fatalf("droppedEvents = %d, want 250 (channel full, no consumer)", got)
	}
}

func TestTaskLoggerService_StampsAgentNameAndErrorMessage(t *testing.T) {
	db := store.TestDB(t)
	taskRepo := store.NewTaskRepo(db)
	eventRepo := store.NewTaskEventRepo(db)
	brk := newMockBroker()

	cfg := &TaskLoggerConfig{
		Enabled:              true,
		LogStatusUpdates:     true,
		LogArtifactEvents:    true,
		LogFileParts:         true,
		MaxFilePartSizeBytes: 102400,
		QueueSize:            100,
	}
	svc := NewTaskLoggerService(cfg, taskRepo, eventRepo, brk, "test-ns", "gw-test", slog.Default())

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	if err := svc.Start(ctx); err != nil {
		t.Fatal(err)
	}

	requestPayload, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      tid("task-err"),
		"method":  "message/stream",
		"params": map[string]any{"message": map[string]any{
			"parts":    []any{},
			"metadata": map[string]any{"agent_name": "SearchAgent"},
		}},
	})
	svc.LogEvent(TaskLogEvent{
		TaskID:             tid("task-err"),
		UserID:             "user-1",
		SessionID:          tid("sess-err"),
		Topic:              "test-ns/a2a/v1/agent/request/SearchAgent",
		Direction:          "request",
		Payload:            requestPayload,
		IsRequest:          true,
		InitialRequestText: "boom please",
		AgentName:          "SearchAgent",
		ExecutionMode:      "foreground",
	})

	errPayload, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      tid("task-err"),
		"error":   map[string]any{"code": -32000, "message": "the agent exploded"},
	})
	svc.LogEvent(TaskLogEvent{
		TaskID:      tid("task-err"),
		UserID:      "user-1",
		Topic:       "test-ns/a2a/v1/gateway/response/gw-test/task-err",
		Direction:   "error",
		Payload:     errPayload,
		IsFinal:     true,
		FinalStatus: "failed",
	})

	cancel()
	svc.Stop()

	task, err := taskRepo.GetByID(context.Background(), tid("task-err"))
	if err != nil {
		t.Fatal(err)
	}
	if task == nil {
		t.Fatal("expected task to be created")
	}
	if task.AgentName == nil || *task.AgentName != "SearchAgent" {
		t.Errorf("task.AgentName = %v, want SearchAgent", task.AgentName)
	}
	if task.Status == nil || *task.Status != "failed" {
		t.Errorf("task.Status = %v, want failed", task.Status)
	}
	if task.ErrorMessage == nil || *task.ErrorMessage != "the agent exploded" {
		t.Errorf("task.ErrorMessage = %v, want 'the agent exploded'", task.ErrorMessage)
	}
}

func TestAgentNameFromRequestMetadata(t *testing.T) {
	cases := []struct {
		name string
		meta map[string]any
		want string
	}{
		{"nil", nil, ""},
		{"agentName", map[string]any{"agent_name": "A"}, "A"},
		{"workflowWins", map[string]any{"agent_name": "A", "workflow_name": "W"}, "W"},
		{"emptyAgentName", map[string]any{"agent_name": ""}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := agentNameFromRequestMetadata(c.meta); got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

func TestErrorMessageFromFinalPayload(t *testing.T) {
	errShape := []byte(`{"jsonrpc":"2.0","id":"x","error":{"code":-1,"message":"boom"}}`)
	if got := errorMessageFromFinalPayload(errShape); got != "boom" {
		t.Errorf("error shape: got %q, want boom", got)
	}

	statusShape := []byte(`{"jsonrpc":"2.0","result":{"kind":"task","status":{"state":"failed","message":{"kind":"message","parts":[{"kind":"text","text":"failed hard"}]}}}}`)
	if got := errorMessageFromFinalPayload(statusShape); got != "failed hard" {
		t.Errorf("status shape: got %q, want 'failed hard'", got)
	}

	completed := []byte(`{"jsonrpc":"2.0","result":{"kind":"task","status":{"state":"completed"}}}`)
	if got := errorMessageFromFinalPayload(completed); got != "" {
		t.Errorf("completed: got %q, want empty", got)
	}

	if got := errorMessageFromFinalPayload([]byte(`not json`)); got != "" {
		t.Errorf("garbage: got %q, want empty", got)
	}
}

func statusUpdatePayload(t *testing.T, state string, final bool, parts []a2a.Part) json.RawMessage {
	t.Helper()
	var msg *a2a.Message
	if parts != nil {
		msg = &a2a.Message{Kind: "message", Parts: parts}
	}
	env := map[string]any{
		"jsonrpc": "2.0",
		"result": a2a.TaskStatusUpdateEvent{
			Kind:   "status-update",
			Final:  final,
			Status: a2a.TaskStatus{State: a2a.TaskState(state), Message: msg},
		},
	}
	raw, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal status payload: %v", err)
	}
	return raw
}

func TestIsDeltaEvent(t *testing.T) {
	textPart := []a2a.Part{{Kind: "text", Text: "hello"}}
	dataPart := []a2a.Part{{Kind: "data", Data: map[string]any{"type": "tool_result"}}}
	filePart := []a2a.Part{{Kind: "file", File: &a2a.FileContent{Name: "a.png"}}}
	mixedParts := []a2a.Part{{Kind: "text", Text: "hi"}, {Kind: "data", Data: map[string]any{"type": "llm_invocation"}}}

	cases := []struct {
		name string
		evt  TaskLogEvent
		want bool
	}{
		{
			name: "streaming text delta",
			evt:  TaskLogEvent{Direction: "status", EventKind: "status-update", Payload: statusUpdatePayload(t, "working", false, textPart)},
			want: true,
		},
		{
			name: "request event",
			evt:  TaskLogEvent{Direction: "request", IsRequest: true, Payload: statusUpdatePayload(t, "working", false, textPart)},
			want: false,
		},
		{
			name: "final response",
			evt:  TaskLogEvent{Direction: "response", IsFinal: true, EventKind: "status-update", Payload: statusUpdatePayload(t, "completed", true, textPart)},
			want: false,
		},
		{
			name: "error event",
			evt:  TaskLogEvent{Direction: "error", IsFinal: true, Payload: statusUpdatePayload(t, "failed", false, textPart)},
			want: false,
		},
		{
			name: "artifact update",
			evt:  TaskLogEvent{Direction: "status", EventKind: "artifact-update", Payload: statusUpdatePayload(t, "working", false, textPart)},
			want: false,
		},
		{
			name: "status update with data part",
			evt:  TaskLogEvent{Direction: "status", EventKind: "status-update", Payload: statusUpdatePayload(t, "working", false, dataPart)},
			want: false,
		},
		{
			name: "status update with file part",
			evt:  TaskLogEvent{Direction: "status", EventKind: "status-update", Payload: statusUpdatePayload(t, "working", false, filePart)},
			want: false,
		},
		{
			name: "status update with mixed text and data",
			evt:  TaskLogEvent{Direction: "status", EventKind: "status-update", Payload: statusUpdatePayload(t, "working", false, mixedParts)},
			want: false,
		},
		{
			name: "final flag set inside payload",
			evt:  TaskLogEvent{Direction: "status", EventKind: "status-update", Payload: statusUpdatePayload(t, "working", true, textPart)},
			want: false,
		},
		{
			name: "state transition to input-required",
			evt:  TaskLogEvent{Direction: "status", EventKind: "status-update", Payload: statusUpdatePayload(t, "input-required", false, textPart)},
			want: false,
		},
		{
			name: "empty message",
			evt:  TaskLogEvent{Direction: "status", EventKind: "status-update", Payload: statusUpdatePayload(t, "working", false, nil)},
			want: false,
		},
		{
			name: "empty text part",
			evt:  TaskLogEvent{Direction: "status", EventKind: "status-update", Payload: statusUpdatePayload(t, "working", false, []a2a.Part{{Kind: "text", Text: ""}})},
			want: false,
		},
		{
			name: "malformed payload",
			evt:  TaskLogEvent{Direction: "status", EventKind: "status-update", Payload: json.RawMessage(`not json`)},
			want: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isDeltaEvent(tc.evt); got != tc.want {
				t.Errorf("isDeltaEvent(%s) = %v, want %v", tc.name, got, tc.want)
			}
			if got := buildTaskEvent(tc.evt, 123).IsDelta; got != tc.want {
				t.Errorf("buildTaskEvent(%s).IsDelta = %v, want %v", tc.name, got, tc.want)
			}
		})
	}
}

func TestSanitizePayload_RedactsAuthSecrets(t *testing.T) {
	const (
		verifier = "pkce-verifier-must-not-leak"
		authURI  = "https://idp.example.com/authorize?state=prompt-state-must-not-leak"
		code     = "authorization-code-must-not-leak"
		state    = "callback-state-must-not-leak"
		callback = "https://gw.example.com/callback?code=authorization-code-must-not-leak"
	)
	authRequired, err := json.Marshal(map[string]any{"result": map[string]any{"status": map[string]any{
		"state": "input-required",
		"message": map[string]any{"parts": []any{map[string]any{"kind": "data", "data": map[string]any{
			"type": string(a2a.SignalAuthenticationRequired), "target_agent": "Orchestrator",
			"auth_uri": authURI, "code_verifier": verifier, "tool_display_name": "Atlassian",
		}}}},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	authResponse, err := json.Marshal(map[string]any{"params": map[string]any{
		"message": map[string]any{"parts": []any{map[string]any{"kind": "data", "data": map[string]any{
			"type": string(a2a.SignalAuthResponse), "target_agent": "Orchestrator", "user_id": "u1",
			"code": code, "state": state, "auth_response_uri": callback, "code_verifier": verifier,
		}}}},
	}})
	if err != nil {
		t.Fatal(err)
	}

	cases := map[string]struct {
		payload json.RawMessage
		secrets []string
		keep    []string
	}{
		"authenticationRequired": {authRequired, []string{verifier, authURI, "prompt-state-must-not-leak"},
			[]string{"Orchestrator", "Atlassian", "input-required"}},
		"authResponse": {authResponse, []string{code, state, callback, verifier},
			[]string{"Orchestrator", "u1"}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			s := &TaskLoggerService{cfg: &TaskLoggerConfig{LogFileParts: true}}
			out := string(s.sanitizePayload(tc.payload))
			for _, secret := range tc.secrets {
				if strings.Contains(out, secret) {
					t.Errorf("stored payload still contains %q:\n%s", secret, out)
				}
			}
			for _, keep := range tc.keep {
				if !strings.Contains(out, keep) {
					t.Errorf("stored payload lost non-secret %q:\n%s", keep, out)
				}
			}
		})
	}
}

func TestSanitizePayload_LeavesCollidingKeysIntact(t *testing.T) {
	s := &TaskLoggerService{cfg: &TaskLoggerConfig{LogFileParts: true}}
	payload := json.RawMessage(`{"error":{"code":-32603,"message":"boom"},` +
		`"result":{"status":{"state":"failed"},"metadata":{"code":"US","state":"NY"}},` +
		`"params":{"message":{"parts":[{"kind":"data","data":{"type":"authentication_required","auth_uri":"https://idp.example.com/authorize?state=leak"}}]}}}`)

	var got map[string]any
	if err := json.Unmarshal(s.sanitizePayload(payload), &got); err != nil {
		t.Fatal(err)
	}
	result := got["result"].(map[string]any)
	if state := result["status"].(map[string]any)["state"]; state != "failed" {
		t.Errorf("result.status.state = %v, want failed", state)
	}
	metadata := result["metadata"].(map[string]any)
	if metadata["code"] != "US" || metadata["state"] != "NY" {
		t.Errorf("non-auth code/state were masked: %v", metadata)
	}
	if code := got["error"].(map[string]any)["code"]; code != float64(-32603) {
		t.Errorf("error.code = %v, want -32603", code)
	}
	if strings.Contains(string(mustJSON(t, got)), "leak") {
		t.Error("auth_uri state survived redaction")
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
	mcpgo "github.com/mark3labs/mcp-go/mcp"
)

type capturedRecord struct {
	Level   slog.Level
	Message string
	Attrs   map[string]any
}

type sharedHandler struct {
	mu      sync.Mutex
	records []capturedRecord
}

type captureLogger struct {
	shared *sharedHandler
	attrs  []slog.Attr
}

func newCaptureLogger() (*captureLogger, *sharedHandler) {
	s := &sharedHandler{}
	return &captureLogger{shared: s}, s
}

func (h *captureLogger) Enabled(_ context.Context, lvl slog.Level) bool {
	return lvl >= slog.LevelInfo
}

func (h *captureLogger) Handle(_ context.Context, r slog.Record) error {
	out := capturedRecord{
		Level:   r.Level,
		Message: r.Message,
		Attrs:   make(map[string]any),
	}
	for _, a := range h.attrs {
		out.Attrs[a.Key] = a.Value.Any()
	}
	r.Attrs(func(a slog.Attr) bool {
		out.Attrs[a.Key] = a.Value.Any()
		return true
	})
	h.shared.mu.Lock()
	h.shared.records = append(h.shared.records, out)
	h.shared.mu.Unlock()
	return nil
}

func (h *captureLogger) WithAttrs(attrs []slog.Attr) slog.Handler {
	next := make([]slog.Attr, 0, len(h.attrs)+len(attrs))
	next = append(next, h.attrs...)
	next = append(next, attrs...)
	return &captureLogger{shared: h.shared, attrs: next}
}

func (h *captureLogger) WithGroup(_ string) slog.Handler {
	return h
}

func (s *sharedHandler) snapshot() []capturedRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]capturedRecord, len(s.records))
	copy(out, s.records)
	return out
}

func (s *sharedHandler) firstWithMessage(msg string) (capturedRecord, bool) {
	for _, r := range s.snapshot() {
		if r.Message == msg {
			return r, true
		}
	}
	return capturedRecord{}, false
}

func TestLogCall_EmitsExpectedFields(t *testing.T) {
	h, shared := newCaptureLogger()
	logger := slog.New(h)

	logCall(logger, auditIdentity{
		UserID:    "user-42",
		AgentID:   "ExampleAgent",
		SessionID: "sess-99",
		ToolName:  "atlassianUserInfo",
	})

	rec, ok := shared.firstWithMessage(auditMsgCall)
	if !ok {
		t.Fatalf("no %q record; have %+v", auditMsgCall, shared.snapshot())
	}
	if rec.Level != slog.LevelInfo {
		t.Errorf("level = %v, want INFO", rec.Level)
	}
	want := map[string]any{
		"userID":    "user-42",
		"agentID":   "ExampleAgent",
		"sessionID": "sess-99",
		"toolName":  "atlassianUserInfo",
	}
	for k, v := range want {
		if rec.Attrs[k] != v {
			t.Errorf("attr %q = %v, want %v (full=%v)", k, rec.Attrs[k], v, rec.Attrs)
		}
	}
}

func TestLogSuccess_EmitsDurationMs(t *testing.T) {
	h, shared := newCaptureLogger()
	logger := slog.New(h)
	logSuccess(logger, auditIdentity{
		UserID:    "u",
		AgentID:   "a",
		SessionID: "s",
		ToolName:  "t",
	}, 1234*time.Microsecond)

	rec, ok := shared.firstWithMessage(auditMsgSuccess)
	if !ok {
		t.Fatalf("no success record; have %+v", shared.snapshot())
	}
	d, ok := rec.Attrs["duration_ms"].(float64)
	if !ok {
		t.Fatalf("duration_ms type = %T, want float64", rec.Attrs["duration_ms"])
	}
	if d < 1.22 || d > 1.24 {
		t.Errorf("duration_ms = %v, want ~1.23", d)
	}
	if rec.Level != slog.LevelInfo {
		t.Errorf("success log should be INFO, got %v", rec.Level)
	}
}

func TestLogFailure_EmitsErrorFields(t *testing.T) {
	h, shared := newCaptureLogger()
	logger := slog.New(h)
	logFailure(logger, auditIdentity{
		UserID:    "u",
		AgentID:   "a",
		SessionID: "s",
		ToolName:  "t",
	}, 5*time.Millisecond, slog.LevelError, "timeout", "deadline exceeded")

	rec, ok := shared.firstWithMessage(auditMsgFailure)
	if !ok {
		t.Fatalf("no failure record; have %+v", shared.snapshot())
	}
	if rec.Level != slog.LevelError {
		t.Errorf("failure log should be ERROR, got %v", rec.Level)
	}
	if rec.Attrs["errorType"] != "timeout" {
		t.Errorf("error_type = %v", rec.Attrs["errorType"])
	}
	if rec.Attrs["error"] != "deadline exceeded" {
		t.Errorf("error = %v", rec.Attrs["error"])
	}
	if _, ok := rec.Attrs["duration_ms"].(float64); !ok {
		t.Errorf("duration_ms missing or wrong type: %T", rec.Attrs["duration_ms"])
	}
}

func TestLogHelpers_NilLoggerIsNoOp(t *testing.T) {
	logCall(nil, auditIdentity{ToolName: "t"})
	logSuccess(nil, auditIdentity{ToolName: "t"}, time.Second)
	logFailure(nil, auditIdentity{ToolName: "t"}, time.Second, slog.LevelError, "x", "y")
}

func TestDurationMilliseconds(t *testing.T) {
	cases := []struct {
		name string
		in   time.Duration
		want float64
	}{
		{"zero", 0, 0},
		{"1ms", time.Millisecond, 1.00},
		{"1.234ms", 1234 * time.Microsecond, 1.23},
		{"123.456ms", 123456 * time.Microsecond, 123.46},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := durationMilliseconds(tc.in)
			if got != tc.want {
				t.Errorf("durationMilliseconds(%v) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestSanitizeErrorMessage_RedactsBearer(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"nothing to redact", "plain error", "plain error"},
		{"Bearer token", "401 Bearer abcdef123 returned", "401 Bearer [REDACTED] returned"},
		{"lowercase bearer", `auth: bearer xyz; retry`, `auth: bearer [REDACTED]; retry`},
		{"trailing token", "oops: Bearer eyJhbGciOi.JIUzI1NiJ9", "oops: Bearer [REDACTED]"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := sanitizeErrorMessage(tc.in)
			if got != tc.want {
				t.Errorf("sanitizeErrorMessage(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestSanitizeErrorMessage_MultipleBearers(t *testing.T) {
	in := "primary Bearer abc123 failed; retry with Bearer def456 also failed"
	got := sanitizeErrorMessage(in)
	want := "primary Bearer [REDACTED] failed; retry with Bearer [REDACTED] also failed"
	if got != want {
		t.Errorf("sanitizeErrorMessage = %q, want %q", got, want)
	}
}

func TestSanitizeErrorMessage_CaseInsensitive(t *testing.T) {
	cases := []struct{ in, want string }{
		{"BEARER token123", "BEARER [REDACTED]"},
		{"bEaReR mixedCase", "bEaReR [REDACTED]"},
		{"AUTHORIZATION: Basic abc==", "AUTHORIZATION: [REDACTED]"},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got := sanitizeErrorMessage(tc.in)
			if got != tc.want {
				t.Errorf("sanitizeErrorMessage(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestSanitizeErrorMessage_BroaderPatterns(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Authorization: Bearer xyz", "Authorization: [REDACTED]"},
		{"request had api_key=sk-12345 in query", "request had api_key=[REDACTED] in query"},
		{"apikey: hunter2 failed", "apikey:[REDACTED] failed"},
		{"access_token=eyJhbG expired", "access_token=[REDACTED] expired"},
		{"Cookie: session=abc; other=def", "Cookie: [REDACTED]"},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got := sanitizeErrorMessage(tc.in)
			if got != tc.want {
				t.Errorf("sanitizeErrorMessage(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestSanitizeErrorMessage_Truncates(t *testing.T) {
	in := strings.Repeat("x", 600)
	got := sanitizeErrorMessage(in)
	if !strings.HasSuffix(got, "...(truncated)") {
		t.Errorf("long message should be truncated, got tail %q", got[len(got)-40:])
	}
	if len(got) > 600 {
		t.Errorf("truncated length = %d, want <= 600", len(got))
	}
}

func TestAuditIdentityFor_NilContext(t *testing.T) {
	id := auditIdentityFor("test_tool", nil)
	if id.ToolName != "test_tool" {
		t.Errorf("ToolName = %q", id.ToolName)
	}
	if id.UserID != "" || id.AgentID != "" || id.SessionID != "" {
		t.Errorf("nil ctx must yield empty ids, got %+v", id)
	}
}

func TestAuditIdentityFor_PopulatesFromContext(t *testing.T) {
	tc := &tool.Context{
		UserID:    "alice",
		AgentName: "Orchestrator",
		SessionID: "S-1",
	}
	id := auditIdentityFor("searchJira", tc)
	if id.UserID != "alice" || id.AgentID != "Orchestrator" || id.SessionID != "S-1" || id.ToolName != "searchJira" {
		t.Errorf("identity mismatch: %+v", id)
	}
}

func TestEmitAuditResult_SuccessLogsSuccess(t *testing.T) {
	h, shared := newCaptureLogger()
	logger := slog.New(h)
	res := tool.OK("done")
	emitAuditResult(logger, auditIdentity{ToolName: "t"}, 2*time.Millisecond, res, nil)
	if _, ok := shared.firstWithMessage(auditMsgSuccess); !ok {
		t.Errorf("expected success log for OK result, have %+v", shared.snapshot())
	}
}

func TestEmitAuditResult_ErrorStatusLogsFailure(t *testing.T) {
	h, shared := newCaptureLogger()
	logger := slog.New(h)
	res := tool.Error("boom")
	emitAuditResult(logger, auditIdentity{ToolName: "t"}, 2*time.Millisecond, res, nil)
	rec, ok := shared.firstWithMessage(auditMsgFailure)
	if !ok {
		t.Fatalf("expected failure log for Error result, have %+v", shared.snapshot())
	}
	if rec.Level != slog.LevelWarn {
		t.Errorf("tool_error (LLM-recoverable) log level = %v, want WARN", rec.Level)
	}
	if rec.Attrs["errorType"] != "tool_error" {
		t.Errorf("error_type = %v, want tool_error", rec.Attrs["errorType"])
	}
}

func TestEmitAuditResult_PendingStatusSilent(t *testing.T) {
	h, shared := newCaptureLogger()
	logger := slog.New(h)
	res := &tool.Result{Status: tool.StatusPending, Message: "need auth"}
	emitAuditResult(logger, auditIdentity{ToolName: "t"}, time.Millisecond, res, nil)
	if len(shared.snapshot()) != 0 {
		t.Errorf("pending result must not emit audit records, have %+v", shared.snapshot())
	}
}

func TestEmitAuditResult_ReturnedErrorLogsFailure(t *testing.T) {
	h, shared := newCaptureLogger()
	logger := slog.New(h)
	emitAuditResult(logger, auditIdentity{ToolName: "t"}, time.Millisecond, nil, errors.New("ctx cancelled"))
	rec, ok := shared.firstWithMessage(auditMsgFailure)
	if !ok {
		t.Fatalf("expected failure log for returned error, have %+v", shared.snapshot())
	}
	if rec.Attrs["error"] != "ctx cancelled" {
		t.Errorf("error = %v", rec.Attrs["error"])
	}
}

func TestEmitAuditResult_NilResultNilError(t *testing.T) {
	h, shared := newCaptureLogger()
	logger := slog.New(h)
	emitAuditResult(logger, auditIdentity{ToolName: "t"}, time.Millisecond, nil, nil)
	rec, ok := shared.firstWithMessage(auditMsgFailure)
	if !ok {
		t.Fatalf("expected failure log for nil/nil, have %+v", shared.snapshot())
	}
	if rec.Attrs["errorType"] != "nil_result" {
		t.Errorf("error_type = %v, want nil_result", rec.Attrs["errorType"])
	}
}

func TestMCPTool_Execute_EmitsAuditLogs_Success(t *testing.T) {
	const expectedBearer = "Bearer integration-audit-token"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != expectedBearer {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		body, _ := io.ReadAll(r.Body)
		_ = r.Body.Close()
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		if err := json.Unmarshal(body, &req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if len(req.ID) == 0 || string(req.ID) == "null" {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch req.Method {
		case "initialize":
			writeJSONRPC(w, req.ID, mcpgo.InitializeResult{
				ProtocolVersion: mcpgo.LATEST_LEGACY_PROTOCOL_VERSION,
				ServerInfo:      mcpgo.Implementation{Name: "audit-mock", Version: "1"},
				Capabilities:    mcpgo.ServerCapabilities{},
			})
		case "tools/call":
			writeJSONRPC(w, req.ID, map[string]any{
				"content": []any{
					map[string]any{"type": "text", "text": "ok"},
				},
			})
		default:
			writeJSONRPC(w, req.ID, map[string]any{})
		}
	}))
	defer srv.Close()

	cfg, err := ParseToolsetConfig(map[string]any{
		"tool_type": "mcp",
		"connection_params": map[string]any{
			"type": "streamable-http",
			"url":  srv.URL,
		},
		"auth": map[string]any{
			"type": "bearer",
			"credential": map[string]any{
				"token": "integration-audit-token",
			},
		},
		"manifest": []any{
			map[string]any{
				"name":        "auditPing",
				"description": "Tiny ping.",
				"inputSchema": map[string]any{"type": "object"},
			},
		},
	})
	if err != nil {
		t.Fatalf("ParseToolsetConfig: %v", err)
	}

	h, shared := newCaptureLogger()
	logger := slog.New(h)

	mgr := NewManager(logger)
	defer mgr.Stop()
	if addErr := mgr.AddClient(cfg); addErr != nil {
		t.Fatalf("AddClient: %v", addErr)
	}
	tools, err := mgr.DiscoverTools(cfg)
	if err != nil {
		t.Fatalf("DiscoverTools: %v", err)
	}
	if len(tools) != 1 {
		t.Fatalf("len(tools) = %d, want 1", len(tools))
	}

	toolCtx := &tool.Context{
		UserID:    "audit-user",
		AgentName: "AuditAgent",
		SessionID: "audit-session",
	}
	result, err := tools[0].Execute(context.Background(), map[string]any{}, toolCtx)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result == nil {
		t.Fatal("Execute returned nil result")
	}

	callRec, ok := shared.firstWithMessage(auditMsgCall)
	if !ok {
		t.Fatalf("no %q record; have %+v", auditMsgCall, shared.snapshot())
	}
	if callRec.Attrs["userID"] != "audit-user" {
		t.Errorf("call userID = %v", callRec.Attrs["userID"])
	}
	if callRec.Attrs["agentID"] != "AuditAgent" {
		t.Errorf("call agentID = %v", callRec.Attrs["agentID"])
	}
	if callRec.Attrs["sessionID"] != "audit-session" {
		t.Errorf("call sessionID = %v", callRec.Attrs["sessionID"])
	}
	if callRec.Attrs["toolName"] != "auditPing" {
		t.Errorf("call tool_name = %v", callRec.Attrs["toolName"])
	}

	successRec, ok := shared.firstWithMessage(auditMsgSuccess)
	if !ok {
		t.Fatalf("no %q record; have messages %v", auditMsgSuccess, messageList(shared))
	}
	d, ok := successRec.Attrs["duration_ms"].(float64)
	if !ok || d <= 0 {
		t.Errorf("success duration_ms = %v (type %T); want positive float", successRec.Attrs["duration_ms"], successRec.Attrs["duration_ms"])
	}

	for _, rec := range shared.snapshot() {
		for k, v := range rec.Attrs {
			if s, ok := v.(string); ok && strings.Contains(s, "integration-audit-token") {
				t.Errorf("bearer token leaked into audit log key=%q value=%q", k, s)
			}
		}
	}
}

func TestMCPTool_Execute_EmitsAuditLogs_Failure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = r.Body.Close()
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		if err := json.Unmarshal(body, &req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if len(req.ID) == 0 || string(req.ID) == "null" {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch req.Method {
		case "initialize":
			writeJSONRPC(w, req.ID, mcpgo.InitializeResult{
				ProtocolVersion: mcpgo.LATEST_LEGACY_PROTOCOL_VERSION,
				ServerInfo:      mcpgo.Implementation{Name: "audit-mock", Version: "1"},
				Capabilities:    mcpgo.ServerCapabilities{},
			})
		case "tools/call":
			writeJSONRPC(w, req.ID, map[string]any{
				"isError": true,
				"content": []any{
					map[string]any{"type": "text", "text": "database unavailable"},
				},
			})
		default:
			writeJSONRPC(w, req.ID, map[string]any{})
		}
	}))
	defer srv.Close()

	cfg, err := ParseToolsetConfig(map[string]any{
		"tool_type": "mcp",
		"connection_params": map[string]any{
			"type": "streamable-http",
			"url":  srv.URL,
		},
		"manifest": []any{
			map[string]any{
				"name":        "auditFail",
				"description": "Always errors.",
				"inputSchema": map[string]any{"type": "object"},
			},
		},
	})
	if err != nil {
		t.Fatalf("ParseToolsetConfig: %v", err)
	}

	h, shared := newCaptureLogger()
	logger := slog.New(h)

	mgr := NewManager(logger)
	defer mgr.Stop()
	if addErr := mgr.AddClient(cfg); addErr != nil {
		t.Fatalf("AddClient: %v", addErr)
	}
	tools, err := mgr.DiscoverTools(cfg)
	if err != nil {
		t.Fatalf("DiscoverTools: %v", err)
	}

	toolCtx := &tool.Context{
		UserID:    "fail-user",
		AgentName: "FailAgent",
		SessionID: "fail-session",
	}
	result, err := tools[0].Execute(context.Background(), map[string]any{}, toolCtx)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Status != tool.StatusError {
		t.Errorf("result.Status = %q, want error", result.Status)
	}

	failRec, ok := shared.firstWithMessage(auditMsgFailure)
	if !ok {
		t.Fatalf("no %q record; have messages %v", auditMsgFailure, messageList(shared))
	}
	if failRec.Level != slog.LevelWarn {
		t.Errorf("tool_error (LLM-recoverable) log level = %v, want WARN", failRec.Level)
	}
	if failRec.Attrs["errorType"] != "tool_error" {
		t.Errorf("error_type = %v, want tool_error", failRec.Attrs["errorType"])
	}
	if failRec.Attrs["userID"] != "fail-user" {
		t.Errorf("userID = %v", failRec.Attrs["userID"])
	}
	if _, ok := failRec.Attrs["duration_ms"].(float64); !ok {
		t.Errorf("duration_ms missing or wrong type: %T", failRec.Attrs["duration_ms"])
	}
}

func messageList(s *sharedHandler) []string {
	recs := s.snapshot()
	out := make([]string, len(recs))
	for i, r := range recs {
		out[i] = r.Message
	}
	return out
}

// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package audit

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/strutil"
)

func testLogger() (*StructuredLogger, *bytes.Buffer) {
	var buf bytes.Buffer
	handler := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	logger := slog.New(handler)
	return NewStructuredLogger(logger), &buf
}

func testLoggerJSON() (*StructuredLogger, *bytes.Buffer) {
	var buf bytes.Buffer
	handler := slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	logger := slog.New(handler)
	return NewStructuredLogger(logger), &buf
}

func TestLogAuth_Success(t *testing.T) {
	l, buf := testLogger()
	l.LogAuth(context.Background(), auth.AuthEvent{
		UserID:  "user1",
		TaskID:  "task-123",
		Success: true,
	})
	out := buf.String()
	if !strings.Contains(out, "authentication succeeded") {
		t.Errorf("expected 'authentication succeeded', got: %s", out)
	}
	if !strings.Contains(out, "user1") {
		t.Errorf("expected user_id in output, got: %s", out)
	}
	if !strings.Contains(out, "task-123") {
		t.Errorf("expected task_id in output, got: %s", out)
	}
	if !strings.Contains(out, "INFO") {
		t.Errorf("expected INFO level, got: %s", out)
	}
}

func TestLogAuth_Failure(t *testing.T) {
	l, buf := testLogger()
	l.LogAuth(context.Background(), auth.AuthEvent{
		UserID:  "user2",
		TaskID:  "task-456",
		Success: false,
		Reason:  "invalid token",
	})
	out := buf.String()
	if !strings.Contains(out, "authentication failed") {
		t.Errorf("expected 'authentication failed', got: %s", out)
	}
	if !strings.Contains(out, "invalid token") {
		t.Errorf("expected reason in output, got: %s", out)
	}
	if !strings.Contains(out, "WARN") {
		t.Errorf("expected WARN level, got: %s", out)
	}
}

func TestLogToolAccess_Granted(t *testing.T) {
	l, buf := testLogger()
	l.LogToolAccess(context.Background(), auth.ToolAccessEvent{
		UserID:   "user1",
		ToolName: "search_web",
		Allowed:  true,
		Scopes:   []string{"tool:search_web:invoke"},
	})
	out := buf.String()
	if !strings.Contains(out, "tool access granted") {
		t.Errorf("expected 'tool access granted', got: %s", out)
	}
	if !strings.Contains(out, "search_web") {
		t.Errorf("expected tool name in output, got: %s", out)
	}
	if !strings.Contains(out, "DEBUG") {
		t.Errorf("expected DEBUG level, got: %s", out)
	}
}

func TestLogToolAccess_Denied(t *testing.T) {
	l, buf := testLogger()
	l.LogToolAccess(context.Background(), auth.ToolAccessEvent{
		UserID:   "user1",
		ToolName: "delete_all",
		Allowed:  false,
		Scopes:   []string{"tool:delete_all:invoke"},
	})
	out := buf.String()
	if !strings.Contains(out, "tool access denied") {
		t.Errorf("expected 'tool access denied', got: %s", out)
	}
	if !strings.Contains(out, "WARN") {
		t.Errorf("expected WARN level, got: %s", out)
	}
}

func TestLogAgentAccess_Granted(t *testing.T) {
	l, buf := testLogger()
	l.LogAgentAccess(context.Background(), auth.AgentAccessEvent{
		UserID:    "user1",
		AgentName: "peer-agent",
		Allowed:   true,
	})
	out := buf.String()
	if !strings.Contains(out, "agent delegation granted") {
		t.Errorf("expected 'agent delegation granted', got: %s", out)
	}
	if !strings.Contains(out, "DEBUG") {
		t.Errorf("expected DEBUG level, got: %s", out)
	}
}

func TestLogAgentAccess_Denied(t *testing.T) {
	l, buf := testLogger()
	l.LogAgentAccess(context.Background(), auth.AgentAccessEvent{
		UserID:    "user1",
		AgentName: "restricted-agent",
		Allowed:   false,
	})
	out := buf.String()
	if !strings.Contains(out, "agent delegation denied") {
		t.Errorf("expected 'agent delegation denied', got: %s", out)
	}
	if !strings.Contains(out, "WARN") {
		t.Errorf("expected WARN level, got: %s", out)
	}
}

func TestLogToolExecution_Success(t *testing.T) {
	l, buf := testLogger()
	l.LogToolExecution(context.Background(), auth.ToolExecutionEvent{
		UserID:     "user1",
		AgentName:  "my-agent",
		ToolName:   "search_web",
		SessionID:  "sess-001",
		DurationMs: 123.45,
		Success:    true,
	})
	out := buf.String()
	if !strings.Contains(out, "tool execution succeeded") {
		t.Errorf("expected 'tool execution succeeded', got: %s", out)
	}
	if !strings.Contains(out, "search_web") {
		t.Errorf("expected tool name in output, got: %s", out)
	}
	if !strings.Contains(out, "my-agent") {
		t.Errorf("expected agent name in output, got: %s", out)
	}
	if !strings.Contains(out, "123.45") {
		t.Errorf("expected duration in output, got: %s", out)
	}
	if !strings.Contains(out, "INFO") {
		t.Errorf("expected INFO level, got: %s", out)
	}
}

func TestLogToolExecution_Failure(t *testing.T) {
	l, buf := testLogger()
	l.LogToolExecution(context.Background(), auth.ToolExecutionEvent{
		UserID:     "user1",
		AgentName:  "my-agent",
		ToolName:   "failing_tool",
		SessionID:  "sess-002",
		DurationMs: 500.0,
		Success:    false,
		Error:      "tool returned error",
	})
	out := buf.String()
	if !strings.Contains(out, "tool execution failed") {
		t.Errorf("expected 'tool execution failed', got: %s", out)
	}
	if !strings.Contains(out, "tool returned error") {
		t.Errorf("expected error in output, got: %s", out)
	}
	if !strings.Contains(out, "ERROR") {
		t.Errorf("expected ERROR level, got: %s", out)
	}
}

func TestLogToolExecution_CredentialIDOnlyWhenAKeyRanIt(t *testing.T) {
	l, buf := testLogger()
	l.LogToolExecution(context.Background(), auth.ToolExecutionEvent{
		UserID: "system:ci-deployer", CredentialID: "key-1", ToolName: "search_web", Success: true,
	})
	if !strings.Contains(buf.String(), "credentialID=key-1") {
		t.Errorf("a key's tool run does not name the key: %s", buf.String())
	}

	l, buf = testLogger()
	l.LogToolExecution(context.Background(), auth.ToolExecutionEvent{UserID: "user1", ToolName: "search_web", Success: true})
	if strings.Contains(buf.String(), "credentialID") {
		t.Errorf("a person's tool run carries a credentialID key: %s", buf.String())
	}
}

func TestLogAccessDecisions_CredentialIDOnlyWhenAKeyAsked(t *testing.T) {
	for _, allowed := range []bool{false, true} {
		l, buf := testLogger()
		l.LogToolAccess(t.Context(), auth.ToolAccessEvent{UserID: "system:ci-deployer", CredentialID: "key-1", ToolName: "t", Allowed: allowed})
		l.LogAgentAccess(t.Context(), auth.AgentAccessEvent{UserID: "system:ci-deployer", CredentialID: "key-1", AgentName: "a", Allowed: allowed})
		if got := strings.Count(buf.String(), "credentialID=key-1"); got != 2 {
			t.Errorf("allowed=%v: credentialID appears %d times, want once per decision: %s", allowed, got, buf.String())
		}

		l, buf = testLogger()
		l.LogToolAccess(t.Context(), auth.ToolAccessEvent{UserID: "user1", ToolName: "t", Allowed: allowed})
		l.LogAgentAccess(t.Context(), auth.AgentAccessEvent{UserID: "user1", AgentName: "a", Allowed: allowed})
		if strings.Contains(buf.String(), "credentialID") {
			t.Errorf("allowed=%v: a person's access decision carries a credentialID key: %s", allowed, buf.String())
		}
	}
}

func TestLogAuth_CredentialIDOnlyWhenAKeySubmittedTheTask(t *testing.T) {
	l, buf := testLogger()
	l.LogAuth(t.Context(), auth.AuthEvent{UserID: "system:ci-deployer", CredentialID: "key-1", TaskID: "t1", Success: true})
	out := buf.String()
	if !strings.Contains(out, "authentication succeeded") || !strings.Contains(out, "credentialID=key-1") {
		t.Errorf("a key's authentication line does not name the key: %s", out)
	}

	l, buf = testLogger()
	l.LogAuth(t.Context(), auth.AuthEvent{UserID: "user1", TaskID: "t1", Success: true})
	if strings.Contains(buf.String(), "credentialID") {
		t.Errorf("a person's authentication line carries a credentialID key: %s", buf.String())
	}
}

func TestLogToolExecution_ResultErrorLogsWarn(t *testing.T) {
	for _, code := range []string{"tool_error", "TOOL_ERROR", "validation", "INVALID_REQUEST", "ARTIFACT_NOT_FOUND", "canceled", "auth_required", "backend_4xx"} {
		l, buf := testLogger()
		l.LogToolExecution(context.Background(), auth.ToolExecutionEvent{
			UserID:   "user1",
			ToolName: "some_tool",
			Success:  false,
			Error:    code,
		})
		out := buf.String()
		if !strings.Contains(out, "tool execution failed") {
			t.Errorf("code %q: expected 'tool execution failed', got: %s", code, out)
		}
		if !strings.Contains(out, "level=WARN") || strings.Contains(out, "level=ERROR") {
			t.Errorf("code %q: expected WARN level, got: %s", code, out)
		}
	}
}

func TestLogToolExecution_InfraErrorLogsError(t *testing.T) {
	for _, code := range []string{"timeout", "backend_5xx", "internal", "TOOL_TIMEOUT", "SANDBOX_FAILED", "INTERNAL_ERROR", "ARTIFACT_ERROR", "something_new"} {
		l, buf := testLogger()
		l.LogToolExecution(context.Background(), auth.ToolExecutionEvent{
			UserID:   "user1",
			ToolName: "some_tool",
			Success:  false,
			Error:    code,
		})
		out := buf.String()
		if !strings.Contains(out, "level=ERROR") {
			t.Errorf("code %q: expected ERROR level, got: %s", code, out)
		}
	}
}

func TestSequenceMonotonic_AcrossEventTypes(t *testing.T) {
	l, buf := testLoggerJSON()
	ctx := context.Background()

	l.LogAuth(ctx, auth.AuthEvent{UserID: "u", TaskID: "t", Success: true})
	l.LogToolAccess(ctx, auth.ToolAccessEvent{UserID: "u", ToolName: "x", Allowed: true})
	l.LogAgentAccess(ctx, auth.AgentAccessEvent{UserID: "u", AgentName: "a", Allowed: true})
	l.LogControlAccess(ctx, auth.ControlAccessEvent{UserID: "u", Method: "m", Allowed: true})
	l.LogToolExecution(ctx, auth.ToolExecutionEvent{UserID: "u", ToolName: "x", Success: true})
	l.LogShareGrant(ctx, auth.ShareGrantEvent{Actor: "o", ResourceType: "prompt_group", ResourceID: "r", RecipientEmail: "a@b", AccessLevel: "resource_viewer"})
	l.LogShareLevelChange(ctx, auth.ShareLevelChangeEvent{Actor: "o", ResourceType: "prompt_group", ResourceID: "r", RecipientEmail: "a@b", AccessLevel: "resource_editor"})
	l.LogShareRevoke(ctx, auth.ShareRevokeEvent{Actor: "o", ResourceType: "prompt_group", ResourceID: "r", RecipientEmail: "a@b"})
	l.LogTemplateDeleted(ctx, auth.TemplateDeletedEvent{Actor: "o", ResourceType: "prompt_group", ResourceID: "r", RecipientEmails: []string{"a@b", "c@d"}})

	var seqs []float64
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if line == "" {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("decode: %v (line=%q)", err, line)
		}
		seqRaw, ok := rec["seq"]
		if !ok {
			continue
		}
		seq, ok := seqRaw.(float64)
		if !ok {
			t.Fatalf("seq is not a number: %T (record=%v)", seqRaw, rec)
		}
		seqs = append(seqs, seq)
	}

	if len(seqs) != 9 {
		t.Fatalf("expected 9 audit events with seq, got %d (seqs=%v)", len(seqs), seqs)
	}
	for i := 1; i < len(seqs); i++ {
		if seqs[i] <= seqs[i-1] {
			t.Errorf("seq not strictly monotonic at index %d: %v then %v", i, seqs[i-1], seqs[i])
		}
	}
	if seqs[0] != 1 {
		t.Errorf("expected first seq == 1, got %v", seqs[0])
	}
}

func TestNonJSONHandlerEmitsStartupWarn(t *testing.T) {
	var buf bytes.Buffer
	handler := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	_ = NewStructuredLogger(slog.New(handler))

	out := buf.String()
	if !strings.Contains(out, "non-JSON slog handler") {
		t.Errorf("expected startup WARN about non-JSON handler, got: %q", out)
	}
	if !strings.Contains(out, "WARN") {
		t.Errorf("expected WARN level, got: %q", out)
	}
}

func TestJSONHandlerNoStartupWarn(t *testing.T) {
	var buf bytes.Buffer
	handler := slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	_ = NewStructuredLogger(slog.New(handler))

	if buf.Len() != 0 {
		t.Errorf("expected no startup output for JSON handler, got: %q", buf.String())
	}
}

type jsonInsideHandler struct {
	*slog.JSONHandler
}

func (jsonInsideHandler) JSONHandlerInside() bool { return true }

func TestJSONHandlerInsideMarkerSkipsWarn(t *testing.T) {
	var buf bytes.Buffer
	jh := slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	wrapped := jsonInsideHandler{JSONHandler: jh}
	_ = NewStructuredLogger(slog.New(wrapped))

	if buf.Len() != 0 {
		t.Errorf("expected no startup output for JSON-wrapper handler, got: %q", buf.String())
	}
}

func TestLogShareGrant(t *testing.T) {
	l, buf := testLogger()
	l.LogShareGrant(context.Background(), auth.ShareGrantEvent{
		Actor:          "owner@example.com",
		ResourceType:   "prompt_group",
		ResourceID:     "grp-1",
		RecipientEmail: "alice@example.com",
		AccessLevel:    "resource_viewer",
	})
	out := buf.String()
	if !strings.Contains(out, "share granted") {
		t.Errorf("expected 'share granted', got: %s", out)
	}
	if !strings.Contains(out, "INFO") {
		t.Errorf("expected INFO level, got: %s", out)
	}
	if !strings.Contains(out, "owner@example.com") {
		t.Errorf("expected actor in output, got: %s", out)
	}
	if !strings.Contains(out, "prompt_group") {
		t.Errorf("expected resourceType in output, got: %s", out)
	}
	if !strings.Contains(out, "grp-1") {
		t.Errorf("expected resourceID in output, got: %s", out)
	}
	if strings.Contains(out, "alice@example.com") {
		t.Errorf("recipient email must not be logged, got: %s", out)
	}
	if !strings.Contains(out, strutil.PIIFingerprint("alice@example.com")) {
		t.Errorf("expected recipientEmailFP in output, got: %s", out)
	}
	if !strings.Contains(out, "resource_viewer") {
		t.Errorf("expected accessLevel in output, got: %s", out)
	}
}

func TestLogShareLevelChange(t *testing.T) {
	l, buf := testLogger()
	l.LogShareLevelChange(context.Background(), auth.ShareLevelChangeEvent{
		Actor:          "owner@example.com",
		ResourceType:   "prompt_group",
		ResourceID:     "grp-2",
		RecipientEmail: "bob@example.com",
		AccessLevel:    "resource_editor",
	})
	out := buf.String()
	if !strings.Contains(out, "share level changed") {
		t.Errorf("expected 'share level changed', got: %s", out)
	}
	if !strings.Contains(out, "INFO") {
		t.Errorf("expected INFO level, got: %s", out)
	}
	if !strings.Contains(out, "owner@example.com") {
		t.Errorf("expected actor in output, got: %s", out)
	}
	if !strings.Contains(out, "prompt_group") {
		t.Errorf("expected resourceType in output, got: %s", out)
	}
	if !strings.Contains(out, "grp-2") {
		t.Errorf("expected resourceID in output, got: %s", out)
	}
	if strings.Contains(out, "bob@example.com") {
		t.Errorf("recipient email must not be logged, got: %s", out)
	}
	if !strings.Contains(out, strutil.PIIFingerprint("bob@example.com")) {
		t.Errorf("expected recipientEmailFP in output, got: %s", out)
	}
	if !strings.Contains(out, "resource_editor") {
		t.Errorf("expected accessLevel in output, got: %s", out)
	}
}

func TestLogShareRevoke(t *testing.T) {
	l, buf := testLogger()
	l.LogShareRevoke(context.Background(), auth.ShareRevokeEvent{
		Actor:          "owner@example.com",
		ResourceType:   "prompt_group",
		ResourceID:     "grp-3",
		RecipientEmail: "carol@example.com",
	})
	out := buf.String()
	if !strings.Contains(out, "share revoked") {
		t.Errorf("expected 'share revoked', got: %s", out)
	}
	if !strings.Contains(out, "INFO") {
		t.Errorf("expected INFO level, got: %s", out)
	}
	if !strings.Contains(out, "owner@example.com") {
		t.Errorf("expected actor in output, got: %s", out)
	}
	if !strings.Contains(out, "prompt_group") {
		t.Errorf("expected resourceType in output, got: %s", out)
	}
	if !strings.Contains(out, "grp-3") {
		t.Errorf("expected resourceID in output, got: %s", out)
	}
	if strings.Contains(out, "carol@example.com") {
		t.Errorf("recipient email must not be logged, got: %s", out)
	}
	if !strings.Contains(out, strutil.PIIFingerprint("carol@example.com")) {
		t.Errorf("expected recipientEmailFP in output, got: %s", out)
	}
}

func TestLogTemplateDeleted(t *testing.T) {
	l, buf := testLoggerJSON()
	l.LogTemplateDeleted(context.Background(), auth.TemplateDeletedEvent{
		Actor:           "owner@example.com",
		ResourceType:    "prompt_group",
		ResourceID:      "grp-4",
		RecipientEmails: []string{"alice@example.com", "bob@example.com"},
	})

	line := strings.TrimSpace(buf.String())
	var rec map[string]any
	if err := json.Unmarshal([]byte(line), &rec); err != nil {
		t.Fatalf("decode: %v (line=%q)", err, line)
	}
	if got, want := rec["msg"], "template deleted"; got != want {
		t.Errorf("msg: got %v, want %v", got, want)
	}
	if got, want := rec["level"], "INFO"; got != want {
		t.Errorf("level: got %v, want %v", got, want)
	}
	if got, want := rec["actor"], "owner@example.com"; got != want {
		t.Errorf("actor: got %v, want %v", got, want)
	}
	if got, want := rec["resourceType"], "prompt_group"; got != want {
		t.Errorf("resourceType: got %v, want %v", got, want)
	}
	if got, want := rec["resourceID"], "grp-4"; got != want {
		t.Errorf("resourceID: got %v, want %v", got, want)
	}
	raw, ok := rec["recipientEmailFPs"]
	if !ok {
		t.Fatalf("recipientEmailFPs missing from record: %v", rec)
	}
	arr, ok := raw.([]any)
	if !ok {
		t.Fatalf("recipientEmailFPs not a JSON array: %T (%v)", raw, raw)
	}
	wantA, wantB := strutil.PIIFingerprint("alice@example.com"), strutil.PIIFingerprint("bob@example.com")
	if len(arr) != 2 || arr[0] != wantA || arr[1] != wantB {
		t.Errorf("recipientEmailFPs: got %v, want [%s %s]", arr, wantA, wantB)
	}
}

func TestLogConsentChange(t *testing.T) {
	l, buf := testLoggerJSON()
	l.LogConsentChange(context.Background(), auth.ConsentChangeEvent{
		Actor: "alice@example.com",
		Scope: "install",
		From:  "unset",
		To:    "granted",
	})

	line := strings.TrimSpace(buf.String())
	var rec map[string]any
	if err := json.Unmarshal([]byte(line), &rec); err != nil {
		t.Fatalf("decode: %v (line=%q)", err, line)
	}
	for field, want := range map[string]string{
		"msg":          "consent changed",
		"level":        "INFO",
		"actor":        "alice@example.com",
		"consentScope": "install",
		"from":         "unset",
		"to":           "granted",
	} {
		if got := rec[field]; got != want {
			t.Errorf("%s: got %v, want %v", field, got, want)
		}
	}
	if _, ok := rec["seq"]; !ok {
		t.Error("seq missing — consent events must be ordered with the rest of the audit stream")
	}
}

func TestLogActivationChange(t *testing.T) {
	l, buf := testLoggerJSON()
	l.LogActivationChange(context.Background(), auth.ActivationChangeEvent{
		Actor:          "sam_dev_user",
		Change:         auth.ActivationChangeAccepted,
		Subject:        "auth0|ada",
		FreeModelTerms: true,
		AgreementID:    "agreement-1",
	})

	line := strings.TrimSpace(buf.String())
	var rec map[string]any
	if err := json.Unmarshal([]byte(line), &rec); err != nil {
		t.Fatalf("decode: %v (line=%q)", err, line)
	}
	for field, want := range map[string]any{
		"msg":            "activation changed",
		"level":          "INFO",
		"actor":          "sam_dev_user",
		"change":         auth.ActivationChangeAccepted,
		"subject":        "auth0|ada",
		"freeModelTerms": true,
		"agreementId":    "agreement-1",
	} {
		if got := rec[field]; got != want {
			t.Errorf("%s: got %v, want %v", field, got, want)
		}
	}
	if _, ok := rec["seq"]; !ok {
		t.Error("seq missing — activation events must be ordered with the rest of the audit stream")
	}
}

func TestEveryEventNamesItsProcess(t *testing.T) {
	first, firstBuf := testLoggerJSON()
	second, secondBuf := testLoggerJSON()
	ctx := t.Context()

	first.LogAuth(ctx, auth.AuthEvent{UserID: "u", Success: true})
	first.LogAuth(ctx, auth.AuthEvent{UserID: "u", Success: false, Reason: "r"})
	second.LogAuth(ctx, auth.AuthEvent{UserID: "u", Success: true})

	processIDs := func(buf *bytes.Buffer) []string {
		var ids []string
		for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
			var rec map[string]any
			if err := json.Unmarshal([]byte(line), &rec); err != nil {
				t.Fatalf("decode: %v (line=%q)", err, line)
			}
			id, _ := rec["processID"].(string)
			ids = append(ids, id)
		}
		return ids
	}
	firstIDs, secondIDs := processIDs(firstBuf), processIDs(secondBuf)
	if len(firstIDs) != 2 || firstIDs[0] == "" || firstIDs[0] != firstIDs[1] {
		t.Fatalf("first logger processIDs = %v, want one non-empty id on every event", firstIDs)
	}
	if len(secondIDs) != 1 || secondIDs[0] == "" || secondIDs[0] == firstIDs[0] {
		t.Fatalf("second logger processID = %v, want an id of its own (first %q)", secondIDs, firstIDs[0])
	}
}

// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package whatsapp

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
)

func authRequiredEvent(authURI string) *a2a.TaskStatusUpdateEvent {
	return &a2a.TaskStatusUpdateEvent{
		Kind: "status-update",
		Status: a2a.TaskStatus{
			State: a2a.TaskStateInputNeeded,
			Message: &a2a.Message{
				Role: "agent",
				Parts: []a2a.Part{a2a.NewDataPart(map[string]any{
					"type":              "authentication_required",
					"target_agent":      "Builder",
					"auth_uri":          authURI,
					"tool_display_name": "Atlassian",
				})},
			},
		},
	}
}

func authResultEvent(success bool) *a2a.TaskStatusUpdateEvent {
	return &a2a.TaskStatusUpdateEvent{
		Kind: "status-update",
		Status: a2a.TaskStatus{
			State: a2a.TaskStateWorking,
			Message: &a2a.Message{
				Role: "agent",
				Parts: []a2a.Part{a2a.NewDataPart(map[string]any{
					"type":    "authentication_result",
					"success": success,
				})},
			},
		},
	}
}

func registerAuthTask(a *Adapter, taskID, phone string) *taskMeta {
	_ = a.core.Tasks().Register(taskID, "Builder", "user", entrypoint.TaskIdentity{})
	meta := &taskMeta{FromPhone: phone, SessionID: "sess"}
	a.core.Tasks().SetMetadata(taskID, meta)
	return meta
}

func TestOnStatusUpdate_AuthRequiredSendsCTAURL(t *testing.T) {
	t.Parallel()
	mock := newWAMock(t)
	a := newAdapterRig(t, mock, "Builder")
	meta := registerAuthTask(a, "task-auth", "15551230001")

	const uri = "https://idp.example/authorize?state=s1"
	a.OnStatusUpdate(context.Background(), "task-auth", authRequiredEvent(uri), nil)

	raws := mock.rawSends()
	if len(raws) != 1 {
		t.Fatalf("sends = %d, want 1 (the cta_url prompt)", len(raws))
	}
	var sent outboundInteractiveMessage
	if err := json.Unmarshal(raws[0], &sent); err != nil {
		t.Fatalf("unmarshal cta_url send: %v", err)
	}
	if sent.Type != "interactive" || sent.Interactive.Type != "cta_url" {
		t.Errorf("wire type = %q/%q, want interactive/cta_url", sent.Type, sent.Interactive.Type)
	}
	act := sent.Interactive.Action
	if act.Name != "cta_url" || act.Parameters == nil || act.Parameters.URL != uri || act.Parameters.DisplayText != authorizeButtonLabel {
		t.Errorf("cta action = %+v, want name=cta_url url=%q display=%q", act, uri, authorizeButtonLabel)
	}
	if !strings.Contains(sent.Interactive.Body.Text, "Atlassian") {
		t.Errorf("prompt body %q should name the service", sent.Interactive.Body.Text)
	}
	meta.mu.Lock()
	tracked := len(meta.authPromptIDs)
	meta.mu.Unlock()
	if tracked != 1 {
		t.Errorf("authPromptIDs = %d, want 1", tracked)
	}
}

func TestOnStatusUpdate_AuthRequired_UnpresentableURISkipped(t *testing.T) {
	t.Parallel()
	mock := newWAMock(t)
	a := newAdapterRig(t, mock, "Builder")
	registerAuthTask(a, "task-auth", "15551230001")

	for _, uri := range []string{
		"http://idp.example/authorize",
		"https:///no-host",
		"https://idp.example/auth\x7f",
		"://not-a-uri",
	} {
		a.OnStatusUpdate(context.Background(), "task-auth", authRequiredEvent(uri), nil)
	}

	if got := len(mock.rawSends()); got != 0 {
		t.Errorf("sends = %d, want 0 (non-https, host-less, and unparseable consent URLs must not be presented)", got)
	}
}

func TestOnStatusUpdate_AuthResultResolvesPromptOnce(t *testing.T) {
	t.Parallel()
	mock := newWAMock(t)
	a := newAdapterRig(t, mock, "Builder")
	registerAuthTask(a, "task-auth", "15551230001")

	a.OnStatusUpdate(context.Background(), "task-auth", authRequiredEvent("https://idp.example/a"), nil)
	a.OnStatusUpdate(context.Background(), "task-auth", authResultEvent(true), nil)
	a.OnStatusUpdate(context.Background(), "task-auth", authResultEvent(true), nil)

	var confirmations int
	for _, s := range mock.rawSends() {
		if strings.Contains(string(s), authSuccessMessage) {
			confirmations++
		}
	}
	if confirmations != 1 {
		t.Errorf("success confirmations = %d, want exactly 1 (idempotent resolution)", confirmations)
	}
}

func TestAuthPrompt_FallsBackToTextWhenCTAURLRejected(t *testing.T) {
	t.Parallel()
	mock := newWAMock(t)
	mock.rejectInteractive = true
	a := newAdapterRig(t, mock, "Builder")
	meta := registerAuthTask(a, "task-auth", "15551230001")

	const uri = "https://idp.example/authorize?state=s2"
	a.OnStatusUpdate(context.Background(), "task-auth", authRequiredEvent(uri), nil)

	raws := mock.attempts()
	if len(raws) != 2 {
		t.Fatalf("attempts = %d, want 2 (rejected cta_url, then text fallback)", len(raws))
	}
	var fallback outboundTextMessage
	if err := json.Unmarshal(raws[1], &fallback); err != nil {
		t.Fatalf("unmarshal fallback: %v", err)
	}
	if fallback.Type != "text" || !strings.Contains(fallback.Text.Body, uri) {
		t.Errorf("fallback = type %q body %q, want text carrying the consent URL", fallback.Type, fallback.Text.Body)
	}
	meta.mu.Lock()
	tracked := len(meta.authPromptIDs)
	meta.mu.Unlock()
	if tracked != 1 {
		t.Errorf("authPromptIDs = %d, want 1 (fallback prompt still tracked)", tracked)
	}
}

func TestOnFinalResponse_FailedTaskResolvesPromptAsFailed(t *testing.T) {
	t.Parallel()
	mock := newWAMock(t)
	a := newAdapterRig(t, mock, "Builder")
	registerAuthTask(a, "task-auth", "15551230001")

	a.OnStatusUpdate(context.Background(), "task-auth", authRequiredEvent("https://idp.example/a"), nil)
	a.OnFinalResponse(context.Background(), "task-auth", &a2a.Task{
		Status: a2a.TaskStatus{State: a2a.TaskStateFailed},
	}, nil)

	var sawFailure bool
	for _, s := range mock.rawSends() {
		if strings.Contains(string(s), authFailureMessage) {
			sawFailure = true
		}
	}
	if !sawFailure {
		t.Error("failed terminal task must send the failure confirmation for the outstanding prompt")
	}
}

func TestOnError_ResolvesPromptAsFailed(t *testing.T) {
	t.Parallel()
	mock := newWAMock(t)
	a := newAdapterRig(t, mock, "Builder")
	registerAuthTask(a, "task-auth", "15551230001")

	a.OnStatusUpdate(context.Background(), "task-auth", authRequiredEvent("https://idp.example/a"), nil)
	a.OnError(context.Background(), "task-auth", &a2a.Error{Code: -32000, Message: "boom"}, nil)

	var sawFailure bool
	for _, s := range mock.rawSends() {
		if strings.Contains(string(s), authFailureMessage) {
			sawFailure = true
		}
	}
	if !sawFailure {
		t.Error("task error must send the failure confirmation for the outstanding prompt")
	}
}

func TestResolveAuthPrompt_ConcurrentResolversConfirmOnce(t *testing.T) {
	t.Parallel()
	mock := newWAMock(t)
	a := newAdapterRig(t, mock, "Builder")
	meta := registerAuthTask(a, "task-auth", "15551230001")

	a.OnStatusUpdate(context.Background(), "task-auth", authRequiredEvent("https://idp.example/a"), nil)

	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() { a.resolveAuthPrompt("task-auth", meta, true) })
	}
	wg.Wait()

	var confirmations int
	for _, s := range mock.rawSends() {
		if strings.Contains(string(s), authSuccessMessage) {
			confirmations++
		}
	}
	if confirmations != 1 {
		t.Errorf("confirmations = %d, want exactly 1 under concurrent resolution", confirmations)
	}
}

func TestAuthPrompt_UndeliverableTellsTheUser(t *testing.T) {
	t.Parallel()
	mock := newWAMock(t)
	a := newAdapterRig(t, mock, "Builder")
	meta := registerAuthTask(a, "task-auth", "15551230001")

	a.OnStatusUpdate(context.Background(), "task-auth", authRequiredEvent("https://idp.example/authorize?state=s1"), nil)
	meta.mu.Lock()
	if len(meta.authPromptIDs) != 1 {
		meta.mu.Unlock()
		t.Fatal("the auth prompt was not queued, so this proves nothing")
	}
	outboundID := meta.authPromptIDs[0]
	meta.mu.Unlock()

	a.onDeliveryFailed(&store.WhatsAppOutbound{
		ID: outboundID, GatewayID: a.gatewayID(), TaskID: "task-auth",
		Recipient: "15551230001", Kind: kindInteractivePrompt,
		State: store.WhatsAppOutboundFailed,
	}, true)

	if !slices.ContainsFunc(mock.calls(), func(m outboundTextMessage) bool {
		return strings.Contains(m.Text.Body, "couldn't deliver an authorization request")
	}) {
		t.Errorf("no notice about the undeliverable authorization prompt; sent %+v", mock.calls())
	}
	meta.mu.Lock()
	defer meta.mu.Unlock()
	if len(meta.authPromptIDs) != 0 {
		t.Errorf("authPromptIDs = %v, want the withdrawn prompt dropped", meta.authPromptIDs)
	}
}

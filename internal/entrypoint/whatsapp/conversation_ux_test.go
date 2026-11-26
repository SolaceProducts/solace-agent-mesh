// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package whatsapp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
)

func TestAdapter_SubmitDenied_NotAuthorizedReply(t *testing.T) {
	const agentName = "TestAgent"
	mock := newWAMock(t)
	a := newAdapterRig(t, mock, agentName)
	a.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: agentName})
	a.core.SetScopeResolver(waScopeResolver{scopes: nil})

	value, msg := textMessage("wamid.denied", "15551230000", "hello")
	deliveries := driveToSettlement(t, a, value, msg)
	if deliveries != maxAuthzDenialAttempts+1 {
		t.Fatalf("deliveries before the decline = %d, want %d — a denial must be retried, "+
			"because an authz lookup failure is indistinguishable from a real refusal",
			deliveries, maxAuthzDenialAttempts+1)
	}

	calls := mock.calls()
	if len(calls) != 1 {
		t.Fatalf("expected exactly one reply, got %d: %+v", len(calls), calls)
	}
	got := calls[0].Text.Body
	if got == entrypoint.SubmitFailedText {
		t.Errorf("denied sender got the generic retryable message %q; a permission boundary must read differently so the user does not retry forever", got)
	}
	if !strings.Contains(got, "don't have access") {
		t.Errorf("reply = %q, want a refusal naming the permission boundary", got)
	}
	if strings.Contains(strings.ToLower(got), "sign in") {
		t.Errorf("reply = %q, must not tell a WhatsApp user to sign in: their identity keys on a verified phone claim, so a WebUI login does not link them", got)
	}
	if readTypingPosted(mock.rawSends()) {
		t.Error("a denied sender must not see a typing indicator (it is posted only after a successful submit)")
	}
	if got := a.claimState(msg.ID); got != store.WhatsAppMessageDone {
		t.Errorf("claim state after the decline = %q, want %q — a told sender must not be retried again",
			got, store.WhatsAppMessageDone)
	}
}

func TestAdapter_SubmitDenied_SurvivesAnAuthzBlip(t *testing.T) {
	const agentName = "TestAgent"
	mock := newWAMock(t)
	a := newAdapterRig(t, mock, agentName)
	a.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: agentName})
	a.core.SetScopeResolver(waScopeResolver{scopes: nil})

	value, msg := textMessage("wamid.blip", "15551230000", "hello")
	if got := a.deliver(value, msg); got != settleNack {
		t.Fatalf("denial settled %v, want settleNack so the redelivery can retry", got)
	}
	if len(mock.calls()) != 0 {
		t.Fatalf("a retried denial must stay silent, got %+v", mock.calls())
	}

	a.core.SetScopeResolver(waScopeResolver{scopes: []string{"*"}})
	if got := a.deliver(value, msg); got != settleAck {
		t.Fatalf("redelivery after authz recovered settled %v, want settleAck", got)
	}
	if ids := a.core.Tasks().AllByAgent(agentName); len(ids) != 1 {
		t.Fatalf("tasks submitted = %d, want 1 — the blip must not have cost the message", len(ids))
	}
}

func readTypingPosted(raw [][]byte) bool {
	for _, b := range raw {
		var s outboundReadStatus
		if err := json.Unmarshal(b, &s); err != nil {
			continue
		}
		if s.Status == "read" && s.MessageID != "" && s.TypingIndicator != nil && s.TypingIndicator.Type == "text" {
			return true
		}
	}
	return false
}

func TestAdapter_TypingIndicator(t *testing.T) {
	const agentName = "TestAgent"
	mock := newWAMock(t)
	a := newAdapterRig(t, mock, agentName)
	a.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: agentName})

	value, msg := textMessage("wamid.typing", "15551230000", "hello")
	a.deliver(value, msg)

	if !readTypingPosted(mock.rawSends()) {
		t.Fatalf("expected a read+typing status POST, got none in %d sends", len(mock.rawSends()))
	}
	for _, c := range mock.calls() {
		if c.Type == "text" {
			t.Errorf("typing indicator must not send a text message, got body %q", c.Text.Body)
		}
	}
}

func TestAdapter_TypingTrack_EmptyWamidPostsNothing(t *testing.T) {
	mock := newWAMock(t)
	a := newAdapterRig(t, mock, "TestAgent")

	a.typingTrack("15551230000", "task-1", "")
	a.typingShow("15551230000")

	if n := len(mock.rawSends()); n != 0 {
		t.Fatalf("empty wamid must post nothing, got %d", n)
	}
}

func TestAdapter_TypingShow_FailureIsNonBlocking(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"code":100,"message":"bad request"}}`))
	}))
	t.Cleanup(srv.Close)

	cfg := &WhatsAppConfig{
		AccessToken:         "tok",
		PhoneNumberID:       "555",
		GraphAPIVersion:     "v26.0",
		GraphAPIBaseURL:     srv.URL,
		DisableRateLimiting: true,
	}
	a := &Adapter{cfg: cfg, logger: discardLogger(), client: NewClient(cfg, discardLogger()), ctx: t.Context()}

	a.typingTrack("15551230000", "task-1", "wamid.fail")
	a.typingShow("15551230000")
	a.stopTyping()
}

func driveToSettlement(t *testing.T, a *Adapter, value *webhookChangeValue, msg *inboundMessage) int {
	t.Helper()
	for i := 1; i <= 64; i++ {
		if a.deliver(value, msg) == settleAck {
			return i
		}
	}
	t.Fatal("message never settled after 64 deliveries")
	return 0
}

func TestAdapter_AuthzBudgetIsNotSpentByAnOutage(t *testing.T) {
	const agentName = "TestAgent"
	mock := newWAMock(t)
	a := newAdapterRig(t, mock, "")

	value, msg := textMessage("wamid.budget", "15551230000", "hello")
	for range maxAuthzDenialAttempts {
		if got := a.deliver(value, msg); got != settleNack {
			t.Fatalf("infrastructure failure settled %v, want settleNack", got)
		}
	}
	if len(mock.calls()) != 0 {
		t.Fatalf("an infrastructure outage must stay silent, got %+v", mock.calls())
	}

	a.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: agentName})
	a.core.SetScopeResolver(waScopeResolver{scopes: nil})

	if got := a.deliver(value, msg); got != settleNack {
		t.Fatalf("first authz refusal after an outage settled %v, want settleNack — "+
			"a shared counter would decline it immediately", got)
	}
	if sentText(mock.calls(), msgNotAuthorized) {
		t.Fatalf("the sender was told 'not authorized' on the first refusal; the short budget was already spent")
	}
}

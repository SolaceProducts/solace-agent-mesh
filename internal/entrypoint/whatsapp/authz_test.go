// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package whatsapp

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

type waScopeResolver struct{ scopes []string }

func (r waScopeResolver) ResolveScopes(string, []string) []string { return r.scopes }

func TestWhatsAppSubmit_RBACEnforced(t *testing.T) {
	const agentName = "TestAgent"

	tests := []struct {
		name          string
		scopes        []string
		wantPublished bool
	}{
		{name: "scope-less identity denied", scopes: nil, wantPublished: false},
		{name: "wrong agent scope denied", scopes: []string{rbac.AgentInvokeScope("OtherAgent")}, wantPublished: false},
		{name: "correct invoke scope allowed", scopes: []string{rbac.AgentInvokeScope(agentName)}, wantPublished: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mock := newWAMock(t)
			a := newAdapterRig(t, mock, agentName)
			a.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: agentName})
			a.core.SetScopeResolver(waScopeResolver{scopes: tc.scopes})

			brk := a.core.Services().Broker()
			q, err := brk.CreateQueue("wa-authz-q", broker.QueueOptions{Named: true})
			if err != nil {
				t.Fatalf("create queue: %v", err)
			}
			if err = brk.Subscribe(q, a2a.AgentRequestTopic("test-ns", agentName)); err != nil {
				t.Fatalf("subscribe: %v", err)
			}

			value, msg := textMessage("wamid.authz", "15551230000", "invoke the agent")
			a.deliver(value, msg)

			timeout := 300 * time.Millisecond
			if tc.wantPublished {
				timeout = 2 * time.Second
			}
			recvCtx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()
			received, _ := brk.Receive(recvCtx, q)

			if tc.wantPublished && received == nil {
				t.Fatal("expected task published to agent request topic, got none")
			}
			if !tc.wantPublished && received != nil {
				t.Fatalf("expected no task published (RBAC deny), got payload=%s", string(received.Payload))
			}
		})
	}
}

func TestWhatsAppHILResume_RBACEnforced(t *testing.T) {
	const agentName = "TestAgent"
	const phone = "15551230000"

	tests := []struct {
		name          string
		scopes        []string
		wantPublished bool
	}{
		{name: "correct invoke scope reaches the agent", scopes: []string{rbac.AgentInvokeScope(agentName)}, wantPublished: true},
		{name: "scope-less identity denied", scopes: nil, wantPublished: false},
		{name: "wrong agent scope denied", scopes: []string{rbac.AgentInvokeScope("OtherAgent")}, wantPublished: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mock := newWAMock(t)
			a := newAdapterRig(t, mock, agentName)
			a.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: agentName})
			a.core.SetScopeResolver(waScopeResolver{scopes: tc.scopes})
			_ = a.core.Tasks().Register("t-hil", agentName, "whatsapp:"+phone, entrypoint.TaskIdentity{})
			a.core.Tasks().SetMetadata("t-hil", &taskMeta{FromPhone: phone, SessionID: "sess"})

			brk := a.core.Services().Broker()
			q, err := brk.CreateQueue("wa-hil-authz-q", broker.QueueOptions{Named: true})
			if err != nil {
				t.Fatalf("create queue: %v", err)
			}
			if err = brk.Subscribe(q, a2a.AgentRequestTopic("test-ns", agentName)); err != nil {
				t.Fatalf("subscribe: %v", err)
			}

			a.OnStatusUpdate(t.Context(), "t-hil", inputNeededEvent(t, "req-1", json.RawMessage(askSurfaceSingleChoiceWA)), nil)
			waitPromptDelivered(t, a, phone)

			value, msg := interactiveReply(phone, "wamid.hil-reply", optionRefID(1))
			if tc.wantPublished {
				a.deliver(value, msg)
			} else {
				if deliveries := driveToSettlement(t, a, value, msg); deliveries != maxAuthzDenialAttempts+1 {
					t.Fatalf("deliveries before the HIL decline = %d, want %d — a denied tap must be retried",
						deliveries, maxAuthzDenialAttempts+1)
				}
			}

			timeout := 300 * time.Millisecond
			if tc.wantPublished {
				timeout = 2 * time.Second
			}
			recvCtx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()
			received, _ := brk.Receive(recvCtx, q)

			stillPending := pendingPromptRow(t, a, phone) != nil
			if tc.wantPublished {
				if received == nil {
					t.Fatal("expected the user's answer published to the agent request topic, got none")
				}
				if stillPending {
					t.Error("a submitted answer must clear the pending prompt")
				}
				return
			}
			if received != nil {
				t.Fatalf("expected no answer published (RBAC deny), got payload=%s", string(received.Payload))
			}
			if !stillPending {
				t.Error("a denied answer must leave the prompt pending so the user can retry")
			}
			if !sentText(mock.calls(), msgHILSubmitFailed) {
				t.Errorf("expected the decline notice %q, got sends %+v", msgHILSubmitFailed, mock.calls())
			}
			if sentText(mock.calls(), msgNotAuthorized) {
				t.Errorf("the HIL leg must not use the submit path's wording, got sends %+v", mock.calls())
			}
		})
	}
}

func sentText(sends []outboundTextMessage, body string) bool {
	for _, s := range sends {
		if s.Type == "text" && s.Text.Body == body {
			return true
		}
	}
	return false
}

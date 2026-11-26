// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package harness

import (
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
)

func TestTestGatewaySatisfiesOAuthCapableGateway(t *testing.T) {
	var _ OAuthCapableGateway = (*TestGateway)(nil)
}

func TestTestGatewaySendRequestMethodAndStatusTopic(t *testing.T) {
	tests := []struct {
		name            string
		input           GatewayInput
		wantMethod      string
		wantStatusTopic bool
	}{
		{"default", GatewayInput{}, "message/send", true},
		{"streaming", GatewayInput{Streaming: true}, "message/stream", true},
		{"omit_status_topic", GatewayInput{OmitStatusTopic: true}, "message/send", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			brk := broker.NewDevBroker()
			if err := brk.Connect(t.Context()); err != nil {
				t.Fatalf("connect: %v", err)
			}
			q, err := brk.CreateQueue("agent-q", broker.QueueOptions{})
			if err != nil {
				t.Fatalf("create queue: %v", err)
			}
			if err = brk.Subscribe(q, a2a.AgentRequestTopic("ns-test", "target")); err != nil {
				t.Fatalf("subscribe: %v", err)
			}
			gw := NewTestGateway("gw-test", "ns-test", brk, slog.Default())
			input := tt.input
			input.Parts = []InputPart{{Type: "text", Text: "hi"}}
			if _, err = gw.SendRequest(t.Context(), "target", &input); err != nil {
				t.Fatalf("SendRequest: %v", err)
			}

			msg, err := brk.Receive(t.Context(), q)
			if err != nil {
				t.Fatalf("receive: %v", err)
			}
			var req a2a.Request
			if err = json.Unmarshal(msg.Payload, &req); err != nil {
				t.Fatalf("decode request: %v", err)
			}
			if req.Method != tt.wantMethod {
				t.Errorf("method = %q, want %q", req.Method, tt.wantMethod)
			}
			if got := a2a.DecodeUserProperties(msg.UserProperties).A2AStatusTopic != ""; got != tt.wantStatusTopic {
				t.Errorf("has a2aStatusTopic = %v, want %v", got, tt.wantStatusTopic)
			}
		})
	}
}

func TestTestGatewayAuthRequiredChan(t *testing.T) {
	brk := broker.NewDevBroker()
	gw := NewTestGateway("gw-test", "ns-test", brk, slog.Default())

	ch := gw.AuthRequiredChan()
	if ch == nil {
		t.Fatal("AuthRequiredChan returned nil")
	}

	want := &a2a.AuthenticationRequiredData{CredentialKey: "k1", TargetAgent: "a1"}
	select {
	case gw.authRequiredCh <- want:
	default:
		t.Fatal("internal channel should accept send")
	}

	select {
	case got := <-ch:
		if got != want {
			t.Fatalf("got %v, want %v", got, want)
		}
	case <-t.Context().Done():
		t.Fatal("timed out waiting for signal")
	default:
		t.Fatal("AuthRequiredChan did not yield the signal")
	}
}

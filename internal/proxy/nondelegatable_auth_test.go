// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package proxy

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

func TestHandleRequest_NonDelegatablePrincipalShortCircuitsOAuth(t *testing.T) {
	p := newBrokerProxy(t)
	p.cfg.TaskAuth = &AuthConfig{Type: "oauth2_authorization_code"}
	ctx := context.Background()
	brk := p.svc.Broker()

	replyQ, err := brk.CreateQueue("nd-reply-q", broker.QueueOptions{})
	if err != nil {
		t.Fatalf("create queue: %v", err)
	}
	if subErr := brk.Subscribe(replyQ, "reply/topic"); subErr != nil {
		t.Fatalf("subscribe: %v", subErr)
	}

	reqBody := a2a.Request{
		Method: "message/send",
		ID:     "t1",
		Params: map[string]any{
			"message": map[string]any{
				"contextId": "sess-1",
				"parts":     []any{map[string]any{"kind": "text", "text": "hello"}},
			},
		},
	}
	data, _ := json.Marshal(reqBody)
	msg := broker.NewDeliveredMessage("m1", "topic", data, func() {}, nil)
	msg.UserProperties = map[string]string{
		"replyTo": "reply/topic",
		"userId":  rbac.DefaultSystemUserSub,
	}

	p.handleRequest(ctx, msg, &reqBody)

	recvCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	received, err := brk.Receive(recvCtx, replyQ)
	if err != nil {
		t.Fatalf("receive: %v", err)
	}
	if received == nil {
		t.Fatal("expected a terminal error response, got none")
	}
	var resp a2a.Response
	if err := json.Unmarshal(received.Payload, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Error == nil {
		t.Fatal("expected a JSON-RPC error response (guard), got a non-error result (auth flow ran)")
	}
	if !strings.Contains(resp.Error.Message, "DM me") {
		t.Errorf("error message = %q, want the non-delegation 'DM me' message", resp.Error.Message)
	}
	if p.tasks.Get("t1") != nil {
		t.Error("task should be removed after the short-circuit, not left paused for auth")
	}
}

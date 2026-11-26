// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
)

func authResponseMessage(t *testing.T, targetTaskID, fromUser string) (*broker.Message, *a2a.Request) {
	t.Helper()
	req := &a2a.Request{
		Method: "message/send",
		ID:     "auth-msg-" + fromUser,
		Params: map[string]any{"message": map[string]any{
			"parts": []any{map[string]any{"kind": "data", "data": map[string]any{
				"type":                 "auth_response",
				"target_agent_task_id": targetTaskID,
				"code":                 "authorization-code-marker",
			}}},
		}},
	}
	data, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	msg := broker.NewDeliveredMessage("m-"+fromUser, "topic", data, func() {}, nil)
	msg.UserProperties = map[string]string{"userId": fromUser}
	return msg, req
}

func TestAuthResponseFromAnotherUserDoesNotResumeThePausedTask(t *testing.T) {
	p := newBrokerProxy(t)
	p.cfg.TaskAuth = &AuthConfig{Type: "oauth2_authorization_code"}
	var logs bytes.Buffer
	p.logger = slog.New(slog.NewJSONHandler(&logs, nil))
	brk := p.svc.Broker()
	replyQ, err := brk.CreateQueue("auth-owner-reply-q", broker.QueueOptions{})
	if err != nil {
		t.Fatalf("create queue: %v", err)
	}
	if err = brk.Subscribe(replyQ, "reply/topic"); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	p.tasks.Add(&TaskContext{
		TaskID: "task-alice", UserID: "alice@dev.local", ReplyToTopic: "reply/topic",
		CredentialKey: "a2a_proxy_test-proxy", CreatedAt: time.Now(),
		OriginalRequest: &a2a.Request{Method: "message/send", ID: "task-alice"},
	})

	bobMsg, bobReq := authResponseMessage(t, "task-alice", "bob@dev.local")
	p.handleAuthResponse(context.Background(), bobMsg, bobReq)

	if p.tasks.Get("task-alice") == nil {
		t.Fatal("bob's auth_response consumed alice's paused task")
	}
	recvCtx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if got, _ := brk.Receive(recvCtx, replyQ); got != nil {
		t.Fatalf("bob's auth_response made the proxy answer on alice's reply topic: %s", got.Payload)
	}
	if n := strings.Count(logs.String(), "auth_response refused: sender does not own the paused task"); n != 1 {
		t.Fatalf("want exactly one refusal record, got %d:\n%s", n, logs.String())
	}
	for _, want := range []string{`"level":"WARN"`, `"taskID":"task-alice"`, `"userID":"bob@dev.local"`} {
		if !strings.Contains(logs.String(), want) {
			t.Errorf("refusal record lacks %s:\n%s", want, logs.String())
		}
	}
	if strings.Contains(logs.String(), "authorization-code-marker") {
		t.Error("the refusal logged the authorization code")
	}

	aliceMsg, aliceReq := authResponseMessage(t, "task-alice", "ALICE@dev.local")
	p.handleAuthResponse(context.Background(), aliceMsg, aliceReq)
	if p.tasks.Get("task-alice") != nil {
		t.Error("alice's own auth_response did not reach the token exchange")
	}
}

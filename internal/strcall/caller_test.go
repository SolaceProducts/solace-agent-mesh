// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package strcall

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a/remotetool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
	"github.com/SolaceProducts/solace-agent-mesh/internal/trust"
)

const namespace = "test-ns"

func newSigner(t *testing.T) *trust.Signer {
	t.Helper()
	km, err := trust.NewKeyManager("test-gw")
	if err != nil {
		t.Fatal(err)
	}
	return trust.NewSigner(km, "test-gw", time.Hour, 2*time.Hour)
}

func newBroker(t *testing.T) broker.Broker {
	t.Helper()
	brk := broker.NewDevBroker()
	if err := brk.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })
	return brk
}

type received struct {
	subject string
	taskID  string
	params  remotetool.InvokeParams
}

func serveOnce(t *testing.T, brk broker.Broker, signer *trust.Signer, tool string, result map[string]any) <-chan received {
	t.Helper()
	q, err := brk.CreateQueue("str-"+tool, broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := brk.Subscribe(q, remotetool.InvokeTopic(namespace, tool)); err != nil {
		t.Fatal(err)
	}
	out := make(chan received, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		msg, err := brk.Receive(ctx, q)
		if err != nil {
			close(out)
			return
		}
		msg.Ack()
		up := remotetool.DecodeUserProperties(msg.UserProperties)
		claims := jwt.MapClaims{}
		if _, err := jwt.ParseWithClaims(up.AuthToken, claims, func(*jwt.Token) (any, error) { return signer.PublicKey(), nil }); err != nil {
			close(out)
			return
		}
		var req struct {
			ID     string                  `json:"id"`
			Params remotetool.InvokeParams `json:"params"`
		}
		if json.Unmarshal(msg.Payload, &req) != nil {
			close(out)
			return
		}
		sub, _ := claims["sub"].(string)
		taskID, _ := claims["task_id"].(string)
		out <- received{subject: sub, taskID: taskID, params: req.Params}
		reply, _ := json.Marshal(a2a.NewResponse(req.ID, remotetool.InvokeResult{ToolResult: result}))
		_ = brk.Publish(context.Background(), up.ReplyTo, &broker.Message{Payload: reply, CorrelationID: msg.CorrelationID})
	}()
	return out
}

func TestCaller_SignsAsItsSystemUserAndSendsFilesInline(t *testing.T) {
	brk, signer := newBroker(t), newSigner(t)
	got := serveOnce(t, brk, signer, "sys_tool", map[string]any{"status": "success", "data": map[string]any{"answer": 42.0}})
	caller, err := New(brk, namespace, signer, rbac.EvidenceSystemUserSub, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, err := caller.Do(ctx, Call{Name: "sys_tool", Args: map[string]any{"task": "t"}, TimeoutSeconds: 5,
		Files: map[string]File{"bundle": {Name: "w.tar.gz", MIMEType: "application/gzip", Data: []byte("payload")}}})
	if err != nil {
		t.Fatal(err)
	}
	if data, _ := result["data"].(map[string]any); data["answer"] != 42.0 {
		t.Fatalf("result = %v", result)
	}
	call, ok := <-got
	if !ok {
		t.Fatal("the call did not reach the STR with a token its signer verifies")
	}
	if call.subject != rbac.EvidenceSystemUserSub || call.taskID != call.params.TaskID {
		t.Fatalf("signed as %q for task %q, call task %q", call.subject, call.taskID, call.params.TaskID)
	}
	file, _ := call.params.PreloadedArtifacts["bundle"].(map[string]any)
	if content, _ := file["content"].(string); content != base64.StdEncoding.EncodeToString([]byte("payload")) {
		t.Fatalf("bundle = %v", call.params.PreloadedArtifacts)
	}
}

func TestNew_RunsOnlyAsASystemUser(t *testing.T) {
	if _, err := New(newBroker(t), namespace, newSigner(t), "alice@example.com", slog.Default()); err == nil {
		t.Error("a caller running as a person was built")
	}
}

func TestCaller_WithNoSignerSendsTheCallUnsigned(t *testing.T) {
	brk := newBroker(t)
	q, err := brk.CreateQueue("str-unsigned", broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatal(err)
	}
	err = brk.Subscribe(q, remotetool.InvokeTopic(namespace, "plain_tool"))
	if err != nil {
		t.Fatal(err)
	}
	caller, err := New(brk, namespace, nil, rbac.EvidenceSystemUserSub, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go func() { _, _ = caller.Do(ctx, Call{Name: "plain_tool", TimeoutSeconds: 5}) }()
	msg, err := brk.Receive(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	msg.Ack()
	if up := remotetool.DecodeUserProperties(msg.UserProperties); up.AuthToken != "" || up.UserID != rbac.EvidenceSystemUserSub {
		t.Fatalf("user properties = %+v, want no token and the system user", up)
	}
}

func TestCaller_SendsTheTraceIDItsCallerGaveAndAWaitingCallEndsWithItsContextAfterClose(t *testing.T) {
	brk := newBroker(t)
	q, err := brk.CreateQueue("str-traced", broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatal(err)
	}
	err = brk.Subscribe(q, remotetool.InvokeTopic(namespace, "traced_tool"))
	if err != nil {
		t.Fatal(err)
	}
	caller, err := New(brk, namespace, nil, rbac.EvidenceSystemUserSub, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	failed := make(chan error, 1)
	go func() {
		_, doErr := caller.Do(ctx, Call{Name: "traced_tool", TimeoutSeconds: 120, TraceID: "trace-from-the-caller"})
		failed <- doErr
	}()
	msg, err := brk.Receive(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	msg.Ack()
	if up := remotetool.DecodeUserProperties(msg.UserProperties); up.TraceID != "trace-from-the-caller" {
		t.Errorf("trace id = %q, want the caller's", up.TraceID)
	}

	if err = caller.Close(); err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case doErr := <-failed:
		if doErr == nil {
			t.Error("a call whose context ended after Close returned no error")
		}
	case <-time.After(30 * time.Second):
		t.Error("a call still waiting at Close did not end with its context")
	}
}

// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import (
	"context"
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

const (
	authzWorkflowCard = "workflow_019e47f4_2be0_75d8_828e_77a0d1d9221f"
	authzWorkflowUUID = "019e47f4-2be0-75d8-828e-77a0d1d9221f"
)

func newAuthzWorkflowInstance(t *testing.T, name string, svc *stubServices) *WorkflowInstance {
	t.Helper()
	return newAuthzWorkflowInstanceWithNodes(t, name, []*NodeConfig{
		{ID: "a", Type: "agent", AgentName: "agentA"},
		{ID: "b", Type: "agent", AgentName: "agentB"},
	}, svc)
}

func newAuthzWorkflowInstanceWithNodes(t *testing.T, name string, nodes []*NodeConfig, svc *stubServices) *WorkflowInstance {
	t.Helper()
	cfg := &WorkflowConfig{
		Name:      name,
		Namespace: "test-ns",
		Nodes:     nodes,
	}
	dag, err := BuildDependencyGraph(cfg.Nodes)
	if err != nil {
		t.Fatalf("BuildDependencyGraph: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	w := &WorkflowInstance{
		cfg:            cfg,
		dag:            dag,
		svc:            svc,
		logger:         slog.Default(),
		ctx:            ctx,
		executions:     make(map[string]*WorkflowExecution),
		executionsByID: make(map[string]*WorkflowExecution),
		subTaskIndex:   make(map[string]string),
	}
	w.timerManager = NewInProcessTimerManager(slog.Default(), func(_ string, _ Event) {})
	go w.timerManager.Start(ctx)
	return w
}

func authzTaskMessage(taskID string, props a2a.UserProperties) (*a2a.Request, *broker.Message) {
	req := a2a.NewRequest(taskID, "message/send", map[string]any{
		"message": a2a.Message{
			Kind:      "message",
			MessageID: "m1",
			Role:      a2a.RoleUser,
			ContextID: "ctx-1",
			Parts:     []a2a.Part{a2a.NewTextPart("do the thing")},
		},
	})
	reqBytes, _ := json.Marshal(req)
	return req, &broker.Message{
		Payload:        reqBytes,
		UserProperties: a2a.EncodeUserProperties(&props),
	}
}

func agentRequestTopics() map[string]bool {
	return map[string]bool{
		a2a.AgentRequestTopic("test-ns", "agentA"): true,
		a2a.AgentRequestTopic("test-ns", "agentB"): true,
	}
}

func assertSingleDenial(t *testing.T, msgs []capturedMessage, replyTo string) {
	t.Helper()
	if len(msgs) != 1 {
		topics := make([]string, len(msgs))
		for i, m := range msgs {
			topics[i] = m.Topic
		}
		t.Fatalf("expected exactly 1 publish (the denial to ReplyTo) and no DAG dispatch, got %d: %v", len(msgs), topics)
	}
	if msgs[0].Topic != replyTo {
		t.Fatalf("denial published to %q, want ReplyTo %q", msgs[0].Topic, replyTo)
	}
	var resp a2a.Response
	if err := json.Unmarshal(msgs[0].Payload, &resp); err != nil {
		t.Fatalf("ReplyTo payload is not a JSON-RPC response: %v", err)
	}
	if resp.Error == nil {
		t.Fatal("expected a JSON-RPC error on the denial response")
	}
}

func assertDAGDispatched(t *testing.T, msgs []capturedMessage) {
	t.Helper()
	want := agentRequestTopics()
	got := map[string]bool{}
	for _, m := range msgs {
		if want[m.Topic] {
			got[m.Topic] = true
		}
		var resp a2a.Response
		if err := json.Unmarshal(m.Payload, &resp); err == nil && resp.Error != nil {
			t.Fatalf("unexpected JSON-RPC error published to %q: %v", m.Topic, resp.Error)
		}
	}
	for topic := range want {
		if !got[topic] {
			topics := make([]string, len(msgs))
			for i, m := range msgs {
				topics[i] = m.Topic
			}
			t.Fatalf("expected agent request published to %q; saw topics=%v", topic, topics)
		}
	}
}

func TestHandleTaskRequest_DirectPublishDeniesWithoutWorkflowScope(t *testing.T) {
	const replyTo = "test/reply/topic"
	brk := &capturingBroker{}
	w := newAuthzWorkflowInstance(t, authzWorkflowCard, &stubServices{
		brk:       brk,
		authn:     &fakeAuthn{identity: &auth.Identity{UserID: "alice", Scopes: []string{"agent:*:invoke"}}},
		agentAuth: rbac.NewAgentAuth(nil, slog.Default()),
	})

	taskID := "019e0000-0000-7000-8000-000000000001"
	req, bmsg := authzTaskMessage(taskID, a2a.UserProperties{UserID: "alice", ReplyTo: replyTo, AuthToken: "signed-jwt"})
	w.handleTaskRequest(context.Background(), *req, bmsg)

	assertSingleDenial(t, brk.snapshot(), replyTo)
}

func TestHandleTaskRequest_DirectPublishAllowsWithWorkflowScope(t *testing.T) {
	const replyTo = "test/reply/topic"
	brk := &capturingBroker{}
	w := newAuthzWorkflowInstance(t, authzWorkflowCard, &stubServices{
		brk:       brk,
		authn:     &fakeAuthn{identity: &auth.Identity{UserID: "alice", Scopes: []string{"workflow:" + authzWorkflowUUID + ":invoke"}}},
		agentAuth: rbac.NewAgentAuth(nil, slog.Default()),
	})

	taskID := "019e0000-0000-7000-8000-000000000002"
	req, bmsg := authzTaskMessage(taskID, a2a.UserProperties{UserID: "alice", ReplyTo: replyTo, AuthToken: "signed-jwt"})
	w.handleTaskRequest(context.Background(), *req, bmsg)

	assertDAGDispatched(t, brk.snapshot())
}

func TestHandleTaskRequest_PeerDelegationSkipsGate(t *testing.T) {
	const replyTo = "test/reply/topic"
	brk := &capturingBroker{}
	w := newAuthzWorkflowInstance(t, authzWorkflowCard, &stubServices{
		brk:       brk,
		authn:     &fakeAuthn{identity: &auth.Identity{UserID: "alice", Scopes: []string{"agent:*:invoke"}}},
		agentAuth: rbac.NewAgentAuth(nil, slog.Default()),
	})

	taskID := "019e0000-0000-7000-8000-000000000003"
	req, bmsg := authzTaskMessage(taskID, a2a.UserProperties{
		UserID:              "alice",
		ReplyTo:             replyTo,
		AuthToken:           "signed-jwt",
		DelegatingAgentName: "parent_wf",
	})
	w.handleTaskRequest(context.Background(), *req, bmsg)

	assertDAGDispatched(t, brk.snapshot())
}

func TestHandleTaskRequest_CommunityModePermissive(t *testing.T) {
	const replyTo = "test/reply/topic"
	brk := &capturingBroker{}
	w := newAuthzWorkflowInstance(t, authzWorkflowCard, &stubServices{brk: brk})

	taskID := "019e0000-0000-7000-8000-000000000004"
	req, bmsg := authzTaskMessage(taskID, a2a.UserProperties{UserID: "alice", ReplyTo: replyTo})
	w.handleTaskRequest(context.Background(), *req, bmsg)

	assertDAGDispatched(t, brk.snapshot())
}

func TestHandleTaskRequest_PlainNamedWorkflowUsesWorkflowScope(t *testing.T) {
	const (
		replyTo = "test/reply/topic"
		wfName  = "orders_wf"
	)

	t.Run("allowed by workflow scope", func(t *testing.T) {
		brk := &capturingBroker{}
		w := newAuthzWorkflowInstance(t, wfName, &stubServices{
			brk:       brk,
			authn:     &fakeAuthn{identity: &auth.Identity{UserID: "alice", Scopes: []string{"workflow:" + wfName + ":invoke"}}},
			agentAuth: rbac.NewAgentAuth(nil, slog.Default()),
		})
		req, bmsg := authzTaskMessage("019e0000-0000-7000-8000-000000000005", a2a.UserProperties{UserID: "alice", ReplyTo: replyTo, AuthToken: "signed-jwt"})
		w.handleTaskRequest(context.Background(), *req, bmsg)
		assertDAGDispatched(t, brk.snapshot())
	})

	t.Run("denied by agent scope", func(t *testing.T) {
		brk := &capturingBroker{}
		w := newAuthzWorkflowInstance(t, wfName, &stubServices{
			brk:       brk,
			authn:     &fakeAuthn{identity: &auth.Identity{UserID: "alice", Scopes: []string{"agent:" + wfName + ":invoke"}}},
			agentAuth: rbac.NewAgentAuth(nil, slog.Default()),
		})
		req, bmsg := authzTaskMessage("019e0000-0000-7000-8000-000000000006", a2a.UserProperties{UserID: "alice", ReplyTo: replyTo, AuthToken: "signed-jwt"})
		w.handleTaskRequest(context.Background(), *req, bmsg)
		assertSingleDenial(t, brk.snapshot(), replyTo)
	})
}

func TestHandleTaskRequest_NestedWorkflowHopCarriesDelegation(t *testing.T) {
	const (
		replyTo = "test/reply/topic"
		childWF = "child_wf"
	)
	brk := &capturingBroker{}
	w := newAuthzWorkflowInstanceWithNodes(t, authzWorkflowCard, []*NodeConfig{
		{ID: "nested", Type: "workflow", WorkflowName: childWF},
	}, &stubServices{
		brk:       brk,
		authn:     &fakeAuthn{identity: &auth.Identity{UserID: "alice", Scopes: []string{"workflow:" + authzWorkflowUUID + ":invoke"}}},
		agentAuth: rbac.NewAgentAuth(nil, slog.Default()),
	})
	seedCard(w, a2a.AgentCard{Name: childWF})

	req, bmsg := authzTaskMessage("019e0000-0000-7000-8000-000000000007", a2a.UserProperties{UserID: "alice", ReplyTo: replyTo, AuthToken: "signed-jwt"})
	w.handleTaskRequest(context.Background(), *req, bmsg)

	childTopic := a2a.AgentRequestTopic("test-ns", childWF)
	msgs := brk.snapshot()
	var (
		dispatched bool
		delegating string
		topics     []string
	)
	for _, m := range msgs {
		topics = append(topics, m.Topic)
		if m.Topic == childTopic {
			dispatched = true
			delegating = m.UserProperties[a2a.PropDelegatingAgentName]
		}
	}
	if !dispatched {
		t.Fatalf("expected dispatch to nested-workflow topic %q; saw %v", childTopic, topics)
	}
	if delegating != authzWorkflowCard {
		t.Fatalf("nested-workflow hop must carry %s=%q so the child skips its gate; got %q",
			a2a.PropDelegatingAgentName, authzWorkflowCard, delegating)
	}
}

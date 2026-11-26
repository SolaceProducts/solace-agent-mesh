// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eventmesh

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	corauth "github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/budget"
	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
	"github.com/SolaceProducts/solace-agent-mesh/internal/volume"
)

type fakeScopeResolver struct {
	scopes []string

	mu     sync.Mutex
	userID string
}

func (f *fakeScopeResolver) ResolveScopes(userID string, _ []string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.userID = userID
	return f.scopes
}

func (f *fakeScopeResolver) lastUserID() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.userID
}

type stubServicesAuth struct {
	budgetGate *budget.Gate
	brk        broker.Broker
	artifacts  *artifact.ScopedService
}

func (s *stubServicesAuth) Broker() broker.Broker              { return s.brk }
func (s *stubServicesAuth) Config() config.Config              { return nil }
func (s *stubServicesAuth) KVStore() runtime.KVStore           { return nil }
func (s *stubServicesAuth) Cache() runtime.Cache               { return nil }
func (s *stubServicesAuth) Metrics() runtime.Metrics           { return runtime.NoopMetrics{} }
func (s *stubServicesAuth) Tracer() runtime.Tracer             { return runtime.NoopTracer{} }
func (s *stubServicesAuth) LLM(_ string) llm.Client            { return nil }
func (s *stubServicesAuth) Artifacts() *artifact.ScopedService { return s.artifacts }
func (s *stubServicesAuth) Sessions() session.Store            { return nil }
func (s *stubServicesAuth) Authenticator() corauth.Authenticator {
	return corauth.NoopAuthenticator{}
}
func (s *stubServicesAuth) ToolAuthorizer() corauth.ToolAuthorizer {
	return corauth.NoopToolAuthorizer{}
}
func (s *stubServicesAuth) AgentAuthorizer() corauth.AgentAuthorizer {
	return corauth.NoopAgentAuthorizer{}
}
func (s *stubServicesAuth) AuditLogger() corauth.AuditLogger { return corauth.NoopAuditLogger{} }
func (s *stubServicesAuth) ControlAuthorizer() corauth.ControlAuthorizer {
	return corauth.NoopControlAuthorizer{}
}
func (s *stubServicesAuth) CredentialService() corauth.CredentialService {
	return corauth.NoopCredentialService{}
}
func (s *stubServicesAuth) Volumes() volume.Service             { return nil }
func (s *stubServicesAuth) BudgetGate() *budget.Gate            { return s.budgetGate }
func (s *stubServicesAuth) HTTPClient() *http.Client            { return http.DefaultClient }
func (s *stubServicesAuth) FeatureRegistry() *features.Registry { return nil }

func (s *stubServicesAuth) UsageReporter() runtime.Component { return nil }
func (s *stubServicesAuth) ModelResolver() runtime.ModelAliasResolver {
	return nil
}
func (s *stubServicesAuth) Bindings() runtime.ModelBindings                { return nil }
func (s *stubServicesAuth) ConnectorSpecStore() runtime.ConnectorSpecStore { return nil }
func (s *stubServicesAuth) Logger() *slog.Logger                           { return slog.Default() }

var _ runtime.Services = (*stubServicesAuth)(nil)

type authAdapterOpts struct {
	gatewayDefaultIdentity string
	handlerExpression      string
	handlerDefault         string
	handlerRunAs           string
	errorTopic             string
	artifacts              *artifact.ScopedService
	structuredInvocation   map[string]any
	runAsUserUUIDs         map[string]any
}

func defaultAuthAdapterOpts() authAdapterOpts {
	return authAdapterOpts{
		gatewayDefaultIdentity: "default-user",
		handlerExpression:      "input.user_properties:user_id",
		handlerDefault:         "handler-default",
	}
}

func setupAdapterForAuth(t *testing.T, opts authAdapterOpts) (
	*EventMeshAdapter,
	broker.Broker,
	*fakeScopeResolver,
) {
	t.Helper()

	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatalf("broker connect: %v", err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })

	appConfig := map[string]any{
		"namespace":  "test",
		"gateway_id": "em-test-gw",
		"event_handlers": []any{
			map[string]any{
				"name": "h1",
				"subscriptions": []any{
					map[string]any{"topic": "in/>"},
				},
				"input_expression":         "input.payload:msg",
				"target_agent_name":        "TestAgent",
				"user_identity_expression": opts.handlerExpression,
				"default_user_identity":    opts.handlerDefault,
				"run_as":                   opts.handlerRunAs,
			},
		},
		"event_mesh_broker_config": map[string]any{},
	}
	if opts.gatewayDefaultIdentity != "" {
		appConfig["default_user_identity"] = opts.gatewayDefaultIdentity
	}
	if opts.runAsUserUUIDs != nil {
		appConfig["run_as_user_uuids"] = opts.runAsUserUUIDs
	}
	if opts.structuredInvocation != nil {
		appConfig["event_handlers"].([]any)[0].(map[string]any)["structured_invocation"] = opts.structuredInvocation
	}
	if opts.errorTopic != "" {
		appConfig["output_handlers"] = []any{
			map[string]any{
				"name":               "errs",
				"topic_expression":   "static:" + opts.errorTopic,
				"payload_expression": "input.payload:a2a_task_response",
				"payload_format":     "json",
			},
		}
		appConfig["event_handlers"].([]any)[0].(map[string]any)["on_error"] = "errs"
	}

	svc := &stubServicesAuth{brk: brk, artifacts: opts.artifacts}

	adapter := NewEventMeshAdapter()
	adapter.SetDataBroker(brk)

	core := entrypoint.NewGatewayCore(adapter, appConfig)
	core.SetServices(svc)

	fakeScopes := &fakeScopeResolver{
		scopes: []string{"agent:TestAgent:invoke", "tool:foo:read"},
	}
	core.SetScopeResolver(fakeScopes)

	if err := core.Init(t.Context(), nil); err != nil {
		t.Fatalf("core init: %v", err)
	}

	return adapter, brk, fakeScopes
}

func TestProcessIncomingMessage_InjectsIdentity_ScopesPropagate(t *testing.T) {
	adapter, brk, scopes := setupAdapterForAuth(t, defaultAuthAdapterOpts())

	payload, _ := json.Marshal(map[string]any{"msg": "hello"})
	msg := &broker.Message{
		Topic:          "in/orders",
		Payload:        payload,
		UserProperties: map[string]string{"user_id": "alice"},
	}

	type captured struct {
		req   *a2a.Request
		props a2a.UserProperties
	}
	ready := make(chan struct{})
	got := make(chan captured, 1)
	go func() {
		queue, err := brk.CreateDirectSubscription()
		if err != nil {
			t.Errorf("subscribe: %v", err)
			close(ready)
			return
		}
		if err = brk.Subscribe(queue, a2a.AgentRequestTopic("test", "TestAgent")); err != nil {
			t.Errorf("subscribe topic: %v", err)
			close(ready)
			return
		}
		close(ready)

		ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
		defer cancel()
		recvMsg, err := brk.Receive(ctx, queue)
		if err != nil || recvMsg == nil {
			t.Errorf("receive: err=%v msg=%v", err, recvMsg)
			return
		}
		var req a2a.Request
		if err := json.Unmarshal(recvMsg.Payload, &req); err != nil {
			t.Errorf("unmarshal request: %v", err)
			return
		}
		got <- captured{req: &req, props: a2a.DecodeUserProperties(recvMsg.UserProperties)}
	}()
	<-ready
	adapter.processIncomingMessage(t.Context(), adapter.cfg.EventHandlers[0], msg)

	c := <-got

	if scopes.lastUserID() != "alice" {
		t.Errorf("scopes.lastUserID() = %q, want alice", scopes.lastUserID())
	}

	if c.props.UserID != "alice" {
		t.Errorf("props.UserID = %q, want alice", c.props.UserID)
	}

	if c.props.A2AUserConfig == "" {
		t.Fatal("A2AUserConfig is empty; expected _enterprise_capabilities")
	}
	var cfg map[string]any
	if err := json.Unmarshal([]byte(c.props.A2AUserConfig), &cfg); err != nil {
		t.Fatalf("unmarshal A2AUserConfig: %v", err)
	}
	rawCaps, _ := cfg["_enterprise_capabilities"].([]any)
	gotCaps := make([]string, 0, len(rawCaps))
	for _, x := range rawCaps {
		if s, ok := x.(string); ok {
			gotCaps = append(gotCaps, s)
		}
	}
	if !slices.Contains(gotCaps, "agent:TestAgent:invoke") || !slices.Contains(gotCaps, "tool:foo:read") {
		t.Errorf("capabilities = %v, want both agent:TestAgent:invoke and tool:foo:read", gotCaps)
	}

	if c.req.Method != "message/stream" {
		t.Errorf("method = %q, want message/stream", c.req.Method)
	}
}

func TestProcessIncomingMessage_RBACDeny_NotPublished(t *testing.T) {
	adapter, brk, scopes := setupAdapterForAuth(t, defaultAuthAdapterOpts())
	scopes.scopes = nil

	payload, _ := json.Marshal(map[string]any{"msg": "hello"})
	msg := &broker.Message{
		Topic:          "in/orders",
		Payload:        payload,
		UserProperties: map[string]string{"user_id": "alice"},
	}

	queue, err := brk.CreateDirectSubscription()
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	if err := brk.Subscribe(queue, a2a.AgentRequestTopic("test", "TestAgent")); err != nil {
		t.Fatalf("subscribe topic: %v", err)
	}

	adapter.processIncomingMessage(t.Context(), adapter.cfg.EventHandlers[0], msg)

	ctx, cancel := context.WithTimeout(t.Context(), 300*time.Millisecond)
	defer cancel()
	if recvMsg, _ := brk.Receive(ctx, queue); recvMsg != nil {
		t.Fatalf("expected no publish under RBAC deny, got payload=%s", string(recvMsg.Payload))
	}
}

func TestProcessIncomingMessage_UnresolvedExpression_Discards(t *testing.T) {
	adapter, brk, _ := setupAdapterForAuth(t, authAdapterOpts{
		gatewayDefaultIdentity: "",
		handlerExpression:      "input.user_properties:missing",
		handlerDefault:         "",
		handlerRunAs:           "",
	})

	payload, _ := json.Marshal(map[string]any{"msg": "hello"})
	msg := &broker.Message{Topic: "in/orders", Payload: payload, UserProperties: map[string]string{}}

	queue, err := brk.CreateDirectSubscription()
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	if err := brk.Subscribe(queue, a2a.AgentRequestTopic("test", "TestAgent")); err != nil {
		t.Fatalf("subscribe topic: %v", err)
	}

	adapter.processIncomingMessage(t.Context(), adapter.cfg.EventHandlers[0], msg)

	ctx, cancel := context.WithTimeout(t.Context(), 1*time.Second)
	defer cancel()
	if got, _ := brk.Receive(ctx, queue); got != nil {
		t.Fatal("expected the message to be discarded (no identity, no run_as), but it published")
	}
}

func TestProcessIncomingMessage_NoRunAs_PublishesToErrorTopic(t *testing.T) {
	adapter, brk, _ := setupAdapterForAuth(t, authAdapterOpts{
		gatewayDefaultIdentity: "",
		handlerExpression:      "input.user_properties:missing",
		handlerDefault:         "",
		handlerRunAs:           "",
		errorTopic:             "out/errors",
	})

	queue, err := brk.CreateDirectSubscription()
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	if err := brk.Subscribe(queue, "out/errors"); err != nil {
		t.Fatalf("subscribe error topic: %v", err)
	}

	payload, _ := json.Marshal(map[string]any{"msg": "hello"})
	adapter.processIncomingMessage(t.Context(), adapter.cfg.EventHandlers[0], &broker.Message{
		Topic: "in/orders", Payload: payload, UserProperties: map[string]string{},
	})

	ctx, cancel := context.WithTimeout(t.Context(), 1*time.Second)
	defer cancel()
	got, _ := brk.Receive(ctx, queue)
	if got == nil {
		t.Fatal("expected an error-topic publish for the no-run_as rejection, got none")
	}
	if !strings.Contains(string(got.Payload), "user_identity_expression") {
		t.Errorf("error payload should name the unresolved identity expression, got: %s", string(got.Payload))
	}
}

func TestProcessIncomingMessage_NoIdentityMechanism_RunsAsDefaultSystemUser(t *testing.T) {
	adapter, brk, scopes := setupAdapterForAuth(t, authAdapterOpts{
		gatewayDefaultIdentity: "",
		handlerExpression:      "",
		handlerDefault:         "",
		handlerRunAs:           "",
	})
	scopes.scopes = []string{rbac.AgentInvokeScope("TestAgent")}

	queue, err := brk.CreateDirectSubscription()
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	if err := brk.Subscribe(queue, a2a.AgentRequestTopic("test", "TestAgent")); err != nil {
		t.Fatalf("subscribe topic: %v", err)
	}

	payload, _ := json.Marshal(map[string]any{"msg": "hello"})
	adapter.processIncomingMessage(t.Context(), adapter.cfg.EventHandlers[0], &broker.Message{
		Topic: "in/orders", Payload: payload, UserProperties: map[string]string{},
	})

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	if got, _ := brk.Receive(ctx, queue); got == nil {
		t.Fatal("expected the event to publish as the default system user, got no publish")
	}
	if scopes.lastUserID() != rbac.DefaultSystemUserSub {
		t.Errorf("resolved identity = %q, want %q", scopes.lastUserID(), rbac.DefaultSystemUserSub)
	}
}

func TestProcessIncomingMessage_NoUserIdentity_RunAs_RunsAsSystemUser(t *testing.T) {
	adapter, brk, scopes := setupAdapterForAuth(t, authAdapterOpts{
		gatewayDefaultIdentity: "",
		handlerExpression:      "",
		handlerDefault:         "",
		handlerRunAs:           "default",
	})
	scopes.scopes = []string{rbac.AgentInvokeScope("TestAgent")}

	payload, _ := json.Marshal(map[string]any{"msg": "hello"})
	msg := &broker.Message{Topic: "in/orders", Payload: payload, UserProperties: map[string]string{}}

	queue, err := brk.CreateDirectSubscription()
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	if err := brk.Subscribe(queue, a2a.AgentRequestTopic("test", "TestAgent")); err != nil {
		t.Fatalf("subscribe topic: %v", err)
	}

	adapter.processIncomingMessage(t.Context(), adapter.cfg.EventHandlers[0], msg)

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	got, _ := brk.Receive(ctx, queue)
	if got == nil {
		t.Fatal("expected publish (event runs as the configured run_as system user), got none")
	}
	if scopes.lastUserID() != rbac.DefaultSystemUserSub {
		t.Errorf("scopes.lastUserID() = %q, want %q (run_as system user drives authz)", scopes.lastUserID(), rbac.DefaultSystemUserSub)
	}
}

func TestProcessIncomingMessage_CustomRunAsSubmitsWithTheRenderedSystemUserRow(t *testing.T) {
	const qaBotID = "0192f0c4-6a4e-7b1e-9d3c-5f2a8b7e1c42"
	adapter, brk, scopes := setupAdapterForAuth(t, authAdapterOpts{
		handlerRunAs:   "qa-bot",
		runAsUserUUIDs: map[string]any{"system:qa-bot": qaBotID},
	})
	scopes.scopes = []string{rbac.AgentInvokeScope("TestAgent")}
	queue, err := brk.CreateDirectSubscription()
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	if err := brk.Subscribe(queue, a2a.AgentRequestTopic("test", "TestAgent")); err != nil {
		t.Fatalf("subscribe topic: %v", err)
	}

	payload, _ := json.Marshal(map[string]any{"msg": "hello"})
	adapter.processIncomingMessage(t.Context(), adapter.cfg.EventHandlers[0], &broker.Message{Topic: "in/orders", Payload: payload, UserProperties: map[string]string{}})

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	got, _ := brk.Receive(ctx, queue)
	if got == nil {
		t.Fatal("expected the event to publish as the run_as system user, got none")
	}
	if props := a2a.DecodeUserProperties(got.UserProperties); props.UserID != "system:qa-bot" || props.UserUUID != qaBotID {
		t.Errorf("task ran as %q with user uuid %q, want system:qa-bot with %s", props.UserID, props.UserUUID, qaBotID)
	}
}

func TestProcessIncomingMessage_MessageDerivedSystemPrincipal_Discarded(t *testing.T) {
	adapter, brk, _ := setupAdapterForAuth(t, authAdapterOpts{
		gatewayDefaultIdentity: "",
		handlerExpression:      "input.user_properties:user_id",
		handlerDefault:         "",
		handlerRunAs:           "default",
	})

	payload, _ := json.Marshal(map[string]any{"msg": "hello"})
	msg := &broker.Message{Topic: "in/orders", Payload: payload, UserProperties: map[string]string{"user_id": rbac.DefaultSystemUserSub}}

	queue, err := brk.CreateDirectSubscription()
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	if err := brk.Subscribe(queue, a2a.AgentRequestTopic("test", "TestAgent")); err != nil {
		t.Fatalf("subscribe topic: %v", err)
	}

	adapter.processIncomingMessage(t.Context(), adapter.cfg.EventHandlers[0], msg)

	ctx, cancel := context.WithTimeout(t.Context(), 500*time.Millisecond)
	defer cancel()
	if got, _ := brk.Receive(ctx, queue); got != nil {
		t.Fatalf("expected discard of a message-derived system: principal, but it published: %s", string(got.Payload))
	}
}

func TestProcessIncomingMessage_ResolvedIdentityWinsOverRunAs(t *testing.T) {
	adapter, brk, scopes := setupAdapterForAuth(t, authAdapterOpts{
		gatewayDefaultIdentity: "",
		handlerExpression:      "input.user_properties:user_id",
		handlerDefault:         "",
		handlerRunAs:           "default",
	})
	scopes.scopes = []string{rbac.AgentInvokeScope("TestAgent")}

	payload, _ := json.Marshal(map[string]any{"msg": "hello"})
	msg := &broker.Message{Topic: "in/orders", Payload: payload, UserProperties: map[string]string{"user_id": "alice"}}

	queue, err := brk.CreateDirectSubscription()
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	if err := brk.Subscribe(queue, a2a.AgentRequestTopic("test", "TestAgent")); err != nil {
		t.Fatalf("subscribe topic: %v", err)
	}

	adapter.processIncomingMessage(t.Context(), adapter.cfg.EventHandlers[0], msg)

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	got, _ := brk.Receive(ctx, queue)
	if got == nil {
		t.Fatal("expected publish under the resolved identity")
	}
	if props := a2a.DecodeUserProperties(got.UserProperties); props.UserID != "alice" {
		t.Errorf("props.UserID = %q, want alice (resolved identity must win over run_as %q)", props.UserID, rbac.DefaultSystemUserSub)
	}
	if scopes.lastUserID() != "alice" {
		t.Errorf("scope resolution keyed on %q, want alice", scopes.lastUserID())
	}
}

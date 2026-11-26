// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package runtime

import (
	"context"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
)

func TestServicesEnterpriseAuthNilGuards(t *testing.T) {
	b := broker.NewDevBroker()
	rt := NewRuntime(b, nil)
	svc := rt.Services()

	if svc.Authenticator() == nil {
		t.Error("Authenticator should return noop, not nil")
	}
	if svc.ToolAuthorizer() == nil {
		t.Error("ToolAuthorizer should return noop, not nil")
	}
	if svc.AgentAuthorizer() == nil {
		t.Error("AgentAuthorizer should return noop, not nil")
	}
	if svc.AuditLogger() == nil {
		t.Error("AuditLogger should return noop, not nil")
	}
	if svc.CredentialService() == nil {
		t.Error("CredentialService should return noop, not nil")
	}
	if svc.ControlAuthorizer() == nil {
		t.Error("ControlAuthorizer should return noop, not nil")
	}
}

func TestServicesVolumesNilByDefault(t *testing.T) {
	b := broker.NewDevBroker()
	rt := NewRuntime(b, nil)
	svc := rt.Services()

	if svc.Volumes() != nil {
		t.Error("Volumes should be nil when not configured")
	}
}

func TestRequestReplyPublish(t *testing.T) {
	b := broker.NewDevBroker()
	if err := b.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}

	rr := NewRequestReply(b, "test/reply")
	ctx := context.Background()

	q, err := b.CreateQueue("pub-q", broker.QueueOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if subErr := b.Subscribe(q, "test/publish"); subErr != nil {
		t.Fatal(subErr)
	}

	msg := &Message{
		Payload:        []byte("hello"),
		UserProperties: map[string]string{"x": "y"},
	}
	if pubErr := rr.Publish(ctx, "test/publish", msg); pubErr != nil {
		t.Fatal(err)
	}

	received, err := b.Receive(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	if received == nil {
		t.Fatal("expected to receive published message")
	}
	if string(received.Payload) != "hello" {
		t.Errorf("payload = %q, want %q", received.Payload, "hello")
	}
}

func TestNewRequestReplyDefaultPrefix(t *testing.T) {
	b := broker.NewDevBroker()
	rr := NewRequestReply(b, "")
	if rr.replyPrefix != "sam/reply" {
		t.Errorf("default prefix = %q, want %q", rr.replyPrefix, "sam/reply")
	}
}

func TestReplyNoReplyTo(t *testing.T) {
	b := broker.NewDevBroker()
	rr := NewRequestReply(b, "test/reply")

	req := &Message{}
	resp := &Message{Payload: []byte("response")}

	err := rr.Reply(context.Background(), req, resp)
	if err == nil {
		t.Error("Reply should fail when request has no ReplyTo")
	}
}

func TestServicesEnterpriseAuthWithRealValues(t *testing.T) {
	b := broker.NewDevBroker()
	authn := auth.NoopAuthenticator{}
	toolAuth := auth.NoopToolAuthorizer{}
	agentAuth := auth.NoopAgentAuthorizer{}
	audit := auth.NoopAuditLogger{}
	cred := auth.NoopCredentialService{}
	ctrl := auth.NoopControlAuthorizer{}

	rt := NewRuntime(b, nil,
		WithAuthenticator(authn),
		WithToolAuthorizer(toolAuth),
		WithAgentAuthorizer(agentAuth),
		WithAuditLogger(audit),
		WithCredentialService(cred),
		WithControlAuthorizer(ctrl),
	)
	svc := rt.Services()

	if svc.Authenticator() != authn {
		t.Error("Authenticator should return the configured instance")
	}
	if svc.ToolAuthorizer() != toolAuth {
		t.Error("ToolAuthorizer should return the configured instance")
	}
	if svc.AgentAuthorizer() != agentAuth {
		t.Error("AgentAuthorizer should return the configured instance")
	}
	if svc.AuditLogger() != audit {
		t.Error("AuditLogger should return the configured instance")
	}
	if svc.CredentialService() != cred {
		t.Error("CredentialService should return the configured instance")
	}
	if svc.ControlAuthorizer() != ctrl {
		t.Error("ControlAuthorizer should return the configured instance")
	}
}

func TestWithOptionsCoverage(t *testing.T) {
	b := broker.NewDevBroker()
	rt := NewRuntime(b, nil,
		WithSessions(nil),
		WithVolumes(nil),
		WithArtifacts(nil),
	)
	svc := rt.Services()

	if svc.Sessions() != nil {
		t.Error("Sessions should be nil")
	}
	if svc.Volumes() != nil {
		t.Error("Volumes should be nil")
	}
	if svc.Artifacts() != nil {
		t.Error("Artifacts should be nil")
	}
	if svc.Config() != nil {
		t.Error("Config should be nil when not set")
	}
}

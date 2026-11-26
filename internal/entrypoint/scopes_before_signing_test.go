// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
)

type scopeCapturingSigner struct {
	signedScopes []string
	signedUserID string
	calls        int
}

func (s *scopeCapturingSigner) SignTask(identity *auth.Subject, taskID string) (string, error) {
	s.calls++
	s.signedUserID = identity.UserID()
	s.signedScopes = append([]string(nil), identity.Scopes()...)
	return "signed-" + taskID, nil
}

func TestSubmitTask_ScopesResolvedBeforeSigning(t *testing.T) {
	const wantScope = "agent:target-agent:invoke"

	must := func(s *auth.Subject, err error) *auth.Subject {
		t.Helper()
		if err != nil {
			t.Fatalf("constructor: %v", err)
		}
		return s
	}
	scopelessCallers := map[string]*auth.Subject{
		"slack":     auth.ForSlackUser("alice@corp.com", "alice@corp.com", "U1", "T1"),
		"teams":     auth.ForTeamsUser("bob@corp.com", "bob@corp.com", "Bob", "T1", "aad-1", "graph"),
		"whatsapp":  auth.ForWhatsAppSenderIdentity("15551234567", "Dave"),
		"email":     must(auth.ForEmailSender(auth.ExternalID{Type: auth.IDEmail, Value: "carol@corp.com"}, "", false)),
		"webhook":   auth.ForWebhookCaller("webhook"),
		"eventmesh": must(auth.ForEventMeshCaller(nil, "eventmesh")),
	}

	for name, caller := range scopelessCallers {
		t.Run(name, func(t *testing.T) {
			if len(caller.Scopes()) != 0 {
				t.Fatalf("fixture error: %s caller must start scope-less", name)
			}

			core, brk, _ := setupTestGateway(t)
			if err := core.Init(t.Context(), nil); err != nil {
				t.Fatalf("Init: %v", err)
			}
			core.SetScopeResolver(&fixedScopeResolver{scopes: []string{wantScope}})
			signer := &scopeCapturingSigner{}
			core.SetTaskSigner(signer)

			q, err := brk.CreateQueue("scopes-before-signing-"+name, broker.QueueOptions{Named: true})
			if err != nil {
				t.Fatalf("create queue: %v", err)
			}
			if err = brk.Subscribe(q, a2a.AgentRequestTopic("test-ns", "target-agent")); err != nil {
				t.Fatalf("subscribe: %v", err)
			}

			msg := a2a.Message{
				Kind:      "message",
				MessageID: "msg-" + name,
				Role:      a2a.RoleUser,
				Parts:     []a2a.Part{a2a.NewTextPart("hello")},
			}
			if err = core.SubmitTask(t.Context(), "target-agent", "task-"+name, msg, caller); err != nil {
				t.Fatalf("SubmitTask: %v", err)
			}

			if signer.calls == 0 {
				t.Fatal("the task was never signed; a scope-less caller must still be resolved and signed")
			}
			if len(signer.signedScopes) == 0 {
				t.Fatalf("scopes were empty AT SIGNING TIME for %s; the caller reached the signer unresolved", name)
			}
			if signer.signedScopes[0] != wantScope {
				t.Errorf("signed scopes = %v, want [%s]", signer.signedScopes, wantScope)
			}
			if signer.signedUserID != caller.UserID() {
				t.Errorf("signed userID = %q, want %q", signer.signedUserID, caller.UserID())
			}

			recvCtx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
			defer cancel()
			received, err := brk.Receive(recvCtx, q)
			if err != nil || received == nil {
				t.Fatalf("no task published: err=%v", err)
			}
			props := a2a.DecodeUserProperties(received.UserProperties)
			if props.AuthToken == "" {
				t.Error("published task carries no AuthToken")
			}
			if !strings.Contains(props.A2AUserConfig, "_enterprise_capabilities") {
				t.Fatalf("published task carries no _enterprise_capabilities; A2AUserConfig = %q", props.A2AUserConfig)
			}
			var extras map[string]any
			if err := json.Unmarshal([]byte(props.A2AUserConfig), &extras); err != nil {
				t.Fatalf("unmarshal A2AUserConfig: %v", err)
			}
			caps, _ := extras["_enterprise_capabilities"].([]any)
			if len(caps) == 0 || caps[0] != wantScope {
				t.Errorf("_enterprise_capabilities = %v, want [%s]", caps, wantScope)
			}
		})
	}
}

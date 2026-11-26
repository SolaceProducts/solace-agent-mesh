// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package email

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

type platformUserScopeResolver struct {
	scopes          []string
	platformUserIDs map[string]string
}

func (r platformUserScopeResolver) ResolveScopes(string, []string) []string { return r.scopes }

func (r platformUserScopeResolver) ResolveScopesAndPlatformUser(userID string, _ []string) ([]string, string) {
	return r.scopes, r.platformUserIDs[userID]
}

func TestDispatch_StampsThePlatformUserTheScopeLookupMatched(t *testing.T) {
	const agentName = "Agent"
	const aliceID = "0192f0c4-6a4e-7b1e-9d3c-5f2a8b7e1c44"
	for _, tc := range []struct {
		name            string
		platformUserIDs map[string]string
		verdict         AuthVerdict
		trust           InternalTrustResult
		want            string
	}{
		{"a DMARC-verified sender the platform matched to one user", map[string]string{"alice@vendor.com": aliceID}, AuthVerdict{DMARC: AuthPass}, InternalTrustResult{}, aliceID},
		{"an internally trusted sender DMARC did not verify", map[string]string{"alice@vendor.com": aliceID}, AuthVerdict{}, InternalTrustResult{Trusted: true}, aliceID},
		{"a sender the platform matched to no single user", nil, AuthVerdict{DMARC: AuthPass}, InternalTrustResult{}, ""},
		{"a sender whose address DMARC did not verify, which anyone could spoof", map[string]string{"alice@vendor.com": aliceID}, AuthVerdict{}, InternalTrustResult{}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			brk := broker.NewDevBroker()
			if err := brk.Connect(t.Context()); err != nil {
				t.Fatalf("connect broker: %v", err)
			}
			t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })
			logger := slog.New(slog.NewTextHandler(io.Discard, nil))
			core := entrypoint.NewGatewayCore(&entrypoint.LoggingAdapter{}, map[string]any{"namespace": "test-ns", "gateway_id": "test-gw"})
			core.SetServices(runtime.NewRuntime(brk, nil).Services())
			if err := core.Init(t.Context(), nil); err != nil {
				t.Fatalf("core.Init: %v", err)
			}
			if err := core.Start(t.Context()); err != nil {
				t.Fatalf("core.Start: %v", err)
			}
			t.Cleanup(func() { _ = core.Stop(context.Background()) })
			a := &EmailAdapter{cfg: &EmailConfig{DefaultAgentName: agentName}, logger: logger, core: core}
			a.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: agentName})
			a.core.SetScopeResolver(platformUserScopeResolver{scopes: []string{rbac.AgentInvokeScope(agentName)}, platformUserIDs: tc.platformUserIDs})
			q, err := brk.CreateQueue("email-user-uuid-q", broker.QueueOptions{Named: true})
			if err != nil {
				t.Fatalf("create queue: %v", err)
			}
			if err = brk.Subscribe(q, a2a.AgentRequestTopic("test-ns", agentName)); err != nil {
				t.Fatalf("subscribe: %v", err)
			}

			from := EmailAddress{Address: "alice@vendor.com"}
			identity, err := NewEmailIdentityResolver(logger, false).Resolve(t.Context(), from, tc.verdict, tc.trust)
			if err != nil {
				t.Fatalf("resolve identity: %v", err)
			}
			n := &NormalizedEmail{From: from, Subject: "hi", MessageID: "<msg-1@vendor.com>", BodyText: "hello"}
			if _, err = (&gatewayDispatcher{adapter: a}).Dispatch(t.Context(), n, identity, tc.trust); err != nil {
				t.Fatalf("Dispatch: %v", err)
			}

			recvCtx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
			defer cancel()
			received, _ := brk.Receive(recvCtx, q)
			if received == nil {
				t.Fatal("expected task published to agent request topic, got none")
			}
			if got := received.UserProperties[a2a.PropUserUUID]; got != tc.want {
				t.Errorf("user uuid = %q, want %q", got, tc.want)
			}
		})
	}
}

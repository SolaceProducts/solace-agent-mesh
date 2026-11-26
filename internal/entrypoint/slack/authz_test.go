// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package slack

import (
	"context"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
	"github.com/slack-go/slack/slackevents"
)

type stubScopeResolver struct{ scopes []string }

func (r stubScopeResolver) ResolveScopes(string, []string) []string { return r.scopes }

func TestSlackSubmit_RBACEnforced(t *testing.T) {
	t.Parallel()
	const agentName = "TestAgent"

	tests := []struct {
		name          string
		scopes        []string
		wantPublished bool
	}{
		{
			name:          "scope-less identity denied",
			scopes:        nil,
			wantPublished: false,
		},
		{
			name:          "wrong agent scope denied",
			scopes:        []string{rbac.AgentInvokeScope("OtherAgent")},
			wantPublished: false,
		},
		{
			name:          "correct invoke scope allowed",
			scopes:        []string{rbac.AgentInvokeScope(agentName)},
			wantPublished: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			a, _, brk := newHandlerTestAdapter(t)
			a.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: agentName})
			a.core.SetScopeResolver(stubScopeResolver{scopes: tc.scopes})

			q, err := brk.CreateQueue("slack-authz-q", broker.QueueOptions{Named: true})
			if err != nil {
				t.Fatalf("create queue: %v", err)
			}
			if err = brk.Subscribe(q, a2a.AgentRequestTopic("test-ns", agentName)); err != nil {
				t.Fatalf("subscribe: %v", err)
			}

			ev := &slackevents.MessageEvent{
				ChannelType: "im",
				User:        "U1",
				Channel:     "C1",
				Text:        "invoke the agent",
				TimeStamp:   "1700000090.000001",
			}
			a.HandleMessage(ev, "T1")

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

type platformUserScopeResolver struct {
	scopes          []string
	platformUserIDs map[string]string
}

func (r platformUserScopeResolver) ResolveScopes(string, []string) []string { return r.scopes }

func (r platformUserScopeResolver) ResolveScopesAndPlatformUser(userID string, _ []string) ([]string, string) {
	return r.scopes, r.platformUserIDs[userID]
}

func TestSlackSubmit_StampsThePlatformUserTheScopeLookupMatched(t *testing.T) {
	t.Parallel()
	const agentName = "TestAgent"
	const aliceID = "0192f0c4-6a4e-7b1e-9d3c-5f2a8b7e1c44"
	for _, tc := range []struct {
		name            string
		platformUserIDs map[string]string
		want            string
	}{
		{"a sender the platform matched to one user", map[string]string{"test@test.com": aliceID}, aliceID},
		{"a sender the platform matched to no single user", nil, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			a, _, brk := newHandlerTestAdapter(t)
			a.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: agentName})
			a.core.SetScopeResolver(platformUserScopeResolver{scopes: []string{rbac.AgentInvokeScope(agentName)}, platformUserIDs: tc.platformUserIDs})
			q, err := brk.CreateQueue("slack-user-uuid-q", broker.QueueOptions{Named: true})
			if err != nil {
				t.Fatalf("create queue: %v", err)
			}
			if err = brk.Subscribe(q, a2a.AgentRequestTopic("test-ns", agentName)); err != nil {
				t.Fatalf("subscribe: %v", err)
			}

			a.HandleMessage(&slackevents.MessageEvent{ChannelType: "im", User: "U1", Channel: "C1", Text: "hi", TimeStamp: "1700000090.000002"}, "T1")

			recvCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
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

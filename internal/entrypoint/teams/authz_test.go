// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package teams

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

type teamsAuthzScopeResolver struct{ scopes []string }

func (r teamsAuthzScopeResolver) ResolveScopes(string, []string) []string { return r.scopes }

func offlineBotClient(t *testing.T) *BotClient {
	t.Helper()
	bc := NewBotClient("app", "secret", "", teamsNoopLogger())
	bc.client = &http.Client{
		Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: 200,
				Header:     http.Header{"Content-Type": {"application/json"}},
				Body:       io.NopCloser(bytes.NewReader([]byte(`{"access_token":"tok","token_type":"Bearer","expires_in":3600,"id":"sent"}`))),
			}, nil
		}),
	}
	return bc
}

func TestTeamsSubmit_RBACEnforced(t *testing.T) {
	const agentName = "TestAgent"

	tests := []struct {
		name          string
		scopes        []string
		wantPublished bool
	}{
		{name: "scope-less identity denied", scopes: nil, wantPublished: false},
		{name: "wrong agent scope denied", scopes: []string{rbac.AgentInvokeScope("OtherAgent")}, wantPublished: false},
		{name: "correct invoke scope allowed", scopes: []string{rbac.AgentInvokeScope(agentName)}, wantPublished: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			a, brk := newTeamsAuthzRig(t, agentName, teamsAuthzScopeResolver{scopes: tc.scopes})
			q, err := brk.CreateQueue("teams-authz-q", broker.QueueOptions{Named: true})
			if err != nil {
				t.Fatalf("create queue: %v", err)
			}
			if err = brk.Subscribe(q, a2a.AgentRequestTopic("test-ns", agentName)); err != nil {
				t.Fatalf("subscribe: %v", err)
			}

			activity := &Activity{Type: ActivityTypeMessage, Text: "invoke the agent"}
			activity.From.ID = "29:user"
			activity.From.AadObjectID = "aad-user"
			activity.Conversation.ID = "conv-1"

			a.submitTeamsTask(t.Context(), activity, "invoke the agent", nil, nil)

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

func newTeamsAuthzRig(t *testing.T, agentName string, resolver entrypoint.ScopeResolver) (*TeamsAdapter, *broker.DevBroker) {
	t.Helper()
	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatalf("connect broker: %v", err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })

	core := entrypoint.NewGatewayCore(&entrypoint.LoggingAdapter{}, map[string]any{
		"namespace":  "test-ns",
		"gateway_id": "test-gw",
		"session_service": map[string]any{
			"type":         "sql",
			"database_url": store.TestSQLiteDBURL(t),
		},
	})
	core.SetServices(runtime.NewRuntime(brk, nil).Services())
	if err := core.Init(t.Context(), nil); err != nil {
		t.Fatalf("core.Init: %v", err)
	}
	t.Cleanup(func() { _ = core.Stop(context.Background()) })
	core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: agentName})
	core.SetScopeResolver(resolver)

	a := newTestAdapter(t)
	a.core = core
	a.ctx = t.Context()
	a.cfg.DefaultAgentName = agentName
	a.botClient = offlineBotClient(t)
	a.emailCache = NewEmailCache("", 0, a.botClient, a.logger)
	return a, brk
}

type platformUserScopeResolver struct {
	scopes          []string
	platformUserIDs map[string]string
}

func (r platformUserScopeResolver) ResolveScopes(string, []string) []string { return r.scopes }

func (r platformUserScopeResolver) ResolveScopesAndPlatformUser(userID string, _ []string) ([]string, string) {
	return r.scopes, r.platformUserIDs[userID]
}

func TestTeamsSubmit_StampsThePlatformUserTheScopeLookupMatched(t *testing.T) {
	const agentName = "TestAgent"
	const aliceID = "0192f0c4-6a4e-7b1e-9d3c-5f2a8b7e1c44"
	for _, tc := range []struct {
		name             string
		conversationType string
		platformUserIDs  map[string]string
		want             string
	}{
		{"a sender the platform matched to one user", ConversationTypePersonal, map[string]string{"aad-user": aliceID}, aliceID},
		{"a sender the platform matched to no single user", ConversationTypePersonal, nil, ""},
		{"a channel swapped to its run_as system user", "channel", map[string]string{"aad-user": aliceID}, rbac.DefaultSystemUserID},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, brk := newTeamsAuthzRig(t, agentName, platformUserScopeResolver{scopes: []string{rbac.AgentInvokeScope(agentName)}, platformUserIDs: tc.platformUserIDs})
			q, err := brk.CreateQueue("teams-user-uuid-q", broker.QueueOptions{Named: true})
			if err != nil {
				t.Fatalf("create queue: %v", err)
			}
			if err = brk.Subscribe(q, a2a.AgentRequestTopic("test-ns", agentName)); err != nil {
				t.Fatalf("subscribe: %v", err)
			}

			activity := &Activity{Type: ActivityTypeMessage, Text: "hi"}
			activity.From.ID = "29:user"
			activity.From.AadObjectID = "aad-user"
			activity.Conversation.ID = "conv-1"
			activity.Conversation.ConversationType = tc.conversationType
			a.submitTeamsTask(t.Context(), activity, "hi", nil, nil)

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

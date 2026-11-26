// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package teams

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

type capturingScopeResolver struct {
	mu       sync.Mutex
	scopes   []string
	gotUser  string
	gotRoles []string
}

func (r *capturingScopeResolver) ResolveScopes(userID string, roles []string) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.gotUser = userID
	r.gotRoles = append([]string(nil), roles...)
	return r.scopes
}

func (r *capturingScopeResolver) captured() (string, []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.gotUser, append([]string(nil), r.gotRoles...)
}

func newRunAsTeamsFixture(t *testing.T, agentName string) (*TeamsAdapter, broker.Broker, broker.Queue) {
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

	a := newTestAdapter(t)
	a.core = core
	a.ctx = t.Context()
	a.cfg.DefaultAgentName = agentName
	a.botClient = offlineBotClient(t)
	a.emailCache = NewEmailCache("", 0, a.botClient, a.logger)

	q, err := brk.CreateQueue("teams-runas-q", broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatalf("create queue: %v", err)
	}
	if err := brk.Subscribe(q, a2a.AgentRequestTopic("test-ns", agentName)); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	return a, brk, q
}

func teamsActivity(convType string) *Activity {
	act := &Activity{Type: ActivityTypeMessage, Text: "what's in my DMs?"}
	act.From.ID = "29:user"
	act.From.AadObjectID = "aad-user"
	act.Conversation.ID = "conv-1"
	act.Conversation.ConversationType = convType
	return act
}

func recvTeamsMessage(t *testing.T, brk broker.Broker, q broker.Queue) (*broker.Message, a2a.Message) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	m, _ := brk.Receive(ctx, q)
	if m == nil {
		t.Fatal("expected a task published to the agent, got none")
	}
	var env struct {
		Params struct {
			Message a2a.Message `json:"message"`
		} `json:"params"`
	}
	if err := json.Unmarshal(m.Payload, &env); err != nil {
		t.Fatalf("decode A2A request payload: %v", err)
	}
	return m, env.Params.Message
}

func TestSubmitTeamsTask_RunAsBySurface(t *testing.T) {
	const agentName = "TestAgent"

	realUserID := func(a *TeamsAdapter, act *Activity) string {
		return a.emailCache.ResolveIdentity(a.ctx, act.From.ID, act.From.Name, act.From.AadObjectID, act.ServiceURL, act.Conversation.ID).UserID()
	}

	for _, convType := range []string{ConversationTypeChannel, ConversationTypeGroupChat} {
		t.Run("multi-party "+convType+" runs as system user", func(t *testing.T) {
			a, brk, q := newRunAsTeamsFixture(t, agentName)
			act := teamsActivity(convType)
			a.submitTeamsTask(t.Context(), act, act.Text, nil, nil)
			m, _ := recvTeamsMessage(t, brk, q)
			if up := a2a.DecodeUserProperties(m.UserProperties); up.UserID != rbac.DefaultSystemUserSub {
				t.Errorf("%s userID = %q, want %q (run-as low-priv principal)", convType, up.UserID, rbac.DefaultSystemUserSub)
			}
		})
	}

	t.Run("personal chat stays the real user", func(t *testing.T) {
		a, brk, q := newRunAsTeamsFixture(t, agentName)
		act := teamsActivity(ConversationTypePersonal)
		want := realUserID(a, act)
		a.submitTeamsTask(t.Context(), act, act.Text, nil, nil)
		m, _ := recvTeamsMessage(t, brk, q)
		if up := a2a.DecodeUserProperties(m.UserProperties); up.UserID != want {
			t.Errorf("personal userID = %q, want the real user %q (must not run as system user)", up.UserID, want)
		}
	})
}

func TestSubmitTeamsTask_MultiPartyResolvesScopesForChannelUser(t *testing.T) {
	const agentName = "TestAgent"
	a, brk, q := newRunAsTeamsFixture(t, agentName)
	res := &capturingScopeResolver{scopes: []string{rbac.AgentInvokeScope(agentName)}}
	a.core.SetScopeResolver(res)

	a.submitTeamsTask(t.Context(), teamsActivity(ConversationTypeChannel), "hi", nil, nil)
	recvTeamsMessage(t, brk, q)

	gotUser, _ := res.captured()
	if gotUser != rbac.DefaultSystemUserSub {
		t.Errorf("authz resolved scopes for %q, want %q (low-priv identity drives authz)", gotUser, rbac.DefaultSystemUserSub)
	}
}

func TestSubmitTeamsTask_RunAsFailsClosed(t *testing.T) {
	const agentName = "TestAgent"

	cases := []struct {
		name     string
		convType string
		isGroup  bool
	}{
		{"empty ConversationType", "", false},
		{"unknown ConversationType", "meeting", false},
		{"personal but IsGroup", ConversationTypePersonal, true},
	}
	for _, c := range cases {
		t.Run(c.name+" runs as system user", func(t *testing.T) {
			a, brk, q := newRunAsTeamsFixture(t, agentName)
			act := teamsActivity(c.convType)
			act.Conversation.IsGroup = c.isGroup
			a.submitTeamsTask(t.Context(), act, act.Text, nil, nil)
			m, _ := recvTeamsMessage(t, brk, q)
			if up := a2a.DecodeUserProperties(m.UserProperties); up.UserID != rbac.DefaultSystemUserSub {
				t.Errorf("%s: userID = %q, want %q (fail-closed to run-as)", c.name, up.UserID, rbac.DefaultSystemUserSub)
			}
		})
	}
}

func TestSubmitTeamsTask_RunAsInjectsNoteAndAudit(t *testing.T) {
	const agentName = "TestAgent"

	t.Run("channel injects note + audit", func(t *testing.T) {
		a, brk, q := newRunAsTeamsFixture(t, agentName)
		act := teamsActivity(ConversationTypeChannel)
		want := a.emailCache.ResolveIdentity(a.ctx, act.From.ID, act.From.Name, act.From.AadObjectID, act.ServiceURL, act.Conversation.ID).UserID()
		a.submitTeamsTask(t.Context(), act, act.Text, nil, nil)
		_, msg := recvTeamsMessage(t, brk, q)

		ctxVal, _ := msg.Metadata["conversation_context"].(string)
		if !strings.Contains(ctxVal, entrypoint.ChannelRunAsSystemNote) {
			t.Errorf("channel conversation_context should carry the run-as system note; got %q", ctxVal)
		}
		if got := msg.Metadata["run_as"]; got != rbac.DefaultSystemUserSub {
			t.Errorf("run_as = %v, want %q", got, rbac.DefaultSystemUserSub)
		}
		if got := msg.Metadata["requesting_user"]; got != want {
			t.Errorf("requesting_user = %v, want the real asker %q", got, want)
		}
	})

	t.Run("personal carries neither", func(t *testing.T) {
		a, brk, q := newRunAsTeamsFixture(t, agentName)
		act := teamsActivity(ConversationTypePersonal)
		a.submitTeamsTask(t.Context(), act, act.Text, nil, nil)
		_, msg := recvTeamsMessage(t, brk, q)

		ctxVal, _ := msg.Metadata["conversation_context"].(string)
		if strings.Contains(ctxVal, entrypoint.ChannelRunAsSystemNote) {
			t.Errorf("personal chat must NOT carry the run-as system note; got %q", ctxVal)
		}
		if _, ok := msg.Metadata["run_as"]; ok {
			t.Error("personal chat must NOT carry run_as audit metadata")
		}
		if _, ok := msg.Metadata["requesting_user"]; ok {
			t.Error("personal chat must NOT carry requesting_user audit metadata")
		}
	})
}

func TestSubmitTeamsTask_RunAsDisabled(t *testing.T) {
	const agentName = "TestAgent"
	a, brk, q := newRunAsTeamsFixture(t, agentName)
	a.cfg.UseUserIdentityInChannels = true
	act := teamsActivity(ConversationTypeChannel)
	want := a.emailCache.ResolveIdentity(a.ctx, act.From.ID, act.From.Name, act.From.AadObjectID, act.ServiceURL, act.Conversation.ID).UserID()
	a.submitTeamsTask(t.Context(), act, act.Text, nil, nil)
	m, _ := recvTeamsMessage(t, brk, q)
	if up := a2a.DecodeUserProperties(m.UserProperties); up.UserID != want {
		t.Errorf("run-as disabled: channel userID = %q, want the real user %q", up.UserID, want)
	}
}

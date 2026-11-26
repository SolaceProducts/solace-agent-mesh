// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package slack

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
	"github.com/slack-go/slack/slackevents"
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

func TestSubmitSlackTask_NonDMResolvesScopesForSlackUser(t *testing.T) {
	t.Parallel()
	a, _, brk := newHandlerTestAdapter(t)
	a.cfg.DefaultAgentName = "TestAgent"
	a.botUserID = "U-bot"
	a.identityResolved = true
	a.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: "TestAgent"})
	res := &capturingScopeResolver{scopes: []string{rbac.AgentInvokeScope("TestAgent")}}
	a.core.SetScopeResolver(res)

	q, err := brk.CreateQueue("runas-scope-q", broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatalf("create queue: %v", err)
	}
	if err := brk.Subscribe(q, a2a.AgentRequestTopic("test-ns", "TestAgent")); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	a.HandleAppMention(&slackevents.AppMentionEvent{
		Channel: "C1", User: "U1", Text: "<@U-bot> hi", TimeStamp: "1700000093.000001",
	}, "T1")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if m, _ := brk.Receive(ctx, q); m == nil {
		t.Fatal("expected publish (system user granted TestAgent invoke)")
	}

	gotUser, _ := res.captured()
	if gotUser != rbac.DefaultSystemUserSub {
		t.Errorf("authz resolved scopes for %q, want %q (low-priv identity drives authz)", gotUser, rbac.DefaultSystemUserSub)
	}
}

func TestSubmitSlackTask_RunAsBySurface(t *testing.T) {
	t.Parallel()
	a, _, brk := newHandlerTestAdapter(t)
	a.cfg.DefaultAgentName = "TestAgent"
	a.botUserID = "U-bot"
	a.identityResolved = true
	a.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: "TestAgent"})

	q, err := brk.CreateQueue("runas-q", broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatalf("create queue: %v", err)
	}
	if err := brk.Subscribe(q, a2a.AgentRequestTopic("test-ns", "TestAgent")); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	recv := func() *broker.Message {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		m, _ := brk.Receive(ctx, q)
		if m == nil {
			t.Fatal("expected a task published to the agent, got none")
		}
		return m
	}

	a.HandleAppMention(&slackevents.AppMentionEvent{
		Channel: "C1", User: "U1", Text: "<@U-bot> what's in my DMs?", TimeStamp: "1700000090.000001",
	}, "T1")
	if up := a2a.DecodeUserProperties(recv().UserProperties); up.UserID != rbac.DefaultSystemUserSub {
		t.Errorf("non-DM userID = %q, want %q (run-as low-priv principal)", up.UserID, rbac.DefaultSystemUserSub)
	}

	a.HandleMessage(&slackevents.MessageEvent{
		ChannelType: "im", Channel: "D1", User: "U1", Text: "hi", TimeStamp: "1700000091.000001",
	}, "T1")
	if up := a2a.DecodeUserProperties(recv().UserProperties); up.UserID != "test@test.com" {
		t.Errorf("DM userID = %q, want the real user test@test.com (must not run as system user)", up.UserID)
	}
}

func TestSubmitSlackTask_RunAsInjectsSystemNoteAndAudit(t *testing.T) {
	t.Parallel()
	const agentName = "TestAgent"

	t.Run("non-DM injects note + audit", func(t *testing.T) {
		a, _, brk := newHandlerTestAdapter(t)
		a.cfg.DefaultAgentName = agentName
		a.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: agentName})

		q, err := brk.CreateQueue("runas-note-q", broker.QueueOptions{Named: true})
		if err != nil {
			t.Fatalf("create queue: %v", err)
		}
		if err := brk.Subscribe(q, a2a.AgentRequestTopic("test-ns", agentName)); err != nil {
			t.Fatalf("subscribe: %v", err)
		}

		a.submitSlackTask(slackInbound{channelID: "C1", threadTS: "100.1", userID: "U1", teamID: "T1", text: "what's in my DMs?", isDM: false})
		m := receiveMessage(t, brk, q)

		ctxVal, _ := m.Metadata["conversation_context"].(string)
		if !strings.Contains(ctxVal, entrypoint.ChannelRunAsSystemNote) {
			t.Errorf("non-DM conversation_context should carry the shared-channel system note; got %q", ctxVal)
		}
		if got := m.Metadata["run_as"]; got != rbac.DefaultSystemUserSub {
			t.Errorf("run_as = %v, want %q", got, rbac.DefaultSystemUserSub)
		}
		if got := m.Metadata["requesting_user"]; got != "test@test.com" {
			t.Errorf("requesting_user = %v, want the real asker test@test.com", got)
		}
	})

	t.Run("DM carries neither", func(t *testing.T) {
		a, _, brk := newHandlerTestAdapter(t)
		a.cfg.DefaultAgentName = agentName
		a.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: agentName})

		q, err := brk.CreateQueue("dm-nonote-q", broker.QueueOptions{Named: true})
		if err != nil {
			t.Fatalf("create queue: %v", err)
		}
		if err := brk.Subscribe(q, a2a.AgentRequestTopic("test-ns", agentName)); err != nil {
			t.Fatalf("subscribe: %v", err)
		}

		a.submitSlackTask(slackInbound{channelID: "D1", threadTS: "100.1", userID: "U1", teamID: "T1", text: "what's in my DMs?", isDM: true})
		m := receiveMessage(t, brk, q)

		ctxVal, _ := m.Metadata["conversation_context"].(string)
		if strings.Contains(ctxVal, entrypoint.ChannelRunAsSystemNote) {
			t.Errorf("DM must NOT carry the shared-channel system note; got %q", ctxVal)
		}
		if _, ok := m.Metadata["run_as"]; ok {
			t.Error("DM must NOT carry run_as audit metadata")
		}
		if _, ok := m.Metadata["requesting_user"]; ok {
			t.Error("DM must NOT carry requesting_user audit metadata")
		}
	})
}

func TestSubmitSlackTask_RunAsDisabled(t *testing.T) {
	t.Parallel()
	a, _, brk := newHandlerTestAdapter(t)
	a.cfg.DefaultAgentName = "TestAgent"
	a.cfg.UseUserIdentityInChannels = true
	a.botUserID = "U-bot"
	a.identityResolved = true
	a.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: "TestAgent"})

	q, err := brk.CreateQueue("runas-off-q", broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatalf("create queue: %v", err)
	}
	if err := brk.Subscribe(q, a2a.AgentRequestTopic("test-ns", "TestAgent")); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	a.HandleAppMention(&slackevents.AppMentionEvent{
		Channel: "C1", User: "U1", Text: "<@U-bot> hi", TimeStamp: "1700000092.000001",
	}, "T1")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	m, _ := brk.Receive(ctx, q)
	if m == nil {
		t.Fatal("expected a task published, got none")
	}
	if up := a2a.DecodeUserProperties(m.UserProperties); up.UserID != "test@test.com" {
		t.Errorf("flag off: non-DM userID = %q, want the real user test@test.com", up.UserID)
	}
}

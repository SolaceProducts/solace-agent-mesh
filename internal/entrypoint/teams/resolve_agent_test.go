// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package teams

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
)

func cardWithDisplayName(brokerName, display string) a2a.AgentCard {
	return a2a.AgentCard{
		Name: brokerName,
		Capabilities: a2a.AgentCapabilities{
			Extensions: []a2a.AgentExtension{{
				URI:    "https://solace.com/a2a/extensions/display-name",
				Params: map[string]any{"display_name": display},
			}},
		},
	}
}

func newAdapterForResolveTest(t *testing.T, cfg *TeamsConfig) *TeamsAdapter {
	t.Helper()
	a := &TeamsAdapter{cfg: cfg, logger: teamsNoopLogger()}
	a.core = entrypoint.NewGatewayCore(a, map[string]any{})
	return a
}

func TestResolveAgentName_AtMention(t *testing.T) {
	t.Parallel()
	a := newAdapterForResolveTest(t, &TeamsConfig{DefaultAgentName: "DefaultAgent"})
	a.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: "BetaAgent"})
	a.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: "AlphaAgent"})

	if got := a.resolveTarget(t.Context(), nil, "hey @BetaAgent please"); got.Name != "BetaAgent" {
		t.Errorf("at-mention = %q, want BetaAgent", got.Name)
	}
}

func TestResolveAgentName_AtMention_DisplayName(t *testing.T) {
	t.Parallel()
	a := newAdapterForResolveTest(t, &TeamsConfig{})
	a.core.AgentRegistry().AddOrUpdate(cardWithDisplayName(
		"workflow_019e394b_528f_737e_a032_45f04b8c1a2c", "shipment-request-workflow",
	))

	res := a.resolveTarget(t.Context(), nil, "hey @shipment-request-workflow please parse this")
	if res.Name != "workflow_019e394b_528f_737e_a032_45f04b8c1a2c" {
		t.Errorf("display-name mention = %q, want UUID-derived instance", res.Name)
	}
}

func TestResolveAgentName_DefaultPresent(t *testing.T) {
	t.Parallel()
	a := newAdapterForResolveTest(t, &TeamsConfig{DefaultAgentName: "DefaultAgent"})
	a.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: "DefaultAgent"})
	a.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: "OtherAgent"})

	if got := a.resolveTarget(t.Context(), nil, "hello"); got.Name != "DefaultAgent" {
		t.Errorf("default = %q, want DefaultAgent", got.Name)
	}
}

func TestResolveAgentName_DefaultViaDisplayName(t *testing.T) {
	t.Parallel()
	a := newAdapterForResolveTest(t, &TeamsConfig{DefaultAgentName: "shipment-request-workflow"})
	a.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: "Builder"})
	a.core.AgentRegistry().AddOrUpdate(cardWithDisplayName(
		"workflow_019e394b_528f_737e_a032_45f04b8c1a2c", "shipment-request-workflow",
	))

	res := a.resolveTarget(t.Context(), nil, "hello")
	if res.Misconfigured {
		t.Fatalf("expected match via display-name, got misconfigured=true")
	}
	if res.Name != "workflow_019e394b_528f_737e_a032_45f04b8c1a2c" {
		t.Errorf("via display-name = %q, want UUID-derived instance", res.Name)
	}
}

func TestResolveAgentName_MisconfiguredDoesNotFallBack(t *testing.T) {
	t.Parallel()
	a := newAdapterForResolveTest(t, &TeamsConfig{DefaultAgentName: "MissingAgent"})
	a.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: "Builder"})

	res := a.resolveTarget(t.Context(), nil, "hello")
	if !res.Misconfigured {
		t.Fatalf("expected misconfigured=true, got name=%q", res.Name)
	}
	if res.Name != "" {
		t.Errorf("expected empty name on misconfig, got %q", res.Name)
	}
}

func TestResolveAgentName_NoAgentsNoDefault(t *testing.T) {
	t.Parallel()
	a := newAdapterForResolveTest(t, &TeamsConfig{})
	res := a.resolveTarget(t.Context(), nil, "anybody home?")
	if res.Name != "" || res.Misconfigured {
		t.Errorf("empty registry = {%q, misconfig=%v}, want empty/false", res.Name, res.Misconfigured)
	}
}

func TestResolveAgentName_EmptyDefaultFallsBackToFirstAlphabetic(t *testing.T) {
	t.Parallel()
	a := newAdapterForResolveTest(t, &TeamsConfig{})
	a.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: "Zeta"})
	a.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: "Builder"})
	a.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: "Manager"})

	res := a.resolveTarget(t.Context(), nil, "hello")
	if res.Misconfigured || res.Name != "Builder" {
		t.Errorf("got {%q, misconfig=%v}, want Builder/false", res.Name, res.Misconfigured)
	}
}

type teamsHTTPMock struct {
	mu     sync.Mutex
	posts  []map[string]any
	server *httptest.Server
}

func newTeamsHTTPMock(t *testing.T) *teamsHTTPMock {
	t.Helper()
	m := &teamsHTTPMock{}
	mux := http.NewServeMux()
	mux.HandleFunc("/botframework.com/oauth2/v2.0/token", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "t", "token_type": "Bearer", "expires_in": 3600,
		})
	})
	mux.HandleFunc("/v3/conversations/", func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/activities") {
			http.NotFound(w, r)
			return
		}
		body, _ := io.ReadAll(r.Body)
		var act map[string]any
		_ = json.Unmarshal(body, &act)
		m.mu.Lock()
		m.posts = append(m.posts, act)
		m.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"id": "a1"})
	})
	m.server = httptest.NewServer(mux)
	t.Cleanup(m.server.Close)
	return m
}

func (m *teamsHTTPMock) sent() []map[string]any {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]map[string]any, len(m.posts))
	copy(out, m.posts)
	return out
}

func TestSubmitTeamsTask_MisconfigSendsChannelReply(t *testing.T) {
	t.Parallel()
	mock := newTeamsHTTPMock(t)

	a := newAdapterForResolveTest(t, &TeamsConfig{
		AppID:            "app",
		AppPassword:      "pw",
		DefaultAgentName: "does-not-exist",
		RunAs:            "default",
	})
	a.botClient = NewBotClient("app", "pw", "", a.logger)
	a.botClient.apiBaseURL = mock.server.URL
	a.emailCache = NewEmailCache("", 0, a.botClient, a.logger)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	a.ctx = ctx
	a.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: "Builder"})

	act := &Activity{
		Type:         ActivityTypeMessage,
		ID:           "in-1",
		ServiceURL:   mock.server.URL,
		Conversation: Conversation{ID: "conv1"},
		From:         Account{ID: "U-alice", Name: "alice"},
	}
	a.submitTeamsTask(ctx, act, "what is the capital of France?", nil, nil)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(mock.sent()) > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	posts := mock.sent()
	if len(posts) != 1 {
		t.Fatalf("expected exactly one SendActivity call, got %d (%+v)", len(posts), posts)
	}
	text, _ := posts[0]["text"].(string)
	if !strings.Contains(text, "does-not-exist") {
		t.Errorf("posted text = %q, want it to name the misconfigured agent", text)
	}
}

func TestHandleMessageActivity_PlainTextReachesSubmit(t *testing.T) {
	t.Parallel()
	mock := newTeamsHTTPMock(t)

	a := newAdapterForResolveTest(t, &TeamsConfig{AppID: "app", AppPassword: "pw", DefaultAgentName: "does-not-exist"})
	a.botClient = NewBotClient("app", "pw", "", a.logger)
	a.botClient.apiBaseURL = mock.server.URL
	a.emailCache = NewEmailCache("", 0, a.botClient, a.logger)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	a.ctx = ctx
	a.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: "Builder"})

	act := &Activity{
		Type:         ActivityTypeMessage,
		ID:           "in-text",
		ServiceURL:   mock.server.URL,
		Conversation: Conversation{ID: "conv-text", ConversationType: ConversationTypePersonal},
		From:         Account{ID: "U-alice", Name: "alice"},
		Text:         "what is the capital of France?",
	}
	a.handleMessageActivity(ctx, act)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(mock.sent()) > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(mock.sent()) != 1 {
		t.Fatalf("plain text must reach submitTeamsTask (expected 1 channel reply, got %d)", len(mock.sent()))
	}
}

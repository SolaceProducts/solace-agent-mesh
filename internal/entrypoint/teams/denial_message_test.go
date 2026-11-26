// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package teams

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

const deniedAgentWireName = "agent_019fe224_dd07_764b_ae4c_125a04fb3ab2"

type recordingBotClient struct {
	mu    sync.Mutex
	texts []string
}

func (r *recordingBotClient) record(body []byte) {
	var activity Activity
	if err := json.Unmarshal(body, &activity); err != nil || activity.Text == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.texts = append(r.texts, activity.Text)
}

func (r *recordingBotClient) sent() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.texts...)
}

func (r *recordingBotClient) botClient(t *testing.T) *BotClient {
	t.Helper()
	bc := NewBotClient("app", "secret", "", teamsNoopLogger())
	bc.client = &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			if req.Body != nil {
				body, _ := io.ReadAll(req.Body)
				r.record(body)
			}
			return &http.Response{
				StatusCode: 200,
				Header:     http.Header{"Content-Type": {"application/json"}},
				Body:       io.NopCloser(bytes.NewReader([]byte(`{"access_token":"tok","token_type":"Bearer","expires_in":3600,"id":"sent"}`))),
			}, nil
		}),
	}
	return bc
}

func TestTeamsDenial_ShowsPermissionCopyNotRawError(t *testing.T) {
	rec := &recordingBotClient{}
	a := newDenyingTeamsAdapter(t, rec, nil)

	activity := &Activity{Type: ActivityTypeMessage, Text: "hello"}
	activity.From.ID = "29:user"
	activity.From.AadObjectID = "aad-user"
	activity.Conversation.ID = "conv-1"

	a.submitTeamsTask(t.Context(), activity, "hello", nil, nil)

	sent := rec.sent()
	if len(sent) == 0 {
		t.Fatal("a denied user must be told something, not left in silence")
	}
	reply := sent[0]

	if strings.Contains(reply, deniedAgentWireName) {
		t.Errorf("reply leaks the agent's internal wire name: %q", reply)
	}
	if strings.Contains(reply, "agent delegation denied") || strings.Contains(reply, "Failed to submit task") {
		t.Errorf("reply exposes the raw Go error: %q", reply)
	}
	if !strings.Contains(reply, "don't have access") {
		t.Errorf("reply should name the permission boundary, got %q", reply)
	}

	allowedRec := &recordingBotClient{}
	allowed := newDenyingTeamsAdapter(t, allowedRec, []string{rbac.InvokeScopeForInstance(deniedAgentWireName)})
	okActivity := &Activity{Type: ActivityTypeMessage, Text: "hello"}
	okActivity.From.ID = "29:user"
	okActivity.From.AadObjectID = "aad-user"
	okActivity.Conversation.ID = "conv-2"
	allowed.submitTeamsTask(t.Context(), okActivity, "hello", nil, nil)
	for _, sent := range allowedRec.sent() {
		if strings.Contains(sent, "don't have access") {
			t.Errorf("a scoped user was refused: %q", sent)
		}
	}
}

func TestTeamsDenial_TargetedAgentMatchesUnknownAgent(t *testing.T) {
	rec := &recordingBotClient{}
	a := newDenyingTeamsAdapter(t, rec, nil)

	send := func(text string) string {
		t.Helper()
		before := len(rec.sent())
		activity := &Activity{Type: ActivityTypeMessage, Text: text}
		activity.From.ID = "29:user"
		activity.From.AadObjectID = "aad-user"
		activity.Conversation.ID = "conv-1"
		a.submitTeamsTask(t.Context(), activity, text, nil, nil)
		sent := rec.sent()
		if len(sent) <= before {
			t.Fatalf("no reply sent for %q", text)
		}
		return sent[before]
	}

	denied := send("@Orchestrator do a thing")
	unknown := send("@Orchestrator2 do a thing")

	if !strings.Contains(denied, "Orchestrator") || !strings.Contains(unknown, "Orchestrator2") {
		t.Fatalf("replies should echo the typed name:\n denied:  %q\n unknown: %q", denied, unknown)
	}

	if strings.Replace(denied, "Orchestrator", "TOKEN", 1) != strings.Replace(unknown, "Orchestrator2", "TOKEN", 1) {
		t.Errorf("a denied agent and a nonexistent one must be indistinguishable to the user:\n denied:  %q\n unknown: %q", denied, unknown)
	}
}

func newDenyingTeamsAdapter(t *testing.T, rec *recordingBotClient, scopes []string) *TeamsAdapter {
	t.Helper()
	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatalf("connect broker: %v", err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })

	core := entrypoint.NewGatewayCore(&entrypoint.LoggingAdapter{}, map[string]any{
		"namespace":  "test-ns",
		"gateway_id": "test-gw",
	})
	core.SetServices(runtime.NewRuntime(brk, nil).Services())
	if err := core.Init(t.Context(), nil); err != nil {
		t.Fatalf("core.Init: %v", err)
	}
	t.Cleanup(func() { _ = core.Stop(context.Background()) })

	card := a2a.AgentCard{Name: deniedAgentWireName}
	card.Capabilities.Extensions = append(card.Capabilities.Extensions, a2a.AgentExtension{
		URI:    a2a.ExtensionURIDisplayName,
		Params: map[string]any{"display_name": "Orchestrator"},
	})
	core.AgentRegistry().AddOrUpdate(card)
	core.SetScopeResolver(teamsAuthzScopeResolver{scopes: scopes})

	a := newTestAdapter(t)
	a.core = core
	a.ctx = t.Context()
	a.cfg.DefaultAgentName = "Orchestrator"
	a.botClient = rec.botClient(t)
	a.emailCache = NewEmailCache("", 0, a.botClient, a.logger)
	return a
}

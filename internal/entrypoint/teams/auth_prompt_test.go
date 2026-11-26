// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package teams

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

func authRequiredEvent(authURI string) *a2a.TaskStatusUpdateEvent {
	return &a2a.TaskStatusUpdateEvent{
		Kind: "status-update",
		Status: a2a.TaskStatus{
			State: a2a.TaskStateInputNeeded,
			Message: &a2a.Message{
				Role: "agent",
				Parts: []a2a.Part{a2a.NewDataPart(map[string]any{
					"type":         "authentication_required",
					"target_agent": "agent-x",
					"auth_uri":     authURI,
				})},
			},
		},
	}
}

func authResultEvent(success bool) *a2a.TaskStatusUpdateEvent {
	return &a2a.TaskStatusUpdateEvent{
		Kind: "status-update",
		Status: a2a.TaskStatus{
			State: a2a.TaskStateWorking,
			Message: &a2a.Message{
				Role: "agent",
				Parts: []a2a.Part{a2a.NewDataPart(map[string]any{
					"type":                 "authentication_result",
					"target_agent":         "agent-x",
					"target_agent_task_id": "task-auth",
					"success":              success,
				})},
			},
		},
	}
}

func newAuthPromptAdapter(t *testing.T, srv *httptest.Server) *TeamsAdapter {
	t.Helper()
	a := newTestAdapter(t)
	brk := broker.NewDevBroker()
	if err := brk.Connect(context.Background()); err != nil {
		t.Fatalf("connect dev broker: %v", err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })
	core := entrypoint.NewGatewayCore(&entrypoint.LoggingAdapter{}, map[string]any{
		"namespace": "test-ns", "gateway_id": "test-gw",
	})
	core.SetServices(runtime.NewRuntime(brk, nil).Services())
	a.core = core
	a.ctx = t.Context()
	a.cfg.APIBaseURL = srv.URL
	a.botClient = NewBotClient("app", "secret", "", a.logger)
	a.botClient.apiBaseURL = srv.URL
	a.queues = map[string]*MessageQueue{}
	return a
}

func registerAuthTask(t *testing.T, a *TeamsAdapter, taskID, serviceURL string) {
	t.Helper()
	_ = a.core.Tasks().Register(taskID, "agent", "u", entrypoint.TaskIdentity{})
	a.core.Tasks().SetMetadata(taskID, &TeamsTaskMeta{
		ConversationID: "conv-1", ServiceURL: serviceURL, SessionID: "s", Activity: &Activity{ID: "m1"},
	})
}

func countMethod(calls *[]capturedCall, mu *sync.Mutex, method string) int {
	mu.Lock()
	defer mu.Unlock()
	n := 0
	for _, c := range *calls {
		if c.Method == method {
			n++
		}
	}
	return n
}

func anyBodyContains(calls *[]capturedCall, mu *sync.Mutex, sub string) bool {
	mu.Lock()
	defer mu.Unlock()
	for _, c := range *calls {
		if b, err := json.Marshal(c.Body); err == nil && strings.Contains(string(b), sub) {
			return true
		}
	}
	return false
}

func TestAuthPromptCard(t *testing.T) {
	const uri = "https://idp.example/authorize?state=s1&code_challenge=abc"
	att := authPromptCard(uri)
	card, ok := att.Content.(map[string]any)
	if !ok {
		t.Fatalf("card content type = %T, want map", att.Content)
	}
	actions, ok := card["actions"].([]any)
	if !ok || len(actions) != 1 {
		t.Fatalf("actions = %v, want exactly one", card["actions"])
	}
	action := actions[0].(map[string]any)
	if action["type"] != "Action.OpenUrl" {
		t.Errorf("action type = %v, want Action.OpenUrl", action["type"])
	}
	if action["url"] != uri {
		t.Errorf("action url = %v, want %q", action["url"], uri)
	}
	if action["title"] != "Authorize" {
		t.Errorf("action title = %v, want Authorize", action["title"])
	}
}

func TestTeamsOnStatusUpdate_AuthRequiredPostsPrompt(t *testing.T) {
	t.Run("https posts the prompt with the auth_uri", func(t *testing.T) {
		srv, calls, mu := newMockBotServer(t)
		t.Cleanup(srv.Close)
		a := newAuthPromptAdapter(t, srv)
		registerAuthTask(t, a, "task-auth", srv.URL)

		const uri = "https://idp.example/authorize?state=s1"
		a.OnStatusUpdate(context.Background(), "task-auth", authRequiredEvent(uri), nil)

		if got := countMethod(calls, mu, "POST"); got != 1 {
			t.Fatalf("SendActivity (POST) calls = %d, want 1", got)
		}
		if !anyBodyContains(calls, mu, uri) || !anyBodyContains(calls, mu, "Action.OpenUrl") {
			t.Error("posted card must carry an Action.OpenUrl with the auth_uri")
		}
		meta := a.getTaskMeta(t.Context(), "task-auth")
		meta.mu.Lock()
		n := len(meta.authPromptActivityIDs)
		meta.mu.Unlock()
		if n != 1 {
			t.Errorf("authPromptActivityIDs len = %d, want 1", n)
		}
	})

	t.Run("auth part co-occurring with text still posts the prompt", func(t *testing.T) {
		srv, calls, mu := newMockBotServer(t)
		t.Cleanup(srv.Close)
		a := newAuthPromptAdapter(t, srv)
		registerAuthTask(t, a, "task-auth", srv.URL)

		const uri = "https://idp.example/authorize?state=s2"
		event := &a2a.TaskStatusUpdateEvent{
			Kind: "status-update",
			Status: a2a.TaskStatus{
				State: a2a.TaskStateInputNeeded,
				Message: &a2a.Message{
					Role: "agent",
					Parts: []a2a.Part{
						a2a.NewTextPart("hang on, I need access"),
						a2a.NewDataPart(map[string]any{
							"type":     "authentication_required",
							"auth_uri": uri,
						}),
					},
				},
			},
		}
		a.OnStatusUpdate(context.Background(), "task-auth", event, nil)

		if got := countMethod(calls, mu, "POST"); got != 1 {
			t.Errorf("SendActivity (POST) calls = %d, want 1 (auth prompt posted despite co-occurring text)", got)
		}
		if !anyBodyContains(calls, mu, uri) || !anyBodyContains(calls, mu, "Action.OpenUrl") {
			t.Error("posted card must carry an Action.OpenUrl with the auth_uri")
		}
	})

	for _, uri := range []string{
		"http://evil.example/authorize",
		"javascript:alert(1)",
		"data:text/html,<script>",
		"//evil.example/authorize",
		"https:",
		"https://",
		"",
	} {
		t.Run("rejected: "+uri, func(t *testing.T) {
			srv, calls, mu := newMockBotServer(t)
			t.Cleanup(srv.Close)
			a := newAuthPromptAdapter(t, srv)
			registerAuthTask(t, a, "task-auth", srv.URL)

			a.OnStatusUpdate(context.Background(), "task-auth", authRequiredEvent(uri), nil)

			if got := countMethod(calls, mu, "POST"); got != 0 {
				t.Errorf("SendActivity (POST) calls = %d for %q, want 0 (only https surfaced)", got, uri)
			}
		})
	}
}

func TestTeamsOnStatusUpdate_InterimActivityPreservesAuthPrompt(t *testing.T) {
	interimEvents := map[string]*a2a.TaskStatusUpdateEvent{
		"text status": {
			Kind: "status-update",
			Status: a2a.TaskStatus{State: a2a.TaskStateWorking, Message: &a2a.Message{
				Role:  "agent",
				Parts: []a2a.Part{a2a.NewTextPart("working on it...")},
			}},
		},
		"interim tool_result": {
			Kind: "status-update",
			Status: a2a.TaskStatus{State: a2a.TaskStateWorking, Message: &a2a.Message{
				Role:  "agent",
				Parts: []a2a.Part{a2a.NewDataPart(map[string]any{"type": "tool_result"})},
			}},
		},
	}

	for name, interim := range interimEvents {
		t.Run(name, func(t *testing.T) {
			srv, calls, mu := newMockBotServer(t)
			t.Cleanup(srv.Close)
			a := newAuthPromptAdapter(t, srv)
			registerAuthTask(t, a, "task-auth", srv.URL)
			ctx := context.Background()

			a.OnStatusUpdate(ctx, "task-auth", authRequiredEvent("https://idp.example/authorize?state=s1"), nil)
			meta := a.getTaskMeta(t.Context(), "task-auth")
			meta.mu.Lock()
			posted := len(meta.authPromptActivityIDs)
			meta.mu.Unlock()
			if posted != 1 {
				t.Fatalf("authPromptActivityIDs len = %d, want 1 after prompt posted", posted)
			}

			a.OnStatusUpdate(ctx, "task-auth", interim, nil)

			meta.mu.Lock()
			remaining := len(meta.authPromptActivityIDs)
			meta.mu.Unlock()
			if remaining != 1 {
				t.Errorf("authPromptActivityIDs len = %d after interim activity, want 1 (card preserved)", remaining)
			}
			if got := countMethod(calls, mu, "PUT"); got != 0 {
				t.Errorf("UpdateActivity (PUT) calls = %d, want 0 (card must not be retired by interim activity)", got)
			}
		})
	}
}

func TestTeamsResolveAuthPrompt(t *testing.T) {
	t.Run("updates and clears when prompts are outstanding", func(t *testing.T) {
		srv, calls, mu := newMockBotServer(t)
		t.Cleanup(srv.Close)
		a := newAuthPromptAdapter(t, srv)
		meta := &TeamsTaskMeta{ConversationID: "conv-1", ServiceURL: srv.URL, authPromptActivityIDs: []string{"a1", "a2"}}

		a.resolveAuthPrompt(t.Context(), "task-auth", meta, true)

		if got := countMethod(calls, mu, "PUT"); got != 2 {
			t.Errorf("UpdateActivity (PUT) calls = %d, want 2 (both cards retired)", got)
		}
		meta.mu.Lock()
		remaining := len(meta.authPromptActivityIDs)
		meta.mu.Unlock()
		if remaining != 0 {
			t.Errorf("authPromptActivityIDs len = %d, want 0", remaining)
		}
	})

	t.Run("success outcome renders the authorized card", func(t *testing.T) {
		srv, calls, mu := newMockBotServer(t)
		t.Cleanup(srv.Close)
		a := newAuthPromptAdapter(t, srv)
		meta := &TeamsTaskMeta{ConversationID: "conv-1", ServiceURL: srv.URL, authPromptActivityIDs: []string{"a1"}}

		a.resolveAuthPrompt(t.Context(), "task-auth", meta, true)

		if !anyBodyContains(calls, mu, "Authorized") {
			t.Error("resolved card must show the Authorized banner on success")
		}
	})

	t.Run("failure outcome renders a retry notice, not a false success", func(t *testing.T) {
		srv, calls, mu := newMockBotServer(t)
		t.Cleanup(srv.Close)
		a := newAuthPromptAdapter(t, srv)
		meta := &TeamsTaskMeta{ConversationID: "conv-1", ServiceURL: srv.URL, authPromptActivityIDs: []string{"a1"}}

		a.resolveAuthPrompt(t.Context(), "task-auth", meta, false)

		if !anyBodyContains(calls, mu, "didn't complete") {
			t.Error("resolved card must show a 'didn't complete' notice on failure")
		}
		if anyBodyContains(calls, mu, "Authorized — thanks!") {
			t.Error("failure outcome must NOT render a false success card")
		}
	})

	t.Run("no-op when none outstanding", func(t *testing.T) {
		srv, calls, mu := newMockBotServer(t)
		t.Cleanup(srv.Close)
		a := newAuthPromptAdapter(t, srv)
		meta := &TeamsTaskMeta{ConversationID: "conv-1", ServiceURL: srv.URL}

		a.resolveAuthPrompt(t.Context(), "task-auth", meta, true)

		if got := countMethod(calls, mu, "PUT"); got != 0 {
			t.Errorf("UpdateActivity (PUT) calls = %d, want 0 (nothing to retire)", got)
		}
	})

	t.Run("idempotent: second call after a resolve is a no-op", func(t *testing.T) {
		srv, calls, mu := newMockBotServer(t)
		t.Cleanup(srv.Close)
		a := newAuthPromptAdapter(t, srv)
		meta := &TeamsTaskMeta{ConversationID: "conv-1", ServiceURL: srv.URL, authPromptActivityIDs: []string{"a1"}}

		a.resolveAuthPrompt(t.Context(), "task-auth", meta, true)
		a.resolveAuthPrompt(t.Context(), "task-auth", meta, true)

		if got := countMethod(calls, mu, "PUT"); got != 1 {
			t.Errorf("UpdateActivity (PUT) calls = %d, want 1 (second resolve is a no-op)", got)
		}
	})
}

func TestTeamsOnStatusUpdate_AuthResultResolvesPrompt(t *testing.T) {
	t.Run("success result shows the authorized card", func(t *testing.T) {
		srv, calls, mu := newMockBotServer(t)
		t.Cleanup(srv.Close)
		a := newAuthPromptAdapter(t, srv)
		registerAuthTask(t, a, "task-auth", srv.URL)
		ctx := context.Background()

		a.OnStatusUpdate(ctx, "task-auth", authRequiredEvent("https://idp.example/authorize?state=s1"), nil)
		a.OnStatusUpdate(ctx, "task-auth", authResultEvent(true), nil)

		if !anyBodyContains(calls, mu, "Authorized") {
			t.Error("card must show the Authorized banner on a success result")
		}
		meta := a.getTaskMeta(t.Context(), "task-auth")
		meta.mu.Lock()
		remaining := len(meta.authPromptActivityIDs)
		meta.mu.Unlock()
		if remaining != 0 {
			t.Errorf("authPromptActivityIDs len = %d after result, want 0 (card retired)", remaining)
		}
	})

	t.Run("failure result shows a retry notice, not a false success", func(t *testing.T) {
		srv, calls, mu := newMockBotServer(t)
		t.Cleanup(srv.Close)
		a := newAuthPromptAdapter(t, srv)
		registerAuthTask(t, a, "task-auth", srv.URL)
		ctx := context.Background()

		a.OnStatusUpdate(ctx, "task-auth", authRequiredEvent("https://idp.example/authorize?state=s1"), nil)
		a.OnStatusUpdate(ctx, "task-auth", authResultEvent(false), nil)

		if !anyBodyContains(calls, mu, "didn't complete") {
			t.Error("card must show a failure notice on a failure result")
		}
		if anyBodyContains(calls, mu, "Authorized — thanks!") {
			t.Error("failure result must NOT render a false success card")
		}
	})
}

func TestTeamsOnError_ResolvesAuthPromptAsFailed(t *testing.T) {
	srv, calls, mu := newMockBotServer(t)
	t.Cleanup(srv.Close)
	a := newAuthPromptAdapter(t, srv)
	registerAuthTask(t, a, "task-auth", srv.URL)
	ctx := context.Background()

	a.OnStatusUpdate(ctx, "task-auth", authRequiredEvent("https://idp.example/authorize?state=s1"), nil)
	a.OnError(ctx, "task-auth", &a2a.Error{Message: "boom"}, nil)

	if got := countMethod(calls, mu, "PUT"); got < 1 {
		t.Errorf("UpdateActivity (PUT) calls = %d, want >=1 (card retired on error)", got)
	}
	if anyBodyContains(calls, mu, "Authorized — thanks!") {
		t.Error("an errored task must NOT render a false success auth card")
	}
}

func TestTeamsOnFinalResponse_CompletedResolvesAuthPrompt(t *testing.T) {
	srv, calls, mu := newMockBotServer(t)
	t.Cleanup(srv.Close)
	a := newAuthPromptAdapter(t, srv)
	registerAuthTask(t, a, "task-auth", srv.URL)
	ctx := context.Background()

	a.OnStatusUpdate(ctx, "task-auth", authRequiredEvent("https://idp.example/authorize?state=s1"), nil)
	a.OnFinalResponse(ctx, "task-auth", &a2a.Task{
		ID: "task-auth", Kind: "task", Status: a2a.TaskStatus{State: a2a.TaskStateCompleted},
	}, nil)

	if got := countMethod(calls, mu, "PUT"); got < 1 {
		t.Errorf("UpdateActivity (PUT) calls = %d, want >=1 (card retired on final response)", got)
	}
	if !anyBodyContains(calls, mu, "Authorized") {
		t.Error("a completed task with an outstanding prompt must resolve the card as authorized")
	}
}

func TestTeamsOnFinalResponse_FailedResolvesAuthPromptAsFailed(t *testing.T) {
	srv, calls, mu := newMockBotServer(t)
	t.Cleanup(srv.Close)
	a := newAuthPromptAdapter(t, srv)
	registerAuthTask(t, a, "task-auth", srv.URL)
	ctx := context.Background()

	a.OnStatusUpdate(ctx, "task-auth", authRequiredEvent("https://idp.example/authorize?state=s1"), nil)
	a.OnFinalResponse(ctx, "task-auth", &a2a.Task{
		ID: "task-auth", Kind: "task", Status: a2a.TaskStatus{State: a2a.TaskStateFailed},
	}, nil)

	if !anyBodyContains(calls, mu, "didn't complete") {
		t.Error("a failed task must resolve the auth card as 'didn't complete'")
	}
	if anyBodyContains(calls, mu, "Authorized — thanks!") {
		t.Error("a failed task must NOT render a false success auth card")
	}
}

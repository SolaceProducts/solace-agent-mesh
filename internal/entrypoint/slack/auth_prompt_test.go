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

	goslack "github.com/slack-go/slack"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
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

func TestAuthPromptBlocks(t *testing.T) {
	const uri = "https://idp.example/authorize?state=s1&code_challenge=abc"
	blocks := authPromptBlocks(uri)

	var btn *goslack.ButtonBlockElement
	for _, b := range blocks {
		ab, ok := b.(*goslack.ActionBlock)
		if !ok || ab.Elements == nil {
			continue
		}
		for _, el := range ab.Elements.ElementSet {
			if e, ok := el.(*goslack.ButtonBlockElement); ok {
				btn = e
			}
		}
	}
	if btn == nil {
		t.Fatal("expected an action block with a button")
	}
	if btn.URL != uri {
		t.Errorf("button URL = %q, want %q", btn.URL, uri)
	}
	if btn.ActionID != toolAuthActionID {
		t.Errorf("button action_id = %q, want %q", btn.ActionID, toolAuthActionID)
	}
	if btn.Style != goslack.StylePrimary {
		t.Errorf("button style = %q, want primary", btn.Style)
	}
}

func TestOnStatusUpdate_AuthRequiredPostsLink(t *testing.T) {
	t.Run("https posts the prompt with the auth_uri", func(t *testing.T) {
		a, calls, _ := newHandlerTestAdapter(t)
		registerSlackTask(a, "task-auth")

		const uri = "https://idp.example/authorize?state=s1"
		a.OnStatusUpdate(context.Background(), "task-auth", authRequiredEvent(uri), nil)

		if got := calls.Posts(); got != 1 {
			t.Fatalf("chat.postMessage calls = %d, want 1 (auth link)", got)
		}
		if blocks := calls.LastPostBlocks(); !strings.Contains(blocks, uri) {
			t.Errorf("posted blocks do not contain the auth_uri %q; blocks=%s", uri, blocks)
		}
		meta := a.getTaskMeta(t.Context(), "task-auth")
		meta.mu.Lock()
		n := len(meta.authPromptTSs)
		meta.mu.Unlock()
		if n != 1 {
			t.Errorf("authPromptTSs len = %d, want 1 after posting the auth prompt", n)
		}
	})

	t.Run("auth part co-occurring with text still posts the link", func(t *testing.T) {
		a, calls, _ := newHandlerTestAdapter(t)
		registerSlackTask(a, "task-auth")

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
							"auth_uri": "https://idp.example/authorize?state=s2",
						}),
					},
				},
			},
		}
		a.OnStatusUpdate(context.Background(), "task-auth", event, nil)

		if got := calls.Posts(); got != 1 {
			t.Errorf("chat.postMessage calls = %d, want 1 (auth link posted despite co-occurring text)", got)
		}
	})

	for _, uri := range []string{
		"http://evil.example/authorize?state=s1",
		"javascript:alert(1)",
		"data:text/html,<script>",
		"//evil.example/authorize",
		"",
	} {
		t.Run("rejected: "+uri, func(t *testing.T) {
			a, calls, _ := newHandlerTestAdapter(t)
			registerSlackTask(a, "task-auth")

			a.OnStatusUpdate(context.Background(), "task-auth", authRequiredEvent(uri), nil)

			if got := calls.Posts(); got != 0 {
				t.Errorf("chat.postMessage calls = %d for %q, want 0 (only https surfaced)", got, uri)
			}
		})
	}
}

func TestOnStatusUpdate_InterimActivityPreservesAuthPrompt(t *testing.T) {
	a, calls, _ := newHandlerTestAdapter(t)
	registerSlackTask(a, "task-auth")
	ctx := context.Background()

	a.OnStatusUpdate(ctx, "task-auth", authRequiredEvent("https://idp.example/authorize?state=s1"), nil)
	meta := a.getTaskMeta(t.Context(), "task-auth")
	meta.mu.Lock()
	posted := len(meta.authPromptTSs)
	meta.mu.Unlock()
	if posted != 1 {
		t.Fatalf("authPromptTSs len = %d, want 1 after the prompt was posted", posted)
	}

	interim := &a2a.TaskStatusUpdateEvent{
		Kind: "status-update",
		Status: a2a.TaskStatus{
			State: a2a.TaskStateWorking,
			Message: &a2a.Message{
				Role:  "agent",
				Parts: []a2a.Part{a2a.NewDataPart(map[string]any{"type": "tool_result"})},
			},
		},
	}
	a.OnStatusUpdate(ctx, "task-auth", interim, nil)

	meta.mu.Lock()
	remaining := len(meta.authPromptTSs)
	meta.mu.Unlock()
	if remaining != 1 {
		t.Errorf("authPromptTSs len = %d after interim activity, want 1 (button preserved)", remaining)
	}
	if calls.Updates() != 0 {
		t.Errorf("chat.update calls = %d, want 0 (button must not be retired by interim activity)", calls.Updates())
	}
}

func TestResolveAuthPrompt(t *testing.T) {
	t.Run("updates and clears when prompts are outstanding", func(t *testing.T) {
		a, calls, _ := newHandlerTestAdapter(t)
		meta := &SlackTaskMeta{ChannelID: "C123", ThreadTS: "t1", authPromptTSs: []string{"ts-1", "ts-2"}}

		a.resolveAuthPrompt(meta, true)

		if calls.Updates() != 2 {
			t.Errorf("chat.update calls = %d, want 2 (both buttons retired)", calls.Updates())
		}
		meta.mu.Lock()
		remaining := len(meta.authPromptTSs)
		meta.mu.Unlock()
		if remaining != 0 {
			t.Errorf("authPromptTSs len = %d, want 0 (cleared)", remaining)
		}
	})

	t.Run("failure outcome renders a retry notice, not a false success", func(t *testing.T) {
		a, calls, _ := newHandlerTestAdapter(t)
		meta := &SlackTaskMeta{ChannelID: "C123", ThreadTS: "t1", authPromptTSs: []string{"ts-1"}}

		a.resolveAuthPrompt(meta, false)

		if txt := calls.LastUpdateText(); !strings.Contains(strings.ToLower(txt), "didn't complete") {
			t.Errorf("update text = %q, want a 'didn't complete' notice (not a false success)", txt)
		}
	})

	t.Run("success outcome renders the authorized banner", func(t *testing.T) {
		a, calls, _ := newHandlerTestAdapter(t)
		meta := &SlackTaskMeta{ChannelID: "C123", ThreadTS: "t1", authPromptTSs: []string{"ts-1"}}

		a.resolveAuthPrompt(meta, true)

		if txt := calls.LastUpdateText(); !strings.Contains(txt, "Authorized") {
			t.Errorf("update text = %q, want the Authorized banner", txt)
		}
	})

	t.Run("no-op when no prompt outstanding", func(t *testing.T) {
		a, calls, _ := newHandlerTestAdapter(t)
		meta := &SlackTaskMeta{ChannelID: "C123", ThreadTS: "t1"}

		a.resolveAuthPrompt(meta, true)

		if calls.Updates() != 0 {
			t.Errorf("chat.update calls = %d, want 0 (nothing to retire)", calls.Updates())
		}
	})

	t.Run("idempotent: second call after a resolve is a no-op", func(t *testing.T) {
		a, calls, _ := newHandlerTestAdapter(t)
		meta := &SlackTaskMeta{ChannelID: "C123", ThreadTS: "t1", authPromptTSs: []string{"ts-1"}}

		a.resolveAuthPrompt(meta, true)
		a.resolveAuthPrompt(meta, true)

		if calls.Updates() != 1 {
			t.Errorf("chat.update calls = %d, want 1 (second resolve is a no-op)", calls.Updates())
		}
	})
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

func TestOnStatusUpdate_AuthResultResolvesPrompt(t *testing.T) {
	t.Run("failure result shows a retry notice, not a false success", func(t *testing.T) {
		a, calls, _ := newHandlerTestAdapter(t)
		registerSlackTask(a, "task-auth")
		ctx := context.Background()

		a.OnStatusUpdate(ctx, "task-auth", authRequiredEvent("https://idp.example/authorize?state=s1"), nil)
		a.OnStatusUpdate(ctx, "task-auth", authResultEvent(false), nil)

		if txt := calls.LastUpdateText(); !strings.Contains(strings.ToLower(txt), "didn't complete") {
			t.Errorf("update text = %q, want a failure notice (not 'Authorized')", txt)
		}
		meta := a.getTaskMeta(t.Context(), "task-auth")
		meta.mu.Lock()
		remaining := len(meta.authPromptTSs)
		meta.mu.Unlock()
		if remaining != 0 {
			t.Errorf("authPromptTSs len = %d after the result, want 0 (button retired)", remaining)
		}
	})

	t.Run("success result shows the authorized banner", func(t *testing.T) {
		a, calls, _ := newHandlerTestAdapter(t)
		registerSlackTask(a, "task-auth")
		ctx := context.Background()

		a.OnStatusUpdate(ctx, "task-auth", authRequiredEvent("https://idp.example/authorize?state=s1"), nil)
		a.OnStatusUpdate(ctx, "task-auth", authResultEvent(true), nil)

		if txt := calls.LastUpdateText(); !strings.Contains(txt, "Authorized") {
			t.Errorf("update text = %q, want the Authorized banner", txt)
		}
	})
}

func TestResolveAuthPrompt_ConcurrentResolversClaimPromptOnce(t *testing.T) {
	a, calls, _ := newHandlerTestAdapter(t)
	registerSlackTask(a, "task-auth")
	ctx := context.Background()

	a.OnStatusUpdate(ctx, "task-auth", authRequiredEvent("https://idp.example/authorize?state=s1"), nil)
	meta := a.getTaskMeta(t.Context(), "task-auth")
	meta.mu.Lock()
	cardTS := meta.authPromptTSs[0]
	meta.mu.Unlock()

	var wg sync.WaitGroup
	wg.Go(func() { a.OnStatusUpdate(ctx, "task-auth", authResultEvent(false), nil) })
	wg.Go(func() {
		a.OnFinalResponse(ctx, "task-auth", &a2a.Task{
			ID: "task-auth", Kind: "task", Status: a2a.TaskStatus{State: a2a.TaskStateCompleted},
		}, nil)
	})
	wg.Wait()

	meta.mu.Lock()
	remaining := len(meta.authPromptTSs)
	meta.mu.Unlock()
	if remaining != 0 {
		t.Errorf("authPromptTSs len = %d, want 0 (prompt claimed exactly once)", remaining)
	}
	txt := calls.UpdateTextForTS(cardTS)
	if !strings.Contains(txt, "Authorized") && !strings.Contains(strings.ToLower(txt), "didn't complete") {
		t.Errorf("auth card text = %q, want one of the two terminal banners", txt)
	}
}

func TestOnError_ClearsAuthPrompt(t *testing.T) {
	a, calls, _ := newHandlerTestAdapter(t)
	registerSlackTask(a, "task-auth")
	ctx := context.Background()

	a.OnStatusUpdate(ctx, "task-auth", authRequiredEvent("https://idp.example/authorize?state=s1"), nil)
	meta := a.getTaskMeta(t.Context(), "task-auth")
	meta.mu.Lock()
	cardTS := meta.authPromptTSs[0]
	meta.mu.Unlock()

	a.OnError(ctx, "task-auth", &a2a.Error{Message: "boom"}, nil)

	meta.mu.Lock()
	remaining := len(meta.authPromptTSs)
	meta.mu.Unlock()
	if remaining != 0 {
		t.Errorf("authPromptTSs len = %d after OnError, want 0 (button retired)", remaining)
	}
	if txt := calls.UpdateTextForTS(cardTS); strings.Contains(txt, "Authorized") || !strings.Contains(strings.ToLower(txt), "didn't complete") {
		t.Errorf("auth card update = %q, want a 'didn't complete' notice (not a false success)", txt)
	}
}

func TestOnFinalResponse_ClearsAuthPrompt(t *testing.T) {
	a, calls, _ := newHandlerTestAdapter(t)
	registerSlackTask(a, "task-auth")
	ctx := context.Background()

	a.OnStatusUpdate(ctx, "task-auth", authRequiredEvent("https://idp.example/authorize?state=s1"), nil)
	a.OnFinalResponse(ctx, "task-auth", &a2a.Task{
		ID:     "task-auth",
		Kind:   "task",
		Status: a2a.TaskStatus{State: a2a.TaskStateCompleted},
	}, nil)

	meta := a.getTaskMeta(t.Context(), "task-auth")
	if meta != nil {
		meta.mu.Lock()
		remaining := len(meta.authPromptTSs)
		meta.mu.Unlock()
		if remaining != 0 {
			t.Errorf("authPromptTSs len = %d after final response, want 0 (button retired)", remaining)
		}
	}
	if calls.Updates() < 1 {
		t.Errorf("chat.update calls = %d, want >=1 (button retired on final response)", calls.Updates())
	}
}

func TestOnFinalResponse_FailedTaskResolvesPromptAsFailed(t *testing.T) {
	a, calls, _ := newHandlerTestAdapter(t)
	registerSlackTask(a, "task-auth")
	ctx := context.Background()

	a.OnStatusUpdate(ctx, "task-auth", authRequiredEvent("https://idp.example/authorize?state=s1"), nil)
	meta := a.getTaskMeta(t.Context(), "task-auth")
	meta.mu.Lock()
	cardTS := meta.authPromptTSs[0]
	meta.mu.Unlock()

	a.OnFinalResponse(ctx, "task-auth", &a2a.Task{
		ID:     "task-auth",
		Kind:   "task",
		Status: a2a.TaskStatus{State: a2a.TaskStateFailed},
	}, nil)

	if txt := calls.UpdateTextForTS(cardTS); strings.Contains(txt, "Authorized") || !strings.Contains(strings.ToLower(txt), "didn't complete") {
		t.Errorf("auth card update = %q, want a 'didn't complete' notice for a failed task (not 'Authorized')", txt)
	}
}

func TestOnStatusUpdate_AuthResultNoOutstandingPrompt(t *testing.T) {
	a, calls, _ := newHandlerTestAdapter(t)
	registerSlackTask(a, "task-auth")

	a.OnStatusUpdate(context.Background(), "task-auth", authResultEvent(true), nil)

	if calls.Updates() != 0 {
		t.Errorf("chat.update calls = %d, want 0 (no outstanding prompt to resolve)", calls.Updates())
	}
}

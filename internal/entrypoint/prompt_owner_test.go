// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
)

const (
	promptTask  = "task-prompt-1"
	promptAlice = "alice@dev.local"
	promptBob   = "bob@dev.local"
)

func registerPromptTask(t *testing.T, core *GatewayCore, owner string) {
	t.Helper()
	if err := core.tasks.Register(promptTask, "target-agent", owner, TaskIdentity{}); err != nil {
		t.Fatalf("register task: %v", err)
	}
}

func TestMayAnswerPrompt(t *testing.T) {
	core, _, _ := setupTestGateway(t)
	registerPromptTask(t, core, promptAlice)
	for _, tc := range []struct {
		name   string
		taskID string
		user   string
		want   bool
	}{
		{"the owner", promptTask, promptAlice, true},
		{"the owner in another ASCII case", promptTask, "ALICE@dev.local", true},
		{"another user", promptTask, promptBob, false},
		{"no user", promptTask, "", false},
		{"a task the gateway does not track", "task-unknown", promptBob, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := core.MayAnswerPrompt(tc.taskID, tc.user); got != tc.want {
				t.Errorf("MayAnswerPrompt(%q, %q) = %v, want %v", tc.taskID, tc.user, got, tc.want)
			}
		})
	}
}

func TestPromptAnswerUserID(t *testing.T) {
	core, _, _ := setupTestGateway(t)
	registerPromptTask(t, core, promptAlice)
	const slackFallback = "slack:T1:U1"
	asked := ChatPlace{Conversation: "D-asked", User: "U-asker"}
	for _, tc := range []struct {
		name    string
		taskID  string
		asked   ChatPlace
		clicked ChatPlace
		want    string
	}{
		{"the click from where the task was asked answers as the task's user", promptTask, asked, asked, promptAlice},
		{"another conversation", promptTask, asked, ChatPlace{Conversation: "D-other", User: "U-asker"}, slackFallback},
		{"another person in that conversation", promptTask, asked, ChatPlace{Conversation: "D-asked", User: "U-other"}, slackFallback},
		{"no record of where the task was asked", promptTask, ChatPlace{}, asked, slackFallback},
		{"a record without the person", promptTask, ChatPlace{Conversation: "D-asked"}, ChatPlace{Conversation: "D-asked"}, slackFallback},
		{"a record without the conversation", promptTask, ChatPlace{User: "U-asker"}, ChatPlace{User: "U-asker"}, slackFallback},
		{"the same place in another case", promptTask, asked, ChatPlace{Conversation: "d-asked", User: "u-asker"}, slackFallback},
		{"a task the gateway does not track", "task-unknown", asked, asked, slackFallback},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := core.PromptAnswerUserID(tc.taskID, slackFallback, tc.asked, tc.clicked); got != tc.want {
				t.Errorf("PromptAnswerUserID = %q, want %q", got, tc.want)
			}
		})
	}
}

func startPromptGateway(t *testing.T) (*GatewayCore, broker.Queue) {
	t.Helper()
	core, brk, _ := setupTestGateway(t)
	ctx := context.Background()
	if err := core.Init(ctx, nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := core.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = core.Stop(ctx) })
	q, err := brk.CreateQueue("prompt-owner-agent-q", broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatalf("create agent queue: %v", err)
	}
	if err = brk.Subscribe(q, a2a.AgentRequestTopic("test-ns", "target-agent")); err != nil {
		t.Fatalf("subscribe agent: %v", err)
	}
	return core, q
}

func receiveWithin(core *GatewayCore, q broker.Queue, wait time.Duration) *broker.Message {
	ctx, cancel := context.WithTimeout(context.Background(), wait)
	defer cancel()
	msg, err := core.Services().Broker().Receive(ctx, q)
	if err != nil {
		return nil
	}
	return msg
}

func TestSubmitUserInputResponsePublishesTheAnsweringUser(t *testing.T) {
	core, q := startPromptGateway(t)
	registerPromptTask(t, core, promptAlice)
	resp := a2a.UserInputResponseData{Type: "user_input_response", RequestID: "req-1", Status: "answered"}

	for _, answerer := range []string{promptAlice, "ALICE@dev.local"} {
		caller := auth.NewTestSubject(auth.SubjectFields{UserID: answerer, Authenticated: true})
		if err := core.SubmitUserInputResponse(t.Context(), "target-agent", promptTask, resp, caller); err != nil {
			t.Fatalf("answer as %q: %v", answerer, err)
		}
		msg := receiveWithin(core, q, 2*time.Second)
		if msg == nil {
			t.Fatalf("answer as %q was not published", answerer)
		}
		if got := a2a.DecodeUserProperties(msg.UserProperties).UserID; got != answerer {
			t.Errorf("published userId = %q, want %q", got, answerer)
		}
	}
}

func TestSubmitUserInputResponseRefusesAnotherUsersAnswer(t *testing.T) {
	core, q := startPromptGateway(t)
	registerPromptTask(t, core, promptAlice)
	resp := a2a.UserInputResponseData{Type: "user_input_response", RequestID: "req-1", Status: "answered"}
	caller := auth.NewTestSubject(auth.SubjectFields{UserID: promptBob, Authenticated: true})

	err := core.SubmitUserInputResponse(t.Context(), "target-agent", promptTask, resp, caller)

	if !errors.Is(err, ErrNotTaskOwner) {
		t.Fatalf("err = %v, want ErrNotTaskOwner", err)
	}
	if msg := receiveWithin(core, q, 200*time.Millisecond); msg != nil {
		t.Errorf("another user's answer was published: %s", msg.Payload)
	}
}

func TestAuthenticationRequiredNamingAnotherUserIsNotCached(t *testing.T) {
	for _, tc := range []struct {
		name      string
		owner     string
		wantCache bool
	}{
		{"the task's own user", "u1", true},
		{"the task's user in another ASCII case", "U1", true},
		{"another user", "someone-else", false},
		{"a task the gateway does not track", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			core, _, _ := setupTestGateway(t)
			core.logger = silentLogger()
			if tc.owner != "" {
				if err := core.tasks.Register("task-auth", "agent-x", tc.owner, TaskIdentity{}); err != nil {
					t.Fatalf("register task: %v", err)
				}
			}
			event, msg := authStatusEvent("task-auth", "state-"+tc.name)

			got := core.cacheAuthStateFromStatus(event, msg)

			if got != tc.wantCache {
				t.Fatalf("cacheAuthStateFromStatus = %v, want %v", got, tc.wantCache)
			}
			if _, found := core.TakeToolAuthState(t.Context(), "state-"+tc.name); found != tc.wantCache {
				t.Errorf("state cached = %v, want %v", found, tc.wantCache)
			}
		})
	}
}

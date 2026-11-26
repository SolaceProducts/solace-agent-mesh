// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package teams

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

const (
	askedConversation = "conv-asked"
	askerTeamsID      = "29:asker"
)

func clickActivity(t *testing.T, a *TeamsAdapter, requestID, taskID, conversationID, conversationType, userID string) *Activity {
	t.Helper()
	card, _ := a.buildHILCard(parseTeamsSurfaceT(t), requestID, taskID, "TestAgent", false)
	submit := submitActionData(t, card, HILSubmitAction)
	submit["hil_0"] = "o0"
	value, err := json.Marshal(submit)
	if err != nil {
		t.Fatal(err)
	}
	return &Activity{
		Type: ActivityTypeInvoke, Value: value, From: Account{ID: userID},
		Conversation: Conversation{ID: conversationID, ConversationType: conversationType},
	}
}

func askedTask(t *testing.T, a *TeamsAdapter, taskID, owner string) {
	t.Helper()
	if err := a.core.Tasks().Register(taskID, "TestAgent", owner, entrypoint.TaskIdentity{}); err != nil {
		t.Fatal(err)
	}
	a.core.Tasks().SetMetadata(taskID, &TeamsTaskMeta{
		ConversationID: askedConversation,
		Activity:       &Activity{From: Account{ID: askerTeamsID}, Conversation: Conversation{ID: askedConversation}},
	})
}

func receiveWithin(t *testing.T, brk *broker.DevBroker, q broker.Queue, wait time.Duration) *broker.Message {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), wait)
	defer cancel()
	msg, err := brk.Receive(ctx, q)
	if err != nil {
		return nil
	}
	return msg
}

func subscribeToAgent(t *testing.T, brk *broker.DevBroker, name string) broker.Queue {
	t.Helper()
	q, err := brk.CreateQueue(name, broker.QueueOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err = brk.Subscribe(q, a2a.AgentRequestTopic("test-ns", "TestAgent")); err != nil {
		t.Fatal(err)
	}
	return q
}

type sentActivity struct{ Path, Text string }

func captureSends(t *testing.T, a *TeamsAdapter) func() []sentActivity {
	t.Helper()
	var mu sync.Mutex
	var sent []sentActivity
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.URL.Path, "oauth2") {
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "tok", "token_type": "Bearer", "expires_in": 3600})
			return
		}
		var activity map[string]any
		_ = json.NewDecoder(r.Body).Decode(&activity)
		text, _ := activity["text"].(string)
		mu.Lock()
		sent = append(sent, sentActivity{Path: r.URL.Path, Text: text})
		mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "activity-1"})
	}))
	t.Cleanup(srv.Close)
	a.botClient.apiBaseURL = srv.URL
	return func() []sentActivity {
		mu.Lock()
		defer mu.Unlock()
		return append([]sentActivity(nil), sent...)
	}
}

func TestTeamsHILClickOutsideWhereTheTaskWasAskedIsRefusedWithoutSideEffects(t *testing.T) {
	a, brk, _ := newHILTeamsAdapter(t)
	sends := captureSends(t, a)
	const taskID = "task-owned"
	askedTask(t, a, taskID, "owner@dev.local")
	q := subscribeToAgent(t, brk, "teams-owner-refused")

	for _, tc := range []struct {
		name         string
		conversation string
		convType     string
		user         string
	}{
		{"a forged task id in the clicker's own personal chat", "conv-other-user", ConversationTypePersonal, "29:other"},
		{"another user in the conversation the task was asked in", askedConversation, ConversationTypePersonal, "29:other"},
		{"the asker from a conversation the task was not asked in", "conv-elsewhere", ConversationTypePersonal, askerTeamsID},
		{"another user in a group chat", "conv-group", "groupChat", "29:other"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sentBefore := len(sends())

			a.handleHILSubmit(clickActivity(t, a, "req-owned", taskID, tc.conversation, tc.convType, tc.user))

			if msg := receiveWithin(t, brk, q, 200*time.Millisecond); msg != nil {
				t.Fatalf("the click was forwarded to the agent: %s", msg.Payload)
			}
			notices := sends()[sentBefore:]
			if len(notices) != 1 || notices[0].Text != msgHILNotYours || !strings.Contains(notices[0].Path, "/conversations/"+tc.conversation+"/") {
				t.Errorf("notices after the refused click = %+v, want exactly one saying %q in conversation %s", notices, msgHILNotYours, tc.conversation)
			}
		})
	}

	a.handleHILSubmit(clickActivity(t, a, "req-owned", taskID, askedConversation, ConversationTypePersonal, askerTeamsID))

	msg := receiveWithin(t, brk, q, 2*time.Second)
	if msg == nil {
		t.Fatal("the asker's answer after the refused clicks was dropped: a refusal took the dedup")
	}
	if got := a2a.DecodeUserProperties(msg.UserProperties).UserID; got != "owner@dev.local" {
		t.Errorf("answer attributed to %q, want the task's user", got)
	}
}

func TestTeamsHILClickFromWhereTheTaskWasAskedAnswersAsTheAskerWhateverTheLookupReturned(t *testing.T) {
	a, brk, _ := newHILTeamsAdapter(t)
	const taskID = "task-1to1"
	askedTask(t, a, taskID, "teams:conversation-user")
	q := subscribeToAgent(t, brk, "teams-owner-ok")

	a.handleHILSubmit(clickActivity(t, a, "req-1to1", taskID, askedConversation, ConversationTypePersonal, askerTeamsID))

	msg := receiveWithin(t, brk, q, 2*time.Second)
	if msg == nil {
		t.Fatal("the asker's own click was not forwarded")
	}
	if got := a2a.DecodeUserProperties(msg.UserProperties).UserID; got != "teams:conversation-user" {
		t.Errorf("answer attributed to %q, want the task's user", got)
	}
}

func TestTeamsHILSubmitFailureReleasesTheDedup(t *testing.T) {
	a, brk, _ := newHILTeamsAdapter(t)
	const taskID = "task-fail"
	askedTask(t, a, taskID, "owner@dev.local")
	if err := brk.Disconnect(t.Context()); err != nil {
		t.Fatal(err)
	}

	a.handleHILSubmit(clickActivity(t, a, "req-fail", taskID, askedConversation, ConversationTypePersonal, askerTeamsID))

	if !a.hilDedup.SetIfAbsent("req-fail", struct{}{}, runtime.WithTTL(time.Minute)) {
		t.Error("a failed submit left the dedup held: the asker's retry would be dropped as a duplicate")
	}
}

func subscribeToNotifyReplies(t *testing.T, brk *broker.DevBroker, name string) broker.Queue {
	t.Helper()
	q, err := brk.CreateQueue(name, broker.QueueOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err = brk.Subscribe(q, a2a.NotifyReplySubscription("test-ns")); err != nil {
		t.Fatal(err)
	}
	return q
}

func TestTeamsHILCardWithAForgedNotifyRoutingIsRefused(t *testing.T) {
	a, brk, _ := newHILTeamsAdapter(t)
	const taskID = "task-live"
	askedTask(t, a, taskID, "owner@dev.local")
	agentQ := subscribeToAgent(t, brk, "teams-forged-agent")
	notifyQ := subscribeToNotifyReplies(t, brk, "teams-forged-notify")

	forge := func(requestID, taskIDInCard string, notifyInCard bool) *Activity {
		card, _ := a.buildHILCard(parseTeamsSurfaceT(t), requestID, taskID, "TestAgent", false)
		data := submitActionData(t, card, HILSubmitAction)
		data["hil_0"] = "o0"
		data["taskId"], data["notify"] = taskIDInCard, notifyInCard
		value, err := json.Marshal(data)
		if err != nil {
			t.Fatal(err)
		}
		return &Activity{
			Type: ActivityTypeInvoke, Value: value, From: Account{ID: "29:other"},
			Conversation: Conversation{ID: "conv-other", ConversationType: ConversationTypePersonal},
		}
	}
	for _, tc := range []struct {
		name      string
		requestID string
		forged    *Activity
	}{
		{"notify flag on a card naming a live task", "req-forged-notify", forge("req-forged-notify", taskID, true)},
		{"no task on a card that claims not to notify", "req-forged-notask", forge("req-forged-notask", "", false)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a.handleHILSubmit(tc.forged)

			if msg := receiveWithin(t, brk, agentQ, 200*time.Millisecond); msg != nil {
				t.Errorf("the click reached the agent: %s", msg.Payload)
			}
			if msg := receiveWithin(t, brk, notifyQ, 200*time.Millisecond); msg != nil {
				t.Errorf("the click reached the notification service as a reply: %s", msg.Payload)
			}

			a.handleHILSubmit(clickActivity(t, a, tc.requestID, taskID, askedConversation, ConversationTypePersonal, askerTeamsID))
			if receiveWithin(t, brk, agentQ, 2*time.Second) == nil {
				t.Error("the asker's answer after the forged click was dropped: the refusal took the dedup")
			}
		})
	}
}

func TestTeamsHILProactiveCardStillRoutesToTheNotificationService(t *testing.T) {
	a, brk, _ := newHILTeamsAdapter(t)
	notifyQ := subscribeToNotifyReplies(t, brk, "teams-proactive-notify")
	card, _ := a.buildHILCard(parseTeamsSurfaceT(t), "req-proactive", "", "", true)
	data := submitActionData(t, card, HILSubmitAction)
	data["hil_0"] = "o0"
	value, err := json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}

	a.handleHILSubmit(&Activity{Type: ActivityTypeInvoke, Value: value, From: Account{ID: askerTeamsID}, Conversation: Conversation{ID: "dm-conv-1"}})

	if msg := receiveWithin(t, brk, notifyQ, 2*time.Second); msg == nil {
		t.Fatal("a proactive card's reply did not reach the notification service")
	}
}

func TestTeamsHILClickResolvesTheClickersIdentityOnce(t *testing.T) {
	a, brk, _ := newHILTeamsAdapter(t)
	var lookups atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(r.URL.Path, "oauth2"):
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "tok", "token_type": "Bearer", "expires_in": 3600})
		case strings.Contains(r.URL.Path, "/members/"):
			lookups.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]any{"id": askerTeamsID, "email": "asker@dev.local"})
		default:
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "activity-1"})
		}
	}))
	t.Cleanup(srv.Close)
	a.botClient.apiBaseURL = srv.URL
	a.emailCache = NewEmailCache("tenant-1", 0, a.botClient, a.logger)
	const taskID = "task-once"
	askedTask(t, a, taskID, "owner@dev.local")
	q := subscribeToAgent(t, brk, "teams-lookup-once")
	activity := clickActivity(t, a, "req-once", taskID, askedConversation, ConversationTypePersonal, askerTeamsID)
	activity.ServiceURL = srv.URL

	a.handleHILSubmit(activity)

	if receiveWithin(t, brk, q, 2*time.Second) == nil {
		t.Fatal("the asker's click was not forwarded")
	}
	if got := lookups.Load(); got != 1 {
		t.Errorf("identity lookups per click = %d, want 1", got)
	}
}

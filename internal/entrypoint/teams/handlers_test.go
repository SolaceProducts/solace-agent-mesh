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
	"sync/atomic"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
)

func TestHandleMessageActivity_MentionRequirement(t *testing.T) {
	cases := []struct {
		name   string
		convoT string
	}{
		{name: "channel without mention is dropped", convoT: ConversationTypeChannel},
		{name: "group chat without mention is dropped", convoT: ConversationTypeGroupChat},
		{name: "unknown conversation type is dropped", convoT: "unknown_type"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := newTestAdapter(t)
			a.core = nil
			a.botClient = nil
			a.emailCache = nil

			activity := &Activity{
				Type: ActivityTypeMessage,
				ID:   "msg-no-mention",
				From: Account{ID: "user-1"},
				Conversation: Conversation{
					ID:               "conv-1",
					ConversationType: tc.convoT,
				},
				Text: "hello bot, no @mention here",
			}

			a.handleMessageActivity(context.Background(), activity)
		})
	}
}

func TestHandleAdaptiveCardAction_CancelWithoutTaskID_NoCoreCall(t *testing.T) {
	a := newTestAdapter(t)
	a.core = nil

	value, err := json.Marshal(AdaptiveCardActionData{Action: "a2a_cancel_request_action"})
	if err != nil {
		t.Fatalf("marshal value: %v", err)
	}
	activity := &Activity{
		Type:  ActivityTypeInvoke,
		Name:  "adaptiveCard/action",
		Value: value,
	}

	a.handleAdaptiveCardAction(activity)
}

func TestHandleAdaptiveCardAction_UnknownAction_NoCoreCall(t *testing.T) {
	a := newTestAdapter(t)
	a.core = nil

	value, _ := json.Marshal(AdaptiveCardActionData{Action: "totally-bogus-action"})
	activity := &Activity{Type: ActivityTypeInvoke, Name: "adaptiveCard/action", Value: value}

	a.handleAdaptiveCardAction(activity)
}

func TestReplyInputValidationError_SwallowsSendActivityFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/botframework.com/oauth2/v2.0/token" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
				"access_token": "tok", "token_type": "Bearer", "expires_in": 3600,
			})
			return
		}
		w.WriteHeader(http.StatusBadRequest)
	}))
	t.Cleanup(srv.Close)

	a := newTestAdapter(t)
	a.botClient = NewBotClient(a.cfg.AppID, a.cfg.AppPassword, a.cfg.TenantID, a.logger)
	a.botClient.apiBaseURL = srv.URL
	a.ctx = context.Background()

	activity := &Activity{
		ID:           "user-msg-1",
		ServiceURL:   srv.URL,
		From:         Account{ID: "user-1"},
		Conversation: Conversation{ID: "conv-1"},
	}

	a.replyInputValidationError(activity, &InputValidationError{Message: "boom"})
}

func TestHandleFileConsentResponse_RoutesByAction(t *testing.T) {
	cases := []struct {
		name           string
		action         string
		wantUploadHits int32
	}{
		{name: "accept routes to upload path", action: "accept", wantUploadHits: 1},
		{name: "decline skips upload path", action: "decline", wantUploadHits: 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var posts []map[string]any
			var postCount int32
			botSrv := fakeBotServer(t, &posts, &postCount)

			var uploadHits int32
			uploadSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				atomic.AddInt32(&uploadHits, 1)
				w.WriteHeader(http.StatusOK)
			}))
			t.Cleanup(uploadSrv.Close)

			a := newTestAdapter(t)
			a.botClient = NewBotClient(a.cfg.AppID, a.cfg.AppPassword, a.cfg.TenantID, a.logger)
			a.botClient.apiBaseURL = botSrv.URL
			a.ctx = context.Background()
			a.uploads = NewUploadManager(a.logger)

			taskID := "task-route"
			filename := "report.pdf"
			a.uploads.addPendingInMemory(pendingKey(taskID, filename), filename, []byte("file-bytes"), "application/pdf", taskID)

			contextJSON, _ := json.Marshal(map[string]any{"filename": filename, "task_id": taskID})
			value, _ := json.Marshal(map[string]any{
				"action":     tc.action,
				"context":    json.RawMessage(contextJSON),
				"uploadInfo": map[string]any{"uploadUrl": uploadSrv.URL, "uniqueId": "u-1", "fileType": "pdf"},
			})
			activity := &Activity{
				Type:         ActivityTypeInvoke,
				Name:         InvokeFileConsent,
				ID:           "invoke-1",
				ServiceURL:   botSrv.URL,
				From:         Account{ID: "user-1"},
				Conversation: Conversation{ID: "conv-1"},
				Value:        value,
			}

			a.core = entrypoint.NewGatewayCore(a, map[string]any{})
			a.handleFileConsentResponse(t.Context(), activity)
			a.server.Wait()

			if got := atomic.LoadInt32(&uploadHits); got != tc.wantUploadHits {
				t.Errorf("upload PUT hits = %d, want %d", got, tc.wantUploadHits)
			}
			if p := a.uploads.takePending(pendingKey(taskID, filename)); p != nil {
				t.Errorf("pending upload still present after %s — should have been consumed", tc.action)
			}
		})
	}
}

func TestHandleMessageActivity_MentionUsesRecipientID(t *testing.T) {
	const agentName = "TestAgent"
	a, brk, q := newRunAsTeamsFixture(t, agentName)

	teamsBotID := "28:" + a.cfg.AppID
	botMention := "<at>TestBot</at>"
	otherMention := "<at>OtherUser</at>"
	activity := teamsActivity(ConversationTypeChannel)
	activity.Recipient = Account{ID: teamsBotID}
	activity.Entities = []Entity{
		{Type: "mention", Mentioned: &Account{ID: teamsBotID}, Text: botMention},
		{Type: "mention", Mentioned: &Account{ID: "29:other-user"}, Text: otherMention},
	}
	activity.Text = botMention + " hey " + otherMention + " what's in my DMs?"

	a.handleMessageActivity(context.Background(), activity)

	_, msg := recvTeamsMessage(t, brk, q)
	want := "hey " + otherMention + " what's in my DMs?"
	if len(msg.Parts) == 0 || msg.Parts[0].Text != want {
		t.Errorf("submitted parts = %+v, want bot mention stripped and other mention preserved: %q", msg.Parts, want)
	}
}

func TestHandleMessageActivity_OtherUserMentionIsDropped(t *testing.T) {
	a := newTestAdapter(t)
	a.core = nil
	a.botClient = nil
	a.emailCache = nil

	activity := &Activity{
		Type:         ActivityTypeMessage,
		ID:           "msg-other-mention",
		From:         Account{ID: "user-1"},
		Conversation: Conversation{ID: "conv-1", ConversationType: ConversationTypeChannel},
		Recipient:    Account{ID: "28:" + a.cfg.AppID},
		Entities:     []Entity{{Type: "mention", Mentioned: &Account{ID: "29:someone-else"}, Text: "<at>OtherUser</at>"}},
		Text:         "<at>OtherUser</at> hey, is the bot around?",
	}

	a.handleMessageActivity(context.Background(), activity)
}

func TestHandleMessageActivity_EmptyRecipientIDIsDropped(t *testing.T) {
	a := newTestAdapter(t)
	a.core = nil
	a.botClient = nil
	a.emailCache = nil

	activity := &Activity{
		Type:         ActivityTypeMessage,
		ID:           "msg-no-recipient",
		From:         Account{ID: "user-1"},
		Conversation: Conversation{ID: "conv-1", ConversationType: ConversationTypeChannel},
		Entities:     []Entity{{Type: "mention", Mentioned: &Account{ID: ""}, Text: "<at></at>"}},
		Text:         "<at></at> hello",
	}

	a.handleMessageActivity(context.Background(), activity)
}

func TestIsBotMentioned(t *testing.T) {
	botID := "bot-123"

	tests := []struct {
		name     string
		activity *Activity
		want     bool
	}{
		{
			name: "mentioned",
			activity: &Activity{
				Entities: []Entity{
					{Type: "mention", Mentioned: &Account{ID: botID}},
				},
			},
			want: true,
		},
		{
			name: "not mentioned",
			activity: &Activity{
				Entities: []Entity{
					{Type: "mention", Mentioned: &Account{ID: "other"}},
				},
			},
			want: false,
		},
		{
			name:     "no entities",
			activity: &Activity{},
			want:     false,
		},
		{
			name: "non-mention entity",
			activity: &Activity{
				Entities: []Entity{
					{Type: "clientInfo"},
				},
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isBotMentioned(tt.activity, botID)
			if got != tt.want {
				t.Errorf("isBotMentioned() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestTruncate(t *testing.T) {
	if got := truncate("hello", 10); got != "hello" {
		t.Errorf("truncate short = %q", got)
	}
	if got := truncate("hello world", 5); got != "hello..." {
		t.Errorf("truncate long = %q", got)
	}
}

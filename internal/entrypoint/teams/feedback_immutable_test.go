// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package teams

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
)

func TestB10_TeamsFeedbackRatingThenDetailsOnce(t *testing.T) {
	a := storeBackedTeamsAdapter(t)
	a.core.Config().FeedbackPublishingRaw = map[string]any{"enabled": true}
	brk, q := subscribeFeedback(t, a.core, "test-teams-feedback-b10")
	taskID := "11111111-1111-1111-1111-1111111111b1"
	seedTeamsTask(t, a, taskID, "OrchestratorAgent")

	act := &Activity{}
	act.From.ID = "29:abc"
	act.From.AadObjectID = "aad-123"
	fbCtx := feedbackContext{TaskID: taskID, SessionID: "22222222-2222-2222-2222-2222222222b1"}
	submit := func(rating, comment string) feedbackSubmitData {
		return feedbackSubmitData{Rating: rating, Comment: comment}
	}

	for _, st := range []struct {
		name      string
		data      feedbackSubmitData
		wantErr   error
		wantEvent string
	}{
		{"B10_first_rating_accepted", submit("down", ""), nil, "created"},
		{"B10_identical_rating_redelivery_idempotent", submit("down", ""), nil, ""},
		{"B10_details_added_once", submit("down", "too vague"), nil, "updated"},
		{"B10_identical_details_redelivery_idempotent", submit("down", "too vague"), nil, ""},
		{"B10_changed_comment_conflicts", submit("down", "different"), store.ErrFeedbackConflict, ""},
		{"B10_changed_rating_conflicts", submit("up", ""), store.ErrFeedbackConflict, ""},
	} {
		t.Run(st.name, func(t *testing.T) {
			if _, err := a.recordFeedback(t.Context(), act, fbCtx, st.data); !errors.Is(err, st.wantErr) {
				t.Fatalf("err = %v, want %v", err, st.wantErr)
			}
			if st.wantEvent == "" {
				assertNoFeedbackEvent(t, brk, q)
				return
			}
			if got := receiveFeedbackEvent(t, brk, q)["event"]; got != st.wantEvent {
				t.Fatalf("event = %v, want %s", got, st.wantEvent)
			}
		})
	}

	fbs, err := store.NewFeedbackRepo(a.core.Store()).Search(t.Context(), "*", 0, 0, taskID, "", "", 10, 0)
	if err != nil || len(fbs) != 1 {
		t.Fatalf("search: %v rows=%d", err, len(fbs))
	}
	if got := fbs[0]; got.Rating != "down" || got.Comment == nil || *got.Comment != "too vague" || got.ShareContent {
		t.Fatalf("stored feedback = (%s, %v, %v), want the first valid submission with no consent",
			got.Rating, got.Comment, got.ShareContent)
	}
}

func TestB10_TeamsConflictingSubmitSendsNoticeAndKeepsTheStoredRating(t *testing.T) {
	type call struct {
		method string
		text   string
	}
	var (
		mu    sync.Mutex
		calls []call
	)
	mux := http.NewServeMux()
	mux.HandleFunc("/botframework.com/oauth2/v2.0/token", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "t", "token_type": "Bearer", "expires_in": 3600})
	})
	mux.HandleFunc("/v3/conversations/", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var b map[string]any
		_ = json.Unmarshal(body, &b)
		text, _ := b["text"].(string)
		mu.Lock()
		calls = append(calls, call{method: r.Method, text: text})
		mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]string{"id": "a1"})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	a := storeBackedTeamsAdapter(t)
	a.botClient.apiBaseURL = srv.URL
	taskID := "11111111-1111-1111-1111-1111111111b2"
	seedTeamsTask(t, a, taskID, "OrchestratorAgent")
	a.registerFeedback(taskID, &TeamsTaskMeta{
		SessionID: "22222222-2222-2222-2222-2222222222b2", ConversationID: "conv1",
	}, "answer-msg-1", "the answer")

	activity := func(rating string) *Activity {
		act := &Activity{
			Type:         ActivityTypeMessage,
			ServiceURL:   srv.URL,
			ReplyToID:    "answer-msg-1",
			Conversation: Conversation{ID: "conv1"},
			Value:        mustJSON(t, map[string]any{"rating": rating}),
		}
		act.From.ID = "29:user"
		act.From.AadObjectID = "aad-1"
		return act
	}

	a.handleFeedbackSubmit(activity("up"), false)
	mu.Lock()
	calls = nil
	mu.Unlock()

	a.handleFeedbackSubmit(activity("down"), false)

	mu.Lock()
	defer mu.Unlock()
	var posts []call
	for _, c := range calls {
		if c.method == http.MethodPost {
			posts = append(posts, c)
		}
	}
	if len(posts) != 1 || posts[0].text != entrypoint.FeedbackAlreadyRecordedNotice {
		t.Fatalf("conflicting submit posted %+v, want exactly the already-recorded notice", posts)
	}
	for _, c := range calls {
		if strings.Contains(c.text, entrypoint.FeedbackAckNotice) {
			t.Errorf("a thank-you was sent alongside the conflict notice: %+v", c)
		}
	}
	fbs, err := store.NewFeedbackRepo(a.core.Store()).Search(t.Context(), "*", 0, 0, taskID, "", "", 10, 0)
	if err != nil || len(fbs) != 1 || fbs[0].Rating != "up" {
		t.Fatalf("stored feedback = %+v (err %v), want the first rating kept", fbs, err)
	}
}

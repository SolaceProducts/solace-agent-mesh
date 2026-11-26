// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

func (env *feedbackTestEnv) postFeedback(t *testing.T, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	if sessionID, ok := body["sessionId"].(string); ok {
		env.createSessionIfValid(t, sessionID)
	}
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	env.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/feedback", bytes.NewReader(data)))
	return rec
}

func assertNoFeedbackPublished(t *testing.T, brk broker.Broker, q broker.Queue) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()
	if msg, err := brk.Receive(ctx, q); err == nil && msg != nil {
		t.Fatalf("unexpected feedback event published: %s", msg.Payload)
	}
}

func TestB10_HTTPFeedbackIsImmutableOnceSubmitted(t *testing.T) {
	env := setupFeedbackTest(t, true)
	q := env.subscribeFeedback(t, "b10-http-feedback")
	taskID, sessID := idgen.New(), idgen.New()
	env.seedTask(t, taskID, "sam_dev_user")

	body := func(rating string, extra map[string]any) map[string]any {
		b := map[string]any{"taskId": taskID, "sessionId": sessID, "feedbackType": rating}
		for k, v := range extra {
			b[k] = v
		}
		return b
	}
	details := map[string]any{"feedbackText": "wrong doc", "shareTaskContent": true}

	for _, st := range []struct {
		name      string
		body      map[string]any
		wantCode  int
		wantEvent string
	}{
		{"B10_first_rating_accepted", body("down", nil), http.StatusOK, "created"},
		{"B10_identical_rating_redelivery_idempotent", body("down", nil), http.StatusOK, ""},
		{"B10_details_added_once", body("down", details), http.StatusOK, "updated"},
		{"B10_identical_details_redelivery_idempotent", body("down", details), http.StatusOK, ""},
		{"B10_changed_comment_conflicts", body("down", map[string]any{"feedbackText": "other", "shareTaskContent": true}), http.StatusConflict, ""},
		{"B10_toggled_share_is_not_a_change", body("down", map[string]any{"feedbackText": "wrong doc", "shareTaskContent": false}), http.StatusOK, ""},
		{"B10_changed_rating_conflicts", body("up", nil), http.StatusConflict, ""},
	} {
		t.Run(st.name, func(t *testing.T) {
			rec := env.postFeedback(t, st.body)
			if rec.Code != st.wantCode {
				t.Fatalf("status = %d, want %d: %s", rec.Code, st.wantCode, rec.Body.String())
			}
			if st.wantCode == http.StatusConflict {
				var errResp ErrorResponse
				if err := json.Unmarshal(rec.Body.Bytes(), &errResp); err != nil {
					t.Fatalf("decode error envelope: %v", err)
				}
				if errResp.Message == "" || errResp.ErrorID == "" {
					t.Fatalf("409 body %s lacks the {message, errorId} envelope", rec.Body.String())
				}
			}
			if st.wantEvent == "" {
				assertNoFeedbackPublished(t, env.brk, q)
				return
			}
			if got := receiveFeedbackEvent(t, env.brk, q)["event"]; got != st.wantEvent {
				t.Fatalf("published event = %v, want %s", got, st.wantEvent)
			}
		})
	}

	rows, err := store.NewFeedbackRepo(env.adapter.core.Store()).Search(t.Context(), "*", 0, 0, taskID, "", "", 10, 0)
	if err != nil || len(rows) != 1 {
		t.Fatalf("search: %v rows=%d", err, len(rows))
	}
	got := rows[0]
	if got.Rating != "down" || got.Comment == nil || *got.Comment != "wrong doc" || got.ShareContent {
		t.Fatalf("stored feedback = (%s, %v, %v), want the first valid submission with consent unset", got.Rating, got.Comment, got.ShareContent)
	}
}

func TestB10_HTTPFeedbackIdempotentResponseKeepsStoredID(t *testing.T) {
	env := setupFeedbackTest(t, false)
	taskID, sessID := idgen.New(), idgen.New()
	env.seedTask(t, taskID, "sam_dev_user")
	body := map[string]any{"taskId": taskID, "sessionId": sessID, "feedbackType": "up"}

	ids := make([]any, 0, 2)
	for range 2 {
		rec := env.postFeedback(t, body)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
		}
		var resp map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, resp["id"])
	}
	if ids[0] == nil || ids[0] != ids[1] {
		t.Fatalf("redelivery returned id %v, first submission %v", ids[1], ids[0])
	}
}

// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package slack

import (
	"net/url"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
)

func TestB10_SlackModalResubmitWithChangedCommentConflicts(t *testing.T) {
	a, calls := storeBackedFeedbackAdapterWithCalls(t)
	taskID := "11111111-1111-1111-1111-111111111111"
	seedFeedbackTask(t, a.core, taskID, "OrchestratorAgent")

	a.handleNativeFeedbackSubmit(submitCallback(t, taskID, "first reason"), func() {})
	a.wg.Wait()
	a.handleNativeFeedbackSubmit(submitCallback(t, taskID, "first reason"), func() {})
	a.wg.Wait()
	if got := calls.count("chat.postEphemeral"); got != 0 {
		t.Fatalf("identical resubmit posted %d notices, want 0", got)
	}
	acks := calls.count("chat.update")

	a.handleNativeFeedbackSubmit(submitCallback(t, taskID, "second reason"), func() {})
	a.wg.Wait()

	if got := onlyFeedbackRow(t, a, taskID); got.Comment == nil || *got.Comment != "first reason" {
		t.Errorf("comment = %v, want the first submission's", got.Comment)
	}
	if got := calls.count("chat.postEphemeral"); got != 1 {
		t.Fatalf("chat.postEphemeral called %d times, want 1 conflict notice", got)
	}
	if body := calls.lastBody("chat.postEphemeral"); !strings.Contains(body, url.QueryEscape(entrypoint.FeedbackAlreadyRecordedNotice)) {
		t.Errorf("notice body = %q, want the already-recorded notice", body)
	}
	if got := calls.count("chat.update"); got != acks+1 {
		t.Errorf("chat.update calls %d -> %d, want the conflict to acknowledge once more", acks, got)
	}
	if body := calls.lastBody("chat.update"); !strings.Contains(body, url.QueryEscape(feedbackAckText(feedbackRatingDown))) {
		t.Errorf("acknowledgement = %q, want the stored down rating", body)
	}
}

func TestB10_SlackConflictingSubmitAcknowledgesTheStoredRating(t *testing.T) {
	a, calls := storeBackedFeedbackAdapterWithCalls(t)
	taskID := "11111111-1111-1111-1111-111111111111"
	seedFeedbackTask(t, a.core, taskID, "OrchestratorAgent")

	a.handleNativeFeedbackSubmit(submitCallbackRated(t, taskID, "", feedbackRatingUp), func() {})
	a.wg.Wait()
	acks := calls.count("chat.update")

	a.handleNativeFeedbackSubmit(submitCallbackRated(t, taskID, "changed my mind", feedbackRatingDown), func() {})
	a.wg.Wait()

	if got := onlyFeedbackRow(t, a, taskID); got.Rating != feedbackRatingUp || got.Comment != nil {
		t.Fatalf("stored feedback = (%s, %v), want the first up rating with no comment", got.Rating, got.Comment)
	}
	if body := calls.lastBody("chat.postEphemeral"); !strings.Contains(body, url.QueryEscape(entrypoint.FeedbackAlreadyRecordedNotice)) {
		t.Errorf("notice body = %q, want the already-recorded notice", body)
	}
	upAck, downAck := url.QueryEscape(feedbackAckText(feedbackRatingUp)), url.QueryEscape(feedbackAckText(feedbackRatingDown))
	bodies := calls.allBodies("chat.update")[acks:]
	if len(bodies) != 1 {
		t.Fatalf("chat.update calls after the conflicting submit = %d, want 1: an empty loop would pass vacuously", len(bodies))
	}
	for _, body := range bodies {
		if !strings.Contains(body, upAck) || strings.Contains(body, downAck) {
			t.Errorf("acknowledgement = %q, want the stored up rating only", body)
		}
	}
}

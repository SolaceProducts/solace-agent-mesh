// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package whatsapp

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
)

func subscribeWhatsAppFeedback(t *testing.T, a *Adapter, name string) (broker.Broker, broker.Queue) {
	t.Helper()
	a.core.Config().FeedbackPublishingRaw = map[string]any{"enabled": true}
	brk := a.core.Services().Broker()
	q, err := brk.CreateQueue(name, broker.QueueOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := brk.Subscribe(q, a2a.FeedbackTopic(a.core.Config().Namespace)); err != nil {
		t.Fatal(err)
	}
	return brk, q
}

func nextWhatsAppFeedbackEvent(t *testing.T, brk broker.Broker, q broker.Queue, wait time.Duration) (string, bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), wait)
	defer cancel()
	msg, err := brk.Receive(ctx, q)
	if err != nil || msg == nil {
		return "", false
	}
	var payload struct {
		Feedback struct {
			Event string `json:"event"`
		} `json:"feedback"`
	}
	if err := json.Unmarshal(msg.Payload, &payload); err != nil {
		t.Fatalf("decode feedback event: %v", err)
	}
	return payload.Feedback.Event, true
}

func TestB10_WhatsAppFeedbackRatingThenReasonOnce(t *testing.T) {
	t.Parallel()
	mock := newWAMock(t)
	a := newFeedbackRig(t, mock, "Builder")
	metrics := newCountingMetrics()
	a.metrics = metrics
	brk, q := subscribeWhatsAppFeedback(t, a, "b10-whatsapp-feedback")
	const phone = "15551230090"
	taskID, row := seedRatableTask(t, a, phone, "wamid.OUT-B10")
	seedReasonCard(t, a, row, "wamid.CARD-B10")

	for _, st := range []struct {
		name      string
		msg       *inboundMessage
		wantEvent string
	}{
		{"B10_first_rating_accepted", reactionMsg(phone, "wamid.B10-1", "wamid.OUT-B10", "👎"), "created"},
		{"B10_identical_rating_redelivery_idempotent", reactionMsg(phone, "wamid.B10-2", "wamid.OUT-B10", "👎"), ""},
		{"B10_reason_added_once", listTap(phone, "wamid.B10-3", encodeFeedbackRowID("confusing", taskID), "wamid.CARD-B10"), "updated"},
		{"B10_identical_reason_redelivery_idempotent", listTap(phone, "wamid.B10-4", encodeFeedbackRowID("confusing", taskID), "wamid.CARD-B10"), ""},
		{"B10_changed_reason_conflicts", listTap(phone, "wamid.B10-5", encodeFeedbackRowID("inaccurate", taskID), "wamid.CARD-B10"), ""},
		{"B10_changed_rating_conflicts", reactionMsg(phone, "wamid.B10-6", "wamid.OUT-B10", "👍"), ""},
	} {
		t.Run(st.name, func(t *testing.T) {
			a.deliver(&webhookChangeValue{}, st.msg)
			event, published := nextWhatsAppFeedbackEvent(t, brk, q, 300*time.Millisecond)
			switch {
			case st.wantEvent == "" && published:
				t.Fatalf("unexpected %q feedback event", event)
			case st.wantEvent != "" && event != st.wantEvent:
				t.Fatalf("event = %q (published %v), want %q", event, published, st.wantEvent)
			}
		})
	}

	rows := feedbackRows(t, a, taskID)
	if len(rows) != 1 || rows[0].Rating != "down" || rows[0].Comment == nil || *rows[0].Comment != "Confusing" {
		t.Fatalf("rows = %+v, want down with the first reason", rows)
	}
	replies := replyBodies(t, a, mock)
	var notices int
	for _, r := range replies {
		if r == feedbackConflictNotice() {
			notices++
		}
	}
	if notices != 2 {
		t.Fatalf("replies = %v, want exactly two already-recorded notices (one per conflict)", replies)
	}
	for outcome, want := range map[string]int64{
		fbOutcomeDown: 1, fbOutcomeReason: 1, fbOutcomeUnchanged: 2, fbOutcomeConflict: 2,
	} {
		if got := metrics.outcomeCount(metricWhatsAppFeedback, outcome); got != want {
			t.Errorf("%s outcome counted %d times, want %d: a restated rating or reason is not a new one", outcome, got, want)
		}
	}
}

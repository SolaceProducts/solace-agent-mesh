// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package whatsapp

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

func newFeedbackRig(t *testing.T, mock *waMock, defaultAgent string, logger ...*slog.Logger) *Adapter {
	t.Helper()
	a := newAdapterRig(t, mock, defaultAgent, logger...)
	a.cfg.FeedbackEnabled = true
	return a
}

func outboundRowsOfKind(t *testing.T, a *Adapter, taskID, kind string) []*store.WhatsAppOutbound {
	t.Helper()
	rows, err := dbutil.QueryRowPtrs[store.WhatsAppOutbound](context.Background(), a.core.Store(),
		`SELECT * FROM whatsapp_outbound WHERE gateway_id = ? AND task_id = ? AND kind = ?`,
		a.gatewayID(), taskID, kind)
	if err != nil {
		t.Fatalf("query outbound rows: %v", err)
	}
	return rows
}

func TestClassifyReaction(t *testing.T) {
	t.Parallel()
	cases := []struct {
		emoji  string
		rating string
		ok     bool
	}{
		{"👍", "up", true},
		{"👍🏽", "up", true},
		{"👍\ufe0f", "up", true},
		{"👎", "down", true},
		{"👎🏿", "down", true},
		{"❤️", "", false},
		{"🙏", "", false},
		{"😂", "", false},
		{"🔥", "", false},
		{"👏", "", false},
		{"😢", "", false},
		{"😡", "", false},
		{"💩", "", false},
		{"😮", "", false},
		{"🤔", "", false},
		{"", "", false},
		{"not an emoji", "", false},
	}
	for _, c := range cases {
		rating, ok := classifyReaction(c.emoji)
		if rating != c.rating || ok != c.ok {
			t.Errorf("classifyReaction(%q) = (%q, %v), want (%q, %v)", c.emoji, rating, ok, c.rating, c.ok)
		}
	}
}

func TestFeedbackRowID_RoundTrip(t *testing.T) {
	t.Parallel()
	const task = "01a0b0fa-c8d3-7505-b718-2b42ea9edfb9"
	for _, r := range feedbackReasons {
		id := encodeFeedbackRowID(r.Key, task)
		if len(id) > 200 {
			t.Fatalf("row id %q exceeds WhatsApp's 200-char limit", id)
		}
		reason, gotTask, ok := decodeFeedbackRowID(id)
		if !ok || reason != r.Key || gotTask != task {
			t.Fatalf("decode(%q) = (%q,%q,%v)", id, reason, gotTask, ok)
		}
		if title, ok := feedbackReasonTitle(reason); !ok || title != r.Title {
			t.Fatalf("title(%q) = (%q,%v), want %q", reason, title, ok, r.Title)
		}
	}
}

func TestFeedbackRowID_RejectsForeignIDs(t *testing.T) {
	t.Parallel()
	for _, id := range []string{
		"opt:0",
		"fb:",
		"fb:inaccurate",
		"fb:inaccurate:not-a-uuid",
		"fb:bogus:01a0b0fa-c8d3-7505-b718-2b42ea9edfb9",
		"FB:inaccurate:01a0b0fa-c8d3-7505-b718-2b42ea9edfb9",
		"",
	} {
		if _, _, ok := decodeFeedbackRowID(id); ok {
			t.Errorf("decodeFeedbackRowID(%q) accepted a foreign id", id)
		}
	}
}

func TestFeedbackCopy_WithinWhatsAppLimits(t *testing.T) {
	t.Parallel()
	if n := len([]rune(msgFeedbackCardFooter)); n > 60 {
		t.Errorf("footer is %d chars, WhatsApp caps footers at 60", n)
	}
	if n := len([]rune(msgFeedbackCardBody)); n > 1024 {
		t.Errorf("body is %d chars, cap is 1024", n)
	}
	if n := len([]rune(msgFeedbackCardButton)); n > 20 {
		t.Errorf("list button text is %d chars, cap is 20", n)
	}
	for _, r := range feedbackReasons {
		if n := len([]rune(r.Title)); n > 24 {
			t.Errorf("row title %q is %d chars, cap is 24", r.Title, n)
		}
	}
	if msgFeedbackCardFooter != "This conversation may be viewed to help improve responses" {
		t.Errorf("footer copy changed; it is the only user-facing consent text and was approved verbatim")
	}
	if msgFeedbackWithdraw != "Remove your reaction to withdraw this feedback" {
		t.Errorf("withdraw line changed; it is the only place a user learns the rating is reversible")
	}
	if !strings.Contains(msgFeedbackCardBody, msgFeedbackWithdraw) {
		t.Errorf("card body must carry the withdraw line: the footer is capped at 60 chars and already holds the notice")
	}
}

func TestFeedbackAcknowledgements_CarryTheNoticeAndTheWithdrawLine(t *testing.T) {
	t.Parallel()
	for _, lead := range []string{msgFeedbackThanks, msgFeedbackNoted} {
		ack := feedbackAck(lead)
		if !strings.HasPrefix(ack, lead) {
			t.Errorf("ack for %q does not lead with it: %q", lead, ack)
		}
		if !strings.Contains(ack, msgFeedbackCardFooter) {
			t.Errorf("ack for %q omits the data-use notice; every rater must see it, not only a first-time thumbs-down", lead)
		}
		if !strings.Contains(ack, msgFeedbackWithdraw) {
			t.Errorf("ack for %q omits the withdraw line; a notice with no lever is not a choice", lead)
		}
	}
	if !strings.Contains(feedbackConflictNotice(), msgFeedbackWithdraw) {
		t.Errorf("the already-recorded notice must name the way out, or a mis-rating is a dead end on this surface")
	}
}

func seedRatableTask(t *testing.T, a *Adapter, phone, wamid string) (taskID string, row *store.WhatsAppOutbound) {
	t.Helper()
	ctx := context.Background()
	db := a.core.Store()
	if db == nil {
		t.Fatal("rig has no store")
	}
	taskID = idgen.New()
	sessionID := idgen.New()
	handler := "agent_01a0b077_329e_7f32_9c81_dc37a58e7aa7"
	hType := store.TaskHandlerTypeAgent
	if err := store.NewTaskRepo(db).Create(ctx, &store.Task{
		ID: taskID, UserID: "whatsapp:" + phone, StartTime: time.Now().UnixMilli(),
		ExecutionMode: "foreground", SessionID: &sessionID,
		HandlerName: &handler, HandlerType: &hType,
	}); err != nil {
		t.Fatalf("seed task: %v", err)
	}
	now := time.Now().UnixMilli()
	row = &store.WhatsAppOutbound{
		ID: idgen.New(), GatewayID: a.gatewayID(), Recipient: phone,
		TaskID: taskID, UserID: "whatsapp:" + phone, SessionID: sessionID,
		Kind: kindText, Payload: `{"text":"answer"}`, State: store.WhatsAppOutboundRead,
		Wamid: wamid, Attempts: 1, CreatedAt: now, UpdatedAt: now,
	}
	if err := a.outbound.Enqueue(ctx, nil, row); err != nil {
		t.Fatalf("seed outbound row: %v", err)
	}
	return taskID, row
}

func mustSaveFeedback(t *testing.T, a *Adapter, row *store.WhatsAppOutbound, rating string, comment *string) {
	t.Helper()
	if _, err := a.saveFeedback(row, rating, comment); err != nil {
		t.Fatalf("saveFeedback: %v", err)
	}
}

func feedbackRows(t *testing.T, a *Adapter, taskID string) []*store.Feedback {
	t.Helper()
	rows, err := store.NewFeedbackRepo(a.core.Store()).Search(context.Background(), "", 0, 0, taskID, "", "", 10, 0)
	if err != nil {
		t.Fatalf("search feedback: %v", err)
	}
	return rows
}

func TestSaveFeedback_WritesRatingWithWhatsAppAttribution(t *testing.T) {
	t.Parallel()
	mock := newWAMock(t)
	a := newFeedbackRig(t, mock, "Builder")
	taskID, row := seedRatableTask(t, a, "15551230000", "wamid.OUT-A")

	if _, err := a.saveFeedback(row, feedbackRatingDown, nil); err != nil {
		t.Fatalf("saveFeedback: %v", err)
	}
	rows := feedbackRows(t, a, taskID)
	if len(rows) != 1 {
		t.Fatalf("feedback rows = %d, want 1", len(rows))
	}
	got := rows[0]
	if got.Rating != "down" || got.Comment != nil || got.EntrypointType != store.EntrypointTypeWhatsApp ||
		got.UserID != row.UserID || got.SessionID != row.SessionID ||
		got.SubjectType != store.SubjectTypeAgent || got.SubjectID != "01a0b077-329e-7f32-9c81-dc37a58e7aa7" {
		t.Fatalf("row = %+v", *got)
	}
}

func TestSaveFeedback_ReasonUpsertsSameRow(t *testing.T) {
	t.Parallel()
	mock := newWAMock(t)
	a := newFeedbackRig(t, mock, "Builder")
	taskID, row := seedRatableTask(t, a, "15551230001", "wamid.OUT-B")

	mustSaveFeedback(t, a, row, feedbackRatingDown, nil)
	reason := "Inaccurate"
	mustSaveFeedback(t, a, row, feedbackRatingDown, &reason)

	rows := feedbackRows(t, a, taskID)
	if len(rows) != 1 || rows[0].Comment == nil || *rows[0].Comment != "Inaccurate" {
		t.Fatalf("rows = %+v, want one row with comment Inaccurate", rows)
	}
}

func TestB10_WhatsAppSaveFeedbackReRateConflicts(t *testing.T) {
	t.Parallel()
	mock := newWAMock(t)
	a := newFeedbackRig(t, mock, "Builder")
	taskID, row := seedRatableTask(t, a, "15551230002", "wamid.OUT-C")

	reason := "Confusing"
	if _, err := a.saveFeedback(row, feedbackRatingDown, &reason); err != nil {
		t.Fatalf("first submission: %v", err)
	}
	if _, err := a.saveFeedback(row, feedbackRatingUp, nil); !errors.Is(err, store.ErrFeedbackConflict) {
		t.Fatalf("re-rate err = %v, want ErrFeedbackConflict", err)
	}

	rows := feedbackRows(t, a, taskID)
	if len(rows) != 1 || rows[0].Rating != "down" || rows[0].Comment == nil || *rows[0].Comment != reason {
		t.Fatalf("rows = %+v, want the first submission kept", rows)
	}
}

func TestSaveFeedback_DropsWhenTaskUnknown(t *testing.T) {
	t.Parallel()
	mock := newWAMock(t)
	a := newFeedbackRig(t, mock, "Builder")
	row := &store.WhatsAppOutbound{TaskID: idgen.New(), UserID: "u", SessionID: "s", Recipient: "15551230003"}
	if _, err := a.saveFeedback(row, feedbackRatingUp, nil); err == nil {
		t.Fatal("saveFeedback must fail closed when the task has no record")
	}
}

func reactionMsg(phone, wamid, target, emoji string) *inboundMessage {
	return &inboundMessage{From: phone, ID: wamid, Type: "reaction",
		Reaction: &reactionObject{MessageID: target, Emoji: emoji}}
}

func interactiveSends(t *testing.T, a *Adapter, mock *waMock) []map[string]any {
	t.Helper()
	waitQueueDrained(t, a)
	var out []map[string]any
	for _, raw := range mock.rawSends() {
		var body map[string]any
		if err := json.Unmarshal(raw, &body); err != nil {
			continue
		}
		if body["type"] == "interactive" {
			out = append(out, body)
		}
	}
	return out
}

func TestHandleReaction_PositiveSavesUpAndThanks(t *testing.T) {
	t.Parallel()
	mock := newWAMock(t)
	a := newFeedbackRig(t, mock, "Builder")
	const phone = "15551230010"
	taskID, _ := seedRatableTask(t, a, phone, "wamid.OUT-P")

	if got := a.deliver(&webhookChangeValue{}, reactionMsg(phone, "wamid.R1", "wamid.OUT-P", "👍🏽")); got != settleAck {
		t.Fatalf("settled %v, want settleAck", got)
	}
	rows := feedbackRows(t, a, taskID)
	if len(rows) != 1 || rows[0].Rating != "up" {
		t.Fatalf("rows = %+v, want one up", rows)
	}
	if got := replyBodies(t, a, mock); len(got) != 1 || got[0] != feedbackAck(msgFeedbackThanks) {
		t.Fatalf("replies = %v, want [%q]", got, feedbackAck(msgFeedbackThanks))
	}
}

func TestHandleReaction_NegativeSavesDownAndSendsCard(t *testing.T) {
	t.Parallel()
	mock := newWAMock(t)
	a := newFeedbackRig(t, mock, "Builder")
	const phone = "15551230011"
	taskID, _ := seedRatableTask(t, a, phone, "wamid.OUT-N")

	a.deliver(&webhookChangeValue{}, reactionMsg(phone, "wamid.R2", "wamid.OUT-N", "👎"))

	rows := feedbackRows(t, a, taskID)
	if len(rows) != 1 || rows[0].Rating != "down" || rows[0].Comment != nil {
		t.Fatalf("rows = %+v, want one down with no comment yet", rows)
	}
	cards := interactiveSends(t, a, mock)
	if len(cards) != 1 {
		t.Fatalf("interactive sends = %d, want the reason card", len(cards))
	}
	inter, _ := cards[0]["interactive"].(map[string]any)
	if inter["type"] != "list" {
		t.Fatalf("card type = %v, want list", inter["type"])
	}
	footer, _ := inter["footer"].(map[string]any)
	if footer["text"] != msgFeedbackCardFooter {
		t.Fatalf("footer = %v, want the consent copy", footer)
	}
	action, _ := inter["action"].(map[string]any)
	sections, _ := action["sections"].([]any)
	rowsWire, _ := sections[0].(map[string]any)["rows"].([]any)
	if len(rowsWire) != len(feedbackReasons) {
		t.Fatalf("card rows = %d, want %d", len(rowsWire), len(feedbackReasons))
	}
	firstID, _ := rowsWire[0].(map[string]any)["id"].(string)
	if reason, gotTask, ok := decodeFeedbackRowID(firstID); !ok || reason != "inaccurate" || gotTask != taskID {
		t.Fatalf("first row id %q did not decode to (inaccurate, %s)", firstID, taskID)
	}
	if got := replyBodies(t, a, mock); len(got) != 0 {
		t.Fatalf("a negative reaction must not also send the thanks text, got %v", got)
	}
}

func TestHandleReaction_UnclassifiedIgnored(t *testing.T) {
	t.Parallel()
	mock := newWAMock(t)
	a := newFeedbackRig(t, mock, "Builder")
	const phone = "15551230012"
	taskID, _ := seedRatableTask(t, a, phone, "wamid.OUT-U")

	a.deliver(&webhookChangeValue{}, reactionMsg(phone, "wamid.R3", "wamid.OUT-U", "😮"))

	if rows := feedbackRows(t, a, taskID); len(rows) != 0 {
		t.Fatalf("unclassified emoji wrote %+v", rows)
	}
	if got := replyBodies(t, a, mock); len(got) != 0 {
		t.Fatalf("unclassified emoji replied %v", got)
	}
}

func TestHandleReaction_UntrackedMessageIgnored(t *testing.T) {
	t.Parallel()
	mock := newWAMock(t)
	a := newFeedbackRig(t, mock, "Builder")
	const phone = "15551230013"

	if got := a.deliver(&webhookChangeValue{}, reactionMsg(phone, "wamid.R4", "wamid.NOT-OURS", "👍")); got != settleAck {
		t.Fatalf("settled %v, want settleAck", got)
	}
	if got := replyBodies(t, a, mock); len(got) != 0 {
		t.Fatalf("untracked reaction replied %v", got)
	}
	a.deliver(&webhookChangeValue{}, &inboundMessage{From: phone, ID: "wamid.R5", Type: "reaction"})
	if got := replyBodies(t, a, mock); len(got) != 0 {
		t.Fatalf("bare reaction replied %v", got)
	}
}

func TestHandleReaction_RemovalWithdrawsTheFeedback(t *testing.T) {
	t.Parallel()
	mock := newWAMock(t)
	a := newFeedbackRig(t, mock, "Builder")
	metrics := newCountingMetrics()
	a.metrics = metrics
	const phone = "15551230014"
	taskID, _ := seedRatableTask(t, a, phone, "wamid.OUT-RM")

	a.deliver(&webhookChangeValue{}, reactionMsg(phone, "wamid.R6", "wamid.OUT-RM", "👍"))
	a.deliver(&webhookChangeValue{}, reactionMsg(phone, "wamid.R7", "wamid.OUT-RM", ""))

	if rows := feedbackRows(t, a, taskID); len(rows) != 0 {
		t.Fatalf("rows = %+v, want none: removing the reaction withdraws the feedback", rows)
	}
	if got := replyBodies(t, a, mock); len(got) != 1 {
		t.Fatalf("replies = %v, want only the rating acknowledgement: a withdrawal is silent because the client already shows the reaction gone", got)
	}
	if got := metrics.outcomeCount(metricWhatsAppFeedback, fbOutcomeRevoked); got != 1 {
		t.Errorf("revoked counted %d times, want 1", got)
	}
}

func TestHandleReaction_RemovalWithNothingStoredIsACleanNoop(t *testing.T) {
	t.Parallel()
	mock := newWAMock(t)
	a := newFeedbackRig(t, mock, "Builder")
	metrics := newCountingMetrics()
	a.metrics = metrics
	const phone = "15551230016"
	taskID, _ := seedRatableTask(t, a, phone, "wamid.OUT-RN")

	a.deliver(&webhookChangeValue{}, reactionMsg(phone, "wamid.R10", "wamid.OUT-RN", ""))

	if rows := feedbackRows(t, a, taskID); len(rows) != 0 {
		t.Fatalf("rows = %+v, want none", rows)
	}
	if got := replyBodies(t, a, mock); len(got) != 0 {
		t.Fatalf("replies = %v, want none: there was nothing to withdraw", got)
	}
	if got := metrics.outcomeCount(metricWhatsAppFeedback, fbOutcomeRemovedNoop); got != 1 {
		t.Errorf("removed_noop counted %d times, want 1", got)
	}
	if got := metrics.outcomeCount(metricWhatsAppFeedback, fbOutcomeRevoked); got != 0 {
		t.Errorf("revoked counted %d times, want 0: nothing was withdrawn", got)
	}
}

func TestHandleReaction_RemovalBySomeoneElseLeavesTheRowIntact(t *testing.T) {
	t.Parallel()
	mock := newWAMock(t)
	a := newFeedbackRig(t, mock, "Builder")
	const phone = "15551230017"
	const intruder = "15559998888"
	taskID, _ := seedRatableTask(t, a, phone, "wamid.OUT-RI")

	a.deliver(&webhookChangeValue{}, reactionMsg(phone, "wamid.R11", "wamid.OUT-RI", "👍"))
	a.deliver(&webhookChangeValue{}, reactionMsg(intruder, "wamid.R12", "wamid.OUT-RI", ""))

	rows := feedbackRows(t, a, taskID)
	if len(rows) != 1 || rows[0].Rating != "up" {
		t.Fatalf("rows = %+v, want the recipient's up intact: only the recipient may withdraw", rows)
	}
}

func TestHandleReaction_RemovalOnGatewayCopyWithdrawsNothing(t *testing.T) {
	t.Parallel()
	mock := newWAMock(t)
	a := newFeedbackRig(t, mock, "Builder")
	const phone = "15551230018"
	taskID, row := seedRatableTask(t, a, phone, "wamid.OUT-RC")

	a.deliver(&webhookChangeValue{}, reactionMsg(phone, "wamid.R13", "wamid.OUT-RC", "👎"))
	seedReasonCard(t, a, row, "wamid.CARD-RC")
	a.deliver(&webhookChangeValue{}, reactionMsg(phone, "wamid.R14", "wamid.CARD-RC", ""))

	if rows := feedbackRows(t, a, taskID); len(rows) != 1 {
		t.Fatalf("rows = %+v, want the rating intact: a reaction on gateway copy rates and withdraws nothing", rows)
	}
}

func TestHandleReaction_RemovalTakesTheReasonWithIt(t *testing.T) {
	t.Parallel()
	mock := newWAMock(t)
	a := newFeedbackRig(t, mock, "Builder")
	const phone = "15551230019"
	taskID, row := seedRatableTask(t, a, phone, "wamid.OUT-RR2")

	a.deliver(&webhookChangeValue{}, reactionMsg(phone, "wamid.R15", "wamid.OUT-RR2", "👎"))
	seedReasonCard(t, a, row, "wamid.CARD-RR2")
	a.deliver(&webhookChangeValue{}, listTap(phone, "wamid.R16", encodeFeedbackRowID("confusing", taskID), "wamid.CARD-RR2"))
	a.deliver(&webhookChangeValue{}, reactionMsg(phone, "wamid.R17", "wamid.OUT-RR2", ""))

	if rows := feedbackRows(t, a, taskID); len(rows) != 0 {
		t.Fatalf("rows = %+v, want none: the reason explains the rating and cannot outlive it", rows)
	}
}

func TestHandleReaction_WithdrawThenRateAgainLandsTheNewRating(t *testing.T) {
	t.Parallel()
	mock := newWAMock(t)
	a := newFeedbackRig(t, mock, "Builder")
	const phone = "15551230020"
	taskID, _ := seedRatableTask(t, a, phone, "wamid.OUT-RW")

	a.deliver(&webhookChangeValue{}, reactionMsg(phone, "wamid.R18", "wamid.OUT-RW", "👍"))
	a.deliver(&webhookChangeValue{}, reactionMsg(phone, "wamid.R19", "wamid.OUT-RW", ""))
	a.deliver(&webhookChangeValue{}, reactionMsg(phone, "wamid.R20", "wamid.OUT-RW", "👎"))

	rows := feedbackRows(t, a, taskID)
	if len(rows) != 1 || rows[0].Rating != "down" {
		t.Fatalf("rows = %+v, want one down: withdrawing frees the user to rate again", rows)
	}
}

func TestHandleReaction_RemovalPublishesNothing(t *testing.T) {
	t.Parallel()
	mock := newWAMock(t)
	a := newFeedbackRig(t, mock, "Builder")
	brk, q := subscribeWhatsAppFeedback(t, a, "whatsapp-feedback-withdraw")
	const phone = "15551230021"
	seedRatableTask(t, a, phone, "wamid.OUT-RP")

	a.deliver(&webhookChangeValue{}, reactionMsg(phone, "wamid.R21", "wamid.OUT-RP", "👍"))
	if event, published := nextWhatsAppFeedbackEvent(t, brk, q, 300*time.Millisecond); !published || event != "created" {
		t.Fatalf("rating published %q (%v), want created", event, published)
	}

	a.deliver(&webhookChangeValue{}, reactionMsg(phone, "wamid.R22", "wamid.OUT-RP", ""))
	if event, published := nextWhatsAppFeedbackEvent(t, brk, q, 300*time.Millisecond); published {
		t.Fatalf("withdrawal published %q; it must publish nothing — the created event already left and cannot be recalled", event)
	}
}

func TestB10_WhatsAppReactionFlipConflicts(t *testing.T) {
	t.Parallel()
	mock := newWAMock(t)
	a := newFeedbackRig(t, mock, "Builder")
	const phone = "15551230015"
	taskID, _ := seedRatableTask(t, a, phone, "wamid.OUT-RR")

	a.deliver(&webhookChangeValue{}, reactionMsg(phone, "wamid.R8", "wamid.OUT-RR", "👍"))
	a.deliver(&webhookChangeValue{}, reactionMsg(phone, "wamid.R9", "wamid.OUT-RR", "👎"))

	rows := feedbackRows(t, a, taskID)
	if len(rows) != 1 || rows[0].Rating != "up" {
		t.Fatalf("rows = %+v, want the first rating kept", rows)
	}
	if cards := interactiveSends(t, a, mock); len(cards) != 0 {
		t.Fatalf("a rejected flip must not send the reason card, got %d interactive sends", len(cards))
	}
	if got := replyBodies(t, a, mock); len(got) != 2 || got[1] != feedbackConflictNotice() {
		t.Fatalf("replies = %v, want thanks then the already-recorded notice", got)
	}
}

func TestHandleReaction_OnReasonCardIgnored(t *testing.T) {
	t.Parallel()
	mock := newWAMock(t)
	a := newFeedbackRig(t, mock, "Builder")
	const phone = "15551230016"
	taskID, row := seedRatableTask(t, a, phone, "wamid.OUT-ANS")
	seedReasonCard(t, a, row, "wamid.OUT-CARD")

	a.deliver(&webhookChangeValue{}, reactionMsg(phone, "wamid.R10", "wamid.OUT-CARD", "👎"))

	if rows := feedbackRows(t, a, taskID); len(rows) != 0 {
		t.Fatalf("reaction on the card must not write feedback; rows = %+v", rows)
	}
	if cards := interactiveSends(t, a, mock); len(cards) != 0 {
		t.Fatalf("reaction on the card must not send a second card, got %d", len(cards))
	}
}

func TestHandleReaction_OnNoticeRowIgnored(t *testing.T) {
	t.Parallel()
	mock := newWAMock(t)
	a := newFeedbackRig(t, mock, "Builder")
	const phone = "15551230017"
	taskID, row := seedRatableTask(t, a, phone, "wamid.OUT-NOT")

	notice := *row
	notice.ID = idgen.New()
	notice.Kind = kindNotice
	notice.UserID = ""
	notice.Wamid = "wamid.OUT-NOTICE"
	if err := a.outbound.Enqueue(context.Background(), nil, &notice); err != nil {
		t.Fatalf("seed notice row: %v", err)
	}

	a.deliver(&webhookChangeValue{}, reactionMsg(phone, "wamid.R11", "wamid.OUT-NOTICE", "👍"))

	if rows := feedbackRows(t, a, taskID); len(rows) != 0 {
		t.Fatalf("reaction on a notice row must not write feedback; rows = %+v", rows)
	}
	if got := replyBodies(t, a, mock); len(got) != 0 {
		t.Fatalf("reaction on a notice row must not reply, got %v", got)
	}
}

func TestHandleReaction_SenderMismatchIgnored(t *testing.T) {
	t.Parallel()
	mock := newWAMock(t)
	a := newFeedbackRig(t, mock, "Builder")
	const owner, other = "15551230018", "15551230019"
	taskID, _ := seedRatableTask(t, a, owner, "wamid.OUT-MISM")

	a.deliver(&webhookChangeValue{}, reactionMsg(other, "wamid.R12", "wamid.OUT-MISM", "👍"))

	if rows := feedbackRows(t, a, taskID); len(rows) != 0 {
		t.Fatalf("reaction from a different sender must not write feedback; rows = %+v", rows)
	}
	if got := replyBodies(t, a, mock); len(got) != 0 {
		t.Fatalf("reaction from a different sender must not reply, got %v", got)
	}
}

func TestHandleReaction_ThanksRowCarriesUser(t *testing.T) {
	t.Parallel()
	mock := newWAMock(t)
	a := newFeedbackRig(t, mock, "Builder")
	const phone = "15551230025"
	taskID, row := seedRatableTask(t, a, phone, "wamid.OUT-PROV")
	traceID := idgen.New()
	if _, err := a.core.Store().ExecContext(context.Background(),
		`UPDATE whatsapp_outbound SET trace_id = ? WHERE id = ?`, traceID, row.ID); err != nil {
		t.Fatalf("seed traceID onto the answer row: %v", err)
	}
	row.TraceID = traceID

	a.deliver(&webhookChangeValue{}, reactionMsg(phone, "wamid.R13", "wamid.OUT-PROV", "👍"))
	waitQueueDrained(t, a)

	notices := outboundRowsOfKind(t, a, taskID, kindNotice)
	if len(notices) != 1 {
		t.Fatalf("notice rows for task = %d, want 1", len(notices))
	}
	notice := notices[0]
	if notice.UserID != row.UserID || notice.TraceID != row.TraceID || notice.SessionID != row.SessionID {
		t.Fatalf("notice row = %+v, want userID/traceID/sessionID copied from the rated row %+v", notice, row)
	}
}

func listTap(phone, wamid, rowID, contextWamid string) *inboundMessage {
	msg := &inboundMessage{From: phone, ID: wamid, Type: "interactive",
		Interactive: &inboundInteractive{Type: "list_reply", ListReply: &replyRef{ID: rowID, Title: "x"}}}
	if contextWamid != "" {
		msg.Context = &messageContext{ID: contextWamid}
	}
	return msg
}

func seedReasonCard(t *testing.T, a *Adapter, base *store.WhatsAppOutbound, wamid string) {
	t.Helper()
	card := *base
	card.ID = idgen.New()
	card.Kind = kindFeedbackPrompt
	card.Wamid = wamid
	card.Payload = `{"text":"card"}`
	if err := a.outbound.Enqueue(context.Background(), nil, &card); err != nil {
		t.Fatalf("seed card: %v", err)
	}
}

func TestHandleFeedbackListReply_StoresReasonAndThanks(t *testing.T) {
	t.Parallel()
	mock := newWAMock(t)
	a := newFeedbackRig(t, mock, "Builder")
	const phone = "15551230020"
	taskID, row := seedRatableTask(t, a, phone, "wamid.OUT-L1")
	seedReasonCard(t, a, row, "wamid.CARD-1")
	mustSaveFeedback(t, a, row, feedbackRatingDown, nil)

	tap := listTap(phone, "wamid.T1", encodeFeedbackRowID("confusing", taskID), "wamid.CARD-1")
	if got := a.deliver(&webhookChangeValue{}, tap); got != settleAck {
		t.Fatalf("settled %v", got)
	}
	rows := feedbackRows(t, a, taskID)
	if len(rows) != 1 || rows[0].Rating != "down" || rows[0].Comment == nil || *rows[0].Comment != "Confusing" {
		t.Fatalf("rows = %+v, want down with comment Confusing", rows)
	}
	if got := replyBodies(t, a, mock); len(got) != 1 || got[0] != feedbackAck(msgFeedbackNoted) {
		t.Fatalf("replies = %v, want [%q]", got, feedbackAck(msgFeedbackNoted))
	}
}

func TestHandleFeedbackListReply_TwoCardsAnsweredOutOfOrder(t *testing.T) {
	t.Parallel()
	mock := newWAMock(t)
	a := newFeedbackRig(t, mock, "Builder")
	const phone = "15551230021"
	taskA, rowA := seedRatableTask(t, a, phone, "wamid.OUT-A2")
	taskB, rowB := seedRatableTask(t, a, phone, "wamid.OUT-B2")
	seedReasonCard(t, a, rowA, "wamid.CARD-A")
	seedReasonCard(t, a, rowB, "wamid.CARD-B")
	mustSaveFeedback(t, a, rowA, feedbackRatingDown, nil)
	mustSaveFeedback(t, a, rowB, feedbackRatingDown, nil)

	a.deliver(&webhookChangeValue{}, listTap(phone, "wamid.T2", encodeFeedbackRowID("incomplete", taskB), "wamid.CARD-B"))
	a.deliver(&webhookChangeValue{}, listTap(phone, "wamid.T3", encodeFeedbackRowID("inaccurate", taskA), "wamid.CARD-A"))

	if rows := feedbackRows(t, a, taskA); len(rows) != 1 || rows[0].Comment == nil || *rows[0].Comment != "Inaccurate" {
		t.Fatalf("task A rows = %+v", rows)
	}
	if rows := feedbackRows(t, a, taskB); len(rows) != 1 || rows[0].Comment == nil || *rows[0].Comment != "Incomplete" {
		t.Fatalf("task B rows = %+v", rows)
	}
}

func TestHandleFeedbackListReply_ContextMismatchIgnored(t *testing.T) {
	t.Parallel()
	mock := newWAMock(t)
	a := newFeedbackRig(t, mock, "Builder")
	const phone = "15551230022"
	taskA, rowA := seedRatableTask(t, a, phone, "wamid.OUT-A3")
	_, rowB := seedRatableTask(t, a, phone, "wamid.OUT-B3")
	seedReasonCard(t, a, rowA, "wamid.CARD-A3")
	seedReasonCard(t, a, rowB, "wamid.CARD-B3")
	mustSaveFeedback(t, a, rowA, feedbackRatingDown, nil)

	a.deliver(&webhookChangeValue{}, listTap(phone, "wamid.T4", encodeFeedbackRowID("other", taskA), "wamid.CARD-B3"))

	if rows := feedbackRows(t, a, taskA); len(rows) != 1 || rows[0].Comment != nil {
		t.Fatalf("mismatched tap must not write a reason; rows = %+v", rows)
	}
	if got := replyBodies(t, a, mock); len(got) != 0 {
		t.Fatalf("mismatched tap replied %v", got)
	}
}

func TestHandleFeedbackListReply_PrunedCardIgnored(t *testing.T) {
	t.Parallel()
	mock := newWAMock(t)
	a := newFeedbackRig(t, mock, "Builder")
	const phone = "15551230023"
	taskID, row := seedRatableTask(t, a, phone, "wamid.OUT-A4")
	mustSaveFeedback(t, a, row, feedbackRatingDown, nil)
	a.deliver(&webhookChangeValue{}, listTap(phone, "wamid.T5", encodeFeedbackRowID("other", taskID), "wamid.CARD-GONE"))

	if rows := feedbackRows(t, a, taskID); len(rows) != 1 || rows[0].Comment != nil {
		t.Fatalf("tap on a pruned card must not write; rows = %+v", rows)
	}
}

func TestHandleFeedbackListReply_SenderMismatchIgnored(t *testing.T) {
	t.Parallel()
	mock := newWAMock(t)
	a := newFeedbackRig(t, mock, "Builder")
	const owner, other = "15551230026", "15551230027"
	taskID, row := seedRatableTask(t, a, owner, "wamid.OUT-A5")
	seedReasonCard(t, a, row, "wamid.CARD-A5")
	mustSaveFeedback(t, a, row, feedbackRatingDown, nil)

	a.deliver(&webhookChangeValue{}, listTap(other, "wamid.T7", encodeFeedbackRowID("other", taskID), "wamid.CARD-A5"))

	if rows := feedbackRows(t, a, taskID); len(rows) != 1 || rows[0].Comment != nil {
		t.Fatalf("tap from a different sender must not write a reason; rows = %+v", rows)
	}
	if got := replyBodies(t, a, mock); len(got) != 0 {
		t.Fatalf("tap from a different sender must not reply, got %v", got)
	}
}

func TestHandleFeedbackListReply_WinsOverOpenHILPrompt(t *testing.T) {
	t.Parallel()
	a, raw := newHILRig(t)
	a.cfg.FeedbackEnabled = true
	const phone = "15551230024"
	taskID, row := seedRatableTask(t, a, phone, "wamid.OUT-H")
	seedReasonCard(t, a, row, "wamid.CARD-H")
	mustSaveFeedback(t, a, row, feedbackRatingDown, nil)
	seedPendingPrompt(t, a, phone, store.WhatsAppPendingPrompt{TaskID: "t-hil", RequestID: "req-hil", ExpiresAt: time.Now().Add(time.Hour).UnixMilli()})

	a.deliver(&webhookChangeValue{}, listTap(phone, "wamid.T6", encodeFeedbackRowID("incomplete", taskID), "wamid.CARD-H"))

	if rows := feedbackRows(t, a, taskID); len(rows) != 1 || rows[0].Comment == nil || *rows[0].Comment != "Incomplete" {
		t.Fatalf("feedback tap must be recorded even with a HIL prompt open; rows = %+v", rows)
	}
	if pendingPromptRow(t, a, phone) == nil {
		t.Fatal("the HIL prompt must be untouched by a feedback tap")
	}
	_ = raw
}

func TestFeedbackListReplyID_IgnoresHILTaps(t *testing.T) {
	t.Parallel()
	if _, ok := feedbackListReplyID(&inboundMessage{Type: "interactive",
		Interactive: &inboundInteractive{Type: "list_reply", ListReply: &replyRef{ID: "opt:0"}}}); ok {
		t.Fatal("a HIL option id must not be claimed as feedback")
	}
	if _, ok := feedbackListReplyID(&inboundMessage{Type: "interactive",
		Interactive: &inboundInteractive{Type: "button_reply", ButtonReply: &replyRef{ID: "fb:other:01a0b0fa-c8d3-7505-b718-2b42ea9edfb9"}}}); ok {
		t.Fatal("only list replies carry feedback row ids")
	}
	if _, ok := feedbackListReplyID(&inboundMessage{Type: "text"}); ok {
		t.Fatal("text is never a feedback tap")
	}
}

func lastSentWamid(t *testing.T, a *Adapter, kind string) string {
	t.Helper()
	waitQueueDrained(t, a)
	var wamid string
	err := a.core.Store().QueryRowContext(context.Background(),
		`SELECT wamid FROM whatsapp_outbound
		  WHERE gateway_id = ? AND kind = ? AND wamid <> ''
		  ORDER BY updated_at DESC, id DESC LIMIT 1`,
		a.gatewayID(), kind).Scan(&wamid)
	if err != nil {
		t.Fatalf("no sent %s row: %v", kind, err)
	}
	return wamid
}

func TestHandleReaction_SecondChunkKeepsStoredReason(t *testing.T) {
	t.Parallel()
	mock := newWAMock(t)
	a := newFeedbackRig(t, mock, "Builder")
	const phone = "15551230040"
	taskID, chunk1 := seedRatableTask(t, a, phone, "wamid.OUT-C1")

	chunk2 := *chunk1
	chunk2.ID = idgen.New()
	chunk2.Wamid = "wamid.OUT-C2"
	if err := a.outbound.Enqueue(context.Background(), nil, &chunk2); err != nil {
		t.Fatalf("seed second chunk: %v", err)
	}

	a.deliver(&webhookChangeValue{}, reactionMsg(phone, "wamid.R-C1", "wamid.OUT-C1", "👎"))
	cards := interactiveSends(t, a, mock)
	if len(cards) != 1 {
		t.Fatalf("first thumbs-down must send one card, got %d", len(cards))
	}
	a.deliver(&webhookChangeValue{}, listTap(phone, "wamid.T-C1", encodeFeedbackRowID("confusing", taskID), lastSentWamid(t, a, kindFeedbackPrompt)))
	if rows := feedbackRows(t, a, taskID); len(rows) != 1 || rows[0].Comment == nil || *rows[0].Comment != "Confusing" {
		t.Fatalf("reason not stored; rows = %+v", rows)
	}

	a.deliver(&webhookChangeValue{}, reactionMsg(phone, "wamid.R-C2", "wamid.OUT-C2", "👎"))

	rows := feedbackRows(t, a, taskID)
	if len(rows) != 1 || rows[0].Comment == nil || *rows[0].Comment != "Confusing" {
		t.Fatalf("reacting to a second chunk wiped the reason; rows = %+v", rows)
	}
	if cards := interactiveSends(t, a, mock); len(cards) != 1 {
		t.Fatalf("an already-answered task must not be asked again; cards = %d", len(cards))
	}
}

func TestHandleReaction_RepeatedThumbsDownKeepsReason(t *testing.T) {
	t.Parallel()
	mock := newWAMock(t)
	a := newFeedbackRig(t, mock, "Builder")
	const phone = "15551230041"
	taskID, row := seedRatableTask(t, a, phone, "wamid.OUT-E")
	seedReasonCard(t, a, row, "wamid.CARD-E")
	mustSaveFeedback(t, a, row, feedbackRatingDown, nil)
	reason := "Confusing"
	mustSaveFeedback(t, a, row, feedbackRatingDown, &reason)

	a.deliver(&webhookChangeValue{}, reactionMsg(phone, "wamid.R-E", "wamid.OUT-E", "👎"))

	if rows := feedbackRows(t, a, taskID); len(rows) != 1 || rows[0].Comment == nil || *rows[0].Comment != "Confusing" {
		t.Fatalf("an unchanged rating must keep its reason; rows = %+v", rows)
	}
}

func TestB10_WhatsAppFlipAfterReasonConflicts(t *testing.T) {
	t.Parallel()
	mock := newWAMock(t)
	a := newFeedbackRig(t, mock, "Builder")
	const phone = "15551230042"
	taskID, row := seedRatableTask(t, a, phone, "wamid.OUT-F")
	seedReasonCard(t, a, row, "wamid.CARD-F")
	mustSaveFeedback(t, a, row, feedbackRatingDown, nil)
	reason := "Confusing"
	mustSaveFeedback(t, a, row, feedbackRatingDown, &reason)

	a.deliver(&webhookChangeValue{}, reactionMsg(phone, "wamid.R-F", "wamid.OUT-F", "👍"))

	rows := feedbackRows(t, a, taskID)
	if len(rows) != 1 || rows[0].Rating != "down" || rows[0].Comment == nil || *rows[0].Comment != reason {
		t.Fatalf("rows = %+v, want down with the submitted reason", rows)
	}
	if got := replyBodies(t, a, mock); len(got) != 1 || got[0] != feedbackConflictNotice() {
		t.Fatalf("replies = %v, want the already-recorded notice", got)
	}
}

func TestHandleReaction_DisabledByDefault(t *testing.T) {
	t.Parallel()
	mock := newWAMock(t)
	a := newAdapterRig(t, mock, "Builder")
	const phone = "15551230043"
	taskID, _ := seedRatableTask(t, a, phone, "wamid.OUT-D")

	a.deliver(&webhookChangeValue{}, reactionMsg(phone, "wamid.R-D", "wamid.OUT-D", "👎"))

	if rows := feedbackRows(t, a, taskID); len(rows) != 0 {
		t.Fatalf("capture is off by default; rows = %+v", rows)
	}
	if got := replyBodies(t, a, mock); len(got) != 0 {
		t.Fatalf("a disabled entrypoint must send nothing; replies = %v", got)
	}
	if cards := interactiveSends(t, a, mock); len(cards) != 0 {
		t.Fatalf("a disabled entrypoint must send no card; cards = %d", len(cards))
	}
}

func TestHandleReaction_BSUIDSenderIsRecognised(t *testing.T) {
	t.Parallel()
	mock := newWAMock(t)
	a := newFeedbackRig(t, mock, "Builder")
	const bsuid = "BSUID-9911"
	taskID, _ := seedRatableTask(t, a, bsuid, "wamid.OUT-B")

	msg := reactionMsg("", "wamid.R-B", "wamid.OUT-B", "👍")
	msg.FromUserID = bsuid
	a.deliver(&webhookChangeValue{}, msg)

	if rows := feedbackRows(t, a, taskID); len(rows) != 1 || rows[0].Rating != "up" {
		t.Fatalf("a BSUID sender must be able to rate; rows = %+v", rows)
	}
}

func TestHandleReaction_FailedWithdrawalTellsTheUser(t *testing.T) {
	t.Parallel()
	mock := newWAMock(t)
	a := newFeedbackRig(t, mock, "Builder")
	metrics := newCountingMetrics()
	a.metrics = metrics
	const phone = "15551230022"
	seedRatableTask(t, a, phone, "wamid.OUT-RF")

	a.deliver(&webhookChangeValue{}, reactionMsg(phone, "wamid.R23", "wamid.OUT-RF", "👍"))
	waitQueueDrained(t, a)

	if _, err := a.core.Store().ExecContext(context.Background(), `DROP TABLE feedback`); err != nil {
		t.Fatalf("drop feedback table: %v", err)
	}

	a.deliver(&webhookChangeValue{}, reactionMsg(phone, "wamid.R24", "wamid.OUT-RF", ""))

	got := replyBodies(t, a, mock)
	if len(got) != 2 || got[1] != msgFeedbackWithdrawFailed {
		t.Fatalf("replies = %v, want the acknowledgement then %q: a failed withdrawal must not pass for a successful one", got, msgFeedbackWithdrawFailed)
	}
	if n := metrics.outcomeCount(metricWhatsAppFeedback, fbOutcomeRevoked); n != 0 {
		t.Errorf("revoked counted %d times, want 0: nothing was withdrawn", n)
	}
	if n := metrics.outcomeCount(metricWhatsAppFeedback, fbOutcomeDropped); n != 1 {
		t.Errorf("dropped counted %d times, want 1", n)
	}
}

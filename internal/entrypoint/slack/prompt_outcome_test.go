// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package slack

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
)

func postApproverCard(t *testing.T, a *SlackGatewayAdapter, replies func(time.Duration) *broker.Message, requestID string) slackCard {
	t.Helper()
	a.handleNotifyDeliver(a2a.NotifyDeliver{
		RequestID: requestID, Mode: a2a.NotifyModeInput, UserID: "dana@example.com", ChannelType: "slack",
		Address: json.RawMessage(`{"team_id":"T1","user_id":"U-dana"}`), Surface: approvalSurfaceFor(t, map[string]any{"method": "POST"}),
		ApproverSet: true,
	})
	msg := replies(2 * time.Second)
	if msg == nil {
		t.Fatal("the posted card was not reported to the notification service")
	}
	var receipt struct {
		Status string `json:"status"`
		Data   struct {
			Card slackCard `json:"card"`
		} `json:"data"`
	}
	if err := json.Unmarshal(msg.Payload, &receipt); err != nil {
		t.Fatal(err)
	}
	if receipt.Status != a2a.HILStatusDelivered || msg.UserProperties[a2a.PropChannel] != "slack" {
		t.Fatalf("receipt = %+v %v, want a delivered receipt from slack", receipt, msg.UserProperties)
	}
	return receipt.Data.Card
}

func TestSlackApprovalCard_ReportedWhenPosted(t *testing.T) {
	a, _, brk := newHandlerTestAdapter(t)
	card := postApproverCard(t, a, replyQueue(t, brk, "req-reported"), "req-reported")

	if card.ChannelID != "D-U-dana" || card.TS != "1700000000.000010" || card.UserID != "" {
		t.Errorf("card = %+v, want the DM and the posted ts", card)
	}
	blocks := string(card.Blocks)
	if !strings.Contains(blocks, "Approval Required") || !strings.Contains(blocks, "POST") {
		t.Errorf("kept blocks lack the card's content: %s", blocks)
	}
	if strings.Contains(blocks, "hil_input_actions") || strings.Contains(blocks, expiryNotePrefix) {
		t.Errorf("kept blocks still offer buttons or an expiry: %s", blocks)
	}
}

func TestSlackApprovalCard_ShowsTheOutcomeInPlace(t *testing.T) {
	for _, tc := range []struct{ name, outcome, text string }{
		{"accepted here", a2a.NotifyOutcomeApproved, "Approved by Dana Kim"},
		{"declined by another approver", a2a.NotifyOutcomeDeclined, "Declined by Maya Ortiz"},
		{"expired", a2a.NotifyOutcomeExpired, "Expired"},
		{"the run ended", a2a.NotifyOutcomeWithdrawn, "No longer needed: the run ended"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, calls, brk := newHandlerTestAdapter(t)
			card := postApproverCard(t, a, replyQueue(t, brk, "req-outcome"), "req-outcome")
			address, _ := json.Marshal(card)

			a.handleNotifyDeliver(a2a.NotifyDeliver{
				RequestID: "req-outcome", Mode: a2a.NotifyModeNotify, ChannelType: "slack", Address: address, ApproverSet: true,
				Title: "Approval request answered", Message: tc.text + ".",
				PromptOutcome: &a2a.NotifyPromptOutcome{Outcome: tc.outcome, Text: tc.text},
			})

			up := calls.LastUpdate()
			if calls.Updates() != 1 || up.Channel != "D-U-dana" || up.TS != card.TS || calls.LastUpdateText() != tc.text {
				t.Fatalf("update = %+v text %q (%d updates), want the card edited to %q", up, calls.LastUpdateText(), calls.Updates(), tc.text)
			}
			if strings.Contains(up.Blocks, HILInputActionPrefix) || strings.Contains(up.Blocks, expiryNotePrefix) {
				t.Errorf("the settled card still offers buttons or an expiry: %s", up.Blocks)
			}
			if !strings.Contains(up.Blocks, tc.text) || !strings.Contains(up.Blocks, "POST") {
				t.Errorf("the settled card lacks the outcome or its content: %s", up.Blocks)
			}
			if calls.Posts() != 1 || len(calls.Ephemerals()) != 0 {
				t.Errorf("posts = %d, ephemerals = %+v, want the outcome shown only on the card", calls.Posts(), calls.Ephemerals())
			}
		})
	}
}

func TestSlackApprovalCard_ARefusalReachesOnlyTheClicker(t *testing.T) {
	a, calls, _ := newHandlerTestAdapter(t)
	a.handleNotifyDeliver(a2a.NotifyDeliver{
		RequestID: "req-refused", Mode: a2a.NotifyModeNotify, ChannelType: "slack", ApproverSet: true,
		Address:       json.RawMessage(`{"channel_id":"C0OPS","ts":"1700000000.1","user_id":"U-bob"}`),
		Message:       "Your answer was not counted: you are not one of the approvers of this request.",
		PromptOutcome: &a2a.NotifyPromptOutcome{Refusal: "Your answer was not counted: you are not one of the approvers of this request."},
	})
	if e := calls.Ephemerals(); len(e) != 1 || e[0].User != "U-bob" || e[0].Channel != "C0OPS" || !strings.Contains(e[0].Text, "not one of the approvers") {
		t.Errorf("ephemerals = %+v, want the reason shown to bob alone", e)
	}
	if calls.Updates() != 0 || calls.Posts() != 0 {
		t.Errorf("updates = %d, posts = %d, want neither", calls.Updates(), calls.Posts())
	}
}

func TestSlackApproverSet_ClickNamesItsCard(t *testing.T) {
	a, calls, brk := newHandlerTestAdapter(t)
	replies := replyQueue(t, brk, "req-named")

	approverSetClick(t, a, "req-named", "U-alice")

	msg := replies(2 * time.Second)
	if msg == nil {
		t.Fatal("no reply was published")
	}
	var card slackCard
	if err := json.Unmarshal([]byte(msg.UserProperties[a2a.PropNotifyCard]), &card); err != nil {
		t.Fatalf("reply names no card: %v", msg.UserProperties)
	}
	if card.ChannelID != "C0OPS" || card.TS != "1700000000.1" || card.UserID != "U-alice" || len(card.Blocks) != 0 {
		t.Errorf("named card = %+v, want the clicked message and the clicker", card)
	}
	if e := calls.Ephemerals(); len(e) != 1 || e[0].Text != msgHILAnswerSent || !strings.Contains(e[0].Text, "card shows the result") {
		t.Errorf("ephemerals = %+v, want the clicker told where the result shows", e)
	}
}

func TestSlackApprovalCard_AnApproversNameIsEscapedOnTheSettledCard(t *testing.T) {
	a, calls, brk := newHandlerTestAdapter(t)
	card := postApproverCard(t, a, replyQueue(t, brk, "req-name"), "req-name")
	address, _ := json.Marshal(card)
	text := "Approved by <!channel> & <https://evil.example|bank>"

	a.handleNotifyDeliver(a2a.NotifyDeliver{
		RequestID: "req-name", Mode: a2a.NotifyModeNotify, ChannelType: "slack", Address: address, ApproverSet: true,
		PromptOutcome: &a2a.NotifyPromptOutcome{Outcome: a2a.NotifyOutcomeApproved, Text: text},
	})

	up := calls.LastUpdate()
	shown := calls.LastUpdateText() + up.Blocks
	for _, raw := range []string{"<!channel>", "<https://evil.example"} {
		if strings.Contains(shown, raw) {
			t.Errorf("the settled card carries %q unescaped, which Slack would act on: %s", raw, shown)
		}
	}
	if !strings.Contains(calls.LastUpdateText(), "&lt;!channel&gt;") {
		t.Errorf("settled text = %q, want the name escaped", calls.LastUpdateText())
	}
}

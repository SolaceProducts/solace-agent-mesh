// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package slack

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"

	goslack "github.com/slack-go/slack"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
)

func TestSlackApprovalCard_ALateClickCanSettleTheCardItCameFrom(t *testing.T) {
	a, calls, brk := newHandlerTestAdapter(t)
	replies := replyQueue(t, brk, "req-late")
	card := postApproverCard(t, a, replies, "req-late")
	posted, ok := a.buildHILBlocksFor(parseSurfaceT(t), slackHILValue{RequestID: "req-late", Notify: true, ApproverSet: true})
	if !ok {
		t.Fatal("buildHILBlocksFor ok=false")
	}
	posted = append(posted, goslack.NewContextBlock("", goslack.NewTextBlockObject(goslack.MarkdownType, expiryNotePrefix+"45 minutes", false, false)))
	submit := submitButtonValue(t, posted, "submit-btn")
	a.handleHILInput(goslack.InteractionCallback{
		User:      goslack.User{ID: "U-alice"},
		Container: goslack.Container{ChannelID: "C0OPS", MessageTs: card.TS},
		Message:   goslack.Message{Msg: goslack.Msg{Blocks: goslack.Blocks{BlockSet: posted}}},
		BlockActionState: &goslack.BlockActionStates{Values: map[string]map[string]goslack.BlockAction{
			"q0-picker": {"q0-picker-input": {SelectedOption: goslack.OptionBlockObject{Value: "Blue"}}},
		}},
	}, &goslack.BlockAction{ActionID: HILInputActionPrefix + "submit-btn", Value: submit})
	msg := replies(2 * time.Second)
	if msg == nil {
		t.Fatal("no reply was published")
	}
	var named slackCard
	if err := json.Unmarshal([]byte(msg.UserProperties[a2a.PropNotifyCard]), &named); err != nil {
		t.Fatalf("reply names no card: %v", msg.UserProperties)
	}
	if len(named.Blocks) == 0 || !strings.Contains(string(named.Blocks), "Choose") || strings.Contains(string(named.Blocks), HILInputActionPrefix) || strings.Contains(string(named.Blocks), expiryNotePrefix) {
		t.Fatalf("named blocks = %s, want the card's content without buttons or an expiry", named.Blocks)
	}

	address, _ := json.Marshal(named)
	refusal := "Your answer was not counted: this request was already settled. Approved by Dana Kim."
	a.handleNotifyDeliver(a2a.NotifyDeliver{
		RequestID: "req-late", Mode: a2a.NotifyModeNotify, ChannelType: "slack", Address: address, ApproverSet: true, Message: refusal,
		PromptOutcome: &a2a.NotifyPromptOutcome{Outcome: a2a.NotifyOutcomeApproved, Text: "Approved by Dana Kim", Refusal: refusal},
	})

	up := calls.LastUpdate()
	if calls.Updates() != 1 || up.TS != card.TS || strings.Contains(up.Blocks, HILInputActionPrefix) || !strings.Contains(up.Blocks, "Approved by Dana Kim") {
		t.Errorf("update = %+v (%d updates), want the clicked card settled without buttons", up, calls.Updates())
	}
	if e := calls.Ephemerals(); len(e) == 0 || e[len(e)-1].Text != refusal {
		t.Errorf("ephemerals = %+v, want the clicker told why", e)
	}
}

func TestSlackApprovalCard_ACardTooLargeToSettleIsLogged(t *testing.T) {
	a, _, _ := newHandlerTestAdapter(t)
	logs := &bytes.Buffer{}
	a.logger = slog.New(slog.NewJSONHandler(logs, nil))
	surface := json.RawMessage(strings.Replace(askSurfaceJSON, "Choose", strings.Repeat("a", 2*slackMaxSettledBlocksBytes), 1))

	a.handleNotifyDeliver(a2a.NotifyDeliver{
		RequestID: "req-huge", Mode: a2a.NotifyModeInput, ChannelType: "slack", Address: placeAddr(t), Surface: surface, ApproverSet: true,
	})

	if !strings.Contains(logs.String(), "posted card too large to settle in place") {
		t.Errorf("no warning that the card cannot be settled in place; log:\n%s", logs.String())
	}
}

func refusalShownTo(a *SlackGatewayAdapter, requestID, slackUserID string, retryable bool) {
	a.handleNotifyDeliver(a2a.NotifyDeliver{
		RequestID: requestID, Mode: a2a.NotifyModeNotify, ChannelType: "slack", ApproverSet: true,
		Address:       json.RawMessage(`{"channel_id":"C0OPS","ts":"1700000000.1","user_id":"` + slackUserID + `"}`),
		Message:       "refused",
		PromptOutcome: &a2a.NotifyPromptOutcome{Refusal: "refused", Retryable: retryable},
	})
}

func TestSlackApproverSet_ARetryAfterARefusalNoticeIsPublished(t *testing.T) {
	a, _, brk := newHandlerTestAdapter(t)
	replies := replyQueue(t, brk, "req-retry")

	approverSetClick(t, a, "req-retry", "U-alice")
	if replies(2*time.Second) == nil {
		t.Fatal("the first click was not published")
	}
	approverSetClick(t, a, "req-retry", "U-alice")
	if got := replies(300 * time.Millisecond); got != nil {
		t.Fatalf("a repeat click before any notice was published: %s", got.Payload)
	}

	refusalShownTo(a, "req-retry", "U-alice", true)
	approverSetClick(t, a, "req-retry", "U-alice")

	if replies(2*time.Second) == nil {
		t.Error("the retry click after a refusal notice was swallowed as a duplicate")
	}
}

func TestSlackApproverSet_ARefusalNoticeFreesOnlyItsOwnRequestAndUser(t *testing.T) {
	a, _, brk := newHandlerTestAdapter(t)
	one, two := replyQueue(t, brk, "req-one"), replyQueue(t, brk, "req-two")
	approverSetClick(t, a, "req-one", "U-alice")
	approverSetClick(t, a, "req-two", "U-alice")
	approverSetClick(t, a, "req-one", "U-bob")
	if one(2*time.Second) == nil || two(2*time.Second) == nil || one(2*time.Second) == nil {
		t.Fatal("clicks on different requests or by different people were swallowed as duplicates")
	}

	refusalShownTo(a, "req-one", "U-alice", true)
	approverSetClick(t, a, "req-two", "U-alice")
	approverSetClick(t, a, "req-one", "U-bob")

	if got := two(300 * time.Millisecond); got != nil {
		t.Errorf("another request's repeat click was freed by req-one's notice: %s", got.Payload)
	}
	if got := one(300 * time.Millisecond); got != nil {
		t.Errorf("another person's repeat click was freed by alice's notice: %s", got.Payload)
	}
}

func TestSlackApproverSet_AClickThatCouldNotBePublishedCanBeRetried(t *testing.T) {
	a, calls, brk := newHandlerTestAdapter(t)
	if err := brk.Disconnect(t.Context()); err != nil {
		t.Fatal(err)
	}
	approverSetClick(t, a, "req-publish-fails", "U-alice")
	if e := calls.Ephemerals(); len(e) != 1 || e[0].Text != msgHILAnswerNotSent {
		t.Fatalf("ephemerals = %+v, want the clicker told the answer was not sent", e)
	}
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatal(err)
	}
	replies := replyQueue(t, brk, "req-publish-fails")

	approverSetClick(t, a, "req-publish-fails", "U-alice")

	if replies(2*time.Second) == nil {
		t.Error("the retry after a failed publish was swallowed as a duplicate")
	}
}

func TestSlackApproverSet_ARefusalThatCannotBeRetriedKeepsTheRepeatClickSwallowed(t *testing.T) {
	a, _, brk := newHandlerTestAdapter(t)
	replies := replyQueue(t, brk, "req-not-approver")
	approverSetClick(t, a, "req-not-approver", "U-mallory")
	if replies(2*time.Second) == nil {
		t.Fatal("the first click was not published")
	}

	refusalShownTo(a, "req-not-approver", "U-mallory", false)
	approverSetClick(t, a, "req-not-approver", "U-mallory")

	if got := replies(300 * time.Millisecond); got != nil {
		t.Errorf("a click after a final refusal was published again: %s", got.Payload)
	}
}

func TestSlackApproverSet_ARepeatClickIsOnlySwallowedBrieflyBecauseAnotherPodMayHoldTheNotice(t *testing.T) {
	a, _, brk := newHandlerTestAdapter(t)
	a.approverClickTTL = 50 * time.Millisecond
	replies := replyQueue(t, brk, "req-ttl")
	approverSetClick(t, a, "req-ttl", "U-alice")
	if replies(2*time.Second) == nil {
		t.Fatal("the first click was not published")
	}

	time.Sleep(150 * time.Millisecond)
	approverSetClick(t, a, "req-ttl", "U-alice")

	if replies(2*time.Second) == nil {
		t.Error("a repeat click after the short dedup window was swallowed")
	}
}

func TestSlackApproverSet_AClickedCardTooLargeToKeepIsStillPublishedWithoutItsBlocks(t *testing.T) {
	a, _, brk := newHandlerTestAdapter(t)
	replies := replyQueue(t, brk, "req-big-card")
	posted, ok := a.buildHILBlocksFor(parseSurfaceT(t), slackHILValue{RequestID: "req-big-card", Notify: true, ApproverSet: true})
	if !ok {
		t.Fatal("buildHILBlocksFor ok=false")
	}
	huge := goslack.NewSectionBlock(goslack.NewTextBlockObject(goslack.MarkdownType, strings.Repeat("a", 2*slackMaxClickedBlocksBytes), false, false), nil, nil)
	posted = append([]goslack.Block{huge}, posted...)

	a.handleHILInput(goslack.InteractionCallback{
		User:      goslack.User{ID: "U-alice"},
		Container: goslack.Container{ChannelID: "C0OPS", MessageTs: "1700000000.1"},
		Message:   goslack.Message{Msg: goslack.Msg{Blocks: goslack.Blocks{BlockSet: posted}}},
		BlockActionState: &goslack.BlockActionStates{Values: map[string]map[string]goslack.BlockAction{
			"q0-picker": {"q0-picker-input": {SelectedOption: goslack.OptionBlockObject{Value: "Blue"}}},
		}},
	}, &goslack.BlockAction{ActionID: HILInputActionPrefix + "submit-btn", Value: submitButtonValue(t, posted, "submit-btn")})

	msg := replies(2 * time.Second)
	if msg == nil {
		t.Fatal("the click was not published")
	}
	var named slackCard
	if err := json.Unmarshal([]byte(msg.UserProperties[a2a.PropNotifyCard]), &named); err != nil {
		t.Fatalf("reply names no card: %v", msg.UserProperties)
	}
	if named.ChannelID != "C0OPS" || named.TS != "1700000000.1" || named.UserID != "U-alice" || len(named.Blocks) != 0 {
		t.Errorf("named card = %+v, want the clicked message and the clicker without blocks over the cap", named)
	}
}

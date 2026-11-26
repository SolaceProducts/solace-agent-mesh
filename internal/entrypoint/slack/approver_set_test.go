// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package slack

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	goslack "github.com/slack-go/slack"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
	"github.com/SolaceProducts/solace-agent-mesh/pkg/slackapi"
)

func placeAddr(t *testing.T) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(map[string]string{"channel_id": "C0OPS"})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestSlackApproverSet_PostsInThePlaceNotADM(t *testing.T) {
	a, calls, _ := newHandlerTestAdapter(t)
	a.handleNotifyDeliver(a2a.NotifyDeliver{
		RequestID: "req-place", Mode: a2a.NotifyModeInput, ChannelType: "slack",
		Address: placeAddr(t), Surface: json.RawMessage(askSurfaceJSON), ApproverSet: true,
	})
	if calls.Posts() != 1 || calls.LastPostChannel() != "C0OPS" {
		t.Fatalf("posts = %d to %q, want one post in C0OPS", calls.Posts(), calls.LastPostChannel())
	}
	if got := calls.OpenDMUsers(); len(got) != 0 {
		t.Errorf("a place post opened a DM for %v", got)
	}

	a.handleNotifyDeliver(a2a.NotifyDeliver{
		RequestID: "req-place", Mode: a2a.NotifyModeNotify, ChannelType: "slack",
		Address: placeAddr(t), Title: "Approval request answered", Message: "Answered by alice@example.com via slack (approved).", ApproverSet: true,
	})
	if calls.Posts() != 2 || calls.LastPostChannel() != "C0OPS" {
		t.Errorf("the resolution notice was not posted in the place")
	}
}

func TestSlackApproverSet_PlaceWithoutChannelDropped(t *testing.T) {
	a, calls, _ := newHandlerTestAdapter(t)
	a.handleNotifyDeliver(a2a.NotifyDeliver{
		RequestID: "req-bad", Mode: a2a.NotifyModeInput, ChannelType: "slack",
		Address: json.RawMessage(`{"user_id":"U1"}`), Surface: json.RawMessage(askSurfaceJSON), ApproverSet: true,
	})
	if calls.Posts() != 0 || len(calls.OpenDMUsers()) != 0 {
		t.Errorf("an unreadable place address was delivered somewhere")
	}
}

func approverSetClick(t *testing.T, a *SlackGatewayAdapter, requestID, slackUserID string) {
	t.Helper()
	blocks, ok := a.buildHILBlocksFor(parseSurfaceT(t), slackHILValue{RequestID: requestID, Notify: true, ApproverSet: true})
	if !ok {
		t.Fatal("buildHILBlocksFor ok=false")
	}
	a.handleHILInput(goslack.InteractionCallback{
		User:      goslack.User{ID: slackUserID},
		Container: goslack.Container{ChannelID: "C0OPS", MessageTs: "1700000000.1"},
		BlockActionState: &goslack.BlockActionStates{Values: map[string]map[string]goslack.BlockAction{
			"q0-picker": {"q0-picker-input": {SelectedOption: goslack.OptionBlockObject{Value: "Blue"}}},
		}},
	}, &goslack.BlockAction{ActionID: HILInputActionPrefix + "submit-btn", Value: submitButtonValue(t, blocks, "submit-btn")})
}

func replyQueue(t *testing.T, brk broker.Broker, requestID string) func(time.Duration) *broker.Message {
	t.Helper()
	q, err := brk.CreateQueue("reply-"+requestID, broker.QueueOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := brk.Subscribe(q, a2a.NotifyReplyTopic("test-ns", requestID)); err != nil {
		t.Fatal(err)
	}
	return func(timeout time.Duration) *broker.Message {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		msg, err := brk.Receive(ctx, q)
		if err != nil {
			return nil
		}
		return msg
	}
}

func TestSlackApproverSet_ClickAnswersAsTheVerifiedClicker(t *testing.T) {
	a, calls, brk := newHandlerTestAdapter(t)
	replies := replyQueue(t, brk, "req-click")

	approverSetClick(t, a, "req-click", "U-alice")

	msg := replies(2 * time.Second)
	if msg == nil {
		t.Fatal("no reply was published")
	}
	if msg.UserProperties[a2a.PropUserID] != "test@test.com" || msg.UserProperties[a2a.PropChannel] != "slack" {
		t.Errorf("reply properties = %v, want the clicker's profile email via slack", msg.UserProperties)
	}
	if calls.Updates() != 0 {
		t.Error("the prompt was marked answered before the notification service decided")
	}
	if e := calls.Ephemerals(); len(e) != 1 || e[0].User != "U-alice" || e[0].Text != msgHILAnswerSent {
		t.Errorf("ephemerals = %+v, want one telling the clicker the answer was sent", e)
	}

	approverSetClick(t, a, "req-click", "U-bob")
	if msg := replies(2 * time.Second); msg == nil {
		t.Error("a second person's click on the same prompt was swallowed as a duplicate")
	}
}

func TestSlackApproverSet_UnmappedClickerRefusedVisibly(t *testing.T) {
	a, calls, brk := newHandlerTestAdapter(t)
	calls.noProfileEmail.Store(true)
	replies := replyQueue(t, brk, "req-unmapped")

	approverSetClick(t, a, "req-unmapped", "U-guest")

	if msg := replies(300 * time.Millisecond); msg != nil {
		t.Fatalf("an unmapped click was published: %s", msg.Payload)
	}
	if e := calls.Ephemerals(); len(e) != 1 || e[0].User != "U-guest" || e[0].Text != msgHILUnverifiedClicker {
		t.Errorf("ephemerals = %+v, want one refusal shown to the clicker", e)
	}
}

func TestSlackApproverSet_AdvertisedOnlyWithTheFlag(t *testing.T) {
	for _, on := range []bool{false, true} {
		brk := broker.NewDevBroker()
		if err := brk.Connect(t.Context()); err != nil {
			t.Fatal(err)
		}
		reg := features.NewRegistry()
		reg.Register(
			features.Definition{Key: features.KeyNotifyService, Default: true},
			features.Definition{Key: features.KeyHILApproverSets, Default: on},
		)
		core := entrypoint.NewGatewayCore(&recordingGWAdapter{}, map[string]any{"namespace": "test-ns", "gateway_id": "slack-gw"})
		core.SetServices(runtime.NewRuntime(brk, nil, runtime.WithFeatureRegistry(reg)).Services())
		a := &SlackGatewayAdapter{core: core, logger: testLogger(), api: slackapi.NewInMemorySlackAPI()}
		a.startNotifyDeliverLoop(t.Context())
		if got := core.AdvertisedNotifyCapabilities(); got == nil || got.ApproverSets != on {
			t.Errorf("flag %v: advertised = %+v", on, got)
		}
		_ = brk.Disconnect(context.Background())
	}
}

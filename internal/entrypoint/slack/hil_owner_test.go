// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package slack

import (
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	goslack "github.com/slack-go/slack"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
)

const hilOwnerAgent = "TestAgent"

func submitValueFor(t *testing.T, a *SlackGatewayAdapter, requestID, taskID string) string {
	t.Helper()
	blocks, ok := a.buildHILBlocks(parseSurfaceT(t), requestID, taskID, hilOwnerAgent, false)
	if !ok {
		t.Fatal("buildHILBlocks ok=false")
	}
	return submitButtonValue(t, blocks, "submit-btn")
}

func subscribeAgent(t *testing.T, brk *broker.DevBroker, name string) broker.Queue {
	t.Helper()
	q, err := brk.CreateQueue(name, broker.QueueOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err = brk.Subscribe(q, a2a.AgentRequestTopic("test-ns", hilOwnerAgent)); err != nil {
		t.Fatal(err)
	}
	return q
}

func askedTask(t *testing.T, a *SlackGatewayAdapter, taskID, owner, channel, slackUser string) {
	t.Helper()
	if err := a.core.Tasks().Register(taskID, hilOwnerAgent, owner, entrypoint.TaskIdentity{}); err != nil {
		t.Fatal(err)
	}
	a.core.Tasks().SetMetadata(taskID, &SlackTaskMeta{ChannelID: channel, UserID: slackUser, AgentName: hilOwnerAgent, OwnerID: owner})
}

func clickAs(a *SlackGatewayAdapter, slackUser, channel, value string) {
	a.handleHILInput(goslack.InteractionCallback{
		User:      goslack.User{ID: slackUser, Name: "clicker"},
		Container: goslack.Container{ChannelID: channel, MessageTs: "1700000000.9"},
		BlockActionState: &goslack.BlockActionStates{
			Values: map[string]map[string]goslack.BlockAction{
				"q0-picker": {"q0-picker-input": {SelectedOption: goslack.OptionBlockObject{Value: "Blue"}}},
			},
		},
	}, &goslack.BlockAction{ActionID: HILInputActionPrefix + "submit-btn", Value: value})
}

func receiveFrom(t *testing.T, brk *broker.DevBroker, q broker.Queue, wait time.Duration) *broker.Message {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), wait)
	defer cancel()
	msg, err := brk.Receive(ctx, q)
	if err != nil {
		return nil
	}
	return msg
}

func TestSlackHILClickOutsideWhereTheTaskWasAskedIsRefusedWithoutSideEffects(t *testing.T) {
	a, calls, brk := newHandlerTestAdapter(t)
	a.cfg.UseUserIdentityInChannels = true
	const taskID = "task-owned"
	askedTask(t, a, taskID, "owner@dev.local", "C123", "U-asker")
	q := subscribeAgent(t, brk, "hil-owner-refused")
	value := submitValueFor(t, a, "req-owned", taskID)
	postsBefore := calls.Posts()

	for _, tc := range []struct{ name, user, channel string }{
		{"another user in the conversation the task was asked in", "U-other", "C123"},
		{"another user in a 1:1 chat naming the task from a replayed card", "U-other", "D999"},
		{"the asker from a conversation the task was not asked in", "U-asker", "D999"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			noticesBefore := len(calls.Ephemerals())

			clickAs(a, tc.user, tc.channel, value)

			if msg := receiveFrom(t, brk, q, 200*time.Millisecond); msg != nil {
				t.Fatalf("the click was forwarded to the agent: %s", msg.Payload)
			}
			notices := calls.Ephemerals()[noticesBefore:]
			if len(notices) != 1 || notices[0] != (ephemeralCall{Channel: tc.channel, User: tc.user, Text: msgHILNotYours}) {
				t.Errorf("notices after the refused click = %+v, want exactly one telling %s in %s that %q", notices, tc.user, tc.channel, msgHILNotYours)
			}
			if got := calls.Updates(); got != 0 {
				t.Errorf("the click replaced the card (%d chat.update calls)", got)
			}
			if got := calls.Posts() - postsBefore; got != 0 {
				t.Errorf("the click posted %d messages (a retry notice means it was claimed before it was refused)", got)
			}
		})
	}

	clickAs(a, "U-asker", "C123", value)

	msg := receiveFrom(t, brk, q, 2*time.Second)
	if msg == nil {
		t.Fatal("the asker's answer after the refused clicks was dropped: a refusal took the dedup or the claim")
	}
	if got := a2a.DecodeUserProperties(msg.UserProperties).UserID; got != "owner@dev.local" {
		t.Errorf("answer attributed to %q, want the task's user", got)
	}
}

func TestSlackHILClickFromWhereTheTaskWasAskedAnswersAsTheAskerWhateverTheLookupReturned(t *testing.T) {
	a, _, brk := newHandlerTestAdapter(t)
	const taskID = "task-dm"
	askedTask(t, a, taskID, "slack:T1:U1", "D123", "U1")
	q := subscribeAgent(t, brk, "hil-owner-dm")

	clickAs(a, "U1", "D123", submitValueFor(t, a, "req-dm", taskID))

	msg := receiveFrom(t, brk, q, 2*time.Second)
	if msg == nil {
		t.Fatal("the asker's own click was not forwarded")
	}
	if got := a2a.DecodeUserProperties(msg.UserProperties).UserID; got != "slack:T1:U1" {
		t.Errorf("answer attributed to %q, want the task's user slack:T1:U1", got)
	}
}

func TestSlackHILClickWithNoRecordOfWhereTheTaskWasAskedIsRefused(t *testing.T) {
	a, calls, brk := newHandlerTestAdapter(t)
	logs := &lockedBuffer{}
	a.logger = slog.New(slog.NewJSONHandler(logs, nil))
	const taskID = "task-no-record"
	if err := a.core.Tasks().Register(taskID, hilOwnerAgent, "slack:T1:U1", entrypoint.TaskIdentity{}); err != nil {
		t.Fatal(err)
	}
	q := subscribeAgent(t, brk, "hil-owner-no-record")

	clickAs(a, "U1", "D123", submitValueFor(t, a, "req-no-record", taskID))

	if msg := receiveFrom(t, brk, q, 200*time.Millisecond); msg != nil {
		t.Fatalf("a click with no recorded place was forwarded as the asker's: %s", msg.Payload)
	}
	if got := calls.Ephemerals(); len(got) != 1 || got[0].User != "U1" || got[0].Text != msgHILNotYours {
		t.Errorf("notices = %+v, want one telling U1 that %q", got, msgHILNotYours)
	}
	if !strings.Contains(logs.String(), "no record of where the task was asked") {
		t.Errorf("the missing record was not logged:\n%s", logs.String())
	}
}

func TestSlackHILClickByAnotherUserInTheAskersDMIsRefused(t *testing.T) {
	a, _, brk := newHandlerTestAdapter(t)
	askedTask(t, a, "task-dm-2", "slack:T1:U1", "D123", "U1")
	q := subscribeAgent(t, brk, "hil-owner-dm-other")

	clickAs(a, "U-other", "D123", submitValueFor(t, a, "req-dm-2", "task-dm-2"))

	if msg := receiveFrom(t, brk, q, 200*time.Millisecond); msg != nil {
		t.Fatalf("a different user's click was forwarded as the asker's: %s", msg.Payload)
	}
}

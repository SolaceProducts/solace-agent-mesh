// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package whatsapp

import (
	"strconv"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
)

func TestSenderGate_TokenBucket(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	g := &senderGate{now: func() time.Time { return now }}

	for i := range senderBurst {
		if !g.admitMessage("15551234567") {
			t.Fatalf("message %d of the initial burst was refused", i+1)
		}
	}
	if g.admitMessage("15551234567") {
		t.Fatal("a message past the burst was admitted; the bucket is not limiting")
	}
	if !g.admitMessage("15559999999") {
		t.Fatal("a second sender was refused because the first exhausted its bucket")
	}

	now = now.Add(time.Minute)
	admitted := 0
	for range senderMessagesPerMinute + 1 {
		if g.admitMessage("15551234567") {
			admitted++
		}
	}
	if admitted != senderBurst {
		t.Errorf("after a minute idle the bucket admitted %d, want %d (capped at the burst size)", admitted, senderBurst)
	}
}

func TestSenderGate_TaskLimit(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	g := &senderGate{now: func() time.Time { return now }}
	const sender = "15551234567"

	for i := range maxInFlightTasksPerSender {
		if !g.admitTask(sender) {
			t.Fatalf("task %d was refused below the cap", i+1)
		}
		g.taskStarted(sender, "task-"+string(rune('a'+i)))
	}
	if g.admitTask(sender) {
		t.Fatal("a task past the cap was admitted")
	}
	if !g.admitTask("15559999999") {
		t.Fatal("a second sender was refused because the first is at its cap")
	}

	g.taskFinished(sender, "task-a")
	if !g.admitTask(sender) {
		t.Fatal("finishing a task did not free its slot")
	}

	now = now.Add(inFlightTaskTTL + time.Second)
	for i := range maxInFlightTasksPerSender {
		if !g.admitTask(sender) {
			t.Fatalf("slot %d never freed after the in-flight TTL lapsed", i+1)
		}
		g.taskStarted(sender, "later-"+string(rune('a'+i)))
	}
}

func TestInbound_TaskLimitDeclines(t *testing.T) {
	t.Parallel()
	mock := newWAMock(t)
	a := newAdapterRig(t, mock, "Builder")
	const sender = "15551230001"

	for i := range maxInFlightTasksPerSender {
		a.senders.taskStarted(sender, "busy-"+string(rune('a'+i)))
	}

	msg := &inboundMessage{From: sender, ID: "wamid.OVER", Type: "text"}
	msg.Text.Body = "one more thing"
	if got := a.deliver(&webhookChangeValue{}, msg); got != settleAck {
		t.Fatalf("capped message settled %v, want settleAck", got)
	}
	if got := replyBodies(t, a, mock); len(got) != 1 || got[0] != msgTooManyTasks {
		t.Errorf("replies = %v, want the concurrent-task notice", got)
	}

	seedPendingPrompt(t, a, sender, store.WhatsAppPendingPrompt{TaskID: "busy-a", RequestID: "req-1"})
	answer := &inboundMessage{From: sender, ID: "wamid.ANSWER", Type: "image",
		Image: &mediaObject{ID: "media-1", MimeType: "image/png"}}
	a.deliver(&webhookChangeValue{}, answer)

	got := replyBodies(t, a, mock)
	if len(got) != 2 || got[1] != msgReplyToQuestion {
		t.Errorf("replies = %v, want the pending-prompt nudge second — a message answering an open prompt must not hit the concurrency cap", got)
	}
}

func replyBodies(t *testing.T, a *Adapter, mock *waMock) []string {
	t.Helper()
	waitQueueDrained(t, a)
	var out []string
	for _, sent := range mock.calls() {
		if sent.Text.Body != "" {
			out = append(out, sent.Text.Body)
		}
	}
	return out
}

func TestWarnThrottle_ForgetsLapsedKeys(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	w := &warnThrottle{now: func() time.Time { return now }}
	const window = time.Minute

	for i := range warnThrottleKeyCap {
		w.shouldWarn("lapsed-"+strconv.Itoa(i), window)
	}
	if got := len(w.lastWarn); got != warnThrottleKeyCap {
		t.Fatalf("tracked keys = %d, want %d before anything lapses", got, warnThrottleKeyCap)
	}

	now = now.Add(window + time.Second)
	w.shouldWarn("live", window)
	w.shouldWarn("live", window)

	w.shouldWarn("fresh", window)
	if got := len(w.lastWarn); got > 3 {
		t.Errorf("tracked keys = %d after the sweep, want only the recent ones", got)
	}

	now = now.Add(window + time.Second)
	warn, suppressed, _ := w.shouldWarn("live", window)
	if !warn {
		t.Fatal("a key whose window lapsed must warn again")
	}
	if suppressed != 1 {
		t.Errorf("suppressedSinceLast = %d, want 1 — an active key's count must survive the sweep", suppressed)
	}
}

func TestInbound_RateLimitChargesOncePerMessage(t *testing.T) {
	t.Parallel()
	const agentName = "Builder"
	mock := newWAMock(t)
	a := newAdapterRig(t, mock, agentName)
	a.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: agentName})
	a.core.SetScopeResolver(waScopeResolver{scopes: nil})

	if maxAuthzDenialAttempts < senderBurst {
		t.Skipf("retry budget %d no longer exceeds the burst size %d; the test cannot distinguish the two",
			maxAuthzDenialAttempts, senderBurst)
	}

	retried, retriedMsg := textMessage("wamid.RETRIED", "15551230000", "hello")
	for range maxAuthzDenialAttempts + 1 {
		if a.deliver(retried, retriedMsg) == settleAck {
			break
		}
	}

	fresh, freshMsg := textMessage("wamid.FRESH", "15551230000", "hello again")
	a.deliver(fresh, freshMsg)

	for _, body := range replyBodies(t, a, mock) {
		if body == msgTooManyMessages {
			t.Fatal("a sender was rate-limited by their own message's redeliveries")
		}
	}
}

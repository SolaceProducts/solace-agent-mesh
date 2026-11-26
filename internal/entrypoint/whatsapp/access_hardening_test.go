// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package whatsapp

import (
	"log/slog"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
)

func allowEverySubmit(t *testing.T, a *Adapter, agentName string) {
	t.Helper()
	a.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: agentName})
	a.core.SetScopeResolver(waScopeResolver{scopes: []string{"*"}})
}

func liveTaskIDs(a *Adapter, sender string) []string {
	a.senders.mu.Lock()
	defer a.senders.mu.Unlock()
	out := make([]string, 0, len(a.senders.inFlight[sender]))
	for id := range a.senders.inFlight[sender] {
		out = append(out, id)
	}
	return out
}

func TestInbound_TaskCapIsWiredToRealSubmits(t *testing.T) {
	t.Parallel()
	const agentName, sender = "Builder", "15551230001"
	mock := newWAMock(t)
	a := newAdapterRig(t, mock, agentName)
	allowEverySubmit(t, a, agentName)

	if maxInFlightTasksPerSender >= senderBurst {
		t.Skipf("cap %d is not reachable within burst %d", maxInFlightTasksPerSender, senderBurst)
	}

	for i := range maxInFlightTasksPerSender {
		value, msg := textMessage("wamid.OK"+strconv.Itoa(i), sender, "hello")
		if got := a.deliver(value, msg); got != settleAck {
			t.Fatalf("message %d settled %v, want settleAck", i, got)
		}
	}
	if got := len(liveTaskIDs(a, sender)); got != maxInFlightTasksPerSender {
		t.Fatalf("in-flight tasks = %d, want %d — trySubmit is not counting the tasks it creates",
			got, maxInFlightTasksPerSender)
	}

	value, msg := textMessage("wamid.OVER", sender, "one more")
	if got := a.deliver(value, msg); got != settleAck {
		t.Fatalf("capped message settled %v, want settleAck", got)
	}
	if got := len(liveTaskIDs(a, sender)); got != maxInFlightTasksPerSender {
		t.Errorf("in-flight tasks = %d after the capped message, want %d — it must not have created a task",
			got, maxInFlightTasksPerSender)
	}
	replies := replyBodies(t, a, mock)
	if len(replies) == 0 || replies[len(replies)-1] != msgTooManyTasks {
		t.Errorf("replies = %v, want the concurrent-task notice last", replies)
	}
}

func TestInbound_TaskSlotIsReleasedOnFinalResponse(t *testing.T) {
	t.Parallel()
	const agentName, sender = "Builder", "15551230002"
	mock := newWAMock(t)
	a := newAdapterRig(t, mock, agentName)
	allowEverySubmit(t, a, agentName)

	if maxInFlightTasksPerSender >= senderBurst {
		t.Skipf("cap %d is not reachable within burst %d", maxInFlightTasksPerSender, senderBurst)
	}
	for i := range maxInFlightTasksPerSender {
		value, msg := textMessage("wamid.FILL"+strconv.Itoa(i), sender, "hello")
		a.deliver(value, msg)
	}
	ids := liveTaskIDs(a, sender)
	if len(ids) != maxInFlightTasksPerSender {
		t.Fatalf("in-flight tasks = %d, want %d", len(ids), maxInFlightTasksPerSender)
	}

	done := &a2a.Task{}
	done.Status.State = a2a.TaskStateCompleted
	a.OnFinalResponse(t.Context(), ids[0], done, nil)

	if got := len(liveTaskIDs(a, sender)); got != maxInFlightTasksPerSender-1 {
		t.Fatalf("in-flight tasks = %d after a terminal task, want %d — the slot was not released",
			got, maxInFlightTasksPerSender-1)
	}

	before := len(liveTaskIDs(a, sender))
	value, msg := textMessage("wamid.AFTER", sender, "next question")
	a.deliver(value, msg)
	if got := len(liveTaskIDs(a, sender)); got != before+1 {
		t.Errorf("in-flight tasks = %d, want %d — a sender under the cap must be admitted again", got, before+1)
	}
}

func TestInbound_TaskCapCoversEveryTaskCreatingType(t *testing.T) {
	t.Parallel()
	for _, msgType := range []string{"text", "image", "document", "video", "audio"} {
		t.Run(msgType, func(t *testing.T) {
			t.Parallel()
			if !createsTask(msgType) {
				t.Fatalf("createsTask(%q) = false; %q reaches an agent as a new task, so the cap must gate it", msgType, msgType)
			}
		})
	}
	for _, msgType := range []string{"reaction", "system", "unsupported"} {
		t.Run("not_"+msgType, func(t *testing.T) {
			t.Parallel()
			if createsTask(msgType) {
				t.Errorf("createsTask(%q) = true; it is declined without a task, so counting it would refuse real work", msgType)
			}
		})
	}
}

func TestRefusalLog_MasksSenderAndWarnsOnce(t *testing.T) {
	t.Parallel()
	const sender = "15551239876"
	logs := &recordCollector{}
	mock := newWAMock(t)
	a := newAdapterRig(t, mock, "Builder")
	a.logger = slog.New(logs)
	for range senderBurst {
		a.senders.admitMessage(sender)
	}

	value, msg := textMessage("wamid.REFUSED", sender, "let me in")
	a.deliver(value, msg)

	var levels []slog.Level
	records := logs.all()
	for _, r := range records {
		if !strings.Contains(r.Message, "refused") {
			continue
		}
		levels = append(levels, r.Level)
		r.Attrs(func(at slog.Attr) bool {
			if strings.Contains(at.Value.String(), sender) {
				t.Errorf("refusal log field %q carries the sender in the clear: %q", at.Key, at.Value.String())
			}
			return true
		})
	}
	if len(levels) == 0 {
		t.Fatalf("no refusal was logged; records = %d", len(records))
	}
	if levels[0] < slog.LevelWarn {
		t.Errorf("first refusal logged at %v, want >= WARN — a security rejection is never lower", levels[0])
	}
}

func TestAuditRefused_RecordsEveryReason(t *testing.T) {
	t.Parallel()
	const sender = "15551234321"
	for _, reason := range []string{
		refusalRateLimited, refusalTaskLimit, refusalUploadsDisabled,
	} {
		t.Run(reason, func(t *testing.T) {
			t.Parallel()
			logs := &recordCollector{}
			a := &Adapter{logger: slog.New(logs)}
			a.auditRefused(&inboundMessage{From: sender, ID: "wamid.A", Type: "text"}, reason)

			var found bool
			records := logs.all()
			for _, r := range records {
				if r.Message != auditMsg {
					continue
				}
				fields := map[string]string{}
				r.Attrs(func(at slog.Attr) bool {
					fields[at.Key] = at.Value.String()
					return true
				})
				if fields["disposition"] != "refused" || fields["refusalReason"] != reason {
					continue
				}
				found = true
				if fields["userFP"] == "" || strings.Contains(fields["userFP"], sender) {
					t.Errorf("userFP = %q, want a fingerprint that does not carry the number", fields["userFP"])
				}
			}
			if !found {
				t.Errorf("no audit record with refusalReason=%q; records = %d", reason, len(records))
			}
		})
	}
}

func TestWarnThrottle_BoundsKeysThatNeverLapse(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	w := &warnThrottle{now: func() time.Time { return now }}
	const window = time.Hour

	for i := range warnThrottleKeyCap * 2 {
		now = now.Add(time.Second)
		w.shouldWarn("sender-"+strconv.Itoa(i), window)
	}
	if got := len(w.lastWarn); got > warnThrottleKeyCap {
		t.Errorf("tracked keys = %d, want <= %d — nothing lapsed, so the cap is the only bound",
			got, warnThrottleKeyCap)
	}
	if got := len(w.suppressed); got > warnThrottleKeyCap {
		t.Errorf("suppressed keys = %d, want <= %d", got, warnThrottleKeyCap)
	}
}

func TestWarnThrottle_SaturatedMapDoesNotReflect(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	w := &warnThrottle{now: func() time.Time { return now }}
	const window = time.Hour

	warned := 0
	for i := range warnThrottleKeyCap * 4 {
		now = now.Add(10 * time.Millisecond)
		if warn, _, _ := w.shouldWarn("rotating-"+strconv.Itoa(i), window); warn {
			warned++
		}
	}
	if warned > warnThrottleKeyCap {
		t.Errorf("notices = %d for %d rotating senders, want <= %d — past the cap a new sender must not draw a reply",
			warned, warnThrottleKeyCap*4, warnThrottleKeyCap)
	}

	now = now.Add(2 * window)
	if warn, _, _ := w.shouldWarn("rotating-0", window); !warn {
		t.Error("a tracked sender whose window lapsed was not warned; the bound must not mute an ongoing episode")
	}
}

func TestSenderGate_PrunesAbandonedInFlightTasks(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	g := &senderGate{now: func() time.Time { return now }}

	for i := range senderGateKeyCap {
		sender := "abandoned-" + strconv.Itoa(i)
		g.admitMessage(sender)
		g.taskStarted(sender, "task-"+strconv.Itoa(i))
	}

	now = now.Add(inFlightTaskTTL + time.Hour)
	g.admitMessage("a-new-sender")

	g.mu.Lock()
	buckets, inFlight := len(g.buckets), len(g.inFlight)
	g.mu.Unlock()
	if inFlight > 1 {
		t.Errorf("in-flight senders = %d, want the abandoned ones forgotten", inFlight)
	}
	if buckets > senderGateKeyCap {
		t.Errorf("buckets = %d, want <= %d — abandoned tasks were pinning them", buckets, senderGateKeyCap)
	}
}

func TestSenderGate_SaturationNeverGrantsUnearnedAllowance(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	g := &senderGate{now: func() time.Time { return now }}

	drained := make([]string, 0, senderGateKeyCap)
	for i := range senderGateKeyCap {
		sender := "drained-" + strconv.Itoa(i)
		drained = append(drained, sender)
		for range senderBurst {
			g.admitMessage(sender)
		}
	}
	for _, sender := range drained {
		if g.admitMessage(sender) {
			t.Fatalf("%s still had allowance before the bound was exercised", sender)
		}
	}

	g.admitMessage("a-new-sender")

	g.mu.Lock()
	buckets := len(g.buckets)
	g.mu.Unlock()
	if buckets > senderGateKeyCap {
		t.Errorf("buckets = %d, want <= %d", buckets, senderGateKeyCap)
	}

	var regranted int
	for _, sender := range drained {
		if g.admitMessage(sender) {
			regranted++
		}
	}
	if regranted != 0 {
		t.Errorf("%d of %d throttled senders were admitted again with zero elapsed time — the bound reset their rate limit",
			regranted, len(drained))
	}
}

func TestSenderGate_SaturatedGateRefusesRatherThanGrows(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	g := &senderGate{now: func() time.Time { return now }}

	for i := range senderGateKeyCap {
		g.admitMessage("holder-" + strconv.Itoa(i))
	}
	if got := g.admitMessage("stranger"); got {
		t.Error("a new sender was admitted into a saturated gate; they would be tracked by nothing and rate-limited by nothing")
	}
	g.mu.Lock()
	buckets := len(g.buckets)
	g.mu.Unlock()
	if buckets > senderGateKeyCap {
		t.Errorf("buckets = %d, want <= %d", buckets, senderGateKeyCap)
	}

	now = now.Add(time.Hour)
	if got := g.admitMessage("stranger"); !got {
		t.Error("a new sender is still refused after every holder went idle; the gate must drain")
	}
}

func TestInbound_UploadsDisabledDeclineIsWindowed(t *testing.T) {
	t.Parallel()
	const sender = "15551230003"
	mock := newWAMock(t)
	a := newAdapterRig(t, mock, "Builder")
	a.cfg.DisableFileUploads = true

	for i := range 4 {
		msg := &inboundMessage{
			From: sender, ID: "wamid.MEDIA" + strconv.Itoa(i), Type: "image",
			Image: &mediaObject{ID: "media-" + strconv.Itoa(i), MimeType: "image/png"},
		}
		if got := a.deliver(&webhookChangeValue{}, msg); got != settleAck {
			t.Fatalf("declined media %d settled %v, want settleAck", i, got)
		}
	}

	var notices int
	for _, body := range replyBodies(t, a, mock) {
		if body == msgUploadsDisabled {
			notices++
		}
	}
	if notices != 1 {
		t.Errorf("upload-disabled notices = %d, want exactly 1 per window", notices)
	}
	if calls := mock.mediaRequests(); calls != 0 {
		t.Errorf("media requests = %d, want 0 — the bytes must never be fetched", calls)
	}
}

func TestInbound_TaskCapGatesMediaAtTheCallSite(t *testing.T) {
	t.Parallel()
	const agentName, sender = "Builder", "15551230004"
	mock := newWAMock(t)
	a := newAdapterRig(t, mock, agentName)
	allowEverySubmit(t, a, agentName)

	if maxInFlightTasksPerSender >= senderBurst {
		t.Skipf("cap %d is not reachable within burst %d", maxInFlightTasksPerSender, senderBurst)
	}
	for i := range maxInFlightTasksPerSender {
		value, msg := textMessage("wamid.FILL"+strconv.Itoa(i), sender, "hello")
		a.deliver(value, msg)
	}
	if got := len(liveTaskIDs(a, sender)); got != maxInFlightTasksPerSender {
		t.Fatalf("in-flight tasks = %d, want the cap filled with %d", got, maxInFlightTasksPerSender)
	}

	before := mock.mediaRequests()
	media := &inboundMessage{
		From: sender, ID: "wamid.MEDIA-OVER", Type: "image",
		Image: &mediaObject{ID: "MEDIA-OVER", MimeType: "image/png", Caption: "and this one"},
	}
	if got := a.deliver(&webhookChangeValue{}, media); got != settleAck {
		t.Fatalf("capped media settled %v, want settleAck", got)
	}

	if got := len(liveTaskIDs(a, sender)); got != maxInFlightTasksPerSender {
		t.Errorf("in-flight tasks = %d, want %d — a media message at the cap must not create a task",
			got, maxInFlightTasksPerSender)
	}
	if got := mock.mediaRequests(); got != before {
		t.Errorf("media requests = %d, want %d — a message refused by the cap must not reach the Graph download",
			got, before)
	}
	replies := replyBodies(t, a, mock)
	if len(replies) == 0 || replies[len(replies)-1] != msgTooManyTasks {
		t.Errorf("replies = %v, want the concurrent-task notice for the media message", replies)
	}
}

func TestInbound_RateLimitVerdictIsEnforced(t *testing.T) {
	t.Parallel()
	const sender = "15551230005"
	mock := newWAMock(t)
	a := newAdapterRig(t, mock, "Builder")

	for i := range senderBurst {
		msg := &inboundMessage{From: sender, ID: "wamid.R" + strconv.Itoa(i), Type: "reaction"}
		if got := a.deliver(&webhookChangeValue{}, msg); got != settleAck {
			t.Fatalf("reaction %d settled %v, want settleAck", i, got)
		}
	}
	if got := replyBodies(t, a, mock); len(got) != 0 {
		t.Fatalf("replies within the burst allowance = %v, want none", got)
	}

	over := &inboundMessage{From: sender, ID: "wamid.OVER", Type: "reaction"}
	if got := a.deliver(&webhookChangeValue{}, over); got != settleAck {
		t.Fatalf("throttled message settled %v, want settleAck", got)
	}
	replies := replyBodies(t, a, mock)
	if len(replies) != 1 || replies[0] != msgTooManyMessages {
		t.Errorf("replies = %v, want exactly the backpressure notice — the rate verdict is not enforced", replies)
	}
}

func TestInbound_TaskSlotIsReleasedOnError(t *testing.T) {
	t.Parallel()
	const agentName, sender = "Builder", "15551230006"
	mock := newWAMock(t)
	a := newAdapterRig(t, mock, agentName)
	allowEverySubmit(t, a, agentName)

	if maxInFlightTasksPerSender >= senderBurst {
		t.Skipf("cap %d is not reachable within burst %d", maxInFlightTasksPerSender, senderBurst)
	}
	for i := range maxInFlightTasksPerSender {
		value, msg := textMessage("wamid.ERR"+strconv.Itoa(i), sender, "hello")
		a.deliver(value, msg)
	}
	ids := liveTaskIDs(a, sender)
	if len(ids) != maxInFlightTasksPerSender {
		t.Fatalf("in-flight tasks = %d, want the cap filled", len(ids))
	}

	for _, id := range ids {
		a.OnError(t.Context(), id, &a2a.Error{Code: -32000, Message: "agent unavailable"}, nil)
	}
	if got := len(liveTaskIDs(a, sender)); got != 0 {
		t.Fatalf("in-flight tasks = %d after every task errored, want 0 — a failed task holds its slot for the whole TTL", got)
	}

	value, msg := textMessage("wamid.RETRY", sender, "try again")
	a.deliver(value, msg)
	if got := len(liveTaskIDs(a, sender)); got != 1 {
		t.Errorf("in-flight tasks = %d, want 1 — the sender must be able to retry after a failure", got)
	}
}

func TestRefuse_SaturationIsVisibleToTheOperator(t *testing.T) {
	t.Parallel()
	logs := &recordCollector{}
	mock := newWAMock(t)
	a := newAdapterRig(t, mock, "Builder")
	a.logger = slog.New(logs)
	for i := range warnThrottleKeyCap {
		a.burstySenders.shouldWarn("filler-"+strconv.Itoa(i), backpressureNoticeWindow)
	}
	before := len(replyBodies(t, a, mock))
	a.refuse(&inboundMessage{From: "15559998888", ID: "wamid.SAT", Type: "text"},
		&a.burstySenders, backpressureNoticeWindow, refusalRateLimited, msgTooManyMessages,
		"whatsapp inbound message refused: sender exceeded the per-sender message rate")

	if got := len(replyBodies(t, a, mock)); got != before {
		t.Errorf("replies = %d, want %d — a saturated notice map must not reply, or it becomes a reflector", got, before)
	}
	var sawSaturationWarn bool
	records := logs.all()
	for _, r := range records {
		if r.Level >= slog.LevelWarn && strings.Contains(r.Message, "saturated") {
			sawSaturationWarn = true
		}
	}
	if !sawSaturationWarn {
		t.Error("no WARN said senders are being refused silently; the rejection is invisible at default levels")
	}
}

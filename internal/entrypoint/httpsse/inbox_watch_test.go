// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/backoff"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/notify"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

const inboxWatchPlatformUserID = "0192f0a6-3c1b-7d2e-8f4a-5b6c7d8e9f01"

type inboxWatchMesh struct {
	adapter *HTTPSSEAdapter
	brk     broker.Broker
}

func newInboxWatchMesh(t *testing.T, notifyServiceOn bool) inboxWatchMesh {
	t.Helper()
	return newInboxWatchMeshOn(t, notifyServiceOn, func(brk broker.Broker) broker.Broker { return brk })
}

func newInboxWatchMeshOn(t *testing.T, notifyServiceOn bool, wrap func(broker.Broker) broker.Broker) inboxWatchMesh {
	t.Helper()
	if notifyServiceOn {
		t.Setenv("SAM_FEATURE_NOTIFY_SERVICE", "true")
	} else {
		t.Setenv("SAM_FEATURE_NOTIFY_SERVICE", "false")
	}
	ctx := t.Context()
	dev := broker.NewDevBroker()
	if err := dev.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = dev.Disconnect(context.Background()) })
	brk := wrap(dev)
	registry := features.NewRegistry()
	if err := features.LoadFlags(registry); err != nil {
		t.Fatalf("load flags: %v", err)
	}
	rt := runtime.NewRuntime(brk, nil, runtime.WithLogger(slog.Default()), runtime.WithFeatureRegistry(registry))

	adapter := NewHTTPSSEAdapter()
	core := entrypoint.NewGatewayCore(adapter, map[string]any{
		"namespace":             "test-ns",
		"session_secret_key":    "test-secret",
		"gateway_id":            "test-gw",
		"default_user_identity": "sam_dev_user",
	})
	core.SetServices(rt.Services())
	if err := core.Init(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if adapter.inboxWatch != nil {
		adapter.inboxWatch.retry = fastRetry
	}
	if err := adapter.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = adapter.Stop(context.Background()) })

	notifyCore := notify.NewNotifyCore(map[string]any{
		"namespace":    "test-ns",
		"database_url": "sqlite://" + filepath.Join(t.TempDir(), "notify.db"),
	}, notify.NotifyExtensions{})
	notifyCore.SetServices(rt.Services())
	if err := notifyCore.Init(ctx, nil); err != nil {
		t.Fatalf("notify Init: %v", err)
	}
	if err := notifyCore.Start(ctx); err != nil {
		t.Fatalf("notify Start: %v", err)
	}
	t.Cleanup(func() { _ = notifyCore.Stop(context.Background()) })
	return inboxWatchMesh{adapter: adapter, brk: brk}
}

func (m inboxWatchMesh) openStream(t *testing.T, id *auth.Subject) *streamReader {
	t.Helper()
	h := &userNotificationsHandler{core: m.adapter.core, sseManager: m.adapter.sseManager, logger: m.adapter.logger}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.handleSubscribe(w, r.WithContext(auth.ContextWithSubject(r.Context(), id)))
	}))
	t.Cleanup(srv.Close)
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	reader := newStreamReader(resp)
	if got := reader.wait(t, "notification stream established", 2*time.Second); !strings.Contains(got, "notification stream established") {
		t.Fatalf("stream did not open: %q", got)
	}
	return reader
}

func (m inboxWatchMesh) sendNotification(t *testing.T, req a2a.NotifyRequest) {
	t.Helper()
	payload, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	topic := a2a.NotifyRequestTopic("test-ns", cmp.Or(req.RecipientPlatformUserID, req.RecipientUserID))
	if err := m.brk.PublishGuaranteed(t.Context(), topic, &broker.Message{Payload: payload}); err != nil {
		t.Fatalf("publish notify request: %v", err)
	}
}

func createdEventIDs(stream string) []string {
	var ids []string
	for _, frame := range strings.Split(stream, "\n\n") {
		event, data, ok := strings.Cut(frame, "\ndata: ")
		if !ok || !strings.HasSuffix(event, "event: "+notificationCreatedEvent) {
			continue
		}
		var created notificationCreated
		if err := json.Unmarshal([]byte(data), &created); err != nil {
			continue
		}
		ids = append(ids, created.ID)
	}
	return ids
}

func waitForCreatedEvents(t *testing.T, stream *streamReader, n int) []string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		ids := createdEventIDs(stream.snapshot())
		if len(ids) >= n {
			return ids
		}
		if time.Now().After(deadline) {
			t.Fatalf("got %d notification_created events, want %d: %q", len(ids), n, stream.snapshot())
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func requireMarkerLast(t *testing.T, ids []string) {
	t.Helper()
	if len(ids) != 2 || ids[0] == "" || ids[1] == "" || ids[0] == ids[1] {
		t.Fatalf("notification_created ids = %q, want the first notification's then the marker's", ids)
	}
}

func TestIntegration_ANewInboxNotificationIsPushedToTheRecipientsStreamOnce(t *testing.T) {
	for _, tc := range []struct {
		name string
		req  a2a.NotifyRequest
	}{
		{"addressed by both ids", a2a.NotifyRequest{RequestID: "r-both", RecipientUserID: "ben", RecipientPlatformUserID: inboxWatchPlatformUserID, Title: "Budget"}},
		{"addressed by users.id only", a2a.NotifyRequest{RequestID: "r-uuid", RecipientPlatformUserID: inboxWatchPlatformUserID, Title: "Budget"}},
		{"addressed by login id only", a2a.NotifyRequest{RequestID: "r-login", RecipientUserID: "ben", Title: "Budget"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mesh := newInboxWatchMesh(t, true)
			ben := mesh.openStream(t, auth.NewTestSubject(auth.SubjectFields{UserID: "ben", PlatformUserID: strings.ToUpper(inboxWatchPlatformUserID), Authenticated: true}))
			other := mesh.openStream(t, auth.NewTestSubject(auth.SubjectFields{UserID: "alice", Authenticated: true}))

			mesh.sendNotification(t, tc.req)
			marker := tc.req
			marker.RequestID, marker.Title = tc.req.RequestID+"-marker", "Marker"
			mesh.sendNotification(t, marker)

			requireMarkerLast(t, waitForCreatedEvents(t, ben, 2))
			if strings.Contains(other.snapshot(), notificationCreatedEvent) {
				t.Errorf("another user's stream got the event: %q", other.snapshot())
			}
		})
	}
}

func TestIntegration_ANotificationAddressedByAMixedCaseEmailReachesTheEmailStream(t *testing.T) {
	mesh := newInboxWatchMesh(t, true)
	ben := mesh.openStream(t, auth.NewTestSubject(auth.SubjectFields{UserID: "idp-subject-ben", Email: "ben@example.com", Authenticated: true}))

	req := a2a.NotifyRequest{RequestID: "r-email", RecipientUserID: "Ben@Example.com", Title: "Shared"}
	mesh.sendNotification(t, req)
	req.RequestID, req.Title = "r-email-marker", "Marker"
	mesh.sendNotification(t, req)

	requireMarkerLast(t, waitForCreatedEvents(t, ben, 2))
}

func TestIntegration_ADuplicateNotificationIsNotPushedAgain(t *testing.T) {
	mesh := newInboxWatchMesh(t, true)
	ben := mesh.openStream(t, auth.NewTestSubject(auth.SubjectFields{UserID: "ben", PlatformUserID: inboxWatchPlatformUserID, Authenticated: true}))
	req := a2a.NotifyRequest{RecipientPlatformUserID: inboxWatchPlatformUserID, Title: "Budget", Dedup: "budget-warn-1"}

	req.RequestID = "r-1"
	mesh.sendNotification(t, req)
	req.RequestID = "r-2"
	mesh.sendNotification(t, req)
	mesh.sendNotification(t, a2a.NotifyRequest{RequestID: "r-3", RecipientPlatformUserID: inboxWatchPlatformUserID, Title: "Marker"})

	requireMarkerLast(t, waitForCreatedEvents(t, ben, 2))
}

func TestIntegration_AStreamThatCannotKeepUpEndsSoTheClientReconnects(t *testing.T) {
	mesh := newInboxWatchMesh(t, true)
	ben := mesh.openStream(t, auth.NewTestSubject(auth.SubjectFields{UserID: "ben", Authenticated: true}))

	for sent := 0; ; sent++ {
		select {
		case <-ben.done:
			if sent <= userNotifQueueSize {
				t.Fatalf("the stream ended after %d events, before its queue could fill", sent)
			}
			return
		default:
		}
		if sent == 1_000_000 {
			t.Fatal("the stream stayed open through a million events")
		}
		if err := mesh.adapter.sseManager.SendUserNotificationToKeys([]string{"ben"}, notificationCreatedEvent, notificationCreated{ID: "0192f0a6-0000-7000-8000-000000000001"}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestInboxWatch_NotStartedWithTheNotifyServiceOff(t *testing.T) {
	mesh := newInboxWatchMesh(t, false)
	if mesh.adapter.inboxWatch != nil {
		t.Error("the inbox watch is wired with the notify service off")
	}
}

func TestInboxWatch_AnUndecodableSignalIsDropped(t *testing.T) {
	for _, tc := range []struct {
		name    string
		payload string
	}{
		{"not json", "not json"},
		{"notification id not a uuid", `{"recipient_user_id":"ben","notification_id":"n1"}`},
		{"no notification id", `{"recipient_user_id":"ben"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mgr := NewSSEManager(0, slog.Default())
			ch, remove := mgr.AddUserNotificationQueue("ben")
			defer remove()
			newInboxWatch(nil, "test-ns", mgr, slog.Default()).handle([]byte(tc.payload))
			select {
			case evt := <-ch:
				t.Errorf("an undecodable signal produced an event: %+v", evt)
			default:
			}
		})
	}
}

func TestInboxWatch_AnUpperCaseUsersIDReachesTheStream(t *testing.T) {
	mgr := NewSSEManager(0, slog.Default())
	ch, remove := mgr.AddUserNotificationQueueForKeys("idp-subject-ben", inboxWatchPlatformUserID)
	defer remove()
	payload, err := json.Marshal(a2a.NotifyInboxCreated{
		RecipientPlatformUserID: strings.ToUpper(inboxWatchPlatformUserID),
		NotificationID:          "0192f0a6-0000-7000-8000-000000000001",
	})
	if err != nil {
		t.Fatal(err)
	}
	newInboxWatch(nil, "test-ns", mgr, slog.Default()).handle(payload)
	if got := len(ch); got != 1 {
		t.Errorf("stream got %d events, want 1", got)
	}
}

var fastRetry = backoff.Exponential{Initial: time.Millisecond, Max: 5 * time.Millisecond}

const refuseForever = math.MaxInt32

type inboxWatchProbeBroker struct {
	broker.Broker
	createRefusals    atomic.Int32
	subscribeRefusals atomic.Int32
	creates           atomic.Int32

	mu           sync.Mutex
	unsubscribed []string
	deleted      []string
}

func (b *inboxWatchProbeBroker) CreateDirectSubscription() (broker.Queue, error) {
	b.creates.Add(1)
	if b.createRefusals.Add(-1) >= 0 {
		return nil, errors.New("direct subscription refused")
	}
	return b.Broker.CreateDirectSubscription()
}

func (b *inboxWatchProbeBroker) Subscribe(queue broker.Queue, topic string) error {
	if topic == a2a.NotifyInboxCreatedTopic("test-ns") && b.subscribeRefusals.Add(-1) >= 0 {
		return errors.New("subscribe refused")
	}
	return b.Broker.Subscribe(queue, topic)
}

func (b *inboxWatchProbeBroker) Unsubscribe(queue broker.Queue, topic string) error {
	b.mu.Lock()
	b.unsubscribed = append(b.unsubscribed, topic)
	b.mu.Unlock()
	return b.Broker.Unsubscribe(queue, topic)
}

func (b *inboxWatchProbeBroker) DeleteQueue(name string) error {
	b.mu.Lock()
	b.deleted = append(b.deleted, name)
	b.mu.Unlock()
	return b.Broker.DeleteQueue(name)
}

func (b *inboxWatchProbeBroker) calls() (unsubscribed, deleted []string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return slices.Clone(b.unsubscribed), slices.Clone(b.deleted)
}

func newConnectedProbeBroker(t *testing.T) *inboxWatchProbeBroker {
	t.Helper()
	dev := broker.NewDevBroker()
	if err := dev.Connect(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = dev.Disconnect(context.Background()) })
	return &inboxWatchProbeBroker{Broker: dev}
}

func newProbedWatch(brk *inboxWatchProbeBroker, mgr *SSEManager) *inboxWatch {
	w := newInboxWatch(brk, "test-ns", mgr, slog.Default())
	w.retry = fastRetry
	return w
}

func publishInboxCreated(t *testing.T, brk broker.Broker, ch <-chan UserNotification) {
	t.Helper()
	payload, err := json.Marshal(a2a.NotifyInboxCreated{RecipientUserID: "ben", NotificationID: "0192f0a6-0000-7000-8000-000000000001"})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if err := brk.Publish(t.Context(), a2a.NotifyInboxCreatedTopic("test-ns"), &broker.Message{Payload: payload}); err != nil {
			t.Fatal(err)
		}
		select {
		case <-ch:
			return
		case <-time.After(20 * time.Millisecond):
		}
	}
	t.Fatal("the watch did not push the signal")
}

func TestInboxWatch_RetriesUntilTheSubscriptionCanBeCreated(t *testing.T) {
	brk := newConnectedProbeBroker(t)
	brk.createRefusals.Store(3)
	mgr := NewSSEManager(0, slog.Default())
	ch, remove := mgr.AddUserNotificationQueue("ben")
	defer remove()
	w := newProbedWatch(brk, mgr)

	w.start(t.Context())
	publishInboxCreated(t, brk, ch)
	w.stop()

	if got := brk.creates.Load(); got != 4 {
		t.Errorf("created %d times, want 3 refusals then a success", got)
	}
	if _, deleted := brk.calls(); len(deleted) != 1 {
		t.Errorf("deleted %q, want only the subscription that succeeded", deleted)
	}
}

func TestInboxWatch_AFailedSubscribeDeletesItsSubscriptionAndRetries(t *testing.T) {
	brk := newConnectedProbeBroker(t)
	brk.subscribeRefusals.Store(2)
	mgr := NewSSEManager(0, slog.Default())
	ch, remove := mgr.AddUserNotificationQueue("ben")
	defer remove()
	w := newProbedWatch(brk, mgr)

	w.start(t.Context())
	publishInboxCreated(t, brk, ch)
	if _, deleted := brk.calls(); len(deleted) != 2 {
		t.Errorf("deleted %q before stop, want the two refused subscriptions", deleted)
	}
	w.stop()
	if _, deleted := brk.calls(); len(deleted) != 3 {
		t.Errorf("deleted %q after stop, want the refused two and the live one", deleted)
	}
}

func TestInboxWatch_AWatchStoppedBeforeItSubscribesLeavesNothingToRemove(t *testing.T) {
	brk := newConnectedProbeBroker(t)
	brk.createRefusals.Store(refuseForever)
	w := newProbedWatch(brk, NewSSEManager(0, slog.Default()))

	w.start(t.Context())
	for brk.creates.Load() < 2 {
		time.Sleep(time.Millisecond)
	}
	w.stop()

	if unsubscribed, deleted := brk.calls(); len(unsubscribed) != 0 || len(deleted) != 0 {
		t.Errorf("a watch that never subscribed unsubscribed %q and deleted %q", unsubscribed, deleted)
	}
}

func TestInboxWatch_StopUnsubscribesAndDeletesTheSubscription(t *testing.T) {
	brk := newConnectedProbeBroker(t)
	mgr := NewSSEManager(0, slog.Default())
	ch, remove := mgr.AddUserNotificationQueue("ben")
	defer remove()
	w := newProbedWatch(brk, mgr)
	w.start(t.Context())
	publishInboxCreated(t, brk, ch)

	w.stop()

	unsubscribed, deleted := brk.calls()
	if !slices.Equal(unsubscribed, []string{a2a.NotifyInboxCreatedTopic("test-ns")}) || len(deleted) != 1 {
		t.Errorf("stop unsubscribed %q and deleted %q, want the inbox created topic and its subscription", unsubscribed, deleted)
	}
}

func TestIntegration_TheGatewayPushesNotificationsOnceTheBrokerAcceptsTheWatch(t *testing.T) {
	mesh := newInboxWatchMeshOn(t, true, func(brk broker.Broker) broker.Broker {
		probe := &inboxWatchProbeBroker{Broker: brk}
		probe.subscribeRefusals.Store(3)
		return probe
	})
	ben := mesh.openStream(t, auth.NewTestSubject(auth.SubjectFields{UserID: "ben", PlatformUserID: inboxWatchPlatformUserID, Authenticated: true}))

	req := a2a.NotifyRequest{RequestID: "r-1", RecipientPlatformUserID: inboxWatchPlatformUserID, Title: "Budget"}
	deadline := time.Now().Add(5 * time.Second)
	for i := 0; len(createdEventIDs(ben.snapshot())) == 0; i++ {
		if time.Now().After(deadline) {
			t.Fatal("no notification_created event after the watch's subscribe was refused")
		}
		req.RequestID = fmt.Sprintf("r-%d", i)
		mesh.sendNotification(t, req)
		time.Sleep(20 * time.Millisecond)
	}
}

func newUserEventGateway(t *testing.T, brk broker.Broker) inboxWatchMesh {
	t.Helper()
	ctx := t.Context()
	registry := features.NewRegistry()
	if err := features.LoadFlags(registry); err != nil {
		t.Fatalf("load flags: %v", err)
	}
	rt := runtime.NewRuntime(brk, nil, runtime.WithLogger(slog.Default()), runtime.WithFeatureRegistry(registry))
	adapter := NewHTTPSSEAdapter()
	core := entrypoint.NewGatewayCore(adapter, map[string]any{
		"namespace":             "test-ns",
		"session_secret_key":    "test-secret",
		"gateway_id":            "test-gw",
		"default_user_identity": "sam_dev_user",
	})
	core.SetServices(rt.Services())
	if err := core.Init(ctx, nil); err != nil {
		t.Fatal(err)
	}
	adapter.userEventWatch.retry = fastRetry
	if err := adapter.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = adapter.Stop(context.Background()) })
	return inboxWatchMesh{adapter: adapter, brk: brk}
}

func streamEventCount(stream *streamReader, event string) int {
	return strings.Count(stream.snapshot(), "event: "+event+"\n")
}

func TestIntegration_AUserEventReachesTheUsersStreamOnEveryGatewayOnce(t *testing.T) {
	dev := broker.NewDevBroker()
	if err := dev.Connect(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = dev.Disconnect(context.Background()) })
	sender, other := newUserEventGateway(t, dev), newUserEventGateway(t, dev)
	ben := auth.NewTestSubject(auth.SubjectFields{UserID: "ben", Authenticated: true})
	onSender, onOther := sender.openStream(t, ben), other.openStream(t, ben)
	alice := other.openStream(t, auth.NewTestSubject(auth.SubjectFields{UserID: "alice", Authenticated: true}))

	sends := 0
	deadline := time.Now().Add(5 * time.Second)
	for streamEventCount(onOther, "execution_queued") == 0 {
		if time.Now().After(deadline) {
			t.Fatalf("the other gateway's stream got no event: %q", onOther.snapshot())
		}
		if err := sender.adapter.userNotifier.SendUserNotification("ben", "execution_queued", map[string]any{"execution_id": "e1"}); err != nil {
			t.Fatal(err)
		}
		sends++
		time.Sleep(20 * time.Millisecond)
	}
	if err := sender.adapter.userNotifier.SendUserNotification("ben", "session_created", map[string]any{"session_id": "s1"}); err != nil {
		t.Fatal(err)
	}
	onOther.wait(t, "event: session_created", 5*time.Second)
	onSender.wait(t, "event: session_created", 5*time.Second)

	if got := streamEventCount(onSender, "execution_queued"); got != sends {
		t.Errorf("sending gateway's stream got %d events for %d sends, want each once", got, sends)
	}
	if got := streamEventCount(onSender, "session_created"); got != 1 {
		t.Errorf("sending gateway's stream got %d session_created events, want 1", got)
	}
	if !strings.Contains(onOther.snapshot(), `data: {"session_id":"s1"}`) {
		t.Errorf("the other gateway's stream lost the event data: %q", onOther.snapshot())
	}
	if strings.Contains(alice.snapshot(), "execution_queued") {
		t.Errorf("another user's stream got the event: %q", alice.snapshot())
	}
}

func TestUserEventWatch_AnIncompleteEventIsDropped(t *testing.T) {
	for _, tc := range []struct {
		name    string
		payload string
	}{
		{"not JSON", `{`},
		{"no origin", `{"keys":["ben"],"event":"execution_queued","data":{}}`},
		{"no event", `{"origin":"o","keys":["ben"],"data":{}}`},
		{"no recipient", `{"origin":"o","keys":[],"event":"execution_queued","data":{}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mgr := NewSSEManager(0, slog.Default())
			ch, remove := mgr.AddUserNotificationQueue("ben")
			defer remove()
			newUserEventWatch(nil, "test-ns", "self", mgr, slog.Default()).handleUserEvent([]byte(tc.payload))
			if got := len(ch); got != 0 {
				t.Errorf("stream got %d events, want 0", got)
			}
		})
	}
}

type refusingPublishBroker struct{ broker.Broker }

func (refusingPublishBroker) Publish(context.Context, string, *broker.Message) error {
	return errors.New("refused")
}

func TestUserEventPublisher_ARefusedPublishStillReachesThisGatewaysStreams(t *testing.T) {
	mgr := NewSSEManager(0, slog.Default())
	ch, remove := mgr.AddUserNotificationQueue("ben")
	defer remove()
	p := newUserEventPublisher(refusingPublishBroker{}, "test-ns", "self", mgr, slog.Default())

	if err := p.SendUserNotification("ben", "execution_started", map[string]any{"execution_id": "e1"}); err != nil {
		t.Fatalf("SendUserNotification = %v, want nil", err)
	}
	if got := len(ch); got != 1 {
		t.Errorf("stream got %d events, want 1", got)
	}
}

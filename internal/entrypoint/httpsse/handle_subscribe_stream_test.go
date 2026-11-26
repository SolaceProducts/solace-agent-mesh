// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"bufio"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/eventlog"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

func liveFeed(ctx context.Context, t *testing.T, url string) (frames <-chan sseFrame, ended <-chan struct{}) {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	ch := make(chan sseFrame, 32)
	end := make(chan struct{})
	go func() {
		defer close(end)
		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(make([]byte, 1<<20), 1<<20)
		var cur sseFrame
		for sc.Scan() {
			line := sc.Text()
			switch {
			case strings.HasPrefix(line, "id: "):
				cur.id = strings.TrimPrefix(line, "id: ")
			case strings.HasPrefix(line, "event: "):
				cur.eventType = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				cur.data = strings.TrimPrefix(line, "data: ")
			case line == "" && cur.eventType != "":
				ch <- cur
				cur = sseFrame{}
			}
		}
	}()
	return ch, end
}

func nextFrame(t *testing.T, ch <-chan sseFrame, within time.Duration) sseFrame {
	t.Helper()
	select {
	case fr := <-ch:
		return fr
	case <-time.After(within):
		t.Fatal("no frame within the wait")
		return sseFrame{}
	}
}

func TestStreamLive_FollowsRowsAnotherInstanceWrote(t *testing.T) {
	adapter, core, _, _, repo := setupTestAdapterWithDB(t)
	writer := eventlog.NewPersistentEventLog(repo, store.NewTaskRepo(core.Store()), slog.Default())
	taskID, sessionID := idgen.New(), idgen.New()
	ctx := t.Context()

	adapter.eventLog.(*eventlog.PersistentEventLog).SetTaskRunningCheck(func(string) (bool, bool) { return true, true })
	adapter.sseManager.RegisterTask(taskID, sessionID, "user-1")
	if _, err := writer.Append(ctx, taskID, sessionID, "user-1", "status_update", statusPayload(1)); err != nil {
		t.Fatal(err)
	}
	ts := subscribeServerForHandler(t, &subscribeHandler{sseManager: adapter.sseManager, eventLog: adapter.eventLog})
	frames, ended := liveFeed(ctx, t, ts.URL+"/api/v1/sse/subscribe/"+taskID)

	if fr := nextFrame(t, frames, 3*time.Second); fr.id != "1" {
		t.Fatalf("first frame id = %q, want 1", fr.id)
	}

	if _, err := writer.Append(ctx, taskID, sessionID, "user-1", "status_update", statusPayload(2)); err != nil {
		t.Fatal(err)
	}
	adapter.OnTaskStreamRecord(ctx, taskID, entrypoint.StreamRecord{Sequence: 2, EventType: "status_update"})
	if fr := nextFrame(t, frames, 700*time.Millisecond); fr.id != "2" {
		t.Fatalf("frame after the wake has id %q, want 2", fr.id)
	}

	if _, err := writer.Append(ctx, taskID, sessionID, "user-1", "final_response", finalPayload()); err != nil {
		t.Fatal(err)
	}
	if err := writer.Complete(ctx, taskID); err != nil {
		t.Fatal(err)
	}
	adapter.OnTaskStreamRecord(ctx, taskID, entrypoint.StreamRecord{Sequence: 3, EventType: "final_response"})
	if fr := nextFrame(t, frames, 700*time.Millisecond); fr.id != "3" || fr.eventType != "final_response" {
		t.Fatalf("last frame = %+v, want the final response with id 3", fr)
	}
	select {
	case <-ended:
	case <-time.After(3 * time.Second):
		t.Fatal("the stream did not end when the task completed")
	}
	waitFor(t, "the entry was not released when the stream ended", func() bool { return !adapter.sseManager.HasTask(taskID) })
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal(what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestStreamLive_RowsFromTheLogAndFramesFromTheLiveChannel(t *testing.T) {
	el := &subscribeSignalLog{TaskEventLog: eventlog.NewMemoryEventLog(), subscribed: make(chan struct{}, 4)}
	mgr := NewSSEManager(100, nil)
	taskID := idgen.New()
	mgr.RegisterTask(taskID, "s", "u")
	ts := subscribeServerForHandler(t, &subscribeHandler{sseManager: mgr, eventLog: el})
	frames, ended := liveFeed(t.Context(), t, ts.URL+"/api/v1/sse/subscribe/"+taskID)

	recvWithin(t, el.subscribed, "the stream never asked the log")
	if _, err := el.Append(t.Context(), taskID, "", "", "status_update", statusPayload(1)); err != nil {
		t.Fatal(err)
	}
	mgr.Publish(taskID, &SSEEvent{EventType: "status_update", Data: statusPayload(1), Sequence: 1})
	if fr := nextFrame(t, frames, 2*time.Second); fr.id != "1" || fr.eventType != "status_update" {
		t.Fatalf("frame = %+v, want status_update id 1 from the log", fr)
	}

	mgr.Publish(taskID, &SSEEvent{EventType: "activity_event", Data: []byte(`{"a":1}`)})
	if fr := nextFrame(t, frames, 2*time.Second); fr.id != "" || fr.eventType != "activity_event" || fr.data != `{"a":1}` {
		t.Fatalf("frame = %+v, want the activity frame with no id", fr)
	}

	mgr.Publish(taskID, &SSEEvent{EventType: "final_response", Data: finalPayload(), Sequence: 2, Final: true})
	select {
	case <-ended:
		t.Fatal("the stream ended with the entry while the log had more to deliver")
	case <-time.After(200 * time.Millisecond):
	}
	if _, err := el.Append(t.Context(), taskID, "", "", "final_response", finalPayload()); err != nil {
		t.Fatal(err)
	}
	if err := el.Complete(t.Context(), taskID); err != nil {
		t.Fatal(err)
	}
	if fr := nextFrame(t, frames, 2*time.Second); fr.id != "2" || fr.eventType != "final_response" {
		t.Fatalf("frame = %+v, want final_response id 2", fr)
	}
	select {
	case <-ended:
	case <-time.After(2 * time.Second):
		t.Fatal("the stream did not end when the log completed")
	}
}

func TestStreamLive_ClientLeavingKeepsTheEntryOpen(t *testing.T) {
	el := eventlog.NewMemoryEventLog()
	mgr := NewSSEManager(100, nil)
	taskID := idgen.New()
	mgr.RegisterTask(taskID, "s", "u")
	if _, err := el.Append(t.Context(), taskID, "", "", "status_update", statusPayload(1)); err != nil {
		t.Fatal(err)
	}
	ts := subscribeServerForHandler(t, &subscribeHandler{sseManager: mgr, eventLog: el})
	ctx, cancel := context.WithCancel(t.Context())
	frames, ended := liveFeed(ctx, t, ts.URL+"/api/v1/sse/subscribe/"+taskID)
	nextFrame(t, frames, 2*time.Second)

	cancel()
	recvWithin(t, ended, "the stream did not end when the client left")
	waitFor(t, "the stream never left the entry", func() bool { return subscriberCount(mgr, taskID) == 0 })

	ch, connID, exists := mgr.Subscribe(taskID)
	if !exists {
		t.Fatal("the entry was removed when the client left")
	}
	defer mgr.Unsubscribe(taskID, connID)
	select {
	case _, open := <-ch:
		if !open {
			t.Fatal("the entry was closed when the client left")
		}
	default:
	}
}

func TestStreamLive_LogFailureEmitsTerminalErrorFrame(t *testing.T) {
	mgr := NewSSEManager(100, slog.New(slog.NewTextHandler(io.Discard, nil)))
	taskID := idgen.New()
	mgr.RegisterTask(taskID, "s", "u")
	el := &flakyEventLog{TaskEventLog: eventlog.NewMemoryEventLog(), subscribeErr: errors.New("event log down")}
	ts := subscribeServerForHandler(t, &subscribeHandler{sseManager: mgr, eventLog: el})
	frames, ended := liveFeed(t.Context(), t, ts.URL+"/api/v1/sse/subscribe/"+taskID)

	if fr := nextFrame(t, frames, 2*time.Second); fr.eventType != "final_response" {
		t.Fatalf("frame = %+v, want the terminal error frame", fr)
	}
	select {
	case <-ended:
	case <-time.After(2 * time.Second):
		t.Fatal("the stream stayed open after the error frame")
	}
}

type wakeRecorder struct {
	eventlog.TaskEventLog
	woken atomic.Value
}

func (w *wakeRecorder) Wake(taskID string) { w.woken.Store(taskID) }

func TestOnTaskStreamRecord(t *testing.T) {
	adapter, _, _ := setupTestAdapter(t)
	wake := &wakeRecorder{TaskEventLog: adapter.eventLog}
	adapter.eventLog = wake
	taskID, other := idgen.New(), idgen.New()

	adapter.OnTaskStreamRecord(t.Context(), taskID, entrypoint.StreamRecord{Sequence: 3, EventType: "status_update"})
	if got := wake.woken.Load(); got != taskID {
		t.Fatalf("woken = %v, want %s", got, taskID)
	}

	adapter.sseManager.RegisterTask(taskID, "s", "u")
	ch, _, _ := adapter.sseManager.Subscribe(taskID)
	adapter.OnTaskStreamRecord(t.Context(), taskID, entrypoint.StreamRecord{EventType: "activity_event", Data: []byte(`{"a":1}`)})
	select {
	case evt := <-ch:
		if evt.EventType != "activity_event" || string(evt.Data) != `{"a":1}` || evt.Sequence != 0 {
			t.Fatalf("frame = %+v", evt)
		}
	case <-time.After(time.Second):
		t.Fatal("the activity frame did not reach the stream")
	}

	adapter.OnTaskStreamRecord(t.Context(), taskID, entrypoint.StreamRecord{EventType: "final_response", Data: []byte(`{}`)})
	select {
	case evt := <-ch:
		t.Fatalf("a record with no row that is not an activity frame reached the stream: %+v", evt)
	case <-time.After(100 * time.Millisecond):
	}

	adapter.OnTaskStreamRecord(t.Context(), other, entrypoint.StreamRecord{EventType: "activity_event", Data: []byte(`{}`)})
	if adapter.sseManager.HasTask(other) {
		t.Fatal("a frame for a task with no stream created an entry")
	}

	adapter.OnTaskStreamRecord(t.Context(), taskID, entrypoint.StreamRecord{Sequence: 4, EventType: "final_response"})
	if _, open := recvWithinOpen(t, ch, "the final record did not finish the task's entry"); open {
		t.Fatal("the final record did not finish the task's entry")
	}
}

func TestSSEManager_Finish(t *testing.T) {
	mgr := NewSSEManager(10, nil)
	mgr.orphanGrace = 20 * time.Millisecond
	forgotten := make(chan string, 2)
	mgr.SetForgetHook(func(id string) { forgotten <- id })

	mgr.Finish(idgen.New())

	withStream := idgen.New()
	mgr.RegisterTask(withStream, "s", "u")
	ch, connID, _ := mgr.Subscribe(withStream)
	mgr.Finish(withStream)
	if _, open := recvWithinOpen(t, ch, "Finish left the subscriber's channel open"); open {
		t.Fatal("Finish left the subscriber's channel open")
	}
	mgr.Finish(withStream)
	mgr.Unsubscribe(withStream, connID)
	if mgr.HasTask(withStream) {
		t.Fatal("the entry stayed after its last subscriber left")
	}
	if got := recvWithin(t, forgotten, "the entry was never forgotten"); got != withStream {
		t.Fatalf("forgot %q, want %q", got, withStream)
	}

	unwatched := idgen.New()
	mgr.RegisterTask(unwatched, "s", "u")
	mgr.Finish(unwatched)
	select {
	case got := <-forgotten:
		if got != unwatched {
			t.Fatalf("forgot %q, want %q", got, unwatched)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a finished entry with no subscriber was never swept")
	}
}

func TestServeFromEventLog_ReceivesFramesWithNoRow(t *testing.T) {
	el := eventlog.NewMemoryEventLog()
	mgr := NewSSEManager(100, nil)
	taskID := idgen.New()
	if _, err := el.Append(t.Context(), taskID, "", "", "status_update", statusPayload(1)); err != nil {
		t.Fatal(err)
	}
	ts := subscribeServerForHandler(t, &subscribeHandler{sseManager: mgr, eventLog: el})
	frames, ended := liveFeed(t.Context(), t, ts.URL+"/api/v1/sse/subscribe/"+taskID)
	if fr := nextFrame(t, frames, 2*time.Second); fr.id != "1" {
		t.Fatalf("frame = %+v, want id 1 from the log", fr)
	}
	waitFor(t, "the stream did not hold an entry for the task", func() bool { return mgr.HasTask(taskID) })

	mgr.Publish(taskID, &SSEEvent{EventType: "activity_event", Data: []byte(`{"a":1}`)})
	if fr := nextFrame(t, frames, 2*time.Second); fr.eventType != "activity_event" || fr.id != "" {
		t.Fatalf("frame = %+v, want the activity frame with no id", fr)
	}

	if _, err := el.Append(t.Context(), taskID, "", "", "final_response", finalPayload()); err != nil {
		t.Fatal(err)
	}
	if err := el.Complete(t.Context(), taskID); err != nil {
		t.Fatal(err)
	}
	if fr := nextFrame(t, frames, 2*time.Second); fr.id != "2" || fr.eventType != "final_response" {
		t.Fatalf("frame = %+v, want final_response id 2", fr)
	}
	select {
	case <-ended:
	case <-time.After(2 * time.Second):
		t.Fatal("the stream did not end when the task completed")
	}
	waitFor(t, "the entry stayed after the task ended", func() bool { return !mgr.HasTask(taskID) })
}

type subscribeSignalLog struct {
	eventlog.TaskEventLog
	subscribed chan struct{}
}

func (l *subscribeSignalLog) Subscribe(ctx context.Context, taskID string, fromSeq int64) (<-chan eventlog.Event, error) {
	ch, err := l.TaskEventLog.Subscribe(ctx, taskID, fromSeq)
	l.subscribed <- struct{}{}
	return ch, err
}

func subscriberCount(m *SSEManager, taskID string) int {
	sh := m.shardFor(taskID)
	sh.mu.Lock()
	defer sh.mu.Unlock()
	if e := sh.tasks[taskID]; e != nil {
		return len(e.subscribers)
	}
	return -1
}

func TestServeFromEventLog_ClientLeavingReleasesTheHeldEntry(t *testing.T) {
	el := eventlog.NewMemoryEventLog()
	mgr := NewSSEManager(100, nil)
	var forgotten atomic.Int32
	mgr.SetForgetHook(func(string) { forgotten.Add(1) })
	taskID := idgen.New()
	if _, err := el.Append(t.Context(), taskID, "", "", "status_update", statusPayload(1)); err != nil {
		t.Fatal(err)
	}
	ts := subscribeServerForHandler(t, &subscribeHandler{sseManager: mgr, eventLog: el})
	ctx, cancel := context.WithCancel(t.Context())
	frames, ended := liveFeed(ctx, t, ts.URL+"/api/v1/sse/subscribe/"+taskID)
	nextFrame(t, frames, 2*time.Second)
	waitFor(t, "the stream did not hold an entry", func() bool { return mgr.HasTask(taskID) })

	cancel()
	recvWithin(t, ended, "the stream did not end when the client left")
	waitFor(t, "the held entry stayed after the client left", func() bool { return !mgr.HasTask(taskID) })
	if forgotten.Load() != 0 {
		t.Fatal("a running task was forgotten when its client left")
	}
}

type closedLog struct {
	eventlog.TaskEventLog
	completed bool
}

func (l closedLog) Subscribe(context.Context, string, int64) (<-chan eventlog.Event, error) {
	ch := make(chan eventlog.Event)
	close(ch)
	return ch, nil
}

func (l closedLog) Completed(context.Context, string) (bool, error) { return l.completed, nil }

func TestStream_FinishesOnlyACompletedTask(t *testing.T) {
	for _, path := range []string{"from the log", "live"} {
		for _, completed := range []bool{false, true} {
			mgr := NewSSEManager(100, nil)
			taskID := idgen.New()
			other, otherID, _ := mgr.SubscribeHeld(taskID)
			h := &subscribeHandler{sseManager: mgr, eventLog: closedLog{TaskEventLog: eventlog.NewMemoryEventLog(), completed: completed}}
			rec := httptest.NewRecorder()
			if path == "live" {
				h.streamLive(t.Context(), rec, taskID, nil, func() <-chan *SSEEvent { return nil }, 0, rec)
			} else {
				h.serveFromEventLog(rec, httptest.NewRequest("GET", "/", nil), taskID, 1, rec)
			}

			finished := false
			select {
			case _, open := <-other:
				finished = !open
			default:
			}
			if finished != completed {
				t.Fatalf("%s, completed=%v: entry finished = %v", path, completed, finished)
			}
			mgr.Unsubscribe(taskID, otherID)
		}
	}
}

type readyLog struct {
	eventlog.TaskEventLog
	events []eventlog.Event
}

func (l readyLog) Subscribe(context.Context, string, int64) (<-chan eventlog.Event, error) {
	ch := make(chan eventlog.Event, len(l.events))
	for _, e := range l.events {
		ch <- e
	}
	close(ch)
	return ch, nil
}

func TestStreamLive_WaitingFramesGoBeforeTheRow(t *testing.T) {
	for range 20 {
		h := &subscribeHandler{sseManager: NewSSEManager(10, nil), eventLog: readyLog{
			TaskEventLog: eventlog.NewMemoryEventLog(),
			events:       []eventlog.Event{{Sequence: 1, EventType: "final_response", Payload: finalPayload()}},
		}}
		live := make(chan *SSEEvent, 1)
		live <- &SSEEvent{EventType: "activity_event", Data: []byte(`{"last":true}`)}
		rec := httptest.NewRecorder()

		h.streamLive(t.Context(), rec, idgen.New(), live, func() <-chan *SSEEvent { return nil }, 0, rec)

		body := rec.Body.String()
		a, f := strings.Index(body, "event: activity_event"), strings.Index(body, "event: final_response")
		if a < 0 || f < 0 || a > f {
			t.Fatalf("want the waiting activity frame before the final row, got:\n%s", body)
		}
	}
}

func recvWithin[T any](t *testing.T, ch <-chan T, what string) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(3 * time.Second):
		t.Fatal(what)
		var zero T
		return zero
	}
}

func recvWithinOpen[T any](t *testing.T, ch <-chan T, what string) (T, bool) {
	t.Helper()
	select {
	case v, open := <-ch:
		return v, open
	case <-time.After(3 * time.Second):
		t.Fatal(what)
		var zero T
		return zero, false
	}
}

type gateWriter struct {
	*httptest.ResponseRecorder
	mu      sync.Mutex
	gate    chan struct{}
	blocked chan struct{}
	once    sync.Once
}

func (w *gateWriter) Write(b []byte) (int, error) {
	if strings.Contains(string(b), "activity_event") {
		w.once.Do(func() { close(w.blocked); <-w.gate })
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.ResponseRecorder.Write(b)
}

func (w *gateWriter) body() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.Body.String()
}

func TestStreamLive_SubscribesAgainAfterAShed(t *testing.T) {
	el := eventlog.NewMemoryEventLog()
	mgr := NewSSEManager(1, nil)
	taskID := idgen.New()
	mgr.RegisterTask(taskID, "s", "u")
	if _, err := el.Append(t.Context(), taskID, "", "", "status_update", statusPayload(1)); err != nil {
		t.Fatal(err)
	}
	live, _, _ := mgr.Subscribe(taskID)
	resubscribe := func() <-chan *SSEEvent {
		ch, _, ok := mgr.SubscribeOpen(taskID)
		if !ok {
			return nil
		}
		return ch
	}
	w := &gateWriter{ResponseRecorder: httptest.NewRecorder(), gate: make(chan struct{}), blocked: make(chan struct{})}
	h := &subscribeHandler{sseManager: mgr, eventLog: el}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { defer close(done); h.streamLive(ctx, w, taskID, live, resubscribe, 1, w) }()
	defer func() { cancel(); recvWithin(t, done, "the stream did not end") }()

	mgr.Publish(taskID, &SSEEvent{EventType: "activity_event", Data: []byte(`{"n":1}`)})
	recvWithin(t, w.blocked, "the first frame never reached the writer")
	mgr.Publish(taskID, &SSEEvent{EventType: "activity_event", Data: []byte(`{"n":2}`)})
	mgr.Publish(taskID, &SSEEvent{EventType: "activity_event", Data: []byte(`{"n":3}`)})
	close(w.gate)

	waitFor(t, "the stream did not subscribe again after the shed", func() bool { return subscriberCount(mgr, taskID) == 1 })
	mgr.Publish(taskID, &SSEEvent{EventType: "activity_event", Data: []byte(`{"probe":true}`)})
	waitFor(t, "a frame after the shed did not reach the stream", func() bool { return strings.Contains(w.body(), "probe") })
}

func TestStreamLive_ResumesARunningTaskFromLastEventID(t *testing.T) {
	for _, tc := range []struct {
		lastEventID string
		query       string
		want        []string
	}{
		{"2", "", []string{"3", "4", "5"}},
		{"3", "", []string{"4", "5"}},
		{"2", "?reconnect=true&lastEventTimestamp=0", []string{"3", "4", "5"}},
		{"2", "?lastSequence=0", []string{"3", "4", "5"}},
	} {
		t.Run("after "+tc.lastEventID+tc.query, func(t *testing.T) {
			adapter, _, _, _, _ := setupTestAdapterWithDB(t)
			taskID, sessionID := idgen.New(), idgen.New()
			adapter.sseManager.RegisterTask(taskID, sessionID, "user-1")
			for i := 1; i <= 3; i++ {
				adapter.OnStatusUpdate(t.Context(), taskID, nil, statusPayload(i))
			}
			ts := subscribeServerForAdapter(t, adapter)
			req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, ts.URL+"/api/v1/sse/subscribe/"+taskID+tc.query, nil)
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Last-Event-ID", tc.lastEventID)
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = resp.Body.Close() }()
			go func() {
				adapter.OnStatusUpdate(context.Background(), taskID, nil, statusPayload(4))
				adapter.OnFinalResponse(context.Background(), taskID, nil, finalPayload())
			}()

			frames := parseSSEFrames(t, resp, 5*time.Second)
			assertFrameIDs(t, frames, tc.want)
		})
	}
}

func TestSSEManager_SubscribeHeldOpenAndPublishIfPresent(t *testing.T) {
	mgr := NewSSEManager(10, nil)
	taskID := idgen.New()

	mgr.PublishIfPresent(taskID, &SSEEvent{EventType: "activity_event"})
	if mgr.HasTask(taskID) {
		t.Fatal("PublishIfPresent created an entry")
	}
	if _, _, ok := mgr.SubscribeOpen(taskID); ok {
		t.Fatal("SubscribeOpen subscribed to a task with no entry")
	}

	ch, connID, ok := mgr.SubscribeHeld(taskID)
	if !ok {
		t.Fatal("SubscribeHeld did not subscribe")
	}
	mgr.PublishIfPresent(taskID, &SSEEvent{EventType: "activity_event", Data: []byte(`{}`)})
	if evt := recvWithin(t, ch, "the held entry got no frame"); evt.EventType != "activity_event" {
		t.Fatalf("frame = %+v", evt)
	}
	mgr.Unsubscribe(taskID, connID)
	if mgr.HasTask(taskID) {
		t.Fatal("the held entry stayed after its last subscriber left")
	}

	mgr.RegisterTask(taskID, "s", "u")
	mgr.Finish(taskID)
	for name, subscribe := range map[string]func(string) (<-chan *SSEEvent, uint64, bool){"SubscribeHeld": mgr.SubscribeHeld, "SubscribeOpen": mgr.SubscribeOpen} {
		if _, _, ok := subscribe(taskID); ok {
			t.Fatalf("%s subscribed to a closed entry", name)
		}
	}
}

func TestStreamLive_FinalWithNoRowGoesAfterTheRows(t *testing.T) {
	for range 20 {
		h := &subscribeHandler{sseManager: NewSSEManager(10, nil), eventLog: readyLog{
			TaskEventLog: eventlog.NewMemoryEventLog(),
			events:       []eventlog.Event{{Sequence: 1, EventType: "status_update", Payload: statusPayload(1)}},
		}}
		live := make(chan *SSEEvent, 1)
		live <- &SSEEvent{EventType: "final_response", Data: finalPayload(), Final: true}
		rec := httptest.NewRecorder()

		h.streamLive(t.Context(), rec, idgen.New(), live, func() <-chan *SSEEvent { return nil }, 0, rec)

		body := rec.Body.String()
		s, f := strings.Index(body, "event: status_update"), strings.Index(body, "event: final_response")
		if s < 0 || f < 0 || s > f {
			t.Fatalf("want the row before the final with no row, got:\n%s", body)
		}
	}
}

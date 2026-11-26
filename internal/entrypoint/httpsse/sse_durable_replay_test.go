// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/eventlog"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

type flakyEventLog struct {
	eventlog.TaskEventLog
	replayErr    error
	subscribeErr error
}

func (f *flakyEventLog) Replay(ctx context.Context, taskID string, fromSeq int64) ([]eventlog.Event, error) {
	if f.replayErr != nil {
		return nil, f.replayErr
	}
	return f.TaskEventLog.Replay(ctx, taskID, fromSeq)
}

func (f *flakyEventLog) Subscribe(ctx context.Context, taskID string, fromSeq int64) (<-chan eventlog.Event, error) {
	if f.subscribeErr != nil {
		return nil, f.subscribeErr
	}
	return f.TaskEventLog.Subscribe(ctx, taskID, fromSeq)
}

func subscribeServerForHandler(t *testing.T, sub *subscribeHandler) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/sse/subscribe/{taskId}", sub.handleSubscribe)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts
}

func TestSSESubscribe_BacklogReplayErrorEmitsTerminalErrorFrame(t *testing.T) {
	mgr := NewSSEManager(100, slog.New(slog.NewTextHandler(io.Discard, nil)))
	taskID := idgen.New()
	mgr.RegisterTask(taskID, "s", "u")
	el := &flakyEventLog{TaskEventLog: eventlog.NewMemoryEventLog(), replayErr: errors.New("backlog db down")}
	ts := subscribeServerForHandler(t, &subscribeHandler{sseManager: mgr, eventLog: el})

	resp, err := http.Get(ts.URL + "/api/v1/sse/subscribe/" + taskID)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200 (headers already committed)", resp.StatusCode)
	}
	frames := parseSSEFrames(t, resp, 3*time.Second)
	if len(frames) != 1 || frames[0].eventType != "final_response" {
		t.Fatalf("got frames %+v, want one final_response error frame", frames)
	}
	if !strings.Contains(frames[0].data, "failed to load conversation history") {
		t.Errorf("error frame data = %q, want it to carry the replay-failure message", frames[0].data)
	}
}

func TestSSESubscribe_EventLogInfraErrorReturns503(t *testing.T) {
	mgr := NewSSEManager(100, slog.New(slog.NewTextHandler(io.Discard, nil)))
	taskID := idgen.New()
	el := &flakyEventLog{TaskEventLog: eventlog.NewMemoryEventLog(), subscribeErr: errors.New("db down")}
	ts := subscribeServerForHandler(t, &subscribeHandler{sseManager: mgr, eventLog: el})

	resp, err := http.Get(ts.URL + "/api/v1/sse/subscribe/" + taskID)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status %d, want 503 for an event-log infra error", resp.StatusCode)
	}
}

func subscribeServerForAdapter(t *testing.T, a *HTTPSSEAdapter) *httptest.Server {
	t.Helper()
	sub := &subscribeHandler{sseManager: a.sseManager, eventLog: a.eventLog}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/sse/subscribe/{taskId}", sub.handleSubscribe)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts
}

func frameIDs(frames []sseFrame) []string {
	ids := make([]string, len(frames))
	for i, f := range frames {
		ids[i] = f.id
	}
	return ids
}

func TestSSESubscribe_BrandNewTaskAttachesThenLive(t *testing.T) {
	adapter, _, _ := setupTestAdapter(t)
	taskID := idgen.New()
	adapter.sseManager.RegisterTask(taskID, "", "")

	ts := subscribeServerForAdapter(t, adapter)

	type result struct {
		frames []sseFrame
	}
	resCh := make(chan result, 1)
	go func() {
		resp, err := http.Get(ts.URL + "/api/v1/sse/subscribe/" + taskID)
		if err != nil {
			resCh <- result{}
			return
		}
		defer func() { _ = resp.Body.Close() }()
		resCh <- result{frames: parseSSEFrames(t, resp, 3*time.Second)}
	}()

	time.Sleep(50 * time.Millisecond)
	adapter.OnStatusUpdate(context.Background(), taskID, nil, []byte(`{"n":1}`))
	adapter.OnFinalResponse(context.Background(), taskID, nil, []byte(`{"n":2}`))

	res := <-resCh
	if got := frameIDs(res.frames); !slices.Equal(got, []string{"1", "2"}) {
		t.Fatalf("brand-new task: got frame ids %v, want [1 2]", got)
	}
	if res.frames[1].eventType != "final_response" {
		t.Errorf("last frame eventType = %q, want final_response", res.frames[1].eventType)
	}
}

func TestSSESubscribe_BacklogThenLiveHandoff(t *testing.T) {
	adapter, _, _ := setupTestAdapter(t)
	taskID := idgen.New()
	adapter.sseManager.RegisterTask(taskID, "", "")

	adapter.OnStatusUpdate(context.Background(), taskID, nil, []byte(`{"n":1}`))
	adapter.OnStatusUpdate(context.Background(), taskID, nil, []byte(`{"n":2}`))

	ts := subscribeServerForAdapter(t, adapter)
	resCh := make(chan []sseFrame, 1)
	go func() {
		resp, err := http.Get(ts.URL + "/api/v1/sse/subscribe/" + taskID)
		if err != nil {
			resCh <- nil
			return
		}
		defer func() { _ = resp.Body.Close() }()
		resCh <- parseSSEFrames(t, resp, 3*time.Second)
	}()

	time.Sleep(50 * time.Millisecond)
	adapter.OnStatusUpdate(context.Background(), taskID, nil, []byte(`{"n":3}`))
	adapter.OnStatusUpdate(context.Background(), taskID, nil, []byte(`{"n":4}`))
	adapter.OnFinalResponse(context.Background(), taskID, nil, []byte(`{"n":5}`))

	frames := <-resCh
	if got := frameIDs(frames); !slices.Equal(got, []string{"1", "2", "3", "4", "5"}) {
		t.Fatalf("backlog+live: got frame ids %v, want [1 2 3 4 5]", got)
	}
}

func TestSSESubscribe_ReconnectFromLastSequence(t *testing.T) {
	adapter, _, _ := setupTestAdapter(t)
	taskID := idgen.New()
	adapter.sseManager.RegisterTask(taskID, "", "")

	for n := 1; n <= 4; n++ {
		adapter.OnStatusUpdate(context.Background(), taskID, nil, fmt.Appendf(nil, `{"n":%d}`, n))
	}
	adapter.OnFinalResponse(context.Background(), taskID, nil, []byte(`{"n":5}`))

	ts := subscribeServerForAdapter(t, adapter)
	resp, err := http.Get(ts.URL + "/api/v1/sse/subscribe/" + taskID + "?reconnect=true&lastSequence=3")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	frames := parseSSEFrames(t, resp, 3*time.Second)
	if got := frameIDs(frames); !slices.Equal(got, []string{"4", "5"}) {
		t.Fatalf("reconnect lastSequence=3: got frame ids %v, want [4 5]", got)
	}
}

func TestSSESubscribe_SecondSubscriberGetsFullHistory(t *testing.T) {
	adapter, _, _ := setupTestAdapter(t)
	taskID := idgen.New()
	adapter.sseManager.RegisterTask(taskID, "", "")

	adapter.OnStatusUpdate(context.Background(), taskID, nil, []byte(`{"n":1}`))
	adapter.OnStatusUpdate(context.Background(), taskID, nil, []byte(`{"n":2}`))

	ts := subscribeServerForAdapter(t, adapter)

	first := make(chan []sseFrame, 1)
	go func() {
		resp, err := http.Get(ts.URL + "/api/v1/sse/subscribe/" + taskID)
		if err != nil {
			first <- nil
			return
		}
		defer func() { _ = resp.Body.Close() }()
		first <- parseSSEFrames(t, resp, 3*time.Second)
	}()

	time.Sleep(50 * time.Millisecond)

	second := make(chan []sseFrame, 1)
	go func() {
		resp, err := http.Get(ts.URL + "/api/v1/sse/subscribe/" + taskID)
		if err != nil {
			second <- nil
			return
		}
		defer func() { _ = resp.Body.Close() }()
		second <- parseSSEFrames(t, resp, 3*time.Second)
	}()

	time.Sleep(50 * time.Millisecond)
	adapter.OnFinalResponse(context.Background(), taskID, nil, []byte(`{"n":3}`))

	want := []string{"1", "2", "3"}
	if got := frameIDs(<-first); !slices.Equal(got, want) {
		t.Errorf("first subscriber: got ids %v, want %v", got, want)
	}
	if got := frameIDs(<-second); !slices.Equal(got, want) {
		t.Errorf("second subscriber: got ids %v, want %v", got, want)
	}
}

func TestSSESubscribe_NoGapNoDupUnderConcurrentPublish(t *testing.T) {
	const attempts = 16
	t.Run("memory", func(t *testing.T) {
		rng := rand.New(rand.NewPCG(0x5EED, 0xC0FFEE))
		for attempt := range attempts {
			adapter, _, _ := setupTestAdapter(t)
			noGapNoDupAttempt(t, adapter, "", attempt, rng)
		}
	})
	for _, async := range []bool{false, true} {
		t.Run(fmt.Sprintf("persistent async=%v", async), func(t *testing.T) {
			rng := rand.New(rand.NewPCG(0x5EED, 0xC0FFEE))
			adapter, _, _, _, _ := setupTestAdapterWithDBConfig(t, map[string]any{"async_event_log": async})
			for attempt := range attempts {
				noGapNoDupAttempt(t, adapter, idgen.New(), attempt, rng)
			}
		})
	}
}

func noGapNoDupAttempt(t *testing.T, adapter *HTTPSSEAdapter, sessionID string, attempt int, rng *rand.Rand) {
	t.Helper()
	const totalEvents = 60
	taskID := idgen.New()
	adapter.sseManager.RegisterTask(taskID, sessionID, "user-1")
	ts := subscribeServerForAdapter(t, adapter)

	jitter := make([]time.Duration, totalEvents)
	for i := range jitter {
		jitter[i] = time.Duration(rng.IntN(800)) * time.Microsecond
	}
	attachDelay := time.Duration(attempt)*2*time.Millisecond + time.Duration(rng.IntN(2000))*time.Microsecond

	pubDone := make(chan struct{})
	go func() {
		defer close(pubDone)
		for n := 1; n <= totalEvents; n++ {
			adapter.OnStatusUpdate(context.Background(), taskID, nil, fmt.Appendf(nil, `{"n":%d}`, n))
			time.Sleep(jitter[n-1])
		}
		adapter.OnFinalResponse(context.Background(), taskID, nil, []byte(`{"final":true}`))
	}()

	time.Sleep(attachDelay)

	resp, err := http.Get(ts.URL + "/api/v1/sse/subscribe/" + taskID)
	if err != nil {
		<-pubDone
		t.Fatalf("attempt %d: subscribe GET: %v", attempt, err)
	}
	frames := parseSSEFrames(t, resp, 5*time.Second)
	_ = resp.Body.Close()
	<-pubDone

	seen := map[string]int{}
	for _, f := range frames {
		seen[f.id]++
	}
	for n := 1; n <= totalEvents+1; n++ {
		id := fmt.Sprintf("%d", n)
		switch seen[id] {
		case 1:
		case 0:
			t.Fatalf("attempt %d (attachDelay=%s): missing sequence %s (gap); got ids %v", attempt, attachDelay, id, frameIDs(frames))
		default:
			t.Fatalf("attempt %d (attachDelay=%s): sequence %s delivered %d times (dup); got ids %v", attempt, attachDelay, id, seen[id], frameIDs(frames))
		}
	}
}

func TestSSESubscribe_ReplayAfterOrphanSweep(t *testing.T) {
	adapter, _, _ := setupTestAdapter(t)
	adapter.sseManager.orphanGrace = 20 * time.Millisecond

	taskID := idgen.New()
	adapter.sseManager.RegisterTask(taskID, "session-1", "user-1")
	adapter.OnStatusUpdate(context.Background(), taskID, nil, []byte(`{"n":1}`))
	adapter.OnStatusUpdate(context.Background(), taskID, nil, []byte(`{"n":2}`))
	adapter.OnFinalResponse(context.Background(), taskID, nil, []byte(`{"n":3}`))

	deadline := time.After(2 * time.Second)
	for adapter.sseManager.HasTask(taskID) {
		select {
		case <-deadline:
			t.Fatal("in-memory task entry was not swept")
		case <-time.After(5 * time.Millisecond):
		}
	}

	ts := subscribeServerForAdapter(t, adapter)
	resp, err := http.Get(ts.URL + "/api/v1/sse/subscribe/" + taskID)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("reconnect after sweep: status %d, want 200", resp.StatusCode)
	}

	if got := frameIDs(parseSSEFrames(t, resp, 3*time.Second)); !slices.Equal(got, []string{"1", "2", "3"}) {
		t.Fatalf("reconnect after sweep: got frame ids %v, want [1 2 3] (from the event log)", got)
	}
}

func TestSSESubscribe_UnknownTask404s(t *testing.T) {
	adapter, _, _ := setupTestAdapter(t)
	ts := subscribeServerForAdapter(t, adapter)

	resp, err := http.Get(ts.URL + "/api/v1/sse/subscribe/" + idgen.New())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown task: got status %d, want 404", resp.StatusCode)
	}
}

func frameTypeIDs(frames []sseFrame) []string {
	out := make([]string, len(frames))
	for i, f := range frames {
		out[i] = f.eventType + ":" + f.id
	}
	return out
}

func TestSSESubscribe_BacklogArtifactDroppedWhenFinalFollows(t *testing.T) {
	adapter, _, _ := setupTestAdapter(t)
	taskID := idgen.New()
	adapter.sseManager.RegisterTask(taskID, "s", "u")

	adapter.OnStatusUpdate(context.Background(), taskID, nil, []byte(`{"n":1}`))
	adapter.OnArtifactUpdate(context.Background(), taskID, nil, []byte(`{"n":2}`))
	adapter.OnFinalResponse(context.Background(), taskID, nil, []byte(`{"n":3}`))

	ts := subscribeServerForAdapter(t, adapter)
	resp, err := http.Get(ts.URL + "/api/v1/sse/subscribe/" + taskID)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	got := frameTypeIDs(parseSSEFrames(t, resp, 3*time.Second))
	want := []string{"status_update:1", "final_response:3"}
	if !slices.Equal(got, want) {
		t.Fatalf("artifact-before-final should be dropped on replay: got %v, want %v", got, want)
	}
}

func TestSSESubscribe_BacklogArtifactFlushedWhenNonFinalFollows(t *testing.T) {
	adapter, _, _ := setupTestAdapter(t)
	taskID := idgen.New()
	adapter.sseManager.RegisterTask(taskID, "s", "u")

	adapter.OnArtifactUpdate(context.Background(), taskID, nil, []byte(`{"n":1}`))
	adapter.OnStatusUpdate(context.Background(), taskID, nil, []byte(`{"n":2}`))
	adapter.OnFinalResponse(context.Background(), taskID, nil, []byte(`{"n":3}`))

	ts := subscribeServerForAdapter(t, adapter)
	resp, err := http.Get(ts.URL + "/api/v1/sse/subscribe/" + taskID)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	got := frameTypeIDs(parseSSEFrames(t, resp, 3*time.Second))
	want := []string{"artifact_update:1", "status_update:2", "final_response:3"}
	if !slices.Equal(got, want) {
		t.Fatalf("held artifact should be flushed before a following non-final event: got %v, want %v", got, want)
	}
}

func TestSSESubscribe_SweptArtifactDroppedWhenFinalFollows(t *testing.T) {
	adapter, _, _ := setupTestAdapter(t)
	adapter.sseManager.orphanGrace = 20 * time.Millisecond
	taskID := idgen.New()
	adapter.sseManager.RegisterTask(taskID, "s", "u")

	adapter.OnStatusUpdate(context.Background(), taskID, nil, []byte(`{"n":1}`))
	adapter.OnArtifactUpdate(context.Background(), taskID, nil, []byte(`{"n":2}`))
	adapter.OnFinalResponse(context.Background(), taskID, nil, []byte(`{"n":3}`))

	deadline := time.After(2 * time.Second)
	for adapter.sseManager.HasTask(taskID) {
		select {
		case <-deadline:
			t.Fatal("in-memory task entry was not swept")
		case <-time.After(5 * time.Millisecond):
		}
	}

	ts := subscribeServerForAdapter(t, adapter)
	resp, err := http.Get(ts.URL + "/api/v1/sse/subscribe/" + taskID)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	got := frameTypeIDs(parseSSEFrames(t, resp, 3*time.Second))
	want := []string{"status_update:1", "final_response:3"}
	if !slices.Equal(got, want) {
		t.Fatalf("swept-path artifact-before-final should be dropped: got %v, want %v", got, want)
	}
}

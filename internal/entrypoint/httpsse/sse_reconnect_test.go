// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/eventlog"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

func setupSubscribeServer(t *testing.T, el eventlog.TaskEventLog) (*httptest.Server, *SSEManager) {
	t.Helper()

	mgr := NewSSEManager(100, nil)
	sub := &subscribeHandler{sseManager: mgr, eventLog: el}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/sse/subscribe/{taskId}", sub.handleSubscribe)

	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts, mgr
}

func scanSSEFrames(t *testing.T, resp *http.Response, maxFrames int, timeout time.Duration) (frames []sseFrame, closed bool) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 1<<20), 1<<20)
		var cur sseFrame
		for scanner.Scan() {
			line := scanner.Text()
			switch {
			case strings.HasPrefix(line, ": "):
				continue
			case strings.HasPrefix(line, "id: "):
				cur.id = strings.TrimPrefix(line, "id: ")
			case strings.HasPrefix(line, "event: "):
				cur.eventType = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				cur.data = strings.TrimPrefix(line, "data: ")
			case line == "" && cur.eventType != "":
				frames = append(frames, cur)
				cur = sseFrame{}
				if maxFrames > 0 && len(frames) >= maxFrames {
					return
				}
			}
		}
	}()

	select {
	case <-done:
		return frames, true
	case <-time.After(timeout):
		return frames, false
	}
}

func assertFrameIDs(t *testing.T, frames []sseFrame, wantIDs []string) {
	t.Helper()
	if len(frames) != len(wantIDs) {
		gotIDs := make([]string, len(frames))
		for i, f := range frames {
			gotIDs[i] = f.id
		}
		t.Fatalf("got %d frames (ids=%v), want %d (ids=%v)",
			len(frames), gotIDs, len(wantIDs), wantIDs)
	}
	for i, f := range frames {
		if f.id != wantIDs[i] {
			t.Errorf("frames[%d].id=%q, want %q", i, f.id, wantIDs[i])
		}
	}
}

func statusPayload(n int) []byte {
	return fmt.Appendf(nil, `{"jsonrpc":"2.0","result":{"kind":"status-update","n":%d}}`, n)
}

func finalPayload() []byte {
	return []byte(`{"jsonrpc":"2.0","result":{"kind":"task"}}`)
}

func TestSSEReconnect_OrderingUnderConcurrentWrites(t *testing.T) {
	t.Parallel()

	const numEvents = 200
	const concurrency = 16

	el := eventlog.NewMemoryEventLog()
	ts, _ := setupSubscribeServer(t, el)

	taskID := idgen.New()
	ctx := t.Context()

	var wg sync.WaitGroup
	sem := make(chan struct{}, concurrency)
	for i := range numEvents {
		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()
			if _, err := el.Append(ctx, taskID, "", "", "status_update", statusPayload(i)); err != nil {
				t.Errorf("append %d: %v", i, err)
			}
		})
	}
	wg.Wait()

	if _, err := el.Append(ctx, taskID, "", "", "final_response", finalPayload()); err != nil {
		t.Fatalf("append final: %v", err)
	}
	if err := el.Complete(ctx, taskID); err != nil {
		t.Fatalf("complete: %v", err)
	}

	resp, err := http.Get(ts.URL + "/api/v1/sse/subscribe/" + taskID + "?reconnect=true&lastSequence=0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", resp.StatusCode)
	}

	frames, closed := scanSSEFrames(t, resp, 0, 10*time.Second)
	if !closed {
		t.Fatal("body did not close within timeout")
	}
	if len(frames) != numEvents+1 {
		t.Fatalf("expected %d frames, got %d", numEvents+1, len(frames))
	}

	ids := make([]int, 0, len(frames))
	for _, f := range frames {
		n, err := strconv.Atoi(f.id)
		if err != nil {
			t.Fatalf("non-numeric id %q: %v", f.id, err)
		}
		ids = append(ids, n)
	}

	if !sort.IntsAreSorted(ids) {
		t.Errorf("SSE ids not ascending: %v", ids)
	}
	for i, id := range ids {
		if id != i+1 {
			t.Errorf("frame %d id=%d, want %d", i, id, i+1)
			break
		}
	}
	if frames[len(frames)-1].eventType != "final_response" {
		t.Errorf("last frame type=%q, want final_response", frames[len(frames)-1].eventType)
	}
}

func TestSSEReconnect_MidStreamDisconnectThenReconnect(t *testing.T) {
	t.Parallel()

	el := eventlog.NewMemoryEventLog()
	ts, mgr := setupSubscribeServer(t, el)

	taskID := idgen.New()
	ctx := t.Context()
	mgr.RegisterTask(taskID, "", "")

	for i := 1; i <= 3; i++ {
		seq, err := el.Append(ctx, taskID, "", "", "status_update", statusPayload(i))
		if err != nil {
			t.Fatalf("pre-append %d: %v", i, err)
		}
		mgr.Publish(taskID, &SSEEvent{
			EventType: "status_update",
			Data:      statusPayload(i),
			Sequence:  seq,
		})
	}

	firstCtx, cancelFirst := context.WithCancel(ctx)
	defer cancelFirst()
	req, err := http.NewRequestWithContext(firstCtx, http.MethodGet,
		ts.URL+"/api/v1/sse/subscribe/"+taskID, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("initial connect: %v", err)
	}
	gotFrames, _ := scanSSEFrames(t, resp, 2, 3*time.Second)
	if len(gotFrames) < 2 {
		t.Fatalf("initial connect: wanted >= 2 frames, got %d", len(gotFrames))
	}
	lastSeq := gotFrames[1].id
	cancelFirst()
	_ = resp.Body.Close()

	for i := 4; i <= 6; i++ {
		if _, appendErr := el.Append(ctx, taskID, "", "", "status_update", statusPayload(i)); appendErr != nil {
			t.Fatalf("post-drop append %d: %v", i, appendErr)
		}
	}
	if _, appendErr := el.Append(ctx, taskID, "", "", "final_response", finalPayload()); appendErr != nil {
		t.Fatalf("append final: %v", appendErr)
	}
	_ = el.Complete(ctx, taskID)

	url := ts.URL + "/api/v1/sse/subscribe/" + taskID + "?reconnect=true&lastSequence=" + lastSeq
	resp2, err := http.Get(url) //nolint:noctx // one-shot SSE read in test
	if err != nil {
		t.Fatalf("reconnect: %v", err)
	}
	defer func() { _ = resp2.Body.Close() }()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("reconnect status=%d", resp2.StatusCode)
	}

	replayed, _ := scanSSEFrames(t, resp2, 0, 5*time.Second)
	assertFrameIDs(t, replayed, []string{"3", "4", "5", "6", "7"})
	if replayed[len(replayed)-1].eventType != "final_response" {
		t.Errorf("last replay event type=%q, want final_response",
			replayed[len(replayed)-1].eventType)
	}
}

func TestSSEReconnect_LastSequenceAtOrBeyondTip(t *testing.T) {
	t.Parallel()

	el := eventlog.NewMemoryEventLog()
	ts, _ := setupSubscribeServer(t, el)

	taskID := idgen.New()
	ctx := t.Context()
	for i := 1; i <= 3; i++ {
		if _, err := el.Append(ctx, taskID, "", "", "status_update", statusPayload(i)); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}
	_ = el.Complete(ctx, taskID)

	for _, lastSeq := range []string{"3", "999"} {
		resp, err := http.Get(ts.URL + "/api/v1/sse/subscribe/" + taskID + "?reconnect=true&lastSequence=" + lastSeq)
		if err != nil {
			t.Fatalf("lastSequence=%s: %v", lastSeq, err)
		}
		if resp.StatusCode != http.StatusOK {
			_ = resp.Body.Close()
			t.Fatalf("lastSequence=%s: status=%d", lastSeq, resp.StatusCode)
		}
		frames, _ := scanSSEFrames(t, resp, 0, 2*time.Second)
		_ = resp.Body.Close()
		if len(frames) != 0 {
			t.Errorf("lastSequence=%s: got %d frames, want 0: %+v", lastSeq, len(frames), frames)
		}
	}
}

func TestSSEReconnect_TimestampBasedReplay(t *testing.T) {
	t.Parallel()

	el := eventlog.NewMemoryEventLog()
	ts, _ := setupSubscribeServer(t, el)

	taskID := idgen.New()
	ctx := t.Context()

	for i := 1; i <= 2; i++ {
		if _, err := el.Append(ctx, taskID, "", "", "status_update", statusPayload(i)); err != nil {
			t.Fatalf("first-batch append %d: %v", i, err)
		}
	}
	time.Sleep(20 * time.Millisecond)
	cutoff := time.Now().UnixMilli()
	time.Sleep(20 * time.Millisecond)
	for i := 3; i <= 4; i++ {
		if _, err := el.Append(ctx, taskID, "", "", "status_update", statusPayload(i)); err != nil {
			t.Fatalf("second-batch append %d: %v", i, err)
		}
	}
	if _, err := el.Append(ctx, taskID, "", "", "final_response", finalPayload()); err != nil {
		t.Fatalf("append final: %v", err)
	}
	_ = el.Complete(ctx, taskID)

	url := ts.URL + "/api/v1/sse/subscribe/" + taskID +
		"?reconnect=true&lastEventTimestamp=" + strconv.FormatInt(cutoff, 10)
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", resp.StatusCode)
	}

	frames, _ := scanSSEFrames(t, resp, 0, 5*time.Second)
	assertFrameIDs(t, frames, []string{"3", "4", "5"})
}

func TestSSEReconnect_InvalidTimestampBadRequest(t *testing.T) {
	t.Parallel()

	el := eventlog.NewMemoryEventLog()
	ts, _ := setupSubscribeServer(t, el)

	resp, err := http.Get(ts.URL + "/api/v1/sse/subscribe/some-task?reconnect=true&lastEventTimestamp=notanumber")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status=%d, want 400", resp.StatusCode)
	}
}

func TestSSEReconnect_PersistentBackend(t *testing.T) {
	adapter, _, _, _, _ := setupTestAdapterWithDB(t)

	mux := http.NewServeMux()
	sub := &subscribeHandler{sseManager: adapter.sseManager, eventLog: adapter.eventLog}
	mux.HandleFunc("GET /api/v1/sse/subscribe/{taskId}", sub.handleSubscribe)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	taskID := idgen.New()
	sessionID := idgen.New()
	ctx := t.Context()
	for i := 1; i <= 5; i++ {
		if _, err := adapter.eventLog.Append(ctx, taskID, sessionID, "user-1", "status_update", statusPayload(i)); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}
	if _, err := adapter.eventLog.Append(ctx, taskID, sessionID, "user-1", "final_response", finalPayload()); err != nil {
		t.Fatalf("append final: %v", err)
	}
	_ = adapter.eventLog.Complete(ctx, taskID)

	resp, err := http.Get(ts.URL + "/api/v1/sse/subscribe/" + taskID + "?reconnect=true&lastSequence=3")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", resp.StatusCode)
	}

	frames, _ := scanSSEFrames(t, resp, 0, 5*time.Second)
	assertFrameIDs(t, frames, []string{"4", "5", "6"})
	if frames[len(frames)-1].eventType != "final_response" {
		t.Errorf("last frame type=%q, want final_response", frames[len(frames)-1].eventType)
	}
}

func TestSSEReconnect_LiveDeliveryAfterReplay(t *testing.T) {
	t.Parallel()

	el := eventlog.NewMemoryEventLog()
	ts, _ := setupSubscribeServer(t, el)

	taskID := idgen.New()
	ctx := t.Context()
	for i := 1; i <= 2; i++ {
		if _, err := el.Append(ctx, taskID, "", "", "status_update", statusPayload(i)); err != nil {
			t.Fatalf("seed append %d: %v", i, err)
		}
	}

	reqCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet,
		ts.URL+"/api/v1/sse/subscribe/"+taskID+"?reconnect=true&lastSequence=0", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", resp.StatusCode)
	}

	frameCh := make(chan sseFrame, 16)
	readErrCh := make(chan error, 1)
	go func() {
		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 1<<20), 1<<20)
		var cur sseFrame
		for scanner.Scan() {
			line := scanner.Text()
			switch {
			case strings.HasPrefix(line, ": "):
				continue
			case strings.HasPrefix(line, "id: "):
				cur.id = strings.TrimPrefix(line, "id: ")
			case strings.HasPrefix(line, "event: "):
				cur.eventType = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				cur.data = strings.TrimPrefix(line, "data: ")
			case line == "" && cur.eventType != "":
				frameCh <- cur
				cur = sseFrame{}
			}
		}
		if err := scanner.Err(); err != nil && !errors.Is(err, io.EOF) {
			readErrCh <- err
		}
		close(frameCh)
	}()

	waitFrame := func() sseFrame {
		t.Helper()
		select {
		case f, ok := <-frameCh:
			if !ok {
				t.Fatal("frame channel closed before expected frame arrived")
			}
			return f
		case err := <-readErrCh:
			t.Fatalf("read error: %v", err)
		case <-time.After(3 * time.Second):
			t.Fatal("timed out waiting for frame")
		}
		return sseFrame{}
	}

	if f := waitFrame(); f.id != "1" {
		t.Errorf("replay frame id=%q, want 1", f.id)
	}
	if f := waitFrame(); f.id != "2" {
		t.Errorf("replay frame id=%q, want 2", f.id)
	}

	if _, err := el.Append(ctx, taskID, "", "", "status_update", statusPayload(3)); err != nil {
		t.Fatalf("live append: %v", err)
	}
	if f := waitFrame(); f.id != "3" {
		t.Errorf("live frame id=%q, want 3", f.id)
	}

	if _, err := el.Append(ctx, taskID, "", "", "final_response", finalPayload()); err != nil {
		t.Fatalf("append final: %v", err)
	}
	_ = el.Complete(ctx, taskID)

	f := waitFrame()
	if f.id != "4" || f.eventType != "final_response" {
		t.Errorf("final frame id=%q type=%q, want 4/final_response", f.id, f.eventType)
	}
}

// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package teams

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type capturedCall struct {
	Method string
	Path   string
	Body   map[string]any
}

func newMockBotServer(t *testing.T) (*httptest.Server, *[]capturedCall, *sync.Mutex) {
	t.Helper()
	var calls []capturedCall
	var mu sync.Mutex

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/botframework.com/oauth2/v2.0/token" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
				"access_token": "mock-token",
				"token_type":   "Bearer",
				"expires_in":   3600,
			})
			return
		}

		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body) //nolint:errcheck

		mu.Lock()
		calls = append(calls, capturedCall{
			Method: r.Method,
			Path:   r.URL.Path,
			Body:   body,
		})
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"id": "activity-1"}) //nolint:errcheck
	}))

	return srv, &calls, &mu
}

func newTestQueue(t *testing.T, srv *httptest.Server) *MessageQueue {
	t.Helper()
	bc := NewBotClient("app-id", "app-password", "", slog.Default())
	bc.apiBaseURL = srv.URL

	q := NewMessageQueue("task-1", "conv-1", srv.URL, "bot-id", "reply-to-1", bc, slog.Default())
	q.SetMinThrottle(0)
	q.SetBufferInterval(0)
	return q
}

func TestQueue_TextUpdate_SendsMessage(t *testing.T) {
	srv, calls, mu := newMockBotServer(t)
	defer srv.Close()

	q := newTestQueue(t, srv)
	q.Start()

	q.Send(TextUpdateOp{Text: "Hello world"})
	q.Stop(5 * time.Second)

	mu.Lock()
	defer mu.Unlock()
	if len(*calls) < 1 {
		t.Fatalf("expected at least 1 call, got %d", len(*calls))
	}
	c := (*calls)[0]
	if c.Method != "POST" {
		t.Errorf("method = %q, want POST", c.Method)
	}
	text, _ := c.Body["text"].(string)
	if text != "Hello world" {
		t.Errorf("text = %q, want %q", text, "Hello world")
	}
}

func TestQueue_TextUpdate_CoalescesUpdates(t *testing.T) {
	srv, calls, mu := newMockBotServer(t)
	defer srv.Close()

	q := newTestQueue(t, srv)

	q.Send(TextUpdateOp{Text: "Hello "})
	q.Send(TextUpdateOp{Text: "world"})
	q.Send(TextUpdateOp{Text: "!"})
	q.Start()
	q.Stop(5 * time.Second)

	mu.Lock()
	defer mu.Unlock()
	if len(*calls) > 2 {
		t.Errorf("expected at most 2 calls (coalesced), got %d", len(*calls))
	}

	lastCall := (*calls)[len(*calls)-1]
	text, _ := lastCall.Body["text"].(string)
	if !strings.Contains(text, "Hello ") || !strings.Contains(text, "world") {
		t.Errorf("expected coalesced text, got %q", text)
	}
}

func TestQueue_TextUpdate_UpdatesExistingMessage(t *testing.T) {
	srv, calls, mu := newMockBotServer(t)
	defer srv.Close()

	q := newTestQueue(t, srv)
	q.Start()

	q.Send(TextUpdateOp{Text: "first"})
	q.WaitDrain()
	q.Send(TextUpdateOp{Text: " second"})
	q.Stop(5 * time.Second)

	mu.Lock()
	defer mu.Unlock()
	if len(*calls) < 2 {
		t.Fatalf("expected at least 2 calls, got %d", len(*calls))
	}
	if (*calls)[0].Method != "POST" {
		t.Errorf("first call method = %q, want POST", (*calls)[0].Method)
	}
	if (*calls)[1].Method != "PUT" {
		t.Errorf("second call method = %q, want PUT", (*calls)[1].Method)
	}
}

func TestQueue_Finalize_FlushesTextFirst(t *testing.T) {
	srv, calls, mu := newMockBotServer(t)
	defer srv.Close()

	q := newTestQueue(t, srv)
	q.Start()

	q.Send(TextUpdateOp{Text: "buffered text"})
	q.Send(FinalizeOp{StatusActivityID: "status-1", Text: "Done"})
	q.Stop(5 * time.Second)

	mu.Lock()
	defer mu.Unlock()

	foundText := false
	foundFinalize := false
	for _, c := range *calls {
		text, _ := c.Body["text"].(string)
		if c.Method == "POST" && text == "buffered text" {
			foundText = true
		}
		if c.Method == "PUT" && text == "Done" {
			foundFinalize = true
		}
	}
	if !foundText {
		t.Error("expected POST with 'buffered text'")
	}
	if !foundFinalize {
		t.Error("expected PUT with 'Done' for finalize")
	}
}

func TestQueue_Finalize_PostsTextWithoutStatusActivityID(t *testing.T) {
	srv, calls, mu := newMockBotServer(t)
	defer srv.Close()

	q := newTestQueue(t, srv)
	q.Start()

	q.Send(FinalizeOp{StatusActivityID: "", Text: "Done"})
	q.Stop(5 * time.Second)

	mu.Lock()
	defer mu.Unlock()
	if len(*calls) != 1 || (*calls)[0].Method != http.MethodPost || (*calls)[0].Body["text"] != "Done" {
		t.Errorf("calls = %+v, want one POST of the terminal text", *calls)
	}
}

func TestQueue_Delete_FlushesTextThenDeletesStatus(t *testing.T) {
	srv, calls, mu := newMockBotServer(t)
	defer srv.Close()

	q := newTestQueue(t, srv)
	q.Start()

	q.Send(TextUpdateOp{Text: "buffered text"})
	q.Send(DeleteOp{StatusActivityID: "status-1"})
	q.Stop(5 * time.Second)

	mu.Lock()
	defer mu.Unlock()

	postIdx, deleteIdx := -1, -1
	for i, c := range *calls {
		text, _ := c.Body["text"].(string)
		if c.Method == "POST" && text == "buffered text" {
			postIdx = i
		}
		if c.Method == "DELETE" && strings.HasSuffix(c.Path, "/activities/status-1") {
			deleteIdx = i
		}
	}
	if postIdx < 0 {
		t.Error("expected POST with 'buffered text'")
	}
	if deleteIdx < 0 {
		t.Error("expected DELETE of status-1 activity")
	}
	if postIdx >= 0 && deleteIdx >= 0 && postIdx > deleteIdx {
		t.Errorf("POST must precede DELETE, got POST@%d DELETE@%d", postIdx, deleteIdx)
	}
}

func TestQueue_Delete_SkipsEmptyStatusActivityID(t *testing.T) {
	srv, calls, mu := newMockBotServer(t)
	defer srv.Close()

	q := newTestQueue(t, srv)
	q.Start()

	q.Send(DeleteOp{StatusActivityID: ""})
	q.Stop(5 * time.Second)

	mu.Lock()
	defer mu.Unlock()
	if len(*calls) != 0 {
		t.Errorf("expected 0 calls, got %d", len(*calls))
	}
}

func TestQueue_BufferInterval_FlushesOnTimer(t *testing.T) {
	srv, calls, mu := newMockBotServer(t)
	defer srv.Close()

	bc := NewBotClient("app-id", "app-password", "", slog.Default())
	bc.apiBaseURL = srv.URL

	q := NewMessageQueue("task-1", "conv-1", srv.URL, "bot-id", "reply-to-1", bc, slog.Default())
	q.SetMinThrottle(0)
	q.SetBufferInterval(100 * time.Millisecond)
	q.Start()

	q.Send(TextUpdateOp{Text: "buffered"})
	time.Sleep(300 * time.Millisecond)

	mu.Lock()
	callCount := len(*calls)
	mu.Unlock()

	q.Stop(5 * time.Second)

	if callCount < 1 {
		t.Errorf("expected at least 1 call from buffer timer, got %d", callCount)
	}
}

func TestQueue_BufferInterval_FlushesOnSize(t *testing.T) {
	srv, calls, mu := newMockBotServer(t)
	defer srv.Close()

	bc := NewBotClient("app-id", "app-password", "", slog.Default())
	bc.apiBaseURL = srv.URL

	q := NewMessageQueue("task-1", "conv-1", srv.URL, "bot-id", "reply-to-1", bc, slog.Default())
	q.SetMinThrottle(0)
	q.SetBufferInterval(10 * time.Second)
	q.Start()

	bigText := strings.Repeat("x", 1100)
	q.Send(TextUpdateOp{Text: bigText})
	q.WaitDrain()

	mu.Lock()
	callCount := len(*calls)
	mu.Unlock()

	q.Stop(5 * time.Second)

	if callCount < 1 {
		t.Errorf("expected at least 1 call from size-based flush, got %d", callCount)
	}
}

func TestQueue_DefaultIntervals(t *testing.T) {
	q := NewMessageQueue("t", "c", "https://example", "bot", "r", nil, slog.Default())

	if q.minAPIInterval != 200*time.Millisecond {
		t.Errorf("minAPIInterval default = %v, want 200ms", q.minAPIInterval)
	}
	if q.bufferInterval != 500*time.Millisecond {
		t.Errorf("bufferInterval default = %v, want 500ms", q.bufferInterval)
	}
}

func TestQueue_Flush_SkipsEmptyAndWhitespaceBuffer(t *testing.T) {
	cases := []string{"", "   ", "\t\n  "}
	for _, in := range cases {
		t.Run("buffer="+in, func(t *testing.T) {
			srv, calls, mu := newMockBotServer(t)
			defer srv.Close()

			q := newTestQueue(t, srv)
			q.Start()
			q.Send(TextUpdateOp{Text: in})
			q.Stop(5 * time.Second)

			mu.Lock()
			defer mu.Unlock()
			for _, c := range *calls {
				if strings.Contains(c.Path, "/conversations/") {
					t.Errorf("unexpected Bot Framework call for empty/whitespace buffer: %s %s", c.Method, c.Path)
				}
			}
		})
	}
}

func TestQueue_StatusUpdate_PUTsToStatusActivity(t *testing.T) {
	srv, calls, mu := newMockBotServer(t)
	defer srv.Close()

	q := newTestQueue(t, srv)
	q.Start()
	q.Send(StatusUpdateOp{StatusActivityID: "status-act-1", Text: "💭 working..."})
	q.Stop(3 * time.Second)

	mu.Lock()
	defer mu.Unlock()
	var sawPut bool
	for _, c := range *calls {
		if c.Method == http.MethodPut && strings.HasSuffix(c.Path, "/status-act-1") {
			if text, _ := c.Body["text"].(string); text == "💭 working..." {
				sawPut = true
				break
			}
		}
	}
	if !sawPut {
		paths := make([]string, len(*calls))
		for i, c := range *calls {
			paths[i] = c.Method + " " + c.Path
		}
		t.Errorf("expected a PUT to /status-act-1 with the status text; calls = %v", paths)
	}
}

func TestQueue_StatusUpdate_FailureSwallowed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/botframework.com/oauth2/v2.0/token" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
				"access_token": "tok", "token_type": "Bearer", "expires_in": 3600,
			})
			return
		}
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()

	q := newTestQueue(t, srv)
	q.Start()
	q.Send(StatusUpdateOp{StatusActivityID: "status-act-fail", Text: "won't land"})
	q.Stop(3 * time.Second)
}

func TestQueue_FallsBackToNewMessageOnUpdateFailure(t *testing.T) {
	var calls []capturedCall
	var postCount int
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/botframework.com/oauth2/v2.0/token" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
				"access_token": "mock-token", "token_type": "Bearer", "expires_in": 3600,
			})
			return
		}
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body) //nolint:errcheck
		mu.Lock()
		calls = append(calls, capturedCall{Method: r.Method, Path: r.URL.Path, Body: body})
		mu.Unlock()
		if r.Method == http.MethodPut {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		mu.Lock()
		postCount++
		id := "msg-original"
		if postCount > 1 {
			id = "msg-fallback"
		}
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"id": id}) //nolint:errcheck
	}))
	defer srv.Close()

	q := newTestQueue(t, srv)
	q.Start()

	q.Send(TextUpdateOp{Text: "first message"})
	q.WaitDrain()

	q.Send(TextUpdateOp{Text: " second"})
	q.Stop(5 * time.Second)

	mu.Lock()
	defer mu.Unlock()

	var initialPost, sawPut, fallbackPost bool
	for _, c := range calls {
		switch c.Method {
		case http.MethodPost:
			if sawPut {
				fallbackPost = true
			} else {
				initialPost = true
			}
		case http.MethodPut:
			sawPut = true
		}
	}
	paths := make([]string, len(calls))
	for i, c := range calls {
		paths[i] = c.Method + " " + c.Path
	}
	if !initialPost {
		t.Errorf("missing initial POST (first send); calls = %v", paths)
	}
	if !sawPut {
		t.Errorf("missing PUT (update attempt) on second send; calls = %v", paths)
	}
	if !fallbackPost {
		t.Errorf("missing fallback POST after PUT failure; calls = %v", paths)
	}
}

func TestQueue_StopFlushesRemainingText(t *testing.T) {
	srv, calls, mu := newMockBotServer(t)
	defer srv.Close()

	bc := NewBotClient("app-id", "app-password", "", slog.Default())
	bc.apiBaseURL = srv.URL

	q := NewMessageQueue("task-1", "conv-1", srv.URL, "bot-id", "reply-to-1", bc, slog.Default())
	q.SetMinThrottle(0)
	q.SetBufferInterval(10 * time.Second)
	q.Start()

	q.Send(TextUpdateOp{Text: "remaining"})
	q.Stop(5 * time.Second)

	mu.Lock()
	defer mu.Unlock()
	if len(*calls) < 1 {
		t.Fatalf("expected at least 1 call on stop, got %d", len(*calls))
	}
	text, _ := (*calls)[0].Body["text"].(string)
	if text != "remaining" {
		t.Errorf("text = %q, want %q", text, "remaining")
	}
}

func newHoldingBotServer(t *testing.T) (srv *httptest.Server, arrived <-chan struct{}, release func()) {
	t.Helper()
	in := make(chan struct{}, 16)
	held := make(chan struct{})
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/botframework.com/oauth2/v2.0/token" {
			json.NewEncoder(w).Encode(map[string]any{"access_token": "mock-token", "expires_in": 3600}) //nolint:errcheck
			return
		}
		in <- struct{}{}
		select {
		case <-held:
		case <-r.Context().Done():
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"id": "activity-1"}) //nolint:errcheck
	}))
	t.Cleanup(srv.Close)
	release = sync.OnceFunc(func() { close(held) })
	t.Cleanup(release)
	return srv, in, release
}

func waitArrived(t *testing.T, arrived <-chan struct{}) {
	t.Helper()
	select {
	case <-arrived:
	case <-time.After(5 * time.Second):
		t.Fatal("no call reached Bot Connector")
	}
}

func TestQueue_FlushWaitsForTheCallInFlight(t *testing.T) {
	srv, arrived, release := newHoldingBotServer(t)
	q := newTestQueue(t, srv)
	q.Start()
	t.Cleanup(func() { q.Stop(time.Second) })

	q.Send(TextUpdateOp{Text: "answer"})
	waitArrived(t, arrived)
	flushed := make(chan struct{})
	go func() {
		q.Flush()
		close(flushed)
	}()

	select {
	case <-flushed:
		t.Fatal("Flush returned while the POST was still in flight")
	case <-time.After(500 * time.Millisecond):
	}
	release()
	select {
	case <-flushed:
	case <-time.After(5 * time.Second):
		t.Fatal("Flush did not return once the POST was answered")
	}
}

func TestQueue_FlushWritesBufferedText(t *testing.T) {
	srv, calls, mu := newMockBotServer(t)
	defer srv.Close()
	q := newTestQueue(t, srv)
	q.SetBufferInterval(10 * time.Second)
	q.Start()
	t.Cleanup(func() { q.Stop(time.Second) })

	q.Send(TextUpdateOp{Text: "first"})
	deadline := time.Now().Add(5 * time.Second)
	for {
		mu.Lock()
		n := len(*calls)
		mu.Unlock()
		if n == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("first text never sent")
		}
		time.Sleep(10 * time.Millisecond)
	}
	q.Send(TextUpdateOp{Text: " second"})
	for q.ContentText() != "first second" {
		if time.Now().After(deadline) {
			t.Fatal("second text never buffered")
		}
		time.Sleep(10 * time.Millisecond)
	}
	q.Flush()

	mu.Lock()
	defer mu.Unlock()
	last, _ := (*calls)[len(*calls)-1].Body["text"].(string)
	if last != "first second" {
		t.Errorf("last text sent = %q, want %q", last, "first second")
	}
}

func TestQueue_FlushDoesNotResendSentText(t *testing.T) {
	srv, calls, mu := newMockBotServer(t)
	defer srv.Close()
	q := newTestQueue(t, srv)
	q.Start()
	t.Cleanup(func() { q.Stop(time.Second) })

	q.Send(TextUpdateOp{Text: "answer"})
	q.Flush()

	mu.Lock()
	defer mu.Unlock()
	if len(*calls) != 1 {
		t.Errorf("calls = %d, want the one POST", len(*calls))
	}
}

func TestQueue_FlushGivesUpAtItsTimeout(t *testing.T) {
	srv, arrived, _ := newHoldingBotServer(t)
	q := newTestQueue(t, srv)
	q.flushTimeout = 100 * time.Millisecond
	q.Start()
	t.Cleanup(func() { q.Stop(time.Second) })

	q.Send(TextUpdateOp{Text: "answer"})
	waitArrived(t, arrived)
	flushed := make(chan struct{})
	go func() {
		q.Flush()
		close(flushed)
	}()

	select {
	case <-flushed:
	case <-time.After(5 * time.Second):
		t.Fatal("Flush still waiting past its timeout")
	}
}

func (q *MessageQueue) WaitDrain() {
	for range 600 {
		if len(q.ops) == 0 {
			time.Sleep(100 * time.Millisecond)
			if len(q.ops) == 0 {
				return
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	q.logger.Warn("queue drain timeout", "taskID", q.taskID)
}

func TestQueue_DiscardOnAFullQueueWritesNothingMore(t *testing.T) {
	srv, arrived, release := newHoldingBotServer(t)
	q := newTestQueue(t, srv)
	q.Start()

	q.Send(TextUpdateOp{Text: "answer"})
	waitArrived(t, arrived)
	for range cap(q.ops) {
		q.Send(StatusUpdateOp{StatusActivityID: "status-1", Text: "💭 working"})
	}
	q.appendText(" tail")

	discarded := make(chan struct{})
	go func() {
		q.Discard(5 * time.Second)
		close(discarded)
	}()
	select {
	case <-discarded:
		t.Fatal("Discard returned while the channel was full and the POST in flight")
	case <-time.After(200 * time.Millisecond):
	}

	release()
	select {
	case <-discarded:
	case <-time.After(5 * time.Second):
		t.Fatal("Discard did not return once the POST was answered")
	}
	select {
	case <-q.done:
	default:
		t.Error("the processor is still running after Discard")
	}
	select {
	case <-arrived:
		t.Error("a write reached Bot Connector after Discard")
	case <-time.After(300 * time.Millisecond):
	}
}

func TestQueue_StopTwice(t *testing.T) {
	srv, _, _ := newMockBotServer(t)
	defer srv.Close()
	q := newTestQueue(t, srv)
	q.Start()

	q.Stop(time.Second)
	done := make(chan struct{})
	go func() {
		q.Discard(time.Second)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the second stop did not return")
	}
}

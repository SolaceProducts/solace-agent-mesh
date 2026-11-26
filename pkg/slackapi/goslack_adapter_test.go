// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package slackapi

import (
	"context"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	goslack "github.com/slack-go/slack"
)

func TestGetConversationReplies_PaginatesAndKeepsNewest(t *testing.T) {
	var page atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch page.Add(1) {
		case 1:
			_, _ = io.WriteString(w, `{"ok":true,"messages":[{"user":"U1","text":"m1","ts":"1.1"},{"user":"U1","text":"m2","ts":"1.2"}],"has_more":true,"response_metadata":{"next_cursor":"CUR2"}}`)
		default:
			_, _ = io.WriteString(w, `{"ok":true,"messages":[{"user":"U1","text":"m3","ts":"1.3"},{"user":"U1","text":"m4","ts":"1.4"}],"has_more":false}`)
		}
	}))
	defer srv.Close()

	a := &slackAPIAdapterImpl{
		client:  goslack.New("xoxb-test", goslack.OptionAPIURL(srv.URL+"/")),
		limiter: newRateLimiterDisabled(),
	}

	msgs, err := a.GetConversationReplies(context.Background(), "C1", "1.1", 3)
	if err != nil {
		t.Fatalf("GetConversationReplies: %v", err)
	}
	if got := page.Load(); got != 2 {
		t.Errorf("expected 2 paged requests, got %d", got)
	}
	got := []string{}
	for _, m := range msgs {
		got = append(got, m.Text)
	}
	want := []string{"m2", "m3", "m4"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("kept %v, want newest-3 %v", got, want)
	}
}

func newTestAdapter(serverURL string) *slackAPIAdapterImpl {
	return &slackAPIAdapterImpl{
		client:  goslack.New("xoxb-test", goslack.OptionAPIURL(serverURL+"/")),
		limiter: newRateLimiterDisabled(),
	}
}

func TestUpload(t *testing.T) {
	tests := []struct {
		name           string
		handler        http.HandlerFunc
		ctxFn          func(t *testing.T) context.Context
		wantErr        bool
		wantErrContain string
	}{
		{
			name:    "Success",
			handler: func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) },
			ctxFn:   func(t *testing.T) context.Context { return t.Context() },
		},
		{
			name: "HTTPError",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte("internal server error"))
			},
			ctxFn:          func(t *testing.T) context.Context { return t.Context() },
			wantErr:        true,
			wantErrContain: "500",
		},
		{
			name: "Status300",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusMultipleChoices)
				_, _ = w.Write([]byte("redirect body"))
			},
			ctxFn:          func(t *testing.T) context.Context { return t.Context() },
			wantErr:        true,
			wantErrContain: "300",
		},
		{
			name:    "ContextCancelled",
			handler: func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) },
			ctxFn: func(t *testing.T) context.Context {
				ctx, cancel := context.WithCancel(t.Context())
				cancel()
				return ctx
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(tt.handler)
			defer srv.Close()

			url := srv.URL
			if tt.name == "ContextCancelled" {
				url = "http://localhost:0/unused"
			}

			adapter := newTestAdapter(srv.URL)
			err := adapter.Upload(tt.ctxFn(t), url, "upload.bin", []byte("file-content"))

			if tt.wantErr && err == nil {
				t.Fatal("expected error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
			if tt.wantErrContain != "" && !strings.Contains(err.Error(), tt.wantErrContain) {
				t.Errorf("error %q should contain %q", err.Error(), tt.wantErrContain)
			}
		})
	}
}

func TestThrottleErrorPropagation(t *testing.T) {
	t.Parallel()

	rl := newRateLimiter()
	if err := rl.throttle(t.Context(), methodChatPostMessage); err != nil {
		t.Fatal(err)
	}
	if err := rl.throttle(t.Context(), methodChatDelete); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	adapter := &slackAPIAdapterImpl{limiter: rl}

	if _, _, err := adapter.PostMessage(ctx, "C1"); err == nil {
		t.Error("PostMessage: expected throttle error, got nil")
	}
	if _, _, err := adapter.DeleteMessage(ctx, "C1", "ts1"); err == nil {
		t.Error("DeleteMessage: expected throttle error, got nil")
	}
}

type capturedUpload struct {
	contentType        string
	partBytes          []byte
	partName           string
	contentDisposition string
	parseErr           error
}

func captureUploadServer(t *testing.T) (*httptest.Server, func() capturedUpload) {
	t.Helper()
	var mu sync.Mutex
	var captured capturedUpload

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		captured.contentType = r.Header.Get("Content-Type")

		mediaType, params, parseErr := mime.ParseMediaType(captured.contentType)
		if parseErr != nil {
			captured.parseErr = fmt.Errorf("parse media type: %w", parseErr)
			w.WriteHeader(http.StatusOK)
			return
		}
		if mediaType != "multipart/form-data" {
			captured.parseErr = fmt.Errorf("unexpected media type: %q", mediaType)
			w.WriteHeader(http.StatusOK)
			return
		}
		boundary := params["boundary"]
		mr := multipart.NewReader(r.Body, boundary)
		for {
			part, err := mr.NextPart()
			if err != nil {
				break
			}
			if part.FormName() == "file" {
				captured.partName = part.FileName()
				captured.contentDisposition = part.Header.Get("Content-Disposition")
				data, readErr := io.ReadAll(part)
				if readErr != nil {
					captured.parseErr = fmt.Errorf("read file part: %w", readErr)
				}
				captured.partBytes = data
				_ = part.Close()
				break
			}
			_ = part.Close()
		}
		w.WriteHeader(http.StatusOK)
	}))

	return srv, func() capturedUpload {
		mu.Lock()
		defer mu.Unlock()
		return captured
	}
}

func TestUpload_SendsMultipartFormData(t *testing.T) {
	srv, captured := captureUploadServer(t)
	defer srv.Close()

	adapter := newTestAdapter(srv.URL)
	content := []byte("hello world")
	if err := adapter.Upload(t.Context(), srv.URL, "notes.txt", content); err != nil {
		t.Fatalf("Upload: %v", err)
	}

	got := captured()
	if got.parseErr != nil {
		t.Fatalf("server failed to parse request: %v", got.parseErr)
	}
	if !strings.HasPrefix(got.contentType, "multipart/form-data; boundary=") {
		t.Errorf("Content-Type = %q, want prefix %q", got.contentType, "multipart/form-data; boundary=")
	}
	if got.partName != "notes.txt" {
		t.Errorf("multipart filename = %q, want %q", got.partName, "notes.txt")
	}
	if string(got.partBytes) != string(content) {
		t.Errorf("multipart file part = %q, want %q", got.partBytes, content)
	}
	if !strings.Contains(got.contentDisposition, `filename="notes.txt"`) {
		t.Errorf("Content-Disposition = %q, want it to include filename=\"notes.txt\"", got.contentDisposition)
	}
}

func TestUpload_PreservesLargeTextArtifact(t *testing.T) {
	srv, captured := captureUploadServer(t)
	defer srv.Close()

	const lines = 64
	var sb strings.Builder
	for i := 0; i < lines; i++ {
		fmt.Fprintf(&sb, "%03d: the quick brown fox jumps over the lazy dog\n", i)
	}
	content := []byte(sb.String())
	if len(content) <= 1024 {
		t.Fatalf("test fixture too small (%d bytes); should exercise >1 KiB", len(content))
	}

	adapter := newTestAdapter(srv.URL)
	if err := adapter.Upload(t.Context(), srv.URL, "report.txt", content); err != nil {
		t.Fatalf("Upload: %v", err)
	}

	got := captured()
	if got.parseErr != nil {
		t.Fatalf("server failed to parse request: %v", got.parseErr)
	}
	if len(got.partBytes) != len(content) {
		t.Fatalf("multipart file part has %d bytes, want %d", len(got.partBytes), len(content))
	}
	if string(got.partBytes) != string(content) {
		t.Errorf("multipart file part bytes do not match input content")
	}
}

func TestUpload_RejectsEmptyFilename(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	adapter := newTestAdapter(srv.URL)
	err := adapter.Upload(t.Context(), srv.URL, "", []byte("data"))
	if err == nil {
		t.Fatal("expected error for empty filename, got nil")
	}
	if !strings.Contains(err.Error(), "filename is required") {
		t.Errorf("error should mention filename requirement, got: %v", err)
	}
	if hits != 0 {
		t.Errorf("expected no HTTP request when filename is empty, got %d hits", hits)
	}
}

func TestOpenViewAndPostEphemeral_HitTheRightEndpoints(t *testing.T) {
	var mu sync.Mutex
	seen := map[string]string{}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		seen[r.URL.Path] = string(body)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/views.open":
			_, _ = io.WriteString(w, `{"ok":true,"view":{"id":"V1","callback_id":"cb-1"}}`)
		default:
			_, _ = io.WriteString(w, `{"ok":true,"message_ts":"1700000000.000100"}`)
		}
	}))
	defer srv.Close()

	a := &slackAPIAdapterImpl{
		client:  goslack.New("xoxb-test", goslack.OptionAPIURL(srv.URL+"/")),
		limiter: newRateLimiterDisabled(),
	}

	resp, err := a.OpenView(context.Background(), "trigger-1", goslack.ModalViewRequest{
		Type:       goslack.VTModal,
		CallbackID: "cb-1",
		Title:      goslack.NewTextBlockObject(goslack.PlainTextType, "Feedback", false, false),
	})
	if err != nil {
		t.Fatalf("OpenView: %v", err)
	}
	if resp == nil || resp.ID != "V1" {
		t.Errorf("OpenView response = %+v, want view id V1", resp)
	}

	ts, err := a.PostEphemeral(context.Background(), "C1", "U1", goslack.MsgOptionText("private", false))
	if err != nil {
		t.Fatalf("PostEphemeral: %v", err)
	}
	if ts != "1700000000.000100" {
		t.Errorf("PostEphemeral ts = %q", ts)
	}

	mu.Lock()
	defer mu.Unlock()
	if _, ok := seen["/views.open"]; !ok {
		t.Errorf("views.open was never called; saw %v", keysOf(seen))
	}
	body, ok := seen["/chat.postEphemeral"]
	if !ok {
		t.Fatalf("chat.postEphemeral was never called; saw %v", keysOf(seen))
	}
	if !strings.Contains(body, "user=U1") {
		t.Errorf("ephemeral not addressed to the user: %s", body)
	}
}

func keysOf(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestConversationReadsReturnMessageMetadata(t *testing.T) {
	const body = `{"ok":true,"messages":[{"user":"UBOT","text":"refused","ts":"1.1","metadata":{"event_type":"sam_gateway_notice","event_payload":{"kind":"budget"}}}],"has_more":false}`
	var asked sync.Map
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		asked.Store(r.URL.Path, r.FormValue("include_all_metadata"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, body)
	}))
	defer srv.Close()
	a := newTestAdapter(srv.URL)

	replies, err := a.GetConversationReplies(t.Context(), "C1", "1.1", 10)
	if err != nil {
		t.Fatalf("GetConversationReplies: %v", err)
	}
	history, err := a.GetConversationHistory(t.Context(), "C1", 10)
	if err != nil {
		t.Fatalf("GetConversationHistory: %v", err)
	}

	for _, path := range []string{"/conversations.replies", "/conversations.history"} {
		if got, _ := asked.Load(path); got != "1" {
			t.Errorf("%s include_all_metadata = %v, want 1", path, got)
		}
	}
	for name, msgs := range map[string][]goslack.Message{"replies": replies, "history": history} {
		if len(msgs) != 1 || msgs[0].Metadata.EventType != "sam_gateway_notice" || msgs[0].Metadata.EventPayload["kind"] != "budget" {
			t.Errorf("%s did not carry the message metadata: %+v", name, msgs)
		}
	}
}

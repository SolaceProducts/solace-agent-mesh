// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package whatsapp

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func testServer(t *testing.T) *Server {
	t.Helper()
	cfg := &WhatsAppConfig{AppSecret: "test-secret", VerifyToken: "verify-me"}
	return NewServer(context.Background(), cfg, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func sign(secret, body string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(body))
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func TestHandleVerify_Success(t *testing.T) {
	s := testServer(t)
	r := httptest.NewRequest(http.MethodGet,
		"/webhook?hub.mode=subscribe&hub.verify_token=verify-me&hub.challenge=abc123", nil)
	w := httptest.NewRecorder()

	s.handleVerify(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if got := w.Body.String(); got != "abc123" {
		t.Fatalf("challenge echo = %q, want %q", got, "abc123")
	}
}

func TestHandleVerify_WrongToken(t *testing.T) {
	s := testServer(t)
	r := httptest.NewRequest(http.MethodGet,
		"/webhook?hub.mode=subscribe&hub.verify_token=WRONG&hub.challenge=abc123", nil)
	w := httptest.NewRecorder()

	s.handleVerify(w, r)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", w.Code)
	}
	if w.Body.Len() != 0 {
		t.Fatalf("body should be empty on rejection, got %q", w.Body.String())
	}
}

func TestValidSignature(t *testing.T) {
	s := testServer(t)
	body := `{"object":"whatsapp_business_account"}`

	if !s.validSignature(sign("test-secret", body), []byte(body)) {
		t.Error("valid signature rejected")
	}
	if s.validSignature(sign("wrong-secret", body), []byte(body)) {
		t.Error("signature from wrong secret accepted")
	}
	if s.validSignature("", []byte(body)) {
		t.Error("empty signature accepted")
	}
	if s.validSignature("sha256=not-hex", []byte(body)) {
		t.Error("malformed hex signature accepted")
	}
	if s.validSignature("md5=abc", []byte(body)) {
		t.Error("non-sha256 prefix accepted")
	}
}

func TestHandleEvent_DispatchesBeforeAck(t *testing.T) {
	var mu sync.Mutex
	var got []string
	cfg := &WhatsAppConfig{AppSecret: "test-secret", VerifyToken: "v"}
	s := NewServer(context.Background(), cfg,
		func(_ context.Context, _ *webhookChangeValue, msg *inboundMessage) error {
			mu.Lock()
			got = append(got, msg.ID)
			mu.Unlock()
			return nil
		},
		nil, slog.New(slog.NewTextHandler(io.Discard, nil)))

	body := `{"object":"whatsapp_business_account","entry":[{"id":"waba","changes":[{"field":"messages","value":{"messages":[{"from":"15551234567","id":"wamid.ABC","type":"text","text":{"body":"hi"}}]}}]}]}`
	r := httptest.NewRequest(http.MethodPost, "/webhook", strings.NewReader(body))
	r.Header.Set("X-Hub-Signature-256", sign("test-secret", body))
	w := httptest.NewRecorder()

	s.handleEvent(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 || got[0] != "wamid.ABC" {
		t.Fatalf("handler dispatch = %v, want [wamid.ABC]", got)
	}
}

func TestHandleEvent_SpoolFailureRefusesAck(t *testing.T) {
	cfg := &WhatsAppConfig{AppSecret: "test-secret", VerifyToken: "v"}
	s := NewServer(context.Background(), cfg,
		func(_ context.Context, _ *webhookChangeValue, _ *inboundMessage) error {
			return errors.New("broker unavailable")
		},
		nil, slog.New(slog.NewTextHandler(io.Discard, nil)))

	r := httptest.NewRequest(http.MethodPost, "/webhook", strings.NewReader(oneMessageBody))
	r.Header.Set("X-Hub-Signature-256", sign("test-secret", oneMessageBody))
	w := httptest.NewRecorder()

	s.handleEvent(w, r)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 — a 200 here tells Meta to forget a message we never stored", w.Code)
	}
}

const oneMessageBody = `{"object":"whatsapp_business_account","entry":[{"id":"waba","changes":[{"field":"messages","value":{"messages":[{"from":"15551234567","id":"wamid.ABC","type":"text","text":{"body":"hi"}}]}}]}]}`

func TestHandleEvent_AfterClose_Refuses(t *testing.T) {
	var dispatched atomic.Int64
	cfg := &WhatsAppConfig{AppSecret: "test-secret", VerifyToken: "v"}
	s := NewServer(context.Background(), cfg,
		func(_ context.Context, _ *webhookChangeValue, _ *inboundMessage) error { dispatched.Add(1); return nil },
		nil, slog.New(slog.NewTextHandler(io.Discard, nil)))

	s.Close()

	r := httptest.NewRequest(http.MethodPost, "/webhook", strings.NewReader(oneMessageBody))
	r.Header.Set("X-Hub-Signature-256", sign("test-secret", oneMessageBody))
	w := httptest.NewRecorder()

	s.Handler().ServeHTTP(w, r)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 (the retry-please status; a 200 makes Meta drop the message)", w.Code)
	}
	if got := dispatched.Load(); got != 0 {
		t.Fatalf("dispatched %d messages after Close, want 0", got)
	}
}

func TestServer_ConcurrentPostsAcrossClose(t *testing.T) {
	var dispatched atomic.Int64
	cfg := &WhatsAppConfig{AppSecret: "test-secret", VerifyToken: "v"}
	s := NewServer(context.Background(), cfg,
		func(_ context.Context, _ *webhookChangeValue, _ *inboundMessage) error { dispatched.Add(1); return nil },
		nil, slog.New(slog.NewTextHandler(io.Discard, nil)))

	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	signature := sign("test-secret", oneMessageBody)
	const posters = 64
	var acked, refused atomic.Int64
	start := make(chan struct{})

	var posting sync.WaitGroup
	for range posters {
		posting.Go(func() {
			<-start
			req, err := http.NewRequestWithContext(t.Context(), http.MethodPost,
				srv.URL+"/webhook", strings.NewReader(oneMessageBody))
			if err != nil {
				t.Errorf("build request: %v", err)
				return
			}
			req.Header.Set("X-Hub-Signature-256", signature)
			resp, err := srv.Client().Do(req)
			if err != nil {
				t.Errorf("POST /webhook: %v", err)
				return
			}
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
			switch resp.StatusCode {
			case http.StatusOK:
				acked.Add(1)
			case http.StatusServiceUnavailable:
				refused.Add(1)
			default:
				t.Errorf("status = %d, want 200 or 503", resp.StatusCode)
			}
		})
	}

	close(start)
	s.Close()
	s.Wait()
	posting.Wait()

	if total := acked.Load() + refused.Load(); total != posters {
		t.Fatalf("accounted-for responses = %d, want %d", total, posters)
	}
	if got := dispatched.Load(); got != acked.Load() {
		t.Fatalf("dispatched = %d, acked = %d — an acked webhook must always be dispatched", got, acked.Load())
	}
}

func TestHandleEvent_OverCapBody(t *testing.T) {
	called := false
	cfg := &WhatsAppConfig{AppSecret: "test-secret", VerifyToken: "v"}
	s := NewServer(context.Background(), cfg,
		func(_ context.Context, _ *webhookChangeValue, _ *inboundMessage) error { called = true; return nil },
		nil, slog.New(slog.NewTextHandler(io.Discard, nil)))

	huge := strings.Repeat("a", maxWebhookBody+1024)
	r := httptest.NewRequest(http.MethodPost, "/webhook", strings.NewReader(huge))
	w := httptest.NewRecorder()

	s.handleEvent(w, r)

	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", w.Code)
	}
	if called {
		t.Error("handler invoked for an over-cap body")
	}
}

func TestHandleEvent_InvalidJSONValidSignature(t *testing.T) {
	called := false
	cfg := &WhatsAppConfig{AppSecret: "test-secret", VerifyToken: "v"}
	s := NewServer(context.Background(), cfg,
		func(_ context.Context, _ *webhookChangeValue, _ *inboundMessage) error { called = true; return nil },
		nil, slog.New(slog.NewTextHandler(io.Discard, nil)))

	body := `{"object": not-json`
	r := httptest.NewRequest(http.MethodPost, "/webhook", strings.NewReader(body))
	r.Header.Set("X-Hub-Signature-256", sign("test-secret", body))
	w := httptest.NewRecorder()

	s.handleEvent(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (don't make Meta retry unparseable payloads)", w.Code)
	}
	if called {
		t.Error("handler invoked for unparseable JSON")
	}
}

func TestHandleEvent_DispatchesStatus(t *testing.T) {
	var got []string
	cfg := &WhatsAppConfig{AppSecret: "test-secret", VerifyToken: "v"}
	s := NewServer(context.Background(), cfg, nil,
		func(_ *webhookChangeValue, st *messageStatus) { got = append(got, st.Status) },
		slog.New(slog.NewTextHandler(io.Discard, nil)))

	body := `{"object":"whatsapp_business_account","entry":[{"id":"waba","changes":[{"field":"messages","value":{"statuses":[{"id":"wamid.S","status":"failed","recipient_id":"15551234567"}]}}]}]}`
	r := httptest.NewRequest(http.MethodPost, "/webhook", strings.NewReader(body))
	r.Header.Set("X-Hub-Signature-256", sign("test-secret", body))
	w := httptest.NewRecorder()

	s.handleEvent(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if len(got) != 1 || got[0] != "failed" {
		t.Fatalf("status dispatch = %v, want [failed]", got)
	}
}

func TestHandleEvent_PreservesBatchOrder(t *testing.T) {
	var got []string
	cfg := &WhatsAppConfig{AppSecret: "test-secret", VerifyToken: "v"}
	s := NewServer(context.Background(), cfg,
		func(_ context.Context, _ *webhookChangeValue, msg *inboundMessage) error {
			got = append(got, msg.ID)
			return nil
		},
		nil, slog.New(slog.NewTextHandler(io.Discard, nil)))

	body := `{"object":"whatsapp_business_account","entry":[{"id":"waba","changes":[{"field":"messages","value":{"messages":[` +
		`{"from":"1555","id":"wamid.1","type":"text","text":{"body":"run the report"}},` +
		`{"from":"1555","id":"wamid.2","type":"text","text":{"body":"for last week"}},` +
		`{"from":"1555","id":"wamid.3","type":"text","text":{"body":"in pdf"}}]}}]}]}`
	r := httptest.NewRequest(http.MethodPost, "/webhook", strings.NewReader(body))
	r.Header.Set("X-Hub-Signature-256", sign("test-secret", body))
	w := httptest.NewRecorder()

	s.handleEvent(w, r)

	want := []string{"wamid.1", "wamid.2", "wamid.3"}
	if len(got) != len(want) {
		t.Fatalf("dispatched %d messages, want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("message order = %v, want %v", got, want)
		}
	}
}

func TestMaxWebhookBody_CoversMetaEnvelope(t *testing.T) {
	if maxWebhookBody < 3*1024*1024 {
		t.Fatalf("maxWebhookBody = %d, must cover Meta's 3 MB payload max", maxWebhookBody)
	}
}

func TestHandleHealth(t *testing.T) {
	s := testServer(t)
	r := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()

	s.Handler().ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if !strings.Contains(w.Body.String(), `"status":"ok"`) {
		t.Errorf("health body = %q, want it to contain status:ok", w.Body.String())
	}
}

func TestHandleEvent_RejectsBadSignature(t *testing.T) {
	called := false
	cfg := &WhatsAppConfig{AppSecret: "test-secret", VerifyToken: "v"}
	s := NewServer(context.Background(), cfg,
		func(_ context.Context, _ *webhookChangeValue, _ *inboundMessage) error { called = true; return nil },
		nil, slog.New(slog.NewTextHandler(io.Discard, nil)))

	body := `{"object":"x"}`
	r := httptest.NewRequest(http.MethodPost, "/webhook", strings.NewReader(body))
	r.Header.Set("X-Hub-Signature-256", sign("WRONG", body))
	w := httptest.NewRecorder()

	s.handleEvent(w, r)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", w.Code)
	}
	if called {
		t.Error("message handler invoked despite bad signature")
	}
}
